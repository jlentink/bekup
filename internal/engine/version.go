package engine

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version is a numeric server or client version. For PostgreSQL 10+ only
// Major and Minor are meaningful (18.6); for MySQL and MariaDB all three
// parts are (8.4.13).
type Version struct {
	Major int
	Minor int
	Patch int
}

// String formats the version as "major.minor.patch".
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// IsZero reports whether v is the zero (unknown) version.
func (v Version) IsZero() bool { return v == Version{} }

// Compare returns -1, 0 or +1 when v is lower than, equal to or higher than w.
func (v Version) Compare(w Version) int {
	switch {
	case v.Major != w.Major:
		return cmpInt(v.Major, w.Major)
	case v.Minor != w.Minor:
		return cmpInt(v.Minor, w.Minor)
	default:
		return cmpInt(v.Patch, w.Patch)
	}
}

// Less reports whether v < w.
func (v Version) Less(w Version) bool { return v.Compare(w) < 0 }

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// maxVersionPart bounds every numeric part so that parsing never overflows
// and absurd values are rejected.
const maxVersionPart = 99999

// versionPrefix matches "major.minor[.patch]" at the start of a string.
var versionPrefix = regexp.MustCompile(`^(\d{1,5})\.(\d{1,5})(?:\.(\d{1,5}))?`)

// parseParts parses the leading "major.minor[.patch]" of s and returns the
// version and the rest of the string.
func parseParts(s string) (Version, string, error) {
	m := versionPrefix.FindStringSubmatch(s)
	if m == nil {
		return Version{}, "", fmt.Errorf("%w: %q", ErrInvalidVersion, truncate(s))
	}
	var v Version
	var err error
	if v.Major, err = atoiBounded(m[1]); err != nil {
		return Version{}, "", err
	}
	if v.Minor, err = atoiBounded(m[2]); err != nil {
		return Version{}, "", err
	}
	if m[3] != "" {
		if v.Patch, err = atoiBounded(m[3]); err != nil {
			return Version{}, "", err
		}
	}
	return v, s[len(m[0]):], nil
}

func atoiBounded(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > maxVersionPart {
		return 0, fmt.Errorf("%w: part %q out of range", ErrInvalidVersion, truncate(s))
	}
	return n, nil
}

// ParseVersion parses a plain "major.minor[.patch]" version without
// trailing text.
func ParseVersion(s string) (Version, error) {
	v, rest, err := parseParts(s)
	if err != nil {
		return Version{}, err
	}
	if rest != "" {
		return Version{}, fmt.Errorf("%w: trailing text in %q", ErrInvalidVersion, truncate(s))
	}
	return v, nil
}

// ParsePGServerVersionNum parses the output of SHOW server_version_num
// ("180006", "90624"). PostgreSQL 10+ encode major*10000+minor; older
// releases encode major*10000+minor*100+patch.
func ParsePGServerVersionNum(s string) (Version, error) {
	s = strings.TrimSpace(s)
	if len(s) < 5 || len(s) > 7 {
		return Version{}, fmt.Errorf("%w: server_version_num %q", ErrInvalidVersion, truncate(s))
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return Version{}, fmt.Errorf("%w: server_version_num %q", ErrInvalidVersion, truncate(s))
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return Version{}, fmt.Errorf("%w: server_version_num %q", ErrInvalidVersion, truncate(s))
	}
	if n >= 100000 {
		return Version{Major: n / 10000, Minor: n % 10000}, nil
	}
	if n < 70000 {
		return Version{}, fmt.Errorf("%w: server_version_num %q", ErrInvalidVersion, truncate(s))
	}
	return Version{Major: n / 10000, Minor: (n / 100) % 100, Patch: n % 100}, nil
}

// ParseMySQLVersion parses a MySQL VERSION() string ("8.4.13",
// "8.0.36-0ubuntu0.22.04.1", "9.7.4-log"). MariaDB strings are rejected:
// MySQL and MariaDB are separate engines.
func ParseMySQLVersion(s string) (Version, error) {
	if strings.Contains(strings.ToLower(s), "mariadb") {
		return Version{}, fmt.Errorf("%w: %q is a MariaDB server", ErrWrongEngine, truncate(s))
	}
	v, rest, err := parseParts(s)
	if err != nil {
		return Version{}, err
	}
	if rest != "" && rest[0] != '-' {
		return Version{}, fmt.Errorf("%w: %q", ErrInvalidVersion, truncate(s))
	}
	return v, nil
}

// ParseMariaDBVersion parses a MariaDB VERSION() or handshake string
// ("11.4.2-MariaDB-log", "5.5.5-10.6.18-MariaDB-1:10.6.18+maria~ubu2004").
// The string must identify itself as MariaDB.
func ParseMariaDBVersion(s string) (Version, error) {
	if !strings.Contains(strings.ToLower(s), "mariadb") {
		return Version{}, fmt.Errorf("%w: %q is not a MariaDB server", ErrWrongEngine, truncate(s))
	}
	// Replication-compatibility prefix sent in the handshake by MariaDB 10+.
	t := strings.TrimPrefix(s, "5.5.5-")
	v, rest, err := parseParts(t)
	if err != nil {
		return Version{}, err
	}
	if rest == "" || rest[0] != '-' {
		return Version{}, fmt.Errorf("%w: %q", ErrInvalidVersion, truncate(s))
	}
	return v, nil
}

// truncate shortens untrusted strings before they are put into errors.
func truncate(s string) string {
	const limit = 64
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}
