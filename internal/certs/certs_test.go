package certs

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const testPass = "correct horse battery staple"

// testWorkFactor keeps scrypt fast in tests.
const testWorkFactor = 10

func initDir(t *testing.T, mod ...func(*InitOptions)) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "certs")
	opts := InitOptions{Dir: dir, Passphrase: []byte(testPass), workFactor: testWorkFactor}
	for _, m := range mod {
		m(&opts)
	}
	if _, err := Init(opts); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return dir
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- test file
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func readCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	certs := readCerts(t, path)
	if len(certs) != 1 {
		t.Fatalf("%s: %d certificates, want 1", path, len(certs))
	}
	return certs[0]
}

func readCerts(t *testing.T, path string) []*x509.Certificate {
	t.Helper()
	certs, err := ParseCertificates(readFile(t, path))
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return certs
}

func pool(certs ...*x509.Certificate) *x509.CertPool {
	p := x509.NewCertPool()
	for _, c := range certs {
		p.AddCert(c)
	}
	return p
}

func TestInitLayoutAndPermissions(t *testing.T) {
	dir := initDir(t)
	l := Layout{Dir: dir}

	want := map[string]os.FileMode{
		"ca/ca.crt":         0o644,
		"ca/ca.key":         0o600,
		"runner/runner.crt": 0o644,
		"runner/runner.key": 0o600,
		"runner/ca.crt":     0o644,
		"serve/serve.crt":   0o644,
		"serve/serve.key":   0o600,
		"serve/ca.crt":      0o644,
		"worker/worker.crt": 0o644,
		"worker/worker.key": 0o600,
		"worker/ca.crt":     0o644,
	}
	var got []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if d.IsDir() {
			if rel != "." && fi.Mode().Perm() != 0o700 {
				t.Errorf("%s: dir mode %04o, want 0700", rel, fi.Mode().Perm())
			}
			return nil
		}
		got = append(got, rel)
		if m, ok := want[rel]; !ok {
			t.Errorf("unexpected file %s", rel)
		} else if fi.Mode().Perm() != m {
			t.Errorf("%s: mode %04o, want %04o", rel, fi.Mode().Perm(), m)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Errorf("files = %v, want %d files", got, len(want))
	}

	// No component directory contains CA key material.
	caKey, err := os.ReadFile(l.CAKey())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range Components() {
		entries, err := os.ReadDir(l.ComponentDir(c))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			data, err := os.ReadFile(filepath.Join(l.ComponentDir(c), e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(data, caKey) || strings.Contains(e.Name(), "ca.key") {
				t.Errorf("%s/%s contains CA key material", c, e.Name())
			}
		}
	}
	if !IsEncryptedKey(caKey) {
		t.Error("CA key is not encrypted")
	}
}

func TestInitCertificates(t *testing.T) {
	dir := initDir(t, func(o *InitOptions) { o.RunnerNames = []string{"bekup-runner", "10.0.0.5"} })
	l := Layout{Dir: dir}
	ca := readCert(t, l.CACert())

	if _, ok := ca.PublicKey.(ed25519.PublicKey); !ok {
		t.Fatal("CA key is not ed25519")
	}
	if !ca.IsCA || ca.MaxPathLen != 0 || !ca.MaxPathLenZero || ca.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Errorf("CA constraints wrong: isCA=%v maxPathLen=%d", ca.IsCA, ca.MaxPathLen)
	}
	if !slices.Equal(ca.PermittedDNSDomains, []string{"runner", "bekup-runner"}) ||
		!slices.Equal(ca.PermittedURIDomains, []string{TrustDomain}) || len(ca.PermittedIPRanges) != 1 {
		t.Errorf("name constraints: dns=%v uri=%v ip=%v", ca.PermittedDNSDomains, ca.PermittedURIDomains, ca.PermittedIPRanges)
	}
	if v := ca.NotAfter.Sub(ca.NotBefore); v < DefaultCAValidity || v > DefaultCAValidity+time.Hour {
		t.Errorf("CA validity %s", v)
	}

	for _, c := range Components() {
		leaf := readCert(t, l.CertFile(c))
		if _, ok := leaf.PublicKey.(ed25519.PublicKey); !ok {
			t.Errorf("%s: not ed25519", c)
		}
		if leaf.IsCA {
			t.Errorf("%s: leaf is a CA", c)
		}
		eku := x509.ExtKeyUsageClientAuth
		if IsServer(c) {
			eku = x509.ExtKeyUsageServerAuth
		}
		if !slices.Equal(leaf.ExtKeyUsage, []x509.ExtKeyUsage{eku}) {
			t.Errorf("%s: EKU %v", c, leaf.ExtKeyUsage)
		}
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool(ca), KeyUsages: []x509.ExtKeyUsage{eku}}); err != nil {
			t.Errorf("%s: chain does not verify: %v", c, err)
		}
		bundle := readCerts(t, l.BundleFile(c))
		if len(bundle) != 1 || !bundle[0].Equal(ca) {
			t.Errorf("%s: bundle is not the CA", c)
		}
		if v := leaf.NotAfter.Sub(leaf.NotBefore); v < DefaultLeafValidity || v > DefaultLeafValidity+time.Hour {
			t.Errorf("%s: validity %s", c, v)
		}
	}

	runner := readCert(t, l.CertFile(ComponentRunner))
	if !slices.Equal(runner.DNSNames, []string{"runner", "bekup-runner"}) || len(runner.IPAddresses) != 1 ||
		runner.IPAddresses[0].String() != "10.0.0.5" || len(runner.URIs) != 0 {
		t.Errorf("runner SANs: dns=%v ip=%v uri=%v", runner.DNSNames, runner.IPAddresses, runner.URIs)
	}
	if err := runner.VerifyHostname("runner"); err != nil {
		t.Error(err)
	}
	for _, c := range []string{ComponentServe, ComponentWorker} {
		leaf := readCert(t, l.CertFile(c))
		if len(leaf.URIs) != 1 || leaf.URIs[0].String() != "spiffe://bekup/"+c || len(leaf.DNSNames) != 0 || len(leaf.IPAddresses) != 0 {
			t.Errorf("%s SANs: uri=%v dns=%v", c, leaf.URIs, leaf.DNSNames)
		}
	}
}

