package runner

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/dbprobe"
	"github.com/jlentink/bekup/internal/runner/executor"
	"github.com/jlentink/bekup/internal/runner/pinning"
	"github.com/jlentink/bekup/internal/runner/targets"
	"github.com/jlentink/bekup/internal/runner/validate"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/tunnel"
)

const (
	opsRunID    = "0b7f5a3c-2c1e-4d7a-9a51-8c3f0e6d2b19"
	opsHost     = "db.example.test"
	opsPassword = "Op$-Secret-Password-1"
)

// --- fakes ------------------------------------------------------------------

type fakeConn struct {
	raw       string
	version   engine.Version
	dbs       []string
	nonInnoDB int
	err       error
}

func (c *fakeConn) ServerVersion(context.Context) (string, engine.Version, error) {
	return c.raw, c.version, c.err
}

func (c *fakeConn) Databases(context.Context) ([]string, int, error) {
	return slices.Clone(c.dbs), 0, c.err
}

func (c *fakeConn) NonInnoDBTables(context.Context, string) (int, error) { return c.nonInnoDB, nil }
func (c *fakeConn) Close() error                                         { return nil }

type fakePins struct {
	mu        sync.Mutex
	preflight int
	err       error
	warnings  []string
}

func (p *fakePins) Preflight(context.Context, pinning.Target, []validate.Pin) (pinning.Report, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.preflight++
	return pinning.Report{Warnings: p.warnings}, p.err
}

func (p *fakePins) TrustAnchor([]validate.Pin) ([]byte, error) {
	return []byte("-----BEGIN CERTIFICATE-----\nPINNED\n-----END CERTIFICATE-----\n"), nil
}

// call is one recorded executor run.
type call struct {
	spec     executor.RunSpec
	cmd      engine.Command
	pgpass   string
	mycnf    string
	ca       string
	stdin    []byte
	stdinErr error
}

// fakeExec runs scripted "containers": script[i] handles run i.
type fakeExec struct {
	mu      sync.Mutex
	calls   []*call
	script  []func(ctx context.Context, c *call) (executor.Result, error)
	present bool
	pullErr error
}

func (f *fakeExec) Run(ctx context.Context, rs executor.RunSpec) (executor.Result, error) {
	c := &call{spec: rs}
	ep := executor.ClientEndpoint{Host: rs.Target.Host, Port: rs.Target.Port}
	cmd, err := rs.Command(ep)
	if err != nil {
		return executor.Result{}, err
	}
	c.cmd = cmd
	if m := rs.Material; m != nil {
		c.pgpass, c.mycnf, c.ca = string(m.PGPass), string(m.MyCnf), string(m.CA)
	}
	f.mu.Lock()
	i := len(f.calls)
	f.calls = append(f.calls, c)
	var fn func(context.Context, *call) (executor.Result, error)
	if i < len(f.script) {
		fn = f.script[i]
	}
	f.mu.Unlock()
	if rs.Stdin != nil {
		c.stdin, c.stdinErr = io.ReadAll(rs.Stdin)
	}
	if fn == nil {
		return executor.Result{}, nil
	}
	return fn(ctx, c)
}

func (f *fakeExec) PrePull(context.Context, engine.Entry) (bool, error) { return f.present, f.pullErr }

func (f *fakeExec) Calls() []*call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// --- fixtures ---------------------------------------------------------------

type opsEnv struct {
	ops    *operations
	exec   *fakeExec
	pins   *fakePins
	conn   *fakeConn
	logs   *syncBuffer
	openFn func(context.Context, dbprobe.Params) (dbprobe.Conn, error)
	opened []dbprobe.Params
	mu     sync.Mutex
}

