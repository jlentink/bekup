package dbsetup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/logging"
)

// BootstrapRequest is one `db bootstrap` run (D-207).
type BootstrapRequest struct {
	// Config is the loaded bekup.toml (fields mode).
	Config *config.Bekup
	// AdminUser is the administrator role (two-roles mode; in
	// single-account mode empty or database.user).
	AdminUser string
	// AdminPassword reads the administrator password after the request
	// passed its checks (two-roles mode only; Bootstrap zeroes the result).
	AdminPassword func() ([]byte, error)
	// AdminPasswordFileSet: --admin-password-file was given (refused in
	// single-account mode).
	AdminPasswordFileSet bool
	// AdminDatabase is the maintenance database (default postgres);
	// AdminHost and AdminPort override database.host/port.
	AdminDatabase string
	AdminHost     string
	AdminPort     int
	// ExternalPasswordFile (--external-password-file, two roles only) also
	// creates the external API role bekup_external with the password in
	// this file (D-220).
	ExternalPasswordFile string
	// AdoptExisting, ResetPasswords, DryRun and AllowInsecure are the
	// flags of the same names.
	AdoptExisting, ResetPasswords, DryRun, AllowInsecure bool
	// Out receives the report, Warn the insecure-connection warning.
	Out, Warn io.Writer
	// Logger logs the connection close failures.
	Logger *slog.Logger
}

// Bootstrap creates bekup's roles and database with an administrator
// account (db.Bootstrap), then logs in as the configured roles, runs the
// role checks and the migrate preflight, and records cli.db.bootstrap when
// the installation already has its audit log. The caller ran
// CheckDeployment.
func Bootstrap(ctx context.Context, req BootstrapRequest) error {
	cfg := req.Config
	d := &cfg.Database
	if !d.FieldsMode() {
		return errors.New("bekup db bootstrap needs fields mode: set database.host, database.password_file and database.owner_password_file (docs/installation/external-database.md)")
	}
	if err := CheckAdminTLS(req.Warn, d.TLSMode, req.AllowInsecure); err != nil {
		return err
	}
	if req.AdminDatabase == "" {
		req.AdminDatabase = "postgres"
	}
	admin := config.DBCredentials{Host: d.Host, Port: uint16(d.Port), Database: req.AdminDatabase} //nolint:gosec // G115: validated port
	if req.AdminHost != "" {
		admin.Host = req.AdminHost
	}
	if req.AdminPort != 0 {
		if req.AdminPort < 1 || req.AdminPort > 65535 {
			return errors.New("--admin-port must be between 1 and 65535")
		}
		admin.Port = uint16(req.AdminPort)
	}
	if req.AdminDatabase != "postgres" && !config.ValidDBName(req.AdminDatabase) {
		return fmt.Errorf("--admin-database: invalid name %q", req.AdminDatabase)
	}
	if req.AdminDatabase == d.Name {
		return errors.New("--admin-database must be a maintenance database (e.g. postgres), not bekup's own database")
	}
	bopts := db.BootstrapOptions{
		Database: d.Name, SingleAccount: d.SingleAccount, AdoptExistingRoles: req.AdoptExisting,
		ResetPasswords: req.ResetPasswords, DryRun: req.DryRun, ConnectTimeout: d.ConnectTimeout.D(),
	}
	var err error
	if d.SingleAccount {
		if req.AdminUser != "" && req.AdminUser != d.User {
			return fmt.Errorf("single-account mode: --admin-user must be database.user (%q) or omitted", d.User)
		}
		if req.AdminPasswordFileSet || req.ResetPasswords {
			return errors.New("single-account mode uses database.user with database.password_file; --admin-password-file and --reset-passwords do not apply")
		}
		if req.ExternalPasswordFile != "" {
			return fmt.Errorf("--external-password-file: %w", db.ErrExternalSingleAccount)
		}
		admin.User = d.User
		if admin.Password, err = config.ReadSecretFile(d.PasswordFile); err != nil {
			return fmt.Errorf("database.password_file: %w", err)
		}
	} else {
		if req.AdminUser == "" {
			return errors.New("--admin-user is required (the administrator account that may create roles and databases)")
		}
		if req.ResetPasswords && !req.AdoptExisting {
			return errors.New("--reset-passwords changes existing roles: add --adopt-existing-roles after reviewing them")
		}
		if !config.ValidDBRoleName(req.AdminUser) {
			return fmt.Errorf("--admin-user: invalid role name %q (not a bekup_* or pg_* name)", req.AdminUser)
		}
		admin.User = req.AdminUser
		if req.AdminPassword == nil {
			return errors.New("no administrator password")
		}
		if admin.Password, err = req.AdminPassword(); err != nil {
			return err
		}
		if len(admin.Password) == 0 {
			return errors.New("empty administrator password")
		}
	}
	defer admin.Zero()
	logging.DefaultRegistry.Register(admin.Password)
	bopts.Admin = admin
	if bopts.TLS, err = db.LoadTLSOptions(cfg); err != nil {
		return fmt.Errorf("database TLS: %w", err)
	}
	if !d.SingleAccount {
		if bopts.OwnerPassword, err = readRolePassword("database.owner_password_file", d.OwnerPasswordFile); err != nil {
			return err
		}
		defer config.Zero(bopts.OwnerPassword)
		if bopts.AppPassword, err = readRolePassword("database.password_file", d.PasswordFile); err != nil {
			return err
		}
		defer config.Zero(bopts.AppPassword)
		if req.ExternalPasswordFile != "" {
			if bopts.ExternalPassword, err = readRolePassword("--external-password-file", req.ExternalPasswordFile); err != nil {
				return err
			}
			defer config.Zero(bopts.ExternalPassword)
		}
	}

	br, err := db.Bootstrap(ctx, bopts)
	if werr := WriteBootstrap(req.Out, br, req.DryRun); werr != nil {
		return werr
	}
	if err != nil {
		return fmt.Errorf("bootstrap failed: %w", err)
	}
	if req.DryRun {
		return nil
	}
	return verifyAfterBootstrap(ctx, &req, br)
}

