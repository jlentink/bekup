package graph

import (
	"context"
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/authz/authztest"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/generated"
	"github.com/jlentink/bekup/internal/graph/model"
)

func TestPolicyAccessDirectives(t *testing.T) {
	a := NewPolicyAccess(nil)
	d := directives(a)
	ran := false
	var nextErr error // the resolver's own result
	next := func(context.Context) (any, error) { ran = true; return "ok", nextErr }
	admin := authztest.Context(t.Context(), authztest.Principal(auth.RoleAdmin))
	maint := authztest.Context(t.Context(), authztest.Principal(auth.RoleMaintainer))
	staleUser := authztest.Context(t.Context(), authztest.Stale(authztest.Principal(auth.RoleUser)))
	anon := t.Context()
	adminOnly := []model.Role{model.RoleAdmin}
	cases := []struct {
		name string
		call func() (any, error)
		want apierror.Code // "" = allowed
	}{
		{"authenticated anon", func() (any, error) { return d.Authenticated(anon, nil, next) }, apierror.Unauthenticated},
		{"authenticated user", func() (any, error) { return d.Authenticated(staleUser, nil, next) }, ""},
		{"hasRole anon", func() (any, error) { return d.HasRole(anon, nil, next, adminOnly) }, apierror.Unauthenticated},
		{"hasRole maintainer→admin", func() (any, error) { return d.HasRole(maint, nil, next, adminOnly) }, apierror.Forbidden},
		{"hasRole admin", func() (any, error) { return d.HasRole(admin, nil, next, adminOnly) }, ""},
		{"hasRole unknown role", func() (any, error) { return d.HasRole(admin, nil, next, []model.Role{"ROOT"}) }, apierror.Forbidden},
		{"stepUp anon", func() (any, error) { return d.StepUp(anon, nil, next) }, apierror.Unauthenticated},
		{"stepUp stale", func() (any, error) { return d.StepUp(staleUser, nil, next) }, apierror.StepUpRequired},
		{"stepUp fresh", func() (any, error) { return d.StepUp(admin, nil, next) }, ""},
	}
	for _, c := range cases {
		ran = false
		res, err := c.call()
		if c.want == "" {
			if err != nil || res != "ok" || !ran {
				t.Errorf("%s: res %v, err %v", c.name, res, err)
			}
			continue
		}
		if !apierror.IsCode(err, c.want) || ran || res != nil {
			t.Errorf("%s: res %v, err %v, ran %v; want %s", c.name, res, err, ran, c.want)
		}
	}
	if a.IntrospectionAllowed(anon) || !a.IntrospectionAllowed(staleUser) {
		t.Error("introspection must require exactly an authenticated principal")
	}
	if err := a.CheckOperation(maint, authz.OpAuditLogView); !apierror.IsCode(err, apierror.Forbidden) {
		t.Errorf("CheckOperation maintainer audit: %v", err)
	}
	if err := a.CheckOperation(admin, authz.OpAuditLogView); err != nil {
		t.Errorf("CheckOperation admin audit: %v", err)
	}
}

// The @stepUp window follows security.stepup_max_age.
func TestPolicyAccessStepUpWindow(t *testing.T) {
	p := authztest.Principal(auth.RoleAdmin)
	p.StepUpAt = time.Now().Add(-3 * time.Minute)
	ctx := authztest.Context(t.Context(), p)
	if err := NewPolicyAccess(authz.New(authz.Options{})).StepUp(ctx); err != nil {
		t.Errorf("3m within default 5m: %v", err)
	}
	if err := NewPolicyAccess(authz.New(authz.Options{StepUpWindow: time.Minute})).StepUp(ctx); !apierror.IsCode(err, apierror.StepUpRequired) {
		t.Errorf("3m within 1m: %v", err)
	}
}

