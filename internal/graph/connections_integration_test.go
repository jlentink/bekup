//go:build integration

package graph_test

// GraphQL connections API against a real runner (task 2.11). The outer
// test builds the bekup binary for the Docker daemon's platform and runs
// `bekup runner` in a container (as in the 2.6/2.7 harnesses), with a
// TLS PostgreSQL target (leaf issued by a private CA, chain presented) and
// an OpenSSH bastion on an internal "targets" network. The runner's mTLS
// port is published on 127.0.0.1 so serve's GraphQL handler (in this test
// process) reaches it through internal/runnerclient; serve itself never
// reaches the targets.

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/graph"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/runner/spec"
	"github.com/jlentink/bekup/internal/runnerclient"
	"github.com/jlentink/bekup/internal/testutil/dockertest"
	"github.com/jlentink/bekup/internal/testutil/runnertest"
)

const (
	crOpenSSH  = "lscr.io/linuxserver/openssh-server:10.3_p1-r1-ls240@sha256:b07414bb329b86a00525c9256711a7855fc3a369e9f62c36f1de8d1923197580"
	crPGHost   = "pg.bekup.test"
	crBastion  = "bastion.bekup.test"
	crSSHUser  = "tunnel"
	crPGUser   = "bekup"
	crDBPass   = "DB-PASSWORD-graph-7f3e9a"
	crSSHPass  = "SSH-PASSWORD-graph-2b8c41"
	crRunnerTO = 3 * time.Minute
)

const crEnableForwarding = `#!/bin/bash
set -euo pipefail
f=/config/sshd/sshd_config
sed -i 's/^AllowTcpForwarding no$/AllowTcpForwarding yes/' "$f"
grep -qx 'AllowTcpForwarding yes' "$f"
echo "bastion: AllowTcpForwarding enabled"
`

func crIgnore(error) {}

func crIgnore2[T any](_ T, err error) { crIgnore(err) }

func crTar(t *testing.T, files map[string][]byte, modes map[string]int64) io.Reader {
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

type crCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func crNewCA(t *testing.T) *crCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "bekup graph integration CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(400 * 24 * time.Hour),
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
	return &crCA{cert: c, key: key}
}

// leaf issues a server certificate for crPGHost and returns the chain
// (leaf + CA) and key as PEM.
func (ca *crCA) leaf(t *testing.T) (chainPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: crPGHost},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour),
		DNSNames: []string{crPGHost}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	kder, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	chain := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw})...)
	return chain, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kder})
}

type crHarness struct {
	t          *testing.T
	c          *client.Client
	pg         string
	bastion    string
	runner     string
	runnerAddr string
	pki        certs.Layout
	ca         *crCA
}

func (h *crHarness) logs(id string) string {
	rc, err := h.c.ContainerLogs(context.Background(), id, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return ""
	}
	defer func() { crIgnore(rc.Close()) }()
	var out bytes.Buffer
	crIgnore2(stdcopy.StdCopy(&out, &out, rc))
	return out.String()
}

func (h *crHarness) waitLog(id, needle string, count int, timeout time.Duration) {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Count(h.logs(id), needle) >= count {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	h.t.Fatalf("container %s: %q not logged %d times:\n%s", id[:12], needle, count, h.logs(id))
}

func (h *crHarness) exec(id string, cmd ...string) string {
	h.t.Helper()
	ex, err := h.c.ExecCreate(h.t.Context(), id, client.ExecCreateOptions{User: "root", Cmd: cmd, AttachStdout: true, AttachStderr: true})
	if err != nil {
		h.t.Fatal(err)
	}
	att, err := h.c.ExecAttach(h.t.Context(), ex.ID, client.ExecAttachOptions{})
	if err != nil {
		h.t.Fatal(err)
	}
	defer att.Close()
	var out bytes.Buffer
	crIgnore2(stdcopy.StdCopy(&out, &out, att.Reader))
	ins, err := h.c.ExecInspect(h.t.Context(), ex.ID, client.ExecInspectOptions{})
	if err != nil {
		h.t.Fatal(err)
	}
	if ins.ExitCode != 0 {
		h.t.Fatalf("exec %v: exit %d: %s", cmd, ins.ExitCode, out.String())
	}
	return out.String()
}

func (h *crHarness) container(opts client.ContainerCreateOptions) string {
	h.t.Helper()
	res, err := h.c.ContainerCreate(h.t.Context(), opts)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() {
		crIgnore2(h.c.ContainerRemove(context.Background(), res.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}))
	})
	return res.ID
}

