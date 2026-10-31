//go:build integration

package jobs

// End-to-end backup runs (task 3.6) against a real runner. As in the
// task 2.11 graph harness, the outer test builds the bekup binary for the
// Docker daemon's platform and runs `bekup runner` in a container with the
// Docker socket, a TLS PostgreSQL target (CA-issued leaf, pinned) and a
// MariaDB target on an internal "targets" network; the runner's mTLS port
// is published on 127.0.0.1. The worker runs in this process with the app
// database (dbtest), a disk destination and a MinIO destination, through a
// real River client: scheduled and manual runs produce artifacts plus a
// signed manifest that verifies, decrypts and restores with stock tools.

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/riverqueue/river/rivertype"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/authz/authztest"
	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/destinations"
	"github.com/jlentink/bekup/internal/downloads"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/instancekeys"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/retention"
	"github.com/jlentink/bekup/internal/runner/spec"
	"github.com/jlentink/bekup/internal/runnerclient"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/scheduler"
	"github.com/jlentink/bekup/internal/storage"
	"github.com/jlentink/bekup/internal/testutil/dockertest"
	"github.com/jlentink/bekup/internal/testutil/imagepull"
	"github.com/jlentink/bekup/internal/testutil/runnertest"
)

const (
	biPGHost    = "pg.bekup.test"
	biMariaHost = "maria.bekup.test"
	biPGUser    = "bekup"
	biDBPass    = "DB-PASSWORD-backup-it-5c1e7d"
	biMinioUser = "bekupbackupit"
	biMinioPass = "MINIO-PASSWORD-backup-it-93ab"
	biMinio     = "cgr.dev/chainguard/minio:latest@sha256:268d68a2a3b6fc65f905a61f84d7eab6345ce6bff14f551e2b028ec31e6a1341"
	biItems     = 1000
	biOrders    = 500
	biRunWait   = 5 * time.Minute
)

const biPGSeed = `CREATE TABLE items (id int PRIMARY KEY, name text NOT NULL);
INSERT INTO items SELECT g, 'item ' || g FROM generate_series(1, 1000) g;
CREATE ROLE reporter LOGIN PASSWORD 'reporter-role-pass-71';
CREATE DATABASE big;
\connect big
CREATE TABLE blob (id int, payload text);
INSERT INTO blob SELECT g, md5(g::text) || md5((g * 7)::text) || md5((g * 13)::text) FROM generate_series(1, 2000000) g;
`

const biMariaSeed = "CREATE TABLE shop.orders (id INT PRIMARY KEY, item VARCHAR(64) NOT NULL) ENGINE=InnoDB;\n" +
	"INSERT INTO shop.orders SELECT seq, CONCAT('order ', seq) FROM seq_1_to_500;\n"

func biIgnore(error) {}

func biText(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func biIgnore2[T any](_ T, err error) { biIgnore(err) }

type biDocker struct {
	t *testing.T
	c *client.Client
}

func (d *biDocker) tar(files map[string][]byte, modes map[string]int64) io.Reader {
	d.t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, data := range files {
		mode := int64(0o644)
		if m, ok := modes[name]; ok {
			mode = m
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data)), ModTime: time.Now()}); err != nil {
			d.t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			d.t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		d.t.Fatal(err)
	}
	return &buf
}

func (d *biDocker) copy(id string, files map[string][]byte, modes map[string]int64) {
	d.t.Helper()
	if _, err := d.c.CopyToContainer(d.t.Context(), id, client.CopyToContainerOptions{DestinationPath: "/", Content: d.tar(files, modes)}); err != nil {
		d.t.Fatal(err)
	}
}

func (d *biDocker) container(opts client.ContainerCreateOptions) string {
	d.t.Helper()
	res, err := d.c.ContainerCreate(d.t.Context(), opts)
	if err != nil {
		d.t.Fatal(err)
	}
	d.t.Cleanup(func() {
		biIgnore2(d.c.ContainerRemove(context.Background(), res.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}))
	})
	return res.ID
}

func (d *biDocker) start(id string) {
	d.t.Helper()
	if _, err := d.c.ContainerStart(d.t.Context(), id, client.ContainerStartOptions{}); err != nil {
		d.t.Fatal(err)
	}
}

func (d *biDocker) network(name string, internal bool) (string, string) {
	d.t.Helper()
	res, err := d.c.NetworkCreate(d.t.Context(), name, client.NetworkCreateOptions{Driver: "bridge", Internal: internal,
		Labels: map[string]string{"bekup.integration": "backup-runs"}})
	if err != nil {
		d.t.Fatal(err)
	}
	d.t.Cleanup(func() { biIgnore2(d.c.NetworkRemove(context.Background(), res.ID, client.NetworkRemoveOptions{})) })
	ins, err := d.c.NetworkInspect(d.t.Context(), res.ID, client.NetworkInspectOptions{})
	if err != nil || len(ins.Network.IPAM.Config) == 0 {
		d.t.Fatalf("inspect network: %v", err)
	}
	return res.ID, ins.Network.IPAM.Config[0].Subnet.String()
}

func (d *biDocker) ensureImage(ref string) {
	d.t.Helper()
	imagepull.Ensure(d.t, d.c, ref)
}

func (d *biDocker) logs(id string) string {
	rc, err := d.c.ContainerLogs(context.Background(), id, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return ""
	}
	defer func() { biIgnore(rc.Close()) }()
	var out bytes.Buffer
	biIgnore2(stdcopy.StdCopy(&out, &out, rc))
	return out.String()
}

func (d *biDocker) waitLog(id, needle string, count int, timeout time.Duration) {
	d.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Count(d.logs(id), needle) >= count {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	d.t.Fatalf("container %s: %q not logged %d times:\n%s", id[:12], needle, count, d.logs(id))
}

// exec runs cmd in a container as user and returns its output; it fails
// the test on a non-zero exit.
func (d *biDocker) exec(id, user string, cmd ...string) string {
	d.t.Helper()
	ex, err := d.c.ExecCreate(d.t.Context(), id, client.ExecCreateOptions{User: user, Cmd: cmd, AttachStdout: true, AttachStderr: true})
	if err != nil {
		d.t.Fatal(err)
	}
	att, err := d.c.ExecAttach(d.t.Context(), ex.ID, client.ExecAttachOptions{})
	if err != nil {
		d.t.Fatal(err)
	}
	defer att.Close()
	var out bytes.Buffer
	biIgnore2(stdcopy.StdCopy(&out, &out, att.Reader))
	ins, err := d.c.ExecInspect(d.t.Context(), ex.ID, client.ExecInspectOptions{})
	if err != nil {
		d.t.Fatal(err)
	}
	if ins.ExitCode != 0 {
		d.t.Fatalf("exec %v: exit %d: %s", cmd, ins.ExitCode, out.String())
	}
	return out.String()
}

func biLeaf(t *testing.T, host string) (caPEM, chainPEM, keyPEM []byte) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "bekup backup-run CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(400 * 24 * time.Hour),
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
		SerialNumber: big.NewInt(time.Now().UnixNano() + 1), Subject: pkix.Name{CommonName: host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour),
		DNSNames: []string{host}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	kder, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	chainPEM = append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), caPEM...)
	return caPEM, chainPEM, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kder})
}

