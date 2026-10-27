package sshpolicy

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Limits shared by every SSH client and the services storing SSH settings.
const (
	// MinRSABits is the smallest accepted RSA host or client key.
	MinRSABits = 2048
	// MaxAuthorizedKeyLineLen bounds an authorized_keys line entered or
	// stored as a host-key pin (a 16 kbit RSA key is about 2.8 KiB).
	MaxAuthorizedKeyLineLen = 8 << 10
	// MaxPrivateKeyLen bounds a PEM private key (a 16 kbit RSA key is about
	// 12 KiB).
	MaxPrivateKeyLen = 64 << 10
	// MaxPasswordLen bounds an SSH password and a key passphrase.
	MaxPasswordLen = 1024
	// MaxUserLen bounds an SSH user name.
	MaxUserLen = 128
)

// FingerprintPrefix is the OpenSSH prefix of a SHA-256 fingerprint.
const FingerprintPrefix = "SHA256:"

// Error kinds of the host-key policy.
var (
	// ErrInvalidPin: the pin is missing or malformed.
	ErrInvalidPin = errors.New("invalid ssh host key pin")
	// ErrUnsupportedHostKey: the host key type or size is not accepted (for
	// example ssh-dss or RSA below 2048 bits).
	ErrUnsupportedHostKey = errors.New("ssh host key type is not supported")
	// ErrHostKeyMismatch: the server presented another host key than the
	// pinned one, or does not offer the pinned key type. A security event
	// (possible impersonation / MITM). The concrete error is a
	// *MismatchError.
	ErrHostKeyMismatch = errors.New("ssh host key does not match the pinned key")
	// ErrMalformedHostKeyLine: an authorized_keys host-key line does not
	// parse (or carries options, more than one key or line breaks).
	ErrMalformedHostKeyLine = errors.New("malformed ssh host key line")
)

// policyError is a static, secret-free message classified by kind.
type policyError struct {
	kind error
	msg  string
}

func (e *policyError) Error() string { return e.msg }
func (e *policyError) Unwrap() error { return e.kind }

func errorf(kind error, msg string) error { return &policyError{kind: kind, msg: msg} }

// hostKeyAlgos maps an accepted host-key type to the signature algorithms
// offered for it. RSA keys are only accepted with SHA-2 signatures.
var hostKeyAlgos = map[string][]string{
	ssh.KeyAlgoED25519:  {ssh.KeyAlgoED25519},
	ssh.KeyAlgoECDSA256: {ssh.KeyAlgoECDSA256},
	ssh.KeyAlgoECDSA384: {ssh.KeyAlgoECDSA384},
	ssh.KeyAlgoECDSA521: {ssh.KeyAlgoECDSA521},
	ssh.KeyAlgoRSA:      {ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256},
}

// fetchHostKeyAlgos is the preference order offered when capturing a host
// key for trust on first use.
var fetchHostKeyAlgos = []string{
	ssh.KeyAlgoED25519,
	ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521,
	ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256,
}

// Algorithms returns the only transport algorithms negotiated.
func Algorithms() ssh.Config {
	return ssh.Config{
		KeyExchanges: []string{ssh.KeyExchangeMLKEM768X25519, ssh.KeyExchangeCurve25519},
		Ciphers: []string{
			ssh.CipherChaCha20Poly1305, ssh.CipherAES256GCM, ssh.CipherAES128GCM,
		},
		MACs: []string{ssh.HMACSHA256ETM, ssh.HMACSHA512ETM},
	}
}

// AcceptedHostKeyType reports whether typ is an accepted host-key type.
func AcceptedHostKeyType(typ string) bool {
	_, ok := hostKeyAlgos[typ]
	return ok
}

// HostKeyAlgorithms returns the signature algorithms offered for a pinned
// key type (nil for a type that is not accepted). The caller may modify
// the result.
func HostKeyAlgorithms(typ string) []string {
	return slices.Clone(hostKeyAlgos[typ])
}

// FetchHostKeyAlgorithms returns the host-key algorithms offered when a
// key is captured for trust on first use (every accepted type).
func FetchHostKeyAlgorithms() []string {
	return slices.Clone(fetchHostKeyAlgos)
}

// CheckHostKey reports whether key is an accepted host-key type and size.
// The error wraps ErrUnsupportedHostKey.
func CheckHostKey(key ssh.PublicKey) error {
	if !AcceptedHostKeyType(key.Type()) {
		return errorf(ErrUnsupportedHostKey, "host key type "+strconv.Quote(key.Type())+" not accepted")
	}
	if key.Type() == ssh.KeyAlgoRSA {
		ck, ok := key.(ssh.CryptoPublicKey)
		if !ok {
			return errorf(ErrUnsupportedHostKey, "unreadable RSA host key")
		}
		rk, ok := ck.CryptoPublicKey().(*rsa.PublicKey)
		if !ok || rk.N == nil || rk.N.BitLen() < MinRSABits {
			return errorf(ErrUnsupportedHostKey, "RSA host key shorter than 2048 bits")
		}
	}
	return nil
}

// Pin pins a server's host key. Set Type plus FingerprintSHA256 (OpenSSH
// format "SHA256:<base64>"), or AuthorizedKey (an authorized_keys style
// line "<type> <base64> [comment]"), or both (they must agree).
type Pin struct {
	Type              string
	FingerprintSHA256 string
	AuthorizedKey     string
}

// NormalizedPin is a validated pin.
type NormalizedPin struct {
	typ string
	sum [sha256.Size]byte
}

