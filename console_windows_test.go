// SPDX-License-Identifier: MIT

//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
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

// interruptProcess must stop rsync the way Ctrl+C does, not by terminating it:
// rsync removes its partial temporary file only when it gets the interrupt.
// As above, the test relaunches itself without a console (the supervisor's
// condition) and the child interrupts a slow copy.
func TestInterruptProcessFermaRsyncConGentilezza(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync non disponibile")
	}
	const detachedProcess = 0x00000008
	cmd := exec.Command(os.Args[0], "-test.run=^TestInterruptProcessFermaRsyncConGentilezzaFiglio$")
	cmd.Env = append(os.Environ(), "RSYNCGUI_TEST_FIGLIO_INTERRUZIONE="+t.TempDir())
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("processo senza console: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "interruzione=ok\n") {
		t.Fatalf("rsync non si è fermato con gentilezza\n%s", out)
	}
}

func TestInterruptProcessFermaRsyncConGentilezzaFiglio(t *testing.T) {
	dir := os.Getenv("RSYNCGUI_TEST_FIGLIO_INTERRUZIONE")
	if dir == "" {
		t.Skip("solo come processo figlio")
	}
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "grande.bin"), make([]byte, 20<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	// 1 MB/s: la copia da 20 MB dura 20 s, ampiamente più dell'interruzione.
	cmd := newRsyncCmd(context.Background(), "rsync", "-a", "--bwlimit=1000", rsyncPath(src)+"/", rsyncPath(dst)+"/")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	time.Sleep(3 * time.Second)

	start := time.Now()
	if err := interruptProcess(cmd.Process); err != nil {
		cmd.Process.Kill()
		t.Fatalf("interruptProcess: %v", err)
	}
	select {
	case err := <-done:
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("rsync doveva uscire con un codice di errore, non %v", err)
		}
		fmt.Printf("uscita: codice %d dopo %v\n", exitErr.ExitCode(), time.Since(start))
	case <-time.After(4 * time.Second):
		cmd.Process.Kill()
		t.Fatal("rsync non si è fermato entro 4 s dall'interruzione")
	}
	entries, _ := os.ReadDir(dst)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("rimasto un temporaneo in destinazione: %s", e.Name())
		}
	}
	fmt.Println("interruzione=ok")
}