// biEnv is the shared end-to-end environment.
type biEnv struct {
	t          *testing.T
	dk         *biDocker
	pgRef      string
	mariaRef   string
	pg         string
	maria      string
	runner     string
	runnerAddr string
	pki        certs.Layout

	minioEndpoint string
	minioAdmin    *s3.Client

	pool    *pgxpool.Pool
	q       *sqlc.Queries
	auditor *audit.Auditor
	keyring *crypto.Keyring
	conns   *connections.Service
	dests   *destinations.Service
	keys    *jobkeys.Service
	jobs    *Service
	queue   *queue.Client
	policy  *authz.Policy
	dls     *downloads.Service
	diskDir string
	admin   user
	reg     *logging.Registry
	logBuf  *biBuffer
	stop    func()
}

type biBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *biBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *biBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func startBackupEnv(t *testing.T) *biEnv {
	t.Helper()
	c, err := client.New(client.FromEnv)
	if err != nil {
		dockertest.Skipf(t, "docker client: %v", err)
	}
	t.Cleanup(func() { biIgnore(c.Close()) })
	ctx := t.Context()
	if _, err := c.Ping(ctx, client.PingOptions{}); err != nil {
		dockertest.Skipf(t, "docker not available: %v", err)
	}
	sv, err := c.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil || sv.Os != "linux" {
		dockertest.Skipf(t, "needs a linux Docker daemon (%v)", err)
	}
	dk := &biDocker{t: t, c: c}
	e := &biEnv{t: t, dk: dk, reg: logging.NewRegistry(), logBuf: &biBuffer{}}
	cat, err := engine.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	pg17, _ := cat.Lookup(engine.KindPostgres, "17")
	maria, _ := cat.Lookup(engine.KindMariaDB, "11.8")
	e.pgRef = spec.ImageRef(pg17)
	mariaRef := spec.ImageRef(maria)
	e.mariaRef = mariaRef
	dk.ensureImage(e.pgRef)
	dk.ensureImage(mariaRef)

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
	ctlID, _ := dk.network("bekup-it-backup-ctl-"+suffix, false)
	tgID, tgSubnet := dk.network("bekup-it-backup-targets-"+suffix, true)

	// PostgreSQL 17 with a CA-issued leaf and seed data.
	_, chain, key := biLeaf(t, biPGHost)
	e.pg = dk.container(client.ContainerCreateOptions{
		Name: "bekup-it-backup-pg-" + suffix,
		Config: &container.Config{Image: e.pgRef, Env: []string{"POSTGRES_USER=" + biPGUser, "POSTGRES_PASSWORD=" + biDBPass, "POSTGRES_DB=app"},
			Entrypoint: []string{"sh"}, Cmd: []string{"-c", "install -o postgres -g postgres -m 0600 /certs/server.key /tmp/server.key && " +
				"install -o postgres -g postgres -m 0644 /certs/server.crt /tmp/server.crt && " +
				"exec docker-entrypoint.sh postgres -c ssl=on -c ssl_cert_file=/tmp/server.crt -c ssl_key_file=/tmp/server.key"}},
		HostConfig: &container.HostConfig{NetworkMode: container.NetworkMode(tgID)},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			tgID: {Aliases: []string{biPGHost}},
		}},
	})
	dk.copy(e.pg, map[string][]byte{"certs/server.crt": chain, "certs/server.key": key, "docker-entrypoint-initdb.d/10-seed.sql": []byte(biPGSeed)}, nil)
	dk.start(e.pg)

	// MariaDB 11.8 without TLS (an audited opt-out) and seed data.
	e.maria = dk.container(client.ContainerCreateOptions{
		Name: "bekup-it-backup-maria-" + suffix,
		Config: &container.Config{Image: mariaRef, Env: []string{"MARIADB_ROOT_PASSWORD=" + biDBPass, "MARIADB_ROOT_HOST=%", "MARIADB_DATABASE=shop"},
			Entrypoint: []string{"docker-entrypoint.sh"}, Cmd: []string{"mariadbd", "--skip-ssl"}},
		HostConfig: &container.HostConfig{NetworkMode: container.NetworkMode(tgID)},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			tgID: {Aliases: []string{biMariaHost}},
		}},
	})
	dk.copy(e.maria, map[string][]byte{"docker-entrypoint-initdb.d/10-seed.sql": []byte(biMariaSeed)}, nil)
	dk.start(e.maria)

	// The runner.
	pkiDir := filepath.Join(t.TempDir(), "pki")
	if _, err := certs.Init(certs.InitOptions{Dir: pkiDir, Components: []string{certs.ComponentRunner, certs.ComponentServe}, AllowUnencrypted: true}); err != nil {
		t.Fatal(err)
	}
	e.pki = certs.Layout{Dir: pkiDir}
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
	e.runner = dk.container(client.ContainerCreateOptions{
		Name: "bekup-it-backup-runner-" + suffix,
		Config: &container.Config{
			Image: e.pgRef, User: "0", Entrypoint: []string{"/bekup/bekup"},
			Cmd:          []string{"runner", "--config", "/bekup/runner.toml"},
			ExposedPorts: network.PortSet{port: {}},
		},
		HostConfig: &container.HostConfig{
			NetworkMode:  container.NetworkMode(ctlID),
			Binds:        []string{"/var/run/docker.sock:/var/run/docker.sock"},
			PortBindings: network.PortMap{port: {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: "0"}}},
		},
	})
	if _, err := c.NetworkConnect(ctx, tgID, client.NetworkConnectOptions{Container: e.runner}); err != nil {
		t.Fatal(err)
	}
	dk.copy(e.runner, map[string][]byte{
		"bekup/bekup": binData, "bekup/runner.toml": []byte(runnerTOML),
		"bekup/runner.crt": read(e.pki.CertFile(certs.ComponentRunner)),
		"bekup/runner.key": read(e.pki.KeyFile(certs.ComponentRunner)),
		"bekup/ca.crt":     read(e.pki.CACert()),
	}, map[string]int64{"bekup/bekup": 0o755, "bekup/runner.key": 0o600})
	dk.start(e.runner)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("runner logs:\n%s", dk.logs(e.runner))
		}
	})

	e.startMinio()

	dk.waitLog(e.pg, "database system is ready to accept connections", 2, 5*time.Minute)
	dk.waitLog(e.maria, "port: 3306", 1, 4*time.Minute)
	dk.waitLog(e.runner, "runner listening", 1, time.Minute)
	ins, err := c.ContainerInspect(ctx, e.runner, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b := ins.Container.NetworkSettings.Ports[port]
	if len(b) == 0 {
		t.Fatal("runner port not published")
	}
	e.runnerAddr = net.JoinHostPort("127.0.0.1", b[0].HostPort)
	e.services()
	return e
}

