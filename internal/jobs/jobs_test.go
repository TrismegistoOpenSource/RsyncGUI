package jobs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tempStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(filepath.Join(t.TempDir(), "jobs"))
}

func newState(id string) State {
	return State{JobID: id, Label: "prova", Status: StatusRunning, StartedAt: time.Now()}
}

// --- id e percorsi ------------------------------------------------------------

// Every file is derived from the job id, so an id that could contain path
// separators would let a tampered state file point the app — and its cleanup,
// which deletes files — anywhere on disk.
func TestPathsRejectDangerousJobIDs(t *testing.T) {
	bad := []string{
		"../../etc/passwd",
		"/etc/passwd",
		"..",
		"abc",                                // troppo corto
		"ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ",   // fuori dall'alfabeto esadecimale
		"0123456789abcdef0123456789abcdef/x", // separatore
		"",
	}
	for _, id := range bad {
		if ValidJobID(id) {
			t.Errorf("ValidJobID(%q) = true, doveva essere rifiutato", id)
		}
		if _, err := PathsFor("/tmp/jobs", id); err == nil {
			t.Errorf("PathsFor(%q) non ha dato errore", id)
		}
	}
}

func TestNewJobIDIsUniqueAndValid(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		id := NewJobID()
		if !ValidJobID(id) {
			t.Fatalf("id generato non valido: %q", id)
		}
		if seen[id] {
			t.Fatalf("id duplicato: %q", id)
		}
		seen[id] = true
	}
}

// --- stato --------------------------------------------------------------------

// The window polls these files while a job runs, so a half-written state must
// never be observable.
func TestWriteStateIsAtomic(t *testing.T) {
	s := tempStore(t)
	id := NewJobID()
	st := newState(id)
	st.Summary = strings.Repeat("x", 200_000) // abbastanza grande da non essere atomico per caso

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			if got, err := ReadState(s.Dir, id); err == nil {
				if got.JobID != id {
					t.Errorf("stato letto incoerente: %q", got.JobID)
					return
				}
			}
		}
	}()
	for i := 0; i < 50; i++ {
		if err := WriteState(s.Dir, st); err != nil {
			t.Fatal(err)
		}
	}
	<-done
}

// The id in the file must not be able to disagree with the file it lives in:
// every path is built from the id.
func TestReadStateTrustsTheFilenameNotTheContents(t *testing.T) {
	s := tempStore(t)
	realID := NewJobID()
	st := newState(realID)
	st.JobID = NewJobID() // un id diverso da quello del file
	if err := WriteStateAt(s.Dir, realID, st); err != nil {
		t.Fatal(err)
	}
	got, err := ReadState(s.Dir, realID)
	if err != nil {
		t.Fatal(err)
	}
	if got.JobID != realID {
		t.Fatalf("JobID = %q, doveva vincere il nome del file %q", got.JobID, realID)
	}
}

func TestAllDestinationsDeduplicates(t *testing.T) {
	st := State{Profiles: []ProfileRun{
		{Destinations: []string{"/a", "/b"}},
		{Destinations: []string{"/b", "/c"}},
	}}
	got := st.AllDestinations()
	if len(got) != 3 {
		t.Fatalf("destinazioni = %v, attese 3 distinte", got)
	}
}

// --- lock ---------------------------------------------------------------------

func TestLockIsExclusiveAndReleasable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")

	l1, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if !IsHeld(path) {
		t.Fatal("IsHeld deve vedere il lock preso")
	}
	if err := l1.Unlock(); err != nil {
		t.Fatal(err)
	}
	if IsHeld(path) {
		t.Fatal("dopo Unlock il lock deve risultare libero")
	}
	l2, err := TryLock(path)
	if err != nil {
		t.Fatalf("il lock rilasciato deve essere riprendibile: %v", err)
	}
	l2.Unlock()
}