func newOpsEnv(t *testing.T) *opsEnv {
	t.Helper()
	cat, err := engine.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	guard, err := targets.New(targets.Options{
		AllowCIDRs: []string{"10.20.0.0/16"},
		Lookup: func(_ context.Context, _, host string) ([]netip.Addr, error) {
			if host == opsHost {
				return []netip.Addr{netip.MustParseAddr("10.20.0.5")}, nil
			}
			return nil, errors.New("no such host")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	val, err := validate.New(cat, guard)
	if err != nil {
		t.Fatal(err)
	}
	env := &opsEnv{
		exec: &fakeExec{},
		pins: &fakePins{},
		conn: &fakeConn{raw: "16.4", version: engine.Version{Major: 16, Minor: 4}, dbs: []string{"app", "postgres"}},
		logs: &syncBuffer{},
	}
	log := slog.New(slog.NewTextHandler(env.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	env.ops = newOperations(val, env.exec, env.pins, log)
	env.ops.swapGap = time.Millisecond
	env.ops.connect = func(context.Context, pinning.Upstream) (pinning.DialFunc, func(), error) {
		return func(context.Context) (net.Conn, error) { return nil, errors.New("not dialed in unit tests") }, func() {}, nil
	}
	env.ops.clientTLS = func(host string, _ []validate.Pin) (*tls.Config, error) {
		return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}, nil
	}
	env.ops.open = func(ctx context.Context, p dbprobe.Params) (dbprobe.Conn, error) {
		env.mu.Lock()
		env.opened = append(env.opened, p)
		fn := env.openFn
		env.mu.Unlock()
		if fn != nil {
			return fn(ctx, p)
		}
		return env.conn, nil
	}
	t.Cleanup(func() {
		if strings.Contains(env.logs.String(), opsPassword) {
			t.Error("the runner log contains the database password")
		}
	})
	return env
}

var (
	leafOnce sync.Once
	leafDER  []byte
	leafSPKI []byte
)

func pinnedLeaf(t *testing.T) ([]byte, []byte) {
	t.Helper()
	leafOnce.Do(func() {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		tpl := &x509.Certificate{
			SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: opsHost}, DNSNames: []string{opsHost},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		}
		leafDER, err = x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		c, err := x509.ParseCertificate(leafDER)
		if err != nil {
			t.Fatal(err)
		}
		s := sha256.Sum256(c.RawSubjectPublicKeyInfo)
		leafSPKI = s[:]
	})
	return leafDER, leafSPKI
}

func pgTarget(t *testing.T, mode runnerpb.TlsMode) *runnerpb.Target {
	t.Helper()
	tg := &runnerpb.Target{
		Engine: runnerpb.Engine_ENGINE_POSTGRES, VersionKey: "17", Host: opsHost, Port: 5432,
		User: "backup", Password: []byte(opsPassword), TlsMode: mode,
	}
	if mode == runnerpb.TlsMode_TLS_MODE_PINNED {
		der, spki := pinnedLeaf(t)
		tg.Pins = []*runnerpb.CertificatePin{{AnchorDer: der, LeafSpkiSha256: spki}}
	}
	return tg
}

func myTarget(kind runnerpb.Engine, key string) *runnerpb.Target {
	return &runnerpb.Target{
		Engine: kind, VersionKey: key, Host: opsHost, Port: 3306,
		User: "backup", Password: []byte(opsPassword), TlsMode: runnerpb.TlsMode_TLS_MODE_DISABLED,
	}
}

func rc() *runnerpb.RunContext { return &runnerpb.RunContext{RunId: opsRunID} }

func wantReason(t *testing.T, err error, code codes.Code, reason runnerpb.Reason) {
	t.Helper()
	if status.Code(err) != code {
		t.Fatalf("code = %v (%v), want %v", status.Code(err), err, code)
	}
	if reason != "" && runnerpb.ErrorReason(err) != reason {
		t.Fatalf("reason = %q (%v), want %q", runnerpb.ErrorReason(err), err, reason)
	}
}

// --- Probe ------------------------------------------------------------------

func TestProbePinned(t *testing.T) {
	env := newOpsEnv(t)
	env.pins.warnings = []string{pinning.WarningExpires30d}
	resp, err := env.ops.probe(t.Context(), &runnerpb.ProbeRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_PINNED)})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetServerVersion() != "16.4" || resp.GetDetectedVersionKey() != "16" || !resp.GetVersionKeyCompatible() {
		t.Fatalf("resp = %v", resp)
	}
	if !slices.Equal(resp.GetDatabases(), []string{"app", "postgres"}) {
		t.Fatalf("databases = %v", resp.GetDatabases())
	}
	if !slices.Contains(resp.GetWarnings(), pinning.WarningExpires30d) {
		t.Fatalf("warnings = %v", resp.GetWarnings())
	}
	if env.pins.preflight != 1 {
		t.Fatalf("preflight calls = %d", env.pins.preflight)
	}
	p := env.opened[0]
	if p.TLS == nil || p.TLS.ServerName != opsHost || p.Host != opsHost || string(p.Password) != opsPassword {
		t.Fatalf("driver params: %+v", p)
	}
	if !strings.Contains(env.logs.String(), "runner_rpc_audit") {
		t.Fatal("no audit line")
	}
}

func TestProbeTLSModes(t *testing.T) {
	env := newOpsEnv(t)
	if _, err := env.ops.probe(t.Context(), &runnerpb.ProbeRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED)}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.ops.probe(t.Context(), &runnerpb.ProbeRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_UNVERIFIED)}); err != nil {
		t.Fatal(err)
	}
	if env.pins.preflight != 0 {
		t.Fatal("pre-flight ran for an unpinned target")
	}
	if env.opened[0].TLS != nil {
		t.Fatal("DISABLED used TLS")
	}
	if c := env.opened[1].TLS; c == nil || !c.InsecureSkipVerify || c.MinVersion != tls.VersionTLS12 {
		t.Fatalf("UNVERIFIED tls = %+v", c)
	}
}

func TestProbeCompat(t *testing.T) {
	env := newOpsEnv(t)
	// A server newer than every allowlisted PostgreSQL client.
	env.conn.version, env.conn.raw = engine.Version{Major: 99}, "99.0"
	resp, err := env.ops.probe(t.Context(), &runnerpb.ProbeRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED)})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetDetectedVersionKey() != "" || resp.GetVersionKeyCompatible() || !slices.Contains(resp.GetWarnings(), warnNoClient) {
		t.Fatalf("resp = %v", resp)
	}
	// pg17 client against a pg16 server is compatible; pg16 against 17 not.
	env.conn.version = engine.Version{Major: 17, Minor: 2}
	tg := pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED)
	tg.VersionKey = "16"
	resp, err = env.ops.probe(t.Context(), &runnerpb.ProbeRequest{Ctx: rc(), Target: tg})
	if err != nil || resp.GetVersionKeyCompatible() || resp.GetDetectedVersionKey() != "17" {
		t.Fatalf("resp = %v, %v", resp, err)
	}
	env.conn.version = engine.Version{Major: 9, Minor: 6}
	resp, err = env.ops.probe(t.Context(), &runnerpb.ProbeRequest{Ctx: rc(), Target: tg})
	if err != nil || !slices.Contains(resp.GetWarnings(), warnServerTooOld) {
		t.Fatalf("too old: %v, %v", resp, err)
	}
}

