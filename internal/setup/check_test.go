package setup

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func runCheck(t *testing.T, dir string, mut func(*CheckOptions)) ([]Finding, string, error) {
	t.Helper()
	var out bytes.Buffer
	o := CheckOptions{Dir: dir, Version: "1.2.3", Ownership: OwnershipNone, Out: &out,
		LookupEnv: func(string) (string, bool) { return "", false }, sys: (&fakeSys{}).env()}
	if mut != nil {
		mut(&o)
	}
	fs, err := Check(o)
	return fs, out.String(), err
}

func hasFinding(fs []Finding, sev Severity, substr string) bool {
	for _, f := range fs {
		if f.Severity == sev && strings.Contains(f.String(), substr) {
			return true
		}
	}
	return false
}

func TestCheckFindings(t *testing.T) {
	t.Parallel()
	type tc struct {
		name string
		mut  func(t *testing.T, dir string)
		opt  func(*CheckOptions)
		sev  Severity
		want string
	}
	write := func(rel, data string) func(*testing.T, string) {
		return func(t *testing.T, dir string) {
			if err := os.WriteFile(pathIn(dir, rel), []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	replaceIn := func(rel, from, to string) func(*testing.T, string) {
		return func(t *testing.T, dir string) {
			b := strings.Replace(string(readFile(t, dir, rel)), from, to, 1)
			if err := os.WriteFile(pathIn(dir, rel), []byte(b), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	cases := []tc{
		{name: "clean", sev: SeverityWarning, want: "CA private key is on this host's disk"},
		{name: "mode", mut: func(t *testing.T, dir string) {
			if err := os.Chmod(pathIn(dir, "secrets/bekup_master_key"), 0o644); err != nil { //nolint:gosec // G302: the test needs a wrong mode
				t.Fatal(err)
			}
		}, sev: SeverityError, want: "secrets/bekup_master_key: mode 0644, want 0400"},
		{name: "missing secret", mut: func(t *testing.T, dir string) {
			if err := os.Remove(pathIn(dir, "secrets/bekup_app_password")); err != nil {
				t.Fatal(err)
			}
		}, sev: SeverityError, want: "secrets/bekup_app_password: missing"},
		{name: "symlink", mut: func(t *testing.T, dir string) {
			if err := os.Symlink("/etc/passwd", pathIn(dir, "deploy/x")); err != nil {
				t.Fatal(err)
			}
		}, sev: SeverityError, want: "deploy/x: is a symlink"},
		{name: "hard link", mut: func(t *testing.T, dir string) {
			if err := os.Link(pathIn(dir, BekupTOML), pathIn(dir, "deploy/bekup-toml-link")); err != nil {
				t.Fatal(err)
			}
		}, sev: SeverityError, want: "bekup.toml: has 2 hard links"},
		{name: "compose edited", mut: replaceIn(ComposeFile, "max-file: \"5\"", "max-file: \"6\""), sev: SeverityError, want: "compose.yaml: edited locally"},
		{name: "unpinned image", mut: replaceIn(ComposeFile, "x-bekup-image: &bekup-image ghcr.io/jlentink/bekup:1.2.3@"+testDigest, "x-bekup-image: &bekup-image bekup:dev"), sev: SeverityError, want: "is not digest-pinned"},
		{name: "base url", mut: replaceIn(BekupTOML, "https://backup.example.com", "https://other.example.com"), sev: SeverityError, want: "differs from BEKUP_DOMAIN"},
		{name: "trusted proxies", mut: replaceIn(BekupTOML, "10.213.250.5/32", "10.213.250.0/24"), sev: SeverityError, want: "server.trusted_proxies"},
		{name: "listen", mut: replaceIn(RunnerTOML, "10.213.251.10:7443", "10.213.251.11:7443"), sev: SeverityError, want: "listen.address"},
		{name: "gid", mut: replaceIn(RunnerTOML, "gid = 999", "gid = 998"), sev: SeverityError, want: "docker.gid = 998"},
		{name: "invalid toml", mut: write(RunnerTOML, "[nope]\n"), sev: SeverityError, want: "runner.toml"},
		{name: "env line", mut: replaceIn(EnvFile, "DOCKER_GID=999", "DOCKER_GID=$(id)"), sev: SeverityError, want: "not a plain KEY=value"},
		{name: "profiles", mut: replaceIn(EnvFile, "COMPOSE_PROFILES=traefik", "COMPOSE_PROFILES=nginx"), sev: SeverityWarning, want: "COMPOSE_PROFILES"},
		{name: "shell override", opt: func(o *CheckOptions) {
			o.LookupEnv = func(k string) (string, bool) {
				if k == "BEKUP_DOMAIN" {
					return "evil.example.com", true
				}
				return "", false
			}
		}, sev: SeverityWarning, want: "BEKUP_DOMAIN is set in the environment"},
		{name: "expiring", opt: func(o *CheckOptions) { o.Now = func() time.Time { return time.Now().Add(350 * 24 * time.Hour) } }, sev: SeverityWarning, want: "expires on"},
		{name: "expired", opt: func(o *CheckOptions) { o.Now = func() time.Time { return time.Now().Add(400 * 24 * time.Hour) } }, sev: SeverityError, want: "expired on"},
		{name: "owners", opt: func(o *CheckOptions) { o.Ownership = OwnershipChown }, sev: SeverityError, want: "wrong owner"},
		{name: "drift on same version", opt: func(o *CheckOptions) {
			o.assets = nil
			o.Version = "1.2.3"
		}, mut: func(t *testing.T, dir string) {
			st := mustState(t, dir)
			st.Assets["deploy/nginx/nginx.conf"] = strings.Repeat("0", 64)
			saveState(t, dir, st)
		}, sev: SeverityError, want: "differ from those of bekup"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			mustInit(t, dir, testAnswers(t, ProxyTraefik))
			if c.mut != nil {
				c.mut(t, dir)
			}
			fs, out, err := runCheck(t, dir, c.opt)
			if !hasFinding(fs, c.sev, c.want) {
				t.Fatalf("no %s finding %q:\n%s", c.sev, c.want, out)
			}
			if (c.sev == SeverityError) != errors.Is(err, ErrCheckFailed) {
				t.Fatalf("err = %v", err)
			}
			if c.name == "clean" && len(fs) != 1 {
				t.Fatalf("clean directory findings:\n%s", out)
			}
		})
	}
}

// check never opens secret files: make them unreadable and it still passes.
func TestCheckDoesNotReadSecrets(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	dir := t.TempDir()
	mustInit(t, dir, testAnswers(t, ProxyTraefik))
	for _, e := range Manifest(Layout{KeysDir: "secrets"}, nil, ManifestOptions{}) {
		if e.Secret && e.Kind == KindFile {
			p := pathIn(dir, e.Path)
			if err := os.Chmod(p, 0o000); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.Chmod(p, e.Mode); err != nil {
					t.Error(err)
				}
			})
		}
	}
	fs, out, err := runCheck(t, dir, nil)
	if err != nil && !errors.Is(err, ErrCheckFailed) { // the 0000 modes are findings
		t.Fatalf("%v\n%s", err, out)
	}
	for _, f := range fs {
		if strings.Contains(f.Msg, "permission denied") {
			t.Fatalf("check opened a secret:\n%s", out)
		}
	}
}

func TestCheckFixOwnership(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustInit(t, dir, testAnswers(t, ProxyTraefik))
	if err := os.Chmod(pathIn(dir, "secrets"), 0o755); err != nil { //nolint:gosec // G302: the test needs a wrong mode
		t.Fatal(err)
	}
	f := &fakeSys{}
	_, out, err := runCheck(t, dir, func(o *CheckOptions) {
		o.FixOwnership, o.Ownership, o.sys = true, OwnershipChown, f.env()
	})
	if strings.Contains(out, "mode 0755") {
		t.Fatalf("mode not fixed:\n%s", out)
	}
	if len(f.calls) == 0 || err != nil {
		t.Fatalf("calls %d, err %v\n%s", len(f.calls), err, out)
	}
}
