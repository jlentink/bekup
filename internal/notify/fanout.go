package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/queue"
)

// Skip codes of deliveries that were not attempted.
const (
	CodeEmailUnverified    = "email_unverified"
	CodeEmailUnavailable   = "email_unavailable"
	CodeEmailNotConfigured = "email_not_configured"
	CodeWebhookUnavailable = "webhook_unavailable"
	CodeFloodCollapsed     = "flood_collapsed"
	CodeRecipientInactive  = "recipient_inactive"
	CodeAccessRevoked      = "access_revoked"
	CodeChannelDisabled    = "channel_disabled"
	CodeChannelDetached    = "channel_detached"
	CodeEventGone          = "event_gone"
	CodeAttemptsExhausted  = "attempts_exhausted"
)

// FanoutWorker works notify_fanout jobs.
type FanoutWorker struct {
	river.WorkerDefaults[queue.NotifyFanoutArgs]
	s *Service
}

// Work implements river.Worker.
func (w *FanoutWorker) Work(ctx context.Context, job *river.Job[queue.NotifyFanoutArgs]) error {
	return w.s.FanOut(ctx, job.Args.EventID)
}

// recipient is an active account that may receive an event.
type recipient struct {
	id       uuid.UUID
	role     sqlc.UserRole
	verified bool
}

// deliverySession is the placeholder session id of the principals built
// for delivery-time access checks (never a real session).
var deliverySession = uuid.MustParse("00000000-0000-4000-8000-00000000d1e1")

// Allowed reports whether the active account (id, role) may currently see
// the subject of an event of m: the authz policy decides with the event's
// operation (and the subject job for job events); own-account events go
// only to their subject. Denials are false; lookup failures are errors.
func (s *Service) allowed(ctx context.Context, id uuid.UUID, role sqlc.UserRole, m Meta, ev *sqlc.NotificationEvent) (bool, error) {
	if m.Audience == AudienceOwnAccount && (ev.UserID == nil || *ev.UserID != id) {
		return false, nil
	}
	var res []authz.Resource
	if m.Subject == SubjectJob {
		if ev.JobID == nil {
			return false, nil
		}
		res = append(res, authz.Job{ID: *ev.JobID})
	}
	// A synthetic principal for the policy decision only: the recipient's
	// current role from the database, no session (a placeholder session id
	// makes it valid), no step-up and no restrictions; the view
	// operations of the catalog need no freshness.
	pctx := auth.WithPrincipal(ctx, auth.Principal{UserID: id, Role: auth.Role(role), SessionID: deliverySession})
	err := s.o.Policy.Authorize(pctx, m.Op, res...)
	if err == nil {
		return true, nil
	}
	var ae *apierror.Error
	if errors.As(err, &ae) {
		return false, nil
	}
	return false, fmt.Errorf("notify: authorize recipient: %w", err)
}

// recipients returns the accounts that may see the event now.
func (s *Service) recipients(ctx context.Context, q *sqlc.Queries, ev *sqlc.NotificationEvent, m Meta) ([]recipient, error) {
	var rows []sqlc.ListNotifiableUsersRow
	if m.Audience == AudienceOwnAccount {
		if ev.UserID == nil {
			return nil, nil
		}
		u, err := q.GetNotifiableUser(ctx, *ev.UserID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("notify: load user: %w", err)
		}
		rows = append(rows, sqlc.ListNotifiableUsersRow(u))
	} else {
		var err error
		if rows, err = q.ListNotifiableUsers(ctx); err != nil {
			return nil, fmt.Errorf("notify: list users: %w", err)
		}
	}
	var out []recipient
	for _, u := range rows {
		ok, err := s.allowed(ctx, u.ID, u.Role, m, ev)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, recipient{id: u.ID, role: u.Role, verified: u.EmailVerifiedAt != nil})
		}
	}
	return out, nil
}

