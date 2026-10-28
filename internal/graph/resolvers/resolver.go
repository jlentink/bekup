// Package resolvers implements the GraphQL resolvers. gqlgen regenerates
// the *.resolvers.go files from internal/graph/schema (one per schema file)
// and keeps the method bodies; dependencies live in Resolver.
//
// Resolvers return *apierror.Error for anything the user should see; every
// other error is masked by the error presenter (D-105).
//
// Authorization: the root-field gate (graph.Access.CheckOperation) and the
// schema directives run before a resolver. Resolvers of operations with an
// owner grant (authz.OwnerJob) must additionally call
// Authz.Authorize(ctx, op, authz.Job{...}, ...) with the job loaded from the
// database; list operations (authz.OwnerList) must filter by
// Authz.JobScope(ctx).
package resolvers

import (
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/auth/passkey"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/calendar"
	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/destinations"
	"github.com/jlentink/bekup/internal/downloads"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/restore"
	"github.com/jlentink/bekup/internal/runstate"
)

// Resolver is the root resolver and holds the dependencies of all
// resolvers. Later tasks add services (auth, jobs, ...).
type Resolver struct {
	// ServerInfo is the static, public server information.
	ServerInfo model.ServerInfo
	// Authz is the central policy for object-level checks.
	Authz *authz.Policy
	// AuditLog backs Query.auditLog (nil: the field fails with an
	// internal error).
	AuditLog AuditLister
	// Auth backs login, logout, me, sessions and password changes (nil:
	// those fields fail with an internal error).
	Auth *auth.Service
	// Users backs user administration (nil: those fields fail with an
	// internal error).
	Users *auth.UserAdmin
	// TOTP backs TOTP enrollment, recovery codes and myTwoFactorStatus
	// (task 1.7; nil: those fields fail with an internal error).
	TOTP *auth.TOTP
	// Passkeys backs webauthnStatus, myPasskeys and the passkey mutations
	// (task 1.8; nil: those fields fail with an internal error).
	Passkeys *passkey.Service
	// SecondFactor backs the two-step login and step-up (task 1.9; nil:
	// those fields fail with an internal error).
	SecondFactor *auth.SecondFactorFlow
	// TwoFactorPolicy backs the 2FA policy fields and the 2FA standing of
	// users (task 1.9; nil: the policy fields fail, the standing reads as
	// not required).
	TwoFactorPolicy *auth.TwoFactorPolicies
	// Destinations backs the destination fields (task 2.12; nil: those
	// fields fail with an internal error).
	Destinations *destinations.Service
	// Connections backs the connection fields (task 2.11; nil: those
	// fields fail with an internal error).
	Connections *connections.Service
	// Jobs and JobKeys back the job fields and the job encryption fields
	// (tasks 3.5 and 3.2; nil: those fields fail with an internal error).
	Jobs    *jobs.Service
	JobKeys *jobkeys.Service
	// Runs backs the run history fields and Bus the run event
	// subscriptions (task 3.7; nil: those fields fail with an internal
	// error).
	Runs *runstate.Reader
	Bus  *pubsub.Bus
	// Downloads backs createDownloadTicket (task 3.9; nil: the field
	// fails with an internal error).
	Downloads *downloads.Service
	// Calendar backs Query.calendar (task 3.14; nil: the field fails
	// with an internal error).
	Calendar *calendar.Service
	// Restores backs the restore fields and, with Bus, restoreUpdated
	// (task 4.5; nil: those fields fail with an internal error).
	Restores *restore.API
	// TestRestores backs the test-restore fields (task 4.6; nil: those
	// fields fail with an internal error).
	TestRestores *restore.TestRestoreAPI
}
