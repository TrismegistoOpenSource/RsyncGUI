package main

import (
	"context"
	"encoding/json"
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

// The exact JSON written by the 1.x Swift app must keep loading.
const legacyJSON = `[{"tag":"Vault","source":"/Users/admin/src","id":"9BCAE6EC","options":{"checksum":true,"verbose":true,"dryRun":false,"delete":true,"compress":false},"name":"ArgusManuale","destination":"/Volumes/X/dest"}]`

func TestUnmarshalLegacySchema(t *testing.T) {
	var profiles []SyncProfile
	if err := json.Unmarshal([]byte(legacyJSON), &profiles); err != nil {
		t.Fatal(err)
	}
	p := profiles[0]
	if len(p.Sources) != 1 || p.Sources[0] != "/Users/admin/src" {
		t.Fatalf("sources = %v", p.Sources)
	}
	if len(p.Destinations) != 1 || p.Destinations[0] != "/Volumes/X/dest" {
		t.Fatalf("destinations = %v", p.Destinations)
	}
	if !p.Options.Checksum || !p.Options.Delete || p.Options.Compress {
		t.Fatalf("options = %+v", p.Options)
	}
	if p.Tag != "Vault" {
		t.Fatalf("tag = %q", p.Tag)
	}
}

func TestMarshalKeepsLegacyKeys(t *testing.T) {
	p := SyncProfile{
		ID:           "X1",
		Name:         "multi",
		Sources:      []string{"/a", "/b"},
		Destinations: []string{"/d1", "/d2"},
		Tag:          "night",
		Options:      SyncOptions{Checksum: true},
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		`"sources":["/a","/b"]`,
		`"destinations":["/d1","/d2"]`,
		`"source":"/a"`,
		`"destination":"/d1"`,
		`"tag":"night"`,
		`"checksum":true`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("marshal output missing %s: %s", want, s)
		}
	}

	// round-trip: new schema wins over legacy keys
	var back SyncProfile
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Sources) != 2 || len(back.Destinations) != 2 {
		t.Fatalf("round-trip lost paths: %+v", back)
	}
}

func TestIsRemote(t *testing.T) {
	cases := map[string]bool{
		"/Users/admin/docs":      false,
		"relative/path":          false,
		"nas:/backup/foto":       true,
		"user@host:/backup":      true,
		"rsync://host/mod/path":  true,
		`C:\Users\x`:             false, // Windows drive letter
		"/dir/with:colon/inside": false,
	}
	for path, want := range cases {
		if got := isRemote(path); got != want {
			t.Errorf("isRemote(%q) = %v, want %v", path, got, want)
		}
	}
}

// The happy path enqueues work that needs the Wails runtime, so this covers
// the selection logic through the guard: tagged profiles must not be picked up.
func TestRunUntaggedSkipsTaggedProfiles(t *testing.T) {
	a := NewApp()
	a.profiles = []SyncProfile{
		{ID: "1", Name: "a", Tag: "vault"},
		{ID: "2", Name: "b", Tag: "notte"},
	}
	if err := a.RunUntagged(); err == nil {
		t.Fatal("expected an error: every profile is tagged, none should be selected")
	}
}

