//go:build integration

package jobs

// The per-connection run limit at the worker (task 3.13, D-156): the
// BackupWorker with the real run store and slots snoozes a run while the
// connection's limit (or the global default) is used up. Active runs hold
// their slots through runstate.Slots exactly as the worker takes them; the
// worker itself is only run where it must snooze (no runner here).

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/runstate"
)

type slInserter struct{ next int64 }

func (f *slInserter) InsertTx(context.Context, pgx.Tx, river.JobArgs, queue.InsertOptions) (queue.InsertResult, error) {
	f.next++
	return queue.InsertResult{JobID: f.next}, nil
}

// Stubs that panic when used: a snoozed run never reaches them.
type (
	slTargets struct{ TargetSource }
	slRunner  struct{ runnerpb.RunnerServiceClient }
	slDests   struct{ DestinationOpener }
	slKeys    struct{ EncryptorSource }
	slSigner  struct{ ManifestSigner }
)

func TestIntegrationConnectionRunLimit(t *testing.T) {
	ctx := t.Context()
	d := dbtest.Migrated(t)
	pool := d.AppPool(t)
	dest := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO destinations (id, name, type, config) VALUES ($1, 'disk', 'disk', '{"path":"x"}')`, dest); err != nil {
		t.Fatal(err)
	}
	newConn := func(name string, limit *int) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO connections (name, engine, version_key, image_key, host, port, username, max_concurrent_runs)
			VALUES ($1, 'postgres', '17', '17', 'db.example.test', 5432, 'u', $2) RETURNING id`, name, limit).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	slots := runstate.NewSlots(pool)
	limits := runstate.SlotLimits{Global: 10, PerConnection: 1}
	w, err := NewBackupWorker(BackupWorkerOptions{
		Store: runstate.NewStore(pool, nil, nil), Slots: slots,
		Targets: slTargets{}, Runner: slRunner{}, Destinations: slDests{}, Keys: slKeys{}, Signer: slSigner{},
		Limits: limits, QueueTimeout: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	ins := &slInserter{}
	// work enqueues a run of a new job on conn and lets the worker take
	// it; it must snooze, and the run stays queued without a slot.
	work := func(conn uuid.UUID) {
		t.Helper()
		job, err := dbtest.InsertJob(ctx, pool, dbtest.JobRow{Enabled: true, ConnectionID: conn, DestinationIDs: []uuid.UUID{dest}})
		if err != nil {
			t.Fatal(err)
		}
		args := queue.BackupRunArgs{JobID: job, Trigger: queue.TriggerManual}
		var enq runstate.Enqueued
		if err := db.TxWith(ctx, pool, func(tx pgx.Tx, _ *sqlc.Queries) error {
			enq, err = runstate.EnqueueTx(ctx, tx, ins, nil, args)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		err = w.Work(ctx, &river.Job[queue.BackupRunArgs]{JobRow: &rivertype.JobRow{ID: enq.JobID, Attempt: 1}, Args: args})
		var se *river.JobSnoozeError
		if !errors.As(err, &se) {
			t.Fatalf("Work = %v, want a snooze", err)
		}
		var status string
		if err := pool.QueryRow(ctx, `SELECT status::text FROM runs WHERE id = $1`, enq.RunID).Scan(&status); err != nil || status != "queued" {
			t.Fatalf("snoozed run: %q %v", status, err)
		}
	}
	hold := func(conn uuid.UUID) uuid.UUID {
		t.Helper()
		h := uuid.New()
		ok, err := slots.TryAcquire(ctx, h, conn, runstate.SlotBackup, limits)
		if err != nil || !ok {
			t.Fatalf("slot refused: %v %v", ok, err)
		}
		return h
	}

	// Limit 2: two runs hold slots, the third snoozes.
	two := 2
	limited := newConn("limited", &two)
	h1, h2 := hold(limited), hold(limited)
	work(limited)
	// NULL: the global default (1) applies, the second run snoozes.
	dflt := newConn("default", nil)
	hold(dflt)
	work(dflt)
	// Lowering 2 -> 1 keeps both active runs; a new run snoozes until the
	// count is below the new limit.
	if _, err := pool.Exec(ctx, `UPDATE connections SET max_concurrent_runs = 1 WHERE id = $1`, limited); err != nil {
		t.Fatal(err)
	}
	for _, h := range []uuid.UUID{h1, h2} {
		if err := slots.Heartbeat(ctx, h); err != nil {
			t.Fatalf("active run lost its slot: %v", err)
		}
	}
	work(limited)
	if err := slots.Release(ctx, h1); err != nil {
		t.Fatal(err)
	}
	work(limited)
	if err := slots.Release(ctx, h2); err != nil {
		t.Fatal(err)
	}
	hold(limited)
}
