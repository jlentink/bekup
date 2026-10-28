package runstate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/pubsub"
)

func TestPayloadRoundTrip(t *testing.T) {
	r := Ref{RunID: uuid.New(), JobID: uuid.New()}
	for _, kind := range []string{EventRun, EventLog} {
		p := Payload(kind, r)
		if err := pubsub.Validate(pubsub.RunEvents.Name, p); err != nil {
			t.Fatalf("payload %q: %v", p, err)
		}
		k, got, err := ParsePayload(p)
		if err != nil || k != kind || got != r {
			t.Fatalf("%q: %s %+v %v", p, k, got, err)
		}
	}
	for _, bad := range []string{"", "run:", "x:" + r.RunID.String() + ":" + r.JobID.String(),
		"run:" + r.RunID.String() + "-" + r.JobID.String(), "run:" + strings.Repeat("z", 36) + ":" + r.JobID.String()} {
		if _, _, err := ParsePayload(bad); !errors.Is(err, ErrBadPayload) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestSanitizeAndTruncate(t *testing.T) {
	if got := SanitizeLine("  a\x00b\x1b[31mc\r\n d\tok \xff "); got != "a b [31mc   d\tok �" {
		t.Fatalf("%q", got)
	}
	s := strings.Repeat("é", 100) // 200 bytes
	got := truncateBytes(s, 51)
	if len(got) > 51 || !utf8.ValidString(got) || !strings.HasSuffix(got, truncSuffix) {
		t.Fatalf("%q (%d)", got, len(got))
	}
	if truncateBytes("short", 10) != "short" {
		t.Fatal("short line changed")
	}
}

func TestWarningsJSONBounds(t *testing.T) {
	ws := make([]Warning, MaxWarnings+5)
	for i := range ws {
		ws[i] = Warning{Code: "Bad Code!", Message: strings.Repeat("m", 2*MaxWarningChars)}
	}
	b, err := warningsJSON(ws, RunWarningsBytes)
	if err != nil {
		t.Fatal(err)
	}
	var out []Warning
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != MaxWarnings+1 || out[0].Code != "warning" || utf8.RuneCountInString(out[0].Message) != MaxWarningChars ||
		out[MaxWarnings].Code != "warnings_truncated" {
		t.Fatalf("%d warnings, first %+v", len(out), out[0].Code)
	}
	code, msg := failureCols(&Failure{Code: "DROP TABLE", Message: "x\x00y"})
	if *code != "internal" || *msg != "x y" {
		t.Fatalf("%q %q", *code, *msg)
	}
}

// jsonbTextBound over-approximates the length of Postgres' jsonb text form
// of b (a space after every ':' and ',', counted also inside strings).
func jsonbTextBound(b []byte) int {
	return len(b) + strings.Count(string(b), ":") + strings.Count(string(b), ",")
}

// TestWarningsJSONByteBudget: long multibyte names and messages (4-byte
// runes, quotes and backslashes that encode to two bytes) never exceed the
// column CHECKs (runs 256 KiB, run_items 64 KiB) and always end with one
// warnings_truncated entry.
func TestWarningsJSONByteBudget(t *testing.T) {
	db := strings.Repeat("𝔡", 200)                     // 800 bytes, 200 runes
	msg := strings.Repeat("\\"+"𝔪\"", MaxWarningChars) // > MaxWarningBytes
	for _, tc := range []struct {
		budget, check, n int
	}{
		{RunWarningsBytes, 262144, 5000},
		{ItemWarningsBytes, 65536, 5000},
		{NoticesBytes, 65536, 300},
		{ItemWarningsBytes, 65536, 10},
	} {
		ws := make([]Warning, tc.n)
		for i := range ws {
			ws[i] = Warning{Code: "insufficient_privileges", Message: msg, Database: db, DestinationID: "0b0e7c63-4f2f-4a0a-9a51-3d9c7f1e2a10"}
		}
		b, err := warningsJSON(ws, tc.budget)
		if err != nil {
			t.Fatal(err)
		}
		if got := jsonbTextBound(b); got > tc.check || len(b) > tc.budget {
			t.Fatalf("budget %d: %d bytes (jsonb bound %d) exceed the check %d", tc.budget, len(b), got, tc.check)
		}
		var out []Warning
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		for _, w := range out[:len(out)-1] {
			if len(w.Message) > MaxWarningBytes || len(w.Database) > MaxWarningDatabaseBytes || !utf8.ValidString(w.Message) || !utf8.ValidString(w.Database) {
				t.Fatalf("entry not capped: %d / %d bytes", len(w.Message), len(w.Database))
			}
		}
		last := out[len(out)-1]
		truncated := last.Code == "warnings_truncated"
		if (truncated && len(out) > tc.n) || (!truncated && len(out) != tc.n) || (tc.n == 10 && truncated) || (tc.n > 10 && !truncated) {
			t.Fatalf("budget %d, %d in: %d out, last %q", tc.budget, tc.n, len(out), last.Code)
		}
		if truncated && strings.Count(string(b), "warnings_truncated") != 1 {
			t.Fatal("more than one warnings_truncated entry")
		}
	}
}

type captured struct {
	mu      sync.Mutex
	batches [][]LogLine
	fail    bool
}

func (c *captured) append(_ context.Context, lines []LogLine) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return errors.New("db down")
	}
	c.batches = append(c.batches, append([]LogLine(nil), lines...))
	return nil
}