func TestReorderProfiles(t *testing.T) {
	// ReorderProfiles persists; redirect storage so the real profiles.json
	// is never touched by the test run.
	t.Setenv("RSYNCGUI_CONFIG_DIR", t.TempDir())

	a := NewApp()
	a.profiles = []SyncProfile{
		{ID: "1", Name: "a", Sources: []string{"/a"}, Destinations: []string{"/x"}},
		{ID: "2", Name: "b", Sources: []string{"/b"}, Destinations: []string{"/y"}},
		{ID: "3", Name: "c", Sources: []string{"/c"}, Destinations: []string{"/z"}},
	}
	if _, err := a.ReorderProfiles([]string{"3", "1"}); err != nil {
		t.Fatal(err)
	}

	got := []string{}
	for _, p := range a.profiles {
		got = append(got, p.ID)
	}
	want := []string{"3", "1", "2"} // listed ids first, unlisted kept at the end
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

// A save must always leave the previous contents recoverable from the .bak.
func TestSaveKeepsBackupOfPreviousContents(t *testing.T) {
	t.Setenv("RSYNCGUI_CONFIG_DIR", t.TempDir())

	a := NewApp()
	if _, err := a.SaveProfile(SyncProfile{
		Name: "originale", Sources: []string{"/src"}, Destinations: []string{"/dst"},
	}); err != nil {
		t.Fatal(err)
	}

	path, _ := profilesPath()
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// A second save must push the first version into the backup.
	if _, err := a.SaveProfile(SyncProfile{
		Name: "successivo", Sources: []string{"/src2"}, Destinations: []string{"/dst2"},
	}); err != nil {
		t.Fatal(err)
	}

	bak, err := backupPath()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(bak)
	if err != nil {
		t.Fatalf("nessun backup creato: %v", err)
	}
	if string(saved) != string(first) {
		t.Fatalf("il backup non contiene la versione precedente:\n%s", saved)
	}
	if strings.Contains(string(saved), "successivo") {
		t.Fatal("il backup contiene già i dati nuovi: è stato scritto troppo tardi")
	}
}

func TestAbortWhenIdleReturnsError(t *testing.T) {
	a := NewApp()
	if err := a.Abort(); err == nil {
		t.Fatal("Abort senza esecuzioni in corso deve dare errore")
	}
}

// The real point of Abort: a cancelled run context must stop rsync rather than
// let it run to completion.
func TestExecRsyncStopsOnCancelledContext(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync non disponibile")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "file.txt"), []byte("contenuto"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // già interrotto prima di partire

	a := NewApp()
	_, err := a.execRsync(ctx, SyncOptions{}, []string{src}, dst)
	if err == nil {
		t.Fatal("con context annullato rsync non doveva essere eseguito")
	}
	// Nothing must have been copied.
	if _, statErr := os.Stat(filepath.Join(dst, "file.txt")); statErr == nil {
		t.Fatal("rsync ha copiato comunque: l'interruzione non ha avuto effetto")
	}
}

// Abort must behave like Ctrl+C: the process has to die from SIGINT, not the
// SIGKILL that exec.CommandContext would send by default. rsync only cleans up
// its partial temp files when it gets SIGINT.
func TestInterruptOnCancelSendsSIGINTNotSIGKILL(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "sleep", "30")
	interruptOnCancel(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	err := cmd.Wait()
	if err == nil {
		t.Fatal("il processo doveva essere interrotto, non finire da solo")
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("errore inatteso: %v", err)
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		t.Skip("stato di uscita non ispezionabile su questa piattaforma")
	}
	if got := status.Signal(); got != syscall.SIGINT {
		t.Fatalf("segnale = %v, atteso SIGINT (Ctrl+C)", got)
	}
}

// Profiles saved before the option existed carry no "excludeSystemFiles" key:
// they must still get the exclusions, not silently decode to false.
func TestLegacyProfileGetsSystemExcludesByDefault(t *testing.T) {
	var profiles []SyncProfile
	if err := json.Unmarshal([]byte(legacyJSON), &profiles); err != nil {
		t.Fatal(err)
	}
	if !profiles[0].Options.ExcludeSystemFiles {
		t.Fatal("un profilo vecchio deve ereditare l'esclusione dei file di sistema")
	}
	// An explicit false must still win.
	var off []SyncProfile
	if err := json.Unmarshal([]byte(`[{"name":"x","options":{"excludeSystemFiles":false}}]`), &off); err != nil {
		t.Fatal(err)
	}
	if off[0].Options.ExcludeSystemFiles {
		t.Fatal("un false esplicito deve essere rispettato")
	}
}

func TestRsyncArgs(t *testing.T) {
	joined := func(args []string) string { return strings.Join(args, " ") }

	// -a is always present: permissions are preserved regardless.
	base := rsyncArgs(SyncOptions{}, []string{"/src"}, "/dst")
	if base[0] != "-a" {
		t.Fatalf("-a deve essere sempre il primo flag: %v", base)
	}
	if strings.Contains(joined(base), "--exclude") {
		t.Fatalf("senza ExcludeSystemFiles non devono esserci esclusioni: %v", base)
	}
	if strings.Contains(joined(base), "--inplace") {
		t.Fatalf("--inplace non deve comparire se non richiesto: %v", base)
	}

	full := rsyncArgs(SyncOptions{
		Checksum:           true,
		Delete:             true,
		Inplace:            true,
		ExcludeSystemFiles: true,
		CustomExcludes:     []string{"node_modules", "  ", "*.tmp"},
	}, []string{"/src"}, "/dst")
	s := joined(full)

	for _, want := range []string{"-a", "-c", "--delete", "--inplace",
		"--exclude=.DS_Store", "--exclude=._*", "--exclude=Thumbs.db",
		"--exclude=node_modules", "--exclude=*.tmp"} {
		if !strings.Contains(s, want) {
			t.Errorf("manca %q in: %s", want, s)
		}
	}
	if strings.Contains(s, "--exclude=  ") || strings.Contains(s, "--exclude= ") {
		t.Errorf("le esclusioni vuote vanno scartate: %s", s)
	}
	// source and destination must stay last, in order
	if full[len(full)-1] != "/dst" {
		t.Errorf("la destinazione deve essere l'ultimo argomento: %v", full)
	}
}

// A directory source must sync its contents, not itself as a subfolder.
func TestRsyncArgsAddsTrailingSlashToDirectorySource(t *testing.T) {
	dir := t.TempDir()
	args := rsyncArgs(SyncOptions{}, []string{dir}, "/dst")
	src := args[len(args)-2]
	if !strings.HasSuffix(src, "/") {
		t.Fatalf("sorgente cartella senza slash finale: %q", src)
	}
}

func TestIsPartialTransferExitCode(t *testing.T) {
	for code, want := range map[int]bool{0: false, 1: false, 11: false, 23: true, 24: true, 30: false} {
		if got := isPartialTransferExitCode(code); got != want {
			t.Errorf("isPartialTransferExitCode(%d) = %v, want %v", code, got, want)
		}
	}
}

// Regression: a QNAP/SMB share can list a file (readdir succeeds) but then
// refuse to open it — reproduced here with a permission-denied file, which
// rsync reports the exact same way (exit 23, "partial transfer due to
// error"). Before this fix the app treated ANY non-zero exit as a hard
// failure, so a destination that received 250+ files out of 255 was reported
// as fully failed. It must instead count as completed, with a warning.
func TestExecRsyncReturnsPartialExitCodeNotError(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync non disponibile")
	}

	src := t.TempDir()
	dst := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "leggibile.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(src, "illeggibile.txt")
	if err := os.WriteFile(broken, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(broken, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(broken, 0o644) })

	a := NewApp()
	exitCode, err := a.execRsync(context.Background(), SyncOptions{}, []string{src}, dst)
	if err != nil {
		t.Fatalf("un file illeggibile non deve far fallire l'esecuzione stessa: %v", err)
	}
	if !isPartialTransferExitCode(exitCode) {
		t.Fatalf("exit code = %d, atteso 23 (partial transfer due to error)", exitCode)
	}
	if _, statErr := os.Stat(filepath.Join(dst, "leggibile.txt")); statErr != nil {
		t.Fatal("il file leggibile doveva comunque arrivare a destinazione")
	}
}

