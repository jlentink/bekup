package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/imageref"
	"github.com/jlentink/bekup/internal/setup"
)

const setupTestImage = "registry.example.com/team/bekup:1.0.0@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func setupArgs(dir string, extra ...string) []string {
	return append([]string{
		"setup", "init", "--dir", dir, "--non-interactive", "--image", setupTestImage,
		"--write-compose", "--variant", "full",
		"--allow-version-mismatch", "--proxy", "traefik", "--domain", "backup.example.com",
		"--tls-mode", "acme", "--acme-email", "ops@example.com", "--docker-gid", "999",
		"--ownership", "none", "--ca-passphrase-stdin",
	}, extra...)
}

func TestSetupInitCheckUpgrade(t *testing.T) {
	t.Setenv("BEKUP_IMAGE", "")
	dir := t.TempDir()
	const pass = "correct horse battery staple x\n"
	stdout, stderr, err := runIn(t, pass, setupArgs(dir, "--target-cidr", "10.20.0.0/16")...)
	if err != nil {
		t.Fatalf("init: %v\n%s", err, stderr)
	}
	// No generated secret value and no passphrase on stdout/stderr.
	all := stdout + stderr
	if strings.Contains(all, strings.TrimSpace(pass)) {
		t.Fatal("passphrase printed")
	}
	for _, f := range []string{"bekup_master_key", "bekup_database_url", "bekup_database_owner_url", "postgres_password", "bekup_owner_password", "bekup_app_password", "certs/runner/runner.key", "certs/serve/serve.key", "certs/postgres/postgres.key"} {
		b, err := os.ReadFile(filepath.Clean(filepath.Join(dir, "secrets", f)))
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
			if len(l) >= 12 && !bytes.HasPrefix(l, []byte("-----")) && strings.Contains(all, string(l)) {
				t.Fatalf("secret material of %s printed", f)
			}
		}
	}
	if !strings.Contains(stdout, "Next steps") {
		t.Fatalf("stdout:\n%s", stdout)
	}

	if out, _, err := run(t, "setup", "check", "--dir", dir, "--ownership", "none"); err != nil {
		t.Fatalf("check: %v\n%s", err, out)
	}
	out, _, err := run(t, "setup", "upgrade", "--dir", dir, "--write-compose", "--image", setupTestImage, "--allow-version-mismatch", "--ownership", "none")
	if err != nil || !strings.Contains(out, "no changes") {
		t.Fatalf("upgrade: %v\n%s", err, out)
	}
}

func TestSetupInitErrors(t *testing.T) {
	t.Setenv("BEKUP_IMAGE", "")
	dir := t.TempDir()
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"unpinned", []string{"setup", "init", "--dir", dir, "--non-interactive", "--write-compose", "--variant", "full", "--image", "bekup:1.0.0"}, "docker image inspect --format '{{index .RepoDigests 0}}'"},
		{"no image", []string{"setup", "init", "--dir", dir, "--non-interactive", "--write-compose", "--variant", "full"}, "pass --image or set BEKUP_IMAGE"},
		{"missing flags", []string{"setup", "init", "--dir", dir, "--non-interactive", "--write-compose", "--variant", "full", "--image", setupTestImage}, "missing --proxy, --domain, --docker-gid"},
		{"no compose.yaml", []string{"setup", "init", "--dir", dir, "--non-interactive"}, "no compose.yaml in"},
		{"write-compose without variant", []string{"setup", "init", "--dir", dir, "--write-compose"}, "--write-compose needs --variant"},
		{"bad variant", []string{"setup", "init", "--dir", dir, "--variant", "caddy"}, "--variant must be"},
		{"version mismatch", setupArgs(dir)[:len(setupArgs(dir))-1], ""},
		{"generate without tty", append(setupArgs(dir)[:len(setupArgs(dir))-1], "--generate-ca-passphrase"), "needs stderr to be a terminal"},
		{"two sources", append(setupArgs(dir), "--generate-ca-passphrase"), "mutually exclusive"},
		{"regenerate non-interactive", append(setupArgs(dir), "--regenerate-secrets"), "needs a terminal"},
		{"bad ownership", append(setupArgs(dir), "--ownership", "root"), "--ownership must be"},
		{"no passphrase", setupArgs(dir)[:len(setupArgs(dir))-1], "no terminal for the CA key passphrase"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := tc.args
			if tc.name == "version mismatch" {
				var filtered []string
				for _, a := range args {
					if a != "--allow-version-mismatch" {
						filtered = append(filtered, a)
					}
				}
				args = filtered
				tc.want = "does not match this bekup version"
			}
			_, _, err := runIn(t, "", args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed runs left %d entries", len(entries))
	}
	if _, _, err := run(t, "setup", "upgrade", "--dir", dir); err == nil || !strings.Contains(err.Error(), "bekup-setup.json not found") {
		t.Fatalf("upgrade without image: %v", err)
	}
	if _, _, err := run(t, "setup", "check", "--dir", dir); err == nil || !strings.Contains(err.Error(), "bekup-setup.json not found") {
		t.Fatalf("check without state: %v", err)
	}
}