func TestNameConstraintsEnforced(t *testing.T) {
	dir := initDir(t)
	l := Layout{Dir: dir}
	ca, err := LoadCA(l.CACert(), l.CAKey(), []byte(testPass))
	if err != nil {
		t.Fatal(err)
	}
	// Issue refuses names outside the constraints.
	if _, err := ca.Issue(ComponentRunner, LeafOptions{ServerNames: []string{"evil.example.com"}}); err == nil {
		t.Error("Issue accepted a name outside the CA's constraints")
	}
	if _, err := ca.Issue(ComponentRunner, LeafOptions{ServerNames: []string{"192.0.2.1"}}); err == nil {
		t.Error("Issue accepted an IP although the CA excludes all IPs")
	}
	// A leaf forged with the CA key outside the constraints does not verify.
	for _, tmpl := range []*x509.Certificate{
		{DNSNames: []string{"www.example.com"}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}},
		{URIs: nil, EmailAddresses: nil, DNSNames: []string{"runner.evil"}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}},
	} {
		tmpl.SerialNumber = big.NewInt(42)
		tmpl.Subject = pkix.Name{CommonName: "forged"}
		tmpl.NotBefore = time.Now().Add(-time.Hour)
		tmpl.NotAfter = time.Now().Add(time.Hour)
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, pub, ca.Key)
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool(ca.Cert)}); err == nil {
			t.Errorf("forged leaf %v verified", tmpl.DNSNames)
		}
	}
	// A client URI in another trust domain does not verify either.
	_, err = ParseClientID("spiffe://other/serve")
	if err == nil {
		t.Error("ParseClientID accepted another trust domain")
	}
}