// TestProbeNormalizedErrors: every failure category has a fixed code,
// reason and message; refused / filtered / timed out look the same.
func TestProbeNormalizedErrors(t *testing.T) {
	refused := &dbprobe.Error{Reason: runnerpb.ReasonUnreachable, Detail: "connect"}
	tests := []struct {
		name    string
		connErr error
		pinErr  error
		openErr error
		code    codes.Code
		reason  runnerpb.Reason
	}{
		{"refused", nil, nil, refused, codes.Unavailable, runnerpb.ReasonUnreachable},
		{"preflight unreachable", nil, pinning.ErrUnreachable, nil, codes.Unavailable, runnerpb.ReasonUnreachable},
		{"preflight timeout", nil, context.DeadlineExceeded, nil, codes.Unavailable, runnerpb.ReasonUnreachable},
		{"preflight protocol", nil, pinning.ErrProtocol, nil, codes.Unavailable, runnerpb.ReasonUnreachable},
		{"preflight handshake", nil, pinning.ErrHandshake, nil, codes.Unavailable, runnerpb.ReasonUnreachable},
		{"tunnel target leg", &tunnel.Error{Kind: tunnel.ErrUnreachable}, nil, nil, codes.Unavailable, runnerpb.ReasonUnreachable},
		{"auth", nil, nil, &dbprobe.Error{Reason: runnerpb.ReasonAuthFailed}, codes.FailedPrecondition, runnerpb.ReasonAuthFailed},
		{"tls", nil, pinning.ErrNoTLS, nil, codes.FailedPrecondition, runnerpb.ReasonTLSFailed},
		{"pin", nil, &pinning.MismatchError{Reason: pinning.ReasonLeafSPKI}, nil, codes.FailedPrecondition, runnerpb.ReasonPinMismatch},
		{"pin in driver", nil, nil, &dbprobe.Error{Reason: runnerpb.ReasonPinMismatch, Detail: pinning.ReasonUntrusted}, codes.FailedPrecondition, runnerpb.ReasonPinMismatch},
		{"permission", nil, nil, &dbprobe.Error{Reason: runnerpb.ReasonPermission}, codes.FailedPrecondition, runnerpb.ReasonPermission},
		{"timeout", nil, nil, &dbprobe.Error{Reason: runnerpb.ReasonTimeout}, codes.DeadlineExceeded, runnerpb.ReasonTimeout},
		{"ssh host key", &tunnel.HostKeyMismatchError{}, nil, nil, codes.FailedPrecondition, runnerpb.ReasonSSHHostKey},
		{"denied", targets.ErrDenied, nil, nil, codes.PermissionDenied, ""},
	}
	msgs := map[runnerpb.Reason]string{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newOpsEnv(t)
			env.pins.err = tt.pinErr
			if tt.connErr != nil {
				err := tt.connErr
				env.ops.connect = func(context.Context, pinning.Upstream) (pinning.DialFunc, func(), error) {
					return nil, func() {}, err
				}
			}
			if tt.openErr != nil {
				err := tt.openErr
				env.openFn = func(context.Context, dbprobe.Params) (dbprobe.Conn, error) { return nil, err }
			}
			_, err := env.ops.probe(t.Context(), &runnerpb.ProbeRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_PINNED)})
			wantReason(t, err, tt.code, tt.reason)
			msg := status.Convert(err).Message()
			if prev, ok := msgs[tt.reason]; ok && prev != msg {
				t.Fatalf("reason %s has two messages: %q / %q", tt.reason, prev, msg)
			}
			msgs[tt.reason] = msg
		})
	}
}

// --- Dump -------------------------------------------------------------------

func TestDumpPostgres(t *testing.T) {
	env := newOpsEnv(t)
	env.exec.script = []func(context.Context, *call) (executor.Result, error){
		func(_ context.Context, c *call) (executor.Result, error) {
			c.spec.Log("pg_dump: dumping contents of table public.t")
			for range 3 {
				if err := c.spec.Stdout(bytes.Repeat([]byte("D"), 1000)); err != nil {
					return executor.Result{}, err
				}
			}
			return executor.Result{ExitCode: 0}, nil
		},
	}
	var msgs []*runnerpb.DumpResponse
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	err := env.ops.dump(ctx, &runnerpb.DumpRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_PINNED), Database: "app"},
		func(m *runnerpb.DumpResponse) error { msgs = append(msgs, m); return nil })
	if err != nil {
		t.Fatal(err)
	}
	var data int
	var logs []string
	for _, m := range msgs[:len(msgs)-1] {
		switch {
		case m.GetChunk() != nil:
			data += len(m.GetChunk().GetData())
		case m.GetLog() != nil:
			logs = append(logs, m.GetLog().GetText())
		default:
			t.Fatalf("unexpected message before the result: %v", m)
		}
	}
	res := msgs[len(msgs)-1].GetResult()
	if res == nil || res.GetBytes() != 3000 || data != 3000 || res.GetExitCode() != 0 || res.GetVerified() {
		t.Fatalf("result = %v (data %d)", res, data)
	}
	if len(logs) != 1 {
		t.Fatalf("logs = %v", logs)
	}
	c := env.exec.Calls()[0]
	if c.cmd.Argv[0] != "pg_dump" || !strings.Contains(strings.Join(c.cmd.Argv, " "), "dbname='app'") {
		t.Fatalf("argv = %v", c.cmd.Argv)
	}
	if !strings.HasPrefix(c.pgpass, opsHost+":5432:*:backup:") || !strings.Contains(c.ca, "PINNED") {
		t.Fatalf("material pgpass=%q ca=%q", c.pgpass, c.ca)
	}
	if !c.spec.Target.VerifyHost || c.spec.Tunnel != nil || c.spec.Timeout <= 0 {
		t.Fatalf("spec = %+v", c.spec)
	}
	if !slices.ContainsFunc(c.spec.Redact, func(b []byte) bool { return string(b) == opsPassword }) {
		t.Fatal("password not redacted from tool output")
	}
}

