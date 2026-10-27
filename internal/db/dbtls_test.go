package db

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/logging"
)

func TestCheckURLTLS(t *testing.T) {
	tests := []struct {
		url, mode, wantErr string
	}{
		{"postgres://u:pw@db/bekup", config.DBTLSVerifyFull, ""},
		{"postgresql://u:pw@db/bekup?sslmode=disable", config.DBTLSVerifyFull, ""},
		{"postgres://u:pw@db/bekup?sslmode=prefer", config.DBTLSRequire, ""},
		{"postgres://u:pw@db/bekup?sslmode=verify-full", config.DBTLSVerifyFull, ""},
		{"postgres://u:pw@db/bekup?sslmode=verify-full", config.DBTLSRequire, "stricter than"},
		{"postgres://u:pw@db/bekup?sslmode=verify-ca", config.DBTLSRequire, "stricter than"},
		{"postgres://u:pw@db/bekup?sslmode=require", config.DBTLSDisable, "stricter than"},
		{"postgres://u:pw@db/bekup?sslmode=prefer", config.DBTLSDisable, "stricter than"},
		{"postgres://u:pw@db/bekup?sslmode=bogus", config.DBTLSVerifyFull, "not a valid"},
		{"postgres://u:pw@db/bekup?sslrootcert=/tmp/ca.crt", config.DBTLSVerifyFull, "sslrootcert is not supported"},
		{"postgres://u:pw@db/bekup?sslkey=/tmp/k", config.DBTLSVerifyFull, "sslkey is not supported"},
		{"host=db password=hunter2-pw sslmode=disable", config.DBTLSVerifyFull, "not a valid"},
		{"mysql://u:hunter2-pw@db/x", config.DBTLSVerifyFull, "not a valid"},
	}
	for _, tc := range tests {
		_, err := checkURLTLS([]byte(tc.url), tc.mode)
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("%s (%s): %v", tc.url, tc.mode, err)
		case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
			t.Errorf("%s (%s): got %v, want %q", tc.url, tc.mode, err, tc.wantErr)
		}
		if err != nil && strings.Contains(err.Error(), "pw") {
			t.Errorf("error leaks the URL: %v", err)
		}
	}
}

func parsed(t *testing.T, url string) *pgconn.Config {
	t.Helper()
	cc, err := pgconn.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	return cc
}