func TestInitRefusesOverwrite(t *testing.T) {
	dir := initDir(t)
	l := Layout{Dir: dir}
	before := readFile(t, l.CACert())

	_, err := Init(InitOptions{Dir: dir, Passphrase: []byte(testPass), workFactor: testWorkFactor})
	if !errors.Is(err, ErrExists) {
		t.Fatalf("second Init: err = %v, want ErrExists", err)
	}
	after := readFile(t, l.CACert())
	if !bytes.Equal(before, after) {
		t.Fatal("refused Init changed the CA")
	}
	// Even a single existing file blocks init.
	dir2 := filepath.Join(t.TempDir(), "c")
	if err := os.MkdirAll(filepath.Join(dir2, "serve"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir2, "serve", "serve.key"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(InitOptions{Dir: dir2, Passphrase: []byte(testPass), workFactor: testWorkFactor}); !errors.Is(err, ErrExists) {
		t.Fatalf("Init over a single file: err = %v", err)
	}

	if _, err := Init(InitOptions{Dir: dir, Passphrase: []byte(testPass), Force: true, workFactor: testWorkFactor}); err != nil {
		t.Fatalf("forced Init: %v", err)
	}
	after = readFile(t, l.CACert())
	if bytes.Equal(before, after) {
		t.Fatal("forced Init kept the old CA")
	}
}

func TestInitCAKeyProtection(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "c")
	if _, err := Init(InitOptions{Dir: dir}); err == nil {
		t.Error("Init without passphrase and without AllowUnencrypted succeeded")
	}
	if _, err := Init(InitOptions{Dir: dir, Passphrase: []byte("short")}); err == nil {
		t.Error("Init accepted a short passphrase")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("a refused Init created the directory")
	}

	enc := initDir(t)
	l := Layout{Dir: enc}
	if _, err := LoadCA(l.CACert(), l.CAKey(), nil); !errors.Is(err, ErrPassphraseRequired) {
		t.Errorf("LoadCA without passphrase: %v", err)
	}
	if _, err := LoadCA(l.CACert(), l.CAKey(), []byte("wrong passphrase, long enough")); err == nil {
		t.Error("LoadCA with a wrong passphrase succeeded")
	}
	if ok, err := CAKeyEncrypted(enc); err != nil || !ok {
		t.Errorf("CAKeyEncrypted = %v, %v", ok, err)
	}

	plain := initDir(t, func(o *InitOptions) { o.Passphrase = nil; o.AllowUnencrypted = true })
	if ok, err := CAKeyEncrypted(plain); err != nil || ok {
		t.Errorf("CAKeyEncrypted(plain) = %v, %v", ok, err)
	}
	pl := Layout{Dir: plain}
	if _, err := LoadCA(pl.CACert(), pl.CAKey(), nil); err != nil {
		t.Errorf("LoadCA(plain): %v", err)
	}
	// A world-readable CA key is refused.
	if err := os.Chmod(pl.CAKey(), 0o644); err != nil { // #nosec G302 -- deliberately unsafe for the test
		t.Fatal(err)
	}
	if _, err := LoadCA(pl.CACert(), pl.CAKey(), nil); err == nil {
		t.Error("LoadCA accepted a world-readable key")
	}
	// The key of another CA is refused.
	if _, err := LoadCA(pl.CACert(), l.CAKey(), []byte(testPass)); err == nil {
		t.Error("LoadCA accepted a mismatched key")
	}
}

func TestRotateLeaves(t *testing.T) {
	dir := initDir(t, func(o *InitOptions) { o.RunnerNames = []string{"bekup-runner"} })
	l := Layout{Dir: dir}
	caBefore := readFile(t, l.CACert())
	old := map[string]*x509.Certificate{}
	oldKeys := map[string][]byte{}
	for _, c := range Components() {
		old[c] = readCert(t, l.CertFile(c))
		oldKeys[c] = readFile(t, l.KeyFile(c))
	}

	if _, err := Rotate(RotateOptions{Dir: dir, Components: []string{ComponentServe}, Passphrase: []byte(testPass)}); err != nil {
		t.Fatalf("Rotate serve: %v", err)
	}
	if !readCert(t, l.CertFile(ComponentWorker)).Equal(old[ComponentWorker]) {
		t.Error("rotating serve changed worker")
	}
	if readCert(t, l.CertFile(ComponentServe)).Equal(old[ComponentServe]) {
		t.Error("serve was not reissued")
	}

	if _, err := Rotate(RotateOptions{Dir: dir, All: true, Passphrase: []byte(testPass), LeafValidity: 48 * time.Hour}); err != nil {
		t.Fatalf("Rotate all: %v", err)
	}
	caAfter := readFile(t, l.CACert())
	if !bytes.Equal(caBefore, caAfter) {
		t.Fatal("Rotate changed the CA")
	}
	ca := readCert(t, l.CACert())
	for _, c := range Components() {
		leaf := readCert(t, l.CertFile(c))
		if leaf.Equal(old[c]) || leaf.SerialNumber.Cmp(old[c].SerialNumber) == 0 {
			t.Errorf("%s: not replaced", c)
		}
		key := readFile(t, l.KeyFile(c))
		if bytes.Equal(key, oldKeys[c]) {
			t.Errorf("%s: key not replaced", c)
		}
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool(ca), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
			t.Errorf("%s: %v", c, err)
		}
		if v := leaf.NotAfter.Sub(leaf.NotBefore); v > 49*time.Hour {
			t.Errorf("%s: validity %s, want ~48h", c, v)
		}
	}
	if got := readCert(t, l.CertFile(ComponentRunner)).DNSNames; !slices.Equal(got, []string{"runner", "bekup-runner"}) {
		t.Errorf("runner names not preserved: %v", got)
	}

	// Errors: no passphrase, unknown component, nothing selected.
	if _, err := Rotate(RotateOptions{Dir: dir, All: true}); !errors.Is(err, ErrPassphraseRequired) {
		t.Errorf("Rotate without passphrase: %v", err)
	}
	if _, err := Rotate(RotateOptions{Dir: dir, Components: []string{"db"}, Passphrase: []byte(testPass)}); err == nil {
		t.Error("Rotate accepted an unknown component")
	}
	if _, err := Rotate(RotateOptions{Dir: dir, Passphrase: []byte(testPass)}); err == nil {
		t.Error("Rotate without selection succeeded")
	}
}