// End-to-end: runProfile must classify a partial-exit-code destination as
// completed (with a warning issue), not as a failed destination — that's the
// actual behavioral bug this fix closes.
func TestRunProfileCountsPartialTransferAsCompleted(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync non disponibile")
	}

	src := t.TempDir()
	dst := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "ok.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(src, "rotto.txt")
	if err := os.WriteFile(broken, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(broken, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(broken, 0o644) })

	a := NewApp()
	p := SyncProfile{Name: "test", Sources: []string{src}, Destinations: []string{dst}}
	completed, issues := a.runProfile(context.Background(), p)

	if completed != 1 {
		t.Fatalf("completed = %d, atteso 1 (la destinazione ha comunque ricevuto la maggior parte dei file)", completed)
	}
	if len(issues) != 1 {
		t.Fatalf("issues = %v, atteso esattamente 1 avviso (non un fallimento pieno)", issues)
	}
}

// verifyWalk must catch a file that a directory listing shows but that
// refuses to open — the exact class of bug found on the QNAP share (readdir/
// stat succeed, open fails) — while leaving genuinely readable files alone.
func TestVerifyWalkFindsUnopenableFiles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("da root i permessi non bloccano la lettura")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "buono.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sotto")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "anche_buono.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(dir, "rotto.txt")
	if err := os.WriteFile(broken, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(broken, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(broken, 0o644) })

	a := NewApp()
	scanned, brokenList := a.verifyWalk(context.Background(), dir)

	if scanned != 3 {
		t.Fatalf("scanned = %d, attesi 3 file (buono, anche_buono, rotto)", scanned)
	}
	if len(brokenList) != 1 || !strings.Contains(brokenList[0], "rotto.txt") {
		t.Fatalf("broken = %v, atteso esattamente rotto.txt", brokenList)
	}
}

