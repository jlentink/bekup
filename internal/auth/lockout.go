package auth

import (
	"context"
	"net/netip"
	"time"

	"github.com/google/uuid"
)

// Lockout defaults (security.lockout_*).
const (
	DefaultLockoutThreshold = 5
	DefaultLockoutBase      = time.Minute
	DefaultLockoutMax       = time.Hour
)

// LockoutPolicy is the per-account backoff after failed password checks:
// from Threshold consecutive failures on, the account is locked for Base,
// doubling with every further failure, capped at Max. A successful login
// resets the counter. Attempts while locked are refused without checking
// the password and do not extend the lock.
type LockoutPolicy struct {
	Threshold int
	Base      time.Duration
	Max       time.Duration
}

// normalized fills in defaults for unset fields.
func (p LockoutPolicy) normalized() LockoutPolicy {
	if p.Threshold < 1 {
		p.Threshold = DefaultLockoutThreshold
	}
	if p.Base <= 0 {
		p.Base = DefaultLockoutBase
	}
	if p.Max <= 0 {
		p.Max = DefaultLockoutMax
	}
	p.Max = max(p.Max, p.Base)
	return p
}

// LockFor returns how long the account is locked after its failures-th
// consecutive failure (0: not locked).
func (p LockoutPolicy) LockFor(failures int) time.Duration {
	p = p.normalized()
	if failures < p.Threshold {
		return 0
	}
	d := p.Base
	for i := p.Threshold; i < failures; i++ {
		if d >= p.Max/2 {
			return p.Max
		}
		d *= 2
	}
	return min(d, p.Max)
}

// LockoutEvent describes an account that was just locked.
type LockoutEvent struct {
	UserID   uuid.UUID
	Until    time.Time
	Failures int
	// IP is the client address of the failed attempt that triggered it.
	IP netip.Addr
}

// LockoutNotifier is told about lockouts after they are committed (task
// 5.5 raises the failed-login security alert from it). Implementations
// must not block for long; errors are theirs to log.
type LockoutNotifier interface {
	AccountLocked(ctx context.Context, ev LockoutEvent)
}

// NopLockoutNotifier ignores lockouts (until task 5.5).
type NopLockoutNotifier struct{}

// AccountLocked does nothing.
func (NopLockoutNotifier) AccountLocked(context.Context, LockoutEvent) {}

// reserveArgs returns the lock parameters of the attempt-reservation
// queries (ReserveLoginAttempt, ReserveMFAAttempt): the database computes
// LockFor(count) itself, in the statement that counts the attempt.
func (p LockoutPolicy) reserveArgs() (threshold int32, baseSeconds, maxSeconds float64) {
	p = p.normalized()
	return int32(min(p.Threshold, 1<<20)), p.Base.Seconds(), p.Max.Seconds() //nolint:gosec // G115: bounded above
}

// attempt is a reserved, already counted verification (D-117): count is
// the consecutive-failure count including this attempt; lockedUntil is set
// when reserving it reached the threshold and locked the account (the lock
// stays if the verification fails; a success clears it).
type attempt struct {
	count       int32
	lockedUntil *time.Time
}

// locked reports whether reserving this attempt applied a lock.
func (a attempt) locked() bool { return a.lockedUntil != nil }
