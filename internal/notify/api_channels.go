package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/notify/email"
	"github.com/jlentink/bekup/internal/notify/webhook"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// Channel tables (AAD tables of the sealed fields and audit targets).
const (
	tableShared = "shared_channels"
	tableUser   = "user_channels"
)

// Limits of channel input.
const (
	maxChannelName = 100
	// MaxListAddresses caps the addresses of an e-mail list (one message
	// per delivery, email.MaxRecipients).
	MaxListAddresses = email.MaxRecipients
	// DisabledManual is the disabled_reason of a channel an admin turned
	// off.
	DisabledManual = "manual"
)

// ChannelState is the delivery health of a channel (webhook failure
// counters, auto-disable).
type ChannelState struct {
	Enabled             bool
	DisabledAt          *time.Time
	DisabledReason      string // manual | repeated_failures | permanent_failures
	DisabledMessage     string
	ConsecutiveFailures int
	PermanentFailures   int
	LastFailureAt       *time.Time
	LastFailureCode     string
}

func stateFrom(enabled bool, at *time.Time, reason *string, consecutive, permanent int32, lastAt *time.Time, code *string) ChannelState {
	st := ChannelState{
		Enabled: enabled, DisabledAt: at, ConsecutiveFailures: int(consecutive), PermanentFailures: int(permanent),
		LastFailureAt: lastAt,
	}
	if code != nil {
		st.LastFailureCode = *code
	}
	if !enabled && reason != nil {
		st.DisabledReason = *reason
		if *reason == DisabledManual {
			st.DisabledMessage = "Turned off by an administrator."
		} else {
			st.DisabledMessage = webhook.DisableReason(*reason).Message()
		}
	}
	return st
}

// Attachment is a shared channel attached to a job with its event filter
// (both lists empty: every non-security event of the job).
type Attachment struct {
	JobID   uuid.UUID
	JobName string
	Groups  []Group
	Events  []EventType
}

// SharedChannel is an admin-managed e-mail list or webhook. The webhook
// URL is only shown as its DisplayURL; the signing key never.
type SharedChannel struct {
	ID          uuid.UUID
	Name        string
	Type        sqlc.SharedChannelType
	Addresses   []string
	DisplayURL  string
	State       ChannelState
	Attachments []Attachment
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// SharedChannelInput creates a shared channel: an e-mail list (Addresses)
// or a webhook (URL).
type SharedChannelInput struct {
	Name      string
	Type      sqlc.SharedChannelType
	Addresses []string
	URL       string
}

// SharedChannelUpdate changes a shared channel; nil fields stay.
type SharedChannelUpdate struct {
	Name      *string
	Addresses []string // nil: keep
	URL       *string
}

// KeyedSharedChannel is a channel with its new signing key: returned once
// by CreateSharedChannel and RotateSharedChannelKey. The caller shows Key
// once and zeroes it.
type KeyedSharedChannel struct {
	Channel SharedChannel
	Key     []byte
}

func sharedFrom(c *sqlc.SharedChannel) SharedChannel {
	out := SharedChannel{
		ID: c.ID, Name: c.Name, Type: c.Type, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		State: stateFrom(c.Enabled, c.DisabledAt, c.DisabledReason, c.ConsecutiveFailures, c.PermanentFailures,
			c.LastFailureAt, c.LastFailureCode),
	}
	if c.Type == sqlc.SharedChannelTypeEmailList {
		out.Addresses = emailListAddresses(c.Config)
		if out.Addresses == nil {
			out.Addresses = []string{}
		}
	} else {
		var cfg WebhookConfig
		if json.Unmarshal(c.Config, &cfg) == nil {
			out.DisplayURL = cfg.DisplayURL
		}
	}
	return out
}

// checkName validates a channel name or label.
func checkName(field, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fieldErr(field, "is required")
	}
	if !cleanText(name, maxChannelName) {
		return "", fieldErr(field, "must be at most %d characters without control characters", maxChannelName)
	}
	return name, nil
}

// checkAddresses validates and de-duplicates the addresses of an e-mail
// list.
func checkAddresses(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, fieldErr("addresses", "at least one address is required")
	}
	if len(in) > MaxListAddresses {
		return nil, fieldErr("addresses", "at most %d addresses", MaxListAddresses)
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, a := range in {
		a = strings.TrimSpace(a)
		if err := email.CheckAddress(a); err != nil {
			return nil, fieldErr("addresses", "%s", err.Error())
		}
		if k := strings.ToLower(a); !seen[k] {
			seen[k] = true
			out = append(out, a)
		}
	}
	return out, nil
}

