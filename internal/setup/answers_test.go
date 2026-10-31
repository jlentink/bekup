package setup

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/jlentink/bekup/internal/imageref"
)

func TestAnswersValidate(t *testing.T) {
	tests := []struct {
		name  string
		mut   func(*Answers)
		proxy Proxy
		field string // "" = valid
	}{
		{name: "traefik acme", proxy: ProxyTraefik},
		{name: "nginx static", proxy: ProxyNginx, mut: func(a *Answers) { a.TLSMode, a.ACMEEmail = TLSStatic, "" }},
		{name: "none", proxy: ProxyNone},
		{name: "staging", proxy: ProxyTraefik, mut: func(a *Answers) { a.ACMEStaging = true }},
		{name: "no image", proxy: ProxyTraefik, mut: func(a *Answers) { a.Image = imageref.Ref{} }, field: "image"},
		{name: "unpinned third-party image", proxy: ProxyTraefik, mut: func(a *Answers) { a.Image = imageref.Ref{Repo: "registry.example.com/bekup", Tag: "1.2.3"} }, field: "image"},
		{name: "bad proxy", proxy: ProxyTraefik, mut: func(a *Answers) { a.Proxy = "caddy" }, field: "proxy"},
		{name: "domain without dot", proxy: ProxyTraefik, mut: func(a *Answers) { a.Domain = "localhost" }, field: "domain"},
		{name: "domain newline", proxy: ProxyTraefik, mut: func(a *Answers) { a.Domain = "a.example.com\nX=1" }, field: "domain"},
		{name: "domain dollar", proxy: ProxyTraefik, mut: func(a *Answers) { a.Domain = "${X}.example.com" }, field: "domain"},
		{name: "domain too long", proxy: ProxyTraefik, mut: func(a *Answers) { a.Domain = strings.Repeat("a.", 127) + "com" }, field: "domain"},
		{name: "email quote", proxy: ProxyTraefik, mut: func(a *Answers) { a.ACMEEmail = `o"ps@example.com` }, field: "acme-email"},
		{name: "email hash", proxy: ProxyTraefik, mut: func(a *Answers) { a.ACMEEmail = "ops#@example.com" }, field: "acme-email"},
		{name: "email missing", proxy: ProxyTraefik, mut: func(a *Answers) { a.ACMEEmail = "" }, field: "acme-email"},
		{name: "tls mode missing", proxy: ProxyTraefik, mut: func(a *Answers) { a.TLSMode = "" }, field: "tls-mode"},
		{name: "static with email", proxy: ProxyNginx, mut: func(a *Answers) { a.TLSMode = TLSStatic }, field: "acme-email"},
		{name: "none with tls", proxy: ProxyNone, mut: func(a *Answers) { a.TLSMode = TLSACME }, field: "tls-mode"},
		{name: "none without trusted", proxy: ProxyNone, mut: func(a *Answers) { a.TrustedProxies = nil }, field: "trusted-proxy"},
		{name: "trusted with bundled proxy", proxy: ProxyTraefik, mut: func(a *Answers) { a.TrustedProxies = []string{"10.0.0.1/32"} }, field: "trusted-proxy"},
		{name: "trusted host bits", proxy: ProxyNone, mut: func(a *Answers) { a.TrustedProxies = []string{"10.0.0.1/24"} }, field: "trusted-proxy"},
		{name: "trusted is bundled proxy address", proxy: ProxyNone, mut: func(a *Answers) { a.TrustedProxies = []string{"10.213.250.5/32"} }, field: "trusted-proxy"},
		{name: "trusted covers serve address", proxy: ProxyNone, mut: func(a *Answers) { a.TrustedProxies = []string{"10.213.250.8/29"} }, field: "trusted-proxy"},
		{name: "gid negative", proxy: ProxyTraefik, mut: func(a *Answers) { a.DockerGID = -1 }, field: "docker-gid"},
		{name: "cidr garbage", proxy: ProxyTraefik, mut: func(a *Answers) { a.TargetCIDRs = []string{"10.0.0.0/8\"]"} }, field: "target-cidr"},
		{name: "host is ip", proxy: ProxyTraefik, mut: func(a *Answers) { a.TargetHosts = []string{"10.0.0.1"} }, field: "target-host"},
		{name: "host quote", proxy: ProxyTraefik, mut: func(a *Answers) { a.TargetHosts = []string{`db"x`} }, field: "target-host"},
		{name: "keys dir absolute", proxy: ProxyTraefik, mut: func(a *Answers) { a.KeysDir = "/srv/keys" }, field: "keys-dir"},
		{name: "keys dir dotdot", proxy: ProxyTraefik, mut: func(a *Answers) { a.KeysDir = "a/../b" }, field: "keys-dir"},
		{name: "keys dir reserved", proxy: ProxyTraefik, mut: func(a *Answers) { a.KeysDir = "deploy/keys" }, field: "keys-dir"},
		{name: "keys dir nested ok", proxy: ProxyTraefik, mut: func(a *Answers) { a.KeysDir = "vault/bekup" }},
		{name: "ip outside subnet", proxy: ProxyTraefik, mut: func(a *Answers) { a.Network.ServeFrontendIP = "10.1.1.1" }, field: "serve-ip"},
		{name: "ip gateway", proxy: ProxyTraefik, mut: func(a *Answers) { a.Network.ProxyFrontendIP = "10.213.250.1" }, field: "proxy-ip"},
		{name: "subnet overlap", proxy: ProxyTraefik, mut: func(a *Answers) {
			a.Network.RunnerCtlSubnet, a.Network.RunnerCtlIP = "10.213.250.0/25", "10.213.250.20"
		}, field: "runner-ctl-subnet"},
		{name: "public subnet", proxy: ProxyTraefik, mut: func(a *Answers) {
			a.Network.MetricsSubnet, a.Network.ServeMetricsIP = "8.8.8.0/24", "8.8.8.10"
		}, field: "metrics-subnet"},
		{name: "custom network", proxy: ProxyTraefik, mut: func(a *Answers) {
			a.Network = Network{FrontendSubnet: "172.30.1.0/24", ServeFrontendIP: "172.30.1.10", ProxyFrontendIP: "172.30.1.5"}
		}},
		{name: "same serve and proxy ip", proxy: ProxyTraefik, mut: func(a *Answers) { a.Network.ProxyFrontendIP = "10.213.250.10" }, field: "proxy-ip"},
		// Variants (D-205).
		{name: "variant traefik", proxy: ProxyTraefik, mut: func(a *Answers) { a.Variant, a.Proxy = VariantTraefik, "" }},
		{name: "variant traefik with nginx", proxy: ProxyNginx, mut: func(a *Answers) { a.Variant = VariantTraefik }, field: "proxy"},
		{name: "bad variant", proxy: ProxyTraefik, mut: func(a *Answers) { a.Variant = "caddy" }, field: "variant"},
		{name: "npm", proxy: ProxyNone, mut: externalVariant(VariantNPM)},
		{name: "traefik-external", proxy: ProxyNone, mut: externalVariant(VariantTraefikExternal)},
		{name: "external with tls", proxy: ProxyNone, mut: func(a *Answers) { externalVariant(VariantNPM)(a); a.TLSMode = TLSACME }, field: "tls-mode"},
		{name: "external with trusted", proxy: ProxyNone, mut: func(a *Answers) { externalVariant(VariantNPM)(a); a.TrustedProxies = []string{"10.0.0.1/32"} }, field: "trusted-proxy"},
		{name: "external with --proxy none", proxy: ProxyNone, mut: func(a *Answers) { externalVariant(VariantNPM)(a); a.Proxy = ProxyNone }, field: "proxy"},
		{name: "external proxy in full", proxy: ProxyNone, mut: func(a *Answers) { a.Proxy, a.TrustedProxies = ProxyExternal, nil }, field: "proxy"},
		{name: "external frontend subnet", proxy: ProxyNone, mut: func(a *Answers) { externalVariant(VariantNPM)(a); a.Network.FrontendSubnet = "172.30.1.0/24" }, field: "frontend-subnet"},
		{name: "link on a bundled variant", proxy: ProxyTraefik, mut: func(a *Answers) { a.Link.Name = "x" }, field: "proxy-network"},
		{name: "link custom", proxy: ProxyNone, mut: func(a *Answers) {
			externalVariant(VariantNPM)(a)
			a.Link = LinkNetwork{Name: "my_proxy.net", Subnet: "192.168.77.0/29", ProxyIP: "192.168.77.2", ServeIP: "192.168.77.6"}
		}},
		{name: "link public subnet", proxy: ProxyNone, mut: func(a *Answers) {
			externalVariant(VariantNPM)(a)
			a.Link = LinkNetwork{Subnet: "8.8.8.0/28", ProxyIP: "8.8.8.5", ServeIP: "8.8.8.10"}
		}, field: "proxy-subnet"},
		{name: "link /30", proxy: ProxyNone, mut: func(a *Answers) {
			externalVariant(VariantNPM)(a)
			a.Link = LinkNetwork{Subnet: "10.9.9.0/30", ProxyIP: "10.9.9.2", ServeIP: "10.9.9.2"}
		}, field: "proxy-subnet"},
		{name: "link overlaps runner-ctl", proxy: ProxyNone, mut: func(a *Answers) {
			externalVariant(VariantNPM)(a)
			a.Link = LinkNetwork{Subnet: "10.213.251.0/28", ProxyIP: "10.213.251.5", ServeIP: "10.213.251.11"}
		}, field: "proxy-subnet"},
		{name: "link gateway", proxy: ProxyNone, mut: func(a *Answers) { externalVariant(VariantNPM)(a); a.Link.ProxyIP = "10.213.253.1" }, field: "proxy-ip"},
		{name: "link broadcast", proxy: ProxyNone, mut: func(a *Answers) { externalVariant(VariantNPM)(a); a.Link.ServeIP = "10.213.253.15" }, field: "serve-ip"},
		{name: "link outside", proxy: ProxyNone, mut: func(a *Answers) { externalVariant(VariantNPM)(a); a.Link.ServeIP = "10.213.253.20" }, field: "serve-ip"},
		{name: "link same", proxy: ProxyNone, mut: func(a *Answers) { externalVariant(VariantNPM)(a); a.Link.ServeIP = "10.213.253.5" }, field: "proxy-ip"},
		{name: "link name", proxy: ProxyNone, mut: func(a *Answers) { externalVariant(VariantNPM)(a); a.Link.Name = "-bad" }, field: "proxy-network"},
		{name: "entrypoint", proxy: ProxyNone, mut: func(a *Answers) { externalVariant(VariantTraefikExternal)(a); a.TraefikEntrypoint = "web secure" }, field: "traefik-entrypoint"},
		{name: "certresolver", proxy: ProxyNone, mut: func(a *Answers) { externalVariant(VariantTraefikExternal)(a); a.TraefikCertResolver = "le`x" }, field: "traefik-certresolver"},
		{name: "entrypoint on npm", proxy: ProxyNone, mut: func(a *Answers) { externalVariant(VariantNPM)(a); a.TraefikEntrypoint = "web" }, field: "traefik-entrypoint"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := testAnswers(t, tc.proxy)
			if tc.mut != nil {
				tc.mut(&a)
			}
			err := a.Validate()
			if tc.field == "" {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				if a.KeysDir == "" || a.Network.RunnerCtlIP == "" {
					t.Fatalf("defaults not filled: %+v", a)
				}
				return
			}
			var fe *FieldError
			if !errors.As(err, &fe) || fe.Field != tc.field {
				t.Fatalf("err = %v, want field %s", err, tc.field)
			}
		})
	}
}

