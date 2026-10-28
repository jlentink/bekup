package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"filippo.io/age"

	"github.com/jlentink/bekup/internal/crypto"
)

// EncryptionMode is how an artifact is encrypted.
type EncryptionMode string

// Encryption modes (D-008). The zero value is invalid, so a forgotten
// configuration fails closed instead of writing plaintext.
const (
	// EncryptPassphrase: age scrypt recipient with a versioned per-job
	// passphrase.
	EncryptPassphrase EncryptionMode = "passphrase"
	// EncryptRecipients: age X25519 recipients; the identities are never
	// held by bekup.
	EncryptRecipients EncryptionMode = "recipients"
	// EncryptNone: no encryption. Only valid with Encryption.OptedOut set
	// (an admin opted the job out: step-up, audited, badge, alert).
	EncryptNone EncryptionMode = "none"
)

// MaxRecipients bounds the number of age recipients of one artifact.
const MaxRecipients = 16

// Encryption errors.
var (
	// ErrEncryptionConfig reports an invalid or missing encryption
	// configuration (including "none" without an explicit opt-out).
	ErrEncryptionConfig = errors.New("pipeline: invalid encryption configuration")
)

// Encryption is the per-run encryption configuration. Task 3.2 (job
// encryption keys) produces it through an [Encryptor]; the pipeline only
// consumes it.
//
// The passphrase is borrowed: Run never retains or destroys it, so the
// caller destroys it after Run returns.
type Encryption struct {
	Mode EncryptionMode
	// Passphrase and PassphraseVersion are set for EncryptPassphrase. The
	// version is recorded in the manifest so restores pick the right one.
	Passphrase        *crypto.Secret
	PassphraseVersion int
	// WorkFactor is the scrypt work factor (0 = crypto.DefaultScryptWorkFactor;
	// at most crypto.MaxScryptWorkFactor).
	WorkFactor int
	// Recipients are age X25519 recipients ("age1...") for
	// EncryptRecipients; RecipientVersion is the job's recipient key
	// version (task 3.2), recorded in the manifest like the passphrase
	// version.
	Recipients       []string
	RecipientVersion int
	// OptedOut must be true for EncryptNone: the job's encryption opt-out
	// was confirmed by an admin (D-008, D-001).
	OptedOut bool
}

// Encryptor yields the encryption configuration of a job for one run. Task
// 3.2 implements it on top of the versioned job passphrases and recipient
// keys. The returned passphrase (if any) is owned by the caller of
// BackupEncryption, who destroys it after the run.
type Encryptor interface {
	BackupEncryption(ctx context.Context) (Encryption, error)
}

// PassphraseEncryption returns a passphrase configuration (borrowing p).
func PassphraseEncryption(p *crypto.Secret, version int) Encryption {
	return Encryption{Mode: EncryptPassphrase, Passphrase: p, PassphraseVersion: version}
}

// RecipientEncryption returns a recipient configuration for the job's
// recipient key version.
func RecipientEncryption(version int, recipients ...string) Encryption {
	return Encryption{Mode: EncryptRecipients, RecipientVersion: version, Recipients: append([]string(nil), recipients...)}
}

// OptedOutEncryption returns the "none" configuration of a job whose
// encryption opt-out an admin confirmed.
func OptedOutEncryption() Encryption {
	return Encryption{Mode: EncryptNone, OptedOut: true}
}

// String never prints the passphrase.
func (e Encryption) String() string {
	switch e.Mode {
	case EncryptPassphrase:
		return fmt.Sprintf("Encryption{passphrase v%d}", e.PassphraseVersion)
	case EncryptRecipients:
		return fmt.Sprintf("Encryption{%d recipients v%d}", len(e.Recipients), e.RecipientVersion)
	case EncryptNone:
		return "Encryption{none}"
	default:
		return "Encryption{invalid}"
	}
}

// GoString never prints the passphrase.
func (e Encryption) GoString() string { return e.String() }

