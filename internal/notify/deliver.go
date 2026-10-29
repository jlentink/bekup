package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/notify/email"
	"github.com/jlentink/bekup/internal/notify/webhook"
	"github.com/jlentink/bekup/internal/queue"
)

// DeliverWorker works notify_deliver jobs.
type DeliverWorker struct {
	river.WorkerDefaults[queue.NotifyDeliverArgs]
	s *Service
}

// Work implements river.Worker: a retryable failure returns an error
// (River's backoff) or snoozes for the receiver's Retry-After; the
// delivery's own attempt counter caps the attempts at MaxAttemptsNotify.
func (w *DeliverWorker) Work(ctx context.Context, job *river.Job[queue.NotifyDeliverArgs]) error {
	final := job.Attempt >= job.MaxAttempts
	retryAfter, err := w.s.Deliver(ctx, job.Args.DeliveryID, final)
	if err != nil {
		return err
	}
	if retryAfter > 0 {
		return river.JobSnooze(retryAfter)
	}
	return nil
}

// errRetry asks River to retry the delivery with its backoff.
var errRetry = errors.New("notify: delivery attempt failed; will retry")

// Webhook AAD fields (D-086): the sealed URL and HMAC key of a channel row.
const (
	FieldURL     = "url"
	FieldHMACKey = "hmac_key"
)

// ChannelAAD is the AAD of a sealed field of a user_channels or
// shared_channels row.
func ChannelAAD(table string, id uuid.UUID, field string) crypto.AAD {
	return crypto.AAD{Table: table, RowID: id.String(), Field: field}
}

// target is a resolved delivery target.
type target struct {
	to      []string // e-mail
	url     []byte   // webhook (sealed fields opened)
	key     []byte
	table   string // user_channels | shared_channels for webhooks
	channel uuid.UUID
	owner   uuid.UUID // user channel owner
	shared  bool
}

func (t *target) zero() {
	crypto.Zero(t.url)
	crypto.Zero(t.key)
}

// outcome is the classified result of one attempt.
type outcome struct {
	status     sqlc.NotificationDeliveryStatus // sent, failed, skipped or queued (retry)
	code       string
	message    string
	retryAfter time.Duration
	webhook    *webhook.Result
	aborted    bool
}

// Deliver performs one attempt of a queued delivery after re-checking the
// recipient (active, still allowed to see the subject, verified address,
// channel enabled and attached). final marks River's last attempt. It
// returns a positive duration to snooze (Retry-After) or errRetry.
func (s *Service) Deliver(ctx context.Context, id uuid.UUID, final bool) (time.Duration, error) {
	q := sqlc.New(s.o.DB)
	d, err := q.GetNotificationDelivery(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("notify: load delivery: %w", err)
	}
	if d.Status != sqlc.NotificationDeliveryStatusQueued {
		return 0, nil
	}
	ev, err := q.GetNotificationEvent(ctx, d.EventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, s.finish(ctx, &d, outcome{status: sqlc.NotificationDeliveryStatusSkipped, code: CodeEventGone}, final)
	}
	if err != nil {
		return 0, fmt.Errorf("notify: load event: %w", err)
	}
	m, ok := Lookup(EventType(ev.Type))
	if !ok {
		return 0, s.finish(ctx, &d, outcome{status: sqlc.NotificationDeliveryStatusSkipped, code: CodeEventGone}, final)
	}
	t, skip, err := s.resolveTarget(ctx, q, &d, &ev, m)
	if err != nil {
		return 0, err
	}
	if skip != "" {
		return 0, s.finish(ctx, &d, outcome{status: sqlc.NotificationDeliveryStatusSkipped, code: skip}, final)
	}
	defer t.zero()
	n, err := s.loadNames(ctx, q, &ev)
	if err != nil {
		return 0, err
	}
	c := s.render(&ev, m, n)
	var out outcome
	if Channel(d.Channel) == ChannelEmail {
		out = s.sendEmail(ctx, &d, &ev, c, n, t)
	} else {
		out = s.sendWebhook(ctx, &d, &ev, c, t)
	}
	if out.aborted {
		// Shutdown or timeout: says nothing about the receiver.
		return 0, ctx.Err()
	}
	status, err := s.record(context.WithoutCancel(ctx), &d, t, out, final)
	if err != nil {
		return 0, err
	}
	if status == sqlc.NotificationDeliveryStatusQueued {
		if out.retryAfter > 0 {
			return out.retryAfter, nil
		}
		return 0, errRetry
	}
	return 0, nil
}

