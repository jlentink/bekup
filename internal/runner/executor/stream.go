package executor

import (
	"bytes"
	"errors"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// DefaultChunkBytes is the stdout chunk size handed to RunSpec.Stdout.
const DefaultChunkBytes = 256 << 10

// errFinished is returned by the writers once the run is being torn down.
var errFinished = errors.New("executor: run finished")

// chunkWriter collects stdout into chunks of at most size bytes and hands
// each to emit synchronously, so a slow consumer blocks the attach stream
// and, through Docker's pipe, the tool itself (backpressure). It never
// buffers more than one chunk.
type chunkWriter struct {
	buf      []byte
	emit     func([]byte) error
	finished *atomic.Bool
	total    int64
	err      error
}

func newChunkWriter(size int, emit func([]byte) error, finished *atomic.Bool) *chunkWriter {
	if size <= 0 || size > runnerpb.MaxChunkBytes {
		size = DefaultChunkBytes
	}
	return &chunkWriter{buf: make([]byte, 0, size), emit: emit, finished: finished}
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	n := 0
	for len(p) > 0 {
		k := min(cap(w.buf)-len(w.buf), len(p))
		w.buf = append(w.buf, p[:k]...)
		p = p[k:]
		n += k
		if len(w.buf) == cap(w.buf) {
			if err := w.flush(); err != nil {
				return n, err
			}
		}
	}
	return n, nil
}

// flush emits the buffered chunk.
func (w *chunkWriter) flush() error {
	if w.err != nil {
		return w.err
	}
	if len(w.buf) == 0 {
		return nil
	}
	if w.finished.Load() {
		w.err = errFinished
		return w.err
	}
	if err := w.emit(w.buf); err != nil {
		w.err = errors.Join(ErrOutput, err)
		return w.err
	}
	w.total += int64(len(w.buf))
	w.buf = w.buf[:0]
	return nil
}

// LogStats summarizes the stderr log of a run.
type LogStats struct {
	// Lines is the number of lines handed to RunSpec.Log.
	Lines int
	// Dropped counts lines beyond limits.max_log_lines.
	Dropped int
	// Truncated counts lines cut at limits.max_log_line_bytes.
	Truncated int
}

// lineLogger splits stderr into lines, bounds their length and number,
// removes control characters and invalid UTF-8, redacts the run's known
// secret values plus credential patterns, and hands each line to emit.
// Memory is bounded by maxLineBytes plus the longest secret.
type lineLogger struct {
	emit         func(string)
	reg          *logging.Registry
	maxLines     int
	maxLineBytes int
	keep         int // bytes kept of an over-long line before redaction
	line         []byte
	overflow     bool
	stats        LogStats
	// finished stops emitting once the run is torn down (nil = never).
	finished *atomic.Bool
}

func newLineLogger(emit func(string), reg *logging.Registry, maxLines, maxLineBytes, longestSecret int) *lineLogger {
	return &lineLogger{
		emit:         emit,
		reg:          reg,
		maxLines:     maxLines,
		maxLineBytes: maxLineBytes,
		keep:         maxLineBytes + longestSecret,
		line:         make([]byte, 0, min(maxLineBytes+longestSecret, 64<<10)),
	}
}

func (l *lineLogger) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		part := p
		if i >= 0 {
			part = p[:i]
		}
		room := l.keep - len(l.line)
		if len(part) > room {
			l.line = append(l.line, part[:max(room, 0)]...)
			l.overflow = true
		} else {
			l.line = append(l.line, part...)
		}
		if i < 0 {
			break
		}
		l.finishLine()
		p = p[i+1:]
	}
	return n, nil
}

// Flush emits a final line without a newline.
func (l *lineLogger) Flush() {
	if len(l.line) > 0 || l.overflow {
		l.finishLine()
	}
}

func (l *lineLogger) finishLine() {
	defer func() {
		clear(l.line)
		l.line = l.line[:0]
		l.overflow = false
	}()
	if l.stats.Lines >= l.maxLines {
		l.stats.Dropped++
		return
	}
	s := sanitizeLine(l.line)
	s = logging.RedactCredentials(logging.DefaultRegistry.Scrub(l.reg.Scrub(s)))
	truncated := l.overflow
	if len(s) > l.maxLineBytes {
		s = s[:l.maxLineBytes]
		for !utf8.ValidString(s) && s != "" {
			s = s[:len(s)-1]
		}
		truncated = true
	}
	if truncated {
		l.stats.Truncated++
		s += " [truncated]"
	}
	l.stats.Lines++
	if l.emit != nil && (l.finished == nil || !l.finished.Load()) {
		l.emit(s)
	}
}

// sanitizeLine drops a trailing CR, replaces invalid UTF-8 and control
// characters (except tab) with U+FFFD / '?', so log lines cannot forge
// terminal escapes or extra lines in the UI.
func sanitizeLine(b []byte) string {
	b = bytes.TrimSuffix(b, []byte{'\r'})
	s := strings.ToValidUTF8(string(b), "\uFFFD")
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return r
		}
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return '?'
		}
		return r
	}, s)
}
