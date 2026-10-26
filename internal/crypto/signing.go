package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sync"
)

// Instance signing keys (ed25519) sign backup manifests and audit chain
// heads. A key's ID is derived from its public key: the first 8 bytes of
// SHA-256(public key) in lowercase hex (16 characters), so an ID can never
// be paired with the wrong public key. The private key (its 32-byte seed)
// is stored sealed with the keyring under AAD "instance_keys:<id>:private".
//
// Signatures are domain-separated: the signed bytes are
//
//	"bekup-sig-v1" 0x00 <purpose> 0x00 <message>
//
// so a manifest signature can never be replayed as, for example, an audit
// chain-head signature.
const (
	signingKeyIDLen = 8
	sigDomain       = "bekup-sig-v1"
)

// Signing errors.
var (
	ErrBadSignature   = errors.New("signature verification failed")
	ErrInvalidPurpose = errors.New("invalid signature purpose")
	ErrSigningKey     = errors.New("invalid signing key")
)

var purposeRe = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

// SigningKeyID returns the key ID for an ed25519 public key.
func SigningKeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:signingKeyIDLen])
}

// FingerprintHex returns the lowercase hex SHA-256 of a public key.
func FingerprintHex(pub []byte) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])
}

// FingerprintBase64 returns "SHA256:" + unpadded standard base64 of the
// SHA-256 of a public key (the OpenSSH fingerprint style).
func FingerprintBase64(pub []byte) string {
	sum := sha256.Sum256(pub)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// SigningKey is an ed25519 instance key with its ID.
type SigningKey struct {
	ID     string
	Public ed25519.PublicKey
	priv   ed25519.PrivateKey
}

// GenerateSigningKey creates a new ed25519 instance key from crypto/rand.
func GenerateSigningKey() (*SigningKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating ed25519 key: %w", err)
	}
	return &SigningKey{ID: SigningKeyID(pub), Public: pub, priv: priv}, nil
}

// SigningKeyAAD returns the AAD under which a signing key's seed is sealed.
func SigningKeyAAD(id string) AAD {
	return AAD{Table: "instance_keys", RowID: id, Field: "private"}
}

// SealPrivate returns the private key seed sealed with the keyring's
// primary key under SigningKeyAAD(k.ID).
func (k *SigningKey) SealPrivate(kr *Keyring) ([]byte, error) {
	if k.priv == nil {
		return nil, ErrSigningKey
	}
	seed := k.priv.Seed()
	defer Zero(seed)
	return kr.Seal(seed, SigningKeyAAD(k.ID))
}

// OpenSigningKey unseals a signing key stored with SealPrivate and checks
// that it matches the stored ID and public key.
func OpenSigningKey(kr *Keyring, id string, pub ed25519.PublicKey, sealed []byte) (*SigningKey, error) {
	if len(pub) != ed25519.PublicKeySize || SigningKeyID(pub) != id {
		return nil, fmt.Errorf("%w: id does not match public key", ErrSigningKey)
	}
	seed, err := kr.Open(sealed, SigningKeyAAD(id))
	if err != nil {
		return nil, err
	}
	defer Zero(seed)
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("%w: bad seed length", ErrSigningKey)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	derived, ok := priv.Public().(ed25519.PublicKey)
	if !ok || subtle.ConstantTimeCompare(derived, pub) != 1 {
		Zero(priv)
		return nil, fmt.Errorf("%w: private key does not match public key", ErrSigningKey)
	}
	return &SigningKey{ID: id, Public: derived, priv: priv}, nil
}

// Sign signs message for purpose (domain separation, see package docs).
func (k *SigningKey) Sign(purpose string, message []byte) ([]byte, error) {
	if k.priv == nil {
		return nil, ErrSigningKey
	}
	msg, err := signedMessage(purpose, message)
	if err != nil {
		return nil, err
	}
	return ed25519.Sign(k.priv, msg), nil
}

// Destroy zeroes the private key.
func (k *SigningKey) Destroy() {
	Zero(k.priv)
	k.priv = nil
}

// String redacts the private key.
func (k *SigningKey) String() string {
	return "SigningKey{id=" + k.ID + " fingerprint=" + FingerprintBase64(k.Public) + "}"
}

// GoString redacts the private key.
func (k *SigningKey) GoString() string { return k.String() }

func signedMessage(purpose string, message []byte) ([]byte, error) {
	if !purposeRe.MatchString(purpose) {
		return nil, fmt.Errorf("%w: must match %s", ErrInvalidPurpose, purposeRe)
	}
	msg := make([]byte, 0, len(sigDomain)+len(purpose)+2+len(message))
	msg = append(msg, sigDomain...)
	msg = append(msg, 0)
	msg = append(msg, purpose...)
	msg = append(msg, 0)
	return append(msg, message...), nil
}

// Verifier holds every known instance public key (current and retired) by
// ID. It is safe for concurrent use.
type Verifier struct {
	mu   sync.RWMutex
	keys map[string]ed25519.PublicKey
}

// NewVerifier returns an empty verifier.
func NewVerifier() *Verifier {
	return &Verifier{keys: make(map[string]ed25519.PublicKey)}
}

// Add registers a public key under its derived ID and returns that ID.
func (v *Verifier) Add(pub ed25519.PublicKey) (string, error) {
	if len(pub) != ed25519.PublicKeySize {
		return "", fmt.Errorf("%w: public key must be %d bytes", ErrSigningKey, ed25519.PublicKeySize)
	}
	id := SigningKeyID(pub)
	v.mu.Lock()
	v.keys[id] = append(ed25519.PublicKey(nil), pub...)
	v.mu.Unlock()
	return id, nil
}

// Len returns the number of registered keys.
func (v *Verifier) Len() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.keys)
}

// Verify checks sig over message for purpose with the key id.
func (v *Verifier) Verify(id, purpose string, message, sig []byte) error {
	v.mu.RLock()
	pub, ok := v.keys[id]
	v.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: signing key %q", ErrUnknownKeyID, id)
	}
	msg, err := signedMessage(purpose, message)
	if err != nil {
		return err
	}
	if len(sig) != ed25519.SignatureSize || !ed25519.Verify(pub, msg, sig) {
		return ErrBadSignature
	}
	return nil
}