// CheckAdminTLS refuses an admin connection weaker than verify-full unless
// the operator explicitly allows it (D-207): the administrator password
// would cross an unverified (require) or unencrypted (disable) connection.
// SCRAM channel binding still protects the password itself with require,
// but not the session's DDL against a machine in the middle.
func CheckAdminTLS(stderr io.Writer, mode string, allow bool) error {
	if mode == config.DBTLSVerifyFull {
		return nil
	}
	what := "without verifying the server certificate"
	if mode == config.DBTLSDisable {
		what = "WITHOUT ENCRYPTION"
	}
	if !allow {
		return fmt.Errorf("database.tls_mode = %q: the administrator account would connect %s; use tls_mode verify-full (--tls-mode verify-full --tls-ca-file <ca.pem>), "+
			"or pass --allow-insecure-admin-connection to accept the risk", mode, what)
	}
	_, err := fmt.Fprintf(stderr, "\n!!! WARNING: INSECURE ADMIN CONNECTION (tls_mode %s): the administrator account connects %s.\n"+
		"!!! A machine in the middle could impersonate the server or alter what bootstrap sends. Use verify-full whenever possible.\n\n", mode, what)
	return err
}

func readRolePassword(key, path string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("%s is not set: bootstrap sets the role passwords from the configured files", key)
	}
	pw, err := config.ReadSecretFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	logging.DefaultRegistry.Register(pw)
	return pw, nil
}