// A clean tree must report nothing broken.
func TestVerifyWalkCleanTreeReportsNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := NewApp()
	scanned, broken := a.verifyWalk(context.Background(), dir)
	if scanned != 1 || len(broken) != 0 {
		t.Fatalf("scanned=%d broken=%v, attesi 1 file e nessun problema", scanned, broken)
	}
}

// Cancelling the context mid-walk must stop it rather than run to completion,
// same contract as Abort on a sync.
func TestVerifyWalkStopsOnCancelledContext(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 50; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d.txt", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // già annullato prima di iniziare

	a := NewApp()
	scanned, _ := a.verifyWalk(ctx, dir)
	if scanned >= 50 {
		t.Fatalf("scanned = %d, il context era già annullato: non doveva scansionare tutto", scanned)
	}
}

func TestAvailability(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "src")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}

	if !sourceAvailable(existing) {
		t.Error("existing source should be available")
	}
	if sourceAvailable(filepath.Join(dir, "missing")) {
		t.Error("missing source should be unavailable")
	}
	// destination may not exist if its parent does (rsync creates it)
	if !destAvailable(filepath.Join(dir, "newdest")) {
		t.Error("dest with existing parent should be available")
	}
	if destAvailable("/Volumes/DiscoInesistente/backup/foto") {
		t.Error("dest on unmounted volume should be unavailable")
	}
	if !sourceAvailable("nas:/remote") || !destAvailable("nas:/remote") {
		t.Error("remote paths are always attempted")
	}
}

// Regression: a dead cloud/network mount still stat's fine while every real
// read fails. Checking only stat (on the path or its parent) marked such a
// destination available, the sync was attempted and rsync failed with
// "Socket is not connected" — precisely what the skip was meant to prevent.
// An unreadable directory stands in for the dead mount here.
func TestDestAvailableRejectsUnreadableParent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("da root i permessi non fermano la lettura")
	}
	dir := t.TempDir()
	dead := filepath.Join(dir, "dead")
	if err := os.MkdirAll(dead, 0o755); err != nil {
		t.Fatal(err)
	}
	// 0o000: exists and stat's fine, but cannot be read — like a dead mount.
	if err := os.Chmod(dead, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dead, 0o755) })

	if _, err := os.Stat(dead); err != nil {
		t.Fatal("la premessa del test non regge: stat deve riuscire")
	}
	if destAvailable(filepath.Join(dead, "backup")) {
		t.Fatal("una destinazione il cui genitore non è leggibile non è disponibile")
	}
	if sourceAvailable(dead) {
		t.Fatal("una sorgente non leggibile non è disponibile")
	}
}

