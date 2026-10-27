package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"syscall"
)

// DialContext resolves address ("host:port") and connects to the first
// allowed address. Every attempt is checked again in the dialer's Control
// hook on the exact socket address, so DNS rebinding between validation and
// dial cannot reach a denied address. Only tcp, tcp4 and tcp6 are
// supported. Its signature fits http.Transport.DialContext and most client
// libraries.
func (g *Guard) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	ipNet, ok := lookupNetwork(network)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedNetwork, network)
	}
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("netguard: invalid address: %w", err)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil || port == 0 {
		return nil, fmt.Errorf("netguard: invalid port %q", portStr)
	}

	ctx, cancel := context.WithTimeout(ctx, g.dialTimeout)
	defer cancel()

	addrs, name, err := g.resolve(ctx, ipNet, host)
	if err != nil {
		return nil, err
	}

	d := g.dialer(name)
	var firstErr error
	for _, a := range addrs {
		// Pre-check to skip denied addresses cheaply; Control is the
		// authoritative check.
		if err := g.Check(a, name); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		conn, err := d.DialContext(ctx, network, netip.AddrPortFrom(a.Unmap(), uint16(port)).String())
		if err == nil {
			return conn, nil
		}
		if firstErr == nil || (errors.Is(firstErr, ErrBlockedAddress) && !errors.Is(err, ErrBlockedAddress)) {
			firstErr = err
		}
		if ctx.Err() != nil {
			break
		}
	}
	return nil, firstErr
}

// Dialer returns a net.Dialer whose Control hook enforces the guard on
// every socket it opens. It does not know the requested host name, so only
// the CIDR allowlist (not host entries) can re-allow soft-denied
// addresses. Prefer DialContext where a dial function is accepted.
func (g *Guard) Dialer() *net.Dialer {
	return g.dialer("")
}

func (g *Guard) dialer(host string) *net.Dialer {
	return &net.Dialer{
		Timeout: g.dialTimeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return fmt.Errorf("%w: unparsable socket address", ErrBlockedAddress)
			}
			return g.Check(ap.Addr(), host)
		},
	}
}

// ResolveAndCheck resolves host (a name or IP literal, optionally in
// brackets) and returns its addresses when every one of them is allowed.
// Use it to validate input when it is entered; the dial-time check still
// applies later.
func (g *Guard) ResolveAndCheck(ctx context.Context, host string) ([]netip.Addr, error) {
	ctx, cancel := context.WithTimeout(ctx, g.dialTimeout)
	defer cancel()
	addrs, name, err := g.resolve(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	for _, a := range addrs {
		if err := g.Check(a, name); err != nil {
			return nil, err
		}
	}
	return addrs, nil
}

// resolve returns the addresses for host and the normalized host name for
// allowlist matching ("" when host is an IP literal).
func (g *Guard) resolve(ctx context.Context, ipNet, host string) ([]netip.Addr, string, error) {
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if host == "" {
		return nil, "", errors.New("netguard: empty host")
	}
	if a, err := netip.ParseAddr(host); err == nil {
		a = a.WithZone("")
		if (ipNet == "ip4" && !a.Unmap().Is4()) || (ipNet == "ip6" && a.Is4()) {
			return nil, "", ErrNoAddresses
		}
		return []netip.Addr{a}, "", nil
	}
	name := normalizeHost(host)
	if !validHostName(name) {
		return nil, "", errors.New("netguard: invalid host name")
	}
	addrs, err := g.lookup(ctx, ipNet, name)
	if err != nil {
		return nil, "", fmt.Errorf("netguard: resolve host: %w", err)
	}
	if len(addrs) == 0 {
		return nil, "", ErrNoAddresses
	}
	if len(addrs) > maxAddrs {
		addrs = addrs[:maxAddrs]
	}
	return addrs, name, nil
}

func lookupNetwork(network string) (string, bool) {
	switch network {
	case "tcp":
		return "ip", true
	case "tcp4":
		return "ip4", true
	case "tcp6":
		return "ip6", true
	default:
		return "", false
	}
}
