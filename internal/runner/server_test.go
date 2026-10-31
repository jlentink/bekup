package runner

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/runnerclient"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// pki is a throw-away private CA with runner, serve and worker certificates.
type pki struct{ l certs.Layout }

func newPKI(t *testing.T) pki {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "pki")
	if _, err := certs.Init(certs.InitOptions{
		Dir:              dir,
		Components:       []string{certs.ComponentRunner, certs.ComponentServe, certs.ComponentWorker},
		AllowUnencrypted: true,
	}); err != nil {
		t.Fatal(err)
	}
	return pki{l: certs.Layout{Dir: dir}}
}

func (p pki) clientTLS(t *testing.T, component string) *tls.Config {
	t.Helper()
	c, err := certs.ClientTLSConfig(p.l.CertFile(component), p.l.KeyFile(component), p.l.CACert(), "runner")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// syncBuffer is a log sink safe for concurrent use.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type testServer struct {
	addr   string
	pki    pki
	logs   *syncBuffer
	cancel context.CancelFunc
	done   chan error
	srv    *Server
}

type serverOpt func(*Options)

func startServer(t *testing.T, opts ...serverOpt) *testServer {
	t.Helper()
	p := newPKI(t)
	cfg := config.DefaultRunner()
	cfg.Listen.Address = "127.0.0.1:0"
	cfg.Listen.HealthSocket = ""
	cfg.TLS.CertFile = p.l.CertFile(certs.ComponentRunner)
	cfg.TLS.KeyFile = p.l.KeyFile(certs.ComponentRunner)
	cfg.TLS.ClientCAFile = p.l.CACert()
	cfg.TLS.AllowedClientSANs = []string{"spiffe://bekup/serve"}
	logs := &syncBuffer{}
	o := Options{
		Config:  &cfg,
		Logger:  slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Version: "1.2.3",
	}
	for _, f := range opts {
		f(&o)
	}
	srv, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	var lc net.ListenConfig
	lis, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	ts := &testServer{addr: lis.Addr().String(), pki: p, logs: logs, cancel: cancel, done: make(chan error, 1), srv: srv}
	go func() { ts.done <- srv.Serve(ctx, lis) }()
	t.Cleanup(func() { ts.stop(t) })
	return ts
}

func (ts *testServer) stop(t *testing.T) {
	t.Helper()
	if ts.cancel == nil {
		return
	}
	ts.cancel()
	ts.cancel = nil
	select {
	case err := <-ts.done:
		if err != nil {
			t.Errorf("serve: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("server did not stop")
	}
}

func (ts *testServer) client(t *testing.T, tlsCfg *tls.Config) *runnerclient.Client {
	t.Helper()
	c, err := runnerclient.NewWithTLS(ts.addr, tlsCfg, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeT(t, c) })
	return c
}

// rawConn is a connection without the runnerclient interceptors (to send
// calls without a deadline).
func (ts *testServer) rawConn(t *testing.T, tlsCfg *tls.Config) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient(ts.addr, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)), grpc.WithNoProxy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeT(t, conn) })
	return conn
}

func ctxTimeout(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestHealthOverMTLS(t *testing.T) {
	ts := startServer(t)
	c := ts.client(t, ts.pki.clientTLS(t, certs.ComponentServe))
	resp, err := c.Health(ctxTimeout(t))
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetCallerIdentity() != "spiffe://bekup/serve" || resp.GetVersion() != "1.2.3" || resp.GetApiVersion() != "v1" ||
		resp.GetDockerStatus() != runnerpb.DockerStatus_DOCKER_STATUS_UNKNOWN || resp.GetMaxConcurrentRuns() != 4 ||
		resp.GetActiveRuns() != 0 || !resp.GetCertificateNotAfter().AsTime().Equal(ts.srv.certEnd) ||
		resp.ImagePinned != nil {
		t.Fatalf("unexpected health: %v", resp)
	}
	if !strings.Contains(ts.logs.String(), `"caller":"spiffe://bekup/serve"`) {
		t.Errorf("call not logged: %s", ts.logs.String())
	}
}

// D-212: Health carries the own image's pinning verdict once it is known
// (unset before), and nothing else about the image.
func TestHealthImagePinned(t *testing.T) {
	for _, tc := range []struct {
		name          string
		pinned, known bool
	}{
		{"unknown", false, false},
		{"pinned", true, true},
		{"unpinned", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := startServer(t, func(o *Options) {
				o.ImagePinned = func() (bool, bool) { return tc.pinned, tc.known }
			})
			resp, err := ts.client(t, ts.pki.clientTLS(t, certs.ComponentServe)).Health(ctxTimeout(t))
			if err != nil {
				t.Fatal(err)
			}
			if (resp.ImagePinned != nil) != tc.known || resp.GetImagePinned() != tc.pinned {
				t.Fatalf("image_pinned = %v", resp.ImagePinned)
			}
		})
	}
}

