package restore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/runstate"
)

// The stuck-restore sweep (task 4.5, D-167). A restore whose worker
// crashed stays in staging/verifying/restoring (or queued when its River
// job was discarded) forever: the worker records every other outcome. The
// sweep (River kind restore_stuck_sweep, maintenance queue, every
// StuckSweepInterval) fails such restores with code abandoned, deletes an
// identity handoff, releases the artifact hold and the run slot, removes
// the staging directory on this process (other processes' scratch sweepers
// remove theirs within 10 minutes, the restore no longer running) and
// audits restore.abandoned. A restore is stuck when
//
//   - it started more than workers.restore_timeout + StuckGrace ago (the
//     queue timeout has then canceled any live worker, which records the
//     outcome within its final timeout), or
//   - it has not changed for StuckGrace and its River job is gone or
//     already finalized (completed, discarded, canceled).
//
// Queued restores waiting for a run slot (River job available or
// scheduled) are never touched.

// Sweep settings.
const (
	// StuckSweepInterval is the period of the sweep.
	StuckSweepInterval = 5 * time.Minute
	// StuckGrace is added to the restore timeout (and is the idle time
	// of a restore whose River job ended) before a restore counts as
	// stuck; it covers the worker's final timeout and clock skew.
	StuckGrace = 15 * time.Minute
	// maxStuckPerSweep bounds one sweep.
	maxStuckPerSweep = 100
)

// SweepResult counts what a sweep did.
type SweepResult struct {
	Abandoned int
}

// SweepStuck fails the restores no worker will finish (see above).
// timeout is workers.restore_timeout; grace <= 0 uses StuckGrace.
func (s *Service) SweepStuck(ctx context.Context, timeout, grace time.Duration) (SweepResult, error) {
	if timeout <= 0 {
		return SweepResult{}, errors.New("restore: the stuck sweep needs the restore timeout")
	}
	if grace <= 0 {
		grace = StuckGrace
	}
	now := s.o.Now()
	params := sqlc.ListStuckRestoresParams{
		StartedBefore: now.Add(-timeout - grace), IdleBefore: now.Add(-grace), MaxRows: maxStuckPerSweep,
	}
	rows, err := sqlc.New(s.o.DB).ListStuckRestores(ctx, params)
	if err != nil {
		return SweepResult{}, fmt.Errorf("restore: list stuck restores: %w", err)
	}
	var (
		res  SweepResult
		errs []error
	)
	for _, row := range rows {
		ok, err := s.abandon(ctx, row.ID, params)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !ok {
			continue
		}
		res.Abandoned++
		if !s.isLive(row.ID) {
			if err := s.removeDir(row.ID); err != nil {
				s.o.Logger.WarnContext(ctx, "removing the staging directory of an abandoned restore failed",
					slog.String("restore_id", row.ID.String()), slog.Any("error", err))
			}
		}
		s.o.Logger.WarnContext(ctx, "restore abandoned: no worker finished it",
			slog.String("restore_id", row.ID.String()), slog.String("job_id", row.JobID.String()), slog.String("status", string(row.Status)))
	}
	return res, errors.Join(errs...)
}

// abandon fails one restore after re-checking the condition under its row
// lock; false when it is no longer stuck.
func (s *Service) abandon(ctx context.Context, id uuid.UUID, p sqlc.ListStuckRestoresParams) (bool, error) {
	done := false
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		r, err := q.LockRestore(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("restore: lock restore: %w", err)
		}
		stuck, err := q.IsRestoreStuck(ctx, sqlc.IsRestoreStuckParams{ID: id, StartedBefore: p.StartedBefore, IdleBefore: p.IdleBefore})
		if err != nil {
			return fmt.Errorf("restore: re-check stuck restore: %w", err)
		}
		if !stuck {
			return nil
		}
		changed, err := s.finishTx(ctx, q, id, StatusFailed, ErrAbandoned, Outcome{
			LogLines: int(r.LogLines), LogLinesDropped: int(r.LogLinesDropped), Warnings: r.Warnings,
		})
		if err != nil || !changed {
			return err
		}
		if _, err := q.DeleteRestoreIdentityHandoff(ctx, id); err != nil {
			return fmt.Errorf("restore: delete identity handoff: %w", err)
		}
		if _, err := q.ReleaseArtifactHoldsOfHolder(ctx, sqlc.ReleaseArtifactHoldsOfHolderParams{Kind: string(runstate.HoldRestore), HolderID: id}); err != nil {
			return fmt.Errorf("restore: release artifact hold: %w", err)
		}
		if err := q.DeleteRunSlot(ctx, id); err != nil {
			return fmt.Errorf("restore: release run slot: %w", err)
		}
		d := audit.Details{
			"job_id": r.JobID.String(), "artifact_id": r.ArtifactID.String(), "target_connection_id": r.TargetConnectionID.String(),
			"mode": string(r.Mode), "previous_status": string(r.Status), "error_code": CodeAbandoned,
		}
		if r.TargetDbName != nil {
			d["target_database"] = *r.TargetDbName
		}
		if r.StartedAt != nil {
			d["started_at"] = r.StartedAt.UTC().Format(time.RFC3339)
		}
		if _, err := s.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.SystemActor(), Action: audit.ActionRestoreAbandoned, TargetType: "restore", TargetID: id.String(), Details: d,
		}); err != nil {
			return err
		}
		done = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("restore: abandon restore %s: %w", id, err)
	}
	return done, nil
}

// StuckSweepWorker runs SweepStuck (River kind restore_stuck_sweep).
type StuckSweepWorker struct {
	river.WorkerDefaults[queue.RestoreStuckSweepArgs]
	Service *Service
	// RestoreTimeout is workers.restore_timeout.
	RestoreTimeout time.Duration
	// Grace overrides StuckGrace (tests).
	Grace time.Duration
}

// Work implements river.Worker.
func (w *StuckSweepWorker) Work(ctx context.Context, _ *river.Job[queue.RestoreStuckSweepArgs]) error {
	res, err := w.Service.SweepStuck(ctx, w.RestoreTimeout, w.Grace)
	if res.Abandoned > 0 {
		w.Service.o.Logger.InfoContext(ctx, "stuck restores abandoned", slog.Int("count", res.Abandoned))
	}
	return err
}

// RegisterStuckSweep registers the sweep worker and its periodic job.
func RegisterStuckSweep(ws *queue.Workers, svc *Service, timeout time.Duration) error {
	if svc == nil || timeout <= 0 {
		return errors.New("restore: the stuck sweep needs the service and the restore timeout")
	}
	if err := queue.Register(ws, &StuckSweepWorker{Service: svc, RestoreTimeout: timeout}); err != nil {
		return err
	}
	return ws.AddPeriodic(StuckSweepInterval, queue.RestoreStuckSweepArgs{})
}
