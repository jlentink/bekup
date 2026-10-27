package executor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/preamble"
	"github.com/jlentink/bekup/internal/runner/spec"
	"github.com/jlentink/bekup/internal/runner/targets"
	"github.com/jlentink/bekup/internal/tunnel"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

const (
	testRunID  = "0b7f5a3c-2c1e-4d7a-9a51-8c3f0e6d2b19"
	testRunID2 = "1c8f6b4d-3d2f-4e8b-8b62-9d4f1f7e3c20"
	dbHost     = "db.example.test"
	dbIP       = "10.50.0.10"
	password   = "S3cret-Passw0rd-Value"
)

// testInfra are the runner's (fake) name server and default gateway.
var testInfra = []netip.Addr{netip.MustParseAddr("10.98.0.3"), netip.MustParseAddr("10.98.1.1")}

func testEntry(t *testing.T) engine.Entry {
	t.Helper()
	cat, err := engine.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	e, ok := cat.Lookup(engine.KindPostgres, "17")
	if !ok {
		t.Fatal("no postgres 17 entry")
	}
	return e
}

func testConfig() *config.Runner {
	c := config.DefaultRunner()
	c.Listen.Address = selfIP + ":7443"
	c.Limits.MaxConcurrentRuns = 4
	c.Limits.MaxLogLines = 50
	c.Limits.MaxLogLineBytes = 256
	return &c
}

func testGuard(t *testing.T) *targets.Guard {
	t.Helper()
	g, err := targets.New(targets.Options{
		AllowCIDRs: []string{"10.50.0.0/16", "10.99.0.0/16"},
		AllowHosts: []string{"bastion.example.test"},
		Lookup: func(_ context.Context, _, host string) ([]netip.Addr, error) {
			switch host {
			case dbHost:
				return []netip.Addr{netip.MustParseAddr(dbIP)}, nil
			case "bastion.example.test":
				return []netip.Addr{netip.MustParseAddr("10.50.0.20")}, nil
			}
			return nil, errors.New("no such host")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func newTestExecutor(t *testing.T, fd *fakeDocker, mutate ...func(*config.Runner)) *Executor {
	t.Helper()
	cfg := testConfig()
	for _, m := range mutate {
		m(cfg)
	}
	e, err := New(Options{Config: cfg, Docker: fd, Guard: testGuard(t), Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	e.mountinfo = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("1 2 0:1 /var/lib/docker/containers/" + selfID + "/hostname /etc/hostname rw - ext4 /dev/vda1 rw\n")), nil
	}
	e.hostname = func() (string, error) { return "runnerhost", nil }
	e.hostInfra = func() []netip.Addr { return testInfra }
	e.localAddrs = func() ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr(selfIP)}, nil
	}
	e.killGrace = 50 * time.Millisecond
	e.cleanupTimeout = 2 * time.Second
	e.drainAfterKill = 500 * time.Millisecond
	e.listen = func(ctx context.Context, _ netip.AddrPort) (net.Listener, error) {
		var lc net.ListenConfig
		return lc.Listen(ctx, "tcp4", "127.0.0.1:0")
	}
	fd.images[spec.ImageRef(testEntry(t))] = true
	if err := e.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	return e
}

func material() *preamble.Material {
	return &preamble.Material{
		PGPass: []byte("*:*:*:backup:" + password + "\n"),
		CA:     []byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"),
	}
}

func pgCommand(t *testing.T, ent engine.Entry, tls engine.TLSMode) func(ClientEndpoint) (engine.Command, error) {
	t.Helper()
	eng, err := engine.ForKind(engine.KindPostgres)
	if err != nil {
		t.Fatal(err)
	}
	return func(ep ClientEndpoint) (engine.Command, error) {
		return eng.Dump(ent, engine.Target{Host: ep.Host, Port: ep.Port, User: "backup", TLS: tls}, "app")
	}
}

type sink struct {
	mu     sync.Mutex
	data   bytes.Buffer
	chunks []int
	lines  []string
}

func (s *sink) stdout(b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chunks = append(s.chunks, len(b))
	s.data.Write(b)
	return nil
}

func (s *sink) log(l string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, l)
}

func baseSpec(t *testing.T, s *sink) RunSpec {
	ent := testEntry(t)
	return RunSpec{
		RunID:    testRunID,
		Entry:    ent,
		Target:   Target{Host: dbHost, Port: 5432, VerifyHost: true},
		Command:  pgCommand(t, ent, engine.TLSPinned),
		Material: material(),
		Stdout:   s.stdout,
		Log:      s.log,
		Redact:   [][]byte{[]byte(password)},
		Timeout:  10 * time.Second,
	}
}

// indexOf returns the position of op in calls (-1 if missing).
func indexOf(calls []string, op string) int { return slices.Index(calls, op) }

func requireOrder(t *testing.T, calls []string, ops ...string) {
	t.Helper()
	last := -1
	for _, op := range ops {
		i := slices.Index(calls[last+1:], op)
		if i < 0 {
			t.Fatalf("missing %s after position %d in %v", op, last, calls)
		}
		last += i + 1
	}
}

func checkReleased(t *testing.T, e *Executor, fd *fakeDocker) {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.runs) != 0 || len(e.pool.used) != 0 {
		t.Fatalf("run state not released: runs=%d used=%d", len(e.runs), len(e.pool.used))
	}
	own := e.guard.OwnNetworks()
	if len(own) != len(e.base)+len(e.zombie) {
		t.Fatalf("own networks %v, want base %v", own, e.base)
	}
	fd.mu.Lock()
	defer fd.mu.Unlock()
	if len(fd.conts) != 0 || len(fd.nets) != 0 {
		t.Fatalf("leftovers: %d containers, %d networks", len(fd.conts), len(fd.nets))
	}
}

