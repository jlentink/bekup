package graph

import (
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/graph/generated"
)

func TestCheckSingleRootFields(t *testing.T) {
	es := generated.NewExecutableSchema(generated.Config{})
	if err := CheckSingleRootFields(es.Schema(), SingleRootFields, PublicRootFields, RootFieldOperations, CredentialOperations); err != nil {
		t.Fatalf("real schema: %v", err)
	}
	s := loadSchema(t, `
type Query { me: Int @authenticated, pub: Int }
type Mutation { open: Int, pw: Int @stepUp, other: Int @authenticated, listed: Int @authenticated }
`)
	ops := map[string]authz.Operation{
		"Query.me": authz.OpAccountView, "Mutation.pw": authz.OpAccountPasswordChange,
		"Mutation.other": authz.OpAccountLogout, "Mutation.listed": authz.OpUserCreate,
	}
	err := CheckSingleRootFields(s, []string{"Mutation.listed", "Mutation.gone"}, []string{"Query.pub", "Mutation.open"}, ops, CredentialOperations)
	if err == nil {
		t.Fatal("violations accepted")
	}
	for _, want := range []string{
		"Mutation.open is a public mutation but not in SingleRootFields",
		"Mutation.pw maps to credential operation account.password_change",
		"Mutation.gone is in SingleRootFields but not a root field",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
	for _, unwanted := range []string{"Query.pub", "Mutation.other", "Mutation.listed ", "Query.me"} {
		if strings.Contains(err.Error(), unwanted) {
			t.Errorf("%q reported: %v", unwanted, err)
		}
	}
}

func TestRootFields(t *testing.T) {
	cases := []struct {
		q    string
		want []string
	}{
		{`{ serverInfo { version } }`, []string{"serverInfo"}},
		{`{ __typename serverInfo { version } }`, []string{"serverInfo"}},
		{`{ a: serverInfo { version } b: serverInfo { version } }`, []string{"serverInfo", "serverInfo"}},
		{`{ ...F ... on Query { serverInfo { devMode } } } fragment F on Query { serverInfo { version } }`, []string{"serverInfo", "serverInfo"}},
		{`mutation { a: login(email: "a", password: "b") { secondFactorRequired } b: login(email: "a", password: "c") { secondFactorRequired } }`, []string{"login", "login"}},
	}
	for _, tc := range cases {
		doc, err := parser.ParseQuery(&ast.Source{Input: tc.q})
		if err != nil {
			t.Fatalf("%s: %v", tc.q, err)
		}
		got := RootFields(doc, doc.Operations[0])
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: %v, want %v", tc.q, got, tc.want)
		}
	}
	// A fragment cycle (rejected by validation) does not loop.
	doc := &ast.QueryDocument{
		Operations: ast.OperationList{{Operation: ast.Query, SelectionSet: ast.SelectionSet{&ast.FragmentSpread{Name: "A"}}}},
		Fragments: ast.FragmentDefinitionList{
			{Name: "A", SelectionSet: ast.SelectionSet{&ast.FragmentSpread{Name: "A"}, &ast.Field{Name: "x"}}},
		},
	}
	if got := RootFields(doc, doc.Operations[0]); len(got) != 1 {
		t.Fatalf("cycle: %v", got)
	}
}

// M1 review H1: aliases cannot multiply credential checks; any operation
// with a credential field must select exactly one root field (rejected
// before any resolver runs).
func TestSingleRootFieldGate(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Access = allowAll{} })
	login := `login(email: "a@example.com", password: "x") { secondFactorRequired }`
	for name, q := range map[string]string{
		"aliased logins":      `mutation { a: ` + login + ` b: ` + login + ` }`,
		"login plus another":  `mutation { ` + login + ` logout }`,
		"login via fragments": `mutation { ...L ...L } fragment L on Mutation { ` + login + ` }`,
		"aliased step-ups":    `mutation { a: completeStepUp(method: PASSWORD, password: "x") { aal } b: completeStepUp(method: PASSWORD, password: "y") { aal } }`,
		"aliased TOTP codes":  `mutation { a: completeSecondFactor(method: TOTP, code: "000000") { secondFactorRequired } b: completeSecondFactor(method: TOTP, code: "000001") { secondFactorRequired } }`,
	} {
		t.Run(name, func(t *testing.T) {
			_, resp := e.query(t, q)
			if got := errorCode(resp); got != "VALIDATION" {
				t.Fatalf("code %q (%+v), want VALIDATION", got, resp)
			}
			if resp.Data != nil {
				t.Fatalf("data returned: %v", resp.Data)
			}
		})
	}
	// One credential field alone passes the gate (and then fails for the
	// missing auth service: an internal error, not VALIDATION).
	_, resp := e.query(t, `mutation { `+login+` }`)
	if got := errorCode(resp); got == "VALIDATION" {
		t.Fatalf("a single login was rejected: %+v", resp)
	}
	// Other operations may still select several root fields.
	_, resp = e.query(t, `{ a: serverInfo { version } b: serverInfo { devMode } }`)
	if len(resp.Errors) > 0 {
		t.Fatalf("several non-credential fields rejected: %+v", resp)
	}
}
