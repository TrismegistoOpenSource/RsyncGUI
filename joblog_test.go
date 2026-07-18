package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rsyncgui/internal/jobs"
)

// writeJobWithLog prepares a job whose log is n bytes of numbered lines.
func writeJobWithLog(t *testing.T, n int) (*App, string) {
	t.Helper()
	t.Setenv("RSYNCGUI_CONFIG_DIR", t.TempDir())

	dir, err := jobsDir()
	if err != nil {
		t.Fatal(err)
	}
	id := jobs.NewJobID()
	st := jobs.State{JobID: id, Label: "t", Status: jobs.StatusRunning, StartedAt: time.Now()}
	if err := jobs.WriteState(dir, st); err != nil {
		t.Fatal(err)
	}
	p, _ := jobs.PathsFor(dir, id)
	if err := os.MkdirAll(filepath.Dir(p.Log), 0o755); err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		b.WriteString("riga numero ")
		b.WriteString(strings.Repeat("x", 40))
		b.WriteString("\n")
	}
	if err := os.WriteFile(p.Log, []byte(b.String()[:n]), 0o644); err != nil {
		t.Fatal(err)
	}
	return NewApp(), id
}

// Attaching to a job that has already written a lot must NOT replay it from
// the beginning. Results of a bound method reach the webview on the platform's
// UI thread, so streaming megabytes stalls the window itself — which is what
// made the interface take seconds to respond to a drag or a click while
// following a job.
func TestReadJobLogStartsNearTheEndOfALongLog(t *testing.T) {
	a, id := writeJobWithLog(t, 4<<20) // 4 MiB

	chunk, err := a.ReadJobLog(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !chunk.Skipped {
		t.Fatal("agganciandosi a un log lungo il salto va dichiarato")
	}
	if len(chunk.Text) > logChunkBytes {
		t.Fatalf("blocco = %d byte, oltre il tetto di %d", len(chunk.Text), logChunkBytes)
	}
	if chunk.Offset < int64(4<<20)-int64(logTailWindow)-1 {
		t.Fatalf("offset = %d: la lettura doveva partire vicino alla fine del file", chunk.Offset)
	}
}

// A short log has no reason to be skipped: it is shown from the start.
func TestReadJobLogShowsShortLogFromTheBeginning(t *testing.T) {
	a, id := writeJobWithLog(t, 4<<10) // 4 KiB

	chunk, err := a.ReadJobLog(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	if chunk.Skipped {
		t.Fatal("un log corto non va saltato")
	}
	if !strings.HasPrefix(chunk.Text, "riga numero") {
		t.Fatalf("il log corto deve partire dall'inizio: %q", chunk.Text[:20])
	}
}

// No single read may be large, whatever is asked for: the bridge is the
// bottleneck, not the disk.
func TestReadJobLogNeverReturnsAHugeChunk(t *testing.T) {
	a, id := writeJobWithLog(t, 8<<20)

	var offset int64
	for i := 0; i < 20; i++ {
		chunk, err := a.ReadJobLog(id, offset)
		if err != nil {
			t.Fatal(err)
		}
		if len(chunk.Text) > logChunkBytes {
			t.Fatalf("blocco = %d byte, oltre il tetto di %d", len(chunk.Text), logChunkBytes)
		}
		if chunk.Offset == offset {
			break // fine del file
		}
		offset = chunk.Offset
	}
}

// Falling a long way behind a fast writer must resolve by jumping to the end,
// not by chasing it one chunk at a time for ever.
func TestReadJobLogSkipsAheadWhenFarBehind(t *testing.T) {
	a, id := writeJobWithLog(t, 4<<20)

	chunk, err := a.ReadJobLog(id, 0) // fa finta di essere rimasta indietro
	if err != nil {
		t.Fatal(err)
	}
	if !chunk.Skipped {
		t.Fatal("restando molto indietro si deve saltare alla fine")
	}
	// Ora è vicina alla fine: la lettura successiva non deve più saltare.
	next, err := a.ReadJobLog(id, chunk.Offset)
	if err != nil {
		t.Fatal(err)
	}
	if next.Skipped {
		t.Fatal("una volta raggiunta la fine non ci devono essere altri salti")
	}
}

// An offset past the end of the file means the log was compacted underneath
// us; reading from it would land past the end or mid-line.
func TestReadJobLogRecoversFromACompactedLog(t *testing.T) {
	a, id := writeJobWithLog(t, 8<<10)

	chunk, err := a.ReadJobLog(id, 900_000) // offset impossibile
	if err != nil {
		t.Fatal(err)
	}
	if !chunk.Skipped {
		t.Fatal("un offset oltre la fine deve essere segnalato come salto")
	}
	if chunk.Offset > 8<<10 {
		t.Fatalf("offset = %d, oltre la dimensione del file", chunk.Offset)
	}
}

// After a jump the text must begin at a line boundary: resuming halfway
// through a path is unreadable.
func TestReadJobLogResumesAtALineBoundary(t *testing.T) {
	a, id := writeJobWithLog(t, 4<<20)

	chunk, err := a.ReadJobLog(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(chunk.Text, "riga numero") {
		t.Fatalf("dopo un salto si deve ripartire da inizio riga, non da %q", chunk.Text[:30])
	}
}

// The log of a job that went fine is deleted on purpose; asking for it is a
// normal outcome, not an error.
func TestReadJobLogReportsAMissingLogWithoutFailing(t *testing.T) {
	a, id := writeJobWithLog(t, 1024)
	dir, _ := jobsDir()
	p, _ := jobs.PathsFor(dir, id)
	os.Remove(p.Log)

	chunk, err := a.ReadJobLog(id, 0)
	if err != nil {
		t.Fatalf("un log cancellato non è un errore: %v", err)
	}
	if !chunk.Missing {
		t.Fatal("va segnalato come mancante")
	}
}

// Paths come from the id, so a bogus id cannot be used to read another file.
func TestReadJobLogRejectsInvalidJobID(t *testing.T) {
	t.Setenv("RSYNCGUI_CONFIG_DIR", t.TempDir())
	a := NewApp()
	if _, err := a.ReadJobLog("../../etc/passwd", 0); err == nil {
		t.Fatal("un id non valido deve essere rifiutato")
	}
}
