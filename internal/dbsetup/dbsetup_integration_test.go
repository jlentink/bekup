//go:build integration

package dbsetup

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db/externaltest"
)

// externalConfig is a fields-mode configuration for the test server s.
func externalConfig(t *testing.T, s *externaltest.Server, name string) *config.Bekup {
	t.Helper()
	cfg := fieldsConfig(t, func(d *config.Database) {
		d.Host, d.Port, d.Name = s.Host, int(s.Port), name
		d.TLSMode, d.TLSCAFile = config.DBTLSVerifyFull, s.CAFile
	})
	return cfg
}

// TestIntegrationBootstrapMigrateVerify runs the shared database code of
// `db bootstrap`, `migrate up` and the browser setup (D-207, D-217) against
// a plain PostgreSQL 17 with a test CA: bootstrap as a CREATEROLE+CREATEDB
// admin, Verify refuses the unmigrated schema, MigrateUp applies every
// migration once, Verify passes, and a password reset of the migrated
// installation is audited. A second database on the same server with the
// existing roles is the bundled flow: migrations only.
func TestIntegrationBootstrapMigrateVerify(t *testing.T) {
	ctx := t.Context()
	s := externaltest.Start(t, externaltest.PG17, externaltest.Options{})
	const adminPW = "admin-password-0123456789"
	if err := s.CreateAdmin(ctx, "provider_admin", adminPW); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.DiscardHandler)
	cfg := externalConfig(t, s, "bekup_ext")
	bootstrap := func(cfg *config.Bekup, adopt, reset bool) (string, error) {
		var out, warn bytes.Buffer
		err := Bootstrap(ctx, BootstrapRequest{
			Config: cfg, AdminUser: "provider_admin", AdoptExisting: adopt, ResetPasswords: reset,
			AdminPassword: func() ([]byte, error) { return []byte(adminPW), nil },
			Out:           &out, Warn: &warn, Logger: logger,
		})
		if warn.Len() > 0 {
			t.Errorf("verify-full warned: %q", warn.String())
		}
		return out.String(), err
	}
	out, err := bootstrap(cfg, false, false)
	if err != nil {
		t.Fatalf("bootstrap: %v\n%s", err, out)
	}
	for _, want := range []string{"done: create role bekup_owner", "done: create database bekup_ext owned by bekup_owner",
		"host verified: true", "preflight OK", "log in and pass the role checks", "next:    bekup migrate up"} {
		if !strings.Contains(out, want) {
			t.Errorf("bootstrap report misses %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, adminPW) || strings.Contains(out, "SCRAM-SHA-256$") {
		t.Fatal("a password or verifier in the report")
	}

	var report bytes.Buffer
	if err := Verify(ctx, cfg, &report, logger); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("verify before the migrations: %v\n%s", err, report.String())
	}
	rs, err := MigrateUp(ctx, cfg, logger, &report)
	if err != nil || len(rs) == 0 {
		t.Fatalf("migrate up: %d applied, %v\n%s", len(rs), err, report.String())
	}
	if rs, err = MigrateUp(ctx, cfg, logger, &report); err != nil || len(rs) != 0 {
		t.Fatalf("second migrate up: %d applied, %v", len(rs), err)
	}
	report.Reset()
	if err := Verify(ctx, cfg, &report, logger); err != nil || !strings.Contains(report.String(), "preflight OK") || !strings.Contains(report.String(), "schema:  current") {
		t.Fatalf("verify: %v\n%s", err, report.String())
	}
	if out, err = bootstrap(cfg, true, true); err != nil || !strings.Contains(out, "recorded in the audit log (cli.db.bootstrap)") {
		t.Fatalf("password reset: %v\n%s", err, out)
	}

	// The bundled flow: the roles exist, the database is new, only the
	// migrations run (the browser setup's setup-web-db with the bundled
	// postgres).
	two := externalConfig(t, s, "bekup_two")
	two.Database.PasswordFile, two.Database.OwnerPasswordFile = cfg.Database.PasswordFile, cfg.Database.OwnerPasswordFile
	if out, err = bootstrap(two, true, false); err != nil || !strings.Contains(out, "create database bekup_two") {
		t.Fatalf("second database: %v\n%s", err, out)
	}
	if rs, err = MigrateUp(ctx, two, logger, &report); err != nil || len(rs) == 0 {
		t.Fatalf("migrate up of the second database: %d applied, %v", len(rs), err)
	}
	report.Reset()
	if err := Verify(ctx, two, &report, logger); err != nil || !strings.Contains(report.String(), "schema:  current") {
		t.Fatalf("verify of the second database: %v\n%s", err, report.String())
	}
}
