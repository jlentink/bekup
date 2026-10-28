package calendar

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/retention"
	"github.com/jlentink/bekup/internal/scheduler"
)

// QueryTimeout bounds every statement of a calendar read (D-160): the
// reads run in a read-only transaction with this statement_timeout.
const QueryTimeout = 5 * time.Second

// Service loads and builds calendars. It never writes and never
// authorizes beyond the scope it is given.
type Service struct {
	db  db.TxBeginner
	now func() time.Time
}

// New returns a service on pool (*pgxpool.Pool).
func New(pool db.TxBeginner) *Service { return &Service{db: pool, now: time.Now} }

// Build validates req (a *ValidationError when invalid) and builds the
// calendar of the jobs in scope with four batched queries, in one
// read-only transaction whose statements are bounded by QueryTimeout.
func (s *Service) Build(ctx context.Context, scope Scope, req Request) (Calendar, error) {
	if !scope.valid() {
		return Calendar{}, ErrBadScope
	}
	r, err := validate(req)
	if err != nil {
		return Calendar{}, err
	}
	in := input{Now: s.now().UTC(), From: r.From, To: r.To, Timezone: r.Timezone, Copies: map[uuid.UUID][]retention.Copy{}}
	if r.JobIDs != nil && len(r.JobIDs) == 0 {
		return compute(in), nil
	}
	err = db.TxWith(ctx, s.db, func(_ pgx.Tx, q *sqlc.Queries) error {
		if err := q.SetCalendarReadLimits(ctx, fmt.Sprintf("%dms", QueryTimeout.Milliseconds())); err != nil {
			return fmt.Errorf("calendar: read limits: %w", err)
		}
		return load(ctx, q, scope, r, &in)
	})
	if err != nil {
		return Calendar{}, err
	}
	return compute(in), nil
}

// load runs the four batched reads into in.
func load(ctx context.Context, q *sqlc.Queries, scope Scope, r Request, in *input) error {
	jobRows, err := q.ListCalendarJobs(ctx, sqlc.ListCalendarJobsParams{
		ScopeAll: scope.All, UserID: scope.Owner, JobIds: r.JobIDs, MaxRows: MaxJobs + 1,
	})
	if err != nil {
		return fmt.Errorf("calendar: list jobs: %w", err)
	}
	if len(jobRows) > MaxJobs {
		jobRows, in.Truncated = jobRows[:MaxJobs], true
	}
	if len(jobRows) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(jobRows))
	for i := range jobRows {
		j := &jobRows[i]
		ids = append(ids, j.ID)
		in.Jobs = append(in.Jobs, jobOf(j))
	}

	runRows, err := q.ListCalendarRuns(ctx, sqlc.ListCalendarRunsParams{
		JobIds: ids, RangeFrom: r.From, RangeTo: r.To, ScanFrom: r.From.Add(-runLookback), MaxRows: MaxRuns + 1,
	})
	if err != nil {
		return fmt.Errorf("calendar: list runs: %w", err)
	}
	if len(runRows) > MaxRuns {
		runRows, in.Truncated = runRows[:MaxRuns], true
	}
	for i := range runRows {
		x := &runRows[i]
		in.Runs = append(in.Runs, run{
			ID: x.ID, JobID: x.JobID, Status: string(x.Status), Trigger: x.RunTrigger, ScheduledFor: x.ScheduledFor,
			QueuedAt: x.QueuedAt, StartedAt: x.StartedAt, FinishedAt: x.FinishedAt, ErrorCode: x.ErrorCode,
		})
	}

	skipRows, err := q.ListCalendarSkips(ctx, sqlc.ListCalendarSkipsParams{JobIds: ids, RangeFrom: r.From, RangeTo: r.To, MaxRows: MaxSkips + 1})
	if err != nil {
		return fmt.Errorf("calendar: list skips: %w", err)
	}
	if len(skipRows) > MaxSkips {
		skipRows, in.Truncated = skipRows[:MaxSkips], true
	}
	for _, x := range skipRows {
		in.Skips = append(in.Skips, skip{JobID: x.JobID, ScheduledFor: x.ScheduledFor, Reason: x.Reason, CreatedAt: x.CreatedAt})
	}

	copyRows, err := q.ListCalendarCopies(ctx, sqlc.ListCalendarCopiesParams{JobIds: ids, MaxRows: MaxCopies + 1})
	if err != nil {
		return fmt.Errorf("calendar: list copies: %w", err)
	}
	if len(copyRows) > MaxCopies {
		// The last job may be cut off: its projection would be wrong, so
		// it gets none.
		last := copyRows[MaxCopies].JobID
		copyRows, in.Truncated = copyRows[:MaxCopies], true
		for len(copyRows) > 0 && copyRows[len(copyRows)-1].JobID == last {
			copyRows = copyRows[:len(copyRows)-1]
		}
	}
	for _, x := range copyRows {
		in.Copies[x.JobID] = append(in.Copies[x.JobID], retention.Copy{
			RunID: x.RunID, DestinationID: x.DestinationID, Time: x.RunTime, Complete: x.Complete, LockUntil: x.LockUntil,
		})
	}
	return nil
}

// jobOf converts a job row; a stored schedule that does not compile gives
// no planned occurrences and no projection (the scheduler records it as
// unschedulable).
func jobOf(j *sqlc.ListCalendarJobsRow) job {
	out := job{ID: j.ID, Name: j.Name, Enabled: j.Enabled, NextRunAt: j.NextRunAt}
	if p, err := scheduler.CompileStored(j.Schedule); err == nil {
		out.Plan = p
	}
	if j.RetentionKeepLast != nil {
		out.Policy.KeepLast = int(*j.RetentionKeepLast)
	}
	if j.RetentionKeepDays != nil {
		out.Policy.KeepDays = int(*j.RetentionKeepDays)
	}
	return out
}
