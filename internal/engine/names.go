package engine

import (
	"fmt"
	"regexp"
	"strings"

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
)

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