func TestRunSuccess(t *testing.T) {
	fd := newFakeDocker()
	payload := bytes.Repeat([]byte("0123456789abcdef"), 200_000) // 3.2 MB
	var preambleSeen string
	fd.proc = func(p *fakeProc) {
		preambleSeen = p.readPreamble()
		discardErr(p.stderr("pg_dump: connecting with password " + password + "\n"))
		discardErr(p.stderr("line with \x1b[31mescape\x1b[0m\r\npartial "))
		for off := 0; off < len(payload); off += 32 << 10 {
			discardErr(p.stdout(payload[off:min(off+32<<10, len(payload))]))
		}
		discardErr(p.stderr("end"))
		p.exit(0)
	}
	e := newTestExecutor(t, fd)
	s := &sink{}
	rs := baseSpec(t, s)
	m := rs.Material
	var info RunInfo
	rs.OnStart = func(i RunInfo) { info = i }
	res, err := e.Run(t.Context(), rs)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || res.PreambleFailed || res.StdoutBytes != int64(len(payload)) {
		t.Fatalf("result %+v", res)
	}
	if !bytes.Equal(s.data.Bytes(), payload) {
		t.Fatal("stdout differs")
	}
	for _, c := range s.chunks {
		if c > DefaultChunkBytes {
			t.Fatalf("chunk of %d bytes", c)
		}
	}
	joined := strings.Join(s.lines, "\n")
	if strings.Contains(joined, password) || !strings.Contains(joined, "[REDACTED]") {
		t.Fatalf("log not redacted: %q", joined)
	}
	if strings.ContainsRune(joined, '\x1b') || len(s.lines) != 3 || s.lines[2] != "partial end" {
		t.Fatalf("log lines %q", s.lines)
	}
	if !strings.HasPrefix(preambleSeen, preamble.Header+"\n") || !strings.Contains(preambleSeen, "pgpass ") {
		t.Fatalf("preamble %q", preambleSeen)
	}
	if !bytes.Equal(m.PGPass, make([]byte, len(m.PGPass))) || !bytes.Equal(m.CA, make([]byte, len(m.CA))) {
		t.Fatal("material not zeroed")
	}
	// Network and container shape.
	nr := fd.lastNetReq
	if nr.Name != "bekup-run-"+testRunID || !nr.Internal || nr.Subnet.String() != "10.214.0.0/28" ||
		nr.Gateway.String() != "10.214.0.1" || nr.Labels[LabelRunner] != selfID || nr.Labels[spec.LabelRunID] != testRunID {
		t.Fatalf("network request %+v", nr)
	}
	for k, v := range networkOptions {
		if nr.Options[k] != v {
			t.Fatalf("network option %s = %q", k, nr.Options[k])
		}
	}
	sp := fd.lastSpec
	if string(sp.HostConfig.NetworkMode) != nr.Name ||
		!slices.Equal(sp.HostConfig.ExtraHosts, []string{dbHost + ":10.214.0.2"}) ||
		sp.Networking.EndpointsConfig[nr.Name].IPAMConfig.IPv4Address.String() != "10.214.0.3" ||
		sp.Config.Labels[LabelRunner] != selfID {
		t.Fatalf("spec %+v %+v", sp.HostConfig, sp.Networking.EndpointsConfig[nr.Name])
	}
	argv := strings.Join(sp.Config.Cmd, " ")
	if !strings.Contains(argv, "host='"+dbHost+"'") || strings.Contains(argv, password) {
		t.Fatalf("argv %q", argv)
	}
	if info.ClientHost != dbHost || info.ClientIP.String() != "10.214.0.3" || info.Network != nr.Name {
		t.Fatalf("run info %+v", info)
	}
	requireOrder(t, fd.Calls(), "ImageInspect", "NetworkList", "NetworkCreate", "NetworkConnect",
		"ContainerCreate", "ContainerAttach", "ContainerStart", "ContainerRemove", "NetworkDisconnect", "NetworkRemove")
	if indexOf(fd.Calls(), "ImagePull") >= 0 || indexOf(fd.Calls(), "ContainerKill:SIGKILL") >= 0 {
		t.Fatalf("unexpected calls %v", fd.Calls())
	}
	checkReleased(t, e, fd)
}

