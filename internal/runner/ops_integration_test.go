//go:build integration

package runner

// Integration harness of the runner RPCs (task 2.7). As in task 2.6 the
// runner must run inside a container so the executor's self-join is real:
// the outer test (on the host) builds this package's test binary for
// linux, starts the target databases (TLS with a private CA) and an SSH
// bastion on an internal "targets" network, and runs the binary in a
// container on an internal control network (+ targets) with the Docker
// socket. The inner test (TestInnerRunnerRPCs, selected by
// BEKUP_RPC_INNER) starts a real runner.Server with the Docker executor and
// drives every RPC through a real mTLS gRPC client.

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
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
	itInnerEnv = "BEKUP_RPC_INNER"
	// itOpenSSH is the bastion image of docker-compose.dev.yml.
	itOpenSSH = "lscr.io/linuxserver/openssh-server:10.3_p1-r1-ls240@sha256:b07414bb329b86a00525c9256711a7855fc3a369e9f62c36f1de8d1923197580"
	itSSHUser = "tunnel"
	itPGUser  = "bekup"
	itDomain  = ".bekup.test"
)

const itEnableForwarding = `#!/bin/bash
set -euo pipefail
f=/config/sshd/sshd_config
sed -i 's/^AllowTcpForwarding no$/AllowTcpForwarding yes/' "$f"
grep -qx 'AllowTcpForwarding yes' "$f"
echo "bastion: AllowTcpForwarding enabled"
`

// itTarget is one target database of the harness.
type itTarget struct {
	Name      string      `json:"name"`
	Host      string      `json:"host"`
	Port      int         `json:"port"`
	Kind      engine.Kind `json:"kind"`
	Key       string      `json:"key"`
	TLS       string      `json:"tls"` // pinned | unverified | disabled
	Container string      `json:"container"`
	// readyLog / readyCount: the startup line the outer test waits for.
	readyLog   string
	readyCount int
}

// itHarness is passed from the outer to the inner test (a file in the
// inner container; test-only credentials).
type itHarness struct {
	CADER         []byte              `json:"ca_der"`
	OtherCADER    []byte              `json:"other_ca_der"`
	LeafSPKI      []byte              `json:"leaf_spki"`
	Password      string              `json:"password"`
	Targets       map[string]itTarget `json:"targets"`
	BastionHost   string              `json:"bastion_host"`
	HostKey       string              `json:"host_key"`
	SSHKeyPEM     string              `json:"ssh_key_pem"`
	TargetsSubnet string              `json:"targets_subnet"`
}

// itDBSpec describes how a target container is started.
type itDBSpec struct {
	name, key string
	kind      engine.Kind
	tls       string
	port      int
}

var itDBs = []itDBSpec{
	{"pg13", "13", engine.KindPostgres, "pinned", 5432},
	{"pg16", "16", engine.KindPostgres, "pinned", 5432},
	{"pg18", "18", engine.KindPostgres, "pinned", 5432},
	{"mysql84", "8.4", engine.KindMySQL, "pinned", 3306},
	{"mysql97", "9.7", engine.KindMySQL, "unverified", 3306},
	{"maria118", "11.8", engine.KindMariaDB, "pinned", 3306},
	{"maria1011", "10.11", engine.KindMariaDB, "disabled", 3306},
}

func itDocker(t *testing.T) *client.Client {
	t.Helper()
	c, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	t.Cleanup(func() { itIgnore(c.Close()) })
	if _, err := c.Ping(t.Context(), client.PingOptions{}); err != nil {
		dockertest.Skipf(t, "docker not available: %v", err)
	}
	return c
}

func itRandHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func itTar(t *testing.T, files map[string][]byte, modes map[string]int64) io.Reader {
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

type itCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	der  []byte
}

func itNewCA(t *testing.T, name string) *itCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &itCA{cert: c, key: key, der: der}
}

// leaf issues one server certificate for every target name.
func (ca *itCA) leaf(t *testing.T, names []string) (certPEM, keyPEM, spki []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: names[0]},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour),
		DNSNames: names, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	kder, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(c.RawSubjectPublicKeyInfo)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kder}), sum[:]
}

