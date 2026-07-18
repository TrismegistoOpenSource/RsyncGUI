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
	JobID          string   `json:"jobId"`
	Label          string   `json:"label"`
	Status         string   `json:"status"`
	Alive          bool     `json:"alive"`
	StartedAt      string   `json:"startedAt"`
	FinishedAt     string   `json:"finishedAt"`
	Summary        string   `json:"summary"`
	Issues         []string `json:"issues"`
	CurrentProfile string   `json:"currentProfile"`
	CurrentDest    string   `json:"currentDest"`
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
		v := JobView{
			JobID: st.JobID, Label: st.Label, Status: st.Status,
			Alive:   st.Running() && store.IsAlive(st.JobID),
			Summary: st.Summary, Issues: st.Issues,
			CurrentProfile: st.CurrentProfile, CurrentDest: st.CurrentDest,
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

// ReadJobLog returns the tail of a job's log, and the offset reached, so the
// window can ask for just what is new next time instead of re-reading it all.
//
// Reading in chunks matters for the same reason the log is batched: a job that
// has been running unattended can have written megabytes, and handing them to
// the webview line by line is what froze the window in 2.2.
type JobLogChunk struct {
	Text    string `json:"text"`
	Offset  int64  `json:"offset"`
	Missing bool   `json:"missing"`
}

const maxLogChunkBytes = 512 << 10

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
			// Normal: the log of a job that went fine is deleted on purpose.
			return JobLogChunk{Missing: true, Offset: 0}, nil
		}
		return JobLogChunk{}, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return JobLogChunk{}, err
	}
	// A compacted log is shorter than it was: start over rather than reading
	// from an offset that now points into the middle of a line.
	if from > fi.Size() {
		from = 0
	}
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return JobLogChunk{}, err
	}

	buf := make([]byte, maxLogChunkBytes)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return JobLogChunk{}, err
	}
	return JobLogChunk{Text: string(buf[:n]), Offset: from + int64(n)}, nil
}

// CleanupJobs applies the retention policy. Called at startup; the supervisor
// also calls it when a job ends.
func (a *App) cleanupJobs() {
	if store, err := jobStore(); err == nil {
		_ = store.Cleanup(time.Now())
	}
}