// checkURL validates a webhook URL (input time, D-177) against the guard
// of its scope (personal: the personal_webhooks guard, D-198) and returns
// its display form. Errors are fixed messages that never echo the URL.
func (a *API) checkURL(ctx context.Context, raw string, personal bool) (string, error) {
	guard := a.o.Guard
	if personal {
		guard = a.o.PersonalGuard
	}
	u, err := webhook.ValidateURL(ctx, raw, guard, webhook.URLOptions{AllowHTTP: a.o.AllowHTTP, Logger: a.o.Logger})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return "", err
		}
		var msg string
		switch {
		case errors.Is(err, webhook.ErrURLTooLong), errors.Is(err, webhook.ErrInsecureScheme),
			errors.Is(err, webhook.ErrUserinfo), errors.Is(err, webhook.ErrFragment),
			errors.Is(err, webhook.ErrHostNotAllowed), errors.Is(err, webhook.ErrHostUnreachable):
			msg = err.Error()
		default:
			msg = webhook.ErrInvalidURL.Error()
		}
		return "", fieldErr("url", "%s", msg)
	}
	return webhook.DisplayURL(u.String()), nil
}

// sealWebhook seals a URL and signing key for the row (table, id).
func (a *API) sealWebhook(table string, id uuid.UUID, rawURL string, key []byte) (urlEnc, keyEnc []byte, err error) {
	if rawURL != "" {
		u := []byte(rawURL)
		defer crypto.Zero(u)
		if urlEnc, err = a.o.Keyring.Seal(u, ChannelAAD(table, id, FieldURL)); err != nil {
			return nil, nil, fmt.Errorf("notify: seal webhook url: %w", err)
		}
	}
	if key != nil {
		if keyEnc, err = a.o.Keyring.Seal(key, ChannelAAD(table, id, FieldHMACKey)); err != nil {
			return nil, nil, fmt.Errorf("notify: seal webhook key: %w", err)
		}
	}
	return urlEnc, keyEnc, nil
}

func (a *API) secretChanged(ctx context.Context, q *sqlc.Queries, p auth.Principal, ip netip.Addr, table string, id uuid.UUID, fields []string, via audit.Action) error {
	return a.record(ctx, q, p, ip, audit.ActionSecretChanged, table, id, audit.Details{"fields": fields, "via": string(via)})
}

// --- shared channels (admin) -------------------------------------------------