func (c *captured) lines() []LogLine {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []LogLine
	for _, b := range c.batches {
		out = append(out, b...)
	}
	return out
}

func TestLogWriterBatchesRedactsAndCaps(t *testing.T) {
	c := &captured{}
	w := StartLogWriter(t.Context(), LogWriterOptions{
		Append: c.append, MaxLines: 150, MaxLineBytes: 128, BatchSize: 100, FlushInterval: time.Hour,
		Scrub: func(s string) string { return strings.ReplaceAll(s, "SECRETVALUE", "[REDACTED]") },
	})
	// The secret sits across the truncation point: scrubbed before cut.
	w.Log(LevelInfo, StreamStderr, strings.Repeat("a", 120)+"SECRETVALUE")
	for range 200 {
		w.Log(LevelInfo, StreamStdout, "line")
	}
	w.Log(LevelInfo, StreamStdout, "   ") // empty after sanitizing: ignored
	stored, dropped := w.Close()
	lines := c.lines()
	if stored != 151 || dropped != 51 || len(lines) != 151 {
		t.Fatalf("stored %d dropped %d lines %d", stored, dropped, len(lines))
	}
	if len(c.batches) < 2 || len(c.batches[0]) != 100 {
		t.Fatalf("batches %d (first %d)", len(c.batches), len(c.batches[0]))
	}
	if strings.Contains(lines[0].Text, "SECRET") || len(lines[0].Text) > 128 {
		t.Fatalf("first line %q", lines[0].Text)
	}
	if last := lines[len(lines)-1]; last.Level != LevelWarn || !strings.Contains(last.Text, "51 log lines were not stored") {
		t.Fatalf("notice %+v", last)
	}
	w.Log(LevelInfo, StreamStdout, "after close")
	if s2, d2 := w.Close(); s2 != stored || d2 != dropped {
		t.Fatal("second Close changed the counts")
	}
}

func TestLogWriterFlushesOnInterval(t *testing.T) {
	c := &captured{}
	w := StartLogWriter(t.Context(), LogWriterOptions{Append: c.append, FlushInterval: 5 * time.Millisecond})
	w.Logf(LevelInfo, "hello %d", 1)
	deadline := time.Now().Add(2 * time.Second)
	for len(c.lines()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := c.lines(); len(got) != 1 || got[0].Text != "hello 1" || got[0].Stream != StreamWorker {
		t.Fatalf("%+v", got)
	}
	w.Close()
}

func TestLogWriterCountsFailedInserts(t *testing.T) {
	c := &captured{fail: true}
	var errs int
	var mu sync.Mutex
	w := StartLogWriter(t.Context(), LogWriterOptions{Append: c.append, OnError: func(error) { mu.Lock(); errs++; mu.Unlock() }})
	for range 3 {
		w.Log(LevelInfo, StreamWorker, "x")
	}
	stored, dropped := w.Close()
	if stored != 0 || dropped < 3 || errs == 0 {
		t.Fatalf("stored %d dropped %d errs %d", stored, dropped, errs)
	}
}
