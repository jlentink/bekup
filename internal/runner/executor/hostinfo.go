package executor

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

// MinDockerVersion is the oldest Docker Engine the executor accepts
// (D-138): 26.0.0 fixes CVE-2024-29018 (containers on an internal network
// could reach external DNS through the embedded resolver, a data path out
// of the per-run network) and every earlier advisory the executor's
// isolation relies on. Patched older lines are not detected reliably, so
// they are refused too.
var MinDockerVersion = [3]int{26, 0, 0}

// ErrDockerVersion is returned when the daemon is older than
// MinDockerVersion (or its version cannot be parsed).
var ErrDockerVersion = errors.New("docker engine is too old")

// CheckDockerVersion parses an engine version ("27.3.1",
// "28.0.0-rc.1", "26.1.4+dfsg1") and refuses one below MinDockerVersion
// or one it cannot parse.
func CheckDockerVersion(v string) error {
	minStr := fmt.Sprintf("%d.%d.%d", MinDockerVersion[0], MinDockerVersion[1], MinDockerVersion[2])
	got, ok := parseEngineVersion(v)
	if !ok {
		return fmt.Errorf("%w: cannot parse the engine version %q; Docker Engine %s or newer is required", ErrDockerVersion, truncateVersion(v), minStr)
	}
	for i := range got {
		if got[i] != MinDockerVersion[i] {
			if got[i] < MinDockerVersion[i] {
				return fmt.Errorf("%w: Docker Engine %s is older than the required %s (CVE-2024-29018 and earlier isolation fixes); upgrade the daemon", ErrDockerVersion, truncateVersion(v), minStr)
			}
			return nil
		}
	}
	return nil
}

func parseEngineVersion(v string) ([3]int, bool) {
	var out [3]int
	core := v
	if i := strings.IndexAny(core, "-+~ "); i >= 0 {
		core = core[:i]
	}
	parts := strings.Split(core, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		if p == "" || len(p) > 6 {
			return out, false
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func truncateVersion(v string) string {
	if len(v) > 40 {
		return v[:40]
	}
	return v
}

// hostInfraAddrs returns the name servers of /etc/resolv.conf and the
// default gateways of /proc/net/route and /proc/net/ipv6_route: the
// runner's own infrastructure, which a per-run /29 must never shadow
// (D-138). Missing files yield nothing.
func hostInfraAddrs() []netip.Addr {
	var out []netip.Addr
	if f, err := os.Open("/etc/resolv.conf"); err == nil {
		out = append(out, parseResolvConf(f)...)
		discardErr(f.Close())
	}
	if f, err := os.Open("/proc/net/route"); err == nil {
		out = append(out, parseRouteV4(f)...)
		discardErr(f.Close())
	}
	if f, err := os.Open("/proc/net/ipv6_route"); err == nil {
		out = append(out, parseRouteV6(f)...)
		discardErr(f.Close())
	}
	return out
}

// maxInfraLines bounds the lines read from each file.
const maxInfraLines = 4096

func scanLines(r io.Reader, f func(fields []string)) {
	sc := bufio.NewScanner(io.LimitReader(r, 1<<20))
	for n := 0; sc.Scan() && n < maxInfraLines; n++ {
		f(strings.Fields(sc.Text()))
	}
}

// parseResolvConf returns the "nameserver" addresses.
func parseResolvConf(r io.Reader) []netip.Addr {
	var out []netip.Addr
	scanLines(r, func(f []string) {
		if len(f) >= 2 && f[0] == "nameserver" {
			if a, err := netip.ParseAddr(f[1]); err == nil {
				out = append(out, a.WithZone("").Unmap())
			}
		}
	})
	return out
}

// parseRouteV4 returns the gateways of default routes (destination and
// mask 0) in /proc/net/route (little-endian hex).
func parseRouteV4(r io.Reader) []netip.Addr {
	var out []netip.Addr
	scanLines(r, func(f []string) {
		if len(f) < 8 || f[1] != "00000000" || f[7] != "00000000" {
			return
		}
		b, err := hex.DecodeString(f[2])
		if err != nil || len(b) != 4 {
			return
		}
		var a [4]byte
		binary.BigEndian.PutUint32(a[:], binary.LittleEndian.Uint32(b))
		if ip := netip.AddrFrom4(a); !ip.IsUnspecified() {
			out = append(out, ip)
		}
	})
	return out
}

// parseRouteV6 returns the next hops of default routes (::/0) in
// /proc/net/ipv6_route.
func parseRouteV6(r io.Reader) []netip.Addr {
	var out []netip.Addr
	scanLines(r, func(f []string) {
		if len(f) < 5 || f[0] != strings.Repeat("0", 32) || f[1] != "00" {
			return
		}
		b, err := hex.DecodeString(f[4])
		if err != nil || len(b) != 16 {
			return
		}
		if ip := netip.AddrFrom16([16]byte(b)); !ip.IsUnspecified() {
			out = append(out, ip)
		}
	})
	return out
}
