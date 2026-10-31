package setup

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/certs"
)

// An existing CA (e.g. offline media mounted at secrets/certs/ca) is never
// reused, overwritten or deleted by a failed init.
func TestInitRefusesExistingCA(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	caDir := pathIn(dir, "secrets/certs/ca")
	if err := os.MkdirAll(caDir, 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"ca.crt": "old CA certificate\n", "ca.key": "old CA key\n"}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(caDir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before := snapshot(t, dir)
	o := testOptions(t, dir, testAnswers(t, ProxyTraefik))
	o.CAPassphrase = func() ([]byte, error) { t.Fatal("passphrase asked before the CA check"); return nil, nil }
	err := Init(o)
	if err == nil || !strings.Contains(err.Error(), "existing CA found in secrets/certs/ca") {
		t.Fatalf("err = %v", err)
	}
	if d := diffSnapshots(before, snapshot(t, dir)); len(d) > 0 {
		t.Fatalf("refused init changed files: %v", d)
	}
	// generateCerts refuses on its own too (upgrade path), before tracking.
	w, err := newWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := w.close(); err != nil {
			t.Error(err)
		}
	}()
	if err := generateCerts(w, Layout{KeysDir: "secrets"}, certComponentsFor(DBBundled), []byte(testPass), fastCerts); err == nil {
		t.Fatal("generateCerts accepted an existing CA")
	}
	if err := w.rollback(); err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		if b, err := os.ReadFile(filepath.Join(caDir, name)); err != nil || string(b) != data { //nolint:gosec // G304: test path
			t.Fatalf("%s: %q %v", name, b, err)
		}
	}
}

// --regenerate-secrets keeps the old secrets until the run succeeded: a
// failure midway restores every one of them.
func TestInitRegenerateRollback(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := testAnswers(t, ProxyTraefik)
	mustInit(t, dir, a)
	before := snapshot(t, dir)
	o := testOptions(t, dir, a)
	o.RegenerateSecrets = true
	o.certsInit = func(opts certs.InitOptions) (*certs.Result, error) {
		if _, err := fastCerts(opts); err != nil {
			return nil, err
		}
		return nil, errors.New("injected failure")
	}
	if err := Init(o); err == nil || !strings.Contains(err.Error(), "injected failure") {
		t.Fatalf("err = %v", err)
	}
	if d := diffSnapshots(before, snapshot(t, dir)); len(d) > 0 {
		t.Fatalf("failed regenerate left changes: %v", d)
	}
	// A successful run leaves no old copies behind.
	o.certsInit = fastCerts
	if err := Init(o); err != nil {
		t.Fatal(err)
	}
	for k := range snapshot(t, dir) {
		if strings.Contains(k, ".setup-old-") {
			t.Fatalf("old secret left: %s", k)
		}
	}
}

func TestCheckOverride(t *testing.T) {
	t.Parallel()
	const pinned = "nginx:1.27@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, tc := range []struct {
		name, yaml string
		want       string // "" = no finding
	}{
		{"empty", "", ""},
		{"pinned image", "services:\n  proxy:\n    image: " + pinned + "\n    user: \"101:101\"\n    read_only: true\n    cap_drop: [ALL]\n    security_opt: [\"no-new-privileges:true\"]\n", ""},
		{"unhardened new service", "services:\n  proxy:\n    image: " + pinned + "\n", "must be read-only"},
		{"volumes only", "services:\n  traefik:\n    volumes: [./proxy-tls:/certs:ro]\n  certbot:\n    profiles: !override [off]\n", ""},
		{"unpinned image", "services:\n  serve:\n    image: bekup:latest\n", `image "bekup:latest" is not digest-pinned`},
		{"variable image", "services:\n  serve:\n    image: ${IMG}\n", "neither an image"},
		{"reset image", "services:\n  serve:\n    image: !reset null\n", "neither an image"},
		{"build", "services:\n  serve:\n    build: .\n", "`build` is not allowed"},
		{"include", "include:\n  - other.yml\n", "`include` and `extends.file`"},
		{"extends file", "services:\n  serve:\n    extends:\n      file: x.yml\n      service: s\n", "`include` and `extends.file`"},
		{"extends same file", "services:\n  serve:\n    extends: runner\n", "cannot extend service"},
		{"extends in file", "services:\n  x:\n    image: " + pinned + "\n    volumes: [\"/var/run/docker.sock:/var/run/docker.sock\"]\n  serve:\n    extends: x\n", "container engine socket"},
		{"merge key", "x-bad: &bad\n  image: evil:1\nservices:\n  serve:\n    <<: *bad\n", `image "evil:1" is not digest-pinned`},
		{"merge list", "x-a: &a\n  build: .\nservices:\n  serve:\n    <<: [*a]\n", "`build`"},
		{"alias service", "x-s: &s\n  image: evil:2\nservices:\n  serve: *s\n", `"evil:2"`},
		{"not a mapping", "- a\n", "would not load"},
		{"invalid", "services: [\n", "not valid YAML"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ps := lintWith(t, nil, []byte(tc.yaml), VariantFull, nil)
			if tc.want == "" {
				if len(ps) > 0 {
					t.Fatalf("findings: %+v", ps)
				}
				return
			}
			for _, p := range ps {
				if p.Severity == SeverityError && strings.Contains(p.Msg, tc.want) {
					return
				}
			}
			t.Fatalf("no error %q in %+v", tc.want, ps)
		})
	}
}

func FuzzCheckOverride(f *testing.F) {
	f.Add("services:\n  a:\n    image: x\n")
	f.Add("x: &a {<<: *a}\n")
	f.Fuzz(func(t *testing.T, s string) {
		_ = lintWith(t, nil, []byte(s), VariantTraefik, nil)
	})
}

