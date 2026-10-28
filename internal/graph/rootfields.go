package graph

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/httpserver"
)

// SingleRootFields are the root fields ("Type.field") that verify, set or
// reset credentials (passwords, TOTP and recovery codes, passkeys) or run
// argon2id: an operation that selects one of them must select exactly one
// root field (no aliases, no other fields), so one request can never
// multiply credential checks or hashing (M1 review H1, D-117). New refuses
// to start when a public mutation or a root field of a
// CredentialOperations operation is missing here.
var SingleRootFields = []string{
	// Public; verifies a password.
	"Mutation.login",
	// Password verification and hashing.
	"Mutation.changePassword",
	// Second step of a login and step-up: password, TOTP, recovery code
	// or passkey assertion; the begin fields start WebAuthn ceremonies.
	"Mutation.beginPasskeyLogin",
	"Mutation.completeSecondFactor",
	"Mutation.beginStepUp",
	"Mutation.completeStepUp",
	// Own second factors (TOTP code check, WebAuthn attestation, new
	// recovery codes).
	"Mutation.beginTotpEnrollment",
	"Mutation.confirmTotpEnrollment",
	"Mutation.disableTotp",
	"Mutation.regenerateRecoveryCodes",
	"Mutation.beginPasskeyRegistration",
	"Mutation.finishPasskeyRegistration",
	"Mutation.renamePasskey",
	"Mutation.deletePasskey",
	// User administration that hashes a (temporary) password or removes
	// factors.
	"Mutation.createUser",
	"Mutation.resetUserPassword",
	"Mutation.resetUserSecondFactors",
	// Outbound connections with stored credentials or to an arbitrary
	// SSH server (task 2.12): one per request, so a single request cannot
	// fan out many probes past the per-user limiter.
	"Mutation.testDestination",
	"Mutation.fetchDestinationHostKey",
	// Runner calls with a connection's stored credentials or towards its
	// server or bastion (task 2.11, D-135): one per request.
	"Mutation.testConnection",
	"Mutation.detectVersion",
	"Mutation.listDatabases",
	"Mutation.prePullImage",
	"Mutation.fetchCertificate",
	"Mutation.confirmCertificatePin",
	"Mutation.stageBackupPin",
	"Mutation.fetchSshHostKey",
	// Temporary restore connections carry credentials (task 4.4): one
	// creation per request.
	"Mutation.createTemporaryConnection",
	// Download tickets (task 3.9, D-150): may carry an age identity.
	"Mutation.createDownloadTicket",
	// Restores (task 4.5, D-167): restoreArtifact may carry an age
	// identity; both requests verify a signed manifest on a destination;
	// restoreTargetDatabases and restorePreview make a runner call
	// (D-135; the preview reads the target role's privileges, D-068).
	"Mutation.restoreArtifact",
	"Mutation.restoreGlobals",
	"Query.restoreTargetDatabases",
	"Query.restorePreview",
}

// CredentialOperations are the authz operations whose root fields verify,
// set or reset credentials; every root field mapped to one of them must be
// in SingleRootFields.
var CredentialOperations = []authz.Operation{
	authz.OpAccountPasswordChange,
	authz.OpAccountSecondFactorEdit,
	authz.OpAccountSecondFactorLogin,
	authz.OpAccountStepUp,
	authz.OpUserCreate,
	authz.OpUserPasswordReset,
	authz.OpUserSecondFactorEdit,
	// Connection operations that log in to a database or bastion with
	// stored credentials or open an outbound runner connection (D-135).
	authz.OpConnectionTest,
	authz.OpConnectionListDatabases,
	authz.OpConnectionPrePull,
	authz.OpConnectionCertFetch,
	authz.OpConnectionPinConfirm,
	authz.OpConnectionHostKeyFetch,
	// createDownloadTicket may carry an age identity (D-150).
	authz.OpArtifactDownload,
	// restoreArtifact may carry an age identity (D-167).
	authz.OpRestoreArtifact,
	authz.OpRestoreGlobals,
}

