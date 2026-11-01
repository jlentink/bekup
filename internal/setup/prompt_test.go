package setup

import (
	"bufio"
	"strings"
	"testing"
)

func promptTerminal(in string) (*Terminal, *strings.Builder) {
	out := &strings.Builder{}
	return &Terminal{In: bufio.NewReader(strings.NewReader(in)), Out: out, IsTerminal: true}, out
}

// TestAskDBTLSConfirmsWeakChoices: the plain prompt takes require and
// disable only after the warning and an explicit yes (D-215 review L2).
func TestAskDBTLSConfirmsWeakChoices(t *testing.T) {
	cases := []struct {
		name, in string
		want     DBTLS
		warn     string
	}{
		{"default", "\n", "", ""},
		{"system", "verify-system\n", DBTLSVerifySystem, ""},
		{"require confirmed", "require\nyes\n", DBTLSRequire, "ARP"},
		{"disable declined then verify-system", "disable\n\nverify-system\n", DBTLSVerifySystem, "Prefer require over disable"},
		{"disable confirmed", "disable\nyes\n", DBTLSDisable, "CAP_NET_RAW"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term, out := promptTerminal(tc.in)
			got, err := askDBTLS(term)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q\n%s", got, err, tc.want, out)
			}
			if tc.warn != "" && (!strings.Contains(out.String(), "EXPLICIT WEAKENING") || !strings.Contains(out.String(), tc.warn)) {
				t.Fatalf("no warning with %q:\n%s", tc.warn, out)
			}
			if tc.warn == "" && strings.Contains(out.String(), "WEAKENING") {
				t.Fatalf("unexpected warning:\n%s", out)
			}
		})
	}
	// Through Complete: an external database asked on the terminal.
	term, out := promptTerminal("db.example.com\nrequire\nno\nrequire\nyes\n\n")
	a := Answers{Variant: VariantTraefik, Domain: "backup.example.com", TLSMode: TLSACME, ACMEEmail: "a@example.com", DockerGID: 1, DB: Database{Mode: DBExternal, Location: DBRemote}}
	g := Given{Image: true, Domain: true, TLSMode: true, ACMEEmail: true, ACMEStaging: true, DockerGID: true, Targets: true, Database: true, DBLocation: true}
	if err := Complete(&a, g, "ghcr.io/jlentink/bekup:1.0.0@sha256:"+strings.Repeat("a", 64), term, false); err != nil {
		t.Fatalf("Complete: %v\n%s", err, out)
	}
	if a.DB.TLS != DBTLSRequire || strings.Count(out.String(), "EXPLICIT WEAKENING") != 2 {
		t.Fatalf("TLS %q\n%s", a.DB.TLS, out)
	}
}

// TestParseProxyFlag: --proxy accepts own, never the recorded form
// "external" (D-215 review nit).
func TestParseProxyFlag(t *testing.T) {
	if p, err := ParseProxy("own"); err != nil || p != ProxyExternal {
		t.Fatalf("own: %q %v", p, err)
	}
	if _, err := ParseProxy("external"); err == nil {
		t.Fatal("external accepted as a --proxy value")
	}
}
