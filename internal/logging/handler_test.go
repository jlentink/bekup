package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func newTestLogger(t *testing.T, reg *Registry) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	l, err := New(&buf, Options{Level: "debug", Format: "json", Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	return l, &buf
}

func decode(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("invalid JSON %q: %v", buf.String(), err)
	}
	return m
}

func TestIsSecretKey(t *testing.T) {
	secret := []string{
		"password", "Password", "db_password", "new_password", "passphrase", "secret",
		"client_secret", "token", "access_token", "refreshToken", "api_key", "apiKey",
		"key", "private_key", "authorization", "Authorization", "cookie", "set_cookie",
		"dsn", "database_url", "db_url", "connection_string", "credentials", "totp",
		"x-api-key", "sessionToken", "userpassword",
		// Near misses of the non-secret identifiers stay secret.
		"version_private_key", "image_key_pem", "my_version_key", "key_version_key_secret",
		"ssh_key", "host_key", "signing_key", "host_key_cleared_value_key", "private_key_cleared",
	}
	notSecret := []string{
		"user", "job_id", "run_id", "master_key_file", "key_id", "token_ttl",
		"password_min_length", "level", "url", "base_url", "config_file", "duration",
		"bytes", "keyring_version", "status", "error", "",
		"version_key", "source_version_key", "versionKey", "image_key", "catalog_key", "VersionKey",
		"host_key_cleared", "hostKeyCleared", "host_key_changed",
	}
	for _, k := range secret {
		if !IsSecretKey(k) {
			t.Errorf("%q should be secret", k)
		}
	}
	for _, k := range notSecret {
		if IsSecretKey(k) {
			t.Errorf("%q should not be secret", k)
		}
	}
}

func TestHandlerRedactsSecretKeys(t *testing.T) {
	l, buf := newTestLogger(t, NewRegistry())
	l.Info("login attempt",
		slog.String("user", "alice"),
		slog.String("password", "hunter2"),
		slog.Group("request", slog.String("authorization", "Bearer abc"), slog.String("path", "/api")),
		slog.Any("token", []byte("raw-token")),
		slog.Int("api_key", 12345),
	)
	out := buf.String()
	for _, leak := range []string{"hunter2", "Bearer abc", "raw-token", "12345"} {
		if strings.Contains(out, leak) {
			t.Fatalf("leaked %q: %s", leak, out)
		}
	}
	m := decode(t, buf)
	if m["user"] != "alice" || m["password"] != Redacted || m["api_key"] != Redacted {
		t.Fatalf("unexpected: %v", m)
	}
	req, ok := m["request"].(map[string]any)
	if !ok || req["authorization"] != Redacted || req["path"] != "/api" {
		t.Fatalf("group not redacted correctly: %v", m)
	}
}

func TestHandlerRedactsURLCredentials(t *testing.T) {
	l, buf := newTestLogger(t, NewRegistry())
	l.Warn("connect failed",
		slog.String("target", "postgres://bekup:s3cr3t@db:5432/app"),
		slog.String("conninfo", "host=db user=bekup password=s3cr3t sslmode=verify-full"),
		slog.Any("err", errors.New("dial mysql://root:s3cr3t@10.0.0.1/: refused")),
	)
	out := buf.String()
	if strings.Contains(out, "s3cr3t") {
		t.Fatalf("leaked: %s", out)
	}
	m := decode(t, buf)
	if m["target"] != "postgres://bekup:"+Redacted+"@db:5432/app" {
		t.Fatalf("target = %v", m["target"])
	}
	if ci, ok := m["conninfo"].(string); !ok || !strings.Contains(ci, "password="+Redacted+" sslmode=verify-full") {
		t.Fatalf("conninfo = %v", m["conninfo"])
	}
}

type leaky struct{ Inner string }

func TestHandlerScrubsRegisteredSecrets(t *testing.T) {
	reg := NewRegistry()
	unregister := reg.Register([]byte("correct-horse-battery"))
	reg.Register([]byte("short")) // below MinSecretLen: ignored
	if reg.Len() != 1 {
		t.Fatalf("Len = %d", reg.Len())
	}
	l, buf := newTestLogger(t, reg)
	child := l.With(slog.String("context", "prefix correct-horse-battery suffix")).WithGroup("g")
	child.Info("value correct-horse-battery in message",
		slog.String("line", "pg_dump: correct-horse-battery"),
		slog.Any("err", errors.New("bad correct-horse-battery")),
		slog.Any("obj", leaky{Inner: "correct-horse-battery"}),
		slog.Any("raw", []byte("x correct-horse-battery")),
		slog.String("short", "short"),
	)
	out := buf.String()
	if strings.Contains(out, "correct-horse-battery") {
		t.Fatalf("leaked: %s", out)
	}
	if !strings.Contains(out, `"short":"short"`) {
		t.Fatalf("short value must not be scrubbed: %s", out)
	}

	// Attributes attached before registration are scrubbed too.
	reg2 := NewRegistry()
	l2, buf2 := newTestLogger(t, reg2)
	pre := l2.With(slog.String("line", "late-registered-secret"))
	reg2.Register([]byte("late-registered-secret"))
	pre.Info("event")
	if strings.Contains(buf2.String(), "late-registered-secret") {
		t.Fatalf("leaked pre-attached attr: %s", buf2.String())
	}

	unregister()
	unregister() // idempotent
	if reg.Len() != 0 {
		t.Fatalf("Len after unregister = %d", reg.Len())
	}
	buf.Reset()
	l.Info("event", slog.String("v", "correct-horse-battery"))
	if !strings.Contains(buf.String(), "correct-horse-battery") {
		t.Fatalf("value still scrubbed after unregister: %s", buf.String())
	}
}

