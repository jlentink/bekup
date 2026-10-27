package executor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/runner/spec"
	"github.com/jlentink/bekup/internal/runner/targets"
	"github.com/jlentink/bekup/internal/tunnel"
)

func TestSubnetPool(t *testing.T) {
	p, err := newSubnetPool(netip.MustParsePrefix("10.214.0.0/26"))
	if err != nil {
		t.Fatal(err)
	}
	if p.count != 4 {
		t.Fatalf("count %d", p.count)
	}
	rn := p.plan(2)
	if rn.Subnet.String() != "10.214.0.32/28" || rn.Gateway.String() != "10.214.0.33" ||
		rn.Proxy.String() != "10.214.0.34" || rn.Client.String() != "10.214.0.35" {
		t.Fatalf("plan %+v", rn)
	}
	avoid := []netip.Prefix{netip.MustParsePrefix("10.214.0.0/27")}
	a, err := p.acquire(avoid)
	if err != nil || a.index != 2 {
		t.Fatalf("acquire %+v %v", a, err)
	}
	b, err := p.acquire(avoid)
	if err != nil || b.index != 3 {
		t.Fatalf("acquire %+v %v", b, err)
	}
	if _, err := p.acquire(avoid); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("exhausted: %v", err)
	}
	p.release(a)
	c, err := p.acquire(avoid)
	if err != nil || c.index != 2 {
		t.Fatalf("reuse %+v %v", c, err)
	}
	for _, bad := range []string{"10.0.0.0/29", "fd00::/64", "10.0.0.0/7"} {
		if _, err := newSubnetPool(netip.MustParsePrefix(bad)); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestAroundTarget(t *testing.T) {
	rn, err := aroundTarget(netip.MustParseAddr("10.99.0.5"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rn.Subnet.String() != "10.99.0.0/29" || rn.Proxy.String() != "10.99.0.5" ||
		rn.Gateway.String() != "10.99.0.1" || rn.Client.String() != "10.99.0.2" || !rn.around {
		t.Fatalf("plan %+v", rn)
	}
	rn, err = aroundTarget(netip.MustParseAddr("10.99.0.1"), nil, nil)
	if err != nil || rn.Gateway.String() != "10.99.0.2" || rn.Client.String() != "10.99.0.3" {
		t.Fatalf("first host: %+v %v", rn, err)
	}
	if rn, err := aroundTarget(netip.MustParseAddr("::ffff:192.168.7.6"), nil, nil); err != nil || rn.Proxy.String() != "192.168.7.6" {
		t.Fatalf("mapped: %+v %v", rn, err)
	}
	for _, bad := range []string{"10.99.0.0", "10.99.0.7", "127.0.0.1", "169.254.169.254", "fd00::5", "0.0.0.0", "224.0.0.1"} {
		if _, err := aroundTarget(netip.MustParseAddr(bad), nil, nil); !errors.Is(err, ErrUnmappableTarget) {
			t.Errorf("%s: %v", bad, err)
		}
	}
	x := netip.MustParseAddr("10.99.0.5")
	if _, err := aroundTarget(x, []netip.Prefix{netip.MustParsePrefix("10.99.0.0/16")}, nil); !errors.Is(err, ErrUnmappableTarget) {
		t.Fatalf("forbidden: %v", err)
	}
	if _, err := aroundTarget(x, nil, []netip.Prefix{netip.MustParsePrefix("10.99.0.6/32")}); !errors.Is(err, ErrTargetBusy) {
		t.Fatalf("busy: %v", err)
	}
}

func TestLineLogger(t *testing.T) {
	reg := logging.NewRegistry()
	defer reg.Register([]byte("hunter2-secret-value"))()
	var lines []string
	l := newLineLogger(func(s string) { lines = append(lines, s) }, reg, 4, 256, len("hunter2-secret-value"))
	long := strings.Repeat("x", 250) + "hunter2-secret-value" + strings.Repeat("y", 10_000)
	for _, chunk := range []string{"a\x07b\n", "pass", "word=abc def\nhun", "ter2-secret-value\n", long + "\n", "dropped\n", "dropped2"} {
		if _, err := l.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	l.Flush()
	if len(lines) != 4 {
		t.Fatalf("lines %q", lines)
	}
	if lines[0] != "a?b" {
		t.Fatalf("control chars: %q", lines[0])
	}
	if lines[1] != "password=[REDACTED] def" {
		t.Fatalf("credentials: %q", lines[1])
	}
	// A secret split across writes is redacted; one that crosses the
	// 256-byte limit of a long line is redacted before truncation.
	if lines[2] != "[REDACTED]" || strings.Contains(strings.Join(lines, ""), "hunter2") {
		t.Fatalf("secret leaked: %q", lines)
	}
	if !strings.HasSuffix(lines[3], " [truncated]") || len(lines[3]) > 256+len(" [truncated]") ||
		!strings.Contains(lines[3], "[REDACTED]"[:6]) {
		t.Fatalf("truncation: %d %q", len(lines[3]), lines[3])
	}
	if l.stats.Dropped != 2 || l.stats.Truncated != 1 || l.stats.Lines != 4 {
		t.Fatalf("stats %+v", l.stats)
	}
	if got := sanitizeLine([]byte("a\xffb c\tq\r")); got != "a�b?c\tq" {
		t.Fatalf("sanitize %q", got)
	}
}

func TestChunkWriter(t *testing.T) {
	var sizes []int
	fin := &atomic.Bool{}
	w := newChunkWriter(10, func(b []byte) error { sizes = append(sizes, len(b)); return nil }, fin)
	if _, err := w.Write([]byte(strings.Repeat("a", 25))); err != nil {
		t.Fatal(err)
	}
	if err := w.flush(); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(sizes) != "[10 10 5]" || w.total != 25 {
		t.Fatalf("sizes %v total %d", sizes, w.total)
	}
	fin.Store(true)
	if _, err := w.Write([]byte(strings.Repeat("b", 10))); !errors.Is(err, errFinished) {
		t.Fatalf("after finish: %v", err)
	}
	w2 := newChunkWriter(0, func([]byte) error { return errors.New("gone") }, &atomic.Bool{})
	if cap(w2.buf) != DefaultChunkBytes {
		t.Fatalf("default size %d", cap(w2.buf))
	}
	if _, err := w2.Write(make([]byte, DefaultChunkBytes)); !errors.Is(err, ErrOutput) {
		t.Fatalf("emit error: %v", err)
	}
}

// echoUpstream accepts one connection and echoes with a prefix.
func echoUpstream(t *testing.T) (dialFunc, func()) {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { discardErr(c.Close()) }()
				line, err := bufio.NewReader(c).ReadString('\n')
				if err != nil {
					return
				}
				ignore2(io.WriteString(c, "upstream:"+line))
			}()
		}
	}()
	var d net.Dialer
	return func(ctx context.Context) (net.Conn, error) { return d.DialContext(ctx, "tcp4", ln.Addr().String()) },
		func() { discardErr(ln.Close()); <-done }
}

func TestProxyForwardsOnlyTheAllowedPeer(t *testing.T) {
	dial, stop := echoUpstream(t)
	defer stop()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := startProxy(t.Context(), ln, netip.MustParseAddr("127.0.0.1"), dial, discardLogger())
	defer p.Close()

	var d net.Dialer
	c, err := d.DialContext(t.Context(), "tcp4", p.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ignore2(io.WriteString(c, "hello\n"))
	got, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	discardErr(c.Close())
	if got != "upstream:hello\n" {
		t.Fatalf("got %q", got)
	}

	// A proxy that only allows another address closes the connection
	// without dialing upstream.
	ln2, err := lc.Listen(t.Context(), "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dialed := atomic.Bool{}
	p2 := startProxy(t.Context(), ln2, netip.MustParseAddr("10.214.0.3"), func(ctx context.Context) (net.Conn, error) {
		dialed.Store(true)
		return dial(ctx)
	}, discardLogger())
	c2, err := d.DialContext(t.Context(), "tcp4", p2.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	discardErr(c2.SetReadDeadline(time.Now().Add(2 * time.Second)))
	if n, err := c2.Read(make([]byte, 1)); n != 0 || err == nil {
		t.Fatalf("rejected peer read %d %v", n, err)
	}
	discardErr(c2.Close())
	p2.Close()
	p2.Close() // idempotent
	if dialed.Load() || p2.Rejected() != 1 {
		t.Fatalf("dialed=%t rejected=%d", dialed.Load(), p2.Rejected())
	}
}

func TestProxyCloseEndsConnections(t *testing.T) {
	upLn, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { discardErr(upLn.Close()) }()
	go func() {
		c, err := upLn.Accept()
		if err == nil {
			defer func() { discardErr(c.Close()) }()
			ignore2(io.Copy(io.Discard, c)) // hold the connection open
		}
	}()
	var d net.Dialer
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := startProxy(t.Context(), ln, netip.MustParseAddr("127.0.0.1"), func(ctx context.Context) (net.Conn, error) {
		return d.DialContext(ctx, "tcp4", upLn.Addr().String())
	}, discardLogger())
	c, err := d.DialContext(t.Context(), "tcp4", p.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { discardErr(c.Close()) }()
	ignore2(c.Write([]byte("x")))
	time.Sleep(50 * time.Millisecond)
	closed := make(chan struct{})
	go func() { p.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung on an open connection")
	}
}

func TestListenRetryGivesUp(t *testing.T) {
	_, err := listenRetry(t.Context(), netip.MustParseAddrPort("192.0.2.123:5432"), 200*time.Millisecond)
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("err = %v", err)
	}
}

func TestReap(t *testing.T) {
	fd := newFakeDocker()
	e := newTestExecutor(t, fd)
	old := time.Now().Add(-time.Hour)
	add := func(name, runID, owner string, created time.Time) {
		fd.mu.Lock()
		defer fd.mu.Unlock()
		id := fd.nextID("ctr")
		labels := map[string]string{spec.LabelManaged: "true", spec.LabelRunID: runID}
		if owner != "" {
			labels[LabelRunner] = owner
		}
		inR, inW := io.Pipe()
		outR, outW := io.Pipe()
		fd.conts[id] = &fakeContainer{id: id, name: name, created: created, spec: spec.Spec{Config: &container.Config{Labels: labels}},
			inR: inR, inW: inW, outR: outR, outW: outW, exited: make(chan struct{}), killed: make(chan struct{})}
		nid := fd.nextID("net")
		fd.nets[nid] = &NetworkInfo{ID: nid, Name: name, Labels: labels, Created: created,
			Subnets: []netip.Prefix{netip.MustParsePrefix(fmt.Sprintf("10.214.%d.0/28", fd.seq))}, Containers: []string{selfID}}
	}
	add("bekup-run-old-mine", "r1", selfID, old)           // reaped
	add("bekup-run-new-mine", "r2", selfID, time.Now())    // too young (periodic), reaped at startup
	add("bekup-run-alive-other", "r3", "otherrunner", old) // other runner alive
	add("bekup-run-dead-other", "r4", "deadrunner", old)   // other runner gone: reaped
	add("someone-else", "r5", selfID, old)                 // not our name: kept
	add("bekup-run-legacy", "r6", "", old)                 // unlabeled owner, old: reaped
	fd.inspect["otherrunner"] = ContainerInfo{ID: "otherrunner"}
	e.mu.Lock()
	e.runs["bekup-run-active"] = &activeRun{runID: "r7"}
	e.mu.Unlock()
	add("bekup-run-active", "r7", selfID, old) // active here: kept

	if err := e.Reap(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	names := func() map[string]int {
		fd.mu.Lock()
		defer fd.mu.Unlock()
		m := map[string]int{}
		for _, c := range fd.conts {
			m[c.name]++
		}
		for _, n := range fd.nets {
			m[n.Name]++
		}
		return m
	}
	got := names()
	for _, keep := range []string{"bekup-run-new-mine", "bekup-run-alive-other", "someone-else", "bekup-run-active"} {
		if got[keep] != 2 {
			t.Errorf("%s removed: %v", keep, got)
		}
	}
	for _, gone := range []string{"bekup-run-old-mine", "bekup-run-dead-other", "bekup-run-legacy"} {
		if got[gone] != 0 {
			t.Errorf("%s kept: %v", gone, got)
		}
	}
	calls := fd.Calls()
	requireOrder(t, calls, "NetworkDisconnect", "NetworkRemove")

	if err := e.Reap(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if got := names(); got["bekup-run-new-mine"] != 0 || got["bekup-run-active"] != 2 {
		t.Fatalf("startup reap: %v", got)
	}
	e.mu.Lock()
	delete(e.runs, "r7")
	e.mu.Unlock()
}

func TestStatusMapping(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code codes.Code
	}{
		{context.Canceled, codes.Canceled},
		{ErrTimeout, codes.DeadlineExceeded},
		{fmt.Errorf("x: %w", ErrBusy), codes.ResourceExhausted},
		{ErrPoolExhausted, codes.ResourceExhausted},
		{ErrTargetBusy, codes.ResourceExhausted},
		{ErrNotReady, codes.Unavailable},
		{ErrUnmappableTarget, codes.FailedPrecondition},
		{&targets.DeniedError{Reason: "x"}, codes.PermissionDenied},
		{&tunnel.Error{Kind: tunnel.ErrHostKeyMismatch, Op: "dial", Addr: "10.0.0.1:22"}, codes.FailedPrecondition},
		{&tunnel.Error{Kind: tunnel.ErrAuthFailed, Op: "dial"}, codes.FailedPrecondition},
		{&tunnel.Error{Kind: tunnel.ErrUnreachable, Op: "dial"}, codes.Unavailable},
		{ErrImage, codes.Unavailable},
		{ErrStdin, codes.Aborted},
		{ErrOutput, codes.Aborted},
		{ErrDuplicateRun, codes.AlreadyExists},
		{fmt.Errorf("%w: secret detail 10.1.2.3", ErrDocker), codes.Internal},
		{status.Error(codes.NotFound, "x"), codes.NotFound},
	} {
		s, _ := status.FromError(Status(tc.err))
		if s.Code() != tc.code {
			t.Errorf("%v: code %s, want %s", tc.err, s.Code(), tc.code)
		}
		if strings.Contains(s.Message(), "10.") {
			t.Errorf("%v: message leaks details: %q", tc.err, s.Message())
		}
	}
	if Status(nil) != nil {
		t.Fatal("nil error mapped")
	}
}

// ignore2 discards a (value, error) result in tests.
func ignore2[T any](_ T, err error) { discardErr(err) }
