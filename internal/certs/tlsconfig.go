package certs

import (
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"slices"

	"github.com/jlentink/bekup/internal/config"
)

// ErrClientNotAllowed is returned by the server's connection check when the
// client identity is not in the allowlist.
var ErrClientNotAllowed = errors.New("mtls: client identity not allowed")

// ServerTLSConfig returns the runner's TLS configuration: TLS 1.3 only, a
// verified client certificate from the private CA (clientCAFile, never the
// system roots) is required, and the client's identity (its single
// spiffe://bekup/<name> URI SAN) must be in allowedClientIDs. The key file is
// read with the secret-file safety checks.
func ServerTLSConfig(certFile, keyFile, clientCAFile string, allowedClientIDs []string) (*tls.Config, error) {
	if len(allowedClientIDs) == 0 {
		return nil, errors.New("mtls: at least one allowed client identity is required")
	}
	allowed := make([]string, 0, len(allowedClientIDs))
	for _, id := range allowedClientIDs {
		canon, err := ParseClientID(id)
		if err != nil {
			return nil, fmt.Errorf("mtls: %w", err)
		}
		allowed = append(allowed, canon)
	}
	cert, err := loadKeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	pool, err := loadCAPool(clientCAFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		// No resumption: every connection presents and verifies a client
		// certificate against the current allowlist.
		SessionTicketsDisabled: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			id, err := PeerIdentity(cs)
			if err != nil {
				return err
			}
			if !slices.Contains(allowed, id) {
				return fmt.Errorf("%w: %s", ErrClientNotAllowed, id)
			}
			return nil
		},
	}, nil
}

// ClientTLSConfig returns the TLS configuration for serve/worker dialing the
// runner: TLS 1.3 only, RootCAs is only the private CA bundle in caFile
// (never the system roots), and the server certificate must be valid for
// serverName.
func ClientTLSConfig(certFile, keyFile, caFile, serverName string) (*tls.Config, error) {
	if serverName == "" {
		return nil, errors.New("mtls: server name is required")
	}
	cert, err := loadKeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	pool, err := loadCAPool(caFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		ServerName:   serverName,
	}, nil
}

// PeerIdentity returns the identity (spiffe://bekup/<name>) of the verified
// client certificate of a connection. The leaf must carry exactly one URI
// SAN and no DNS, IP or email SANs.
func PeerIdentity(cs tls.ConnectionState) (string, error) {
	if len(cs.VerifiedChains) == 0 || len(cs.VerifiedChains[0]) == 0 {
		return "", errors.New("mtls: no verified client certificate")
	}
	return certIdentity(cs.VerifiedChains[0][0])
}

func certIdentity(c *x509.Certificate) (string, error) {
	if len(c.URIs) != 1 || len(c.DNSNames) != 0 || len(c.IPAddresses) != 0 || len(c.EmailAddresses) != 0 {
		return "", errors.New("mtls: client certificate must carry exactly one URI SAN and no other SANs")
	}
	return ParseClientID(c.URIs[0].String())
}

func loadKeyPair(certFile, keyFile string) (tls.Certificate, error) {
	certPEM, err := config.ReadPublicFile(certFile)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("mtls: reading certificate: %w", err)
	}
	keyPEM, err := config.ReadSecretFile(keyFile)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("mtls: reading key: %w", err)
	}
	defer config.Zero(keyPEM)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("mtls: %s / %s: %w", certFile, keyFile, err)
	}
	if _, ok := cert.PrivateKey.(ed25519.PrivateKey); !ok {
		return tls.Certificate{}, fmt.Errorf("mtls: %s: key is not ed25519", keyFile)
	}
	return cert, nil
}

func loadCAPool(caFile string) (*x509.CertPool, error) {
	data, err := config.ReadPublicFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("mtls: reading CA bundle: %w", err)
	}
	certs, err := ParseCertificates(data)
	if err != nil {
		return nil, fmt.Errorf("mtls: %s: %w", caFile, err)
	}
	pool := x509.NewCertPool()
	for _, c := range certs {
		if !c.IsCA || !c.BasicConstraintsValid {
			return nil, fmt.Errorf("mtls: %s: %q is not a CA certificate", caFile, c.Subject)
		}
		pool.AddCert(c)
	}
	return pool, nil
}
