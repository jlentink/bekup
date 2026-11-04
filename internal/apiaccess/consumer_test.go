package apiaccess

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/pubsub"
)

// TestReasonOf: every refusal of RunFromAPI maps to its rejection reason
// (also when wrapped); anything else leaves the request pending.
func TestReasonOf(t *testing.T) {
	for _, c := range []struct {
		err  error
		want Reason
	}{
		{jobs.ErrRunActive, ReasonRunActive},
		{jobs.ErrDisabled, ReasonJobDisabled},
		{jobs.ErrNotFound, ReasonJobGone},
		{jobs.ErrAPIJobOutOfScope, ReasonJobGone},
		{jobs.ErrAPITokenInvalid, ReasonTokenInvalid},
		{fmt.Errorf("wrapped: %w", jobs.ErrRunActive), ReasonRunActive},
	} {
		got, ok := reasonOf(c.err)
		if !ok || got != c.want {
			t.Errorf("reasonOf(%v) = %q, %v; want %q", c.err, got, ok, c.want)
		}
	}
	for _, err := range []error{errors.New("connection reset"), jobs.ErrActorMismatch, nil} {
		if r, ok := reasonOf(err); ok {
			t.Errorf("reasonOf(%v) = %q, want no reason", err, r)
		}
	}
}

// TestOutcomes: the metric outcomes are accepted plus every reason the
// run_requests_reason CHECK allows, without duplicates.
func TestOutcomes(t *testing.T) {
	want := []string{"accepted", "run_active", "job_disabled", "job_gone", "token_invalid", "expired"}
	got := Outcomes()
	if !slices.Equal(got, want) {
		t.Fatalf("Outcomes() = %v, want %v", got, want)
	}
}

func TestNewConsumerDefaults(t *testing.T) {
	if _, err := NewConsumer(ConsumerOptions{}); err == nil {
		t.Fatal("consumer without DB and Runner accepted")
	}
}

// beginDB fails every transaction and records when one was begun.
type beginDB struct {
	mu    sync.Mutex
	begun []time.Time
}

var errBeginDB = errors.New("no database in unit tests")

func (d *beginDB) Begin(context.Context) (pgx.Tx, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.begun = append(d.begun, time.Now())
	return nil, errBeginDB
}

func (d *beginDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errBeginDB
}

func (d *beginDB) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, errBeginDB }

func (d *beginDB) QueryRow(context.Context, string, ...any) pgx.Row { return failedRow{} }

func (d *beginDB) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.begun)
}

type failedRow struct{}

func (failedRow) Scan(...any) error { return errBeginDB }

type discardAuditor struct{}

func (discardAuditor) RecordTx(context.Context, *sqlc.Queries, audit.Event) (audit.Entry, error) {
	return audit.Entry{}, nil
}

type noRunner struct{}

func (noRunner) RunFromAPI(context.Context, pgx.Tx, jobs.APIActor, uuid.UUID) (jobs.RunRequest, error) {
	return jobs.RunRequest{}, errBeginDB
}

// TestConsumerWakeGap: a flood of notifications and resync signals (a
// full buffer turns notifications into resync signals) never makes the
// consumer drain more often than every minWakeGap (finding 8).
func TestConsumerWakeGap(t *testing.T) {
	d := &beginDB{}
	bus := pubsub.NewBus()
	c, err := NewConsumer(ConsumerOptions{DB: d, Runner: noRunner{}, Auditor: discardAuditor{}, Bus: bus, SweepInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	// One Drain against the failing database: its number of
	// transactions.
	c.Drain(t.Context())
	perDrain := d.count()
	if perDrain == 0 {
		t.Fatal("a drain began no transaction")
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	for bus.Subscribers(pubsub.APIRequests.Name) == 0 {
		time.Sleep(time.Millisecond)
	}
	const flood = 3 * minWakeGap
	start := time.Now()
	for time.Since(start) < flood {
		for range 2 * wakeBuffer {
			bus.Publish(pubsub.APIRequests.Name, "x")
		}
		bus.Resync(pubsub.APIRequests.Name)
		time.Sleep(time.Millisecond)
	}
	elapsed := time.Since(start)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// The start drain, then at most one per gap (plus one in flight).
	drains := d.count()/perDrain - 1
	if limit := int(elapsed/minWakeGap) + 2; drains > limit {
		t.Fatalf("%d drains within %v, want at most %d", drains, elapsed, limit)
	}
}
