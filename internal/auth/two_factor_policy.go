package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// TwoFactorPolicySetting is the settings key of the 2FA policy (task 1.9,
// D-112).
const TwoFactorPolicySetting = "security.two_factor_policy"

// 2FA policy bounds and defaults (D-112).
const (
	// MaxTwoFactorGraceDays is the longest enrollment grace period.
	MaxTwoFactorGraceDays = 90
	// DefaultTwoFactorGraceDays is the grace period of the default policy.
	DefaultTwoFactorGraceDays = 7
	// twoFactorPolicyCacheTTL bounds how long a policy read is reused for
	// the per-request enrollment check (other serve instances see a change
	// after at most this long; this instance at once).
	twoFactorPolicyCacheTTL = 5 * time.Second
)

// ErrInvalidTwoFactorPolicy means a policy update is out of bounds.
var ErrInvalidTwoFactorPolicy = errors.New("auth: invalid 2FA policy")

// TwoFactorPolicy is the admin-configurable 2FA policy: whether a second
// factor is required for everyone or for some roles, and the grace period
// users without a factor get to enroll one.
type TwoFactorPolicy struct {
	// RequireAll requires a second factor for every role.
	RequireAll bool
	// Roles require a second factor (in addition to RequireAll).
	Roles []Role
	// GraceDays is the enrollment grace period in days (0: immediately).
	GraceDays int
	// Since is when the requirement became effective per role (the grace
	// period starts then, or at account creation or the last second-factor
	// reset if that is later). A required role without an entry counts
	// from account creation (or the reset).
	Since map[Role]time.Time
	// UpdatedAt and UpdatedBy describe the last change (nil: default).
	UpdatedAt *time.Time
	UpdatedBy *uuid.UUID
}

// DefaultTwoFactorPolicy is the policy until an admin changes it (user
// decision, D-112): no role requires a second factor; users may enroll
// one voluntarily. The 7-day grace period applies once an admin requires
// 2FA for a role (it then runs from that moment for existing accounts).
func DefaultTwoFactorPolicy() TwoFactorPolicy {
	return TwoFactorPolicy{GraceDays: DefaultTwoFactorGraceDays, Since: map[Role]time.Time{}}
}

// RequiredFor reports whether role needs a second factor.
func (p TwoFactorPolicy) RequiredFor(role Role) bool {
	return p.RequireAll || slices.Contains(p.Roles, role)
}

// DeadlineFor is the end of the enrollment grace period of an account with
// role created at createdAt whose second factors were last reset at
// resetAt (nil: never; meaningful only when RequiredFor(role)). The grace
// period starts at the latest of the requirement's start for the role,
// the account creation and the reset.
func (p TwoFactorPolicy) DeadlineFor(role Role, createdAt time.Time, resetAt *time.Time) time.Time {
	start := createdAt
	if s, ok := p.Since[role]; ok && s.After(start) {
		start = s
	}
	if resetAt != nil && resetAt.After(start) {
		start = *resetAt
	}
	return start.Add(time.Duration(p.GraceDays) * 24 * time.Hour)
}

// twoFactorPolicyJSON is the stored form (settings.value).
type twoFactorPolicyJSON struct {
	Version    int                  `json:"version"`
	RequireAll bool                 `json:"require_all"`
	Roles      []Role               `json:"roles"`
	GraceDays  int                  `json:"grace_period_days"`
	Since      map[string]time.Time `json:"since,omitempty"`
}

func decodeTwoFactorPolicy(row sqlc.Setting) (TwoFactorPolicy, error) {
	var j twoFactorPolicyJSON
	if err := json.Unmarshal(row.Value, &j); err != nil {
		return TwoFactorPolicy{}, fmt.Errorf("auth: decode 2FA policy: %w", err)
	}
	if j.Version != 1 || j.GraceDays < 0 || j.GraceDays > MaxTwoFactorGraceDays {
		return TwoFactorPolicy{}, fmt.Errorf("auth: stored 2FA policy is invalid (version %d)", j.Version)
	}
	p := TwoFactorPolicy{RequireAll: j.RequireAll, GraceDays: j.GraceDays, Since: map[Role]time.Time{}}
	for _, r := range j.Roles {
		if !r.Valid() {
			return TwoFactorPolicy{}, fmt.Errorf("auth: stored 2FA policy names unknown role %q", r)
		}
		if !slices.Contains(p.Roles, r) {
			p.Roles = append(p.Roles, r)
		}
	}
	for k, v := range j.Since {
		if r := Role(k); r.Valid() {
			p.Since[r] = v
		}
	}
	at := row.UpdatedAt
	p.UpdatedAt = &at
	p.UpdatedBy = row.UpdatedBy
	return p, nil
}

