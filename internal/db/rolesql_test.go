package db

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jlentink/bekup/internal/config"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden roles-sql scripts")

// fixedVerifier is a verifier with a fixed salt, so the golden files are
// stable.
func fixedVerifier(t *testing.T, pw string, salt byte) string {
	t.Helper()
	v, err := SCRAMVerifier([]byte(pw), bytes.Repeat([]byte{salt}, scramSaltLen), SCRAMIterations)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestRolesSQLGolden(t *testing.T) {
	const ownerPW, appPW = "owner-password-0123456789", `app-p@ss'w"o\rd;--0123`
	ownerV, appV := fixedVerifier(t, ownerPW, 1), fixedVerifier(t, appPW, 2)
	tests := []struct {
		name string
		plan RolePlan
	}{
		{"roles_verifier", NewRolesSQLPlan("bekup", ownerV, appV, false, false)},
		{"roles_verifier_citext", NewRolesSQLPlan("bekup_prod", ownerV, appV, false, true)},
		{"roles_prompt", NewRolesSQLPlan("bekup", "", "", true, false)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RolesSQLScript(tc.plan)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{ownerPW, appPW, "p@ss"} {
				if strings.Contains(got, secret) {
					t.Fatalf("script contains a plaintext password")
				}
			}
			golden := filepath.Join("testdata", tc.name+".sql")
			if *updateGolden {
				if err := os.MkdirAll("testdata", 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden) //nolint:gosec // G304: fixed test path
			if err != nil {
				t.Fatalf("read golden (run go test -update): %v", err)
			}
			if got != string(want) {
				t.Errorf("script differs from %s (review, then run with -update):\n%s", golden, got)
			}
		})
	}
}

func TestRolePlanStatements(t *testing.T) {
	ownerV, appV := fixedVerifier(t, "owner-password-0123456789", 1), fixedVerifier(t, "app-password-0123456789", 2)

	// Bootstrap-style plan on PostgreSQL 16 with existing roles and a reset.
	st, err := RolePlan{
		Database: "bekup", SetOwnerPassword: true, SetAppPassword: true, OwnerVerifier: ownerV, AppVerifier: appV,
		GrantOwner: true, RevokeOwner: true, GrantWithOptions: true,
	}.Statements()
	if err != nil {
		t.Fatal(err)
	}
	var sqls []string
	for _, s := range st {
		sqls = append(sqls, s.SQL)
		if strings.Contains(s.SQL, "SCRAM-SHA-256$") != s.Sensitive {
			t.Errorf("sensitive flag wrong for %q", s.Action)
		}
		if strings.Contains(s.Action, "SCRAM") {
			t.Errorf("action leaks a verifier: %s", s.Action)
		}
	}
	joined := strings.Join(sqls, "\n")
	for _, want := range []string{
		"ALTER ROLE bekup_owner WITH PASSWORD 'SCRAM-SHA-256$4096:",
		"GRANT bekup_owner TO CURRENT_USER WITH INHERIT TRUE, SET TRUE;",
		`REVOKE ALL ON DATABASE "bekup" FROM PUBLIC;`,
		`GRANT CONNECT ON DATABASE "bekup" TO bekup_app;`,
		"REVOKE bekup_owner FROM CURRENT_USER;",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "CREATE ROLE") || strings.Contains(joined, "CREATE DATABASE") {
		t.Errorf("unexpected creates:\n%s", joined)
	}

	// Single account: NOLOGIN roles, kept memberships, CONNECT for the account.
	st, err = NewSingleAccountPlan("bekup").Statements()
	if err != nil {
		t.Fatal(err)
	}
	sqls = sqls[:0]
	for _, s := range st {
		sqls = append(sqls, s.SQL)
	}
	joined = strings.Join(sqls, "\n")
	for _, want := range []string{
		"CREATE ROLE bekup_owner WITH NOLOGIN", "CREATE ROLE bekup_app WITH NOLOGIN",
		"GRANT bekup_app TO CURRENT_USER;", `GRANT CONNECT ON DATABASE "bekup" TO CURRENT_USER;`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "PASSWORD") || strings.Contains(joined, "REVOKE bekup_owner") {
		t.Errorf("single account must not set passwords or revoke:\n%s", joined)
	}

	for name, p := range map[string]RolePlan{
		"bad database":          {Database: `x"; DROP`, CreateOwner: true, OwnerVerifier: ownerV},
		"postgres":              {Database: "postgres"},
		"missing verifier":      {Database: "bekup", CreateOwner: true},
		"malformed verifier":    {Database: "bekup", CreateApp: true, AppVerifier: "SCRAM-SHA-256$4096:x'; DROP ROLE x; --"},
		"single with passwords": {Database: "bekup", SingleAccount: true, SetAppPassword: true},
	} {
		if _, err := p.Statements(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestBootstrapOptionsValidate(t *testing.T) {
	admin := config.DBCredentials{Host: "db", Port: 5432, Database: "postgres", User: "admin", Password: []byte("admin-password-x")}
	good := func() BootstrapOptions {
		return BootstrapOptions{Admin: admin, Database: "bekup", OwnerPassword: []byte("owner-password-0123456789"), AppPassword: []byte("app-password-0123456789")}
	}
	if o := good(); o.validate() != nil {
		t.Fatal(o.validate())
	}
	for name, mutate := range map[string]func(*BootstrapOptions){
		"url admin":        func(o *BootstrapOptions) { o.Admin = config.DBCredentials{URL: []byte("postgres://x")} },
		"no admin user":    func(o *BootstrapOptions) { o.Admin.User = "" },
		"bad database":     func(o *BootstrapOptions) { o.Database = "template1" },
		"short owner pw":   func(o *BootstrapOptions) { o.OwnerPassword = []byte("short") },
		"non-ascii app pw": func(o *BootstrapOptions) { o.AppPassword = []byte("pässwörd-0123456789") },
		"same passwords":   func(o *BootstrapOptions) { o.AppPassword = o.OwnerPassword },
		"single with reset": func(o *BootstrapOptions) {
			o.SingleAccount, o.OwnerPassword, o.AppPassword, o.ResetPasswords = true, nil, nil, true
		},
		"single with pws": func(o *BootstrapOptions) { o.SingleAccount = true },
	} {
		o := good()
		mutate(&o)
		err := o.validate()
		if err == nil {
			t.Errorf("%s accepted", name)
			continue
		}
		if strings.Contains(err.Error(), "password-0123") || strings.Contains(err.Error(), "pässwörd") {
			t.Errorf("%s: error leaks a password: %v", name, err)
		}
	}
	o := good()
	o.SingleAccount, o.OwnerPassword, o.AppPassword = true, nil, nil
	if err := o.validate(); err != nil {
		t.Errorf("single account: %v", err)
	}
}

func TestPGErrorText(t *testing.T) {
	pe := &pgconn.PgError{Message: `role "bekup_app" already exists`, Code: "42710", Hint: "drop it"}
	if got := pgErrorText(pe); got != `role "bekup_app" already exists (SQLSTATE 42710); hint: drop it` {
		t.Errorf("got %q", got)
	}
	if got := pgErrorText(errors.New("dial tcp: secret-ish detail")); strings.Contains(got, "secret") {
		t.Errorf("got %q", got)
	}
	if got := pgErrorText(context.Canceled); got != context.Canceled.Error() {
		t.Errorf("got %q", got)
	}
}
