package runnerclient

import (
	"context"
	"crypto/tls"
	"net"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// fakeRunner answers GetTargetPolicy and records whether the call carried
// a deadline.
type fakeRunner struct {
	runnerpb.UnimplementedRunnerServiceServer
	resp        *runnerpb.GetTargetPolicyResponse
	err         error
	hadDeadline chan bool
}

func (f *fakeRunner) GetTargetPolicy(ctx context.Context, _ *runnerpb.GetTargetPolicyRequest) (*runnerpb.GetTargetPolicyResponse, error) {
	_, ok := ctx.Deadline()
	f.hadDeadline <- ok
	return f.resp, f.err
}

// startFake serves f over mTLS with a fresh private CA and returns a
// client (component serve) for it.
func startFake(t *testing.T, f *fakeRunner) *Client {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "pki")
	if _, err := certs.Init(certs.InitOptions{
		Dir: dir, Components: []string{certs.ComponentRunner, certs.ComponentServe}, AllowUnencrypted: true,
	}); err != nil {
		t.Fatal(err)
	}
	l := certs.Layout{Dir: dir}
	srvTLS, err := certs.ServerTLSConfig(l.CertFile(certs.ComponentRunner), l.KeyFile(certs.ComponentRunner), l.CACert(), []string{"spiffe://bekup/serve"})
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(srvTLS)))
	runnerpb.RegisterRunnerServiceServer(srv, f)
	var lc net.ListenConfig
	lis, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
		if err := <-served; err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	cliTLS, err := certs.ClientTLSConfig(l.CertFile(certs.ComponentServe), l.KeyFile(certs.ComponentServe), l.CACert(), "runner")
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewWithTLS(lis.Addr().String(), cliTLS, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return c
}

func TestGetTargetPolicy(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{
		resp: &runnerpb.GetTargetPolicyResponse{
			AllowCidrs: []string{"10.20.0.0/16"}, RunSubnetPool: "10.214.0.0/16",
			Warnings: []runnerpb.TargetPolicyWarning{runnerpb.TargetPolicyWarning_TARGET_POLICY_WARNING_BASTIONS_UNRESTRICTED},
		},
		hadDeadline: make(chan bool, 2),
	}
	c := startFake(t, f)
	got, err := c.GetTargetPolicy(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.GetAllowCidrs(), []string{"10.20.0.0/16"}) || got.GetRunSubnetPool() != "10.214.0.0/16" || len(got.GetWarnings()) != 1 {
		t.Errorf("policy = %v", got)
	}
	if !<-f.hadDeadline {
		t.Error("the call carried no deadline (the runner refuses such calls)")
	}

	// Runner errors reach the caller unchanged (serve maps them).
	f.resp, f.err = nil, status.Error(codes.Unimplemented, "unknown method")
	if _, err := c.GetTargetPolicy(t.Context()); status.Code(err) != codes.Unimplemented {
		t.Errorf("err = %v", err)
	}
}

func TestNewWithTLSRefusesWeakConfig(t *testing.T) {
	t.Parallel()
	for _, cfg := range []*tls.Config{
		nil,
		{MinVersion: tls.VersionTLS12},
		{MinVersion: tls.VersionTLS13}, // no private CA
	} {
		if _, err := NewWithTLS("runner:7443", cfg, 0); err == nil {
			t.Errorf("%+v: accepted", cfg)
		}
	}
	if _, err := NewWithTLS("", &tls.Config{MinVersion: tls.VersionTLS13}, 0); err == nil {
		t.Error("empty address accepted")
	}
}
