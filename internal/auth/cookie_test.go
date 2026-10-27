package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/netip"
	"strings"
	"testing"
)

func TestCookiePolicyHTTPS(t *testing.T) {
	c := CookiePolicyFor("https://bekup.example.com")
	if !c.Secure || c.Name() != "__Host-bekup_session" {
		t.Fatalf("https policy = %+v %q", c, c.Name())
	}
	got := c.Session("tok").String()
	for _, want := range []string{"__Host-bekup_session=tok", "Path=/", "HttpOnly", "Secure", "SameSite=Strict"} {
		if !strings.Contains(got, want) {
			t.Errorf("cookie %q lacks %q", got, want)
		}
	}
	for _, bad := range []string{"Domain=", "Expires=", "Max-Age="} {
		if strings.Contains(got, bad) {
			t.Errorf("cookie %q must not carry %q", got, bad)
		}
	}
	cleared := c.Clear().String()
	if !strings.Contains(cleared, "__Host-bekup_session=;") || !strings.Contains(cleared, "Max-Age=0") || !strings.Contains(cleared, "Secure") {
		t.Errorf("clear cookie %q", cleared)
	}
}

func TestCookiePolicyHTTP(t *testing.T) {
	c := CookiePolicyFor("http://localhost:8080")
	if c.Secure || c.Name() != "bekup_session" {
		t.Fatalf("http policy = %+v %q", c, c.Name())
	}
	got := c.Session("tok").String()
	for _, want := range []string{"bekup_session=tok", "Path=/", "HttpOnly", "SameSite=Strict"} {
		if !strings.Contains(got, want) {
			t.Errorf("cookie %q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "Secure") || strings.Contains(got, "__Host-") {
		t.Errorf("http cookie %q must not be Secure/__Host-", got)
	}
	// Anything but plain http (including garbage) is treated as https.
	for _, u := range []string{"HTTPS://x", "::bad", "ftp://x"} {
		if !CookiePolicyFor(u).Secure {
			t.Errorf("CookiePolicyFor(%q) not secure", u)
		}
	}
}

func TestSessionToken(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		tok, hash := newSessionToken()
		if len(tok) != 43 || seen[tok] {
			t.Fatalf("bad or repeated token %q", tok)
		}
		seen[tok] = true
		raw, err := base64.RawURLEncoding.DecodeString(tok)
		if err != nil || len(raw) != 32 {
			t.Fatalf("token is not 256 bits: %v", err)
		}
		sum := sha256.Sum256(raw)
		if !bytes.Equal(sum[:], hash) {
			t.Fatal("stored hash is not sha256 of the token")
		}
		if bytes.Contains(hash, []byte(tok)) {
			t.Fatal("hash contains the token")
		}
		h2, ok := hashSessionToken(tok)
		if !ok || !bytes.Equal(h2, hash) {
			t.Fatal("hashSessionToken mismatch")
		}
	}
	for _, bad := range []string{"", "short", strings.Repeat("A", 42), strings.Repeat("A", 44), strings.Repeat("+", 43), strings.Repeat("A", 42) + "\xff"} {
		if _, ok := hashSessionToken(bad); ok {
			t.Errorf("hashSessionToken(%q) accepted", bad)
		}
	}
}

func TestRequestMeta(t *testing.T) {
	m := NewRequestMeta(netip.MustParseAddr("::ffff:192.0.2.1"), "Mozilla\x00/5.0\n"+strings.Repeat("x", 600))
	if m.IP != netip.MustParseAddr("192.0.2.1") {
		t.Fatalf("IP not unmapped: %v", m.IP)
	}
	if strings.ContainsAny(m.UserAgent, "\x00\n") || len([]rune(m.UserAgent)) != MaxUserAgentRunes {
		t.Fatalf("user agent not sanitized/truncated: %d", len(m.UserAgent))
	}
	ctx := WithRequestMeta(context.Background(), m)
	if RequestMetaFrom(ctx) != m || RequestMetaFrom(context.Background()) != (RequestMeta{}) {
		t.Fatal("RequestMeta context round trip")
	}
}

type recSetter struct{ set, cleared int }

func (r *recSetter) SetSessionCookie(string) { r.set++ }
func (r *recSetter) ClearSessionCookie()     { r.cleared++ }

func TestCookieSetterContext(t *testing.T) {
	if _, ok := CookieSetterFrom(context.Background()); ok {
		t.Fatal("setter in empty context")
	}
	r := &recSetter{}
	s, ok := CookieSetterFrom(WithCookieSetter(context.Background(), r))
	if !ok {
		t.Fatal("no setter")
	}
	s.SetSessionCookie("x")
	if r.set != 1 {
		t.Fatal("setter not used")
	}
}