func TestApplyTLSOverridesURL(t *testing.T) {
	roots := x509.NewCertPool()
	vf := TLSOptions{Mode: config.DBTLSVerifyFull, RootCAs: roots}

	// No sslmode means libpq "prefer": TLS without verification plus a
	// plaintext fallback. Both must be replaced.
	url := "postgres://u:pw@db.example:5433,db2.example:5434/bekup?sslmode=prefer"
	cc := parsed(t, url)
	if err := applyTLS(cc, []byte(url), vf); err != nil {
		t.Fatal(err)
	}
	check := func(host string, c *tls.Config) {
		t.Helper()
		if c == nil || c.InsecureSkipVerify || c.ServerName != host || c.RootCAs != roots || c.MinVersion != tls.VersionTLS12 {
			t.Fatalf("%s: TLS config %+v", host, c)
		}
	}
	check("db.example", cc.TLSConfig)
	if len(cc.Fallbacks) != 1 || cc.Fallbacks[0].Host != "db2.example" {
		t.Fatalf("fallbacks: %+v", cc.Fallbacks)
	}
	check("db2.example", cc.Fallbacks[0].TLSConfig)

	// The configured mode beats a weaker sslmode in the URL.
	url = "postgres://u:pw@10.0.0.7/bekup?sslmode=disable"
	cc = parsed(t, url)
	if err := applyTLS(cc, []byte(url), vf); err != nil {
		t.Fatal(err)
	}
	check("10.0.0.7", cc.TLSConfig)
	if len(cc.Fallbacks) != 0 {
		t.Fatalf("fallbacks: %+v", cc.Fallbacks)
	}

	// disable: no TLS at all, no fallbacks.
	url = "postgres://u:pw@db/bekup"
	cc = parsed(t, url)
	if err := applyTLS(cc, []byte(url), TLSOptions{Mode: config.DBTLSDisable}); err != nil {
		t.Fatal(err)
	}
	if cc.TLSConfig != nil || len(cc.Fallbacks) != 0 {
		t.Fatalf("disable: %+v %+v", cc.TLSConfig, cc.Fallbacks)
	}

	// require without a CA: encrypted, unverified.
	cc = parsed(t, url)
	if err := applyTLS(cc, []byte(url), TLSOptions{Mode: config.DBTLSRequire}); err != nil {
		t.Fatal(err)
	}
	if cc.TLSConfig == nil || !cc.TLSConfig.InsecureSkipVerify || cc.TLSConfig.VerifyConnection != nil {
		t.Fatalf("require: %+v", cc.TLSConfig)
	}

	// Unix sockets cannot use TLS: refused in TLS modes, fine with disable.
	url = "postgres://u:pw@/bekup?host=/var/run/postgresql"
	if err := applyTLS(parsed(t, url), []byte(url), vf); err == nil || !strings.Contains(err.Error(), "Unix-socket") {
		t.Fatalf("socket with verify-full: %v", err)
	}
	cc = parsed(t, url)
	if err := applyTLS(cc, []byte(url), TLSOptions{Mode: config.DBTLSDisable}); err != nil || cc.TLSConfig != nil {
		t.Fatalf("socket with disable: %v", err)
	}

	// Invalid options.
	for _, o := range []TLSOptions{{}, {Mode: config.DBTLSVerifyFull}, {Mode: "prefer"}} {
		if err := applyTLS(parsed(t, url), []byte(url), o); err == nil {
			t.Errorf("%+v accepted", o)
		}
	}
}

type testPKI struct {
	ca     *certs.CA
	server *certs.Leaf
	client *certs.Leaf
}

