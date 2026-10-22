// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"rsyncgui/internal/autoverifica"
)

func TestVersionMatchesWails(t *testing.T) {
	b, err := os.ReadFile("wails.json")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`"productVersion":\s*"([^"]+)"`).FindSubmatch(b)
	if m == nil || string(m[1]) != appVersion {
		t.Fatalf("appVersion %q, wails.json %q", appVersion, m)
	}
}

// Outside a packaged build (here, the test binary) there is no INTEGRITA.txt:
// the outcome is "not verifiable", with no network, and it is written to the
// (temporary) config folder.
func TestVerifyIntegrityOutsidePackage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RSYNCGUI_CONFIG_DIR", dir)
	a := NewApp()
	s := a.VerifyIntegrity()
	if s.Esito != autoverifica.EsitoNonVerificabile {
		t.Fatalf("outcome %q", s.Esito)
	}
	if again := a.VerifyIntegrity(); again.Esito != s.Esito {
		t.Error("second call differs")
	}
	if _, err := os.Stat(filepath.Join(dir, autoverifica.NomeFileStato)); err != nil {
		t.Errorf("state not written: %v", err)
	}
}
