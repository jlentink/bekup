package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/notify/email"
	"github.com/jlentink/bekup/internal/notify/webhook"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// API is the notifications API behind the GraphQL layer (task 5.3b,
// D-191): shared channels (admin), personal webhooks, the preference
// matrix and the in-app inbox. Every method takes the caller from the
// request context (auth.FromContext) and re-checks its authz operation
// (defense in depth behind the GraphQL root-field gate); personal data
// is always scoped to the caller's own account.
type API struct {
	o       APIOptions
	limiter *serviceutil.Limiter
}

// APIOptions configure NewAPI.
type APIOptions struct {
	DB      DB
	Keyring *crypto.Keyring
	Auditor *audit.Auditor
	Policy  *authz.Policy
	// Guard checks shared-channel webhook hosts when a URL is entered
	// (webhook.ValidateURL; the shared_webhooks *netguard.Guard).
	Guard webhook.Resolver
	// PersonalGuard checks personal webhook hosts (the personal_webhooks
	// guard: only allowlist entries an admin scoped to personal webhooks
	// apply, D-198).
	PersonalGuard webhook.Resolver
	// Webhooks sends shared-channel test deliveries (the 5.2 Sender);
	// nil: tests answer "not available".
	Webhooks WebhookSender
	// PersonalWebhooks sends personal test deliveries (a Sender behind
	// PersonalGuard); nil: tests answer "not available".
	PersonalWebhooks WebhookSender
	// Mailer sends the test e-mail of an e-mail list (the 5.1 Sender);
	// nil: such tests answer "not available".
	Mailer email.Mailer
	// AllowHTTP accepts http webhook URLs (dev mode only).
	AllowHTTP bool
	// Limiter is the per-user limit of test sends; nil: serviceutil
	// defaults (10 per minute, burst 5).
	Limiter func(key string) (bool, time.Duration)
	Logger  *slog.Logger
	Now     func() time.Time
}

// NewAPI validates o.
func NewAPI(o APIOptions) (*API, error) {
	if o.DB == nil || o.Keyring == nil || o.Auditor == nil || o.Policy == nil || o.Guard == nil || o.PersonalGuard == nil {
		return nil, errors.New("notify: DB, Keyring, Auditor, Policy, Guard and PersonalGuard are required")
	}
	lim, err := serviceutil.NewLimiter(2, o.Limiter)
	if err != nil {
		return nil, fmt.Errorf("notify: %w", err)
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &API{o: o, limiter: lim}, nil
}

// Errors of the API. Field problems are *serviceutil.FieldError; rate
// limits *serviceutil.RateLimitedError or serviceutil.ErrBusy.
var (
	// ErrNotFound: the channel, job or notification does not exist (or is
	// not the caller's).
	ErrNotFound = errors.New("notify: not found")
	// ErrNameTaken: a shared channel with this name exists.
	ErrNameTaken = errors.New("notify: a shared channel with this name already exists")
	// ErrTooManyWebhooks: the caller already has MaxPersonalWebhooks.
	ErrTooManyWebhooks = errors.New("notify: at most 5 personal webhooks")
	// ErrUnavailable: the sender a test needs is not configured.
	ErrUnavailable = errors.New("notify: sending is not available")
)

// MaxPersonalWebhooks is the per-user limit of personal webhooks (D-023;
// enforced by a database trigger as well).
const MaxPersonalWebhooks = 5

// FieldError is an invalid input.
type FieldError = serviceutil.FieldError

func fieldErr(field, format string, a ...any) error { return serviceutil.FieldErr(field, format, a...) }

// who authorizes op for the caller of ctx and returns the principal and
// client address.
func (a *API) who(ctx context.Context, op authz.Operation) (auth.Principal, netip.Addr, error) {
	if err := a.o.Policy.Authorize(ctx, op); err != nil {
		return auth.Principal{}, netip.Addr{}, err
	}
	p, ok := auth.FromContext(ctx)
	if !ok {
		return auth.Principal{}, netip.Addr{}, errors.New("notify: no principal")
	}
	return p, auth.RequestMetaFrom(ctx).IP, nil
}

func (a *API) record(ctx context.Context, q *sqlc.Queries, p auth.Principal, ip netip.Addr, action audit.Action, targetType string, id uuid.UUID, d audit.Details) error {
	_, err := a.o.Auditor.RecordTx(ctx, q, audit.Event{
		Actor: audit.UserActor(p.UserID), Action: action, TargetType: targetType, TargetID: id.String(),
		IP: ip, Details: d,
	})
	return err
}

// TestResult is the outcome of a test send: fixed codes and messages
// only (never the receiver's response or the SMTP server's text).
// Personal webhook tests carry no StatusCode and only the codes ok,
// not_allowed and failed (D-198), so they cannot probe services.
type TestResult struct {
	OK         bool
	Code       string
	Message    string
	StatusCode int
}
