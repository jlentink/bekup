// Package serviceutil holds the small building blocks shared by the
// services behind the GraphQL API that manage outbound endpoints
// (internal/connections, internal/destinations; D-140): presentable field
// errors, the per-user rate limit plus concurrency bound of network
// operations, and the unique-violation check of the app database. The SSH
// parts live in internal/sshpolicy.
package serviceutil

import (
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jlentink/bekup/internal/httpserver"
)

// FieldError is an invalid input; Field names the input field (GraphQL
// path such as "ssh.password") and Message is presentable.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return "invalid " + e.Field + ": " + e.Message }

// FieldErr returns a *FieldError with a formatted message.
func FieldErr(field, format string, a ...any) error {
	return &FieldError{Field: field, Message: fmt.Sprintf(format, a...)}
}

// RateLimitedError reports that the caller exceeded the per-user limit of
// network operations.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string { return "rate limited" }

// ErrBusy reports that too many network operations run at the same time.
var ErrBusy = errors.New("too many operations in progress")

// Default limits of NewLimiter.
const (
	DefaultPerMinute     = 10
	DefaultBurst         = 5
	DefaultMaxConcurrent = 4
)

// Limiter applies a per-key (per-user) rate limit and a global bound on
// concurrent operations. It never queues: a caller over either limit is
// refused at once.
type Limiter struct {
	sem   chan struct{}
	allow func(string) (bool, time.Duration)
}

// NewLimiter returns a limiter with at most maxConcurrent operations at
// once (DefaultMaxConcurrent when ≤ 0). allow is the per-key limit; nil
// uses DefaultPerMinute per minute with a burst of DefaultBurst.
func NewLimiter(maxConcurrent int, allow func(key string) (bool, time.Duration)) (*Limiter, error) {
	if maxConcurrent <= 0 {
		maxConcurrent = DefaultMaxConcurrent
	}
	if allow == nil {
		rl, err := httpserver.NewRateLimiter[string](httpserver.RateLimitOptions{
			PerSecond: DefaultPerMinute / 60.0, Burst: DefaultBurst, MaxClients: 1000,
		})
		if err != nil {
			return nil, err
		}
		allow = rl.Allow
	}
	return &Limiter{sem: make(chan struct{}, maxConcurrent), allow: allow}, nil
}

// Acquire takes a token for key and a concurrency slot. It returns
// *RateLimitedError or ErrBusy when refused; otherwise call release when
// the operation ended.
func (l *Limiter) Acquire(key string) (release func(), err error) {
	if ok, wait := l.allow(key); !ok {
		return nil, &RateLimitedError{RetryAfter: wait}
	}
	select {
	case l.sem <- struct{}{}:
		return func() { <-l.sem }, nil
	default:
		return nil, ErrBusy
	}
}

// IsUniqueViolation reports a unique constraint violation (SQLSTATE 23505).
func IsUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}
