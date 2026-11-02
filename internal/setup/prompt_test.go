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
		shared   bool // the database container is on the proxy's network (D-216)
	}{
		{"default", "\n", "", "", false},
		{"system", "verify-system\n", DBTLSVerifySystem, "", false},
		{"require confirmed", "require\nyes\n", DBTLSRequire, "ARP", false},
		{"disable declined then verify-system", "disable\n\nverify-system\n", DBTLSVerifySystem, "Prefer require over disable", false},
		{"disable confirmed", "disable\nyes\n", DBTLSDisable, "CAP_NET_RAW", false},
		{"require on the proxy network", "require\nyes\n", DBTLSRequire, "your reverse proxy's network", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term, out := promptTerminal(tc.in)
			got, err := askDBTLS(term, tc.shared)
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

// TestCompleteDBNetworkOnProxy: the line prompt applies the D-216 rule to
// the database network: the proxy's network in another case is refused,
// its exact name is taken, and the weak-TLS warning names the shared
// network.
func TestCompleteDBNetworkOnProxy(t *testing.T) {
	term, out := promptTerminal("WEB\nweb\npostgres\nrequire\nyes\n" + strings.Repeat("\n", 10))
	a := Answers{Variant: VariantOwnProxy, Domain: "backup.example.com", DockerGID: 1, ProxyNetwork: "web", ProxyContainer: "npm",
		DB: Database{Mode: DBExternal, Location: DBDocker}}
	g := Given{Image: true, Domain: true, DockerGID: true, Targets: true, TrustedProxy: true, Database: true, DBLocation: true,
		ProxyNetwork: true, ProxyContainer: true}
	if err := Complete(&a, g, "ghcr.io/jlentink/bekup:1.0.0@sha256:"+strings.Repeat("a", 64), term, false); err != nil {
		t.Fatalf("Complete: %v\n%s", err, out)
	}
	if a.DB.Network != "web" || !a.DBOnProxyNetwork() || !strings.Contains(out.String(), "only in case") ||
		!strings.Contains(out.String(), DBSharedProxyNetworkNote) {
		t.Fatalf("network %q\n%s", a.DB.Network, out)
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
