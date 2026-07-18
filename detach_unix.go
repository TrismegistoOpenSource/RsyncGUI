//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detach puts the supervisor in a session of its own, so signals aimed at the
// window's process group — and the terminal going away — never reach it.
//
// Surviving is otherwise the default: a child is not "inside" its parent, and
// when the parent dies the child is simply adopted by PID 1. What used to kill
// transfers was not the lack of detachment but the pipe carrying rsync's
// output: when the GUI died the read end closed and rsync died on its next
// write. That is why the supervisor's output goes to a file.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// signalStop asks a supervisor to stop, the same way Ctrl+C would, so rsync
// gets to remove its partial temporary file.
func signalStop(pid int) error {
	return syscall.Kill(pid, syscall.SIGINT)
}
