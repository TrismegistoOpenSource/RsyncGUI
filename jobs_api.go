package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"rsyncgui/internal/jobs"
)

// jobsDir is where detached jobs keep their state and logs, inside the config
// directory the app already owns — and which RSYNCGUI_CONFIG_DIR already
// redirects, so tests never touch the real one.
//
// profiles.json is never read or written by any of this: jobs live in their
// own subdirectory alongside it.
func jobsDir() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "jobs"), nil
}

func jobStore() (*jobs.Store, error) {
	dir, err := jobsDir()
	if err != nil {
		return nil, err
	}
	return jobs.NewStore(dir), nil
}

// JobView is one row of the Attività list. It is the state plus the couple of
// facts only the window can work out.
type JobView struct {
	JobID      string   `json:"jobId"`
	Label      string   `json:"label"`
	Status     string   `json:"status"`
	Alive      bool     `json:"alive"`
	StartedAt  string   `json:"startedAt"`
	FinishedAt string   `json:"finishedAt"`
	Summary    string   `json:"summary"`
	Issues     []string `json:"issues"`
	// ProfileIDs is what lets the profile list show a status dot again. With
	// jobs detached, the runner no longer emits per-profile events to this
	// window — it may not even have been the window that started them — so the
	// cards derive their state from the jobs on disk instead.
	ProfileIDs     []string `json:"profileIds"`
	CurrentProfile string   `json:"currentProfile"`
	CurrentDest    string   `json:"currentDest"`
	Percent        int      `json:"percent"`
	FilesDone      int      `json:"filesDone"`
	FilesTotal     int      `json:"filesTotal"`
	HasLog         bool     `json:"hasLog"`
	LogTruncated   bool     `json:"logTruncated"`
}

// ListJobs returns the detached jobs, newest first. Jobs whose supervisor died
// without recording an outcome come back as "orphaned" rather than being left
// running for ever or quietly called failed.
func (a *App) ListJobs() ([]JobView, error) {
	store, err := jobStore()
	if err != nil {
		return nil, err
	}
	states, err := store.List()
	if err != nil {
		return nil, err
	}

	out := make([]JobView, 0, len(states))
	for _, st := range states {
		p, _ := store.Paths(st.JobID)
		hasLog := false
		if _, err := os.Stat(p.Log); err == nil {
			hasLog = true
		}
		ids := make([]string, 0, len(st.Profiles))
		for _, pr := range st.Profiles {
			if pr.ID != "" {
				ids = append(ids, pr.ID)
			}
		}
		v := JobView{
			JobID: st.JobID, Label: st.Label, Status: st.Status, ProfileIDs: ids,
			// Only running states are worth a lock check: it opens a file and
			// asks the kernel, and doing it for every finished job in the
			// history would be a syscall storm once a second.
			Alive:   st.Running() && store.IsAlive(st.JobID),
			Summary: st.Summary, Issues: st.Issues,
			CurrentProfile: st.CurrentProfile, CurrentDest: st.CurrentDest,
			Percent: st.Percent, FilesDone: st.FilesDone, FilesTotal: st.FilesTotal,
			HasLog: hasLog, LogTruncated: st.LogTruncated,
			StartedAt: st.StartedAt.Format(time.RFC3339),
		}
		if !st.FinishedAt.IsZero() {
			v.FinishedAt = st.FinishedAt.Format(time.RFC3339)
		}
		out = append(out, v)
	}
	return out, nil
}

// AnyJobRunning reports whether a detached job holds the global lock. The
// window uses it to refuse a second start with a helpful message; the
// authoritative check is in the supervisor, which is where the race is settled.
func (a *App) AnyJobRunning() bool {
	store, err := jobStore()
	if err != nil {
		return false
	}
	return jobs.IsHeld(store.RunningLockPath())
}

// startDetached writes the job state and launches a supervisor that will
// outlive this window.
func (a *App) startDetached(label string, list []SyncProfile) (string, error) {
	if len(list) == 0 {
		return "", errors.New("nessuna destinazione da eseguire")
	}
	store, err := jobStore()
	if err != nil {
		return "", err
	}
	if jobs.IsHeld(store.RunningLockPath()) {
		return "", errors.New("un'esecuzione è già in corso")
	}

	st := jobs.State{
		JobID:     jobs.NewJobID(),
		Label:     label,
		Status:    jobs.StatusRunning,
		StartedAt: time.Now(),
		Percent:   -1,
	}
	for _, p := range list {
		raw, err := json.Marshal(p.Options)
		if err != nil {
			return "", err
		}
		st.Profiles = append(st.Profiles, jobs.ProfileRun{
			ID: p.ID, Name: p.Name, Sources: p.Sources,
			Destinations: p.Destinations, Options: raw,
		})
	}
	if err := jobs.WriteState(store.Dir, st); err != nil {
		return "", err
	}

	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	cmd := exec.Command(self, superviseFlag, store.Dir, st.JobID)
	// No pipes: nothing of ours may become the reason the job dies. This is
	// the whole point — see detach().
	cmd.Stdout, cmd.Stderr, cmd.Stdin = nil, nil, nil
	detach(cmd)
	// Deliberately not exec.CommandContext: its cancellation would kill the
	// child when this window's context ends, which is exactly what we are
	// trying to avoid.
	if err := cmd.Start(); err != nil {
		return "", err
	}
	// Not waited on either: the supervisor is on its own from here. Releasing
	// our handle lets the OS reparent it.
	_ = cmd.Process.Release()

	a.emitEvent("jobs:changed")
	return st.JobID, nil
}