func TestDumpChecks(t *testing.T) {
	env := newOpsEnv(t)
	send := func(*runnerpb.DumpResponse) error { return nil }
	err := env.ops.dump(t.Context(), &runnerpb.DumpRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), Database: "missing"}, send)
	wantReason(t, err, codes.NotFound, runnerpb.ReasonDatabaseNotFound)

	// pg_dump 16 must not dump a PostgreSQL 17 server.
	env.conn.version = engine.Version{Major: 17}
	tg := pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED)
	tg.VersionKey = "16"
	err = env.ops.dump(t.Context(), &runnerpb.DumpRequest{Ctx: rc(), Target: tg, Database: "app"}, send)
	wantReason(t, err, codes.FailedPrecondition, runnerpb.ReasonUnsupportedServer)
	if len(env.exec.Calls()) != 0 {
		t.Fatal("a container ran despite failed checks")
	}

	env.ops.exec = nil
	err = env.ops.dump(t.Context(), &runnerpb.DumpRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), Database: "app"}, send)
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("no executor: %v", err)
	}
}

func TestDumpMySQLTrailerAndWarnings(t *testing.T) {
	for _, tc := range []struct {
		kind runnerpb.Engine
		key  string
		raw  string
		tool string
	}{
		{runnerpb.Engine_ENGINE_MYSQL, "8.4", "8.4.3", "mysqldump"},
		{runnerpb.Engine_ENGINE_MARIADB, "11.8", "11.8.2-MariaDB", "mariadb-dump"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			env := newOpsEnv(t)
			eng, err := engine.ForKind(map[runnerpb.Engine]engine.Kind{runnerpb.Engine_ENGINE_MYSQL: engine.KindMySQL, runnerpb.Engine_ENGINE_MARIADB: engine.KindMariaDB}[tc.kind])
			if err != nil {
				t.Fatal(err)
			}
			v, err := eng.ParseServerVersion(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			env.conn.raw, env.conn.version, env.conn.dbs, env.conn.nonInnoDB = tc.raw, v, []string{"shop"}, 2
			body := "-- dump\nINSERT INTO t VALUES (1);\n-- Dump completed on 2026-10-27 10:00:00\n"
			env.exec.script = []func(context.Context, *call) (executor.Result, error){
				func(_ context.Context, c *call) (executor.Result, error) {
					return executor.Result{}, c.spec.Stdout([]byte(body))
				},
				func(_ context.Context, c *call) (executor.Result, error) {
					return executor.Result{}, c.spec.Stdout([]byte("-- dump\nINSERT INTO t VALUES (1);\n"))
				},
			}
			var last *runnerpb.DumpResult
			send := func(m *runnerpb.DumpResponse) error {
				if m.GetResult() != nil {
					last = m.GetResult()
				}
				return nil
			}
			req := &runnerpb.DumpRequest{Ctx: rc(), Target: myTarget(tc.kind, tc.key), Database: "shop"}
			if err := env.ops.dump(t.Context(), req, send); err != nil {
				t.Fatal(err)
			}
			if !last.GetVerified() || !slices.Contains(last.GetWarnings(), warnNonInnoDB) {
				t.Fatalf("result = %v", last)
			}
			c := env.exec.Calls()[0]
			if c.cmd.Argv[0] != tc.tool || c.mycnf == "" || !strings.Contains(c.mycnf, "password=") || c.pgpass != "" {
				t.Fatalf("argv %v mycnf %q", c.cmd.Argv, c.mycnf)
			}
			req = &runnerpb.DumpRequest{Ctx: rc(), Target: myTarget(tc.kind, tc.key), Database: "shop"}
			if err := env.ops.dump(t.Context(), req, send); err != nil {
				t.Fatal(err)
			}
			if last.GetVerified() {
				t.Fatal("a dump without trailer was verified")
			}
		})
	}
}

func TestDumpCancelAndOutputLoss(t *testing.T) {
	env := newOpsEnv(t)
	env.exec.script = []func(context.Context, *call) (executor.Result, error){
		func(context.Context, *call) (executor.Result, error) { return executor.Result{}, context.Canceled },
		func(_ context.Context, c *call) (executor.Result, error) {
			if err := c.spec.Stdout([]byte("x")); err != nil {
				return executor.Result{}, errors.Join(executor.ErrOutput, err)
			}
			return executor.Result{}, nil
		},
	}
	req := func() *runnerpb.DumpRequest {
		return &runnerpb.DumpRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), Database: "app"}
	}
	err := env.ops.dump(t.Context(), req(), func(*runnerpb.DumpResponse) error { return nil })
	if status.Code(err) != codes.Canceled {
		t.Fatalf("cancel: %v", err)
	}
	err = env.ops.dump(t.Context(), req(), func(*runnerpb.DumpResponse) error { return errors.New("stream gone") })
	if status.Code(err) != codes.Aborted {
		t.Fatalf("output loss: %v", err)
	}
}