// SharedChannels lists the shared channels with their attachments.
func (a *API) SharedChannels(ctx context.Context) ([]SharedChannel, error) {
	if _, _, err := a.who(ctx, authz.OpSharedChannelList); err != nil {
		return nil, err
	}
	q := sqlc.New(a.o.DB)
	rows, err := q.ListSharedChannels(ctx)
	if err != nil {
		return nil, fmt.Errorf("notify: list shared channels: %w", err)
	}
	out := make([]SharedChannel, len(rows))
	ids := make([]uuid.UUID, len(rows))
	for i := range rows {
		out[i] = sharedFrom(&rows[i])
		ids[i] = rows[i].ID
	}
	if err := a.attachments(ctx, q, ids, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (a *API) attachments(ctx context.Context, q *sqlc.Queries, ids []uuid.UUID, out []SharedChannel) error {
	if len(ids) == 0 {
		return nil
	}
	att, err := q.ListSharedChannelAttachments(ctx, ids)
	if err != nil {
		return fmt.Errorf("notify: list attachments: %w", err)
	}
	by := map[uuid.UUID][]Attachment{}
	for _, r := range att {
		x := Attachment{JobID: r.JobID, JobName: displayName(r.JobName), Groups: []Group{}, Events: []EventType{}}
		for _, g := range r.Groups {
			x.Groups = append(x.Groups, Group(g))
		}
		for _, e := range r.Events {
			x.Events = append(x.Events, EventType(e))
		}
		by[r.ChannelID] = append(by[r.ChannelID], x)
	}
	for i := range out {
		out[i].Attachments = by[out[i].ID]
		if out[i].Attachments == nil {
			out[i].Attachments = []Attachment{}
		}
	}
	return nil
}

func (a *API) sharedChannel(ctx context.Context, q *sqlc.Queries, c *sqlc.SharedChannel) (SharedChannel, error) {
	out := []SharedChannel{sharedFrom(c)}
	if err := a.attachments(ctx, q, []uuid.UUID{c.ID}, out); err != nil {
		return SharedChannel{}, err
	}
	return out[0], nil
}

// CreateSharedChannel creates an e-mail list or a webhook (admin,
// step-up). A webhook gets a new signing key, returned once.
func (a *API) CreateSharedChannel(ctx context.Context, in SharedChannelInput) (KeyedSharedChannel, error) {
	p, ip, err := a.who(ctx, authz.OpSharedChannelEdit)
	if err != nil {
		return KeyedSharedChannel{}, err
	}
	name, err := checkName("name", in.Name)
	if err != nil {
		return KeyedSharedChannel{}, err
	}
	id := uuid.New()
	params := sqlc.InsertSharedChannelParams{ID: id, Name: name, Type: in.Type, CreatedBy: &p.UserID}
	var key []byte
	details := audit.Details{"name": name, "type": string(in.Type)}
	switch in.Type {
	case sqlc.SharedChannelTypeEmailList:
		if in.URL != "" {
			return KeyedSharedChannel{}, fieldErr("url", "an e-mail list has no URL")
		}
		addrs, err := checkAddresses(in.Addresses)
		if err != nil {
			return KeyedSharedChannel{}, err
		}
		if params.Config, err = json.Marshal(EmailListConfig{Addresses: addrs}); err != nil {
			return KeyedSharedChannel{}, err
		}
		details["addresses"] = len(addrs)
	case sqlc.SharedChannelTypeWebhook:
		if len(in.Addresses) > 0 {
			return KeyedSharedChannel{}, fieldErr("addresses", "a webhook has no addresses")
		}
		display, err := a.checkURL(ctx, in.URL, false)
		if err != nil {
			return KeyedSharedChannel{}, err
		}
		if key, err = webhook.GenerateHMACKey(); err != nil {
			return KeyedSharedChannel{}, err
		}
		if params.UrlEnc, params.HmacKeyEnc, err = a.sealWebhook(tableShared, id, in.URL, key); err != nil {
			crypto.Zero(key)
			return KeyedSharedChannel{}, err
		}
		if params.Config, err = json.Marshal(WebhookConfig{DisplayURL: display}); err != nil {
			crypto.Zero(key)
			return KeyedSharedChannel{}, err
		}
		details["display_url"] = display
	default:
		return KeyedSharedChannel{}, fieldErr("type", "unknown channel type")
	}
	var out SharedChannel
	err = db.Tx(ctx, a.o.DB, func(q *sqlc.Queries) error {
		row, err := q.InsertSharedChannel(ctx, params)
		if serviceutil.IsUniqueViolation(err) {
			return ErrNameTaken
		}
		if err != nil {
			return fmt.Errorf("notify: insert shared channel: %w", err)
		}
		if err := a.record(ctx, q, p, ip, audit.ActionNotificationChannelCreated, tableShared, id, details); err != nil {
			return err
		}
		if key != nil {
			if err := a.secretChanged(ctx, q, p, ip, tableShared, id, []string{"url", "signing_key"}, audit.ActionNotificationChannelCreated); err != nil {
				return err
			}
		}
		out = sharedFrom(&row)
		out.Attachments = []Attachment{}
		return nil
	})
	if err != nil {
		crypto.Zero(key)
		return KeyedSharedChannel{}, err
	}
	return KeyedSharedChannel{Channel: out, Key: key}, nil
}

// lockShared loads a shared channel for update.
func lockShared(ctx context.Context, q *sqlc.Queries, id uuid.UUID) (sqlc.SharedChannel, error) {
	c, err := q.GetSharedChannelForUpdate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, fmt.Errorf("notify: load shared channel: %w", err)
	}
	return c, nil
}

// UpdateSharedChannel changes the name, the addresses of an e-mail list
// or the URL of a webhook (admin, step-up). The signing key stays.
func (a *API) UpdateSharedChannel(ctx context.Context, id uuid.UUID, in SharedChannelUpdate) (SharedChannel, error) {
	p, ip, err := a.who(ctx, authz.OpSharedChannelEdit)
	if err != nil {
		return SharedChannel{}, err
	}
	var name string
	if in.Name != nil {
		if name, err = checkName("name", *in.Name); err != nil {
			return SharedChannel{}, err
		}
	}
	var addrs []string
	if in.Addresses != nil {
		if addrs, err = checkAddresses(in.Addresses); err != nil {
			return SharedChannel{}, err
		}
	}
	display := ""
	if in.URL != nil {
		if display, err = a.checkURL(ctx, *in.URL, false); err != nil {
			return SharedChannel{}, err
		}
	}
	var out SharedChannel
	err = db.Tx(ctx, a.o.DB, func(q *sqlc.Queries) error {
		cur, err := lockShared(ctx, q, id)
		if err != nil {
			return err
		}
		params := sqlc.UpdateSharedChannelParams{ID: id, Name: cur.Name, Config: cur.Config}
		var changed []string
		if in.Name != nil && name != cur.Name {
			params.Name = name
			changed = append(changed, "name")
		}
		switch cur.Type {
		case sqlc.SharedChannelTypeEmailList:
			if in.URL != nil {
				return fieldErr("url", "an e-mail list has no URL")
			}
			if addrs != nil {
				if params.Config, err = json.Marshal(EmailListConfig{Addresses: addrs}); err != nil {
					return err
				}
				changed = append(changed, "addresses")
			}
		default:
			if in.Addresses != nil {
				return fieldErr("addresses", "a webhook has no addresses")
			}
			if in.URL != nil {
				if params.UrlEnc, _, err = a.sealWebhook(tableShared, id, *in.URL, nil); err != nil {
					return err
				}
				if params.Config, err = json.Marshal(WebhookConfig{DisplayURL: display}); err != nil {
					return err
				}
				changed = append(changed, "url")
			}
		}
		row, err := q.UpdateSharedChannel(ctx, params)
		if serviceutil.IsUniqueViolation(err) {
			return ErrNameTaken
		}
		if err != nil {
			return fmt.Errorf("notify: update shared channel: %w", err)
		}
		if len(changed) > 0 {
			d := audit.Details{"fields": changed}
			if display != "" {
				d["display_url"] = display
			}
			if err := a.record(ctx, q, p, ip, audit.ActionNotificationChannelUpdated, tableShared, id, d); err != nil {
				return err
			}
			if slices.Contains(changed, "url") {
				if err := a.secretChanged(ctx, q, p, ip, tableShared, id, []string{"url"}, audit.ActionNotificationChannelUpdated); err != nil {
					return err
				}
			}
		}
		out, err = a.sharedChannel(ctx, q, &row)
		return err
	})
	return out, err
}

// RotateSharedChannelKey replaces the signing key of a shared webhook
// (admin, step-up) and returns the new key once.
func (a *API) RotateSharedChannelKey(ctx context.Context, id uuid.UUID) (KeyedSharedChannel, error) {
	p, ip, err := a.who(ctx, authz.OpSharedChannelEdit)
	if err != nil {
		return KeyedSharedChannel{}, err
	}
	key, err := webhook.GenerateHMACKey()
	if err != nil {
		return KeyedSharedChannel{}, err
	}
	var out SharedChannel
	err = db.Tx(ctx, a.o.DB, func(q *sqlc.Queries) error {
		cur, err := lockShared(ctx, q, id)
		if err != nil {
			return err
		}
		if cur.Type != sqlc.SharedChannelTypeWebhook {
			return fieldErr("id", "only webhooks have a signing key")
		}
		_, keyEnc, err := a.sealWebhook(tableShared, id, "", key)
		if err != nil {
			return err
		}
		if _, err := q.UpdateSharedChannelKey(ctx, sqlc.UpdateSharedChannelKeyParams{ID: id, HmacKeyEnc: keyEnc}); err != nil {
			return fmt.Errorf("notify: rotate key: %w", err)
		}
		if err := a.record(ctx, q, p, ip, audit.ActionNotificationChannelKeyRotated, tableShared, id, nil); err != nil {
			return err
		}
		if err := a.secretChanged(ctx, q, p, ip, tableShared, id, []string{"signing_key"}, audit.ActionNotificationChannelKeyRotated); err != nil {
			return err
		}
		out, err = a.sharedChannel(ctx, q, &cur)
		return err
	})
	if err != nil {
		crypto.Zero(key)
		return KeyedSharedChannel{}, err
	}
	return KeyedSharedChannel{Channel: out, Key: key}, nil
}

// DeleteSharedChannel deletes a shared channel with its attachments and
// pending deliveries (admin, step-up).
func (a *API) DeleteSharedChannel(ctx context.Context, id uuid.UUID) error {
	p, ip, err := a.who(ctx, authz.OpSharedChannelEdit)
	if err != nil {
		return err
	}
	return db.Tx(ctx, a.o.DB, func(q *sqlc.Queries) error {
		cur, err := lockShared(ctx, q, id)
		if err != nil {
			return err
		}
		if _, err := q.DeleteSharedChannel(ctx, id); err != nil {
			return fmt.Errorf("notify: delete shared channel: %w", err)
		}
		return a.record(ctx, q, p, ip, audit.ActionNotificationChannelDeleted, tableShared, id,
			audit.Details{"name": cur.Name, "type": string(cur.Type)})
	})
}

// SetSharedChannelEnabled turns a shared channel on (failure counters
// start afresh, also after an auto-disable) or off (admin).
func (a *API) SetSharedChannelEnabled(ctx context.Context, id uuid.UUID, enabled bool) (SharedChannel, error) {
	p, ip, err := a.who(ctx, authz.OpSharedChannelToggle)
	if err != nil {
		return SharedChannel{}, err
	}
	var out SharedChannel
	err = db.Tx(ctx, a.o.DB, func(q *sqlc.Queries) error {
		cur, err := lockShared(ctx, q, id)
		if err != nil {
			return err
		}
		var row sqlc.SharedChannel
		action := audit.ActionNotificationChannelDisabled
		if enabled {
			action = audit.ActionNotificationChannelEnabled
			row, err = q.EnableSharedChannel(ctx, id)
		} else {
			row, err = q.DisableSharedChannel(ctx, sqlc.DisableSharedChannelParams{ID: id, At: a.o.Now()})
		}
		if err != nil {
			return fmt.Errorf("notify: toggle shared channel: %w", err)
		}
		if cur.Enabled != enabled {
			d := audit.Details{}
			if enabled && cur.DisabledReason != nil {
				d["previous_reason"] = *cur.DisabledReason
			}
			if err := a.record(ctx, q, p, ip, action, tableShared, id, d); err != nil {
				return err
			}
		}
		out, err = a.sharedChannel(ctx, q, &row)
		return err
	})
	return out, err
}

// checkFilter validates an attachment filter: groups and events of the
// catalog that shared channels can receive (never security; events must
// concern a job).
func checkFilter(groups []Group, events []EventType) ([]string, []string, error) {
	if len(groups) > 16 {
		return nil, nil, fieldErr("filter.groups", "at most 16 groups")
	}
	if len(events) > 64 {
		return nil, nil, fieldErr("filter.events", "at most 64 events")
	}
	gs := []string{}
	for _, g := range groups {
		if g == GroupSecurity || !slices.Contains(Groups(), g) {
			return nil, nil, fieldErr("filter.groups", "unknown or not allowed group %q", string(g))
		}
		if !slices.Contains(gs, string(g)) {
			gs = append(gs, string(g))
		}
	}
	es := []string{}
	for _, e := range events {
		m, ok := Lookup(e)
		if !ok || m.Group == GroupSecurity || m.Subject != SubjectJob {
			return nil, nil, fieldErr("filter.events", "unknown or not allowed event")
		}
		if !slices.Contains(es, string(e)) {
			es = append(es, string(e))
		}
	}
	return gs, es, nil
}

// AttachSharedChannel attaches a shared channel to a job with an event
// filter, or replaces the filter of an existing attachment (admin).
func (a *API) AttachSharedChannel(ctx context.Context, jobID, channelID uuid.UUID, groups []Group, events []EventType) (SharedChannel, error) {
	p, ip, err := a.who(ctx, authz.OpJobSharedChannelsEdit)
	if err != nil {
		return SharedChannel{}, err
	}
	gs, es, err := checkFilter(groups, events)
	if err != nil {
		return SharedChannel{}, err
	}
	var out SharedChannel
	err = db.Tx(ctx, a.o.DB, func(q *sqlc.Queries) error {
		cur, err := lockShared(ctx, q, channelID)
		if err != nil {
			return err
		}
		if _, err := q.GetJobForAttach(ctx, jobID); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return fmt.Errorf("notify: load job: %w", err)
		}
		if err := q.AttachSharedChannel(ctx, sqlc.AttachSharedChannelParams{
			JobID: jobID, ChannelID: channelID, Groups: gs, Events: es, CreatedBy: &p.UserID,
		}); err != nil {
			var pe *pgconn.PgError
			if errors.As(err, &pe) && pe.Code == "23503" {
				return ErrNotFound
			}
			return fmt.Errorf("notify: attach: %w", err)
		}
		if err := a.record(ctx, q, p, ip, audit.ActionNotificationChannelAttached, tableShared, channelID,
			audit.Details{"job_id": jobID.String(), "groups": gs, "events": es}); err != nil {
			return err
		}
		out, err = a.sharedChannel(ctx, q, &cur)
		return err
	})
	return out, err
}

