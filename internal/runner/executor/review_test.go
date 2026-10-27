package executor

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/spec"
	"github.com/jlentink/bekup/internal/runner/targets"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/tunnel"
)

// TestRunCoversImageVolumes: the image's VOLUME paths reach the spec as
// read-only tmpfs mounts; an image with an unsupported VOLUME is refused
// before anything is created (PrePull too).
func TestRunCoversImageVolumes(t *testing.T) {
	fd := newFakeDocker()
	e := newTestExecutor(t, fd)
	ref := spec.ImageRef(testEntry(t))
	fd.volumes[ref] = []string{"/var/lib/postgresql/data"}
	if _, err := e.Run(t.Context(), baseSpec(t, &sink{})); err != nil {
		t.Fatal(err)
	}
	m := fd.lastSpec.HostConfig.Mounts
	if len(m) != 1 || m[0].Target != "/var/lib/postgresql/data" || !m[0].ReadOnly || m[0].Type != "tmpfs" {
		t.Fatalf("mounts %+v", m)
	}

	fd.volumes[ref] = []string{"relative/path"}
	before := len(fd.Calls())
	if _, err := e.Run(t.Context(), baseSpec(t, &sink{})); !errors.Is(err, ErrImage) {
		t.Fatalf("bad volume: %v", err)
	}
	if slices.Contains(fd.Calls()[before:], "ContainerCreate") {
		t.Fatal("container created for an image with a bad volume")
	}
	if present, err := e.PrePull(t.Context(), testEntry(t)); !present || !errors.Is(err, ErrImage) {
		t.Fatalf("prepull: %v %v", present, err)
	}
	checkReleased(t, e, fd)
}

// TestRunWritesStdinPrefix: a psql command's \restrict line follows the
// preamble directly and precedes the payload.
func TestRunWritesStdinPrefix(t *testing.T) {
	fd := newFakeDocker()
	var rest atomic.Pointer[[]byte]
	fd.proc = func(p *fakeProc) {
		p.readPreamble()
		b, err := p.readAll()
		if err != nil {
			b = append(b, []byte("<read error>")...)
		}
		rest.Store(&b)
		p.exit(0)
	}
	e := newTestExecutor(t, fd)
	rs := baseSpec(t, &sink{})
	ent := rs.Entry
	eng, err := engine.ForKind(engine.KindPostgres)
	if err != nil {
		t.Fatal(err)
	}
	var prefix []byte
	rs.Command = func(ep ClientEndpoint) (engine.Command, error) {
		c, err := eng.RestoreGlobals(ent, engine.Target{Host: ep.Host, Port: ep.Port, User: "backup", TLS: engine.TLSPinned})
		prefix = c.StdinPrefix
		return c, err
	}
	want := []byte("CREATE ROLE x;\n")
	rs.Stdin = bytes.NewReader(want)
	if _, err := e.Run(t.Context(), rs); err != nil {
		t.Fatal(err)
	}
	got := *rest.Load()
	key, payload, ok := bytes.Cut(got, []byte("\n"))
	if !ok || !engine.IsPSQLRestrictPrefix(append(bytes.Clone(key), '\n')) || !bytes.Equal(payload, want) {
		t.Fatalf("stdin after preamble %q", got)
	}
	if !bytes.Equal(prefix, make([]byte, len(prefix))) {
		t.Fatal("restrict prefix not zeroed after the run")
	}
	checkReleased(t, e, fd)
}

