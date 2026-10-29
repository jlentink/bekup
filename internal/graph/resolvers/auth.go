package resolvers

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/httpserver"
)

var (
	errAuthNotConfigured = errors.New("resolvers: auth service not configured")
	errNoCookieSetter    = errors.New("resolvers: no session cookie setter in the request context")
	// errInvalidCredentials is the single answer to every failed login.
	errInvalidCredentials = apierror.New(apierror.Unauthenticated, "invalid email or password")
	errBusy               = apierror.New(apierror.RateLimited, "the server is busy; please try again in a moment")
	errSessionNotFound    = apierror.New(apierror.NotFound, "session not found")
	errUnauthenticated    = apierror.New(apierror.Unauthenticated, "authentication required")
)

// authError maps service errors to API errors; anything else stays an
// internal (masked) error.
func authError(err error) error {
	var rl *auth.RateLimitedError
	var pe *auth.PolicyError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &rl):
		return apierror.Newf(apierror.RateLimited, "too many login attempts; try again in %d seconds",
			httpserver.RetryAfterSeconds(rl.RetryAfter))
	case errors.Is(err, auth.ErrBusy):
		return errBusy
	case errors.Is(err, auth.ErrInvalidCredentials):
		return errInvalidCredentials
	case errors.As(err, &pe):
		return apierror.Invalid("newPassword", pe.Message)
	case errors.Is(err, auth.ErrWrongPassword):
		return apierror.Invalid("currentPassword", "the current password is wrong")
	case errors.Is(err, auth.ErrSessionNotFound):
		return errSessionNotFound
	default:
		return err
	}
}

func (r *Resolver) authService() (*auth.Service, error) {
	if r.Auth == nil {
		return nil, errAuthNotConfigured
	}
	return r.Auth, nil
}

// caller returns the principal; the directives guarantee one, this is
// defense in depth.
func caller(ctx context.Context) (auth.Principal, error) {
	p, ok := auth.FromContext(ctx)
	if !ok {
		return auth.Principal{}, errUnauthenticated
	}
	return p, nil
}

func (r *Resolver) login(ctx context.Context, email, password string) (*model.LoginResult, error) {
	svc, err := r.authService()
	if err != nil {
		return nil, err
	}
	cookies, ok := auth.CookieSetterFrom(ctx)
	if !ok {
		return nil, errNoCookieSetter
	}
	var current uuid.UUID
	if p, ok := auth.FromContext(ctx); ok {
		current = p.SessionID
	}
	res, err := svc.Login(ctx, email, password, auth.RequestMetaFrom(ctx), current)
	if err != nil {
		return nil, authError(err)
	}
	cookies.SetSessionCookie(res.Issued.Token)
	factors := make([]model.SecondFactorKind, 0, len(res.AvailableFactors))
	for _, f := range res.AvailableFactors {
		if f == auth.FactorPasskey && (r.Passkeys == nil || !r.Passkeys.Available()) {
			// Passkeys cannot be used with this server (D-095).
			continue
		}
		if k, ok := factorKind(f); ok {
			factors = append(factors, k)
		}
	}
	out := &model.LoginResult{
		SecondFactorRequired:   res.SecondFactorRequired,
		AvailableFactors:       factors,
		PasswordChangeRequired: res.PasswordChangeRequired,
	}
	if !res.SecondFactorRequired {
		// Only a completed login returns the account (M1 review L1).
		user, err := userModel(res.User)
		if err != nil {
			return nil, err
		}
		out.User = user
		st, err := r.enrollmentStatus(ctx, user)
		if err != nil {
			return nil, err
		}
		out.TwoFactorEnrollmentRequired = st.Restricted
	}
	return out, nil
}

func factorKind(f auth.Factor) (model.SecondFactorKind, bool) {
	switch f {
	case auth.FactorTOTP:
		return model.SecondFactorKindTotp, true
	case auth.FactorPasskey:
		return model.SecondFactorKindPasskey, true
	case auth.FactorRecoveryCode:
		return model.SecondFactorKindRecoveryCode, true
	default:
		return "", false
	}
}

