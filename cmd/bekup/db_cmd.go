package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/dbsetup"
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
	var database, appFile, ownerFile, externalFile, passwords string
	var createExt, externalRole bool
	cmd := &cobra.Command{
		Use:   "roles-sql",
		Short: "Print the SQL that creates bekup_owner, bekup_app and the database (for a DBA, run with psql)",
		Long: "Prints a psql script that creates the roles bekup_owner and bekup_app and bekup's database on a\n" +
			"PostgreSQL 15+ server (D-207). With --passwords verifier (default) the roles get SCRAM-SHA-256\n" +
			"verifiers of the passwords in --owner-password-file and --app-password-file: the passwords\n" +
			"themselves are never printed, but the verifiers are sensitive (keep the output private). With\n" +
			"--passwords prompt the script asks for them with psql's \\password instead. --external-password-file\n" +
			"(or --external-role with prompts) also creates bekup_external, the login role of the optional\n" +
			"external API (D-220; not with single-account mode). Output: stdout only.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if !config.ValidDBName(database) {
				return fmt.Errorf("--database: invalid database name %q (letters, digits, _, - and $; not postgres or a template)", database)
			}
			var ownerV, appV, externalV string
			switch passwords {
			case "verifier":
				if ownerFile == "" || appFile == "" {
					return errors.New("--passwords verifier needs --owner-password-file and --app-password-file")
				}
				if externalRole {
					return errors.New("--external-role is for --passwords prompt; with verifiers pass --external-password-file")
				}
				var err error
				if ownerV, err = verifierFromFile("--owner-password-file", ownerFile); err != nil {
					return err
				}
				if appV, err = verifierFromFile("--app-password-file", appFile); err != nil {
					return err
				}
				if externalFile != "" {
					if externalV, err = verifierFromFile("--external-password-file", externalFile); err != nil {
						return err
					}
					externalRole = true
				}
			case "prompt":
				if ownerFile != "" || appFile != "" || externalFile != "" {
					return errors.New("--passwords prompt reads no password files")
				}
			default:
				return fmt.Errorf("--passwords must be verifier or prompt (got %q)", passwords)
			}
			plan := db.NewRolesSQLPlan(database, ownerV, appV, passwords == "prompt", createExt)
			if externalRole {
				plan = plan.WithExternal(externalV)
			}
			script, err := db.RolesSQLScript(plan)
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
	f.StringVar(&externalFile, "external-password-file", "", "also create bekup_external (external API, D-220) with the password in this file")
	f.BoolVar(&externalRole, "external-role", false, "with --passwords prompt: also create bekup_external (external API, D-220)")
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
	externalPasswordFile                                   string
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
			"--allow-insecure-admin-connection is given. With --external-password-file it also creates\n" +
			"bekup_external, the login role of the optional external API (D-220; two roles only), logs in as it\n" +
			"and audits its privileges. Like migrate up it refuses to run on another version than\n" +
			"BEKUP_SETUP_VERSION (D-212).",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error { return runBootstrap(c, g, &bf) },
	}
	f := cmd.Flags()
	f.StringVar(&bf.adminUser, "admin-user", "", "administrator role to connect as (two roles: required)")
	f.StringVar(&bf.adminPasswordFile, "admin-password-file", "", "file with the administrator password (default: prompt on the terminal)")
	f.StringVar(&bf.adminDatabase, "admin-database", "postgres", "maintenance database the administrator connects to")
	f.StringVar(&bf.adminHost, "admin-host", "", "host of the administrator connection (default: database.host)")
	f.IntVar(&bf.adminPort, "admin-port", 0, "port of the administrator connection (default: database.port)")
	f.StringVar(&bf.externalPasswordFile, "external-password-file", "", "also create bekup_external (external API, D-220) with the password in this file")
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
	logLoaded(c, logger, res.Path, withoutListenWarning(res.Config, res.Warnings))
	// D-212: the migrate service runs bootstrap with the deployment facts;
	// never prepare a database with an image setup has not prepared the
	// deployment for (checked before anything connects).
	if err := checkDeployment(c, logger, res.Dev, "db bootstrap"); err != nil {
		return err
	}
	return dbsetup.Bootstrap(c.Context(), dbsetup.BootstrapRequest{
		Config: res.Config, AdminUser: bf.adminUser, AdminPasswordFileSet: bf.adminPasswordFile != "",
		AdminPassword: func() ([]byte, error) { return readAdminPassword(c, bf.adminPasswordFile) },
		AdminDatabase: bf.adminDatabase, AdminHost: bf.adminHost, AdminPort: bf.adminPort,
		ExternalPasswordFile: bf.externalPasswordFile, AdoptExisting: bf.adoptExisting,
		ResetPasswords: bf.resetPasswords, DryRun: bf.dryRun, AllowInsecure: bf.allowInsecure,
		Out: c.OutOrStdout(), Warn: c.ErrOrStderr(), Logger: logger,
	})
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
