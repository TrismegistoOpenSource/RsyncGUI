// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

func nfc(s string) string { return norm.NFC.String(s) }
func nfd(s string) string { return norm.NFD.String(s) }

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTwinSpellings(t *testing.T) {
	if got := twinSpellings("plain.md"); len(got) != 0 {
		t.Errorf("un nome ASCII non ha gemelli: %q", got)
	}
	if got := twinSpellings(nfc("Caffè.md")); !slices.Equal(got, []string{nfd("Caffè.md")}) {
		t.Errorf("NFC → atteso il solo gemello NFD, ottenuto %q", got)
	}
	if got := twinSpellings(nfd("Caffè.md")); !slices.Equal(got, []string{nfc("Caffè.md")}) {
		t.Errorf("NFD → atteso il solo gemello NFC, ottenuto %q", got)
	}
	// Half composed, half decomposed: both canonical spellings are twins.
	mixed := nfc("è") + nfd("à")
	if got := twinSpellings(mixed); len(got) != 2 {
		t.Errorf("nome misto → attesi 2 gemelli, ottenuto %q", got)
	}
	if got := twinSpellings(nfc("a\nè")); len(got) != 0 {
		t.Errorf("i nomi con a capo vanno saltati: %q", got)
	}
}

func TestEscapeFilterPattern(t *testing.T) {
	for in, want := range map[string]string{
		"a/b è.md":     "a/b è.md",
		`b\è.md`:       `b\è.md`, // no wildcard: backslash is literal for rsync
		"a[1]*.md":     `a\[1]\*.md`,
		`c\[è].md`:     `c\\\[è].md`,
		"d?.md":        `d\?.md`,
		"# e - f .md ": "# e - f .md ",
	} {
		if got := escapeFilterPattern(in); got != want {
			t.Errorf("escapeFilterPattern(%q) = %q, atteso %q", in, got, want)
		}
	}
}

func TestProtectRulesForTwins(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, nfc("Città"), nfc("Caffè.md")), "x")
	writeFile(t, filepath.Join(src, nfc("Città"), "ascii.md"), "x")
	writeFile(t, filepath.Join(src, "solo-ascii", "a.md"), "x")
	want := []string{
		"-r /" + nfd("Città"),
		"-r /" + nfc("Città") + "/" + nfd("Caffè.md"),
	}
	// "*" non può stare in un nome di file su Windows: lì il caso del
	// carattere jolly da proteggere non esiste.
	if runtime.GOOS != "windows" {
		writeFile(t, filepath.Join(src, "Luoghi", nfc("La città*.md")), "x")
		want = append(want, `-r /Luoghi/`+nfd(`La città\*.md`))
	}

	// Contents mode: paths are relative to the source; the parent keeps the
	// source's spelling, only the last component is the twin.
	got := protectRulesForTwins(SyncOptions{}, []string{src})
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("regole:\n got %q\nwant %q", got, want)
	}

	// RecreateStructure: everything sits under the source folder's own name,
	// which is itself protected when it has an accent.
	accented := filepath.Join(t.TempDir(), nfc("Unità"))
	writeFile(t, filepath.Join(accented, nfc("è.md")), "x")
	got = protectRulesForTwins(SyncOptions{RecreateStructure: true}, []string{accented + "/"})
	want = []string{"-r /" + nfd("Unità"), "-r /" + nfc("Unità") + "/" + nfd("è.md")}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("RecreateStructure:\n got %q\nwant %q", got, want)
	}

	// A single file as source lands in the destination by its own name.
	file := filepath.Join(t.TempDir(), nfc("Perché.md"))
	writeFile(t, file, "x")
	got = protectRulesForTwins(SyncOptions{}, []string{file})
	if !slices.Equal(got, []string{"-r /" + nfd("Perché.md")}) {
		t.Errorf("sorgente file: %q", got)
	}

	// A missing source is rsync's problem to report, not a crash here.
	if got := protectRulesForTwins(SyncOptions{}, []string{filepath.Join(src, "manca")}); len(got) != 0 {
		t.Errorf("sorgente inesistente: %q", got)
	}
}

func TestWriteTwinProtectRulesOnlyWithDelete(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, nfc("è.md")), "x")

	p, cleanup, err := writeTwinProtectRules(SyncOptions{}, []string{src})
	cleanup()
	if err != nil || p != "" {
		t.Fatalf("senza Delete non serve nessun file: %q, %v", p, err)
	}

	p, cleanup, err = writeTwinProtectRules(SyncOptions{Delete: true}, []string{src})
	if err != nil || p == "" {
		t.Fatalf("con Delete e un nome accentato serve il file: %q, %v", p, err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "-r /"+nfd("è.md")+"\n" {
		t.Errorf("contenuto del file: %q", data)
	}
	cleanup()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("cleanup deve rimuovere il file: %v", err)
	}

	ascii := t.TempDir()
	writeFile(t, filepath.Join(ascii, "a.md"), "x")
	p, cleanup, err = writeTwinProtectRules(SyncOptions{Delete: true}, []string{ascii})
	cleanup()
	if err != nil || p != "" {
		t.Fatalf("senza nomi accentati non serve nessun file: %q, %v", p, err)
	}
}

