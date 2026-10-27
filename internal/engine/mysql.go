package engine

import (
	"bytes"
	"fmt"
	"slices"
)

// mysqlFamily implements MySQL and MariaDB. They share the option-file and
// argv layout but differ in binaries, TLS flags, dump flags and the restore
// sandbox flags; the two are never mixed (D-036).
type mysqlFamily struct {
	kind Kind
	// tls maps a TLS mode to the client flags (without client cert).
	tls func(TLSMode) []string
	// dumpExtra are engine-specific dump flags.
	dumpExtra []string
	// restoreSafety are the flags that stop a malicious dump from running
	// shell commands or reading files on restore.
	restoreSafety []string
}

var mysqlSystemDatabases = []string{"information_schema", "mysql", "performance_schema", "sys"}

func newMySQLEngine() mysqlFamily {
	return mysqlFamily{
		kind: KindMySQL,
		tls: func(m TLSMode) []string {
			switch m {
			case TLSPinned:
				return []string{"--ssl-mode=VERIFY_IDENTITY", "--ssl-ca=" + string(FileCA), "--tls-version=TLSv1.2,TLSv1.3"}
			case TLSUnverified:
				return []string{"--ssl-mode=REQUIRED", "--tls-version=TLSv1.2,TLSv1.3"}
			default:
				return []string{"--ssl-mode=DISABLED"}
			}
		},
		dumpExtra:     []string{"--set-gtid-purged=OFF"},
		restoreSafety: []string{"--system-command=OFF", "--commands=OFF", "--local-infile=0"},
	}
}

func newMariaDBEngine() mysqlFamily {
	return mysqlFamily{
		kind: KindMariaDB,
		tls: func(m TLSMode) []string {
			switch m {
			case TLSPinned:
				return []string{"--ssl", "--ssl-ca=" + string(FileCA), "--ssl-verify-server-cert", "--tls-version=TLSv1.2,TLSv1.3"}
			case TLSUnverified:
				return []string{"--ssl", "--skip-ssl-verify-server-cert", "--tls-version=TLSv1.2,TLSv1.3"}
			default:
				return []string{"--skip-ssl"}
			}
		},
		restoreSafety: []string{"--sandbox", "--local-infile=0"},
	}
}

func (f mysqlFamily) Kind() Kind { return f.kind }

func (mysqlFamily) SystemDatabases() []string { return slices.Clone(mysqlSystemDatabases) }

func (mysqlFamily) IsSystemDatabase(name string) bool {
	return slices.Contains(mysqlSystemDatabases, name)
}

func (mysqlFamily) ListDatabasesQuery() string { return "SHOW DATABASES" }

func (mysqlFamily) VersionQuery() string { return "SELECT VERSION()" }

func (f mysqlFamily) ParseServerVersion(s string) (Version, error) {
	if f.kind == KindMariaDB {
		return ParseMariaDBVersion(s)
	}
	return ParseMySQLVersion(s)
}

// connArgv is binary + the connection flags. --defaults-file must be the
// first option: it makes the client read only bekup's option file (not the
// image's /etc/mysql configs or ~/.my.cnf).
func (f mysqlFamily) connArgv(bin string, t Target) []string {
	argv := []string{
		bin,
		"--defaults-file=" + string(FileMyCnf),
		"--protocol=TCP",
		"--host=" + t.Host,
		"--port=" + formatPort(t.Port),
		"--user=" + t.User,
	}
	argv = append(argv, f.tls(t.TLS)...)
	if t.ClientCert {
		argv = append(argv, "--ssl-cert="+string(FileClientCert), "--ssl-key="+string(FileClientKey))
	}
	return argv
}

func (f mysqlFamily) files(t Target) []File {
	return append([]File{FileMyCnf}, tlsFiles(t)...)
}

func (f mysqlFamily) prepare(c Entry, t Target) error {
	if err := checkEntry(c, f.kind); err != nil {
		return err
	}
	return t.Validate()
}

func (f mysqlFamily) userDB(db string) error {
	if err := ValidateDBName(db); err != nil {
		return err
	}
	if f.IsSystemDatabase(db) {
		return fmt.Errorf("%w: %q", ErrSystemDatabase, db)
	}
	return nil
}