func (h *crHarness) network(name string, internal bool) (string, string) {
	h.t.Helper()
	res, err := h.c.NetworkCreate(h.t.Context(), name, client.NetworkCreateOptions{Driver: "bridge", Internal: internal,
		Labels: map[string]string{"bekup.integration": "graph-connections"}})
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { crIgnore2(h.c.NetworkRemove(context.Background(), res.ID, client.NetworkRemoveOptions{})) })
	ins, err := h.c.NetworkInspect(h.t.Context(), res.ID, client.NetworkInspectOptions{})
	if err != nil || len(ins.Network.IPAM.Config) == 0 {
		h.t.Fatalf("inspect network: %v", err)
	}
	return res.ID, ins.Network.IPAM.Config[0].Subnet.String()
}

func (h *crHarness) ensureImage(ref string) {
	h.t.Helper()
	if _, err := h.c.ImageInspect(h.t.Context(), ref); err == nil {
		return
	}
	// Registries rate-limit bursts of pulls (CI: "toomanyrequests"):
	// retry those with a growing pause, fail on anything else.
	var err error
	for attempt := range 5 {
		if attempt > 0 {
			select {
			case <-h.t.Context().Done():
				h.t.Fatal(h.t.Context().Err())
			case <-time.After(time.Duration(attempt) * 3 * time.Second):
			}
		}
		var resp client.ImagePullResponse
		if resp, err = h.c.ImagePull(h.t.Context(), ref, client.ImagePullOptions{}); err == nil {
			err = resp.Wait(h.t.Context())
		}
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "toomanyrequests") {
			break
		}
	}
	if err != nil {
		h.t.Fatal(err)
	}
}

func (h *crHarness) copy(id string, files map[string][]byte, modes map[string]int64) {
	h.t.Helper()
	if _, err := h.c.CopyToContainer(h.t.Context(), id, client.CopyToContainerOptions{DestinationPath: "/", Content: crTar(h.t, files, modes)}); err != nil {
		h.t.Fatal(err)
	}
}

// swapCert installs a new server certificate in the target and reloads it.
func (h *crHarness) swapCert(chainPEM, keyPEM []byte) {
	h.t.Helper()
	h.copy(h.pg, map[string][]byte{"certs/server.crt": chainPEM, "certs/server.key": keyPEM}, nil)
	h.exec(h.pg, "sh", "-c", "install -o postgres -g postgres -m 0600 /certs/server.key /tmp/server.key && "+
		"install -o postgres -g postgres -m 0644 /certs/server.crt /tmp/server.crt && kill -HUP 1")
}

