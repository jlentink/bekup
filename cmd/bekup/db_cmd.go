package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/logging"
)

// External app database setup (D-207): `bekup db roles-sql` prints the
// role and database DDL for a DBA, `bekup db bootstrap` runs it with an
// administrator account.

func newDBCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Set up bekup's roles and database on an external PostgreSQL (bootstrap, roles-sql)",
		Long: "Prepare an external PostgreSQL 15-17 for bekup (D-207; docs/installation/external-database.md).\n" +
			"`bootstrap` creates the two least-privilege roles and the database with an administrator\n" +
			"account; `roles-sql` prints the same statements for a DBA. Then run `bekup migrate up`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newDBRolesSQLCmd(), newDBBootstrapCmd(g))
	return cmd
}

func newDBRolesSQLCmd() *cobra.Command {
	var database, appFile, ownerFile, passwords string
	var createExt bool
	cmd := &cobra.Command{
		Use:   "roles-sql",
		Short: "Print the SQL that creates bekup_owner, bekup_app and the database (for a DBA, run with psql)",
		Long: "Prints a psql script that creates the roles bekup_owner and bekup_app and bekup's database on a\n" +
			"PostgreSQL 15+ server (D-207). With --passwords verifier (default) the roles get SCRAM-SHA-256\n" +
			"verifiers of the passwords in --owner-password-file and --app-password-file: the passwords\n" +
			"themselves are never printed, but the verifiers are sensitive (keep the output private). With\n" +
			"--passwords prompt the script asks for them with psql's \\password instead. Output: stdout only.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if !config.ValidDBName(database) {
				return fmt.Errorf("--database: invalid database name %q (letters, digits, _, - and $; not postgres or a template)", database)
			}
			var ownerV, appV string
			switch passwords {
			case "verifier":
				if ownerFile == "" || appFile == "" {
					return errors.New("--passwords verifier needs --owner-password-file and --app-password-file")
				}
				var err error
				if ownerV, err = verifierFromFile("--owner-password-file", ownerFile); err != nil {
					return err
				}
				if appV, err = verifierFromFile("--app-password-file", appFile); err != nil {
					return err
				}
			case "prompt":
				if ownerFile != "" || appFile != "" {
					return errors.New("--passwords prompt reads no password files")
				}
			default:
				return fmt.Errorf("--passwords must be verifier or prompt (got %q)", passwords)
			}
			script, err := db.RolesSQLScript(db.NewRolesSQLPlan(database, ownerV, appV, passwords == "prompt", createExt))
			if err != nil {
				return err
			}
			_, err = io.WriteString(c.OutOrStdout(), script)
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&database, "database", config.DefaultDBName, "name of bekup's database (database.name)")
	f.StringVar(&ownerFile, "owner-password-file", "", "file with the bekup_owner password (database.owner_password_file)")
	f.StringVar(&appFile, "app-password-file", "", "file with the bekup_app password (database.password_file)")
	f.StringVar(&passwords, "passwords", "verifier", "verifier: embed SCRAM verifiers of the password files; prompt: psql asks for the passwords")
	f.BoolVar(&createExt, "create-extensions", false, "also create the citext extension as the admin (servers where it is not trusted)")
	return cmd
}

// verifierFromFile reads a role password with the secret-file rules and
// returns its SCRAM verifier (registered for log redaction).
func verifierFromFile(flag, path string) (string, error) {
	pw, err := config.ReadSecretFile(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", flag, err)
	}
	defer config.Zero(pw)
	logging.DefaultRegistry.Register(pw)
	v, err := db.NewSCRAMVerifier(pw)
	if err != nil {
		return "", fmt.Errorf("%s: %w", flag, err)
	}
	logging.DefaultRegistry.Register([]byte(v))
	return v, nil
}

type bootstrapFlags struct {
	adminUser, adminPasswordFile, adminDatabase, adminHost string
	adminPort                                              int
	tlsMode, tlsCAFile, tlsServerName                      string
	resetPasswords, dryRun, adoptExisting, allowInsecure   bool
}

