package certs

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Identity scheme (D-089): clients are identified by exactly one URI SAN of
// the form spiffe://bekup/<component>; the runner's server certificate
// carries DNS SANs (default "runner", the compose service name) and is
// verified by host name. Client certificates carry no DNS/IP SANs and only
// the clientAuth EKU; the server certificate only the serverAuth EKU.
const (
	// TrustDomain is the host part of every client identity URI.
	TrustDomain = "bekup"

	// ComponentRunner is the runner (gRPC server).
	ComponentRunner = "runner"
	// ComponentServe is `bekup serve` (gRPC client).
	ComponentServe = "serve"
	// ComponentWorker is `bekup worker` (gRPC client).
	ComponentWorker = "worker"

	// DefaultRunnerDNSName is always present in the runner certificate.
	DefaultRunnerDNSName = "runner"

	// DefaultCAValidity and DefaultLeafValidity are the default lifetimes.
	DefaultCAValidity   = 5 * 365 * 24 * time.Hour
	DefaultLeafValidity = 365 * 24 * time.Hour

	// MaxCAValidity and MaxLeafValidity bound the configurable lifetimes.
	MaxCAValidity   = 10 * 365 * 24 * time.Hour
	MaxLeafValidity = 825 * 24 * time.Hour

	// clockSkew backdates NotBefore so that slightly skewed clocks accept
	// freshly issued certificates.
	clockSkew = 5 * time.Minute
)

// Components returns the known component names in a stable order.
func Components() []string {
	return []string{ComponentRunner, ComponentServe, ComponentWorker}
}

// IsServer reports whether the component gets a server certificate.
func IsServer(component string) bool { return component == ComponentRunner }

// ValidComponent reports whether name is a known component.
func ValidComponent(name string) bool { return slices.Contains(Components(), name) }

// ClientID returns the SAN URI identity of a client component.
func ClientID(component string) string {
	return (&url.URL{Scheme: "spiffe", Host: TrustDomain, Path: "/" + component}).String()
}

// ParseClientID validates a client identity of the form
// spiffe://bekup/<name> and returns it in canonical form.
func ParseClientID(id string) (string, error) {
	u, err := url.Parse(id)
	if err != nil {
		return "", fmt.Errorf("client identity %q: %w", id, err)
	}
	name := strings.TrimPrefix(u.Path, "/")
	if u.Scheme != "spiffe" || u.Host != TrustDomain || u.User != nil || u.RawQuery != "" ||
		u.Fragment != "" || u.Opaque != "" || name == "" || !componentNameOK(name) {
		return "", fmt.Errorf("client identity %q: must be spiffe://%s/<name> with name [a-z0-9-]", id, TrustDomain)
	}
	return ClientID(name), nil
}

func componentNameOK(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// CA is a loaded certificate authority.
type CA struct {
	Cert    *x509.Certificate
	CertPEM []byte
	Key     ed25519.PrivateKey
}

// CAOptions configures NewCA.
type CAOptions struct {
	// Validity of the CA certificate (default DefaultCAValidity).
	Validity time.Duration
	// ServerNames are the DNS names and IP addresses that the runner
	// certificate may carry. They become the CA's name constraints.
	ServerNames []string
	// Now overrides the clock (tests).
	Now func() time.Time
}

// NewCA creates a self-signed ed25519 CA with path length 0 and name
// constraints: DNS names and IPs limited to ServerNames and URIs limited to
// the bekup trust domain.
func NewCA(opts CAOptions) (*CA, error) {
	validity := opts.Validity
	if validity == 0 {
		validity = DefaultCAValidity
	}
	if validity < 0 || validity > MaxCAValidity {
		return nil, fmt.Errorf("CA validity %s out of range (max %s)", validity, MaxCAValidity)
	}
	dns, ips, err := splitNames(opts.ServerNames)
	if err != nil {
		return nil, err
	}
	if len(dns) == 0 && len(ips) == 0 {
		return nil, errors.New("CA needs at least one runner server name")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating CA key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	now := clock(opts.Now)
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"bekup"},
			CommonName:   "bekup runner mTLS CA " + hex.EncodeToString(serial.Bytes()[:4]),
		},
		NotBefore:             now.Add(-clockSkew),
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,

		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         dns,
		PermittedURIDomains:         []string{TrustDomain},
	}
	if len(ips) == 0 {
		tmpl.ExcludedIPRanges = []*net.IPNet{
			{IP: net.IPv4zero.To4(), Mask: net.CIDRMask(0, 32)},
			{IP: net.IPv6zero, Mask: net.CIDRMask(0, 128)},
		}
	}
	for _, ip := range ips {
		bits := 128
		if ip.To4() != nil {
			ip, bits = ip.To4(), 32
		}
		tmpl.PermittedIPRanges = append(tmpl.PermittedIPRanges, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		return nil, fmt.Errorf("creating CA certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parsing CA certificate: %w", err)
	}
	return &CA{Cert: cert, CertPEM: encodeCert(der), Key: priv}, nil
}

// ServerNames returns the runner names permitted by the CA's name
// constraints (DNS names and IP addresses).
func (ca *CA) ServerNames() []string {
	names := slices.Clone(ca.Cert.PermittedDNSDomains)
	for _, n := range ca.Cert.PermittedIPRanges {
		names = append(names, n.IP.String())
	}
	return names
}