func itNetwork(t *testing.T, c *client.Client, name string) (string, string) {
	t.Helper()
	res, err := c.NetworkCreate(t.Context(), name, client.NetworkCreateOptions{Driver: "bridge", Internal: true,
		Labels: map[string]string{"bekup.integration": "runner-rpcs"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { itIgnore2(c.NetworkRemove(context.Background(), res.ID, client.NetworkRemoveOptions{})) })
	ins, err := c.NetworkInspect(t.Context(), res.ID, client.NetworkInspectOptions{})
	if err != nil || len(ins.Network.IPAM.Config) == 0 {
		t.Fatalf("inspect network: %v", err)
	}
	return res.ID, ins.Network.IPAM.Config[0].Subnet.String()
}

func itContainer(t *testing.T, c *client.Client, opts client.ContainerCreateOptions) string {
	t.Helper()
	res, err := c.ContainerCreate(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		itIgnore2(c.ContainerRemove(context.Background(), res.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}))
	})
	return res.ID
}

// itExec runs cmd in a container (as root) and returns its output; it
// fails the test on a non-zero exit.
func itExec(t *testing.T, c *client.Client, id string, cmd ...string) string {
	t.Helper()
	out, code := itExecCode(t, c, id, cmd...)
	if code != 0 {
		t.Fatalf("exec %v: exit %d: %s", cmd, code, out)
	}
	return out
}

func itExecCode(t *testing.T, c *client.Client, id string, cmd ...string) (string, int) {
	t.Helper()
	stdout, stderr, code := itExecSplit(t, c, id, cmd...)
	return stdout + stderr, code
}

// itExecStdout runs cmd like itExec but returns only its stdout: stderr
// (e.g. the mysql client's "Using a password on the command line" warning)
// is written by the tool independently of stdout, so a combined buffer
// interleaves them in no fixed order. stderr is shown on failure only.
func itExecStdout(t *testing.T, c *client.Client, id string, cmd ...string) string {
	t.Helper()
	stdout, stderr, code := itExecSplit(t, c, id, cmd...)
	if code != 0 {
		t.Fatalf("exec %v: exit %d: %s%s", cmd, code, stdout, stderr)
	}
	return stdout
}

