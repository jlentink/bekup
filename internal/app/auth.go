package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/pubsub"
)

// newAuth builds the password hasher, the session store and the auth
// service from [security] (task 1.5). Building the hasher runs argon2id
// once (the dummy hash for timing-safe failures).
func (a *App) newAuth() (*auth.Service, error) {
	sec := a.opts.Config.Security
	hasher, err := NewHasher(&sec)
	if err != nil {
		return nil, err
	}
	sessions := auth.NewSessions(a.pool, auth.SessionOptions{
		IdleTimeout:     sec.SessionIdleTimeout.D(),
		AbsoluteTimeout: sec.SessionAbsoluteTimeout.D(),
		TouchInterval:   sec.SessionTouchInterval.D(),
		Logger:          a.opts.Logger,
		// Revocations and rotations reach open event streams (tasks 1.10, 2.13).
		Events: auth.PubSubSessionEvents{Publisher: &pubsub.Publisher{Bus: a.subs.bus}},
	})
	limiter, err := httpserver.NewRateLimiter[netip.Prefix](httpserver.RateLimitOptions{
		PerSecond:  float64(sec.LoginRateLimitPerMinute) / 60,
		Burst:      sec.LoginRateLimitBurst,
		MaxClients: a.opts.Config.Server.RateLimitMaxClients,
	})
	if err != nil {
		return nil, fmt.Errorf("login rate limiter: %w", err)
	}
	// The same rate per account bounds argon2id verifications of logins,
	// password step-ups and password changes across clients (D-117).
	accounts, err := httpserver.NewRateLimiter[string](httpserver.RateLimitOptions{
		PerSecond:  float64(sec.LoginRateLimitPerMinute) / 60,
		Burst:      sec.LoginRateLimitBurst,
		MaxClients: a.opts.Config.Server.RateLimitMaxClients,
	})
	if err != nil {
		return nil, fmt.Errorf("account rate limiter: %w", err)
	}
	a.loginLimiter, a.accountLimiter = limiter, accounts
	return auth.NewService(auth.ServiceOptions{
		DB:       a.pool,
		Hasher:   hasher,
		Policy:   auth.NewPasswordPolicy(sec.PasswordMinLength, sec.PasswordMinScore),
		Sessions: sessions,
		Auditor:  a.auditor,
		// Accounts with a second factor sign in in two steps (task 1.9).
		Factors: a.secondFactors(),
		Lockout: auth.LockoutPolicy{
			Threshold: sec.LockoutThreshold,
			Base:      sec.LockoutBaseDuration.D(),
			Max:       sec.LockoutMaxDuration.D(),
		},
		LoginLimiter:   func(ip netip.Addr) (bool, time.Duration) { return limiter.Allow(httpserver.IPKey(ip)) },
		AccountLimiter: accounts.Allow,
		Logger:         a.opts.Logger,
	})
}

// Session housekeeping: rows that expired or were revoked more than
// sessionRetention ago are deleted every sessionCleanupInterval.
const (
	sessionCleanupInterval = time.Hour
	sessionRetention       = 7 * 24 * time.Hour
)

// sessionCleanupComponent deletes stale session rows periodically. Failures
// are logged and retried at the next tick.
type sessionCleanupComponent struct {
	sessions *auth.Sessions
	logger   *slog.Logger
}

func (c *sessionCleanupComponent) Name() string { return "session cleanup" }

func (c *sessionCleanupComponent) Run(ctx context.Context) error {
	t := time.NewTicker(sessionCleanupInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			n, err := c.sessions.DeleteStale(ctx, sessionRetention)
			if err != nil {
				c.logger.WarnContext(ctx, "session cleanup failed", slog.Any("error", err))
				continue
			}
			if n > 0 {
				c.logger.InfoContext(ctx, "stale sessions deleted", slog.Int64("count", n))
			}
		}
	}
}
