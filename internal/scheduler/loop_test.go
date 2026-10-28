package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/queue"
)

func TestNewLoopDefaults(t *testing.T) {
	if _, err := NewLoop(LoopOptions{}); err == nil {
		t.Fatal("missing dependencies accepted")
	}
}

func TestDecide(t *testing.T) {
	t0 := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	hourly := []byte(`{"kind":"interval","every":1,"unit":"hours","anchor":"2026-01-01T00:00","timezone":"UTC"}`)
	d, err := decide(hourly, t0, t0.Add(time.Minute))
	if err != nil || !d.Run || d.CatchUp || !d.ScheduledAt.Equal(t0) || !d.NextRunAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("%+v %v", d, err)
	}
	d, err = decide(hourly, t0, t0.Add(3*time.Hour))
	if err != nil || !d.Run || !d.CatchUp || !d.NextRunAt.Equal(t0.Add(4*time.Hour)) {
		t.Fatalf("catch-up %+v %v", d, err)
	}
	// A one-minute interval below today's policy still computes: the
	// policy is only checked on write (D-143).
	minutely := []byte(`{"kind":"interval","every":1,"unit":"minutes","anchor":"2026-01-01T00:00","timezone":"UTC"}`)
	if d, err := decide(minutely, t0, t0.Add(30*time.Second)); err != nil || !d.Run {
		t.Fatalf("minutely %+v %v", d, err)
	}
	if d, err := decide([]byte(`{"kind":"manual"}`), t0, t0.Add(time.Minute)); err != nil || d.Run || !d.NextRunAt.IsZero() {
		t.Fatalf("manual %+v %v", d, err)
	}
	for name, raw := range map[string]string{
		"unknown field": `{"kind":"manual","x":1}`,
		"bad kind":      `{"kind":"weekly"}`,
		"not json":      `nope`,
		"bad zone":      `{"kind":"cron","cron":"0 3 * * *","timezone":"Mars/Base"}`,
	} {
		_, err := decide([]byte(raw), t0, t0.Add(time.Minute))
		if !errors.Is(err, ErrInvalid) || unschedulableReason(err) != "invalid schedule" {
			t.Errorf("%s: %v", name, err)
		}
	}
	big := make([]byte, MaxJSONBytes+1)
	if _, err := decide(big, t0, t0); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversized schedule accepted")
	}
	if unschedulableReason(ErrNoOccurrence) != "no further occurrence" || unschedulableReason(ErrOutOfRange) != "time out of range" ||
		unschedulableReason(errors.New("x")) != "schedule evaluation failed" {
		t.Fatal("reasons")
	}
}

func TestNopMetrics(t *testing.T) {
	var m Metrics = NopMetrics{}
	m.TickCompleted(time.Second, nil)
	m.Enqueued("schedule")
	m.Skipped(SkipStillRunning)
	l, err := NewLoop(LoopOptions{DB: nopDB{}, Queue: nopQueue{}, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	if l.interval != DefaultTickInterval || l.batch != DefaultBatchSize || l.now == nil || l.metrics == nil || l.Name() != "scheduler" {
		t.Fatalf("defaults %+v", l)
	}
	if _, err := l.Tick(t.Context()); err == nil {
		t.Fatal("tick without a database succeeded")
	}
}

type nopDB struct{}

func (nopDB) Begin(context.Context) (pgx.Tx, error) { return nil, errors.New("no db") }

type nopQueue struct{}

func (nopQueue) InsertTx(context.Context, pgx.Tx, river.JobArgs, queue.InsertOptions) (queue.InsertResult, error) {
	return queue.InsertResult{}, nil
}
