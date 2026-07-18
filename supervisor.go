package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"rsyncgui/internal/jobs"
)

// superviseFlag is how the binary is asked to be a supervisor instead of a
// window. It is an internal re-invocation, not a documented user option.
const superviseFlag = "--supervise"

// runSupervisorIfRequested returns true when this process was started to
// supervise a job, in which case it has already done the work and main must
// return without ever opening a window.
//
// Why a supervisor at all, rather than detaching rsync directly: if rsync were
// the detached process and the window died, nobody would be left to collect
// its exit status — it would be reaped by init and lost. On reopening, the app
// would know the job was gone but not whether it had worked. The supervisor is
// the piece that waits for rsync and records how it ended.
func runSupervisorIfRequested() bool {
	if len(os.Args) < 4 || os.Args[1] != superviseFlag {
		return false
	}
	if err := supervise(os.Args[2], os.Args[3]); err != nil {
		fmt.Fprintln(os.Stderr, "supervisor:", err)
		os.Exit(1)
	}
	return true
}

// supervise runs one job to completion and records the outcome.
func supervise(dir, jobID string) error {
	paths, err := jobs.PathsFor(dir, jobID)
	if err != nil {
		return err
	}

	st, err := jobs.ReadState(dir, jobID)
	if err != nil {
		return err
	}

	// The global one-at-a-time lock is held by the supervisor, not by the
	// window: a lock held by the window would be released the moment it is
	// closed, and a second job could start on top of the first.
	//
	// The window checks this before launching us, but that check cannot be
	// authoritative — two windows could pass it at the same instant. This is
	// where the race is actually settled, and losing it is a normal outcome to
	// report, not a crash.
	running, err := jobs.TryLock(jobs.NewStore(dir).RunningLockPath())
	if err != nil {
		st.Status = jobs.StatusFailed
		st.FinishedAt = time.Now()
		st.Summary = "Un'altra esecuzione era già in corso: questa non è partita."
		_ = jobs.WriteState(dir, st)
		return nil
	}
	defer running.Unlock()

	// Held for the whole run, this is what tells everyone else the job is
	// alive. The kernel releases it when this process ends, however it ends,
	// which is the property a pid cannot offer.
	lock, err := jobs.TryLock(paths.Lock)
	if err != nil {
		return fmt.Errorf("impossibile prendere il lock del job: %w", err)
	}
	defer lock.Unlock()

	logw, err := jobs.NewLogWriter(paths.Log)
	if err != nil {
		return err
	}

	st.PID = os.Getpid()
	st.Status = jobs.StatusRunning
	_ = jobs.WriteState(dir, st)

	// A stop request from the window arrives as a stop FILE, polled here, with
	// a Unix signal as a mere accelerator. The file is the mechanism that
	// works everywhere: on Windows the supervisor is a DETACHED_PROCESS with
	// no console, and console Ctrl events — the closest thing Windows has to
	// SIGINT — cannot reach it at all. A file also cannot be delivered to the
	// wrong process, which a recycled pid could.
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, os.Interrupt, syscall.SIGTERM)
	_ = os.Remove(paths.Stop) // stale request from a previous run must not kill this one
	defer os.Remove(paths.Stop)
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		tick := time.NewTicker(300 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-watchDone:
				return
			case <-tick.C:
				if _, err := os.Stat(paths.Stop); err == nil {
					select {
					case stopped <- os.Interrupt:
					default: // già in consegna: non serve insistere
					}
					return
				}
			}
		}
	}()

	res := runProfiles(dir, &st, logw, stopped)

	st.FinishedAt = time.Now()
	st.LogTruncated = logw.Truncated()
	st.Status = res.status
	st.ExitCode = res.exitCode
	st.Issues = res.issues
	st.Summary = res.summary
	st.CurrentProfile, st.CurrentDest = "", ""
	if res.status == jobs.StatusSuccess {
		st.Percent = 100
	}
	if err := jobs.WriteState(dir, st); err != nil {
		return err
	}

	// Closed before retention runs: on Windows an open file cannot be deleted.
	logw.Close()
	store := jobs.NewStore(dir)
	_ = store.FinishLog(st)
	_ = store.Cleanup(time.Now())
	return nil
}

type superviseResult struct {
	status   string
	exitCode int
	issues   []string
	summary  string
}

