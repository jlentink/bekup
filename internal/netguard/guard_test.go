package netguard

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/jlentink/bekup/internal/config"
)

func mustGuard(t *testing.T, opts Options) *Guard {
	t.Helper()
	g, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g
}

func TestAllowlistValidation(t *testing.T) {
	tests := []struct {
		name    string
		cidrs   []string
		hosts   []string
		dev     bool
		wantErr string
	}{
		{name: "empty"},
		{name: "private v4", cidrs: []string{"10.0.0.0/8", "192.168.1.10/32"}},
		{name: "mapped v4", cidrs: []string{"::ffff:10.0.0.0/104"}},
		{name: "ula", cidrs: []string{"fd00::/8"}},
		{name: "public v6", cidrs: []string{"2600::/12"}},
		{name: "hosts", hosts: []string{"minio.internal", "*.corp.example", "MinIO.Lan."}},
		{name: "dev loopback", cidrs: []string{"127.0.0.0/8", "::1/128"}, dev: true},
		{name: "slash zero v4", cidrs: []string{"0.0.0.0/0"}, wantErr: "every address"},
		{name: "slash zero v6", cidrs: []string{"::/0"}, wantErr: "every address"},
		{name: "garbage", cidrs: []string{"10.0.0.0"}, wantErr: "invalid CIDR"},
		{name: "loopback outside dev", cidrs: []string{"127.0.0.1/32"}, wantErr: "loopback"},
		{name: "v6 loopback outside dev", cidrs: []string{"::1/128"}, wantErr: "outside"},
		{name: "metadata", cidrs: []string{"169.254.169.254/32"}, wantErr: "always-denied"},
		{name: "link-local", cidrs: []string{"169.254.0.0/16"}, wantErr: "always-denied"},
		{name: "wide v4 covering hard", cidrs: []string{"0.0.0.0/1"}, dev: true, wantErr: "always-denied"},
		{name: "cgnat with metadata carve-out", cidrs: []string{"100.64.0.0/10", "fc00::/7"}},
		{name: "exact metadata v6", cidrs: []string{"fd00:ec2::254/128"}, wantErr: "always-denied"},
		{name: "wide covering metadata", cidrs: []string{"169.0.0.0/8"}, wantErr: "always-denied"},
		{name: "multicast", cidrs: []string{"224.0.0.0/4"}, wantErr: "always-denied"},
		{name: "whole mapped", cidrs: []string{"::ffff:0:0/96"}, wantErr: "IPv4-mapped"},
		{name: "mapped metadata", cidrs: []string{"::ffff:169.254.169.254/128"}, wantErr: "always-denied"},
		{name: "v6 link-local", cidrs: []string{"fe80::/10"}, wantErr: "outside"},
		{name: "teredo", cidrs: []string{"2001::/32"}, wantErr: "always-denied"},
		{name: "6to4", cidrs: []string{"2002::/16"}, wantErr: "6to4"},
		{name: "v6 covering mapped", cidrs: []string{"2000::/2"}, wantErr: "IPv4-mapped"},
		{name: "v6 reserved", cidrs: []string{"4000::/3"}, wantErr: "outside"},
		{name: "nat64 well-known", cidrs: []string{"64:ff9b::/96"}, wantErr: "outside"},
		{name: "global covering teredo", cidrs: []string{"2001::/16"}, wantErr: "always-denied"},
		{name: "zone", cidrs: []string{"fd00::%eth0/64"}, wantErr: "CIDR"},
		{name: "ip as host", hosts: []string{"10.0.0.1"}, wantErr: "IP address"},
		{name: "url as host", hosts: []string{"https://x.example"}, wantErr: "invalid host"},
		{name: "bare wildcard", hosts: []string{"*"}, wantErr: "invalid host"},
		{name: "tld wildcard", hosts: []string{"*.com"}, wantErr: "wildcard"},
		{name: "inner wildcard", hosts: []string{"a.*.example.com"}, wantErr: "invalid host"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(Options{AllowCIDRs: tc.cidrs, AllowHosts: tc.hosts, LoopbackAllowable: tc.dev})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	g := mustGuard(t, Options{
		AllowCIDRs: []string{"10.1.0.0/16", "fd00:1::/32", "100.64.0.0/10", "fd00:ec2::/32"},
		AllowHosts: []string{"minio.lan", "*.corp.example"},
	})
	tests := []struct {
		addr, host string
		allowed    bool
	}{
		{"8.8.8.8", "", true},
		{"10.1.2.3", "", true},
		{"::ffff:10.1.2.3", "", true},
		{"10.2.0.1", "", false},
		{"fd00:1::5", "", true},
		{"fd00:2::5", "", false},
		{"192.168.1.1", "minio.lan", true},
		{"192.168.1.1", "MINIO.LAN.", true},
		{"192.168.1.1", "db.corp.example", true},
		{"192.168.1.1", "corp.example", false},
		{"192.168.1.1", "evilcorp.example", false},
		{"192.168.1.1", "other.lan", false},
		// host entries never re-allow hard-denied addresses
		{"169.254.169.254", "minio.lan", false},
		{"127.0.0.1", "minio.lan", false},
		{"64:ff9b::a01:203", "minio.lan", false},
		// metadata carve-outs inside allowlisted ranges stay denied
		{"100.64.1.1", "", true},
		{"100.100.100.200", "", false},
		{"fd00:ec2::1", "", true},
		{"fd00:ec2::254", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.addr+"/"+tc.host, func(t *testing.T) {
			err := g.Check(netip.MustParseAddr(tc.addr), tc.host)
			if (err == nil) != tc.allowed {
				t.Fatalf("Check = %v, want allowed=%v", err, tc.allowed)
			}
			if err != nil {
				var be *BlockedAddressError
				if !errors.As(err, &be) || !errors.Is(err, ErrBlockedAddress) || be.Reason == "" {
					t.Fatalf("error %v is not a BlockedAddressError", err)
				}
			}
		})
	}
}

func TestSetAllowlist(t *testing.T) {
	g := mustGuard(t, Options{AllowCIDRs: []string{"10.0.0.0/8"}})
	priv := netip.MustParseAddr("192.168.5.5")
	if g.Check(priv, "") == nil {
		t.Fatal("192.168.5.5 allowed before SetAllowlist")
	}
	if err := g.SetAllowlist([]string{"192.168.0.0/16"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := g.Check(priv, ""); err != nil {
		t.Fatalf("runtime entry not applied: %v", err)
	}
	if err := g.Check(netip.MustParseAddr("10.0.0.1"), ""); err != nil {
		t.Fatalf("bootstrap entry lost: %v", err)
	}
	// An invalid list is rejected and the current one stays.
	if err := g.SetAllowlist([]string{"169.254.0.0/16"}, nil); err == nil {
		t.Fatal("SetAllowlist accepted link-local")
	}
	if err := g.Check(priv, ""); err != nil {
		t.Fatalf("runtime list replaced by an invalid one: %v", err)
	}
	if err := g.ValidateAllowlist(nil, []string{"*.x"}); err == nil {
		t.Fatal("ValidateAllowlist accepted *.x")
	}
	if err := g.SetAllowlist(nil, nil); err != nil {
		t.Fatal(err)
	}
	if g.Check(priv, "") == nil {
		t.Fatal("cleared runtime entry still applies")
	}
}

func TestSetAllowlistConcurrent(t *testing.T) {
	g := mustGuard(t, Options{})
	if err := g.SetAllowlist(nil, []string{"a.lan"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; ctx.Err() == nil && j < 500; j++ {
				if err := g.SetAllowlist([]string{fmt.Sprintf("10.%d.0.0/16", (i+j)%256)}, []string{"a.lan"}); err != nil {
					t.Error(err)
					return
				}
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 2000; j++ {
				if g.Check(netip.MustParseAddr("192.168.1.1"), "a.lan") != nil {
					t.Error("host entry lost during swap")
					return
				}
				if g.Check(netip.MustParseAddr("169.254.169.254"), "a.lan") == nil {
					t.Error("metadata allowed")
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestFromConfig(t *testing.T) {
	cfg := config.DefaultBekup()
	c := &cfg
	c.Dev.Enabled = false
	c.Netguard.AllowCIDRs = []string{"10.0.0.0/8"}
	c.Netguard.AllowHosts = []string{"*.lan.example"}
	g, err := FromConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	if g.loopbackSoft {
		t.Fatal("loopback allowable outside dev mode")
	}
	if g.Check(netip.MustParseAddr("192.168.1.1"), "nas.lan.example") != nil {
		t.Fatal("config host pattern not applied")
	}
	c.Netguard.AllowCIDRs = []string{"127.0.0.0/8"}
	if _, err := FromConfig(c); err == nil {
		t.Fatal("loopback CIDR accepted outside dev mode")
	}
	c.Dev.Enabled = true
	if _, err := FromConfig(c); err != nil {
		t.Fatalf("loopback CIDR refused in dev mode: %v", err)
	}
}
