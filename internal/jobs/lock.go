// Package jobs owns everything about a sync that outlives the window: its
// state file, its log, and the locks that say whether it is still alive.
//
// The organising idea is that a job is identified by its jobId and nothing
// else. Every file belonging to a job is derived from that id, never read from
// a stored path — see Paths.
package jobs

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrLocked means someone else holds the lock. It is a normal outcome, not a
// failure: it is how "a job is already running" is discovered.
var ErrLocked = errors.New("risorsa già in uso da un'altra esecuzione")

// Lock is an advisory lock held on a file for as long as the owning process
// lives. It is the answer to "is that job still running?".
//
// A pid cannot answer that question: pids get recycled, so a state file saying
// "pid 12064 is running" may well be pointing at some unrelated program that
// started days later, and the job would look alive forever. The lock, by
// contrast, is released by the operating system the moment the holder dies,
// however it dies — clean exit, crash or power loss. If the lock is free, the
// job is over. Full stop.
type Lock struct {
	path string
	file *os.File
}

// TryLock takes the lock without waiting. It returns ErrLocked if another live
// process holds it.
func TryLock(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := tryLockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	return &Lock{path: path, file: f}, nil
}

// Unlock releases the lock. Deleting the file is deliberately not done here:
// on Windows a file cannot be removed while another process has it open, and
// racing to delete it would only reintroduce the ambiguity the lock exists to
// remove. Stale lock files are tiny and cleaned up with the rest (see Store).
func (l *Lock) Unlock() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := unlockFile(l.file)
	if cerr := l.file.Close(); err == nil {
		err = cerr
	}
	l.file = nil
	return err
}

// IsHeld reports whether someone currently holds the lock at path. It works by
// trying to take it and immediately letting go, so it says nothing about who
// holds it — only that the resource is busy.
func IsHeld(path string) bool {
	if _, err := os.Stat(path); err != nil {
		return false // no lock file at all: nothing can be holding it
	}
	l, err := TryLock(path)
	if err != nil {
		return true
	}
	l.Unlock()
	return false
}