// FanOut resolves the recipients of one event and writes, in one
// transaction: an inbox row per recipient whose inbox preference is on,
// one delivery (and River job) per (recipient, e-mail / webhook channel)
// and per matching shared channel of the job, then marks the event done.
// Re-running it never duplicates anything (unique inbox and idempotency
// keys, fanned_out_at).
func (s *Service) FanOut(ctx context.Context, eventID uuid.UUID) error {
	ins, err := s.inserter()
	if err != nil {
		return err
	}
	return db.TxWith(ctx, s.o.DB, func(tx pgx.Tx, q *sqlc.Queries) error {
		ev, err := q.GetNotificationEventForUpdate(ctx, eventID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("notify: load event: %w", err)
		}
		if ev.FannedOutAt != nil {
			return nil
		}
		now := s.o.Now()
		m, ok := Lookup(EventType(ev.Type))
		if !ok {
			s.o.Logger.WarnContext(ctx, "notification event of unknown type dropped", slog.String("event_id", ev.ID.String()))
			return q.MarkEventFannedOut(ctx, sqlc.MarkEventFannedOutParams{ID: ev.ID, At: now})
		}
		if collapsed, err := s.collapse(ctx, q, &ev, m, now); err != nil || collapsed {
			return err
		}
		n, err := s.loadNames(ctx, q, &ev)
		if err != nil {
			return err
		}
		c := s.render(&ev, m, n)
		payload, err := json.Marshal(c.Data)
		if err != nil {
			return fmt.Errorf("notify: payload: %w", err)
		}
		rcpts, err := s.recipients(ctx, q, &ev, m)
		if err != nil {
			return err
		}
		ids := make([]uuid.UUID, len(rcpts))
		for i, r := range rcpts {
			ids[i] = r.id
		}
		prefRows, err := q.ListNotificationPrefs(ctx, sqlc.ListNotificationPrefsParams{UserIds: ids, Event: ev.Type, JobID: ev.JobID})
		if err != nil {
			return fmt.Errorf("notify: load preferences: %w", err)
		}
		prefs := prefsByUser(prefRows)
		hooks, err := q.ListEnabledUserWebhooks(ctx, ids)
		if err != nil {
			return fmt.Errorf("notify: load webhooks: %w", err)
		}
		hooksByUser := map[uuid.UUID][]uuid.UUID{}
		for _, h := range hooks {
			hooksByUser[h.UserID] = append(hooksByUser[h.UserID], h.ID)
		}
		f := fanout{s: s, ctx: ctx, tx: tx, q: q, ins: ins, ev: &ev, m: m, now: now}
		for _, r := range rcpts {
			// Serializes the flood counters of the user (users in id
			// order: no lock-order inversion between fan-outs).
			if err := q.NotifyAdvisoryLock(ctx, "bekup.notify.user:"+r.id.String()); err != nil {
				return fmt.Errorf("notify: lock user: %w", err)
			}
			up := prefs[r.id]
			if Enabled(m, ChannelInbox, ev.JobID, up) {
				link := &c.Link
				if c.Link == "" {
					link = nil
				}
				if err := s.insertInbox(ctx, q, sqlc.InsertInboxNotificationParams{
					UserID: r.id, EventID: &ev.ID, Type: ev.Type, Severity: ev.Severity,
					Title: c.Title, Body: c.Body, Link: link, Payload: payload,
				}); err != nil {
					return err
				}
			}
			if Enabled(m, ChannelEmail, ev.JobID, up) {
				if err := f.userDelivery(r, ChannelEmail, nil); err != nil {
					return err
				}
			}
			if Enabled(m, ChannelWebhook, ev.JobID, up) {
				for _, h := range hooksByUser[r.id] {
					if err := f.userDelivery(r, ChannelWebhook, &h); err != nil {
						return err
					}
				}
			}
		}
		if ev.JobID != nil && m.Group != GroupSecurity {
			shared, err := q.ListJobSharedChannels(ctx, *ev.JobID)
			if err != nil {
				return fmt.Errorf("notify: load shared channels: %w", err)
			}
			for _, sc := range shared {
				if !sharedFilterMatches(m, sc.Groups, sc.Events) {
					continue
				}
				if err := f.sharedDelivery(sc); err != nil {
					return err
				}
			}
		}
		return q.MarkEventFannedOut(ctx, sqlc.MarkEventFannedOutParams{ID: ev.ID, At: now})
	})
}

