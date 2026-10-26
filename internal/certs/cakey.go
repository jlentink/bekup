package certs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"filippo.io/age"
	"filippo.io/age/armor"
)

// The CA private key is stored as PKCS#8 PEM, sealed with an age scrypt
// passphrase recipient (work factor 18, D-088) and ASCII-armored (D-090).
// An unencrypted key is only written when explicitly requested.

const (
	// MinPassphraseLen is the minimum CA key passphrase length in bytes.
	MinPassphraseLen = 16
	// maxPassphraseLen bounds the passphrase (and scrypt input).
	maxPassphraseLen = 1024
	// defaultScryptWorkFactor is age's default (2^18).
	defaultScryptWorkFactor = 18
	// maxEncryptedKeyBytes bounds the decrypted output.
	maxDecryptedKeyBytes = 64 << 10

	ageArmorHeader = "-----BEGIN AGE ENCRYPTED FILE-----"
)

// ErrPassphraseRequired is returned when the CA key is encrypted and no
// passphrase was supplied.
var ErrPassphraseRequired = errors.New("the CA key is encrypted: a passphrase is required")

// ValidatePassphrase checks the passphrase policy.
func ValidatePassphrase(p []byte) error {
	if len(p) < MinPassphraseLen {
		return fmt.Errorf("passphrase too short: at least %d characters required", MinPassphraseLen)
	}
	if len(p) > maxPassphraseLen {
		return fmt.Errorf("passphrase too long: at most %d bytes", maxPassphraseLen)
	}
	if !utf8.Valid(p) {
		return errors.New("passphrase is not valid UTF-8")
	}
	return nil
}

// IsEncryptedKey reports whether data is an age-armored (encrypted) key.
func IsEncryptedKey(data []byte) bool {
	return bytes.HasPrefix(bytes.TrimSpace(data), []byte(ageArmorHeader))
}

// sealKey encrypts keyPEM with passphrase. workFactor 0 means the default.
func sealKey(keyPEM, passphrase []byte, workFactor int) ([]byte, error) {
	if err := ValidatePassphrase(passphrase); err != nil {
		return nil, err
	}
	r, err := age.NewScryptRecipient(string(passphrase))
	if err != nil {
		return nil, fmt.Errorf("age recipient: %w", err)
	}
	if workFactor == 0 {
		workFactor = defaultScryptWorkFactor
	}
	r.SetWorkFactor(workFactor)
	var buf bytes.Buffer
	aw := armor.NewWriter(&buf)
	w, err := age.Encrypt(aw, r)
	if err != nil {
		return nil, fmt.Errorf("age encrypt: %w", err)
	}
	if _, err := w.Write(keyPEM); err != nil {
		return nil, fmt.Errorf("age encrypt: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("age encrypt: %w", err)
	}
	if err := aw.Close(); err != nil {
		return nil, fmt.Errorf("age armor: %w", err)
	}
	return buf.Bytes(), nil
}

// openKey returns the PKCS#8 PEM in data, decrypting it when it is sealed.
func openKey(data, passphrase []byte) ([]byte, error) {
	if !IsEncryptedKey(data) {
		return bytes.Clone(data), nil
	}
	if len(passphrase) == 0 {
		return nil, ErrPassphraseRequired
	}
	id, err := age.NewScryptIdentity(string(passphrase))
	if err != nil {
		return nil, fmt.Errorf("age identity: %w", err)
	}
	// Bound the scrypt work a crafted header can demand (D-088).
	id.SetMaxWorkFactor(defaultScryptWorkFactor)
	r, err := age.Decrypt(armor.NewReader(bytes.NewReader(data)), id)
	if err != nil {
		return nil, fmt.Errorf("decrypting the CA key (wrong passphrase?): %w", err)
	}
	out, err := io.ReadAll(io.LimitReader(r, maxDecryptedKeyBytes+1))
	if err != nil {
		clear(out)
		return nil, fmt.Errorf("decrypting the CA key: %w", err)
	}
	if len(out) > maxDecryptedKeyBytes {
		clear(out)
		return nil, errors.New("decrypted CA key too large")
	}
	return out, nil
}