// stuckReader returns (0, nil) forever.
type stuckReader struct{}

func (stuckReader) Read([]byte) (int, error) { return 0, nil }

func TestReadPassphraseLine(t *testing.T) {
	p, err := readPassphraseLine(strings.NewReader("  a long enough passphrase  \nsecond line"))
	if err != nil || string(p) != "a long enough passphrase" {
		t.Fatalf("%q %v", p, err)
	}
	if _, err := readPassphraseLine(strings.NewReader("short\n")); err == nil {
		t.Fatal("short passphrase accepted")
	}
	if _, err := readPassphraseLine(strings.NewReader(strings.Repeat("x", 1025))); err == nil {
		t.Fatal("overlong line accepted")
	}
	// A reader that never makes progress ((0, nil) forever) is refused.
	if _, err := readPassphraseLine(stuckReader{}); !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("stuck reader: %v", err)
	}
	// It stops at the newline and never waits for more input (a pipe that
	// stays open, like an interactive stdin).
	pr, pw := io.Pipe()
	defer func() {
		if err := pr.Close(); err != nil {
			t.Error(err)
		}
	}()
	go func() {
		if _, err := pw.Write([]byte("a long enough passphrase\n")); err != nil {
			t.Error(err)
		}
	}()
	done := make(chan error, 1)
	go func() {
		p, err := readPassphraseLine(pr)
		if err == nil && string(p) != "a long enough passphrase" {
			err = fmt.Errorf("got %q", p)
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readPassphraseLine waited past the newline")
	}
}

func TestSetupUpgradeUnpinnedHint(t *testing.T) {
	_, _, err := run(t, "setup", "upgrade", "--dir", t.TempDir(), "--image", "bekup:1.0.0")
	if err == nil || !strings.Contains(err.Error(), "docker image inspect --format '{{index .RepoDigests 0}}'") {
		t.Fatalf("err = %v", err)
	}
}