// WriteBootstrap prints the bootstrap result.
func WriteBootstrap(w io.Writer, r *db.BootstrapResult, dryRun bool) error {
	if r == nil {
		return nil
	}
	var b strings.Builder
	if r.ServerVersion != "" {
		fmt.Fprintf(&b, "server:  PostgreSQL %s (admin role %s)\n", r.ServerVersion, r.AdminRole)
		fmt.Fprintf(&b, "tls:     %s\n", TLSFactsLine(r.TLS))
	}
	verb := "done"
	if dryRun {
		verb = "would do"
	}
	if r.ManualRevoke != "" {
		fmt.Fprintf(&b, "\n!!! ACTION REQUIRED: manual REVOKE needed. The admin account still holds %s; run as an administrator:\n!!!   %s\n\n", db.OwnerRole, r.ManualRevoke)
	}
	for _, a := range r.Actions {
		fmt.Fprintf(&b, "%s: %s\n", verb, a)
	}
	if r.Citext != "" {
		fmt.Fprintf(&b, "citext:  %s\n", r.Citext)
	}
	for _, wn := range r.Warnings {
		fmt.Fprintf(&b, "WARNING: %s\n", wn)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// verifyAfterBootstrap logs in as the configured roles, runs the D-099 role
// checks and the migrate preflight, and records cli.db.bootstrap when the
// installation already has its audit log (D-207: before the first
// migration there is no audit log; the printed report is the evidence).
func verifyAfterBootstrap(ctx context.Context, req *BootstrapRequest, br *db.BootstrapResult) error {
	cfg, out, logger := req.Config, req.Out, req.Logger
	hint := "if the role's stored password differs from the configured file or is an MD5 hash, run bootstrap again with --adopt-existing-roles --reset-passwords"
	m, err := OpenMigrator(ctx, cfg)
	if err != nil {
		return fmt.Errorf("check as %s failed (%s): %w", db.OwnerRole, hint, err)
	}
	defer func() {
		if cerr := m.Close(context.WithoutCancel(ctx)); cerr != nil {
			logger.WarnContext(ctx, "closing database connection failed", slog.Any("error", cerr))
		}
	}()
	rep, checkErr := m.Check(ctx)
	if werr := WritePreflight(out, rep, checkErr); werr != nil {
		return werr
	}
	if checkErr != nil {
		return errors.New("bootstrap finished, but the migrate preflight failed (see above)")
	}

	pool, err := openAppPool(ctx, cfg, "bekup-bootstrap")
	if err != nil {
		return fmt.Errorf("%w (%s)", err, hint)
	}
	defer pool.Close()
	if _, err := fmt.Fprintf(out, "roles:   %s and %s log in and pass the role checks\n", db.OwnerRole, db.AppRole); err != nil {
		return err
	}
	schemaErr := db.CheckSchemaVersion(ctx, pool)
	if req.ExternalPasswordFile != "" {
		if err := checkExternalLogin(ctx, cfg, req.ExternalPasswordFile, schemaErr == nil); err != nil {
			return fmt.Errorf("%w (%s)", err, hint)
		}
		if _, err := fmt.Fprintf(out, "roles:   %s logs in and passes the role checks (bekup migrate up grants and audits its api functions)\n", db.ExternalRole); err != nil {
			return err
		}
	}
	if schemaErr != nil {
		if !errors.Is(schemaErr, db.ErrSchemaBehind) {
			return schemaErr
		}
		_, err := fmt.Fprintf(out, "next:    bekup migrate up (the audit log starts with the migrated schema; this report is the bootstrap's record)\n")
		return err
	}
	if !br.Changed {
		_, err := fmt.Fprintf(out, "nothing changed; the schema is current\n")
		return err
	}
	_, err = Auditor(pool).Record(ctx, audit.CLIEvent(audit.ActionCLIDBBootstrap, "database", cfg.Database.Name, BootstrapAuditDetails(cfg, br, req.ResetPasswords, req.AdoptExisting)))
	if err != nil {
		return fmt.Errorf("the changes were applied, but the audit entry could not be recorded: %w", err)
	}
	_, err = fmt.Fprintf(out, "recorded in the audit log (cli.db.bootstrap)\n")
	return err
}

// openAppPool logs in as bekup_app (or the single account acting as it)
// and runs the D-099 role checks.
func openAppPool(ctx context.Context, cfg *config.Bekup, appName string) (*pgxpool.Pool, error) {
	poolOpts, err := db.PoolOptionsFromConfig(cfg, appName)
	if err != nil {
		return nil, fmt.Errorf("database TLS: %w", err)
	}
	poolOpts.MaxConns = 2
	creds, err := cfg.AppDBCredentials()
	if err != nil {
		return nil, fmt.Errorf("read database credentials: %w", err)
	}
	login := ""
	if creds.SetRole != "" {
		login = creds.User
	}
	cctx, cancel := context.WithTimeout(ctx, cfg.Database.ConnectTimeout.D())
	pool, err := db.NewPool(cctx, creds, poolOpts)
	cancel()
	creds.Zero()
	if err != nil {
		return nil, fmt.Errorf("log in as %s failed: %w", db.AppRole, err)
	}
	if err := db.CheckAppRole(ctx, pool, login); err != nil {
		pool.Close()
		return nil, fmt.Errorf("role check of %s failed: %w", db.AppRole, err)
	}
	return pool, nil
}

// checkExternalLogin logs in as bekup_external (fields mode, the
// configured TLS policy) and runs db.CheckExternalRole on a migrated
// schema, db.CheckExternalLogin otherwise (D-220).
func checkExternalLogin(ctx context.Context, cfg *config.Bekup, pwFile string, migrated bool) error {
	poolOpts, err := db.PoolOptionsFromConfig(cfg, "bekup-bootstrap")
	if err != nil {
		return fmt.Errorf("database TLS: %w", err)
	}
	poolOpts.MaxConns, poolOpts.MinConns = 1, 0
	pw, err := readRolePassword("--external-password-file", pwFile)
	if err != nil {
		return err
	}
	d := &cfg.Database
	creds := config.DBCredentials{
		Host: d.Host, Port: uint16(d.Port), //nolint:gosec // G115: validated to 1..65535
		Database: d.Name, User: db.ExternalRole, Password: pw,
	}
	cctx, cancel := context.WithTimeout(ctx, d.ConnectTimeout.D())
	pool, err := db.NewPool(cctx, creds, poolOpts)
	cancel()
	creds.Zero()
	if err != nil {
		return fmt.Errorf("log in as %s failed: %w", db.ExternalRole, err)
	}
	defer pool.Close()
	check := db.CheckExternalLogin
	if migrated {
		check = db.CheckExternalRole
	}
	if err := check(ctx, pool); err != nil {
		return fmt.Errorf("role check of %s failed: %w", db.ExternalRole, err)
	}
	return nil
}

// Verify is the check after the migrations (the browser setup's last
// database step, D-217): the migrate preflight as bekup_owner, the login
// and role checks of bekup_app, and the schema version (current). It
// changes nothing.
func Verify(ctx context.Context, cfg *config.Bekup, out io.Writer, logger *slog.Logger) error {
	m, err := OpenMigrator(ctx, cfg)
	if err != nil {
		return fmt.Errorf("check as %s failed: %w", db.OwnerRole, err)
	}
	rep, checkErr := m.Check(ctx)
	if cerr := m.Close(context.WithoutCancel(ctx)); cerr != nil {
		logger.WarnContext(ctx, "closing database connection failed", slog.Any("error", cerr))
	}
	if werr := WritePreflight(out, rep, checkErr); werr != nil {
		return werr
	}
	if checkErr != nil {
		return errors.New("the migrate preflight failed (see above)")
	}
	pool, err := openAppPool(ctx, cfg, "bekup-setup-check")
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.CheckSchemaVersion(ctx, pool); err != nil {
		return fmt.Errorf("schema check: %w", err)
	}
	_, err = fmt.Fprintf(out, "roles:   %s and %s log in and pass the role checks\nschema:  current\n", db.OwnerRole, db.AppRole)
	return err
}

// BootstrapAuditDetails are the details of cli.db.bootstrap. The keys must
// not look like secrets to the redaction (logging.IsSecretKey), or the
// audit entry would lose them.
func BootstrapAuditDetails(cfg *config.Bekup, br *db.BootstrapResult, rotated, adopted bool) audit.Details {
	return audit.Details{
		"actions":        br.Actions,
		"admin_role":     br.AdminRole,
		"single_account": cfg.Database.SingleAccount,
		"rotated_logins": rotated,
		"adopted_roles":  adopted,
	}
}
