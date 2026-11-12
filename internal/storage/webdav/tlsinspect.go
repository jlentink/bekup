package webdav

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"time"

	"github.com/jlentink/bekup/internal/i18n"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/storage"
)

// Trust names how the server certificate is verified.
type Trust string

// Trust modes.
const (
	TrustSystem    Trust = "system"
	TrustCABundle  Trust = "ca_bundle"
	TrustPinned    Trust = "pinned"
	TrustPlainHTTP Trust = "plain_http" // dev mode only: no TLS at all
)

// Certificate describes a server's leaf certificate (public data).
type Certificate struct {
	// DER is the certificate.
	DER []byte
	// SPKISHA256 is the SHA-256 of its SubjectPublicKeyInfo (the pinned
	// value).
	SPKISHA256 []byte
	// SHA256 is the SHA-256 of the whole certificate (what browsers and
	// `openssl x509 -fingerprint -sha256` show).
	SHA256     []byte
	NotBefore  time.Time
	NotAfter   time.Time
	SelfSigned bool
}

// PEM returns the certificate in PEM form.
func (c *Certificate) PEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.DER})
}

// Parsed returns the parsed certificate.
func (c *Certificate) Parsed() (*x509.Certificate, error) { return x509.ParseCertificate(c.DER) }

// CertificateOf describes cert.
func CertificateOf(cert *x509.Certificate) *Certificate {
	spki := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	whole := sha256.Sum256(cert.Raw)
	return &Certificate{
		DER: append([]byte(nil), cert.Raw...), SPKISHA256: spki[:], SHA256: whole[:],
		NotBefore: cert.NotBefore, NotAfter: cert.NotAfter,
		SelfSigned: bytes.Equal(cert.RawIssuer, cert.RawSubject) &&
			cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature) == nil,
	}
}

// TLSReport is the outcome of a TLS inspection.
type TLSReport struct {
	Trust Trust
	// Certificate is the leaf the server presented (nil when none was
	// seen: plain http, or the connection failed before).
	Certificate *Certificate
	// Trusted reports that the handshake verified under Trust.
	Trusted bool
	// PinMatches reports that the presented leaf's SPKI is the pinned one
	// (pinned destinations only).
	PinMatches bool
	// Err is the classified failure (nil when Trusted).
	Err error
}

// InspectConfig is what Inspect needs (no credentials: nothing is sent
// over the connection).
type InspectConfig struct {
	URL         string
	CABundlePEM []byte
	Pin         *Pin
	AllowHTTP   bool
	Guard       *netguard.Guard
}

// inspectTimeout bounds dial plus handshake.
const inspectTimeout = 15 * time.Second

// Inspect connects to the URL's server through the guard and runs only
// the TLS handshake under the configured trust (system roots, the CA
// bundle or the pin); it sends nothing else. The presented leaf is
// reported whether or not it verified (a failed verification carries the
// certificates in tls.CertificateVerificationError, so verification is
// never skipped to capture it). It is the "show the fingerprint" step of
// the pin flow (D-222).
func Inspect(ctx context.Context, cfg InspectConfig) TLSReport {
	u, err := ParseURL(cfg.URL, cfg.AllowHTTP)
	if err != nil {
		return TLSReport{Trust: TrustSystem, Err: err}
	}
	if cfg.Guard == nil {
		return TLSReport{Trust: TrustSystem, Err: invalid("the netguard guard is required")}
	}
	s := &settings{scheme: u.scheme, host: u.host, hostPort: u.hostPort, guard: cfg.Guard}
	if len(cfg.CABundlePEM) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(cfg.CABundlePEM) {
			return TLSReport{Trust: TrustCABundle, Err: invalid("the CA bundle contains no PEM certificate")}
		}
		s.roots = pool
	}
	if cfg.Pin != nil {
		p, err := parsePin(cfg.Pin)
		if err != nil {
			return TLSReport{Trust: TrustPinned, Err: err}
		}
		s.pin = p
	}
	return inspect(ctx, s)
}

// InspectTLS is Inspect for the destination's own settings.
func (d *Destination) InspectTLS(ctx context.Context) TLSReport {
	if d.closed.Load() {
		return TLSReport{Trust: d.s.trust(), Err: storage.ErrClosed}
	}
	return inspect(ctx, d.s)
}

// Trust returns the destination's trust mode.
func (d *Destination) Trust() Trust { return d.s.trust() }

func (s *settings) trust() Trust {
	switch {
	case s.scheme == "http":
		return TrustPlainHTTP
	case s.pin != nil:
		return TrustPinned
	case s.roots != nil:
		return TrustCABundle
	}
	return TrustSystem
}

func inspect(ctx context.Context, s *settings) TLSReport {
	rep := TLSReport{Trust: s.trust()}
	if s.scheme == "http" {
		rep.Trusted = true
		return rep
	}
	host, port, err := net.SplitHostPort(s.hostPort)
	if err != nil {
		host, port = s.host, "443"
	}
	ctx, cancel := context.WithTimeout(ctx, inspectTimeout)
	defer cancel()
	d := &Destination{s: s} // error classification only
	conn, err := s.guard.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		rep.Err = d.transportErr(ctx, "connect", err)
		return rep
	}
	var leaf *x509.Certificate
	tc := tls.Client(conn, s.tlsConfig(func(c *x509.Certificate) { leaf = c }))
	herr := tc.HandshakeContext(ctx)
	discard(tc.Close()) // the inspection is over either way
	var cv *tls.CertificateVerificationError
	if errors.As(herr, &cv) && len(cv.UnverifiedCertificates) > 0 {
		leaf = cv.UnverifiedCertificates[0]
	}
	if leaf != nil {
		rep.Certificate = CertificateOf(leaf)
		if s.pin != nil {
			rep.PinMatches = subtle.ConstantTimeCompare(rep.Certificate.SPKISHA256, s.pin.spki) == 1
		}
	}
	if herr != nil {
		rep.Err = d.transportErr(ctx, "TLS handshake", herr)
		return rep
	}
	rep.Trusted = true
	return rep
}

// CheckPinnable reports whether cert can serve as the pinned anchor for
// host now: it must verify with itself as the only root for the host name
// (DNS or IP SAN), within its validity, for TLS servers. The error is a
// presentable reason.
func CheckPinnable(cert *x509.Certificate, host string, now time.Time) error {
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	_, err := cert.Verify(x509.VerifyOptions{Roots: pool, DNSName: host, CurrentTime: now})
	if err == nil {
		return nil
	}
	var (
		he x509.HostnameError
		ci x509.CertificateInvalidError
	)
	switch {
	case errors.As(err, &he):
		return i18n.Errorf("the certificate does not name the host in the URL (a subject alternative name is required); create a certificate for this host name on the server")
	case errors.As(err, &ci) && ci.Reason == x509.Expired:
		return i18n.Errorf("the certificate has expired or is not valid yet")
	case errors.As(err, &ci) && ci.Reason == x509.IncompatibleUsage:
		return i18n.Errorf("the certificate is not valid for TLS servers")
	default:
		return i18n.Errorf("the certificate cannot be used as a pin")
	}
}
