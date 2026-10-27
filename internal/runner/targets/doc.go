// Package targets implements the runner's target allowlist (D-029, D-098,
// D-130): which database servers and SSH bastions the runner's per-run
// proxies and tunnels may connect to.
//
// A connection to address A for host name H (empty for an IP literal) is
// decided in this order:
//
//  1. A is invalid, or always denied: every range internal/netguard
//     classifies as hard-denied (loopback, link-local incl. cloud metadata,
//     unspecified, multicast, reserved, Teredo, NAT64/6to4 forms embedding a
//     denied IPv4 address, ...). IPv4-mapped IPv6 addresses are unmapped
//     first. No configuration can re-allow these.
//  2. A is in one of the runner's own networks (SetOwnNetworks): the
//     control network and the per-run networks. Denied.
//  3. H is in deny_hosts, or A is in deny_cidrs: denied.
//  4. A is in allow_cidrs, or H is in allow_hosts: allowed.
//  5. Anything else is denied. An empty allowlist therefore denies every
//     target (the secure default; Warnings reports it at startup).
//
// The check runs when a request is validated (Resolve: every resolved
// address must pass) and again at dial time on the exact socket address
// (DialContext, through the dialer's Control hook), so DNS rebinding between
// validation and dial cannot reach a denied address.
package targets
