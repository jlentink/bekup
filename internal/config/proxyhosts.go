package config

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

// Own reverse proxy on this Docker host (D-215): serve joins the proxy's
// Docker network and trusts the proxy by its container name.

// MaxTrustedProxyHosts bounds server.trusted_proxy_hosts.
const MaxTrustedProxyHosts = 8

// OwnProxyJoinListen is the server.listen address of the own-proxy compose
// shape that joins the proxy's network (D-215): serve's address on that
// network is assigned by Docker, so it binds every interface of its
// container. The startup warning of allow_wildcard_listen is suppressed
// only for exactly this value in exactly that compose shape
// (deployguard.ListenShapeOwnProxyJoin).
const OwnProxyJoinListen = "0.0.0.0:8080"

// SystemCABundle is the system CA bundle of the bekup image (distroless,
// Debian's ca-certificates). setup writes it as database.tls_ca_file for
// the "verify with the system CA bundle" choice (D-215); the Dockerfile
// fails the build when the runtime image lacks it.
const SystemCABundle = "/etc/ssl/certs/ca-certificates.crt"

// containerNameRe is Docker's container name rule without dots (a dotted
// name could be taken for a DNS name outside Docker), bounded to one DNS
// label's length (Docker's embedded DNS resolves the name).
var containerNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$`)

// ReservedContainerNames are the service names and the network alias of
// bekup's own compose files (D-215): a trusted proxy container named like
// one of them would resolve to (or collide with) a bekup container on a
// shared network, so they are refused (case-insensitive, like Docker's
// DNS).
var ReservedContainerNames = []string{
	"serve", "runner", "migrate", "admin", "setup", "certs-init", "postgres",
	"bekup-serve", "worker", "traefik", "traefik-init",
}

// IsReservedContainerName reports whether s is one of
// ReservedContainerNames (case-insensitive).
func IsReservedContainerName(s string) bool {
	for _, r := range ReservedContainerNames {
		if strings.EqualFold(s, r) {
			return true
		}
	}
	return false
}

// ValidContainerName reports whether s is a Docker container name (or
// network alias) that serve can resolve and trust as a proxy (D-215):
// letters, digits, _ and -, at most 63 characters, starting with a letter
// or digit, no dots (so never a DNS or IP name) and none of bekup's own
// service names (ReservedContainerNames).
func ValidContainerName(s string) bool {
	return containerNameRe.MatchString(s) && !IsReservedContainerName(s)
}

// trustedProxyHosts checks server.trusted_proxy_hosts.
func (v *validator) trustedProxyHosts(path string, hosts []string) {
	if len(hosts) > MaxTrustedProxyHosts {
		v.addf(path, "at most %d names (got %d)", MaxTrustedProxyHosts, len(hosts))
	}
	seen := map[string]bool{}
	for _, h := range hosts {
		switch {
		case strings.Contains(h, "*"):
			v.addf(path, "%q: wildcards are not allowed (name each proxy container)", bounded(h))
		case isIPLiteral(h):
			v.addf(path, "%q is an IP address: put it into server.trusted_proxies as a /32 or /128", bounded(h))
		case IsReservedContainerName(h):
			v.addf(path, "%q is the name of one of bekup's own services; name your proxy's container", bounded(h))
		case !ValidContainerName(h):
			v.addf(path, "%q is not a Docker container name without dots (letters, digits, _ -; at most 63 characters)", bounded(h))
		case seen[strings.ToLower(h)]:
			v.addf(path, "%q is listed twice", bounded(h))
		}
		seen[strings.ToLower(h)] = true
	}
}

func isIPLiteral(s string) bool {
	_, err := netip.ParseAddr(strings.Trim(s, "[]"))
	return err == nil
}

// bounded shortens a value for an error message.
func bounded(s string) string {
	const n = 80
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// WildcardListenWarning is the startup warning for a wildcard
// server.listen allowed by server.allow_wildcard_listen ("" when there is
// none). serve drops exactly this warning in the own-proxy join shape
// (D-215).
func (c *Bekup) WildcardListenWarning() string { return c.wildcardListenWarning(c.Dev.Enabled) }

func (c *Bekup) wildcardListenWarning(dev bool) string {
	if dev || !c.Server.AllowWildcardListen || !isWildcardListen(c.Server.Listen) {
		return ""
	}
	return fmt.Sprintf("server.listen %q listens on every interface (server.allow_wildcard_listen = true); bind the internal network address instead", c.Server.Listen)
}
