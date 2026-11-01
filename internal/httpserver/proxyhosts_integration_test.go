//go:build integration

package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/testutil/dockertest"
)

// TestIntegrationTrustedProxyHosts (D-215) runs a server in the own-proxy
// join shape on a real user-defined Docker network (alias bekup-serve, a
// wildcard listener filtered to that network) that trusts the container
// "proxy-<id>" by name; the server is also on a second "database" network.
// X-Forwarded-For from the proxy container is used (after the name
// resolved through Docker's embedded DNS), from another container on the
// proxy network it is ignored; a container on the database network cannot
// reach port 8080 at all; a container that takes the alias bekup-serve is
// reported and fails /readyz.
func TestIntegrationTrustedProxyHosts(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		dockertest.Skipf(t, "docker CLI not found: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	docker := func(args ...string) (string, error) {
		out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput() //nolint:gosec // G204: fixed docker subcommands with test-made names
		return strings.TrimSpace(string(out)), err
	}
	arch, err := docker("version", "--format", "{{.Server.Arch}}")
	if err != nil {
		dockertest.Skipf(t, "docker daemon not reachable: %v: %s", err, arch)
	}
	idb := make([]byte, 4)
	if _, err := rand.Read(idb); err != nil {
		t.Fatal(err)
	}
	id := hex.EncodeToString(idb)
	// The helper, built for the daemon's platform, in a scratch image.
	build := t.TempDir()
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", filepath.Join(build, "helper"), "./testdata/proxyhostsrv") //nolint:gosec // G204: a fixed go build into t.TempDir()
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+arch)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the helper (%s): %v\n%s", runtime.GOOS, err, out)
	}
	if err := os.WriteFile(filepath.Join(build, "Dockerfile"), []byte("FROM scratch\nCOPY helper /helper\nUSER 65532:65532\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	image := "bekup-test-proxyhosts:" + id
	if out, err := docker("build", "-q", "-t", image, build); err != nil {
		t.Fatalf("docker build: %v\n%s", err, out)
	}
	network, dbNet := "bekup-test-proxyhosts-"+id, "bekup-test-proxyhosts-db-"+id
	srv, proxy, other, dbc, impostor := "srv-"+id, "proxy-"+id, "other-"+id, "dbc-"+id, "impostor-"+id
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), time.Minute)
		defer ccancel()
		for _, args := range [][]string{
			{"rm", "-f", srv, proxy, other, dbc, impostor},
			{"network", "rm", network},
			{"network", "rm", dbNet},
			{"image", "rm", "-f", image},
		} {
			if out, err := exec.CommandContext(cctx, "docker", args...).CombinedOutput(); err != nil { //nolint:gosec // G204: fixed docker subcommands with test-made names
				t.Logf("cleanup docker %v: %v: %s", args, err, out)
			}
		}
	})
	for _, n := range []string{network, dbNet} {
		if out, err := docker("network", "create", n); err != nil {
			t.Fatalf("network %s: %v\n%s", n, err, out)
		}
	}
	hardening := []string{"--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges"}
	run := append([]string{"run", "--network", network}, hardening...)
	if out, err := docker(append([]string{"run", "--network", network, "--network-alias", "bekup-serve"},
		append(hardening, "-d", "--name", srv, image, "/helper", "serve", proxy)...)...); err != nil {
		t.Fatalf("server: %v\n%s", err, out)
	}
	if out, err := docker("network", "connect", dbNet, srv); err != nil {
		t.Fatalf("server on the database network: %v\n%s", err, out)
	}
	url := "http://bekup-serve:8080/api/whoami"
	// Both clients stay up for the whole test (the helper's serve mode as
	// a long-running process) and send their requests with docker exec:
	// a removed container's address could be handed to the next one
	// within the re-resolution interval (documented residual risk).
	for _, name := range []string{proxy, other} {
		if out, err := docker(append(run, "-d", "--name", name, image, "/helper", "serve", "nobody")...); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
	}
	// The trusted proxy: its first request may come before the name
	// resolved; an untrusted X-Forwarded-For triggers a re-resolution,
	// then the forwarded address is used.
	out, err := docker("exec", proxy, "/helper", "get", url, "20")
	if err != nil {
		t.Fatalf("proxy: %v\n%s", err, out)
	}
	lines := strings.Split(out, "\n")
	if lines[len(lines)-1] != "198.51.100.9" {
		logs, lerr := docker("logs", srv)
		t.Fatalf("the proxy container's X-Forwarded-For was never trusted:\n%s\nserver log (%v):\n%s", out, lerr, logs)
	}
	// Another container on the same network: never trusted, also after
	// the re-resolutions its X-Forwarded-For triggers.
	out, err = docker("exec", other, "/helper", "get", url, "8")
	if err != nil {
		t.Fatalf("other: %v\n%s", err, out)
	}
	if strings.Contains(out, "198.51.100.9") || out == "" {
		t.Fatalf("another container's X-Forwarded-For was used: %q", out)
	}
	if ip, err := docker("inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", other); err != nil || out != ip {
		t.Fatalf("the other container is seen as %q, its address is %q (%v)", out, ip, err)
	}
	logs, err := docker("logs", srv)
	if err != nil {
		t.Fatalf("server log: %v\n%s", err, logs)
	}
	if !strings.Contains(logs, "trusted proxy host resolved") || !strings.Contains(logs, "not in server.trusted_proxies") ||
		!strings.Contains(logs, "serve's address on the proxy network") {
		t.Fatalf("server log lacks the resolution, the untrusted warning or the own address:\n%s", logs)
	}

	// A container on the database network reaches serve's address there,
	// but the listener closes the connection (only the proxy network is
	// served); through the proxy network the same server answers.
	if out, err := docker(append(append([]string{"run", "--network", dbNet}, hardening...),
		"-d", "--name", dbc, image, "/helper", "serve", "nobody")...); err != nil {
		t.Fatalf("db client: %v\n%s", err, out)
	}
	srvDBIP, err := docker("inspect", "-f", "{{(index .NetworkSettings.Networks \""+dbNet+"\").IPAddress}}", srv)
	if err != nil || srvDBIP == "" {
		t.Fatalf("serve's address on the database network: %q %v", srvDBIP, err)
	}
	for _, u := range []string{"http://" + srvDBIP + ":8080/healthz", "http://" + srv + ":8080/healthz"} {
		if out, err := docker("exec", dbc, "/helper", "try", u); err != nil || out != "ERR" {
			t.Fatalf("a container on the database network reached serve at %s: %q (%v)", u, out, err)
		}
	}
	if out, err := docker("exec", proxy, "/helper", "try", "http://bekup-serve:8080/healthz"); err != nil || out != "ok" {
		t.Fatalf("the proxy must still reach serve: %q (%v)", out, err)
	}
	if out, err := docker("exec", proxy, "/helper", "try", "http://bekup-serve:8080/readyz"); err != nil || out != "ok" {
		t.Fatalf("readyz before the conflict: %q (%v)", out, err)
	}
	waitLogs := func(what string) string {
		t.Helper()
		deadline := time.Now().Add(90 * time.Second)
		for {
			logs, err := docker("logs", srv)
			if err == nil && strings.Contains(logs, what) {
				return logs
			}
			if time.Now().After(deadline) {
				t.Fatalf("server log lacks %q:\n%s", what, logs)
			}
			time.Sleep(time.Second)
		}
	}
	if logs := waitLogs("closed a connection that did not arrive on its address on the proxy network"); strings.Contains(logs, "SELF CONFLICT") {
		t.Fatalf("no conflict yet:\n%s", logs)
	}

	// Another container takes the alias bekup-serve on the proxy network:
	// serve warns, reports it and fails /readyz (re-checked every
	// ProxyHostInterval).
	if out, err := docker(append([]string{"run", "--network", network, "--network-alias", "bekup-serve"},
		append(hardening, "-d", "--name", impostor, image, "/helper", "serve", "nobody")...)...); err != nil {
		t.Fatalf("impostor: %v\n%s", err, out)
	}
	waitLogs("SELF CONFLICT REPORTED")
	waitLogs("alias takeover")
	srvIP, err := docker("inspect", "-f", "{{(index .NetworkSettings.Networks \""+network+"\").IPAddress}}", srv)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := docker("exec", proxy, "/helper", "try", "http://"+srvIP+":8080/readyz"); err != nil || out != "not ready" {
		t.Fatalf("readyz during the conflict: %q (%v)", out, err)
	}
}