func startConnHarness(t *testing.T) *crHarness {
	t.Helper()
	c, err := client.New(client.FromEnv)
	if err != nil {
		dockertest.Skipf(t, "docker client: %v", err)
	}
	t.Cleanup(func() { crIgnore(c.Close()) })
	ctx := t.Context()
	if _, err := c.Ping(ctx, client.PingOptions{}); err != nil {
		dockertest.Skipf(t, "docker not available: %v", err)
	}
	sv, err := c.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil || sv.Os != "linux" {
		dockertest.Skipf(t, "needs a linux Docker daemon (%v)", err)
	}
	h := &crHarness{t: t, c: c}
	cat, err := engine.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	pg17, _ := cat.Lookup(engine.KindPostgres, "17")
	pgRef := spec.ImageRef(pg17)
	h.ensureImage(pgRef)
	h.ensureImage(crOpenSSH)

	// The real bekup binary for the daemon's platform.
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	bin := filepath.Join(t.TempDir(), "bekup")
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, "./cmd/bekup") //nolint:gosec // fixed go toolchain invocation; bin is our temp dir
	build.Dir = root
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+sv.Arch, "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build bekup: %v\n%s", err, out)
	}
	binData, err := os.ReadFile(bin) //nolint:gosec // our own build output
	if err != nil {
		t.Fatal(err)
	}

	suffix := fmt.Sprintf("%x", time.Now().UnixNano()&0xffffff)
	ctlID, _ := h.network("bekup-it-conn-ctl-"+suffix, false)
	tgID, tgSubnet := h.network("bekup-it-conn-targets-"+suffix, true)

	// Target PostgreSQL with a CA-issued leaf (chain presented).
	h.ca = crNewCA(t)
	chain, key := h.ca.leaf(t)
	h.pg = h.container(client.ContainerCreateOptions{
		Name: "bekup-it-conn-pg-" + suffix,
		Config: &container.Config{Image: pgRef, Env: []string{"POSTGRES_USER=" + crPGUser, "POSTGRES_PASSWORD=" + crDBPass, "POSTGRES_DB=app"},
			Entrypoint: []string{"sh"}, Cmd: []string{"-c", "install -o postgres -g postgres -m 0600 /certs/server.key /tmp/server.key && " +
				"install -o postgres -g postgres -m 0644 /certs/server.crt /tmp/server.crt && " +
				"exec docker-entrypoint.sh postgres -c ssl=on -c ssl_cert_file=/tmp/server.crt -c ssl_key_file=/tmp/server.key"}},
		HostConfig: &container.HostConfig{NetworkMode: container.NetworkMode(tgID)},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			tgID: {Aliases: []string{crPGHost}},
		}},
	})
	h.copy(h.pg, map[string][]byte{"certs/server.crt": chain, "certs/server.key": key}, nil)
	if _, err := c.ContainerStart(ctx, h.pg, client.ContainerStartOptions{}); err != nil {
		t.Fatal(err)
	}

	// SSH bastion (password login, TCP forwarding enabled).
	h.bastion = h.container(client.ContainerCreateOptions{
		Name: "bekup-it-conn-bastion-" + suffix,
		Config: &container.Config{Image: crOpenSSH, Env: []string{
			"PUID=1000", "PGID=1000", "TZ=Etc/UTC", "USER_NAME=" + crSSHUser, "USER_PASSWORD=" + crSSHPass,
			"PASSWORD_ACCESS=true", "SUDO_ACCESS=false", "LOG_STDOUT=true",
		}},
		HostConfig: &container.HostConfig{NetworkMode: container.NetworkMode(tgID)},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			tgID: {Aliases: []string{crBastion}},
		}},
	})
	h.copy(h.bastion, map[string][]byte{"custom-cont-init.d/10-allow-tcp-forwarding": []byte(crEnableForwarding)},
		map[string]int64{"custom-cont-init.d/10-allow-tcp-forwarding": 0o555})
	if _, err := c.ContainerStart(ctx, h.bastion, client.ContainerStartOptions{}); err != nil {
		t.Fatal(err)
	}

	// The runner: `bekup runner` with its own runner.toml, the private CA,
	// the Docker socket (PrePull) and the targets network; its mTLS port is
	// published on 127.0.0.1 for this test process.
	pkiDir := filepath.Join(t.TempDir(), "pki")
	if _, err := certs.Init(certs.InitOptions{Dir: pkiDir, Components: []string{certs.ComponentRunner, certs.ComponentServe}, AllowUnencrypted: true}); err != nil {
		t.Fatal(err)
	}
	h.pki = certs.Layout{Dir: pkiDir}
	read := func(p string) []byte {
		b, err := os.ReadFile(p) //nolint:gosec // test PKI
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	runnerTOML := fmt.Sprintf(`[listen]
address = "0.0.0.0:7443"
allow_wildcard = true
health_socket = ""

[tls]
cert_file = "/bekup/runner.crt"
key_file = "/bekup/runner.key"
client_ca_file = "/bekup/ca.crt"
allowed_client_sans = ["spiffe://bekup/serve"]

[docker]
host = "unix:///var/run/docker.sock"

[targets]
allow_cidrs = [%q]
`, tgSubnet)
	port := network.MustParsePort("7443/tcp")
	h.runner = h.container(client.ContainerCreateOptions{
		Name: "bekup-it-conn-runner-" + suffix,
		Config: &container.Config{
			Image: pgRef, User: "0", Entrypoint: []string{"/bekup/bekup"},
			Cmd:          []string{"runner", "--config", "/bekup/runner.toml"},
			ExposedPorts: network.PortSet{port: {}},
		},
		HostConfig: &container.HostConfig{
			NetworkMode:  container.NetworkMode(ctlID),
			Binds:        []string{"/var/run/docker.sock:/var/run/docker.sock"},
			PortBindings: network.PortMap{port: {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: "0"}}},
		},
	})
	if _, err := c.NetworkConnect(ctx, tgID, client.NetworkConnectOptions{Container: h.runner}); err != nil {
		t.Fatal(err)
	}
	h.copy(h.runner, map[string][]byte{
		"bekup/bekup": binData, "bekup/runner.toml": []byte(runnerTOML),
		"bekup/runner.crt": read(h.pki.CertFile(certs.ComponentRunner)),
		"bekup/runner.key": read(h.pki.KeyFile(certs.ComponentRunner)),
		"bekup/ca.crt":     read(h.pki.CACert()),
	}, map[string]int64{"bekup/bekup": 0o755, "bekup/runner.key": 0o600})
	if _, err := c.ContainerStart(ctx, h.runner, client.ContainerStartOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("runner logs:\n%s", h.logs(h.runner))
		}
	})

	h.waitLog(h.pg, "database system is ready to accept connections", 2, 4*time.Minute)
	h.waitLog(h.bastion, "bastion: AllowTcpForwarding enabled", 1, 3*time.Minute)
	h.waitLog(h.runner, "runner listening", 1, time.Minute)
	ins, err := c.ContainerInspect(ctx, h.runner, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b := ins.Container.NetworkSettings.Ports[port]
	if len(b) == 0 {
		t.Fatal("runner port not published")
	}
	h.runnerAddr = net.JoinHostPort("127.0.0.1", b[0].HostPort)
	return h
}