// runRealRsync runs the rsync on PATH exactly as the app would.
func runRealRsync(t *testing.T, opts SyncOptions, src, dst string) string {
	t.Helper()
	bin, err := exec.LookPath("rsync")
	if err != nil {
		t.Skip("rsync non disponibile")
	}
	rules, cleanup, err := writeTwinProtectRules(opts, []string{src})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	out, err := exec.Command(bin, rsyncArgs(opts, []string{src}, dst, rules)...).CombinedOutput()
	if err != nil {
		t.Fatalf("rsync: %v\n%s", err, out)
	}
	return string(out)
}

// A folder removed from the source used to survive in the destination with
// just a .DS_Store inside: excluded files are protected from deletion.
func TestRealRsyncDeletesFoldersLeftWithJunkOnly(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, "Viva", "nota.md"), "x")
	writeFile(t, filepath.Join(src, "Viva", ".DS_Store"), "src")
	writeFile(t, filepath.Join(dst, "Rimossa", "Sotto", ".DS_Store"), "d")
	writeFile(t, filepath.Join(dst, "Rimossa", ".DS_Store"), "d")
	writeFile(t, filepath.Join(dst, ".Spotlight-V100", "indice"), "volume")
	writeFile(t, filepath.Join(dst, "Viva", "._nota.md"), "xattr")

	opts := SyncOptions{Delete: true, Verbose: true, ExcludeSystemFiles: true}
	runRealRsync(t, opts, src+"/", dst)

	if _, err := os.Stat(filepath.Join(dst, "Rimossa")); !os.IsNotExist(err) {
		t.Errorf("la cartella tolta dalla sorgente deve sparire: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "Viva", ".DS_Store")); !os.IsNotExist(err) {
		t.Errorf(".DS_Store non va copiato: %v", err)
	}
	for _, keep := range []string{".Spotlight-V100/indice", "Viva/._nota.md", "Viva/nota.md"} {
		if _, err := os.Stat(filepath.Join(dst, keep)); err != nil {
			t.Errorf("%s deve restare: %v", keep, err)
		}
	}
}

// The data-loss case: the destination spells an accented name differently from
// the source. APFS behaves like the macOS SMB client here: it treats the two
// spellings as one file but lists the name as stored, so a destination written
// in NFD with a source in NFC reproduces what rsync sees on the QNAP share.
// Without the protection rsync deletes the destination file as "extra" (on the
// share the fresh copy vanishes with it). Other systems keep the two spellings
// as distinct files, so the scenario does not exist there.
func TestRealRsyncKeepsAccentedTwins(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("serve un filesystem che equipara NFC e NFD (APFS)")
	}
	src, dst := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, nfc("Città"), nfc("Caffè.md")), "nuovo")
	writeFile(t, filepath.Join(dst, nfd("Città"), nfd("Caffè.md")), "vecchio")
	writeFile(t, filepath.Join(dst, nfd("Città"), nfd("Tolto è.md")), "da cancellare")

	opts := SyncOptions{Delete: true, Verbose: true, Inplace: true, ExcludeSystemFiles: true}
	out := runRealRsync(t, opts, src+"/", dst)

	if strings.Contains(nfc(out), "deleting "+nfc("Città/Caffè.md")) {
		t.Errorf("il gemello accentato non deve essere cancellato:\n%s", out)
	}
	if !strings.Contains(nfc(out), "deleting "+nfc("Città/Tolto è.md")) {
		t.Errorf("un file tolto davvero dalla sorgente va cancellato:\n%s", out)
	}
	entries, err := os.ReadDir(filepath.Join(dst, nfd("Città")))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	// Still the NFD name: the file was updated in place, never deleted and
	// recreated (which would have left the source's NFC spelling).
	if !slices.Equal(names, []string{nfd("Caffè.md")}) {
		t.Errorf("atteso solo %+q (NFD), trovato %+q", nfd("Caffè.md"), names)
	}
	data, _ := os.ReadFile(filepath.Join(dst, nfc("Città"), nfc("Caffè.md")))
	if string(data) != "nuovo" {
		t.Errorf("il contenuto va aggiornato: %q", data)
	}

	// Second run: nothing left to do, nothing deleted.
	if out := runRealRsync(t, opts, src+"/", dst); strings.Contains(out, "deleting") {
		t.Errorf("il secondo giro non deve cancellare nulla:\n%s", out)
	}
}
