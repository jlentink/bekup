//go:build integration

package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"

	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Forged River control notifications (D-220, SECURITY.md EA-7): any role
// that can connect, bekup_external included, can NOTIFY on any channel.
// A River client that LISTENs acts on river_control payloads without a
// database check; bekup's clients are poll-only and never LISTEN.

// blockingWorkers registers a backup_run worker that reports each started
// job and blocks until its context ends, reporting the cause.
func blockingWorkers(t *testing.T, s Settings) (*Workers, <-chan int64, <-chan error) {
	t.Helper()
	ws, err := NewWorkers(s)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan int64, 8)
	ended := make(chan error, 8)
	if err := Register(ws, &testWorker[BackupRunArgs]{work: func(ctx context.Context, j *river.Job[BackupRunArgs]) error {
		started <- j.ID
		<-ctx.Done()
		ended <- context.Cause(ctx)
		return ctx.Err()
	}}); err != nil {
		t.Fatal(err)
	}
	return ws, started, ended
}

// forge sends notifications as bekup_external on River's topic, with
// and without the schema prefix River's listener uses (current_schema(),
// "public.river_control").
func forge(t *testing.T, ext *pgxpool.Pool, topic string, payloads ...string) {
	t.Helper()
	for _, channel := range []string{"public." + topic, topic} {
		for _, p := range payloads {
			if _, err := ext.Exec(t.Context(), "SELECT pg_notify($1, $2)", channel, p); err != nil {
				t.Fatalf("bekup_external could not NOTIFY %s: %v", channel, err)
			}
		}
	}
}

func waitStarted(t *testing.T, started <-chan int64, want int64) {
	t.Helper()
	select {
	case id := <-started:
		if id != want {
			t.Fatalf("started job %d, want %d", id, want)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("job %d not started", want)
	}
}

func forgedControl(jobID int64) []string {
	return []string{
		fmt.Sprintf(`{"action":"cancel","job_id":%d,"queue":"backup"}`, jobID),
		`{"action":"pause","queue":"backup"}`,
		`{"action":"pause","queue":"*"}`,
	}
}

// TestForgedControlWithoutPollOnly is the control experiment: a River
// client that LISTENs (PollOnly false, not bekup's configuration) cancels
// a running job on a forged river_control NOTIFY from bekup_external.
func TestForgedControlWithoutPollOnly(t *testing.T) {
	d := dbtest.Migrated(t)
	pool, ext := d.AppPool(t), d.ExternalPool(t)
	s := testSettings()
	ws, started, ended := blockingWorkers(t, s)
	c, err := New(pool, Options{Settings: s, Logger: slog.New(slog.DiscardHandler), Workers: ws, FetchPollInterval: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	cfg := c.RiverConfig()
	cfg.PollOnly = false
	rc, err := river.NewClient(riverpgxv5.New(pool), &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := rc.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
		defer cancel()
		if err := rc.StopAndCancel(ctx); err != nil {
			t.Error(err)
		}
	}()
	res := insert(t, pool, c, BackupRunArgs{JobID: uuid.New(), Trigger: TriggerSchedule})
	waitStarted(t, started, res.JobID)
	forge(t, ext, "river_control", forgedControl(res.JobID)[0])
	select {
	case cause := <-ended:
		if !errors.Is(cause, rivertype.ErrJobCancelledRemotely) {
			t.Fatalf("job ended by %v", cause)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a listening River client ignored the forged cancel (the control experiment is broken)")
	}
}

// TestForgedControlIgnored: bekup's poll-only client ignores forged
// cancel and pause notifications, while a real JobCancelTx from another
// client (another process) still cancels the running job.
func TestForgedControlIgnored(t *testing.T) {
	d := dbtest.Migrated(t)
	pool, ext := d.AppPool(t), d.ExternalPool(t)
	s := testSettings()
	q := s.Queues[QueueBackup]
	q.MaxWorkers = 4
	s.Queues[QueueBackup] = q
	ws, started, ended := blockingWorkers(t, s)
	c, err := New(pool, Options{Settings: s, Logger: slog.New(slog.DiscardHandler), Workers: ws, FetchPollInterval: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	runErr := make(chan error, 1)
	go func() { runErr <- c.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-runErr; err != nil {
			t.Error(err)
		}
	}()
	first := insert(t, pool, c, BackupRunArgs{JobID: uuid.New(), Trigger: TriggerSchedule})
	waitStarted(t, started, first.JobID)

	forge(t, ext, "river_control", forgedControl(first.JobID)...)
	forge(t, ext, "river_leadership", `{"action":"request_resign","leader_id":"x"}`)
	// River polls cancel requests and queue settings every 2 s: give a
	// listener-driven reaction well over that time.
	select {
	case cause := <-ended:
		t.Fatalf("forged notification ended the job: %v", cause)
	case <-time.After(5 * time.Second):
	}
	if j := waitState(t, c, first.JobID, rivertype.JobStateRunning); strings.Contains(string(j.Metadata), "cancel_attempted_at") {
		t.Fatalf("job marked for cancel: %s", j.Metadata)
	}
	// The queue was not paused: a new job starts.
	second := insert(t, pool, c, BackupRunArgs{JobID: uuid.New(), Trigger: TriggerSchedule})
	waitStarted(t, started, second.JobID)

	// A real cancel from another, insert-only client: River's poll finds
	// the request in the database.
	other := newClient(t, pool, s, nil)
	err = db.TxWith(t.Context(), pool, func(tx pgx.Tx, _ *sqlc.Queries) error {
		st, err := other.CancelTx(t.Context(), tx, first.JobID)
		if err == nil && st != rivertype.JobStateRunning {
			err = fmt.Errorf("state %s", st)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case cause := <-ended:
		if !errors.Is(cause, rivertype.ErrJobCancelledRemotely) {
			t.Fatalf("job ended by %v", cause)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("JobCancelTx from another client did not reach the running job")
	}
	waitState(t, c, first.JobID, rivertype.JobStateCancelled)
}
