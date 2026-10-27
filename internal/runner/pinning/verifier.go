package pinning

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"time"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/validate"
)

// DialFunc opens the raw TCP connection to the target: directly through
// the target allowlist guard or through the SSH tunnel (see Connect).
type DialFunc func(ctx context.Context) (net.Conn, error)

// Target is the endpoint a capture or pre-flight connects to.
type Target struct {
	// Kind selects the TLS upgrade (PostgreSQL or MySQL/MariaDB).
	Kind engine.Kind
	// Host is the target host as the native client uses it (a DNS name or
	// an IP literal): the name the certificate must be valid for and the
	// TLS server name.
	Host string
	// Dial opens the TCP connection (mandatory).
	Dial DialFunc
	// Upgrade selects how TLS is started (default UpgradeAuto).
	Upgrade Upgrade
}

// Upgrade selects how a TLS session is started on a target connection.
type Upgrade int

// Upgrades.
const (
	// UpgradeAuto: PostgreSQL SSLRequest with a fallback to direct TLS
	// (PG 17+, ALPN "postgresql") when the server does not answer the
	// SSLRequest; the MySQL/MariaDB SSL request.
	UpgradeAuto Upgrade = iota
	// UpgradeInProtocol: SSLRequest / MySQL SSL request only.
	UpgradeInProtocol
	// UpgradeDirect: PostgreSQL direct TLS only (PG 17+). Not valid for
	// MySQL/MariaDB.
	UpgradeDirect
)

// Report is the result of a successful pre-flight.
type Report struct {
	// PinIndex is the index of the matching pin (0 = current, 1 = the
	// staged backup pin).
	PinIndex int
	// LeafNotAfter is the presented leaf's expiry.
	LeafNotAfter time.Time
	// AnchorNotAfter is the matching pinned anchor's expiry.
	AnchorNotAfter time.Time
	// NotAfter is the earliest expiry of the verified chain (leaf,
	// intermediates, anchor).
	NotAfter time.Time
	// Warnings are fixed codes for the run result (Warning* constants).
	Warnings []string
}

// Warning codes of a Report (stable strings; serve maps them to alerts).
const (
	// WarningStagedPinInUse: the server presented the certificate of the
	// staged backup pin; the admin should promote it.
	WarningStagedPinInUse = "tls_staged_pin_in_use"
	// WarningExpires30d / WarningExpires7d: the verified chain expires
	// within 30 / 7 days (only the more urgent one is reported).
	WarningExpires30d = "tls_certificate_expires_within_30_days"
	WarningExpires7d  = "tls_certificate_expires_within_7_days"
)

// Expiry warning thresholds.
const (
	ExpiryWarn30 = 30 * 24 * time.Hour
	ExpiryWarn7  = 7 * 24 * time.Hour
)

// Verifier is the hook the runner's RPC handlers (task 2.7) call for
// TLSPinned targets: Preflight before every connection the runner or a dump
// container makes, TrustAnchor for the preamble's CA record.
type Verifier interface {
	// Preflight connects to t, starts TLS and checks the presented chain
	// against pins. It returns ErrPinMismatch (wrapped in *MismatchError)
	// on a pin failure (the RPC layer maps it to
	// runnerpb.PinMismatchStatus).
	Preflight(ctx context.Context, t Target, pins []validate.Pin) (Report, error)
	// TrustAnchor returns the PEM of every pinned anchor: the native
	// client's only trust anchor (preamble.Material.CA). The caller owns
	// the slice.
	TrustAnchor(pins []validate.Pin) ([]byte, error)
}

// failClosed is a Verifier that rejects everything (used when no checker
// is configured, so a wiring mistake never skips the pin check).
type failClosed struct{}

// ErrNoVerifier is returned by FailClosed.
var ErrNoVerifier = errors.New("pinning: no certificate pin verifier configured")

// FailClosed returns a Verifier that rejects every call.
func FailClosed() Verifier { return failClosed{} }

func (failClosed) Preflight(context.Context, Target, []validate.Pin) (Report, error) {
	return Report{}, ErrNoVerifier
}

func (failClosed) TrustAnchor([]validate.Pin) ([]byte, error) { return nil, ErrNoVerifier }

var _ Verifier = (*Checker)(nil)

// IsPublicAnchor reports whether cert is, or chains to, a certificate of
// the system trust store (a public CA). Pin anchors must be a self-signed
// leaf or a private CA (D-139): the runner refuses public anchors in
// every request, and serve's pin confirmation should refuse them before
// storing a pin.
func IsPublicAnchor(cert *x509.Certificate) bool { return validate.IsPublicAnchor(cert) }
