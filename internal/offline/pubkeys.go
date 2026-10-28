package offline

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/pipeline"
)

// Public key file format (`bekup keys export-public`):
//
//	bekup-instance-public-keys v1
//	# comments
//	key <key id> <standard base64 of the 32-byte ed25519 key> created <RFC 3339> [retired <RFC 3339>]
//
// One key per line. The key ID must be SigningKeyID of the key. A file (or
// a --pubkey value) holding just one bare base64 key is accepted too.
const (
	// PublicKeysHeader is the first line of the public key file format.
	PublicKeysHeader = "bekup-instance-public-keys v1"
	// MaxPublicKeys bounds the keys of one file.
	MaxPublicKeys = 256
	maxKeyLineLen = 1024
)

// ErrPublicKeys reports an unusable public key file or value.
var ErrPublicKeys = errors.New("offline: invalid instance public keys")

// PublicKey is an instance signing public key with its metadata.
type PublicKey struct {
	ID        string
	Key       ed25519.PublicKey
	CreatedAt time.Time  // zero when unknown
	RetiredAt *time.Time // nil: active (or unknown)
}

// FingerprintHex is the lower-case hex SHA-256 of the key.
func (k PublicKey) FingerprintHex() string { return crypto.FingerprintHex(k.Key) }

// Fingerprint is "SHA256:<base64>" of the key.
func (k PublicKey) Fingerprint() string { return crypto.FingerprintBase64(k.Key) }

func pubErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrPublicKeys, fmt.Sprintf(format, args...))
}

// FormatPublicKeys writes keys in the public key file format.
func FormatPublicKeys(w io.Writer, keys []PublicKey, exportedAt time.Time) error {
	var b strings.Builder
	b.WriteString(PublicKeysHeader + "\n")
	b.WriteString("# bekup instance public keys (manifest and audit chain-head signatures). Not secret, but\n")
	b.WriteString("# integrity-relevant: escrow this file with the master keyring and compare the fingerprints\n")
	b.WriteString("# with an independent record before trusting it. See docs/recovery.md.\n")
	b.WriteString("# exported " + exportedAt.UTC().Format(time.RFC3339) + "\n")
	for _, k := range keys {
		if len(k.Key) != ed25519.PublicKeySize || crypto.SigningKeyID(k.Key) != k.ID {
			return pubErr("key %s does not match its id", k.ID)
		}
		state := "active"
		if k.RetiredAt != nil {
			state = "retired"
		}
		fmt.Fprintf(&b, "# %s (%s): fingerprint %s, hex %s\n", k.ID, state, k.Fingerprint(), k.FingerprintHex())
		b.WriteString("key " + k.ID + " " + base64.StdEncoding.EncodeToString(k.Key))
		if !k.CreatedAt.IsZero() {
			b.WriteString(" created " + k.CreatedAt.UTC().Format(time.RFC3339))
		}
		if k.RetiredAt != nil {
			b.WriteString(" retired " + k.RetiredAt.UTC().Format(time.RFC3339))
		}
		b.WriteString("\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// decodeBareKey decodes standard (padded or unpadded) base64 of a 32-byte
// ed25519 public key.
func decodeBareKey(s string) (ed25519.PublicKey, bool) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding.Strict(), base64.RawStdEncoding.Strict()} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == ed25519.PublicKeySize {
			return ed25519.PublicKey(b), true
		}
	}
	return nil, false
}

// ParsePublicKeys parses the public key file format, or a single bare
// base64 key.
func ParsePublicKeys(data []byte) ([]PublicKey, error) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 256), maxKeyLineLen)
	var lines []string
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	if err := sc.Err(); err != nil {
		return nil, pubErr("line %d: %v", n+1, err)
	}
	if len(lines) == 0 {
		return nil, pubErr("no keys")
	}
	if lines[0] != PublicKeysHeader {
		if len(lines) == 1 {
			if pub, ok := decodeBareKey(lines[0]); ok {
				return []PublicKey{{ID: crypto.SigningKeyID(pub), Key: pub}}, nil
			}
		}
		return nil, pubErr("not a public key file (first line must be %q) or a base64 ed25519 key", PublicKeysHeader)
	}
	var out []PublicKey
	for i, line := range lines[1:] {
		k, err := parseKeyLine(line)
		if err != nil {
			return nil, fmt.Errorf("key line %d: %w", i+1, err)
		}
		if slices.ContainsFunc(out, func(o PublicKey) bool { return o.ID == k.ID }) {
			return nil, pubErr("key %s listed twice", k.ID)
		}
		out = append(out, k)
		if len(out) > MaxPublicKeys {
			return nil, pubErr("more than %d keys", MaxPublicKeys)
		}
	}
	if len(out) == 0 {
		return nil, pubErr("no keys")
	}
	return out, nil
}

func parseKeyLine(line string) (PublicKey, error) {
	f := strings.Fields(line)
	if len(f) < 3 || f[0] != "key" || len(f)%2 != 1 {
		return PublicKey{}, pubErr("expected: key <id> <base64> [created <time>] [retired <time>]")
	}
	pub, ok := decodeBareKey(f[2])
	if !ok {
		return PublicKey{}, pubErr("invalid base64 ed25519 public key")
	}
	k := PublicKey{ID: f[1], Key: pub}
	if crypto.SigningKeyID(pub) != k.ID {
		return PublicKey{}, pubErr("key id %q does not match the public key", SafeText(k.ID))
	}
	for i := 3; i+1 < len(f); i += 2 {
		t, err := time.Parse(time.RFC3339, f[i+1])
		if err != nil {
			return PublicKey{}, pubErr("invalid %s time", SafeText(f[i]))
		}
		switch f[i] {
		case "created":
			k.CreatedAt = t.UTC()
		case "retired":
			u := t.UTC()
			k.RetiredAt = &u
		default:
			return PublicKey{}, pubErr("unknown attribute %q", SafeText(f[i]))
		}
	}
	return k, nil
}

// KeySet is a set of instance public keys for verification.
type KeySet struct {
	Keys []PublicKey
}

// Verifier returns the keys as a pipeline.KeyVerifier.
func (s *KeySet) Verifier() pipeline.PublicKeys {
	m := make(pipeline.PublicKeys, len(s.Keys))
	for _, k := range s.Keys {
		m[k.ID] = k.Key
	}
	return m
}

// Lookup returns the key with the given ID.
func (s *KeySet) Lookup(id string) (PublicKey, bool) {
	for _, k := range s.Keys {
		if k.ID == id {
			return k, true
		}
	}
	return PublicKey{}, false
}

// Add adds keys, ignoring exact duplicates.
func (s *KeySet) Add(keys ...PublicKey) {
	for _, k := range keys {
		if _, ok := s.Lookup(k.ID); !ok {
			s.Keys = append(s.Keys, k)
		}
	}
}

// LoadPublicKeys builds a key set from --pubkey values: each value is a
// bare base64 ed25519 key or the path of a public key file (read with the
// file safety rules of config.ReadPublicFile).
func LoadPublicKeys(values []string) (*KeySet, error) {
	set := &KeySet{}
	for _, v := range values {
		if pub, ok := decodeBareKey(v); ok {
			set.Add(PublicKey{ID: crypto.SigningKeyID(pub), Key: pub})
			continue
		}
		data, err := config.ReadPublicFile(v)
		if err != nil {
			return nil, fmt.Errorf("--pubkey: %w", err)
		}
		keys, err := ParsePublicKeys(data)
		if err != nil {
			return nil, fmt.Errorf("--pubkey %s: %w", v, err)
		}
		set.Add(keys...)
	}
	return set, nil
}
