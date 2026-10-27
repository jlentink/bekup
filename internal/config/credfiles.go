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

// DatabaseURL returns the app DB connection URL from database.url_file (or
// the dev-only inline database.url). Zero the result after use.
func (c *Bekup) DatabaseURL() ([]byte, error) {
	return resolveSecret(c.Database.URL, c.Database.URLFile)
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
