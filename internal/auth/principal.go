package auth

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Role is a bekup user role (the user_role enum of the app DB, D-011).
type Role string

// Roles.
const (
	RoleAdmin      Role = "admin"
	RoleMaintainer Role = "maintainer"
	RoleUser       Role = "user"
)

// Roles returns every role.
func Roles() []Role { return []Role{RoleAdmin, RoleMaintainer, RoleUser} }

// Valid reports whether r is a known role.
func (r Role) Valid() bool {
	switch r {
	case RoleAdmin, RoleMaintainer, RoleUser:
		return true
	default:
		return false
	}
}

// clockSkew is how far in the future a step-up/AAL2 timestamp may lie and
// still count (clock differences between serve instances and the DB).
const clockSkew = 5 * time.Second

// Principal is the authenticated caller of a request: the user behind a
// valid session. Task 1.5 builds it from the session row and attaches it
// with WithPrincipal; everything that authorizes (internal/authz, the
// GraphQL directives) reads it with FromContext.
type Principal struct {
	UserID    uuid.UUID
	Role      Role
	SessionID uuid.UUID
	// AAL is the session's authentication assurance level (1: password,
	// 2: password + second factor).
	AAL int
	// StepUpAt is the time of the last step-up (fresh re-authentication);
	// zero if none.
	StepUpAt time.Time
	// AAL2At is the time of the last second-factor check (passkey or TOTP,
	// not a password); zero if none.
	AAL2At time.Time
	// PasswordChangeRequired is set while the account signs in with an
	// admin-issued temporary password (task 1.6): only the own account,
	// sessions, logout and changePassword are allowed until it is changed.
	PasswordChangeRequired bool
	// MFAPending is set on the session of a login whose password step
	// succeeded for an account with a second factor (task 1.9): until the
	// second factor completes, only that and logout are allowed.
	MFAPending bool
	// EnrollmentRequired is set when the 2FA policy requires a second
	// factor for the user's role, the user has none and the enrollment
	// grace period is over (task 1.9): only enrolling a factor, viewing the
	// own account and logging out are allowed.
	EnrollmentRequired bool
}

// Valid reports whether p is complete enough to authorize anything: a
// non-nil user and session and a known role.
func (p Principal) Valid() bool {
	return p.UserID != uuid.Nil && p.SessionID != uuid.Nil && p.Role.Valid()
}

// HasRecentStepUp reports whether the last step-up is at most window old.
func (p Principal) HasRecentStepUp(window time.Duration) bool {
	return fresh(p.StepUpAt, window, time.Now())
}

// HasRecentAAL2 reports whether the last second-factor check is at most
// window old (the restore gate, D-057).
func (p Principal) HasRecentAAL2(window time.Duration) bool {
	return p.AAL >= 2 && fresh(p.AAL2At, window, time.Now())
}

// fresh reports whether ts is set, not in the future (beyond clockSkew)
// and at most window before now. A non-positive window is never fresh.
func fresh(ts time.Time, window time.Duration, now time.Time) bool {
	if ts.IsZero() || window <= 0 {
		return false
	}
	if ts.After(now.Add(clockSkew)) {
		return false
	}
	return now.Sub(ts) <= window
}

type principalKey struct{}

// WithPrincipal returns ctx carrying p. Only the session middleware (task
// 1.5) and tests call it.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// FromContext returns the principal of ctx. It reports false when there is
// none or it is incomplete (Valid is false): such a caller is anonymous.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	if !ok || !p.Valid() {
		return Principal{}, false
	}
	return p, true
}