// runProfiles executes the job's profiles one after another, and each
// profile's destinations one after another — never in parallel, as it has
// always been.
func runProfiles(dir string, st *jobs.State, logw *jobs.LogWriter, stopped <-chan os.Signal) superviseResult {
	var (
		issues    []string
		completed int
		lastExit  int
		aborted   bool
	)
	start := time.Now()

	// Progress reaches the window through the state file, rewritten at most a
	// few times a second: rsync reports on every file, and writing the state
	// that often would hammer the disk to say almost nothing new.
	//
	// The mutex is not decoration. onProgress runs on the goroutine that
	// exec.Cmd spawns to copy rsync's output (any non-*os.File writer gets
	// one), while this loop writes CurrentDest and resets the counters between
	// profiles — same struct, two goroutines. Without the lock that is a data
	// race on everything WriteState reads.
	var (
		stMu      sync.Mutex
		lastWrite time.Time
		lastPct   = -2
	)
	mutate := func(f func()) {
		stMu.Lock()
		f()
		_ = jobs.WriteState(dir, *st)
		stMu.Unlock()
	}
	onProgress := func(ps ProgressState) {
		stMu.Lock()
		if ps.Percent == lastPct && (ps.Percent >= 0 || time.Since(lastWrite) < time.Second) {
			stMu.Unlock()
			return // stessa percentuale: niente di nuovo da dire
		}
		lastPct, lastWrite = ps.Percent, time.Now()
		st.Percent, st.FilesDone, st.FilesTotal = ps.Percent, ps.FilesDone, ps.FilesTotal
		_ = jobs.WriteState(dir, *st)
		stMu.Unlock()
	}

	for _, prof := range st.Profiles {
		if aborted {
			break
		}
		mutate(func() {
			st.CurrentProfile = prof.Name
			// Each profile is a fresh transfer: carrying the previous one's
			// percentage over would show a bar that starts at 100%.
			st.Percent, st.FilesDone, st.FilesTotal = -1, 0, 0
			lastPct = -2
		})
		fmt.Fprintf(logw, "\n═══ %s ═══\n", prof.Name)

		var opts SyncOptions
		if len(prof.Options) > 0 {
			if err := json.Unmarshal(prof.Options, &opts); err != nil {
				msg := fmt.Sprintf("%s: opzioni illeggibili, profilo saltato", prof.Name)
				issues = append(issues, msg)
				fmt.Fprintf(logw, "✗ %s\n", msg)
				continue
			}
		}

		srcs := make([]string, 0, len(prof.Sources))
		for _, s := range prof.Sources {
			if sourceAvailable(s) {
				srcs = append(srcs, s)
				continue
			}
			msg := fmt.Sprintf("%s: sorgente non disponibile, saltata: %s", prof.Name, s)
			issues = append(issues, msg)
			fmt.Fprintf(logw, "⚠ %s\n", msg)
		}
		if len(srcs) == 0 {
			msg := fmt.Sprintf("%s: nessuna sorgente disponibile, nulla da copiare", prof.Name)
			issues = append(issues, msg)
			fmt.Fprintf(logw, "⚠ %s\n", msg)
			continue
		}

		for _, dest := range prof.Destinations {
			if aborted {
				break
			}
			if !destAvailable(dest) {
				msg := fmt.Sprintf("%s: destinazione non disponibile, saltata: %s", prof.Name, dest)
				issues = append(issues, msg)
				fmt.Fprintf(logw, "⚠ %s\n", msg)
				continue
			}

			mutate(func() { st.CurrentDest = dest })
			if len(prof.Destinations) > 1 {
				fmt.Fprintf(logw, "→ verso %s\n", dest)
			}

			exit, err := runOneRsync(opts, srcs, dest, logw, stopped, &aborted, onProgress)
			lastExit = exit
			switch {
			case aborted:
				fmt.Fprintf(logw, "⛔ Interrotto su richiesta.\n")
			case err != nil:
				msg := fmt.Sprintf("%s → %s: %v", prof.Name, dest, err)
				issues = append(issues, msg)
				fmt.Fprintf(logw, "✗ %s\n", msg)
			case exit == 0:
				completed++
				fmt.Fprintf(logw, "Completato.\n")
			case isPartialTransferExitCode(exit):
				completed++
				msg := fmt.Sprintf("%s → %s: completata con uno o più file non trasferibili", prof.Name, dest)
				issues = append(issues, msg)
				fmt.Fprintf(logw, "⚠ %s\n", msg)
			default:
				msg := fmt.Sprintf("%s → %s: rsync terminato con errore (exit %d)", prof.Name, dest, exit)
				issues = append(issues, msg)
				fmt.Fprintf(logw, "✗ %s\n", msg)
			}
		}
	}

	elapsed := time.Since(start).Round(time.Second)
	switch {
	case aborted:
		return superviseResult{status: jobs.StatusAborted, exitCode: lastExit, issues: issues,
			summary: fmt.Sprintf("Interrotta dopo %s.", elapsed)}
	case len(issues) == 0:
		return superviseResult{status: jobs.StatusSuccess, issues: issues,
			summary: fmt.Sprintf("Completata in %s (%d destinazioni).", elapsed, completed)}
	case completed > 0:
		return superviseResult{status: jobs.StatusPartial, exitCode: lastExit, issues: issues,
			summary: fmt.Sprintf("Completata in %s con %d avvisi.", elapsed, len(issues))}
	default:
		return superviseResult{status: jobs.StatusFailed, exitCode: lastExit, issues: issues,
			summary: fmt.Sprintf("Fallita dopo %s.", elapsed)}
	}
}

// runOneRsync executes a single rsync and streams its output into the job log.
//
// The pipe between rsync and this process is harmless: this process is the one
// that outlives the window, so nothing closes the read end early. The pipe
// that used to kill transfers was the one between rsync and the GUI.
func runOneRsync(opts SyncOptions, srcs []string, dest string, logw *jobs.LogWriter, stopped <-chan os.Signal, aborted *bool, onProgress func(ProgressState)) (int, error) {
	bin, err := exec.LookPath("rsync")
	if err != nil {
		return -1, errors.New("rsync non trovato nel PATH")
	}

	// The progress writer sits in front of the log: it reads rsync's --progress
	// output for the percentage and keeps those lines out of the log, which
	// would otherwise double in size for status that is obsolete a second later.
	pw := newProgressWriter(logw, onProgress)
	defer pw.Flush()

	cmd := exec.Command(bin, rsyncArgs(opts, srcs, dest)...)
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		return -1, err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-stopped:
		*aborted = true
		// SIGINT rather than a hard kill: rsync traps it and removes the
		// temporary file it was writing.
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		return -1, nil
	case waitErr := <-done:
		if waitErr == nil {
			return 0, nil
		}
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return -1, waitErr
	}
}
