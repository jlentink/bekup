package setup

import (
	"bytes"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/hostname"
	"github.com/jlentink/bekup/internal/logging"
)

// The database dimension of a deployment (D-208): the bundled postgres
// service, or the operator's PostgreSQL in fields mode (D-207). setup runs
// without a network (the setup container has network_mode none), so it
// never connects to the database: it writes bekup.toml [database], the
// password files and the copied CA bundle, and prints the one command that
// creates the roles (`docker compose run --rm migrate db bootstrap ...`)
// or, for a DBA, writes the roles SQL while the passwords are in memory.

// DBAuth is how bekup's roles and database come to exist on an external
// PostgreSQL.
type DBAuth string

// External database authentication choices.
const (
	// DBAuthBootstrap: `bekup db bootstrap` with an administrator account
	// creates both login roles with the generated passwords (default).
	DBAuthBootstrap DBAuth = "bootstrap"
	// DBAuthExistingRoles: bekup_owner and bekup_app exist already; the
	// operator supplies their passwords.
	DBAuthExistingRoles DBAuth = "existing-roles"
	// DBAuthDBASQL: setup writes the roles SQL (SCRAM verifiers of the
	// generated passwords) for a DBA to run with psql.
	DBAuthDBASQL DBAuth = "dba-sql"
	// DBAuthSingleAccount: one account for migrations and the app; an
	// explicit weakening (database.single_account, D-207).
	DBAuthSingleAccount DBAuth = "single-account"
)

// DBAuths lists the choices in the order the wizard offers them.
var DBAuths = []DBAuth{DBAuthBootstrap, DBAuthExistingRoles, DBAuthDBASQL, DBAuthSingleAccount}

// ParseDBAuth validates a --db-auth value.
func ParseDBAuth(s string) (DBAuth, error) {
	for _, a := range DBAuths {
		if string(a) == s {
			return a, nil
		}
	}
	return "", fmt.Errorf("--db-auth must be bootstrap, existing-roles, dba-sql or single-account (got %q)", bounded(s))
}

// generated reports whether setup generates the role passwords.
func (a DBAuth) generated() bool { return a == DBAuthBootstrap || a == DBAuthDBASQL }

// Database are the database answers. The zero value is the bundled
// postgres service.
type Database struct {
	Mode DBMode
	// Host, Port and Name of the external PostgreSQL (fields mode).
	Host string
	Port int
	Name string
	// CAFile is the CA bundle (PEM) of the server certificate, a path
	// relative to the deployment directory; setup copies it to
	// <keys>/bekup_database_ca.crt (database.tls_ca_file).
	CAFile string
	// TLSServerName is the optional name the certificate is verified
	// against (database.tls_server_name).
	TLSServerName string
	Auth          DBAuth
	// User is the single account (single-account only).
	User string
	// AdminUser names the administrator for the printed bootstrap command
	// (bootstrap only; never stored).
	AdminUser string
	// PasswordFile and OwnerPasswordFile name files (relative to the
	// deployment directory) with the passwords of database.user and
	// bekup_owner instead of generated (or prompted) ones.
	PasswordFile      string
	OwnerPasswordFile string
}

// External reports an external database.
func (d *Database) External() bool { return d.Mode == DBExternal }

// Fixed names of the external database files below the keys directory.
const (
	dbPasswordName      = "bekup_database_password"
	dbOwnerPasswordName = "bekup_database_owner_password"
	dbCAName            = "bekup_database_ca.crt"
	dbRolesSQLName      = "bekup-roles.sql"
)

// Container paths of the external database files (the compose secrets).
const (
	dbPasswordTarget      = "/run/secrets/bekup_database_password"
	dbOwnerPasswordTarget = "/run/secrets/bekup_database_owner_password"
	dbCATarget            = "/run/secrets/bekup_db_ca.crt"
)

// groupDatabaseExternal ties the external database password files.
const groupDatabaseExternal = "database-external"

// maxDBCAFile bounds the CA bundle (a system bundle is about 200 KiB).
const maxDBCAFile = 1 << 20

// maxPasswordFile bounds a password file read by setup.
const maxPasswordFile = 4096

// checkSourcePath validates a relative input path (CA bundle, password
// file) inside the deployment directory.
func checkSourcePath(field, p string) error {
	if p == "" || path.IsAbs(p) || path.Clean(p) != p || p == ".." || len(p) > 255 || hasDotDot(p) {
		return fieldErr(field, "must be a clean path relative to the deployment directory (the setup container sees only that directory), e.g. db-ca.pem")
	}
	return nil
}

func hasDotDot(p string) bool {
	for _, seg := range splitSlash(p) {
		if seg == ".." || seg == "." {
			return true
		}
	}
	return false
}