func encodeTwoFactorPolicy(p TwoFactorPolicy) ([]byte, error) {
	j := twoFactorPolicyJSON{Version: 1, RequireAll: p.RequireAll, Roles: p.Roles, GraceDays: p.GraceDays, Since: map[string]time.Time{}}
	if j.Roles == nil {
		j.Roles = []Role{}
	}
	for r, t := range p.Since {
		j.Since[string(r)] = t.UTC()
	}
	return json.Marshal(j)
}

// TwoFactorPolicyUpdate is an admin's new policy.
type TwoFactorPolicyUpdate struct {
	RequireAll bool
	Roles      []Role
	GraceDays  int
}

// TwoFactorPolicyEvent describes a committed policy change that weakens
// the policy (a role no longer requires 2FA or the grace period grew) for
// the security alert of task 5.5.
type TwoFactorPolicyEvent struct {
	ActorID uuid.UUID
	Old     TwoFactorPolicy
	New     TwoFactorPolicy
}

// TwoFactorPolicyNotifier is told about committed weakening changes.
type TwoFactorPolicyNotifier interface {
	TwoFactorPolicyWeakened(ctx context.Context, ev TwoFactorPolicyEvent)
}

// NopTwoFactorPolicyNotifier ignores every event (until task 5.5).
type NopTwoFactorPolicyNotifier struct{}

// TwoFactorPolicyWeakened does nothing.
func (NopTwoFactorPolicyNotifier) TwoFactorPolicyWeakened(context.Context, TwoFactorPolicyEvent) {}

// FactorLister lists the second-factor kinds a user has enrolled
// (*SecondFactors implements it).
type FactorLister interface {
	Enrolled(ctx context.Context, userID uuid.UUID) ([]Factor, error)
}

// TwoFactorPoliciesOptions configure NewTwoFactorPolicies.
type TwoFactorPoliciesOptions struct {
	DB      DB
	Auditor *audit.Auditor
	// Factors lists enrolled factors (required).
	Factors FactorLister
	// StepUpWindow is security.stepup_max_age (at most 5 min).
	StepUpWindow time.Duration
	// Notifier receives weakening changes (nil: none).
	Notifier TwoFactorPolicyNotifier
	Logger   *slog.Logger
	// Now replaces time.Now (tests).
	Now func() time.Time
}

// TwoFactorPolicies stores and applies the 2FA policy (task 1.9, D-112):
// reading and changing it, the per-request enrollment check that restricts
// principals after the grace period, and the last-factor guard
// (FactorRemovalGuard). It is safe for concurrent use.
type TwoFactorPolicies struct {
	o TwoFactorPoliciesOptions

	mu       sync.Mutex
	cached   TwoFactorPolicy
	cachedAt time.Time
}

// NewTwoFactorPolicies returns the service.
func NewTwoFactorPolicies(o TwoFactorPoliciesOptions) (*TwoFactorPolicies, error) {
	if o.DB == nil || o.Auditor == nil || o.Factors == nil {
		return nil, errors.New("auth: DB, Auditor and Factors are required")
	}
	if o.StepUpWindow <= 0 || o.StepUpWindow > maxStepUpWindow {
		o.StepUpWindow = maxStepUpWindow
	}
	if o.Notifier == nil {
		o.Notifier = NopTwoFactorPolicyNotifier{}
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &TwoFactorPolicies{o: o}, nil
}

// policyFrom decodes the result of a settings read (the default policy
// when none is stored).
func policyFrom(row sqlc.Setting, err error) (TwoFactorPolicy, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultTwoFactorPolicy(), nil
	}
	if err != nil {
		return TwoFactorPolicy{}, fmt.Errorf("auth: load 2FA policy: %w", err)
	}
	return decodeTwoFactorPolicy(row)
}

