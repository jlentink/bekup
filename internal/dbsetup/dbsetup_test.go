package dbsetup

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/logging"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// fieldsConfig is a fields-mode configuration with role password files and
// an unreachable server (port 1).
func fieldsConfig(t *testing.T, mut func(*config.Database)) *config.Bekup {
	t.Helper()
	dir := t.TempDir()
	cfg := config.DefaultBekup()
	d := &cfg.Database
	d.Host, d.Port, d.Name, d.TLSMode = "127.0.0.1", 1, "bekup", config.DBTLSRequire
	d.User, d.OwnerUser, d.TLSCAFile = config.DBAppRole, config.DBOwnerRole, ""
	d.PasswordFile = writeFile(t, dir, "app", "app-password-0123456789")
	d.OwnerPasswordFile = writeFile(t, dir, "owner", "owner-password-0123456789")
	if mut != nil {
		mut(d)
	}
	return &cfg
}

func TestCheckAdminTLS(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		mode    string
		allow   bool
		wantErr string
		warn    string
	}{
		{mode: config.DBTLSVerifyFull},
		{mode: config.DBTLSVerifyFull, allow: true},
		{mode: config.DBTLSRequire, wantErr: "--allow-insecure-admin-connection"},
		{mode: config.DBTLSDisable, wantErr: "WITHOUT ENCRYPTION"},
		{mode: config.DBTLSRequire, allow: true, warn: "without verifying the server certificate"},
		{mode: config.DBTLSDisable, allow: true, warn: "WITHOUT ENCRYPTION"},
	} {
		var w bytes.Buffer
		err := CheckAdminTLS(&w, tc.mode, tc.allow)
		switch {
		case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
			t.Errorf("%+v: error %v", tc, err)
		case tc.wantErr == "" && err != nil:
			t.Errorf("%+v: %v", tc, err)
		case tc.warn == "" && w.Len() > 0:
			t.Errorf("%+v: warned %q", tc, w.String())
		case tc.warn != "" && !strings.Contains(w.String(), "INSECURE ADMIN CONNECTION") || !strings.Contains(w.String(), tc.warn):
			t.Errorf("%+v: warning %q", tc, w.String())
		}
	}
}

// TestBootstrapRefusals: every refusal comes before the administrator
// password is read, and nothing connects.
func TestBootstrapRefusals(t *testing.T) {
	t.Parallel()
	single := func(d *config.Database) {
		d.SingleAccount, d.User, d.OwnerPasswordFile = true, "acct", ""
	}
	for name, tc := range map[string]struct {
		mut  func(*config.Database)
		req  BootstrapRequest
		want string
	}{
		"url mode":          {mut: func(d *config.Database) { d.Host = "" }, want: "needs fields mode"},
		"tls require":       {req: BootstrapRequest{AdminUser: "admin"}, want: "--allow-insecure-admin-connection"},
		"admin port":        {req: BootstrapRequest{AdminUser: "admin", AdminPort: 70000, AllowInsecure: true}, want: "--admin-port"},
		"admin database":    {req: BootstrapRequest{AdminUser: "admin", AdminDatabase: "a;b", AllowInsecure: true}, want: "--admin-database"},
		"bekup database":    {req: BootstrapRequest{AdminUser: "admin", AdminDatabase: "bekup", AllowInsecure: true}, want: "maintenance database"},
		"no admin user":     {req: BootstrapRequest{AllowInsecure: true}, want: "--admin-user is required"},
		"bekup admin":       {req: BootstrapRequest{AdminUser: "bekup_owner", AllowInsecure: true}, want: "invalid role name"},
		"pg admin":          {req: BootstrapRequest{AdminUser: "pg_monitor", AllowInsecure: true}, want: "invalid role name"},
		"reset not adopted": {req: BootstrapRequest{AdminUser: "admin", ResetPasswords: true, AllowInsecure: true}, want: "--adopt-existing-roles"},
		"single other user": {mut: single, req: BootstrapRequest{AdminUser: "admin", AllowInsecure: true}, want: "--admin-user must be database.user"},
		"single reset":      {mut: single, req: BootstrapRequest{ResetPasswords: true, AllowInsecure: true}, want: "do not apply"},
		"single file":       {mut: single, req: BootstrapRequest{AdminPasswordFileSet: true, AllowInsecure: true}, want: "do not apply"},
		"single external":   {mut: single, req: BootstrapRequest{ExternalPasswordFile: "/nonexistent", AllowInsecure: true}, want: "does not support the external API"},
		"no password func":  {req: BootstrapRequest{AdminUser: "admin", AllowInsecure: true}, want: "no administrator password"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			req := tc.req
			req.Config = fieldsConfig(t, tc.mut)
			var out, warn bytes.Buffer
			req.Out, req.Warn, req.Logger = &out, &warn, slog.New(slog.DiscardHandler)
			read := false
			if name != "no password func" {
				req.AdminPassword = func() ([]byte, error) { read = true; return []byte("admin-pass"), nil }
			}
			err := Bootstrap(context.Background(), req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
			if read {
				t.Fatal("the administrator password was read before the request passed its checks")
			}
			if out.Len() > 0 {
				t.Fatalf("report before a refusal: %q", out.String())
			}
		})
	}
}