func (r *Resolver) logout(ctx context.Context) (bool, error) {
	svc, err := r.authService()
	if err != nil {
		return false, err
	}
	p, err := caller(ctx)
	if err != nil {
		return false, err
	}
	cookies, ok := auth.CookieSetterFrom(ctx)
	if !ok {
		return false, errNoCookieSetter
	}
	if err := svc.Logout(ctx, p, auth.RequestMetaFrom(ctx)); err != nil {
		return false, authError(err)
	}
	cookies.ClearSessionCookie()
	return true, nil
}

func (r *Resolver) revokeSession(ctx context.Context, id uuid.UUID) (bool, error) {
	svc, err := r.authService()
	if err != nil {
		return false, err
	}
	p, err := caller(ctx)
	if err != nil {
		return false, err
	}
	wasCurrent, err := svc.RevokeSession(ctx, p, id, auth.RequestMetaFrom(ctx))
	if err != nil {
		return false, authError(err)
	}
	if wasCurrent {
		if cookies, ok := auth.CookieSetterFrom(ctx); ok {
			cookies.ClearSessionCookie()
		}
	}
	return true, nil
}

func (r *Resolver) changePassword(ctx context.Context, current, next string) (bool, error) {
	svc, err := r.authService()
	if err != nil {
		return false, err
	}
	p, err := caller(ctx)
	if err != nil {
		return false, err
	}
	cookies, ok := auth.CookieSetterFrom(ctx)
	if !ok {
		return false, errNoCookieSetter
	}
	issued, err := svc.ChangePassword(ctx, p, current, next, auth.RequestMetaFrom(ctx))
	if err != nil {
		return false, authError(err)
	}
	cookies.SetSessionCookie(issued.Token)
	return true, nil
}

func (r *Resolver) me(ctx context.Context) (*model.User, error) {
	svc, err := r.authService()
	if err != nil {
		return nil, err
	}
	p, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	u, err := svc.User(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	return userModel(u)
}

func (r *Resolver) mySessions(ctx context.Context) ([]model.Session, error) {
	svc, err := r.authService()
	if err != nil {
		return nil, err
	}
	p, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := svc.ListSessions(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make([]model.Session, 0, len(rows))
	for i := range rows {
		out = append(out, sessionModel(&rows[i], p.SessionID))
	}
	return out, nil
}

// modelRole maps an auth.Role to the GraphQL Role.
func modelRole(r auth.Role) (model.Role, error) {
	switch r {
	case auth.RoleAdmin:
		return model.RoleAdmin, nil
	case auth.RoleMaintainer:
		return model.RoleMaintainer, nil
	case auth.RoleUser:
		return model.RoleUser, nil
	default:
		return "", fmt.Errorf("resolvers: unknown role %q", r)
	}
}

func userModel(u sqlc.User) (*model.User, error) {
	role, err := modelRole(auth.Role(u.Role))
	if err != nil {
		return nil, err
	}
	return &model.User{
		ID: u.ID, Email: u.Email, DisplayName: u.DisplayName, Role: role,
		Disabled: u.Disabled, LockedUntil: u.LockedUntil,
		PasswordChangeRequired: u.PasswordTemporaryUntil != nil, TemporaryPasswordExpiresAt: u.PasswordTemporaryUntil,
		CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt, DeletedAt: u.DeletedAt,
		EmailVerified: u.EmailVerifiedAt != nil, EmailVerifiedAt: u.EmailVerifiedAt,
	}, nil
}

func sessionModel(s *sqlc.Session, current uuid.UUID) model.Session {
	m := model.Session{
		ID: s.ID, CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt,
		IdleExpiresAt: s.IdleExpiresAt, AbsoluteExpiresAt: s.AbsoluteExpiresAt,
		UserAgent: s.UserAgent, Aal: int(s.Aal), Current: s.ID == current,
	}
	if s.Ip != nil {
		ip := s.Ip.String()
		m.IP = &ip
	}
	return m
}
