package main

import (
	"bytes"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/deployguard"
)

// D-215: the wildcard warning is dropped only for exactly the own-proxy
// join shape with the proxy trusted by name only; every other warning
// stays, and the dropped one is returned for the reloader.
func TestDropExpectedWildcard(t *testing.T) {
	cfg := config.DefaultBekup()
	cfg.Server.Listen = config.OwnProxyJoinListen
	cfg.Server.AllowWildcardListen = true
	cfg.Server.TrustedProxies = nil
	cfg.Server.TrustedProxyHosts = []string{"npm"}
	w := cfg.WildcardListenWarning()
	in := []string{"other warning", w}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	join := deployguard.Deployment{OwnProxyJoin: true}

	got, dropped := dropExpectedWildcard(logger, &cfg, in, join)
	if !slices.Equal(got, []string{"other warning"}) || !slices.Equal(dropped, []string{w}) {
		t.Fatalf("join shape: %v / %v", got, dropped)
	}
	if !strings.Contains(buf.String(), "own-proxy join shape") || !strings.Contains(buf.String(), "only on its address on your proxy's network") {
		t.Fatalf("no INFO line: %s", buf.String())
	}
	keep := func(name string, c *config.Bekup, in []string, dep deployguard.Deployment) {
		t.Helper()
		if got, dropped := dropExpectedWildcard(logger, c, in, dep); !slices.Equal(got, in) || dropped != nil {
			t.Fatalf("%s: the warning stays: %v / %v", name, got, dropped)
		}
	}
	keep("without the shape", &cfg, in, deployguard.Deployment{})
	noHosts := cfg
	noHosts.Server.TrustedProxyHosts = nil
	noHosts.Server.TrustedProxies = []string{"10.0.0.0/8"}
	keep("without trusted_proxy_hosts", &noHosts, in, join)
	withCIDRs := cfg
	withCIDRs.Server.TrustedProxies = []string{"172.16.0.0/12"}
	keep("with trusted_proxies CIDRs", &withCIDRs, in, join)
	cfg.Server.Listen = ":8080"
	keep("another wildcard form", &cfg, []string{cfg.WildcardListenWarning()}, join)
}

func TestWithoutListenWarning(t *testing.T) {
	cfg := config.DefaultBekup()
	cfg.Server.Listen = config.OwnProxyJoinListen
	cfg.Server.AllowWildcardListen = true
	in := []string{"keep", cfg.WildcardListenWarning()}
	if got := withoutListenWarning(&cfg, in); !slices.Equal(got, []string{"keep"}) {
		t.Fatalf("got %v", got)
	}
	if kept, dropped := splitListenWarning(&cfg, in); !slices.Equal(kept, []string{"keep"}) || len(dropped) != 1 {
		t.Fatalf("split: %v / %v", kept, dropped)
	}
	cfg.Server.AllowWildcardListen = false
	if got := withoutListenWarning(&cfg, []string{"keep"}); !slices.Equal(got, []string{"keep"}) {
		t.Fatalf("got %v", got)
	}
}
