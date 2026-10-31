package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/logging"
)

func run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	// Isolate from the developer's environment.
	t.Setenv("BEKUP_CONFIG", "")
	t.Setenv("BEKUP_DEV", "")
	cmd := newRootCmd()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errb.String(), err
}

func writeConfig(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir() // mode 0700
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const validConfig = `
[server]
listen = "10.0.0.10:8080"
base_url = "https://backup.example.com"
trusted_proxies = ["172.18.0.0/16"]
`

func TestVersion(t *testing.T) {
	out, _, err := run(t, "version")
	if err != nil || out != "bekup "+version+"\n" {
		t.Fatalf("got %q, %v", out, err)
	}
}

func TestStubs(t *testing.T) {
	for _, s := range stubs {
		_, _, err := run(t, s.name, "--anything")
		if err == nil || !strings.Contains(err.Error(), "not implemented yet (task "+s.task+")") {
			t.Errorf("%s: got %v", s.name, err)
		}
	}
}

func TestConfigValidate(t *testing.T) {
	p := writeConfig(t, "bekup.toml", validConfig)
	out, stderr, err := run(t, "--config", p, "config", "validate")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "configuration OK (file: "+p) || !strings.Contains(stderr, "configuration loaded") {
		t.Fatalf("stdout=%q stderr=%q", out, stderr)
	}

	bad := writeConfig(t, "bekup.toml", "[server]\nbase_url = \"http://x\"\nbogus = 1\n")
	_, _, err = run(t, "--config", bad, "config", "validate")
	if err == nil || !strings.Contains(err.Error(), `unknown key "server.bogus"`) {
		t.Fatalf("got %v", err)
	}

	_, _, err = run(t, "--config", filepath.Join(t.TempDir(), "missing.toml"), "config", "validate")
	if err == nil || !strings.Contains(err.Error(), "never falls back") {
		t.Fatalf("got %v", err)
	}
}

func TestConfigValidatePrintsWarnings(t *testing.T) {
	p := writeConfig(t, "bekup.toml", "[server]\nlisten = \"10.0.0.10:8080\"\nbase_url = \"http://bekup.lan\"\nallow_insecure_http = true\n")
	t.Setenv("BEKUP_SERVER_BASEURL", "typo")
	out, stderr, err := run(t, "--config", p, "config", "validate")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"warning: plain HTTP: traffic not encrypted", "warning: environment variable BEKUP_SERVER_BASEURL does not match"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout misses %q:\n%s", want, out)
		}
	}
	if !strings.Contains(stderr, "configuration warning") {
		t.Errorf("stderr misses the warning log: %q", stderr)
	}
}

func TestErrorMessageRedacted(t *testing.T) {
	err := errors.New("connect postgres://bekup_owner:s3cr3t@db/bekup: password=hunter22 failed")
	msg := errorMessage(err)
	for _, leak := range []string{"s3cr3t", "hunter22"} {
		if strings.Contains(msg, leak) {
			t.Fatalf("leaks %q: %s", leak, msg)
		}
	}
	unregister := logging.DefaultRegistry.Register([]byte("registered-secret-value"))
	defer unregister()
	if msg := errorMessage(errors.New("bad registered-secret-value")); strings.Contains(msg, "registered-secret-value") {
		t.Fatalf("registered secret not scrubbed: %s", msg)
	}
}

func TestConfigValidateDevWarns(t *testing.T) {
	t.Chdir(t.TempDir()) // no ./bekup.toml
	_, stderr, err := run(t, "--dev", "config", "validate")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "DEV MODE IS ENABLED") {
		t.Fatalf("missing dev warning: %q", stderr)
	}
}

func TestConfigPrint(t *testing.T) {
	p := writeConfig(t, "bekup.toml", validConfig)
	out, _, err := run(t, "--config", p, "--log-level", "debug", "config", "print", "--redacted")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# loaded from: " + p, "level = 'debug'  # flag", "base_url = 'https://backup.example.com'  # file"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if _, _, err := run(t, "--config", p, "config", "print", "--redacted=false"); err == nil {
		t.Fatal("--redacted=false must be refused")
	}
}

func TestConfigPrintRunner(t *testing.T) {
	p := writeConfig(t, "runner.toml", "[listen]\naddress = \"10.0.0.11:7443\"\n[targets]\nallow_cidrs = [\"10.0.0.0/8\"]\n")
	out, _, err := run(t, "--config", p, "config", "print", "--runner")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "allow_cidrs = ['10.0.0.0/8']  # file") {
		t.Fatalf("unexpected:\n%s", out)
	}
}

func TestMigrateDownOneNeedsDev(t *testing.T) {
	p := writeConfig(t, "bekup.toml", validConfig)
	_, _, err := run(t, "--config", p, "migrate", "down-one")
	if err == nil || !strings.Contains(err.Error(), "only allowed in dev mode") {
		t.Fatalf("got %v", err)
	}
}

func TestMigrateMissingOwnerURL(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "owner_url")
	p := writeConfig(t, "bekup.toml", validConfig+"\n[database]\nowner_url_file = \""+missing+"\"\n")
	_, _, err := run(t, "--config", p, "migrate", "status")
	if err == nil || !strings.Contains(err.Error(), "read database owner credentials") {
		t.Fatalf("got %v", err)
	}
}

func TestDocsCLI(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "bekup_removed.md")
	if err := os.WriteFile(stale, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "docs", "cli", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(dir, "bekup_config_validate.md")
	first, err := os.ReadFile(page) //nolint:gosec // G304: test temp dir
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(first), "Auto generated") {
		t.Fatal("the auto-gen date tag must be disabled")
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale file not removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bekup_docs.md")); !os.IsNotExist(err) {
		t.Fatal("the hidden docs command must not be documented")
	}
	if _, _, err := run(t, "docs", "cli", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(page) //nolint:gosec // G304: test temp dir
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("output is not deterministic: %v", err)
	}
	if out, _, err := run(t, "--help"); err != nil || strings.Contains(out, "docs") {
		t.Fatalf("docs must be hidden from help (%v):\n%s", err, out)
	}
}
