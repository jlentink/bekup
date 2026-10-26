package crypto

import (
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
)

// Ciphertext format (version 1), all fields concatenated:
//
//	version (1 byte, 0x01) | key id (4 bytes) | nonce (12 random bytes) | AES-256-GCM ciphertext + 16-byte tag
//
// The GCM additional data is the 5-byte header (version | key id) followed
// by the AAD string "table:row_id:field", so neither the header nor the
// record binding can be changed without failing authentication.
const (
	ciphertextV1 byte = 0x01
	nonceSize         = 12
	tagSize           = 16
	headerSize        = 1 + len(KeyID{})
	// Overhead is the number of bytes Seal adds to the plaintext.
	Overhead = headerSize + nonceSize + tagSize
	// MaxPlaintextSize bounds what Seal accepts; credentials are small.
	MaxPlaintextSize = 1 << 20
)

// Encryption errors. ErrDecrypt deliberately covers every authentication
// failure (tampering, wrong key material, wrong AAD) without detail.
var (
	ErrMalformedCiphertext = errors.New("malformed ciphertext")
	ErrUnsupportedVersion  = errors.New("unsupported ciphertext version")
	ErrDecrypt             = errors.New("decryption failed: ciphertext, key or AAD mismatch")
	ErrInvalidAAD          = errors.New("invalid AAD")
)

var (
	aadNameRe  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	aadRowIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// AAD binds a ciphertext to the place it is stored: table, row ID and
// field. Every component is restricted to a character set without ':' so
// the encoding "table:row_id:field" is unambiguous.
type AAD struct {
	Table string // ^[a-z][a-z0-9_]{0,62}$
	RowID string // ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ (UUID, integer, ...)
	Field string // ^[a-z][a-z0-9_]{0,62}$
}

// NewAAD returns a validated AAD.
func NewAAD(table, rowID, field string) (AAD, error) {
	a := AAD{Table: table, RowID: rowID, Field: field}
	return a, a.Validate()
}

// Validate checks every component.
func (a AAD) Validate() error {
	if !aadNameRe.MatchString(a.Table) {
		return fmt.Errorf("%w: table must match %s", ErrInvalidAAD, aadNameRe)
	}
	if !aadRowIDRe.MatchString(a.RowID) {
		return fmt.Errorf("%w: row id must match %s", ErrInvalidAAD, aadRowIDRe)
	}
	if !aadNameRe.MatchString(a.Field) {
		return fmt.Errorf("%w: field must match %s", ErrInvalidAAD, aadNameRe)
	}
	return nil
}

// String returns "table:row_id:field".
func (a AAD) String() string { return a.Table + ":" + a.RowID + ":" + a.Field }

func additionalData(header []byte, a AAD) []byte {
	s := a.String()
	ad := make([]byte, 0, len(header)+len(s))
	ad = append(ad, header...)
	return append(ad, s...)
}

// Seal encrypts plaintext with the primary key, bound to aad.
func (k *Keyring) Seal(plaintext []byte, aad AAD) ([]byte, error) {
	if err := aad.Validate(); err != nil {
		return nil, err
	}
	if len(plaintext) > MaxPlaintextSize {
		return nil, fmt.Errorf("plaintext larger than %d bytes", MaxPlaintextSize)
	}
	mk, ok := k.keys[k.primary]
	if !ok {
		return nil, fmt.Errorf("%w: no primary key", ErrUnknownKeyID)
	}
	out := make([]byte, headerSize+nonceSize, Overhead+len(plaintext))
	out[0] = ciphertextV1
	copy(out[1:headerSize], mk.id[:])
	// Random 96-bit nonces: safe for well below 2^32 messages per key,
	// far beyond the credential volume of one instance.
	_, _ = rand.Read(out[headerSize : headerSize+nonceSize]) // crypto/rand.Read never returns an error
	return mk.aead.Seal(out, out[headerSize:headerSize+nonceSize], plaintext, additionalData(out[:headerSize], aad)), nil
}

// Open decrypts a ciphertext produced by Seal with the same aad. Any key in
// the keyring can decrypt. The caller owns the returned plaintext and should
// Zero it after use.
func (k *Keyring) Open(ciphertext []byte, aad AAD) ([]byte, error) {
	if err := aad.Validate(); err != nil {
		return nil, err
	}
	id, err := KeyIDOf(ciphertext)
	if err != nil {
		return nil, err
	}
	mk, ok := k.keys[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownKeyID, id)
	}
	nonce := ciphertext[headerSize : headerSize+nonceSize]
	pt, err := mk.aead.Open(nil, nonce, ciphertext[headerSize+nonceSize:], additionalData(ciphertext[:headerSize], aad))
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

// KeyIDOf returns the key ID recorded in a ciphertext header after checking
// the version and minimum length. It does not authenticate the ciphertext.
func KeyIDOf(ciphertext []byte) (KeyID, error) {
	var id KeyID
	if len(ciphertext) < Overhead {
		return id, ErrMalformedCiphertext
	}
	if ciphertext[0] != ciphertextV1 {
		return id, fmt.Errorf("%w: %d", ErrUnsupportedVersion, ciphertext[0])
	}
	copy(id[:], ciphertext[1:headerSize])
	return id, nil
}

// NeedsRotation reports whether ciphertext was sealed with a key other than
// the current primary.
func (k *Keyring) NeedsRotation(ciphertext []byte) (bool, error) {
	id, err := KeyIDOf(ciphertext)
	if err != nil {
		return false, err
	}
	return id != k.primary, nil
}

// Rotate re-encrypts ciphertext under the primary key. If it is already
// sealed with the primary key it is authenticated and returned unchanged
// (rotated == false). The plaintext is zeroed before returning.
func (k *Keyring) Rotate(ciphertext []byte, aad AAD) (out []byte, rotated bool, err error) {
	pt, err := k.Open(ciphertext, aad) // also validates the header
	if err != nil {
		return nil, false, err
	}
	defer Zero(pt)
	if ciphertext[0] == ciphertextV1 && KeyID(ciphertext[1:headerSize]) == k.primary {
		return ciphertext, false, nil
	}
	out, err = k.Seal(pt, aad)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// SealedField is a stored ciphertext together with its AAD, used by
// RotateAll.
type SealedField struct {
	AAD        AAD
	Ciphertext []byte
}

// RotateAll re-encrypts every field not yet under the primary key, in
// place, and returns how many changed. It stops at the first error, leaving
// earlier fields rotated; callers persist results transactionally (wired to
// the database in a later task).
func (k *Keyring) RotateAll(fields []SealedField) (changed int, err error) {
	for i := range fields {
		out, rotated, err := k.Rotate(fields[i].Ciphertext, fields[i].AAD)
		if err != nil {
			return changed, fmt.Errorf("field %d (%s): %w", i, fields[i].AAD, err)
		}
		if rotated {
			fields[i].Ciphertext = out
			changed++
		}
	}
	return changed, nil
}
