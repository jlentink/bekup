package auth

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/google/uuid"
)

// FactorChecker reports whether a user has enrolled one kind of second
// factor. Each factor package (TOTP: task 1.7, passkeys: task 1.8)
// implements it and is registered with SecondFactors; recovery codes are
// not a factor of their own (they only stand in for one).
type FactorChecker interface {
	// Kind is the factor kind this checker reports.
	Kind() Factor
	// HasFactor reports whether userID has at least one usable factor of
	// this kind enrolled. An error means "unknown" and denies.
	HasFactor(ctx context.Context, userID uuid.UUID) (bool, error)
}

// SecondFactors aggregates the registered FactorCheckers. It implements
// authz.SecondFactors (HasSecondFactor), so REQUIRES_2FA_ENROLLMENT vs.
// STEP_UP_REQUIRED and the 2FA policy (task 1.9) see every factor kind.
// It is safe for concurrent use; register checkers at startup.
type SecondFactors struct {
	mu       sync.RWMutex
	checkers []FactorChecker
}

// NewSecondFactors returns an aggregator with checkers registered.
func NewSecondFactors(checkers ...FactorChecker) *SecondFactors {
	s := &SecondFactors{}
	for _, c := range checkers {
		s.Register(c)
	}
	return s
}

// Register adds c. A second checker of the same kind replaces the first
// (wiring code registers each kind once).
func (s *SecondFactors) Register(c FactorChecker) {
	if c == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkers = slices.DeleteFunc(s.checkers, func(o FactorChecker) bool { return o.Kind() == c.Kind() })
	s.checkers = append(s.checkers, c)
}

func (s *SecondFactors) snapshot() []FactorChecker {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.checkers)
}

// Enrolled returns the factor kinds userID has enrolled, in registration
// order. Any checker error fails the whole call (deny on doubt).
func (s *SecondFactors) Enrolled(ctx context.Context, userID uuid.UUID) ([]Factor, error) {
	var out []Factor
	for _, c := range s.snapshot() {
		ok, err := c.HasFactor(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("auth: check %s factor: %w", c.Kind(), err)
		}
		if ok {
			out = append(out, c.Kind())
		}
	}
	return out, nil
}

// HasSecondFactor reports whether userID has any second factor enrolled
// (authz.SecondFactors).
func (s *SecondFactors) HasSecondFactor(ctx context.Context, userID uuid.UUID) (bool, error) {
	for _, c := range s.snapshot() {
		ok, err := c.HasFactor(ctx, userID)
		if err != nil {
			return false, fmt.Errorf("auth: check %s factor: %w", c.Kind(), err)
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// ErrLastFactor is returned by a FactorRemovalGuard that refuses to let a
// user remove their last second factor (task 1.9's policy).
var ErrLastFactor = errors.New("auth: cannot remove the last second factor while 2FA is required")

// FactorRemovalGuard decides whether a user may remove a second factor
// (the last-factor protection of task 1.9's 2FA policy). remaining are the
// factor kinds the user would still have afterwards (empty: none). Return
// nil to allow, ErrLastFactor (or another error) to refuse.
type FactorRemovalGuard interface {
	CheckFactorRemoval(ctx context.Context, userID uuid.UUID, removing Factor, remaining []Factor) error
}

// AllowFactorRemoval is the FactorRemovalGuard used until the 2FA policy
// exists (task 1.9): every removal is allowed.
type AllowFactorRemoval struct{}

// CheckFactorRemoval always allows.
func (AllowFactorRemoval) CheckFactorRemoval(context.Context, uuid.UUID, Factor, []Factor) error {
	return nil
}
