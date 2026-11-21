package executor

import (
	"errors"
	"net/netip"
	"slices"
	"testing"

	"github.com/jlentink/bekup/internal/tunnel"
)

// errPoolOverlap is Docker's answer when another network took the subnet.
var errPoolOverlap = errors.New("Error response from daemon: invalid pool request: Pool overlaps with other one on this address space")

// takeSubnet emulates another Docker client creating a network in the
// requested subnet between the runner's network list and its create.
func takeSubnet(f *fakeDocker, req NetworkRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.foreign = append(f.foreign, NetworkInfo{ID: f.nextID("foreign"), Name: "other", Subnets: []netip.Prefix{req.Subnet}})
	return errPoolOverlap
}

func count(calls []string, op string) int {
	n := 0
	for _, c := range calls {
		if c == op {
			n++
		}
	}
	return n
}

// A subnet another client took after the network list is planned again:
// the run gets the pool's next /28 and succeeds.
func TestRunReplansTakenSubnet(t *testing.T) {
	fd := newFakeDocker()
	e := newTestExecutor(t, fd)
	var seen []netip.Prefix
	fd.onNetCreate = func(f *fakeDocker, req NetworkRequest) error {
		seen = append(seen, req.Subnet)
		if len(seen) == 1 {
			return takeSubnet(f, req)
		}
		return nil
	}
	var info RunInfo
	rs := baseSpec(t, &sink{})
	rs.OnStart = func(i RunInfo) { info = i }
	res, err := e.Run(t.Context(), rs)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("run: %+v %v", res, err)
	}
	want := []netip.Prefix{netip.MustParsePrefix("10.214.0.0/28"), netip.MustParsePrefix("10.214.0.16/28")}
	if !slices.Equal(seen, want) {
		t.Fatalf("subnets tried %v, want %v", seen, want)
	}
	// The container, its address and the alias follow the new plan.
	if info.Subnet != want[1] || info.ClientIP.String() != "10.214.0.19" ||
		!slices.Equal(fd.lastSpec.HostConfig.ExtraHosts, []string{dbHost + ":10.214.0.18"}) {
		t.Fatalf("run info %+v, extra hosts %v", info, fd.lastSpec.HostConfig.ExtraHosts)
	}
	checkReleased(t, e, fd)
}

// A subnet taken every time fails after networkAttempts plans, with
// nothing left behind.
func TestRunSubnetTakenEveryTime(t *testing.T) {
	fd := newFakeDocker()
	e := newTestExecutor(t, fd)
	fd.onNetCreate = takeSubnet
	_, err := e.Run(t.Context(), baseSpec(t, &sink{}))
	if !errors.Is(err, ErrDocker) {
		t.Fatalf("run: %v", err)
	}
	if n := count(fd.Calls(), "NetworkCreate"); n != networkAttempts {
		t.Fatalf("%d network creates, want %d", n, networkAttempts)
	}
	if indexOf(fd.Calls(), "ContainerCreate") >= 0 {
		t.Fatal("a container was created without a network")
	}
	checkReleased(t, e, fd)
}

// Other refusals are not retried.
func TestRunNetworkErrorNotRetried(t *testing.T) {
	fd := newFakeDocker()
	e := newTestExecutor(t, fd)
	fd.onNetCreate = func(*fakeDocker, NetworkRequest) error { return errors.New("permission denied") }
	if _, err := e.Run(t.Context(), baseSpec(t, &sink{})); !errors.Is(err, ErrDocker) {
		t.Fatalf("run: %v", err)
	}
	if n := count(fd.Calls(), "NetworkCreate"); n != 1 {
		t.Fatalf("%d network creates, want 1", n)
	}
	checkReleased(t, e, fd)
}

// A /29 around a target literal has no other subnet: never planned again.
func TestRunAroundSubnetNotReplanned(t *testing.T) {
	fd := newFakeDocker()
	e := newTestExecutor(t, fd)
	fd.onNetCreate = takeSubnet
	rs := baseSpec(t, &sink{})
	rs.Target.Host = "10.99.0.5"
	rs.Tunnel = &tunnel.Config{Host: "bastion.example.test", Port: 22, TargetHost: "10.99.0.5", TargetPort: 5432}
	if _, err := e.Run(t.Context(), rs); !errors.Is(err, ErrDocker) {
		t.Fatalf("run: %v", err)
	}
	if n := count(fd.Calls(), "NetworkCreate"); n != 1 {
		t.Fatalf("%d network creates, want 1", n)
	}
	checkReleased(t, e, fd)
}
