package engine

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jlentink/bekup/internal/hostname"
)

// MaxDBNameLen is the longest database name bekup accepts (PostgreSQL's
// NAMEDATALEN-1; MySQL allows 64).
const MaxDBNameLen = 63

var (
	// dbNameRe: first character alphanumeric or "_" (never "-"), then a
	// conservative set. No quotes, spaces, "=", "/", "\" or control bytes,
	// so a name can never be read as an option, a URI or a conninfo string
	// (D-033).
	dbNameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_$.-]{0,62}$`)
	// userRe additionally allows "@" and "+" (cloud user@server forms).
	userRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.@$+-]{0,62}$`)
	// restoreIDRe is the restore identifier embedded in temporary names.
	restoreIDRe = regexp.MustCompile(`^[a-z0-9]{1,26}$`)
	// bekupTempRe matches TempDatabaseName / OldDatabaseName results.
	bekupTempRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_$.-]*__bekup_(?:restore|old)_[a-z0-9]{1,26}$`)
	// scratchIDRe is the identifier of a test-restore scratch database
	// (the first 48 bits of the restore id in hex, task 4.6).
	scratchIDRe = regexp.MustCompile(`^[a-z0-9]{12}$`)
	// snapshotRe is a pg_export_snapshot() id: upper-case hex groups
	// separated by "-" ("00000003-0000001B-1"; older servers
	// "000003A1-1"). It can never be read as an option or carry quotes.
	snapshotRe = regexp.MustCompile(`^[0-9A-F]{1,16}(?:-[0-9A-F]{1,16}){1,3}$`)
)

// Object identifier limits (task 3.10 statistics): PostgreSQL truncates
// identifiers to NAMEDATALEN-1 bytes, MySQL/MariaDB allow 64 characters.
const (
	MaxPGObjectNameBytes    = 63
	MaxMySQLObjectNameChars = 64
)

// ValidateSnapshotID checks a pg_export_snapshot() id for pg_dump
// --snapshot.
func ValidateSnapshotID(id string) error {
	if !snapshotRe.MatchString(id) {
		return fmt.Errorf("%w: snapshot id %q", ErrInvalidName, truncate(id))
	}
	return nil
}

// QuotePGObject quotes a schema, table or other object name as reported by
// the server catalog (any valid UTF-8 without NUL, at most
// MaxPGObjectNameBytes) as a PostgreSQL identifier. Unlike QuotePGIdent it
// does not restrict the charset: the name is not user input but a catalog
// value, and doubling the quotes is the complete escaping rule.
func QuotePGObject(name string) (string, error) {
	if name == "" || len(name) > MaxPGObjectNameBytes || !utf8.ValidString(name) || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("%w: object name %q", ErrInvalidName, truncate(name))
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`, nil
}

// QuoteMySQLObject quotes a catalog object name (valid UTF-8 without NUL,
// at most MaxMySQLObjectNameChars characters) as a MySQL/MariaDB
// identifier (backticks doubled; the same under every sql_mode, since
// ANSI_QUOTES only adds double quotes).
func QuoteMySQLObject(name string) (string, error) {
	if name == "" || utf8.RuneCountInString(name) > MaxMySQLObjectNameChars || !utf8.ValidString(name) || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("%w: object name %q", ErrInvalidName, truncate(name))
	}
	return "`" + strings.ReplaceAll(name, "`", "``") + "`", nil
}

// ValidateDBName checks a database name against the strict charset.
// Callers must additionally check that the name is in the server-reported
// list (D-033).
func ValidateDBName(name string) error {
	if !dbNameRe.MatchString(name) {
		return fmt.Errorf("%w: database name %q", ErrInvalidName, truncate(name))
	}
	return nil
}

// ValidateUser checks a database user name.
func ValidateUser(name string) error {
	if !userRe.MatchString(name) {
		return fmt.Errorf("%w: user name %q", ErrInvalidName, truncate(name))
	}
	return nil
}

