package summary

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/queue"
)

// TickInterval is how often the leader enqueues notify_summary_tick
// (a summary goes out at most this late).
const TickInterval = 5 * time.Minute

// Bounds of one tick.
const (
	// summaryTickBudget is the most summaries one tick handles; the rest
	// are handled by the next tick.
	summaryTickBudget = 1000
	summaryBatch      = 500
	summaryMaxBatches = 100
)

// Outcomes recorded in user_summary_settings.last_outcome.
const (
	summaryOutcomeSent   = "sent"
	summaryOutcomeNoJobs = "no_jobs"
)

// TickWorker works the periodic notify_summary_tick job.
type TickWorker struct {
	river.WorkerDefaults[queue.NotifySummaryTickArgs]
	s *Service
}

// Work implements river.Worker.
func (w *TickWorker) Work(ctx context.Context, _ *river.Job[queue.NotifySummaryTickArgs]) error {
	_, err := w.s.Tick(ctx)
	return err
}

// TickResult counts what one tick did.
type TickResult struct {
	// Planned rows got a (new) next due time.
	Planned int
	// Emitted summary events (a dedup hit is not counted).
	Emitted int
	// NoJobs due summaries were skipped: the account can see no job.
	NoJobs int
	// Failed due summaries are retried at the next tick.
	Failed int
}

// Tick handles the periodic summaries (D-226): it adds state rows
// for accounts without one, computes the next due time of rows that need
// one (new rows, rows that follow changed defaults) and emits one
// summary.periodic event per due summary, each claimed FOR UPDATE SKIP
// LOCKED in its own transaction (one failing account never blocks the
// others). Missed due times collapse into one summary covering the gap.
func (s *Service) Tick(ctx context.Context) (TickResult, error) {
	var res TickResult
	q := sqlc.New(s.o.DB)
	for range summaryMaxBatches {
		n, err := q.InsertMissingSummarySettings(ctx, summaryBatch)
		if err != nil {
			return res, fmt.Errorf("notify: add summary rows: %w", err)
		}
		if n < summaryBatch {
			break
		}
	}
	planned, err := s.planSummaries(ctx)
	res.Planned = planned
	if err != nil {
		return res, err
	}
	now := s.o.Now()
	skip := []uuid.UUID{}
	defer func() {
		if res.Emitted > 0 || res.NoJobs > 0 || res.Failed > 0 {
			s.o.Logger.InfoContext(ctx, "periodic summaries handled",
				slog.Int("emitted", res.Emitted), slog.Int("no_jobs", res.NoJobs), slog.Int("failed", res.Failed))
		}
	}()
	for range summaryTickBudget {
		user, outcome, err := s.handleDueSummary(ctx, now, skip)
		if user == uuid.Nil {
			// Nothing due, or the claim itself failed.
			return res, err
		}
		if err != nil {
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			s.o.Logger.WarnContext(ctx, "periodic summary failed; retried at the next tick",
				slog.String("user_id", user.String()), slog.Any("error", err))
			res.Failed++
			skip = append(skip, user)
			continue
		}
		switch outcome {
		case summaryOutcomeSent:
			res.Emitted++
		case summaryOutcomeNoJobs:
			res.NoJobs++
		}
	}
	return res, nil
}

// planSummaries computes the next due time of rows without one and of
// rows that follow defaults other than the current ones.
func (s *Service) planSummaries(ctx context.Context) (int, error) {
	basis := s.o.Defaults.Settings().basis()
	total := 0
	for range summaryMaxBatches {
		n := 0
		err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
			rows, err := q.ListSummarySettingsToPlan(ctx, sqlc.ListSummarySettingsToPlanParams{DefaultBasis: basis, MaxRows: summaryBatch})
			if err != nil {
				return fmt.Errorf("notify: list summaries to plan: %w", err)
			}
			n = len(rows)
			now := s.o.Now()
			for i := range rows {
				eff, _ := effective(&rows[i], s.o.Defaults)
				next, err := nextDueOrLog(ctx, s.o.Logger, eff, rows[i].UserID, rows[i].LastPeriodEnd, now)
				if err != nil {
					return err
				}
				if err := q.SetSummaryPlan(ctx, sqlc.SetSummaryPlanParams{DueBasis: eff.basis(), NextDueAt: next, UserID: rows[i].UserID}); err != nil {
					return fmt.Errorf("notify: store summary plan: %w", err)
				}
			}
			return nil
		})
		if err != nil {
			return total, err
		}
		total += n
		if n < summaryBatch {
			break
		}
	}
	return total, nil
}

