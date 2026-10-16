// SPDX-License-Identifier: MIT

//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

// rsync is a console program. Launched by the window (a GUI program, no
// console) or by the supervisor (DETACHED_PROCESS), Windows gives it a console
// window of its own unless it is created with CREATE_NO_WINDOW: a black window
// open for the whole copy.
//
// The test relaunches itself without a console, as the supervisor runs, and
// from there starts powershell through newRsyncCmd — the constructor of every
// rsync invocation — asking it whether it has a console window. (From a
// process that already has a console, the child would inherit it and the test
// could not tell.)
func TestRsyncSenzaFinestraDiConsole(t *testing.T) {
	const detachedProcess = 0x00000008
	cmd := exec.Command(os.Args[0], "-test.run=^TestRsyncSenzaFinestraDiConsoleFiglio$")
	cmd.Env = append(os.Environ(), "RSYNCGUI_TEST_FIGLIO_SENZA_CONSOLE=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("processo senza console: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "console=0\n") {
		t.Fatalf("il comando ha aperto una finestra di console: serve CREATE_NO_WINDOW\n%s", out)
	}
}

func TestRsyncSenzaFinestraDiConsoleFiglio(t *testing.T) {
	if os.Getenv("RSYNCGUI_TEST_FIGLIO_SENZA_CONSOLE") != "1" {
		t.Skip("solo come processo figlio")
	}
	probe := `Add-Type -Name W -Namespace N -MemberDefinition ` +
		`'[DllImport("kernel32.dll")] public static extern IntPtr GetConsoleWindow();'; ` +
		`"console=" + [N.W]::GetConsoleWindow()`
	out, err := newRsyncCmd(context.Background(), "powershell", "-NoProfile", "-NonInteractive", "-Command", probe).CombinedOutput()
	if err != nil {
		t.Fatalf("powershell: %v\n%s", err, out)
	}
	fmt.Printf("%s\n", strings.TrimSpace(string(out)))
}