// resolveTarget re-checks the recipient at delivery time and opens the
// channel. A non-empty skip code means: do not send.
func (s *Service) resolveTarget(ctx context.Context, q *sqlc.Queries, d *sqlc.NotificationDelivery, ev *sqlc.NotificationEvent, m Meta) (*target, string, error) {
	t := &target{}
	if d.UserID != nil {
		u, err := q.GetNotifiableUser(ctx, *d.UserID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, CodeRecipientInactive, nil
		}
		if err != nil {
			return nil, "", fmt.Errorf("notify: load recipient: %w", err)
		}
		ok, err := s.allowed(ctx, u.ID, u.Role, m, ev)
		if err != nil {
			return nil, "", err
		}
		if !ok {
			return nil, CodeAccessRevoked, nil
		}
		if Channel(d.Channel) == ChannelEmail {
			if u.EmailVerifiedAt == nil {
				return nil, CodeEmailUnverified, nil
			}
			t.to = []string{u.Email}
			return t, "", nil
		}
		if d.UserChannelID == nil {
			return nil, CodeChannelDisabled, nil
		}
		ch, err := q.GetUserChannel(ctx, *d.UserChannelID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, CodeChannelDisabled, nil
		}
		if err != nil {
			return nil, "", fmt.Errorf("notify: load channel: %w", err)
		}
		if !ch.Enabled || ch.UserID != u.ID {
			return nil, CodeChannelDisabled, nil
		}
		t.table, t.channel, t.owner = "user_channels", ch.ID, ch.UserID
		if err := s.openWebhook(t, ch.UrlEnc, ch.HmacKeyEnc); err != nil {
			return nil, "", err
		}
		return t, "", nil
	}
	if d.SharedChannelID == nil {
		return nil, CodeChannelDisabled, nil
	}
	ch, err := q.GetSharedChannel(ctx, *d.SharedChannelID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, CodeChannelDisabled, nil
	case err != nil:
		return nil, "", fmt.Errorf("notify: load shared channel: %w", err)
	case !ch.Enabled:
		return nil, CodeChannelDisabled, nil
	}
	if ev.JobID == nil {
		return nil, CodeChannelDetached, nil
	}
	f, err := q.GetJobSharedChannelFilter(ctx, sqlc.GetJobSharedChannelFilterParams{JobID: *ev.JobID, ChannelID: ch.ID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, CodeChannelDetached, nil
	case err != nil:
		return nil, "", fmt.Errorf("notify: load shared channel filter: %w", err)
	case !sharedFilterMatches(m, f.Groups, f.Events):
		return nil, CodeChannelDetached, nil
	}
	t.shared, t.channel = true, ch.ID
	if ch.Type == sqlc.SharedChannelTypeEmailList {
		if t.to = emailListAddresses(ch.Config); len(t.to) == 0 {
			return nil, CodeChannelDisabled, nil
		}
		return t, "", nil
	}
	t.table = "shared_channels"
	if err := s.openWebhook(t, ch.UrlEnc, ch.HmacKeyEnc); err != nil {
		return nil, "", err
	}
	return t, "", nil
}

// EmailListConfig is the config of an email_list shared channel.
type EmailListConfig struct {
	Addresses []string `json:"addresses"`
}

// emailListAddresses decodes the addresses of an email_list channel; an
// unreadable config yields none (the delivery is skipped).
func emailListAddresses(raw []byte) []string {
	var cfg EmailListConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil
	}
	return cfg.Addresses
}