// Get returns the current policy, read from the database (uncached).
func (s *TwoFactorPolicies) Get(ctx context.Context) (TwoFactorPolicy, error) {
	row, err := sqlc.New(s.o.DB).GetSetting(ctx, TwoFactorPolicySetting)
	p, err := policyFrom(row, err)
	if err != nil {
		return TwoFactorPolicy{}, err
	}
	s.mu.Lock()
	s.cached, s.cachedAt = p, s.o.Now()
	s.mu.Unlock()
	return p, nil
}

// current returns the policy, reusing a read younger than the cache TTL.
func (s *TwoFactorPolicies) current(ctx context.Context) (TwoFactorPolicy, error) {
	s.mu.Lock()
	if !s.cachedAt.IsZero() && s.o.Now().Sub(s.cachedAt) < twoFactorPolicyCacheTTL && !s.cachedAt.After(s.o.Now()) {
		p := s.cached
		s.mu.Unlock()
		return p, nil
	}
	s.mu.Unlock()
	return s.Get(ctx)
}

// Update replaces the policy (admin, fresh step-up; the authz policy and
// the @stepUp directive check it first). Turning a role's requirement on
// starts its grace period now; keeping it keeps the start. The change is
// audited (old and new values) in the same transaction; a weakening change
// is also logged and reported to the Notifier.
func (s *TwoFactorPolicies) Update(ctx context.Context, actor Principal, in TwoFactorPolicyUpdate) (TwoFactorPolicy, error) {
	if !actor.Valid() || actor.Role != RoleAdmin {
		return TwoFactorPolicy{}, errors.New("auth: only an admin may change the 2FA policy")
	}
	if !fresh(actor.StepUpAt, s.o.StepUpWindow, s.o.Now()) {
		return TwoFactorPolicy{}, ErrStepUpRequired
	}
	if in.GraceDays < 0 || in.GraceDays > MaxTwoFactorGraceDays {
		return TwoFactorPolicy{}, fmt.Errorf("%w: the grace period must be 0 to %d days", ErrInvalidTwoFactorPolicy, MaxTwoFactorGraceDays)
	}
	var roles []Role
	for _, r := range in.Roles {
		if !r.Valid() {
			return TwoFactorPolicy{}, fmt.Errorf("%w: unknown role", ErrInvalidTwoFactorPolicy)
		}
		if !slices.Contains(roles, r) {
			roles = append(roles, r)
		}
	}
	slices.Sort(roles)
	meta := RequestMetaFrom(ctx)
	var old, next TwoFactorPolicy
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		row, err := q.GetSettingForUpdate(ctx, TwoFactorPolicySetting)
		if old, err = policyFrom(row, err); err != nil {
			return err
		}
		now := s.o.Now().UTC()
		next = TwoFactorPolicy{RequireAll: in.RequireAll, Roles: roles, GraceDays: in.GraceDays, Since: map[Role]time.Time{}}
		for _, r := range Roles() {
			switch was, is := old.RequiredFor(r), next.RequiredFor(r); {
			case is && was:
				if t, ok := old.Since[r]; ok {
					next.Since[r] = t
				}
			case is:
				next.Since[r] = now
			}
		}
		value, err := encodeTwoFactorPolicy(next)
		if err != nil {
			return fmt.Errorf("auth: encode 2FA policy: %w", err)
		}
		uid := actor.UserID
		saved, err := q.PutSetting(ctx, sqlc.PutSettingParams{Key: TwoFactorPolicySetting, Value: value, UpdatedBy: &uid})
		if err != nil {
			return fmt.Errorf("auth: store 2FA policy: %w", err)
		}
		at := saved.UpdatedAt
		next.UpdatedAt, next.UpdatedBy = &at, &uid
		_, err = s.o.Auditor.RecordTx(ctx, q, audit.Event{
			Actor: audit.UserActor(actor.UserID), Action: audit.ActionMFAPolicyUpdated, IP: meta.IP,
			TargetType: "setting", TargetID: TwoFactorPolicySetting,
			Details: audit.Details{"old": policyDetails(old), "new": policyDetails(next), "weakened": weakens(old, next)},
		})
		return err
	})
	if err != nil {
		return TwoFactorPolicy{}, err
	}
	s.mu.Lock()
	s.cached, s.cachedAt = next, s.o.Now()
	s.mu.Unlock()
	if weakens(old, next) {
		s.o.Logger.WarnContext(ctx, "2FA policy weakened", slog.String("user_id", actor.UserID.String()),
			slog.Bool("require_all", next.RequireAll), slog.Int("grace_period_days", next.GraceDays))
		s.o.Notifier.TwoFactorPolicyWeakened(ctx, TwoFactorPolicyEvent{ActorID: actor.UserID, Old: old, New: next})
	}
	return next, nil
}