// --- ricrea struttura ---------------------------------------------------------

// RecreateStructure is the trailing-slash rule: without the slash rsync copies
// the folder itself, so the source name must survive into the arguments.
func TestRsyncArgsRecreateStructureOmitsTrailingSlash(t *testing.T) {
	dir := t.TempDir()
	args := rsyncArgs(SyncOptions{RecreateStructure: true}, []string{dir}, "/dst")
	src := args[len(args)-2]
	if strings.HasSuffix(src, "/") {
		t.Fatalf("con RecreateStructure la sorgente non deve avere lo slash finale: %q", src)
	}
	if src != dir {
		t.Fatalf("sorgente = %q, attesa %q", src, dir)
	}
}

// A path the user typed (or an older profile stored) with a trailing slash must
// still recreate the folder, otherwise the switch would silently do nothing.
func TestRsyncArgsRecreateStructureStripsStoredSlash(t *testing.T) {
	args := rsyncArgs(SyncOptions{RecreateStructure: true}, []string{"/data/A/"}, "/dst")
	if src := args[len(args)-2]; src != "/data/A" {
		t.Fatalf("sorgente = %q, attesa /data/A", src)
	}
}

// "/" has no folder name to recreate; stripping it would produce an empty
// argument and rsync would read the current directory instead.
func TestRsyncArgsRecreateStructureLeavesRootAlone(t *testing.T) {
	args := rsyncArgs(SyncOptions{RecreateStructure: true}, []string{"/"}, "/dst")
	if src := args[len(args)-2]; src != "/" {
		t.Fatalf("sorgente = %q, attesa /", src)
	}
}

// End-to-end with real rsync: this is the behaviour the user asked for —
// copying /A into /B must produce /B/A/…, not /B/… .
func TestRecreateStructureEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync non disponibile")
	}

	root := t.TempDir()
	srcParent := filepath.Join(root, "origine")
	srcA := filepath.Join(srcParent, "A")
	if err := os.MkdirAll(filepath.Join(srcA, "sotto"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcA, "sotto", "file.txt"), []byte("dati"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "B")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}

	a := NewApp()
	if _, err := a.execRsync(context.Background(), SyncOptions{RecreateStructure: true}, []string{srcA}, dst); err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(dst, "A", "sotto", "file.txt")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("atteso %s, non trovato: %v", want, err)
	}
	// And the contents must NOT have been poured into /B directly.
	if _, err := os.Stat(filepath.Join(dst, "sotto")); err == nil {
		t.Fatal("il contenuto è finito direttamente in /B: la cartella contenitore non è stata ricreata")
	}
}

// The default (off) must keep behaving exactly as before: contents poured in.
func TestWithoutRecreateStructureContentsArePouredIn(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync non disponibile")
	}

	root := t.TempDir()
	srcA := filepath.Join(root, "A")
	if err := os.MkdirAll(srcA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcA, "file.txt"), []byte("dati"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "B")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}

	a := NewApp()
	if _, err := a.execRsync(context.Background(), SyncOptions{}, []string{srcA}, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "file.txt")); err != nil {
		t.Fatalf("senza RecreateStructure il contenuto deve finire in /B: %v", err)
	}
}

// --- ripristino ---------------------------------------------------------------