// WebhookConfig is the config of a webhook shared channel (non-secret).
type WebhookConfig struct {
	DisplayURL string `json:"display_url"`
}

func (s *Service) openWebhook(t *target, urlEnc, keyEnc []byte) error {
	u, err := s.o.Keyring.Open(urlEnc, ChannelAAD(t.table, t.channel, FieldURL))
	if err != nil {
		return fmt.Errorf("notify: open webhook url: %w", err)
	}
	k, err := s.o.Keyring.Open(keyEnc, ChannelAAD(t.table, t.channel, FieldHMACKey))
	if err != nil {
		crypto.Zero(u)
		return fmt.Errorf("notify: open webhook key: %w", err)
	}
	t.url, t.key = u, k
	return nil
}

func (s *Service) sendEmail(ctx context.Context, d *sqlc.NotificationDelivery, ev *sqlc.NotificationEvent, c Content, n names, t *target) outcome {
	if s.o.Mailer == nil {
		return outcome{status: sqlc.NotificationDeliveryStatusSkipped, code: CodeEmailUnavailable}
	}
	var content email.Content
	var err error
	if d.Summary {
		content, err = s.floodEmail(s.CurrentLimits().MaxPerUserPerHour)
	} else {
		content, err = emailFor(ev, c, n)
	}
	if err != nil {
		return outcome{status: sqlc.NotificationDeliveryStatusFailed, code: string(email.CodeInvalidMessage), message: email.CodeInvalidMessage.Message()}
	}
	err = s.o.Mailer.Send(ctx, email.NewMessage(t.to, content))
	if err == nil {
		return outcome{status: sqlc.NotificationDeliveryStatusSent}
	}
	if ctx.Err() != nil {
		return outcome{aborted: true}
	}
	var se *email.SendError
	if !errors.As(err, &se) {
		return outcome{status: sqlc.NotificationDeliveryStatusQueued, code: string(email.CodeFailed), message: email.CodeFailed.Message()}
	}
	o := outcome{code: string(se.Code), message: se.Code.Message()}
	switch se.Code {
	case email.CodeNotConfigured:
		o.status, o.code = sqlc.NotificationDeliveryStatusSkipped, CodeEmailNotConfigured
	case email.CodeUnreachable, email.CodeFailed:
		o.status = sqlc.NotificationDeliveryStatusQueued
	default:
		o.status = sqlc.NotificationDeliveryStatusFailed
	}
	return o
}

var webhookMessages = map[webhook.Code]string{
	webhook.CodeHTTPStatus:  "the receiver answered with an error status",
	webhook.CodeRedirect:    "the receiver answered with a redirect, which is never followed",
	webhook.CodeNotAllowed:  "the receiver's address is not allowed by the SSRF guard",
	webhook.CodeTLS:         "the TLS handshake with the receiver failed",
	webhook.CodeUnreachable: "the receiver could not be reached",
	webhook.CodeFailed:      "the delivery failed",
	webhook.CodeInvalid:     "the delivery was invalid",
}

func (s *Service) sendWebhook(ctx context.Context, d *sqlc.NotificationDelivery, ev *sqlc.NotificationEvent, c Content, t *target) outcome {
	// Personal webhooks go through their own guard (D-198).
	sender := s.o.Webhooks
	if !t.shared {
		sender = s.o.PersonalWebhooks
	}
	if sender == nil {
		return outcome{status: sqlc.NotificationDeliveryStatusSkipped, code: CodeWebhookUnavailable}
	}
	typ, at, data := ev.Type, ev.CreatedAt, any(c.Data)
	if d.Summary {
		typ, at, data = FloodSummaryType, s.o.Now(), s.floodData(s.CurrentLimits().MaxPerUserPerHour)
	}
	body, err := webhook.BuildPayload(ev.ID.String(), typ, at, data)
	if err != nil {
		return outcome{status: sqlc.NotificationDeliveryStatusFailed, code: string(webhook.CodeInvalid), message: webhookMessages[webhook.CodeInvalid]}
	}
	res := sender.Send(ctx, webhook.Delivery{
		URL: string(t.url), HMACKey: t.key, EventID: ev.ID.String(), EventType: typ,
		DeliveryID: d.ID.String(), Payload: body,
	})
	o := outcome{code: string(res.Code), webhook: &res}
	switch res.Outcome {
	case webhook.OutcomeSuccess:
		o.status = sqlc.NotificationDeliveryStatusSent
	case webhook.OutcomeRetryable:
		o.status, o.retryAfter = sqlc.NotificationDeliveryStatusQueued, res.RetryAfter
	case webhook.OutcomeAborted:
		o.aborted = true
	default:
		o.status = sqlc.NotificationDeliveryStatusFailed
	}
	if o.status != sqlc.NotificationDeliveryStatusSent {
		o.message = webhookMessages[res.Code]
	}
	return o
}

