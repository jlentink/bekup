package graph

import (
	"context"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/graph/model"
)

// RootFieldOperations maps every guarded root field ("Type.field") to its
// authz operation (D-106). New refuses to start when a guarded root field
// is missing here, and the matrix in internal/authz pins what each
// operation allows. Add the entry together with the schema field.
var RootFieldOperations = map[string]authz.Operation{
	"Query.auditLog": authz.OpAuditLogView,

	// Own account and sessions (task 1.5). Mutation.login is public
	// (PublicRootFields) and rate limited by internal/auth.
	"Query.me":                authz.OpAccountView,
	"Query.mySessions":        authz.OpAccountSessionsView,
	"Mutation.logout":         authz.OpAccountLogout,
	"Mutation.revokeSession":  authz.OpAccountSessionsRevoke,
	"Mutation.changePassword": authz.OpAccountPasswordChange,
	// Passkeys (task 1.8): own account data; every change needs step-up.
	"Query.webauthnStatus":               authz.OpAccountView,
	"Query.myPasskeys":                   authz.OpAccountView,
	"Mutation.beginPasskeyRegistration":  authz.OpAccountSecondFactorEdit,
	"Mutation.finishPasskeyRegistration": authz.OpAccountSecondFactorEdit,
	"Mutation.renamePasskey":             authz.OpAccountSecondFactorEdit,
	"Mutation.deletePasskey":             authz.OpAccountSecondFactorEdit,
	// TOTP and recovery codes (task 1.7): own account data; every change
	// needs step-up.
	"Query.myTwoFactorStatus":          authz.OpAccountView,
	"Mutation.beginTotpEnrollment":     authz.OpAccountSecondFactorEdit,
	"Mutation.confirmTotpEnrollment":   authz.OpAccountSecondFactorEdit,
	"Mutation.disableTotp":             authz.OpAccountSecondFactorEdit,
	"Mutation.regenerateRecoveryCodes": authz.OpAccountSecondFactorEdit,
	// Two-step login and step-up (task 1.9): completing the second factor
	// is the only thing a pending login may do (besides logout).
	"Mutation.beginPasskeyLogin":    authz.OpAccountSecondFactorLogin,
	"Mutation.completeSecondFactor": authz.OpAccountSecondFactorLogin,
	"Mutation.beginStepUp":          authz.OpAccountStepUp,
	"Mutation.completeStepUp":       authz.OpAccountStepUp,
	// 2FA policy (task 1.9): admin; changes need step-up.
	"Query.twoFactorPolicy":          authz.OpMFAPolicyView,
	"Mutation.updateTwoFactorPolicy": authz.OpMFAPolicyUpdate,
	// Subscriptions (task 1.10): every event is re-authorized at delivery.
	"Subscription.sessionEvents": authz.OpAccountSessionsView,

	// User administration (task 1.6).
	"Query.users":                     authz.OpUserList,
	"Query.user":                      authz.OpUserView,
	"Query.userSessions":              authz.OpUserSessionsView,
	"Mutation.createUser":             authz.OpUserCreate,
	"Mutation.updateUser":             authz.OpUserUpdate,
	"Mutation.resetUserPassword":      authz.OpUserPasswordReset,
	"Mutation.resetUserSecondFactors": authz.OpUserSecondFactorEdit,
	"Mutation.disableUser":            authz.OpUserDisable,
	"Mutation.enableUser":             authz.OpUserEnable,
	"Mutation.unlockUser":             authz.OpUserUpdate,
	"Mutation.deleteUser":             authz.OpUserDelete,
	"Mutation.revokeUserSessions":     authz.OpUserSessionsRevoke,
}

// PolicyAccess is the Access backed by internal/authz and the principal
// that the session middleware (task 1.5) puts into the request context.
// Without a principal every check denies with UNAUTHENTICATED.
type PolicyAccess struct {
	Policy *authz.Policy
}

// NewPolicyAccess returns the Access for p (nil: a default, deny-by-default
// policy).
func NewPolicyAccess(p *authz.Policy) PolicyAccess {
	if p == nil {
		p = authz.New(authz.Options{})
	}
	return PolicyAccess{Policy: p}
}

// Authenticated requires a principal (@authenticated).
func (a PolicyAccess) Authenticated(ctx context.Context) error { return a.Policy.Authenticated(ctx) }

// HasRole requires a principal with one of roles (@hasRole).
func (a PolicyAccess) HasRole(ctx context.Context, roles []model.Role) error {
	rs := make([]auth.Role, 0, len(roles))
	for _, r := range roles {
		if ar, ok := roleFromModel(r); ok {
			rs = append(rs, ar)
		}
	}
	return a.Policy.HasRole(ctx, rs...)
}

// StepUp requires a step-up within the policy window (@stepUp).
func (a PolicyAccess) StepUp(ctx context.Context) error { return a.Policy.StepUp(ctx) }

// CheckOperation runs the policy precheck of op.
func (a PolicyAccess) CheckOperation(ctx context.Context, op authz.Operation) error {
	return a.Policy.Precheck(ctx, op)
}

// IntrospectionAllowed allows an authenticated principal without
// restrictions (D-050; not a pending login, temporary password or
// required enrollment).
func (a PolicyAccess) IntrospectionAllowed(ctx context.Context) bool {
	return a.Policy.IntrospectionAllowed(ctx)
}

// roleFromModel maps a GraphQL Role to an auth.Role.
func roleFromModel(r model.Role) (auth.Role, bool) {
	switch r {
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
