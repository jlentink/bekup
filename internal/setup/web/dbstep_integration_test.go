//go:build integration

package web

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db/externaltest"
	"github.com/jlentink/bekup/internal/dbsetup"
	"github.com/jlentink/bekup/internal/setup"
	"github.com/jlentink/bekup/internal/setup/wizard"
)

// TestIntegrationBrowserDatabaseStep drives the browser setup's database
// run (setup-web-db, D-217) over its HTTPS API against a plain PostgreSQL
// 17 with a test CA: db bootstrap with the administrator password sent as
// a secret answer, migrate up and the final check. A second run on a new
// database with the roles in place is the bundled flow (migrations and
// check only, no questions).
func TestIntegrationBrowserDatabaseStep(t *testing.T) {
	ctx := t.Context()
	s := externaltest.Start(t, externaltest.PG17, externaltest.Options{})
	const adminPW = "admin-password-0123456789"
	if err := s.CreateAdmin(ctx, "provider_admin", adminPW); err != nil {
		t.Fatal(err)
	}
	keys := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(keys, name)
		if err := os.WriteFile(p, []byte(content+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	appFile, ownerFile := write("app", "app-password-0123456789"), write("owner", "owner-password-0123456789")
	configFor := func(name string) func() (*config.Result[config.Bekup], error) {
		return func() (*config.Result[config.Bekup], error) {
			cfg := config.DefaultBekup()
			d := &cfg.Database
			d.Host, d.Port, d.Name = s.Host, int(s.Port), name
			d.User, d.OwnerUser = config.DBAppRole, config.DBOwnerRole
			d.PasswordFile, d.OwnerPasswordFile = appFile, ownerFile
			d.TLSMode, d.TLSCAFile = config.DBTLSVerifyFull, s.CAFile
			return &config.Result[config.Bekup]{Config: &cfg}, nil
		}
	}
	run := func(name string, st setup.State, answers []wizard.Answer) Status {
		t.Helper()
		h := start(t, func(o *Options) {
			o.Mode, o.Init = ModeDatabase, nil
			o.Database = DatabaseStep(DatabaseOptions{
				Load: configFor(name),
				Guard: dbsetup.Guard{Version: "1.2.3", LookupEnv: func(k string) (string, bool) {
					return map[string]string{"BEKUP_SETUP_VERSION": "1.2.3"}[k], k == "BEKUP_SETUP_VERSION"
				}},
				Logger:    slog.New(slog.DiscardHandler),
				readState: func(string) (*setup.State, error) { return &st, nil },
			})
			if err := os.WriteFile(filepath.Join(o.Dir, setup.StateFileName), []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		})
		h.login()
		final, _ := h.drive(answers)
		if final.Phase != PhaseDone {
			t.Fatalf("%s: %s %q\n%s", name, final.Phase, final.Error, final.Report)
		}
		if strings.Contains(final.Report, adminPW) || strings.Contains(final.Report, "SCRAM-SHA-256$") {
			t.Fatalf("%s: a password or verifier in the report", name)
		}
		return final
	}

	st := run("bekup_ext", setup.State{DatabaseMode: setup.DBExternal, DatabaseAuth: setup.DBAuthBootstrap}, []wizard.Answer{
		{Title: "Create bekup's roles", Value: dbChoiceBootstrap},
		{Title: "Administrator role", Value: "provider_admin"},
		{Title: "Existing bekup roles", Value: "no"},
		{Title: "Administrator password", Value: adminPW},
	})
	for _, want := range []string{"done: create role bekup_owner", "done: create database bekup_ext owned by bekup_owner", "host verified: true",
		"-- migrate up --", "applied: ", "migrations done", "-- migrate check --", "preflight OK", "schema:  current", "admin create"} {
		if !strings.Contains(st.Report, want) {
			t.Errorf("report misses %q:\n%s", want, st.Report)
		}
	}

	// The roles exist now: a new database for the bundled flow.
	var out bytes.Buffer
	res, err := configFor("bekup_two")()
	if err != nil {
		t.Fatal(err)
	}
	if err := dbsetup.Bootstrap(ctx, dbsetup.BootstrapRequest{
		Config: res.Config, AdminUser: "provider_admin", AdoptExisting: true,
		AdminPassword: func() ([]byte, error) { return []byte(adminPW), nil }, Out: &out, Warn: &out, Logger: slog.New(slog.DiscardHandler),
	}); err != nil {
		t.Fatalf("second database: %v\n%s", err, out.String())
	}
	st = run("bekup_two", setup.State{DatabaseMode: setup.DBBundled}, nil)
	if !strings.Contains(st.Report, "applied: ") || !strings.Contains(st.Report, "schema:  current") || strings.Contains(st.Report, "-- db bootstrap --") {
		t.Fatalf("bundled flow:\n%s", st.Report)
	}
}
