package destinations

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/crypto"
)

// Credentials are the secret parts of a destination. They exist in memory
// only while needed; Zero overwrites every field.
type Credentials struct {
	// SecretAccessKey is the S3 secret access key.
	SecretAccessKey []byte
	// Password is the SFTP password.
	Password []byte
	// PrivateKey is the SFTP private key (PEM) and Passphrase its optional
	// passphrase.
	PrivateKey []byte
	Passphrase []byte
}

// Zero overwrites and drops every field.
func (c *Credentials) Zero() {
	if c == nil {
		return
	}
	for _, b := range []*[]byte{&c.SecretAccessKey, &c.Password, &c.PrivateKey, &c.Passphrase} {
		crypto.Zero(*b)
		*b = nil
	}
}

func (c *Credentials) empty() bool {
	return len(c.SecretAccessKey) == 0 && len(c.Password) == 0 && len(c.PrivateKey) == 0 && len(c.Passphrase) == 0
}

// Encoding of the sealed blob: a version byte followed by fields of
// (tag byte, big-endian uint32 length, bytes). A hand-written format keeps
// the secrets in []byte (encoding/json would copy them into strings that
// cannot be zeroed).
const (
	credsVersion        = 1
	tagSecretAccessKey  = 1
	tagPassword         = 2
	tagPrivateKey       = 3
	tagPassphrase       = 4
	maxCredentialsBytes = 96 << 10
)

var errCorruptCredentials = errors.New("destinations: stored credentials are malformed")

// marshal encodes c. The caller zeroes the result.
func (c *Credentials) marshal() []byte {
	n := 1
	fields := []struct {
		tag byte
		v   []byte
	}{
		{tagSecretAccessKey, c.SecretAccessKey}, {tagPassword, c.Password},
		{tagPrivateKey, c.PrivateKey}, {tagPassphrase, c.Passphrase},
	}
	for _, f := range fields {
		if len(f.v) > 0 {
			n += 5 + len(f.v)
		}
	}
	out := make([]byte, 0, n)
	out = append(out, credsVersion)
	for _, f := range fields {
		if len(f.v) == 0 {
			continue
		}
		out = append(out, f.tag)
		out = binary.BigEndian.AppendUint32(out, uint32(len(f.v))) //nolint:gosec // G115: bounded by maxCredentialsBytes (checked by callers)
		out = append(out, f.v...)
	}
	return out
}

// unmarshalCredentials decodes b into fresh copies; b is not retained.
func unmarshalCredentials(b []byte) (*Credentials, error) {
	if len(b) == 0 || b[0] != credsVersion {
		return nil, errCorruptCredentials
	}
	c := &Credentials{}
	rest := b[1:]
	for len(rest) > 0 {
		if len(rest) < 5 {
			c.Zero()
			return nil, errCorruptCredentials
		}
		tag := rest[0]
		n := binary.BigEndian.Uint32(rest[1:5])
		rest = rest[5:]
		if uint64(n) > uint64(len(rest)) || n == 0 {
			c.Zero()
			return nil, errCorruptCredentials
		}
		v := append([]byte(nil), rest[:n]...)
		rest = rest[n:]
		var dst *[]byte
		switch tag {
		case tagSecretAccessKey:
			dst = &c.SecretAccessKey
		case tagPassword:
			dst = &c.Password
		case tagPrivateKey:
			dst = &c.PrivateKey
		case tagPassphrase:
			dst = &c.Passphrase
		default:
			crypto.Zero(v)
			c.Zero()
			return nil, errCorruptCredentials
		}
		if *dst != nil {
			crypto.Zero(v)
			c.Zero()
			return nil, errCorruptCredentials
		}
		*dst = v
	}
	return c, nil
}

// credentialsAAD binds the sealed credentials to their row.
func credentialsAAD(id uuid.UUID) crypto.AAD {
	return crypto.AAD{Table: "destinations", RowID: id.String(), Field: "secret"}
}

// sealCredentials seals c for the destination id; nil when c is empty.
func sealCredentials(kr *crypto.Keyring, id uuid.UUID, c *Credentials) ([]byte, error) {
	if c == nil || c.empty() {
		return nil, nil
	}
	plain := c.marshal()
	defer crypto.Zero(plain)
	if len(plain) > maxCredentialsBytes {
		return nil, &FieldError{Field: "credentials", Message: "credentials are too large"}
	}
	out, err := kr.Seal(plain, credentialsAAD(id))
	if err != nil {
		return nil, fmt.Errorf("destinations: seal credentials: %w", err)
	}
	return out, nil
}

// openCredentials opens the sealed credentials of the destination id (an
// empty Credentials when sealed is empty). The caller zeroes the result.
func openCredentials(kr *crypto.Keyring, id uuid.UUID, sealed []byte) (*Credentials, error) {
	if len(sealed) == 0 {
		return &Credentials{}, nil
	}
	plain, err := kr.Open(sealed, credentialsAAD(id))
	if err != nil {
		return nil, fmt.Errorf("destinations: open credentials: %w", err)
	}
	defer crypto.Zero(plain)
	return unmarshalCredentials(plain)
}