func TestRunPullsMissingImage(t *testing.T) {
	fd := newFakeDocker()
	e := newTestExecutor(t, fd)
	clear(fd.images)
	if _, err := e.Run(t.Context(), baseSpec(t, &sink{})); err != nil {
		t.Fatal(err)
	}
	requireOrder(t, fd.Calls(), "ImageInspect", "ImagePull", "ImageInspect", "NetworkCreate")

	fd.fail["ImagePull"] = errors.New("registry down")
	clear(fd.images)
	rs := baseSpec(t, &sink{})
	if _, err := e.Run(t.Context(), rs); !errors.Is(err, ErrImage) {
		t.Fatalf("err = %v", err)
	}
	checkReleased(t, e, fd)
}

func TestRunRestoreStdin(t *testing.T) {
	fd := newFakeDocker()
	var got []byte
	fd.proc = func(p *fakeProc) {
		p.readPreamble()
		var err error
		got, err = p.readAll()
		discardErr(err)
		p.exit(0)
	}
	e := newTestExecutor(t, fd)
	rs := baseSpec(t, &sink{})
	payload := bytes.Repeat([]byte("restore-data;"), 100_000)
	rs.Stdin = bytes.NewReader(payload)
	if _, err := e.Run(t.Context(), rs); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload: got %d bytes, want %d", len(got), len(payload))
	}
}

// failingReader returns some data, then an error (lost restore stream).
type failingReader struct{ n int }

func (r *failingReader) Read(p []byte) (int, error) {
	if r.n > 0 {
		r.n--
		return copy(p, "INSERT INTO t VALUES (1);\n"), nil
	}
	return 0, errors.New("stream lost")
}

func TestRunStdinErrorKillsWithoutEOF(t *testing.T) {
	fd := newFakeDocker()
	sawEOF := atomic.Bool{}
	fd.proc = func(p *fakeProc) {
		p.readPreamble()
		_, err := p.readAll()
		if err == nil {
			sawEOF.Store(true)
		}
	}
	e := newTestExecutor(t, fd)
	rs := baseSpec(t, &sink{})
	rs.Stdin = &failingReader{n: 3}
	_, err := e.Run(t.Context(), rs)
	if !errors.Is(err, ErrStdin) {
		t.Fatalf("err = %v", err)
	}
	fd.lastCont.mu.Lock()
	closed := fd.lastCont.closeWrite
	fd.lastCont.mu.Unlock()
	if sawEOF.Load() || closed {
		t.Fatal("container saw a clean end of input")
	}
	requireOrder(t, fd.Calls(), "ContainerKill:SIGKILL", "ContainerRemove")
	if indexOf(fd.Calls(), "ContainerKill:SIGTERM") >= 0 {
		t.Fatal("stdin failure must kill at once")
	}
	checkReleased(t, e, fd)
}

