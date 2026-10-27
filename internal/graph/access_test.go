package graph

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/generated"
	"github.com/jlentink/bekup/internal/graph/model"
)

type allowAll struct{}

func (allowAll) Authenticated(context.Context) error         { return nil }
func (allowAll) HasRole(context.Context, []model.Role) error { return nil }
func (allowAll) StepUp(context.Context) error                { return nil }
func (allowAll) IntrospectionAllowed(context.Context) bool   { return true }
func (allowAll) CheckOperation(context.Context, authz.Operation) error {
	return nil
}

func TestDirectiveStubsDeny(t *testing.T) {
	d := directives(DenyAll{})
	called := false
	var nextErr error // the resolver's own result
	next := func(context.Context) (any, error) { called = true; return "ok", nextErr }
	ctx := context.Background()
	checks := map[string]func() (any, error){
		"authenticated": func() (any, error) { return d.Authenticated(ctx, nil, next) },
		"hasRole":       func() (any, error) { return d.HasRole(ctx, nil, next, []model.Role{model.RoleAdmin}) },
		"stepUp":        func() (any, error) { return d.StepUp(ctx, nil, next) },
	}
	for name, check := range checks {
		res, err := check()
		if res != nil || !apierror.IsCode(err, apierror.Unauthenticated) || called {
			t.Errorf("%s: res %v, err %v, next called %v", name, res, err, called)
		}
	}
	if err := (DenyAll{}).CheckOperation(ctx, authz.OpAccountView); !apierror.IsCode(err, apierror.Unauthenticated) {
		t.Errorf("DenyAll.CheckOperation: %v", err)
	}
	if (DenyAll{}).IntrospectionAllowed(ctx) {
		t.Error("DenyAll allows introspection")
	}
	// With an allowing Access the resolver runs.
	d = directives(allowAll{})
	for name, check := range map[string]func() (any, error){
		"authenticated": func() (any, error) { return d.Authenticated(ctx, nil, next) },
		"hasRole":       func() (any, error) { return d.HasRole(ctx, nil, next, []model.Role{model.RoleUser}) },
		"stepUp":        func() (any, error) { return d.StepUp(ctx, nil, next) },
	} {
		called = false
		if res, err := check(); err != nil || res != "ok" || !called {
			t.Errorf("%s allowed: res %v, err %v", name, res, err)
		}
	}
	// @hasRole with an empty list never allows.
	called = false
	if _, err := d.HasRole(ctx, nil, next, nil); err == nil || called {
		t.Error("@hasRole([]) allowed")
	}
}

