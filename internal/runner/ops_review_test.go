package runner

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/executor"
	"github.com/jlentink/bekup/internal/runner/pinning"
	"github.com/jlentink/bekup/internal/runner/validate"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/tunnel"
)

// TestFinalSendNeverConcurrent: the executor may return while its output
// reader is still inside a blocked Send; the handler's final message
// waits for it and no Send happens after the final one (run with -race).
func TestFinalSendNeverConcurrent(t *testing.T) {
	env := newOpsEnv(t)
	var late sync.WaitGroup
	started := make(chan struct{})
	env.exec.script = []func(context.Context, *call) (executor.Result, error){
		func(_ context.Context, c *call) (executor.Result, error) {
			late.Add(1)
			go func() { // the "stuck" reader: keeps writing after Run returned
				defer late.Done()
				close(started)
				for range 200 {
					if err := c.spec.Stdout([]byte("late chunk")); err != nil {
						return
					}
					c.spec.Log("late log line")
				}
			}()
			<-started
			return executor.Result{}, nil
		},
	}
	var inFlight, maxInFlight atomic.Int32
	var finalSeen atomic.Bool
	var afterFinal atomic.Int32
	send := func(m *runnerpb.DumpResponse) error {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		if n > maxInFlight.Load() {
			maxInFlight.Store(n)
		}
		if finalSeen.Load() {
			afterFinal.Add(1)
		}
		if m.GetResult() != nil {
			finalSeen.Store(true)
		}
		time.Sleep(50 * time.Microsecond) // widen the window
		return nil
	}
	err := env.ops.dump(t.Context(), &runnerpb.DumpRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), Database: "app"}, send)
	late.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if maxInFlight.Load() > 1 || afterFinal.Load() != 0 || !finalSeen.Load() {
		t.Fatalf("concurrent sends %d, sends after the result %d, result sent %v", maxInFlight.Load(), afterFinal.Load(), finalSeen.Load())
	}
}

// secretStream is a goroutine-safe stream of credential-carrying messages.
type secretStream struct {
	grpc.ServerStream
	mu  sync.Mutex
	got []*runnerpb.RestoreRequest
}

func (s *secretStream) RecvMsg(m any) error {
	dst, ok := m.(*runnerpb.RestoreRequest)
	if !ok {
		return errors.New("unexpected type")
	}
	dst.Msg = &runnerpb.RestoreRequest_Header{Header: &runnerpb.RestoreHeader{Target: &runnerpb.Target{Password: []byte("secret-pw")}}}
	s.mu.Lock()
	s.got = append(s.got, dst)
	s.mu.Unlock()
	return nil
}

