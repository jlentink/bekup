package crypto

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Keyring file format (version 1). A UTF-8 text file:
//
//	# comments and blank lines are ignored
//	bekup-keyring v1
//	key <key-id> <created RFC 3339 UTC> <base64 (standard, padded) of 32 random bytes>
//	key ...
//	primary <key-id>
//
// The header line must be the first non-comment line. Key IDs are 8
// lowercase hex characters (4 random bytes). There is exactly one primary
// line, naming one of the keys: the primary key encrypts, every key
// decrypts. Duplicate IDs, duplicate key material, unknown directives and
// malformed values are rejected. See docs/security/key-management.md.
const (
	keyringHeader = "bekup-keyring v1"
	// KeySize is the size of a master key in bytes (AES-256).
	KeySize = 32
	// MaxKeys bounds the number of keys in a keyring.
	MaxKeys = 256
)

// HKDF info strings: every master key is only ever used through subkeys
// derived for a single purpose (domain separation).
const (
	hkdfInfoEnc = "bekup/v1/credentials/aes-256-gcm"
	hkdfInfoKCV = "bekup/v1/key-check-value/hmac-sha256"
)

// Keyring errors. Error messages never contain key material.
var (
	ErrKeyringFormat = errors.New("invalid keyring")
	ErrUnknownKeyID  = errors.New("unknown key id")
)

// KeyID identifies a master key. It is stored in every ciphertext.
type KeyID [4]byte

// String returns the 8-character lowercase hex form.
func (id KeyID) String() string { return hex.EncodeToString(id[:]) }

// ParseKeyID parses the 8-character lowercase hex form.
func ParseKeyID(s string) (KeyID, error) {
	var id KeyID
	if len(s) != 2*len(id) || strings.ToLower(s) != s {
		return id, fmt.Errorf("%w: key id must be 8 lowercase hex characters", ErrKeyringFormat)
	}
	if _, err := hex.Decode(id[:], []byte(s)); err != nil {
		return id, fmt.Errorf("%w: key id must be 8 lowercase hex characters", ErrKeyringFormat)
	}
	return id, nil
}

type masterKey struct {
	id      KeyID
	created time.Time
	raw     []byte
	aead    cipher.AEAD
	kcvKey  []byte
}

func newMasterKey(id KeyID, created time.Time, raw []byte) (*masterKey, error) {
	if len(raw) != KeySize {
		return nil, fmt.Errorf("%w: key %s is not %d bytes", ErrKeyringFormat, id, KeySize)
	}
	encKey, err := hkdf.Key(sha256.New, raw, nil, hkdfInfoEnc, KeySize)
	if err != nil {
		return nil, fmt.Errorf("deriving encryption key: %w", err)
	}
	// The AES block keeps its own expanded key schedule, which cannot be
	// zeroed from Go; the derived key itself is zeroed right away.
	block, err := aes.NewCipher(encKey)
	Zero(encKey)
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}
	kcvKey, err := hkdf.Key(sha256.New, raw, nil, hkdfInfoKCV, KeySize)
	if err != nil {
		return nil, fmt.Errorf("deriving key-check key: %w", err)
	}
	return &masterKey{id: id, created: created.UTC().Truncate(time.Second), raw: raw, aead: aead, kcvKey: kcvKey}, nil
}

func (m *masterKey) destroy() {
	Zero(m.raw)
	Zero(m.kcvKey)
	m.aead = nil
}

// Keyring holds the master keys used for credential encryption. It is safe
// for concurrent use by Seal/Open/key-check calls; mutating methods
// (AddPrimary, Destroy) must not run concurrently with other calls.
type Keyring struct {
	keys    map[KeyID]*masterKey
	order   []KeyID // file order, oldest first
	primary KeyID
}

// GenerateKeyring returns a new keyring with a single, primary key.
func GenerateKeyring(now time.Time) (*Keyring, error) {
	k := &Keyring{keys: make(map[KeyID]*masterKey)}
	if _, err := k.AddPrimary(now); err != nil {
		return nil, err
	}
	return k, nil
}

