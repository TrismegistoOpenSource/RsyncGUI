package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rsyncgui/internal/jobs"
)

// buildSelf compiles the app so the test can launch it as a real, separate
// process. Nothing about detaching can be proven inside one process: the whole
// claim is about what happens when a process dies.
func buildSelf(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "rsyncgui-test")
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go non disponibile")
	}
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fallita: %v\n%s", err, out)
	}
	return bin
}

// makeTree creates a source tree big enough that the copy is still going when
// the parent is killed.
func makeTree(t *testing.T, root string, dirs, files int) {
	t.Helper()
	for d := 0; d < dirs; d++ {
		dir := filepath.Join(root, "d"+string(rune('a'+d%26))+strings.Repeat("x", d/26))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for f := 0; f < files; f++ {
			p := filepath.Join(dir, "f"+strings.Repeat("y", f%5)+string(rune('0'+f%10))+"_"+itoa(f))
			if err := os.WriteFile(p, make([]byte, 4096), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// This is the test the whole version exists for: a job started by the window
// must finish even if the window is violently killed.
//
// In 2.2 it did not — rsync's output went through a pipe to the GUI, and when
// the GUI died the read end closed and rsync died on its next write. Here the
// supervisor owns the output, so the only thing lost is the window.
func TestJobSurvivesWindowBeingKilled(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync non disponibile")
	}
	bin := buildSelf(t)

	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	dir := filepath.Join(root, "cfg", "jobs")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	makeTree(t, src, 12, 300) // ~3600 file: la copia dura abbastanza

	// Lo stato che la finestra avrebbe scritto prima di lanciare il supervisore.
	id := jobs.NewJobID()
	st := jobs.State{
		JobID: id, Label: "test", Status: jobs.StatusRunning, StartedAt: time.Now(),
		Profiles: []jobs.ProfileRun{{
			ID: "p1", Name: "test", Sources: []string{src}, Destinations: []string{dst},
			Options: []byte(`{"verbose":true}`),
		}},
	}
	if err := jobs.WriteState(dir, st); err != nil {
		t.Fatal(err)
	}

	// A stand-in for the window: a process that starts the supervisor and then
	// does nothing. The test kills *this*, not the supervisor — killing the
	// supervisor would prove nothing.
	parent := exec.Command("/bin/sh", "-c",
		exec.Command(bin).Path+" "+superviseFlag+" "+dir+" "+id+" & sleep 600")
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}

	store := jobs.NewStore(dir)
	waitUntil(t, 15*time.Second, func() bool { return store.IsAlive(id) },
		"il supervisore non ha preso il lock")

	// LA FINESTRA MUORE, di morte violenta: nessuna possibilità di ripulire,
	// nessun segnale gestito. È il caso del crash.
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = parent.Wait()
	waitUntil(t, 10*time.Second, func() bool {
		return syscallKill(parent.Process.Pid, 0) != nil
	}, "il processo padre non è morto")

	// Da qui in poi nessuno tiene più in vita il job: se finisce, è perché il
	// supervisore è indipendente davvero.

	waitUntil(t, 120*time.Second, func() bool {
		s, err := jobs.ReadState(dir, id)
		return err == nil && !s.Running()
	}, "il job non è terminato")

	final, err := jobs.ReadState(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != jobs.StatusSuccess {
		t.Fatalf("stato finale = %q (%s), atteso success", final.Status, final.Summary)
	}
	if final.FinishedAt.IsZero() {
		t.Fatal("l'orario di fine deve essere registrato")
	}
	if final.Summary == "" {
		t.Fatal("il riepilogo deve sopravvivere alla cancellazione del log")
	}

	// I file devono esserci davvero: lo stato potrebbe mentire.
	var copied int
	filepath.Walk(dst, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			copied++
		}
		return nil
	})
	if copied != 12*300 {
		t.Fatalf("file copiati = %d, attesi %d", copied, 12*300)
	}

	// Il log di un job riuscito resta consultabile ma ridotto alla coda: la
	// ritenzione serve a non riempire il disco, non a cancellare le prove.
	p, _ := store.Paths(id)
	fi, err := os.Stat(p.Log)
	if err != nil {
		t.Fatalf("il log di un job riuscito deve restare leggibile: %v", err)
	}
	if fi.Size() > jobs.SuccessLogTail+2048 {
		t.Errorf("log = %d byte, doveva essere ridotto a ~%d", fi.Size(), jobs.SuccessLogTail)
	}

	// Il lock globale dev'essere stato rilasciato, o niente ripartirebbe più.
	if jobs.IsHeld(store.RunningLockPath()) {
		t.Error("il lock globale è rimasto preso dopo la fine del job")
	}
}

// Two supervisors must not run at once: that is the one-job-at-a-time
// invariant, which the in-memory flag of 2.2 can no longer protect once jobs
// outlive the window.
func TestSecondJobIsRefusedWhileOneIsRunning(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync non disponibile")
	}
	bin := buildSelf(t)

	root := t.TempDir()
	src := filepath.Join(root, "src")
	dir := filepath.Join(root, "cfg", "jobs")
	makeTree(t, src, 8, 300)

	start := func(dst string) string {
		id := jobs.NewJobID()
		if err := os.MkdirAll(dst, 0o755); err != nil {
			t.Fatal(err)
		}
		st := jobs.State{
			JobID: id, Label: "t", Status: jobs.StatusRunning, StartedAt: time.Now(),
			Profiles: []jobs.ProfileRun{{
				Name: "t", Sources: []string{src}, Destinations: []string{dst},
				Options: []byte(`{}`),
			}},
		}
		if err := jobs.WriteState(dir, st); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(bin, superviseFlag, dir, id)
		detach(cmd)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmd.Process.Release()
		return id
	}

	store := jobs.NewStore(dir)
	first := start(filepath.Join(root, "dst1"))
	waitUntil(t, 10*time.Second, func() bool { return store.IsAlive(first) },
		"il primo job non è partito")

	second := start(filepath.Join(root, "dst2"))
	waitUntil(t, 20*time.Second, func() bool {
		s, err := jobs.ReadState(dir, second)
		return err == nil && !s.Running()
	}, "il secondo job non si è fermato")

	s2, _ := jobs.ReadState(dir, second)
	if s2.Status != jobs.StatusFailed {
		t.Fatalf("il secondo job doveva essere rifiutato, stato = %q", s2.Status)
	}
	if !strings.Contains(s2.Summary, "già in corso") {
		t.Fatalf("il motivo del rifiuto deve essere chiaro: %q", s2.Summary)
	}

	// Il primo deve essere rimasto indisturbato.
	if s1, _ := jobs.ReadState(dir, first); s1.Status != jobs.StatusRunning && s1.Status != jobs.StatusSuccess {
		t.Fatalf("il primo job è stato disturbato: %q", s1.Status)
	}
	waitUntil(t, 120*time.Second, func() bool {
		s, err := jobs.ReadState(dir, first)
		return err == nil && !s.Running()
	}, "il primo job non è terminato")
}