func splitSlash(p string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(p); i++ {
		if i == len(p) || p[i] == '/' {
			out = append(out, p[start:i])
			start = i + 1
		}
	}
	return out
}

// validate checks the database answers and fills the defaults.
func (d *Database) validate() error {
	d.Mode = d.Mode.OrBundled()
	switch d.Mode {
	case DBBundled:
		if *d != (Database{Mode: DBBundled}) {
			return fieldErr("database", "--db-* flags apply to --database external only")
		}
		return nil
	case DBExternal:
	default:
		return fieldErr("database", "must be bundled or external")
	}
	if d.Host == "" {
		return fieldErr("db-host", "is required with --database external (your PostgreSQL's host name or IP address)")
	}
	if _, err := netip.ParseAddr(d.Host); err != nil && !hostRe.MatchString(d.Host) {
		return fieldErr("db-host", "%q is not a host name or an IPv4 or IPv6 address", bounded(d.Host))
	}
	if err := hostname.Validate(d.Host); err != nil {
		return fieldErr("db-host", "%q: %v", bounded(d.Host), err)
	}
	if d.Port == 0 {
		d.Port = config.DefaultDBPort
	}
	if d.Port < 1 || d.Port > 65535 {
		return fieldErr("db-port", "must be between 1 and 65535")
	}
	if d.Name == "" {
		d.Name = config.DefaultDBName
	}
	if !config.ValidDBName(d.Name) {
		return fieldErr("db-name", "must be 1 to 63 letters, digits, _, - or $ starting with a letter or _, and not postgres or a template database")
	}
	if err := checkSourcePath("db-ca-file", d.CAFile); err != nil {
		if d.CAFile == "" {
			return fieldErr("db-ca-file", "is required: the CA bundle (PEM) of your PostgreSQL's server certificate, inside the deployment directory (bekup verifies it with tls_mode verify-full)")
		}
		return err
	}
	if d.TLSServerName != "" {
		if !hostRe.MatchString(d.TLSServerName) {
			return fieldErr("db-tls-server-name", "%q is not a host name", bounded(d.TLSServerName))
		}
		if err := hostname.Validate(d.TLSServerName); err != nil {
			return fieldErr("db-tls-server-name", "%q: %v", bounded(d.TLSServerName), err)
		}
	}
	if d.Auth == "" {
		d.Auth = DBAuthBootstrap
	}
	if _, err := ParseDBAuth(string(d.Auth)); err != nil {
		return fieldErr("db-auth", "must be bootstrap, existing-roles, dba-sql or single-account")
	}
	switch {
	case d.Auth == DBAuthSingleAccount && !config.ValidDBRoleName(d.User):
		return fieldErr("db-user", "single-account needs the account's role name (letters, digits, _ . @ $ -; no bekup_ or pg_ prefix)")
	case d.Auth != DBAuthSingleAccount && d.User != "":
		return fieldErr("db-user", "applies to --db-auth single-account only (the role names bekup_app / bekup_owner are fixed)")
	}
	if d.AdminUser != "" {
		if d.Auth != DBAuthBootstrap {
			return fieldErr("db-admin-user", "applies to --db-auth bootstrap only")
		}
		if !config.ValidDBRoleName(d.AdminUser) {
			return fieldErr("db-admin-user", "is not a role name (letters, digits, _ . @ $ -; no bekup_ or pg_ prefix)")
		}
	}
	if d.PasswordFile != "" {
		if err := checkSourcePath("db-password-file", d.PasswordFile); err != nil {
			return err
		}
	}
	if d.OwnerPasswordFile != "" {
		if d.Auth == DBAuthSingleAccount {
			return fieldErr("db-owner-password-file", "does not apply to single-account (one account runs everything)")
		}
		if err := checkSourcePath("db-owner-password-file", d.OwnerPasswordFile); err != nil {
			return err
		}
		if d.OwnerPasswordFile == d.PasswordFile {
			return fieldErr("db-owner-password-file", "must differ from --db-password-file (separate passwords)")
		}
	}
	return nil
}

// readDBCA reads, parses and returns the CA bundle the operator named
// (inside the deployment directory, no symlinks). Only CERTIFICATE blocks
// are accepted, so a private key is never copied by mistake.
func readDBCA(root, rel string) ([]byte, error) {
	data, err := readInput(root, rel, maxDBCAFile)
	if err != nil {
		return nil, fmt.Errorf("--db-ca-file %s: %w", rel, err)
	}
	if _, err := certs.ParseCertificates(data); err != nil {
		return nil, fmt.Errorf("--db-ca-file %s: %w (a PEM file with only CERTIFICATE blocks: your PostgreSQL's CA bundle)", rel, err)
	}
	return data, nil
}