// LeafOptions configures CA.Issue.
type LeafOptions struct {
	// Validity of the leaf (default DefaultLeafValidity); capped at the CA's
	// NotAfter.
	Validity time.Duration
	// ServerNames are the runner's DNS names / IPs (server certs only;
	// DefaultRunnerDNSName is always included).
	ServerNames []string
	// Now overrides the clock (tests).
	Now func() time.Time
}

// Leaf is an issued certificate and its private key.
type Leaf struct {
	Cert    *x509.Certificate
	CertPEM []byte
	KeyPEM  []byte // PKCS#8; zero after use
}

// Issue creates a fresh ed25519 key and certificate for component.
func (ca *CA) Issue(component string, opts LeafOptions) (*Leaf, error) {
	if !ValidComponent(component) {
		return nil, fmt.Errorf("unknown component %q", component)
	}
	validity := opts.Validity
	if validity == 0 {
		validity = DefaultLeafValidity
	}
	if validity < 0 || validity > MaxLeafValidity {
		return nil, fmt.Errorf("leaf validity %s out of range (max %s)", validity, MaxLeafValidity)
	}
	now := clock(opts.Now)
	notAfter := now.Add(validity)
	if notAfter.After(ca.Cert.NotAfter) {
		notAfter = ca.Cert.NotAfter
	}
	if !notAfter.After(now) {
		return nil, errors.New("the CA has expired; rotate the CA")
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{Organization: []string{"bekup"}, CommonName: "bekup " + component},
		NotBefore:             now.Add(-clockSkew),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  false,
	}
	if IsServer(component) {
		names := append([]string{DefaultRunnerDNSName}, opts.ServerNames...)
		dns, ips, err := splitNames(names)
		if err != nil {
			return nil, err
		}
		tmpl.DNSNames, tmpl.IPAddresses = dns, ips
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	} else {
		u, err := url.Parse(ClientID(component))
		if err != nil {
			return nil, err
		}
		tmpl.URIs = []*url.URL{u}
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating %s key: %w", component, err)
	}
	defer clear(priv)
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, pub, ca.Key)
	if err != nil {
		return nil, fmt.Errorf("creating %s certificate: %w", component, err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parsing %s certificate: %w", component, err)
	}
	// Self-check: the leaf must verify against its CA, including the name
	// constraints (a server name outside them fails here, not at runtime).
	if err := verifyLeaf(cert, ca.Cert); err != nil {
		return nil, fmt.Errorf("issued %s certificate does not verify: %w", component, err)
	}
	keyPEM, err := encodeKey(priv)
	if err != nil {
		return nil, err
	}
	return &Leaf{Cert: cert, CertPEM: encodeCert(der), KeyPEM: keyPEM}, nil
}

func verifyLeaf(leaf, ca *x509.Certificate) error {
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	_, err := leaf.Verify(x509.VerifyOptions{
		Roots:       roots,
		CurrentTime: leaf.NotBefore.Add(clockSkew),
		KeyUsages:   leaf.ExtKeyUsage,
	})
	return err
}

// splitNames splits names into deduplicated DNS names and IP addresses and
// validates them (no wildcards, no empty labels).
func splitNames(names []string) ([]string, []net.IP, error) {
	var dns []string
	var ips []net.IP
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if ip := net.ParseIP(n); ip != nil {
			if !slices.ContainsFunc(ips, ip.Equal) {
				ips = append(ips, ip)
			}
			continue
		}
		if !validDNSName(n) {
			return nil, nil, fmt.Errorf("invalid server name %q (DNS name or IP address, no wildcards)", n)
		}
		if !slices.Contains(dns, n) {
			dns = append(dns, n)
		}
	}
	return dns, ips, nil
}

func validDNSName(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for label := range strings.SplitSeq(s, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

func randomSerial() (*big.Int, error) {
	// 128 random bits, top bit cleared so the DER INTEGER stays positive
	// and 16 bytes long; never zero in practice.
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("generating serial: %w", err)
	}
	b[0] = b[0]&0x7f | 0x40
	return new(big.Int).SetBytes(b), nil
}

func clock(now func() time.Time) time.Time {
	if now != nil {
		return now().UTC()
	}
	return time.Now().UTC()
}

func encodeCert(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func encodeKey(priv ed25519.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("encoding private key: %w", err)
	}
	defer clear(der)
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// parseKey parses a PKCS#8 PEM ed25519 private key.
func parseKey(keyPEM []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errors.New("no PKCS#8 PRIVATE KEY PEM block")
	}
	defer clear(block.Bytes)
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing private key: %w", err)
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not ed25519")
	}
	return priv, nil
}

// ParseCertificates parses every CERTIFICATE block in data. It fails when
// there is none or when other PEM blocks are present.
func ParseCertificates(data []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("unexpected PEM block %q", block.Type)
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parsing certificate: %w", err)
		}
		certs = append(certs, c)
	}
	if len(certs) == 0 {
		return nil, errors.New("no PEM certificate found")
	}
	if strings.TrimSpace(string(rest)) != "" {
		return nil, errors.New("trailing non-PEM data")
	}
	return certs, nil
}
