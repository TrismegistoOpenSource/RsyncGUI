//go:build !windows

package main

import "syscall"

// syscallKill with signal 0 checks a process exists without disturbing it.
func syscallKill(pid int, sig syscall.Signal) error {
	return syscall.Kill(pid, sig)
}
