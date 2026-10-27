package resolvers

import (
	"context"
	"errors"
	"time"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/httpserver"
)

var (
	errSecondFactorNotConfigured = errors.New("resolvers: second-factor flow not configured")
	errPolicyNotConfigured       = errors.New("resolvers: 2FA policy not configured")
)

// secondFactorError maps second-factor flow errors to API errors; field
// names the input a rejected code or response belongs to. Anything else
// stays an internal (masked) error.
func secondFactorError(err error, field string) error {
	var locked *auth.SecondFactorLockedError
	var rl *auth.RateLimitedError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, auth.ErrNotPending):
		return apierror.New(apierror.Conflict, "no sign-in is waiting for a second factor on this session")
	case errors.Is(err, auth.ErrSecondFactorPending):
		return apierror.New(apierror.SecondFactorRequired, "complete the sign-in with your second factor first")
	case errors.Is(err, auth.ErrPasskeysUnavailable):
		return apierror.New(apierror.Forbidden, "passkeys cannot be used for this account on this server")
	case errors.Is(err, auth.ErrInvalidCode):
		if field == "assertion" {
			return apierror.Invalid(field, "the passkey response could not be verified; start again")
		}
		return apierror.Invalid(field, "the code is not valid")
	case errors.As(err, &locked):
		return apierror.Newf(apierror.RateLimited, "too many failed attempts; try again in %d seconds",
			httpserver.RetryAfterSeconds(locked.RetryAfter))
	case errors.As(err, &rl):
		return apierror.Newf(apierror.RateLimited, "too many attempts; try again in %d seconds",
			httpserver.RetryAfterSeconds(rl.RetryAfter))
	case errors.Is(err, auth.ErrBusy):
		return errBusy
	case errors.Is(err, auth.ErrWrongPassword):
		return apierror.Invalid("password", "the password is wrong")
	case errors.Is(err, auth.ErrFactorRequired):
		return apierror.Invalid("method", "your account has a second factor: confirm with it instead of the password")
	case errors.Is(err, auth.ErrMethodUnavailable), errors.Is(err, auth.ErrNoSecondFactor):
		return apierror.Invalid("method", "this method is not available for your account")
	case errors.Is(err, auth.ErrSessionNotFound):
		return errUnauthenticated
	default:
		return err
	}
}

func (r *Resolver) secondFactorFlow(ctx context.Context) (*auth.SecondFactorFlow, auth.Principal, error) {
	if r.SecondFactor == nil {
		return nil, auth.Principal{}, errSecondFactorNotConfigured
	}
	p, err := caller(ctx)
	if err != nil {
		return nil, auth.Principal{}, err
	}
	return r.SecondFactor, p, nil
}

func factorOf(k model.SecondFactorKind) (auth.Factor, bool) {
	switch k {
	case model.SecondFactorKindTotp:
		return auth.FactorTOTP, true
	case model.SecondFactorKindPasskey:
		return auth.FactorPasskey, true
	case model.SecondFactorKindRecoveryCode:
		return auth.FactorRecoveryCode, true
	default:
		return "", false
	}
}

// secondFactorInput checks that the input a method needs is present (a
// missing value is a VALIDATION error, not a counted failure) and returns
// the code and the field a rejection is reported on.
func secondFactorInput(f auth.Factor, code *string, assertion model.JSON) (string, string, error) {
	if f == auth.FactorPasskey {
		if len(assertion) == 0 {
			return "", "assertion", apierror.Invalid("assertion", "the passkey response is required")
		}
		return "", "assertion", nil
	}
	if code == nil || *code == "" {
		return "", "code", apierror.Invalid("code", "the code is required")
	}
	return *code, "code", nil
}

func (r *Resolver) beginPasskeyLogin(ctx context.Context) (model.JSON, error) {
	flow, p, err := r.secondFactorFlow(ctx)
	if err != nil {
		return nil, err
	}
	out, err := flow.BeginPasskeyLogin(ctx, p)
	if err != nil {
		return nil, secondFactorError(err, "assertion")
	}
	return model.JSON(out), nil
}

