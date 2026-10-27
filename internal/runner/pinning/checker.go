package pinning

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/validate"
)

// ErrPinMismatch means the server's certificate does not match the pin. It
// is a hard failure and a security alert in serve (task 5.5).
var ErrPinMismatch = errors.New("pinning: the server certificate does not match the pinned certificate")

// Mismatch reasons (fixed codes, see MismatchError.Reason).
const (
	// ReasonLeafSPKI: the chain verifies against a pinned anchor but the
	// leaf's public key is not the pinned leaf key.
	ReasonLeafSPKI = "leaf_spki"
	// ReasonUntrusted: the chain does not verify against any pinned
	// anchor (a certificate from another CA).
	ReasonUntrusted = "untrusted_chain"
	// ReasonHostname: the certificate is not valid for the target host.
	ReasonHostname = "hostname"
	// ReasonExpired: the certificate (or the anchor) is expired or not yet
	// valid.
	ReasonExpired = "expired"
	// ReasonNoCertificate: the server presented no certificate.
	ReasonNoCertificate = "no_certificate"
)

// MismatchError is a failed pin check. It matches ErrPinMismatch with
// errors.Is. Reason is a fixed code; no certificate content is included.
type MismatchError struct {
	Reason string
}

func (e *MismatchError) Error() string { return ErrPinMismatch.Error() + " (" + e.Reason + ")" }

// Unwrap makes errors.Is(err, ErrPinMismatch) true.
func (e *MismatchError) Unwrap() error { return ErrPinMismatch }

// MaxChainCerts bounds the certificates of a captured or checked chain.
const MaxChainCerts = 8

// Options configures a Checker.
type Options struct {
	// Now overrides the clock (tests).
	Now func() time.Time
	// Timeout bounds dial + upgrade + handshake (default DefaultTimeout).
	Timeout time.Duration
}

// Checker implements Verifier and Fetch.
type Checker struct {
	now     func() time.Time
	timeout time.Duration
}

// New returns a Checker.
func New(opts Options) *Checker {
	c := &Checker{now: opts.Now, timeout: opts.Timeout}
	if c.now == nil {
		c.now = time.Now
	}
	if c.timeout <= 0 {
		c.timeout = DefaultTimeout
	}
	return c
}

// anchorPool returns a pool with exactly the pins' anchors. It is never
// empty for valid pins, so crypto/tls never falls back to the system roots
// (a nil RootCAs would mean "system roots").
func anchorPool(pins []validate.Pin) (*x509.CertPool, error) {
	if len(pins) == 0 {
		return nil, errors.New("pinning: no pins")
	}
	pool := x509.NewCertPool()
	for _, p := range pins {
		if p.Anchor == nil {
			return nil, errors.New("pinning: pin without anchor")
		}
		pool.AddCert(p.Anchor)
	}
	return pool, nil
}

// checkChain checks a presented chain (leaf first) against pins: one pin
// must both verify the chain (its anchor as the only root, host name / IP
// checked, at now) and, when it carries a leaf SPKI, match the leaf's SPKI
// SHA-256 in constant time. It returns the index of the first matching pin.
func checkChain(chain []*x509.Certificate, pins []validate.Pin, host string, now time.Time) (int, *x509.Certificate, error) {
	if len(chain) == 0 {
		return -1, nil, &MismatchError{Reason: ReasonNoCertificate}
	}
	if len(chain) > MaxChainCerts {
		return -1, nil, &MismatchError{Reason: ReasonUntrusted}
	}
	if len(pins) == 0 {
		return -1, nil, errors.New("pinning: no pins")
	}
	leaf := chain[0]
	spki := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	inter := x509.NewCertPool()
	for _, c := range chain[1:] {
		inter.AddCert(c)
	}
	reason := ""
	for i, p := range pins {
		if p.Anchor == nil {
			return -1, nil, errors.New("pinning: pin without anchor")
		}
		roots := x509.NewCertPool()
		roots.AddCert(p.Anchor)
		chains, verr := leaf.Verify(x509.VerifyOptions{
			Roots:         roots,
			Intermediates: inter,
			DNSName:       host,
			CurrentTime:   now,
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		})
		spkiOK := len(p.LeafSPKI) == 0 || subtle.ConstantTimeCompare(spki[:], p.LeafSPKI) == 1
		if verr == nil && spkiOK {
			return i, earliest(chains), nil
		}
		r := classify(verr, spkiOK)
		if reason == "" || rank(r) > rank(reason) {
			reason = r
		}
	}
	return -1, nil, &MismatchError{Reason: reason}
}