// DetachSharedChannel removes a shared channel from a job (admin).
func (a *API) DetachSharedChannel(ctx context.Context, jobID, channelID uuid.UUID) (SharedChannel, error) {
	p, ip, err := a.who(ctx, authz.OpJobSharedChannelsEdit)
	if err != nil {
		return SharedChannel{}, err
	}
	var out SharedChannel
	err = db.Tx(ctx, a.o.DB, func(q *sqlc.Queries) error {
		cur, err := lockShared(ctx, q, channelID)
		if err != nil {
			return err
		}
		n, err := q.DetachSharedChannel(ctx, sqlc.DetachSharedChannelParams{JobID: jobID, ChannelID: channelID})
		if err != nil {
			return fmt.Errorf("notify: detach: %w", err)
		}
		if n == 0 {
			return ErrNotFound
		}
		if err := a.record(ctx, q, p, ip, audit.ActionNotificationChannelDetached, tableShared, channelID,
			audit.Details{"job_id": jobID.String()}); err != nil {
			return err
		}
		out, err = a.sharedChannel(ctx, q, &cur)
		return err
	})
	return out, err
}

// TestSharedChannel sends a test delivery to a shared channel (admin; rate
// limited; also while it is disabled). The result has fixed codes only
// and does not count towards the auto-disable policy.
func (a *API) TestSharedChannel(ctx context.Context, id uuid.UUID) (TestResult, error) {
	p, ip, err := a.who(ctx, authz.OpSharedChannelTest)
	if err != nil {
		return TestResult{}, err
	}
	q := sqlc.New(a.o.DB)
	c, err := q.GetSharedChannel(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return TestResult{}, ErrNotFound
	}
	if err != nil {
		return TestResult{}, fmt.Errorf("notify: load shared channel: %w", err)
	}
	release, err := a.limiter.Acquire("notify_test:" + p.UserID.String())
	if err != nil {
		return TestResult{}, err
	}
	defer release()
	var res TestResult
	if c.Type == sqlc.SharedChannelTypeEmailList {
		res, err = a.testEmail(ctx, emailListAddresses(c.Config))
	} else {
		res, err = a.testWebhook(ctx, a.o.Webhooks, tableShared, c.ID, c.UrlEnc, c.HmacKeyEnc)
	}
	if err != nil {
		return TestResult{}, err
	}
	return res, a.recordTest(ctx, p, ip, tableShared, id, res)
}