// A job whose supervisor died leaves a state saying "running" and a free lock.
// Reporting it as still running for ever would be worse than admitting the
// outcome is unknown.
func TestListMarksDeadRunningJobsAsOrphaned(t *testing.T) {
	s := tempStore(t)
	id := NewJobID()
	if err := WriteState(s.Dir, newState(id)); err != nil {
		t.Fatal(err)
	}

	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("job elencati = %d, atteso 1", len(list))
	}
	if list[0].Status != StatusOrphaned {
		t.Fatalf("stato = %q, atteso %q", list[0].Status, StatusOrphaned)
	}
	// e deve essere stato persistito, non solo calcolato
	again, _ := ReadState(s.Dir, id)
	if again.Status != StatusOrphaned {
		t.Fatal("lo stato orfano deve essere scritto su disco")
	}
}

func TestIsAliveFollowsTheLockNotThePID(t *testing.T) {
	s := tempStore(t)
	id := NewJobID()
	st := newState(id)
	// Un pid che esiste di sicuro (questo processo) ma che non è il job:
	// è esattamente il caso del pid riciclato.
	st.PID = os.Getpid()
	if err := WriteState(s.Dir, st); err != nil {
		t.Fatal(err)
	}
	if s.IsAlive(id) {
		t.Fatal("senza lock il job non è vivo, per quanto il pid esista")
	}

	p, _ := s.Paths(id)
	l, err := TryLock(p.Lock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Unlock()
	if !s.IsAlive(id) {
		t.Fatal("col lock preso il job deve risultare vivo")
	}
}

// --- ritenzione ---------------------------------------------------------------

func writeFinished(t *testing.T, s *Store, status string, finished time.Time, logBytes int) string {
	t.Helper()
	id := NewJobID()
	st := newState(id)
	st.Status = status
	st.FinishedAt = finished
	st.StartedAt = finished.Add(-time.Minute)
	if err := WriteState(s.Dir, st); err != nil {
		t.Fatal(err)
	}
	if logBytes > 0 {
		p, _ := s.Paths(id)
		if err := os.WriteFile(p.Log, make([]byte, logBytes), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func logExists(t *testing.T, s *Store, id string) bool {
	t.Helper()
	p, _ := s.Paths(id)
	_, err := os.Stat(p.Log)
	return err == nil
}

// A copy that went fine has nothing to tell: its megabytes of "file
// transferred" lines are exactly what fills the folder up.
func TestFinishLogDeletesLogOfSuccessfulRun(t *testing.T) {
	s := tempStore(t)
	for _, tc := range []struct {
		status string
		keep   bool
	}{
		{StatusSuccess, false},
		{StatusAborted, false},
		{StatusFailed, true},
		{StatusPartial, true},
		{StatusOrphaned, true},
	} {
		id := writeFinished(t, s, tc.status, time.Now(), 1024)
		st, _ := ReadState(s.Dir, id)
		if err := s.FinishLog(st); err != nil {
			t.Fatal(err)
		}
		if got := logExists(t, s, id); got != tc.keep {
			t.Errorf("stato %s: log presente = %v, atteso %v", tc.status, got, tc.keep)
		}
	}
}

func TestCleanupRemovesOldFailedLogsButKeepsRecentOnes(t *testing.T) {
	s := tempStore(t)
	old := writeFinished(t, s, StatusFailed, time.Now().Add(-FailedLogRetention-time.Hour), 1024)
	recent := writeFinished(t, s, StatusFailed, time.Now().Add(-time.Hour), 1024)

	if err := s.Cleanup(time.Now()); err != nil {
		t.Fatal(err)
	}
	if logExists(t, s, old) {
		t.Error("il log vecchio doveva essere cancellato")
	}
	if !logExists(t, s, recent) {
		t.Error("il log recente doveva essere conservato")
	}
}

func TestCleanupRemovesStatesPastRetention(t *testing.T) {
	s := tempStore(t)
	old := writeFinished(t, s, StatusFailed, time.Now().Add(-StateRetention-time.Hour), 0)
	if err := s.Cleanup(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadState(s.Dir, old); err == nil {
		t.Error("lo stato oltre la ritenzione doveva essere cancellato")
	}
}

func TestCleanupKeepsOnlyMostRecentJobs(t *testing.T) {
	s := tempStore(t)
	base := time.Now()
	for i := 0; i < MaxJobsKept+10; i++ {
		writeFinished(t, s, StatusFailed, base.Add(-time.Duration(i)*time.Minute), 0)
	}
	if err := s.Cleanup(time.Now()); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List()
	if len(list) > MaxJobsKept {
		t.Fatalf("job conservati = %d, massimo %d", len(list), MaxJobsKept)
	}
}

func TestCleanupEnforcesDirectorySizeCeiling(t *testing.T) {
	s := tempStore(t)
	base := time.Now()
	// Cinque log da 50 MiB: 250 MiB, oltre il tetto di 200 MiB.
	for i := 0; i < 5; i++ {
		writeFinished(t, s, StatusFailed, base.Add(-time.Duration(i)*time.Minute), 50<<20)
	}
	if err := s.Cleanup(time.Now()); err != nil {
		t.Fatal(err)
	}
	size, err := s.dirSize()
	if err != nil {
		t.Fatal(err)
	}
	if size > MaxDirBytes {
		t.Fatalf("cartella = %d byte, oltre il tetto di %d", size, MaxDirBytes)
	}
}

// The guard that matters most: deleting the log of a copy in progress would be
// the silliest possible way to break this feature.
func TestCleanupNeverTouchesALiveJob(t *testing.T) {
	s := tempStore(t)
	id := NewJobID()
	st := newState(id)
	st.StartedAt = time.Now().Add(-StateRetention - 24*time.Hour) // vecchissimo
	if err := WriteState(s.Dir, st); err != nil {
		t.Fatal(err)
	}
	p, _ := s.Paths(id)
	if err := os.WriteFile(p.Log, make([]byte, 300<<20), 0o644); err != nil {
		t.Fatal(err) // enorme: sfonda anche il tetto della cartella
	}

	lock, err := TryLock(p.Lock) // il job è vivo
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()

	if err := s.Cleanup(time.Now()); err != nil {
		t.Fatal(err)
	}
	if !logExists(t, s, id) {
		t.Fatal("il log di un job VIVO non deve mai essere cancellato")
	}
	if _, err := ReadState(s.Dir, id); err != nil {
		t.Fatal("lo stato di un job VIVO non deve mai essere cancellato")
	}
}

// --- log con tetto ------------------------------------------------------------

func TestLogWriterKeepsFileBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.log")
	w, err := NewLogWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	line := []byte(strings.Repeat("x", 199) + "\n")
	for written := 0; written < (logHeadBytes + logTailBytes + logSlackBytes + (8 << 20)); written += len(line) {
		if _, err := w.Write(line); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	budget := int64(logHeadBytes + logTailBytes + logSlackBytes + (1 << 20))
	if fi.Size() > budget {
		t.Fatalf("log = %d byte, oltre il budget di %d", fi.Size(), budget)
	}
	if !w.Truncated() {
		t.Fatal("il log troncato deve dichiararsi tale")
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "parte centrale del log omessa") {
		t.Fatal("il troncamento deve essere visibile nel log, non silenzioso")
	}
}

func TestOptionsRoundTripAsOpaqueJSON(t *testing.T) {
	type opts struct {
		Checksum bool `json:"checksum"`
	}
	raw, _ := json.Marshal(opts{Checksum: true})
	s := tempStore(t)
	id := NewJobID()
	st := newState(id)
	st.Profiles = []ProfileRun{{Name: "p", Options: raw}}
	if err := WriteState(s.Dir, st); err != nil {
		t.Fatal(err)
	}
	got, err := ReadState(s.Dir, id)
	if err != nil {
		t.Fatal(err)
	}
	var back opts
	if err := json.Unmarshal(got.Profiles[0].Options, &back); err != nil {
		t.Fatal(err)
	}
	if !back.Checksum {
		t.Fatal("le opzioni devono sopravvivere al giro su disco")
	}
}
