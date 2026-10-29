package configreload

import (
	"net/netip"
	"slices"
	"strings"

	"github.com/jlentink/bekup/internal/config"
)

// weakenRule reports whether changing a live key from old to next weakens
// security. A nil rule means the key can never weaken it.
type weakenRule func(old, next *config.Bekup) bool

// weakenRules has one entry per live key (TestEveryLiveKeyHasRule). When
// in doubt a change counts as weakening (an alert to every admin).
var weakenRules = map[string]weakenRule{
	"log.level": nil,

	"server.trusted_proxies": func(o, n *config.Bekup) bool {
		return prefixesWidened(o.Server.TrustedProxies, n.Server.TrustedProxies)
	},
	"server.max_body_bytes":        func(o, n *config.Bekup) bool { return n.Server.MaxBodyBytes > o.Server.MaxBodyBytes },
	"server.rate_limit_per_second": func(o, n *config.Bekup) bool { return n.Server.RateLimitPerSecond > o.Server.RateLimitPerSecond },
	"server.rate_limit_burst":      func(o, n *config.Bekup) bool { return n.Server.RateLimitBurst > o.Server.RateLimitBurst },
	"server.user_rate_limit_burst": func(o, n *config.Bekup) bool { return n.Server.UserRateLimitBurst > o.Server.UserRateLimitBurst },
	"server.user_rate_limit_per_second": func(o, n *config.Bekup) bool {
		return n.Server.UserRateLimitPerSecond > o.Server.UserRateLimitPerSecond
	},

	"graphql.max_complexity": func(o, n *config.Bekup) bool { return n.GraphQL.MaxComplexity > o.GraphQL.MaxComplexity },
	"graphql.max_depth":      func(o, n *config.Bekup) bool { return n.GraphQL.MaxDepth > o.GraphQL.MaxDepth },
	"graphql.max_body_bytes": func(o, n *config.Bekup) bool { return n.GraphQL.MaxBodyBytes > o.GraphQL.MaxBodyBytes },

	"netguard.allow_cidrs": func(o, n *config.Bekup) bool {
		return entryAdded(o.Netguard.AllowCIDRs, n.Netguard.AllowCIDRs, canonicalCIDR)
	},
	"netguard.allow_hosts": func(o, n *config.Bekup) bool {
		return entryAdded(o.Netguard.AllowHosts, n.Netguard.AllowHosts, canonicalHost)
	},

	"security.login_rate_limit_per_minute": func(o, n *config.Bekup) bool {
		return n.Security.LoginRateLimitPerMinute > o.Security.LoginRateLimitPerMinute
	},
	"security.login_rate_limit_burst": func(o, n *config.Bekup) bool {
		return n.Security.LoginRateLimitBurst > o.Security.LoginRateLimitBurst
	},
	"security.lockout_threshold": func(o, n *config.Bekup) bool {
		return n.Security.LockoutThreshold > o.Security.LockoutThreshold
	},
	"security.lockout_base_duration": func(o, n *config.Bekup) bool {
		return n.Security.LockoutBaseDuration.D() < o.Security.LockoutBaseDuration.D()
	},
	"security.lockout_max_duration": func(o, n *config.Bekup) bool {
		return n.Security.LockoutMaxDuration.D() < o.Security.LockoutMaxDuration.D()
	},
	"security.password_min_length": func(o, n *config.Bekup) bool {
		return n.Security.PasswordMinLength < o.Security.PasswordMinLength
	},
	"security.password_min_score": func(o, n *config.Bekup) bool {
		return n.Security.PasswordMinScore < o.Security.PasswordMinScore
	},

	"notifications.max_per_user_per_hour": nil,
	"notifications.collapse_window":       nil,
	// A higher threshold keeps failing or refused webhooks (redirects,
	// blocked addresses, TLS errors) retrying longer before auto-disable.
	"notifications.webhook_failure_threshold": func(o, n *config.Bekup) bool {
		return n.Notifications.WebhookFailureThreshold > o.Notifications.WebhookFailureThreshold
	},

	"retention.dry_run": func(o, n *config.Bekup) bool { return o.Retention.DryRun && !n.Retention.DryRun },
	"retention.max_deletions_per_sweep": func(o, n *config.Bekup) bool {
		return n.Retention.MaxDeletionsPerSweep > o.Retention.MaxDeletionsPerSweep
	},
	"retention.partial_cleanup_age": func(o, n *config.Bekup) bool {
		return n.Retention.PartialCleanupAge.D() < o.Retention.PartialCleanupAge.D()
	},
}

// HasWeakenRule reports whether key has an entry in the rule table (nil
// rules included).
func HasWeakenRule(key string) bool {
	_, ok := weakenRules[key]
	return ok
}

// Weakened returns the keys of changed whose change from old to next
// weakens security, in the order of changed. A key without a rule counts
// as weakened (fail safe).
func Weakened(old, next *config.Bekup, changed []string) []string {
	out := []string{}
	for _, k := range changed {
		rule, ok := weakenRules[k]
		if !ok || (rule != nil && rule(old, next)) {
			out = append(out, k)
		}
	}
	return out
}

// prefixesWidened reports whether next contains an address range not
// covered by any prefix of old. Unparsable entries count as widening.
func prefixesWidened(old, next []string) bool {
	var have []netip.Prefix
	for _, s := range old {
		if p, err := netip.ParsePrefix(strings.TrimSpace(s)); err == nil {
			have = append(have, p.Masked())
		}
	}
	for _, s := range next {
		p, err := netip.ParsePrefix(strings.TrimSpace(s))
		if err != nil {
			return true
		}
		p = p.Masked()
		if !slices.ContainsFunc(have, func(h netip.Prefix) bool {
			return h.Addr().Is4() == p.Addr().Is4() && h.Bits() <= p.Bits() && h.Contains(p.Addr())
		}) {
			return true
		}
	}
	return false
}

// entryAdded reports whether next has an entry old lacks (after canon).
func entryAdded(old, next []string, canon func(string) string) bool {
	have := map[string]bool{}
	for _, s := range old {
		have[canon(s)] = true
	}
	for _, s := range next {
		if !have[canon(s)] {
			return true
		}
	}
	return false
}

func canonicalCIDR(s string) string {
	s = strings.TrimSpace(s)
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked().String()
	}
	return s
}

func canonicalHost(s string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
}
