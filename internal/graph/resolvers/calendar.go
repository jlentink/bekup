package resolvers

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/calendar"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
)

// Job calendar (task 3.14, D-156). calendar.view admits every role; the
// jobs are limited to the caller's JobScope (owners: their own jobs) and
// requested ids outside it are dropped silently, so ids cannot be probed.

var errCalendarNotConfigured = errors.New("resolvers: calendar not configured")

func (r *Resolver) calendar(ctx context.Context, from, to time.Time, timezone string, jobIDs []uuid.UUID) (*model.Calendar, error) {
	if r.Calendar == nil {
		return nil, errCalendarNotConfigured
	}
	if err := r.Authz.Authorize(ctx, authz.OpCalendarView); err != nil {
		return nil, err
	}
	scope, uid, err := r.Authz.JobScope(ctx)
	if err != nil {
		return nil, err
	}
	var cs calendar.Scope
	switch scope {
	case authz.ScopeAll:
		cs.All = true
	case authz.ScopeOwned:
		cs.Owner = uid
	default:
		return nil, apierror.New(apierror.Forbidden, "forbidden")
	}
	c, err := r.Calendar.Build(ctx, cs, calendar.Request{From: from, To: to, Timezone: timezone, JobIDs: jobIDs})
	if ve := (*calendar.ValidationError)(nil); errors.As(err, &ve) {
		return nil, apierror.Invalid(ve.Field, ve.Message)
	}
	if err != nil {
		return nil, err
	}
	out := &model.Calendar{Events: make([]model.CalendarEvent, 0, len(c.Events)), Truncated: c.Truncated, From: c.From, To: c.To, Timezone: c.Timezone}
	for i := range c.Events {
		out.Events = append(out.Events, calendarEventModel(&c.Events[i]))
	}
	return out, nil
}

func calendarEventModel(e *calendar.Event) model.CalendarEvent {
	m := model.CalendarEvent{
		JobID: e.JobID, JobName: e.JobName, Kind: model.CalendarEventKind(strings.ToUpper(string(e.Kind))),
		Start: e.Start, End: e.End, RunID: e.RunID, DestinationID: e.DestinationID,
		Projected: e.Projected, Paused: e.Paused, StartedEarly: e.StartedEarly, Overdue: e.Overdue,
	}
	if e.Status != "" {
		st := model.RunStatus(strings.ToUpper(e.Status))
		m.Status = &st
	}
	if e.Trigger != "" {
		tr := model.RunTrigger(strings.ToUpper(e.Trigger))
		m.Trigger = &tr
	}
	if e.Detail != "" {
		d := e.Detail
		m.Detail = &d
	}
	return m
}
