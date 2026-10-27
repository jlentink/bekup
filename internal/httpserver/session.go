package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// SessionResolver resolves a session cookie value to the caller
// (*auth.Sessions implements it through SessionsResolver). It returns
// auth.ErrNoSession for a token that does not belong to an active session
// of an active user.
type SessionResolver interface {
	Resolve(ctx context.Context, token string, meta auth.RequestMeta) (auth.Principal, error)
}

// SessionOptions configure the session middleware of /graphql and /api.
type SessionOptions struct {
	// Resolver looks sessions up (required).
	Resolver SessionResolver
	// Cookies names and builds the session cookie (https vs http mode,
	// D-095).
	Cookies auth.CookiePolicy
	// UserLimit configures the per-user limiter applied to authenticated
	// requests (server.user_rate_limit_*); zero PerSecond disables it.
	UserLimit RateLimitOptions
}

// UserCharge takes n further tokens from the caller's per-user rate limit
// bucket (server.user_rate_limit_*); false means the bucket is empty, with
// the time until n tokens are available.
type UserCharge func(n int) (bool, time.Duration)

type userChargeKey struct{}

// ChargeUser takes n further tokens from the per-user limit of the
// authenticated caller of ctx (the GraphQL layer charges every root field
// beyond the first, D-117). Without a per-user limiter or an authenticated
// caller it allows.
func ChargeUser(ctx context.Context, n int) (bool, time.Duration) {
	if n <= 0 {
		return true, 0
	}
	c, ok := ctx.Value(userChargeKey{}).(UserCharge)
	if !ok {
		return true, 0
	}
	return c(n)
}

// cookieSink writes Set-Cookie headers for the session cookie. Only the
// last decision of a request is sent (login after a stale-cookie clear,
// ...).
type cookieSink struct {
	mu     sync.Mutex
	w      http.ResponseWriter
	policy auth.CookiePolicy
}

func (s *cookieSink) set(c *http.Cookie) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.w.Header()
	prefix := c.Name + "="
	var kept []string
	for _, v := range h.Values("Set-Cookie") {
		if !strings.HasPrefix(v, prefix) {
			kept = append(kept, v)
		}
	}
	h.Del("Set-Cookie")
	for _, v := range kept {
		h.Add("Set-Cookie", v)
	}
	if v := c.String(); v != "" {
		h.Add("Set-Cookie", v)
	}
}

func (s *cookieSink) SetSessionCookie(token string) { s.set(s.policy.Session(token)) }
func (s *cookieSink) ClearSessionCookie()           { s.set(s.policy.Clear()) }

// sessionCookies returns the values of every cookie named name.
func sessionCookies(r *http.Request, name string) []string {
	var out []string
	for _, c := range r.Cookies() {
		if c.Name == name {
			out = append(out, c.Value)
		}
	}
	return out
}

// sessionMiddleware attaches the caller to the request context (task 1.5):
//
//   - always: a cookie setter (auth.CookieSetterFrom) and the client facts
//     (auth.RequestMetaFrom) for login, logout and session records;
//   - no session cookie: anonymous, without any database lookup;
//   - more than one session cookie: anonymous and the cookie is cleared
//     (an injected duplicate must not decide which session is used);
//   - a cookie that resolves to an active session of an active user:
//     auth.WithPrincipal, after the per-user rate limit;
//   - auth.ErrNoSession (unknown, expired, revoked or disabled):
//     anonymous and the cookie is cleared;
//   - any other error: 503 (fail closed, never anonymous by accident).
func sessionMiddleware(o SessionOptions, logger *slog.Logger) (func(http.Handler) http.Handler, error) {
	if o.Resolver == nil {
		return nil, errors.New("httpserver: SessionOptions.Resolver is required")
	}
	var users *RateLimiter[uuid.UUID]
	if o.UserLimit.PerSecond > 0 {
		var err error
		if users, err = NewRateLimiter[uuid.UUID](o.UserLimit); err != nil {
			return nil, err
		}
	}
	name := o.Cookies.Name()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			meta := auth.NewRequestMeta(ClientIP(ctx), r.UserAgent())
			sink := &cookieSink{w: w, policy: o.Cookies}
			ctx = auth.WithRequestMeta(auth.WithCookieSetter(ctx, sink), meta)
			values := sessionCookies(r, name)
			switch len(values) {
			case 0:
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			case 1:
			default:
				sink.ClearSessionCookie()
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			p, err := o.Resolver.Resolve(ctx, values[0], meta)
			switch {
			case errors.Is(err, auth.ErrNoSession):
				sink.ClearSessionCookie()
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			case err != nil:
				logger.LogAttrs(ctx, slog.LevelError, "session lookup failed",
					slog.Any("error", err), slog.String("request_id", RequestID(ctx)))
				writeError(w, http.StatusServiceUnavailable)
				return
			}
			if users != nil {
				if ok, wait := users.Allow(p.UserID); !ok {
					writeTooMany(w, wait)
					return
				}
				uid := p.UserID
				ctx = context.WithValue(ctx, userChargeKey{}, UserCharge(func(n int) (bool, time.Duration) {
					return users.AllowN(uid, n)
				}))
			}
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(ctx, p)))
		})
	}, nil
}

// EnrollmentChecker applies the 2FA policy to a resolved principal (task
// 1.9: *auth.TwoFactorPolicies sets Principal.EnrollmentRequired after the
// enrollment grace period).
type EnrollmentChecker interface {
	ApplyEnrollment(ctx context.Context, p auth.Principal, user sqlc.User) (auth.Principal, error)
}

// SessionsResolver adapts *auth.Sessions (whose Resolve also returns the
// user row) to SessionResolver. With Enrollment set, every principal also
// passes the 2FA policy's enrollment check; a failing check fails the
// request (503), never grants access.
type SessionsResolver struct {
	Sessions   *auth.Sessions
	Enrollment EnrollmentChecker
}

// Resolve returns the principal of token.
func (s SessionsResolver) Resolve(ctx context.Context, token string, meta auth.RequestMeta) (auth.Principal, error) {
	p, user, err := s.Sessions.Resolve(ctx, token, meta)
	if err != nil || s.Enrollment == nil {
		return p, err
	}
	return s.Enrollment.ApplyEnrollment(ctx, p, user)
}

// Check re-validates the session id for a long-lived connection
// (wsconn.Validator) and rebuilds its principal exactly like Resolve does
// for a request, including the 2FA enrollment check (M1 review L2).
func (s SessionsResolver) Check(ctx context.Context, sessionID uuid.UUID) (auth.Principal, error) {
	p, user, err := s.Sessions.Check(ctx, sessionID)
	if err != nil || s.Enrollment == nil {
		return p, err
	}
	return s.Enrollment.ApplyEnrollment(ctx, p, user)
}