func TestRunTimeoutGracefulKill(t *testing.T) {
	fd := newFakeDocker()
	fd.ignoreTerm = true
	fd.proc = func(p *fakeProc) {
		p.readPreamble()
		<-p.killed()
	}
	e := newTestExecutor(t, fd)
	rs := baseSpec(t, &sink{})
	rs.Timeout = 100 * time.Millisecond
	start := time.Now()
	_, err := e.Run(t.Context(), rs)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) < rs.Timeout+e.killGrace {
		t.Fatal("SIGKILL came before the grace period")
	}
	requireOrder(t, fd.Calls(), "ContainerKill:SIGTERM", "ContainerKill:SIGKILL", "ContainerRemove", "NetworkDisconnect", "NetworkRemove")
	checkReleased(t, e, fd)
}

func TestRunTimeoutTermSuffices(t *testing.T) {
	fd := newFakeDocker()
	fd.proc = func(p *fakeProc) {
		p.readPreamble()
		<-p.killed()
	}
	e := newTestExecutor(t, fd)
	rs := baseSpec(t, &sink{})
	rs.Timeout = 50 * time.Millisecond
	if _, err := e.Run(t.Context(), rs); !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v", err)
	}
	if indexOf(fd.Calls(), "ContainerKill:SIGKILL") >= 0 {
		t.Fatalf("SIGKILL after a successful SIGTERM: %v", fd.Calls())
	}
}

func TestRunCancelKillsAtOnce(t *testing.T) {
	fd := newFakeDocker()
	fd.ignoreTerm = true
	fd.proc = func(p *fakeProc) {
		p.readPreamble()
		<-p.killed()
	}
	e := newTestExecutor(t, fd)
	ctx, cancel := context.WithCancel(t.Context())
	rs := baseSpec(t, &sink{})
	rs.OnStart = func(RunInfo) { cancel() }
	_, err := e.Run(ctx, rs)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if indexOf(fd.Calls(), "ContainerKill:SIGTERM") >= 0 || indexOf(fd.Calls(), "ContainerKill:SIGKILL") < 0 {
		t.Fatalf("calls %v", fd.Calls())
	}
	checkReleased(t, e, fd)
}

func TestRunOutputLossKills(t *testing.T) {
	fd := newFakeDocker()
	fd.proc = func(p *fakeProc) {
		p.readPreamble()
		chunk := make([]byte, 64<<10)
		for {
			if err := p.stdout(chunk); err != nil {
				return
			}
		}
	}
	e := newTestExecutor(t, fd)
	rs := baseSpec(t, &sink{})
	n := 0
	rs.Stdout = func([]byte) error {
		n++
		if n == 2 {
			return errors.New("grpc stream closed")
		}
		return nil
	}
	_, err := e.Run(t.Context(), rs)
	if !errors.Is(err, ErrOutput) {
		t.Fatalf("err = %v", err)
	}
	requireOrder(t, fd.Calls(), "ContainerKill:SIGKILL", "ContainerRemove")
	checkReleased(t, e, fd)
}