// A generated CA passphrase goes to stderr only (with the log warning).
func TestSetupGeneratedPassphraseOnStderr(t *testing.T) {
	t.Setenv("BEKUP_IMAGE", "")
	old := stderrIsTerminal
	stderrIsTerminal = func() bool { return true }
	t.Cleanup(func() { stderrIsTerminal = old })
	args := setupArgs(t.TempDir())
	args = append(args[:len(args)-1], "--generate-ca-passphrase")
	stdout, stderr, err := runIn(t, "", args...)
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if strings.Contains(stdout, "Generated CA key passphrase") || !strings.Contains(stderr, "Generated CA key passphrase") || !strings.Contains(stderr, "--log-driver none") {
		t.Fatalf("stdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}

// Compose-first through the CLI (D-205): the release file decides image
// and variant, DOCKER_GID comes from the environment, compose.yaml is never
// written.
func TestSetupComposeFirstCLI(t *testing.T) {
	img, err := imageref.ParsePinned(setupTestImage)
	if err != nil {
		t.Fatal(err)
	}
	data, err := setup.RenderRelease(setup.VariantOwnProxy, setup.DBBundled, &img)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	compose := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(compose, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_GID", "998")
	t.Setenv("BEKUP_IMAGE", "registry.example.com/team/bekup:1.0.0@sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210")
	base := append(make([]string, 0, 17), "setup", "init", "--dir", dir, "--non-interactive", "--allow-version-mismatch",
		"--domain", "backup.example.com", "--ownership", "none", "--ca-passphrase-stdin")
	if _, _, err := runIn(t, "correct horse battery staple x\n", base...); err == nil || !strings.Contains(err.Error(), "does not match the x-bekup-image") {
		t.Fatalf("BEKUP_IMAGE mismatch: %v", err)
	}
	t.Setenv("BEKUP_IMAGE", setupTestImage) // what the setup service passes
	if _, _, err := runIn(t, "correct horse battery staple x\n", base...); err == nil || !strings.Contains(err.Error(), "missing --proxy-network, --proxy-container") {
		t.Fatalf("own proxy without its network: %v", err)
	}
	base = append(base, "--proxy", "own", "--proxy-network", "npm_default", "--proxy-container", "npm-app-1")
	t.Setenv("BEKUP_IMAGE", "registry.example.com/team/bekup:1.0.0@sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210")
	if _, _, err := runIn(t, "correct horse battery staple x\n", base...); err == nil || !strings.Contains(err.Error(), "does not match the x-bekup-image") {
		t.Fatalf("BEKUP_IMAGE mismatch: %v", err)
	}
	t.Setenv("BEKUP_IMAGE", setupTestImage) // what the setup service passes
	stdout, stderr, err := runIn(t, "correct horse battery staple x\n", base...)
	if err != nil {
		t.Fatalf("init: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "compose.yaml: the own-proxy variant (exact)") || !strings.Contains(stdout, "Nginx Proxy Manager") || !strings.Contains(stdout, "http://bekup-serve:8080") {
		t.Fatalf("stdout:\n%s", stdout)
	}
	if got, err := os.ReadFile(filepath.Clean(compose)); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("compose.yaml changed: %v", err)
	}
	env, err := os.ReadFile(filepath.Clean(filepath.Join(dir, ".env")))
	if err != nil || !strings.Contains(string(env), "DOCKER_GID=998\n") || !strings.Contains(string(env), "BEKUP_PROXY_NETWORK=npm_default\n") {
		t.Fatalf(".env: %v\n%s", err, env)
	}
	if out, _, err := run(t, "setup", "check", "--dir", dir, "--ownership", "none"); err != nil {
		t.Fatalf("check: %v\n%s", err, out)
	}
	out, _, err := run(t, "setup", "upgrade", "--dir", dir, "--allow-version-mismatch", "--ownership", "none")
	if err != nil || !strings.Contains(out, "no changes") {
		t.Fatalf("upgrade: %v\n%s", err, out)
	}
	if _, _, err := run(t, "setup", "upgrade", "--dir", dir, "--write-compose", "--allow-version-mismatch", "--ownership", "none"); err == nil || !strings.Contains(err.Error(), "compose-first") {
		t.Fatalf("upgrade --write-compose: %v", err)
	}
	t.Setenv("DOCKER_GID", "x")
	if _, _, err := runIn(t, "", "setup", "init", "--dir", t.TempDir(), "--non-interactive"); err == nil || !strings.Contains(err.Error(), "DOCKER_GID") {
		t.Fatalf("bad DOCKER_GID: %v", err)
	}
}

func TestSetupComposeCmd(t *testing.T) {
	out, _, err := run(t, "setup", "compose", "--variant", "own-proxy", "--image", setupTestImage)
	if err != nil || !strings.Contains(out, "x-bekup-image: &bekup-image "+setupTestImage+"\n") || !strings.Contains(out, "aliases: [bekup-serve]") {
		t.Fatalf("compose: %v\n%.300s", err, out)
	}
	for _, retired := range []string{"traefik-external", "npm"} {
		if _, _, err := run(t, "setup", "compose", "--variant", retired); err == nil || !strings.Contains(err.Error(), "retired") {
			t.Fatalf("retired %s: %v", retired, err)
		}
	}
	out, _, err = run(t, "setup", "compose", "--variant", "full")
	if err != nil || !strings.Contains(out, setup.ComposePlaceholderLine+"\n") {
		t.Fatalf("example: %v", err)
	}
	if _, _, err := run(t, "setup", "compose", "--variant", "caddy"); err == nil {
		t.Fatal("bad variant accepted")
	}
	if _, _, err := run(t, "setup", "compose", "--variant", "own-proxy", "--image", "bekup:1.0.0"); err == nil || !strings.Contains(err.Error(), "RepoDigests") {
		t.Fatalf("unpinned image: %v", err)
	}
}

// TestSetupPlaceholderCLI: the committed compose file (deploy/compose/)
// takes the image from BEKUP_IMAGE; init writes it to .env, upgrade
// rewrites it.
func TestSetupPlaceholderCLI(t *testing.T) {
	data, err := setup.RenderRelease(setup.VariantTraefik, setup.DBBundled, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_GID", "998")
	base := []string{"setup", "init", "--dir", dir, "--non-interactive", "--allow-version-mismatch",
		"--domain", "backup.example.com", "--acme-email", "ops@example.com", "--ownership", "none", "--ca-passphrase-stdin"}
	t.Setenv("BEKUP_IMAGE", "")
	if _, _, err := runIn(t, "correct horse battery staple x\n", base...); err == nil || !strings.Contains(err.Error(), "it is not set") {
		t.Fatalf("no BEKUP_IMAGE: %v", err)
	}
	t.Setenv("BEKUP_IMAGE", "registry.example.com/team/bekup:1.0.0")
	if _, _, err := runIn(t, "correct horse battery staple x\n", base...); err == nil || !strings.Contains(err.Error(), "not digest-pinned") {
		t.Fatalf("unpinned BEKUP_IMAGE: %v", err)
	}
	t.Setenv("BEKUP_IMAGE", setupTestImage)
	stdout, stderr, err := runIn(t, "correct horse battery staple x\n", base...)
	if err != nil {
		t.Fatalf("init: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "compose.yaml: the traefik variant (exact), image "+setupTestImage+" (from BEKUP_IMAGE") {
		t.Fatalf("stdout:\n%s", stdout)
	}
	envPath := filepath.Clean(filepath.Join(dir, ".env"))
	env, err := os.ReadFile(envPath)
	if err != nil || !strings.Contains(string(env), "\nBEKUP_IMAGE="+setupTestImage+"\n") {
		t.Fatalf(".env: %v\n%s", err, env)
	}
	if out, _, err := run(t, "setup", "check", "--dir", dir, "--ownership", "none"); err != nil {
		t.Fatalf("check: %v\n%s", err, out)
	}
	next := "registry.example.com/team/bekup:1.0.0@sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	if _, _, err := run(t, "setup", "upgrade", "--dir", dir, "--image", next, "--allow-version-mismatch", "--ownership", "none"); err == nil || !strings.Contains(err.Error(), "BEKUP_IMAGE="+next+" docker compose run --rm setup upgrade") {
		t.Fatalf("upgrade --image only: %v", err)
	}
	t.Setenv("BEKUP_IMAGE", next)
	if out, _, err := run(t, "setup", "check", "--dir", dir, "--ownership", "none"); err == nil || !strings.Contains(out, "overrides .env") {
		t.Fatalf("check with a shell override: %v\n%s", err, out)
	}
	out, _, err := run(t, "setup", "upgrade", "--dir", dir, "--allow-version-mismatch", "--ownership", "none")
	if err != nil || !strings.Contains(out, ".env: BEKUP_IMAGE="+next) {
		t.Fatalf("upgrade: %v\n%s", err, out)
	}
	if env, err := os.ReadFile(envPath); err != nil || !strings.Contains(string(env), "\nBEKUP_IMAGE="+next+"\n") || strings.Count(string(env), "BEKUP_IMAGE=") != 1 {
		t.Fatalf(".env after upgrade: %v\n%s", err, env)
	}
	if out, _, err := run(t, "setup", "check", "--dir", dir, "--ownership", "none"); err != nil {
		t.Fatalf("check after upgrade: %v\n%s", err, out)
	}
}

// D-215: the own-proxy and database flags map onto the answers: --proxy own
// names the variant setup-first, --proxy-network is the proxy's network in
// join mode and the link network's name in link mode, and the database
// location, network and TLS choice pass through (validation: internal/setup).
func TestSetupInitOwnProxyFlags(t *testing.T) {
	t.Setenv("BEKUP_IMAGE", "")
	t.Setenv("DOCKER_GID", "")
	old := setupInit
	t.Cleanup(func() { setupInit = old })
	var got setup.Options
	setupInit = func(o setup.Options) error { got = o; return nil }
	base := []string{"setup", "init", "--dir", t.TempDir(), "--non-interactive", "--image", setupTestImage,
		"--write-compose", "--allow-version-mismatch", "--domain", "backup.example.com", "--docker-gid", "999",
		"--ownership", "none", "--ca-passphrase-stdin", "--proxy", "own"}
	const pass = "correct horse battery staple x\n"
	if _, stderr, err := runIn(t, pass, append(base, "--proxy-network", "npm_default", "--proxy-container", "npm", "--proxy-labels",
		"--database", "external", "--db-location", "docker", "--db-network", "dbnet", "--db-host", "postgres", "--db-tls", "disable")...); err != nil {
		t.Fatalf("join: %v\n%s", err, stderr)
	}
	a := got.Answers
	if a.Variant != setup.VariantOwnProxy || a.Proxy != setup.ProxyExternal || a.ProxyMode != "" || a.ProxyNetwork != "npm_default" ||
		a.ProxyContainer != "npm" || !a.ProxyLabels || a.Link != (setup.LinkNetwork{}) {
		t.Fatalf("join answers %+v", a)
	}
	if d := a.DB; d.Location != setup.DBDocker || d.Network != "dbnet" || d.Host != "postgres" || d.TLS != setup.DBTLSDisable || d.CAFile != "" {
		t.Fatalf("database %+v", d)
	}
	if _, stderr, err := runIn(t, pass, append(base, "--proxy-network-mode", "link", "--proxy-network", "edge", "--proxy-subnet", "10.9.9.0/28",
		"--proxy-ip", "10.9.9.5", "--serve-ip", "10.9.9.10")...); err != nil {
		t.Fatalf("link: %v\n%s", err, stderr)
	}
	a = got.Answers
	if a.ProxyMode != setup.ProxyLink || a.ProxyNetwork != "" || a.Link != (setup.LinkNetwork{Name: "edge", Subnet: "10.9.9.0/28", ProxyIP: "10.9.9.5", ServeIP: "10.9.9.10"}) {
		t.Fatalf("link answers %+v", a)
	}
	for _, bad := range [][]string{
		{"--proxy-network-mode", "bridge"},
		{"--db-tls", "prefer"},
		{"--db-location", "cloud"},
		{"--proxy", "caddy"},
		{"--proxy", "external"},
	} {
		if _, _, err := runIn(t, pass, append(slices.Clone(base), bad...)...); err == nil || !strings.Contains(err.Error(), bad[0]) {
			t.Errorf("%v: %v", bad, err)
		}
	}
}