// StopJob asks a running job to stop, across process boundaries: the in-memory
// context of 2.2 cannot reach a supervisor that belongs to no window.
func (a *App) StopJob(jobID string) error {
	store, err := jobStore()
	if err != nil {
		return err
	}
	st, err := jobs.ReadState(store.Dir, jobID)
	if err != nil {
		return err
	}
	if !store.IsAlive(jobID) {
		return errors.New("questa esecuzione non è più in corso")
	}
	if st.PID <= 0 {
		return errors.New("esecuzione senza processo registrato")
	}
	if err := signalStop(st.PID); err != nil {
		return fmt.Errorf("impossibile interrompere l'esecuzione: %w", err)
	}
	return nil
}

// JobLogChunk is a slice of a job's log plus where reading got to.
type JobLogChunk struct {
	Text   string `json:"text"`
	Offset int64  `json:"offset"`
	// Missing means the log is gone, which is the normal end for a job that
	// went fine: its log is deleted and only the summary remains.
	Missing bool `json:"missing"`
	// Skipped means older output was passed over to catch up with the end.
	Skipped bool `json:"skipped"`
}

// How much of a log crosses to the window at a time.
//
// These are small on purpose. Results of a bound method are handed to the
// webview on the platform's UI thread, so a large payload does not merely cost
// time — it stalls the window itself, which stops responding to being dragged
// or clicked. The symptom looks like a slow interface but the interface is
// idle; it is the bridge that is busy.
//
// Following a live log also has no need for volume: nobody reads half a
// megabyte a second. What matters is being near the end, which is what these
// limits guarantee.
const (
	logChunkBytes = 64 << 10  // per read
	logTailWindow = 128 << 10 // history shown when first attaching
	maxCatchUp    = 256 << 10 // further behind than this: jump to the end
)

// ReadJobLog returns a slice of a job's log starting at from, and the offset
// reached, so the window can ask only for what is new next time.
//
// It deliberately does not replay a log from the beginning. A job left running
// unattended can have written megabytes, and streaming all of it to catch up
// would saturate the bridge for as long as it took — the window would be
// unusable while doing nothing useful, since only the end is being watched.
// When there is more than maxCatchUp to cover, the reader jumps to the last
// logTailWindow and says so, and the window shows that output was skipped
// rather than pretending the log starts there.
func (a *App) ReadJobLog(jobID string, from int64) (JobLogChunk, error) {
	store, err := jobStore()
	if err != nil {
		return JobLogChunk{}, err
	}
	p, err := store.Paths(jobID) // derived from the id, never a stored path
	if err != nil {
		return JobLogChunk{}, err
	}

	f, err := os.Open(p.Log)
	if err != nil {
		if os.IsNotExist(err) {
			return JobLogChunk{Missing: true}, nil
		}
		return JobLogChunk{}, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return JobLogChunk{}, err
	}
	size := fi.Size()

	start, skipped := from, false
	switch {
	case from <= 0:
		// First attach: show recent history, not the whole file.
		if size > logTailWindow {
			start, skipped = size-logTailWindow, true
		} else {
			start = 0
		}
	case from > size:
		// The log was compacted and is now shorter: an old offset would point
		// into the middle of a line, or past the end.
		start, skipped = maxInt64(0, size-logTailWindow), true
	case size-from > maxCatchUp:
		// Falling behind a fast writer: skip to the end instead of chasing it.
		start, skipped = size-logTailWindow, true
	}

	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return JobLogChunk{}, err
	}

	buf := make([]byte, logChunkBytes)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return JobLogChunk{}, err
	}
	text := buf[:n]

	// After a jump the read almost certainly lands mid-line; start at the next
	// line break so the log never resumes halfway through a filename.
	if skipped && start > 0 {
		if i := indexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
			start += int64(i + 1)
		}
	}

	return JobLogChunk{Text: string(text), Offset: start + int64(len(text)), Skipped: skipped}, nil
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// CleanupJobs applies the retention policy. Called at startup; the supervisor
// also calls it when a job ends.
func (a *App) cleanupJobs() {
	if store, err := jobStore(); err == nil {
		_ = store.Cleanup(time.Now())
	}
}
