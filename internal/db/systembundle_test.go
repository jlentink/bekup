package db

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/config"
)

// negativeSerialPEM returns a self-signed CA certificate whose serial
// number is negative. Go's parser refuses such certificates (since Go
// 1.23), but distribution bundles have carried them; the DER is made with
// a positive serial and its first serial byte is flipped afterwards (the
// signature then no longer matches, which does not matter for a parse
// failure).
func negativeSerialPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial := []byte{0x7a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5b}
	tmpl := &x509.Certificate{
		SerialNumber:          new(big.Int).SetBytes(serial),
		Subject:               pkix.Name{CommonName: "negative serial test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	// INTEGER (0x02), length 8, the serial bytes.
	needle := append([]byte{0x02, 0x08}, serial...)
	i := bytes.Index(der, needle)
	if i < 0 || bytes.Contains(der[i+1:], needle) {
		t.Fatal("serial number not found exactly once in the DER")
	}
	der[i+2] = 0xfa // the high bit set: a negative INTEGER
	if _, err := x509.ParseCertificate(der); err == nil {
		t.Fatal("the negative-serial certificate parses; the test no longer covers the lenient path")
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestParseSystemCABundleSkipsUnparsable(t *testing.T) {
	pki := newTestPKI(t)
	bad := negativeSerialPEM(t)
	bundle := bytes.Join([][]byte{bad, pki.ca.CertPEM, []byte("# a comment line\n")}, nil)
	pool, st, err := ParseSystemCABundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if st.Loaded != 1 || st.Skipped != 1 {
		t.Fatalf("stats %+v, want 1 loaded and 1 skipped", st)
	}
	chain := chainOf(t, pki.server)
	inter := x509.NewCertPool()
	inter.AddCert(chain[1])
	if _, err := pki.server.Cert.Verify(x509.VerifyOptions{Roots: pool, Intermediates: inter, DNSName: "postgres"}); err != nil {
		t.Fatalf("the parsed CA is not a trust anchor: %v", err)
	}
	// Nothing usable: an error.
	for name, data := range map[string][]byte{"only bad": bad, "empty": nil, "not PEM": []byte("nope")} {
		if _, _, err := ParseSystemCABundle(data); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// The strict parser of operator CA files refuses the same bundle.
	dir := t.TempDir()
	cfg := config.DefaultBekup()
	cfg.Database.TLSCAFile = writeTestFile(t, dir, "operator-ca.crt", bundle)
	if _, err := LoadTLSOptions(&cfg); err == nil {
		t.Fatal("an operator CA file with an unparsable certificate was accepted")
	}
}

func TestLoadTLSOptionsSystemBundleLenient(t *testing.T) {
	pki := newTestPKI(t)
	dir := t.TempDir()
	path := writeTestFile(t, dir, "ca-certificates.crt", bytes.Join([][]byte{negativeSerialPEM(t), pki.ca.CertPEM}, nil))
	old := systemCABundlePath
	systemCABundlePath = path
	t.Cleanup(func() { systemCABundlePath = old })

	cfg := config.DefaultBekup()
	cfg.Database.TLSCAFile = path
	o, err := LoadTLSOptions(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if o.RootCAs == nil || o.SystemBundle == nil || o.SystemBundle.Loaded != 1 || o.SystemBundle.Skipped != 1 {
		t.Fatalf("%+v", o)
	}
	var log bytes.Buffer
	o.LogSystemBundle(context.Background(), slog.New(slog.NewTextHandler(&log, nil)))
	if !strings.Contains(log.String(), "loaded=1") || !strings.Contains(log.String(), "skipped=1") {
		t.Fatalf("log line: %s", log.String())
	}
	// A bundle without a usable certificate fails.
	path2 := writeTestFile(t, dir, "only-bad.crt", negativeSerialPEM(t))
	systemCABundlePath = path2
	cfg.Database.TLSCAFile = path2
	if _, err := LoadTLSOptions(&cfg); err == nil {
		t.Fatal("a system bundle without a usable certificate was accepted")
	}
	// Other paths stay strict and carry no bundle stats.
	cfg.Database.TLSCAFile = writeTestFile(t, dir, "ca.crt", pki.ca.CertPEM)
	if o, err = LoadTLSOptions(&cfg); err != nil || o.SystemBundle != nil {
		t.Fatalf("operator CA file: %+v %v", o, err)
	}
	o.LogSystemBundle(context.Background(), slog.New(slog.NewTextHandler(&log, nil)))
	if strings.Count(log.String(), "system CA bundle") != 1 {
		t.Fatalf("logged without a system bundle: %s", log.String())
	}
}

func TestSystemCABundlePathIsConfigs(t *testing.T) {
	if systemCABundlePath != config.SystemCABundle || filepath.Clean(config.SystemCABundle) != config.SystemCABundle {
		t.Fatalf("systemCABundlePath %q, config.SystemCABundle %q", systemCABundlePath, config.SystemCABundle)
	}
}
