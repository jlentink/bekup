package engine

import (
	"errors"
	"fmt"
	"strconv"
)

// Kind identifies a database engine. MySQL and MariaDB are strictly separate
// engines (D-036).
type Kind string

// The supported engines.
const (
	KindPostgres Kind = "postgres"
	KindMySQL    Kind = "mysql"
	KindMariaDB  Kind = "mariadb"
)

// Kinds lists the supported engines in a stable order.
func Kinds() []Kind { return []Kind{KindPostgres, KindMySQL, KindMariaDB} }

// Valid reports whether k is a supported engine.
func (k Kind) Valid() bool {
	switch k {
	case KindPostgres, KindMySQL, KindMariaDB:
		return true
	default:
		return false
	}
}

// Errors returned by this package. Callers compare with errors.Is.
var (
	ErrInvalidCatalog   = errors.New("engine: invalid catalog")
	ErrUnknownEngine    = errors.New("engine: unknown engine")
	ErrInvalidVersion   = errors.New("engine: invalid version")
	ErrWrongEngine      = errors.New("engine: server belongs to a different engine")
	ErrServerTooOld     = errors.New("engine: server version not supported")
	ErrNoClient         = errors.New("engine: no compatible client image in the catalog")
	ErrInvalidName      = errors.New("engine: invalid name")
	ErrSystemDatabase   = errors.New("engine: system database")
	ErrInvalidTarget    = errors.New("engine: invalid target")
	ErrInvalidEntry     = errors.New("engine: catalog entry does not match the engine")
	ErrInvalidSecret    = errors.New("engine: secret value contains forbidden characters")
	ErrUnsupported      = errors.New("engine: operation not supported by this engine")
	ErrNotBekupDatabase = errors.New("engine: database is not a bekup temporary database")
)

// TLSMode is the transport security of a connection to a target database.
// The zero value is the secure default.
type TLSMode int

// TLS modes (D-034, D-123).
const (
	// TLSPinned verifies the server against the pinned certificate (the
	// only trust anchor, delivered as FileCA) including the host name.
	TLSPinned TLSMode = iota
	// TLSUnverified encrypts but does not verify the server. Admin-only,
	// audited downgrade with an "insecure transport" badge.
	TLSUnverified
	// TLSDisabled uses no TLS at all. Admin-only, audited downgrade.
	TLSDisabled
)

// String returns the mode name.
func (m TLSMode) String() string {
	switch m {
	case TLSPinned:
		return "pinned"
	case TLSUnverified:
		return "unverified"
	case TLSDisabled:
		return "disabled"
	default:
		return "invalid(" + strconv.Itoa(int(m)) + ")"
	}
}

func (m TLSMode) valid() bool { return m >= TLSPinned && m <= TLSDisabled }

// Target is the connection a dump or restore container uses. Host and Port
// are what the client dials (inside the container the runner maps Host to
// its per-run proxy, so TLS host-name verification still works).
type Target struct {
	Host string
	Port int
	User string
	TLS  TLSMode
	// ClientCert makes the client present FileClientCert / FileClientKey.
	ClientCert bool
	// MaintenanceDB is the PostgreSQL database used for globals, CREATE,
	// DROP and rename (default "postgres"). Ignored for MySQL and MariaDB.
	MaintenanceDB string
	// ServerVersion is the version Probe detected; zero if unknown. It only
	// selects syntax variants (for example DROP DATABASE ... WITH (FORCE)).
	ServerVersion Version
}

// Validate checks every field of the target.
func (t Target) Validate() error {
	if err := ValidateHost(t.Host); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidTarget, err)
	}
	if err := ValidatePort(t.Port); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidTarget, err)
	}
	if err := ValidateUser(t.User); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidTarget, err)
	}
	if !t.TLS.valid() {
		return fmt.Errorf("%w: TLS mode %s", ErrInvalidTarget, t.TLS)
	}
	if t.ClientCert && t.TLS == TLSDisabled {
		return fmt.Errorf("%w: client certificate without TLS", ErrInvalidTarget)
	}
	if t.MaintenanceDB != "" {
		if err := ValidateDBName(t.MaintenanceDB); err != nil {
			return fmt.Errorf("%w: maintenance database: %w", ErrInvalidTarget, err)
		}
	}
	return nil
}

// maintenanceDB returns the PostgreSQL maintenance database.
func (t Target) maintenanceDB() string {
	if t.MaintenanceDB == "" {
		return "postgres"
	}
	return t.MaintenanceDB
}

