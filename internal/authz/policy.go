package authz

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/graph/apierror"
)

// MaxStepUpWindow is the longest step-up / second-factor freshness window
// (D-047, D-057); security.stepup_max_age can only shorten it.
const MaxStepUpWindow = 5 * time.Minute

// Ownership answers object-level questions about backup owners. Task 3.5
// implements it with job_owners/jobs queries. Implementations must be deny
// by default: on doubt return false or an error (which also denies).
type Ownership interface {
	// IsJobOwner reports whether userID is listed in job_owners of jobID.
	IsJobOwner(ctx context.Context, userID, jobID uuid.UUID) (bool, error)
	// BacksUpDatabase reports whether a (non-deleted) job owned by userID
	// backs up database on connectionID.
	BacksUpDatabase(ctx context.Context, userID, connectionID uuid.UUID, database string) (bool, error)
	// UsesConnection reports whether a (non-deleted) job owned by userID
	// uses connectionID.
	UsesConnection(ctx context.Context, userID, connectionID uuid.UUID) (bool, error)
}

// SecondFactors tells whether a user has a second factor (passkey or TOTP)
// enrolled. Tasks 1.7/1.8 implement it; it decides between
// STEP_UP_REQUIRED and REQUIRES_2FA_ENROLLMENT for FreshAAL2 operations.
type SecondFactors interface {
	HasSecondFactor(ctx context.Context, userID uuid.UUID) (bool, error)
}

// NoOwnership is the Ownership used until jobs exist: nobody owns anything.
type NoOwnership struct{}

// IsJobOwner always reports false.
func (NoOwnership) IsJobOwner(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return false, nil
}

// BacksUpDatabase always reports false.
func (NoOwnership) BacksUpDatabase(context.Context, uuid.UUID, uuid.UUID, string) (bool, error) {
	return false, nil
}

// UsesConnection always reports false.
func (NoOwnership) UsesConnection(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return false, nil
}

// NoSecondFactors is the SecondFactors used until 2FA exists: nobody has a
// factor, so FreshAAL2 operations answer REQUIRES_2FA_ENROLLMENT.
type NoSecondFactors struct{}

// HasSecondFactor always reports false.
func (NoSecondFactors) HasSecondFactor(context.Context, uuid.UUID) (bool, error) { return false, nil }

// Resource is an object an operation acts on: Job or RestoreTarget.
type Resource interface{ resource() }

// Job is a backup job. Resolvers acting on a run, artifact or restore pass
// the Job the object belongs to (loaded from the DB, never from input).
type Job struct{ ID uuid.UUID }

// RestoreTarget is where a restore writes (D-013, D-015, D-068). The
// resolver fills it from the DB, never from client claims.
type RestoreTarget struct {
	ConnectionID uuid.UUID
	// Database is the existing database to overwrite, or the name of the
	// database to create when NewDatabase is set.
	Database    string
	NewDatabase bool
	// Temporary is set for a temporary restore connection (admin only).
	Temporary bool
	// PrivilegedRole is set when the connection's role is a superuser or
	// equivalent (D-068: admin only).
	PrivilegedRole bool
}

func (Job) resource()           {}
func (RestoreTarget) resource() {}

// Options configure New.
type Options struct {
	// Ownership answers owner questions (nil: NoOwnership).
	Ownership Ownership
	// SecondFactors reports enrolled factors (nil: NoSecondFactors).
	SecondFactors SecondFactors
	// StepUpWindow is security.stepup_max_age (0 or above MaxStepUpWindow:
	// MaxStepUpWindow). It also bounds the FreshAAL2 check.
	StepUpWindow time.Duration
}

// Policy is the central authorizer. It is safe for concurrent use.
type Policy struct {
	own     Ownership
	factors SecondFactors
	window  time.Duration
}

// New returns the policy with opts.
func New(opts Options) *Policy {
	p := &Policy{own: opts.Ownership, factors: opts.SecondFactors, window: opts.StepUpWindow}
	if p.own == nil {
		p.own = NoOwnership{}
	}
	if p.factors == nil {
		p.factors = NoSecondFactors{}
	}
	if p.window <= 0 || p.window > MaxStepUpWindow {
		p.window = MaxStepUpWindow
	}
	return p
}

// StepUpWindow is the effective freshness window.
func (p *Policy) StepUpWindow() time.Duration { return p.window }

// User-facing denials. Messages are generic on purpose: they never reveal
// whether an object exists or who owns it.
var (
	errUnauthenticated = apierror.New(apierror.Unauthenticated, "authentication required")
	errForbidden       = apierror.New(apierror.Forbidden, "not allowed")
	errStepUp          = apierror.New(apierror.StepUpRequired, "please confirm your identity again")
	errAAL2            = apierror.New(apierror.StepUpRequired, "a fresh passkey or authenticator code check is required")
	errEnroll          = apierror.New(apierror.Requires2FAEnrollment, "set up a passkey or authenticator app first")
	errPasswordChange  = apierror.New(apierror.PasswordChangeRequired, "change your temporary password first")
	errSecondFactor    = apierror.New(apierror.SecondFactorRequired, "complete the sign-in with your second factor first")
	errEnrollPolicy    = apierror.New(apierror.Requires2FAEnrollment, "two-factor authentication is required: set up a passkey or authenticator app first")
)