func newDBBootstrapCmd(g *globalFlags) *cobra.Command {
	var bf bootstrapFlags
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Create bekup's roles and database with an administrator account (idempotent)",
		Long: "Connects to the external PostgreSQL of bekup.toml (fields mode: database.host, ...) with an\n" +
			"administrator account and creates the login roles bekup_owner and bekup_app with SCRAM verifiers\n" +
			"of database.owner_password_file and database.password_file, the database owned by bekup_owner\n" +
			"(PUBLIC revoked, CONNECT for bekup_app) and, if bekup_owner cannot, the citext extension (D-207).\n" +
			"The admin password comes from --admin-password-file or a terminal prompt; it is used for this\n" +
			"connection only, never stored or logged. Works with CREATEROLE + CREATEDB accounts and managed\n" +
			"admin roles (rds_superuser, cloudsqlsuperuser, azure_pg_admin). Existing roles are checked, not\n" +
			"changed (--reset-passwords sets their passwords); existing roles are only accepted with\n" +
			"--adopt-existing-roles, and other members or ADMIN OPTION holders of them are refused. Then it\n" +
			"runs the role checks and the migrate preflight. With database.single_account = true it uses the\n" +
			"single account itself (weakened). The admin connection needs tls_mode verify-full unless\n" +
			"--allow-insecure-admin-connection is given.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error { return runBootstrap(c, g, &bf) },
	}
	f := cmd.Flags()
	f.StringVar(&bf.adminUser, "admin-user", "", "administrator role to connect as (two roles: required)")
	f.StringVar(&bf.adminPasswordFile, "admin-password-file", "", "file with the administrator password (default: prompt on the terminal)")
	f.StringVar(&bf.adminDatabase, "admin-database", "postgres", "maintenance database the administrator connects to")
	f.StringVar(&bf.adminHost, "admin-host", "", "host of the administrator connection (default: database.host)")
	f.IntVar(&bf.adminPort, "admin-port", 0, "port of the administrator connection (default: database.port)")
	f.StringVar(&bf.tlsMode, "tls-mode", "", "override database.tls_mode for this command (and the checks after it)")
	f.StringVar(&bf.tlsCAFile, "tls-ca-file", "", "override database.tls_ca_file")
	f.StringVar(&bf.tlsServerName, "tls-server-name", "", "override database.tls_server_name")
	f.BoolVar(&bf.resetPasswords, "reset-passwords", false, "set the passwords of existing bekup_owner/bekup_app roles to the configured ones (needs --adopt-existing-roles)")
	f.BoolVar(&bf.adoptExisting, "adopt-existing-roles", false, "accept bekup_owner/bekup_app roles that already exist (an earlier bootstrap, a DBA); they are checked, never changed silently")
	f.BoolVar(&bf.allowInsecure, "allow-insecure-admin-connection", false, "allow the admin connection with tls_mode require or disable (the admin password then crosses an unverified or unencrypted connection)")
	f.BoolVar(&bf.dryRun, "dry-run", false, "only show what would be done")
	return cmd
}