// --- DumpGlobals ------------------------------------------------------------

func TestDumpGlobalsFallback(t *testing.T) {
	env := newOpsEnv(t)
	env.exec.script = []func(context.Context, *call) (executor.Result, error){
		func(_ context.Context, c *call) (executor.Result, error) {
			if err := c.spec.Stdout([]byte("--\n-- PostgreSQL database cluster dump\n--\n")); err != nil {
				return executor.Result{}, err
			}
			c.spec.Log("pg_dumpall: error: query failed: ERROR:  permission denied for table pg_authid")
			return executor.Result{ExitCode: 1}, nil
		},
		func(_ context.Context, c *call) (executor.Result, error) {
			return executor.Result{}, c.spec.Stdout([]byte("CREATE ROLE app;\n--\n-- PostgreSQL database cluster dump complete\n--\n\n"))
		},
	}
	var data bytes.Buffer
	var runnerLines []string
	var res *runnerpb.DumpResult
	err := env.ops.dumpGlobals(t.Context(), &runnerpb.DumpGlobalsRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED)},
		func(m *runnerpb.DumpGlobalsResponse) error {
			switch {
			case m.GetChunk() != nil:
				data.Write(m.GetChunk().GetData())
			case m.GetLog() != nil && m.GetLog().GetStream() == runnerpb.LogStream_LOG_STREAM_RUNNER:
				runnerLines = append(runnerLines, m.GetLog().GetText())
			case m.GetResult() != nil:
				res = m.GetResult()
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	calls := env.exec.Calls()
	if len(calls) != 2 || slices.Contains(calls[0].cmd.Argv, "--no-role-passwords") || !slices.Contains(calls[1].cmd.Argv, "--no-role-passwords") {
		t.Fatalf("attempts: %d", len(calls))
	}
	if strings.Contains(data.String(), "cluster dump\n--\n--") || !strings.HasPrefix(data.String(), "CREATE ROLE app;") {
		t.Fatalf("first attempt's output leaked into the stream: %q", data.String())
	}
	if !res.GetRolesWithoutPasswords() || !res.GetVerified() || res.GetExitCode() != 0 ||
		int(res.GetBytes()) != data.Len() || //nolint:gosec // test sizes are small
		!slices.Contains(res.GetWarnings(), warnRolesNoPasswords) {
		t.Fatalf("result = %v", res)
	}
	if len(runnerLines) != 1 || !strings.Contains(env.logs.String(), "runner globals fallback") {
		t.Fatalf("fallback not logged: %v", runnerLines)
	}
}

func TestDumpGlobalsNoFallbackOnOtherErrors(t *testing.T) {
	env := newOpsEnv(t)
	env.exec.script = []func(context.Context, *call) (executor.Result, error){
		func(_ context.Context, c *call) (executor.Result, error) {
			if err := c.spec.Stdout([]byte("partial")); err != nil {
				return executor.Result{}, err
			}
			c.spec.Log("pg_dumpall: error: connection failed: FATAL:  password authentication failed")
			return executor.Result{ExitCode: 1}, nil
		},
	}
	var data bytes.Buffer
	var res *runnerpb.DumpResult
	err := env.ops.dumpGlobals(t.Context(), &runnerpb.DumpGlobalsRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED)},
		func(m *runnerpb.DumpGlobalsResponse) error {
			if m.GetChunk() != nil {
				data.Write(m.GetChunk().GetData())
			}
			if m.GetResult() != nil {
				res = m.GetResult()
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(env.exec.Calls()) != 1 || data.String() != "partial" || res.GetExitCode() != 1 || res.GetVerified() || res.GetRolesWithoutPasswords() {
		t.Fatalf("calls %d data %q res %v", len(env.exec.Calls()), data.String(), res)
	}
	// MySQL has no globals.
	err = env.ops.dumpGlobals(t.Context(), &runnerpb.DumpGlobalsRequest{Ctx: rc(), Target: myTarget(runnerpb.Engine_ENGINE_MYSQL, "8.4")},
		func(*runnerpb.DumpGlobalsResponse) error { return nil })
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("mysql globals: %v", err)
	}
}

// --- client streams ---------------------------------------------------------

type fakeClientStream[Req any] struct {
	ctx  context.Context
	msgs []*Req
	err  error // returned after msgs (default io.EOF)
	mu   sync.Mutex
	sent []*runnerpb.RestoreResponse
}

func (s *fakeClientStream[Req]) Context() context.Context { return s.ctx }

func (s *fakeClientStream[Req]) Recv() (*Req, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.msgs) == 0 {
		if s.err != nil {
			return nil, s.err
		}
		return nil, io.EOF
	}
	m := s.msgs[0]
	s.msgs = s.msgs[1:]
	return m, nil
}

func (s *fakeClientStream[Req]) Send(m *runnerpb.RestoreResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, m)
	return nil
}