// Restricted principals (tasks 1.6, 1.9). Each restriction allows only
// its operations. A pending second factor excludes everything else; the
// password change and enrollment restrictions apply in this order and
// both must allow the operation when both are set.
var (
	// mfaPendingOps: a login waiting for its second factor
	// (auth.Principal.MFAPending) may only complete it or log out.
	mfaPendingOps = map[Operation]bool{
		OpAccountSecondFactorLogin: true, OpAccountLogout: true,
	}
	// passwordChangeOps: a session signed in with a temporary password
	// (auth.Principal.PasswordChangeRequired) may view the own account and
	// sessions, log out, step up and change the password.
	passwordChangeOps = map[Operation]bool{
		OpAccountView: true, OpAccountLogout: true, OpAccountPasswordChange: true,
		OpAccountSessionsView: true, OpAccountSessionsRevoke: true, OpAccountStepUp: true,
	}
	// enrollmentOps: an account that must enroll a second factor after the
	// grace period (auth.Principal.EnrollmentRequired) may view the own
	// account and 2FA status, step up (password), enroll TOTP or a passkey
	// and log out.
	enrollmentOps = map[Operation]bool{
		OpAccountView: true, OpAccountLogout: true, OpAccountStepUp: true, OpAccountSecondFactorEdit: true,
	}
)

// checkRestrictions denies operations a restricted principal may not
// perform: SECOND_FACTOR_REQUIRED, PASSWORD_CHANGE_REQUIRED or
// REQUIRES_2FA_ENROLLMENT.
func checkRestrictions(pr auth.Principal, op Operation) error {
	if pr.MFAPending {
		// The login is not complete: nothing else applies yet.
		if !mfaPendingOps[op] {
			return errSecondFactor
		}
		return nil
	}
	if pr.PasswordChangeRequired && !passwordChangeOps[op] {
		return errPasswordChange
	}
	if pr.EnrollmentRequired && !enrollmentOps[op] {
		return errEnrollPolicy
	}
	return nil
}

// principal returns the caller or UNAUTHENTICATED.
func principal(ctx context.Context) (auth.Principal, error) {
	pr, ok := auth.FromContext(ctx)
	if !ok {
		return auth.Principal{}, errUnauthenticated
	}
	return pr, nil
}

// Authorize decides whether the caller of ctx may perform op on res. It
// returns nil to allow, an *apierror.Error (UNAUTHENTICATED, FORBIDDEN,
// STEP_UP_REQUIRED, REQUIRES_2FA_ENROLLMENT) to deny, or a wrapped internal
// error when a lookup failed (also a denial). Checks run in this order:
// authentication, role/ownership, restore target, freshness, so a caller
// is never asked to step up for something they may not do anyway.
func (p *Policy) Authorize(ctx context.Context, op Operation, res ...Resource) error {
	rule, ok := policy[op]
	if !ok {
		return errForbidden
	}
	pr, err := principal(ctx)
	if err != nil {
		return err
	}
	if err := checkRestrictions(pr, op); err != nil {
		return err
	}
	jobs, targets := splitResources(res)
	if rule.NeedsTarget && len(targets) != 1 {
		return errForbidden
	}
	if !rule.NeedsTarget && len(targets) > 0 {
		return errForbidden
	}
	viaOwner := false
	if !slices.Contains(rule.Roles, pr.Role) {
		if pr.Role != auth.RoleUser {
			return errForbidden
		}
		switch rule.Owner {
		case OwnerList:
			viaOwner = true
		case OwnerJob:
			if err := p.ownsAll(ctx, pr.UserID, jobs); err != nil {
				return err
			}
			viaOwner = true
		default:
			return errForbidden
		}
	}
	for _, t := range targets {
		if err := p.checkTarget(ctx, pr, t, viaOwner); err != nil {
			return err
		}
	}
	return p.checkFresh(ctx, pr, rule.Fresh)
}

// Precheck is the coarse check run for every GraphQL root field before its
// resolver: authentication, then whether the caller's role can perform op
// at all. Freshness is checked when the role alone grants op; when only an
// owner grant applies, the resolver's Authorize call with the resources
// does the object-level and freshness checks.
func (p *Policy) Precheck(ctx context.Context, op Operation) error {
	rule, ok := policy[op]
	if !ok {
		return errForbidden
	}
	pr, err := principal(ctx)
	if err != nil {
		return err
	}
	if err := checkRestrictions(pr, op); err != nil {
		return err
	}
	if slices.Contains(rule.Roles, pr.Role) {
		return p.checkFresh(ctx, pr, rule.Fresh)
	}
	if pr.Role == auth.RoleUser && rule.Owner != OwnerNone {
		return nil
	}
	return errForbidden
}

func splitResources(res []Resource) (jobs []Job, targets []RestoreTarget) {
	for _, r := range res {
		switch v := r.(type) {
		case Job:
			jobs = append(jobs, v)
		case RestoreTarget:
			targets = append(targets, v)
		}
	}
	return jobs, targets
}

