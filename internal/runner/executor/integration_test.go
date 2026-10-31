//go:build integration

package executor_test

// Integration harness (task 2.6). The executor must run inside a container
// so that the self-join is real: the outer test (on the host) builds this
// test binary for linux, starts an SSH bastion with a TLS PostgreSQL behind
// it on an internal "targets" network, and runs the binary inside a
// container on an internal "control" network (+ targets) with the Docker
// socket. The inner test (TestInnerIsolation, selected by
// BEKUP_EXEC_INNER) then drives the real executor against the real
// daemon and proves the AC of task 2.6.

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"golang.org/x/crypto/ssh"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/spec"
	"github.com/jlentink/bekup/internal/testutil/dockertest"
	"github.com/jlentink/bekup/internal/testutil/imagepull"
)

const (
	// opensshImage is the bastion image of docker-compose.dev.yml.
	opensshImage = "lscr.io/linuxserver/openssh-server:10.3_p1-r1-ls240@sha256:b07414bb329b86a00525c9256711a7855fc3a369e9f62c36f1de8d1923197580"
	entryKey     = "17"
	dbUser       = "bekup"
	sshUser      = "tunnel"
	// literalIP is the target address behind the bastion (an extra
	// address on the bastion's loopback, shared with PostgreSQL).
	literalIP = "10.99.0.5"
	innerEnv  = "BEKUP_EXEC_INNER"
)

const enableForwarding = `#!/bin/bash
set -euo pipefail
f=/config/sshd/sshd_config
sed -i 's/^AllowTcpForwarding no$/AllowTcpForwarding yes/' "$f"
grep -qx 'AllowTcpForwarding yes' "$f"
echo "bastion: AllowTcpForwarding enabled"
`

// harness is passed from the outer to the inner test (a file in the inner
// container; test-only credentials).
type harness struct {
	CAPEM         string `json:"ca_pem"`
	DBPassword    string `json:"db_password"`
	BastionHost   string `json:"bastion_host"`
	BastionIP     string `json:"bastion_ip"`
	HostKey       string `json:"host_key"`
	ClientKeyPEM  string `json:"client_key_pem"`
	TargetsSubnet string `json:"targets_subnet"`
	CtlSubnet     string `json:"ctl_subnet"`
}

func dockerClient(t *testing.T) *client.Client {
	t.Helper()
	c, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	t.Cleanup(func() { ignore(c.Close()) })
	if _, err := c.Ping(t.Context(), client.PingOptions{}); err != nil {
		dockertest.Skipf(t, "docker not available: %v", err)
	}
	return c
}

func randHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func tarOf(t *testing.T, files map[string][]byte, modes map[string]int64) io.Reader {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, data := range files {
		mode := int64(0o644)
		if m, ok := modes[name]; ok {
			mode = m
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data)), ModTime: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

// testCerts returns a CA and a server certificate for db.bekup.test and
// literalIP.
func testCerts(t *testing.T) (caPEM, certPEM, keyPEM []byte) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "bekup integration CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "db.bekup.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		DNSNames: []string{"db.bekup.test"}, IPAddresses: []net.IP{net.ParseIP(literalIP)},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	kder, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kder})
}

func createNetwork(t *testing.T, c *client.Client, name string) (string, netip.Prefix) {
	t.Helper()
	res, err := c.NetworkCreate(t.Context(), name, client.NetworkCreateOptions{Driver: "bridge", Internal: true,
		Labels: map[string]string{"bekup.integration": "executor"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ignore2(c.NetworkRemove(context.Background(), res.ID, client.NetworkRemoveOptions{})) })
	ins, err := c.NetworkInspect(t.Context(), res.ID, client.NetworkInspectOptions{})
	if err != nil || len(ins.Network.IPAM.Config) == 0 {
		t.Fatalf("inspect network: %v", err)
	}
	return res.ID, ins.Network.IPAM.Config[0].Subnet
}

func createContainer(t *testing.T, c *client.Client, opts client.ContainerCreateOptions) string {
	t.Helper()
	res, err := c.ContainerCreate(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ignore2(c.ContainerRemove(context.Background(), res.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}))
	})
	return res.ID
}

