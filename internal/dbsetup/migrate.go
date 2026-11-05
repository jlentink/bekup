package dbsetup

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/deployguard"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/notify"
)

// Guard is the setup version guard's input (D-212).
type Guard struct {
	// Version is this binary's version.
	Version string
	// Dev is dev mode.
	Dev bool
	// LookupEnv reads BEKUP_SETUP_VERSION and BEKUP_IMAGE_MODE.
	LookupEnv func(string) (string, bool)
	// Logger receives the refusal and the warnings.
	Logger *slog.Logger
}

// CheckDeployment applies the setup version guard (D-212) for the commands
// without the app (migrate up, db bootstrap, runner, the browser setup's
// database step), before they connect to anything: a version mismatch
// refuses to run, and so does (outside dev mode) a followed image without
// a setup version; an unset BEKUP_SETUP_VERSION only warns (outside dev
// mode). command names the command in the log and the error.
func CheckDeployment(ctx context.Context, g Guard, command string) error {
	d, err := deployguard.FromEnv(g.LookupEnv)
	if err != nil {
		return fmt.Errorf("refusing to run %s: %w", command, err)
	}
	warn, err := d.Check(g.Version, g.Dev)
	if err != nil {
		g.Logger.ErrorContext(ctx, "refusing to run: the deployment guard failed (D-212)",
			slog.String("command", command), slog.String("version", g.Version), slog.String("setup_version", d.SetupVersion),
			slog.Bool("image_following", d.ImageFollowing))
		return fmt.Errorf("refusing to run %s: %w", command, err)
	}
	if warn != "" {
		g.Logger.WarnContext(ctx, "setup version unknown", slog.String("command", command), slog.String("warning", warn))
	}
	if d.ImageFollowing && !g.Dev {
		g.Logger.WarnContext(ctx, "the bekup image follows a tag instead of a digest (BEKUP_IMAGE_MODE=following, D-212): whoever moves the tag decides what runs",
			slog.String("command", command))
	}
	return nil
}

// Auditor is the auditor of CLI and setup entries: every entry is alerted
// (D-069, notify.ObserveAudit).
func Auditor(q sqlc.DBTX) *audit.Auditor {
	return audit.New(q, audit.Options{Observer: notify.ObserveAudit})
}

// OpenMigrator connects as bekup_owner (or the single account acting as
// bekup_owner, D-207) with the configured TLS policy and runs the role
// checks. The login stays registered with the log redaction for the
// process (the CLI commands are short-lived).
func OpenMigrator(ctx context.Context, cfg *config.Bekup) (*db.Migrator, error) {
	return openMigrator(ctx, cfg, logging.DefaultRegistry)
}

// openMigrator is OpenMigrator registering the login with reg.
func openMigrator(ctx context.Context, cfg *config.Bekup, reg *logging.Registry) (*db.Migrator, error) {
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
	m, err := db.NewMigrator(connectCtx, creds, tlsOpts, reg)
	cancel()
	creds.Zero()
	return m, err
}

// MigrateUp runs the preflight and, when it passes, applies the pending
// migrations; the preflight report goes to report on a failure. It returns
// the applied migrations. The login is registered with the log redaction
// only while it runs (the browser setup retries it in one long-lived
// process, D-217 amendment 1).
func MigrateUp(ctx context.Context, cfg *config.Bekup, logger *slog.Logger, report io.Writer) (_ []db.MigrationResult, err error) {
	reg := logging.DefaultRegistry.Scope()
	defer func() { err = endScope(reg, err) }()
	m, err := openMigrator(ctx, cfg, reg)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := m.Close(context.WithoutCancel(ctx)); cerr != nil {
			logger.WarnContext(ctx, "closing migration connections failed", slog.Any("error", cerr))
		}
	}()
	rep, err := m.Check(ctx)
	f := rep.TLS
	logger.InfoContext(ctx, "app database connected",
		slog.String("config_mode", cfg.Database.DatabaseMode()),
		slog.String("tls_mode", f.Mode), slog.Bool("encrypted", f.Encrypted), slog.String("tls_version", f.Version),
		slog.Bool("chain_verified", f.ChainVerified), slog.Bool("host_verified", f.HostVerified),
		slog.Bool("channel_binding", f.ChannelBinding))
	for _, w := range rep.Warnings {
		logger.WarnContext(ctx, "migrate preflight warning", slog.String("warning", w))
	}
	if err != nil {
		if werr := WritePreflight(report, rep, err); werr != nil {
			return nil, werr
		}
		return nil, errors.New("preflight failed; nothing was migrated (details above; `bekup migrate check` repeats them)")
	}
	rs, err := m.Up(ctx)
	for _, r := range rs {
		logger.InfoContext(ctx, "migration applied", ResultAttrs(r)...)
	}
	if err == nil && len(rs) == 0 {
		logger.InfoContext(ctx, "database schema is up to date")
	}
	return rs, err
}

// ResultAttrs are the log attributes of an applied migration.
func ResultAttrs(r db.MigrationResult) []any {
	return []any{slog.Int64("version", r.Version), slog.String("migration", r.Name), slog.Duration("duration", r.Duration)}
}

// WritePreflight prints the preflight report and the failed checks (facts
// only: no host names, no credentials).
func WritePreflight(w io.Writer, r *db.PreflightReport, checkErr error) error {
	var b strings.Builder
	if r != nil {
		fmt.Fprintf(&b, "server:          PostgreSQL %s\n", r.ServerVersion)
		fmt.Fprintf(&b, "database:        %s\n", r.Database)
		fmt.Fprintf(&b, "citext:          %s\n", r.Citext)
		fmt.Fprintf(&b, "tls:             %s\n", TLSFactsLine(r.TLS))
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
		for _, e := range SplitErrors(checkErr) {
			fmt.Fprintf(&b, "FAILED: %s\n", e)
		}
	} else {
		b.WriteString("preflight OK\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// TLSFactsLine renders db.TLSFacts in one line.
func TLSFactsLine(f db.TLSFacts) string {
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

// SplitErrors flattens errors.Join trees into their messages.
func SplitErrors(err error) []string {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		out := make([]string, 0, len(j.Unwrap()))
		for _, e := range j.Unwrap() {
			out = append(out, SplitErrors(e)...)
		}
		return out
	}
	return []string{err.Error()}
}

// pgCannotConnectNow is SQLSTATE 57P03 ("the database system is starting
// up").
const pgCannotConnectNow = "57P03"

// Retryable reports a failure to reach the database server at all: the
// connection refused or timed out, the host name not (yet) resolvable, or
// a server that is still starting (57P03). Everything else (a refused
// login, a certificate, a role check, the preflight, a migration) is
// permanent: retrying it would only repeat the same failure.
func Retryable(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == pgCannotConnectNow
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return false
	}
	var netErr net.Error
	return errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded)
}

// Scrubbed returns err with its text scrubbed by the log redaction now
// (errors.Is/As still see the cause): format an error before the secrets
// it may mention are unregistered.
func Scrubbed(err error) error {
	if err == nil {
		return nil
	}
	return &scrubbedError{msg: logging.DefaultRegistry.Scrub(err.Error()), err: err}
}