func TestRestorePlanRejectsAmbiguousProfiles(t *testing.T) {
	cases := []struct {
		name string
		p    SyncProfile
	}{
		{"due sorgenti", SyncProfile{Sources: []string{"/a", "/b"}, Destinations: []string{"/d"}}},
		{"due destinazioni", SyncProfile{Sources: []string{"/a"}, Destinations: []string{"/d", "/e"}}},
		{"nessuna sorgente", SyncProfile{Destinations: []string{"/d"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if plan := restorePlan(c.p); plan.Allowed {
				t.Fatal("il ripristino non deve essere permesso su un profilo ambiguo")
			}
		})
	}
}

// Without RecreateStructure the backup sits directly in the destination, and
// must go back into the original source folder.
func TestRestorePlanWithoutRecreateStructure(t *testing.T) {
	p := SyncProfile{
		Sources:      []string{"/home/me/A"},
		Destinations: []string{"/Volumes/Backup"},
	}
	plan := restorePlan(p)
	if !plan.Allowed {
		t.Fatalf("atteso permesso, motivo: %s", plan.Reason)
	}
	if plan.From != "/Volumes/Backup" {
		t.Errorf("From = %q, atteso /Volumes/Backup", plan.From)
	}
	if plan.To != "/home/me/A" {
		t.Errorf("To = %q, atteso /home/me/A", plan.To)
	}
}

// With RecreateStructure the backup is one level deeper, in <dest>/<nome>.
// Reading from the destination root instead would restore the wrong tree.
func TestRestorePlanWithRecreateStructure(t *testing.T) {
	p := SyncProfile{
		Sources:      []string{"/home/me/A"},
		Destinations: []string{"/Volumes/Backup"},
		Options:      SyncOptions{RecreateStructure: true},
	}
	plan := restorePlan(p)
	if !plan.Allowed {
		t.Fatalf("atteso permesso, motivo: %s", plan.Reason)
	}
	if plan.From != "/Volumes/Backup/A" {
		t.Errorf("From = %q, atteso /Volumes/Backup/A", plan.From)
	}
	if plan.To != "/home/me/A" {
		t.Errorf("To = %q, atteso /home/me/A", plan.To)
	}
}

func TestPathBaseHandlesRemoteAndTrailingSlash(t *testing.T) {
	cases := map[string]string{
		"/home/me/A":         "A",
		"/home/me/A/":        "A",
		"user@host:/data/A":  "A",
		"user@host:/data/A/": "A",
	}
	for in, want := range cases {
		if got := pathBase(in); got != want {
			t.Errorf("pathBase(%q) = %q, atteso %q", in, got, want)
		}
	}
}

// The dangerous case: restoring with --delete from a backup that is empty
// (wrong volume, or a destination never written) would wipe the original.
func TestRunRestoreRefusesEmptyBackupWithDelete(t *testing.T) {
	t.Setenv("RSYNCGUI_CONFIG_DIR", t.TempDir())

	root := t.TempDir()
	source := filepath.Join(root, "originale")
	backup := filepath.Join(root, "backup")
	for _, d := range []string{source, backup} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "prezioso.txt"), []byte("dati"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := NewApp()
	a.profiles = []SyncProfile{{
		ID: "x", Name: "test",
		Sources:      []string{source},
		Destinations: []string{backup},
	}}

	err := a.RunRestore("x", true)
	if err == nil {
		t.Fatal("un ripristino con eliminazione da un backup vuoto deve essere rifiutato")
	}
	if _, statErr := os.Stat(filepath.Join(source, "prezioso.txt")); statErr != nil {
		t.Fatal("il file originale non doveva essere toccato")
	}
}

// Same empty backup without --delete is harmless (nothing gets removed), so it
// must be allowed: refusing it would block a legitimate no-op.
func TestRunRestoreAllowsEmptyBackupWithoutDelete(t *testing.T) {
	t.Setenv("RSYNCGUI_CONFIG_DIR", t.TempDir())

	root := t.TempDir()
	source := filepath.Join(root, "originale")
	backup := filepath.Join(root, "backup")
	for _, d := range []string{source, backup} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	a := NewApp()
	a.profiles = []SyncProfile{{
		ID: "x", Name: "test",
		Sources:      []string{source},
		Destinations: []string{backup},
	}}

	if err := a.RunRestore("x", false); err != nil {
		t.Fatalf("un ripristino senza eliminazione non deve essere bloccato: %v", err)
	}
	_ = a.Abort()
}

// End-to-end round trip: copy A → B with structure recreation, lose a file in
// the original, restore it back. This is the whole feature working together.
func TestRestoreRoundTripWithRecreateStructure(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync non disponibile")
	}

	root := t.TempDir()
	source := filepath.Join(root, "A")
	backup := filepath.Join(root, "B")
	if err := os.MkdirAll(filepath.Join(source, "sotto"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(backup, 0o755); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(source, "sotto", "prezioso.txt")
	if err := os.WriteFile(original, []byte("contenuto originale"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := NewApp()
	p := SyncProfile{
		Name:         "test",
		Sources:      []string{source},
		Destinations: []string{backup},
		Options:      SyncOptions{RecreateStructure: true},
	}

	// forward copy
	if _, err := a.execRsync(context.Background(), p.Options, p.Sources, backup); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(backup, "A", "sotto", "prezioso.txt")); err != nil {
		t.Fatalf("il backup non contiene la struttura attesa: %v", err)
	}

	// the user loses the file
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}

	// restore, using exactly the paths the plan computes
	plan := restorePlan(p)
	if !plan.Allowed {
		t.Fatalf("ripristino non permesso: %s", plan.Reason)
	}
	restoreOpts := p.Options
	restoreOpts.RecreateStructure = false
	if _, err := a.execRsync(context.Background(), restoreOpts,
		[]string{strings.TrimRight(plan.From, "/") + "/"}, plan.To); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(original)
	if err != nil {
		t.Fatalf("il file non è stato ripristinato al percorso originale: %v", err)
	}
	if string(got) != "contenuto originale" {
		t.Fatalf("contenuto ripristinato = %q", got)
	}
}

// --- notifiche ----------------------------------------------------------------

func TestStatusNotification(t *testing.T) {
	if msg := statusNotification("success", "Backup", ""); !strings.Contains(msg, "Backup") || !strings.Contains(msg, "completata") {
		t.Errorf("notifica di successo inattesa: %q", msg)
	}
	if msg := statusNotification("partial", "Backup", "un file saltato"); !strings.Contains(msg, "Backup") {
		t.Errorf("notifica parziale inattesa: %q", msg)
	}
	if msg := statusNotification("failed", "Backup", "destinazione irraggiungibile"); !strings.Contains(msg, "destinazione irraggiungibile") {
		t.Errorf("la notifica di errore deve riportare il motivo: %q", msg)
	}
	if msg := statusNotification("failed", "Backup", ""); !strings.Contains(msg, "fallita") {
		t.Errorf("notifica di errore senza dettaglio inattesa: %q", msg)
	}
	// An abort is the user's own doing: no notification.
	if msg := statusNotification("aborted", "Backup", ""); msg != "" {
		t.Errorf("un'interruzione volontaria non deve notificare: %q", msg)
	}
	if msg := statusNotification("running", "Backup", ""); msg != "" {
		t.Errorf("uno stato intermedio non deve notificare: %q", msg)
	}
}

// A long rsync error must not be pasted whole into a notification body.
func TestTruncateKeepsNotificationsShort(t *testing.T) {
	long := strings.Repeat("x", 500)
	got := truncate(long, 120)
	if len([]rune(got)) != 120 {
		t.Fatalf("lunghezza = %d, attesa 120", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("un testo tagliato deve finire con i puntini di sospensione")
	}
	if short := truncate("breve", 120); short != "breve" {
		t.Errorf("un testo corto non va toccato: %q", short)
	}
}

// notify must be a no-op without a window rather than a panic: the runner calls
// it from a goroutine, and a nil context reaching Wails would take the app down.
func TestNotifyWithoutWindowIsSafe(t *testing.T) {
	a := NewApp()
	a.notifyForStatus("success", "Backup", "")
	a.notify("messaggio")
}
