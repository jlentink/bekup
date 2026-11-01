package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/deployguard"
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
	m, err := openMigrator(ctx, res.Config)
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
		if werr := writePreflight(out, rep, err); werr != nil {
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
	case "down-one":
		rs, err := m.DownOne(ctx)
		for _, r := range rs {
			logger.InfoContext(ctx, "migration rolled back", resultAttrs(r)...)
		}
		return err
	default:
		rep, err := m.Check(ctx)
		f := rep.TLS
		logger.InfoContext(ctx, "app database connected",
			slog.String("config_mode", res.Config.Database.DatabaseMode()),
			slog.String("tls_mode", f.Mode), slog.Bool("encrypted", f.Encrypted), slog.String("tls_version", f.Version),
			slog.Bool("chain_verified", f.ChainVerified), slog.Bool("host_verified", f.HostVerified),
			slog.Bool("channel_binding", f.ChannelBinding))
		for _, w := range rep.Warnings {
			logger.WarnContext(ctx, "migrate preflight warning", slog.String("warning", w))
		}
		if err != nil {
			if werr := writePreflight(c.ErrOrStderr(), rep, err); werr != nil {
				return werr
			}
			return errors.New("preflight failed; nothing was migrated (details above; `bekup migrate check` repeats them)")
		}
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

// checkDeployment applies the setup version guard (D-212) for the commands
// without the app (migrate up, db bootstrap, runner), before they connect
// to anything: a version mismatch refuses to run, and so does (outside dev
// mode) a followed image without a setup version; an unset
// BEKUP_SETUP_VERSION only warns (outside dev mode). command names the
// command in the log and the error ("migrate", "db bootstrap", "runner").
func checkDeployment(c *cobra.Command, logger *slog.Logger, dev bool, command string) error {
	d, err := deployguard.FromEnv(lookupEnv)
	if err != nil {
		return fmt.Errorf("refusing to run %s: %w", command, err)
	}
	warn, err := d.Check(version, dev)
	if err != nil {
		logger.ErrorContext(c.Context(), "refusing to run: the deployment guard failed (D-212)",
			slog.String("command", command), slog.String("version", version), slog.String("setup_version", d.SetupVersion),
			slog.Bool("image_following", d.ImageFollowing))
		return fmt.Errorf("refusing to run %s: %w", command, err)
	}
	if warn != "" {
		logger.WarnContext(c.Context(), "setup version unknown", slog.String("command", command), slog.String("warning", warn))
	}
	if d.ImageFollowing && !dev {
		logger.WarnContext(c.Context(), "the bekup image follows a tag instead of a digest (BEKUP_IMAGE_MODE=following, D-212): whoever moves the tag decides what runs",
			slog.String("command", command))
	}
	return nil
}

// openMigrator connects as bekup_owner (or the single account acting as
// bekup_owner, D-207) with the configured TLS policy and runs the role
// checks.
func openMigrator(ctx context.Context, cfg *config.Bekup) (*db.Migrator, error) {
	creds, err := cfg.OwnerDBCredentials()
	if err != nil {
		return nil, fmt.Errorf("read database owner credentials: %w", err)
	}
	tlsOpts, err := db.LoadTLSOptions(cfg)
	if err != nil {
		creds.Zero()
		return nil, fmt.Errorf("database TLS: %w", err)
	}
	connectCtx, cancel := context.WithTimeout(ctx, cfg.Database.ConnectTimeout.D())
	m, err := db.NewMigrator(connectCtx, creds, tlsOpts, nil)
	cancel()
	creds.Zero()
	return m, err
}

// writePreflight prints the preflight report and the failed checks (facts
// only: no host names, no credentials).
func writePreflight(w io.Writer, r *db.PreflightReport, checkErr error) error {
	var b strings.Builder
	if r != nil {
		fmt.Fprintf(&b, "server:          PostgreSQL %s\n", r.ServerVersion)
		fmt.Fprintf(&b, "database:        %s\n", r.Database)
		fmt.Fprintf(&b, "citext:          %s\n", r.Citext)
		fmt.Fprintf(&b, "tls:             %s\n", tlsFactsLine(r.TLS))
		if r.SingleAccount {
			b.WriteString("roles:           SINGLE-ACCOUNT MODE (weakened: one account runs migrations and the app, D-207)\n")
			for _, f := range r.LoginFindings {
				fmt.Fprintf(&b, "  login role:    %s\n", f)
			}
		} else {
			b.WriteString("roles:           bekup_owner and bekup_app checked (no dangerous attributes or memberships)\n")
		}
		for _, wn := range r.Warnings {
			fmt.Fprintf(&b, "WARNING: %s\n", wn)
		}
	}
	if checkErr != nil {
		for _, e := range splitErrors(checkErr) {
			fmt.Fprintf(&b, "FAILED: %s\n", e)
		}
	} else {
		b.WriteString("preflight OK\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// tlsFactsLine renders db.TLSFacts in one line.
func tlsFactsLine(f db.TLSFacts) string {
	if !f.Encrypted {
		return fmt.Sprintf("NOT ENCRYPTED (tls_mode %s)", f.Mode)
	}
	s := fmt.Sprintf("%s, tls_mode %s, chain verified: %t, host verified: %t", f.Version, f.Mode, f.ChainVerified, f.HostVerified)
	if f.ServerNameOverride {
		s += " (against tls_server_name)"
	}
	if f.ChannelBinding {
		s += ", SCRAM channel binding required"
	}
	return s
}

// splitErrors flattens errors.Join trees into their messages.
func splitErrors(err error) []string {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		out := make([]string, 0, len(j.Unwrap()))
		for _, e := range j.Unwrap() {
			out = append(out, splitErrors(e)...)
		}
		return out
	}
	return []string{err.Error()}
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