// TestBootstrapPassword: the password callback's errors and an empty
// password stop bootstrap; a password reaching the connection attempt is
// zeroed and never shown.
func TestBootstrapPassword(t *testing.T) {
	t.Parallel()
	base := func() BootstrapRequest {
		return BootstrapRequest{Config: fieldsConfig(t, nil), AdminUser: "admin", AllowInsecure: true,
			Out: &bytes.Buffer{}, Warn: &bytes.Buffer{}, Logger: slog.New(slog.DiscardHandler)}
	}
	req := base()
	req.AdminPassword = func() ([]byte, error) { return nil, errors.New("no terminal") }
	if err := Bootstrap(context.Background(), req); err == nil || !strings.Contains(err.Error(), "no terminal") {
		t.Fatalf("callback error: %v", err)
	}
	req = base()
	req.AdminPassword = func() ([]byte, error) { return []byte{}, nil }
	if err := Bootstrap(context.Background(), req); err == nil || !strings.Contains(err.Error(), "empty administrator password") {
		t.Fatalf("empty password: %v", err)
	}
	req = base()
	var out, warn bytes.Buffer
	req.Out, req.Warn = &out, &warn
	pw := []byte("admin-s3cret-password")
	req.AdminPassword = func() ([]byte, error) { return pw, nil }
	err := Bootstrap(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "bootstrap failed") {
		t.Fatalf("unreachable server: %v", err)
	}
	if !bytes.Equal(pw, make([]byte, len(pw))) {
		t.Fatal("the administrator password was not zeroed")
	}
	shown := err.Error() + out.String() + warn.String()
	if strings.Contains(shown, "admin-s3cret") || !strings.Contains(warn.String(), "INSECURE ADMIN CONNECTION") {
		t.Fatalf("output %q", shown)
	}
}

func TestWriteBootstrap(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	if err := WriteBootstrap(&b, nil, false); err != nil || b.Len() != 0 {
		t.Fatalf("nil result: %v %q", err, b.String())
	}
	r := &db.BootstrapResult{
		ServerVersion: "17.1", AdminRole: "admin", Actions: []string{"create role bekup_owner"}, Warnings: []string{"w1"},
		Citext: db.CitextInstalled, ManualRevoke: "REVOKE bekup_owner FROM admin;",
		TLS: db.TLSFacts{Mode: config.DBTLSVerifyFull, Encrypted: true, Version: "TLS 1.3", ChainVerified: true, HostVerified: true, ChannelBinding: true},
	}
	if err := WriteBootstrap(&b, r, true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"PostgreSQL 17.1 (admin role admin)", "TLS 1.3, tls_mode verify-full, chain verified: true, host verified: true, SCRAM channel binding required",
		"ACTION REQUIRED", "REVOKE bekup_owner FROM admin;", "would do: create role bekup_owner", "citext:  " + db.CitextInstalled, "WARNING: w1"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("report misses %q:\n%s", want, b.String())
		}
	}
}

