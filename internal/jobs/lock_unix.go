//go:build !windows

package jobs

import (
	"os"
	"syscall"
)

// flock is released automatically by the kernel when the process exits or is
// killed, which is exactly the property that makes it usable as a liveness
// signal for a supervisor that may be SIGKILLed.
func tryLockFile(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return ErrLocked
		}
		return err
	}
	return nil
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
