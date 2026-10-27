package crypto

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
)

// hkdfInfoMACPrefix prefixes the HKDF info of keyed-hash subkeys; the
// purpose follows it, so every purpose has its own subkey and none of them
// relates to the encryption or key-check subkeys.
const hkdfInfoMACPrefix = "bekup/v1/mac/hmac-sha256/"

// macPurposeRe is the syntax of a MAC purpose.
var macPurposeRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// ErrInvalidMACPurpose means a MAC purpose is empty or malformed.
var ErrInvalidMACPurpose = errors.New("invalid MAC purpose")

// MAC returns HMAC-SHA256 of msg under a subkey of the primary key derived
// for purpose (HKDF-SHA256), and the ID of that key. Store the key ID next
// to the MAC and check later values with MACWith: the MAC stays verifiable
// after a key rotation as long as the old key is in the keyring.
//
// Use it for high-entropy values that must be recognized but not stored
// (recovery codes): without the master key, a leaked MAC cannot be tested
// offline against guesses.
func (k *Keyring) MAC(purpose string, msg []byte) (KeyID, []byte, error) {
	sum, err := k.MACWith(k.primary, purpose, msg)
	if err != nil {
		return KeyID{}, nil, err
	}
	return k.primary, sum, nil
}

// MACWith returns HMAC-SHA256 of msg under the purpose subkey of key id.
// Compare results with hmac.Equal.
func (k *Keyring) MACWith(id KeyID, purpose string, msg []byte) ([]byte, error) {
	if !macPurposeRe.MatchString(purpose) {
		return nil, ErrInvalidMACPurpose
	}
	mk, ok := k.keys[id]
	if !ok || mk.raw == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnknownKeyID, id)
	}
	sub, err := hkdf.Key(sha256.New, mk.raw, nil, hkdfInfoMACPrefix+purpose, KeySize)
	if err != nil {
		return nil, fmt.Errorf("deriving MAC key: %w", err)
	}
	defer Zero(sub)
	h := hmac.New(sha256.New, sub)
	h.Write(msg)
	return h.Sum(nil), nil
}