func TestRejectedClients(t *testing.T) {
	ts := startServer(t)
	other := newPKI(t)
	tls12 := ts.pki.clientTLS(t, certs.ComponentServe)
	tls12.MinVersion, tls12.MaxVersion = tls.VersionTLS12, tls.VersionTLS12
	noCert := ts.pki.clientTLS(t, certs.ComponentServe)
	noCert.Certificates = nil

	for name, cfg := range map[string]*tls.Config{
		"identity not in allowlist": ts.pki.clientTLS(t, certs.ComponentWorker),
		"client from another CA": func() *tls.Config {
			c := other.clientTLS(t, certs.ComponentServe)
			c.RootCAs = ts.pki.clientTLS(t, certs.ComponentServe).RootCAs
			return c
		}(),
		"TLS 1.2":               tls12,
		"no client certificate": noCert,
	} {
		t.Run(name, func(t *testing.T) {
			conn := ts.rawConn(t, cfg)
			_, err := runnerpb.NewRunnerServiceClient(conn).Health(ctxTimeout(t), &runnerpb.HealthRequest{})
			if status.Code(err) != codes.Unavailable {
				t.Fatalf("got %v, want Unavailable (handshake failure)", err)
			}
		})
	}
	// A client that does not trust the runner's CA refuses the runner.
	c := ts.client(t, other.clientTLS(t, certs.ComponentServe))
	if _, err := c.Health(ctxTimeout(t)); status.Code(err) != codes.Unavailable {
		t.Fatalf("foreign runner CA accepted: %v", err)
	}
}

func TestDeadlineRequired(t *testing.T) {
	ts := startServer(t)
	conn := ts.rawConn(t, ts.pki.clientTLS(t, certs.ComponentServe))
	rpc := runnerpb.NewRunnerServiceClient(conn)
	if _, err := rpc.Health(t.Context(), &runnerpb.HealthRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("unary without deadline: %v", err)
	}
	stream, err := rpc.Dump(t.Context(), &runnerpb.DumpRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("stream without deadline: %v", err)
	}
	// The client refuses streams without a deadline itself.
	c := ts.client(t, ts.pki.clientTLS(t, certs.ComponentServe))
	if _, err := c.RPC().Dump(t.Context(), &runnerpb.DumpRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("client stream without deadline: %v", err)
	}
}

