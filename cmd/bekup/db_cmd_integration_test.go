//go:build integration

package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/externaltest"
	"github.com/jlentink/bekup/internal/logging"
)

// TestDBBootstrapCLI drives the external database flow (D-207) through the
// CLI against a plain PostgreSQL 17 with a test CA: `db bootstrap` as a
// CREATEROLE+CREATEDB admin, `migrate check`, `migrate up`, then a
// `db bootstrap --reset-passwords` of the migrated installation, which is
// recorded in the audit log (cli.db.bootstrap).
func TestDBBootstrapCLI(t *testing.T) {
	ctx := t.Context()
	s := externaltest.Start(t, externaltest.PG17, externaltest.Options{})
	const adminPW = "admin-password-0123456789"
	if err := s.CreateAdmin(ctx, "provider_admin", adminPW); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	appFile := writePW(t, dir, "app", `app-p@ss'w"o\rd:/?#-0123`)
	ownerFile := writePW(t, dir, "owner", "owner-password-0123456789")
	adminFile := writePW(t, dir, "admin", adminPW)
	cfg := writeConfig(t, "bekup.toml", validConfig+fmt.Sprintf(`
[database]
host = %q
port = %d
name = "bekup_ext"
password_file = %q
owner_password_file = %q
tls_ca_file = %q
`, s.Host, s.Port, appFile, ownerFile, s.CAFile))

	cli := func(args ...string) (string, string, error) {
		return run(t, append([]string{"--config", cfg}, args...)...)
	}
	out, stderr, err := cli("db", "bootstrap", "--admin-user", "provider_admin", "--admin-password-file", adminFile)
	if err != nil {
		t.Fatalf("bootstrap: %v\n%s\n%s", err, out, stderr)
	}
	for _, want := range []string{"done: create role bekup_owner", "done: create database bekup_ext owned by bekup_owner",
		"host verified: true", "preflight OK", "log in and pass the role checks", "next:    bekup migrate up"} {
		if !strings.Contains(out, want) {
			t.Errorf("bootstrap output misses %q:\n%s", want, out)
		}
	}
	if strings.Contains(out+stderr, adminPW) || strings.Contains(out+stderr, "p@ss") || strings.Contains(out+stderr, "SCRAM-SHA-256$") {
		t.Fatal("a password or verifier leaked into the output")
	}

	if out, _, err = cli("migrate", "check"); err != nil || !strings.Contains(out, "preflight OK") || !strings.Contains(out, "citext:          creatable") {
		t.Fatalf("migrate check: %v\n%s", err, out)
	}
	if out, stderr, err = cli("migrate", "up"); err != nil {
		t.Fatalf("migrate up: %v\n%s\n%s", err, out, stderr)
	}

	// Existing roles need --adopt-existing-roles.
	if _, _, err = cli("db", "bootstrap", "--admin-user", "provider_admin", "--admin-password-file", adminFile); err == nil ||
		!strings.Contains(err.Error(), "--adopt-existing-roles") {
		t.Fatalf("existing roles accepted without --adopt-existing-roles: %v", err)
	}
	out, stderr, err = cli("db", "bootstrap", "--admin-user", "provider_admin", "--admin-password-file", adminFile, "--adopt-existing-roles", "--reset-passwords")
	if err != nil || !strings.Contains(out, "recorded in the audit log (cli.db.bootstrap)") {
		t.Fatalf("reset: %v\n%s\n%s", err, out, stderr)
	}
	res, err := config.LoadBekup(config.Options{ConfigPath: cfg, LookupEnv: func(string) (string, bool) { return "", false }, Environ: func() []string { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	creds, err := res.Config.AppDBCredentials()
	if err != nil {
		t.Fatal(err)
	}
	tlsOpts, err := db.LoadTLSOptions(res.Config)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.NewPool(ctx, creds, db.PoolOptions{TLS: tlsOpts, Registry: logging.NewRegistry()})
	creds.Zero()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	page, err := audit.New(pool, audit.Options{}).List(ctx, audit.Filter{Action: audit.ActionCLIDBBootstrap}, 0, 10)
	if err != nil || len(page.Entries) != 1 || page.Entries[0].ActorKind != audit.ActorCLI {
		t.Fatalf("audit: %v %+v", err, page)
	}
	if d := string(page.Entries[0].Details); !strings.Contains(d, "set the password of role bekup_app") || strings.Contains(d, "SCRAM") ||
		!strings.Contains(d, `"rotated_logins":true`) || !strings.Contains(d, `"adopted_roles":true`) {
		t.Fatalf("details %s", d)
	}

	// Nothing to change: no second audit entry.
	if out, _, err = cli("db", "bootstrap", "--admin-user", "provider_admin", "--admin-password-file", adminFile, "--adopt-existing-roles"); err != nil ||
		!strings.Contains(out, "nothing changed") {
		t.Fatalf("idempotent run: %v\n%s", err, out)
	}
}
