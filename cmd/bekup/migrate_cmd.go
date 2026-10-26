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
)

// errDownOneNeedsDev is returned by `migrate down-one` outside dev mode:
// migrations are forward-only in production (D-042).
var errDownOneNeedsDev = errors.New("migrate down-one is only allowed in dev mode (--dev); production migrations are forward-only")

func newMigrateCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Apply database migrations (as bekup_owner); default action: up",
		Long: "Apply the embedded database migrations as bekup_owner, using database.owner_url_file.\n" +
			"An advisory lock ensures only one instance migrates at a time; others wait.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error { return runMigrate(c, g, "up") },
	}
	for _, sub := range []struct{ name, short string }{
		{"up", "Apply all pending migrations (default)"},
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
	logLoaded(c, logger, res.Path, res.Warnings)
	if action == "down-one" && !res.Dev {
		return errDownOneNeedsDev
	}

	ctx := c.Context()
	url, err := res.Config.DatabaseOwnerURL()
	if err != nil {
		return fmt.Errorf("read database owner URL: %w", err)
	}
	connectCtx, cancel := context.WithTimeout(ctx, res.Config.Database.ConnectTimeout.D())
	m, err := db.NewMigrator(connectCtx, url, nil)
	cancel()
	config.Zero(url)
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
	case "status":
		st, err := m.Status(ctx)
		if err != nil {
			return err
		}
		return writeStatus(out, st)
	case "down-one":
		rs, err := m.DownOne(ctx)
		for _, r := range rs {
			logger.InfoContext(ctx, "migration rolled back", resultAttrs(r)...)
		}
		return err
	default:
		rs, err := m.Up(ctx)
		for _, r := range rs {
			logger.InfoContext(ctx, "migration applied", resultAttrs(r)...)
		}
		if err == nil && len(rs) == 0 {
			logger.InfoContext(ctx, "database schema is up to date")
		}
		return err
	}
}

func resultAttrs(r db.MigrationResult) []any {
	return []any{slog.Int64("version", r.Version), slog.String("migration", r.Name), slog.Duration("duration", r.Duration)}
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
