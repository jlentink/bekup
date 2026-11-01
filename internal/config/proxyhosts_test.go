package config

import (
	"strings"
	"testing"
)

func TestTrustedProxyHostsValidation(t *testing.T) {
	base := func() Bekup {
		c := DefaultBekup()
		c.Server.BaseURL = "https://backup.example.com"
		c.Server.Listen = OwnProxyJoinListen
		c.Server.AllowWildcardListen = true
		return c
	}
	for _, tc := range []struct {
		name  string
		hosts []string
		want  string // "" = valid
	}{
		{"one name satisfies the trusted proxy requirement", []string{"npm"}, ""},
		{"compose names", []string{"nginx-proxy-manager-app-1", "traefik_1", "Caddy"}, ""},
		{"dotted name", []string{"proxy.v2"}, "without dots"},
		{"bekup service", []string{"serve"}, "bekup's own services"},
		{"bekup alias any case", []string{"Bekup-Serve"}, "bekup's own services"},
		{"bekup traefik", []string{"traefik"}, "bekup's own services"},
		{"none and no CIDRs", nil, "server.trusted_proxies: is required outside dev mode"},
		{"wildcard", []string{"npm-*"}, "wildcards are not allowed"},
		{"ip literal", []string{"10.0.0.5"}, "is an IP address"},
		{"ipv6 literal", []string{"::1"}, "is an IP address"},
		{"bad characters", []string{"npm proxy"}, "not a Docker container name"},
		{"leading dash", []string{"-npm"}, "not a Docker container name"},
		{"trailing dot", []string{"npm."}, "not a Docker container name"},
		{"empty label", []string{"a..b"}, "not a Docker container name"},
		{"too long", []string{strings.Repeat("a", 64)}, "not a Docker container name"},
		{"duplicate", []string{"npm", "NPM"}, "listed twice"},
		{"too many", []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9"}, "at most 8 names"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			c.Server.TrustedProxyHosts = tc.hosts
			errs := joinErrs(validateBekup(&c, false))
			if tc.want == "" {
				if errs != "" {
					t.Fatalf("unexpected errors: %s", errs)
				}
				return
			}
			if !strings.Contains(errs, tc.want) {
				t.Fatalf("want %q in:\n%s", tc.want, errs)
			}
		})
	}
}

func TestWildcardListenWarning(t *testing.T) {
	c := DefaultBekup()
	c.Server.Listen = OwnProxyJoinListen
	if w := c.WildcardListenWarning(); w != "" {
		t.Fatalf("no warning without allow_wildcard_listen, got %q", w)
	}
	c.Server.AllowWildcardListen = true
	w := c.WildcardListenWarning()
	if w == "" {
		t.Fatal("want the wildcard warning")
	}
	found := false
	for _, x := range c.Warnings() {
		found = found || x == w
	}
	if !found {
		t.Fatalf("Warnings() lacks %q", w)
	}
	c.Dev.Enabled = true
	if w := c.WildcardListenWarning(); w != "" {
		t.Fatalf("dev mode: no warning, got %q", w)
	}
	c.Dev.Enabled = false
	c.Server.Listen = "10.213.250.10:8080"
	if w := c.WildcardListenWarning(); w != "" {
		t.Fatalf("a fixed address: no warning, got %q", w)
	}
}

func FuzzValidContainerName(f *testing.F) {
	for _, s := range []string{"npm", "a_b", "-x", "a..b", "10.0.0.1", "x."} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if !ValidContainerName(s) {
			return
		}
		if s == "" || len(s) > 63 || strings.ContainsAny(s, " /\\'\"=@%[]:*\x00\n\r$") || strings.Contains(s, ".") || IsReservedContainerName(s) {
			t.Fatalf("accepted %q", s)
		}
	})
}
