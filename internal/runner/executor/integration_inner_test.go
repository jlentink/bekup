//go:build integration

package executor_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/executor"
	"github.com/jlentink/bekup/internal/runner/preamble"
	"github.com/jlentink/bekup/internal/runner/spec"
	"github.com/jlentink/bekup/internal/runner/targets"
	"github.com/jlentink/bekup/internal/tunnel"
)

const (
	run1 = "6a1d2c3e-0000-4000-8000-000000000001"
	run2 = "6a1d2c3e-0000-4000-8000-000000000002"
)

type inner struct {
	h      harness
	e      *executor.Executor
	entry  engine.Entry
	ctlIP  netip.Addr
	runSeq atomic.Int32
}

func (in *inner) runID() string {
	return fmt.Sprintf("6a1d2c3e-0000-4000-8000-%012d", 100+in.runSeq.Add(1))
}

type output struct {
	mu     sync.Mutex
	stdout strings.Builder
	lines  []string
}

func (o *output) Stdout(b []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stdout.Write(b)
	return nil
}

func (o *output) Log(l string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lines = append(o.lines, l)
}

func (o *output) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return "stdout: " + o.stdout.String() + "\nstderr: " + strings.Join(o.lines, "\n")
}

// psql returns a command that runs one query against host:port with the
// given sslmode; host "" means "the endpoint the executor chose".
func (in *inner) psql(host string, port int, sslmode, query string) func(executor.ClientEndpoint) (engine.Command, error) {
	return func(ep executor.ClientEndpoint) (engine.Command, error) {
		h, p := host, port
		if h == "" {
			h, p = ep.Host, ep.Port
		}
		conninfo := fmt.Sprintf("host=%s port=%d user=%s dbname=postgres passfile=%s sslmode=%s sslrootcert=%s connect_timeout=5 gssencmode=disable",
			h, p, dbUser, engine.FilePGPass, sslmode, engine.FileCA)
		// psql reads the runner's \restrict line from stdin first (D-138).
		return engine.Command{
			Image:       in.entry.Image(),
			Argv:        []string{"psql", "-X", "-w", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-f", "-", "-c", query, "--dbname=" + conninfo},
			Files:       []engine.File{engine.FilePGPass, engine.FileCA},
			StdinPrefix: engine.PSQLRestrictPrefix(),
		}, nil
	}
}

func (in *inner) material() *preamble.Material {
	return &preamble.Material{
		PGPass: []byte("*:*:*:" + dbUser + ":" + in.h.DBPassword + "\n"),
		CA:     []byte(in.h.CAPEM),
	}
}

func (in *inner) spec(id string, target executor.Target, cmd func(executor.ClientEndpoint) (engine.Command, error), o *output) executor.RunSpec {
	return executor.RunSpec{
		RunID: id, Entry: in.entry, Target: target, Command: cmd, Material: in.material(),
		Stdout: o.Stdout, Log: o.Log, Redact: [][]byte{[]byte(in.h.DBPassword)}, Timeout: 90 * time.Second,
	}
}

func (in *inner) tunnelTo(host string) *tunnel.Config {
	return &tunnel.Config{
		Host: in.h.BastionHost, Port: 2222, User: sshUser,
		Auth:       tunnel.Auth{PrivateKey: crypto.NewSecret([]byte(in.h.ClientKeyPEM))},
		HostKeyPin: tunnel.HostKeyPin{AuthorizedKey: in.h.HostKey},
		TargetHost: host, TargetPort: 5432,
	}
}

func TestInnerIsolation(t *testing.T) {
	path := os.Getenv(innerEnv)
	if path == "" {
		t.Skip("runs inside the harness container only")
	}
	raw, err := os.ReadFile(path) //nolint:gosec // harness file
	if err != nil {
		t.Fatal(err)
	}
	var h harness
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ctlSubnet := netip.MustParsePrefix(h.CtlSubnet)
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	var ctlIP netip.Addr
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil && ctlSubnet.Contains(p.Addr()) {
			ctlIP = p.Addr()
		}
	}
	if !ctlIP.IsValid() {
		t.Fatal("no control network address")
	}

	cfg := config.DefaultRunner()
	cfg.Listen.Address = net.JoinHostPort(ctlIP.String(), "7443")
	cfg.Targets.AllowCIDRs = []string{h.TargetsSubnet}
	guard, err := targets.FromConfig(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	d, closer, err := executor.NewMobyDocker("unix:///var/run/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { ignore(closer.Close()) }()
	e, err := executor.New(executor.Options{Config: &cfg, Docker: d, Guard: guard, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Init(ctx); err != nil {
		t.Fatalf("init (self detection): %v", err)
	}
	cat, err := engine.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := cat.Lookup(engine.KindPostgres, entryKey)
	if !ok {
		t.Fatal("no catalog entry")
	}
	in := &inner{h: h, e: e, entry: entry, ctlIP: ctlIP}

	// The runner's gRPC port: bound to the control address (production
	// rule), plus a wildcard listener behind the same peer filter as
	// Server.Serve (dev / allow_wildcard).
	var lc net.ListenConfig
	grpcLn, err := lc.Listen(ctx, "tcp4", cfg.Listen.Address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { ignore(grpcLn.Close()) }()
	wildLn, err := lc.Listen(ctx, "tcp4", "0.0.0.0:7444")
	if err != nil {
		t.Fatal(err)
	}
	filtered := executor.FilterListener(wildLn, e.IsRunAddress, log)
	defer func() { ignore(filtered.Close()) }()
	var accepted atomic.Int32
	for _, ln := range []net.Listener{grpcLn, filtered} {
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				accepted.Add(1)
				ignore(c.Close())
			}
		}()
	}

	dnsTarget := executor.Target{Host: "db.bekup.test", Port: 5432, VerifyHost: true}

	t.Run("verify-full through the proxy (DNS name)", func(t *testing.T) {
		o := &output{}
		rs := in.spec(in.runID(), dnsTarget, in.psql("", 0, "verify-full", "select 'ssl=' || ssl from pg_stat_ssl where pid = pg_backend_pid()"), o)
		res, err := e.Run(ctx, rs)
		if err != nil || res.ExitCode != 0 || !strings.Contains(o.stdout.String(), "ssl=t") {
			t.Fatalf("res %+v err %v\n%s", res, err, o)
		}
	})

	t.Run("verify-full rejects a name the certificate lacks", func(t *testing.T) {
		o := &output{}
		rs := in.spec(in.runID(), executor.Target{Host: "other.bekup.test", Port: 5432, VerifyHost: true},
			in.psql("", 0, "verify-full", "select 1"), o)
		res, err := e.Run(ctx, rs)
		if err != nil || res.ExitCode == 0 || !strings.Contains(o.String(), "does not match host name") {
			t.Fatalf("res %+v err %v\n%s", res, err, o)
		}
	})

	t.Run("verify-full to an IP-literal target through the SSH bastion", func(t *testing.T) {
		o := &output{}
		rs := in.spec(in.runID(), executor.Target{Host: literalIP, Port: 5432, VerifyHost: true},
			in.psql("", 0, "verify-full", "select 'ip-ssl=' || ssl from pg_stat_ssl where pid = pg_backend_pid()"), o)
		rs.Tunnel = in.tunnelTo(literalIP)
		var info executor.RunInfo
		rs.OnStart = func(i executor.RunInfo) { info = i }
		res, err := e.Run(ctx, rs)
		if err != nil || res.ExitCode != 0 || !strings.Contains(o.stdout.String(), "ip-ssl=t") {
			t.Fatalf("res %+v err %v\n%s", res, err, o)
		}
		if info.Proxy.Addr().String() != literalIP || info.Subnet.String() != "10.99.0.0/29" {
			t.Fatalf("run info %+v", info)
		}
	})

	t.Run("verify-full to a DNS name through the SSH bastion", func(t *testing.T) {
		o := &output{}
		rs := in.spec(in.runID(), dnsTarget, in.psql("", 0, "verify-full", "select 'tun-ssl=' || ssl from pg_stat_ssl where pid = pg_backend_pid()"), o)
		rs.Tunnel = in.tunnelTo("db.bekup.test")
		res, err := e.Run(ctx, rs)
		if err != nil || res.ExitCode != 0 || !strings.Contains(o.stdout.String(), "tun-ssl=t") {
			t.Fatalf("res %+v err %v\n%s", res, err, o)
		}
	})

	t.Run("stdin payload and large chunked output", func(t *testing.T) {
		o := &output{}
		var chunks []int
		rs := in.spec(in.runID(), dnsTarget, func(ep executor.ClientEndpoint) (engine.Command, error) {
			c, err := in.psql("", 0, "verify-full", "")(ep)
			// No -c: psql executes the payload that follows the preamble.
			c.Argv = []string{"psql", "-X", "-w", "-A", "-t", "-v", "ON_ERROR_STOP=1", c.Argv[len(c.Argv)-1]}
			c.Stdin = true
			return c, err
		}, o)
		rs.Stdout = func(b []byte) error {
			chunks = append(chunks, len(b))
			return o.Stdout(b)
		}
		rs.Stdin = strings.NewReader("select 'payload-ok';\nselect repeat('x', 3000000);\n")
		res, err := e.Run(ctx, rs)
		if err != nil || res.ExitCode != 0 || !strings.Contains(o.stdout.String(), "payload-ok") || res.StdoutBytes < 3000000 {
			t.Fatalf("res %+v err %v", res, err)
		}
		for _, n := range chunks {
			if n > executor.DefaultChunkBytes {
				t.Fatalf("chunk of %d bytes", n)
			}
		}
		if len(chunks) < 3000000/executor.DefaultChunkBytes {
			t.Fatalf("output not chunked: %v", chunks)
		}
	})

	t.Run("direct IP-literal with verify-full fails closed", func(t *testing.T) {
		o := &output{}
		rs := in.spec(in.runID(), executor.Target{Host: h.BastionIP, Port: 5432, VerifyHost: true}, in.psql("", 0, "verify-full", "select 1"), o)
		if _, err := e.Run(ctx, rs); !errors.Is(err, executor.ErrUnmappableTarget) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("isolation", func(t *testing.T) {
		// Run A stays connected while run B probes.
		aCtx, cancelA := context.WithCancel(ctx)
		defer cancelA()
		aInfo := make(chan executor.RunInfo, 1)
		aDone := make(chan error, 1)
		go func() {
			o := &output{}
			rs := in.spec(run1, dnsTarget, in.psql("", 0, "verify-full", "select pg_sleep(120)"), o)
			rs.OnStart = func(i executor.RunInfo) { aInfo <- i }
			_, err := e.Run(aCtx, rs)
			aDone <- err
		}()
		var a executor.RunInfo
		select {
		case a = <-aInfo:
		case err := <-aDone:
			t.Fatalf("run A ended early: %v", err)
		}
		before := accepted.Load()

		// Each probe is a run whose client dials something other than its
		// target; host "{proxy}" / "{gateway}" are the run's own per-run
		// addresses (an IP-literal target without host verification makes
		// the client dial the proxy address).
		probe := func(t *testing.T, host string, port int, want ...string) {
			t.Helper()
			o := &output{}
			target := dnsTarget
			cmd := in.psql(host, port, "disable", "select 1")
			switch host {
			case "{proxy}":
				target = executor.Target{Host: h.BastionIP, Port: 5432}
				cmd = func(ep executor.ClientEndpoint) (engine.Command, error) {
					return in.psql(ep.Host, port, "disable", "select 1")(ep)
				}
			case "{gateway}":
				target = executor.Target{Host: h.BastionIP, Port: 5432}
				cmd = func(ep executor.ClientEndpoint) (engine.Command, error) {
					return in.psql(netip.MustParseAddr(ep.Host).Prev().String(), port, "disable", "select 1")(ep)
				}
			}
			res, err := e.Run(ctx, in.spec(run2, target, cmd, o))
			check(t, res, err, o, want)
		}
		unreachable := []string{"Network unreachable", "No route to host", "Host is unreachable", "timeout expired"}
		t.Run("internet", func(t *testing.T) { probe(t, "1.1.1.1", 443, unreachable...) })
		t.Run("dns", func(t *testing.T) { probe(t, "example.com", 443, "could not translate host name") })
		t.Run("runner grpc on the control address", func(t *testing.T) { probe(t, ctlIP.String(), 7443, unreachable...) })
		t.Run("runner grpc on the per-run address", func(t *testing.T) { probe(t, "{proxy}", 7443, "Connection refused") })
		t.Run("runner wildcard listener", func(t *testing.T) {
			probe(t, "{proxy}", 7444, "server closed the connection", "Connection reset", "Connection refused")
		})
		t.Run("gateway", func(t *testing.T) { probe(t, "{gateway}", 5432, unreachable...) })
		t.Run("other run's proxy", func(t *testing.T) { probe(t, a.Proxy.Addr().String(), int(a.Proxy.Port()), unreachable...) })
		t.Run("other run's container", func(t *testing.T) { probe(t, a.ClientIP.String(), 5432, unreachable...) })
		t.Run("target without the proxy", func(t *testing.T) { probe(t, h.BastionIP, 5432, unreachable...) })
		if n := accepted.Load() - before; n != 0 {
			t.Fatalf("the runner's gRPC listeners accepted %d connections from dump containers", n)
		}
		cancelA()
		if err := <-aDone; !errors.Is(err, context.Canceled) {
			t.Fatalf("run A: %v", err)
		}
	})

	t.Run("timeout kills and cleans up", func(t *testing.T) {
		id := in.runID()
		o := &output{}
		rs := in.spec(id, dnsTarget, in.psql("", 0, "verify-full", "select pg_sleep(120)"), o)
		rs.Timeout = 5 * time.Second
		start := time.Now()
		if _, err := e.Run(ctx, rs); !errors.Is(err, executor.ErrTimeout) {
			t.Fatalf("err = %v\n%s", err, o)
		}
		if time.Since(start) > 40*time.Second {
			t.Fatal("kill took too long")
		}
		requireNoRun(ctx, t, d, e.SelfID(), id)
	})

	t.Run("orphan reaper", func(t *testing.T) {
		id := in.runID()
		name := executor.NetworkPrefix + id
		labels := map[string]string{spec.LabelManaged: "true", spec.LabelRunID: id, executor.LabelRunner: e.SelfID()}
		sub := netip.MustParsePrefix("10.214.250.0/28")
		netID, err := d.NetworkCreate(ctx, executor.NetworkRequest{Name: name, Subnet: sub, Gateway: sub.Addr().Next(),
			Internal: true, Options: executor.NetworkOptions(), Labels: labels})
		if err != nil {
			t.Fatal(err)
		}
		if err := d.NetworkConnect(ctx, netID, e.SelfID(), sub.Addr().Next().Next()); err != nil {
			t.Fatal(err)
		}
		sp, err := spec.Build(spec.Params{RunID: id, Entry: entry, Command: mustCmd(t, in.psql("x", 1, "disable", "select 1")),
			Network: name, Limits: spec.LimitsFromConfig(cfg.Docker)})
		if err != nil {
			t.Fatal(err)
		}
		sp.Config.Labels[executor.LabelRunner] = e.SelfID()
		if _, err := d.ContainerCreate(ctx, name, sp); err != nil {
			t.Fatal(err)
		}
		if err := e.Reap(ctx, true); err != nil {
			t.Fatal(err)
		}
		requireNoRun(ctx, t, d, e.SelfID(), id)
	})
}

func mustCmd(t *testing.T, f func(executor.ClientEndpoint) (engine.Command, error)) engine.Command {
	t.Helper()
	c, err := f(executor.ClientEndpoint{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func check(t *testing.T, res executor.Result, err error, o *output, want []string) {
	t.Helper()
	if err != nil {
		t.Fatalf("run error: %v\n%s", err, o)
	}
	if res.ExitCode == 0 {
		t.Fatalf("probe succeeded\n%s", o)
	}
	s := o.String()
	for _, w := range want {
		if strings.Contains(s, w) {
			t.Logf("blocked as expected: %s", strings.Join(o.lines, " | "))
			return
		}
	}
	t.Fatalf("unexpected failure (want one of %q)\n%s", want, s)
}

// requireNoRun checks that no container or network of the run is left and
// the runner is not attached to a per-run network of it.
func requireNoRun(ctx context.Context, t *testing.T, d executor.Docker, selfID, runID string) {
	t.Helper()
	cs, err := d.ContainerList(ctx, spec.LabelRunID+"="+runID)
	if err != nil {
		t.Fatal(err)
	}
	ns, err := d.NetworkList(ctx, spec.LabelRunID+"="+runID)
	if err != nil {
		t.Fatal(err)
	}
	self, err := d.ContainerInspect(ctx, selfID)
	if err != nil {
		t.Fatal(err)
	}
	var attached []string
	for name := range self.Networks {
		if strings.HasSuffix(name, runID) {
			attached = append(attached, name)
		}
	}
	if len(cs) != 0 || len(ns) != 0 || len(attached) != 0 {
		t.Fatalf("leftovers of %s: %d containers, %d networks, attached %v", runID, len(cs), len(ns), attached)
	}
}