// ownsAll requires at least one job and ownership of every job.
func (p *Policy) ownsAll(ctx context.Context, userID uuid.UUID, jobs []Job) error {
	if len(jobs) == 0 {
		return errForbidden
	}
	for _, j := range jobs {
		if j.ID == uuid.Nil {
			return errForbidden
		}
		owns, err := p.own.IsJobOwner(ctx, userID, j.ID)
		if err != nil {
			return fmt.Errorf("authz: job ownership lookup: %w", err)
		}
		if !owns {
			return errForbidden
		}
	}
	return nil
}

// checkTarget applies the restore target rules: temporary connections and
// privileged restore roles are admin only (D-015, D-068); an owner may only
// overwrite a database one of their jobs backs up or create a new database
// on a connection one of their jobs uses (D-013).
func (p *Policy) checkTarget(ctx context.Context, pr auth.Principal, t RestoreTarget, viaOwner bool) error {
	if t.ConnectionID == uuid.Nil || t.Database == "" {
		return errForbidden
	}
	if (t.Temporary || t.PrivilegedRole) && pr.Role != auth.RoleAdmin {
		return errForbidden
	}
	if !viaOwner {
		return nil
	}
	var (
		ok  bool
		err error
	)
	if t.NewDatabase {
		ok, err = p.own.UsesConnection(ctx, pr.UserID, t.ConnectionID)
	} else {
		ok, err = p.own.BacksUpDatabase(ctx, pr.UserID, t.ConnectionID, t.Database)
	}
	if err != nil {
		return fmt.Errorf("authz: restore target lookup: %w", err)
	}
	if !ok {
		return errForbidden
	}
	return nil
}

func (p *Policy) checkFresh(ctx context.Context, pr auth.Principal, f Fresh) error {
	switch f {
	case FreshStepUp:
		if !pr.HasRecentStepUp(p.window) {
			return errStepUp
		}
	case FreshAAL2:
		if pr.HasRecentAAL2(p.window) {
			return nil
		}
		has, err := p.factors.HasSecondFactor(ctx, pr.UserID)
		if err != nil {
			return fmt.Errorf("authz: second factor lookup: %w", err)
		}
		if !has {
			return errEnroll
		}
		return errAAL2
	case FreshNone:
	default:
		return errForbidden
	}
	return nil
}

// Scope is which jobs a caller may see in listings.
type Scope int

// Scopes.
const (
	// ScopeOwned: only jobs the caller owns (backup owners).
	ScopeOwned Scope = iota + 1
	// ScopeAll: every job (admins and maintainers).
	ScopeAll
)

// JobScope returns the job visibility of the caller for OwnerList
// operations. Resolvers of such operations must filter by it.
func (p *Policy) JobScope(ctx context.Context) (Scope, uuid.UUID, error) {
	pr, err := principal(ctx)
	if err != nil {
		return 0, uuid.Nil, err
	}
	switch pr.Role {
	case auth.RoleAdmin, auth.RoleMaintainer:
		return ScopeAll, pr.UserID, nil
	case auth.RoleUser:
		return ScopeOwned, pr.UserID, nil
	default:
		return 0, uuid.Nil, errForbidden
	}
}

// AuthorizeOwnerAssignment authorizes setting the owners of a job: the
// caller needs OpJobOwnersAssign, and a maintainer may not make themselves
// an owner (D-067). Every assignment is audited and alerted by the caller.
func (p *Policy) AuthorizeOwnerAssignment(ctx context.Context, owners []uuid.UUID) error {
	if err := p.Authorize(ctx, OpJobOwnersAssign); err != nil {
		return err
	}
	pr, err := principal(ctx)
	if err != nil {
		return err
	}
	if pr.Role == auth.RoleMaintainer && slices.Contains(owners, pr.UserID) {
		return errForbidden
	}
	return nil
}

// Authenticated allows any valid principal (@authenticated).
func (p *Policy) Authenticated(ctx context.Context) error {
	_, err := principal(ctx)
	return err
}

// IntrospectionAllowed reports whether the caller of ctx may introspect
// the schema outside dev mode (D-050): an authenticated principal without
// any restriction. A login waiting for its second factor, a session with a
// temporary password and an account that must enroll a second factor
// first are denied (M1 review L1).
func (p *Policy) IntrospectionAllowed(ctx context.Context) bool {
	pr, err := principal(ctx)
	if err != nil {
		return false
	}
	return !pr.MFAPending && !pr.PasswordChangeRequired && !pr.EnrollmentRequired
}

// HasRole allows a principal with one of roles (@hasRole). An empty list
// never allows.
func (p *Policy) HasRole(ctx context.Context, roles ...auth.Role) error {
	pr, err := principal(ctx)
	if err != nil {
		return err
	}
	if !slices.Contains(roles, pr.Role) {
		return errForbidden
	}
	return nil
}

// StepUp allows a principal with a step-up within the window (@stepUp).
func (p *Policy) StepUp(ctx context.Context) error {
	pr, err := principal(ctx)
	if err != nil {
		return err
	}
	return p.checkFresh(ctx, pr, FreshStepUp)
}