// classify maps a verification result to a reason.
func classify(verr error, spkiOK bool) string {
	if verr == nil {
		if !spkiOK {
			return ReasonLeafSPKI
		}
		return ReasonUntrusted // unreachable
	}
	var he x509.HostnameError
	var ie x509.CertificateInvalidError
	switch {
	case errors.As(verr, &he):
		return ReasonHostname
	case errors.As(verr, &ie) && ie.Reason == x509.Expired:
		return ReasonExpired
	default:
		return ReasonUntrusted
	}
}

// rank orders reasons by how far the check got (the most specific one is
// reported): a chain that verifies but carries a different leaf key says
// more than "untrusted".
func rank(r string) int {
	switch r {
	case ReasonLeafSPKI:
		return 4
	case ReasonHostname:
		return 3
	case ReasonExpired:
		return 2
	default:
		return 1
	}
}

// earliest returns the certificate with the earliest NotAfter of the first
// verified chain.
func earliest(chains [][]*x509.Certificate) *x509.Certificate {
	var e *x509.Certificate
	if len(chains) == 0 {
		return nil
	}
	for _, c := range chains[0] {
		if e == nil || c.NotAfter.Before(e.NotAfter) {
			e = c
		}
	}
	return e
}

// ClientTLSConfig returns a TLS configuration for a Go client (the runner's
// own Probe driver) with the pin semantics of Preflight: the pinned
// anchors are the only roots, the host name / IP is verified by crypto/tls
// and VerifyConnection additionally runs the per-pin check (anchor and
// leaf SPKI must belong to the same pin). TLS 1.2 is the minimum.
func ClientTLSConfig(host string, pins []validate.Pin, now func() time.Time) (*tls.Config, error) {
	pool, err := anchorPool(pins)
	if err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	pinsCopy := append([]validate.Pin(nil), pins...)
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: host,
		RootCAs:    pool,
		Time:       now,
		VerifyConnection: func(cs tls.ConnectionState) error {
			_, _, err := checkChain(cs.PeerCertificates, pinsCopy, host, now())
			return err
		},
	}, nil
}

// Preflight implements Verifier: it opens t, performs the TLS upgrade and
// the handshake with the pinned anchors as the only roots and checks the
// chain per pin (see checkChain). The connection is closed right after the
// handshake; no credentials are sent.
func (c *Checker) Preflight(ctx context.Context, t Target, pins []validate.Pin) (Report, error) {
	if t.Dial == nil || !t.Kind.Valid() || t.Host == "" {
		return Report{}, errors.New("pinning: incomplete target")
	}
	cfg, err := ClientTLSConfig(t.Host, pins, c.now)
	if err != nil {
		return Report{}, err
	}
	var (
		matched  = -1
		earliest *x509.Certificate
		leaf     *x509.Certificate
	)
	// Replace the plain check with one that records the matching pin.
	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		i, e, err := checkChain(cs.PeerCertificates, pins, t.Host, c.now())
		if err != nil {
			return err
		}
		matched, earliest, leaf = i, e, cs.PeerCertificates[0]
		return nil
	}
	tc, err := c.connect(ctx, t, cfg)
	if err != nil {
		return Report{}, mapHandshake(err)
	}
	discardErr(tc.Close())
	if matched < 0 || leaf == nil {
		return Report{}, &MismatchError{Reason: ReasonUntrusted}
	}
	rep := Report{
		PinIndex:       matched,
		LeafNotAfter:   leaf.NotAfter,
		AnchorNotAfter: pins[matched].Anchor.NotAfter,
		NotAfter:       leaf.NotAfter,
	}
	if earliest != nil {
		rep.NotAfter = earliest.NotAfter
	}
	if matched > 0 {
		rep.Warnings = append(rep.Warnings, WarningStagedPinInUse)
	}
	switch left := rep.NotAfter.Sub(c.now()); {
	case left <= ExpiryWarn7:
		rep.Warnings = append(rep.Warnings, WarningExpires7d)
	case left <= ExpiryWarn30:
		rep.Warnings = append(rep.Warnings, WarningExpires30d)
	}
	return rep, nil
}