// newConnEnv is a destEnv-like GraphQL environment whose connection
// service talks to the harness runner over mTLS.
func newConnEnv(t *testing.T, h *crHarness) *destEnv {
	t.Helper()
	d := dbtest.Migrated(t)
	e := &authEnv{t: t, d: d, pool: d.AppPool(t)}
	e.auditor = audit.New(e.pool, audit.Options{Registry: logging.NewRegistry()})
	var err error
	e.hasher, err = auth.NewHasher(auth.HasherOptions{
		Params: auth.Argon2Params{MemoryKiB: 64, Iterations: 1, Parallelism: 1}, MaxConcurrent: 4, WeakForTests: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessions(e.pool, auth.SessionOptions{})
	svc, err := auth.NewService(auth.ServiceOptions{DB: e.pool, Hasher: e.hasher, Sessions: sessions, Auditor: e.auditor, Factors: auth.NewSecondFactors()})
	if err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kr.Destroy)
	tlsCfg, err := certs.ClientTLSConfig(h.pki.CertFile(certs.ComponentServe), h.pki.KeyFile(certs.ComponentServe), h.pki.CACert(), "runner")
	if err != nil {
		t.Fatal(err)
	}
	rc, err := runnerclient.NewWithTLS(h.runnerAddr, tlsCfg, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { crIgnore(rc.Close()) })
	// "runner listening" precedes the executor's own-container detection.
	runnertest.WaitDockerOK(t, rc, 0, nil)
	conns, err := connections.New(connections.Options{
		DB: e.pool, Keyring: kr, Auditor: e.auditor, Runner: rc.RPC(), OperationTimeout: crRunnerTO,
		Limiter: func(string) (bool, time.Duration) { return true, 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	pol := authz.New(authz.Options{})
	logger := slog.New(slog.DiscardHandler)
	gql, err := graph.New(graph.Options{
		Logger: logger, MaxComplexity: 2000, MaxDepth: 12, MaxBodyBytes: 1 << 16,
		Access: graph.NewPolicyAccess(pol), Policy: pol, AuditLog: e.auditor, Auth: svc, Connections: conns,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := httpserver.New(httpserver.Options{
		BaseURL: testBaseURL, MaxBodyBytes: 1 << 20, Logger: logger,
		Ready:     func(context.Context) error { return nil },
		RateLimit: httpserver.RateLimitOptions{PerSecond: 1000, Burst: 1000, MaxClients: 100},
		GraphQL:   gql,
		Sessions: &httpserver.SessionOptions{
			Resolver: httpserver.SessionsResolver{Sessions: sessions},
			Cookies:  auth.CookiePolicyFor(testBaseURL),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.h = srv.Handler()
	return &destEnv{authEnv: e}
}

const (
	connFields = `id name engine versionKey legacyVersion host port tlsMode insecureTransport pinStatus pinExpiresAt sshEnabled ready notReadyReason
		settings { imageKey username hasPassword clientCertificatePem
			tlsPin { certificatePem subject issuer isCa leafSpkiSha256 notAfter pinnedAt }
			backupPin { subject leafSpkiSha256 }
			ssh { host port user authMethod hasPassword hasPrivateKey hasPassphrase publicKey hostKey { type fingerprintSha256 authorizedKey confirmedAt } } }`
	createConnMut  = `mutation($in: CreateConnectionInput!) { createConnection(input: $in) { ` + connFields + ` } }`
	updateConnMut  = `mutation($id: UUID!, $in: UpdateConnectionInput!) { updateConnection(id: $id, input: $in) { ` + connFields + ` } }`
	listConnQuery  = `{ connections { ` + connFields + ` } }`
	testConnMut    = `mutation($id: UUID!) { testConnection(id: $id) { ok code message serverVersion detectedVersionKey compatible legacy warnings databaseCount } }`
	detectMut      = `mutation($id: UUID!) { detectVersion(id: $id) { serverVersion suggestedVersionKey legacy currentCompatible warnings } }`
	listDBMut      = `mutation($id: UUID!) { listDatabases(id: $id) { databases warnings } }`
	prePullMut     = `mutation($id: UUID!) { prePullImage(id: $id) { imageRef alreadyPresent } }`
	fetchCertMut   = `mutation($id: UUID!) { fetchCertificate(id: $id) { suggestedAnchorIndex leafSpkiSha256 certificates { subject issuer isCa selfSigned spkiSha256 spkiSha256Hex fingerprintSha256 dnsNames notAfter } } }`
	confirmPinMut  = `mutation($id: UUID!, $in: CertificatePinInput!) { confirmCertificatePin(id: $id, input: $in) { ` + connFields + ` } }`
	fetchHostMut   = `mutation($id: UUID!) { fetchSshHostKey(id: $id) { type fingerprintSha256 authorizedKey host port } }`
	confirmHostMut = `mutation($id: UUID!, $k: String!, $h: String!, $p: Int!) { confirmSshHostKey(id: $id, authorizedKey: $k, expectedHost: $h, expectedPort: $p) { ` + connFields + ` } }`
	setTLSMut      = `mutation($id: UUID!, $m: ConnectionTlsMode!) { setTlsMode(id: $id, mode: $m) { ` + connFields + ` } }`
	genKeyMut      = `mutation($id: UUID!) { generateConnectionSshKeypair(id: $id) { publicKey fingerprintSha256 connection { id } } }`
	deleteConnMut  = `mutation($id: UUID!) { deleteConnection(id: $id) }`
)

type connModel struct {
	ID                string
	Name              string
	Engine            string
	VersionKey        string
	TLSMode           string `json:"tlsMode"`
	InsecureTransport bool
	PinStatus         string
	SSHEnabled        bool `json:"sshEnabled"`
	Ready             bool
	NotReadyReason    *string
	Settings          *struct {
		ImageKey    string
		Username    string
		HasPassword bool
		TLSPin      *struct {
			IsCa           bool
			LeafSpkiSha256 *string
		} `json:"tlsPin"`
		SSH *struct {
			AuthMethod string
			PublicKey  *string
			HostKey    *struct{ AuthorizedKey string }
		} `json:"ssh"`
	}
}

type testResult struct {
	OK                 bool `json:"ok"`
	Code               *string
	ServerVersion      *string
	DetectedVersionKey *string
	Compatible         bool
	DatabaseCount      int
}

func TestConnectionsGraphQLWithRunner(t *testing.T) {
	h := startConnHarness(t)
	e := newConnEnv(t, h)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	e.user("plain@example.com", sqlc.UserRoleUser)
	adminC := e.mustLogin("admin@example.com")
	maintC := e.mustLogin("maint@example.com")
	plainC := e.mustLogin("plain@example.com")

	newConn := map[string]any{"in": map[string]any{
		"name": "Primary PG", "engine": "POSTGRES", "versionKey": "17", "host": crPGHost, "username": crPGUser, "password": crDBPass,
	}}
	// Step-up is enforced for every change; maintainers and users cannot.
	e.staleStepUp(admin.ID)
	if r := e.gql(adminC, createConnMut, newConn); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("create without step-up: %+v", r.Errors)
	}
	for name, c := range map[string]string{"maintainer": maintC, "user": plainC} {
		if r := e.gql(c, createConnMut, newConn); r.code() != "FORBIDDEN" {
			t.Fatalf("%s create: %+v", name, r.Errors)
		}
	}
	if r := e.gql(plainC, listConnQuery, nil); r.code() != "FORBIDDEN" {
		t.Fatalf("user list: %+v", r.Errors)
	}
	e.freshStepUp(admin.ID)
	var conn connModel
	e.mustGQL(adminC, createConnMut, newConn).field(t, "createConnection", &conn)
	if conn.Ready || conn.PinStatus != "MISSING" || conn.TLSMode != "PINNED" || conn.Settings == nil || !conn.Settings.HasPassword || conn.Settings.ImageKey != "17" {
		t.Fatalf("created: %+v", conn)
	}
	id := conn.ID

	// D-132: a pinned IP literal without a tunnel is refused with the reason.
	r := e.gql(adminC, createConnMut, map[string]any{"in": map[string]any{
		"name": "By IP", "engine": "POSTGRES", "versionKey": "17", "host": "10.9.8.7", "username": "u",
	}})
	if r.code() != "VALIDATION" || !strings.Contains(r.Errors[0].Message, "D-132") {
		t.Fatalf("ip literal: %+v", r.Errors)
	}

	// Not ready before the pin; fetching certificates is admin only.
	if r := e.gql(maintC, testConnMut, map[string]any{"id": id}); r.code() != "CONFLICT" {
		t.Fatalf("test before pin: %+v", r.Errors)
	}
	if r := e.gql(maintC, fetchCertMut, map[string]any{"id": id}); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer fetch: %+v", r.Errors)
	}
	var chain struct {
		SuggestedAnchorIndex *int
		LeafSpkiSha256       string
		Certificates         []struct {
			Subject       string
			IsCa          bool
			SelfSigned    bool
			SpkiSha256    string
			SpkiSha256Hex string
		}
	}
	e.mustGQL(adminC, fetchCertMut, map[string]any{"id": id}).field(t, "fetchCertificate", &chain)
	if len(chain.Certificates) != 2 || chain.SuggestedAnchorIndex == nil || *chain.SuggestedAnchorIndex != 1 ||
		!chain.Certificates[1].IsCa || chain.Certificates[0].SpkiSha256 != chain.LeafSpkiSha256 {
		t.Fatalf("chain: %+v", chain)
	}
	// Single-root: one runner call per request.
	if r := e.gql(adminC, `mutation($id: UUID!) { a: fetchCertificate(id: $id) { leafSpkiSha256 } b: fetchCertificate(id: $id) { leafSpkiSha256 } }`,
		map[string]any{"id": id}); r.code() != "VALIDATION" {
		t.Fatalf("aliased fetch: %+v", r.Errors)
	}

	// Confirm the CA anchor bound to the leaf we compared (hex works too).
	e.mustGQL(adminC, confirmPinMut, map[string]any{"id": id, "in": map[string]any{
		"certIndex": *chain.SuggestedAnchorIndex, "leafSpkiSha256": chain.Certificates[0].SpkiSha256Hex,
	}}).field(t, "confirmCertificatePin", &conn)
	if !conn.Ready || conn.PinStatus != "VALID" || conn.Settings.TLSPin == nil || !conn.Settings.TLSPin.IsCa || conn.Settings.TLSPin.LeafSpkiSha256 == nil {
		t.Fatalf("pinned: %+v", conn)
	}

	// Maintainers test, detect and list; they see metadata only.
	var res testResult
	e.mustGQL(maintC, testConnMut, map[string]any{"id": id}).field(t, "testConnection", &res)
	if !res.OK || res.ServerVersion == nil || !strings.HasPrefix(*res.ServerVersion, "17") || !res.Compatible ||
		res.DetectedVersionKey == nil || *res.DetectedVersionKey != "17" || res.DatabaseCount < 1 {
		t.Fatalf("test: %+v", res)
	}
	var ver struct {
		ServerVersion       string
		SuggestedVersionKey *string
		CurrentCompatible   bool
	}
	e.mustGQL(maintC, detectMut, map[string]any{"id": id}).field(t, "detectVersion", &ver)
	if ver.SuggestedVersionKey == nil || *ver.SuggestedVersionKey != "17" || !ver.CurrentCompatible {
		t.Fatalf("detect: %+v", ver)
	}
	var dbs struct{ Databases []string }
	e.mustGQL(maintC, listDBMut, map[string]any{"id": id}).field(t, "listDatabases", &dbs)
	if !slices.Contains(dbs.Databases, "app") || slices.Contains(dbs.Databases, "template1") {
		t.Fatalf("databases: %v", dbs.Databases)
	}
	var list []connModel
	e.mustGQL(maintC, listConnQuery, nil).field(t, "connections", &list)
	if len(list) != 1 || list[0].Settings != nil || list[0].Name != "Primary PG" || list[0].PinStatus != "VALID" {
		t.Fatalf("maintainer list: %+v", list)
	}
	var pull struct {
		ImageRef       string
		AlreadyPresent bool
	}
	// Maintainers may pre-pull (D-106; D-135 amended).
	e.mustGQL(maintC, prePullMut, map[string]any{"id": id}).field(t, "prePullImage", &pull)
	if !pull.AlreadyPresent || !strings.Contains(pull.ImageRef, "@sha256:") {
		t.Fatalf("pre-pull: %+v", pull)
	}

	// A swapped certificate (another leaf from the same CA) fails the
	// runner's leaf-SPKI pre-flight: typed error, audited.
	newChain, newKey := h.ca.leaf(t)
	h.swapCert(newChain, newKey)
	var mismatch gqlResp
	for range 40 {
		mismatch = e.gql(maintC, testConnMut, map[string]any{"id": id})
		if mismatch.code() == "TLS_PIN_MISMATCH" {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if mismatch.code() != "TLS_PIN_MISMATCH" {
		t.Fatalf("swapped certificate: %+v", mismatch.Errors)
	}
	if n := countAudit(t, e.auditor, audit.ActionConnectionPinMismatch); n < 1 {
		t.Fatalf("pin mismatch audit entries: %d", n)
	}
	// Re-confirm the new leaf: tests pass again.
	e.mustGQL(adminC, fetchCertMut, map[string]any{"id": id}).field(t, "fetchCertificate", &chain)
	e.freshStepUp(admin.ID)
	e.mustGQL(adminC, confirmPinMut, map[string]any{"id": id, "in": map[string]any{
		"certIndex": 1, "leafSpkiSha256": chain.LeafSpkiSha256,
	}}).field(t, "confirmCertificatePin", &conn)
	e.mustGQL(maintC, testConnMut, map[string]any{"id": id}).field(t, "testConnection", &res)
	if !res.OK {
		t.Fatalf("test after re-pin: %+v", res)
	}

	// SSH tunnel: TOFU host key through the runner, confirm, test. The
	// route to the database changes, so the stored database password must
	// be entered again (D-140).
	e.freshStepUp(admin.ID)
	sshIn := map[string]any{"host": crBastion, "port": 2222, "user": crSSHUser, "password": crSSHPass}
	if r := e.gql(adminC, updateConnMut, map[string]any{"id": id, "in": map[string]any{"ssh": sshIn}}); r.code() != "VALIDATION" {
		t.Fatalf("tunnel without re-entering the database password: %+v", r.Errors)
	}
	e.mustGQL(adminC, updateConnMut, map[string]any{"id": id, "in": map[string]any{
		"ssh": sshIn, "password": crDBPass,
	}}).field(t, "updateConnection", &conn)
	if conn.Ready || !conn.SSHEnabled || conn.Settings.SSH.AuthMethod != "PASSWORD" {
		t.Fatalf("ssh: %+v", conn)
	}
	if r := e.gql(maintC, fetchHostMut, map[string]any{"id": id}); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer host-key fetch: %+v", r.Errors)
	}
	var hk struct {
		Type              string
		FingerprintSha256 string
		AuthorizedKey     string
		Host              string
		Port              int
	}
	e.mustGQL(adminC, fetchHostMut, map[string]any{"id": id}).field(t, "fetchSshHostKey", &hk)
	if hk.Host != crBastion || hk.Port != 2222 {
		t.Fatalf("fetched from %s:%d", hk.Host, hk.Port)
	}
	want := strings.Fields(h.exec(h.bastion, "cat", "/config/ssh_host_keys/ssh_host_ed25519_key.pub"))
	if hk.Type != "ssh-ed25519" || hk.AuthorizedKey != want[0]+" "+want[1] {
		t.Fatalf("host key %+v, want %v", hk, want)
	}
	// A wrong pin is a security event.
	wrong := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"
	e.mustGQL(adminC, confirmHostMut, map[string]any{"id": id, "k": wrong, "h": hk.Host, "p": hk.Port}).field(t, "confirmSshHostKey", &conn)
	if r := e.gql(maintC, testConnMut, map[string]any{"id": id}); r.code() != "SSH_HOST_KEY_MISMATCH" {
		t.Fatalf("wrong host key: %+v", r.Errors)
	}
	if n := countAudit(t, e.auditor, audit.ActionConnectionHostKeyMismatch); n != 1 {
		t.Fatalf("host-key mismatch audit entries: %d", n)
	}
	if r := e.gql(adminC, confirmHostMut, map[string]any{"id": id, "k": hk.AuthorizedKey, "h": "other.bekup.test", "p": hk.Port}); r.code() != "CONFLICT" {
		t.Fatalf("confirm for another bastion: %+v", r.Errors)
	}
	e.mustGQL(adminC, confirmHostMut, map[string]any{"id": id, "k": hk.AuthorizedKey, "h": hk.Host, "p": hk.Port}).field(t, "confirmSshHostKey", &conn)
	if !conn.Ready || conn.Settings.SSH.HostKey == nil {
		t.Fatalf("host key confirmed: %+v", conn)
	}
	e.mustGQL(maintC, testConnMut, map[string]any{"id": id}).field(t, "testConnection", &res)
	if !res.OK {
		t.Fatalf("test through the tunnel: %+v", res)
	}
	// A generated keypair replaces the password once installed.
	e.freshStepUp(admin.ID)
	var kp struct{ PublicKey string }
	e.mustGQL(adminC, genKeyMut, map[string]any{"id": id}).field(t, "generateConnectionSshKeypair", &kp)
	if !strings.HasPrefix(kp.PublicKey, "ssh-ed25519 ") {
		t.Fatalf("keypair: %+v", kp)
	}
	h.exec(h.bastion, "sh", "-c", `printf '%s\n' "$1" >> /config/.ssh/authorized_keys`, "sh", kp.PublicKey)
	e.mustGQL(maintC, testConnMut, map[string]any{"id": id}).field(t, "testConnection", &res)
	if !res.OK {
		t.Fatalf("test with the generated key: %+v", res)
	}

	// TLS downgrade: step-up, audited opt-out, badge.
	e.staleStepUp(admin.ID)
	if r := e.gql(adminC, setTLSMut, map[string]any{"id": id, "m": "UNVERIFIED"}); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("downgrade without step-up: %+v", r.Errors)
	}
	e.freshStepUp(admin.ID)
	e.mustGQL(adminC, setTLSMut, map[string]any{"id": id, "m": "UNVERIFIED"}).field(t, "setTlsMode", &conn)
	if !conn.InsecureTransport || conn.TLSMode != "UNVERIFIED" || conn.PinStatus != "NOT_REQUIRED" {
		t.Fatalf("downgraded: %+v", conn)
	}
	if n := countAudit(t, e.auditor, audit.ActionSecurityOptOut); n != 1 {
		t.Fatalf("opt-out audit entries: %d", n)
	}
	e.mustGQL(maintC, testConnMut, map[string]any{"id": id}).field(t, "testConnection", &res)
	if !res.OK {
		t.Fatalf("unverified test: %+v", res)
	}
	e.mustGQL(adminC, setTLSMut, map[string]any{"id": id, "m": "PINNED"}).field(t, "setTlsMode", &conn)
	if conn.InsecureTransport || !conn.Ready {
		t.Fatalf("upgraded: %+v", conn)
	}

	// Delete (step-up), then nothing is listed.
	e.freshStepUp(admin.ID)
	if r := e.gql(maintC, deleteConnMut, map[string]any{"id": id}); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer delete: %+v", r.Errors)
	}
	e.mustGQL(adminC, deleteConnMut, map[string]any{"id": id})
	e.mustGQL(adminC, listConnQuery, nil).field(t, "connections", &list)
	if len(list) != 0 {
		t.Fatalf("list after delete: %+v", list)
	}

	// No secret in any response, the audit log or the runner's log.
	e.assertNoSecrets(crDBPass, crSSHPass, "PRIVATE KEY")
	if logs := h.logs(h.runner); strings.Contains(logs, crDBPass) || strings.Contains(logs, crSSHPass) {
		t.Fatal("the runner log contains a password")
	}
	if !strings.Contains(h.logs(h.runner), `"rpc":"FetchHostKey"`) {
		t.Fatal("the runner did not audit FetchHostKey")
	}
}
