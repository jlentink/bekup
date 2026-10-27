package executor

import (
	"encoding/binary"
	"fmt"
	"net/netip"

	"github.com/jlentink/bekup/internal/netguard"
)

// Address plan of a per-run network (D-132).
const (
	// pooledBits is the size of a per-run network taken from the pool.
	pooledBits = 28
	// aroundBits is the size of a per-run network built around an
	// IP-literal target: the smallest IPv4 subnet that holds the network
	// and broadcast addresses, the gateway, the proxy (= the target's
	// address) and the dump container.
	aroundBits = 29
	// Offsets inside a pooled /28.
	gatewayOffset = 1
	proxyOffset   = 2
	clientOffset  = 3
)

// runNet is the address plan of one per-run network.
type runNet struct {
	Subnet netip.Prefix
	// Gateway is reserved by Docker's IPAM; with inhibit_ipv4 it is never
	// assigned to the host's bridge, so it is unreachable.
	Gateway netip.Addr
	// Proxy is the runner's address on the network (the per-run proxy
	// listens here).
	Proxy netip.Addr
	// Client is the dump container's fixed address; the proxy accepts
	// connections from it only.
	Client netip.Addr
	// around is true when the subnet was built around an IP-literal target.
	around bool
	// index is the /28 index inside the pool (pooled networks only).
	index int
}

// subnetPool hands out /28 networks from docker.run_subnet_pool. It is not
// safe for concurrent use; the Executor serializes access.
type subnetPool struct {
	pool   netip.Prefix
	count  int
	used   map[int]bool
	cursor int
}

func newSubnetPool(p netip.Prefix) (*subnetPool, error) {
	p = p.Masked()
	if !p.Addr().Is4() || p.Bits() > pooledBits || p.Bits() < 8 {
		return nil, fmt.Errorf("executor: run subnet pool %s must be an IPv4 prefix between /8 and /%d", p, pooledBits)
	}
	return &subnetPool{pool: p, count: 1 << (pooledBits - p.Bits()), used: map[int]bool{}}, nil
}

// plan returns the address plan of the i-th /28.
func (s *subnetPool) plan(i int) runNet {
	base := s.pool.Addr().As4()
	n := binary.BigEndian.Uint32(base[:])
	n += uint32(i) << (32 - pooledBits) //nolint:gosec // i < count <= 2^20
	at := func(off uint32) netip.Addr {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], n+off)
		return netip.AddrFrom4(b)
	}
	return runNet{
		Subnet:  netip.PrefixFrom(at(0), pooledBits),
		Gateway: at(gatewayOffset),
		Proxy:   at(proxyOffset),
		Client:  at(clientOffset),
		index:   i,
	}
}

// acquire reserves the next free /28 that overlaps none of avoid (other
// Docker networks, subnets Docker refused). It starts after the last one
// handed out, so a just-released subnet is not reused at once.
func (s *subnetPool) acquire(avoid []netip.Prefix) (runNet, error) {
	for k := range s.count {
		i := (s.cursor + k) % s.count
		if s.used[i] {
			continue
		}
		rn := s.plan(i)
		if overlapsAny(rn.Subnet, avoid) {
			continue
		}
		s.used[i] = true
		s.cursor = (i + 1) % s.count
		return rn, nil
	}
	return runNet{}, ErrPoolExhausted
}

// release returns a pooled network.
func (s *subnetPool) release(rn runNet) {
	if !rn.around {
		delete(s.used, rn.index)
	}
}

// contains reports whether a is inside the pool.
func (s *subnetPool) contains(a netip.Addr) bool { return s.pool.Contains(a.Unmap()) }

// aroundTarget builds the /29 plan in which the runner's proxy holds the
// target's own address x, so a client that dials the IP literal reaches
// the proxy and verifies the server certificate against x (D-132). It
// fails closed when x cannot be represented safely: not IPv4, an
// always-denied range (loopback, link-local, ...), the network or
// broadcast address of its /29, or a /29 that overlaps forbidden (the
// pool, the runner's own networks, other Docker networks). A /29 that
// overlaps busy (other active runs' networks and upstream addresses) is
// ErrTargetBusy, which is transient.
func aroundTarget(x netip.Addr, forbidden, busy []netip.Prefix) (runNet, error) {
	x = x.WithZone("").Unmap()
	if !x.Is4() {
		return runNet{}, fmt.Errorf("%w: only IPv4 literals can be mapped", ErrUnmappableTarget)
	}
	if c := netguard.Classify(x); c.Blocked && c.Hard {
		return runNet{}, fmt.Errorf("%w: address range %s", ErrUnmappableTarget, c.Reason)
	}
	subnet := netip.PrefixFrom(x, aroundBits).Masked()
	first := subnet.Addr()
	b := first.As4()
	broadcast := netip.AddrFrom4([4]byte{b[0], b[1], b[2], b[3] | (1<<(32-aroundBits) - 1)})
	if x == first || x == broadcast {
		return runNet{}, fmt.Errorf("%w: network or broadcast address of its /%d", ErrUnmappableTarget, aroundBits)
	}
	if overlapsAny(subnet, forbidden) {
		return runNet{}, fmt.Errorf("%w: its /%d overlaps the runner's or Docker's networks", ErrUnmappableTarget, aroundBits)
	}
	if overlapsAny(subnet, busy) {
		return runNet{}, ErrTargetBusy
	}
	var free []netip.Addr
	for a := first.Next(); a != broadcast; a = a.Next() {
		if a != x {
			free = append(free, a)
		}
	}
	return runNet{Subnet: subnet, Gateway: free[0], Proxy: x, Client: free[1], around: true}, nil
}

func overlapsAny(p netip.Prefix, list []netip.Prefix) bool {
	for _, q := range list {
		if q.IsValid() && p.Overlaps(q) {
			return true
		}
	}
	return false
}