func newTestPKI(t *testing.T) testPKI {
	t.Helper()
	ca, err := certs.NewCA(certs.CAOptions{ServerNames: []string{"runner", "postgres"}})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := ca.Issue(certs.ComponentPostgres, certs.LeafOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cli, err := ca.Issue(certs.ComponentServe, certs.LeafOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return testPKI{ca: ca, server: srv, client: cli}
}

// chainOf returns the certificates of a leaf's CertPEM (postgres: leaf +
// issuer).
func chainOf(t *testing.T, l *certs.Leaf) []*x509.Certificate {
	t.Helper()
	c, err := certs.ParseCertificates(l.CertPEM)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRequireWithCAVerifiesChainOnly(t *testing.T) {
	good, other := newTestPKI(t), newTestPKI(t)
	roots := x509.NewCertPool()
	roots.AddCert(good.ca.Cert)
	c := TLSOptions{Mode: config.DBTLSRequire, RootCAs: roots}.clientConfig("10.1.2.3")
	if c.VerifyConnection == nil {
		t.Fatal("require + CA must verify the chain")
	}
	// A wrong host name is accepted (verify-ca semantics) ...
	if err := c.VerifyConnection(tls.ConnectionState{PeerCertificates: chainOf(t, good.server)}); err != nil {
		t.Fatal(err)
	}
	// ... another CA is not.
	if err := c.VerifyConnection(tls.ConnectionState{PeerCertificates: chainOf(t, other.server)}); err == nil {
		t.Fatal("certificate from another CA accepted")
	}
	// Client certificates (clientAuth only) are not server certificates.
	if err := c.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{good.client.Cert}}); err == nil {
		t.Fatal("client certificate accepted as server certificate")
	}
	if err := c.VerifyConnection(tls.ConnectionState{}); err == nil {
		t.Fatal("no certificate accepted")
	}
}

func writeTestFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadTLSOptions(t *testing.T) {
	pki := newTestPKI(t)
	dir := t.TempDir()
	cfg := config.DefaultBekup()
	cfg.Database.TLSCAFile = writeTestFile(t, dir, "ca.crt", pki.ca.CertPEM)
	o, err := LoadTLSOptions(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if o.Mode != config.DBTLSVerifyFull || o.RootCAs == nil || len(o.Certificates) != 0 {
		t.Fatalf("%+v", o)
	}
	chain := chainOf(t, pki.server)
	inter := x509.NewCertPool()
	inter.AddCert(chain[1])
	if _, err := pki.server.Cert.Verify(x509.VerifyOptions{Roots: o.RootCAs, Intermediates: inter, DNSName: "postgres"}); err != nil {
		t.Fatal(err)
	}

	cfg.Database.TLSClientCertFile = writeTestFile(t, dir, "c.crt", pki.client.CertPEM)
	cfg.Database.TLSClientKeyFile = writeTestFile(t, dir, "c.key", pki.client.KeyPEM)
	if o, err = LoadTLSOptions(&cfg); err != nil || len(o.Certificates) != 1 {
		t.Fatalf("client cert: %+v %v", o, err)
	}

	// Mismatched key, non-PEM CA, missing CA.
	other := newTestPKI(t)
	cfg.Database.TLSClientKeyFile = writeTestFile(t, dir, "o.key", other.client.KeyPEM)
	if _, err := LoadTLSOptions(&cfg); err == nil {
		t.Fatal("mismatched client key accepted")
	}
	cfg.Database.TLSClientCertFile, cfg.Database.TLSClientKeyFile = "", ""
	cfg.Database.TLSCAFile = writeTestFile(t, dir, "bad.crt", []byte("nope"))
	if _, err := LoadTLSOptions(&cfg); err == nil {
		t.Fatal("non-PEM CA accepted")
	}
	cfg.Database.TLSCAFile = filepath.Join(dir, "missing.crt")
	if _, err := LoadTLSOptions(&cfg); err == nil {
		t.Fatal("missing CA accepted")
	}
	cfg.Database.TLSMode = config.DBTLSDisable
	if o, err := LoadTLSOptions(&cfg); err != nil || o.RootCAs != nil {
		t.Fatalf("disable reads no files: %+v %v", o, err)
	}
}

func TestPoolConfigRequiresTLSMode(t *testing.T) {
	_, err := poolConfig([]byte("postgres://u:pw@db/bekup"), PoolOptions{Registry: logging.NewRegistry()})
	if err == nil || !strings.Contains(err.Error(), "TLS mode not set") {
		t.Fatalf("got %v", err)
	}
	_, err = poolConfig([]byte("postgres://u:pw@db/bekup?sslmode=verify-full"), PoolOptions{
		Registry: logging.NewRegistry(), TLS: TLSOptions{Mode: config.DBTLSRequire},
	})
	if err == nil || errors.Is(err, ErrInvalidURL) || !strings.Contains(err.Error(), "stricter") {
		t.Fatalf("got %v", err)
	}
}

func TestApplyTLSChannelBinding(t *testing.T) {
	tests := []struct {
		mode, query, want, wantErr string
	}{
		{config.DBTLSRequire, "", "require", ""},
		{config.DBTLSVerifyFull, "?channel_binding=require", "require", ""},
		{config.DBTLSVerifyFull, "?channel_binding=prefer", "", "would weaken"},
		{config.DBTLSRequire, "?channel_binding=disable", "", "would weaken"},
		{config.DBTLSDisable, "", "disable", ""},
		{config.DBTLSDisable, "?channel_binding=prefer", "disable", ""},
		{config.DBTLSDisable, "?channel_binding=require", "", "needs TLS"},
	}
	for _, tc := range tests {
		url := "postgres://u:pw@db/bekup" + tc.query
		cc := parsed(t, url)
		o := TLSOptions{Mode: tc.mode, RootCAs: x509.NewCertPool()}
		err := applyTLS(cc, []byte(url), o)
		switch {
		case tc.wantErr != "":
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s %s: got %v", tc.mode, tc.query, err)
			}
		case err != nil || cc.ChannelBinding != tc.want:
			t.Errorf("%s %s: channel binding %q, %v", tc.mode, tc.query, cc.ChannelBinding, err)
		}
	}
}
