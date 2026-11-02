package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/dbsetup"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/setup"
	"github.com/jlentink/bekup/internal/setup/wizard"
)

// DefaultWaitForDatabase is how long the database step waits for the
// bundled postgres to accept the migration.
const DefaultWaitForDatabase = 3 * time.Minute

// DatabaseOptions configure the database step of the setup-web-db service
// (D-217). It runs as UID 65532 with the migrate service's mounts, so the
// configuration loads exactly as for `bekup migrate up`.
type DatabaseOptions struct {
	// Load loads bekup.toml (config.LoadBekup with the service's
	// defaults).
	Load func() (*config.Result[config.Bekup], error)
	// Guard is the setup version guard (Dev is taken from the loaded
	// configuration).
	Guard dbsetup.Guard
	// Logger receives the migration log lines.
	Logger *slog.Logger
	// WaitFor bounds the retries while the bundled postgres starts.
	WaitFor time.Duration
	// Hooks (tests).
	bootstrap func(context.Context, dbsetup.BootstrapRequest) error
	migrate   func(context.Context, *config.Bekup, *slog.Logger, io.Writer) ([]db.MigrationResult, error)
	verify    func(context.Context, *config.Bekup, io.Writer, *slog.Logger) error
	sleep     func(context.Context, time.Duration) error
	readState func(string) (*setup.State, error)
}

// Step choices of the database step.
const (
	dbChoiceBootstrap = "bootstrap"
	dbChoiceSkip      = "skip"
	dbChoiceRetry     = "retry"
	dbChoiceStop      = "stop"
)

// DatabaseStep returns the Database func of Options: the deployment guard,
// the roles (db bootstrap, external database), the migrations and the
// final check (dbsetup.Verify), each asked and reported through the
// wizard UI.
func DatabaseStep(o DatabaseOptions) func(context.Context, wizard.UI, string) error {
	if o.WaitFor == 0 {
		o.WaitFor = DefaultWaitForDatabase
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	o.Guard.Logger = o.Logger
	if o.bootstrap == nil {
		o.bootstrap = dbsetup.Bootstrap
	}
	if o.migrate == nil {
		o.migrate = dbsetup.MigrateUp
	}
	if o.verify == nil {
		o.verify = dbsetup.Verify
	}
	if o.sleep == nil {
		o.sleep = sleepCtx
	}
	if o.readState == nil {
		o.readState = setup.ReadState
	}
	return func(ctx context.Context, ui wizard.UI, dir string) error {
		st, err := o.readState(dir)
		if err != nil {
			return err
		}
		res, err := o.Load()
		if err != nil {
			return fmt.Errorf("invalid configuration:\n%w", err)
		}
		g := o.Guard
		g.Dev = res.Dev
		// D-212: never prepare a database for an image setup has not
		// prepared the deployment for (before anything connects).
		if err := dbsetup.CheckDeployment(ctx, g, "setup web"); err != nil {
			return err
		}
		out := ui.Out()
		if st.DatabaseMode == setup.DBExternal {
			if err := o.roles(ctx, ui, st, res.Config); err != nil {
				return err
			}
		}
		if err := o.migrateWithRetry(ctx, ui, st.DatabaseMode != setup.DBExternal, res.Config, out); err != nil {
			return err
		}
		// migrate check and the role checks of both roles, as serve will
		// run them at startup.
		_, _ = fmt.Fprintln(out, "\n-- migrate check --")
		return o.verify(ctx, res.Config, out, o.Logger)
	}
}

// roles runs db bootstrap when the deployment's roles come from it
// (bootstrap, single-account); with existing roles or the DBA's SQL the
// roles exist already.
func (o *DatabaseOptions) roles(ctx context.Context, ui wizard.UI, st *setup.State, cfg *config.Bekup) error {
	out := ui.Out()
	switch st.DatabaseAuth {
	case setup.DBAuthExistingRoles:
		_, _ = fmt.Fprintln(out, "roles:   bekup_owner and bekup_app exist already (existing-roles); the migration checks them")
		return nil
	case setup.DBAuthDBASQL:
		_, _ = fmt.Fprintln(out, "roles:   created by your DBA with the roles SQL (dba-sql); the migration checks them")
		for {
			// The first screen of this run: Back stays on it (as in the
			// wizard) instead of ending the run.
			err := ui.Note("Roles from your DBA", "This deployment's roles come from the roles SQL setup wrote for your DBA.\nContinue once the DBA has run it with psql; the migration checks them.")
			if !errors.Is(err, wizard.ErrBack) {
				return err
			}
		}
	case setup.DBAuthBootstrap, setup.DBAuthSingleAccount:
		// db bootstrap below.
	default:
		// An unknown value (a newer state file): bootstrap, which
		// validates the configuration itself.
	}
	for {
		choice, err := ui.Select("Create bekup's roles and database", "db bootstrap connects with an administrator account and creates the roles bekup_owner and\nbekup_app and bekup's database (D-207). The administrator password is used for this\nconnection only, never stored.", []wizard.Option{
			{Value: dbChoiceBootstrap, Label: "create them now (db bootstrap)"},
			{Value: dbChoiceSkip, Label: "they exist already: only migrate"},
		}, dbChoiceBootstrap)
		if errors.Is(err, wizard.ErrBack) {
			continue // the first screen of this run stays (as in the wizard)
		}
		if err != nil {
			return err
		}
		if choice == dbChoiceSkip {
			return nil
		}
		req, release, err := o.bootstrapRequest(ui, st, cfg)
		if errors.Is(err, wizard.ErrBack) {
			continue
		}
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "\n-- db bootstrap --")
		err = o.bootstrap(ctx, req)
		release()
		if err == nil {
			return nil
		}
		_, _ = fmt.Fprintf(out, "bootstrap failed: %v\n", err)
		if again, aerr := askRetry(ui, "db bootstrap failed", err); aerr != nil || !again {
			return errors.Join(err, aerr)
		}
	}
}