func TestRotateCA(t *testing.T) {
	dir := initDir(t)
	l := Layout{Dir: dir}
	oldCA := readCert(t, l.CACert())
	opts := RotateCAOptions{Dir: dir, Passphrase: []byte(testPass), workFactor: testWorkFactor}

	if _, err := RotateCA(withPhase(opts, CAPromote)); err == nil {
		t.Error("promote without stage succeeded")
	}
	if _, err := RotateCA(withPhase(opts, CAFinish)); err == nil {
		t.Error("finish without promote succeeded")
	}

	// stage: bundles trust old + next; leaves unchanged.
	if _, err := RotateCA(withPhase(opts, CAStage)); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if _, err := RotateCA(withPhase(opts, CAStage)); err == nil {
		t.Error("second stage succeeded")
	}
	if _, err := Rotate(RotateOptions{Dir: dir, All: true, Passphrase: []byte(testPass)}); err == nil {
		t.Error("leaf rotation during a staged CA rotation succeeded")
	}
	nextCA := readCert(t, l.nextCACert())
	if nextCA.Equal(oldCA) {
		t.Fatal("next CA equals the old CA")
	}
	if !slices.Equal(nextCA.PermittedDNSDomains, oldCA.PermittedDNSDomains) {
		t.Errorf("next CA constraints %v", nextCA.PermittedDNSDomains)
	}
	for _, c := range Components() {
		b := readCerts(t, l.BundleFile(c))
		if len(b) != 2 || !b[0].Equal(oldCA) || !b[1].Equal(nextCA) {
			t.Errorf("%s: staged bundle wrong", c)
		}
	}
	staged := snapshot(t, l)

	// promote: leaves issued by the new CA; bundles new + old.
	if _, err := RotateCA(withPhase(opts, CAPromote)); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if !readCert(t, l.CACert()).Equal(nextCA) || !readCert(t, l.prevCACert()).Equal(oldCA) {
		t.Fatal("promote did not swap the CAs")
	}
	if _, err := os.Stat(l.nextCAKey()); err == nil {
		t.Error("next CA key left behind")
	}
	if _, err := LoadCA(l.CACert(), l.CAKey(), []byte(testPass)); err != nil {
		t.Errorf("promoted CA does not load: %v", err)
	}
	for _, c := range Components() {
		leaf := readCert(t, l.CertFile(c))
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool(nextCA), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
			t.Errorf("%s: not issued by the new CA: %v", c, err)
		}
		b := readCerts(t, l.BundleFile(c))
		if len(b) != 2 || !b[0].Equal(nextCA) || !b[1].Equal(oldCA) {
			t.Errorf("%s: promoted bundle wrong", c)
		}
	}
	// Overlap: a not-yet-restarted serve (old leaf, staged bundle) still
	// talks to the promoted runner, and vice versa.
	if err := handshakeFiles(t, l.CertFile(ComponentRunner), l.KeyFile(ComponentRunner), l.BundleFile(ComponentRunner),
		staged.cert, staged.key, staged.bundle); err != nil {
		t.Errorf("overlap handshake failed: %v", err)
	}

	// Leaf rotation is allowed after promote and keeps both CAs trusted.
	if _, err := Rotate(RotateOptions{Dir: dir, Components: []string{ComponentWorker}, Passphrase: []byte(testPass)}); err != nil {
		t.Errorf("Rotate after promote: %v", err)
	}
	if b := readCerts(t, l.BundleFile(ComponentWorker)); len(b) != 2 {
		t.Errorf("worker bundle after rotate: %d certs", len(b))
	}

	// finish: bundles trust only the new CA.
	if _, err := RotateCA(withPhase(opts, CAFinish)); err != nil {
		t.Fatalf("finish: %v", err)
	}
	for _, c := range Components() {
		b := readCerts(t, l.BundleFile(c))
		if len(b) != 1 || !b[0].Equal(nextCA) {
			t.Errorf("%s: finished bundle wrong", c)
		}
	}
	if _, err := os.Stat(l.prevCACert()); err == nil {
		t.Error("prev CA left behind")
	}
	// The old serve material is no longer accepted.
	if err := handshakeFiles(t, l.CertFile(ComponentRunner), l.KeyFile(ComponentRunner), l.BundleFile(ComponentRunner),
		staged.cert, staged.key, staged.bundle); err == nil {
		t.Error("old serve certificate accepted after finish")
	}
}

func withPhase(o RotateCAOptions, p CAPhase) RotateCAOptions {
	o.Phase = p
	return o
}

type serveSnapshot struct{ cert, key, bundle string }

// snapshot copies serve's current material to a separate directory.
func snapshot(t *testing.T, l Layout) serveSnapshot {
	t.Helper()
	d := t.TempDir()
	s := serveSnapshot{filepath.Join(d, "serve.crt"), filepath.Join(d, "serve.key"), filepath.Join(d, "ca.crt")}
	for src, dst := range map[string]string{
		l.CertFile(ComponentServe): s.cert, l.KeyFile(ComponentServe): s.key, l.BundleFile(ComponentServe): s.bundle,
	} {
		data, err := os.ReadFile(src) // #nosec G304 -- test file
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil { // #nosec G703 -- test paths below t.TempDir
			t.Fatal(err)
		}
	}
	return s
}

func TestInspectExpiry(t *testing.T) {
	now := time.Now().UTC()
	dir := initDir(t, func(o *InitOptions) {
		o.LeafValidity = 10 * 24 * time.Hour
		o.CAValidity = 400 * 24 * time.Hour
	})
	infos, err := Inspect(dir)
	if err != nil {
		t.Fatal(err)
	}
	// ca/ca.crt + 3 x (leaf + bundle) = 7 certificates.
	if len(infos) != 7 {
		t.Fatalf("%d infos, want 7", len(infos))
	}
	for _, e := range infos {
		if strings.HasSuffix(e.Path, ".key") {
			t.Errorf("inspected a key file %s", e.Path)
		}
		if len(e.SHA256) != 64 || e.SPKISHA256 == "" {
			t.Errorf("%s: fingerprints missing", e.Path)
		}
	}
	exp := Expiring(infos, now, 30*24*time.Hour)
	if len(exp) != 3 {
		t.Errorf("Expiring(30d) = %d, want the 3 leaves", len(exp))
	}
	for _, e := range exp {
		if e.IsCA {
			t.Errorf("CA %s reported as expiring", e.Path)
		}
		if d := e.DaysLeft(now); d != 9 && d != 10 {
			t.Errorf("DaysLeft = %d, want ~10", d)
		}
	}
	if got := Expiring(infos, now, 5*24*time.Hour); len(got) != 0 {
		t.Errorf("Expiring(5d) = %d, want 0", len(got))
	}
	if got := Expiring(infos, now.Add(401*24*time.Hour), 0); len(got) != 7 {
		t.Errorf("after CA expiry: %d expired, want 7", len(got))
	}

	e := ExpiryInfo{NotBefore: now.Add(-time.Hour), NotAfter: now.Add(-36 * time.Hour)}
	if e.DaysLeft(now) != -2 || !e.Expired(now) {
		t.Errorf("expired DaysLeft = %d", e.DaysLeft(now))
	}
	nyv := ExpiryInfo{NotBefore: now.Add(time.Hour), NotAfter: now.Add(1000 * time.Hour)}
	if !nyv.ExpiresWithin(now, 0) {
		t.Error("not-yet-valid certificate not flagged")
	}

	single, err := Inspect(Layout{Dir: dir}.CertFile(ComponentServe))
	if err != nil || len(single) != 1 || !slices.Equal(single[0].SANs(), []string{"URI:spiffe://bekup/serve"}) {
		t.Errorf("Inspect(file) = %+v, %v", single, err)
	}
}

func TestLeafCappedAtCAExpiry(t *testing.T) {
	dir := initDir(t, func(o *InitOptions) {
		o.CAValidity = 24 * time.Hour
		o.LeafValidity = 365 * 24 * time.Hour
	})
	l := Layout{Dir: dir}
	ca := readCert(t, l.CACert())
	if leaf := readCert(t, l.CertFile(ComponentServe)); leaf.NotAfter.After(ca.NotAfter) {
		t.Error("leaf outlives its CA")
	}
}

func TestValidation(t *testing.T) {
	if _, err := NewCA(CAOptions{Validity: MaxCAValidity + time.Hour, ServerNames: []string{"runner"}}); err == nil {
		t.Error("CA validity above max accepted")
	}
	for _, n := range []string{"*.runner", "bad name", "-x", "a..b", ""} {
		if _, err := NewCA(CAOptions{ServerNames: []string{"runner", n}}); err == nil {
			t.Errorf("server name %q accepted", n)
		}
	}
	dir := filepath.Join(t.TempDir(), "c")
	if _, err := Init(InitOptions{Dir: dir, Components: []string{"db"}, Passphrase: []byte(testPass)}); err == nil {
		t.Error("unknown component accepted")
	}
}

func FuzzParseClientID(f *testing.F) {
	for _, s := range []string{"spiffe://bekup/serve", "spiffe://bekup/", "spiffe://evil/serve", "spiffe://bekup/a/b", "https://bekup/serve", "spiffe://bekup/serve?x=1"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		id, err := ParseClientID(s)
		if err != nil {
			return
		}
		if !strings.HasPrefix(id, "spiffe://bekup/") {
			t.Fatalf("accepted %q -> %q", s, id)
		}
		again, err := ParseClientID(id)
		if err != nil || again != id {
			t.Fatalf("not canonical: %q -> %q (%v)", id, again, err)
		}
	})
}
