package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// Format constants (D-220 item 8).
const (
	// Prefix starts every token (secret scanners match it).
	Prefix = "bkp_"
	// IDChars and SecretChars are the base32 lengths of the id (16 bytes)
	// and the secret (32 bytes).
	IDChars     = 26
	SecretChars = 52
	// Len is the length of a token.
	Len = len(Prefix) + IDChars + 1 + SecretChars
	// SecretBytes is the size of the random secret.
	SecretBytes = 32
	// HashBytes is the size of the stored hash.
	HashBytes = sha256.Size
	// hashContext separates the hash from every other SHA-256 use.
	hashContext = "bekup-api-token-v1\x00"
)

// encoding is unpadded lower-case RFC 4648 base32 (a-z, 2-7).
var encoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// ErrMalformed is returned for anything that is not a canonical token. It
// never carries the input.
var ErrMalformed = errors.New("apikey: not a bekup API token")

// Key is a parsed token: the public id and the secret. Zero it once the
// hash is computed.
type Key struct {
	ID     uuid.UUID
	secret [SecretBytes]byte
}

// Hash returns the stored form of the key (HashOf).
func (k *Key) Hash() [HashBytes]byte { return HashOf(k.ID, k.secret[:]) }

// Zero overwrites the secret.
func (k *Key) Zero() { clear(k.secret[:]) }

// HashOf is SHA-256 over "bekup-api-token-v1\x00" ‖ id ‖ secret.
func HashOf(id uuid.UUID, secret []byte) [HashBytes]byte {
	h := sha256.New()
	// hash.Hash.Write never returns an error.
	_, _ = h.Write([]byte(hashContext))
	_, _ = h.Write(id[:])
	_, _ = h.Write(secret)
	var out [HashBytes]byte
	h.Sum(out[:0])
	return out
}

// Parse checks the canonical format and decodes a token. Any deviation
// (length, prefix, separator, alphabet, non-canonical trailing bits) is
// ErrMalformed; the error never includes the input.
func Parse(s string) (Key, error) {
	if len(s) != Len || !strings.HasPrefix(s, Prefix) || s[len(Prefix)+IDChars] != '_' {
		return Key{}, ErrMalformed
	}
	idText := s[len(Prefix) : len(Prefix)+IDChars]
	secretText := s[len(Prefix)+IDChars+1:]
	var k Key
	if !decodeCanonical(k.ID[:], idText) || !decodeCanonical(k.secret[:], secretText) {
		k.Zero()
		return Key{}, ErrMalformed
	}
	return k, nil
}

// decodeCanonical decodes text into exactly len(dst) bytes and accepts
// only the canonical spelling (re-encoding gives the same text).
func decodeCanonical(dst []byte, text string) bool {
	for i := range len(text) {
		c := text[i]
		if (c < 'a' || c > 'z') && (c < '2' || c > '7') {
			return false
		}
	}
	if encoding.DecodedLen(len(text)) != len(dst) {
		return false
	}
	n, err := encoding.Decode(dst, []byte(text))
	if err != nil || n != len(dst) {
		clear(dst)
		return false
	}
	if encoding.EncodeToString(dst) != text {
		clear(dst)
		return false
	}
	return true
}

// Generated is a new token: show Plaintext once, store ID and Hash.
type Generated struct {
	ID        uuid.UUID
	Plaintext string
	Hash      [HashBytes]byte
}

// Generate creates a token with a random uuid and a 256-bit secret from
// crypto/rand.
func Generate() (Generated, error) {
	var secret [SecretBytes]byte
	defer clear(secret[:])
	// crypto/rand.Read never returns an error (it panics instead).
	_, _ = rand.Read(secret[:])
	id, err := uuid.NewRandomFromReader(rand.Reader)
	if err != nil {
		return Generated{}, err
	}
	return Generated{ID: id, Plaintext: Format(id, secret[:]), Hash: HashOf(id, secret[:])}, nil
}

// Format spells a token; secret must be SecretBytes long.
func Format(id uuid.UUID, secret []byte) string {
	if len(secret) != SecretBytes {
		panic("apikey: secret must be 32 bytes")
	}
	return Prefix + encoding.EncodeToString(id[:]) + "_" + encoding.EncodeToString(secret)
}

// DisplayPrefixChars is how many characters of the id part DisplayPrefix
// shows.
const DisplayPrefixChars = 8

// DisplayPrefix is how the UI names a token so its owner can recognize it
// in a CI secret store: the prefix and the first characters of the public
// id part ("bkp_aaisem2e"). It never contains anything of the secret.
func DisplayPrefix(id uuid.UUID) string {
	return Prefix + encoding.EncodeToString(id[:])[:DisplayPrefixChars]
}

// Contains reports whether s may carry a token (the prefix appears in it,
// in any case). Logs redact such values instead of printing them.
func Contains(s string) bool {
	return strings.Contains(strings.ToLower(s), Prefix)
}