func verifyMsgs(kind runnerpb.Engine, key string, dk runnerpb.DumpKind, total int, chunks ...[]byte) []*runnerpb.VerifyDumpRequest {
	out := make([]*runnerpb.VerifyDumpRequest, 0, 1+len(chunks))
	out = append(out, &runnerpb.VerifyDumpRequest{Msg: &runnerpb.VerifyDumpRequest_Header{Header: &runnerpb.VerifyDumpHeader{
		Ctx: rc(), Engine: kind, VersionKey: key, Kind: dk, TotalBytes: uint64(total), //nolint:gosec // test sizes
	}}})
	for _, c := range chunks {
		out = append(out, &runnerpb.VerifyDumpRequest{Msg: &runnerpb.VerifyDumpRequest_Chunk{Chunk: &runnerpb.DataChunk{Data: c}}})
	}
	return out
}

func TestVerifyDumpInGo(t *testing.T) {
	env := newOpsEnv(t)
	good := []byte("-- MySQL dump\nINSERT 1;\n-- Dump completed on 2026-10-27  9:00:00\n")
	s := &fakeClientStream[runnerpb.VerifyDumpRequest]{ctx: t.Context(),
		msgs: verifyMsgs(runnerpb.Engine_ENGINE_MYSQL, "8.4", runnerpb.DumpKind_DUMP_KIND_DATABASE, len(good), good[:10], good[10:])}
	resp, err := env.ops.verifyDump(s)
	if err != nil || !resp.GetVerified() {
		t.Fatalf("good: %v %v", resp, err)
	}
	bad := []byte("-- MySQL dump\nINSERT 1;\n")
	s = &fakeClientStream[runnerpb.VerifyDumpRequest]{ctx: t.Context(),
		msgs: verifyMsgs(runnerpb.Engine_ENGINE_MARIADB, "11.8", runnerpb.DumpKind_DUMP_KIND_DATABASE, len(bad), bad)}
	resp, err = env.ops.verifyDump(s)
	if err != nil || resp.GetVerified() || len(resp.GetLogs()) != 1 {
		t.Fatalf("truncated: %v %v", resp, err)
	}
	globals := []byte("CREATE ROLE a;\n--\n-- PostgreSQL database cluster dump complete\n--\n\n")
	s = &fakeClientStream[runnerpb.VerifyDumpRequest]{ctx: t.Context(),
		msgs: verifyMsgs(runnerpb.Engine_ENGINE_POSTGRES, "17", runnerpb.DumpKind_DUMP_KIND_GLOBALS, len(globals), globals)}
	if resp, err = env.ops.verifyDump(s); err != nil || !resp.GetVerified() {
		t.Fatalf("globals: %v %v", resp, err)
	}
	// Fewer bytes than announced.
	s = &fakeClientStream[runnerpb.VerifyDumpRequest]{ctx: t.Context(),
		msgs: verifyMsgs(runnerpb.Engine_ENGINE_MYSQL, "8.4", runnerpb.DumpKind_DUMP_KIND_DATABASE, len(good)+5, good)}
	if _, err = env.ops.verifyDump(s); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("short stream: %v", err)
	}
	if len(env.exec.Calls()) != 0 {
		t.Fatal("a container ran for a Go-only check")
	}
}

func TestVerifyDumpPostgresOffline(t *testing.T) {
	env := newOpsEnv(t)
	archive := bytes.Repeat([]byte("PGDMP"), 1000)
	env.exec.script = []func(context.Context, *call) (executor.Result, error){
		func(_ context.Context, c *call) (executor.Result, error) {
			return executor.Result{}, c.spec.Stdout([]byte(";\n; Archive created at ...\n"))
		},
		func(_ context.Context, c *call) (executor.Result, error) {
			c.spec.Log("pg_restore: error: input file does not appear to be a valid archive")
			return executor.Result{ExitCode: 1}, nil
		},
	}
	s := &fakeClientStream[runnerpb.VerifyDumpRequest]{ctx: t.Context(),
		msgs: verifyMsgs(runnerpb.Engine_ENGINE_POSTGRES, "17", runnerpb.DumpKind_DUMP_KIND_DATABASE, len(archive), archive[:2000], archive[2000:])}
	resp, err := env.ops.verifyDump(s)
	if err != nil || !resp.GetVerified() || resp.GetExitCode() != 0 {
		t.Fatalf("resp %v %v", resp, err)
	}
	c := env.exec.Calls()[0]
	if !c.spec.Offline || c.spec.Material != nil || c.spec.Tunnel != nil || !bytes.Equal(c.stdin, archive) {
		t.Fatalf("spec offline=%v stdin=%d", c.spec.Offline, len(c.stdin))
	}
	if !slices.Equal(c.cmd.Argv, []string{"pg_restore", "--format=custom", "--file=/dev/null"}) {
		t.Fatalf("argv = %v", c.cmd.Argv)
	}
	s = &fakeClientStream[runnerpb.VerifyDumpRequest]{ctx: t.Context(),
		msgs: verifyMsgs(runnerpb.Engine_ENGINE_POSTGRES, "17", runnerpb.DumpKind_DUMP_KIND_DATABASE, 3, []byte("bad"))}
	resp, err = env.ops.verifyDump(s)
	if err != nil || resp.GetVerified() || resp.GetExitCode() != 1 || len(resp.GetLogs()) != 1 {
		t.Fatalf("invalid archive: %v %v", resp, err)
	}
}

// --- Restore ----------------------------------------------------------------

