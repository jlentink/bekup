package pinning

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/runner/validate"
)

// testCA is a CA or self-signed certificate with its key.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func serial(t *testing.T) *big.Int {
	t.Helper()
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// newCA creates a self-signed CA; parent != nil creates an intermediate.
func newCA(t *testing.T, cn string, parent *testCA) *testCA {
	t.Helper()
	key := newKey(t)
	tmpl := &x509.Certificate{
		SerialNumber:          serial(t),
		Subject:               pkix.Name{CommonName: cn, Organization: []string{cn + " Org"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	signer, signKey := tmpl, key
	if parent != nil {
		signer, signKey = parent.cert, parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signer, &key.PublicKey, signKey)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testCA{cert: c, key: key}
}

type leafOpts struct {
	dns      []string
	ips      []net.IP
	notAfter time.Time
}

// newLeaf issues a server certificate from ca (nil = self-signed).
func newLeaf(t *testing.T, ca *testCA, o leafOpts) *testCA {
	t.Helper()
	key := newKey(t)
	if o.notAfter.IsZero() {
		o.notAfter = time.Now().Add(365 * 24 * time.Hour)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(t),
		Subject:      pkix.Name{CommonName: "db server"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     o.notAfter,
		DNSNames:     o.dns,
		IPAddresses:  o.ips,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	parent, signKey := tmpl, key
	if ca != nil {
		parent, signKey = ca.cert, ca.key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, signKey)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testCA{cert: c, key: key}
}

// tlsCert builds a tls.Certificate presenting leaf + extra chain certs.
func tlsCert(leaf *testCA, chain ...*x509.Certificate) tls.Certificate {
	tc := tls.Certificate{Certificate: [][]byte{leaf.cert.Raw}, PrivateKey: leaf.key, Leaf: leaf.cert}
	for _, c := range chain {
		tc.Certificate = append(tc.Certificate, c.Raw)
	}
	return tc
}

func spkiOf(c *x509.Certificate) []byte {
	s := sha256.Sum256(c.RawSubjectPublicKeyInfo)
	return s[:]
}

// pinOf pins anchor with the expected leaf's SPKI.
func pinOf(anchor, leaf *x509.Certificate) validate.Pin {
	return validate.Pin{Anchor: anchor, LeafSPKI: spkiOf(leaf)}
}
