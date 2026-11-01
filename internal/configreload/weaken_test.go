package configreload

import (
	"slices"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/config"
)

// TestEveryLiveKeyHasRule: a new live key needs a weakening rule (D-200).
func TestEveryLiveKeyHasRule(t *testing.T) {
	for _, k := range config.LiveKeys() {
		if !HasWeakenRule(k) {
			t.Errorf("live key %s has no weakening rule in weaken.go", k)
		}
	}
	for k := range weakenRules {
		if !config.IsLiveKey(k) {
			t.Errorf("weakening rule for %s, which is not a live key", k)
		}
	}
}

func TestWeakened(t *testing.T) {
	d := func(x time.Duration) config.Duration { return config.Duration(x) }
	tests := []struct {
		key    string
		mutate func(c *config.Bekup)
		want   bool
	}{
		{"log.level", func(c *config.Bekup) { c.Log.Level = "debug" }, false},
		{"server.trusted_proxies", func(c *config.Bekup) { c.Server.TrustedProxies = []string{"10.0.0.0/8", "192.168.0.0/16"} }, true},
		{"server.trusted_proxies", func(c *config.Bekup) { c.Server.TrustedProxies = []string{"10.1.0.0/16"} }, false},
		{"server.trusted_proxies", func(c *config.Bekup) { c.Server.TrustedProxies = []string{" 10.1.2.3/16 "} }, false},
		{"server.trusted_proxies", func(c *config.Bekup) { c.Server.TrustedProxies = []string{"::/0"} }, true},
		{"server.trusted_proxies", func(c *config.Bekup) { c.Server.TrustedProxies = []string{"garbage"} }, true},
		{"server.trusted_proxies", func(c *config.Bekup) { c.Server.TrustedProxies = nil }, false},
		{"server.trusted_proxy_hosts", func(c *config.Bekup) { c.Server.TrustedProxyHosts = []string{"npm"} }, true},
		{"server.trusted_proxy_hosts", func(c *config.Bekup) { c.Server.TrustedProxyHosts = nil }, false},
		{"server.max_body_bytes", func(c *config.Bekup) { c.Server.MaxBodyBytes++ }, true},
		{"server.max_body_bytes", func(c *config.Bekup) { c.Server.MaxBodyBytes-- }, false},
		{"server.rate_limit_per_second", func(c *config.Bekup) { c.Server.RateLimitPerSecond++ }, true},
		{"server.rate_limit_burst", func(c *config.Bekup) { c.Server.RateLimitBurst-- }, false},
		{"server.user_rate_limit_per_second", func(c *config.Bekup) { c.Server.UserRateLimitPerSecond *= 2 }, true},
		{"server.user_rate_limit_burst", func(c *config.Bekup) { c.Server.UserRateLimitBurst++ }, true},
		{"graphql.max_complexity", func(c *config.Bekup) { c.GraphQL.MaxComplexity++ }, true},
		{"graphql.max_depth", func(c *config.Bekup) { c.GraphQL.MaxDepth-- }, false},
		{"graphql.max_body_bytes", func(c *config.Bekup) { c.GraphQL.MaxBodyBytes++ }, true},
		{"netguard.allow_cidrs", func(c *config.Bekup) { c.Netguard.AllowCIDRs = []string{"10.0.0.0/8", "10.9.0.0/16"} }, true},
		{"netguard.allow_cidrs", func(c *config.Bekup) { c.Netguard.AllowCIDRs = []string{"10.0.0.1/8"} }, false},
		{"netguard.allow_cidrs", func(c *config.Bekup) { c.Netguard.AllowCIDRs = nil }, false},
		{"netguard.allow_hosts", func(c *config.Bekup) { c.Netguard.AllowHosts = []string{"S3.Internal.", "new.internal"} }, true},
		{"netguard.allow_hosts", func(c *config.Bekup) { c.Netguard.AllowHosts = []string{"S3.INTERNAL"} }, false},
		{"security.login_rate_limit_per_minute", func(c *config.Bekup) { c.Security.LoginRateLimitPerMinute++ }, true},
		{"security.login_rate_limit_burst", func(c *config.Bekup) { c.Security.LoginRateLimitBurst-- }, false},
		{"security.lockout_threshold", func(c *config.Bekup) { c.Security.LockoutThreshold++ }, true},
		{"security.lockout_base_duration", func(c *config.Bekup) { c.Security.LockoutBaseDuration = d(time.Second) }, true},
		{"security.lockout_max_duration", func(c *config.Bekup) { c.Security.LockoutMaxDuration = d(48 * time.Hour) }, false},
		{"security.password_min_length", func(c *config.Bekup) { c.Security.PasswordMinLength-- }, true},
		{"security.password_min_score", func(c *config.Bekup) { c.Security.PasswordMinScore++ }, false},
		{"notifications.max_per_user_per_hour", func(c *config.Bekup) { c.Notifications.MaxPerUserPerHour *= 10 }, false},
		{"notifications.collapse_window", func(c *config.Bekup) { c.Notifications.CollapseWindow = d(time.Second) }, false},
		{"notifications.webhook_failure_threshold", func(c *config.Bekup) { c.Notifications.WebhookFailureThreshold = 1000 }, true},
		{"notifications.webhook_failure_threshold", func(c *config.Bekup) { c.Notifications.WebhookFailureThreshold = 1 }, false},
		{"retention.dry_run", func(c *config.Bekup) { c.Retention.DryRun = false }, true},
		{"retention.max_deletions_per_sweep", func(c *config.Bekup) { c.Retention.MaxDeletionsPerSweep++ }, true},
		{"retention.partial_cleanup_age", func(c *config.Bekup) { c.Retention.PartialCleanupAge = d(time.Hour) }, true},
	}
	for _, tt := range tests {
		old := config.DefaultBekup()
		old.Server.TrustedProxies = []string{"10.0.0.0/8"}
		old.Netguard.AllowCIDRs = []string{"10.0.0.0/8"}
		old.Netguard.AllowHosts = []string{"s3.internal"}
		old.Retention.DryRun = true
		next := old.Clone()
		tt.mutate(next)
		got := Weakened(&old, next, []string{tt.key})
		if (len(got) == 1) != tt.want {
			t.Errorf("%s: weakened = %v, want %v", tt.key, got, tt.want)
		}
	}
	// A key without a rule counts as weakened (fail safe).
	old := config.DefaultBekup()
	if got := Weakened(&old, &old, []string{"server.base_url"}); !slices.Equal(got, []string{"server.base_url"}) {
		t.Fatalf("unknown key: %v", got)
	}
}

func TestTrustedProxyHostsWeakening(t *testing.T) {
	old := config.DefaultBekup()
	old.Server.TrustedProxyHosts = []string{"npm", "traefik"}
	for _, c := range []struct {
		next []string
		want bool
	}{
		{[]string{"NPM"}, false},
		{[]string{"traefik", "npm"}, false},
		{nil, false},
		{[]string{"npm", "caddy"}, true},
	} {
		next := old
		next.Server.TrustedProxyHosts = c.next
		got := Weakened(&old, &next, []string{"server.trusted_proxy_hosts"})
		if (len(got) == 1) != c.want {
			t.Errorf("%v -> %v: weakened = %v, want %v", old.Server.TrustedProxyHosts, c.next, got, c.want)
		}
	}
}
