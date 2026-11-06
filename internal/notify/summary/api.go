package summary

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/scheduler"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// The settings API behind the GraphQL layer (mySummarySettings,
// setSummarySettings, sendSummaryPreview): the caller from the request
// context, the operation re-authorized, always the caller's own row.

// Errors of the API. Field problems are *serviceutil.FieldError; rate
// limits *serviceutil.RateLimitedError or serviceutil.ErrBusy.
var (
	// ErrNotFound: the caller's account is gone.
	ErrNotFound = errors.New("summary: account not found")
)

// View is the caller's summary settings with their schedule.
type View struct {
	// Settings are the effective settings (Custom false: the defaults).
	Settings Settings
	Custom   bool
	// Defaults are the configured defaults.
	Defaults Settings
	// NextDue is when the next summary goes out (nil: never).
	NextDue *time.Time
	// LastSentAt is when the last summary went out.
	LastSentAt *time.Time
	// LastOutcome is sent or no_jobs (the account could see no job), with
	// its time.
	LastOutcome   string
	LastOutcomeAt *time.Time
	// EmailVerified: false means the e-mail part is skipped (inbox only).
	EmailVerified bool
	// Jobs is the number of jobs the caller can see (0: no summary is
	// sent).
	Jobs int
}

// Preview is the result of SendPreview.
type Preview struct {
	PeriodStart, PeriodEnd time.Time
	// EmailVerified: false means the preview reaches the inbox only.
	EmailVerified bool
}

func (s *Service) who(ctx context.Context, op authz.Operation) (auth.Principal, error) {
	if err := s.o.Policy.Authorize(ctx, op); err != nil {
		return auth.Principal{}, err
	}
	p, ok := auth.FromContext(ctx)
	if !ok {
		return auth.Principal{}, errors.New("summary: no principal")
	}
	return p, nil
}

func loadRow(ctx context.Context, q *sqlc.Queries, user uuid.UUID, lock bool) (*sqlc.UserSummarySetting, error) {
	var (
		row sqlc.UserSummarySetting
		err error
	)
	if lock {
		row, err = q.GetSummarySettingsForUpdate(ctx, user)
	} else {
		row, err = q.GetSummarySettings(ctx, user)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("summary: load settings: %w", err)
	}
	return &row, nil
}

// MySettings returns the caller's summary settings and schedule.
func (s *Service) MySettings(ctx context.Context) (View, error) {
	p, err := s.who(ctx, authz.OpSummaryView)
	if err != nil {
		return View{}, err
	}
	return s.view(ctx, sqlc.New(s.o.DB), p.UserID)
}

func (s *Service) view(ctx context.Context, q *sqlc.Queries, user uuid.UUID) (View, error) {
	u, err := q.GetNotifiableUser(ctx, user)
	if errors.Is(err, pgx.ErrNoRows) {
		return View{}, ErrNotFound
	}
	if err != nil {
		return View{}, fmt.Errorf("summary: load account: %w", err)
	}
	row, err := loadRow(ctx, q, user, false)
	if err != nil {
		return View{}, err
	}
	eff, custom := effective(row, s.o.Defaults)
	v := View{Settings: eff, Custom: custom, Defaults: s.o.Defaults.Settings(), EmailVerified: u.EmailVerifiedAt != nil}
	switch {
	case row != nil && row.DueBasis != nil && *row.DueBasis == eff.basis():
		v.NextDue = row.NextDueAt
	default:
		// Not planned yet (a new account, or defaults changed since): what
		// the next tick will compute.
		var last *time.Time
		if row != nil {
			last = row.LastPeriodEnd
		}
		if v.NextDue, err = nextDueOrLog(ctx, s.o.Logger, eff, user, last, s.o.Now()); err != nil {
			return View{}, err
		}
	}
	if row != nil {
		v.LastSentAt, v.LastOutcomeAt = row.LastSentAt, row.LastOutcomeAt
		if row.LastOutcome != nil {
			v.LastOutcome = *row.LastOutcome
		}
	}
	sc, err := s.scopeOf(ctx, user, u.Role)
	if err != nil {
		return View{}, err
	}
	n, err := visibleJobs(ctx, q, sc)
	if err != nil {
		return View{}, err
	}
	v.Jobs = int(n)
	return v, nil
}

// checkSettings validates in field by field.
func checkSettings(in Settings) error {
	if !in.Frequency.Valid() {
		return serviceutil.FieldErr("frequency", "choose daily, every other day, weekly, monthly or never")
	}
	if in.Hour < 0 || in.Hour > 23 {
		return serviceutil.FieldErr("hour", "the hour must be 0 to 23")
	}
	if in.Weekday < 1 || in.Weekday > 7 {
		return serviceutil.FieldErr("weekday", "the weekday must be 1 (Monday) to 7 (Sunday)")
	}
	if in.MonthDay < 1 || in.MonthDay > MaxMonthDay {
		return serviceutil.FieldErr("monthDay", "the day of the month must be 1 to %d", MaxMonthDay)
	}
	if err := in.Validate(); err != nil {
		return serviceutil.FieldErr("timezone", "unknown time zone; use an IANA name such as Europe/Amsterdam or UTC")
	}
	return nil
}

