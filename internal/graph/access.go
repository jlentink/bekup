package graph

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/generated"
	"github.com/jlentink/bekup/internal/graph/model"
)

// PublicRootFields are the root fields ("Type.field") reachable without any
// access directive. Everything else needs @authenticated, @hasRole or
// @stepUp (deny by default, D-105). Keep this list minimal: only data that
// the login page needs and that is safe for anonymous callers.
var PublicRootFields = []string{
	"Query.serverInfo",
	// Rate limited per client IP (security.login_rate_limit_*) and by the
	// argon2id semaphore; failures are generic and timing-safe (task 1.5).
	"Mutation.login",
}

// Directive names that guard a field.
const (
	DirectiveAuthenticated = "authenticated"
	DirectiveHasRole       = "hasRole"
	DirectiveStepUp        = "stepUp"
)

var guardDirectives = []string{DirectiveAuthenticated, DirectiveHasRole, DirectiveStepUp}

// Access decides the access directives, the root-field operation gate and
// introspection. Each check returns nil to allow or an *apierror.Error
// (UNAUTHENTICATED, FORBIDDEN, STEP_UP_REQUIRED, REQUIRES_2FA_ENROLLMENT)
// to deny. Implementations must be deny by default: any doubt (no session,
// lookup error) is a denial. PolicyAccess is the real implementation
// (internal/authz, task 1.4); DenyAll denies everything.
type Access interface {
	// Authenticated allows a request with a valid session.
	Authenticated(ctx context.Context) error
	// HasRole allows an authenticated request whose user has one of roles.
	HasRole(ctx context.Context, roles []model.Role) error
	// StepUp allows an authenticated request with a fresh step-up
	// (security.stepup_max_age).
	StepUp(ctx context.Context) error
	// CheckOperation is the coarse policy check of the operation a root
	// field maps to (RootFieldOperations), run before the field resolves.
	CheckOperation(ctx context.Context, op authz.Operation) error
	// IntrospectionAllowed reports whether the caller may introspect the
	// schema (authenticated users only, D-050). Dev mode always allows it.
	IntrospectionAllowed(ctx context.Context) bool
}

// errNoAuth is the denial of DenyAll.
var errNoAuth = apierror.New(apierror.Unauthenticated, "authentication required")

// DenyAll denies every directive and operation with UNAUTHENTICATED and
// never allows introspection.
type DenyAll struct{}

// Authenticated always denies.
func (DenyAll) Authenticated(context.Context) error { return errNoAuth }

// HasRole always denies.
func (DenyAll) HasRole(context.Context, []model.Role) error { return errNoAuth }

// StepUp always denies.
func (DenyAll) StepUp(context.Context) error { return errNoAuth }

// CheckOperation always denies.
func (DenyAll) CheckOperation(context.Context, authz.Operation) error { return errNoAuth }

// IntrospectionAllowed always returns false.
func (DenyAll) IntrospectionAllowed(context.Context) bool { return false }

// directives binds the schema directives to a. Directive order on a field
// is the schema order; every guard runs before the resolver.
func directives(a Access) generated.DirectiveRoot {
	return generated.DirectiveRoot{
		Authenticated: func(ctx context.Context, _ any, next graphql.Resolver) (any, error) {
			if err := a.Authenticated(ctx); err != nil {
				return nil, err
			}
			return next(ctx)
		},
		HasRole: func(ctx context.Context, _ any, next graphql.Resolver, roles []model.Role) (any, error) {
			if len(roles) == 0 {
				return nil, errors.New("graph: @hasRole without roles")
			}
			if err := a.HasRole(ctx, roles); err != nil {
				return nil, err
			}
			return next(ctx)
		},
		StepUp: func(ctx context.Context, _ any, next graphql.Resolver) (any, error) {
			if err := a.StepUp(ctx); err != nil {
				return nil, err
			}
			return next(ctx)
		},
	}
}

// rootOperationTypes are the types whose fields are operations.
func rootOperationTypes(s *ast.Schema) []*ast.Definition {
	var out []*ast.Definition
	for _, d := range []*ast.Definition{s.Query, s.Mutation, s.Subscription} {
		if d != nil {
			out = append(out, d)
		}
	}
	return out
}

