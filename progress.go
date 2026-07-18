package main

import (
	"bytes"
	"io"
	"regexp"
	"strconv"
	"sync"
)

// Progress is worked out from rsync's own --progress output, which ends each
// transferred file with something like:
//
//	200000 100%  126.48MB/s   00:00:00 (xfer#7, to-check=7/201)
//
// The counter in that tail is the only thing rsync offers that relates to the
// whole run rather than the file in hand. It is also the reason this is done
// by parsing rather than by counting files ourselves: rsync decides what it
// will actually transfer, and any count we made beforehand would disagree with
// it as soon as something was skipped for being up to date.
//
// --info=progress2 would be tidier, but it does not exist everywhere: macOS
// ships openrsync, which reports itself as "rsync version 2.6.9 compatible"
// and rejects the option. --progress works on both.
// GNU rsync writes "to-chk", openrsync writes "to-check": both spellings, not
// one with an optional suffix.
var progressTailRe = regexp.MustCompile(`to-ch(?:k|eck)=(\d+)/(\d+)`)

// progressLineRe matches a progress line itself, so it can be kept out of the
// log. Those lines are transient status, not events: GNU rsync rewrites them
// in place with a carriage return while a large file is going across, and
// keeping every revision would bury the log under its own progress meter.
var progressLineRe = regexp.MustCompile(`^\s*[\d,]+\s+\d+%\s`)

// ProgressState is what the window shows.
type ProgressState struct {
	// Percent is -1 while unknown. It is honest to say "no idea yet" for the
	// first moments rather than to show a made-up zero that then jumps.
	Percent    int `json:"percent"`
	FilesDone  int `json:"filesDone"`
	FilesTotal int `json:"filesTotal"`
}

func unknownProgress() ProgressState { return ProgressState{Percent: -1} }

// progressTracker turns the raw counters into a percentage.
//
// It has to cope with the two rsync families counting in opposite directions:
// GNU rsync's to-chk is how many files are *left* and falls towards zero,
// while openrsync's to-check is how many are *done* and climbs towards the
// total. Neither says which it is, so the direction is learned by watching the
// first two readings move. Until then the percentage stays unknown, which is
// better than showing one that turns out to be inverted.
type progressTracker struct {
	total     int
	first     int
	haveFirst bool
	direction int // 0 unknown, +1 counts up (done), -1 counts down (remaining)
	best      int // highest percentage seen, so the bar never walks backwards
	done      int
}

func newProgressTracker() *progressTracker { return &progressTracker{best: -1} }

// observe feeds one (counter, total) reading and reports the state.
func (p *progressTracker) observe(counter, total int) ProgressState {
	if total <= 0 {
		return p.state()
	}
	// With incremental recursion the total grows as the file list is built.
	if total > p.total {
		p.total = total
	}

	if !p.haveFirst {
		p.first, p.haveFirst = counter, true
		return p.state()
	}
	if p.direction == 0 {
		switch {
		case counter > p.first:
			p.direction = 1
		case counter < p.first:
			p.direction = -1
		default:
			return p.state() // ancora fermo: non si può dedurre nulla
		}
	}

	done := counter
	if p.direction < 0 {
		done = p.total - counter
	}
	if done < 0 {
		done = 0
	}
	if done > p.total {
		done = p.total
	}
	if done > p.done {
		p.done = done
	}

	pct := p.done * 100 / p.total
	if pct > 100 {
		pct = 100
	}
	if pct > p.best {
		p.best = pct
	}
	return p.state()
}

func (p *progressTracker) state() ProgressState {
	return ProgressState{Percent: p.best, FilesDone: p.done, FilesTotal: p.total}
}

// progressWriter sits between rsync and the job log. It passes ordinary output
// through untouched and swallows progress lines, calling onProgress when one
// carries a usable counter.
//
// Splitting has to happen on carriage returns as well as newlines: that is how
// rsync overwrites a progress line in place, and a reader that only knew about
// '\n' would treat an entire transfer as one enormous line.
type progressWriter struct {
	mu         sync.Mutex
	out        io.Writer
	buf        []byte
	tracker    *progressTracker
	onProgress func(ProgressState)
}

func newProgressWriter(out io.Writer, onProgress func(ProgressState)) *progressWriter {
	return &progressWriter{out: out, tracker: newProgressTracker(), onProgress: onProgress}
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	n := len(p)
	w.buf = append(w.buf, p...)

	for {
		i := bytes.IndexAny(w.buf, "\n\r")
		if i < 0 {
			break
		}
		line := w.buf[:i]
		sep := w.buf[i]
		w.buf = w.buf[i+1:]

		if w.handleLine(line) {
			continue // era una riga di progresso: non finisce nel log
		}
		if _, err := w.out.Write(append(append([]byte{}, line...), sep)); err != nil {
			return n, err
		}
	}

	// A partial progress line must not sit in the buffer waiting for a
	// terminator that will not come until the file is finished: read what it
	// says now, and leave it there in case more of it arrives.
	if len(w.buf) > 0 && progressLineRe.Match(w.buf) {
		w.parseCounters(w.buf)
	}
	return n, nil
}

// handleLine reports whether the line was progress output and has been dealt
// with.
func (w *progressWriter) handleLine(line []byte) bool {
	if !progressLineRe.Match(line) {
		return false
	}
	w.parseCounters(line)
	return true
}

func (w *progressWriter) parseCounters(line []byte) {
	m := progressTailRe.FindSubmatch(line)
	if m == nil {
		return // progresso di un singolo file, senza contatore complessivo
	}
	counter, err1 := strconv.Atoi(string(m[1]))
	total, err2 := strconv.Atoi(string(m[2]))
	if err1 != nil || err2 != nil {
		return
	}
	st := w.tracker.observe(counter, total)
	if w.onProgress != nil {
		w.onProgress(st)
	}
}

// Flush writes out whatever is left, so the last line is not lost when a
// transfer ends without a trailing newline.
func (w *progressWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) == 0 {
		return nil
	}
	line := w.buf
	w.buf = nil
	if progressLineRe.Match(line) {
		w.parseCounters(line)
		return nil
	}
	_, err := w.out.Write(line)
	return err
}
