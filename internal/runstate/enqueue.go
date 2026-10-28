package runstate

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/queue"
)

// Inserter enqueues River jobs (*queue.Client).
type Inserter interface {
	InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, o queue.InsertOptions) (queue.InsertResult, error)
}

// Enqueued is the result of EnqueueTx.
type Enqueued struct {
	queue.InsertResult
	// RunID is the queued run (uuid.Nil for a duplicate).
	RunID uuid.UUID
}

// TriggerOf maps a queue trigger to the run_trigger enum.
func TriggerOf(t queue.Trigger) (sqlc.RunTrigger, error) {
	switch t {
	case queue.TriggerSchedule:
		return sqlc.RunTriggerSchedule, nil
	case queue.TriggerCatchUp:
		return sqlc.RunTriggerCatchUp, nil
	case queue.TriggerManual:
		return sqlc.RunTriggerManual, nil
	case queue.TriggerEarly:
		return sqlc.RunTriggerEarly, nil
	default:
		return "", fmt.Errorf("runstate: unknown trigger %q", t)
	}
}

// EnqueueTx inserts a backup_run River job and, unless River refused it as
// a duplicate (the job already has an active run), its queued run row, both
// in tx, and publishes the run event. Every backup run is enqueued here
// (scheduler, runJobNow, startNextRunEarly), so a queued run is visible and cancelable before
// a worker picks it up.
func EnqueueTx(ctx context.Context, tx pgx.Tx, ins Inserter, pub *pubsub.Publisher, args queue.BackupRunArgs) (Enqueued, error) {
	trigger, err := TriggerOf(args.Trigger)
	if err != nil {
		return Enqueued{}, err
	}
	res, err := ins.InsertTx(ctx, tx, args, queue.InsertOptions{})
	if err != nil {
		return Enqueued{}, err
	}
	out := Enqueued{InsertResult: res}
	if res.Duplicate {
		return out, nil
	}
	p := sqlc.InsertRunParams{JobID: args.JobID, Trigger: trigger, TriggeredBy: args.TriggeredBy, RiverJobID: &res.JobID, Attempt: 1}
	if !args.ScheduledFor.IsZero() {
		sf := args.ScheduledFor.UTC()
		p.ScheduledFor = &sf
	}
	q := sqlc.New(tx)
	row, err := q.InsertRun(ctx, p)
	if err != nil {
		return Enqueued{}, fmt.Errorf("runstate: insert queued run: %w", err)
	}
	out.RunID = row.ID
	return out, publish(ctx, pub, q, EventRun, Ref{RunID: row.ID, JobID: row.JobID})
}
