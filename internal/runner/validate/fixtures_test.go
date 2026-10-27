package validate

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"google.golang.org/protobuf/proto"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/targets"
	"github.com/jlentink/bekup/internal/runnerpb"
)

const (
	testRunID    = "0b7f5a3c-2c1e-4d7a-9a51-8c3f0e6d2b19"
	testPassword = "S3cr3t-Pa$$word\"'\\"
	testSSHPass  = "ssh-SECRET-pass"
)

// fixtures are generated once per test binary.
type fixtures struct {
	caDER, leafDER  []byte
	leafSPKI        []byte
	clientCertPEM   []byte
	clientKeyPEM    []byte
	sshKeyPEM       []byte
	hostKeyLine     string
	hostKeyFP       string
	selfSignedLeaf  []byte
	selfSignedSPKI  []byte
	encryptedKeyPEM []byte
}

var (
	fixOnce sync.Once
	fix     fixtures
)

// must panics on err (fixture generation only) and returns v.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func mustCert(t testing.TB, tmpl, parent *x509.Certificate, pub, priv any) []byte {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func getFixtures(t testing.TB) *fixtures {
	t.Helper()
	fixOnce.Do(func() {
		now := time.Now()
		caKey := must(ecdsa.GenerateKey(elliptic.P256(), rand.Reader))
		caTmpl := &x509.Certificate{
			SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test ca"},
			NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
			IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
		}
		fix.caDER = mustCert(t, caTmpl, caTmpl, &caKey.PublicKey, caKey)
		ca := must(x509.ParseCertificate(fix.caDER))

		leafKey := must(ecdsa.GenerateKey(elliptic.P256(), rand.Reader))
		leafTmpl := &x509.Certificate{
			SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "db.example.com"},
			DNSNames:  []string{"db.example.com"},
			NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		}
		fix.leafDER = mustCert(t, leafTmpl, ca, &leafKey.PublicKey, caKey)
		leaf := must(x509.ParseCertificate(fix.leafDER))
		sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
		fix.leafSPKI = sum[:]

		ssKey := must(ecdsa.GenerateKey(elliptic.P256(), rand.Reader))
		ssTmpl := &x509.Certificate{
			SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "db.example.com"},
			DNSNames:  []string{"db.example.com"},
			NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		}
		fix.selfSignedLeaf = mustCert(t, ssTmpl, ssTmpl, &ssKey.PublicKey, ssKey)
		ss := must(x509.ParseCertificate(fix.selfSignedLeaf))
		ssSum := sha256.Sum256(ss.RawSubjectPublicKeyInfo)
		fix.selfSignedSPKI = ssSum[:]

		clKey := must(ecdsa.GenerateKey(elliptic.P256(), rand.Reader))
		clTmpl := &x509.Certificate{
			SerialNumber: big.NewInt(4), Subject: pkix.Name{CommonName: "bekup"},
			NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		}
		clDER := mustCert(t, clTmpl, ca, &clKey.PublicKey, caKey)
		fix.clientCertPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clDER})
		k8 := must(x509.MarshalPKCS8PrivateKey(clKey))
		fix.clientKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: k8})
		fix.encryptedKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Headers: map[string]string{"Proc-Type": "4,ENCRYPTED"}, Bytes: k8})

		_, edPriv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		blk := must(ssh.MarshalPrivateKey(edPriv, ""))
		fix.sshKeyPEM = pem.EncodeToMemory(blk)

		hostPub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		sp := must(ssh.NewPublicKey(hostPub))
		fix.hostKeyLine = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp)))
		fix.hostKeyFP = ssh.FingerprintSHA256(sp)
	})
	return &fix
}

// resolver maps every name below example.com to fixed addresses.
var testDNS = map[string][]netip.Addr{
	"db.example.com":       {netip.MustParseAddr("10.20.0.5")},
	"bastion.example.com":  {netip.MustParseAddr("10.30.0.2")},
	"loop.example.com":     {netip.MustParseAddr("127.0.0.1")},
	"metadata.example.com": {netip.MustParseAddr("169.254.169.254")},
	"public.example.com":   {netip.MustParseAddr("203.0.113.10")},
}

