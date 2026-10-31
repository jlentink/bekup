package runstate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Abandoned runs (D-209). A run whose last River attempt crashed (the
// worker process died and River rescued and finally discarded the job, or
// the job was finalized without the run's terminal write) stays queued or
// running forever: Store.Begin of a later attempt would finish it, but no
// attempt follows. Retention ignores unfinished runs, so their files would
// never be considered. FailAbandonedRuns finishes such runs as failed with
// code interrupted (like Begin does), or as canceled when a cancellation
// was requested, publishing the run event and
// notification and telling the observer, so retention and the unrecorded
// file scan see them afterwards.

const (
	// AbandonedGrace is how long a run must have been unchanged (with its
	// River job gone or finalized) before it counts as abandoned; it
	// covers the worker's final writes and clock skew.
	AbandonedGrace = 15 * time.Minute
	// maxAbandonedPerSweep bounds one call.
	maxAbandonedPerSweep = 200
)

// FailAbandonedRuns finishes the runs no worker will finish (see above) and
// returns how many it finished. Each run is re-checked under its row lock.
func (s *Store) FailAbandonedRuns(ctx context.Context) (int, error) {
	idle := s.now().Add(-AbandonedGrace)
	rows, err := sqlc.New(s.db).ListAbandonedRuns(ctx, sqlc.ListAbandonedRunsParams{IdleBefore: idle, MaxRows: maxAbandonedPerSweep})
	if err != nil {
		return 0, fmt.Errorf("runstate: list abandoned runs: %w", err)
	}
	n := 0
	var errs []error
	for _, r := range rows {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		var (
			finished sqlc.Run
			done     bool
		)
		ref := Ref{RunID: r.ID, JobID: r.JobID}
		err := db.TxWith(ctx, s.db, func(_ pgx.Tx, q *sqlc.Queries) error {
			done = false
			cur, err := q.GetRunForUpdate(ctx, r.ID)
			if err != nil {
				return fmt.Errorf("runstate: lock run: %w", err)
			}
			still, err := q.IsRunAbandoned(ctx, sqlc.IsRunAbandonedParams{ID: r.ID, IdleBefore: idle})
			if err != nil {
				return fmt.Errorf("runstate: re-check abandoned run: %w", err)
			}
			if !still {
				return nil
			}
			res := Result{
				Status: StatusFailed,
				Failure: &Failure{Code: CodeInterrupted,
					Message: "the worker stopped before the run finished and no further attempt follows"},
			}
			if cur.CancelRequestedAt != nil {
				// Someone asked to cancel it: that is how it ended.
				res = Result{Status: StatusCanceled, Failure: &Failure{Code: CodeCanceled,
					Message: "the run was canceled; the worker stopped before it finished"}}
			}
			finished, err = s.finishTx(ctx, q, ref, res)
			if err != nil {
				return err
			}
			done = true
			return nil
		})
		switch {
		case errors.Is(err, ErrNotActive), errors.Is(err, pgx.ErrNoRows):
			continue
		case err != nil:
			errs = append(errs, err)
			continue
		}
		if done {
			s.observe(ctx, finished)
			n++
		}
	}
	return n, errors.Join(errs...)
}
