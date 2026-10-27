package engine

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
)

// pgConnectTimeoutSeconds is libpq's connect_timeout for every tool.
const pgConnectTimeoutSeconds = "10"

type postgresEngine struct{}

var pgSystemDatabases = []string{
	"template0", "template1",
	// Managed-service administration databases.
	"rdsadmin", "cloudsqladmin", "azure_maintenance", "azure_sys",
}

func (postgresEngine) Kind() Kind { return KindPostgres }

func (postgresEngine) SystemDatabases() []string { return slices.Clone(pgSystemDatabases) }

func (postgresEngine) IsSystemDatabase(name string) bool {
	return slices.Contains(pgSystemDatabases, name)
}

func (postgresEngine) ListDatabasesQuery() string {
	return "SELECT datname FROM pg_catalog.pg_database WHERE datallowconn AND NOT datistemplate ORDER BY datname"
}

func (postgresEngine) VersionQuery() string { return "SHOW server_version_num" }

func (postgresEngine) ParseServerVersion(s string) (Version, error) {
	return ParsePGServerVersionNum(s)
}

// pgQuoteValue quotes a libpq conninfo value: single quotes with "\" and
// "'" backslash-escaped. Values are validated before, so this is defense in
// depth.
func pgQuoteValue(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`)
	return "'" + r.Replace(v) + "'"
}

// pgConninfo builds the keyword/value connection string passed as
// --dbname. The database is never a raw name or URI (D-033).
func pgConninfo(t Target, db string) string {
	kv := [][2]string{
		{"dbname", db},
		{"host", t.Host},
		{"port", formatPort(t.Port)},
		{"user", t.User},
		{"passfile", string(FilePGPass)},
	}
	switch t.TLS {
	case TLSPinned:
		kv = append(kv,
			[2]string{"sslmode", "verify-full"},
			[2]string{"sslrootcert", string(FileCA)},
			[2]string{"ssl_min_protocol_version", "TLSv1.2"})
	case TLSUnverified:
		// No sslrootcert: with one, libpq would silently verify the CA.
		kv = append(kv,
			[2]string{"sslmode", "require"},
			[2]string{"ssl_min_protocol_version", "TLSv1.2"})
	default:
		kv = append(kv, [2]string{"sslmode", "disable"})
	}
	if t.ClientCert {
		kv = append(kv,
			[2]string{"sslcert", string(FileClientCert)},
			[2]string{"sslkey", string(FileClientKey)})
	}
	kv = append(kv,
		[2]string{"gssencmode", "disable"},
		[2]string{"application_name", "bekup"},
		[2]string{"connect_timeout", pgConnectTimeoutSeconds})
	parts := make([]string, 0, len(kv))
	for _, p := range kv {
		parts = append(parts, p[0]+"="+pgQuoteValue(p[1]))
	}
	return strings.Join(parts, " ")
}

// pgFiles are the preamble files of a connecting PostgreSQL tool.
func pgFiles(t Target) []File {
	return append([]File{FilePGPass}, tlsFiles(t)...)
}

// pgPrepare validates the common inputs of a connecting builder.
func pgPrepare(c Entry, t Target) error {
	if err := checkEntry(c, KindPostgres); err != nil {
		return err
	}
	return t.Validate()
}

// pgUserDB validates a database the operation may write to: valid, not a
// system database and not the maintenance database the tool connects to.
func (e postgresEngine) pgUserDB(t Target, db string) error {
	if err := ValidateDBName(db); err != nil {
		return err
	}
	if e.IsSystemDatabase(db) || db == t.maintenanceDB() {
		return fmt.Errorf("%w: %q", ErrSystemDatabase, db)
	}
	return nil
}

func (postgresEngine) Dump(c Entry, t Target, db string) (Command, error) {
	if err := pgPrepare(c, t); err != nil {
		return Command{}, err
	}
	if err := ValidateDBName(db); err != nil {
		return Command{}, err
	}
	return Command{
		Image: c.image,
		Argv: []string{
			c.binaries.Dump,
			"--format=custom",
			"--compress=0",
			"--no-password",
			"--dbname=" + pgConninfo(t, db),
		},
		Files: pgFiles(t),
	}, nil
}

func (postgresEngine) DumpGlobals(c Entry, t Target) (primary, fallback Command, err error) {
	if err := pgPrepare(c, t); err != nil {
		return Command{}, Command{}, err
	}
	maint := t.maintenanceDB()
	argv := []string{
		c.binaries.DumpGlobals,
		"--globals-only",
		"--no-password",
		"--database=" + maint,
		"--dbname=" + pgConninfo(t, maint),
	}
	primary = Command{Image: c.image, Argv: argv, Files: pgFiles(t)}
	fallback = Command{
		Image: c.image,
		Argv:  append(slices.Clone(argv), "--no-role-passwords"),
		Files: pgFiles(t),
	}
	return primary, fallback, nil
}

// VerifyDump reads the whole archive from stdin and renders it into a
// script that is discarded (D-134): unlike --list, which stops after the
// table of contents, this reads every data block, so a truncated or
// corrupted archive fails. No connection is made.
func (postgresEngine) VerifyDump(c Entry) (Command, error) {
	if err := checkEntry(c, KindPostgres); err != nil {
		return Command{}, err
	}
	return Command{
		Image: c.image,
		Argv:  []string{c.binaries.Restore, "--format=custom", "--file=/dev/null"},
		Stdin: true,
	}, nil
}

func (e postgresEngine) Restore(c Entry, t Target, db string, o RestoreOptions) (Command, error) {
	if err := pgPrepare(c, t); err != nil {
		return Command{}, err
	}
	if err := e.pgUserDB(t, db); err != nil {
		return Command{}, err
	}
	argv := []string{c.binaries.Restore, "--exit-on-error", "--no-password", "--format=custom"}
	if o.NoOwner {
		argv = append(argv, "--no-owner")
	}
	argv = append(argv, "--dbname="+pgConninfo(t, db))
	return Command{Image: c.image, Argv: argv, Files: pgFiles(t), Stdin: true}, nil
}

// psqlArgv is the hardened psql prefix: no ~/.psqlrc, stop on the first
// error, never prompt, and read stdin first (--file=-): stdin always
// starts with the "\restrict" line of PSQLRestrictPrefix, so no
// meta-command (\!, \o |cmd, \copy ... program, \i, ...) runs, whatever
// follows on stdin or in a --command (D-138).
func psqlArgv(c Entry, t Target) []string {
	return []string{
		c.binaries.Client,
		"--no-psqlrc",
		"--set=ON_ERROR_STOP=1",
		"--no-password",
		"--quiet",
		"--dbname=" + pgConninfo(t, t.maintenanceDB()),
		"--file=-",
	}
}

// restrictKeyBytes is the entropy of a psql \restrict key.
const restrictKeyBytes = 32

// PSQLRestrictPrefix returns "\restrict <key>\n" with a fresh random key
// (32 bytes from crypto/rand, hex): psql then refuses every meta-command
// except "\unrestrict <key>", and the key never leaves the runner except on
// the tool's stdin, so a restored script cannot lift the restriction
// (CVE-2025-8714 class, D-138).
func PSQLRestrictPrefix() []byte {
	var k [restrictKeyBytes]byte
	_, _ = rand.Read(k[:]) // crypto/rand.Read never fails (Go 1.24+)
	out := make([]byte, 0, len(`\restrict `)+2*restrictKeyBytes+1)
	out = append(out, `\restrict `...)
	out = hex.AppendEncode(out, k[:])
	return append(out, '\n')
}

// IsPSQLRestrictPrefix reports whether b is a PSQLRestrictPrefix line
// (the executor refuses a psql command without one).
func IsPSQLRestrictPrefix(b []byte) bool {
	key, ok := strings.CutPrefix(string(b), `\restrict `)
	if !ok {
		return false
	}
	key, ok = strings.CutSuffix(key, "\n")
	if !ok || len(key) != 2*restrictKeyBytes {
		return false
	}
	_, err := hex.DecodeString(key)
	return err == nil && strings.ToLower(key) == key
}

func (postgresEngine) RestoreGlobals(c Entry, t Target) (Command, error) {
	if err := pgPrepare(c, t); err != nil {
		return Command{}, err
	}
	return Command{Image: c.image, Argv: psqlArgv(c, t), Files: pgFiles(t), Stdin: true, StdinPrefix: PSQLRestrictPrefix()}, nil
}

// psqlCommand runs statements, each --command in its own transaction,
// after psql read the \restrict line from stdin (--file=-).
func psqlCommand(c Entry, t Target, stmts ...string) Command {
	argv := psqlArgv(c, t)
	for _, s := range stmts {
		argv = append(argv, "--command="+s)
	}
	return Command{Image: c.image, Argv: argv, Files: pgFiles(t), StdinPrefix: PSQLRestrictPrefix()}
}

func (e postgresEngine) CreateDatabase(c Entry, t Target, db string) (Command, error) {
	if err := pgPrepare(c, t); err != nil {
		return Command{}, err
	}
	if err := e.pgUserDB(t, db); err != nil {
		return Command{}, err
	}
	q, err := QuotePGIdent(db)
	if err != nil {
		return Command{}, err
	}
	return psqlCommand(c, t, "CREATE DATABASE "+q+" TEMPLATE template0"), nil
}

func (e postgresEngine) RenameSwap(c Entry, t Target, db, id string) (SwapPlan, error) {
	if err := pgPrepare(c, t); err != nil {
		return SwapPlan{}, err
	}
	if err := e.pgUserDB(t, db); err != nil {
		return SwapPlan{}, err
	}
	temp, err := TempDatabaseName(db, id)
	if err != nil {
		return SwapPlan{}, err
	}
	old, err := OldDatabaseName(db, id)
	if err != nil {
		return SwapPlan{}, err
	}
	if temp == t.maintenanceDB() || old == t.maintenanceDB() {
		return SwapPlan{}, fmt.Errorf("%w: temporary name collides with the maintenance database", ErrInvalidName)
	}
	qDB, err := QuotePGIdent(db)
	if err != nil {
		return SwapPlan{}, err
	}
	qTemp, err := QuotePGIdent(temp)
	if err != nil {
		return SwapPlan{}, err
	}
	qOld, err := QuotePGIdent(old)
	if err != nil {
		return SwapPlan{}, err
	}
	lit, err := QuoteSQLLiteral(db)
	if err != nil {
		return SwapPlan{}, err
	}
	terminate := "SELECT pg_catalog.pg_terminate_backend(pid) FROM pg_catalog.pg_stat_activity WHERE datname = " +
		lit + " AND pid <> pg_catalog.pg_backend_pid()"
	return SwapPlan{
		Prepare: psqlCommand(c, t,
			"ALTER DATABASE "+qDB+" WITH ALLOW_CONNECTIONS false",
			terminate),
		Swap: psqlCommand(c, t,
			"BEGIN; "+terminate+"; ALTER DATABASE "+qDB+" RENAME TO "+qOld+
				"; ALTER DATABASE "+qTemp+" RENAME TO "+qDB+"; COMMIT"),
		Rollback: psqlCommand(c, t, "ALTER DATABASE "+qDB+" WITH ALLOW_CONNECTIONS true"),
		OldName:  old,
	}, nil
}

func (postgresEngine) DropDatabase(c Entry, t Target, db string) (Command, error) {
	if err := pgPrepare(c, t); err != nil {
		return Command{}, err
	}
	if !IsBekupTempDatabase(db) {
		return Command{}, fmt.Errorf("%w: %q", ErrNotBekupDatabase, truncate(db))
	}
	q, err := QuotePGIdent(db)
	if err != nil {
		return Command{}, err
	}
	stmt := "DROP DATABASE IF EXISTS " + q
	if !t.ServerVersion.Less(Version{Major: 13}) {
		stmt += " WITH (FORCE)"
	}
	return psqlCommand(c, t, stmt), nil
}