// TestInitRefusesOldDocker: daemons older than 26.0.0 are refused.
func TestInitRefusesOldDocker(t *testing.T) {
	fd := newFakeDocker()
	e := newTestExecutor(t, fd)
	fd.version = "25.0.5"
	e2, err := New(Options{Config: testConfig(), Docker: fd, Guard: testGuard(t), Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	e2.mountinfo, e2.hostname, e2.localAddrs = e.mountinfo, e.hostname, e.localAddrs
	err = e2.Init(t.Context())
	if !errors.Is(err, ErrNotReady) || !errors.Is(err, ErrDockerVersion) || e2.Ready() {
		t.Fatalf("old docker: %v", err)
	}
	if !strings.Contains(err.Error(), "26.0.0") {
		t.Fatalf("message %q", err)
	}
}

func TestCheckDockerVersion(t *testing.T) {
	for v, ok := range map[string]bool{
		"26.0.0": true, "26.1.4": true, "27.3.1": true, "29.8.2": true, "28.0.0-rc.1": true, "26.1.4+dfsg1": true,
		"100.0": true, "25.0.5": false, "23.0.11": false, "25.0.4": false, "20.10.24": false, "5.2.2": false,
		"": false, "dev": false, "26": false, "26.x.1": false, "1.2.3.4": false, "-26.0.0": false,
	} {
		if err := CheckDockerVersion(v); (err == nil) != ok {
			t.Errorf("%q: %v", v, err)
		}
	}
}

// TestStartupReapBeforeReady: no run is admitted while the startup reap
// runs, so the reap can never remove a fresh run's network.
func TestStartupReapBeforeReady(t *testing.T) {
	fd := newFakeDocker()
	e := newTestExecutor(t, fd)
	e2, err := New(Options{Config: testConfig(), Docker: fd, Guard: testGuard(t), Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	e2.mountinfo, e2.hostname, e2.localAddrs, e2.hostInfra = e.mountinfo, e.hostname, e.localAddrs, e.hostInfra
	var during []error
	fd.mu.Lock()
	fd.onList = func() {
		if e2.Ready() {
			during = append(during, errors.New("ready during the startup reap"))
		}
		if _, err := e2.Run(t.Context(), baseSpec(t, &sink{})); !errors.Is(err, ErrNotReady) {
			during = append(during, err)
		}
	}
	fd.mu.Unlock()
	if err := e2.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	fd.mu.Lock()
	fd.onList = nil
	fd.mu.Unlock()
	if len(during) != 0 || !e2.Ready() {
		t.Fatalf("during the reap: %v (ready %v)", during, e2.Ready())
	}
}

// TestDockerNetworksDenied: after Init the guard denies every Docker
// subnet and gateway unless an allow_cidrs entry inside the subnet (or
// the gateway's /32) allows it; allow_hosts never opens one.
func TestDockerNetworksDenied(t *testing.T) {
	fd := newFakeDocker()
	fd.foreign = []NetworkInfo{
		{Name: "bridge", Subnets: []netip.Prefix{netip.MustParsePrefix("10.50.200.0/24")}, Gateways: []netip.Addr{netip.MustParseAddr("10.50.200.1")}},
	}
	e := newTestExecutor(t, fd)
	for _, a := range []string{"10.50.200.7", "10.50.200.1"} {
		if err := e.guard.Check(netip.MustParseAddr(a), ""); !errors.Is(err, targets.ErrDenied) {
			t.Fatalf("%s allowed by the broad 10.50.0.0/16 entry: %v", a, err)
		}
	}
	if err := e.guard.Check(netip.MustParseAddr("10.50.1.7"), ""); err != nil {
		t.Fatalf("non-Docker address: %v", err)
	}
	// A dump to a host whose name resolves into the Docker network fails.
	rs := baseSpec(t, &sink{})
	rs.Target.Host = "10.50.200.7"
	rs.Target.VerifyHost = false
	if _, err := e.Run(t.Context(), rs); !errors.Is(err, targets.ErrDenied) {
		t.Fatalf("run into a docker network: %v", err)
	}
	checkReleased(t, e, fd)
}

// TestStepNames: steps of one run get their own network and container
// names, and the reaper treats them as the run's.
func TestStepNames(t *testing.T) {
	fd := newFakeDocker()
	e := newTestExecutor(t, fd)
	for _, step := range []string{"prepare", "swap1"} {
		rs := baseSpec(t, &sink{})
		rs.Step = step
		if _, err := e.Run(t.Context(), rs); err != nil {
			t.Fatal(err)
		}
		if want := NetworkPrefix + testRunID + "-" + step; fd.lastNetReq.Name != want || fd.lastCont.name != want {
			t.Fatalf("names %q %q, want %q", fd.lastNetReq.Name, fd.lastCont.name, want)
		}
	}
	rs := baseSpec(t, &sink{})
	rs.Step = "Bad-Step"
	if _, err := e.Run(t.Context(), rs); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("bad step: %v", err)
	}
	checkReleased(t, e, fd)
}

func TestHostInfraParsers(t *testing.T) {
	rc := "# comment\nnameserver 127.0.0.11\nnameserver 2001:db8::53\nnameserver bad\nsearch example.com\noptions ndots:0\n"
	if got := parseResolvConf(strings.NewReader(rc)); len(got) != 2 || got[0].String() != "127.0.0.11" || got[1].String() != "2001:db8::53" {
		t.Fatalf("resolv.conf %v", got)
	}
	route := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"eth0\t00000000\t011EA8C0\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
		"eth0\t001EA8C0\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n"
	if got := parseRouteV4(strings.NewReader(route)); len(got) != 1 || got[0].String() != "192.168.30.1" {
		t.Fatalf("route %v", got)
	}
	v6 := strings.Repeat("0", 32) + " 00 " + strings.Repeat("0", 32) + " 00 fe800000000000000000000000000001 00000400 00000001 00000000 00000003 eth0\n" +
		"20010db8000000000000000000000000 40 " + strings.Repeat("0", 32) + " 00 " + strings.Repeat("0", 32) + " 00000100 00000001 00000000 00000001 eth0\n"
	if got := parseRouteV6(strings.NewReader(v6)); len(got) != 1 || got[0].String() != "fe80::1" {
		t.Fatalf("ipv6_route %v", got)
	}
	for _, f := range []func(io.Reader) []netip.Addr{parseResolvConf, parseRouteV4, parseRouteV6} {
		if got := f(strings.NewReader("garbage\n\x00\n")); len(got) != 0 {
			t.Fatalf("garbage parsed: %v", got)
		}
	}
}

// TestStatusTunnelReasons: tunnel errors (B's *tunnel.Error wrapping the
// sshpolicy details) map to the normalized runnerpb reasons.
func TestStatusTunnelReasons(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want runnerpb.Reason
	}{
		{fmt.Errorf("run: %w", &tunnel.Error{Kind: tunnel.ErrHostKeyMismatch, Op: "dial"}), runnerpb.ReasonSSHHostKey},
		{&tunnel.Error{Kind: tunnel.ErrUnsupportedHostKey, Op: "dial"}, runnerpb.ReasonSSHHostKeyUnsupported},
		{&tunnel.Error{Kind: tunnel.ErrAuthFailed, Op: "dial"}, runnerpb.ReasonSSHAuthFailed},
	} {
		if got := runnerpb.ErrorReason(Status(tc.err)); got != tc.want {
			t.Errorf("%v: %q, want %q", tc.err, got, tc.want)
		}
	}
	if got := status.Code(Status(&tunnel.Error{Kind: tunnel.ErrUnreachable, Op: "dial"})); got != codes.Unavailable {
		t.Errorf("unreachable: %v", got)
	}
}
