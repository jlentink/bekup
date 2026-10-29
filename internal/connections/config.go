package connections

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"net/netip"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/serviceutil"
	"github.com/jlentink/bekup/internal/sshpolicy"
)

// TLSMode is the transport security towards the target database
// (connection_tls_mode).
type TLSMode string

// TLS modes. Pinned is the default; the others are audited opt-outs.
const (
	TLSPinned     TLSMode = "pinned"
	TLSUnverified TLSMode = "unverified"
	TLSDisabled   TLSMode = "disabled"
)

// Valid reports whether m is a known mode.
func (m TLSMode) Valid() bool { return m == TLSPinned || m == TLSUnverified || m == TLSDisabled }

// strength orders the modes (higher = stronger).
func (m TLSMode) strength() int {
	switch m {
	case TLSPinned:
		return 2
	case TLSUnverified:
		return 1
	default:
		return 0
	}
}

// IsDowngrade reports whether changing from one mode to another weakens
// the transport security.
func IsDowngrade(from, to TLSMode) bool { return to.strength() < from.strength() }

// SSHAuth is the stored bastion credential kind (connection_ssh_auth).
type SSHAuth string

// SSH credential kinds.
const (
	SSHAuthNone       SSHAuth = "none"
	SSHAuthPassword   SSHAuth = "password"
	SSHAuthPrivateKey SSHAuth = "private_key"
)

// PinStatus summarizes the certificate pin of a connection.
type PinStatus string

// Pin states. Expiry warnings follow D-034 (30 and 7 days).
const (
	// PinNotRequired: the TLS mode is not pinned.
	PinNotRequired PinStatus = "not_required"
	PinMissing     PinStatus = "missing"
	PinValid       PinStatus = "valid"
	PinExpires30d  PinStatus = "expires_within_30_days"
	PinExpires7d   PinStatus = "expires_within_7_days"
	PinExpired     PinStatus = "expired"
)

// Limits of the stored configuration.
const (
	maxNameRunes       = 100
	maxPasswordLen     = 1024
	maxClientCertBytes = 32 << 10
	maxClientKeyBytes  = 16 << 10
	maxClientCertChain = 8
	maxAnchorPEMBytes  = 32 << 10
	defaultSSHPort     = 22
)

// Pin is a confirmed certificate pin (public data).
type Pin struct {
	// CertificatePEM is the anchor: a self-signed leaf or the root CA.
	CertificatePEM string
	Subject        string
	Issuer         string
	IsCA           bool
	// LeafSPKI is the expected leaf SPKI SHA-256; nil for a CA anchor
	// whose leaves may rotate.
	LeafSPKI []byte
	// NotAfter is the earliest expiry of what was confirmed.
	NotAfter time.Time
	PinnedAt time.Time
}

// HostKey is a confirmed SSH host-key pin (public data).
type HostKey struct {
	Type          string
	Fingerprint   string // OpenSSH "SHA256:..."
	AuthorizedKey string // "<type> <base64>"
	ConfirmedAt   time.Time
}

// SSH is the non-secret SSH tunnel configuration.
type SSH struct {
	Host string
	Port int
	User string
	Auth SSHAuth
	// HasPassphrase reports a passphrase for the stored private key.
	HasPassphrase bool
	// PublicKey is the authorized_keys line of the stored private key.
	PublicKey string
	HostKey   *HostKey
}

// Connection is the caller-facing view of a connection. It never holds
// credentials, only flags telling which ones are stored.
type Connection struct {
	ID         uuid.UUID
	Name       string
	Engine     engine.Kind
	VersionKey string
	ImageKey   string
	// Legacy is set when the version or image is an end-of-life series.
	Legacy   bool
	Host     string
	Port     int
	Username string
	// HasPassword reports a stored database password.
	HasPassword bool
	// ClientCertPEM is the public TLS client certificate ("" = none); its
	// key is stored sealed.
	ClientCertPEM string
	TLSMode       TLSMode
	// InsecureTransport is the badge flag: TLS is not pinned.
	InsecureTransport bool
	Pin               *Pin
	BackupPin         *Pin
	PinStatus         PinStatus
	// PinExpiresAt is the current pin's expiry (nil without a pin).
	PinExpiresAt *time.Time
	// SSH is nil when no tunnel is configured.
	SSH *SSH
	// Ready reports whether the connection can be used; otherwise
	// NotReadyReason says what is missing (a fixed, presentable text).
	Ready          bool
	NotReadyReason string
	// MaxConcurrentRuns is the connection's own run limit as stored (nil =
	// the global workers.max_runs_per_connection); it may exceed a since
	// lowered workers.max_concurrent_runs. EffectiveMaxConcurrentRuns is the
	// limit that applies, clamped to that global cap (task 3.13, D-156);
	// the cap itself is enforced by runstate's TryInsertRunSlot, which
	// counts every run slot of the process against it.
	MaxConcurrentRuns          *int
	EffectiveMaxConcurrentRuns int
	CreatedBy                  *uuid.UUID
	CreatedAt                  time.Time
	UpdatedAt                  time.Time
	// Temporary marks a temporary restore connection (task 4.4): admin
	// only, a restore target only, deleted with its credentials destroyed
	// at ExpiresAt or after the restore that used it finished.
	Temporary bool
	ExpiresAt *time.Time
	// LastTest is the stored outcome of the newest Test (D-187); nil when
	// never tested.
	LastTest *LastTest
}