func loadSchema(t *testing.T, sdl string) *ast.Schema {
	t.Helper()
	s, err := gqlparser.LoadSchema(&ast.Source{Name: "test.graphqls", Input: `
directive @authenticated on FIELD_DEFINITION
directive @hasRole(roles: [String!]!) on FIELD_DEFINITION
directive @stepUp on FIELD_DEFINITION
` + sdl})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCheckAccessPolicy(t *testing.T) {
	es := generated.NewExecutableSchema(generated.Config{})
	if err := CheckAccessPolicy(es.Schema(), PublicRootFields, RootFieldOperations); err != nil {
		t.Fatalf("real schema: %v", err)
	}
	s := loadSchema(t, `
type Query {
  pub: Int, auth: Int @authenticated, admin: Int @hasRole(roles: ["ADMIN"])
  unmapped: Int @authenticated, unknown: Int @authenticated, pubop: Int
  lax: Int @hasRole(roles: ["ADMIN", "MAINTAINER", "USER"])
  owners: Int @hasRole(roles: ["ADMIN", "MAINTAINER", "USER"])
}
type Mutation { open: Int, step: Int @stepUp }
type Subscription { feed: Int }
`)
	ops := map[string]authz.Operation{
		"Query.auth":    authz.OpAccountView,
		"Query.admin":   authz.OpAuditLogView,
		"Query.unknown": "nope.nope",
		"Query.pubop":   authz.OpAccountView,
		"Query.lax":     authz.OpJobCreate,
		"Query.owners":  authz.OpJobView,
		"Mutation.step": authz.OpUserCreate,
		"Query.stale":   authz.OpAccountView,
	}
	err := CheckAccessPolicy(s, []string{"Query.pub", "Query.auth", "Query.gone", "Query.pubop"}, ops)
	if err == nil {
		t.Fatal("violations accepted")
	}
	for _, want := range []string{
		"Mutation.open has no access directive",
		"Subscription.feed has no access directive",
		"Query.auth is public but also carries",
		"Query.gone is in PublicRootFields but not",
		"Query.unmapped maps to no authz operation",
		"Query.unknown maps to unknown operation nope.nope",
		"Query.pubop is public but also maps to an operation",
		"Query.lax: @hasRole names USER but operation job.create does not allow it",
		"Query.stale is in RootFieldOperations but not",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "Query.admin") || strings.Contains(err.Error(), "Mutation.step") ||
		strings.Contains(err.Error(), "Query.owners") || strings.Contains(err.Error(), "MAINTAINER but") {
		t.Errorf("guarded field reported: %v", err)
	}
}

// opAccess allows CheckOperation only for the listed operations.
type opAccess struct {
	allowAll
	allowed map[authz.Operation]bool
}

func (a opAccess) CheckOperation(_ context.Context, op authz.Operation) error {
	if a.allowed[op] {
		return nil
	}
	return apierror.New(apierror.Unauthenticated, "authentication required")
}

func TestRootFieldGuard(t *testing.T) {
	s := loadSchema(t, `type Query { pub: Int, auth: Int @authenticated, open: Int, denied: Int @authenticated, nomap: Int @authenticated }`)
	ops := map[string]authz.Operation{
		"Query.auth": authz.OpAccountView, "Query.denied": authz.OpAuditLogView, "Query.open": authz.OpAccountView,
	}
	access := opAccess{allowed: map[authz.Operation]bool{authz.OpAccountView: true}}
	guard := rootFieldGuard([]string{"Query.pub"}, ops, access)
	logger := discardLogger()
	cases := map[string]struct {
		run  bool
		code apierror.Code
	}{
		"pub":        {run: true},
		"auth":       {run: true},
		"__typename": {run: true},
		"open":       {code: apierror.Forbidden}, // mapped but carries no directive
		"nomap":      {code: apierror.Forbidden}, // guarded but not mapped
		"denied":     {code: apierror.Unauthenticated},
	}
	for name, want := range cases {
		def := s.Query.Fields.ForName(name)
		if def == nil {
			def = &ast.FieldDefinition{Name: name}
		}
		ctx := graphql.WithResponseContext(context.Background(), errorPresenter(logger), recoverFunc(logger))
		ctx = graphql.WithRootFieldContext(ctx, &graphql.RootFieldContext{
			Object: "Query",
			Field:  graphql.CollectedField{Field: &ast.Field{Name: name, Definition: def, ObjectDefinition: s.Query}},
		})
		ran := false
		guard(ctx, func(context.Context) graphql.Marshaler { ran = true; return graphql.Null })
		if ran != want.run {
			t.Errorf("%s: ran %v, want %v", name, ran, want.run)
		}
		errs := graphql.GetErrors(ctx)
		if !want.run && (len(errs) != 1 || errs[0].Extensions[ExtCode] != string(want.code)) {
			t.Errorf("%s: errors %v, want %s", name, errs, want.code)
		}
	}
}

func TestQueryDepth(t *testing.T) {
	cases := map[string]int{
		"{ a }":                           1,
		"{ a { b { c } } }":               3,
		"{ a { __typename } }":            1,
		"{ __schema { types { name } } }": 0,
		"{ a { ...F } } fragment F on T { b { c } }":                                 3,
		"{ ...F ...F a } fragment F on T { b { ...G } } fragment G on T { c { d } }": 3,
		"{ a { ... on T { b { c { d } } } } }":                                       4,
	}
	for q, want := range cases {
		doc, err := parser.ParseQuery(&ast.Source{Input: q})
		if err != nil {
			t.Fatal(err)
		}
		if got := QueryDepth(doc, doc.Operations[0]); got != want {
			t.Errorf("%s: depth %d, want %d", q, got, want)
		}
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }
