package validate

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/targets"
	"github.com/jlentink/bekup/internal/runnerpb"
)

func TestIsPublicAnchor(t *testing.T) {
	f := getFixtures(t)
	now := time.Now()
	// "public" stands in for a system trust store root.
	pubKey := must(ecdsa.GenerateKey(elliptic.P256(), rand.Reader))
	pubTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(10), Subject: pkix.Name{CommonName: "Public Root CA"},
		NotBefore: now.Add(-24 * time.Hour), NotAfter: now.Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	pub := must(x509.ParseCertificate(mustCert(t, pubTmpl, pubTmpl, &pubKey.PublicKey, pubKey)))
	interKey := must(ecdsa.GenerateKey(elliptic.P256(), rand.Reader))
	interTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(11), Subject: pkix.Name{CommonName: "Public Issuing CA"},
		NotBefore: now.Add(-24 * time.Hour), NotAfter: now.Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	inter := must(x509.ParseCertificate(mustCert(t, interTmpl, pub, &interKey.PublicKey, pubKey)))
	leafKey := must(ecdsa.GenerateKey(elliptic.P256(), rand.Reader))
	expiredTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(12), Subject: pkix.Name{CommonName: "db.example.com"}, DNSNames: []string{"db.example.com"},
		NotBefore: now.Add(-20 * time.Hour), NotAfter: now.Add(-10 * time.Hour),
	}
	expiredLeaf := must(x509.ParseCertificate(mustCert(t, expiredTmpl, pub, &leafKey.PublicKey, pubKey)))
	roots := x509.NewCertPool()
	roots.AddCert(pub)

	for name, tc := range map[string]struct {
		cert *x509.Certificate
		want bool
	}{
		"public root":            {pub, true},
		"public intermediate":    {inter, true},
		"expired public leaf":    {expiredLeaf, true},
		"private ca":             {must(x509.ParseCertificate(f.caDER)), false},
		"leaf of the private ca": {must(x509.ParseCertificate(f.leafDER)), false},
		"self-signed leaf":       {must(x509.ParseCertificate(f.selfSignedLeaf)), false},
		"nil":                    {nil, false},
	} {
		if got := isPublicAnchor(tc.cert, roots); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
	if isPublicAnchor(pub, nil) {
		t.Error("no trust store must report false")
	}
	// The real system store does not contain the test CAs.
	if IsPublicAnchor(must(x509.ParseCertificate(f.caDER))) {
		t.Error("private test CA reported as public")
	}
}

// TestPinsRefusePublicAnchor: a request pinning a public CA is refused.
func TestPinsRefusePublicAnchor(t *testing.T) {
	f := getFixtures(t)
	orig := publicAnchor
	t.Cleanup(func() { publicAnchor = orig })
	publicAnchor = func(c *x509.Certificate) bool { return c.IsCA }
	_, err := checkPins("target.pins", engine.TLSPinned, []*runnerpb.CertificatePin{{AnchorDer: f.caDER, LeafSpkiSha256: f.leafSPKI}})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("public anchor accepted: %v", err)
	}
	if _, err := checkPins("target.pins", engine.TLSPinned, []*runnerpb.CertificatePin{{AnchorDer: f.selfSignedLeaf, LeafSpkiSha256: f.selfSignedSPKI}}); err != nil {
		t.Fatalf("self-signed leaf refused: %v", err)
	}
}

// TestBastionTargetsInValidation: a bastion with a bastion_targets list
// only forwards to its listed targets (Probe and FetchServerCertificate).
func TestBastionTargetsInValidation(t *testing.T) {
	cat, err := engine.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	g, err := targets.New(targets.Options{
		AllowCIDRs:     []string{"10.20.0.0/16", "10.30.0.0/16"},
		AllowHosts:     []string{"public.example.com", "loop.example.com"},
		BastionTargets: []string{"bastion.example.com=db.internal.lan", "bastion.example.com=127.0.0.1/32"},
		Lookup:         testLookup,
	})
	if err != nil {
		t.Fatal(err)
	}
	v, err := New(cat, g)
	if err != nil {
		t.Fatal(err)
	}
	for host, ok := range map[string]bool{"db.internal.lan": true, "127.0.0.1": true, "192.168.1.5": false, "other.lan": false} {
		tg := validTarget(t)
		tg.SshTunnel = validSSH(t)
		tg.Host = host
		_, err := v.Probe(t.Context(), &runnerpb.ProbeRequest{Ctx: runCtx(), Target: tg})
		if ok && err != nil {
			t.Errorf("%s: %v", host, err)
		}
		if !ok && status.Code(err) != codes.PermissionDenied {
			t.Errorf("%s: %v, want PermissionDenied", host, err)
		}
	}
	_, err = v.FetchServerCertificate(t.Context(), &runnerpb.FetchServerCertificateRequest{
		Ctx: runCtx(), Engine: runnerpb.Engine_ENGINE_POSTGRES, Host: "other.lan", Port: 5432, SshTunnel: validSSH(t)})
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("fetch through bastion: %v", err)
	}
}
