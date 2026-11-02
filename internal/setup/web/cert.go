package web

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"math/big"
	"net"
	"strings"
	"time"

	"github.com/jlentink/bekup/internal/hostname"
)

// certLifetime covers the server's longest run (2 h) with margin.
const certLifetime = 24 * time.Hour

// Certificate is the server's self-signed certificate, generated in
// memory and never written (D-217).
type Certificate struct {
	TLS tls.Certificate
	// Fingerprint is the SHA-256 of the DER certificate, colon-separated
	// upper-case hex as browsers show it.
	Fingerprint string
}

// NewCertificate returns a self-signed ECDSA P-256 certificate for hosts
// (host names or IP addresses; "localhost" and the loopback addresses are
// always included).
func NewCertificate(hosts []string, now time.Time) (*Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "bekup setup"},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(certLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
			continue
		}
		if err := hostname.Validate(h); err != nil {
			return nil, err
		}
		tmpl.DNSNames = append(tmpl.DNSNames, strings.ToLower(h))
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(der)
	hexs := strings.ToUpper(hex.EncodeToString(sum[:]))
	parts := make([]string, 0, len(sum))
	for i := 0; i < len(hexs); i += 2 {
		parts = append(parts, hexs[i:i+2])
	}
	if len(der) == 0 {
		return nil, errors.New("empty certificate")
	}
	return &Certificate{
		TLS:         tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key},
		Fingerprint: strings.Join(parts, ":"),
	}, nil
}

// TLSConfig is the server's TLS configuration: TLS 1.3 only.
func (c *Certificate) TLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{c.TLS}}
}