func TestRegistryOverlappingSecrets(t *testing.T) {
	reg := NewRegistry()
	reg.Register([]byte("abcdef"))
	reg.Register([]byte("abcdefghij"))
	if got := reg.Scrub("x abcdefghij y"); got != "x "+Redacted+" y" {
		t.Fatalf("got %q", got)
	}
}

func TestRegisterCopiesSecret(t *testing.T) {
	reg := NewRegistry()
	s := []byte("zeroed-by-caller")
	reg.Register(s)
	clear(s)
	if got := reg.Scrub("zeroed-by-caller"); got != Redacted {
		t.Fatalf("registry must keep its own copy, got %q", got)
	}
}

func TestNewOptions(t *testing.T) {
	var buf bytes.Buffer
	l, err := New(&buf, Options{Level: "warn", Format: "text", Registry: NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	l.Info("hidden")
	l.Warn("shown", slog.String("secret", "x"))
	out := buf.String()
	if strings.Contains(out, "hidden") || !strings.Contains(out, "shown") || !strings.Contains(out, "secret="+Redacted) {
		t.Fatalf("unexpected text output: %s", out)
	}
	if _, err := New(&buf, Options{Level: "loud"}); err == nil {
		t.Fatal("bad level accepted")
	}
	if _, err := New(&buf, Options{Format: "xml"}); err == nil {
		t.Fatal("bad format accepted")
	}
	if !NewHandler(slog.NewJSONHandler(&buf, nil), nil).Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("Enabled should delegate")
	}
}

func FuzzRedactCredentials(f *testing.F) {
	for _, s := range []string{"postgres://u:p@h/db", "password=x", "a@b", "x://:@", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := RedactCredentials(s)
		if strings.Contains(s, "://u:topsecret@") && strings.Contains(out, "topsecret@") {
			t.Fatalf("leaked: %q", out)
		}
	})
}

func TestRedactCredentialsForms(t *testing.T) {
	tests := []struct {
		in    string
		want  string
		leaks []string
		keep  []string
	}{
		{
			in:    "postgres://u:p@ss@h/db",
			want:  "postgres://u:" + Redacted + "@h/db",
			leaks: []string{"p@ss", "ss@"},
		},
		{
			in:    "dial postgres://bekup:a@b:c/d@db.internal:5432/bekup?sslmode=verify-full failed",
			want:  "dial postgres://bekup:" + Redacted + "@db.internal:5432/bekup?sslmode=verify-full failed",
			leaks: []string{"a@b:c/d"},
		},
		{
			in:    `"url":"mysql://root:x@y@db/app","other":"a@b"`,
			leaks: []string{"x@y"},
			keep:  []string{"@db/app", `"other":"a@b"`},
		},
		{
			in:    "host=db user=bekup sslpassword=keypw sslkey=/run/secrets/k.pem sslcert=/run/secrets/c.pem passfile=/home/u/.pgpass sslmode=verify-full",
			leaks: []string{"keypw", "k.pem", "c.pem", ".pgpass"},
			keep:  []string{"host=db", "user=bekup", "sslmode=verify-full"},
		},
		{
			in:    "host=db password='it''s secret' user=x",
			leaks: []string{"secret"},
		},
		{
			in:    `host=db password='a\'b c' user=x`,
			leaks: []string{"b c"},
			keep:  []string{"user=x"},
		},
		{
			in:    "postgres://db/bekup?sslmode=verify-full&sslpassword=keypw&sslkey=/k.pem&sslcert=/c.pem&passfile=/p&password=pw1&application_name=bekup",
			leaks: []string{"keypw", "/k.pem", "/c.pem", "=/p&", "pw1"},
			keep:  []string{"sslmode=verify-full", "application_name=bekup"},
		},
		{
			in:    "client_secret=s1 access_token=t1 db_password=p1",
			leaks: []string{"s1", "t1", "p1"},
		},
	}
	for _, tc := range tests {
		got := RedactCredentials(tc.in)
		if tc.want != "" && got != tc.want {
			t.Errorf("RedactCredentials(%q) = %q, want %q", tc.in, got, tc.want)
		}
		for _, l := range tc.leaks {
			if strings.Contains(got, l) {
				t.Errorf("RedactCredentials(%q) = %q leaks %q", tc.in, got, l)
			}
		}
		for _, k := range tc.keep {
			if !strings.Contains(got, k) {
				t.Errorf("RedactCredentials(%q) = %q lost %q", tc.in, got, k)
			}
		}
	}
}
