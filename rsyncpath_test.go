// SPDX-License-Identifier: MIT

package main

import (
	"runtime"
	"testing"
)

func TestCygwinPath(t *testing.T) {
	for in, want := range map[string]string{
		`C:\Users\Nicolò\Dati con spazi`: "/proc/cygdrive/c/Users/Nicolò/Dati con spazi",
		`d:\backup\`:                     "/proc/cygdrive/d/backup/",
		`E:/misto\percorso`:              "/proc/cygdrive/e/misto/percorso",
		`F:\`:                            "/proc/cygdrive/f/",
		`G:`:                             "/proc/cygdrive/g/",
		`\\nas\condivisa\cartella`:       "//nas/condivisa/cartella",
		"utente@host:/srv/dati":          "utente@host:/srv/dati",
		"host:dati":                      "host:dati",
		"rsync://host/modulo":            "rsync://host/modulo",
		"/già/posix":                     "/già/posix",
		"relativo/a":                     "relativo/a",
	} {
		if got := cygwinPath(in); got != want {
			t.Errorf("cygwinPath(%q) = %q, atteso %q", in, got, want)
		}
	}
}

// Sources, destination and the filter file must all reach rsync converted:
// one plain "C:\..." left behind and rsync takes it for a host.
func TestRsyncArgsConvertsWindowsPaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("solo Windows")
	}
	args := rsyncArgs(SyncOptions{Delete: true}, []string{`C:\non esiste\A`}, `D:\Dest`, `C:\Temp\r.rules`)
	n := len(args)
	if args[n-2] != "/proc/cygdrive/c/non esiste/A" || args[n-1] != "/proc/cygdrive/d/Dest" {
		t.Errorf("sorgente/destinazione non convertite: %q", args[n-2:])
	}
	found := false
	for _, a := range args {
		if a == "--filter=merge /proc/cygdrive/c/Temp/r.rules" {
			found = true
		}
	}
	if !found {
		t.Errorf("file di regole non convertito: %q", args)
	}
}
