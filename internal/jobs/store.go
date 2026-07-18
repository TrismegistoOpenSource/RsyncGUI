package jobs

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RunningLockName is the one file in here with a fixed name, and it is
// deliberate: it is what enforces "one job at a time".
//
// RsyncGUI has always run a single job at a time — the in-memory busy flag,
// profiles of a tag running in sequence, even folder verification sharing the
// same gate. Detaching jobs would quietly break that, because the flag dies
// with the window while the job does not: closing and reopening during a copy
// would let a second one start. This lock is that invariant, moved somewhere
// that outlives the window.
//
// Everything else in this package is already per-job and would cope with
// several live jobs unchanged. If parallel runs are ever wanted, this file is
// the only thing to rethink — and it must come with a check that two jobs
// never write to the same destination, which is the real hazard there.
const RunningLockName = "running.lock"

// Retention. A job that went fine has nothing to tell: its log is deleted the
// moment it finishes and the one-line summary in its state takes over. Logs
// are only worth keeping when something went wrong.
const (
	FailedLogRetention = 30 * 24 * time.Hour // logs of failed runs
	StateRetention     = 90 * 24 * time.Hour // the small state files
	MaxJobsKept        = 50                  // history depth
	MaxDirBytes        = 200 << 20           // hard ceiling on the whole folder
)

// Store is the jobs directory.
type Store struct{ Dir string }

func NewStore(dir string) *Store { return &Store{Dir: dir} }

// RunningLockPath is where the global one-at-a-time lock lives.
func (s *Store) RunningLockPath() string { return filepath.Join(s.Dir, RunningLockName) }

// Paths returns one job's files, derived from its id.
func (s *Store) Paths(jobID string) (Paths, error) { return PathsFor(s.Dir, jobID) }

// IsAlive reports whether a job is genuinely still running, by asking the lock
// rather than the recorded pid. A pid can have been recycled by an unrelated
// program, which would leave a job showing as "in progress" forever.
func (s *Store) IsAlive(jobID string) bool {
	p, err := s.Paths(jobID)
	if err != nil {
		return false
	}
	return IsHeld(p.Lock)
}

// List returns every job on disk, newest first, with states that claim to be
// running but are not reported as orphaned.
//
// Orphaned is an honest answer: the supervisor died without writing an
// outcome, so the job neither succeeded nor failed as far as anyone knows.
// Silently calling it failed would be a lie, and leaving it "running" would be
// worse.
func (s *Store) List() ([]State, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []State
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if !ValidJobID(id) {
			continue
		}
		st, err := ReadState(s.Dir, id)
		if err != nil {
			continue // unreadable or half-written: skip rather than fail the list
		}
		if st.Running() && !s.IsAlive(id) {
			st.Status = StatusOrphaned
			st.Summary = "Interrotto in modo anomalo: l'esito non è noto."
			_ = WriteState(s.Dir, st)
		}
		out = append(out, st)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

// FinishLog applies the end-of-job half of the retention policy: the log of a
// run that went fine (or was deliberately stopped) is deleted straight away.
// Keeping megabytes of "file transferred" lines for a copy nobody will ask
// about again is exactly how the folder fills up.
func (s *Store) FinishLog(st State) error {
	p, err := s.Paths(st.JobID)
	if err != nil {
		return err
	}
	switch st.Status {
	case StatusSuccess, StatusAborted:
		if err := os.Remove(p.Log); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// Cleanup enforces the periodic half of the policy. It runs at startup and
// after every job: a handful of stats over a small directory, cheap enough not
// to need scheduling.
//
// Nothing belonging to a live job is ever touched — that guard comes first in
// every branch, because deleting the log of a copy in progress would be the
// silliest possible way to break this feature.
func (s *Store) Cleanup(now time.Time) error {
	states, err := s.List()
	if err != nil {
		return err
	}

	alive := map[string]bool{}
	for _, st := range states {
		if st.Running() && s.IsAlive(st.JobID) {
			alive[st.JobID] = true
		}
	}

	// 1. Logs of failed runs, past their age.
	for _, st := range states {
		if alive[st.JobID] {
			continue
		}
		if !st.FinishedAt.IsZero() && now.Sub(st.FinishedAt) > FailedLogRetention {
			s.removeLog(st.JobID)
		}
	}

	// 2. States past their age, log and lock with them.
	for _, st := range states {
		if alive[st.JobID] {
			continue
		}
		ref := st.FinishedAt
		if ref.IsZero() {
			ref = st.StartedAt
		}
		if !ref.IsZero() && now.Sub(ref) > StateRetention {
			s.removeJob(st.JobID)
		}
	}

	// 3. History depth: keep the most recent MaxJobsKept.
	if remaining := s.finishedNewestFirst(alive); len(remaining) > MaxJobsKept {
		for _, st := range remaining[MaxJobsKept:] {
			s.removeJob(st.JobID)
		}
	}

	// 4. Hard ceiling on the folder: drop the oldest logs until under budget.
	// The state files are left alone — they are tiny, and losing the history of
	// what ran is a worse trade than losing old output.
	if size, _ := s.dirSize(); size > MaxDirBytes {
		remaining := s.finishedNewestFirst(alive)
		for i := len(remaining) - 1; i >= 0 && size > MaxDirBytes; i-- {
			if n := s.logSize(remaining[i].JobID); n > 0 {
				s.removeLog(remaining[i].JobID)
				size -= n
			}
		}
	}

	s.removeStrayLocks(alive)
	return nil
}

// finishedNewestFirst lists jobs that are not running, newest first.
func (s *Store) finishedNewestFirst(alive map[string]bool) []State {
	states, err := s.List()
	if err != nil {
		return nil
	}
	var out []State
	for _, st := range states {
		if !alive[st.JobID] {
			out = append(out, st)
		}
	}
	return out // List already sorted newest first
}

func (s *Store) removeLog(jobID string) {
	if p, err := s.Paths(jobID); err == nil {
		// A failure here is not worth reporting: on Windows the file may still
		// be open by something, and the next cleanup will get it.
		_ = os.Remove(p.Log)
	}
}

func (s *Store) removeJob(jobID string) {
	if p, err := s.Paths(jobID); err == nil {
		_ = os.Remove(p.Log)
		_ = os.Remove(p.State)
		_ = os.Remove(p.Lock)
	}
}

func (s *Store) logSize(jobID string) int64 {
	p, err := s.Paths(jobID)
	if err != nil {
		return 0
	}
	fi, err := os.Stat(p.Log)
	if err != nil {
		return 0
	}
	return fi.Size()
}

func (s *Store) dirSize() (int64, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, e := range entries {
		if fi, err := e.Info(); err == nil {
			total += fi.Size()
		}
	}
	return total, nil
}

// removeStrayLocks deletes lock files with no state file left beside them,
// which is what a deleted job leaves behind. Live jobs are skipped, and so is
// the global lock.
func (s *Store) removeStrayLocks(alive map[string]bool) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if name == RunningLockName || !strings.HasSuffix(name, ".lock") {
			continue
		}
		id := strings.TrimSuffix(name, ".lock")
		if !ValidJobID(id) || alive[id] {
			continue
		}
		if _, err := os.Stat(filepath.Join(s.Dir, id+".json")); os.IsNotExist(err) {
			_ = os.Remove(filepath.Join(s.Dir, name))
		}
	}
}
