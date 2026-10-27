package executor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"regexp"
	"slices"
	"strings"
)

// selfInfo is the runner's own container.
type selfInfo struct {
	ID       string
	Name     string
	Networks map[string]NetEndpoint
}

// containerMountRE finds the runner's container ID in /proc/self/mountinfo:
// Docker bind-mounts /etc/hostname, /etc/hosts and /etc/resolv.conf from
// .../containers/<id>/.
var containerMountRE = regexp.MustCompile(`/containers/([0-9a-f]{64})/(?:hostname|hosts|resolv\.conf)\s`)

// containerRefRE matches container IDs and names (the config check uses the
// same rule).
var containerRefRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// maxMountinfoBytes bounds the mountinfo read.
const maxMountinfoBytes = 1 << 20

// selfCandidates returns the container references to try, most reliable
// first: the configured docker.self_container, the container ID found in
// mountinfo, then the host name (Docker's default host name is the short
// container ID).
func selfCandidates(configured string, mountinfo io.Reader, hostname string) []string {
	if configured != "" {
		return []string{configured}
	}
	var out []string
	if mountinfo != nil {
		sc := bufio.NewScanner(io.LimitReader(mountinfo, maxMountinfoBytes))
		sc.Buffer(make([]byte, 0, 4096), 64<<10)
		for sc.Scan() {
			if m := containerMountRE.FindStringSubmatch(sc.Text() + " "); len(m) == 2 && !slices.Contains(out, m[1]) {
				out = append(out, m[1])
			}
		}
	}
	if h := strings.TrimSpace(hostname); h != "" && containerRefRE.MatchString(h) && !slices.Contains(out, h) {
		out = append(out, h)
	}
	return out
}

// detectSelf finds the runner's own container and proves it: one of the
// container's addresses must be an address of this process's network
// interfaces, so a name that happens to match another container is never
// joined to per-run networks by mistake.
func detectSelf(ctx context.Context, d Docker, candidates []string, local []netip.Addr) (selfInfo, error) {
	if len(candidates) == 0 {
		return selfInfo{}, errors.New("executor: cannot detect the runner's own container; set runner.toml docker.self_container")
	}
	var errs []error
	for _, ref := range candidates {
		if !containerRefRE.MatchString(ref) {
			continue
		}
		info, err := d.ContainerInspect(ctx, ref)
		if err != nil {
			errs = append(errs, fmt.Errorf("inspect %q: %w", ref, err))
			continue
		}
		for _, ep := range info.Networks {
			if ep.IP.IsValid() && slices.Contains(local, ep.IP.Unmap()) {
				return selfInfo{ID: info.ID, Name: info.Name, Networks: info.Networks}, nil
			}
		}
		errs = append(errs, fmt.Errorf("container %q has none of this process's addresses", ref))
	}
	return selfInfo{}, fmt.Errorf("executor: cannot detect the runner's own container (set runner.toml docker.self_container): %w", errors.Join(errs...))
}

// localAddrs returns the unicast addresses of this process's interfaces.
func localAddrs() ([]netip.Addr, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	out := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil {
			out = append(out, p.Addr().Unmap())
		}
	}
	return out, nil
}

// readMountinfo opens /proc/self/mountinfo (absent outside Linux).
func readMountinfo() (io.ReadCloser, error) { return os.Open("/proc/self/mountinfo") }
