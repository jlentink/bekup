//go:build integration

package queue

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

func newClient(t *testing.T, pool *pgxpool.Pool, s Settings, w river.Worker[BackupRunArgs]) *Client {
	t.Helper()
	var ws *Workers
	if w != nil {
		var err error
		if ws, err = NewWorkers(s); err != nil {
			t.Fatal(err)
		}
		if err := Register(ws, w); err != nil {
			t.Fatal(err)
		}
	}
	c, err := New(pool, Options{Settings: s, Logger: slog.New(slog.DiscardHandler), Workers: ws, FetchPollInterval: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func insert(t *testing.T, pool *pgxpool.Pool, c *Client, args river.JobArgs) InsertResult {
	t.Helper()
	var res InsertResult
	err := db.TxWith(t.Context(), pool, func(tx pgx.Tx, _ *sqlc.Queries) error {
		var err error
		res, err = c.InsertTx(t.Context(), tx, args, InsertOptions{})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func waitState(t *testing.T, c *Client, id int64, want rivertype.JobState) *rivertype.JobRow {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		j, err := c.River().JobGet(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if j.State == want {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %d: state %s, want %s (attempt %d, errors %+v)", id, j.State, want, j.Attempt, j.Errors)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestInsertUniqueAndAttempts(t *testing.T) {
	pool := dbtest.Migrated(t).AppPool(t)
	c := newClient(t, pool, testSettings(), nil)
	jobID := uuid.New()
	first := insert(t, pool, c, BackupRunArgs{JobID: jobID, ScheduledFor: time.Now().UTC(), Trigger: TriggerSchedule})
	if first.Duplicate {
		t.Fatal("first insert is a duplicate")
	}
	row, err := c.River().JobGet(t.Context(), first.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Queue != string(QueueBackup) || row.MaxAttempts != MaxAttemptsBackup || row.Kind != KindBackupRun {
		t.Fatalf("row %s/%d/%s", row.Queue, row.MaxAttempts, row.Kind)
	}
	// Another occurrence of the same job (other scheduled_for, trigger)
	// is a duplicate while the first is active ...
	dup := insert(t, pool, c, BackupRunArgs{JobID: jobID, ScheduledFor: time.Now().Add(time.Hour).UTC(), Trigger: TriggerCatchUp})
	if !dup.Duplicate || dup.JobID != first.JobID {
		t.Fatalf("dup %+v", dup)
	}
	// ... other jobs are independent.
	if other := insert(t, pool, c, BackupRunArgs{JobID: uuid.New(), Trigger: TriggerSchedule}); other.Duplicate {
		t.Fatal("another job's run is a duplicate")
	}
	// Running and retryable block too; completed, canceled and discarded
	// do not (bekup_app may update river_job; River itself does).
	for _, st := range []struct {
		state   string
		blocked bool
	}{{"running", true}, {"retryable", true}, {"completed", false}} {
		if _, err := pool.Exec(t.Context(), "UPDATE river_job SET state = $1::river_job_state, attempted_at = now(), finalized_at = CASE WHEN $1::text = 'completed' THEN now() END WHERE id = $2", st.state, first.JobID); err != nil {
			t.Fatal(err)
		}
		r := insert(t, pool, c, BackupRunArgs{JobID: jobID, Trigger: TriggerSchedule})
		if r.Duplicate != st.blocked {
			t.Fatalf("state %s: duplicate=%v", st.state, r.Duplicate)
		}
		if !r.Duplicate {
			break
		}
	}
	if _, err := c.InsertTx(t.Context(), nil, okArgs{}, InsertOptions{}); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("unknown kind: %v", err)
	}
}

func TestWorkerRunsAndDrainsOnShutdown(t *testing.T) {
	pool := dbtest.Migrated(t).AppPool(t)
	started := make(chan struct{})
	var finished atomic.Bool
	w := &testWorker[BackupRunArgs]{work: func(ctx context.Context, _ *river.Job[BackupRunArgs]) error {
		close(started)
		select {
		case <-time.After(700 * time.Millisecond):
			finished.Store(true)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	c := newClient(t, pool, testSettings(), w)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	res := insert(t, pool, c, BackupRunArgs{JobID: uuid.New(), Trigger: TriggerSchedule})
	select {
	case <-started:
	case <-time.After(15 * time.Second):
		t.Fatal("job not started")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return")
	}
	if !finished.Load() {
		t.Fatal("running job was not allowed to finish")
	}
	waitState(t, c, res.JobID, rivertype.JobStateCompleted)
}

func TestHardStopAfterShutdownTimeout(t *testing.T) {
	pool := dbtest.Migrated(t).AppPool(t)
	s := testSettings()
	s.ShutdownTimeout = 300 * time.Millisecond
	s.HardStopTimeout = 10 * time.Second
	started := make(chan struct{})
	var canceled atomic.Bool
	w := &testWorker[BackupRunArgs]{work: func(ctx context.Context, _ *river.Job[BackupRunArgs]) error {
		close(started)
		<-ctx.Done()
		canceled.Store(true)
		return ctx.Err()
	}}
	c := newClient(t, pool, s, w)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	res := insert(t, pool, c, BackupRunArgs{JobID: uuid.New(), Trigger: TriggerSchedule})
	<-started
	begin := time.Now()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !canceled.Load() {
		t.Fatal("job context not canceled by the hard stop")
	}
	if el := time.Since(begin); el < s.ShutdownTimeout || el > 10*time.Second {
		t.Fatalf("shutdown took %s", el)
	}
	// The interrupted attempt is retryable (backup: 2 attempts).
	j, err := c.River().JobGet(t.Context(), res.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != rivertype.JobStateRetryable && j.State != rivertype.JobStateAvailable {
		t.Fatalf("state %s", j.State)
	}
}

func TestQueueTimeoutBoundsAttempt(t *testing.T) {
	pool := dbtest.Migrated(t).AppPool(t)
	s := testSettings()
	q := s.Queues[QueueBackup]
	q.Timeout = 200 * time.Millisecond
	s.Queues[QueueBackup] = q
	gotDeadline := make(chan time.Duration, 1)
	w := &testWorker[BackupRunArgs]{work: func(ctx context.Context, _ *river.Job[BackupRunArgs]) error {
		dl, ok := ctx.Deadline()
		if !ok {
			gotDeadline <- -1
			return nil
		}
		gotDeadline <- time.Until(dl)
		<-ctx.Done()
		return ctx.Err()
	}}
	c := newClient(t, pool, s, w)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- c.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-runErr; err != nil {
			t.Error(err)
		}
	}()
	res := insert(t, pool, c, BackupRunArgs{JobID: uuid.New(), Trigger: TriggerSchedule})
	select {
	case d := <-gotDeadline:
		if d <= 0 || d > 200*time.Millisecond {
			t.Fatalf("attempt deadline %s, want <= 200ms (the worker's own 1ns Timeout must be ignored)", d)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("job not started")
	}
	// Both attempts (backup: MaxAttempts 2) end at the queue timeout.
	j := waitState(t, c, res.JobID, rivertype.JobStateDiscarded)
	if len(j.Errors) != MaxAttemptsBackup || j.Errors[0].Error != context.DeadlineExceeded.Error() {
		t.Fatalf("attempt errors %+v", j.Errors)
	}
}

func TestRetentionSweepUniquePerJob(t *testing.T) {
	pool := dbtest.Migrated(t).AppPool(t)
	c := newClient(t, pool, testSettings(), nil)
	jobID := uuid.New()
	first := insert(t, pool, c, RetentionSweepArgs{JobID: &jobID, Reason: RetentionPostRun})
	if first.Duplicate {
		t.Fatal("first insert is a duplicate")
	}
	row, err := c.River().JobGet(t.Context(), first.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Queue != string(QueueRetention) || row.MaxAttempts != MaxAttemptsRetention || row.Kind != KindRetentionSweep {
		t.Fatalf("row %s/%d/%s", row.Queue, row.MaxAttempts, row.Kind)
	}
	// The same job with another reason (or dry run) is a duplicate ...
	if dup := insert(t, pool, c, RetentionSweepArgs{JobID: &jobID, Reason: RetentionDaily, DryRun: true}); !dup.Duplicate {
		t.Fatal("second sweep of the same job is not a duplicate")
	}
	// ... another job and the daily fan-out (no job) are independent, and
	// the fan-out is unique itself.
	other := uuid.New()
	if r := insert(t, pool, c, RetentionSweepArgs{JobID: &other, Reason: RetentionDaily}); r.Duplicate {
		t.Fatal("another job's sweep is a duplicate")
	}
	if r := insert(t, pool, c, RetentionSweepArgs{Reason: RetentionDaily}); r.Duplicate {
		t.Fatal("fan-out is a duplicate of a job sweep")
	}
	if r := insert(t, pool, c, RetentionSweepArgs{Reason: RetentionDaily}); !r.Duplicate {
		t.Fatal("second fan-out is not a duplicate")
	}
}