func waitUntil(t *testing.T, limit time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout: %s", msg)
}

// detach() is the other half of surviving: a session of its own means signals
// aimed at the window's process group never reach the supervisor. The
// integration test above cannot see this directly, so it is checked here.
func TestDetachPutsSupervisorInItsOwnSession(t *testing.T) {
	cmd := exec.Command("/bin/true")
	detach(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("detach non ha impostato SysProcAttr")
	}
	if !cmd.SysProcAttr.Setsid {
		t.Fatal("il supervisore deve stare in una sessione propria (Setsid)")
	}
}

// The window must never hand the supervisor a pipe: that is precisely what
// killed transfers in 2.2, when rsync died on its first write after the GUI
// went away and closed the read end.
func TestDetachedSupervisorGetsNoPipes(t *testing.T) {
	cmd := exec.Command("/bin/true")
	cmd.Stdout, cmd.Stderr, cmd.Stdin = nil, nil, nil
	detach(cmd)
	if cmd.Stdout != nil || cmd.Stderr != nil || cmd.Stdin != nil {
		t.Fatal("il supervisore non deve ereditare pipe dalla finestra")
	}
}

// Stopping a detached job goes through a stop FILE, not a signal: on Windows a
// DETACHED_PROCESS has no console for a Ctrl event to reach, and a file cannot
// be delivered to a recycled pid by mistake. This drives a real supervisor and
// stops it mid-copy.
func TestStopFileAbortsARunningJob(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync non disponibile")
	}
	bin := buildSelf(t)

	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	dir := filepath.Join(root, "cfg", "jobs")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	makeTree(t, src, 20, 400) // 8000 file: abbastanza da essere ancora in corsa

	id := jobs.NewJobID()
	st := jobs.State{
		JobID: id, Label: "stop-test", Status: jobs.StatusRunning, StartedAt: time.Now(),
		Profiles: []jobs.ProfileRun{{
			Name: "stop-test", Sources: []string{src}, Destinations: []string{dst},
			Options: []byte(`{"verbose":true}`),
		}},
	}
	if err := jobs.WriteState(dir, st); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, superviseFlag, dir, id)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	cmd.Process.Release()

	store := jobs.NewStore(dir)
	waitUntil(t, 15*time.Second, func() bool { return store.IsAlive(id) },
		"il supervisore non è partito")

	// La finestra chiede lo stop scrivendo il file — nessun segnale, come su
	// Windows.
	p, _ := store.Paths(id)
	if err := os.WriteFile(p.Stop, []byte("stop"), 0o644); err != nil {
		t.Fatal(err)
	}

	waitUntil(t, 30*time.Second, func() bool {
		s, err := jobs.ReadState(dir, id)
		return err == nil && !s.Running()
	}, "il job non si è fermato dopo la richiesta")

	final, _ := jobs.ReadState(dir, id)
	if final.Status != jobs.StatusAborted {
		t.Fatalf("stato = %q, atteso aborted (summary: %s)", final.Status, final.Summary)
	}
	if _, err := os.Stat(p.Stop); err == nil {
		t.Error("il file di stop andava rimosso una volta onorato")
	}
	if jobs.IsHeld(store.RunningLockPath()) {
		t.Error("il lock globale è rimasto preso")
	}
}

// Folder verification shares the one-at-a-time gate with detached jobs: with
// the global lock held (a copy in progress), a verify must be refused — and
// the in-memory flag alone cannot know that.
func TestVerifyRefusedWhileDetachedJobRuns(t *testing.T) {
	t.Setenv("RSYNCGUI_CONFIG_DIR", t.TempDir())
	a := NewApp()

	store, err := jobStore()
	if err != nil {
		t.Fatal(err)
	}
	gate, err := jobs.TryLock(store.RunningLockPath())
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Unlock()

	if err := a.VerifyFolder(t.TempDir()); err == nil {
		t.Fatal("la verifica doveva essere rifiutata con una copia in corso")
	}
}

// And the other direction: a window busy verifying must refuse to launch a
// detached job on top.
func TestStartDetachedRefusedWhileWindowBusy(t *testing.T) {
	t.Setenv("RSYNCGUI_CONFIG_DIR", t.TempDir())
	a := NewApp()
	a.busy = true // una verifica in corso

	_, err := a.startDetached("x", []SyncProfile{{Name: "x", Sources: []string{"/a"}, Destinations: []string{"/b"}}})
	if err == nil {
		t.Fatal("con la finestra occupata non deve partire un job staccato")
	}
}