// Every root field of the real schema is public or maps to a known
// operation, so a new field cannot slip through unmapped.
func TestEveryRootFieldMapped(t *testing.T) {
	s := generated.NewExecutableSchema(generated.Config{}).Schema()
	pub := map[string]bool{}
	for _, p := range PublicRootFields {
		pub[p] = true
	}
	n := 0
	for _, typ := range rootOperationTypes(s) {
		for _, f := range typ.Fields {
			if strings.HasPrefix(f.Name, "__") {
				continue
			}
			n++
			name := typ.Name + "." + f.Name
			op, ok := RootFieldOperations[name]
			switch {
			case pub[name] && ok:
				t.Errorf("%s is public and mapped", name)
			case pub[name]:
			case !ok:
				t.Errorf("%s maps to no operation: add it to RootFieldOperations and the authz matrix", name)
			case !op.Known():
				t.Errorf("%s maps to unknown operation %q", name, op)
			}
		}
	}
	if n < 2 {
		t.Fatalf("only %d root fields found", n)
	}
}

type fakeLister struct {
	gotFilter audit.Filter
	gotCursor int64
	gotLimit  int
	page      audit.Page
	calls     int
}

func (f *fakeLister) List(_ context.Context, flt audit.Filter, cursor int64, limit int) (audit.Page, error) {
	f.calls++
	f.gotFilter, f.gotCursor, f.gotLimit = flt, cursor, limit
	return f.page, nil
}

func TestAuditLogAccess(t *testing.T) {
	uid := uuid.New()
	ip := netip.MustParseAddr("192.0.2.7")
	lister := &fakeLister{page: audit.Page{Entries: []audit.ListEntry{{
		ID: 42, TS: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC), ActorKind: audit.ActorUser, ActorUserID: &uid,
		Action: "user.created", IP: &ip, Details: json.RawMessage(`{"a":1}`), Hash: []byte{0xab, 0xcd},
	}}, NextCursor: 42}}
	newAuditEnv := func(p *auth.Principal) *testEnv {
		pol := authz.New(authz.Options{})
		env := newEnv(t, func(o *Options) {
			o.Access = NewPolicyAccess(pol)
			o.Policy = pol
			o.AuditLog = lister
		})
		env.principal = p
		return env
	}
	const q = `{ auditLog(first: 1, filter: {actionPrefix: "user."}) {
  edges { cursor node { id ts actorKind actorUserId action ip details hash } }
  pageInfo { hasNextPage endCursor } } }`

	_, resp := newAuditEnv(nil).query(t, q)
	if errorCode(resp) != string(apierror.Unauthenticated) || resp.Data != nil {
		t.Errorf("anonymous: %+v", resp)
	}
	for _, role := range []auth.Role{auth.RoleMaintainer, auth.RoleUser} {
		p := authztest.Principal(role)
		if _, resp := newAuditEnv(&p).query(t, q); errorCode(resp) != string(apierror.Forbidden) || resp.Data != nil {
			t.Errorf("%s: %+v", role, resp)
		}
	}
	if lister.calls != 0 {
		t.Fatalf("lister called %d times for denied callers", lister.calls)
	}
	admin := authztest.Principal(auth.RoleAdmin)
	_, resp = newAuditEnv(&admin).query(t, q)
	if len(resp.Errors) != 0 {
		t.Fatalf("admin: %+v", resp.Errors)
	}
	if lister.gotLimit != 1 || lister.gotFilter.ActionPrefix != "user." || lister.gotCursor != 0 {
		t.Errorf("lister args: %+v %d %d", lister.gotFilter, lister.gotCursor, lister.gotLimit)
	}
	b, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"id":"42"`, `"actorKind":"USER"`, `"ip":"192.0.2.7"`, `"hash":"abcd"`,
		`"details":"{\"a\":1}"`, `"hasNextPage":true`, uid.String()} {
		if !strings.Contains(string(b), want) {
			t.Errorf("response lacks %s: %s", want, b)
		}
	}
	// Introspection works for an authenticated principal only.
	if _, resp := newAuditEnv(&admin).query(t, "{ __schema { queryType { name } } }"); len(resp.Errors) != 0 {
		t.Errorf("authenticated introspection: %+v", resp.Errors)
	}
	if _, resp := newAuditEnv(nil).query(t, "{ __schema { queryType { name } } }"); errorCode(resp) != string(apierror.IntrospectionDisabled) {
		t.Errorf("anonymous introspection: %+v", resp)
	}
}
