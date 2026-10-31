package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/logging"
)

func writePW(t *testing.T, dir, name, pw string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(pw+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDBRolesSQL(t *testing.T) {
	dir := t.TempDir()
	const ownerPW, appPW = "owner-password-0123456789", `app-p@ss'w"o\rd;--0123`
	owner, app := writePW(t, dir, "owner", ownerPW), writePW(t, dir, "app", appPW)
	out, stderr, err := run(t, "db", "roles-sql", "--database", "bekup_prod", "--owner-password-file", owner, "--app-password-file", app, "--create-extensions")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{ownerPW, appPW, "p@ss"} {
		if strings.Contains(out, s) || strings.Contains(stderr, s) {
			t.Fatal("plaintext password in the output")
		}
	}
	if stderr != "" {
		t.Errorf("stderr not empty: %q", stderr)
	}
	for _, want := range []string{
		"CREATE ROLE bekup_owner WITH LOGIN", "PASSWORD 'SCRAM-SHA-256$4096:", `CREATE DATABASE "bekup_prod" WITH OWNER bekup_owner`,
		`\connect "bekup_prod"`, "CREATE EXTENSION IF NOT EXISTS citext;", "REVOKE bekup_owner FROM CURRENT_USER;", "SENSITIVE",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}

	out, _, err = run(t, "db", "roles-sql", "--passwords", "prompt")
	if err != nil || !strings.Contains(out, `\password bekup_app`) || strings.Contains(out, "SCRAM-SHA-256$") {
		t.Fatalf("prompt mode: %v\n%s", err, out)
	}

	short := writePW(t, dir, "short", "short")
	loose := writePW(t, dir, "loose", "loose-password-0123456789")
	if err := os.Chmod(loose, 0o644); err != nil { //nolint:gosec // G302: the test needs a world-readable file
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"no files":        {"db", "roles-sql"},
		"prompt + files":  {"db", "roles-sql", "--passwords", "prompt", "--app-password-file", app},
		"bad mode":        {"db", "roles-sql", "--passwords", "plain"},
		"bad database":    {"db", "roles-sql", "--passwords", "prompt", "--database", "template1"},
		"short password":  {"db", "roles-sql", "--owner-password-file", short, "--app-password-file", app},
		"unsafe file":     {"db", "roles-sql", "--owner-password-file", loose, "--app-password-file", app},
		"no stdout dumps": {"db", "roles-sql", "--owner-password-file", filepath.Join(dir, "missing"), "--app-password-file", app},
	} {
		out, _, err := run(t, args...)
		if err == nil {
			t.Errorf("%s accepted", name)
		}
		if out != "" {
			t.Errorf("%s printed %q", name, out)
		}
		if err != nil && (strings.Contains(err.Error(), "p@ss") || strings.Contains(err.Error(), "loose-password")) {
			t.Errorf("%s: error leaks a password: %v", name, err)
		}
	}
}

func bootstrapConfig(t *testing.T, extra string) string {
	t.Helper()
	dir := t.TempDir()
	app := writePW(t, dir, "app", "app-password-0123456789")
	owner := writePW(t, dir, "owner", "owner-password-0123456789")
	return writeConfig(t, "bekup.toml", validConfig+fmt.Sprintf(`
[database]
host = "127.0.0.1"
port = 1
password_file = %q
owner_password_file = %q
tls_mode = "require"
tls_ca_file = ""
connect_timeout = "1s"
%s`, app, owner, extra))
}

func TestDBBootstrapRefusals(t *testing.T) {
	urlMode := writeConfig(t, "bekup.toml", validConfig)
	if _, _, err := run(t, "--config", urlMode, "db", "bootstrap", "--admin-user", "admin"); err == nil || !strings.Contains(err.Error(), "needs fields mode") {
		t.Fatalf("URL mode: %v", err)
	}
	p := bootstrapConfig(t, "")
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no admin user":     {nil, "--admin-user is required"},
		"admin is bekup":    {[]string{"--admin-user", "bekup_owner"}, "invalid role name"},
		"bad admin db":      {[]string{"--admin-user", "admin", "--admin-database", "a;b"}, "--admin-database"},
		"admin db is bekup": {[]string{"--admin-user", "admin", "--admin-database", "bekup"}, "maintenance database"},
		"bad admin port":    {[]string{"--admin-user", "admin", "--admin-port", "70000"}, "--admin-port"},
		"no tty":            {[]string{"--admin-user", "admin"}, "pass --admin-password-file"},
		"tls flag invalid":  {[]string{"--admin-user", "admin", "--tls-mode", "prefer"}, "database.tls_mode"},
		"tls require":       {[]string{"--admin-user", "admin"}, "--allow-insecure-admin-connection"},
		"tls disable":       {[]string{"--admin-user", "admin", "--tls-mode", "disable"}, "WITHOUT ENCRYPTION"},
		"admin is bekup_x":  {[]string{"--admin-user", "bekup_admin"}, "invalid role name"},
		"reset not adopted": {[]string{"--admin-user", "admin", "--reset-passwords"}, "--adopt-existing-roles"},
	} {
		t.Run(name, func(t *testing.T) {
			orig := readPassword
			readPassword = func(string, io.Writer) ([]byte, bool, error) { return nil, false, nil }
			t.Cleanup(func() { readPassword = orig })
			args := []string{"--config", p, "db", "bootstrap"}
			if !strings.HasPrefix(name, "tls ") {
				// The test server has no CA: tls_mode require.
				args = append(args, "--allow-insecure-admin-connection")
			}
			_, _, err := run(t, append(args, tc.args...)...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}

	single := bootstrapConfig(t, "")
	content, err := os.ReadFile(single) //nolint:gosec // G304: test file
	if err != nil {
		t.Fatal(err)
	}
	s := strings.Replace(string(content), "owner_password_file", "# owner_password_file", 1) + "user = \"acct\"\nsingle_account = true\n"
	if err := os.WriteFile(single, []byte(s), 0o600); err != nil { //nolint:gosec // G703: a test file in t.TempDir
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"other admin": {[]string{"--admin-user", "admin"}, "--admin-user must be database.user"},
		"reset":       {[]string{"--reset-passwords"}, "do not apply"},
	} {
		t.Run("single "+name, func(t *testing.T) {
			_, _, err := run(t, append([]string{"--config", single, "db", "bootstrap", "--allow-insecure-admin-connection"}, tc.args...)...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}

	// A file-based admin password reaches the connection attempt (port 1:
	// refused) and never shows up in the output.
	adminPW := writePW(t, t.TempDir(), "admin", "admin-s3cret-password")
	out, stderr, err := run(t, "--config", p, "db", "bootstrap", "--admin-user", "admin", "--admin-password-file", adminPW, "--allow-insecure-admin-connection")
	if err == nil || !strings.Contains(err.Error(), "connect to database postgres as admin") {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(out+stderr+err.Error(), "admin-s3cret") {
		t.Fatal("admin password leaked")
	}

	// The explicitly allowed insecure admin connection warned loudly.
	if !strings.Contains(stderr, "INSECURE ADMIN CONNECTION") {
		t.Fatalf("no warning in %q", stderr)
	}
}

func TestBootstrapAuditDetailsNotRedacted(t *testing.T) {
	d := bootstrapAuditDetails(&config.Bekup{}, &db.BootstrapResult{}, &bootstrapFlags{})
	for k := range d {
		if logging.IsSecretKey(k) {
			t.Errorf("audit detail %q would be redacted", k)
		}
	}
}