// record persists an attempt: the delivery (a retry that ran out of
// attempts fails), the webhook failure counters and, when the failure
// policy says so, auto-disables the webhook and tells its owner (or the
// admins for a shared channel) in the inbox.
// It returns the stored status (queued: retry).
func (s *Service) record(ctx context.Context, d *sqlc.NotificationDelivery, t *target, o outcome, final bool) (sqlc.NotificationDeliveryStatus, error) {
	now := s.o.Now()
	var status sqlc.NotificationDeliveryStatus
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		cur, err := q.GetNotificationDeliveryForUpdate(ctx, d.ID)
		if err != nil {
			return fmt.Errorf("notify: lock delivery: %w", err)
		}
		if cur.Status != sqlc.NotificationDeliveryStatusQueued {
			status = cur.Status
			return nil
		}
		attempts := cur.Attempts + 1
		if o.status == sqlc.NotificationDeliveryStatusQueued && (final || attempts >= queue.MaxAttemptsNotify) {
			o.status = sqlc.NotificationDeliveryStatusFailed
		}
		status = o.status
		if err := s.update(ctx, q, cur.ID, attempts, o, now); err != nil {
			return err
		}
		if o.webhook != nil && t != nil && t.table != "" {
			return s.recordWebhook(ctx, q, t, *o.webhook, now)
		}
		return nil
	})
	return status, err
}

func (s *Service) update(ctx context.Context, q *sqlc.Queries, id uuid.UUID, attempts int32, o outcome, now time.Time) error {
	p := sqlc.UpdateNotificationDeliveryParams{ID: id, Status: o.status, Attempts: attempts}
	if o.code != "" && o.code != string(webhook.CodeOK) {
		p.LastCode = &o.code
	}
	if o.message != "" {
		msg := clampRunes(o.message, 300)
		p.LastError = &msg
	}
	if o.status == sqlc.NotificationDeliveryStatusSent {
		p.SentAt = &now
	}
	if err := q.UpdateNotificationDelivery(ctx, p); err != nil {
		return fmt.Errorf("notify: update delivery: %w", err)
	}
	return nil
}

// finish records a delivery that was not attempted.
func (s *Service) finish(ctx context.Context, d *sqlc.NotificationDelivery, o outcome, _ bool) error {
	return db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		cur, err := q.GetNotificationDeliveryForUpdate(ctx, d.ID)
		if err != nil {
			return fmt.Errorf("notify: lock delivery: %w", err)
		}
		if cur.Status != sqlc.NotificationDeliveryStatusQueued {
			return nil
		}
		return s.update(ctx, q, cur.ID, cur.Attempts, o, s.o.Now())
	})
}

func stateOf(consecutive, permanent int32, first, last *time.Time, code *string) webhook.FailureState {
	st := webhook.FailureState{ConsecutiveFailures: int(consecutive), PermanentFailures: int(permanent)}
	if first != nil {
		st.FirstFailureAt = *first
	}
	if last != nil {
		st.LastFailureAt = *last
	}
	if code != nil {
		st.LastCode = webhook.Code(*code)
	}
	return st
}

func optTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (s *Service) recordWebhook(ctx context.Context, q *sqlc.Queries, t *target, res webhook.Result, now time.Time) error {
	var st webhook.FailureState
	var name string
	if t.shared {
		ch, err := q.GetSharedChannelForUpdate(ctx, t.channel)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("notify: lock shared channel: %w", err)
		}
		if !ch.Enabled {
			return nil
		}
		st, name = stateOf(ch.ConsecutiveFailures, ch.PermanentFailures, ch.FirstFailureAt, ch.LastFailureAt, ch.LastFailureCode), ch.Name
	} else {
		ch, err := q.GetUserChannelForUpdate(ctx, t.channel)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("notify: lock channel: %w", err)
		}
		if !ch.Enabled {
			return nil
		}
		st, name = stateOf(ch.ConsecutiveFailures, ch.PermanentFailures, ch.FirstFailureAt, ch.LastFailureAt, ch.LastFailureCode), ch.Name
	}
	dec := s.CurrentLimits().FailurePolicy.Record(st, res, now)
	ns := dec.State
	var code, reason *string
	if ns.LastCode != "" {
		c := string(ns.LastCode)
		code = &c
	}
	if dec.Disable {
		r := string(dec.Reason)
		reason = &r
	}
	if t.shared {
		if err := q.RecordSharedChannelResult(ctx, sqlc.RecordSharedChannelResultParams{
			ID: t.channel, ConsecutiveFailures: int32(min(max(ns.ConsecutiveFailures, 0), 1<<30)), PermanentFailures: int32(min(max(ns.PermanentFailures, 0), 1<<30)),
			FirstFailureAt: optTime(ns.FirstFailureAt), LastFailureAt: optTime(ns.LastFailureAt), LastFailureCode: code,
			Disable: dec.Disable, At: now, DisabledReason: reason,
		}); err != nil {
			return fmt.Errorf("notify: record shared channel result: %w", err)
		}
	} else if err := q.RecordUserChannelResult(ctx, sqlc.RecordUserChannelResultParams{
		ID: t.channel, ConsecutiveFailures: int32(min(max(ns.ConsecutiveFailures, 0), 1<<30)), PermanentFailures: int32(min(max(ns.PermanentFailures, 0), 1<<30)),
		FirstFailureAt: optTime(ns.FirstFailureAt), LastFailureAt: optTime(ns.LastFailureAt), LastFailureCode: code,
		Disable: dec.Disable, At: now, DisabledReason: reason,
	}); err != nil {
		return fmt.Errorf("notify: record channel result: %w", err)
	}
	if !dec.Disable {
		return nil
	}
	s.o.Logger.WarnContext(ctx, "webhook auto-disabled",
		slog.String("channel_id", t.channel.String()), slog.Bool("shared", t.shared), slog.String("reason", string(dec.Reason)))
	return s.disabledNotice(ctx, q, t, displayName(name), dec.Reason)
}

// disabledNotice writes the inbox notice of an auto-disabled webhook: to
// its owner, or to every active admin for a shared channel.
func (s *Service) disabledNotice(ctx context.Context, q *sqlc.Queries, t *target, name string, reason webhook.DisableReason) error {
	users := []uuid.UUID{t.owner}
	link := s.link("/profile/notifications")
	if t.shared {
		admins, err := q.ListActiveAdminIDs(ctx)
		if err != nil {
			return fmt.Errorf("notify: list admins: %w", err)
		}
		users, link = admins, s.link("/admin/channels")
	}
	payload, err := json.Marshal(map[string]any{
		"channel_id": t.channel.String(), "shared": t.shared, "reason": string(reason),
	})
	if err != nil {
		return err
	}
	for _, u := range slices.Compact(users) {
		if u == uuid.Nil {
			continue
		}
		if err := s.insertInbox(ctx, q, sqlc.InsertInboxNotificationParams{
			UserID: u, Type: NoticeWebhookDisabled, Severity: string(SeverityWarning),
			Title: clampRunes("Webhook disabled: "+name, 200), Body: clampRunes(reason.Message(), 1000),
			Link: &link, Payload: payload,
		}); err != nil {
			return err
		}
	}
	return nil
}
