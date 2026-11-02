package web

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/dbsetup"
	"github.com/jlentink/bekup/internal/setup"
	"github.com/jlentink/bekup/internal/setup/wizard"
)

// dbHarness runs the database step with fakes.
type dbHarness struct {
	st         setup.State
	tlsMode    string
	bootReqs   []dbsetup.BootstrapRequest
	bootPass   [][]byte
	bootErr    []error
	migrateErr []error
	migrations int
	verified   int
	verifyErr  error
	slept      time.Duration
}

func (h *dbHarness) options() DatabaseOptions {
	return DatabaseOptions{
		Load: func() (*config.Result[config.Bekup], error) {
			cfg := config.DefaultBekup()
			cfg.Database.TLSMode = h.tlsMode
			return &config.Result[config.Bekup]{Config: &cfg, Dev: true}, nil
		},
		Guard:     dbsetup.Guard{Version: "1.2.3", LookupEnv: func(string) (string, bool) { return "", false }},
		readState: func(string) (*setup.State, error) { st := h.st; return &st, nil },
		bootstrap: func(_ context.Context, r dbsetup.BootstrapRequest) error {
			h.bootReqs = append(h.bootReqs, r)
			if r.AdminPassword != nil {
				pw, err := r.AdminPassword()
				if err != nil {
					return err
				}
				h.bootPass = append(h.bootPass, append([]byte(nil), pw...))
			}
			if len(h.bootErr) > 0 {
				err := h.bootErr[0]
				h.bootErr = h.bootErr[1:]
				return err
			}
			return nil
		},
		migrate: func(context.Context, *config.Bekup, *slog.Logger, io.Writer) ([]db.MigrationResult, error) {
			h.migrations++
			if len(h.migrateErr) > 0 {
				err := h.migrateErr[0]
				h.migrateErr = h.migrateErr[1:]
				return nil, err
			}
			return []db.MigrationResult{{Version: 1, Name: "init"}}, nil
		},
		verify: func(context.Context, *config.Bekup, io.Writer, *slog.Logger) error {
			h.verified++
			return h.verifyErr
		},
		sleep: func(_ context.Context, d time.Duration) error { h.slept += d; return nil },
	}
}

func runStep(t *testing.T, h *dbHarness, answers []wizard.Answer) (*wizard.Script, error) {
	t.Helper()
	s := &wizard.Script{Answers: answers}
	err := DatabaseStep(h.options())(context.Background(), s, t.TempDir())
	if err == nil && !s.Done() {
		t.Fatalf("unused answers after %v", s.Screens)
	}
	return s, err
}

func TestDatabaseStepBootstrap(t *testing.T) {
	h := &dbHarness{st: setup.State{DatabaseMode: setup.DBExternal, DatabaseAuth: setup.DBAuthBootstrap}, tlsMode: config.DBTLSVerifyFull}
	s, err := runStep(t, h, []wizard.Answer{
		{Title: "Create bekup's roles", Value: dbChoiceBootstrap},
		{Title: "Administrator role", Value: "dbadmin"},
		{Title: "Existing bekup roles", Value: "no"},
		{Title: "Administrator password", Value: "admin-pass-123"},
	})
	if err != nil {
		t.Fatalf("%v\n%v", err, s.Screens)
	}
	if len(h.bootReqs) != 1 || h.bootReqs[0].AdminUser != "dbadmin" || h.bootReqs[0].AllowInsecure || string(h.bootPass[0]) != "admin-pass-123" || h.migrations != 1 || h.verified != 1 {
		t.Fatalf("bootstrap %+v pass %q migrations %d", h.bootReqs, h.bootPass, h.migrations)
	}
	if !strings.Contains(s.Output.String(), "applied: 00001 init") {
		t.Fatalf("report:\n%s", s.Output.String())
	}
}

// A weak tls_mode needs the explicit confirmation; declining goes back.
func TestDatabaseStepInsecureAdmin(t *testing.T) {
	h := &dbHarness{st: setup.State{DatabaseMode: setup.DBExternal, DatabaseAuth: setup.DBAuthBootstrap}, tlsMode: config.DBTLSRequire}
	_, err := runStep(t, h, []wizard.Answer{
		{Title: "Create bekup's roles", Value: dbChoiceBootstrap},
		{Title: "Insecure administrator connection", Value: "no"},
		{Title: "Create bekup's roles", Value: dbChoiceBootstrap},
		{Title: "Insecure administrator connection", Value: "yes"},
		{Title: "Administrator role", Value: "dbadmin"},
		{Title: "Existing bekup roles", Value: "yes"},
		{Title: "Administrator password", Value: "admin-pass-123"},
	})
	if err != nil || len(h.bootReqs) != 1 || !h.bootReqs[0].AllowInsecure || !h.bootReqs[0].AdoptExisting {
		t.Fatalf("%v %+v", err, h.bootReqs)
	}
}

// A failed bootstrap can be retried (the password is asked again) or
// stopped.
func TestDatabaseStepBootstrapRetry(t *testing.T) {
	h := &dbHarness{st: setup.State{DatabaseMode: setup.DBExternal, DatabaseAuth: setup.DBAuthBootstrap}, tlsMode: config.DBTLSVerifyFull,
		bootErr: []error{errors.New("login failed"), errors.New("still failing")}}
	login := []wizard.Answer{
		{Title: "Create bekup's roles", Value: dbChoiceBootstrap},
		{Title: "Administrator role", Value: "dbadmin"},
		{Title: "Existing bekup roles", Value: "no"},
		{Title: "Administrator password", Value: "admin-pass-123"},
	}
	answers := append(append(append([]wizard.Answer{}, login...), wizard.Answer{Title: "db bootstrap failed", Value: dbChoiceRetry}), login...)
	answers = append(answers, wizard.Answer{Title: "db bootstrap failed", Value: dbChoiceStop})
	if _, err := runStep(t, h, answers); err == nil || !strings.Contains(err.Error(), "still failing") || h.migrations != 0 || len(h.bootReqs) != 2 {
		t.Fatalf("%v, %d bootstraps, %d migrations", err, len(h.bootReqs), h.migrations)
	}
}

