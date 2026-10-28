package runstate

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Log levels and streams (the run_log_level / run_log_stream enums).
const (
	LevelInfo  = sqlc.RunLogLevelInfo
	LevelWarn  = sqlc.RunLogLevelWarn
	LevelError = sqlc.RunLogLevelError

	StreamWorker = sqlc.RunLogStreamWorker
	StreamRunner = sqlc.RunLogStreamRunner
	StreamStdout = sqlc.RunLogStreamStdout
	StreamStderr = sqlc.RunLogStreamStderr
)

// Defaults and bounds of the log writer.
const (
	DefaultLogFlushInterval = 500 * time.Millisecond
	DefaultLogBatchSize     = 100
	DefaultLogQueueSize     = 1000
	// DefaultLogLineBytes is the line cap when none is configured.
	DefaultLogLineBytes = 4096
	// HardMaxLineBytes is the column limit; MaxLineBytes is clamped to it.
	HardMaxLineBytes = 16384
	// MinLineBytes is the smallest accepted line cap.
	MinLineBytes = 128
	// appendTimeout bounds one batch insert.
	appendTimeout = 10 * time.Second
	truncSuffix   = " …[truncated]"
)

// LogLine is one run-log line.
type LogLine struct {
	Time   time.Time
	Level  sqlc.RunLogLevel
	Stream sqlc.RunLogStream
	Text   string
}

// AppendLogs inserts a batch of (already sanitized) lines and publishes a
// log event.
func (s *Store) AppendLogs(ctx context.Context, r Ref, lines []LogLine) error {
	if len(lines) == 0 {
		return nil
	}
	p := sqlc.InsertRunLogsParams{RunID: r.RunID}
	for _, l := range lines {
		p.Ts = append(p.Ts, l.Time)
		p.Levels = append(p.Levels, string(l.Level))
		p.Streams = append(p.Streams, string(l.Stream))
		p.Lines = append(p.Lines, l.Text)
	}
	return db.Tx(ctx, s.db, func(q *sqlc.Queries) error {
		if err := q.InsertRunLogs(ctx, p); err != nil {
			return fmt.Errorf("runstate: insert run logs: %w", err)
		}
		return publish(ctx, s.pub, q, EventLog, r)
	})
}

// SanitizeLine makes s storable and harmless to display: invalid UTF-8 is
// replaced, NUL and other control characters (except tab) become spaces,
// and surrounding white space is trimmed.
func SanitizeLine(s string) string {
	s = strings.ToValidUTF8(s, "�")
	clean := true
	for _, r := range s {
		if r != '\t' && (unicode.IsControl(r) || r == '\u2028' || r == '\u2029') {
			clean = false
			break
		}
	}
	if !clean {
		s = strings.Map(func(r rune) rune {
			if r != '\t' && (unicode.IsControl(r) || r == '\u2028' || r == '\u2029') {
				return ' '
			}
			return r
		}, s)
	}
	return strings.TrimSpace(s)
}

// truncateBytes cuts s to at most n bytes on a rune boundary, marking the
// cut.
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := max(n-len(truncSuffix), 0)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + truncSuffix
}

// LogWriterOptions configure StartLogWriter.
type LogWriterOptions struct {
	// Append stores a batch (Store.AppendLogs bound to the run).
	Append func(ctx context.Context, lines []LogLine) error
	// Scrub redacts known secret values and credentials from a line; it
	// runs before the line is truncated, so a cut can never expose part
	// of a secret.
	Scrub func(string) string
	// MaxLines caps the stored lines of the run; further lines are only
	// counted (one notice line is added at Close).
	MaxLines int
	// MaxLineBytes caps one line (MinLineBytes..HardMaxLineBytes).
	MaxLineBytes  int
	FlushInterval time.Duration
	BatchSize     int
	// QueueSize bounds the lines waiting for a flush; a full queue drops
	// (and counts) lines instead of blocking the dump.
	QueueSize int
	Now       func() time.Time
	// OnError is told about failed batch inserts (the lines are counted as
	// dropped; logging never fails a run).
	OnError func(error)
}