// readInput reads a regular file below root up to limit. It is opened
// through an os.Root on root with openat(O_NOFOLLOW) per component
// (openInput) and its type is checked on the descriptor, so a symlink
// swapped in between a check and the open cannot redirect the read.
func readInput(root, rel string, limit int64) (_ []byte, retErr error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { retErr = errors.Join(retErr, r.Close()) }()
	f, err := openInput(r, rel)
	if err != nil {
		return nil, err
	}
	defer func() { retErr = errors.Join(retErr, f.Close()) }()
	return readLimited(f, rel, limit)
}

// readPasswordInput reads a password file the operator named: one line,
// surrounding whitespace trimmed, 16 to 1024 printable ASCII characters
// (db.CheckRolePassword). The caller zeroes the result.
func readPasswordInput(root, field, rel string) ([]byte, error) {
	raw, err := readInput(root, rel, maxPasswordFile)
	if err != nil {
		return nil, fmt.Errorf("--%s %s: %w", field, rel, err)
	}
	defer crypto.Zero(raw)
	pw := bytes.Clone(bytes.TrimSpace(raw))
	if err := db.CheckRolePassword(pw); err != nil {
		crypto.Zero(pw)
		return nil, fmt.Errorf("--%s %s: %w", field, rel, err)
	}
	return pw, nil
}

// dbPasswords are the passwords of one external setup: app is
// database.user's (bekup_app or the single account), owner bekup_owner's
// (nil in single-account mode).
type dbPasswords struct {
	app, owner []byte
	unregister []func()
}

func (p *dbPasswords) destroy() {
	if p == nil {
		return
	}
	crypto.Zero(p.app)
	crypto.Zero(p.owner)
	for _, f := range p.unregister {
		f()
	}
}

func (p *dbPasswords) keep(b []byte) []byte {
	p.unregister = append(p.unregister, logging.DefaultRegistry.Register(b))
	return b
}

// resolveDBPasswords collects the external database passwords before
// anything is written: from the named files, from ask (prompt or wizard)
// or, for bootstrap / dba-sql, generated.
func resolveDBPasswords(root string, d *Database, ask func(role string) ([]byte, error)) (_ *dbPasswords, retErr error) {
	p := &dbPasswords{}
	defer func() {
		if retErr != nil {
			p.destroy()
		}
	}()
	one := func(field, file, role string) ([]byte, error) {
		switch {
		case file != "":
			return readPasswordInput(root, field, file)
		case d.Auth.generated():
			return randomPassword()
		case ask == nil:
			return nil, fmt.Errorf("--db-auth %s needs the password of %s: pass --%s (a file in the deployment directory) or run on a terminal", d.Auth, role, field)
		}
		pw, err := ask(role)
		if err != nil {
			return nil, err
		}
		if err := db.CheckRolePassword(pw); err != nil {
			crypto.Zero(pw)
			return nil, fmt.Errorf("the password of %s: %w", role, err)
		}
		return pw, nil
	}
	appRole := config.DBAppRole
	if d.Auth == DBAuthSingleAccount {
		appRole = d.User
	}
	app, err := one("db-password-file", d.PasswordFile, appRole)
	if err != nil {
		return nil, err
	}
	p.app = p.keep(app)
	if d.Auth != DBAuthSingleAccount {
		owner, err := one("db-owner-password-file", d.OwnerPasswordFile, config.DBOwnerRole)
		if err != nil {
			return nil, err
		}
		p.owner = p.keep(owner)
		if bytes.Equal(p.app, p.owner) {
			return nil, errors.New("bekup_owner and bekup_app need different passwords")
		}
	}
	return p, nil
}

// generateExternalDatabase writes the password files and, for dba-sql, the
// roles SQL (SCRAM verifiers, never the passwords) while the passwords are
// in memory. In single-account mode the owner file stays empty: compose
// mounts it into migrate, bekup never reads it.
func generateExternalDatabase(w *writer, l Layout, d *Database, p *dbPasswords) error {
	if err := w.create(l.keys(dbPasswordName), p.app, 0o400); err != nil {
		return err
	}
	if err := w.create(l.keys(dbOwnerPasswordName), p.owner, 0o400); err != nil {
		return err
	}
	if d.Auth != DBAuthDBASQL {
		return nil
	}
	ownerV, err := db.NewSCRAMVerifier(p.owner)
	if err != nil {
		return err
	}
	appV, err := db.NewSCRAMVerifier(p.app)
	if err != nil {
		return err
	}
	defer logging.DefaultRegistry.Register([]byte(ownerV))()
	defer logging.DefaultRegistry.Register([]byte(appV))()
	script, err := db.RolesSQLScript(db.NewRolesSQLPlan(d.Name, ownerV, appV, false, false))
	if err != nil {
		return err
	}
	// A roles SQL left from an earlier run is moved aside (deleted once the
	// run succeeded).
	if err := w.setAside(l.keys(dbRolesSQLName)); err != nil {
		return err
	}
	return w.create(l.keys(dbRolesSQLName), []byte(script), 0o600)
}