func testLookup(_ context.Context, _, host string) ([]netip.Addr, error) {
	if a, ok := testDNS[host]; ok {
		return a, nil
	}
	return nil, &lookupError{}
}

type lookupError struct{}

func (*lookupError) Error() string { return "no such host" }

func newTestValidator(t testing.TB) *Validator {
	t.Helper()
	cat, err := engine.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	g, err := targets.New(targets.Options{
		AllowCIDRs: []string{"10.20.0.0/16", "10.30.0.0/16"},
		AllowHosts: []string{"public.example.com", "loop.example.com"},
		DenyCIDRs:  []string{"10.20.66.0/24"},
		DenyHosts:  []string{"old.example.com"},
		Lookup:     testLookup,
	})
	if err != nil {
		t.Fatal(err)
	}
	v, err := New(cat, g)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func validTarget(t testing.TB) *runnerpb.Target {
	f := getFixtures(t)
	return &runnerpb.Target{
		Engine:     runnerpb.Engine_ENGINE_POSTGRES,
		VersionKey: "17",
		Host:       "db.example.com",
		Port:       5432,
		User:       "backup",
		Password:   []byte(testPassword),
		TlsMode:    runnerpb.TlsMode_TLS_MODE_PINNED,
		Pins: []*runnerpb.CertificatePin{
			{AnchorDer: f.caDER, LeafSpkiSha256: f.leafSPKI},
		},
	}
}

func validSSH(t testing.TB) *runnerpb.SshTunnel {
	f := getFixtures(t)
	return &runnerpb.SshTunnel{
		Host:          "bastion.example.com",
		Port:          22,
		User:          "tunnel",
		PrivateKeyPem: append([]byte(nil), f.sshKeyPEM...),
		HostKeyPin:    &runnerpb.HostKeyPin{AuthorizedKey: f.hostKeyLine},
	}
}

func runCtx() *runnerpb.RunContext { return &runnerpb.RunContext{RunId: testRunID} }

// secretsOf lists the secret values of a request for leak checks.
func secretsOf(m proto.Message, minLen int) []string {
	var out []string
	var walk func(proto.Message)
	walk = func(m proto.Message) {
		switch x := m.(type) {
		case *runnerpb.Target:
			if x == nil {
				return
			}
			out = append(out, string(x.GetPassword()))
			if cc := x.GetClientCertificate(); cc != nil {
				out = append(out, string(cc.GetKeyPem()))
			}
			if st := x.GetSshTunnel(); st != nil {
				walk(st)
			}
		case *runnerpb.SshTunnel:
			if x == nil {
				return
			}
			out = append(out, string(x.GetPassword()), string(x.GetPrivateKeyPem()), string(x.GetPassphrase()))
		}
	}
	switch x := m.(type) {
	case *runnerpb.ProbeRequest:
		walk(x.GetTarget())
	case *runnerpb.DumpRequest:
		walk(x.GetTarget())
	case *runnerpb.DumpGlobalsRequest:
		walk(x.GetTarget())
	case *runnerpb.CreateDatabaseRequest:
		walk(x.GetTarget())
	case *runnerpb.RenameSwapDatabaseRequest:
		walk(x.GetTarget())
	case *runnerpb.RestoreRequest:
		walk(x.GetHeader().GetTarget())
	case *runnerpb.FetchServerCertificateRequest:
		walk(x.GetSshTunnel())
	}
	var filtered []string
	for _, s := range out {
		if len(s) >= minLen {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

func assertNoLeak(t testing.TB, m proto.Message, err error) {
	t.Helper()
	assertNoLeakMin(t, m, err, 6)
}

// assertNoLeakMin flags secret values of at least minLen bytes that occur
// in the error message. Fuzzing uses a larger minimum so that short
// secrets which happen to equal words of the static messages do not
// produce false positives.
func assertNoLeakMin(t testing.TB, m proto.Message, err error, minLen int) {
	t.Helper()
	if err == nil {
		return
	}
	msg := err.Error()
	for _, s := range secretsOf(m, minLen) {
		if strings.Contains(msg, s) {
			t.Fatalf("error message leaks a secret: %q", msg)
		}
	}
}