// TestZeroingStreamConcurrentRecv: a pump goroutine may still receive
// while the interceptor zeroes after the handler returned (run with
// -race); every received credential ends up zeroed.
func TestZeroingStreamConcurrentRecv(t *testing.T) {
	ss := &secretStream{}
	var pump sync.WaitGroup
	err := zeroStream(nil, ss, nil, func(_ any, s grpc.ServerStream) error {
		pump.Add(1)
		go func() {
			defer pump.Done()
			for range 3 {
				if err := s.RecvMsg(&runnerpb.RestoreRequest{}); err != nil {
					return
				}
			}
		}()
		return nil // the handler returns while the pump still receives
	})
	pump.Wait()
	if err != nil {
		t.Fatal(err)
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	for _, m := range ss.got {
		if pw := m.GetHeader().GetTarget().GetPassword(); pw != nil && !bytes.Equal(pw, make([]byte, len(pw))) {
			t.Fatalf("credential not zeroed: %q", pw)
		}
	}
}

type fakeFetch struct {
	chain []*x509.Certificate
	err   error
	calls int
}

func (f *fakeFetch) fetch(context.Context, pinning.Target) ([]*x509.Certificate, error) {
	f.calls++
	return f.chain, f.err
}

// TestFetchServerCertificateThroughOps: the capture is validated,
// normalized (runnerpb reasons) and audited like the other target RPCs.
func TestFetchServerCertificateThroughOps(t *testing.T) {
	env := newOpsEnv(t)
	der, _ := pinnedLeaf(t)
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	ff := &fakeFetch{chain: []*x509.Certificate{cert}}
	env.ops.fetch = ff.fetch
	req := func() *runnerpb.FetchServerCertificateRequest {
		return &runnerpb.FetchServerCertificateRequest{Ctx: rc(), Engine: runnerpb.Engine_ENGINE_POSTGRES, Host: opsHost, Port: 5432}
	}
	resp, err := env.ops.fetchServerCertificate(t.Context(), req())
	if err != nil || len(resp.GetChain()) != 1 || resp.GetSuggestedAnchorIndex() != 0 {
		t.Fatalf("resp %v %v", resp, err)
	}
	logs := env.logs.String()
	if !strings.Contains(logs, "runner_rpc_audit") || !strings.Contains(logs, "rpc=FetchServerCertificate") {
		t.Fatalf("no audit line:\n%s", logs)
	}
	for in, want := range map[error]runnerpb.Reason{
		pinning.ErrNoTLS:          runnerpb.ReasonTLSFailed,
		pinning.ErrHandshake:      runnerpb.ReasonUnreachable,
		pinning.ErrProtocol:       runnerpb.ReasonUnreachable,
		pinning.ErrServerRejected: runnerpb.ReasonUnreachable,
		pinning.ErrUnreachable:    runnerpb.ReasonUnreachable,
	} {
		ff.err = in
		_, err := env.ops.fetchServerCertificate(t.Context(), req())
		if runnerpb.ErrorReason(err) != want {
			t.Errorf("%v: %v, want %s", in, err, want)
		}
	}
	if !strings.Contains(env.logs.String(), "reason=TARGET_TLS_FAILED") {
		t.Fatal("audit line without the reason")
	}
	r := req()
	r.Host = "127.0.0.1"
	calls := ff.calls
	if _, err := env.ops.fetchServerCertificate(t.Context(), r); status.Code(err) != codes.PermissionDenied || ff.calls != calls {
		t.Fatalf("denied target: %v", err)
	}
	// Without a checker the RPC fails closed.
	plain := newOperations(env.ops.val, env.exec, env.pins, env.ops.log)
	plain.connect = env.ops.connect
	if _, err := plain.fetchServerCertificate(t.Context(), req()); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("no checker: %v", err)
	}
}

// TestTargetStatusPinMismatch: a pin mismatch carries the failed check.
func TestTargetStatusPinMismatch(t *testing.T) {
	err := targetStatus(&pinning.MismatchError{Reason: pinning.ReasonHostname})
	if check, ok := runnerpb.PinMismatchCheck(err); !ok || check != pinning.ReasonHostname {
		t.Fatalf("check %q %v (%v)", check, ok, err)
	}
}

// TestRestoreGlobalsRestricted: psql gets the runner's \restrict line and
// the dump's own \restrict / \unrestrict lines are removed.
func TestRestoreGlobalsRestricted(t *testing.T) {
	env := newOpsEnv(t)
	dump := []byte("--\n\\restrict abcDEF123\nCREATE ROLE a;\n\\unrestrict abcDEF123\n-- PostgreSQL database cluster dump complete\n")
	s := &fakeClientStream[runnerpb.RestoreRequest]{ctx: t.Context(),
		msgs: restoreMsgs(pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), "", "17", runnerpb.DumpKind_DUMP_KIND_GLOBALS, len(dump), dump)}
	if err := env.ops.restore(s); err != nil {
		t.Fatal(err)
	}
	c := env.exec.Calls()[0]
	if c.cmd.Argv[0] != "psql" || !engine.IsPSQLRestrictPrefix(c.cmd.StdinPrefix) {
		t.Fatalf("argv %v prefix %q", c.cmd.Argv, c.cmd.StdinPrefix)
	}
	if want := "--\nCREATE ROLE a;\n-- PostgreSQL database cluster dump complete\n"; string(c.stdin) != want {
		t.Fatalf("stdin %q", c.stdin)
	}
}

