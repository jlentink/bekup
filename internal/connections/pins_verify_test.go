package connections

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"strings"
	"testing"
	"time"
)

// selfSignedCert returns a self-signed server certificate for cn with the
// given DNS SANs (none: a CN-only certificate).
func selfSignedCert(t *testing.T, cn string, sans ...string) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     sans,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestVerifyChainHostName(t *testing.T) {
	const host = "mail.example.test"
	now := time.Now()

	t.Run("SAN matches", func(t *testing.T) {
		c := selfSignedCert(t, host, host)
		if err := verifyChain([]*x509.Certificate{c}, c, host, now); err != nil {
			t.Fatalf("verifyChain: %v", err)
		}
	})

	t.Run("CN only names the missing SAN", func(t *testing.T) {
		c := selfSignedCert(t, host)
		err := verifyChain([]*x509.Certificate{c}, c, host, now)
		if err == nil {
			t.Fatal("verifyChain accepted a certificate without a SAN")
		}
		for _, want := range []string{"no Subject Alternative Name", "subjectAltName=DNS:" + host} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q lacks %q", err, want)
			}
		}
	})

	t.Run("SAN for another host keeps the generic message", func(t *testing.T) {
		c := selfSignedCert(t, host, "other.example.test")
		err := verifyChain([]*x509.Certificate{c}, c, host, now)
		if err == nil {
			t.Fatal("verifyChain accepted a certificate for another host")
		}
		if !strings.Contains(err.Error(), "not valid for the host name") || strings.Contains(err.Error(), "Subject Alternative Name") {
			t.Errorf("unexpected error %q", err)
		}
	})
}
