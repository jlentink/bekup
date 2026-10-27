package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/certs"
)

// TestRunnerAndHealthcheck starts `bekup runner` with a throw-away PKI and
// checks `bekup healthcheck runner` against its local health socket, before
// and after shutdown.
func TestRunnerAndHealthcheck(t *testing.T) {
	t.Setenv("BEKUP_CONFIG", "")
	t.Setenv("BEKUP_DEV", "")
	pkiDir := filepath.Join(t.TempDir(), "pki")
	if _, err := certs.Init(certs.InitOptions{
		Dir:              pkiDir,
		Components:       []string{certs.ComponentRunner, certs.ComponentServe},
		AllowUnencrypted: true,
	}); err != nil {
		t.Fatal(err)
	}
	l := certs.Layout{Dir: pkiDir}
	// Unix socket paths are limited to ~104 bytes; t.TempDir can be longer.
	sockBase, err := os.MkdirTemp("/tmp", "bkr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(sockBase); err != nil {
			t.Error(err)
		}
	})
	sock := filepath.Join(sockBase, "h", "health.sock")
	// A free port (the config requires a fixed one).
	var lc net.ListenConfig
	lis, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := lis.Addr().String()
	if err := lis.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := writeConfig(t, "runner.toml", fmt.Sprintf(`
[listen]
address = %q
health_socket = %q
[tls]
cert_file = %q
key_file = %q
client_ca_file = %q
allowed_client_sans = ["spiffe://bekup/serve"]
`, addr, sock, l.CertFile("runner"), l.KeyFile("runner"), l.CACert()))

	healthcheck := func() (string, error) {
		cmd := newRootCmd()
		var out, errb bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&errb)
		cmd.SetArgs([]string{"--config", cfg, "healthcheck", "runner"})
		err := cmd.ExecuteContext(t.Context())
		return out.String(), err
	}
	if _, err := healthcheck(); err == nil || !strings.Contains(err.Error(), "unhealthy") {
		t.Fatalf("healthy without a runner: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	var logs bytes.Buffer
	runnerCmd := newRootCmd()
	runnerCmd.SetOut(&bytes.Buffer{})
	runnerCmd.SetErr(&logs)
	runnerCmd.SetArgs([]string{"--config", cfg, "runner"})
	go func() { done <- runnerCmd.ExecuteContext(ctx) }()

	var out string
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, err = healthcheck()
		if err == nil || time.Now().After(deadline) {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("runner exited: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
	}
	if err != nil || out != "ok\n" {
		t.Fatalf("healthcheck: %q %v", out, err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runner: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runner did not stop")
	}
	if !strings.Contains(logs.String(), "runner listening") {
		t.Errorf("logs: %s", logs.String())
	}
	if _, err := healthcheck(); err == nil {
		t.Fatal("healthy after shutdown")
	}
}

func TestHealthcheckRunnerDisabled(t *testing.T) {
	cfg := writeConfig(t, "runner.toml", "[listen]\naddress = \"10.0.0.11:7443\"\nhealth_socket = \"\"\n")
	if _, _, err := run(t, "--config", cfg, "healthcheck", "runner"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("got %v", err)
	}
}