func runBootstrap(c *cobra.Command, g *globalFlags, bf *bootstrapFlags) error {
	opts := g.options(c)
	if opts.Flags == nil {
		opts.Flags = map[string]string{}
	}
	for flag, key := range map[string]string{"tls-mode": "database.tls_mode", "tls-ca-file": "database.tls_ca_file", "tls-server-name": "database.tls_server_name"} {
		if c.Flags().Changed(flag) {
			v, err := c.Flags().GetString(flag)
			if err != nil {
				return err
			}
			opts.Flags[key] = v
		}
	}
	res, err := config.LoadBekup(opts)
	if err != nil {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}
	logger, err := newLogger(c.ErrOrStderr(), res)
	if err != nil {
		return err
	}
	logLoaded(c, logger, res.Path, res.Warnings)
	cfg := res.Config
	d := &cfg.Database
	if !d.FieldsMode() {
		return errors.New("bekup db bootstrap needs fields mode: set database.host, database.password_file and database.owner_password_file (docs/installation/external-database.md)")
	}
	if err := checkAdminTLS(c.ErrOrStderr(), d.TLSMode, bf.allowInsecure); err != nil {
		return err
	}
	admin := config.DBCredentials{Host: d.Host, Port: uint16(d.Port), Database: bf.adminDatabase} //nolint:gosec // G115: validated port
	if bf.adminHost != "" {
		admin.Host = bf.adminHost
	}
	if bf.adminPort != 0 {
		if bf.adminPort < 1 || bf.adminPort > 65535 {
			return errors.New("--admin-port must be between 1 and 65535")
		}
		admin.Port = uint16(bf.adminPort)
	}
	if bf.adminDatabase != "postgres" && !config.ValidDBName(bf.adminDatabase) {
		return fmt.Errorf("--admin-database: invalid name %q", bf.adminDatabase)
	}
	if bf.adminDatabase == d.Name {
		return errors.New("--admin-database must be a maintenance database (e.g. postgres), not bekup's own database")
	}
	bopts := db.BootstrapOptions{
		Database: d.Name, SingleAccount: d.SingleAccount, AdoptExistingRoles: bf.adoptExisting,
		ResetPasswords: bf.resetPasswords, DryRun: bf.dryRun, ConnectTimeout: d.ConnectTimeout.D(),
	}
	if d.SingleAccount {
		if bf.adminUser != "" && bf.adminUser != d.User {
			return fmt.Errorf("single-account mode: --admin-user must be database.user (%q) or omitted", d.User)
		}
		if bf.adminPasswordFile != "" || bf.resetPasswords {
			return errors.New("single-account mode uses database.user with database.password_file; --admin-password-file and --reset-passwords do not apply")
		}
		admin.User = d.User
		if admin.Password, err = config.ReadSecretFile(d.PasswordFile); err != nil {
			return fmt.Errorf("database.password_file: %w", err)
		}
	} else {
		if bf.adminUser == "" {
			return errors.New("--admin-user is required (the administrator account that may create roles and databases)")
		}
		if bf.resetPasswords && !bf.adoptExisting {
			return errors.New("--reset-passwords changes existing roles: add --adopt-existing-roles after reviewing them")
		}
		if !config.ValidDBRoleName(bf.adminUser) {
			return fmt.Errorf("--admin-user: invalid role name %q (not a bekup_* or pg_* name)", bf.adminUser)
		}
		admin.User = bf.adminUser
		if admin.Password, err = readAdminPassword(c, bf.adminPasswordFile); err != nil {
			return err
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
	}

	ctx := c.Context()
	out := c.OutOrStdout()
	br, err := db.Bootstrap(ctx, bopts)
	if werr := writeBootstrap(out, br, bf.dryRun); werr != nil {
		return werr
	}
	if err != nil {
		return fmt.Errorf("bootstrap failed: %w", err)
	}
	if bf.dryRun {
		return nil
	}
	return verifyAfterBootstrap(c, cfg, logger, br, bf)
}

// checkAdminTLS refuses an admin connection weaker than verify-full unless
// the operator explicitly allows it (D-207): the administrator password
// would cross an unverified (require) or unencrypted (disable) connection.
// SCRAM channel binding still protects the password itself with require,
// but not the session's DDL against a machine in the middle.
func checkAdminTLS(stderr io.Writer, mode string, allow bool) error {
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

// readAdminPassword reads the administrator password from the file (secret
// file rules) or from a terminal prompt without echo.
func readAdminPassword(c *cobra.Command, file string) ([]byte, error) {
	if file != "" {
		pw, err := config.ReadSecretFile(file)
		if err != nil {
			return nil, fmt.Errorf("--admin-password-file: %w", err)
		}
		return pw, nil
	}
	pw, ok, err := readPassword("Administrator password: ", c.ErrOrStderr())
	switch {
	case !ok:
		return nil, errors.New("no terminal for the password prompt: pass --admin-password-file")
	case err != nil:
		return nil, fmt.Errorf("read password: %w", err)
	case len(pw) == 0:
		return nil, errors.New("empty administrator password")
	}
	return pw, nil
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

func writeBootstrap(w io.Writer, r *db.BootstrapResult, dryRun bool) error {
	if r == nil {
		return nil
	}
	var b strings.Builder
	if r.ServerVersion != "" {
		fmt.Fprintf(&b, "server:  PostgreSQL %s (admin role %s)\n", r.ServerVersion, r.AdminRole)
		fmt.Fprintf(&b, "tls:     %s\n", tlsFactsLine(r.TLS))
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
func verifyAfterBootstrap(c *cobra.Command, cfg *config.Bekup, logger *slog.Logger, br *db.BootstrapResult, bf *bootstrapFlags) error {
	ctx := c.Context()
	out := c.OutOrStdout()
	hint := "if the role's stored password differs from the configured file or is an MD5 hash, run bootstrap again with --adopt-existing-roles --reset-passwords"
	m, err := openMigrator(ctx, cfg)
	if err != nil {
		return fmt.Errorf("check as %s failed (%s): %w", db.OwnerRole, hint, err)
	}
	defer func() {
		if cerr := m.Close(context.WithoutCancel(ctx)); cerr != nil {
			logger.WarnContext(ctx, "closing database connection failed", slog.Any("error", cerr))
		}
	}()
	rep, checkErr := m.Check(ctx)
	if werr := writePreflight(out, rep, checkErr); werr != nil {
		return werr
	}
	if checkErr != nil {
		return errors.New("bootstrap finished, but the migrate preflight failed (see above)")
	}

	poolOpts, err := db.PoolOptionsFromConfig(cfg, "bekup-bootstrap")
	if err != nil {
		return fmt.Errorf("database TLS: %w", err)
	}
	poolOpts.MaxConns = 2
	creds, err := cfg.AppDBCredentials()
	if err != nil {
		return fmt.Errorf("read database credentials: %w", err)
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
		return fmt.Errorf("log in as %s failed (%s): %w", db.AppRole, hint, err)
	}
	defer pool.Close()
	if err := db.CheckAppRole(ctx, pool, login); err != nil {
		return fmt.Errorf("role check of %s failed: %w", db.AppRole, err)
	}
	if err := printf(c, "roles:   %s and %s log in and pass the role checks\n", db.OwnerRole, db.AppRole); err != nil {
		return err
	}
	schemaErr := db.CheckSchemaVersion(ctx, pool)
	if schemaErr != nil {
		if !errors.Is(schemaErr, db.ErrSchemaBehind) {
			return schemaErr
		}
		return printf(c, "next:    bekup migrate up (the audit log starts with the migrated schema; this report is the bootstrap's record)\n")
	}
	if !br.Changed {
		return printf(c, "nothing changed; the schema is current\n")
	}
	_, err = newCLIAuditor(pool).Record(ctx, audit.CLIEvent(audit.ActionCLIDBBootstrap, "database", cfg.Database.Name, bootstrapAuditDetails(cfg, br, bf)))
	if err != nil {
		return fmt.Errorf("the changes were applied, but the audit entry could not be recorded: %w", err)
	}
	return printf(c, "recorded in the audit log (cli.db.bootstrap)\n")
}

// bootstrapAuditDetails are the details of cli.db.bootstrap. The keys must
// not look like secrets to the redaction (logging.IsSecretKey), or the
// audit entry would lose them.
func bootstrapAuditDetails(cfg *config.Bekup, br *db.BootstrapResult, bf *bootstrapFlags) audit.Details {
	return audit.Details{
		"actions":        br.Actions,
		"admin_role":     br.AdminRole,
		"single_account": cfg.Database.SingleAccount,
		"rotated_logins": bf.resetPasswords,
		"adopted_roles":  bf.adoptExisting,
	}
}
