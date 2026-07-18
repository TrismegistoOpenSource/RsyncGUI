//go:build windows

package main

import (
	"os/exec"

	"golang.org/x/sys/windows"
)

// detach is the Windows counterpart of setsid: DETACHED_PROCESS keeps the
// supervisor away from the window's console, and its own process group means a
// Ctrl event sent to the GUI does not reach it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &windows.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
	}
}

// signalStop has no SIGINT to send. CTRL_BREAK_EVENT to the supervisor's own
// process group is the closest equivalent; the supervisor treats it like an
// interrupt. If it does not respond, the caller falls back to terminating it.
func signalStop(pid int) error {
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(pid))
}
