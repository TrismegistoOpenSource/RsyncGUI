// SPDX-License-Identifier: MIT

//go:build linux

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// zombieChildren elenca i figli di questo processo nello stato Z, leggendo
// /proc: non dipende da cmd.Process, che dopo Release non ha più il pid.
func zombieChildren(t *testing.T) []string {
	t.Helper()
	me := strconv.Itoa(os.Getpid())
	stats, _ := filepath.Glob("/proc/[0-9]*/stat")
	var out []string
	for _, p := range stats {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		s := string(data)
		i := strings.LastIndexByte(s, ')')
		if i < 0 {
			continue
		}
		f := strings.Fields(s[i+1:]) // stato, ppid, ...
		if len(f) > 1 && f[0] == "Z" && f[1] == me {
			out = append(out, s[:i+1])
		}
	}
	return out
}

// Un supervisore finito non deve restare zombie finché la finestra è aperta.
// Con Process.Release al posto di Wait il figlio uscito restava nello stato Z
// (visto come "[RsyncGUI] <defunct>" dopo ogni copia, 08/10/2026).
func TestLaunchDetachedReapsTheChild(t *testing.T) {
	// Altri test di questo pacchetto lanciano supervisori senza raccoglierli:
	// conta solo lo zombie nuovo, quello lanciato qui.
	before := map[string]bool{}
	for _, z := range zombieChildren(t) {
		before[z] = true
	}
	if err := launchDetached(exec.Command("/bin/sh", "-c", "exit 0")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond) // il figlio esce subito
	deadline := time.Now().Add(3 * time.Second)
	for {
		var nuovi []string
		for _, z := range zombieChildren(t) {
			if !before[z] {
				nuovi = append(nuovi, z)
			}
		}
		if len(nuovi) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("figli rimasti zombie: %v", nuovi)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
