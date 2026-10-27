package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCertsInitInspectRotateUnencrypted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "certs")
	out, stderr, err := run(t, "certs", "init", "--dir", dir, "--unencrypted-ca-key", "--runner-san", "bekup-runner")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"ca/ca.crt", "ca/ca.key", "runner/runner.key", "serve/serve.crt", "worker/ca.crt"} {
		if !strings.Contains(out, filepath.Join(dir, f)) {
			t.Errorf("output does not list %s:\n%s", f, out)
		}
	}
	if !strings.Contains(stderr, "Move it OFFLINE") || !strings.Contains(stderr, "NOT ENCRYPTED") {
		t.Errorf("missing CA key warning: %q", stderr)
	}

	if _, _, err := run(t, "certs", "init", "--dir", dir, "--unencrypted-ca-key"); err == nil ||
		!strings.Contains(err.Error(), "already exists") {
		t.Errorf("re-init: %v", err)
	}

	out, _, err = run(t, "certs", "inspect", "--dir", dir)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	for _, want := range []string{"URI:spiffe://bekup/serve", "DNS:runner, DNS:bekup-runner", "sha256:", "status:      OK"} {
		if !strings.Contains(out, want) {
			t.Errorf("inspect output lacks %q", want)
		}
	}
	if _, _, err := run(t, "certs", "inspect", "--dir", dir, "--warn-days", "400"); err == nil ||
		!strings.Contains(err.Error(), "expiring within 400 days") {
		t.Errorf("inspect --warn-days 400: %v", err)
	}
	if _, _, err := run(t, "certs", "inspect", filepath.Join(dir, "serve", "serve.crt")); err != nil {
		t.Errorf("inspect file: %v", err)
	}

	if _, _, err := run(t, "certs", "rotate", "--no-audit", "--dir", dir, "--component", "serve"); err != nil {
		t.Errorf("rotate serve: %v", err)
	}
	for _, phase := range []string{"stage", "promote", "finish"} {
		if _, _, err := run(t, "certs", "rotate", "--no-audit", "--dir", dir, "--ca", phase, "--unencrypted-ca-key"); err != nil {
			t.Fatalf("rotate --ca %s: %v", phase, err)
		}
	}
	if _, _, err := run(t, "certs", "rotate", "--no-audit", "--dir", dir, "--all"); err != nil {
		t.Errorf("rotate --all: %v", err)
	}
	out, _, err = run(t, "certs", "inspect", "--dir", dir)
	if err != nil || !strings.Contains(out, "DNS:bekup-runner") {
		t.Errorf("after CA rotation: %v\n%s", err, out)
	}
}

