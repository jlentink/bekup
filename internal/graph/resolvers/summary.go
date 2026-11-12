package resolvers

import (
	"context"
	"errors"

	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/notify/summary"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// Periodic summary resolvers (D-226): the caller's own settings and the
// preview. Everything is scoped to the caller inside internal/notify/summary.

var errSummaryNotConfigured = errors.New("resolvers: periodic summary not configured")

func (r *Resolver) summaries() (*summary.Service, error) {
	if r.Summary == nil {
		return nil, errSummaryNotConfigured
	}
	return r.Summary, nil
}

// summaryError maps service errors to API errors.
func summaryError(err error) error {
	var (
		fe *serviceutil.FieldError
		rl *serviceutil.RateLimitedError
	)
	switch {
	case err == nil:
		return nil
	case errors.As(err, &fe):
		return apierror.InvalidMsg(fe.Field, fe.Msg)
	case errors.As(err, &rl):
		return apierror.Newf(apierror.RateLimited, "too many summary previews; try again in %d seconds",
			httpserver.RetryAfterSeconds(rl.RetryAfter))
	case errors.Is(err, serviceutil.ErrBusy):
		return errBusy
	case errors.Is(err, summary.ErrNotFound):
		return errNotificationNotFound
	default:
		return err
	}
}

var summaryFrequencies = map[summary.Frequency]model.SummaryFrequency{
	summary.Daily:         model.SummaryFrequencyDaily,
	summary.EveryOtherDay: model.SummaryFrequencyEveryOtherDay,
	summary.Weekly:        model.SummaryFrequencyWeekly,
	summary.Monthly:       model.SummaryFrequencyMonthly,
	summary.Never:         model.SummaryFrequencyNever,
}

func summarySettingsModel(s summary.Settings) *model.SummarySettings {
	f, ok := summaryFrequencies[s.Frequency]
	if !ok {
		f = model.SummaryFrequencyNever
	}
	return &model.SummarySettings{Frequency: f, Hour: s.Hour, Weekday: s.Weekday, MonthDay: s.MonthDay, Timezone: s.Timezone}
}

func summaryViewModel(v *summary.View) *model.MySummarySettings {
	out := &model.MySummarySettings{
		Settings: summarySettingsModel(v.Settings), Custom: v.Custom, Defaults: summarySettingsModel(v.Defaults),
		NextSummaryAt: v.NextDue, LastSentAt: v.LastSentAt, LastOutcomeAt: v.LastOutcomeAt,
		EmailVerified: v.EmailVerified, VisibleJobs: v.Jobs,
	}
	if v.LastOutcome != "" {
		o := v.LastOutcome
		out.LastOutcome = &o
	}
	return out
}

func (r *Resolver) mySummarySettings(ctx context.Context) (*model.MySummarySettings, error) {
	svc, err := r.summaries()
	if err != nil {
		return nil, err
	}
	v, err := svc.MySettings(ctx)
	if err != nil {
		return nil, summaryError(err)
	}
	return summaryViewModel(&v), nil
}

func (r *Resolver) setSummarySettings(ctx context.Context, in model.SummarySettingsInput) (*model.MySummarySettings, error) {
	svc, err := r.summaries()
	if err != nil {
		return nil, err
	}
	f := summary.Frequency("")
	for k, m := range summaryFrequencies {
		if m == in.Frequency {
			f = k
		}
	}
	v, err := svc.SetSettings(ctx, summary.Settings{
		Frequency: f, Hour: in.Hour, Weekday: in.Weekday, MonthDay: in.MonthDay, Timezone: in.Timezone,
	})
	if err != nil {
		return nil, summaryError(err)
	}
	return summaryViewModel(&v), nil
}

func (r *Resolver) sendSummaryPreview(ctx context.Context) (*model.SummaryPreviewResult, error) {
	svc, err := r.summaries()
	if err != nil {
		return nil, err
	}
	p, err := svc.SendPreview(ctx)
	if err != nil {
		return nil, summaryError(err)
	}
	return &model.SummaryPreviewResult{PeriodStart: p.PeriodStart, PeriodEnd: p.PeriodEnd, EmailVerified: p.EmailVerified}, nil
}
