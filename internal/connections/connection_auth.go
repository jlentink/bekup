package connections

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/crypto"
)

// Credentials are the secret parts of a connection. They exist in memory
// only while needed; Zero overwrites every field.
type Credentials struct {
	// Password is the database password.
	Password []byte
	// SSHPassword is the bastion password; SSHPrivateKey (PEM) with the
	// optional SSHPassphrase is the alternative (exactly one is stored).
	SSHPassword   []byte
	SSHPrivateKey []byte
	SSHPassphrase []byte
	// ClientKey is the private key (PEM) of the optional TLS client
	// certificate.
	ClientKey []byte
}

func (c *Credentials) fields() []*[]byte {
	return []*[]byte{&c.Password, &c.SSHPassword, &c.SSHPrivateKey, &c.SSHPassphrase, &c.ClientKey}
}

// Zero overwrites and drops every field.
func (c *Credentials) Zero() {
	if c == nil {
		return
	}
	for _, b := range c.fields() {
		crypto.Zero(*b)
		*b = nil
	}
}

func (c *Credentials) empty() bool {
	for _, b := range c.fields() {
		if len(*b) > 0 {
			return false
		}
	}
	return true
}

// clearSSH drops the SSH credentials.
func (c *Credentials) clearSSH() {
	for _, b := range []*[]byte{&c.SSHPassword, &c.SSHPrivateKey, &c.SSHPassphrase} {
		crypto.Zero(*b)
		*b = nil
	}
}

// Encoding of the sealed blob: a version byte followed by fields of
// (tag byte, big-endian uint32 length, bytes), as for destinations (D-131).
// A hand-written format keeps the secrets in []byte (encoding/json would
// copy them into strings that cannot be zeroed).
const (
	credsVersion        = 1
	tagPassword         = 1
	tagSSHPassword      = 2
	tagSSHPrivateKey    = 3
	tagSSHPassphrase    = 4
	tagClientKey        = 5
	maxCredentialsBytes = 96 << 10
)

var errCorruptCredentials = errors.New("connections: stored credentials are malformed")

func (c *Credentials) tagged() []struct {
	tag byte
	v   []byte
} {
	return []struct {
		tag byte
		v   []byte
	}{
		{tagPassword, c.Password}, {tagSSHPassword, c.SSHPassword}, {tagSSHPrivateKey, c.SSHPrivateKey},
		{tagSSHPassphrase, c.SSHPassphrase}, {tagClientKey, c.ClientKey},
	}
}

// marshal encodes c. The caller zeroes the result.
func (c *Credentials) marshal() []byte {
	n := 1
	fields := c.tagged()
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
		out = binary.BigEndian.AppendUint32(out, uint32(len(f.v))) //nolint:gosec // G115: every field is bounded far below 4 GiB by validation
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
		var dst *[]byte
		switch tag {
		case tagPassword:
			dst = &c.Password
		case tagSSHPassword:
			dst = &c.SSHPassword
		case tagSSHPrivateKey:
			dst = &c.SSHPrivateKey
		case tagSSHPassphrase:
			dst = &c.SSHPassphrase
		case tagClientKey:
			dst = &c.ClientKey
		default:
			c.Zero()
			return nil, errCorruptCredentials
		}
		if *dst != nil {
			c.Zero()
			return nil, errCorruptCredentials
		}
		*dst = append([]byte(nil), rest[:n]...)
		rest = rest[n:]
	}
	return c, nil
}

// credentialsAAD binds the sealed credentials to their row.
func credentialsAAD(id uuid.UUID) crypto.AAD {
	return crypto.AAD{Table: "connections", RowID: id.String(), Field: "secret"}
}

// sealCredentials seals c for the connection id; nil when c is empty.
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
		return nil, fmt.Errorf("connections: seal credentials: %w", err)
	}
	return out, nil
}

// openCredentials opens the sealed credentials of the connection id (an
// empty Credentials when sealed is empty). The caller zeroes the result.
func openCredentials(kr *crypto.Keyring, id uuid.UUID, sealed []byte) (*Credentials, error) {
	if len(sealed) == 0 {
		return &Credentials{}, nil
	}
	plain, err := kr.Open(sealed, credentialsAAD(id))
	if err != nil {
		return nil, fmt.Errorf("connections: open credentials: %w", err)
	}
	defer crypto.Zero(plain)
	return unmarshalCredentials(plain)
}