func execIn(t *testing.T, c *client.Client, id string, cmd ...string) string {
	t.Helper()
	ex, err := c.ExecCreate(t.Context(), id, client.ExecCreateOptions{User: "root", Cmd: cmd, AttachStdout: true, AttachStderr: true})
	if err != nil {
		t.Fatal(err)
	}
	att, err := c.ExecAttach(t.Context(), ex.ID, client.ExecAttachOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer att.Close()
	var out bytes.Buffer
	ignore2(stdcopy.StdCopy(&out, &out, att.Reader))
	ins, err := c.ExecInspect(t.Context(), ex.ID, client.ExecInspectOptions{})
	if err != nil || ins.ExitCode != 0 {
		t.Fatalf("exec %v: %v exit %d: %s", cmd, err, ins.ExitCode, out.String())
	}
	return out.String()
}

func logsOf(c *client.Client, id string) string {
	rc, err := c.ContainerLogs(context.Background(), id, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return ""
	}
	defer func() { ignore(rc.Close()) }()
	var out bytes.Buffer
	ignore2(stdcopy.StdCopy(&out, &out, rc))
	return out.String()
}

func waitLog(t *testing.T, c *client.Client, id, needle string, count int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Count(logsOf(c, id), needle) >= count {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("container %s: %q not logged %d times:\n%s", id[:12], needle, count, logsOf(c, id))
}

func ensureImage(t *testing.T, c *client.Client, ref string) {
	t.Helper()
	imagepull.Ensure(t, c, ref)
}

func TestIntegrationExecutorIsolation(t *testing.T) {
	if os.Getenv(innerEnv) != "" {
		t.Skip("outer harness")
	}
	c := dockerClient(t)
	ctx := t.Context()
	sv, err := c.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil || sv.Os != "linux" {
		dockertest.Skipf(t, "needs a linux Docker daemon (%v)", err)
	}
	cat, err := engine.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	pgEntry, ok := cat.Lookup(engine.KindPostgres, entryKey)
	if !ok {
		t.Fatal("no postgres entry")
	}
	pgRef := spec.ImageRef(pgEntry)
	ensureImage(t, c, pgRef)
	ensureImage(t, c, opensshImage)

	// Build the inner test binary for the daemon's platform.
	dir := t.TempDir()
	bin := filepath.Join(dir, "executor.test")
	_, thisFile, _, _ := runtime.Caller(0)
	build := exec.CommandContext(ctx, "go", "test", "-c", "-tags", "integration", "-o", bin, ".") //nolint:gosec // fixed go toolchain invocation; bin is our temp dir
	build.Dir = filepath.Dir(thisFile)
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+sv.Arch, "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build inner test: %v\n%s", err, out)
	}
	binData, err := os.ReadFile(bin) //nolint:gosec // our own build output
	if err != nil {
		t.Fatal(err)
	}

	suffix := randHex(t, 4)
	ctlID, ctlSubnet := createNetwork(t, c, "bekup-it-ctl-"+suffix)
	tgID, tgSubnet := createNetwork(t, c, "bekup-it-targets-"+suffix)

	// SSH bastion (aliases: bastion, db.bekup.test, other.bekup.test).
	_, clientKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(clientKey)
	if err != nil {
		t.Fatal(err)
	}
	bastion := createContainer(t, c, client.ContainerCreateOptions{
		Name: "bekup-it-bastion-" + suffix,
		Config: &container.Config{Image: opensshImage, Env: []string{
			"PUID=1000", "PGID=1000", "TZ=Etc/UTC", "USER_NAME=" + sshUser,
			"PUBLIC_KEY=" + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))),
			"PASSWORD_ACCESS=false", "SUDO_ACCESS=false", "LOG_STDOUT=true",
		}},
		HostConfig: &container.HostConfig{NetworkMode: container.NetworkMode(tgID), CapAdd: []string{"NET_ADMIN"}},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			tgID: {Aliases: []string{"bastion", "db.bekup.test", "other.bekup.test"}},
		}},
	})
	if _, err := c.CopyToContainer(ctx, bastion, client.CopyToContainerOptions{DestinationPath: "/",
		Content: tarOf(t, map[string][]byte{"custom-cont-init.d/10-allow-tcp-forwarding": []byte(enableForwarding)},
			map[string]int64{"custom-cont-init.d/10-allow-tcp-forwarding": 0o555})}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ContainerStart(ctx, bastion, client.ContainerStartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitLog(t, c, bastion, "bastion: AllowTcpForwarding enabled", 1, 3*time.Minute)
	execIn(t, c, bastion, "ip", "addr", "add", literalIP+"/32", "dev", "lo")

	// TLS PostgreSQL sharing the bastion's network namespace.
	caPEM, certPEM, keyPEM := testCerts(t)
	dbPassword := randHex(t, 16)
	pg := createContainer(t, c, client.ContainerCreateOptions{
		Name: "bekup-it-pg-" + suffix,
		Config: &container.Config{Image: pgRef, Env: []string{"POSTGRES_USER=" + dbUser, "POSTGRES_PASSWORD=" + dbPassword},
			Entrypoint: []string{"sh", "-c", "install -o postgres -g postgres -m 0600 /certs/server.key /tmp/server.key && " +
				"install -o postgres -g postgres -m 0644 /certs/server.crt /tmp/server.crt && " +
				"exec docker-entrypoint.sh postgres -c ssl=on -c ssl_cert_file=/tmp/server.crt -c ssl_key_file=/tmp/server.key"}},
		HostConfig: &container.HostConfig{NetworkMode: container.NetworkMode("container:" + bastion)},
	})
	if _, err := c.CopyToContainer(ctx, pg, client.CopyToContainerOptions{DestinationPath: "/",
		Content: tarOf(t, map[string][]byte{"certs/server.crt": certPEM, "certs/server.key": keyPEM}, map[string]int64{"certs/server.key": 0o600})}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ContainerStart(ctx, pg, client.ContainerStartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitLog(t, c, pg, "database system is ready to accept connections", 2, 2*time.Minute)

	hostKey := execIn(t, c, bastion, "cat", "/config/ssh_host_keys/ssh_host_ed25519_key.pub")
	bins, err := c.ContainerInspect(ctx, bastion, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var bastionIP string
	for _, ep := range bins.Container.NetworkSettings.Networks {
		bastionIP = ep.IPAddress.String()
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(clientKey)
	if err != nil {
		t.Fatal(err)
	}
	h, err := json.Marshal(harness{
		CAPEM: string(caPEM), DBPassword: dbPassword, BastionHost: "bastion", BastionIP: bastionIP,
		HostKey: strings.TrimSpace(hostKey), ClientKeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})),
		TargetsSubnet: tgSubnet.String(), CtlSubnet: ctlSubnet.String(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// The runner stand-in: the inner test binary in a container on the
	// control network (+ targets) with the Docker socket.
	inner := createContainer(t, c, client.ContainerCreateOptions{
		Name: "bekup-it-runner-" + suffix,
		Config: &container.Config{
			Image: pgRef, User: "0", Entrypoint: []string{"/harness/executor.test"},
			Cmd: []string{"-test.run", "^TestInnerIsolation$", "-test.v", "-test.timeout", "10m"},
			Env: []string{innerEnv + "=/harness/harness.json"},
		},
		HostConfig: &container.HostConfig{NetworkMode: container.NetworkMode(ctlID),
			Binds: []string{"/var/run/docker.sock:/var/run/docker.sock"}},
	})
	if _, err := c.NetworkConnect(ctx, tgID, client.NetworkConnectOptions{Container: inner}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CopyToContainer(ctx, inner, client.CopyToContainerOptions{DestinationPath: "/",
		Content: tarOf(t, map[string][]byte{"harness/executor.test": binData, "harness/harness.json": h},
			map[string]int64{"harness/executor.test": 0o755, "harness/harness.json": 0o600})}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ContainerStart(ctx, inner, client.ContainerStartOptions{}); err != nil {
		t.Fatal(err)
	}
	w := c.ContainerWait(ctx, inner, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	var code int64
	select {
	case r := <-w.Result:
		code = r.StatusCode
	case err := <-w.Error:
		t.Fatal(err)
	}
	t.Logf("inner test output:\n%s", logsOf(c, inner))
	if code != 0 {
		t.Fatalf("inner test failed (exit %d)", code)
	}

	// Nothing the inner runner created may survive it.
	f := client.Filters{}.Add("label", "bekup.runner="+mustFullID(t, c, inner))
	cl, err := c.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: f})
	if err != nil {
		t.Fatal(err)
	}
	nl, err := c.NetworkList(ctx, client.NetworkListOptions{Filters: f})
	if err != nil {
		t.Fatal(err)
	}
	if len(cl.Items) != 0 || len(nl.Items) != 0 {
		t.Fatalf("leftovers: %d containers, %d networks", len(cl.Items), len(nl.Items))
	}
}

func mustFullID(t *testing.T, c *client.Client, id string) string {
	t.Helper()
	ins, err := c.ContainerInspect(t.Context(), id, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return ins.Container.ID
}

// ignore and ignore2 discard errors of best-effort test cleanup.
func ignore(error) {}

func ignore2[T any](_ T, err error) { ignore(err) }
