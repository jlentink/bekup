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

	// Destinations (task 2.12, D-131): admin CRUD with step-up;
	// maintainers see metadata and may test.
	"Query.destinations":                     authz.OpDestinationList,
	"Query.destination":                      authz.OpDestinationView,
	"Mutation.createDestination":             authz.OpDestinationCreate,
	"Mutation.updateDestination":             authz.OpDestinationUpdate,
	"Mutation.deleteDestination":             authz.OpDestinationDelete,
	"Mutation.testDestination":               authz.OpDestinationTest,
	"Mutation.fetchDestinationHostKey":       authz.OpDestinationHostKeyFetch,
	"Mutation.confirmDestinationHostKey":     authz.OpDestinationHostKeyConfirm,
	"Mutation.generateDestinationSshKeypair": authz.OpDestinationSSHKeyGenerate,

	// Connections (task 2.11, D-135): admin CRUD with step-up for every
	// change; maintainers see metadata, may test and list databases.
	// setTlsMode additionally checks connection.tls_downgrade when it
	// leaves PINNED, updateConnection connection.secret_update when it
	// sets credentials.
	"Query.connections":                     authz.OpConnectionList,
	"Query.connection":                      authz.OpConnectionView,
	"Query.engineVersions":                  authz.OpConnectionList,
	"Mutation.createConnection":             authz.OpConnectionCreate,
	"Mutation.updateConnection":             authz.OpConnectionUpdate,
	"Mutation.deleteConnection":             authz.OpConnectionDelete,
	"Mutation.testConnection":               authz.OpConnectionTest,
	"Mutation.detectVersion":                authz.OpConnectionTest,
	"Mutation.listDatabases":                authz.OpConnectionListDatabases,
	"Mutation.prePullImage":                 authz.OpConnectionPrePull,
	"Mutation.fetchCertificate":             authz.OpConnectionCertFetch,
	"Mutation.confirmCertificatePin":        authz.OpConnectionPinConfirm,
	"Mutation.stageBackupPin":               authz.OpConnectionPinConfirm,
	"Mutation.fetchSshHostKey":              authz.OpConnectionHostKeyFetch,
	"Mutation.confirmSshHostKey":            authz.OpConnectionHostKeyConfirm,
	"Mutation.setTlsMode":                   authz.OpConnectionUpdate,
	"Mutation.generateConnectionSshKeypair": authz.OpConnectionSSHKeyGenerate,

	// Jobs (task 3.5, D-146): staff manage every job; owners (role user)
	// view and run their own (the resolvers authorize with the job id;
	// the list filters by JobScope). Owner changes need step-up.
	"Query.jobs":                 authz.OpJobList,
	"Query.job":                  authz.OpJobView,
	"Query.previewSchedule":      authz.OpJobSchedulePreview,
	"Mutation.createJob":         authz.OpJobCreate,
	"Mutation.updateJob":         authz.OpJobUpdate,
	"Mutation.deleteJob":         authz.OpJobDelete,
	"Mutation.enableJob":         authz.OpJobUpdate,
	"Mutation.disableJob":        authz.OpJobUpdate,
	"Mutation.assignJobOwners":   authz.OpJobOwnersAssign,
	"Mutation.removeJobOwner":    authz.OpJobOwnersAssign,
	"Mutation.runJobNow":         authz.OpRunNow,
	"Mutation.startNextRunEarly": authz.OpRunStartEarly,
	"Mutation.cancelRun":         authz.OpRunCancel,
	// Run history and live runs (task 3.7): owners see their own jobs'
	// runs (lists filtered by JobScope, single runs authorized with the
	// run's job from the DB, stream events re-authorized per event).
	"Query.runs":                  authz.OpRunList,
	"Query.run":                   authz.OpRunView,
	"Query.runLogs":               authz.OpRunLogsView,
	"Query.artifacts":             authz.OpArtifactList,
	"Subscription.runUpdated":     authz.OpRunView,
	"Subscription.runLogAppended": authz.OpRunLogsView,
	// Job calendar (task 3.14, D-156): jobs filtered by JobScope.
	"Query.calendar": authz.OpCalendarView,
	// Job encryption (task 3.2, D-142; re-checked in internal/jobkeys).
	"Mutation.setJobPassphrase":      authz.OpJobEncryptionKeysEdit,
	"Mutation.generateJobPassphrase": authz.OpJobEncryptionKeysEdit,
	"Mutation.setJobRecipient":       authz.OpJobEncryptionKeysEdit,
	"Mutation.generateAgeKeypair":    authz.OpJobEncryptionKeysEdit,
	"Mutation.enableJobEncryption":   authz.OpJobEncryptionKeysEdit,
	"Mutation.disableJobEncryption":  authz.OpJobEncryptionOptOut,
	"Mutation.exportJobPassphrases":  authz.OpJobPassphraseExport,
	// Downloads (task 3.9, D-150): admins and owners (role user) only,
	// re-checked with the artifact's job in internal/downloads.
	"Mutation.createDownloadTicket": authz.OpArtifactDownload,
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