func isGuarded(f *ast.FieldDefinition) bool {
	if f == nil {
		return false
	}
	for _, d := range f.Directives {
		if slices.Contains(guardDirectives, d.Name) {
			return true
		}
	}
	return false
}

// CheckAccessPolicy verifies that every root field of s carries an access
// directive or is in public, that every guarded root field maps to a known
// operation in ops whose rule allows every role its @hasRole names, and that
// every public and ops entry exists (public fields have neither a
// directive nor an operation). New refuses to build a server that fails it.
func CheckAccessPolicy(s *ast.Schema, public []string, ops map[string]authz.Operation) error {
	pub := make(map[string]bool, len(public))
	for _, p := range public {
		pub[p] = true
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
			op, mapped := ops[name]
			switch {
			case isGuarded(f) && pub[name]:
				problems = append(problems, name+" is public but also carries an access directive")
			case !isGuarded(f) && !pub[name]:
				problems = append(problems, name+" has no access directive and is not in PublicRootFields")
			case pub[name] && mapped:
				problems = append(problems, name+" is public but also maps to an operation")
			case isGuarded(f) && !mapped:
				problems = append(problems, name+" maps to no authz operation (RootFieldOperations)")
			case mapped && !op.Known():
				problems = append(problems, name+" maps to unknown operation "+string(op))
			case mapped:
				problems = append(problems, roleMismatches(name, f, op)...)
			}
		}
	}
	for p := range pub {
		if !seen[p] {
			problems = append(problems, p+" is in PublicRootFields but not a root field of the schema")
		}
	}
	for n := range ops {
		if !seen[n] {
			problems = append(problems, n+" is in RootFieldOperations but not a root field of the schema")
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("graph: access policy violations: %s", strings.Join(problems, "; "))
	}
	return nil
}

// roleMismatches reports roles named in the field's @hasRole that the
// operation's rule never allows (the directive would promise more than
// the policy grants).
func roleMismatches(name string, f *ast.FieldDefinition, op authz.Operation) []string {
	rule, _ := authz.RuleFor(op)
	var out []string
	for _, d := range f.Directives {
		if d.Name != DirectiveHasRole {
			continue
		}
		arg := d.Arguments.ForName("roles")
		if arg == nil || arg.Value == nil {
			continue
		}
		for _, c := range arg.Value.Children {
			r, ok := roleFromModel(model.Role(c.Value.Raw))
			eligible := ok && (slices.Contains(rule.Roles, r) || (r == auth.RoleUser && rule.Owner != authz.OwnerNone))
			if !eligible {
				out = append(out, fmt.Sprintf("%s: @hasRole names %s but operation %s does not allow it", name, c.Value.Raw, op))
			}
		}
	}
	return out
}

// errNotAllowed denies an unmapped root field at runtime (defense in depth
// behind CheckAccessPolicy).
var errNotAllowed = apierror.New(apierror.Forbidden, "operation not allowed")

// rootFieldGuard enforces deny by default for root fields: a public field
// runs; a guarded field runs only if it maps to an operation and
// access.CheckOperation allows it (its directives then also decide);
// anything else is denied.
func rootFieldGuard(public []string, ops map[string]authz.Operation, access Access) graphql.RootFieldMiddleware {
	pub := make(map[string]bool, len(public))
	for _, p := range public {
		pub[p] = true
	}
	return func(ctx context.Context, next graphql.RootResolver) graphql.Marshaler {
		fc := graphql.GetRootFieldContext(ctx)
		if fc == nil || fc.Field.Field == nil {
			graphql.AddError(ctx, errNotAllowed)
			return graphql.Null
		}
		// RootFieldContext.Object holds the field name in gqlgen; the type
		// comes from the validated field.
		f := fc.Field.Field
		if strings.HasPrefix(f.Name, "__") {
			return next(ctx)
		}
		if f.ObjectDefinition == nil {
			graphql.AddError(ctx, errNotAllowed)
			return graphql.Null
		}
		name := f.ObjectDefinition.Name + "." + f.Name
		if pub[name] {
			return next(ctx)
		}
		op, mapped := ops[name]
		if !mapped || !isGuarded(f.Definition) {
			graphql.AddError(ctx, errNotAllowed)
			return graphql.Null
		}
		if err := access.CheckOperation(ctx, op); err != nil {
			graphql.AddError(ctx, err)
			return graphql.Null
		}
		return next(ctx)
	}
}