// Back on the first screen of the database run stays on it (as in the
// wizard) instead of ending the run: also after a bootstrap screen went
// back to the role choice, where the browser still offers Back.
func TestDatabaseStepBackStays(t *testing.T) {
	h := &dbHarness{st: setup.State{DatabaseMode: setup.DBExternal, DatabaseAuth: setup.DBAuthBootstrap}, tlsMode: config.DBTLSVerifyFull}
	s, err := runStep(t, h, []wizard.Answer{
		{Title: "Create bekup's roles", Back: true},
		{Title: "Create bekup's roles", Value: dbChoiceBootstrap},
		{Title: "Administrator role", Value: "dbadmin"},
		{Title: "Existing bekup roles", Value: "no"},
		{Title: "Administrator password", Back: true},
		{Title: "Create bekup's roles", Back: true},
		{Title: "Create bekup's roles", Value: dbChoiceBootstrap},
		{Title: "Administrator role", Value: "dbadmin"},
		{Title: "Existing bekup roles", Value: "no"},
		{Title: "Administrator password", Value: "admin-pass-123"},
	})
	if err != nil || len(h.bootReqs) != 1 || h.migrations != 1 || h.verified != 1 {
		t.Fatalf("%v, %d bootstraps, %d migrations\n%v", err, len(h.bootReqs), h.migrations, s.Screens)
	}
	h = &dbHarness{st: setup.State{DatabaseMode: setup.DBExternal, DatabaseAuth: setup.DBAuthDBASQL}, tlsMode: config.DBTLSVerifyFull}
	if _, err := runStep(t, h, []wizard.Answer{{Title: "Roles from your DBA", Back: true}, {Title: "Roles from your DBA"}}); err != nil || h.migrations != 1 {
		t.Fatalf("dba sql: %v", err)
	}
}

// Single-account: no administrator login; existing roles and the DBA's SQL
// skip the bootstrap.
func TestDatabaseStepRoleModes(t *testing.T) {
	h := &dbHarness{st: setup.State{DatabaseMode: setup.DBExternal, DatabaseAuth: setup.DBAuthSingleAccount}, tlsMode: config.DBTLSVerifyFull}
	if _, err := runStep(t, h, []wizard.Answer{{Title: "Create bekup's roles", Value: dbChoiceBootstrap}}); err != nil || len(h.bootReqs) != 1 || h.bootReqs[0].AdminPassword != nil {
		t.Fatalf("single account: %v %+v", err, h.bootReqs)
	}
	h = &dbHarness{st: setup.State{DatabaseMode: setup.DBExternal, DatabaseAuth: setup.DBAuthExistingRoles}, tlsMode: config.DBTLSVerifyFull}
	if _, err := runStep(t, h, nil); err != nil || len(h.bootReqs) != 0 || h.migrations != 1 {
		t.Fatalf("existing roles: %v", err)
	}
	h = &dbHarness{st: setup.State{DatabaseMode: setup.DBExternal, DatabaseAuth: setup.DBAuthDBASQL}, tlsMode: config.DBTLSVerifyFull}
	if _, err := runStep(t, h, []wizard.Answer{{Title: "Roles from your DBA"}}); err != nil || len(h.bootReqs) != 0 || h.migrations != 1 {
		t.Fatalf("dba sql: %v", err)
	}
}

// The bundled database: migrate retries while postgres starts.
func TestDatabaseStepBundledWaits(t *testing.T) {
	h := &dbHarness{st: setup.State{}, migrateErr: []error{errors.New("connection refused"), errors.New("connection refused")}}
	s, err := runStep(t, h, nil)
	if err != nil || h.migrations != 3 || h.slept == 0 || !strings.Contains(s.Output.String(), "waiting for the database") {
		t.Fatalf("%v, %d migrations, slept %v\n%s", err, h.migrations, h.slept, s.Output.String())
	}
}

// The deployment guard refuses before anything connects.
func TestDatabaseStepGuard(t *testing.T) {
	h := &dbHarness{st: setup.State{}}
	o := h.options()
	o.Guard.LookupEnv = func(k string) (string, bool) {
		if k == "BEKUP_SETUP_VERSION" {
			return "9.9.9", true
		}
		return "", false
	}
	err := DatabaseStep(o)(context.Background(), &wizard.Script{}, t.TempDir())
	if err == nil || h.migrations != 0 {
		t.Fatalf("guard: %v, %d migrations", err, h.migrations)
	}
}

// A failed final check fails the step; a failed migration never checks.
func TestDatabaseStepVerify(t *testing.T) {
	h := &dbHarness{st: setup.State{}, verifyErr: errors.New("role check of bekup_app failed")}
	if _, err := runStep(t, h, nil); err == nil || !strings.Contains(err.Error(), "role check") || h.verified != 1 {
		t.Fatalf("verify failure: %v, %d checks", err, h.verified)
	}
	h = &dbHarness{st: setup.State{DatabaseMode: setup.DBExternal, DatabaseAuth: setup.DBAuthExistingRoles}, migrateErr: []error{errors.New("preflight failed")}}
	if _, err := runStep(t, h, []wizard.Answer{{Title: "migrate up failed", Value: dbChoiceStop}}); err == nil || h.verified != 0 {
		t.Fatalf("failed migration: %v, %d checks", err, h.verified)
	}
}