func policyDetails(p TwoFactorPolicy) map[string]any {
	roles := make([]string, 0, len(p.Roles))
	for _, r := range p.Roles {
		roles = append(roles, string(r))
	}
	return map[string]any{"require_all": p.RequireAll, "roles": roles, "grace_period_days": p.GraceDays}
}

// weakens reports whether next requires 2FA for fewer roles or gives more
// time than old.
func weakens(old, next TwoFactorPolicy) bool {
	for _, r := range Roles() {
		if old.RequiredFor(r) && !next.RequiredFor(r) {
			return true
		}
	}
	return next.GraceDays > old.GraceDays
}

// EnrollmentStatus is a user's standing under the 2FA policy.
type EnrollmentStatus struct {
	// Required: the policy requires a second factor for the user's role.
	Required bool
	// HasFactor: the user has a second factor enrolled.
	HasFactor bool
	// Deadline is the end of the grace period (set when Required and not
	// HasFactor).
	Deadline *time.Time
	// Restricted: Required, no factor and the deadline has passed: the
	// principal may only enroll a factor (REQUIRES_2FA_ENROLLMENT).
	Restricted bool
}

// Status returns the standing of the account userID (loaded from the
// database).
func (s *TwoFactorPolicies) Status(ctx context.Context, userID uuid.UUID) (EnrollmentStatus, error) {
	user, err := sqlc.New(s.o.DB).GetUserByID(ctx, userID)
	if err != nil {
		return EnrollmentStatus{}, fmt.Errorf("auth: load user: %w", err)
	}
	return s.statusFor(ctx, user)
}

// statusFor returns the standing of the account row user.
func (s *TwoFactorPolicies) statusFor(ctx context.Context, user sqlc.User) (EnrollmentStatus, error) {
	p, err := s.current(ctx)
	if err != nil {
		return EnrollmentStatus{}, err
	}
	role := Role(user.Role)
	if !p.RequiredFor(role) {
		return EnrollmentStatus{}, nil
	}
	enrolled, err := s.o.Factors.Enrolled(ctx, user.ID)
	if err != nil {
		return EnrollmentStatus{}, err
	}
	if len(enrolled) > 0 {
		return EnrollmentStatus{Required: true, HasFactor: true}, nil
	}
	d := p.DeadlineFor(role, user.CreatedAt, user.MfaResetAt)
	return EnrollmentStatus{Required: true, Deadline: &d, Restricted: !s.o.Now().Before(d)}, nil
}

// ApplyEnrollment sets p.EnrollmentRequired from the policy for the
// session's user (the session middleware calls it for every request). A
// session that still waits for its second factor is returned unchanged
// (it is restricted further anyway). Errors deny (the caller answers 503).
func (s *TwoFactorPolicies) ApplyEnrollment(ctx context.Context, p Principal, user sqlc.User) (Principal, error) {
	if p.MFAPending {
		return p, nil
	}
	st, err := s.statusFor(ctx, user)
	if err != nil {
		return Principal{}, err
	}
	p.EnrollmentRequired = st.Restricted
	return p, nil
}

// CheckFactorRemoval implements FactorRemovalGuard: removing the last
// second factor is refused with ErrLastFactor (and audited) while the
// policy requires 2FA for the user's role, also during a grace period.
// It reads the policy uncached.
func (s *TwoFactorPolicies) CheckFactorRemoval(ctx context.Context, userID uuid.UUID, removing Factor, remaining []Factor) error {
	if len(remaining) > 0 {
		return nil
	}
	user, err := sqlc.New(s.o.DB).GetUserByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("auth: load user: %w", err)
	}
	p, err := s.Get(ctx)
	if err != nil {
		return err
	}
	if !p.RequiredFor(Role(user.Role)) {
		return nil
	}
	if _, err := s.o.Auditor.Record(ctx, audit.Event{
		Actor: audit.UserActor(userID), Action: audit.ActionAuthMFARemovalRefused, IP: RequestMetaFrom(ctx).IP,
		TargetType: "user", TargetID: userID.String(),
		Details: audit.Details{"factor": string(removing), "role": user.Role},
	}); err != nil {
		return err
	}
	return ErrLastFactor
}