func (a *API) recordTest(ctx context.Context, p auth.Principal, ip netip.Addr, table string, id uuid.UUID, res TestResult) error {
	d := audit.Details{"ok": res.OK}
	if res.Code != "" {
		d["code"] = res.Code
	}
	return a.record(context.WithoutCancel(ctx), sqlc.New(a.o.DB), p, ip, audit.ActionNotificationChannelTested, table, id, d)
}

// testWebhook opens the sealed URL and key and posts the test envelope
// with sender.
func (a *API) testWebhook(ctx context.Context, sender WebhookSender, table string, id uuid.UUID, urlEnc, keyEnc []byte) (TestResult, error) {
	if sender == nil {
		return TestResult{}, ErrUnavailable
	}
	u, err := a.o.Keyring.Open(urlEnc, ChannelAAD(table, id, FieldURL))
	if err != nil {
		return TestResult{}, fmt.Errorf("notify: open webhook url: %w", err)
	}
	defer crypto.Zero(u)
	k, err := a.o.Keyring.Open(keyEnc, ChannelAAD(table, id, FieldHMACKey))
	if err != nil {
		return TestResult{}, fmt.Errorf("notify: open webhook key: %w", err)
	}
	defer crypto.Zero(k)
	evID := uuid.NewString()
	body, err := webhook.TestPayload(evID, a.o.Now())
	if err != nil {
		return TestResult{}, err
	}
	r := sender.Send(ctx, webhook.Delivery{
		URL: string(u), HMACKey: k, EventID: evID, EventType: webhook.EventTypeTest, Payload: body,
	})
	if r.Outcome == webhook.OutcomeAborted {
		return TestResult{}, ctx.Err()
	}
	if r.Outcome == webhook.OutcomeSuccess {
		return TestResult{OK: true, Code: string(webhook.CodeOK), StatusCode: r.StatusCode}, nil
	}
	return TestResult{Code: string(r.Code), Message: webhookMessages[r.Code], StatusCode: r.StatusCode}, nil
}