func restoreMsgs(tg *runnerpb.Target, db, src string, dk runnerpb.DumpKind, total int, chunks ...[]byte) []*runnerpb.RestoreRequest {
	out := make([]*runnerpb.RestoreRequest, 0, 1+len(chunks))
	out = append(out, &runnerpb.RestoreRequest{Msg: &runnerpb.RestoreRequest_Header{Header: &runnerpb.RestoreHeader{
		Ctx: rc(), Target: tg, Database: db, SourceVersionKey: src, Kind: dk, TotalBytes: uint64(total), //nolint:gosec // test sizes
	}}})
	for _, c := range chunks {
		out = append(out, &runnerpb.RestoreRequest{Msg: &runnerpb.RestoreRequest_Chunk{Chunk: &runnerpb.DataChunk{Data: c}}})
	}
	return out
}

func TestRestorePostgres(t *testing.T) {
	env := newOpsEnv(t)
	env.conn.dbs = []string{"app", "app__bekup_restore_r1", "postgres"}
	dump := bytes.Repeat([]byte("PGDMP"), 500)
	env.exec.script = []func(context.Context, *call) (executor.Result, error){
		func(_ context.Context, c *call) (executor.Result, error) {
			c.spec.Log("pg_restore: creating TABLE public.t")
			if err := c.spec.Stdout([]byte("tool stdout is not returned")); err != nil {
				return executor.Result{}, err
			}
			return executor.Result{}, nil
		},
	}
	// Dump taken with pg 17, target server 16: the restore client is 17.
	s := &fakeClientStream[runnerpb.RestoreRequest]{ctx: t.Context(),
		msgs: restoreMsgs(pgTarget(t, runnerpb.TlsMode_TLS_MODE_PINNED), "app__bekup_restore_r1", "17", runnerpb.DumpKind_DUMP_KIND_DATABASE, len(dump), dump)}
	if err := env.ops.restore(s); err != nil {
		t.Fatal(err)
	}
	c := env.exec.Calls()[0]
	if c.spec.Entry.Key() != "17" || c.cmd.Argv[0] != "pg_restore" || !slices.Contains(c.cmd.Argv, "--exit-on-error") || !bytes.Equal(c.stdin, dump) {
		t.Fatalf("entry %s argv %v stdin %d", c.spec.Entry.Key(), c.cmd.Argv, len(c.stdin))
	}
	last := s.sent[len(s.sent)-1].GetResult()
	if last == nil || last.GetBytesReceived() != uint64(len(dump)) || last.GetExitCode() != 0 {
		t.Fatalf("result = %v", s.sent)
	}
	for _, m := range s.sent {
		if l := m.GetLog(); l != nil && strings.Contains(l.GetText(), "tool stdout") {
			t.Fatal("tool stdout returned")
		}
	}
}

func TestRestoreFailures(t *testing.T) {
	env := newOpsEnv(t)
	dump := []byte("-- MySQL dump\n")
	env.conn.raw, env.conn.version, env.conn.dbs = "8.4.3", engine.Version{Major: 8, Minor: 4, Patch: 3}, []string{"shop"}
	tg := myTarget(runnerpb.Engine_ENGINE_MYSQL, "8.4")
	s := &fakeClientStream[runnerpb.RestoreRequest]{ctx: t.Context(),
		msgs: restoreMsgs(tg, "missing", "8.4", runnerpb.DumpKind_DUMP_KIND_DATABASE, len(dump), dump)}
	wantReason(t, env.ops.restore(s), codes.NotFound, runnerpb.ReasonDatabaseNotFound)

	// Truncated stream: the container's stdin fails, the run is killed and
	// the validation error is returned.
	env.exec.script = []func(context.Context, *call) (executor.Result, error){
		func(_ context.Context, c *call) (executor.Result, error) {
			if c.stdinErr != nil {
				return executor.Result{}, errors.Join(executor.ErrStdin, c.stdinErr)
			}
			return executor.Result{}, nil
		},
	}
	s = &fakeClientStream[runnerpb.RestoreRequest]{ctx: t.Context(),
		msgs: restoreMsgs(myTarget(runnerpb.Engine_ENGINE_MYSQL, "8.4"), "shop", "8.4", runnerpb.DumpKind_DUMP_KIND_DATABASE, len(dump)+10, dump)}
	if err := env.ops.restore(s); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("truncated: %v", err)
	}
	c := env.exec.Calls()[0]
	argv := strings.Join(c.cmd.Argv, " ")
	for _, f := range []string{"--system-command=OFF", "--local-infile=0", "--database=shop"} {
		if !strings.Contains(argv, f) {
			t.Fatalf("restore argv lacks %s: %v", f, c.cmd.Argv)
		}
	}
	if len(env.exec.Calls()) != 1 {
		t.Fatal("a restore was retried")
	}
}

// --- CreateDatabase / RenameSwap / PrePull --------------------------------------

func TestCreateDatabase(t *testing.T) {
	env := newOpsEnv(t)
	_, err := env.ops.createDatabase(t.Context(), &runnerpb.CreateDatabaseRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), Database: "app"})
	wantReason(t, err, codes.AlreadyExists, runnerpb.ReasonDatabaseExists)
	if _, err := env.ops.createDatabase(t.Context(), &runnerpb.CreateDatabaseRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), Database: "newdb"}); err != nil {
		t.Fatal(err)
	}
	c := env.exec.Calls()[0]
	if c.cmd.Argv[0] != "psql" || !strings.Contains(strings.Join(c.cmd.Argv, " "), `CREATE DATABASE "newdb" TEMPLATE template0`) {
		t.Fatalf("argv = %v", c.cmd.Argv)
	}
	env.exec.script = append(env.exec.script, nil, func(_ context.Context, c *call) (executor.Result, error) {
		c.spec.Log(`ERROR:  permission denied to create database`)
		return executor.Result{ExitCode: 1}, nil
	})
	_, err = env.ops.createDatabase(t.Context(), &runnerpb.CreateDatabaseRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), Database: "other"})
	wantReason(t, err, codes.FailedPrecondition, runnerpb.ReasonPermission)
}

