package notify

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/notify/webhook"
)

// PersonalWebhook is one of the caller's own webhooks; the URL only as its
// display form, the signing key never.
type PersonalWebhook struct {
	ID         uuid.UUID
	Label      string
	DisplayURL string
	State      ChannelState
	CreatedAt  time.Time
}

// KeyedPersonalWebhook carries the new signing key once (AddMyWebhook,
// RotateMyWebhookKey). The caller shows Key once and zeroes it.
type KeyedPersonalWebhook struct {
	Webhook PersonalWebhook
	Key     []byte
}

// MyChannels are the caller's own channels: the e-mail address (needs to
// be verified for e-mail notifications) and the personal webhooks.
type MyChannels struct {
	Email           string
	EmailVerifiedAt *time.Time
	Webhooks        []PersonalWebhook
}

func personalFrom(c *sqlc.UserChannel) PersonalWebhook {
	return PersonalWebhook{
		ID: c.ID, Label: c.Name, DisplayURL: c.DisplayUrl, CreatedAt: c.CreatedAt,
		State: stateFrom(c.Enabled, c.DisabledAt, c.DisabledReason, c.ConsecutiveFailures, c.PermanentFailures,
			c.LastFailureAt, c.LastFailureCode),
	}
}

// MyChannels returns the caller's e-mail address and webhooks.
func (a *API) MyChannels(ctx context.Context) (MyChannels, error) {
	p, _, err := a.who(ctx, authz.OpPersonalChannelList)
	if err != nil {
		return MyChannels{}, err
	}
	q := sqlc.New(a.o.DB)
	u, err := q.GetUserContact(ctx, p.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return MyChannels{}, ErrNotFound
	}
	if err != nil {
		return MyChannels{}, fmt.Errorf("notify: load account: %w", err)
	}
	rows, err := q.ListUserChannelsOfUser(ctx, p.UserID)
	if err != nil {
		return MyChannels{}, fmt.Errorf("notify: list webhooks: %w", err)
	}
	out := MyChannels{Email: u.Email, EmailVerifiedAt: u.EmailVerifiedAt, Webhooks: make([]PersonalWebhook, len(rows))}
	for i := range rows {
		out.Webhooks[i] = personalFrom(&rows[i])
	}
	return out, nil
}

// isLimitViolation reports the per-user webhook limit trigger.
func isLimitViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23514" && pe.ConstraintName == "user_channels_max_per_user"
}

// AddMyWebhook adds a personal webhook (at most MaxPersonalWebhooks; the
// URL passes webhook.ValidateURL) and returns its new signing key once.
func (a *API) AddMyWebhook(ctx context.Context, rawURL, label string) (KeyedPersonalWebhook, error) {
	p, ip, err := a.who(ctx, authz.OpPersonalChannelEdit)
	if err != nil {
		return KeyedPersonalWebhook{}, err
	}
	name, err := checkName("label", label)
	if err != nil {
		return KeyedPersonalWebhook{}, err
	}
	display, err := a.checkURL(ctx, rawURL, true)
	if err != nil {
		return KeyedPersonalWebhook{}, err
	}
	key, err := webhook.GenerateHMACKey()
	if err != nil {
		return KeyedPersonalWebhook{}, err
	}
	id := uuid.New()
	urlEnc, keyEnc, err := a.sealWebhook(tableUser, id, rawURL, key)
	if err != nil {
		crypto.Zero(key)
		return KeyedPersonalWebhook{}, err
	}
	var out PersonalWebhook
	err = db.Tx(ctx, a.o.DB, func(q *sqlc.Queries) error {
		row, err := q.InsertUserChannel(ctx, sqlc.InsertUserChannelParams{
			ID: id, UserID: p.UserID, Name: name, DisplayUrl: display, UrlEnc: urlEnc, HmacKeyEnc: keyEnc,
		})
		if isLimitViolation(err) {
			return ErrTooManyWebhooks
		}
		if err != nil {
			return fmt.Errorf("notify: insert webhook: %w", err)
		}
		if err := a.record(ctx, q, p, ip, audit.ActionNotificationChannelCreated, tableUser, id,
			audit.Details{"type": "webhook", "display_url": display}); err != nil {
			return err
		}
		if err := a.secretChanged(ctx, q, p, ip, tableUser, id, []string{"url", "signing_key"}, audit.ActionNotificationChannelCreated); err != nil {
			return err
		}
		out = personalFrom(&row)
		return nil
	})
	if err != nil {
		crypto.Zero(key)
		return KeyedPersonalWebhook{}, err
	}
	return KeyedPersonalWebhook{Webhook: out, Key: key}, nil
}

func lockMine(ctx context.Context, q *sqlc.Queries, id, user uuid.UUID) (sqlc.UserChannel, error) {
	c, err := q.GetUserChannelOfUserForUpdate(ctx, sqlc.GetUserChannelOfUserForUpdateParams{ID: id, UserID: user})
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, fmt.Errorf("notify: load webhook: %w", err)
	}
	return c, nil
}