// Type is the pinned key type.
func (p NormalizedPin) Type() string { return p.typ }

// Fingerprint is the pinned key's OpenSSH SHA-256 fingerprint.
func (p NormalizedPin) Fingerprint() string {
	return FingerprintPrefix + base64.RawStdEncoding.EncodeToString(p.sum[:])
}

// Algorithms returns the host-key algorithms to offer for the pin.
func (p NormalizedPin) Algorithms() []string { return HostKeyAlgorithms(p.typ) }

// IsZero reports an unset pin.
func (p NormalizedPin) IsZero() bool { return p.typ == "" }

// Matches reports (in constant time for the digest) whether key is the
// pinned key.
func (p NormalizedPin) Matches(key ssh.PublicKey) bool {
	sum := sha256.Sum256(key.Marshal())
	return key.Type() == p.typ && subtle.ConstantTimeCompare(sum[:], p.sum[:]) == 1
}

// Validate checks the pin without connecting. The error is secret-free
// and wraps ErrInvalidPin or ErrUnsupportedHostKey.
func (p Pin) Validate() error {
	_, err := p.Normalize()
	return err
}

// Normalize validates p. Errors wrap ErrInvalidPin or
// ErrUnsupportedHostKey.
func (p Pin) Normalize() (NormalizedPin, error) {
	var out NormalizedPin
	typ := strings.TrimSpace(p.Type)
	fp := strings.TrimSpace(p.FingerprintSHA256)
	line := strings.TrimSpace(p.AuthorizedKey)
	if fp == "" && line == "" {
		return out, errorf(ErrInvalidPin, "a host key pin is required")
	}
	if typ != "" && !AcceptedHostKeyType(typ) {
		return out, errorf(ErrUnsupportedHostKey, "pinned key type not accepted")
	}
	haveLine := false
	if line != "" {
		if len(line) > MaxAuthorizedKeyLineLen {
			return out, errorf(ErrInvalidPin, "host key pin: authorized key too long")
		}
		key, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil || strings.TrimSpace(string(rest)) != "" {
			return out, errorf(ErrInvalidPin, "host key pin: malformed authorized key")
		}
		if err := CheckHostKey(key); err != nil {
			return out, err
		}
		if typ != "" && typ != key.Type() {
			return out, errorf(ErrInvalidPin, "host key pin: type does not match the authorized key")
		}
		out.typ = key.Type()
		out.sum = sha256.Sum256(key.Marshal())
		haveLine = true
	}
	if fp != "" {
		sum, err := parseFingerprint(fp)
		if err != nil {
			return out, err
		}
		if haveLine {
			if subtle.ConstantTimeCompare(sum[:], out.sum[:]) != 1 {
				return out, errorf(ErrInvalidPin, "host key pin: fingerprint does not match the authorized key")
			}
			return out, nil
		}
		if typ == "" {
			return out, errorf(ErrInvalidPin, "host key pin: a key type is required with a fingerprint")
		}
		out.typ = typ
		out.sum = sum
	}
	return out, nil
}

// parseFingerprint parses "SHA256:<base64>" (padding optional).
func parseFingerprint(fp string) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	b64, ok := strings.CutPrefix(fp, FingerprintPrefix)
	if !ok {
		return sum, errorf(ErrInvalidPin, "host key pin: fingerprint must start with SHA256:")
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(b64, "="))
	if err != nil || len(raw) != sha256.Size {
		return sum, errorf(ErrInvalidPin, "host key pin: malformed SHA256 fingerprint")
	}
	copy(sum[:], raw)
	return sum, nil
}

// HostKeyInfo describes a host key (public data).
type HostKeyInfo struct {
	// Type is the key type, e.g. "ssh-ed25519".
	Type string
	// FingerprintSHA256 in OpenSSH format ("SHA256:<base64>").
	FingerprintSHA256 string
	// AuthorizedKey is the key as an authorized_keys line (no comment).
	AuthorizedKey string
}

// Pin returns the pin for this key. Only use it after an admin confirmed
// the fingerprint out of band.
func (h HostKeyInfo) Pin() Pin { return Pin(h) }

// InfoOf describes key.
func InfoOf(key ssh.PublicKey) HostKeyInfo {
	return HostKeyInfo{
		Type:              key.Type(),
		FingerprintSHA256: ssh.FingerprintSHA256(key),
		AuthorizedKey:     strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))),
	}
}

// ParseHostKeyLine parses a host key entered as an authorized_keys line
// (a trailing comment is allowed and dropped; options, several keys and
// line breaks are refused; at most MaxAuthorizedKeyLineLen bytes) and
// checks that it is an accepted host key. It returns the canonical
// "<type> <base64>" line, the type and the SHA-256 fingerprint. Errors
// wrap ErrMalformedHostKeyLine or ErrUnsupportedHostKey.
func ParseHostKeyLine(line string) (HostKeyInfo, error) {
	line = strings.TrimSpace(line)
	if line == "" || len(line) > MaxAuthorizedKeyLineLen || strings.ContainsAny(line, "\r\n") {
		return HostKeyInfo{}, errorf(ErrMalformedHostKeyLine, "a single authorized_keys line is required")
	}
	key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil || len(options) > 0 || strings.TrimSpace(string(rest)) != "" {
		return HostKeyInfo{}, errorf(ErrMalformedHostKeyLine, `malformed host key (expected "<type> <base64> [comment]")`)
	}
	if err := CheckHostKey(key); err != nil {
		return HostKeyInfo{}, err
	}
	return InfoOf(key), nil
}
