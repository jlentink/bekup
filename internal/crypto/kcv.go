package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// Key-check value (KCV) format:
//
//	kcv1:<key-id>:<base64url, no padding, of HMAC-SHA256(kcv-key, "bekup key-check value v1")>
//
// The kcv-key is derived from the master key with HKDF-SHA256 under its own
// info string, so a KCV reveals nothing about the encryption subkey. The
// value is stored in the app DB settings and verified at startup, which
// catches a wrong or truncated keyring before anything is decrypted.
const (
	kcvPrefix  = "kcv1"
	kcvMessage = "bekup key-check value v1"
)

// KCV errors.
var (
	ErrKCVMismatch  = errors.New("key-check value mismatch: the keyring does not contain the key this database was encrypted with")
	ErrMalformedKCV = errors.New("malformed key-check value")
)

// KeyCheckValue returns the KCV of the primary key.
func (k *Keyring) KeyCheckValue() string {
	v, err := k.KeyCheckValueFor(k.primary)
	if err != nil {
		return "" // destroyed keyring
	}
	return v
}

// KeyCheckValueFor returns the KCV of the key id.
func (k *Keyring) KeyCheckValueFor(id KeyID) (string, error) {
	mk, ok := k.keys[id]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownKeyID, id)
	}
	return kcvPrefix + ":" + id.String() + ":" + base64.RawURLEncoding.EncodeToString(mk.kcv()), nil
}

func (m *masterKey) kcv() []byte {
	h := hmac.New(sha256.New, m.kcvKey)
	h.Write([]byte(kcvMessage))
	return h.Sum(nil)
}

// VerifyKeyCheckValue checks a stored KCV against the keyring. It returns
// ErrUnknownKeyID if the key it refers to is missing from the keyring and
// ErrKCVMismatch if the key material differs. The comparison is constant
// time.
func (k *Keyring) VerifyKeyCheckValue(stored string) error {
	parts := strings.Split(stored, ":")
	if len(parts) != 3 || parts[0] != kcvPrefix {
		return ErrMalformedKCV
	}
	id, err := ParseKeyID(parts[1])
	if err != nil {
		return ErrMalformedKCV
	}
	want, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil || len(want) != sha256.Size {
		return ErrMalformedKCV
	}
	mk, ok := k.keys[id]
	if !ok {
		return fmt.Errorf("%w: %s (key-check value)", ErrUnknownKeyID, id)
	}
	if !hmac.Equal(mk.kcv(), want) {
		return ErrKCVMismatch
	}
	return nil
}