// AddPrimary generates a new random key, adds it and makes it the primary.
// Existing keys stay available for decryption.
func (k *Keyring) AddPrimary(now time.Time) (KeyID, error) {
	if len(k.keys) >= MaxKeys {
		return KeyID{}, fmt.Errorf("%w: at most %d keys", ErrKeyringFormat, MaxKeys)
	}
	var id KeyID
	for {
		_, _ = rand.Read(id[:]) // crypto/rand.Read never returns an error
		if _, dup := k.keys[id]; !dup {
			break
		}
	}
	raw := make([]byte, KeySize)
	_, _ = rand.Read(raw)
	mk, err := newMasterKey(id, now, raw)
	if err != nil {
		Zero(raw)
		return KeyID{}, err
	}
	k.keys[id] = mk
	k.order = append(k.order, id)
	k.primary = id
	return id, nil
}

// PrimaryID returns the ID of the primary (encrypting) key.
func (k *Keyring) PrimaryID() KeyID { return k.primary }

// IDs returns all key IDs, oldest first.
func (k *Keyring) IDs() []KeyID { return slices.Clone(k.order) }

// Has reports whether the keyring contains id.
func (k *Keyring) Has(id KeyID) bool {
	_, ok := k.keys[id]
	return ok
}

// Created returns the creation time recorded for id.
func (k *Keyring) Created(id KeyID) (time.Time, bool) {
	mk, ok := k.keys[id]
	if !ok {
		return time.Time{}, false
	}
	return mk.created, true
}

// Destroy zeroes all key material. The keyring is unusable afterwards.
func (k *Keyring) Destroy() {
	for _, mk := range k.keys {
		mk.destroy()
	}
	k.keys = map[KeyID]*masterKey{}
	k.order = nil
}

// String redacts the keyring; only the key IDs are shown.
func (k *Keyring) String() string {
	ids := make([]string, 0, len(k.order))
	for _, id := range k.order {
		ids = append(ids, id.String())
	}
	return fmt.Sprintf("Keyring{primary=%s keys=[%s]}", k.primary, strings.Join(ids, " "))
}

// GoString redacts the keyring.
func (k *Keyring) GoString() string { return k.String() }

// Marshal encodes the keyring in the version 1 file format. The returned
// buffer contains key material: Zero it after use.
func (k *Keyring) Marshal() ([]byte, error) {
	if _, ok := k.keys[k.primary]; !ok {
		return nil, fmt.Errorf("%w: no primary key", ErrKeyringFormat)
	}
	var buf bytes.Buffer
	buf.Grow(128 + 96*len(k.order))
	buf.WriteString("# bekup master keyring. SECRET: keep this file 0600 and escrow an offline copy.\n")
	buf.WriteString("# Losing every copy makes all stored credentials undecryptable.\n")
	buf.WriteString("# Format: docs/security/key-management.md\n")
	buf.WriteString(keyringHeader + "\n")
	b64 := make([]byte, base64.StdEncoding.EncodedLen(KeySize))
	defer Zero(b64)
	for _, id := range k.order {
		mk := k.keys[id]
		base64.StdEncoding.Encode(b64, mk.raw)
		buf.WriteString("key ")
		buf.WriteString(id.String())
		buf.WriteByte(' ')
		buf.WriteString(mk.created.UTC().Format(time.RFC3339))
		buf.WriteByte(' ')
		buf.Write(b64)
		buf.WriteByte('\n')
	}
	buf.WriteString("primary " + k.primary.String() + "\n")
	return buf.Bytes(), nil
}