func (f mysqlFamily) Dump(c Entry, t Target, db string) (Command, error) {
	if err := f.prepare(c, t); err != nil {
		return Command{}, err
	}
	if err := ValidateDBName(db); err != nil {
		return Command{}, err
	}
	argv := f.connArgv(c.binaries.Dump, t)
	argv = append(argv,
		"--single-transaction",
		"--routines",
		"--triggers",
		"--events",
		"--hex-blob",
		"--no-tablespaces",
		"--default-character-set=utf8mb4",
	)
	argv = append(argv, f.dumpExtra...)
	// "--" ends option parsing; the database is positional (no
	// --databases, so the dump carries no CREATE DATABASE / USE).
	argv = append(argv, "--", db)
	return Command{Image: c.image, Argv: argv, Files: f.files(t)}, nil
}

func (f mysqlFamily) DumpGlobals(Entry, Target) (primary, fallback Command, err error) {
	return Command{}, Command{}, fmt.Errorf("%w: %s globals", ErrUnsupported, f.kind)
}

func (f mysqlFamily) VerifyDump(Entry) (Command, error) {
	return Command{}, fmt.Errorf("%w: %s verify (use DumpTrailerOK)", ErrUnsupported, f.kind)
}

func (f mysqlFamily) Restore(c Entry, t Target, db string, _ RestoreOptions) (Command, error) {
	if err := f.prepare(c, t); err != nil {
		return Command{}, err
	}
	if err := f.userDB(db); err != nil {
		return Command{}, err
	}
	argv := f.connArgv(c.binaries.Client, t)
	argv = append(argv, f.restoreSafety...)
	argv = append(argv, "--default-character-set=utf8mb4", "--database="+db)
	return Command{Image: c.image, Argv: argv, Files: f.files(t), Stdin: true}, nil
}

func (f mysqlFamily) RestoreGlobals(Entry, Target) (Command, error) {
	return Command{}, fmt.Errorf("%w: %s globals", ErrUnsupported, f.kind)
}

func (f mysqlFamily) execute(c Entry, t Target, stmt string) Command {
	argv := f.connArgv(c.binaries.Client, t)
	argv = append(argv, f.restoreSafety...)
	argv = append(argv, "--execute="+stmt)
	return Command{Image: c.image, Argv: argv, Files: f.files(t)}
}

func (f mysqlFamily) CreateDatabase(c Entry, t Target, db string) (Command, error) {
	if err := f.prepare(c, t); err != nil {
		return Command{}, err
	}
	if err := f.userDB(db); err != nil {
		return Command{}, err
	}
	q, err := QuoteMySQLIdent(db)
	if err != nil {
		return Command{}, err
	}
	return f.execute(c, t, "CREATE DATABASE "+q), nil
}

func (f mysqlFamily) RenameSwap(Entry, Target, string, string) (SwapPlan, error) {
	return SwapPlan{}, fmt.Errorf("%w: %s has no database rename", ErrUnsupported, f.kind)
}

func (f mysqlFamily) DropDatabase(c Entry, t Target, db string) (Command, error) {
	if err := f.prepare(c, t); err != nil {
		return Command{}, err
	}
	if !IsBekupTempDatabase(db) {
		return Command{}, fmt.Errorf("%w: %q", ErrNotBekupDatabase, truncate(db))
	}
	q, err := QuoteMySQLIdent(db)
	if err != nil {
		return Command{}, err
	}
	return f.execute(c, t, "DROP DATABASE IF EXISTS "+q), nil
}

// DumpTrailerWindow is how many trailing bytes of a MySQL/MariaDB dump
// DumpTrailerOK needs.
const DumpTrailerWindow = 256

var dumpTrailer = []byte("-- Dump completed")

// DumpTrailerOK reports whether the tail of a mysqldump / mariadb-dump
// output ends with the "-- Dump completed" line (written only after a
// complete dump). Together with exit code 0 it detects truncated dumps.
func DumpTrailerOK(tail []byte) bool {
	tail = bytes.TrimRight(tail, "\r\n\t ")
	if i := bytes.LastIndexByte(tail, '\n'); i >= 0 {
		tail = tail[i+1:]
	}
	return bytes.HasPrefix(tail, dumpTrailer)
}