// CheckSingleRootFields verifies that every entry of single is a root
// field of s, that every public mutation and every root field mapped to a
// credential operation is in single. New refuses to build a server that
// fails it.
func CheckSingleRootFields(s *ast.Schema, single, public []string, ops map[string]authz.Operation, credential []authz.Operation) error {
	in := make(map[string]bool, len(single))
	for _, f := range single {
		in[f] = true
	}
	var problems []string
	seen := map[string]bool{}
	for _, t := range rootOperationTypes(s) {
		for _, f := range t.Fields {
			if strings.HasPrefix(f.Name, "__") {
				continue
			}
			name := t.Name + "." + f.Name
			seen[name] = true
			if in[name] {
				continue
			}
			if s.Mutation != nil && t.Name == s.Mutation.Name && slices.Contains(public, name) {
				problems = append(problems, name+" is a public mutation but not in SingleRootFields")
			}
			if op, ok := ops[name]; ok && slices.Contains(credential, op) {
				problems = append(problems, fmt.Sprintf("%s maps to credential operation %s but is not in SingleRootFields", name, op))
			}
		}
	}
	for f := range in {
		if !seen[f] {
			problems = append(problems, f+" is in SingleRootFields but not a root field of the schema")
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("graph: single-root field violations: %s", strings.Join(problems, "; "))
	}
	return nil
}

// RootFields returns the root fields an operation selects, in document
// order, counting every occurrence (aliases, repeated fields and fields
// reached through fragments; skip/include directives are ignored). The
// __typename meta field is not counted.
func RootFields(doc *ast.QueryDocument, op *ast.OperationDefinition) []string {
	var out []string
	var walk func(set ast.SelectionSet, active map[string]bool)
	walk = func(set ast.SelectionSet, active map[string]bool) {
		for _, sel := range set {
			switch s := sel.(type) {
			case *ast.Field:
				if s.Name != "__typename" {
					out = append(out, s.Name)
				}
			case *ast.InlineFragment:
				walk(s.SelectionSet, active)
			case *ast.FragmentSpread:
				if active[s.Name] {
					continue // a cycle (already rejected by validation)
				}
				f := doc.Fragments.ForName(s.Name)
				if f == nil {
					continue
				}
				active[s.Name] = true
				walk(f.SelectionSet, active)
				delete(active, s.Name)
			}
		}
	}
	walk(op.SelectionSet, map[string]bool{})
	return out
}

// rootTypeName is the schema type of op's root fields.
func rootTypeName(s *ast.Schema, op *ast.OperationDefinition) string {
	var d *ast.Definition
	switch op.Operation {
	case ast.Query:
		d = s.Query
	case ast.Mutation:
		d = s.Mutation
	case ast.Subscription:
		d = s.Subscription
	}
	if d == nil {
		return ""
	}
	return d.Name
}

// rootFieldGate enforces SingleRootFields (VALIDATION when an operation
// with a credential field selects more than one root field) and charges
// every further root field of an authenticated caller's operation to the
// per-user rate limit (RATE_LIMITED when it is exhausted; D-117). It runs
// after validation and before any resolver.
type rootFieldGate struct {
	schema *ast.Schema
	single map[string]bool
}

var _ interface {
	graphql.HandlerExtension
	graphql.OperationContextMutator
} = rootFieldGate{}

func (rootFieldGate) ExtensionName() string { return "RootFieldGate" }

func (rootFieldGate) Validate(graphql.ExecutableSchema) error { return nil }

func (g rootFieldGate) MutateOperationContext(ctx context.Context, opCtx *graphql.OperationContext) *gqlerror.Error {
	op := opCtx.Operation
	if op == nil {
		return nil
	}
	fields := RootFields(opCtx.Doc, op)
	typ := rootTypeName(g.schema, op)
	if len(fields) > 1 {
		for _, f := range fields {
			if g.single[typ+"."+f] {
				return protocolError(apierror.Validation, f+" must be the only field of its operation")
			}
		}
	}
	if ok, wait := httpserver.ChargeUser(ctx, len(fields)-1); !ok {
		return protocolError(apierror.RateLimited, fmt.Sprintf("too many requests; try again in %d seconds",
			httpserver.RetryAfterSeconds(wait)))
	}
	return nil
}