func itExecSplit(t *testing.T, c *client.Client, id string, cmd ...string) (stdout, stderr string, code int) {
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
	var out, errOut bytes.Buffer
	itIgnore2(stdcopy.StdCopy(&out, &errOut, att.Reader))
	ins, err := c.ExecInspect(t.Context(), ex.ID, client.ExecInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return out.String(), errOut.String(), ins.ExitCode
}

func itLogs(c *client.Client, id string) string {
	rc, err := c.ContainerLogs(context.Background(), id, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return ""
	}
	defer func() { itIgnore(rc.Close()) }()
	var out bytes.Buffer
	itIgnore2(stdcopy.StdCopy(&out, &out, rc))
	return out.String()
}

func itWaitLog(t *testing.T, c *client.Client, id, needle string, count int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Count(itLogs(c, id), needle) >= count {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("container %s: %q not logged %d times:\n%s", id[:12], needle, count, itLogs(c, id))
}

func itEnsureImage(t *testing.T, c *client.Client, ref string) {
	t.Helper()
	imagepull.Ensure(t, c, ref)
}

// itStartDB starts one target database on the targets network.
func itStartDB(t *testing.T, c *client.Client, cat *engine.Catalog, d itDBSpec, netID, suffix, pw string, files map[string][]byte) itTarget {
	t.Helper()
	ent, ok := cat.Lookup(d.kind, d.key)
	if !ok {
		t.Fatalf("no catalog entry %s %s", d.kind, d.key)
	}
	ref := spec.ImageRef(ent)
	itEnsureImage(t, c, ref)
	host := d.name + itDomain
	var envs, entry []string
	ready, count := "", 2
	switch d.kind {
	case engine.KindPostgres:
		envs = []string{"POSTGRES_USER=" + itPGUser, "POSTGRES_PASSWORD=" + pw, "POSTGRES_DB=app"}
		entry = []string{"sh", "-c", "install -o postgres -g postgres -m 0600 /certs/server.key /tmp/server.key && " +
			"install -o postgres -g postgres -m 0644 /certs/server.crt /tmp/server.crt && " +
			"exec docker-entrypoint.sh postgres -c ssl=on -c ssl_cert_file=/tmp/server.crt -c ssl_key_file=/tmp/server.key"}
		ready = "database system is ready to accept connections"
	case engine.KindMySQL:
		envs = []string{"MYSQL_ROOT_PASSWORD=" + pw, "MYSQL_ROOT_HOST=%", "MYSQL_DATABASE=shop"}
		entry = []string{"docker-entrypoint.sh", "mysqld"}
		if d.tls == "pinned" {
			entry = append(entry, "--ssl-cert=/certs/server.crt", "--ssl-key=/certs/server.key", "--ssl-ca=/certs/ca.pem")
		}
		ready, count = "port: 3306  MySQL", 1 // not the init server (port 0) or the X plugin
	case engine.KindMariaDB:
		envs = []string{"MARIADB_ROOT_PASSWORD=" + pw, "MARIADB_ROOT_HOST=%", "MARIADB_DATABASE=shop"}
		entry = []string{"docker-entrypoint.sh", "mariadbd"}
		if d.tls == "pinned" {
			entry = append(entry, "--ssl-cert=/certs/server.crt", "--ssl-key=/certs/server.key")
		} else {
			entry = append(entry, "--skip-ssl")
		}
		ready, count = "port: 3306", 1 // not the init server (port 0)
	}
	id := itContainer(t, c, client.ContainerCreateOptions{
		Name:       "bekup-it-" + d.name + "-" + suffix,
		Config:     &container.Config{Image: ref, Env: envs, Entrypoint: entry[:1], Cmd: entry[1:]},
		HostConfig: &container.HostConfig{NetworkMode: container.NetworkMode(netID)},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			netID: {Aliases: []string{host}},
		}},
	})
	if _, err := c.CopyToContainer(t.Context(), id, client.CopyToContainerOptions{DestinationPath: "/", Content: itTar(t, files, nil)}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ContainerStart(t.Context(), id, client.ContainerStartOptions{}); err != nil {
		t.Fatal(err)
	}
	return itTarget{Name: d.name, Host: host, Port: d.port, Kind: d.kind, Key: d.key, TLS: d.tls, Container: id, readyLog: ready, readyCount: count}
}

func TestIntegrationRunnerRPCs(t *testing.T) {
	if os.Getenv(itInnerEnv) != "" {
		t.Skip("outer harness")
	}
	c := itDocker(t)
	ctx := t.Context()
	sv, err := c.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil || sv.Os != "linux" {
		dockertest.Skipf(t, "needs a linux Docker daemon (%v)", err)
	}
	cat, err := engine.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	pg17, _ := cat.Lookup(engine.KindPostgres, "17")
	baseRef := spec.ImageRef(pg17)
	itEnsureImage(t, c, baseRef)
	itEnsureImage(t, c, itOpenSSH)

	// Build the inner test binary for the daemon's platform.
	dir := t.TempDir()
	bin := filepath.Join(dir, "runner.test")
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

	suffix := itRandHex(t, 4)
	ctlID, _ := itNetwork(t, c, "bekup-it-rpc-ctl-"+suffix)
	tgID, tgSubnet := itNetwork(t, c, "bekup-it-rpc-targets-"+suffix)

	// TLS material: one leaf for every target name, issued by ca; other is
	// an unrelated CA (pin mismatch).
	ca, other := itNewCA(t, "bekup rpc integration CA"), itNewCA(t, "unrelated CA")
	names := make([]string, 0, len(itDBs))
	for _, d := range itDBs {
		names = append(names, d.name+itDomain)
	}
	certPEM, keyPEM, spki := ca.leaf(t, names)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.der})
	files := map[string][]byte{"certs/server.crt": certPEM, "certs/server.key": keyPEM, "certs/ca.pem": caPEM}
	pw := itRandHex(t, 16)

	targets := map[string]itTarget{}
	for _, d := range itDBs {
		targets[d.name] = itStartDB(t, c, cat, d, tgID, suffix, pw, files)
	}

	// SSH bastion on the targets network (it resolves the targets' names).
	_, clientKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(clientKey)
	if err != nil {
		t.Fatal(err)
	}
	bastionHost := "bastion" + itDomain
	bastion := itContainer(t, c, client.ContainerCreateOptions{
		Name: "bekup-it-rpc-bastion-" + suffix,
		Config: &container.Config{Image: itOpenSSH, Env: []string{
			"PUID=1000", "PGID=1000", "TZ=Etc/UTC", "USER_NAME=" + itSSHUser,
			"PUBLIC_KEY=" + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))),
			"PASSWORD_ACCESS=false", "SUDO_ACCESS=false", "LOG_STDOUT=true",
		}},
		HostConfig: &container.HostConfig{NetworkMode: container.NetworkMode(tgID)},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			tgID: {Aliases: []string{bastionHost}},
		}},
	})
	if _, err := c.CopyToContainer(ctx, bastion, client.CopyToContainerOptions{DestinationPath: "/",
		Content: itTar(t, map[string][]byte{"custom-cont-init.d/10-allow-tcp-forwarding": []byte(itEnableForwarding)},
			map[string]int64{"custom-cont-init.d/10-allow-tcp-forwarding": 0o555})}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ContainerStart(ctx, bastion, client.ContainerStartOptions{}); err != nil {
		t.Fatal(err)
	}

	for _, tg := range targets {
		itWaitLog(t, c, tg.Container, tg.readyLog, tg.readyCount, 4*time.Minute)
	}
	itWaitLog(t, c, bastion, "bastion: AllowTcpForwarding enabled", 1, 3*time.Minute)
	hostKey := itExec(t, c, bastion, "cat", "/config/ssh_host_keys/ssh_host_ed25519_key.pub")
	pkcs8, err := x509.MarshalPKCS8PrivateKey(clientKey)
	if err != nil {
		t.Fatal(err)
	}
	h, err := json.Marshal(itHarness{ //nolint:gosec // test-only credentials for the inner harness container
		CADER: ca.der, OtherCADER: other.der, LeafSPKI: spki, Password: pw, Targets: targets,
		BastionHost: bastionHost, HostKey: strings.TrimSpace(hostKey),
		SSHKeyPEM:     string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})),
		TargetsSubnet: tgSubnet,
	})
	if err != nil {
		t.Fatal(err)
	}

	inner := itContainer(t, c, client.ContainerCreateOptions{
		Name: "bekup-it-rpc-runner-" + suffix,
		Config: &container.Config{
			Image: baseRef, User: "0", Entrypoint: []string{"/harness/runner.test"},
			Cmd: []string{"-test.run", "^TestInnerRunnerRPCs$", "-test.v", "-test.timeout", "20m"},
			Env: []string{itInnerEnv + "=/harness/harness.json"},
		},
		HostConfig: &container.HostConfig{NetworkMode: container.NetworkMode(ctlID),
			Binds: []string{"/var/run/docker.sock:/var/run/docker.sock"}},
	})
	if _, err := c.NetworkConnect(ctx, tgID, client.NetworkConnectOptions{Container: inner}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CopyToContainer(ctx, inner, client.CopyToContainerOptions{DestinationPath: "/",
		Content: itTar(t, map[string][]byte{"harness/runner.test": binData, "harness/harness.json": h},
			map[string]int64{"harness/runner.test": 0o755, "harness/harness.json": 0o600})}); err != nil {
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
	out := itLogs(c, inner)
	t.Logf("inner test output:\n%s", out)
	if code != 0 {
		t.Fatalf("inner test failed (exit %d)", code)
	}
	if strings.Contains(out, pw) {
		t.Fatal("the database password appeared in the runner's output")
	}

	// Nothing the inner runner created may survive it.
	ins, err := c.ContainerInspect(ctx, inner, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f := client.Filters{}.Add("label", "bekup.runner="+ins.Container.ID)
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

// itIgnore and itIgnore2 discard errors of best-effort test cleanup.
func itIgnore(error) {}

func itIgnore2[T any](_ T, err error) { itIgnore(err) }