// LastTest is the stored outcome of a Test (D-187): normalized codes only.
type LastTest struct {
	At time.Time
	OK bool
	// Code is the normalized failure code ("" when OK).
	Code string
	// Stale: the connection changed after the test.
	Stale bool
	// Privileges is the probe's verdict (nil when the test failed first).
	Privileges *Privileges
}

// SSHInput sets the SSH tunnel. It replaces the non-secret tunnel
// settings; Password and PrivateKey (with an optional Passphrase) are
// write-only and mutually exclusive (setting one removes the other); both
// nil keep the stored credential.
type SSHInput struct {
	Host       string
	Port       int
	User       string
	Password   []byte
	PrivateKey []byte
	Passphrase []byte
}

// CreateInput creates a connection. Secret fields are zeroed by Create.
type CreateInput struct {
	Name   string
	Engine engine.Kind
	// VersionKey is the catalog key of the server's release series;
	// ImageKey the client image ("" = VersionKey).
	VersionKey string
	ImageKey   string
	Host       string
	Port       int // 0 = the engine's default
	Username   string
	Password   []byte
	// TLSMode "" = pinned. Any other mode is an audited opt-out.
	TLSMode       TLSMode
	ClientCertPEM string
	ClientKeyPEM  []byte
	SSH           *SSHInput
	// MaxConcurrentRuns is the connection's own run limit (nil = the
	// global workers.max_runs_per_connection); 1..64, at most
	// workers.max_concurrent_runs.
	MaxConcurrentRuns *int
}

// UpdateInput changes a connection; nil fields stay unchanged. The engine
// and the TLS mode cannot change here (SetTLSMode changes the mode).
// Secret fields are zeroed by Update.
type UpdateInput struct {
	Name       *string
	VersionKey *string
	ImageKey   *string
	Host       *string
	Port       *int
	Username   *string
	// Password nil keeps the stored one; ClearPassword removes it.
	Password      []byte
	ClearPassword bool
	// ClientCertPEM + ClientKeyPEM replace the client certificate (both
	// required); ClearClientCertificate removes it.
	ClientCertPEM          *string
	ClientKeyPEM           []byte
	ClearClientCertificate bool
	// SSH sets (and enables) the tunnel; DisableSSH removes it with its
	// credentials and host-key pin.
	SSH        *SSHInput
	DisableSSH bool
	// MaxConcurrentRuns sets the connection's own run limit;
	// ClearMaxConcurrentRuns returns to the global default.
	MaxConcurrentRuns      *int
	ClearMaxConcurrentRuns bool
}

// PinInput selects the anchor of a certificate pin: CertIndex into a fresh
// capture of the server's chain (re-fetched through the runner and bound
// to LeafSPKI, the leaf the admin approved), or CertificatePEM supplied
// out of band. LeafSPKI is the leaf's SPKI SHA-256 (base64 or hex); it is
// required unless AllowLeafRotation pins a CA without binding the leaf.
type PinInput struct {
	CertIndex         *int
	CertificatePEM    string
	LeafSPKI          string
	AllowLeafRotation bool
}

func zeroSSHInput(in *SSHInput) {
	if in != nil {
		crypto.Zero(in.Password)
		crypto.Zero(in.PrivateKey)
		crypto.Zero(in.Passphrase)
	}
}

// FieldError is an invalid input; Message is presentable (shared with
// internal/destinations, D-140).
type FieldError = serviceutil.FieldError

func fieldErr(field, format string, a ...any) error { return serviceutil.FieldErr(field, format, a...) }

// ipLiteralPinnedMessage explains D-132 (IP-literal targets with pinned
// TLS fail closed in the runner without a tunnel).
const ipLiteralPinnedMessage = "pinned TLS needs a host name that matches the server certificate: " +
	"the runner cannot verify an IP address or localhost target directly (D-132); " +
	"use a DNS name, reach the server through an SSH tunnel, or (not recommended) choose an unverified TLS mode"

