package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/dbsetup"
)

// errDownOneNeedsDev is returned by `migrate down-one` outside dev mode:
// migrations are forward-only in production (D-042).
var errDownOneNeedsDev = errors.New("migrate down-one is only allowed in dev mode (--dev); production migrations are forward-only")

func newMigrateCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Apply database migrations (as bekup_owner); default action: up",
		Long: "Apply the embedded database migrations as bekup_owner, using database.owner_url_file (URL mode)\n" +
			"or database.owner_user/owner_password_file (fields mode). An advisory lock ensures only one\n" +
			"instance migrates at a time; others wait. `up` first runs the same preflight as `check`.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error { return runMigrate(c, g, "up") },
	}
	for _, sub := range []struct{ name, short string }{
		{"up", "Run the preflight, then apply all pending migrations (default)"},
		{"check", "Preflight: server version, database owner, citext, role checks, TLS facts (changes nothing)"},
		{"status", "Show applied and pending migrations"},
		{"down-one", "Roll back the most recent migration (dev mode only)"},
	} {
		action := sub.name
		cmd.AddCommand(&cobra.Command{
			Use:   sub.name,
			Short: sub.short,
			Args:  cobra.NoArgs,
			RunE:  func(c *cobra.Command, _ []string) error { return runMigrate(c, g, action) },
		})
	}
	return cmd
}

func runMigrate(c *cobra.Command, g *globalFlags, action string) error {
	res, err := config.LoadBekup(g.options(c))
	if err != nil {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}
	logger, err := newLogger(c.ErrOrStderr(), res)
	if err != nil {
		return err
	}
	logLoaded(c, logger, res.Path, withoutListenWarning(res.Config, res.Warnings))
	if action == "down-one" && !res.Dev {
		return errDownOneNeedsDev
	}
	if action == "up" {
		// D-212: never run the migrations of an image setup has not
		// prepared the deployment for (a moved tag, a replaced compose
		// file without `setup upgrade`), and never those of an older one.
		if err := checkDeployment(c, logger, res.Dev, "migrate"); err != nil {
			return err
		}
	}

	ctx := c.Context()
	if action == "up" {
		_, err := dbsetup.MigrateUp(ctx, res.Config, logger, c.ErrOrStderr())
		return err
	}
	m, err := dbsetup.OpenMigrator(ctx, res.Config)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := m.Close(context.WithoutCancel(ctx)); cerr != nil {
			logger.WarnContext(ctx, "closing migration connections failed", slog.Any("error", cerr))
		}
	}()

	out := c.OutOrStdout()
	switch action {
	case "check":
		rep, err := m.Check(ctx)
		if werr := dbsetup.WritePreflight(out, rep, err); werr != nil {
			return werr
		}
		if err != nil {
			return errors.New("preflight failed")
		}
		return nil
	case "status":
		st, err := m.Status(ctx)
		if err != nil {
			return err
		}
		return writeStatus(out, st)
	default: // down-one
		rs, err := m.DownOne(ctx)
		for _, r := range rs {
			logger.InfoContext(ctx, "migration rolled back", dbsetup.ResultAttrs(r)...)
		}
		return err
	}
}

// checkDeployment applies the setup version guard (D-212) for the commands
// without the app (migrate up, db bootstrap, runner), before they connect
// to anything: a version mismatch refuses to run, and so does (outside dev
// mode) a followed image without a setup version; an unset
// BEKUP_SETUP_VERSION only warns (outside dev mode). command names the
// command in the log and the error ("migrate", "db bootstrap", "runner").
func checkDeployment(c *cobra.Command, logger *slog.Logger, dev bool, command string) error {
	return dbsetup.CheckDeployment(c.Context(), dbsetup.Guard{Version: version, Dev: dev, LookupEnv: lookupEnv, Logger: logger}, command)
}

func writeStatus(w io.Writer, st []db.MigrationStatus) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "VERSION\tSTATE\tAPPLIED AT\tMIGRATION"); err != nil {
		return err
	}
	for _, s := range st {
		state, at := "pending", "-"
		if s.Applied {
			state, at = "applied", s.AppliedAt.UTC().Format(time.RFC3339)
		}
		if _, err := fmt.Fprintf(tw, "%05d\t%s\t%s\t%s\n", s.Version, state, at, s.Name); err != nil {
			return err
		}
	}
	return tw.Flush()
}