func TestRunBackpressure(t *testing.T) {
	fd := newFakeDocker()
	var written atomic.Int64
	const frame = 16 << 10
	fd.proc = func(p *fakeProc) {
		p.readPreamble()
		chunk := make([]byte, frame)
		for range 200 {
			if err := p.stdout(chunk); err != nil {
				return
			}
			written.Add(frame)
		}
		p.exit(0)
	}
	e := newTestExecutor(t, fd)
	rs := baseSpec(t, &sink{})
	release := make(chan struct{})
	first := make(chan struct{})
	var once sync.Once
	var total atomic.Int64
	rs.Stdout = func(b []byte) error {
		once.Do(func() { close(first) })
		<-release
		total.Add(int64(len(b)))
		return nil
	}
	done := make(chan error, 1)
	go func() {
		_, err := e.Run(t.Context(), rs)
		done <- err
	}()
	<-first
	time.Sleep(100 * time.Millisecond)
	// The consumer holds one chunk; the process can be ahead by at most
	// one more chunk being filled plus one frame in the pipe.
	if w := written.Load(); w > 2*DefaultChunkBytes+frame {
		t.Fatalf("process wrote %d bytes while the consumer was blocked", w)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if total.Load() != 200*frame {
		t.Fatalf("total %d", total.Load())
	}
}

func TestRunNonZeroExitAndPreambleFailure(t *testing.T) {
	fd := newFakeDocker()
	fd.proc = func(p *fakeProc) { p.readPreamble(); p.exit(preamble.ExitCode) }
	e := newTestExecutor(t, fd)
	res, err := e.Run(t.Context(), baseSpec(t, &sink{}))
	if err != nil || res.ExitCode != preamble.ExitCode || !res.PreambleFailed {
		t.Fatalf("res %+v err %v", res, err)
	}
}

// TestRunCleanupOnEveryFailure fails each Docker step in turn and checks
// that everything created before is removed, in reverse order.
func TestRunCleanupOnEveryFailure(t *testing.T) {
	steps := []struct {
		fail    string
		cleanup []string
		absent  []string
	}{
		{"NetworkList", nil, []string{"NetworkCreate"}},
		{"NetworkCreate", nil, []string{"NetworkRemove", "NetworkConnect"}},
		{"NetworkConnect", []string{"NetworkRemove"}, []string{"NetworkDisconnect", "ContainerCreate"}},
		{"ContainerCreate", []string{"NetworkDisconnect", "NetworkRemove"}, []string{"ContainerRemove"}},
		{"ContainerAttach", []string{"ContainerRemove", "NetworkDisconnect", "NetworkRemove"}, []string{"ContainerStart"}},
		{"ContainerStart", []string{"ContainerRemove", "NetworkDisconnect", "NetworkRemove"}, nil},
		{"ContainerWait", []string{"ContainerKill:SIGKILL", "ContainerRemove", "NetworkDisconnect", "NetworkRemove"}, nil},
	}
	for _, st := range steps {
		t.Run(st.fail, func(t *testing.T) {
			fd := newFakeDocker()
			fd.proc = func(p *fakeProc) { p.readPreamble(); <-p.killed() }
			e := newTestExecutor(t, fd)
			fd.fail[st.fail] = errors.New("boom")
			rs := baseSpec(t, &sink{})
			m := rs.Material
			_, err := e.Run(t.Context(), rs)
			if err == nil {
				t.Fatal("no error")
			}
			calls := fd.Calls()
			failAt := slices.Index(calls, st.fail)
			if len(st.cleanup) > 0 {
				requireOrder(t, calls[failAt:], st.cleanup...)
			}
			for _, a := range st.absent {
				if slices.Contains(calls[failAt+1:], a) {
					t.Fatalf("%s called after failing %s: %v", a, st.fail, calls)
				}
			}
			if !bytes.Equal(m.PGPass, make([]byte, len(m.PGPass))) {
				t.Fatal("material not zeroed")
			}
			checkReleased(t, e, fd)
		})
	}
}

func TestRunCleanupOnPanic(t *testing.T) {
	fd := newFakeDocker()
	e := newTestExecutor(t, fd)
	fd.panicOn = "ContainerStart"
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("panic swallowed")
			}
		}()
		_, err := e.Run(t.Context(), baseSpec(t, &sink{}))
		discardErr(err)
	}()
	requireOrder(t, fd.Calls(), "ContainerStart", "ContainerRemove", "NetworkDisconnect", "NetworkRemove")
	checkReleased(t, e, fd)
}

