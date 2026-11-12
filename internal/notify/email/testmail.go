package email

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/i18n"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// TestMailer sends the admin's test e-mail (sendTestEmail): rate limited
// per admin and audited (smtp.test_sent with the result code, never the
// server's text).
type TestMailer struct {
	sender  Mailer
	auditor *audit.Auditor
	limiter *serviceutil.Limiter
	now     func() time.Time
}

// TestMailerOptions configure NewTestMailer.
type TestMailerOptions struct {
	Sender  Mailer
	Auditor *audit.Auditor
	// Limiter is the per-admin limit; nil: serviceutil defaults (10 per
	// minute, burst 5, 4 at once).
	Limiter func(key string) (bool, time.Duration)
}

// NewTestMailer validates o.
func NewTestMailer(o TestMailerOptions) (*TestMailer, error) {
	if o.Sender == nil || o.Auditor == nil {
		return nil, errors.New("email: Sender and Auditor are required")
	}
	lim, err := serviceutil.NewLimiter(2, o.Limiter)
	if err != nil {
		return nil, fmt.Errorf("email: %w", err)
	}
	return &TestMailer{sender: o.Sender, auditor: o.Auditor, limiter: lim, now: time.Now}, nil
}

// RateLimitedError reports that the caller exceeded a rate limit.
type RateLimitedError = serviceutil.RateLimitedError

// ErrBusy means that too many sends run at the same time.
var ErrBusy = serviceutil.ErrBusy

// SendTest sends the test message to `to` and reports whether the session
// was encrypted (D-180; a sender that is no DeliveryMailer reports
// unencrypted). A delivery failure is returned as a *SendError after it was
// audited; invalid input as a *FieldError.
func (t *TestMailer) SendTest(ctx context.Context, actor Actor, to string) (Delivery, error) {
	if err := CheckAddress(to); err != nil {
		return Delivery{}, serviceutil.FieldErrFrom("to", err, i18n.M("invalid e-mail address"))
	}
	release, err := t.limiter.Acquire("smtp_test:" + actor.UserID.String())
	if err != nil {
		return Delivery{}, err
	}
	defer release()
	l := i18n.Resolve(nil, actor.Locale)
	tr := mailTexts.For(l)
	c, err := Render(Notice{
		Subject:    tr("test.subject"),
		Heading:    tr("test.heading"),
		Paragraphs: []string{tr("test.p1"), tr("test.p2")},
		Details:    []Detail{{Label: tr("test.sent_at"), Value: i18n.FormatTime(l, t.now().UTC(), "Mon 2 Jan 2006 15:04:05 MST")}},
		Locale:     l,
	})
	if err != nil {
		return Delivery{}, err
	}
	var (
		d       Delivery
		sendErr error
	)
	msg := NewMessage([]string{to}, c)
	if dm, ok := t.sender.(DeliveryMailer); ok {
		d, sendErr = dm.SendDelivery(ctx, msg)
	} else {
		sendErr = t.sender.Send(ctx, msg)
	}
	if errors.Is(sendErr, context.Canceled) {
		return d, sendErr
	}
	details := audit.Details{"to": to, "ok": sendErr == nil, "encrypted": d.Encrypted}
	if d.TLSVersion != "" {
		details["tls_version"] = d.TLSVersion
	}
	var se *SendError
	if errors.As(sendErr, &se) {
		details["code"] = string(se.Code)
	} else if sendErr != nil {
		details["code"] = string(CodeFailed)
	}
	if _, err := t.auditor.Record(context.WithoutCancel(ctx), audit.Event{
		Actor: audit.UserActor(actor.UserID), Action: audit.ActionSMTPTestSent,
		TargetType: "setting", TargetID: SettingKey, IP: actor.IP, Details: details,
	}); err != nil {
		return d, err
	}
	return d, sendErr
}
