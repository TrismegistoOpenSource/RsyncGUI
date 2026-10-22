// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "profiles.json")
	if err := os.WriteFile(p, []byte("vecchio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(p, []byte("nuovo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "nuovo" {
		t.Fatalf("contenuto %q", b)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(left) != 0 {
		t.Errorf("temporanei rimasti: %v", left)
	}
}

func TestWriteFileAtomicFailureKeepsOldFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "profiles.json")
	if err := os.WriteFile(p, []byte("vecchio"), 0o644); err != nil {
		t.Fatal(err)
	}
	// il target è una cartella: il rename fallisce, il vecchio contenuto non si tocca
	d := filepath.Join(dir, "cartella")
	if err := os.Mkdir(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(d, []byte("x"), 0o644); err == nil {
		t.Fatal("scrittura su una cartella riuscita")
	}
	left, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(left) != 0 {
		t.Errorf("temporanei rimasti dopo l'errore: %v", left)
	}
	if b, _ := os.ReadFile(p); string(b) != "vecchio" {
		t.Errorf("il file è cambiato: %q", b)
	}
	// directory inesistente
	if err := writeFileAtomic(filepath.Join(dir, "no", "x"), []byte("x"), 0o644); err == nil {
		t.Error("scrittura in una cartella inesistente riuscita")
	}
}