// RotateMyWebhookKey replaces the signing key of an own webhook and
// returns the new key once.
func (a *API) RotateMyWebhookKey(ctx context.Context, id uuid.UUID) (KeyedPersonalWebhook, error) {
	p, ip, err := a.who(ctx, authz.OpPersonalChannelEdit)
	if err != nil {
		return KeyedPersonalWebhook{}, err
	}
	key, err := webhook.GenerateHMACKey()
	if err != nil {
		return KeyedPersonalWebhook{}, err
	}
	var out PersonalWebhook
	err = db.Tx(ctx, a.o.DB, func(q *sqlc.Queries) error {
		cur, err := lockMine(ctx, q, id, p.UserID)
		if err != nil {
			return err
		}
		_, keyEnc, err := a.sealWebhook(tableUser, id, "", key)
		if err != nil {
			return err
		}
		if _, err := q.UpdateUserChannelKey(ctx, sqlc.UpdateUserChannelKeyParams{ID: id, UserID: p.UserID, HmacKeyEnc: keyEnc}); err != nil {
			return fmt.Errorf("notify: rotate key: %w", err)
		}
		if err := a.record(ctx, q, p, ip, audit.ActionNotificationChannelKeyRotated, tableUser, id, nil); err != nil {
			return err
		}
		if err := a.secretChanged(ctx, q, p, ip, tableUser, id, []string{"signing_key"}, audit.ActionNotificationChannelKeyRotated); err != nil {
			return err
		}
		out = personalFrom(&cur)
		return nil
	})
	if err != nil {
		crypto.Zero(key)
		return KeyedPersonalWebhook{}, err
	}
	return KeyedPersonalWebhook{Webhook: out, Key: key}, nil
}

// DeleteMyWebhook deletes an own webhook (and its pending deliveries).
func (a *API) DeleteMyWebhook(ctx context.Context, id uuid.UUID) error {
	p, ip, err := a.who(ctx, authz.OpPersonalChannelEdit)
	if err != nil {
		return err
	}
	return db.Tx(ctx, a.o.DB, func(q *sqlc.Queries) error {
		cur, err := lockMine(ctx, q, id, p.UserID)
		if err != nil {
			return err
		}
		if _, err := q.DeleteUserChannelOfUser(ctx, sqlc.DeleteUserChannelOfUserParams{ID: id, UserID: p.UserID}); err != nil {
			return fmt.Errorf("notify: delete webhook: %w", err)
		}
		return a.record(ctx, q, p, ip, audit.ActionNotificationChannelDeleted, tableUser, id,
			audit.Details{"type": "webhook", "display_url": cur.DisplayUrl})
	})
}

// ReenableMyWebhook turns an own webhook on again after it was
// auto-disabled (its failure counters start afresh).
func (a *API) ReenableMyWebhook(ctx context.Context, id uuid.UUID) (PersonalWebhook, error) {
	p, ip, err := a.who(ctx, authz.OpPersonalChannelEnable)
	if err != nil {
		return PersonalWebhook{}, err
	}
	var out PersonalWebhook
	err = db.Tx(ctx, a.o.DB, func(q *sqlc.Queries) error {
		cur, err := lockMine(ctx, q, id, p.UserID)
		if err != nil {
			return err
		}
		row, err := q.ReenableUserChannel(ctx, sqlc.ReenableUserChannelParams{ID: id, UserID: p.UserID})
		if err != nil {
			return fmt.Errorf("notify: re-enable webhook: %w", err)
		}
		if !cur.Enabled {
			d := audit.Details{}
			if cur.DisabledReason != nil {
				d["previous_reason"] = *cur.DisabledReason
			}
			if err := a.record(ctx, q, p, ip, audit.ActionNotificationChannelEnabled, tableUser, id, d); err != nil {
				return err
			}
		}
		out = personalFrom(&row)
		return nil
	})
	return out, err
}

// TestMyWebhook sends a test delivery to an own webhook (rate limited;
// also while it is disabled, to check a receiver before re-enabling). It
// does not count towards the auto-disable policy.
func (a *API) TestMyWebhook(ctx context.Context, id uuid.UUID) (TestResult, error) {
	p, ip, err := a.who(ctx, authz.OpPersonalChannelTest)
	if err != nil {
		return TestResult{}, err
	}
	c, err := sqlc.New(a.o.DB).GetUserChannel(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && c.UserID != p.UserID) {
		return TestResult{}, ErrNotFound
	}
	if err != nil {
		return TestResult{}, fmt.Errorf("notify: load webhook: %w", err)
	}
	release, err := a.limiter.Acquire("notify_test:" + p.UserID.String())
	if err != nil {
		return TestResult{}, err
	}
	defer release()
	res, err := a.testWebhook(ctx, a.o.PersonalWebhooks, tableUser, c.ID, c.UrlEnc, c.HmacKeyEnc)
	if err != nil {
		return TestResult{}, err
	}
	res = personalTestResult(res)
	return res, a.recordTest(ctx, p, ip, tableUser, id, res)
}

// Personal test result codes (D-198).
const (
	// CodePersonalTestFailed: the receiver did not accept the test, for
	// whatever reason (status, redirect, TLS, unreachable).
	CodePersonalTestFailed = "failed"
)

// personalTestResult buckets a personal webhook test (M5 review): ok,
// not_allowed (the guard refused the address; the URL check at input time
// says the same) or failed with one fixed message, and never the HTTP
// status. Any user can run the test against a URL of their choice, so
// finer results would let them tell an open port from a closed one or
// read a service's status codes.
func personalTestResult(r TestResult) TestResult {
	switch {
	case r.OK:
		return TestResult{OK: true, Code: string(webhook.CodeOK)}
	case r.Code == string(webhook.CodeNotAllowed):
		return TestResult{Code: r.Code, Message: webhookMessages[webhook.CodeNotAllowed]}
	default:
		return TestResult{Code: CodePersonalTestFailed,
			Message: "the receiver did not accept the test delivery (check the URL, its TLS certificate and that it answers with a 2xx status)"}
	}
}
