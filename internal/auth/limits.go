package auth

import (
	"errors"
	"sync/atomic"
)

// Limits holds the password policy and the lockout policy shared by
// Service, TOTP and UserAdmin. A runtime config reload swaps them
// (security.password_min_*, security.lockout_*; D-200): every check reads
// the current values once. It is safe for concurrent use.
type Limits struct {
	policy  atomic.Pointer[PasswordPolicy]
	lockout atomic.Pointer[LockoutPolicy]
}

// NewLimits returns the holder with the bounds and defaults applied.
func NewLimits(p PasswordPolicy, l LockoutPolicy) *Limits {
	h := &Limits{}
	h.store(p, l)
	return h
}

func (h *Limits) store(p PasswordPolicy, l LockoutPolicy) {
	np := NewPasswordPolicy(p.MinLength, p.MinScore)
	nl := l.normalized()
	h.policy.Store(&np)
	h.lockout.Store(&nl)
}

// Policy returns the current password policy.
func (h *Limits) Policy() PasswordPolicy { return *h.policy.Load() }

// Lockout returns the current lockout policy.
func (h *Limits) Lockout() LockoutPolicy { return *h.lockout.Load() }

// Prepare validates new policies and returns the commit that applies them
// (the commit only does atomic stores). The password bounds are applied
// like NewPasswordPolicy (never below the plan's minimums); the lockout
// needs Threshold >= 1, Base > 0 and Max >= Base.
func (h *Limits) Prepare(p PasswordPolicy, l LockoutPolicy) (func(), error) {
	if l.Threshold < 1 || l.Base <= 0 || l.Max < l.Base {
		return nil, errors.New("auth: lockout needs Threshold >= 1, Base > 0 and Max >= Base")
	}
	if p.MinLength > MaxPasswordLength || p.MinScore > 4 {
		return nil, errors.New("auth: password policy out of bounds")
	}
	return func() { h.store(p, l) }, nil
}