// LogWriter batches, redacts and caps the log lines of one run. Log never
// blocks; Close flushes.
type LogWriter struct {
	o       LogWriterOptions
	ch      chan LogLine
	done    chan struct{}
	mu      sync.Mutex
	closed  bool
	stored  int // accepted lines (count against MaxLines)
	dropped int // over the cap, queue overflow or failed inserts
}

// StartLogWriter starts the flush goroutine; ctx only carries values (the
// writer keeps writing after ctx is canceled, until Close).
func StartLogWriter(ctx context.Context, o LogWriterOptions) *LogWriter {
	if o.Scrub == nil {
		o.Scrub = func(s string) string { return s }
	}
	if o.MaxLines <= 0 {
		o.MaxLines = 10000
	}
	if o.MaxLineBytes <= 0 {
		o.MaxLineBytes = DefaultLogLineBytes
	}
	o.MaxLineBytes = min(max(o.MaxLineBytes, MinLineBytes), HardMaxLineBytes)
	if o.FlushInterval <= 0 {
		o.FlushInterval = DefaultLogFlushInterval
	}
	if o.BatchSize <= 0 {
		o.BatchSize = DefaultLogBatchSize
	}
	if o.QueueSize <= 0 {
		o.QueueSize = DefaultLogQueueSize
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.OnError == nil {
		o.OnError = func(error) {}
	}
	w := &LogWriter{o: o, ch: make(chan LogLine, o.QueueSize), done: make(chan struct{})}
	go w.loop(context.WithoutCancel(ctx))
	return w
}

// Log queues one line (sanitized, scrubbed, truncated). Lines over the cap
// or beyond a full queue are counted as dropped.
func (w *LogWriter) Log(level sqlc.RunLogLevel, stream sqlc.RunLogStream, text string) {
	text = truncateBytes(w.o.Scrub(SanitizeLine(text)), w.o.MaxLineBytes)
	if text == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	if w.stored >= w.o.MaxLines {
		w.dropped++
		return
	}
	select {
	case w.ch <- LogLine{Time: w.o.Now(), Level: level, Stream: stream, Text: text}:
		w.stored++
	default:
		w.dropped++
	}
}

// Logf is Log with a format (worker events; never format secrets).
func (w *LogWriter) Logf(level sqlc.RunLogLevel, format string, args ...any) {
	w.Log(level, StreamWorker, fmt.Sprintf(format, args...))
}

func (w *LogWriter) loop(base context.Context) {
	defer close(w.done)
	t := time.NewTicker(w.o.FlushInterval)
	defer t.Stop()
	batch := make([]LogLine, 0, w.o.BatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(base, appendTimeout)
		err := w.o.Append(ctx, batch)
		cancel()
		if err != nil {
			w.o.OnError(err)
			w.mu.Lock()
			w.dropped += len(batch)
			w.stored -= len(batch)
			w.mu.Unlock()
		}
		batch = batch[:0]
	}
	for {
		select {
		case l, ok := <-w.ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, l)
			if len(batch) >= w.o.BatchSize {
				flush()
			}
		case <-t.C:
			flush()
		}
	}
}

// Close flushes the queued lines, adds a notice when lines were dropped
// and returns the stored and dropped line counts. Later Log calls are
// ignored.
func (w *LogWriter) Close() (stored, dropped int) {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		<-w.done
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.stored, w.dropped
	}
	w.closed = true
	if w.dropped > 0 {
		// The notice bypasses the cap (one line) but not a full queue.
		select {
		case w.ch <- LogLine{Time: w.o.Now(), Level: LevelWarn, Stream: StreamWorker,
			Text: fmt.Sprintf("%d log lines were not stored (log limits: workers.run_log_max_lines / run_log_max_line_bytes)", w.dropped)}:
			w.stored++
		default:
		}
	}
	close(w.ch)
	w.mu.Unlock()
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stored, w.dropped
}