// SetSettings stores the caller's own settings (all fields; weekday and
// month day are kept for the other frequencies) and their next due time.
// Leaving "never" starts afresh: the first summary covers one period, not
// the time the summary was off.
func (s *Service) SetSettings(ctx context.Context, in Settings) (View, error) {
	p, err := s.who(ctx, authz.OpSummaryEdit)
	if err != nil {
		return View{}, err
	}
	if err := checkSettings(in); err != nil {
		return View{}, err
	}
	var out View
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		if _, err := q.GetNotifiableUser(ctx, p.UserID); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return fmt.Errorf("summary: load account: %w", err)
		}
		row, err := loadRow(ctx, q, p.UserID, true)
		if err != nil {
			return err
		}
		prev, _ := effective(row, s.o.Defaults)
		var last *time.Time
		if row != nil && prev.Frequency != Never && in.Frequency != Never {
			last = row.LastPeriodEnd
		}
		next, err := nextDueOrLog(ctx, s.o.Logger, in, p.UserID, last, s.o.Now())
		if err != nil {
			return err
		}
		f := sqlc.SummaryFrequency(in.Frequency)
		h, wd, md := int16(in.Hour), int16(in.Weekday), int16(in.MonthDay) //nolint:gosec // G115: validated ranges (0-23, 1-7, 1-28)
		if err := q.UpsertSummarySettings(ctx, sqlc.UpsertSummarySettingsParams{
			UserID: p.UserID, Frequency: &f, Hour: &h, Weekday: &wd, MonthDay: &md, Timezone: in.Timezone,
			DueBasis: in.basis(), NextDueAt: next, LastPeriodEnd: last,
		}); err != nil {
			return fmt.Errorf("summary: store settings: %w", err)
		}
		out, err = s.view(ctx, q, p.UserID)
		return err
	})
	return out, err
}

// SendPreview emits the caller's summary of the last full period up to now
// (marked as a preview; inbox and, with a verified address, e-mail
// through the normal pipeline: preferences and the flood rule apply).
// Rate limited per user; an account that can see no job gets a field
// error instead of an empty summary.
func (s *Service) SendPreview(ctx context.Context) (Preview, error) {
	p, err := s.who(ctx, authz.OpSummaryPreview)
	if err != nil {
		return Preview{}, err
	}
	release, err := s.limiter.Acquire("summary_preview:" + p.UserID.String())
	if err != nil {
		return Preview{}, err
	}
	defer release()
	q := sqlc.New(s.o.DB)
	u, err := q.GetNotifiableUser(ctx, p.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Preview{}, ErrNotFound
	}
	if err != nil {
		return Preview{}, fmt.Errorf("summary: load account: %w", err)
	}
	sc, err := s.scopeOf(ctx, u.ID, u.Role)
	if err != nil {
		return Preview{}, err
	}
	n, err := visibleJobs(ctx, q, sc)
	if err != nil {
		return Preview{}, err
	}
	if n == 0 {
		return Preview{}, serviceutil.FieldErr("preview", "you can see no backup jobs, so there is nothing to summarize")
	}
	row, err := loadRow(ctx, q, u.ID, false)
	if err != nil {
		return Preview{}, err
	}
	eff, _ := effective(row, s.o.Defaults)
	now := s.o.Now().Truncate(time.Second)
	start := previewStart(eff, now)
	if _, err := notify.Emit(ctx, q, notify.Event{
		Type: notify.SummaryPeriodic, Subject: notify.Subject{UserID: u.ID},
		Detail: eventDetail(start, now, eff.Timezone, eff.Frequency, true, false),
	}); err != nil {
		return Preview{}, err
	}
	return Preview{PeriodStart: start, PeriodEnd: now, EmailVerified: u.EmailVerifiedAt != nil}, nil
}

// previewStart is the start of the preview: one full period before now
// in the user's zone (periodBefore; a week for "never"), so the preview
// shows "the last 24 hours / 7 days up to now". It never reads or moves
// last_period_end.
func previewStart(eff Settings, now time.Time) time.Time {
	f := eff.Frequency
	if f == Never {
		f = Weekly
	}
	loc, err := scheduler.LoadZone(eff.Timezone)
	if err != nil {
		loc = time.UTC
	}
	return periodBefore(now, f, loc)
}
