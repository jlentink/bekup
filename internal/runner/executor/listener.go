package executor

import (
	"log/slog"
	"net"
	"net/netip"
)

// peerFilter closes accepted connections whose remote address deny
// rejects (or that has no IP address), before any byte is read. The runner
// wraps its gRPC listener with it to refuse connections from the per-run
// subnet pool and every active per-run network (D-132), also when the
// listener is bound to a wildcard address.
type peerFilter struct {
	net.Listener
	deny func(netip.Addr) bool
	log  *slog.Logger
}

// FilterListener wraps ln with a peer filter.
func FilterListener(ln net.Listener, deny func(netip.Addr) bool, log *slog.Logger) net.Listener {
	return &peerFilter{Listener: ln, deny: deny, log: log}
}

// Accept returns the next permitted connection.
func (f *peerFilter) Accept() (net.Conn, error) {
	for {
		c, err := f.Listener.Accept()
		if err != nil {
			return nil, err
		}
		ap, perr := netip.ParseAddrPort(c.RemoteAddr().String())
		if perr == nil && !f.deny(ap.Addr()) {
			return c, nil
		}
		f.log.Warn("runner refused grpc connection from a per-run network", slog.String("peer", c.RemoteAddr().String()))
		discardErr(c.Close())
	}
}
