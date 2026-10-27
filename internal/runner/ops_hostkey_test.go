package runner

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/jlentink/bekup/internal/runner/targets"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/tunnel"
)

func TestFetchHostKey(t *testing.T) {
	env := newOpsEnv(t)
	var gotHost string
	var gotPort int
	var gotDialer tunnel.Dialer
	env.ops.hostKey = func(_ context.Context, host string, port int, d tunnel.Dialer) (tunnel.HostKeyInfo, error) {
		gotHost, gotPort, gotDialer = host, port, d
		return tunnel.HostKeyInfo{Type: "ssh-ed25519", FingerprintSHA256: "SHA256:abc", AuthorizedKey: "ssh-ed25519 AAAA"}, nil
	}
	resp, err := env.ops.fetchHostKey(t.Context(), &runnerpb.FetchHostKeyRequest{Ctx: rc(), Host: opsHost, Port: 2222})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetType() != "ssh-ed25519" || resp.GetFingerprintSha256() != "SHA256:abc" || resp.GetAuthorizedKey() != "ssh-ed25519 AAAA" {
		t.Fatalf("resp = %v", resp)
	}
	if gotHost != opsHost || gotPort != 2222 {
		t.Fatalf("dialed %s:%d", gotHost, gotPort)
	}
	// The key exchange must go through the target allowlist dialer.
	if g, ok := gotDialer.(*targets.Guard); !ok || g != env.ops.val.Guard() {
		t.Fatalf("dialer = %T, want the target guard", gotDialer)
	}
	logs := env.logs.String()
	if !strings.Contains(logs, "runner_rpc_audit") || !strings.Contains(logs, "rpc=FetchHostKey") ||
		!strings.Contains(logs, "host_key_fingerprint=SHA256:abc") {
		t.Fatalf("audit line missing: %s", logs)
	}
}

func TestFetchHostKeyValidation(t *testing.T) {
	env := newOpsEnv(t)
	called := false
	env.ops.hostKey = func(context.Context, string, int, tunnel.Dialer) (tunnel.HostKeyInfo, error) {
		called = true
		return tunnel.HostKeyInfo{}, nil
	}
	for name, tc := range map[string]struct {
		req  *runnerpb.FetchHostKeyRequest
		code codes.Code
	}{
		"no run context":   {&runnerpb.FetchHostKeyRequest{Host: opsHost, Port: 22}, codes.InvalidArgument},
		"no host":          {&runnerpb.FetchHostKeyRequest{Ctx: rc(), Port: 22}, codes.InvalidArgument},
		"bad port":         {&runnerpb.FetchHostKeyRequest{Ctx: rc(), Host: opsHost, Port: 70000}, codes.InvalidArgument},
		"metadata address": {&runnerpb.FetchHostKeyRequest{Ctx: rc(), Host: "169.254.169.254", Port: 22}, codes.PermissionDenied},
		"not allowlisted":  {&runnerpb.FetchHostKeyRequest{Ctx: rc(), Host: "192.0.2.10", Port: 22}, codes.PermissionDenied},
	} {
		_, err := env.ops.fetchHostKey(t.Context(), tc.req)
		wantReason(t, err, tc.code, "")
		if called {
			t.Fatalf("%s: dialed despite a validation error", name)
		}
	}
}

func TestFetchHostKeyErrors(t *testing.T) {
	denied := fmt.Errorf("dial: %w", targets.ErrDenied)
	for name, tc := range map[string]struct {
		err    error
		code   codes.Code
		reason runnerpb.Reason
	}{
		"unsupported key": {&tunnel.Error{Kind: tunnel.ErrUnsupportedHostKey}, codes.FailedPrecondition, runnerpb.ReasonSSHHostKeyUnsupported},
		"refused":         {&tunnel.Error{Kind: tunnel.ErrUnreachable}, codes.Unavailable, runnerpb.ReasonUnreachable},
		"timeout":         {&tunnel.Error{Kind: tunnel.ErrTimeout}, codes.Unavailable, runnerpb.ReasonUnreachable},
		"handshake":       {&tunnel.Error{Kind: tunnel.ErrHandshake}, codes.Unavailable, runnerpb.ReasonUnreachable},
		"other":           {&net.OpError{Op: "dial", Err: errors.New("boom")}, codes.Unavailable, runnerpb.ReasonUnreachable},
		"denied":          {denied, codes.PermissionDenied, ""},
		"canceled":        {context.Canceled, codes.Canceled, ""},
	} {
		t.Run(name, func(t *testing.T) {
			env := newOpsEnv(t)
			env.ops.hostKey = func(context.Context, string, int, tunnel.Dialer) (tunnel.HostKeyInfo, error) {
				return tunnel.HostKeyInfo{}, tc.err
			}
			_, err := env.ops.fetchHostKey(t.Context(), &runnerpb.FetchHostKeyRequest{Ctx: rc(), Host: opsHost, Port: 22})
			wantReason(t, err, tc.code, tc.reason)
		})
	}
}