func (r *Resolver) completeSecondFactor(ctx context.Context, method model.SecondFactorKind, code *string, assertion model.JSON) (*model.LoginResult, error) {
	flow, p, err := r.secondFactorFlow(ctx)
	if err != nil {
		return nil, err
	}
	svc, err := r.authService()
	if err != nil {
		return nil, err
	}
	cookies, ok := auth.CookieSetterFrom(ctx)
	if !ok {
		return nil, errNoCookieSetter
	}
	if !p.MFAPending {
		return nil, secondFactorError(auth.ErrNotPending, "")
	}
	f, ok := factorOf(method)
	if !ok {
		return nil, apierror.Invalid("method", "unknown method")
	}
	value, field, err := secondFactorInput(f, code, assertion)
	if err != nil {
		return nil, err
	}
	issued, err := flow.CompleteSecondFactor(ctx, p, f, value, assertion)
	if err != nil {
		return nil, secondFactorError(err, field)
	}
	cookies.SetSessionCookie(issued.Token)
	u, err := svc.User(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	user, err := userModel(u)
	if err != nil {
		return nil, err
	}
	return &model.LoginResult{
		User: user, AvailableFactors: []model.SecondFactorKind{},
		PasswordChangeRequired: u.PasswordTemporaryUntil != nil,
	}, nil
}

func stepUpMethodOf(m model.StepUpMethod) (auth.StepUpMethod, bool) {
	switch m {
	case model.StepUpMethodPassword:
		return auth.StepUpPassword, true
	case model.StepUpMethodTotp:
		return auth.StepUpTOTP, true
	case model.StepUpMethodPasskey:
		return auth.StepUpPasskey, true
	case model.StepUpMethodRecoveryCode:
		return auth.StepUpRecoveryCode, true
	default:
		return "", false
	}
}

func stepUpMethodModel(m auth.StepUpMethod) (model.StepUpMethod, bool) {
	switch m {
	case auth.StepUpPassword:
		return model.StepUpMethodPassword, true
	case auth.StepUpTOTP:
		return model.StepUpMethodTotp, true
	case auth.StepUpPasskey:
		return model.StepUpMethodPasskey, true
	case auth.StepUpRecoveryCode:
		return model.StepUpMethodRecoveryCode, true
	default:
		return "", false
	}
}

func (r *Resolver) beginStepUp(ctx context.Context) (*model.StepUpChallenge, error) {
	flow, p, err := r.secondFactorFlow(ctx)
	if err != nil {
		return nil, err
	}
	ch, err := flow.BeginStepUp(ctx, p)
	if err != nil {
		return nil, secondFactorError(err, "")
	}
	out := &model.StepUpChallenge{Methods: []model.StepUpMethod{}}
	for _, m := range ch.Methods {
		if mm, ok := stepUpMethodModel(m); ok {
			out.Methods = append(out.Methods, mm)
		}
	}
	if len(ch.PasskeyOptions) > 0 {
		out.PasskeyOptions = model.JSON(ch.PasskeyOptions)
	}
	return out, nil
}

func (r *Resolver) completeStepUp(ctx context.Context, method model.StepUpMethod, code, password *string, assertion model.JSON) (*model.StepUpResult, error) {
	flow, p, err := r.secondFactorFlow(ctx)
	if err != nil {
		return nil, err
	}
	cookies, ok := auth.CookieSetterFrom(ctx)
	if !ok {
		return nil, errNoCookieSetter
	}
	m, ok := stepUpMethodOf(method)
	if !ok {
		return nil, apierror.Invalid("method", "unknown method")
	}
	var value, field string
	if m == auth.StepUpPassword {
		if password == nil || *password == "" {
			return nil, apierror.Invalid("password", "the password is required")
		}
		value, field = *password, "password"
	} else if value, field, err = secondFactorInput(auth.Factor(m), code, assertion); err != nil {
		return nil, err
	}
	res, err := flow.CompleteStepUp(ctx, p, m, value, assertion)
	if err != nil {
		return nil, secondFactorError(err, field)
	}
	if res.Issued != nil {
		cookies.SetSessionCookie(res.Issued.Token)
	}
	return &model.StepUpResult{StepUpAt: res.StepUpAt, Aal2At: res.AAL2At, Aal: res.AAL}, nil
}

func (r *Resolver) policyService() (*auth.TwoFactorPolicies, error) {
	if r.TwoFactorPolicy == nil {
		return nil, errPolicyNotConfigured
	}
	return r.TwoFactorPolicy, nil
}

func (r *Resolver) policyModel(p auth.TwoFactorPolicy) *model.TwoFactorPolicy {
	out := &model.TwoFactorPolicy{
		RequireForAll: p.RequireAll, RequiredRoles: []model.Role{}, GracePeriodDays: p.GraceDays,
		UpdatedAt: p.UpdatedAt, PasskeysAvailable: r.Passkeys != nil && r.Passkeys.Available(),
	}
	for _, role := range p.Roles {
		if m, ok := roleModel(role); ok {
			out.RequiredRoles = append(out.RequiredRoles, m)
		}
	}
	return out
}

func roleModel(r auth.Role) (model.Role, bool) {
	switch r {
	case auth.RoleAdmin:
		return model.RoleAdmin, true
	case auth.RoleMaintainer:
		return model.RoleMaintainer, true
	case auth.RoleUser:
		return model.RoleUser, true
	default:
		return "", false
	}
}

func roleOf(m model.Role) (auth.Role, bool) {
	switch m {
	case model.RoleAdmin:
		return auth.RoleAdmin, true
	case model.RoleMaintainer:
		return auth.RoleMaintainer, true
	case model.RoleUser:
		return auth.RoleUser, true
	default:
		return "", false
	}
}

func (r *Resolver) twoFactorPolicy(ctx context.Context) (*model.TwoFactorPolicy, error) {
	svc, err := r.policyService()
	if err != nil {
		return nil, err
	}
	p, err := svc.Get(ctx)
	if err != nil {
		return nil, err
	}
	return r.policyModel(p), nil
}

func (r *Resolver) updateTwoFactorPolicy(ctx context.Context, in model.TwoFactorPolicyInput) (*model.TwoFactorPolicy, error) {
	svc, err := r.policyService()
	if err != nil {
		return nil, err
	}
	p, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	upd := auth.TwoFactorPolicyUpdate{RequireAll: in.RequireForAll, GraceDays: in.GracePeriodDays}
	for _, m := range in.RequiredRoles {
		role, ok := roleOf(m)
		if !ok {
			return nil, apierror.Invalid("requiredRoles", "unknown role")
		}
		upd.Roles = append(upd.Roles, role)
	}
	next, err := svc.Update(ctx, p, upd)
	switch {
	case errors.Is(err, auth.ErrInvalidTwoFactorPolicy):
		return nil, apierror.Invalid("gracePeriodDays", "the grace period must be 0 to 90 days")
	case errors.Is(err, auth.ErrStepUpRequired):
		return nil, apierror.New(apierror.StepUpRequired, "please confirm your identity again")
	case err != nil:
		return nil, err
	}
	return r.policyModel(next), nil
}

// enrollmentStatus is the 2FA standing of a user (not required when the
// policy service is not configured).
func (r *Resolver) enrollmentStatus(ctx context.Context, u *model.User) (auth.EnrollmentStatus, error) {
	if r.TwoFactorPolicy == nil || u == nil {
		return auth.EnrollmentStatus{}, nil
	}
	return r.TwoFactorPolicy.Status(ctx, u.ID)
}

func (r *Resolver) userTwoFactorRequired(ctx context.Context, u *model.User) (bool, error) {
	st, err := r.enrollmentStatus(ctx, u)
	return st.Required, err
}

func (r *Resolver) userTwoFactorEnrollmentDeadline(ctx context.Context, u *model.User) (*time.Time, error) {
	st, err := r.enrollmentStatus(ctx, u)
	return st.Deadline, err
}