func TestWritePreflight(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	rep := &db.PreflightReport{ServerVersion: "16.4", Database: "bekup", Citext: "creatable", TLS: db.TLSFacts{Mode: config.DBTLSDisable}}
	if err := WritePreflight(&b, rep, nil); err != nil {
		t.Fatal(err)
	}
	if s := b.String(); !strings.Contains(s, "NOT ENCRYPTED (tls_mode disable)") || !strings.Contains(s, "bekup_owner and bekup_app checked") || !strings.Contains(s, "preflight OK") {
		t.Fatalf("report:\n%s", s)
	}
	b.Reset()
	rep.SingleAccount, rep.LoginFindings = true, []string{"CREATEROLE"}
	if err := WritePreflight(&b, rep, errors.Join(errors.New("a failed"), errors.Join(errors.New("b failed")))); err != nil {
		t.Fatal(err)
	}
	if s := b.String(); !strings.Contains(s, "SINGLE-ACCOUNT MODE") || !strings.Contains(s, "login role:    CREATEROLE") ||
		!strings.Contains(s, "FAILED: a failed\nFAILED: b failed\n") || strings.Contains(s, "preflight OK") {
		t.Fatalf("report:\n%s", s)
	}
	b.Reset()
	if err := WritePreflight(&b, nil, errors.New("connect failed")); err != nil || b.String() != "FAILED: connect failed\n" {
		t.Fatalf("no report: %v %q", err, b.String())
	}
}

func TestTLSFactsLine(t *testing.T) {
	t.Parallel()
	f := db.TLSFacts{Mode: config.DBTLSRequire, Encrypted: true, Version: "TLS 1.3", ServerNameOverride: true}
	if got := TLSFactsLine(f); got != "TLS 1.3, tls_mode require, chain verified: false, host verified: false (against tls_server_name)" {
		t.Fatalf("got %q", got)
	}
}

func TestBootstrapAuditDetailsNotRedacted(t *testing.T) {
	t.Parallel()
	d := BootstrapAuditDetails(&config.Bekup{}, &db.BootstrapResult{Actions: []string{"x"}, AdminRole: "admin"}, true, true)
	for k := range d {
		if logging.IsSecretKey(k) {
			t.Errorf("audit detail %q would be redacted", k)
		}
	}
	if d["rotated_logins"] != true || d["adopted_roles"] != true || d["admin_role"] != "admin" {
		t.Fatalf("details %+v", d)
	}
}

// TestCheckDeployment: the setup version guard (D-212) of the commands
// without the app and the browser setup's database step.
func TestCheckDeployment(t *testing.T) {
	t.Parallel()
	env := func(kv ...string) func(string) (string, bool) {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	for _, tc := range []struct {
		name    string
		dev     bool
		lookup  func(string) (string, bool)
		wantErr bool
		log     string
	}{
		{name: "match", lookup: env("BEKUP_SETUP_VERSION", "1.2.3")},
		{name: "v prefix", lookup: env("BEKUP_SETUP_VERSION", "v1.2.3")},
		{name: "mismatch", lookup: env("BEKUP_SETUP_VERSION", "1.2.4"), wantErr: true, log: "refusing to run"},
		{name: "unset warns", lookup: env(), log: "setup version unknown"},
		{name: "following without version", lookup: env("BEKUP_IMAGE_MODE", "following"), wantErr: true},
		{name: "following with version", lookup: env("BEKUP_SETUP_VERSION", "1.2.3", "BEKUP_IMAGE_MODE", "following"), log: "follows a tag"},
		{name: "malformed mode", lookup: env("BEKUP_SETUP_VERSION", "1.2.3", "BEKUP_IMAGE_MODE", "sometimes"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			g := Guard{Version: "1.2.3", Dev: tc.dev, LookupEnv: tc.lookup, Logger: slog.New(slog.NewTextHandler(&logs, nil))}
			err := CheckDeployment(context.Background(), g, "setup web")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err %v, want error %v", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "refusing to run setup web") {
				t.Fatalf("error %v does not name the command", err)
			}
			if tc.log != "" && !strings.Contains(logs.String(), tc.log) {
				t.Fatalf("log %q lacks %q", logs.String(), tc.log)
			}
		})
	}
}

func TestSplitErrors(t *testing.T) {
	t.Parallel()
	got := SplitErrors(errors.Join(errors.New("a"), errors.Join(errors.New("b"), errors.New("c"))))
	if strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("got %v", got)
	}
}
