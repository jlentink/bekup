package crypto

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"

	"filippo.io/age"
)

// Thin wrappers around filippo.io/age for the backup pipeline and the
// offline tools. Passphrase encryption uses age's scrypt recipient.
//
// scrypt memory use is 128 * r * 2^workFactor bytes with r = 8:
// 2^18 = 256 MiB, 2^20 = 1 GiB. A malicious age header can request any work
// factor, so decryption always sets SetMaxWorkFactor; anything above the
// bound is rejected before scrypt runs.
const (
	// DefaultScryptWorkFactor is the log2 scrypt work factor bekup writes
	// (age's default).
	DefaultScryptWorkFactor = 18
	// MaxScryptWorkFactor is the hard upper bound for both writing and
	// reading.
	MaxScryptWorkFactor = 20
)

// minScryptWorkFactor is the lowest work factor EncryptWithPassphrase
// accepts. Tests lower it to keep scrypt fast.
var minScryptWorkFactor = DefaultScryptWorkFactor

// ErrAgeIdentity is returned for unparsable or unsupported age identities.
var ErrAgeIdentity = errors.New("invalid age identity")

// EncryptWithPassphrase returns a writer that age-encrypts to dst with an
// scrypt passphrase recipient. workFactor 0 means DefaultScryptWorkFactor.
// Close the writer to flush the final chunk.
func EncryptWithPassphrase(dst io.Writer, passphrase []byte, workFactor int) (io.WriteCloser, error) {
	if workFactor == 0 {
		workFactor = DefaultScryptWorkFactor
	}
	if workFactor < minScryptWorkFactor || workFactor > MaxScryptWorkFactor {
		return nil, fmt.Errorf("scrypt work factor must be %d..%d", minScryptWorkFactor, MaxScryptWorkFactor)
	}
	if len(passphrase) == 0 {
		return nil, errors.New("empty passphrase")
	}
	// age takes the passphrase as a string, which cannot be zeroed.
	r, err := age.NewScryptRecipient(string(passphrase))
	if err != nil {
		return nil, fmt.Errorf("age scrypt recipient: %w", err)
	}
	r.SetWorkFactor(workFactor)
	w, err := age.Encrypt(dst, r)
	if err != nil {
		return nil, fmt.Errorf("age encrypt: %w", err)
	}
	return w, nil
}

// DecryptWithPassphrase returns a reader of the plaintext of an
// scrypt-encrypted age stream. maxWorkFactor 0 means
// DefaultScryptWorkFactor; it may not exceed MaxScryptWorkFactor. Headers
// asking for more are rejected without running scrypt.
func DecryptWithPassphrase(src io.Reader, passphrase []byte, maxWorkFactor int) (io.Reader, error) {
	if maxWorkFactor == 0 {
		maxWorkFactor = DefaultScryptWorkFactor
	}
	if maxWorkFactor < 1 || maxWorkFactor > MaxScryptWorkFactor {
		return nil, fmt.Errorf("maximum scrypt work factor must be 1..%d", MaxScryptWorkFactor)
	}
	if len(passphrase) == 0 {
		return nil, errors.New("empty passphrase")
	}
	id, err := age.NewScryptIdentity(string(passphrase))
	if err != nil {
		return nil, fmt.Errorf("age scrypt identity: %w", err)
	}
	id.SetMaxWorkFactor(maxWorkFactor)
	r, err := age.Decrypt(src, id)
	if err != nil {
		return nil, fmt.Errorf("age decrypt: %w", err)
	}
	return r, nil
}

// EncryptToRecipients returns a writer that age-encrypts to dst for one or
// more X25519 recipients ("age1..."). Close the writer to flush.
func EncryptToRecipients(dst io.Writer, recipients ...string) (io.WriteCloser, error) {
	if len(recipients) == 0 {
		return nil, errors.New("no age recipients")
	}
	rs := make([]age.Recipient, 0, len(recipients))
	for i, s := range recipients {
		r, err := age.ParseX25519Recipient(s)
		if err != nil {
			return nil, fmt.Errorf("age recipient %d: %w", i+1, err)
		}
		rs = append(rs, r)
	}
	w, err := age.Encrypt(dst, rs...)
	if err != nil {
		return nil, fmt.Errorf("age encrypt: %w", err)
	}
	return w, nil
}

// ParseAgeIdentities parses X25519 identities ("AGE-SECRET-KEY-1..."), one
// per line; blank lines and '#' comments are skipped (the age identity file
// format). Only X25519 identities are accepted: no scrypt or plugin
// identities, so decryption never runs an unbounded KDF or external program.
func ParseAgeIdentities(data []byte) ([]age.Identity, error) {
	var ids []age.Identity
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 1024), 1024)
	n := 0
	for sc.Scan() {
		n++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		id, err := age.ParseX25519Identity(string(line))
		if err != nil {
			// Never echo the line: it may be (a mangled) private key.
			return nil, fmt.Errorf("%w: line %d is not an X25519 identity", ErrAgeIdentity, n)
		}
		ids = append(ids, id)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAgeIdentity, err)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: no identities", ErrAgeIdentity)
	}
	return ids, nil
}

// DecryptWithIdentities returns a reader of the plaintext of an age stream
// encrypted to X25519 recipients. identities is identity-file content (see
// ParseAgeIdentities); the caller should Zero it afterwards.
func DecryptWithIdentities(src io.Reader, identities []byte) (io.Reader, error) {
	ids, err := ParseAgeIdentities(identities)
	if err != nil {
		return nil, err
	}
	r, err := age.Decrypt(src, ids...)
	if err != nil {
		return nil, fmt.Errorf("age decrypt: %w", err)
	}
	return r, nil
}