// ValidateHost accepts an IP address without zone or an RFC 1123 host name:
// the shared rule of internal/hostname (D-140), so the runner, the SSRF
// guard, the tunnel and the SFTP backend accept exactly the same hosts.
func ValidateHost(host string) error {
	if err := hostname.Validate(host); err != nil {
		return fmt.Errorf("%w: host %q", ErrInvalidName, truncate(host))
	}
	return nil
}

// ValidatePort checks a TCP port.
func ValidatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("%w: port %d", ErrInvalidName, port)
	}
	return nil
}

// ValidateRestoreID checks the restore identifier used in temporary names.
func ValidateRestoreID(id string) error {
	if !restoreIDRe.MatchString(id) {
		return fmt.Errorf("%w: restore id %q", ErrInvalidName, truncate(id))
	}
	return nil
}

// QuotePGIdent quotes a validated name as a PostgreSQL identifier.
func QuotePGIdent(name string) (string, error) {
	if err := ValidateDBName(name); err != nil {
		return "", err
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`, nil
}

// QuoteMySQLIdent quotes a validated name as a MySQL/MariaDB identifier.
func QuoteMySQLIdent(name string) (string, error) {
	if err := ValidateDBName(name); err != nil {
		return "", err
	}
	return "`" + strings.ReplaceAll(name, "`", "``") + "`", nil
}

// QuoteSQLLiteral quotes a validated name as a standard SQL string literal.
// Validated names contain neither quotes nor backslashes, so the result is
// the same under any standard_conforming_strings / sql_mode setting.
func QuoteSQLLiteral(name string) (string, error) {
	if err := ValidateDBName(name); err != nil {
		return "", err
	}
	return "'" + strings.ReplaceAll(name, "'", "''") + "'", nil
}

const (
	restoreMarker = "__bekup_restore_"
	oldMarker     = "__bekup_old_"
)

// TempDatabaseName is the database a restore of db with restore ID id is
// written into before the swap: "<db>__bekup_restore_<id>", with db
// truncated so the result fits MaxDBNameLen.
func TempDatabaseName(db, id string) (string, error) {
	return markedName(db, restoreMarker, id)
}

// OldDatabaseName is the name the replaced database gets on swap:
// "<db>__bekup_old_<id>".
func OldDatabaseName(db, id string) (string, error) {
	return markedName(db, oldMarker, id)
}

func markedName(db, marker, id string) (string, error) {
	if err := ValidateDBName(db); err != nil {
		return "", err
	}
	if err := ValidateRestoreID(id); err != nil {
		return "", err
	}
	suffix := marker + id
	if room := MaxDBNameLen - len(suffix); len(db) > room {
		db = db[:room] // ASCII only, so byte truncation is safe
	}
	return db + suffix, nil
}

// IsBekupTempDatabase reports whether name was produced by TempDatabaseName
// or OldDatabaseName.
func IsBekupTempDatabase(name string) bool {
	return len(name) <= MaxDBNameLen && bekupTempRe.MatchString(name)
}

// ScratchPrefix starts the name of every test-restore scratch database
// (task 4.6). Such names are reserved: the restore API refuses them as
// targets and hides them from target listings.
const ScratchPrefix = "bekup_test_"

// ScratchDatabaseName is the scratch database of a test restore:
// "bekup_test_<id>" with id exactly 12 characters of [a-z0-9]. The runner
// derives it from the request's temp_id for the scratch RPCs; no request
// names a scratch database to inspect or drop.
func ScratchDatabaseName(id string) (string, error) {
	if !scratchIDRe.MatchString(id) {
		return "", fmt.Errorf("%w: scratch id %q", ErrInvalidName, truncate(id))
	}
	return ScratchPrefix + id, nil
}

// IsBekupScratchDatabase reports whether name was produced by
// ScratchDatabaseName.
func IsBekupScratchDatabase(name string) bool {
	id, ok := strings.CutPrefix(name, ScratchPrefix)
	return ok && scratchIDRe.MatchString(id)
}

// IsReservedScratchName reports names in bekup's scratch namespace (any
// name starting with ScratchPrefix, case-insensitively): user restores
// never target them.
func IsReservedScratchName(name string) bool {
	return len(name) >= len(ScratchPrefix) && strings.EqualFold(name[:len(ScratchPrefix)], ScratchPrefix)
}
