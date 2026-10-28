package jobkeys

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"filippo.io/age"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// Limits of user input.
const (
	// MinPassphraseRunes is the minimum length of a passphrase chosen by a
	// user. Backup files are exposed to offline guessing for as long as
	// they are kept, so the bar is higher than for account passwords.
	MinPassphraseRunes = 20
	// MaxPassphraseBytes bounds a passphrase chosen by a user.
	MaxPassphraseBytes = 1024
	// MinPassphraseScore is the minimum zxcvbn score (of 4) of a passphrase
	// chosen by a user.
	MinPassphraseScore = 4
	// MaxLabelRunes bounds a recipient label.
	MaxLabelRunes = 100
	// MaxReasonRunes bounds an opt-out reason.
	MaxReasonRunes  = 500
	maxRecipientLen = 128
)

var recipientRe = regexp.MustCompile(`^age1[02-9ac-hj-np-z]{58}$`)

func fieldErr(field, format string, a ...any) error { return serviceutil.FieldErr(field, format, a...) }

// validatePassphrase checks a passphrase chosen by a user. Messages never
// contain the passphrase.
func validatePassphrase(p []byte) error {
	const field = "passphrase"
	if len(p) > MaxPassphraseBytes {
		return fieldErr(field, "the passphrase must be at most %d bytes", MaxPassphraseBytes)
	}
	if !utf8.Valid(p) {
		return fieldErr(field, "the passphrase must be valid UTF-8")
	}
	n := 0
	for _, r := range string(p) {
		if unicode.IsControl(r) {
			return fieldErr(field, "the passphrase must not contain control characters")
		}
		n++
	}
	if n < MinPassphraseRunes {
		return fieldErr(field, "the passphrase must be at least %d characters; generate one instead", MinPassphraseRunes)
	}
	if strings.TrimSpace(string(p)) != string(p) {
		return fieldErr(field, "the passphrase must not start or end with white space")
	}
	if auth.Strength(string(p), nil) < MinPassphraseScore {
		return fieldErr(field, "the passphrase is too easy to guess; use a longer one or generate one")
	}
	return nil
}

// parseRecipient validates an age X25519 recipient and returns its
// canonical form and fingerprint. Only X25519 recipients are accepted (no
// SSH, plugin or post-quantum recipients): restores and the offline tools
// decrypt with X25519 identities only (D-088).
func parseRecipient(s string) (canonical, fingerprint string, err error) {
	const field = "recipient"
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", fieldErr(field, "an age recipient (age1...) is required")
	}
	if len(s) > maxRecipientLen {
		return "", "", fieldErr(field, "not an age X25519 recipient (age1...)")
	}
	r, perr := age.ParseX25519Recipient(s)
	if perr != nil {
		return "", "", fieldErr(field, "not an age X25519 recipient (age1...)")
	}
	canonical = r.String()
	if !recipientRe.MatchString(canonical) {
		return "", "", fieldErr(field, "not an age X25519 recipient (age1...)")
	}
	return canonical, Fingerprint(canonical), nil
}

// Fingerprint returns the fingerprint of a canonical age recipient: the
// lowercase hex SHA-256 of the recipient string, the same value the backup
// manifest records (pipeline.RecipientFingerprint). It identifies the
// identity a restore needs.
func Fingerprint(recipient string) string {
	sum := sha256.Sum256([]byte(recipient))
	return hex.EncodeToString(sum[:])
}

// cleanText trims s and checks its length and characters.
func cleanText(field, s string, minRunes, maxRunes int) (string, error) {
	s = strings.TrimSpace(s)
	if !utf8.ValidString(s) {
		return "", fieldErr(field, "must be valid UTF-8")
	}
	n := utf8.RuneCountInString(s)
	if n < minRunes {
		return "", fieldErr(field, "is required")
	}
	if n > maxRunes {
		return "", fieldErr(field, "must be at most %d characters", maxRunes)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", fieldErr(field, "must not contain control characters")
		}
	}
	return s, nil
}