// externalVariant turns proxy-none answers into an external variant.
func externalVariant(v Variant) func(*Answers) {
	return func(a *Answers) { a.Variant, a.Proxy, a.TrustedProxies = v, "", nil }
}

const forbidden = "\r\n\"'$#`\\ \t"

// FuzzAnswers feeds arbitrary strings into every free-text answer: an
// accepted answer never contains a character that could break out of .env,
// TOML or YAML, and the rendered files parse back to exactly the answers.
func FuzzAnswers(f *testing.F) {
	f.Add("backup.example.com", "ops@example.com", "10.20.0.0/16", "db.internal", "secrets", "10.213.250.5/32")
	f.Add("a.b\nX=1", "o\"@e.x", "10.0.0.0/8\"", "h$x", "../x", "1.2.3.4/32 #")
	f.Add("x.y", "a@b.c", "::/0", "a-b.c", "k/v", "fd00::/8")
	f.Fuzz(func(t *testing.T, domain, email, cidr, host, keys, trusted string) {
		for _, proxy := range []Proxy{ProxyTraefik, ProxyNone} {
			a := testAnswers(t, proxy)
			a.Domain, a.TargetCIDRs, a.TargetHosts, a.KeysDir = domain, []string{cidr}, []string{host}, keys
			if proxy == ProxyNone {
				a.TrustedProxies = []string{trusted}
			} else {
				a.ACMEEmail = email
			}
			if a.Validate() != nil {
				continue
			}
			for _, s := range []string{a.Domain, a.ACMEEmail, a.KeysDir, cidr, host, trusted} {
				if s == trusted && proxy != ProxyNone || s == a.ACMEEmail && proxy == ProxyNone {
					continue
				}
				if strings.ContainsAny(s, forbidden) {
					t.Fatalf("accepted %q", s)
				}
			}
			for _, v := range envVars(&a, "1.2.3") {
				line := v.Key + "=" + v.Value
				if strings.ContainsAny(v.Value, forbidden) || strings.Count(line, "=") < 1 {
					t.Fatalf(".env line %q", line)
				}
			}
			var b struct {
				Server struct {
					BaseURL        string   `toml:"base_url"`
					TrustedProxies []string `toml:"trusted_proxies"`
				} `toml:"server"`
			}
			if err := toml.Unmarshal(renderBekupTOML(&a), &b); err != nil {
				t.Fatalf("bekup.toml: %v", err)
			}
			if b.Server.BaseURL != "https://"+a.Domain || len(b.Server.TrustedProxies) != 1 {
				t.Fatalf("bekup.toml round trip: %+v", b)
			}
			var r struct {
				Targets struct {
					AllowCIDRs []string `toml:"allow_cidrs"`
					AllowHosts []string `toml:"allow_hosts"`
				} `toml:"targets"`
			}
			if err := toml.Unmarshal(renderRunnerTOML(&a), &r); err != nil {
				t.Fatalf("runner.toml: %v", err)
			}
			if len(r.Targets.AllowCIDRs) != 1 || r.Targets.AllowCIDRs[0] != cidr || r.Targets.AllowHosts[0] != host {
				t.Fatalf("runner.toml round trip: %+v", r)
			}
		}
	})
}

