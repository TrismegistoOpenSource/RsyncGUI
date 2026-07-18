package jobs

import (
	"bytes"
	"os"
	"sync"
)

// How large a single job's log may get. A verbose rsync over millions of files
// can write gigabytes, and a backup tool that fills the disk it is backing up
// to has defeated its own purpose.
//
// The head is kept because it holds the start of the transfer and the options
// it ran with; the tail because that is where a failure shows up. What gets
// dropped is the middle, which is thousands of identical "file transferred"
// lines and is never what anyone reads.
const (
	logHeadBytes = 2 << 20  // 2 MiB kept from the beginning
	logTailBytes = 14 << 20 // 14 MiB kept from the end
	// Compaction happens when the file grows this far past the budget, so the
	// rewrite is occasional rather than on every write.
	logSlackBytes = 4 << 20
)

// LogWriter appends a job's output to its log file, keeping the file bounded.
//
// It is deliberately used as the supervisor's cmd.Stdout, which does put a
// pipe between rsync and the supervisor. That pipe is harmless: the supervisor
// is the process that outlives the window, so nothing closes the read end
// early. The pipe that used to kill transfers was the one between rsync and
// the *GUI*.
type LogWriter struct {
	mu        sync.Mutex
	file      *os.File
	written   int64
	truncated bool
}

// NewLogWriter opens (or creates) a job's log for appending.
func NewLogWriter(path string) (*LogWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	size := int64(0)
	if fi, statErr := f.Stat(); statErr == nil {
		size = fi.Size()
	}
	return &LogWriter{file: f, written: size}, nil
}

func (w *LogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	n, err := w.file.Write(p)
	w.written += int64(n)
	if err != nil {
		return n, err
	}
	if w.written > logHeadBytes+logTailBytes+logSlackBytes {
		if cerr := w.compact(); cerr != nil {
			return n, cerr
		}
	}
	return n, nil
}

// Truncated reports whether the middle of the log has been dropped, so the
// window can say so instead of letting someone puzzle over a gap.
func (w *LogWriter) Truncated() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.truncated
}

func (w *LogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// compact rewrites the file as head + marker + tail. Called rarely (once per
// logSlackBytes of new output), so the cost of reading the file back is paid
// seldom and stays proportional to the budget, never to the total output.
func (w *LogWriter) compact() error {
	name := w.file.Name()
	if err := w.file.Close(); err != nil {
		return err
	}
	w.file = nil

	data, err := os.ReadFile(name)
	if err != nil {
		return err
	}

	head := data[:min(logHeadBytes, len(data))]
	tail := []byte{}
	if len(data) > logTailBytes {
		tail = data[len(data)-logTailBytes:]
		// Start the tail at a line boundary, so the log does not resume in the
		// middle of a filename.
		if i := bytes.IndexByte(tail, '\n'); i >= 0 && i+1 < len(tail) {
			tail = tail[i+1:]
		}
	}

	var buf bytes.Buffer
	buf.Write(head)
	buf.WriteString("\n⋯ parte centrale del log omessa per non riempire il disco ⋯\n\n")
	buf.Write(tail)

	tmp := name + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, name); err != nil {
		return err
	}

	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	w.file = f
	w.written = int64(buf.Len())
	w.truncated = true
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