// collapse folds a low-priority event (*.started) into the previous one of
// the same type and job within the collapse window.
func (s *Service) collapse(ctx context.Context, q *sqlc.Queries, ev *sqlc.NotificationEvent, m Meta, now time.Time) (bool, error) {
	if m.Priority != PriorityLow || ev.JobID == nil {
		return false, nil
	}
	if err := q.NotifyAdvisoryLock(ctx, "bekup.notify.collapse:"+ev.Type+":"+ev.JobID.String()); err != nil {
		return false, fmt.Errorf("notify: lock collapse: %w", err)
	}
	target, err := q.FindCollapseTarget(ctx, sqlc.FindCollapseTargetParams{
		Type: ev.Type, JobID: *ev.JobID, ID: ev.ID, Since: ev.CreatedAt.Add(-s.CurrentLimits().CollapseWindow), Until: ev.CreatedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("notify: find collapse target: %w", err)
	}
	if err := q.IncrementEventCollapsed(ctx, target); err != nil {
		return false, fmt.Errorf("notify: collapse: %w", err)
	}
	if err := q.CollapseEventInto(ctx, sqlc.CollapseEventIntoParams{ID: ev.ID, Target: &target, At: now}); err != nil {
		return false, fmt.Errorf("notify: collapse: %w", err)
	}
	return true, nil
}

// fanout is the state of one FanOut transaction.
type fanout struct {
	s   *Service
	ctx context.Context
	tx  pgx.Tx
	q   *sqlc.Queries
	ins Inserter
	ev  *sqlc.NotificationEvent
	m   Meta
	now time.Time
}

// userDelivery adds the delivery of one user channel: skipped (with a
// code) when e-mail is unverified or unavailable or the flood rule
// applies, else queued with its River job.
func (f *fanout) userDelivery(r recipient, ch Channel, userChannel *uuid.UUID) error {
	key := fmt.Sprintf("%s:u:%s:%s", f.ev.ID, r.id, ch)
	if userChannel != nil {
		key += ":" + userChannel.String()
	}
	p := sqlc.InsertNotificationDeliveryParams{
		EventID: f.ev.ID, UserID: &r.id, UserChannelID: userChannel, Channel: sqlc.NotificationChannel(ch),
		Status: sqlc.NotificationDeliveryStatusQueued, IdempotencyKey: key,
	}
	skip := ""
	switch {
	case ch == ChannelEmail && !r.verified:
		skip = CodeEmailUnverified
	case ch == ChannelEmail && f.s.o.Mailer == nil:
		skip = CodeEmailUnavailable
	case ch == ChannelWebhook && f.s.o.PersonalWebhooks == nil:
		skip = CodeWebhookUnavailable
	}
	if skip == "" && !f.m.Mandatory {
		n, err := f.q.CountRecentUserDeliveries(f.ctx, sqlc.CountRecentUserDeliveriesParams{
			UserID: &r.id, Channel: sqlc.NotificationChannel(ch), Since: f.now.Add(-FloodWindow), Exempt: MandatoryTypes(),
		})
		if err != nil {
			return fmt.Errorf("notify: count deliveries: %w", err)
		}
		limit := int64(f.s.CurrentLimits().MaxPerUserPerHour)
		switch {
		case n > limit:
			skip = CodeFloodCollapsed
		case n == limit:
			p.Summary = true
		}
	}
	if skip != "" {
		p.Status, p.LastCode = sqlc.NotificationDeliveryStatusSkipped, &skip
	}
	return f.insert(p)
}

func (f *fanout) sharedDelivery(sc sqlc.ListJobSharedChannelsRow) error {
	ch := ChannelWebhook
	if sc.Type == sqlc.SharedChannelTypeEmailList {
		ch = ChannelEmail
	}
	p := sqlc.InsertNotificationDeliveryParams{
		EventID: f.ev.ID, SharedChannelID: &sc.ID, Channel: sqlc.NotificationChannel(ch),
		Status: sqlc.NotificationDeliveryStatusQueued, IdempotencyKey: fmt.Sprintf("%s:s:%s", f.ev.ID, sc.ID),
	}
	skip := ""
	if ch == ChannelEmail && f.s.o.Mailer == nil {
		skip = CodeEmailUnavailable
	} else if ch == ChannelWebhook && f.s.o.Webhooks == nil {
		skip = CodeWebhookUnavailable
	}
	if skip != "" {
		p.Status, p.LastCode = sqlc.NotificationDeliveryStatusSkipped, &skip
	}
	return f.insert(p)
}

func (f *fanout) insert(p sqlc.InsertNotificationDeliveryParams) error {
	ids, err := f.q.InsertNotificationDelivery(f.ctx, p)
	if err != nil {
		return fmt.Errorf("notify: insert delivery: %w", err)
	}
	if len(ids) == 0 || p.Status != sqlc.NotificationDeliveryStatusQueued {
		return nil
	}
	if _, err := f.ins.InsertTx(f.ctx, f.tx, queue.NotifyDeliverArgs{DeliveryID: ids[0]}, queue.InsertOptions{}); err != nil {
		return err
	}
	return nil
}
