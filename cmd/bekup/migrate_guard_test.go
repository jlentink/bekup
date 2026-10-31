package main

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/jlentink/bekup/internal/deployguard"
)

// D-212: migrate up refuses another version than BEKUP_SETUP_VERSION,
// warns without one, refuses a malformed value and (outside dev mode) a
// followed image without a setup version.
func TestMigrateSetupVersionGuard(t *testing.T) {
	oldVersion, oldLookup := version, lookupEnv
	t.Cleanup(func() { version, lookupEnv = oldVersion, oldLookup })
	version = "1.2.4"
	for _, tc := range []struct {
		name   string
		env    map[string]string
		dev    bool
		refuse error
		log    string
	}{
		{name: "match", env: map[string]string{deployguard.EnvSetupVersion: "1.2.4"}},
		{name: "match following", env: map[string]string{deployguard.EnvSetupVersion: "1.2.4", deployguard.EnvImageMode: "following"}, log: "follows a tag"},
		{name: "unset warns", env: nil, log: "setup version unknown"},
		{name: "unset in dev mode", env: nil, dev: true},
		{name: "not prepared", env: map[string]string{deployguard.EnvSetupVersion: "1.2.3"}, refuse: deployguard.ErrVersionMismatch, log: "deployment guard failed"},
		{name: "following without setup version", env: map[string]string{deployguard.EnvImageMode: "following"}, refuse: deployguard.ErrFollowingUnversioned},
		{name: "following without setup version in dev mode", env: map[string]string{deployguard.EnvImageMode: "following"}, dev: true},
		{name: "downgrade", env: map[string]string{deployguard.EnvSetupVersion: "1.3.0"}, refuse: deployguard.ErrVersionMismatch},
		{name: "malformed", env: map[string]string{deployguard.EnvSetupVersion: "latest"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lookupEnv = func(k string) (string, bool) { v, ok := tc.env[k]; return v, ok }
			var logs bytes.Buffer
			c := &cobra.Command{}
			c.SetContext(t.Context())
			err := checkDeployment(c, slog.New(slog.NewTextHandler(&logs, nil)), tc.dev, "migrate")
			switch {
			case tc.name == "malformed":
				if err == nil {
					t.Fatal("malformed BEKUP_SETUP_VERSION accepted")
				}
			case tc.refuse != nil:
				if !errors.Is(err, tc.refuse) {
					t.Fatalf("err = %v", err)
				}
			case err != nil:
				t.Fatal(err)
			}
			if tc.log != "" && !strings.Contains(logs.String(), tc.log) {
				t.Fatalf("log lacks %q:\n%s", tc.log, logs.String())
			}
			if tc.log == "" && tc.refuse == nil && strings.Contains(logs.String(), "WARN") {
				t.Fatalf("unexpected warning:\n%s", logs.String())
			}
		})
	}
}

// D-212: `bekup runner` and `bekup db bootstrap` apply the same guard before
// they touch Docker, the gRPC listener or the database: the refusal is the
// guard's, not a later TLS/Docker/connect error.
func TestRunnerAndBootstrapSetupVersionGuard(t *testing.T) {
	oldVersion, oldLookup := version, lookupEnv
	t.Cleanup(func() { version, lookupEnv = oldVersion, oldLookup })
	version = "1.2.4"
	// Loads, but cannot start: no TLS files, no Docker socket.
	runnerCfg := writeConfig(t, "runner.toml", "[listen]\naddress = \"10.0.0.11:7443\"\nhealth_socket = \"\"\n[docker]\nhost = \"unix:///nonexistent/docker.sock\"\n")
	bootCfg := bootstrapConfig(t, "")
	for _, tc := range []struct {
		name string
		env  map[string]string
		want error
	}{
		{name: "not prepared", env: map[string]string{deployguard.EnvSetupVersion: "1.2.3"}, want: deployguard.ErrVersionMismatch},
		{name: "downgrade", env: map[string]string{deployguard.EnvSetupVersion: "1.3.0", deployguard.EnvImageMode: "following"}, want: deployguard.ErrVersionMismatch},
		{name: "following without setup version", env: map[string]string{deployguard.EnvImageMode: "following"}, want: deployguard.ErrFollowingUnversioned},
		{name: "malformed", env: map[string]string{deployguard.EnvImageMode: "Following"}},
	} {
		lookupEnv = func(k string) (string, bool) { v, ok := tc.env[k]; return v, ok }
		for _, args := range [][]string{
			{"--config", runnerCfg, "runner"},
			{"--config", bootCfg, "db", "bootstrap", "--admin-user", "admin", "--allow-insecure-admin-connection"},
		} {
			t.Run(tc.name+" "+args[len(args)-1], func(t *testing.T) {
				_, stderr, err := run(t, args...)
				if err == nil || !strings.Contains(err.Error(), "refusing to run") {
					t.Fatalf("err = %v\n%s", err, stderr)
				}
				if tc.want != nil && !errors.Is(err, tc.want) {
					t.Fatalf("err = %v, want %v", err, tc.want)
				}
				if strings.Contains(stderr, "INSECURE ADMIN CONNECTION") || strings.Contains(stderr, "runner listening") {
					t.Fatalf("got past the guard:\n%s", stderr)
				}
			})
		}
	}
	// A matching version passes the guard (and fails later, as before).
	lookupEnv = func(k string) (string, bool) {
		v, ok := map[string]string{deployguard.EnvSetupVersion: "1.2.4"}[k]
		return v, ok
	}
	if _, _, err := run(t, "--config", runnerCfg, "runner"); err == nil || strings.Contains(err.Error(), "refusing to run") {
		t.Fatalf("matching runner: %v", err)
	}
}