func TestUnimplementedRPCs(t *testing.T) {
	ts := startServer(t)
	rpc := ts.client(t, ts.pki.clientTLS(t, certs.ComponentServe)).RPC()
	ctx := ctxTimeout(t)
	// Implemented RPCs (tasks 2.7, 2.7a, 2.11) reject an empty request in
	// validation.
	invalid := func(name string, err error) {
		t.Helper()
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: got %v, want InvalidArgument", name, err)
		}
	}
	_, err := rpc.Probe(ctx, &runnerpb.ProbeRequest{})
	invalid("Probe", err)
	_, err = rpc.FetchServerCertificate(ctx, &runnerpb.FetchServerCertificateRequest{})
	invalid("FetchServerCertificate", err)
	_, err = rpc.FetchHostKey(ctx, &runnerpb.FetchHostKeyRequest{})
	invalid("FetchHostKey", err)
	_, err = rpc.CreateDatabase(ctx, &runnerpb.CreateDatabaseRequest{})
	invalid("CreateDatabase", err)
	_, err = rpc.RenameSwapDatabase(ctx, &runnerpb.RenameSwapDatabaseRequest{})
	invalid("RenameSwapDatabase", err)
	_, err = rpc.DropTempDatabase(ctx, &runnerpb.DropTempDatabaseRequest{})
	invalid("DropTempDatabase", err)
	_, err = rpc.PrePull(ctx, &runnerpb.PrePullRequest{})
	invalid("PrePull", err)
	d, err := rpc.Dump(ctx, &runnerpb.DumpRequest{})
	if err == nil {
		_, err = d.Recv()
	}
	invalid("Dump", err)
	g, err := rpc.DumpGlobals(ctx, &runnerpb.DumpGlobalsRequest{})
	if err == nil {
		_, err = g.Recv()
	}
	invalid("DumpGlobals", err)
	v, err := rpc.VerifyDump(ctx)
	if err == nil {
		_, err = v.CloseAndRecv()
	}
	invalid("VerifyDump", err)
	r, err := rpc.Restore(ctx)
	if err == nil {
		if err = r.CloseSend(); err == nil {
			_, err = r.Recv()
		}
	}
	invalid("Restore", err)

	// ListImages over the real mTLS client: the merged allowlist.
	imgs, err := ts.client(t, ts.pki.clientTLS(t, certs.ComponentServe)).ListImages(ctx)
	if err != nil || len(imgs) == 0 {
		t.Fatalf("ListImages: %v %v", len(imgs), err)
	}
	for _, im := range imgs {
		if im.GetEngine() == runnerpb.Engine_ENGINE_UNSPECIFIED || im.GetVersionKey() == "" ||
			!strings.Contains(im.GetImageRef(), "@sha256:") || im.GetClientVersion() == "" || im.GetExtra() {
			t.Fatalf("image %v", im)
		}
	}
}

func TestMethodTableCoversService(t *testing.T) {
	sd := runnerpb.RunnerService_ServiceDesc
	want := map[string]bool{}
	for _, m := range sd.Methods {
		want["/"+sd.ServiceName+"/"+m.MethodName] = true
	}
	for _, s := range sd.Streams {
		want["/"+sd.ServiceName+"/"+s.StreamName] = true
	}
	if len(want) != 16 {
		t.Errorf("service has %d methods, want 16", len(want))
	}
	for m := range want {
		if _, ok := methodClasses[m]; !ok {
			t.Errorf("%s is not classified", m)
		}
	}
	for m := range methodClasses {
		if !want[m] {
			t.Errorf("%s is classified but not in the service", m)
		}
	}
	if sd.ServiceName != runnerpb.ServiceName {
		t.Errorf("ServiceName = %q", runnerpb.ServiceName)
	}
}

func TestAdmitDeniesUnknownMethod(t *testing.T) {
	ic := &interceptors{allowed: map[string]bool{}, maxDuration: time.Hour, limits: newLimiter(1, 1)}
	if _, _, err := ic.admit(t.Context(), "/runner.v1.RunnerService/Exec"); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("got %v", err)
	}
}

// fakeService blocks CreateDatabase and panics in Probe.
type fakeService struct {
	runnerpb.UnimplementedRunnerServiceServer
	entered chan struct{}
	release chan struct{}
	health  atomic.Int32
	code    codes.Code
}

func (f *fakeService) Health(context.Context, *runnerpb.HealthRequest) (*runnerpb.HealthResponse, error) {
	f.health.Add(1)
	if f.code != codes.OK {
		return nil, status.Error(f.code, "fake")
	}
	return &runnerpb.HealthResponse{}, nil
}

func (f *fakeService) CreateDatabase(ctx context.Context, _ *runnerpb.CreateDatabaseRequest) (*runnerpb.CreateDatabaseResponse, error) {
	f.entered <- struct{}{}
	select {
	case <-f.release:
	case <-ctx.Done():
	}
	return &runnerpb.CreateDatabaseResponse{}, nil
}

func (f *fakeService) Probe(context.Context, *runnerpb.ProbeRequest) (*runnerpb.ProbeResponse, error) {
	panic("boom: password=hunter2")
}

