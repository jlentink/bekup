package db

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"hash"
	"regexp"
	"strconv"
)

// SCRAM-SHA-256 password verifiers for CREATE/ALTER ROLE ... PASSWORD
// (D-207). PostgreSQL stores a string in exactly this format as the
// verifier (it never sees the password), so `bekup db roles-sql` and
// `bekup db bootstrap` never send or print a plaintext password.
const (
	// SCRAMIterations matches PostgreSQL's default scram_iterations.
	SCRAMIterations = 4096
	scramSaltLen    = 16
	// MinRolePasswordLen is the minimum length of a password that bekup
	// turns into a verifier (the generated passwords are much longer).
	MinRolePasswordLen = 16
	maxRolePasswordLen = 1024
)

// scramVerifierRe is the exact verifier format SCRAMVerifier produces:
// SCRAM-SHA-256$<iterations>:<salt>$<StoredKey>:<ServerKey> with standard
// padded base64. Only strings matching it are ever quoted into SQL.
var scramVerifierRe = regexp.MustCompile(`^SCRAM-SHA-256\$[1-9][0-9]{3,6}:[A-Za-z0-9+/]{22}==\$[A-Za-z0-9+/]{43}=:[A-Za-z0-9+/]{43}=$`)

// ErrRolePassword is returned for passwords bekup will not turn into a
// verifier.
var ErrRolePassword = errors.New("role password unsuitable")

// CheckRolePassword enforces the rules for role passwords: 16 to 1024
// printable ASCII characters. ASCII keeps the verifier identical to what
// PostgreSQL and pgx compute after SASLprep (RFC 4013 maps and normalizes
// non-ASCII text, so the same password could otherwise hash differently).
// The error never contains the password.
func CheckRolePassword(pw []byte) error {
	if len(pw) < MinRolePasswordLen || len(pw) > maxRolePasswordLen {
		return fmt.Errorf("%w: it must be %d to %d characters long (got %d)", ErrRolePassword, MinRolePasswordLen, maxRolePasswordLen, len(pw))
	}
	for _, c := range pw {
		if c < 0x20 || c > 0x7e {
			return fmt.Errorf("%w: only printable ASCII characters are supported (SASLprep compatibility)", ErrRolePassword)
		}
	}
	return nil
}

// NewSCRAMVerifier returns the SCRAM-SHA-256 verifier of pw with a random
// salt and SCRAMIterations. The verifier is sensitive (it allows an offline
// guessing attack on the password): print it only to stdout, never log it.
func NewSCRAMVerifier(pw []byte) (string, error) {
	salt := make([]byte, scramSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate SCRAM salt: %w", err)
	}
	return SCRAMVerifier(pw, salt, SCRAMIterations)
}

// SCRAMVerifier computes the verifier of pw for salt and iterations
// (RFC 5802 / RFC 7677): SaltedPassword = PBKDF2-HMAC-SHA-256(pw, salt, i),
// StoredKey = SHA-256(HMAC(SaltedPassword, "Client Key")), ServerKey =
// HMAC(SaltedPassword, "Server Key").
func SCRAMVerifier(pw, salt []byte, iterations int) (string, error) {
	if err := CheckRolePassword(pw); err != nil {
		return "", err
	}
	if len(salt) != scramSaltLen || iterations < SCRAMIterations || iterations > 9_999_999 {
		return "", errors.New("SCRAM verifier: invalid salt or iteration count")
	}
	clientKey, stored, server := scramKeys(pw, salt, iterations)
	clear(clientKey)
	enc := base64.StdEncoding
	v := "SCRAM-SHA-256$" + strconv.Itoa(iterations) + ":" + enc.EncodeToString(salt) + "$" +
		enc.EncodeToString(stored) + ":" + enc.EncodeToString(server)
	if !scramVerifierRe.MatchString(v) {
		return "", errors.New("SCRAM verifier: unexpected format")
	}
	return v, nil
}

// scramKeys derives ClientKey (only the RFC 7677 test needs it; callers
// clear it), StoredKey and ServerKey.
func scramKeys(pw, salt []byte, iterations int) (clientKey, stored, server []byte) {
	salted := pbkdf2SHA256(pw, salt, iterations)
	defer clear(salted)
	m := newHMACSHA256(salted)
	defer m.destroy()
	clientKey = m.sum([]byte("Client Key"))
	sum := sha256.Sum256(clientKey)
	stored = bytes.Clone(sum[:])
	clear(sum[:])
	return clientKey, stored, m.sum([]byte("Server Key"))
}

// pbkdf2SHA256 is PBKDF2-HMAC-SHA-256 with a 32-byte output (one block,
// RFC 8018 section 5.2). It is written out instead of using crypto/pbkdf2,
// whose API takes the password as an immutable string that cannot be
// zeroed.
func pbkdf2SHA256(pw, salt []byte, iterations int) []byte {
	m := newHMACSHA256(pw)
	defer m.destroy()
	u := m.sum(salt, []byte{0, 0, 0, 1})
	t := bytes.Clone(u)
	for i := 1; i < iterations; i++ {
		next := m.sum(u)
		clear(u)
		u = next
		for j := range t {
			t[j] ^= u[j]
		}
	}
	clear(u)
	return t
}

// hmacSHA256 is HMAC-SHA-256 (RFC 2104) on byte slices bekup owns, so the
// key-derived pads can be zeroed (destroy): crypto/hmac keeps copies of
// them in unexported state that cannot be cleared. Each sum runs fresh
// SHA-256 states that are Reset (their chaining values overwritten) after
// use.
type hmacSHA256 struct {
	ipad, opad [sha256.BlockSize]byte
	inner      hash.Hash
	outer      hash.Hash
}

func newHMACSHA256(key []byte) *hmacSHA256 {
	m := &hmacSHA256{inner: sha256.New(), outer: sha256.New()}
	var k0 [sha256.BlockSize]byte
	if len(key) > sha256.BlockSize {
		sum := sha256.Sum256(key)
		copy(k0[:], sum[:])
		clear(sum[:])
	} else {
		copy(k0[:], key)
	}
	for i := range k0 {
		m.ipad[i] = k0[i] ^ 0x36
		m.opad[i] = k0[i] ^ 0x5c
	}
	clear(k0[:])
	return m
}

// sum returns HMAC(key, msg...) in a new slice.
func (m *hmacSHA256) sum(msg ...[]byte) []byte {
	m.inner.Reset()
	m.inner.Write(m.ipad[:])
	for _, p := range msg {
		m.inner.Write(p)
	}
	var in [sha256.Size]byte
	m.inner.Sum(in[:0])
	m.inner.Reset()
	m.outer.Reset()
	m.outer.Write(m.opad[:])
	m.outer.Write(in[:])
	clear(in[:])
	out := m.outer.Sum(nil)
	m.outer.Reset()
	return out
}

// destroy zeroes the pads and resets the hash states.
func (m *hmacSHA256) destroy() {
	clear(m.ipad[:])
	clear(m.opad[:])
	m.inner.Reset()
	m.outer.Reset()
}

// validSCRAMVerifier reports whether v has exactly the format SCRAMVerifier
// produces (and is therefore safe inside a single-quoted SQL literal).
func validSCRAMVerifier(v string) bool { return scramVerifierRe.MatchString(v) }
