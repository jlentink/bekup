package certs

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPostgresComponent(t *testing.T) {
	if !IsServer(ComponentPostgres) || DefaultDNSName(ComponentPostgres) != "postgres" || DefaultDNSName(ComponentServe) != "" {
		t.Fatal("postgres must be a server component with DNS name postgres")
	}
	dir := initDir(t, func(o *InitOptions) {
		o.Components = []string{ComponentPostgres}
		o.PostgresNames = []string{"pg.internal"}
	})
	l := Layout{Dir: dir}
	ca := readCert(t, l.CACert())
	if !slices.Contains(ca.PermittedDNSDomains, "postgres") || !slices.Contains(ca.PermittedDNSDomains, "runner") {
		t.Fatalf("CA constraints %v", ca.PermittedDNSDomains)
	}
	leaf := readCert(t, l.CertFile(ComponentPostgres))
	for _, h := range []string{"postgres", "pg.internal"} {
		if err := leaf.VerifyHostname(h); err != nil {
			t.Error(err)
		}
	}
	if err := leaf.VerifyHostname("127.0.0.1"); err == nil {
		t.Error("IP not in SANs accepted")
	}

	// Rotation keeps the extra names unless they are replaced.
	if _, err := Rotate(RotateOptions{Dir: dir, Components: []string{ComponentPostgres}, Passphrase: []byte(testPass)}); err != nil {
		t.Fatal(err)
	}
	if got := readCert(t, l.CertFile(ComponentPostgres)).DNSNames; !slices.Equal(got, []string{"postgres", "pg.internal"}) {
		t.Fatalf("after rotate: %v", got)
	}
	_, err := Rotate(RotateOptions{Dir: dir, Components: []string{ComponentPostgres}, PostgresNames: []string{"other.example"}, Passphrase: []byte(testPass)})
	if err == nil || !strings.Contains(err.Error(), "name constraints") {
		t.Fatalf("name outside the CA constraints: %v", err)
	}
}

// TestPostgresOnPreD100CA covers a CA created before the postgres component
// existed: issuing fails with guidance, and staging a CA rotation adds
// "postgres" to the constraints.
func TestPostgresOnPreD100CA(t *testing.T) {
	dir := initDir(t, func(o *InitOptions) {
		o.Components = []string{ComponentRunner, ComponentServe}
		o.Passphrase, o.AllowUnencrypted = nil, true
	})
	l := Layout{Dir: dir}
	old, err := NewCA(CAOptions{ServerNames: []string{DefaultRunnerDNSName}})
	if err != nil {
		t.Fatal(err)
	}
	keyPEM, err := encodeKey(old.Key)
	if err != nil {
		t.Fatal(err)
	}
	for p, data := range map[string][]byte{l.CAKey(): keyPEM, l.CACert(): old.CertPEM} {
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, err = Rotate(RotateOptions{Dir: dir, Components: []string{ComponentPostgres}})
	if err == nil || !strings.Contains(err.Error(), "--ca stage") {
		t.Fatalf("issuing postgres from an old CA: %v", err)
	}
	if _, err := RotateCA(RotateCAOptions{Dir: dir, Phase: CAStage, AllowUnencrypted: true}); err != nil {
		t.Fatal(err)
	}
	next := readCert(t, l.nextCACert())
	if !slices.Equal(next.PermittedDNSDomains, []string{"runner", "postgres"}) {
		t.Fatalf("next CA constraints %v", next.PermittedDNSDomains)
	}
}

func TestIssuePostgresOutsideConstraints(t *testing.T) {
	ca, err := NewCA(CAOptions{ServerNames: []string{DefaultRunnerDNSName}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ca.Issue(ComponentPostgres, LeafOptions{}); err == nil || !strings.Contains(err.Error(), "do not permit the postgres names postgres") {
		t.Fatalf("got %v", err)
	}
}

func TestPostgresChainForChannelBinding(t *testing.T) {
	dir := initDir(t, func(o *InitOptions) { o.Components = []string{ComponentPostgres} })
	l := Layout{Dir: dir}
	root := readCert(t, l.CACert())
	chain := readCerts(t, l.CertFile(ComponentPostgres))
	if len(chain) != 2 {
		t.Fatalf("postgres.crt: %d certificates, want leaf + issuer", len(chain))
	}
	leaf, issuer := chain[0], chain[1]
	// RFC 5929 tls-server-end-point needs a hash from the leaf's signature
	// algorithm: ECDSA-SHA256, not Ed25519.
	if leaf.SignatureAlgorithm != x509.ECDSAWithSHA256 {
		t.Fatalf("leaf signature algorithm %v", leaf.SignatureAlgorithm)
	}
	if k, ok := leaf.PublicKey.(*ecdsa.PublicKey); !ok || k.Curve != elliptic.P256() {
		t.Fatalf("leaf key %T", leaf.PublicKey)
	}
	if !issuer.IsCA || issuer.MaxPathLen != 0 || !issuer.MaxPathLenZero ||
		!slices.Equal(issuer.PermittedDNSDomains, leaf.DNSNames) || !issuer.PermittedDNSDomainsCritical ||
		!slices.Equal(issuer.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}) ||
		issuer.SignatureAlgorithm != x509.PureEd25519 || !issuer.NotAfter.Equal(leaf.NotAfter) {
		t.Fatalf("issuer: %+v", issuer)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool(root), Intermediates: pool(issuer), DNSName: "postgres"}); err != nil {
		t.Fatal(err)
	}
	// Only the leaf key is stored: it matches the leaf, and it is the only
	// PEM block in the key file.
	key := readFile(t, l.KeyFile(ComponentPostgres))
	if strings.Count(string(key), "BEGIN") != 1 {
		t.Fatal("key file holds more than one key")
	}
	if _, err := tls.X509KeyPair(readFile(t, l.CertFile(ComponentPostgres)), key); err != nil {
		t.Fatal(err)
	}
	// The issuer cannot sign client identities or runner names.
	if !slices.Equal(issuer.PermittedURIDomains, []string{"invalid"}) || slices.Contains(issuer.PermittedDNSDomains, "runner") {
		t.Fatalf("issuer constraints %v %v", issuer.PermittedDNSDomains, issuer.PermittedURIDomains)
	}
}

func TestPostgresOnPathLenZeroCA(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "old"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour * 24 * 400),
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true, MaxPathLenZero: true,
		PermittedDNSDomains: []string{"runner", "postgres"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	ca := &CA{Cert: cert, CertPEM: encodeCert(der), Key: priv}
	if _, err := ca.Issue(ComponentPostgres, LeafOptions{}); err == nil || !strings.Contains(err.Error(), "path length 0") {
		t.Fatalf("got %v", err)
	}
	if _, err := ca.Issue(ComponentRunner, LeafOptions{}); err != nil {
		t.Fatalf("runner from a pathlen-0 CA: %v", err)
	}
}

func TestRunnerMTLSRejectsIntermediateChains(t *testing.T) {
	leaf, inter, root := &x509.Certificate{}, &x509.Certificate{}, &x509.Certificate{}
	if err := requireDirectChain(tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{leaf, root}}}); err != nil {
		t.Fatal(err)
	}
	if err := requireDirectChain(tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{leaf, inter, root}}}); err == nil {
		t.Fatal("chain with an intermediate accepted")
	}
	if err := requireDirectChain(tls.ConnectionState{}); err == nil {
		t.Fatal("no verified chain accepted")
	}
}