// ParseKeyring parses a keyring in the version 1 file format. It does not
// modify data; the caller should Zero it afterwards. Errors name the line
// number but never echo line contents, which may contain key material.
func ParseKeyring(data []byte) (_ *Keyring, retErr error) {
	k := &Keyring{keys: make(map[KeyID]*masterKey)}
	defer func() {
		if retErr != nil {
			k.Destroy()
		}
	}()
	var (
		sawHeader  bool
		sawPrimary bool
		lineNo     int
		raw        = make([]byte, base64.StdEncoding.DecodedLen(base64.StdEncoding.EncodedLen(KeySize)))
	)
	defer Zero(raw)
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 4096), 4096)
	for sc.Scan() {
		lineNo++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		if !sawHeader {
			if string(line) != keyringHeader {
				return nil, fmt.Errorf("%w: line %d: expected header %q", ErrKeyringFormat, lineNo, keyringHeader)
			}
			sawHeader = true
			continue
		}
		fields := bytes.Fields(line)
		switch string(fields[0]) {
		case "key":
			if len(fields) != 4 {
				return nil, fmt.Errorf("%w: line %d: want \"key <id> <created> <base64>\"", ErrKeyringFormat, lineNo)
			}
			if len(k.keys) >= MaxKeys {
				return nil, fmt.Errorf("%w: line %d: more than %d keys", ErrKeyringFormat, lineNo, MaxKeys)
			}
			id, err := ParseKeyID(string(fields[1]))
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNo, err)
			}
			if _, dup := k.keys[id]; dup {
				return nil, fmt.Errorf("%w: line %d: duplicate key id %s", ErrKeyringFormat, lineNo, id)
			}
			created, err := time.Parse(time.RFC3339, string(fields[2]))
			if err != nil {
				return nil, fmt.Errorf("%w: line %d: created time is not RFC 3339", ErrKeyringFormat, lineNo)
			}
			if len(fields[3]) != base64.StdEncoding.EncodedLen(KeySize) {
				return nil, fmt.Errorf("%w: line %d: key %s is not base64 of %d bytes", ErrKeyringFormat, lineNo, id, KeySize)
			}
			n, err := base64.StdEncoding.Strict().Decode(raw, fields[3])
			if err != nil || n != KeySize {
				return nil, fmt.Errorf("%w: line %d: key %s is not base64 of %d bytes", ErrKeyringFormat, lineNo, id, KeySize)
			}
			for _, other := range k.keys {
				if bytes.Equal(other.raw, raw[:KeySize]) {
					return nil, fmt.Errorf("%w: line %d: key %s duplicates the material of key %s", ErrKeyringFormat, lineNo, id, other.id)
				}
			}
			mk, err := newMasterKey(id, created, bytes.Clone(raw[:KeySize]))
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNo, err)
			}
			k.keys[id] = mk
			k.order = append(k.order, id)
		case "primary":
			if len(fields) != 2 {
				return nil, fmt.Errorf("%w: line %d: want \"primary <id>\"", ErrKeyringFormat, lineNo)
			}
			if sawPrimary {
				return nil, fmt.Errorf("%w: line %d: more than one primary line", ErrKeyringFormat, lineNo)
			}
			id, err := ParseKeyID(string(fields[1]))
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNo, err)
			}
			k.primary = id
			sawPrimary = true
		default:
			return nil, fmt.Errorf("%w: line %d: unknown directive", ErrKeyringFormat, lineNo)
		}
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return nil, fmt.Errorf("%w: line %d too long", ErrKeyringFormat, lineNo+1)
		}
		return nil, fmt.Errorf("%w: %w", ErrKeyringFormat, err)
	}
	switch {
	case !sawHeader:
		return nil, fmt.Errorf("%w: missing header %q", ErrKeyringFormat, keyringHeader)
	case len(k.keys) == 0:
		return nil, fmt.Errorf("%w: no keys", ErrKeyringFormat)
	case !sawPrimary:
		return nil, fmt.Errorf("%w: no primary line", ErrKeyringFormat)
	case !k.Has(k.primary):
		return nil, fmt.Errorf("%w: primary %s is not a key in this keyring", ErrKeyringFormat, k.primary)
	}
	return k, nil
}
