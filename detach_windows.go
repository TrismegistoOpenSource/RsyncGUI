// SPDX-License-Identifier: MIT

//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

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

// hideConsole: rsync is a console program, launched by the window (a GUI
// program) or by the supervisor (DETACHED_PROCESS), neither with a console.
// Without CREATE_NO_WINDOW Windows gives it one of its own: a black window open
// for the whole copy.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}

// signalStop has no SIGINT to send. CTRL_BREAK_EVENT to the supervisor's own
// process group is the closest equivalent; the supervisor treats it like an
// interrupt. If it does not respond, the caller falls back to terminating it.
func signalStop(pid int) error {
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(pid))
}

// interruptProcess asks a child to stop the way Ctrl+C does. Windows has no
// os.Interrupt for a child (Process.Signal fails) and a console Ctrl+C does not
// reach rsync, which lives alone on the hidden console CREATE_NO_WINDOW gave
// it. rsync is an MSYS program, though, and the kill.exe that sits next to it
// sends it a real SIGINT from outside (-W: the PID is a Windows one): rsync
// removes its partial temporary file and exits. If there is no kill.exe or it
// fails, the error goes back to the caller, which terminates the process after
// a delay.
func interruptProcess(p *os.Process) error {
	bin, err := exec.LookPath("rsync")
	if err != nil {
		return err
	}
	kill := filepath.Join(filepath.Dir(bin), "kill.exe")
	if _, err := os.Stat(kill); err != nil {
		return err
	}
	cmd := exec.Command(kill, "-INT", "-W", strconv.Itoa(p.Pid))
	hideConsole(cmd)
	return cmd.Run()
}
