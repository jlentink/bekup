//go:build integration

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/logging"
)

func TestAdminCommands(t *testing.T) {
	ctx := t.Context()
	d := dbtest.Migrated(t)
	pool := d.AppPool(t)
	// Cheap argon2 parameters keep the test fast (the CLI uses [security]).
	cfg := writeConfig(t, "bekup.toml", fmt.Sprintf(`
[database]
url = %q
tls_mode = "verify-full"
tls_ca_file = %q
`, d.AppURL, dbtest.CAFile()))
	pwFile := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(pwFile, []byte("blue-mountain-river-42\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := []string{"--dev", "--config", cfg, "admin"}
	cli := func(args ...string) (string, error) {
		out, _, err := run(t, append(append([]string{}, base...), args...)...)
		return out, err
	}

	// Passwords never come from a flag value or a non-terminal stdin.
	if _, err := cli("create", "--email", "ops@example.com", "--display-name", "Ops"); err == nil || !strings.Contains(err.Error(), "--password-file") {
		t.Fatalf("no terminal: %v", err)
	}
	if _, err := cli("create", "--email", "ops@example.com", "--display-name", "Ops", "--password", "x"); err == nil {
		t.Fatal("--password flag accepted")
	}

	out, err := cli("create", "--email", "ops@example.com", "--display-name", "Ops", "--password-file", pwFile)
	if err != nil || !strings.Contains(out, "created admin account ops@example.com") {
		t.Fatalf("create: %v\n%s", err, out)
	}
	q := sqlc.New(pool)
	u, err := q.GetActiveUserByEmail(ctx, "ops@example.com")
	if err != nil || u.Role != sqlc.UserRoleAdmin || u.PasswordTemporaryUntil != nil {
		t.Fatalf("user: %+v %v", u, err)
	}
	if _, err := cli("create", "--email", "OPS@example.com", "--display-name", "Dup", "--password-file", pwFile); err == nil || !strings.Contains(err.Error(), "already uses") {
		t.Fatalf("duplicate: %v", err)
	}
	weak := filepath.Join(t.TempDir(), "weak")
	if err := os.WriteFile(weak, []byte("password1234"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cli("create", "--email", "w@example.com", "--display-name", "W", "--password-file", weak); err == nil || !strings.Contains(err.Error(), "password rejected") {
		t.Fatalf("weak: %v", err)
	}

	if err := d.SuperExec(ctx, "UPDATE users SET failed_login_count = 9, locked_until = now() + interval '1 hour'"); err != nil {
		t.Fatal(err)
	}
	if out, err := cli("unlock", "--email", "ops@example.com"); err != nil || !strings.Contains(out, "lockout of ops@example.com") {
		t.Fatalf("unlock: %v\n%s", err, out)
	}
	if u, err = q.GetUserByID(ctx, u.ID); err != nil || u.LockedUntil != nil || u.FailedLoginCount != 0 {
		t.Fatalf("still locked: %+v", u)
	}
	pw2 := filepath.Join(t.TempDir(), "pw2")
	if err := os.WriteFile(pw2, []byte("copper-lantern-orbit-73"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldHash := *u.PasswordHash
	if out, err := cli("reset-password", "--email", "ops@example.com", "--password-file", pw2); err != nil || !strings.Contains(out, "0 session(s) revoked") {
		t.Fatalf("reset: %v\n%s", err, out)
	}
	if u, err = q.GetUserByID(ctx, u.ID); err != nil || *u.PasswordHash == oldHash {
		t.Fatal("password unchanged")
	}
	if _, err := cli("reset-password", "--email", "nobody@example.com", "--password-file", pw2); err == nil || !strings.Contains(err.Error(), "no account") {
		t.Fatalf("unknown: %v", err)
	}
	// The last active admin cannot be disabled; a second one can.
	if _, err := cli("disable", "--email", "ops@example.com"); err == nil || !strings.Contains(err.Error(), "last active admin") {
		t.Fatalf("disable last: %v", err)
	}
	if _, err := cli("create", "--email", "maint@example.com", "--display-name", "Maint", "--role", "maintainer", "--password-file", pw2); err != nil {
		t.Fatal(err)
	}
	if out, err := cli("disable", "--email", "maint@example.com"); err != nil || !strings.Contains(out, "disabled") {
		t.Fatalf("disable: %v\n%s", err, out)
	}

	// Lost-factor recovery: every factor removed, sessions revoked, a new
	// grace period starts at mfa_reset_at.
	ops := "'" + u.ID.String() + "'"
	for _, stmt := range []string{
		"UPDATE users SET totp_secret_enc = '\\x01', totp_enabled_at = now() WHERE id = " + ops,
		"INSERT INTO recovery_codes (user_id, key_id, code_mac) VALUES (" + ops + ", '0000000a', decode(repeat('aa', 32), 'hex'))",
		"INSERT INTO webauthn_credentials (user_id, credential_id, public_key, name) VALUES (" + ops + ", decode(repeat('cc', 16), 'hex'), '\\x01', 'key')",
		"INSERT INTO sessions (user_id, token_hash, aal, idle_expires_at, absolute_expires_at) VALUES (" + ops + ", decode(repeat('dd', 32), 'hex'), 2, now() + interval '1 hour', now() + interval '2 hours')",
	} {
		if err := d.SuperExec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	out, stderr, err := run(t, append(append([]string{}, base...), "reset-2fa", "--email", "ops@example.com")...)
	if err != nil || !strings.Contains(out, "TOTP removed true, 1 passkey(s), 1 recovery code(s) removed, 1 session(s) revoked") ||
		!strings.Contains(stderr, "warning: removing every second factor") {
		t.Fatalf("reset-2fa: %v\n%s\n%s", err, out, stderr)
	}
	if u, err = q.GetUserByID(ctx, u.ID); err != nil || u.TotpSecretEnc != nil || u.MfaResetAt == nil {
		t.Fatalf("after reset-2fa: %+v %v", u, err)
	}
	if n, err := q.CountWebAuthnCredentials(ctx, u.ID); err != nil || n != 0 {
		t.Fatalf("passkeys left: %d %v", n, err)
	}
	if n, err := q.CountUnusedRecoveryCodes(ctx, u.ID); err != nil || n != 0 {
		t.Fatalf("recovery codes left: %d %v", n, err)
	}
	if sess, err := q.ListActiveUserSessions(ctx, u.ID); err != nil || len(sess) != 0 {
		t.Fatalf("sessions left: %d %v", len(sess), err)
	}

	// Every action is in the audit log with actor kind cli, OS user and hostname.
	a := audit.New(pool, audit.Options{Registry: logging.NewRegistry()})
	for _, action := range []audit.Action{
		audit.ActionCLIAdminCreated, audit.ActionCLIAdminUnlocked, audit.ActionCLIAdminPasswordReset, audit.ActionCLIAdminDisabled,
		audit.ActionCLIAdminMFAReset,
	} {
		page, err := a.List(ctx, audit.Filter{Action: action}, 0, 10)
		if err != nil || len(page.Entries) == 0 {
			t.Fatalf("%s: %v", action, err)
		}
		ent := page.Entries[0]
		if ent.ActorKind != audit.ActorCLI || !strings.Contains(string(ent.Details), audit.DetailOSUser) ||
			!strings.Contains(string(ent.Details), audit.DetailHostname) || strings.Contains(string(ent.Details), "copper") {
			t.Fatalf("%s entry: %+v %s", action, ent, ent.Details)
		}
	}
	if out, _, err := run(t, "--dev", "--config", cfg, "audit", "verify"); err != nil {
		t.Fatalf("verify: %v %s", err, out)
	}

	// Every break-glass action is also a security.system alert to the
	// admins (D-069, task 5.5) in the outbox, with the OS user and host;
	// reset-password and reset-2fa also alert the account itself.
	alerts := func(action audit.Action) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM notification_events WHERE type = 'security.system'
			AND detail->>'action' = $1 AND detail->>'via' = 'cli' AND detail ? 'hostname'`, string(action)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for action, want := range map[audit.Action]int{
		audit.ActionCLIAdminCreated: 2, audit.ActionCLIAdminUnlocked: 1, audit.ActionCLIAdminPasswordReset: 1,
		audit.ActionCLIAdminDisabled: 1, audit.ActionCLIAdminMFAReset: 1,
	} {
		if got := alerts(action); got != want {
			t.Errorf("%s: %d security.system alerts, want %d", action, got, want)
		}
	}
	var account int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notification_events WHERE type = 'security.account'
		AND user_id = $1 AND detail->>'kind' IN ('password_reset', 'second_factors_reset')`, u.ID).Scan(&account); err != nil || account != 2 {
		t.Fatalf("account alerts %d %v", account, err)
	}
}

// certs rotate records certs.rotated with actor cli (M1 code review L3).
func TestCertsRotateRecordsAudit(t *testing.T) {
	d := dbtest.Migrated(t)
	cfg := writeConfig(t, "bekup.toml", fmt.Sprintf(`
[database]
url = %q
tls_mode = "verify-full"
tls_ca_file = %q
`, d.AppURL, dbtest.CAFile()))
	dir := filepath.Join(t.TempDir(), "certs")
	if _, _, err := run(t, "certs", "init", "--dir", dir, "--unencrypted-ca-key"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "--dev", "--config", cfg, "certs", "rotate", "--dir", dir, "--component", "serve"); err != nil {
		t.Fatal(err)
	}
	page, err := audit.New(d.AppPool(t), audit.Options{}).List(t.Context(), audit.Filter{Action: audit.ActionCertsRotated}, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Entries[0].ActorKind != audit.ActorCLI ||
		!strings.Contains(string(page.Entries[0].Details), `"operation": "leaf"`) {
		t.Fatalf("entries: %+v", page.Entries)
	}
	var n int
	if err := d.AppPool(t).QueryRow(t.Context(), `SELECT count(*) FROM notification_events
		WHERE type = 'security.system' AND detail->>'kind' = 'certs_rotated' AND detail->>'via' = 'cli'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("certs.rotated alerts %d %v", n, err)
	}
}
