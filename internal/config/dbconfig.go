package config

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jlentink/bekup/internal/hostname"
)

// Keys of the two mutually exclusive ways to configure the app DB login
// (D-207).
var (
	dbURLKeys = []string{"url_file", "url", "owner_url_file", "owner_url"}
	// dbFieldKeys besides database.host, which selects fields mode.
	dbFieldKeys = []string{"port", "name", "user", "password_file", "owner_user", "owner_password_file", "single_account"}
)

// normalizeBekup resolves the [database] mode (D-207): database.host selects
// fields mode. Fields and URL keys never mix: an explicitly set key of the
// other mode is an error (never a silent precedence), and the defaulted URL
// files are cleared in fields mode so that nothing reads them. In fields
// mode with two roles the role names default to bekup_app / bekup_owner.
func normalizeBekup(c *Bekup, sources map[string]Source) []error {
	var v validator
	d := &c.Database
	explicit := func(key string) bool {
		s, ok := sources["database."+key]
		return ok && s != SourceDefault
	}
	if !d.FieldsMode() {
		// A fields key set to its default (a full dump of the effective
		// configuration writes every key) changes nothing in URL mode.
		def := DefaultBekup()
		for _, k := range dbFieldKeys {
			if explicit(k) && fieldByPath(c, "database."+k).Value() != fieldByPath(&def, "database."+k).Value() {
				v.addf("database."+k, "is a fields-mode key: set database.host to use fields mode (and remove database.url_file / owner_url_file), or remove it")
			}
		}
		return v.errs
	}
	for _, k := range dbURLKeys {
		if explicit(k) && !fieldByPath(c, "database."+k).IsZero() {
			v.addf("database."+k, "cannot be combined with database.host: use either the connection URL files or the fields host/port/name/user/password_file, not both")
		}
	}
	// Defaults of URL mode: nothing may read them in fields mode.
	d.URLFile, d.OwnerURLFile = "", ""
	if d.SingleAccount {
		for _, k := range []string{"owner_user", "owner_password_file"} {
			if !fieldByPath(c, "database."+k).IsZero() {
				v.addf("database."+k, "is not used with database.single_account = true (the one account database.user runs everything); remove it")
			}
		}
		return v.errs
	}
	if d.User == "" {
		d.User = DBAppRole
	}
	if d.OwnerUser == "" {
		d.OwnerUser = DBOwnerRole
	}
	return v.errs
}

// fieldByPath returns the field of c at "section.key" (it must exist).
func fieldByPath(c *Bekup, path string) Field {
	fields := fieldsOf(c)
	for i := range fields {
		if fields[i].Path == path {
			return fields[i]
		}
	}
	panic("config: unknown key " + path)
}

var (
	// dbNameRe is the accepted database name: an identifier bekup can
	// quote safely and the operator can type (D-207).
	dbNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$-]{0,62}$`)
	// dbRoleRe is the accepted login role name of single-account mode
	// (also covers managed-provider names such as user@server).
	dbRoleRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.@$-]{0,62}$`)
)

// ValidDBName reports whether name is an acceptable bekup database name.
func ValidDBName(name string) bool {
	if !dbNameRe.MatchString(name) {
		return false
	}
	switch strings.ToLower(name) {
	case "postgres", "template0", "template1":
		return false
	}
	return true
}

// ValidDBRoleName reports whether name is an acceptable login role name
// for single-account mode or a bootstrap admin (D-207): not a reserved
// pg_ name and not in bekup's own bekup_ namespace (bekup_owner,
// bekup_app and any other bekup_* name, case-insensitively: a look-alike
// such as bekup_admin would blur which role is bekup's in role listings
// and the membership checks).
func ValidDBRoleName(name string) bool {
	lower := strings.ToLower(name)
	return dbRoleRe.MatchString(name) && !strings.HasPrefix(lower, "pg_") && !strings.HasPrefix(lower, "bekup_")
}

// validateDatabaseFields checks the fields-mode keys (D-207).
func validateDatabaseFields(v *validator, d *Database) {
	if !d.FieldsMode() {
		return
	}
	if err := hostname.Validate(d.Host); err != nil {
		v.addf("database.host", "must be a host name or IP address without port or scheme (%v); Unix sockets are not supported in fields mode", err)
	}
	if d.Port < 1 || d.Port > 65535 {
		v.addf("database.port", "must be between 1 and 65535 (got %d)", d.Port)
	}
	if !ValidDBName(d.Name) {
		v.addf("database.name", "must be 1 to 63 characters of letters, digits, _, - or $ starting with a letter or _, and not postgres or a template database (got %q)", d.Name)
	}
	v.absPath("database.password_file", d.PasswordFile, true)
	if d.SingleAccount {
		if !ValidDBRoleName(d.User) {
			v.addf("database.user", "must be the single account's role name (1 to 63 characters of letters, digits, _ . @ $ -; not a bekup_* or pg_* name) with single_account = true (got %q)", d.User)
		}
		return
	}
	if d.User != DBAppRole {
		v.addf("database.user", "must be %s with two roles (role names are fixed, D-207); set single_account = true to use one account for everything (got %q)", DBAppRole, d.User)
	}
	if d.OwnerUser != DBOwnerRole {
		v.addf("database.owner_user", "must be %s (role names are fixed, D-207; got %q)", DBOwnerRole, d.OwnerUser)
	}
	v.absPath("database.owner_password_file", d.OwnerPasswordFile, false)
	if d.OwnerPasswordFile != "" && d.OwnerPasswordFile == d.PasswordFile {
		v.addf("database.owner_password_file", "must differ from database.password_file (bekup_owner and bekup_app have separate passwords)")
	}
}

// validateTLSServerName checks database.tls_server_name.
func validateTLSServerName(v *validator, d *Database) {
	if d.TLSServerName == "" {
		return
	}
	if err := hostname.Validate(d.TLSServerName); err != nil {
		v.addf("database.tls_server_name", "must be a host name or IP address (%v)", err)
	}
	if d.TLSMode != DBTLSVerifyFull {
		v.addf("database.tls_server_name", "only applies with tls_mode = %q (got %q)", DBTLSVerifyFull, d.TLSMode)
	}
}

// DatabaseMode names the configured [database] mode for logs and the
// admin view: "url", "fields" or "single_account".
func (d *Database) DatabaseMode() string {
	switch {
	case !d.FieldsMode():
		return "url"
	case d.SingleAccount:
		return "single_account"
	default:
		return "fields"
	}
}

// String is never used for secrets; it exists to keep fmt from printing
// credentials if a DBCredentials value ends up in a format call.
func (c DBCredentials) String() string {
	if c.FieldsMode() {
		return fmt.Sprintf("DBCredentials{fields, user %q}", c.User)
	}
	return "DBCredentials{url}"
}

// GoString keeps %#v from printing credentials too.
func (c DBCredentials) GoString() string { return c.String() }