// renderDatabaseTOML is the [database] section of bekup.toml for an
// external PostgreSQL (fields mode, D-207).
func renderDatabaseTOML(d *Database) string {
	var b bytes.Buffer
	b.WriteString("\n[database]\n")
	b.WriteString("# Your PostgreSQL (fields mode, D-207, D-208). Passwords come only from the\n")
	b.WriteString("# Docker secret files setup wrote below the keys directory.\n")
	fmt.Fprintf(&b, "host = %s\n", quoteTOML(d.Host))
	fmt.Fprintf(&b, "port = %d\n", d.Port)
	fmt.Fprintf(&b, "name = %s\n", quoteTOML(d.Name))
	if d.Auth == DBAuthSingleAccount {
		b.WriteString("# EXPLICIT WEAKENING (D-207): one account runs migrations and the app, so a\n")
		b.WriteString("# compromised serve can alter the schema and the audit log. Warned at every\n")
		b.WriteString("# start, audited (security.insecure_db_roles) and alerted daily.\n")
		b.WriteString("single_account = true\n")
		fmt.Fprintf(&b, "user = %s\n", quoteTOML(d.User))
		fmt.Fprintf(&b, "password_file = %s\n", quoteTOML(dbPasswordTarget))
	} else {
		fmt.Fprintf(&b, "user = %s\n", quoteTOML(config.DBAppRole))
		fmt.Fprintf(&b, "password_file = %s\n", quoteTOML(dbPasswordTarget))
		b.WriteString("# Migrations only (mounted into migrate, never into serve).\n")
		fmt.Fprintf(&b, "owner_user = %s\n", quoteTOML(config.DBOwnerRole))
		fmt.Fprintf(&b, "owner_password_file = %s\n", quoteTOML(dbOwnerPasswordTarget))
	}
	b.WriteString("# Only the CA bundle you named is trusted (never the system roots).\n")
	fmt.Fprintf(&b, "tls_mode = %s\n", quoteTOML(config.DBTLSVerifyFull))
	fmt.Fprintf(&b, "tls_ca_file = %s\n", quoteTOML(dbCATarget))
	if d.TLSServerName != "" {
		fmt.Fprintf(&b, "tls_server_name = %s\n", quoteTOML(d.TLSServerName))
	}
	return b.String()
}

// dbAddress is host:port for reports.
func (d *Database) dbAddress() string {
	return d.Host + ":" + strconv.Itoa(d.Port)
}

// dbNextSteps returns the commands that prepare the external database,
// in order (the compose project directory is the deployment directory).
func dbNextSteps(d *Database, l Layout) []string {
	switch d.Auth {
	case DBAuthBootstrap:
		admin := d.AdminUser
		if admin == "" {
			admin = "<admin role>"
		}
		return []string{
			fmt.Sprintf("docker compose run --rm migrate db bootstrap --admin-user %s\n     (creates bekup_owner, bekup_app and the database %s with the generated passwords;\n     the admin password is asked on the terminal, used once and never stored)", admin, d.Name),
			"docker compose run --rm migrate migrate check",
		}
	case DBAuthSingleAccount:
		return []string{
			fmt.Sprintf("docker compose run --rm migrate db bootstrap\n     (as %s, which needs CREATEROLE and CREATEDB once: creates the NOLOGIN roles and the database %s)", d.User, d.Name),
			"docker compose run --rm migrate migrate check",
		}
	case DBAuthDBASQL:
		return []string{
			fmt.Sprintf("Hand %s to your DBA (it holds SCRAM verifiers, not the passwords;\n     keep it private) to run as a CREATEROLE + CREATEDB account:\n       psql -v ON_ERROR_STOP=1 --no-psqlrc -f %s \"host=%s port=%d dbname=postgres user=<admin> sslmode=verify-full sslrootcert=<ca.pem>\"\n     then delete it (setup check warns while it exists)", l.keys(dbRolesSQLName), dbRolesSQLName, d.Host, d.Port),
			"docker compose run --rm migrate migrate check",
		}
	default: // existing roles
		return []string{"docker compose run --rm migrate migrate check   (the roles exist already: this verifies them)"}
	}
}

// dbCAExpiry reports whether a CA bundle holds a currently valid
// certificate (provider bundles carry several, some may have expired).
func dbCAExpiry(infos []certs.ExpiryInfo, now time.Time) (anyValid bool) {
	for i := range infos {
		if !infos[i].Expired(now) && !infos[i].NotYetValid(now) {
			return true
		}
	}
	return false
}

// ValidateDBPassword checks a database role password (16 to 1024
// printable ASCII characters; the error never contains it).
func ValidateDBPassword(pw []byte) error { return db.CheckRolePassword(pw) }
