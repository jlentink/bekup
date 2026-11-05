package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	setupweb "github.com/jlentink/bekup/internal/setup/web"
)

// runSetupWeb runs `setup web args...` with setupWebRun replaced and
// returns the options it got.
func runSetupWeb(t *testing.T, args ...string) (setupweb.Options, error) {
	t.Helper()
	var got setupweb.Options
	old := setupWebRun
	setupWebRun = func(_ context.Context, o setupweb.Options) error { got = o; return nil }
	t.Cleanup(func() { setupWebRun = old })
	cmd := newSetupWebCmd()
	cmd.SetArgs(args)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.ExecuteContext(context.Background())
	return got, err
}

// TestSetupWebCmd: the address and port come from the compose
// environment unless the flags are given; --database selects the database
// run (D-217). Not parallel: environment and the setupWebRun hook.
func TestSetupWebCmd(t *testing.T) {
	t.Setenv(envBrowserHost, " setup.example.com ")
	t.Setenv(envBrowserPort, "9443")
	o, err := runSetupWeb(t, "--dir", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Hosts) != 1 || o.Hosts[0] != "setup.example.com" || o.PublicPort != 9443 {
		t.Fatalf("from the environment: hosts %v port %d", o.Hosts, o.PublicPort)
	}
	if o.Mode != "" || o.Database != nil || o.Init == nil || o.Check == nil || o.Assets == nil || o.Out == nil {
		t.Fatalf("init run: mode %q database %v", o.Mode, o.Database != nil)
	}
	o, err = runSetupWeb(t, "--dir", t.TempDir(), "--host", "a.example.com", "--host", "10.0.0.5", "--public-port", "8444", "--database")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(o.Hosts, ",") != "a.example.com,10.0.0.5" || o.PublicPort != 8444 {
		t.Fatalf("flags: hosts %v port %d", o.Hosts, o.PublicPort)
	}
	if o.Mode != setupweb.ModeDatabase || o.Database == nil {
		t.Fatalf("database run: mode %q database %v", o.Mode, o.Database != nil)
	}
	for _, bad := range []string{"abc", "0", "65536", "-1"} {
		t.Setenv(envBrowserPort, bad)
		if _, err := runSetupWeb(t, "--dir", t.TempDir()); err == nil || !strings.Contains(err.Error(), envBrowserPort) {
			t.Errorf("%s=%q: %v", envBrowserPort, bad, err)
		}
	}
	// The flag wins over a bad variable; empty variables keep the defaults.
	if _, err := runSetupWeb(t, "--dir", t.TempDir(), "--public-port", "8443"); err != nil {
		t.Fatalf("flag over a bad variable: %v", err)
	}
	t.Setenv(envBrowserPort, "")
	t.Setenv(envBrowserHost, "")
	if o, err = runSetupWeb(t, "--dir", t.TempDir()); err != nil || len(o.Hosts) != 0 || o.PublicPort != setupweb.ListenPort {
		t.Fatalf("defaults: %v hosts %v port %d", err, o.Hosts, o.PublicPort)
	}
	if _, err := runSetupWeb(t, "--dir", t.TempDir(), "--ownership", "bogus"); err == nil {
		t.Fatal("a bad --ownership was accepted")
	}
}