func TestConcurrencyLimit(t *testing.T) {
	f := &fakeService{entered: make(chan struct{}, 1), release: make(chan struct{})}
	ts := startServer(t, func(o *Options) { o.Config.Limits.MaxConcurrentRuns = 1; o.impl = f })
	rpc := ts.client(t, ts.pki.clientTLS(t, certs.ComponentServe)).RPC()
	ctx := ctxTimeout(t)

	first := make(chan error, 1)
	go func() {
		_, err := rpc.CreateDatabase(ctx, &runnerpb.CreateDatabaseRequest{})
		first <- err
	}()
	<-f.entered
	if got := ts.srv.limits.activeRuns(); got != 1 {
		t.Fatalf("active runs = %d", got)
	}
	if _, err := rpc.CreateDatabase(ctx, &runnerpb.CreateDatabaseRequest{}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second run: %v", err)
	}
	// Health is never limited.
	if _, err := rpc.Health(ctx, &runnerpb.HealthRequest{}); err != nil {
		t.Fatalf("health while busy: %v", err)
	}
	close(f.release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if got := ts.srv.limits.activeRuns(); got != 0 {
		t.Fatalf("slot not released: %d", got)
	}
}

func TestPanicRecovery(t *testing.T) {
	f := &fakeService{}
	ts := startServer(t, func(o *Options) { o.impl = f })
	rpc := ts.client(t, ts.pki.clientTLS(t, certs.ComponentServe)).RPC()
	_, err := rpc.Probe(ctxTimeout(t), &runnerpb.ProbeRequest{})
	if status.Code(err) != codes.Internal || status.Convert(err).Message() != "internal error" {
		t.Fatalf("got %v", err)
	}
	ts.stop(t)
	logs := ts.logs.String()
	if !strings.Contains(logs, "runner rpc panicked") || strings.Contains(logs, "hunter2") {
		t.Fatalf("logs: %s", logs)
	}
}

func TestClientHealthRetry(t *testing.T) {
	for _, tc := range []struct {
		code  codes.Code
		calls int32
	}{
		{codes.OK, 1},
		{codes.Unavailable, 3},
		{codes.PermissionDenied, 1},
		{codes.Internal, 1},
	} {
		f := &fakeService{code: tc.code}
		ts := startServer(t, func(o *Options) { o.impl = f })
		c := ts.client(t, ts.pki.clientTLS(t, certs.ComponentServe))
		_, err := c.Health(ctxTimeout(t))
		if status.Code(err) != tc.code || f.health.Load() != tc.calls {
			t.Errorf("%v: err=%v calls=%d, want %d", tc.code, err, f.health.Load(), tc.calls)
		}
	}
}

func TestClientRequiresVerifyingTLS(t *testing.T) {
	if _, err := runnerclient.NewWithTLS("runner:7443", &tls.Config{MinVersion: tls.VersionTLS12}, 0); err == nil {
		t.Fatal("TLS 1.2 config accepted")
	}
	if _, err := runnerclient.NewWithTLS("runner:7443", &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true}, 0); err == nil { //nolint:gosec // G402: the insecure config is what is tested
		t.Fatal("insecure config accepted")
	}
}