// Command is a fully built container invocation. Argv is executed directly
// (no shell) after the stdin preamble has written Files.
type Command struct {
	// Image is the digest-pinned image reference from the catalog.
	Image string
	// Argv is the tool and its arguments.
	Argv []string
	// Files lists the tmpfs files the preamble must write before exec.
	Files []File
	// Stdin reports whether the tool reads payload data (a dump) from stdin
	// after the preamble.
	Stdin bool
	// StdinPrefix is written to the tool's stdin right after the preamble
	// and before any payload (psql: the "\restrict <key>" line that
	// disables every meta-command, D-138). The key is random per command
	// and never logged; the executor zeroes it with the preamble.
	StdinPrefix []byte
}

// RestoreOptions tunes a restore.
type RestoreOptions struct {
	// NoOwner skips ownership commands (PostgreSQL --no-owner).
	NoOwner bool
	// NoPrivileges skips GRANT/REVOKE (PostgreSQL --no-privileges; test
	// restores into scratch databases only).
	NoPrivileges bool
}

// SwapPlan is the PostgreSQL overwrite sequence: Prepare blocks new
// connections to the target and terminates existing ones; Swap renames
// target to old and temp to target in one transaction (retry it if
// terminated backends have not exited yet); Rollback re-allows connections
// to the target when Swap finally fails.
type SwapPlan struct {
	Prepare  Command
	Swap     Command
	Rollback Command
	// OldName is the name the previous database has after the swap.
	OldName string
}

// Engine builds the native tool invocations for one engine. All builders
// are pure: they validate their input and return argv arrays.
type Engine interface {
	Kind() Kind
	// SystemDatabases lists databases that are never dumped by "dump all"
	// and never created, dropped or overwritten.
	SystemDatabases() []string
	IsSystemDatabase(name string) bool
	// ListDatabasesQuery and VersionQuery are the Probe queries (run by the
	// runner through a Go driver).
	ListDatabasesQuery() string
	VersionQuery() string
	ParseServerVersion(s string) (Version, error)

	Dump(c Entry, t Target, db string) (Command, error)
	// DumpSnapshot is Dump inside an exported snapshot (PostgreSQL
	// pg_dump --snapshot, validated with ValidateSnapshotID; "" = Dump).
	// MySQL and MariaDB refuse a non-empty snapshot.
	DumpSnapshot(c Entry, t Target, db, snapshot string) (Command, error)
	// DumpGlobals returns the primary invocation and the fallback for
	// non-superusers (PostgreSQL only).
	DumpGlobals(c Entry, t Target) (primary, fallback Command, err error)
	// VerifyDump checks a dump read from stdin (PostgreSQL only; MySQL and
	// MariaDB dumps are checked with DumpTrailerOK).
	VerifyDump(c Entry) (Command, error)
	Restore(c Entry, t Target, db string, o RestoreOptions) (Command, error)
	// RestoreGlobals is the separate, admin-only globals restore
	// (PostgreSQL only).
	RestoreGlobals(c Entry, t Target) (Command, error)
	CreateDatabase(c Entry, t Target, db string) (Command, error)
	// RenameSwap replaces db with its restore temp database for restore ID
	// id (PostgreSQL only).
	RenameSwap(c Entry, t Target, db, id string) (SwapPlan, error)
	// DropDatabase drops a bekup temporary database (a TempDatabaseName,
	// OldDatabaseName or test-restore ScratchDatabaseName); any other name
	// is refused.
	DropDatabase(c Entry, t Target, db string) (Command, error)
}

// ForKind returns the engine for k.
func ForKind(k Kind) (Engine, error) {
	switch k {
	case KindPostgres:
		return postgresEngine{}, nil
	case KindMySQL:
		return newMySQLEngine(), nil
	case KindMariaDB:
		return newMariaDBEngine(), nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownEngine, truncate(string(k)))
	}
}

// checkEntry makes sure c came from the catalog and belongs to engine k.
func checkEntry(c Entry, k Kind) error {
	if c.image == "" || c.kind != k {
		return fmt.Errorf("%w: want %s", ErrInvalidEntry, k)
	}
	return nil
}

// tlsFiles returns the tmpfs files a target's TLS settings need.
func tlsFiles(t Target) []File {
	var f []File
	if t.TLS == TLSPinned {
		f = append(f, FileCA)
	}
	if t.ClientCert {
		f = append(f, FileClientCert, FileClientKey)
	}
	return f
}