// Validate checks the configuration. It fails closed: an unknown mode,
// "none" without the opt-out flag, a missing passphrase or an unparsable
// recipient are errors.
func (e Encryption) Validate() error {
	switch e.Mode {
	case EncryptPassphrase:
		if e.Passphrase.Len() == 0 {
			return fmt.Errorf("%w: empty passphrase", ErrEncryptionConfig)
		}
		if e.PassphraseVersion < 1 {
			return fmt.Errorf("%w: passphrase version must be at least 1", ErrEncryptionConfig)
		}
		if e.WorkFactor != 0 && (e.WorkFactor < crypto.DefaultScryptWorkFactor || e.WorkFactor > crypto.MaxScryptWorkFactor) {
			return fmt.Errorf("%w: scrypt work factor must be %d..%d", ErrEncryptionConfig,
				crypto.DefaultScryptWorkFactor, crypto.MaxScryptWorkFactor)
		}
		if len(e.Recipients) != 0 || e.RecipientVersion != 0 || e.OptedOut {
			return fmt.Errorf("%w: passphrase mode takes no recipients or opt-out", ErrEncryptionConfig)
		}
	case EncryptRecipients:
		if len(e.Recipients) == 0 || len(e.Recipients) > MaxRecipients {
			return fmt.Errorf("%w: 1..%d recipients required", ErrEncryptionConfig, MaxRecipients)
		}
		if e.RecipientVersion < 1 {
			return fmt.Errorf("%w: recipient version must be at least 1", ErrEncryptionConfig)
		}
		for i, r := range e.Recipients {
			if _, err := age.ParseX25519Recipient(r); err != nil {
				return fmt.Errorf("%w: recipient %d is not an age X25519 recipient", ErrEncryptionConfig, i+1)
			}
		}
		if e.Passphrase.Len() != 0 || e.PassphraseVersion != 0 || e.OptedOut {
			return fmt.Errorf("%w: recipient mode takes no passphrase or opt-out", ErrEncryptionConfig)
		}
	case EncryptNone:
		if !e.OptedOut {
			return fmt.Errorf("%w: unencrypted backups need an explicit admin opt-out", ErrEncryptionConfig)
		}
		if e.Passphrase.Len() != 0 || len(e.Recipients) != 0 || e.PassphraseVersion != 0 || e.RecipientVersion != 0 {
			return fmt.Errorf("%w: mode none takes no keys", ErrEncryptionConfig)
		}
	default:
		return fmt.Errorf("%w: unknown mode", ErrEncryptionConfig)
	}
	return nil
}

// manifestEncryption returns the manifest description of the configuration.
func (e Encryption) manifestEncryption() EntryEncryption {
	out := EntryEncryption{Mode: e.Mode}
	switch e.Mode {
	case EncryptPassphrase:
		out.PassphraseVersion = e.PassphraseVersion
	case EncryptRecipients:
		out.RecipientVersion = e.RecipientVersion
		for _, r := range e.Recipients {
			out.RecipientFingerprints = append(out.RecipientFingerprints, RecipientFingerprint(r))
		}
	case EncryptNone:
	}
	return out
}

// newEncryptWriter wraps dst according to the configuration. For
// EncryptNone it returns a no-op closer around dst.
func (e Encryption) newEncryptWriter(dst io.Writer) (io.WriteCloser, error) {
	switch e.Mode {
	case EncryptPassphrase:
		return crypto.EncryptWithPassphrase(dst, e.Passphrase.Bytes(), e.WorkFactor)
	case EncryptRecipients:
		return crypto.EncryptToRecipients(dst, e.Recipients...)
	case EncryptNone:
		return nopWriteCloser{dst}, nil
	default:
		return nil, ErrEncryptionConfig
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// RecipientFingerprint is the manifest fingerprint of an age X25519
// recipient: the lowercase hex SHA-256 of its canonical (lowercase)
// "age1..." string. Unparsable input is fingerprinted as given (lowercased),
// so the function never fails; Validate rejects such recipients before use.
func RecipientFingerprint(recipient string) string {
	s := strings.ToLower(strings.TrimSpace(recipient))
	if r, err := age.ParseX25519Recipient(s); err == nil {
		s = r.String()
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