// isAddressHost reports an IP literal or localhost.
func isAddressHost(host string) bool {
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	return h == "localhost" || strings.HasSuffix(h, ".localhost")
}

// checkIPLiteralRule enforces D-132 for the next state.
func checkIPLiteralRule(mode TLSMode, host string, tunnel bool) error {
	if mode == TLSPinned && !tunnel && isAddressHost(host) {
		return fieldErr("host", ipLiteralPinnedMessage)
	}
	return nil
}

// normalizeName trims and checks a connection name.
func normalizeName(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return "", fieldErr("name", "the name is required")
	case !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxNameRunes:
		return "", fieldErr("name", "the name must be at most %d characters", maxNameRunes)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", fieldErr("name", "the name must not contain control characters")
		}
	}
	return s, nil
}

func defaultPort(k engine.Kind) int {
	if k == engine.KindPostgres {
		return 5432
	}
	return 3306
}

// checkPassword enforces the runner's password rules (D-123: no NUL, CR or
// LF, at most 1 KiB).
func checkPassword(field string, pw []byte) error {
	switch {
	case len(pw) == 0:
		return fieldErr(field, "the password must not be empty")
	case len(pw) > maxPasswordLen:
		return fieldErr(field, "the password is too long (max %d bytes)", maxPasswordLen)
	case bytes.ContainsAny(pw, "\x00\r\n"):
		return fieldErr(field, "the password must not contain NUL, CR or LF")
	}
	return nil
}

// checkSSHUser applies the shared SSH user rule (sshpolicy.ValidateUser:
// printable ASCII without spaces, not starting with '-').
func checkSSHUser(u string) error {
	if err := sshpolicy.ValidateUser(u); err != nil {
		return fieldErr("ssh.user", "the SSH user is invalid (%s)", err.Error())
	}
	return nil
}

// pemBlocks decodes data into PEM blocks; only whitespace may surround or
// separate them, and no block may carry headers. The caller zeroes block
// bytes that are secret.
func pemBlocks(data []byte, limit int) ([]*pem.Block, bool) {
	var blocks []*pem.Block
	rest := data
	for {
		rest = bytes.TrimLeft(rest, " \t\r\n")
		if len(rest) == 0 {
			break
		}
		if len(blocks) == limit {
			return blocks, false
		}
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			return blocks, false
		}
		blocks = append(blocks, b)
		if len(b.Headers) != 0 {
			return blocks, false
		}
	}
	return blocks, len(blocks) > 0
}

// checkClientCertificate checks the runner's shape rules: 1..8 parseable
// CERTIFICATE blocks and exactly one unencrypted private key block. The
// key is not parsed (no unzeroable copies); a key that does not match the
// certificate fails in the test.
func checkClientCertificate(certPEM string, key []byte) (string, error) {
	certPEM = strings.TrimSpace(certPEM)
	if certPEM == "" || len(certPEM) > maxClientCertBytes {
		return "", fieldErr("clientCertificatePem", "a PEM certificate of at most %d KiB is required", maxClientCertBytes>>10)
	}
	if len(key) == 0 || len(key) > maxClientKeyBytes {
		return "", fieldErr("clientKeyPem", "a PEM private key of at most %d KiB is required", maxClientKeyBytes>>10)
	}
	blocks, ok := pemBlocks([]byte(certPEM), maxClientCertChain)
	if !ok {
		return "", fieldErr("clientCertificatePem", "must be PEM certificates only")
	}
	for _, b := range blocks {
		if b.Type != "CERTIFICATE" {
			return "", fieldErr("clientCertificatePem", "must be PEM certificates only")
		}
		if _, err := x509.ParseCertificate(b.Bytes); err != nil {
			return "", fieldErr("clientCertificatePem", "a certificate does not parse")
		}
	}
	kb, ok := pemBlocks(key, 1)
	defer func() {
		for _, b := range kb {
			crypto.Zero(b.Bytes)
		}
	}()
	if !ok || len(kb) != 1 || len(kb[0].Bytes) == 0 {
		return "", fieldErr("clientKeyPem", "must be one unencrypted PEM private key")
	}
	switch kb[0].Type {
	case "PRIVATE KEY", "EC PRIVATE KEY", "RSA PRIVATE KEY":
	default:
		return "", fieldErr("clientKeyPem", "must be one unencrypted PEM private key")
	}
	return certPEM, nil
}

// checkSSHPrivateKey parses an SSH private key with the shared client key
// policy and returns its authorized_keys line (serviceutil, D-140).
func checkSSHPrivateKey(pemBytes, passphrase []byte) (string, error) {
	return serviceutil.CheckSSHPrivateKey(pemBytes, passphrase, "ssh.privateKey", "ssh.privateKeyPassphrase")
}