// TestRenameSwapStepNames: every container of a swap has its own step.
func TestRenameSwapStepNames(t *testing.T) {
	env := newOpsEnv(t)
	env.conn.dbs = []string{"app", "app__bekup_restore_r1"}
	busy := func(_ context.Context, c *call) (executor.Result, error) {
		c.spec.Log(`ERROR:  database "app" is being accessed by other users`)
		return executor.Result{ExitCode: 1}, nil
	}
	ok := func(context.Context, *call) (executor.Result, error) { return executor.Result{}, nil }
	env.exec.script = []func(context.Context, *call) (executor.Result, error){ok, busy, busy, busy, ok}
	_, err := env.ops.renameSwap(t.Context(), &runnerpb.RenameSwapDatabaseRequest{Ctx: rc(), Target: pgTarget(t, runnerpb.TlsMode_TLS_MODE_DISABLED), Database: "app", TempId: "r1"})
	if err == nil {
		t.Fatal("expected the swap to fail")
	}
	calls := env.exec.Calls()
	steps := make([]string, 0, len(calls))
	for _, c := range calls {
		steps = append(steps, c.spec.Step)
		if !engine.IsPSQLRestrictPrefix(c.cmd.StdinPrefix) {
			t.Fatalf("step %s without the restrict prefix", c.spec.Step)
		}
	}
	if strings.Join(steps, ",") != "prepare,swap1,swap2,swap3,rollback" {
		t.Fatalf("steps %v", steps)
	}
}

// TestWarnUnrestrictedBastion: a bastion without bastion_targets is
// reported once.
func TestWarnUnrestrictedBastion(t *testing.T) {
	env := newOpsEnv(t)
	s := &validate.SSH{Bastion: validate.Endpoint{Host: "bastion.example.test", Port: 22, Addrs: []netip.Addr{netip.MustParseAddr("10.20.0.9")}}}
	env.ops.warnUnrestrictedBastion(t.Context(), s)
	env.ops.warnUnrestrictedBastion(t.Context(), s)
	if n := strings.Count(env.logs.String(), "runner_bastion_unrestricted"); n != 1 {
		t.Fatalf("warnings %d", n)
	}
}

// TestListImagesIncludesExtra: the operator's extra images are listed
// (and marked) next to the embedded catalog.
func TestListImagesIncludesExtra(t *testing.T) {
	env := newOpsEnv(t)
	cat, err := engine.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	extra := "schema_version = 1\n\n[[images]]\nengine = \"postgres\"\nkey = \"19\"\n" +
		"image = \"registry.example.com/mirror/postgres:19.1-alpine@sha256:" + strings.Repeat("a", 64) + "\"\n" +
		"legacy = false\nbinaries = { dump = \"pg_dump\", dump_globals = \"pg_dumpall\", restore = \"pg_restore\", client = \"psql\" }\n"
	merged, err := cat.WithExtra([]byte(extra))
	if err != nil {
		t.Fatal(err)
	}
	val, err := validate.New(merged, env.ops.val.Guard())
	if err != nil {
		t.Fatal(err)
	}
	env.ops.val = val
	resp, err := env.ops.listImages(t.Context(), &runnerpb.ListImagesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetImages()) != len(merged.Entries()) {
		t.Fatalf("images %d, want %d", len(resp.GetImages()), len(merged.Entries()))
	}
	found := false
	for _, im := range resp.GetImages() {
		if im.GetVersionKey() == "19" && im.GetEngine() == runnerpb.Engine_ENGINE_POSTGRES {
			found = im.GetExtra() && im.GetClientVersion() == "19.1.0" &&
				im.GetImageRef() == "registry.example.com/mirror/postgres@sha256:"+strings.Repeat("a", 64)
		}
	}
	if !found {
		t.Fatalf("extra image missing or wrong: %v", resp.GetImages())
	}
	if !strings.Contains(env.logs.String(), "rpc=ListImages") {
		t.Fatal("no audit line")
	}
}

// TestTargetStatusTunnelError: a host-key mismatch from tunnel.Dial (an
// *tunnel.Error wrapping the sshpolicy details) is SSH_HOST_KEY_MISMATCH.
func TestTargetStatusTunnelError(t *testing.T) {
	err := fmt.Errorf("connect: %w", &tunnel.Error{Kind: tunnel.ErrHostKeyMismatch, Op: "dial", Addr: "10.20.0.9:22"})
	if r := runnerpb.ErrorReason(targetStatus(err)); r != runnerpb.ReasonSSHHostKey {
		t.Fatalf("reason %q", r)
	}
	if r := runnerpb.ErrorReason(targetStatus(&tunnel.Error{Kind: tunnel.ErrUnsupportedHostKey})); r != runnerpb.ReasonSSHHostKeyUnsupported {
		t.Fatalf("unsupported: %q", r)
	}
	if strings.Contains(status.Convert(targetStatus(err)).Message(), "10.20.0.9") {
		t.Fatal("address in the status message")
	}
}