// testEmail sends the test e-mail of an e-mail list. Users only learn
// that it could not be sent (the SMTP details are for the SMTP test).
func (a *API) testEmail(ctx context.Context, to []string) (TestResult, error) {
	if a.o.Mailer == nil {
		return TestResult{}, ErrUnavailable
	}
	if len(to) == 0 {
		return TestResult{Code: CodeChannelDisabled, Message: "the e-mail list has no addresses"}, nil
	}
	c, err := email.Render(email.Notice{
		Subject: "bekup test notification",
		Heading: "This address receives bekup notifications",
		Paragraphs: []string{
			"This is a test message from bekup. An administrator sent it to check a notification e-mail list.",
			"No action is needed.",
		},
		Details: []email.Detail{{Label: "Sent at", Value: a.o.Now().UTC().Format(time.RFC1123)}},
	})
	if err != nil {
		return TestResult{}, err
	}
	err = a.o.Mailer.Send(ctx, email.NewMessage(to, c))
	if err == nil {
		return TestResult{OK: true, Code: string(webhook.CodeOK)}, nil
	}
	if ctx.Err() != nil {
		return TestResult{}, ctx.Err()
	}
	var se *email.SendError
	if errors.As(err, &se) {
		if se.Code == email.CodeNotConfigured {
			return TestResult{}, ErrUnavailable
		}
		return TestResult{Code: string(se.Code), Message: se.Code.Message()}, nil
	}
	return TestResult{Code: string(email.CodeFailed), Message: email.CodeFailed.Message()}, nil
}

// JobAttachment is a shared channel attached to a job as staff see it
// (Job.sharedChannelAttachments, D-198): never e-mail addresses, URLs or
// keys; DisplayURL is the masked `https://host/…` form of a webhook.
type JobAttachment struct {
	ChannelID  uuid.UUID
	Name       string
	Type       sqlc.SharedChannelType
	DisplayURL string
	Enabled    bool
	Groups     []Group
	Events     []EventType
}

// MaxAttachmentJobs caps the jobs of one JobAttachments call.
const MaxAttachmentJobs = 500

// JobAttachments returns the shared channels attached to each of jobIDs
// (staff: notification.shared_channel_attachment_view, re-checked here;
// the caller loaded the jobs with the caller's rights). Jobs without
// attachments map to an empty slice.
func (a *API) JobAttachments(ctx context.Context, jobIDs []uuid.UUID) (map[uuid.UUID][]JobAttachment, error) {
	if _, _, err := a.who(ctx, authz.OpSharedChannelAttachmentView); err != nil {
		return nil, err
	}
	if len(jobIDs) > MaxAttachmentJobs {
		return nil, fmt.Errorf("notify: at most %d jobs per attachment load", MaxAttachmentJobs)
	}
	out := make(map[uuid.UUID][]JobAttachment, len(jobIDs))
	for _, id := range jobIDs {
		out[id] = []JobAttachment{}
	}
	if len(jobIDs) == 0 {
		return out, nil
	}
	rows, err := sqlc.New(a.o.DB).ListJobSharedChannelAttachments(ctx, jobIDs)
	if err != nil {
		return nil, fmt.Errorf("notify: list job attachments: %w", err)
	}
	for i := range rows {
		r := &rows[i]
		x := JobAttachment{ChannelID: r.ChannelID, Name: r.Name, Type: r.Type, Enabled: r.Enabled, Groups: []Group{}, Events: []EventType{}}
		if r.Type == sqlc.SharedChannelTypeWebhook {
			var cfg WebhookConfig
			if json.Unmarshal(r.Config, &cfg) == nil {
				x.DisplayURL = cfg.DisplayURL
			}
		}
		for _, g := range r.Groups {
			x.Groups = append(x.Groups, Group(g))
		}
		for _, e := range r.Events {
			x.Events = append(x.Events, EventType(e))
		}
		out[r.JobID] = append(out[r.JobID], x)
	}
	return out, nil
}