// bootstrapRequest asks the administrator login (two roles) and, for a
// tls_mode below verify-full, the explicit confirmation of the flag
// --allow-insecure-admin-connection. release zeroes the password if
// bootstrap did not take it.
func (o *DatabaseOptions) bootstrapRequest(ui wizard.UI, st *setup.State, cfg *config.Bekup) (dbsetup.BootstrapRequest, func(), error) {
	req := dbsetup.BootstrapRequest{Config: cfg, Out: ui.Out(), Warn: ui.Out(), Logger: o.Logger}
	nothing := func() {}
	if mode := cfg.Database.TLSMode; mode != config.DBTLSVerifyFull {
		ok, err := ui.Confirm("Insecure administrator connection", fmt.Sprintf(
			"database.tls_mode is %s: the administrator account would connect without a verified\n"+
				"certificate (require) or without encryption (disable). A machine in the middle could\n"+
				"impersonate the server or alter what bootstrap sends. This is the flag\n"+
				"--allow-insecure-admin-connection. Use it anyway?", mode), false)
		if err != nil {
			return req, nothing, err
		}
		if !ok {
			return req, nothing, wizard.ErrBack
		}
		req.AllowInsecure = true
	}
	if st.DatabaseAuth == setup.DBAuthSingleAccount {
		return req, nothing, nil // database.user logs in with database.password_file
	}
	user, err := ui.Input("Administrator role", "A role that may create roles and databases (CREATEROLE + CREATEDB, or a managed\nadmin role such as rds_superuser).", "postgres", func(s string) error {
		if !config.ValidDBRoleName(s) {
			return errors.New("not a valid role name (and not a bekup_* or pg_* role)")
		}
		return nil
	})
	if err != nil {
		return req, nothing, err
	}
	adopt, err := ui.Confirm("Existing bekup roles", "Accept bekup_owner and bekup_app if they exist already (an earlier bootstrap)?\nThey are checked, never changed.", false)
	if err != nil {
		return req, nothing, err
	}
	pw, err := ui.Secret("Administrator password", "Used for this connection only; never stored or logged. Empty goes back.", nil)
	if err != nil {
		return req, nothing, err
	}
	unregister := logging.DefaultRegistry.Register(pw)
	req.AdminUser, req.AdoptExisting = user, adopt
	req.AdminPassword = func() ([]byte, error) {
		p := pw
		pw = nil
		if p == nil {
			return nil, errors.New("the administrator password was used already: enter it again")
		}
		return p, nil // Bootstrap zeroes it
	}
	return req, func() {
		crypto.Zero(pw)
		pw = nil
		unregister()
	}, nil
}

// migrateWithRetry runs migrate up; while the bundled postgres starts it
// retries for WaitFor, then (and on every failure of an external
// database) the operator decides.
func (o *DatabaseOptions) migrateWithRetry(ctx context.Context, ui wizard.UI, bundled bool, cfg *config.Bekup, out io.Writer) error {
	for {
		_, _ = fmt.Fprintln(out, "\n-- migrate up --")
		deadline := time.Now().Add(o.WaitFor)
		var err error
		for wait := 2 * time.Second; ; wait = min(wait*2, 15*time.Second) {
			var rs []db.MigrationResult
			rs, err = o.migrate(ctx, cfg, o.Logger, out)
			if err == nil {
				for _, r := range rs {
					_, _ = fmt.Fprintf(out, "applied: %05d %s\n", r.Version, r.Name)
				}
				if len(rs) == 0 {
					_, _ = fmt.Fprintln(out, "the database schema is up to date")
				}
				_, _ = fmt.Fprintln(out, "migrations done")
				return nil
			}
			if !bundled || time.Now().After(deadline) || ctx.Err() != nil {
				break
			}
			_, _ = fmt.Fprintf(out, "waiting for the database (is `docker compose up -d postgres` running?): %v\n", err)
			if serr := o.sleep(ctx, wait); serr != nil {
				return wizard.ErrAborted
			}
		}
		_, _ = fmt.Fprintf(out, "migrate up failed: %v\n", err)
		if again, aerr := askRetry(ui, "migrate up failed", err); aerr != nil || !again {
			return errors.Join(err, aerr)
		}
	}
}

// askRetry lets the operator retry a failed step (after fixing the cause)
// or stop.
func askRetry(ui wizard.UI, title string, cause error) (bool, error) {
	v, err := ui.Select(title, cause.Error()+"\n\nFix the cause (the report shows the details), then try again.", []wizard.Option{
		{Value: dbChoiceRetry, Label: "try again"},
		{Value: dbChoiceStop, Label: "stop"},
	}, dbChoiceRetry)
	if errors.Is(err, wizard.ErrBack) {
		return true, nil
	}
	return v == dbChoiceRetry, err
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
