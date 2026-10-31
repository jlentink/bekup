package config

import (
	"bytes"
	"errors"
	"fmt"
)

// ErrUnsafeFile is returned when a config or secret file fails the safety
// checks (type, owner or permissions).
var ErrUnsafeFile = errors.New("unsafe file")

const (
	maxConfigBytes = 1 << 20  // 1 MiB
	maxSecretBytes = 64 << 10 // 64 KiB
)

// ReadSecretFile reads a secret from path after enforcing the secret-file
// rules: a regular file (symlinks only into safe directories), owned by the
// current user or root, not group/world-writable and not world-readable.
// Surrounding whitespace is trimmed. The caller owns the returned slice and
// should Zero it once the secret is no longer needed.
func ReadSecretFile(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("secret file path is empty")
	}
	raw, err := readSafeFile(path, true, maxSecretBytes)
	if err != nil {
		return nil, fmt.Errorf("reading secret file: %w", err)
	}
	defer zero(raw)
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("reading secret file: %s is empty", path)
	}
	out := make([]byte, len(trimmed))
	copy(out, trimmed)
	return out, nil
}

// Zero overwrites b with zero bytes.
func Zero(b []byte) { zero(b) }

func zero(b []byte) { clear(b) }

// resolveSecret returns the inline value (dev mode only; validation rejects
// it otherwise) or reads the secret file.
func resolveSecret(inline, file string) ([]byte, error) {
	if inline != "" {
		return []byte(inline), nil
	}
	return ReadSecretFile(file)
}

// DBCredentials is the login of one app DB connection (D-207): either a
// connection URL (URL mode) or host, port, database, user and password
// (fields mode, never turned into a connection string). internal/db builds
// the connection from it with one builder for the pool, the LISTEN
// connector and the migrator. Call Zero once it is no longer needed.
type DBCredentials struct {
	// URL is the connection URL in URL mode (nil in fields mode).
	URL []byte
	// Fields mode.
	Host     string
	Port     uint16
	Database string
	User     string
	Password []byte
	// SetRole is the role every session switches to right after it
	// connected (SET ROLE; single-account mode only, D-207). Empty: none.
	SetRole string
}

// FieldsMode reports whether the credentials are host/user/password fields.
func (c *DBCredentials) FieldsMode() bool { return c.URL == nil }

// Zero overwrites the URL and the password.
func (c *DBCredentials) Zero() {
	zero(c.URL)
	zero(c.Password)
	c.URL, c.Password = nil, nil
}

// AppDBCredentials returns the login of serve and worker: the URL from
// database.url_file (or the dev-only inline database.url) in URL mode, or
// database.user with the password from database.password_file in fields
// mode (single-account sessions switch to bekup_app). Zero the result.
func (c *Bekup) AppDBCredentials() (DBCredentials, error) {
	d := &c.Database
	if !d.FieldsMode() {
		u, err := resolveSecret(d.URL, d.URLFile)
		if err != nil {
			return DBCredentials{}, err
		}
		return DBCredentials{URL: u}, nil
	}
	pw, err := ReadSecretFile(d.PasswordFile)
	if err != nil {
		return DBCredentials{}, fmt.Errorf("database.password_file: %w", err)
	}
	creds := d.fieldCredentials(d.User, pw)
	if d.SingleAccount {
		creds.SetRole = DBAppRole
	}
	return creds, nil
}

// OwnerDBCredentials returns the login of bekup migrate: the URL from
// database.owner_url_file (or the dev-only inline database.owner_url) in
// URL mode, bekup_owner with database.owner_password_file in fields mode,
// or the single account (switching to bekup_owner). Zero the result.
func (c *Bekup) OwnerDBCredentials() (DBCredentials, error) {
	d := &c.Database
	if !d.FieldsMode() {
		u, err := resolveSecret(d.OwnerURL, d.OwnerURLFile)
		if err != nil {
			return DBCredentials{}, err
		}
		return DBCredentials{URL: u}, nil
	}
	if d.SingleAccount {
		pw, err := ReadSecretFile(d.PasswordFile)
		if err != nil {
			return DBCredentials{}, fmt.Errorf("database.password_file: %w", err)
		}
		creds := d.fieldCredentials(d.User, pw)
		creds.SetRole = DBOwnerRole
		return creds, nil
	}
	if d.OwnerPasswordFile == "" {
		return DBCredentials{}, errors.New("database.owner_password_file is not set: migrations run as bekup_owner and need its password")
	}
	pw, err := ReadSecretFile(d.OwnerPasswordFile)
	if err != nil {
		return DBCredentials{}, fmt.Errorf("database.owner_password_file: %w", err)
	}
	return d.fieldCredentials(d.OwnerUser, pw), nil
}

func (d *Database) fieldCredentials(user string, pw []byte) DBCredentials {
	return DBCredentials{
		Host: d.Host, Port: uint16(d.Port), //nolint:gosec // G115: validated to 1..65535
		Database: d.Name, User: user, Password: pw,
	}
}

// MasterKey returns the master keyring from security.master_key_file (or the
// dev-only inline security.master_key). Zero the result after use.
func (c *Bekup) MasterKey() ([]byte, error) {
	return resolveSecret(c.Security.MasterKey, c.Security.MasterKeyFile)
}

// DatabaseTLSClientKey returns the app DB TLS client private key from
// database.tls_client_key_file, or nil when none is configured. Zero the
// result after use.
func (c *Bekup) DatabaseTLSClientKey() ([]byte, error) {
	if c.Database.TLSClientKeyFile == "" {
		return nil, nil
	}
	return ReadSecretFile(c.Database.TLSClientKeyFile)
}
