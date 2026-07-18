package main

import (
	"bytes"
	"strings"
	"testing"
)

// openrsync (macOS) counts upwards: to-check goes 1/201 … 200/201.
// Captured from the real thing, not invented.
func TestProgressOpenrsyncCountsUp(t *testing.T) {
	var last ProgressState
	w := newProgressWriter(&bytes.Buffer{}, func(s ProgressState) { last = s })

	w.Write([]byte("f1.bin\n         200000 100%  126.48MB/s   00:00:00 (xfer#1, to-check=1/200)\n"))
	if last.Percent != -1 {
		t.Fatalf("con una sola lettura la direzione non è deducibile: percent = %d", last.Percent)
	}

	w.Write([]byte("f2.bin\n         200000 100%  115.87MB/s   00:00:00 (xfer#2, to-check=2/200)\n"))
	if last.Percent != 1 {
		t.Fatalf("percent = %d, atteso 1 (2 di 200)", last.Percent)
	}

	w.Write([]byte("         200000 100%  115.87MB/s   00:00:00 (xfer#100, to-check=100/200)\n"))
	if last.Percent != 50 {
		t.Fatalf("percent = %d, atteso 50", last.Percent)
	}
	if last.FilesDone != 100 || last.FilesTotal != 200 {
		t.Fatalf("file = %d/%d, attesi 100/200", last.FilesDone, last.FilesTotal)
	}
}

// GNU rsync counts the other way: to-chk is what is LEFT and falls to zero.
// Read the same way as openrsync it would show a backwards bar.
func TestProgressGnuRsyncCountsDown(t *testing.T) {
	var last ProgressState
	w := newProgressWriter(&bytes.Buffer{}, func(s ProgressState) { last = s })

	w.Write([]byte("     32,768 100%   1.02MB/s    0:00:00 (xfr#1, to-chk=199/200)\n"))
	w.Write([]byte("     32,768 100%   1.02MB/s    0:00:00 (xfr#2, to-chk=198/200)\n"))
	if last.Percent != 1 {
		t.Fatalf("percent = %d, atteso 1 (2 fatti su 200)", last.Percent)
	}

	w.Write([]byte("     32,768 100%   1.02MB/s    0:00:00 (xfr#150, to-chk=50/200)\n"))
	if last.Percent != 75 {
		t.Fatalf("percent = %d, atteso 75 (150 su 200)", last.Percent)
	}
}

// The bar must never walk backwards: rsync's counters can wobble, and a
// progress bar that goes down is worse than one that pauses.
func TestProgressNeverGoesBackwards(t *testing.T) {
	var last ProgressState
	w := newProgressWriter(&bytes.Buffer{}, func(s ProgressState) { last = s })

	w.Write([]byte("  1 100% 1MB/s 0:00:00 (xfer#1, to-check=10/100)\n"))
	w.Write([]byte("  1 100% 1MB/s 0:00:00 (xfer#2, to-check=60/100)\n"))
	if last.Percent != 60 {
		t.Fatalf("percent = %d, atteso 60", last.Percent)
	}
	w.Write([]byte("  1 100% 1MB/s 0:00:00 (xfer#3, to-check=40/100)\n"))
	if last.Percent != 60 {
		t.Fatalf("percent = %d: non deve tornare indietro", last.Percent)
	}
}

// Progress lines are status, not events. GNU rsync rewrites them in place with
// a carriage return while a big file goes across; logging every revision would
// bury the log under its own progress meter.
func TestProgressLinesAreKeptOutOfTheLog(t *testing.T) {
	var log bytes.Buffer
	w := newProgressWriter(&log, nil)

	w.Write([]byte("cartella/file.txt\n"))
	w.Write([]byte("     1,024   5%   1.00MB/s    0:00:10\r"))
	w.Write([]byte("    10,240  50%   1.00MB/s    0:00:05\r"))
	w.Write([]byte("    20,480 100%   1.00MB/s    0:00:00 (xfr#1, to-chk=9/10)\n"))
	w.Write([]byte("altro/file.txt\n"))
	w.Flush()

	got := log.String()
	if strings.Contains(got, "%") {
		t.Fatalf("il log non deve contenere righe di progresso:\n%s", got)
	}
	for _, want := range []string{"cartella/file.txt", "altro/file.txt"} {
		if !strings.Contains(got, want) {
			t.Errorf("il log deve conservare le righe normali, manca %q in:\n%s", want, got)
		}
	}
}

// A transfer of one large file emits progress separated by carriage returns
// alone. Splitting only on '\n' would treat the whole thing as one line and
// report nothing until the end.
func TestProgressUpdatesDuringASingleLargeFile(t *testing.T) {
	var seen int
	w := newProgressWriter(&bytes.Buffer{}, func(ProgressState) { seen++ })

	w.Write([]byte("  1,000  1% 1MB/s 0:01:00 (xfr#1, to-chk=1/2)\r"))
	w.Write([]byte("  5,000  5% 1MB/s 0:00:50 (xfr#1, to-chk=1/2)\r"))
	w.Write([]byte(" 10,000 10% 1MB/s 0:00:40 (xfr#1, to-chk=1/2)\r"))
	if seen < 3 {
		t.Fatalf("aggiornamenti visti = %d, attesi almeno 3 anche senza newline", seen)
	}
}

// An incremental run with nothing to copy emits no progress at all — verified
// against the real openrsync. The bar must simply stay unknown rather than
// invent a value.
func TestProgressStaysUnknownWithoutOutput(t *testing.T) {
	var last = ProgressState{Percent: 42}
	w := newProgressWriter(&bytes.Buffer{}, func(s ProgressState) { last = s })
	w.Write([]byte("cartella/\nfile invariato\n"))
	w.Flush()
	if last.Percent != 42 {
		t.Fatalf("senza righe di progresso non si deve emettere nulla, percent = %d", last.Percent)
	}
}

// Ordinary output must survive byte-for-byte: this writer sits in front of the
// log, and mangling it would be a poor trade for a progress bar.
func TestProgressWriterPassesNormalOutputThrough(t *testing.T) {
	var log bytes.Buffer
	w := newProgressWriter(&log, nil)
	in := "sending incremental file list\ndir/\ndir/a.txt\ndir/b.txt\n"
	w.Write([]byte(in))
	w.Flush()
	if log.String() != in {
		t.Fatalf("output alterato:\n%q\nattesor:\n%q", log.String(), in)
	}
}

// Split writes must not lose or duplicate anything: rsync's output arrives in
// whatever chunks the pipe hands over, not in tidy lines.
func TestProgressWriterHandlesSplitWrites(t *testing.T) {
	var log bytes.Buffer
	w := newProgressWriter(&log, nil)
	for _, part := range []string{"pri", "ma/riga", ".txt\nsecon", "da/riga.txt\n"} {
		w.Write([]byte(part))
	}
	w.Flush()
	want := "prima/riga.txt\nseconda/riga.txt\n"
	if log.String() != want {
		t.Fatalf("ricomposizione errata: %q, atteso %q", log.String(), want)
	}
}