// nextDueOrLog computes the next due time; settings that no longer
// compute (a zone dropped from the database) stop the summary with a
// warning instead of failing every tick.
func nextDueOrLog(ctx context.Context, log *slog.Logger, eff Settings, user uuid.UUID, last *time.Time, now time.Time) (*time.Time, error) {
	var lp time.Time
	if last != nil {
		lp = *last
	}
	next, err := NextDue(eff, lp, now)
	if errors.Is(err, ErrInvalidSettings) {
		log.WarnContext(ctx, "periodic summary settings do not compute; no summary until they are saved again",
			slog.String("user_id", user.String()), slog.Any("error", err))
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("notify: next summary: %w", err)
	}
	if next.IsZero() {
		return nil, nil
	}
	return &next, nil
}

// handleDueSummary claims and handles one due summary. It returns the
// user (uuid.Nil: nothing due) and the outcome.
func (s *Service) handleDueSummary(ctx context.Context, now time.Time, skip []uuid.UUID) (uuid.UUID, string, error) {
	var user uuid.UUID
	var outcome string
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		row, err := q.ClaimDueSummary(ctx, sqlc.ClaimDueSummaryParams{Now: now, SkipIds: skip})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("notify: claim summary: %w", err)
		}
		st := &row.UserSummarySetting
		user = st.UserID
		eff, _ := effective(st, s.o.Defaults)
		if st.NextDueAt == nil || st.DueBasis == nil || *st.DueBasis != eff.basis() || eff.Frequency == Never {
			// Stale state (settings or defaults changed since it was
			// planned): plan again from now, nothing is due.
			next, err := nextDueOrLog(ctx, s.o.Logger, eff, user, st.LastPeriodEnd, now)
			if err != nil {
				return err
			}
			outcome = "replanned"
			return q.SetSummaryPlan(ctx, sqlc.SetSummaryPlanParams{DueBasis: eff.basis(), NextDueAt: next, UserID: user})
		}
		dec, err := decide(eff, *st.NextDueAt, st.LastPeriodEnd, now)
		if errors.Is(err, ErrInvalidSettings) {
			outcome = "replanned"
			return q.SetSummaryPlan(ctx, sqlc.SetSummaryPlanParams{DueBasis: eff.basis(), NextDueAt: nil, UserID: user})
		}
		if err != nil {
			return err
		}
		sc, err := s.scopeOf(ctx, user, row.Role)
		if err != nil {
			return err
		}
		jobs, err := visibleJobs(ctx, q, sc)
		if err != nil {
			return err
		}
		rec := sqlc.RecordSummaryHandledParams{
			DueBasis: eff.basis(), NextDueAt: &dec.NextDue, PeriodEnd: dec.PeriodEnd, OutcomeAt: now, UserID: user,
		}
		if jobs == 0 {
			// Never an empty summary: skipped and recorded.
			outcome, rec.Outcome = summaryOutcomeNoJobs, summaryOutcomeNoJobs
			return q.RecordSummaryHandled(ctx, rec)
		}
		id, err := notify.Emit(ctx, q, notify.Event{
			Type: notify.SummaryPeriodic, Subject: notify.Subject{UserID: user},
			Detail:   eventDetail(dec.PeriodStart, dec.PeriodEnd, eff.Timezone, eff.Frequency, false),
			DedupKey: dedupKey(user, dec.PeriodEnd),
		})
		if err != nil {
			return err
		}
		outcome, rec.Outcome, rec.SentAt = summaryOutcomeSent, summaryOutcomeSent, &now
		if id == uuid.Nil {
			// This period's summary went out before (the dedup key).
			outcome, rec.SentAt = "duplicate", nil
		}
		return q.RecordSummaryHandled(ctx, rec)
	})
	return user, outcome, err
}