func TestCheckComposeBypass(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		mut  func(t *testing.T, dir string)
		env  map[string]string
		sev  Severity
		want string
	}{
		{name: "override image", mut: func(t *testing.T, dir string) {
			if err := os.WriteFile(pathIn(dir, OverrideFile), []byte("services:\n  serve:\n    image: bekup:dev\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, sev: SeverityError, want: "compose.override.yaml: service \"serve\": image"},
		{name: "DOCKER_HOST in .env", mut: func(t *testing.T, dir string) {
			b := append(readFile(t, dir, EnvFile), []byte("DOCKER_HOST=tcp://10.0.0.1:2375\n")...)
			if err := os.WriteFile(pathIn(dir, EnvFile), b, 0o600); err != nil {
				t.Fatal(err)
			}
		}, sev: SeverityError, want: ".env: DOCKER_HOST points docker compose at another Docker daemon"},
		{name: "DOCKER_CONTEXT in .env", mut: func(t *testing.T, dir string) {
			b := append(readFile(t, dir, EnvFile), []byte("DOCKER_CONTEXT=remote\n")...)
			if err := os.WriteFile(pathIn(dir, EnvFile), b, 0o600); err != nil {
				t.Fatal(err)
			}
		}, sev: SeverityError, want: "DOCKER_CONTEXT points"},
		{name: "COMPOSE_FILE in .env", mut: func(t *testing.T, dir string) {
			b := append(readFile(t, dir, EnvFile), []byte("COMPOSE_FILE=other.yml\n")...)
			if err := os.WriteFile(pathIn(dir, EnvFile), b, 0o600); err != nil {
				t.Fatal(err)
			}
		}, sev: SeverityError, want: ".env: COMPOSE_FILE selects other compose files"},
		{name: "project dir in env", env: map[string]string{"COMPOSE_PROJECT_DIRECTORY": "/tmp"}, sev: SeverityError, want: "COMPOSE_PROJECT_DIRECTORY is set in the environment"},
		{name: "path separator in env", env: map[string]string{"COMPOSE_PATH_SEPARATOR": ";"}, sev: SeverityError, want: "COMPOSE_PATH_SEPARATOR"},
		{name: "project name", env: map[string]string{"COMPOSE_PROJECT_NAME": "x"}, sev: SeverityWarning, want: "renames the compose project"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			mustInit(t, dir, testAnswers(t, ProxyTraefik))
			if tc.mut != nil {
				tc.mut(t, dir)
			}
			fs, out, err := runCheck(t, dir, func(o *CheckOptions) {
				o.LookupEnv = func(k string) (string, bool) { v, ok := tc.env[k]; return v, ok }
			})
			if !hasFinding(fs, tc.sev, tc.want) {
				t.Fatalf("no %s %q:\n%s", tc.sev, tc.want, out)
			}
			if (tc.sev == SeverityError) != errors.Is(err, ErrCheckFailed) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

// L7: a file of the operator at a path that becomes generated is never
// overwritten silently.
func TestUpgradeForeignFileAtNewAsset(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := testAnswers(t, ProxyTraefik)
	mustInit(t, dir, a)
	const p = "deploy/traefik/dynamic/tls.yml"
	st := mustState(t, dir)
	delete(st.Assets, p) // the old version did not ship it
	saveState(t, dir, st)
	if err := os.WriteFile(pathIn(dir, p), []byte("# operator's own\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, dir)
	o := upgradeOptions(t, dir, a.Image.String())
	if err := Upgrade(o); err == nil || !strings.Contains(err.Error(), "files of your own exist: "+p) {
		t.Fatalf("err = %v", err)
	}
	if d := diffSnapshots(before, snapshot(t, dir)); len(d) > 0 {
		t.Fatalf("refused upgrade changed files: %v", d)
	}
	o.Force = true
	if err := Upgrade(o); err != nil {
		t.Fatal(err)
	}
	if b := readFile(t, dir, p+".bak-20261101T080000Z"); !bytes.Equal(b, []byte("# operator's own\n")) {
		t.Fatalf("backup %q", b)
	}
}

// L8: CIDR answers are bounded.
func TestCIDRBounds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		cidr string
		ok   bool
	}{
		{"0.0.0.0/0", false}, {"::/0", false}, {"10.0.0.0/7", false}, {"10.0.0.0/8", true},
		{"fd00::/31", false}, {"fd00::/32", true}, {"10.20.0.0/16", true},
	} {
		for _, field := range []string{"target-cidr", "trusted-proxy"} {
			a := testAnswers(t, ProxyNone)
			if field == "target-cidr" {
				a.TargetCIDRs = []string{tc.cidr}
			} else {
				a.TrustedProxies = []string{tc.cidr}
			}
			want := tc.ok
			if field == "trusted-proxy" && tc.cidr == "10.0.0.0/8" {
				want = false // contains bekup's reserved frontend addresses
			}
			if err := a.Validate(); (err == nil) != want {
				t.Errorf("%s %s: %v", field, tc.cidr, err)
			}
		}
	}
	a := testAnswers(t, ProxyTraefik)
	a.TargetCIDRs = []string{"10.0.0.0/12", "10.20.0.0/16"}
	if w := wideCIDRWarnings(&a); len(w) != 1 || !strings.Contains(w[0], "10.0.0.0/12 is very wide") {
		t.Fatalf("warnings %q", w)
	}
	// check reports wide entries in the TOML files.
	dir := t.TempDir()
	mustInit(t, dir, a)
	fs, out, err := runCheck(t, dir, nil)
	if err != nil || !hasFinding(fs, SeverityWarning, "targets.allow_cidrs contains the very wide 10.0.0.0/12") {
		t.Fatalf("no wide warning:\n%s", out)
	}
	b := strings.Replace(string(readFile(t, dir, RunnerTOML)), `"10.0.0.0/12"`, `"10.0.0.0/7"`, 1)
	if err := os.WriteFile(pathIn(dir, RunnerTOML), []byte(b), 0o600); err != nil {
		t.Fatal(err)
	}
	fs, out, err = runCheck(t, dir, nil)
	if !errors.Is(err, ErrCheckFailed) || !hasFinding(fs, SeverityError, "targets.allow_cidrs contains 10.0.0.0/7") {
		t.Fatalf("no wide error:\n%s", out)
	}
}
