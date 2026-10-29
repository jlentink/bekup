package netguard

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestNormalizeAllowlist(t *testing.T) {
	tests := []struct {
		name      string
		dev       bool
		cidrs     []string
		hosts     []string
		wantCIDRs []string
		wantHosts []string
		wantErrs  []string // "list[index]:substring"
	}{
		{name: "empty", wantCIDRs: []string{}, wantHosts: []string{}},
		{
			name:      "canonical forms and duplicates",
			cidrs:     []string{"10.1.2.3/8", " 10.0.0.0/8", "::ffff:192.168.0.0/112", "fd00::1/8"},
			hosts:     []string{"MinIO.Lan.", "minio.lan", "*.Corp.Example"},
			wantCIDRs: []string{"10.0.0.0/8", "192.168.0.0/16", "fd00::/8"},
			wantHosts: []string{"minio.lan", "*.corp.example"},
		},
		{
			name:      "every bad entry reported with its index",
			cidrs:     []string{"10.0.0.0/8", "0.0.0.0/0", "169.254.0.0/16", "nonsense"},
			hosts:     []string{"ok.example", "10.0.0.1", "*.com", "http://x.example/"},
			wantCIDRs: []string{"10.0.0.0/8"},
			wantHosts: []string{"ok.example"},
			wantErrs: []string{
				"cidrs[1]:every address", "cidrs[2]:always-denied", "cidrs[3]:invalid CIDR",
				"hosts[1]:IP address", "hosts[2]:wildcard", "hosts[3]:invalid host name",
			},
		},
		{
			name:      "loopback only in dev mode",
			cidrs:     []string{"127.0.0.0/8"},
			wantCIDRs: []string{},
			wantHosts: []string{},
			wantErrs:  []string{"cidrs[0]:loopback"},
		},
		{name: "loopback dev", dev: true, cidrs: []string{"127.0.0.1/8", "::1/128"}, wantCIDRs: []string{"127.0.0.0/8", "::1/128"}, wantHosts: []string{}},
		{
			name: "metadata stays rejected", cidrs: []string{"169.254.169.254/32", "fd00:ec2::254/128"},
			wantCIDRs: []string{}, wantHosts: []string{},
			wantErrs: []string{"cidrs[0]:always-denied", "cidrs[1]:always-denied"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := mustGuard(t, Options{LoopbackAllowable: tt.dev})
			c, h, errs := g.NormalizeAllowlist(tt.cidrs, tt.hosts)
			if !slices.Equal(c, tt.wantCIDRs) || !slices.Equal(h, tt.wantHosts) {
				t.Fatalf("got cidrs %v hosts %v, want %v %v", c, h, tt.wantCIDRs, tt.wantHosts)
			}
			if len(errs) != len(tt.wantErrs) {
				t.Fatalf("got %d errors (%v), want %d", len(errs), errs, len(tt.wantErrs))
			}
			for i, want := range tt.wantErrs {
				loc, sub, _ := strings.Cut(want, ":")
				got := errs[i]
				if got.List+"["+strconv.Itoa(got.Index)+"]" != loc || !strings.Contains(got.Message, sub) {
					t.Errorf("error %d = %s %d %q, want %s containing %q", i, got.List, got.Index, got.Message, loc, sub)
				}
			}
			// The canonical lists are accepted as they are.
			if err := g.SetAllowlist(c, h); err != nil {
				t.Fatalf("SetAllowlist(normalized): %v", err)
			}
		})
	}
	if !mustGuard(t, Options{LoopbackAllowable: true}).LoopbackAllowable() || mustGuard(t, Options{}).LoopbackAllowable() {
		t.Fatal("LoopbackAllowable does not report the option")
	}
}