// mapHandshake turns a crypto/tls verification failure into a
// *MismatchError; other errors pass through.
func mapHandshake(err error) error {
	var me *MismatchError
	if errors.As(err, &me) {
		return me
	}
	if !errors.Is(err, ErrHandshake) {
		return err
	}
	var cve *tls.CertificateVerificationError
	if errors.As(err, &cve) {
		return &MismatchError{Reason: classify(cve.Err, true)}
	}
	var he x509.HostnameError
	var ua x509.UnknownAuthorityError
	var ie x509.CertificateInvalidError
	switch {
	case errors.As(err, &he):
		return &MismatchError{Reason: ReasonHostname}
	case errors.As(err, &ua):
		return &MismatchError{Reason: ReasonUntrusted}
	case errors.As(err, &ie):
		return &MismatchError{Reason: classify(ie, true)}
	}
	return err
}

// connect dials t and starts TLS. UpgradeAuto falls back to PostgreSQL
// direct TLS (on a fresh connection) only when the server's answer to the
// SSLRequest is not a protocol answer at all; an explicit 'N' is final.
func (c *Checker) connect(ctx context.Context, t Target, cfg *tls.Config) (*tls.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	up := t.Upgrade
	if up == UpgradeAuto {
		up = UpgradeInProtocol
	}
	tc, err := c.dialAndStart(ctx, t, up, cfg)
	if err == nil || t.Upgrade != UpgradeAuto || t.Kind != engine.KindPostgres || !errors.Is(err, ErrProtocol) {
		return tc, err
	}
	return c.dialAndStart(ctx, t, UpgradeDirect, cfg)
}

func (c *Checker) dialAndStart(ctx context.Context, t Target, up Upgrade, cfg *tls.Config) (*tls.Conn, error) {
	conn, err := t.Dial(ctx)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		return nil, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	return startTLS(ctx, conn, t.Kind, up, cfg)
}

// TrustAnchor implements Verifier: the PEM CERTIFICATE blocks of every
// pin's anchor (current first, then the staged backup), deduplicated. The
// result is the native client's only CA file; the engine argv points
// sslrootcert / --ssl-ca at it, so no client ever reads the system roots.
func (c *Checker) TrustAnchor(pins []validate.Pin) ([]byte, error) {
	if len(pins) == 0 {
		return nil, errors.New("pinning: no pins")
	}
	var out bytes.Buffer
	var seen [][]byte
	for _, p := range pins {
		if p.Anchor == nil || len(p.Anchor.Raw) == 0 {
			return nil, errors.New("pinning: pin without anchor")
		}
		dup := false
		for _, s := range seen {
			if bytes.Equal(s, p.Anchor.Raw) {
				dup = true
			}
		}
		if dup {
			continue
		}
		seen = append(seen, p.Anchor.Raw)
		if err := pem.Encode(&out, &pem.Block{Type: "CERTIFICATE", Bytes: p.Anchor.Raw}); err != nil {
			return nil, fmt.Errorf("pinning: encode anchor: %w", err)
		}
	}
	return out.Bytes(), nil
}
