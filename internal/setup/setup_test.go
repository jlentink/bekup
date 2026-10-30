package setup

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/certs"
)

// secretValues returns every generated secret value (whole files and
// their lines) for the leak checks.
func secretValues(t *testing.T, dir string, entries []Entry) [][]byte {
	t.Helper()
	var out [][]byte
	for _, e := range entries {
		if !e.Secret || e.Kind != KindFile {
			continue
		}
		b, err := os.ReadFile(filepath.Clean(pathIn(dir, e.Path)))
		if errors.Is(err, fs.ErrNotExist) && e.Optional {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, bytes.TrimSpace(b))
		for _, l := range bytes.Split(b, []byte("\n")) {
			if l = bytes.TrimSpace(l); len(l) >= 12 && !bytes.HasPrefix(l, []byte("-----")) {
				out = append(out, l)
			}
		}
	}
	return out
}

func TestInitWritesDeployment(t *testing.T) {
	for _, proxy := range []Proxy{ProxyTraefik, ProxyNginx, ProxyNone} {
		t.Run(string(proxy), func(t *testing.T) {
			dir := t.TempDir()
			a := testAnswers(t, proxy)
			out := mustInit(t, dir, a)
			if err := a.Validate(); err != nil {
				t.Fatal(err)
			}
			p, err := newPlan(testOptionsAssets(), &a, true)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range p.entries {
				fi, err := os.Lstat(pathIn(dir, e.Path))
				if err != nil {
					if e.Optional {
						continue
					}
					t.Fatalf("%s: %v", e.Path, err)
				}
				if fi.Mode().Perm() != e.Mode {
					t.Errorf("%s: mode %04o, want %04o", e.Path, fi.Mode().Perm(), e.Mode)
				}
			}
			if !bytes.Equal(readFile(t, dir, BekupTOML), renderBekupTOML(&a)) {
				t.Error("bekup.toml differs")
			}
			compose := string(readFile(t, dir, ComposeFile))
			if !hasLine(compose, "x-bekup-image: &bekup-image "+a.Image.String()) {
				t.Error("compose not pinned")
			}
			// Database URLs match the passwords.
			appPW := readFile(t, dir, "secrets/bekup_app_password")
			if got := string(readFile(t, dir, "secrets/bekup_database_url")); got != "postgres://bekup_app:"+string(appPW)+"@postgres:5432/bekup" {
				t.Errorf("app URL does not match its password")
			}
			if len(appPW) != 43 {
				t.Errorf("password length %d", len(appPW))
			}
			// No secret value in the output.
			for _, s := range secretValues(t, dir, p.entries) {
				if bytes.Contains(out.Bytes(), s) {
					t.Fatalf("secret material in the output")
				}
			}
			if bytes.Contains(out.Bytes(), []byte(testPass)) {
				t.Fatal("passphrase in the output")
			}
			for _, want := range []string{"primary key ID", "SHA-256 fingerprints", "Next steps", "ca.crt"} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output lacks %q", want)
				}
			}
			st, err := readState(dir)
			if err != nil {
				t.Fatal(err)
			}
			if st.Image != a.Image.String() || st.Proxy != proxy || len(st.SecretGroups) != 3 || st.Ownership != OwnershipNone {
				t.Fatalf("state %+v", st)
			}
			if _, err := Check(CheckOptions{Dir: dir, Version: "1.2.3", Ownership: OwnershipNone, sys: (&fakeSys{}).env()}); err != nil {
				t.Fatalf("check after init: %v", err)
			}
		})
	}
}

func testOptionsAssets() fs.FS { o := Options{}; o.defaults(); return o.assets }

// The real passphrase path: the CA key is age-encrypted.
func TestInitRealPassphrase(t *testing.T) {
	dir := t.TempDir()
	o := testOptions(t, dir, testAnswers(t, ProxyTraefik))
	o.certsInit = certs.Init
	if err := Init(o); err != nil {
		t.Fatal(err)
	}
	enc, err := certs.CAKeyEncrypted(pathIn(dir, "secrets/certs"))
	if err != nil || !enc {
		t.Fatalf("CA key encrypted = %v, %v", enc, err)
	}
	for _, s := range secretValues(t, dir, Manifest(Layout{KeysDir: "secrets"}, nil, ManifestOptions{})) {
		if bytes.Contains(bufOf(t, o.Out).Bytes(), s) {
			t.Fatal("secret material in the output")
		}
	}
	o.CAPassphrase = func() ([]byte, error) { return []byte("short"), nil }
	o.Dir = t.TempDir()
	if err := Init(o); err == nil {
		t.Fatal("short passphrase accepted")
	}
}

func TestInitRefusesExistingAndKeepExisting(t *testing.T) {
	dir := t.TempDir()
	a := testAnswers(t, ProxyTraefik)
	mustInit(t, dir, a)
	before := snapshot(t, dir)

	o := testOptions(t, dir, a)
	if err := Init(o); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("second init: %v", err)
	}
	o.KeepExisting = true
	o.CAPassphrase = func() ([]byte, error) { t.Fatal("passphrase asked although certificates exist"); return nil, nil }
	if err := Init(o); err != nil {
		t.Fatal(err)
	}
	if d := diffSnapshots(before, snapshot(t, dir)); len(d) > 0 {
		t.Fatalf("--keep-existing changed files: %v", d)
	}

	// A missing operator file is recreated; nothing else changes.
	if err := os.Remove(filepath.Join(dir, RunnerTOML)); err != nil {
		t.Fatal(err)
	}
	if err := Init(o); err != nil {
		t.Fatal(err)
	}
	if d := diffSnapshots(before, snapshot(t, dir)); len(d) > 0 {
		t.Fatalf("--keep-existing: %v", d)
	}
}

func TestInitForceRewritesOnlyNonSecrets(t *testing.T) {
	dir := t.TempDir()
	a := testAnswers(t, ProxyTraefik)
	mustInit(t, dir, a)
	before := snapshot(t, dir)
	for _, f := range []string{BekupTOML, ComposeFile} {
		if err := os.WriteFile(pathIn(dir, f), []byte("edited"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	o := testOptions(t, dir, a)
	o.Force = true
	if err := Init(o); err != nil {
		t.Fatal(err)
	}
	if d := diffSnapshots(before, snapshot(t, dir)); len(d) > 0 {
		t.Fatalf("--force did not restore exactly the generated files: %v", d)
	}
}

func TestInitPartialSecretsRefused(t *testing.T) {
	dir := t.TempDir()
	a := testAnswers(t, ProxyTraefik)
	mustInit(t, dir, a)
	if err := os.Remove(pathIn(dir, "secrets/bekup_app_password")); err != nil {
		t.Fatal(err)
	}
	o := testOptions(t, dir, a)
	o.KeepExisting = true
	if err := Init(o); err == nil || !strings.Contains(err.Error(), "database secrets are incomplete") {
		t.Fatalf("partial group: %v", err)
	}
}

func TestInitRollback(t *testing.T) {
	dir := t.TempDir()
	// A pre-existing, Docker-created mount point of the offline CA.
	if err := os.MkdirAll(pathIn(dir, "secrets/certs/ca"), 0o750); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, dir)
	o := testOptions(t, dir, testAnswers(t, ProxyTraefik))
	o.certsInit = func(opts certs.InitOptions) (*certs.Result, error) {
		if _, err := fastCerts(opts); err != nil {
			return nil, err
		}
		return nil, errors.New("injected failure")
	}
	if err := Init(o); err == nil {
		t.Fatal("no error")
	}
	after := snapshot(t, dir)
	// The pre-existing directories may have their modes fixed; nothing else.
	for _, d := range diffSnapshots(before, after) {
		if !strings.HasPrefix(d, "changed secrets") {
			t.Errorf("rollback left %s", d)
		}
	}
	if len(after) != len(before) {
		t.Fatalf("rollback left files: %v", diffSnapshots(before, after))
	}
}

func TestInitRegenerateSecrets(t *testing.T) {
	dir := t.TempDir()
	a := testAnswers(t, ProxyTraefik)
	mustInit(t, dir, a)
	oldKey := readFile(t, dir, "secrets/bekup_master_key")
	oldCA := readFile(t, dir, "secrets/certs/ca/ca.crt")
	oldCompose := readFile(t, dir, ComposeFile)
	o := testOptions(t, dir, a)
	o.RegenerateSecrets = true
	if err := Init(o); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(oldKey, readFile(t, dir, "secrets/bekup_master_key")) || bytes.Equal(oldCA, readFile(t, dir, "secrets/certs/ca/ca.crt")) {
		t.Fatal("secrets not regenerated")
	}
	if !bytes.Equal(oldCompose, readFile(t, dir, ComposeFile)) {
		t.Fatal("compose changed")
	}
}

func TestInitStaticTLSAndKeysDir(t *testing.T) {
	dir := t.TempDir()
	a := testAnswers(t, ProxyNginx)
	a.TLSMode, a.ACMEEmail, a.KeysDir = TLSStatic, "", "vault/bekup"
	mustInit(t, dir, a)
	if !strings.Contains(string(readFile(t, dir, OverrideFile)), "certbot:") {
		t.Fatal("override missing")
	}
	if fi, err := os.Stat(filepath.Join(dir, ProxyTLSDir)); err != nil || !fi.IsDir() {
		t.Fatalf("proxy-tls: %v", err)
	}
	if !hasLine(string(readFile(t, dir, EnvFile)), "BEKUP_KEYS_DIR=./vault/bekup") {
		t.Fatal(".env keys dir")
	}
	if _, err := os.Stat(pathIn(dir, "vault/bekup/certs/runner/runner.key")); err != nil {
		t.Fatal(err)
	}
	// The generated override passes the hardening lint.
	findings, out, err := runCheck(t, dir, nil)
	if err != nil && !errors.Is(err, ErrCheckFailed) {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Severity == SeverityError && strings.Contains(f.Path, OverrideFile) {
			t.Fatalf("lint error on the generated override:\n%s", out)
		}
	}
}

func TestInitRefusesSymlinkedDirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "secrets")); err != nil {
		t.Fatal(err)
	}
	if err := Init(testOptions(t, dir, testAnswers(t, ProxyTraefik))); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlinked secrets dir: %v", err)
	}
}

func TestInitPrintOwnership(t *testing.T) {
	dir := t.TempDir()
	o := testOptions(t, dir, testAnswers(t, ProxyTraefik))
	o.Ownership = OwnershipAuto // fake euid 1000 -> print
	if err := Init(o); err != nil {
		t.Fatal(err)
	}
	out := bufOf(t, o.Out).String()
	if !strings.Contains(out, "ownership:  print (auto: not running as root)") || !strings.Contains(out, "chown -h 65532:65532 -- secrets/bekup_master_key") {
		t.Fatalf("output:\n%s", out)
	}
}