func TestRenameSwap(t *testing.T) {
	ok := func(context.Context, *call) (executor.Result, error) { return executor.Result{}, nil }
	busy := func(_ context.Context, c *call) (executor.Result, error) {
		c.spec.Log(`ERROR:  database "app" is being accessed by other users`)
		return executor.Result{ExitCode: 1}, nil
	}
	req := func(t *testing.T) *runnerpb.RenameSwapDatabaseRequest {
		return &runnerpb.RenameSwapDatabaseRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), Database: "app", TempId: "r1"}
	}
	t.Run("retry then success", func(t *testing.T) {
		env := newOpsEnv(t)
		env.conn.dbs = []string{"app", "app__bekup_restore_r1"}
		env.exec.script = []func(context.Context, *call) (executor.Result, error){ok, busy, ok}
		if _, err := env.ops.renameSwap(t.Context(), req(t)); err != nil {
			t.Fatal(err)
		}
		calls := env.exec.Calls()
		if len(calls) != 3 {
			t.Fatalf("steps = %d", len(calls))
		}
		swap := strings.Join(calls[2].cmd.Argv, " ")
		if !strings.Contains(swap, `RENAME TO "app__bekup_old_r1"`) || !strings.Contains(swap, `"app__bekup_restore_r1" RENAME TO "app"`) {
			t.Fatalf("swap argv = %v", calls[2].cmd.Argv)
		}
	})
	t.Run("failure rolls back", func(t *testing.T) {
		env := newOpsEnv(t)
		env.conn.dbs = []string{"app", "app__bekup_restore_r1"}
		env.exec.script = []func(context.Context, *call) (executor.Result, error){ok, busy, busy, busy, ok}
		_, err := env.ops.renameSwap(t.Context(), req(t))
		wantReason(t, err, codes.FailedPrecondition, runnerpb.ReasonFailed)
		calls := env.exec.Calls()
		if len(calls) != 5 || !strings.Contains(strings.Join(calls[4].cmd.Argv, " "), "ALLOW_CONNECTIONS true") {
			t.Fatalf("steps = %d", len(calls))
		}
	})
	t.Run("checks", func(t *testing.T) {
		env := newOpsEnv(t)
		env.conn.dbs = []string{"app"}
		_, err := env.ops.renameSwap(t.Context(), req(t))
		wantReason(t, err, codes.NotFound, runnerpb.ReasonDatabaseNotFound)
		env.conn.dbs = []string{"app", "app__bekup_restore_r1", "app__bekup_old_r1"}
		_, err = env.ops.renameSwap(t.Context(), req(t))
		wantReason(t, err, codes.AlreadyExists, runnerpb.ReasonDatabaseExists)
		_, err = env.ops.renameSwap(t.Context(), &runnerpb.RenameSwapDatabaseRequest{Ctx: rc(), Target: myTarget(runnerpb.Engine_ENGINE_MYSQL, "8.4"), Database: "app", TempId: "r1"})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("mysql: %v", err)
		}
		if len(env.exec.Calls()) != 0 {
			t.Fatal("container ran")
		}
	})
}

func TestPrePull(t *testing.T) {
	env := newOpsEnv(t)
	env.exec.present = true
	resp, err := env.ops.prePull(t.Context(), &runnerpb.PrePullRequest{Engine: runnerpb.Engine_ENGINE_POSTGRES, VersionKey: "17"})
	if err != nil || !resp.GetAlreadyPresent() || !strings.Contains(resp.GetImageRef(), "@sha256:") || strings.Contains(resp.GetImageRef(), ":17") {
		t.Fatalf("resp %v %v", resp, err)
	}
	env.exec.pullErr = executor.ErrImage
	if _, err := env.ops.prePull(t.Context(), &runnerpb.PrePullRequest{Engine: runnerpb.Engine_ENGINE_POSTGRES, VersionKey: "17"}); status.Code(err) != codes.Unavailable {
		t.Fatalf("pull failure: %v", err)
	}
	if _, err := env.ops.prePull(t.Context(), &runnerpb.PrePullRequest{Engine: runnerpb.Engine_ENGINE_POSTGRES, VersionKey: "latest"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("not allowlisted: %v", err)
	}
}

func TestPumpCountsAfterConsumerStops(t *testing.T) {
	msgs := [][]byte{[]byte("aa"), []byte("bb"), []byte("cc")}
	i := 0
	var total int
	p := startPump(t.Context(), func() ([]byte, error) {
		if i == len(msgs) {
			return nil, io.EOF
		}
		i++
		total += 2
		return msgs[i-1], nil
	}, func() error {
		if total != 6 {
			return errors.New("incomplete")
		}
		return nil
	})
	b := make([]byte, 2)
	if _, err := io.ReadFull(p.Reader(), b); err != nil {
		t.Fatal(err)
	}
	if err := p.Finish(t.Context()); err != nil {
		t.Fatal(err)
	}
}