func TestRunCleanupFailureKeepsSubnetDenied(t *testing.T) {
	fd := newFakeDocker()
	e := newTestExecutor(t, fd)
	fd.fail["NetworkDisconnect"] = errors.New("daemon busy")
	if _, err := e.Run(t.Context(), baseSpec(t, &sink{})); err != nil {
		t.Fatal(err)
	}
	sub := netip.MustParsePrefix("10.214.0.0/28")
	if !e.IsRunAddress(netip.MustParseAddr("10.214.0.3")) || !slices.Contains(e.guard.OwnNetworks(), sub) {
		t.Fatal("zombie subnet not kept")
	}
	// The reaper removes the leftover network and lifts the reservation.
	delete(fd.fail, "NetworkDisconnect")
	e.now = func() time.Time { return time.Now().Add(ReapGrace + time.Minute) }
	if err := e.Reap(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(e.guard.OwnNetworks(), sub) {
		t.Fatal("zombie subnet still denied after reaping")
	}
	checkReleased(t, e, fd)
}

func TestRunLimitsAndValidation(t *testing.T) {
	fd := newFakeDocker()
	block := make(chan struct{})
	fd.proc = func(p *fakeProc) {
		p.readPreamble()
		select {
		case <-block:
			p.exit(0)
		case <-p.killed():
		}
	}
	e := newTestExecutor(t, fd, func(c *config.Runner) { c.Limits.MaxConcurrentRuns = 2 })
	e.pool.count = 1 // a single /28

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		rs := baseSpec(t, &sink{})
		rs.OnStart = func(RunInfo) { close(started) }
		_, err := e.Run(t.Context(), rs)
		done <- err
	}()
	<-started

	rs := baseSpec(t, &sink{})
	if _, err := e.Run(t.Context(), rs); !errors.Is(err, ErrDuplicateRun) {
		t.Fatalf("duplicate: %v", err)
	}
	rs = baseSpec(t, &sink{})
	rs.RunID = testRunID2
	if _, err := e.Run(t.Context(), rs); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("pool: %v", err)
	}
	e.sem <- struct{}{} // fill the second slot
	rs = baseSpec(t, &sink{})
	rs.RunID = testRunID2
	if _, err := e.Run(t.Context(), rs); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy: %v", err)
	}
	<-e.sem
	close(block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*RunSpec){
		"run id":     func(r *RunSpec) { r.RunID = "x" },
		"port":       func(r *RunSpec) { r.Target.Port = 0 },
		"host":       func(r *RunSpec) { r.Target.Host = "-bad" },
		"stdout":     func(r *RunSpec) { r.Stdout = nil },
		"tunnel":     func(r *RunSpec) { r.Tunnel = &tunnel.Config{TargetHost: "other", TargetPort: 5432} },
		"negative":   func(r *RunSpec) { r.Timeout = -1 },
		"no command": func(r *RunSpec) { r.Command = nil },
	} {
		rs := baseSpec(t, &sink{})
		mutate(&rs)
		if _, err := e.Run(t.Context(), rs); !errors.Is(err, ErrInvalidSpec) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestRunNotReady(t *testing.T) {
	fd := newFakeDocker()
	e, err := New(Options{Config: testConfig(), Docker: fd, Guard: testGuard(t), Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	rs := baseSpec(t, &sink{})
	m := rs.Material
	if _, err := e.Run(t.Context(), rs); !errors.Is(err, ErrNotReady) {
		t.Fatalf("err = %v", err)
	}
	if !bytes.Equal(m.PGPass, make([]byte, len(m.PGPass))) {
		t.Fatal("material not zeroed")
	}
	// The pool is denied even before Init.
	if !slices.Contains(e.guard.OwnNetworks(), netip.MustParsePrefix(config.DefaultRunSubnetPool)) {
		t.Fatal("pool not denied before Init")
	}
}

func TestInitOwnNetworks(t *testing.T) {
	fd := newFakeDocker()
	e := newTestExecutor(t, fd)
	own := e.guard.OwnNetworks()
	for _, want := range []string{config.DefaultRunSubnetPool, "172.30.0.0/24", selfIP + "/32"} {
		if !slices.Contains(own, netip.MustParsePrefix(want)) {
			t.Errorf("own networks %v lack %s", own, want)
		}
	}
	if err := e.guard.Check(netip.MustParseAddr("172.30.0.9"), ""); !errors.Is(err, targets.ErrDenied) {
		t.Fatal("control network not denied")
	}
}

func TestDetectSelfRequiresOwnAddress(t *testing.T) {
	fd := newFakeDocker()
	_, err := detectSelf(t.Context(), fd, []string{selfID}, []netip.Addr{netip.MustParseAddr("10.0.0.1")})
	if err == nil {
		t.Fatal("a container without our address was accepted")
	}
	if _, err := detectSelf(t.Context(), fd, nil, nil); err == nil {
		t.Fatal("no candidates accepted")
	}
	got, err := detectSelf(t.Context(), fd, []string{"missing", "runnerhost"}, []netip.Addr{netip.MustParseAddr(selfIP)})
	if err != nil || got.ID != selfID {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestSelfCandidates(t *testing.T) {
	mi := "100 90 0:50 /docker/containers/" + selfID + "/resolv.conf /etc/resolv.conf rw - ext4 /dev/vda1 rw\n" +
		"101 90 0:50 /docker/containers/" + selfID + "/hostname /etc/hostname rw - ext4 /dev/vda1 rw\n" +
		"102 90 0:51 / /proc rw - proc proc rw\n"
	if got := selfCandidates("", strings.NewReader(mi), "abc123"); !slices.Equal(got, []string{selfID, "abc123"}) {
		t.Fatalf("got %v", got)
	}
	if got := selfCandidates("bekup-runner-1", strings.NewReader(mi), "abc"); !slices.Equal(got, []string{"bekup-runner-1"}) {
		t.Fatalf("configured: %v", got)
	}
	if got := selfCandidates("", nil, "bad name!"); len(got) != 0 {
		t.Fatalf("bad hostname: %v", got)
	}
}

func TestPlanVariants(t *testing.T) {
	fd := newFakeDocker()
	e := newTestExecutor(t, fd)
	ent := testEntry(t)

	mk := func(host string, verify bool, tun *tunnel.Config) *RunSpec {
		return &RunSpec{RunID: testRunID, Entry: ent, Target: Target{Host: host, Port: 5432, VerifyHost: verify}, Tunnel: tun}
	}
	bastion := func(host string) *tunnel.Config {
		return &tunnel.Config{Host: "bastion.example.test", Port: 22, TargetHost: host, TargetPort: 5432}
	}

	// IP literal without host verification: the client dials the proxy.
	p, rel, err := e.makePlan(t.Context(), mk(dbIP, false, nil), "n")
	if err != nil || p.alias != nil || p.clientHost != p.net.Proxy.String() || p.net.around {
		t.Fatalf("plan %+v err %v", p, err)
	}
	rel(false)

	// IP literal with verification and no tunnel: fail closed.
	if _, _, err := e.makePlan(t.Context(), mk(dbIP, true, nil), "n"); !errors.Is(err, ErrUnmappableTarget) {
		t.Fatalf("direct literal: %v", err)
	}
	// localhost behind a bastion with verification: fail closed.
	if _, _, err := e.makePlan(t.Context(), mk("localhost", true, bastion("localhost")), "n"); !errors.Is(err, ErrUnmappableTarget) {
		t.Fatalf("localhost: %v", err)
	}
	// 127.0.0.1 behind a bastion without verification: proxy address.
	p, rel, err = e.makePlan(t.Context(), mk("127.0.0.1", false, bastion("127.0.0.1")), "n")
	if err != nil || p.clientHost != p.net.Proxy.String() {
		t.Fatalf("loopback unverified: %+v %v", p, err)
	}
	rel(false)

	// IP literal with verification behind a bastion: /29 around it.
	p, rel, err = e.makePlan(t.Context(), mk("10.99.0.5", true, bastion("10.99.0.5")), "n")
	if err != nil || !p.net.around || p.net.Proxy.String() != "10.99.0.5" || p.net.Subnet.String() != "10.99.0.0/29" || p.clientHost != "10.99.0.5" {
		t.Fatalf("around: %+v %v", p, err)
	}
	// While active, the /29 is denied to every dial and a second run to
	// the same range is refused.
	if err := e.guard.Check(netip.MustParseAddr("10.99.0.6"), ""); !errors.Is(err, targets.ErrDenied) {
		t.Fatal("around subnet not denied")
	}
	r2 := mk("10.99.0.6", true, bastion("10.99.0.6"))
	r2.RunID = testRunID2
	if _, _, err := e.makePlan(t.Context(), r2, "n2"); !errors.Is(err, ErrTargetBusy) {
		t.Fatalf("busy: %v", err)
	}
	rel(false)

	// A /29 that would contain the bastion is refused.
	if _, _, err := e.makePlan(t.Context(), mk("10.50.0.21", true, bastion("10.50.0.21")), "n"); !errors.Is(err, ErrUnmappableTarget) {
		t.Fatalf("bastion inside /29: %v", err)
	}
	// A /29 holding the runner's name server or default gateway is
	// unmappable (it would shadow the runner's own infrastructure).
	for _, x := range []string{"10.98.0.4", "10.98.1.2"} {
		if _, _, err := e.makePlan(t.Context(), mk(x, true, bastion(x)), "n"); !errors.Is(err, ErrUnmappableTarget) {
			t.Fatalf("infra inside /29 (%s): %v", x, err)
		}
	}
	// A /29 holding a Docker network's gateway is unmappable.
	fd.foreign = []NetworkInfo{{Name: "gw", Gateways: []netip.Addr{netip.MustParseAddr("10.97.0.1")}}}
	if _, _, err := e.makePlan(t.Context(), mk("10.97.0.2", true, bastion("10.97.0.2")), "n"); !errors.Is(err, ErrUnmappableTarget) {
		t.Fatalf("docker gateway inside /29: %v", err)
	}
	// A /29 overlapping a foreign Docker network is unmappable.
	fd.foreign = []NetworkInfo{{Name: "other", Subnets: []netip.Prefix{netip.MustParsePrefix("10.99.1.0/24")}}}
	if _, _, err := e.makePlan(t.Context(), mk("10.99.1.5", true, bastion("10.99.1.5")), "n"); !errors.Is(err, ErrUnmappableTarget) {
		t.Fatalf("docker overlap: %v", err)
	}
	// Pooled subnets skip foreign networks.
	fd.foreign = []NetworkInfo{{Name: "other", Subnets: []netip.Prefix{netip.MustParsePrefix("10.214.0.0/26")}}}
	p, rel, err = e.makePlan(t.Context(), mk(dbHost, true, nil), "n")
	if err != nil || p.net.Subnet.Addr().Compare(netip.MustParseAddr("10.214.0.64")) < 0 {
		t.Fatalf("pooled: %+v %v", p, err)
	}
	rel(false)
	// A denied direct target fails before anything is reserved.
	if _, _, err := e.makePlan(t.Context(), mk("10.214.0.9", false, nil), "n"); !errors.Is(err, targets.ErrDenied) {
		t.Fatalf("pool target: %v", err)
	}
	checkReleased(t, e, fd)
}

func TestIsRunAddress(t *testing.T) {
	fd := newFakeDocker()
	e := newTestExecutor(t, fd)
	if !e.IsRunAddress(netip.MustParseAddr("10.214.200.7")) || e.IsRunAddress(netip.MustParseAddr(selfIP)) {
		t.Fatal("pool classification")
	}
	ent := testEntry(t)
	rs := &RunSpec{RunID: testRunID, Entry: ent, Target: Target{Host: "10.99.0.5", Port: 5432, VerifyHost: true},
		Tunnel: &tunnel.Config{Host: "bastion.example.test", Port: 22, TargetHost: "10.99.0.5", TargetPort: 5432}}
	_, rel, err := e.makePlan(t.Context(), rs, "n")
	if err != nil {
		t.Fatal(err)
	}
	if !e.IsRunAddress(netip.MustParseAddr("10.99.0.2")) {
		t.Fatal("around subnet not a run address")
	}
	rel(false)
	if e.IsRunAddress(netip.MustParseAddr("10.99.0.2")) {
		t.Fatal("released subnet still a run address")
	}
}

func TestStatus(t *testing.T) {
	fd := newFakeDocker()
	e, err := New(Options{Config: testConfig(), Docker: fd, Guard: testGuard(t), Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	if s := e.Status(t.Context()); s.String() != "DOCKER_STATUS_UNKNOWN" {
		t.Fatalf("before init: %s", s)
	}
	fd.fail["Ping"] = errors.New("down")
	if s := e.Status(t.Context()); s.String() != "DOCKER_STATUS_UNREACHABLE" {
		t.Fatalf("down: %s", s)
	}
	if err := e.Init(t.Context()); !errors.Is(err, ErrNotReady) {
		t.Fatalf("init while down: %v", err)
	}
}

func TestStartRetriesAndStops(t *testing.T) {
	fd := newFakeDocker()
	e := newTestExecutor(t, fd)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { e.Start(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return")
	}
}