func TestLinkCreateCommand(t *testing.T) {
	for _, tc := range []struct {
		l    LinkNetwork
		want string
	}{
		{DefaultLinkNetwork(), "docker network create --internal --subnet 10.213.253.0/28 --gateway 10.213.253.1 --ip-range 10.213.253.12/30 bekup-proxy"},
		{LinkNetwork{Name: "edge-link", Subnet: "10.9.0.0/24", ProxyIP: "10.9.0.200", ServeIP: "10.9.0.20"}, "docker network create --internal --subnet 10.9.0.0/24 --gateway 10.9.0.1 --ip-range 10.9.0.128/26 edge-link"},
		{LinkNetwork{Name: "l", Subnet: "192.168.7.0/29", ProxyIP: "192.168.7.2", ServeIP: "192.168.7.3"}, "docker network create --internal --subnet 192.168.7.0/29 --gateway 192.168.7.1 --ip-range 192.168.7.4/30 l"},
		{LinkNetwork{Name: "l", Subnet: "192.168.7.0/29", ProxyIP: "192.168.7.2", ServeIP: "192.168.7.5"}, "docker network create --internal --subnet 192.168.7.0/29 --gateway 192.168.7.1 l"},
	} {
		got := tc.l.CreateCommand()
		if got != tc.want {
			t.Errorf("%+v:\n got %s\nwant %s", tc.l, got, tc.want)
		}
	}
	// The range never contains a fixed address, for every valid placement
	// in the default /28.
	p := netip.MustParsePrefix("10.213.253.0/28")
	for a := 2; a < 15; a++ {
		for b := 2; b < 15; b++ {
			if a == b {
				continue
			}
			pa, pb := fmt.Sprintf("10.213.253.%d", a), fmt.Sprintf("10.213.253.%d", b)
			if r, ok := linkIPRange(p, pa, pb); ok && (r.Contains(netip.MustParseAddr(pa)) || r.Contains(netip.MustParseAddr(pb)) || !p.Contains(r.Addr())) {
				t.Fatalf("%s %s: range %s", pa, pb, r)
			}
		}
	}
}