func TestCertsPassphrase(t *testing.T) {
	noTTY := func(string, io.Writer) ([]byte, bool, error) { return nil, false, nil }
	old := readPassword
	t.Cleanup(func() { readPassword = old })
	readPassword = noTTY

	dir := filepath.Join(t.TempDir(), "certs")
	if _, _, err := run(t, "certs", "init", "--dir", dir); err == nil || !strings.Contains(err.Error(), "no terminal") {
		t.Fatalf("init without passphrase source: %v", err)
	}

	pf := filepath.Join(t.TempDir(), "pass")
	if err := os.WriteFile(pf, []byte("short\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "certs", "init", "--dir", dir, "--ca-passphrase-file", pf); err == nil ||
		!strings.Contains(err.Error(), "too short") {
		t.Fatalf("short passphrase: %v", err)
	}
	if err := os.WriteFile(pf, []byte("a sufficiently long passphrase\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "certs", "init", "--dir", dir, "--ca-passphrase-file", pf); err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(filepath.Join(dir, "ca", "ca.key")) // #nosec G304 -- test file
	if err != nil || !strings.HasPrefix(string(key), "-----BEGIN AGE ENCRYPTED FILE-----") {
		t.Fatalf("CA key not age-encrypted (%v)", err)
	}
	if _, _, err := run(t, "certs", "rotate", "--no-audit", "--dir", dir, "--all"); err == nil ||
		!strings.Contains(err.Error(), "--ca-passphrase-file") {
		t.Errorf("rotate without passphrase: %v", err)
	}
	if _, _, err := run(t, "certs", "rotate", "--no-audit", "--dir", dir, "--all", "--ca-passphrase-file", pf); err != nil {
		t.Errorf("rotate with passphrase: %v", err)
	}

	// Terminal prompt: mismatching repeats are refused.
	answers := []string{"first passphrase 1234", "other passphrase 1234"}
	readPassword = func(string, io.Writer) ([]byte, bool, error) {
		a := answers[0]
		answers = answers[1:]
		return []byte(a), true, nil
	}
	dir2 := filepath.Join(t.TempDir(), "certs")
	if _, _, err := run(t, "certs", "init", "--dir", dir2); err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Errorf("mismatched prompt: %v", err)
	}
}

func TestCertsFlagValidation(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"certs", "init", "--unencrypted-ca-key"},
		{"certs", "init", "--dir", dir, "--unencrypted-ca-key", "--component", "db"},
		{"certs", "init", "--dir", dir, "--unencrypted-ca-key", "--runner-san", "*.evil"},
		{"certs", "init", "--dir", dir, "--unencrypted-ca-key", "--ca-days", "5000"},
		{"certs", "rotate", "--no-audit", "--dir", dir},
		{"certs", "rotate", "--no-audit", "--dir", dir, "--all", "--ca", "stage"},
		{"certs", "rotate", "--no-audit", "--dir", dir, "--ca", "bogus"},
		{"certs", "inspect"},
		{"certs", "inspect", "--dir", dir, "--warn-days", "-1"},
	} {
		if _, _, err := run(t, args...); err == nil {
			t.Errorf("%v: accepted", args)
		}
	}
}

func TestCertsPostgresComponent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "certs")
	if _, _, err := run(t, "certs", "init", "--dir", dir, "--unencrypted-ca-key",
		"--component", "postgres", "--postgres-san", "pg.lan"); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, "certs", "inspect", filepath.Join(dir, "postgres", "postgres.crt"))
	if err != nil || !strings.Contains(out, "DNS:postgres, DNS:pg.lan") {
		t.Fatalf("inspect postgres: %v\n%s", err, out)
	}
	if _, _, err := run(t, "certs", "rotate", "--no-audit", "--dir", dir, "--component", "postgres"); err != nil {
		t.Fatalf("rotate postgres: %v", err)
	}
	if _, _, err := run(t, "certs", "rotate", "--no-audit", "--dir", dir, "--ca", "promote", "--postgres-san", "x"); err == nil ||
		!strings.Contains(err.Error(), "only applies to the stage phase") {
		t.Fatalf("--postgres-san with promote: %v", err)
	}
}

// certs rotate records certs.rotated in the app database (M1 code review
// L3); without one it refuses before changing any file, unless --no-audit.
func TestCertsRotateNeedsAuditDB(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "certs")
	if _, _, err := run(t, "certs", "init", "--dir", dir, "--unencrypted-ca-key"); err != nil {
		t.Fatal(err)
	}
	crt := filepath.Join(dir, "serve", "serve.crt")
	before, err := os.ReadFile(crt) // #nosec G304 -- test file
	if err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing.toml")
	_, _, err = run(t, "--config", missing, "certs", "rotate", "--dir", dir, "--component", "serve")
	if err == nil || !strings.Contains(err.Error(), "--no-audit") {
		t.Fatalf("rotate without an audit database: %v", err)
	}
	if _, _, err := run(t, "--config", missing, "certs", "init", "--dir", dir, "--unencrypted-ca-key", "--force"); err == nil {
		t.Fatal("init --force without an audit database accepted")
	}
	after, err := os.ReadFile(crt) // #nosec G304 -- test file
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("a refused rotation changed the certificate")
	}
	_, stderr, err := run(t, "certs", "rotate", "--no-audit", "--dir", dir, "--component", "serve")
	if err != nil || !strings.Contains(stderr, "NOT recorded in the audit log") {
		t.Fatalf("rotate --no-audit: %v %q", err, stderr)
	}
}