func TestZeroingInterceptors(t *testing.T) {
	pw := []byte("pw")
	req := &runnerpb.CreateDatabaseRequest{Target: &runnerpb.Target{Password: pw}}
	_, err := zeroUnary(t.Context(), req, nil, func(context.Context, any) (any, error) {
		if string(req.GetTarget().GetPassword()) != "pw" {
			t.Error("zeroed before the handler ran")
		}
		return nil, nil //nolint:nilnil // test handler
	})
	if err != nil || !bytes.Equal(pw, []byte{0, 0}) {
		t.Fatalf("unary: %v %q", err, pw)
	}

	key := []byte("key")
	msgs := []*runnerpb.RestoreRequest{
		{Msg: &runnerpb.RestoreRequest_Header{Header: &runnerpb.RestoreHeader{Target: &runnerpb.Target{SshTunnel: &runnerpb.SshTunnel{PrivateKeyPem: key}}}}},
		{Msg: &runnerpb.RestoreRequest_Chunk{Chunk: &runnerpb.DataChunk{Data: []byte("data")}}},
	}
	fs := &fakeStream{msgs: msgs}
	var got []*runnerpb.RestoreRequest
	err = zeroStream(nil, fs, nil, func(_ any, ss grpc.ServerStream) error {
		for range msgs {
			m := &runnerpb.RestoreRequest{}
			if err := ss.RecvMsg(m); err != nil {
				return err
			}
			got = append(got, m)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].GetHeader().GetTarget().GetSshTunnel().GetPrivateKeyPem() != nil || string(got[1].GetChunk().GetData()) != "data" {
		t.Fatalf("stream zeroing: %v", got)
	}

	// More than maxHeldMessages messages with credentials are refused.
	many := make([]*runnerpb.RestoreRequest, 0, maxHeldMessages+1)
	for range maxHeldMessages + 1 {
		many = append(many, &runnerpb.RestoreRequest{Msg: &runnerpb.RestoreRequest_Header{Header: &runnerpb.RestoreHeader{Target: &runnerpb.Target{Password: []byte("x")}}}})
	}
	err = zeroStream(nil, &fakeStream{msgs: many}, nil, func(_ any, ss grpc.ServerStream) error {
		for range many {
			if err := ss.RecvMsg(&runnerpb.RestoreRequest{}); err != nil {
				return err
			}
		}
		return nil
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("too many credential messages: %v", err)
	}
}

type fakeStream struct {
	grpc.ServerStream
	msgs []*runnerpb.RestoreRequest
}

func (f *fakeStream) RecvMsg(m any) error {
	if len(f.msgs) == 0 {
		return errors.New("eof")
	}
	dst, ok := m.(*runnerpb.RestoreRequest)
	if !ok {
		return errors.New("unexpected message type")
	}
	dst.Msg = f.msgs[0].Msg
	f.msgs = f.msgs[1:]
	return nil
}

func TestNewRejectsWildcardOutsideDev(t *testing.T) {
	cfg := config.DefaultRunner()
	for addr, wantErr := range map[string]bool{":7443": true, "0.0.0.0:7443": true, "[::]:7443": true, "10.0.0.1:7443": false} {
		cfg.Listen.Address = addr
		err := checkListen(cfg.Listen, false)
		if (err != nil) != wantErr {
			t.Errorf("%s: %v", addr, err)
		}
		if err := checkListen(cfg.Listen, true); err != nil {
			t.Errorf("%s dev: %v", addr, err)
		}
	}
}

func TestServeRefusesInvalidCertificate(t *testing.T) {
	p := newPKI(t)
	cfg := config.DefaultRunner()
	cfg.Listen.Address = "127.0.0.1:0"
	cfg.Listen.HealthSocket = ""
	cfg.TLS.CertFile = p.l.CertFile(certs.ComponentRunner)
	cfg.TLS.KeyFile = p.l.KeyFile(certs.ComponentRunner)
	cfg.TLS.ClientCAFile = p.l.CACert()
	srv, err := New(Options{
		Config: &cfg,
		Logger: slog.New(slog.DiscardHandler),
		Now:    func() time.Time { return time.Now().Add(certs.MaxLeafValidity) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "not valid now") {
		t.Fatalf("got %v", err)
	}
}

func TestNewLoadsAllowlists(t *testing.T) {
	p := newPKI(t)
	cfg := config.DefaultRunner()
	cfg.Listen.Address = "127.0.0.1:0"
	cfg.Listen.HealthSocket = ""
	cfg.TLS.CertFile = p.l.CertFile(certs.ComponentRunner)
	cfg.TLS.KeyFile = p.l.KeyFile(certs.ComponentRunner)
	cfg.TLS.ClientCAFile = p.l.CACert()
	var logs strings.Builder
	srv, err := New(Options{Config: &cfg, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	v := srv.Validator()
	if v == nil || len(v.Catalog().Entries()) == 0 || !v.Guard().Empty() {
		t.Fatal("validator not initialized from the config")
	}
	if !strings.Contains(logs.String(), "every target database and SSH bastion is denied") {
		t.Errorf("no empty-allowlist warning: %s", logs.String())
	}
	cfg.Images.ExtraFile = t.TempDir() + "/missing.toml"
	if _, err := New(Options{Config: &cfg, Logger: slog.New(slog.DiscardHandler)}); err == nil {
		t.Error("missing extra-images file accepted")
	}
	cfg.Images.ExtraFile = ""
	cfg.Targets.AllowHosts = []string{"10.0.0.1"}
	if _, err := New(Options{Config: &cfg, Logger: slog.New(slog.DiscardHandler)}); err == nil {
		t.Error("IP address in allow_hosts accepted")
	}
}

// socketDir returns a short private directory (unix socket paths are
// limited to ~104 bytes; t.TempDir can be longer on macOS).
func socketDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "bkr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeAllT(t, d) })
	return d
}

func checkSocket(t *testing.T, path string) healthpb.HealthCheckResponse_ServingStatus {
	t.Helper()
	conn, err := grpc.NewClient("unix://"+path, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer closeT(t, conn)
	resp, err := healthpb.NewHealthClient(conn).Check(ctxTimeout(t), &healthpb.HealthCheckRequest{Service: runnerpb.ServiceName})
	if err != nil {
		return healthpb.HealthCheckResponse_UNKNOWN
	}
	return resp.GetStatus()
}

func TestHealthSocket(t *testing.T) {
	dir := filepath.Join(socketDir(t), "h")
	path := filepath.Join(dir, "health.sock")
	ts := startServer(t, func(o *Options) { o.Config.Listen.HealthSocket = path })

	deadline := time.Now().Add(5 * time.Second)
	for !ts.srv.health.serving() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := checkSocket(t, path); got != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("status %v", got)
	}
	// Only the health service is served on the socket.
	conn, err := grpc.NewClient("unix://"+path, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	_, err = runnerpb.NewRunnerServiceClient(conn).Health(ctxTimeout(t), &runnerpb.HealthRequest{})
	closeT(t, conn)
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("runner API reachable on the health socket: %v", err)
	}

	di, err := os.Lstat(dir)
	if err != nil || di.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode: %v %v", di.Mode(), err)
	}
	si, err := os.Lstat(path)
	if err != nil || si.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode: %v %v", si.Mode(), err)
	}

	ts.stop(t)
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("socket not removed: %v", err)
	}
}

func TestHealthStateTransitions(t *testing.T) {
	valid := atomic.Bool{}
	valid.Store(true)
	h := newHealthState(valid.Load)
	if h.serving() {
		t.Fatal("serving before the listener is up")
	}
	h.setListening(true)
	if !h.serving() {
		t.Fatal("not serving")
	}
	valid.Store(false) // certificate expired
	h.update()
	if h.serving() {
		t.Fatal("serving with an expired certificate")
	}
	valid.Store(true)
	h.update()
	h.shutdown()
	h.update()
	if h.serving() {
		t.Fatal("serving after shutdown")
	}
}

func TestHealthSocketRefusesUnsafePaths(t *testing.T) {
	state := newHealthState(func() bool { return true })

	// Directory with group/other access.
	open := filepath.Join(socketDir(t), "open")
	if err := os.Mkdir(open, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o750); err != nil { //nolint:gosec // G302: an unsafe mode is what is tested
		t.Fatal(err)
	}
	if _, err := listenHealthSocket(t.Context(), filepath.Join(open, "h.sock"), state); err == nil || !strings.Contains(err.Error(), "0700") {
		t.Fatalf("open dir: %v", err)
	}

	// Symlinked directory.
	base := socketDir(t)
	realDir := filepath.Join(base, "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := listenHealthSocket(t.Context(), filepath.Join(link, "h.sock"), state); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink dir: %v", err)
	}

	// A regular file where the socket should be is never removed.
	file := filepath.Join(realDir, "h.sock")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := listenHealthSocket(t.Context(), file, state); err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Fatalf("regular file: %v", err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("file removed: %v", err)
	}

	// A stale socket of ours is replaced.
	stale := filepath.Join(realDir, "s.sock")
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	if ul, ok := l.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	closeT(t, l)
	hs, err := listenHealthSocket(t.Context(), stale, state)
	if err != nil {
		t.Fatalf("stale socket: %v", err)
	}
	if err := hs.close(); err != nil {
		t.Fatal(err)
	}
}

func closeT(t *testing.T, c interface{ Close() error }) {
	t.Helper()
	if err := c.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Errorf("close: %v", err)
	}
}

func removeAllT(t *testing.T, dir string) {
	t.Helper()
	if err := os.RemoveAll(dir); err != nil {
		t.Errorf("remove %s: %v", dir, err)
	}
}

func TestServerRunPeers(t *testing.T) {
	s := &Server{runPool: netip.MustParsePrefix("10.214.0.0/16")}
	if !s.isRunPeer(netip.MustParseAddr("10.214.3.3")) || !s.isRunPeer(netip.MustParseAddr("::ffff:10.214.3.3")) || s.isRunPeer(netip.MustParseAddr("10.213.251.20")) {
		t.Fatal("run peer classification")
	}
}