func (e *biEnv) startMinio() {
	t := e.t
	ctx := context.Background()
	mc, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: testcontainers.ContainerRequest{
		Image:        biMinio,
		ExposedPorts: []string{"9000/tcp"},
		Env:          map[string]string{"MINIO_ROOT_USER": biMinioUser, "MINIO_ROOT_PASSWORD": biMinioPass},
		Cmd:          []string{"server", "/tmp/data", "--address", ":9000"},
		WaitingFor:   wait.ForListeningPort("9000/tcp").WithStartupTimeout(90 * time.Second),
	}, Started: true})
	testcontainers.CleanupContainer(t, mc)
	if err != nil {
		t.Fatalf("start minio: %v", err)
	}
	p, err := mc.MappedPort(ctx, "9000/tcp")
	if err != nil {
		t.Fatal(err)
	}
	e.minioEndpoint = "http://127.0.0.1:" + p.Port()
	e.minioAdmin = s3.New(s3.Options{
		Region: "us-east-1",
		Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: biMinioUser, SecretAccessKey: biMinioPass}, nil
		}),
		BaseEndpoint: aws.String(e.minioEndpoint), UsePathStyle: true, HTTPClient: &http.Client{Timeout: time.Minute},
	})
	deadline := time.Now().Add(time.Minute)
	for {
		if _, err := e.minioAdmin.ListBuckets(ctx, &s3.ListBucketsInput{}); err == nil {
			return
		} else if time.Now().After(deadline) {
			t.Fatalf("minio not ready: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// services builds the app side: DB, keyring, connections (real runner
// client), destinations, job keys, jobs and a working River client with the
// backup worker.
func (e *biEnv) services() {
	t := e.t
	ctx := t.Context()
	d := dbtest.Migrated(t)
	e.pool = d.AppPool(t)
	e.q = sqlc.New(e.pool)
	var err error
	e.keyring, err = crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.keyring.Destroy)
	e.auditor = audit.New(e.pool, audit.Options{Registry: e.reg})
	logger, err := logging.New(e.logBuf, logging.Options{Level: "debug", Registry: e.reg})
	if err != nil {
		t.Fatal(err)
	}
	tlsCfg, err := certs.ClientTLSConfig(e.pki.CertFile(certs.ComponentServe), e.pki.KeyFile(certs.ComponentServe), e.pki.CACert(), "runner")
	if err != nil {
		t.Fatal(err)
	}
	rc, err := runnerclient.NewWithTLS(e.runnerAddr, tlsCfg, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { biIgnore(rc.Close()) })
	// "runner listening" precedes the executor's own-container detection.
	runnertest.WaitDockerOK(t, rc, 0, nil)
	e.conns, err = connections.New(connections.Options{
		DB: e.pool, Keyring: e.keyring, Auditor: e.auditor, Runner: rc.RPC(), OperationTimeout: 3 * time.Minute,
		Limiter: func(string) (bool, time.Duration) { return true, 0 }, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	guard, err := netguard.New(netguard.Options{LoopbackAllowable: true, AllowCIDRs: []string{"127.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	e.diskDir = t.TempDir()
	e.dests, err = destinations.New(destinations.Options{DB: e.pool, Keyring: e.keyring, Auditor: e.auditor, Guard: guard,
		DiskRoot: e.diskDir, AllowHTTP: true, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	policy := authz.New(authz.Options{Ownership: Ownership{DB: e.pool}})
	e.keys, err = jobkeys.New(jobkeys.Options{DB: e.pool, Keyring: e.keyring, Auditor: e.auditor, Authorizer: policy})
	if err != nil {
		t.Fatal(err)
	}
	settings := queue.SettingsFromConfig(config.DefaultBekup().Workers)
	ws, err := queue.NewWorkers(settings)
	if err != nil {
		t.Fatal(err)
	}
	// The retention worker with the post-run hook, as serve wires it (no
	// daily periodic job here: the post-run sweeps only).
	ret, err := retention.New(retention.Options{DB: e.pool, Auditor: e.auditor, Destinations: e.dests,
		Queue: func() retention.Inserter { return e.queue }, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Register(ws, ret); err != nil {
		t.Fatal(err)
	}
	store := runstate.NewStore(e.pool, nil, nil).WithFinishHook(retention.PostRunHook(func() retention.Inserter { return e.queue }), logger)
	bw, err := NewBackupWorker(BackupWorkerOptions{
		Store: store, Slots: runstate.NewSlots(e.pool),
		Targets: e.conns, Runner: rc.RPC(), Destinations: e.dests, Keys: e.keys,
		Signer: InstanceKeySigner{DB: e.pool, Keyring: e.keyring, Auditor: e.auditor},
		Stats:  RunnerStats{Store: e.q, Logger: logger},
		Limits: runstate.SlotLimits{Global: 4, PerConnection: 1}, QueueTimeout: settings.Queues[queue.QueueBackup].Timeout,
		LogMaxLines: 10000, LogMaxLineBytes: 4096, BekupVersion: "0.0.0-it", Registry: e.reg, Logger: logger,
		CancelPoll: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Register(ws, bw); err != nil {
		t.Fatal(err)
	}
	e.queue, err = queue.New(e.pool, queue.Options{Settings: settings, Logger: logger, Workers: ws, FetchPollInterval: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	e.jobs, err = New(Options{DB: e.pool, Auditor: e.auditor, Authorizer: policy, Keys: e.keys, Queue: e.queue, Canceler: e.queue,
		Limits: scheduler.Limits{MinInterval: 5 * time.Minute}, MaxTimeout: 6 * time.Hour, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	e.policy = policy
	e.dls, err = downloads.New(downloads.Options{DB: e.pool, Keyring: e.keyring, Auditor: e.auditor, Authorizer: policy,
		Destinations: e.dests, Keys: e.keys, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	u, err := e.q.InsertUser(ctx, sqlc.InsertUserParams{Email: "admin@example.com", DisplayName: "Admin", Role: sqlc.UserRoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	pr := authztest.Principal(auth.RoleAdmin)
	pr.UserID = u.ID
	e.admin = user{actor: Actor{UserID: u.ID}, pr: pr, ctx: authztest.Context(ctx, pr)}

	rctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan error, 1)
	go func() { done <- e.queue.Run(rctx) }()
	e.stop = func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("river: %v", err)
		}
	}
	t.Cleanup(e.stop)
}

func (e *biEnv) pgConnection() uuid.UUID {
	e.t.Helper()
	ctx := e.t.Context()
	c, err := e.conns.Create(ctx, connections.Actor{UserID: e.admin.actor.UserID}, connections.CreateInput{
		Name: "pg", Engine: engine.KindPostgres, VersionKey: "17", Host: biPGHost, Port: 5432, Username: biPGUser, Password: []byte(biDBPass),
	})
	if err != nil {
		e.t.Fatal(err)
	}
	chain, err := e.conns.FetchCertificate(ctx, connections.Actor{UserID: e.admin.actor.UserID}, c.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	idx := chain.SuggestedAnchorIndex
	if _, err := e.conns.ConfirmPin(ctx, connections.Actor{UserID: e.admin.actor.UserID}, c.ID, connections.PinInput{
		CertIndex: &idx, LeafSPKI: hex.EncodeToString(chain.LeafSPKISHA256),
	}); err != nil {
		e.t.Fatal(err)
	}
	return c.ID
}

func (e *biEnv) mariaConnection() uuid.UUID {
	e.t.Helper()
	c, err := e.conns.Create(e.t.Context(), connections.Actor{UserID: e.admin.actor.UserID}, connections.CreateInput{
		Name: "maria", Engine: engine.KindMariaDB, VersionKey: "11.8", Host: biMariaHost, Port: 3306, Username: "root",
		Password: []byte(biDBPass), TLSMode: connections.TLSDisabled,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return c.ID
}

func (e *biEnv) diskDestination(name string) uuid.UUID {
	e.t.Helper()
	d, err := e.dests.Create(e.t.Context(), destinations.Actor{UserID: e.admin.actor.UserID}, destinations.CreateInput{
		Name: name, Type: destinations.TypeDisk, Disk: &destinations.DiskInput{Path: name},
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return d.ID
}

func (e *biEnv) bucket(name string) {
	e.t.Helper()
	if _, err := e.minioAdmin.CreateBucket(e.t.Context(), &s3.CreateBucketInput{Bucket: aws.String(name)}); err != nil {
		e.t.Fatalf("create bucket: %v", err)
	}
}

func (e *biEnv) s3Destination(name, bucket string) uuid.UUID {
	e.t.Helper()
	d, err := e.dests.Create(e.t.Context(), destinations.Actor{UserID: e.admin.actor.UserID}, destinations.CreateInput{
		Name: name, Type: destinations.TypeS3, S3: &destinations.S3Input{
			Endpoint: e.minioEndpoint, Region: "us-east-1", Bucket: bucket, PathStyle: true,
			AccessKeyID: biMinioUser, SecretAccessKey: []byte(biMinioPass),
		},
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return d.ID
}

func (e *biEnv) createJob(in CreateInput) *Created {
	e.t.Helper()
	if in.Retention.KeepLast == nil && in.Retention.KeepDays == nil {
		in.Retention = Retention{KeepLast: keep(7)}
	}
	if in.Schedule.Kind == "" {
		in.Schedule = scheduler.Schedule{Kind: scheduler.KindManual}
	}
	c, err := e.jobs.Create(e.freshAdmin(), e.admin.actor, in)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(c.Destroy)
	return c
}

// freshAdmin is the admin's context with a fresh step-up (choosing a job's
// key needs one; the suite runs longer than the step-up window).
func (e *biEnv) freshAdmin() context.Context {
	pr := e.admin.pr
	pr.StepUpAt, pr.AAL2At = time.Now(), time.Now()
	return authztest.Context(e.t.Context(), pr)
}

// waitRiverState polls a River job until it reaches state (River
// finalizes jobs asynchronously).
func (e *biEnv) waitRiverState(id int64, want rivertype.JobState) {
	e.t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		var state string
		err := e.pool.QueryRow(e.t.Context(), "SELECT state FROM river_job WHERE id = $1", id).Scan(&state)
		if err == nil && state == string(want) {
			return
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("river job %d: state %q (%v), want %q", id, state, err, want)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// download redeems a ticket for an artifact through the downloads service
// as the admin (a real session row) and returns the response.
func (e *biEnv) download(artifact uuid.UUID, mode downloads.Mode) (*httptest.ResponseRecorder, downloads.Ticket) {
	e.t.Helper()
	ctx := e.t.Context()
	hash := sha256.Sum256([]byte(uuid.NewString()))
	var sid uuid.UUID
	if err := e.pool.QueryRow(ctx, `INSERT INTO sessions (token_hash, user_id, aal, stepup_at, aal2_at, idle_expires_at, absolute_expires_at)
		VALUES ($1, $2, 2, now(), now(), now() + interval '1 hour', now() + interval '2 hours') RETURNING id`, hash[:], e.admin.actor.UserID).Scan(&sid); err != nil {
		e.t.Fatal(err)
	}
	pr := e.admin.pr
	pr.SessionID, pr.StepUpAt, pr.AAL2At = sid, time.Now(), time.Now()
	pctx := authztest.Context(ctx, pr)
	tk, err := e.dls.CreateTicket(pctx, downloads.CreateInput{ArtifactID: artifact, Mode: mode})
	if err != nil {
		e.t.Fatalf("create ticket: %v", err)
	}
	req := httptest.NewRequestWithContext(pctx, http.MethodGet, tk.URL, http.NoBody)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	e.dls.ServeDownload(w, req, strings.TrimPrefix(tk.URL, downloads.URLPrefix))
	return w, tk
}

// waitRun waits until the run is terminal.
func (e *biEnv) waitRun(id uuid.UUID) sqlc.Run {
	e.t.Helper()
	deadline := time.Now().Add(biRunWait)
	for {
		r, err := e.q.GetRun(e.t.Context(), id)
		if err != nil {
			e.t.Fatal(err)
		}
		if r.Status != sqlc.RunStatusQueued && r.Status != sqlc.RunStatusRunning {
			return r
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("run %s still %s; logs:\n%s", id, r.Status, e.runLogs(id))
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func (e *biEnv) runLogs(id uuid.UUID) string {
	logs, err := e.q.ListRunLogs(e.t.Context(), sqlc.ListRunLogsParams{RunID: id, AfterID: 0, MaxRows: 10000})
	if err != nil {
		return err.Error()
	}
	var b strings.Builder
	for _, l := range logs {
		fmt.Fprintf(&b, "%s [%s/%s] %s\n", l.Ts.Format(time.RFC3339), l.Stream, l.Level, l.Line)
	}
	return b.String()
}

// object reads an object through the destination service.
func (e *biEnv) object(dest uuid.UUID, key string) []byte {
	e.t.Helper()
	d, err := e.dests.Open(e.t.Context(), dest)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { biIgnore(d.Close()) }()
	k, err := storage.ParseKey(key)
	if err != nil {
		e.t.Fatal(err)
	}
	rc, _, err := d.Get(e.t.Context(), k)
	if err != nil {
		e.t.Fatalf("get %s: %v", key, err)
	}
	defer func() { biIgnore(rc.Close()) }()
	b, err := io.ReadAll(rc)
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

// verifyManifest checks the run's manifest on dest against the DB records
// and returns it.
func (e *biEnv) verifyManifest(run sqlc.Run, dest uuid.UUID, slug string) (*pipeline.Manifest, []sqlc.Artifact) {
	e.t.Helper()
	arts, err := e.q.ListRunArtifacts(e.t.Context(), run.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	var mkey string
	var expect []pipeline.ExpectedEntry
	var mine []sqlc.Artifact
	for i := range arts {
		a := &arts[i]
		if a.DestinationID != dest {
			continue
		}
		if a.Kind == sqlc.ArtifactKindManifest {
			mkey = a.ObjectKey
			continue
		}
		mine = append(mine, *a)
		if a.Verified {
			expect = append(expect, pipeline.ExpectedEntry{Path: a.ObjectKey[strings.LastIndex(a.ObjectKey, "/")+1:], SHA256: a.Sha256, StoredSize: a.Size})
		}
	}
	if mkey == "" {
		e.t.Fatalf("no manifest on %s", dest)
	}
	keys, err := instancekeys.Verifier(e.t.Context(), e.q)
	if err != nil {
		e.t.Fatal(err)
	}
	m, err := pipeline.Verify(e.object(dest, mkey), keys, pipeline.Binding{
		JobID: run.JobID.String(), RunID: run.ID.String(), JobSlug: slug, RunTimestamp: *run.StorageTimestamp, Entries: expect,
	})
	if err != nil {
		e.t.Fatalf("manifest on %s: %v", dest, err)
	}
	return m, mine
}

func (e *biEnv) assertNoSecrets(runID uuid.UUID) {
	e.t.Helper()
	for name, text := range map[string]string{"worker log": e.logBuf.String(), "run log": e.runLogs(runID), "runner log": e.dk.logs(e.runner)} {
		for _, s := range []string{biDBPass, biMinioPass} {
			if strings.Contains(text, s) {
				e.t.Fatalf("a secret leaked into the %s", name)
			}
		}
	}
}

// restorePG restores a custom-format archive with stock pg_restore in a
// fresh PostgreSQL container and returns the row count of table.
func (e *biEnv) restorePG(dump []byte, table string) string {
	e.t.Helper()
	id := e.dk.container(client.ContainerCreateOptions{
		Name:   "bekup-it-backup-restore-" + uuid.NewString()[:8],
		Config: &container.Config{Image: e.pgRef, Env: []string{"POSTGRES_PASSWORD=restore-only-test", "POSTGRES_DB=restored"}},
	})
	e.dk.start(id)
	e.dk.waitLog(id, "database system is ready to accept connections", 2, 3*time.Minute)
	e.dk.copy(id, map[string][]byte{"tmp/app.dump": dump}, map[string]int64{"tmp/app.dump": 0o644})
	e.dk.exec(id, "postgres", "pg_restore", "--no-owner", "--exit-on-error", "-d", "restored", "/tmp/app.dump")
	return strings.TrimSpace(e.dk.exec(id, "postgres", "psql", "-d", "restored", "-tAc", "SELECT count(*) FROM "+table))
}

func TestIntegrationBackupRuns(t *testing.T) {
	e := startBackupEnv(t)
	pgConn := e.pgConnection()
	mariaConn := e.mariaConnection()
	disk := e.diskDestination("backups")
	e.bucket("bekup-backups")
	minio := e.s3Destination("minio", "bekup-backups")

	t.Run("scheduled postgres run", func(t *testing.T) {
		job := e.createJob(CreateInput{
			Name: "pg nightly", Slug: "pg-nightly", ConnectionID: pgConn, DBMode: DBModeAll, Exclude: []string{"postgres", "big"},
			Verification:   VerificationDeep,
			Schedule:       scheduler.Schedule{Kind: scheduler.KindInterval, Every: 1, Unit: scheduler.UnitDays, Anchor: "2026-01-01T03:00", Timezone: "UTC"},
			DestinationIDs: []uuid.UUID{disk, minio},
		})
		pass := job.Encryption.Passphrase
		if pass == nil {
			t.Fatal("no generated passphrase")
		}
		// Make the job due and let the scheduler enqueue it.
		if _, err := e.pool.Exec(t.Context(), "UPDATE jobs SET next_run_at = now() - interval '1 minute' WHERE id = $1", job.Job.ID); err != nil {
			t.Fatal(err)
		}
		loop, err := scheduler.NewLoop(scheduler.LoopOptions{DB: e.pool, Queue: e.queue, Logger: slog.New(slog.DiscardHandler)})
		if err != nil {
			t.Fatal(err)
		}
		if res, err := loop.Tick(t.Context()); err != nil || res.Enqueued != 1 {
			t.Fatalf("tick %+v %v", res, err)
		}
		var runID uuid.UUID
		if err := e.pool.QueryRow(t.Context(), "SELECT id FROM runs WHERE job_id = $1", job.Job.ID).Scan(&runID); err != nil {
			t.Fatal(err)
		}
		run := e.waitRun(runID)
		if run.Status != sqlc.RunStatusSuccess || run.Trigger != sqlc.RunTriggerSchedule {
			t.Fatalf("run %s %s\n%s", run.Status, biText(run.ErrorMessage), e.runLogs(runID))
		}
		items, err := e.q.ListRunItems(t.Context(), runID)
		if err != nil || len(items) != 2 || *items[0].DbName != "app" || items[1].Kind != sqlc.RunItemKindGlobals || !items[0].Verified || !items[1].Verified {
			t.Fatalf("items %+v %v", items, err)
		}
		e.assertPGStats(run, items)
		for _, dest := range []uuid.UUID{disk, minio} {
			m, arts := e.verifyManifest(run, dest, "pg-nightly")
			if !strings.Contains(string(m.Stats), `"databases":{"app":{"consistency":"snapshot"`) ||
				!strings.Contains(string(m.Stats), `"databases_excluded":["big","postgres"]`) {
				t.Fatalf("manifest stats %s", m.Stats)
			}
			if len(m.Entries) != 2 || len(arts) != 2 || m.Engine.Kind != "postgres" {
				t.Fatalf("manifest %+v artifacts %d", m, len(arts))
			}
			for _, a := range arts {
				if a.SigningKeyID == nil || *a.SigningKeyID != m.InstanceKeyID || *a.EncryptionMode != "passphrase" {
					t.Fatalf("artifact %+v", a)
				}
			}
		}
		// Decrypt + decompress the app archive and restore it with
		// stock pg_restore; the globals end with pg_dumpall's trailer.
		_, arts := e.verifyManifest(run, disk, "pg-nightly")
		for _, a := range arts {
			r, err := pipeline.OpenArtifact(bytes.NewReader(e.object(disk, a.ObjectKey)), pipeline.Decryption{Passphrase: pass.Passphrase.Bytes()})
			if err != nil {
				t.Fatal(err)
			}
			plain, err := io.ReadAll(r)
			biIgnore(r.Close())
			if err != nil {
				t.Fatal(err)
			}
			if a.Kind == sqlc.ArtifactKindGlobals {
				if !bytes.Contains(plain, []byte("CREATE ROLE reporter")) || !bytes.Contains(plain, []byte("PostgreSQL database cluster dump complete")) {
					t.Fatal("globals dump incomplete")
				}
				continue
			}
			if got := e.restorePG(plain, "items"); got != strconv.Itoa(biItems) {
				t.Fatalf("restored %s rows, want %d", got, biItems)
			}
		}
		e.assertNoSecrets(runID)
	})

	t.Run("mariadb run", func(t *testing.T) {
		id, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		job := e.createJob(CreateInput{
			Name: "shop", Slug: "shop", ConnectionID: mariaConn, DBMode: DBModeSelected, Databases: []string{"shop"},
			DestinationIDs: []uuid.UUID{disk},
			Encryption:     Encryption{Mode: jobkeys.ModeRecipient, Recipient: id.Recipient().String()},
		})
		req, err := e.jobs.RunNow(e.admin.ctx, e.admin.actor, job.Job.ID)
		if err != nil {
			t.Fatal(err)
		}
		run := e.waitRun(req.RunID)
		if run.Status != sqlc.RunStatusSuccess {
			t.Fatalf("run %s %s\n%s", run.Status, biText(run.ErrorMessage), e.runLogs(req.RunID))
		}
		m, arts := e.verifyManifest(run, disk, "shop")
		e.assertMariaStats(run, m)
		if len(arts) != 1 || m.Engine.Kind != "mariadb" || !strings.HasSuffix(arts[0].ObjectKey, "/shop.sql.zst.age") {
			t.Fatalf("artifacts %+v", arts)
		}
		r, err := pipeline.OpenArtifact(bytes.NewReader(e.object(disk, arts[0].ObjectKey)), pipeline.Decryption{Identities: []byte(id.String())})
		if err != nil {
			t.Fatal(err)
		}
		plain, err := io.ReadAll(r)
		biIgnore(r.Close())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(plain, []byte("INSERT INTO `orders`")) || !bytes.Contains(plain, []byte("-- Dump completed")) ||
			!bytes.Contains(plain, []byte("'order 500'")) {
			t.Fatal("the MariaDB dump is incomplete")
		}
		e.assertNoSecrets(req.RunID)
	})

	t.Run("destination failure is partial", func(t *testing.T) {
		e.bucket("bekup-doomed")
		doomed := e.s3Destination("doomed", "bekup-doomed")
		if _, err := e.minioAdmin.DeleteBucket(t.Context(), &s3.DeleteBucketInput{Bucket: aws.String("bekup-doomed")}); err != nil {
			t.Fatal(err)
		}
		// A disk destination that cannot be written to: opens, then every
		// Put fails (dropped mid-run).
		if os.Geteuid() == 0 {
			t.Skip("file permissions do not stop root")
		}
		readonly := e.diskDestination("readonly")
		roDir := filepath.Join(e.diskDir, "readonly")
		if err := os.MkdirAll(roDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(roDir, 0o500); err != nil { //nolint:gosec // G302: test fixture, read-only on purpose
			t.Fatal(err)
		}
		t.Cleanup(func() { biIgnore(os.Chmod(roDir, 0o700)) }) //nolint:gosec // G302: restore for cleanup
		id, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		job := e.createJob(CreateInput{
			Name: "pg partial", Slug: "pg-partial", ConnectionID: pgConn, DBMode: DBModeSelected, Databases: []string{"app"},
			IncludeGlobals: new(bool), DestinationIDs: []uuid.UUID{disk, doomed, readonly},
			Encryption: Encryption{Mode: jobkeys.ModeRecipient, Recipient: id.Recipient().String()},
		})
		req, err := e.jobs.RunNow(e.admin.ctx, e.admin.actor, job.Job.ID)
		if err != nil {
			t.Fatal(err)
		}
		run := e.waitRun(req.RunID)
		if run.Status != sqlc.RunStatusPartial {
			t.Fatalf("run %s %s\n%s", run.Status, biText(run.ErrorMessage), e.runLogs(req.RunID))
		}
		e.verifyManifest(run, disk, "pg-partial")
		arts, err := e.q.ListRunArtifacts(t.Context(), req.RunID)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range arts {
			if a.DestinationID == doomed || a.DestinationID == readonly {
				t.Fatalf("an artifact was recorded on a failed destination: %+v", a)
			}
		}
		// The S3 destination fails when it is opened (its bucket is gone),
		// the read-only disk when the artifact is written.
		ws := string(run.Warnings)
		if !strings.Contains(ws, CodeDestinations) || !strings.Contains(ws, doomed.String()) ||
			!strings.Contains(ws, CodeDestinationDropped) || !strings.Contains(ws, readonly.String()) {
			t.Fatalf("warnings %s", ws)
		}
		if entries, err := os.ReadDir(roDir); err != nil || len(entries) != 0 {
			t.Fatalf("the read-only destination holds %d entries", len(entries))
		}
	})

	t.Run("cancel mid-run leaves no partial object", func(t *testing.T) {
		id, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		e.bucket("bekup-cancel")
		mc := e.s3Destination("minio cancel", "bekup-cancel")
		job := e.createJob(CreateInput{
			Name: "pg big", Slug: "pg-big", ConnectionID: pgConn, DBMode: DBModeSelected, Databases: []string{"big"},
			IncludeGlobals: new(bool), DestinationIDs: []uuid.UUID{disk, mc},
			Encryption: Encryption{Mode: jobkeys.ModeRecipient, Recipient: id.Recipient().String()},
		})
		req, err := e.jobs.RunNow(e.admin.ctx, e.admin.actor, job.Job.ID)
		if err != nil {
			t.Fatal(err)
		}
		// Wait until the dump is streaming, then cancel.
		deadline := time.Now().Add(biRunWait)
		for {
			items, err := e.q.ListRunItems(t.Context(), req.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if len(items) == 1 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("the dump did not start:\n%s", e.runLogs(req.RunID))
			}
			time.Sleep(100 * time.Millisecond)
		}
		time.Sleep(1500 * time.Millisecond)
		if _, err := e.jobs.CancelRun(e.admin.ctx, e.admin.actor, req.RunID); err != nil {
			t.Fatal(err)
		}
		run := e.waitRun(req.RunID)
		if run.Status != sqlc.RunStatusCanceled {
			t.Fatalf("run %s %s\n%s", run.Status, biText(run.ErrorMessage), e.runLogs(req.RunID))
		}
		// Nothing of the canceled dump is visible: no finalized object,
		// no leftover temporary file, no open multipart upload.
		walkErr := filepath.Walk(filepath.Join(e.diskDir, "backups", "pg-big"), func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				t.Errorf("leftover file %s", p)
			}
			return nil
		})
		biIgnore(walkErr)
		objs, err := e.minioAdmin.ListObjectsV2(t.Context(), &s3.ListObjectsV2Input{Bucket: aws.String("bekup-cancel")})
		if err != nil || len(objs.Contents) != 0 {
			t.Fatalf("objects left in the bucket: %d %v", len(objs.Contents), err)
		}
		ups, err := e.minioAdmin.ListMultipartUploads(t.Context(), &s3.ListMultipartUploadsInput{Bucket: aws.String("bekup-cancel")})
		if err != nil || len(ups.Uploads) != 0 {
			t.Fatalf("multipart uploads left: %d %v", len(ups.Uploads), err)
		}
		if arts, err := e.q.ListRunArtifacts(t.Context(), req.RunID); err != nil || len(arts) != 0 {
			t.Fatalf("artifacts of a canceled dump: %d %v", len(arts), err)
		}
		// River finalizes the job asynchronously after the worker returns.
		e.waitRiverState(req.QueueJobID, rivertype.JobStateCancelled)
	})

	t.Run("post-run retention deletes the predecessor", func(t *testing.T) {
		keep := 1
		job := e.createJob(CreateInput{
			Name: "shop retention", Slug: "shop-retention", ConnectionID: mariaConn, DBMode: DBModeSelected, Databases: []string{"shop"},
			DestinationIDs: []uuid.UUID{disk}, Retention: Retention{KeepLast: &keep},
		})
		runs := make([]sqlc.Run, 0, 2)
		for range 2 {
			req, err := e.jobs.RunNow(e.admin.ctx, e.admin.actor, job.Job.ID)
			if err != nil {
				t.Fatal(err)
			}
			run := e.waitRun(req.RunID)
			if run.Status != sqlc.RunStatusSuccess {
				t.Fatalf("run %s %s\n%s", run.Status, biText(run.ErrorMessage), e.runLogs(req.RunID))
			}
			runs = append(runs, run)
			// Distinct directory timestamps (UTC seconds).
			time.Sleep(1100 * time.Millisecond)
		}
		// The second run's post-run sweep deletes the first run (dump, then
		// its manifest) and keeps the second.
		deadline := time.Now().Add(2 * time.Minute)
		for {
			arts, err := e.q.ListRunArtifacts(t.Context(), runs[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			deleted := 0
			for _, a := range arts {
				if a.State == sqlc.ArtifactStateDeleted {
					deleted++
				}
			}
			if len(arts) == 2 && deleted == 2 {
				for _, a := range arts {
					if _, err := os.Stat(filepath.Join(e.diskDir, "backups", filepath.FromSlash(a.ObjectKey))); !os.IsNotExist(err) {
						t.Fatalf("%s still on disk: %v", a.ObjectKey, err)
					}
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("first run not swept: %d artifacts, %d deleted", len(arts), deleted)
			}
			time.Sleep(300 * time.Millisecond)
		}
		arts, err := e.q.ListRunArtifacts(t.Context(), runs[1].ID)
		if err != nil || len(arts) != 2 {
			t.Fatalf("second run artifacts %d %v", len(arts), err)
		}
		for _, a := range arts {
			if a.State != sqlc.ArtifactStatePresent {
				t.Fatalf("the newest run lost %s", a.ObjectKey)
			}
		}
		e.verifyManifest(runs[1], disk, "shop-retention")
		var swept int
		if err := e.pool.QueryRow(t.Context(), "SELECT count(*) FROM audit_log WHERE action = 'retention.swept' AND target_id = $1", job.Job.ID.String()).Scan(&swept); err != nil || swept < 1 {
			t.Fatalf("retention.swept audits %d %v", swept, err)
		}
	})

	t.Run("plain download of a worker artifact restores with the stock mariadb client", func(t *testing.T) {
		job := e.createJob(CreateInput{
			Name: "shop download", Slug: "shop-download", ConnectionID: mariaConn, DBMode: DBModeSelected, Databases: []string{"shop"},
			DestinationIDs: []uuid.UUID{disk},
		})
		if job.Encryption.Passphrase == nil {
			t.Fatal("no generated passphrase")
		}
		req, err := e.jobs.RunNow(e.admin.ctx, e.admin.actor, job.Job.ID)
		if err != nil {
			t.Fatal(err)
		}
		run := e.waitRun(req.RunID)
		if run.Status != sqlc.RunStatusSuccess {
			t.Fatalf("run %s %s\n%s", run.Status, biText(run.ErrorMessage), e.runLogs(req.RunID))
		}
		_, arts := e.verifyManifest(run, disk, "shop-download")
		if len(arts) != 1 {
			t.Fatalf("artifacts %d", len(arts))
		}
		w, tk := e.download(arts[0].ID, downloads.ModePlain)
		if w.Code != http.StatusOK || tk.Format != "sql" || !strings.HasSuffix(tk.FileName, ".sql") {
			t.Fatalf("download %d %s %s: %s", w.Code, tk.Format, tk.FileName, w.Body.String())
		}
		plain := w.Body.Bytes()
		if !bytes.Contains(plain, []byte("INSERT INTO `orders`")) || !bytes.Contains(plain, []byte("-- Dump completed")) {
			t.Fatal("the downloaded dump is incomplete")
		}
		var holds int
		if err := e.pool.QueryRow(t.Context(), "SELECT count(*) FROM artifact_holds WHERE artifact_id = $1", arts[0].ID).Scan(&holds); err != nil || holds != 0 {
			t.Fatalf("download hold not released: %d %v", holds, err)
		}
		// Restore with the stock client in a fresh MariaDB.
		const rootPass = "restore-only-test-pass"
		id := e.dk.container(client.ContainerCreateOptions{
			Name: "bekup-it-backup-maria-restore-" + uuid.NewString()[:8],
			Config: &container.Config{Image: e.mariaRef, Env: []string{"MARIADB_ROOT_PASSWORD=" + rootPass, "MARIADB_DATABASE=shop"},
				Entrypoint: []string{"docker-entrypoint.sh"}, Cmd: []string{"mariadbd"}},
		})
		e.dk.start(id)
		e.dk.waitLog(id, "port: 3306  mariadb.org", 1, 4*time.Minute)
		e.dk.copy(id, map[string][]byte{"tmp/shop.sql": plain}, map[string]int64{"tmp/shop.sql": 0o644})
		e.dk.exec(id, "", "sh", "-c", "mariadb -uroot -p"+rootPass+" shop < /tmp/shop.sql")
		got := strings.TrimSpace(e.dk.exec(id, "", "mariadb", "-uroot", "-p"+rootPass, "-N", "-e", "SELECT count(*) FROM shop.orders"))
		if got != strconv.Itoa(biOrders) {
			t.Fatalf("restored %s orders, want %d", got, biOrders)
		}
	})
}

// assertPGStats checks the completeness evidence of the deep PostgreSQL
// run (task 3.10): exact counts in the same snapshot, a backup_stats row
// per item, the stats signed into the manifest and the excluded databases
// notice (not a warning; status still success).
func (e *biEnv) assertPGStats(run sqlc.Run, items []sqlc.RunItem) {
	e.t.Helper()
	rows, err := e.q.ListBackupStatsByRun(e.t.Context(), run.ID)
	if err != nil || len(rows) != len(items) {
		e.t.Fatalf("backup_stats %d rows (items %d): %v", len(rows), len(items), err)
	}
	app := rows[0]
	if app.ItemKind != sqlc.RunItemKindDatabase || app.Level != sqlc.JobVerificationDeep || app.Consistency != "snapshot" || app.TablesTotal != 1 {
		e.t.Fatalf("app stats %+v", app)
	}
	var st struct {
		Objects map[string]int64 `json:"objects"`
		Tables  map[string]struct {
			Rows  int64  `json:"rows"`
			Count string `json:"count"`
		} `json:"tables"`
	}
	if err := json.Unmarshal(app.Stats, &st); err != nil {
		e.t.Fatal(err)
	}
	if it := st.Tables["public.items"]; it.Count != "exact" || it.Rows != biItems || st.Objects["tables"] != 1 {
		e.t.Fatalf("app stats %s", app.Stats)
	}
	if g := rows[1]; g.ItemKind != sqlc.RunItemKindGlobals || !strings.Contains(string(g.Stats), `"roles":`) {
		e.t.Fatalf("globals stats %+v", g)
	}
	// The excluded databases are an informational notice (task 3.11), not
	// a warning: has_warnings stays meaningful.
	if strings.Contains(string(run.Warnings), "databases_excluded") || !strings.Contains(string(run.Notices), "databases_excluded") ||
		run.HasWarnings != (string(run.Warnings) != "[]") {
		e.t.Fatalf("warnings %s notices %s (has_warnings %v)", run.Warnings, run.Notices, run.HasWarnings)
	}
}

// assertMariaStats: MariaDB stats are approximate and signed.
func (e *biEnv) assertMariaStats(run sqlc.Run, m *pipeline.Manifest) {
	e.t.Helper()
	var ms struct {
		Databases map[string]struct {
			Consistency string `json:"consistency"`
			Tables      map[string]struct {
				Count       string `json:"count"`
				Approximate int64  `json:"approximate"`
			} `json:"tables"`
		} `json:"databases"`
	}
	if err := json.Unmarshal(m.Stats, &ms); err != nil {
		e.t.Fatal(err)
	}
	shop := ms.Databases["shop"]
	if o := shop.Tables["orders"]; shop.Consistency != "approximate" || o.Approximate != 1 || o.Count != "estimate" {
		e.t.Fatalf("manifest stats %s", m.Stats)
	}
	rows, err := e.q.ListBackupStatsByRun(e.t.Context(), run.ID)
	if err != nil || len(rows) != 1 || rows[0].Level != sqlc.JobVerificationStandard {
		e.t.Fatalf("backup_stats %+v %v", rows, err)
	}
	// bekup_app may not rewrite or delete the evidence.
	for _, stmt := range []string{"UPDATE backup_stats SET warning_count = 9", "DELETE FROM backup_stats"} {
		if _, err := e.pool.Exec(e.t.Context(), stmt); err == nil {
			e.t.Fatalf("%s: allowed for the app role", stmt)
		}
	}
}
