package targets

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
)

// TestDenyDebugLog: every refusal is logged at debug level as
// runner_target_denied with the reason and what decided it (the Docker
// network and subnet, the runner's own network, the deny_cidrs entry, the
// always-denied category, the bastion); nothing is logged above debug.
func TestDenyDebugLog(t *testing.T) {
	t.Parallel()
	g := newGuard(t, Options{
		AllowCIDRs:     []string{"10.20.0.0/16", "172.20.5.0/24"},
		AllowHosts:     []string{"bastion.example.com"},
		DenyCIDRs:      []string{"10.20.99.0/24"},
		DenyHosts:      []string{"evil.example.com"},
		BastionTargets: []string{"bastion.example.com=10.30.0.0/24"},
		OwnNetworks:    []netip.Prefix{netip.MustParsePrefix("10.20.250.0/24")},
	}, fakeDNS{"other-app-db-1": addrs("172.21.0.5")})
	g.SetDockerNetworkList([]DockerNetwork{
		{Name: "bekup_targets", Subnets: []netip.Prefix{netip.MustParsePrefix("172.21.0.0/16")}, Gateways: addrs("172.21.0.1")},
		{Name: "other_default", Subnets: []netip.Prefix{netip.MustParsePrefix("172.20.0.0/16")}},
	})
	var buf bytes.Buffer
	g.SetLogger(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	tests := []struct {
		name  string
		deny  func() error
		attrs map[string]any
	}{
		{"docker network by name", func() error { _, err := g.Resolve(t.Context(), "other-app-db-1"); return err },
			map[string]any{"reason": ReasonDockerNetwork, "address": "172.21.0.5", "host": "other-app-db-1", "docker_network": "bekup_targets", "docker_subnet": "172.21.0.0/16", "gateway": false}},
		{"docker gateway", func() error { return g.Check(netip.MustParseAddr("172.21.0.1"), "") },
			map[string]any{"reason": ReasonDockerNetwork, "docker_network": "bekup_targets", "docker_subnet": "172.21.0.1/32", "gateway": true}},
		{"docker subnet outside the allow entry", func() error { return g.Check(netip.MustParseAddr("172.20.9.9"), "") },
			map[string]any{"reason": ReasonDockerNetwork, "docker_network": "other_default", "docker_subnet": "172.20.0.0/16"}},
		{"own network", func() error { return g.Check(netip.MustParseAddr("10.20.250.7"), "") },
			map[string]any{"reason": ReasonOwnNetwork, "own_network": "10.20.250.0/24"}},
		{"deny_cidrs", func() error { return g.Check(netip.MustParseAddr("10.20.99.1"), "") },
			map[string]any{"reason": ReasonDenyCIDR, "deny_cidr": "10.20.99.0/24"}},
		{"deny_hosts", func() error { _, err := g.Resolve(t.Context(), "Evil.Example.com"); return err },
			map[string]any{"reason": ReasonDenyHost, "host": "evil.example.com"}},
		{"not allowed", func() error { return g.Check(netip.MustParseAddr("192.0.2.1"), "db.example.net") },
			map[string]any{"reason": ReasonNotAllowed, "address": "192.0.2.1", "host": "db.example.net"}},
		{"always denied", func() error { return g.Check(netip.MustParseAddr("127.0.0.1"), "") },
			map[string]any{"reason": "always denied: loopback", "category": "loopback"}},
		{"bastion target", func() error { return g.CheckBehindBastion("bastion.example.com", nil, "10.31.0.1") },
			map[string]any{"reason": ReasonBastionTarget, "bastion": "bastion.example.com", "address": "10.31.0.1"}},
	}
	for _, tc := range tests {
		buf.Reset()
		if err := tc.deny(); err == nil {
			t.Errorf("%s: allowed", tc.name)
			continue
		}
		lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
		if len(lines) != 1 {
			t.Errorf("%s: want one log line, got %q", tc.name, buf.String())
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
			t.Fatal(err)
		}
		if rec["msg"] != "runner_target_denied" || rec["level"] != "DEBUG" {
			t.Errorf("%s: %v", tc.name, rec)
		}
		for k, want := range tc.attrs {
			if rec[k] != want {
				t.Errorf("%s: %s = %v, want %v (%v)", tc.name, k, rec[k], want, rec)
			}
		}
	}

	// Allowed targets are not logged; above debug nothing is.
	buf.Reset()
	if err := g.Check(netip.MustParseAddr("10.20.1.1"), ""); err != nil || buf.Len() != 0 {
		t.Fatalf("allowed: %v %q", err, buf.String())
	}
	g.SetLogger(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err := g.Check(netip.MustParseAddr("192.0.2.1"), ""); err == nil || buf.Len() != 0 {
		t.Fatalf("info level: %v %q", err, buf.String())
	}
}
