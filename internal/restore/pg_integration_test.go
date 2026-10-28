//go:build integration

package restore_test

// PostgreSQL restores end to end (task 4.2): as in the task 3.6 harness,
// `bekup runner` (built for the Docker daemon's platform) runs in a
// container with the Docker socket, next to a PostgreSQL 16 source and a
// PostgreSQL 17 target on an internal network; its mTLS port is published
// on 127.0.0.1. Real dumps of the source (Dump / DumpGlobals) are seeded
// as signed, encrypted backups and restored by the restore worker in this
// process through the real connection service and runner: a new database,
// an overwrite via temporary database + swap, a truncated dump that leaves
// the target intact, a cancel that drops the temporary database, a globals
// restore, and the cross-version client choice (16 dump, 17 client).

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"google.golang.org/grpc"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/connections"
	dbtx "github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/restore"
	"github.com/jlentink/bekup/internal/runner/spec"
	"github.com/jlentink/bekup/internal/runnerclient"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/testutil/dockertest"
	"github.com/jlentink/bekup/internal/testutil/runnertest"
)

const (
	pgiSrcHost = "pg16.bekup.test"
	pgiDstHost = "pg17.bekup.test"
	pgiUser    = "bekup"
	pgiPass    = "DB-PASSWORD-restore-it-4b2e9a"
)

// The source: 1000 items, a role, and a larger database for the cancel.
const pgiSrcSeed = `CREATE TABLE items (id int PRIMARY KEY, name text NOT NULL);
INSERT INTO items SELECT g, 'item ' || g FROM generate_series(1, 1000) g;
CREATE ROLE reporter LOGIN PASSWORD 'reporter-role-pass-28';
CREATE DATABASE big;
\connect big
CREATE TABLE blob (id int, payload text);
INSERT INTO blob SELECT g, md5(g::text) || md5((g * 7)::text) || md5((g * 13)::text) FROM generate_series(1, 400000) g;
`

// The target: an "app" with other data and a "big" to overwrite.
const pgiDstSeed = `CREATE DATABASE app;
CREATE DATABASE big;
\connect app
CREATE TABLE items (id int PRIMARY KEY, name text NOT NULL);
INSERT INTO items VALUES (1, 'old one'), (2, 'old two'), (3, 'old three');
\connect big
CREATE TABLE blob (id int, payload text);
INSERT INTO blob VALUES (1, 'keep'), (2, 'keep'), (3, 'keep');
`

func pgiIgnore(error)                  {}
func pgiIgnore2[T any](_ T, err error) { pgiIgnore(err) }

type pgiDocker struct {
	t *testing.T
	c *client.Client
}

func (d *pgiDocker) copy(id string, files map[string][]byte, modes map[string]int64) {
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
	if _, err := d.c.CopyToContainer(d.t.Context(), id, client.CopyToContainerOptions{DestinationPath: "/", Content: &buf}); err != nil {
		d.t.Fatal(err)
	}
}

func (d *pgiDocker) container(opts client.ContainerCreateOptions) string {
	d.t.Helper()
	res, err := d.c.ContainerCreate(d.t.Context(), opts)
	if err != nil {
		d.t.Fatal(err)
	}
	d.t.Cleanup(func() {
		pgiIgnore2(d.c.ContainerRemove(context.Background(), res.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}))
	})
	return res.ID
}

func (d *pgiDocker) start(id string) {
	d.t.Helper()
	if _, err := d.c.ContainerStart(d.t.Context(), id, client.ContainerStartOptions{}); err != nil {
		d.t.Fatal(err)
	}
}

func (d *pgiDocker) network(name string, internal bool) (string, string) {
	d.t.Helper()
	res, err := d.c.NetworkCreate(d.t.Context(), name, client.NetworkCreateOptions{Driver: "bridge", Internal: internal,
		Labels: map[string]string{"bekup.integration": "restore-pg"}})
	if err != nil {
		d.t.Fatal(err)
	}
	d.t.Cleanup(func() { pgiIgnore2(d.c.NetworkRemove(context.Background(), res.ID, client.NetworkRemoveOptions{})) })
	ins, err := d.c.NetworkInspect(d.t.Context(), res.ID, client.NetworkInspectOptions{})
	if err != nil || len(ins.Network.IPAM.Config) == 0 {
		d.t.Fatalf("inspect network: %v", err)
	}
	return res.ID, ins.Network.IPAM.Config[0].Subnet.String()
}

func (d *pgiDocker) ensureImage(ref string) {
	d.t.Helper()
	if _, err := d.c.ImageInspect(d.t.Context(), ref); err == nil {
		return
	}
	resp, err := d.c.ImagePull(d.t.Context(), ref, client.ImagePullOptions{})
	if err != nil {
		d.t.Fatal(err)
	}
	if err := resp.Wait(d.t.Context()); err != nil {
		d.t.Fatal(err)
	}
}

func (d *pgiDocker) logs(id string) string {
	rc, err := d.c.ContainerLogs(context.Background(), id, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return ""
	}
	defer func() { pgiIgnore(rc.Close()) }()
	var out bytes.Buffer
	pgiIgnore2(stdcopy.StdCopy(&out, &out, rc))
	return out.String()
}

func (d *pgiDocker) waitLog(id, needle string, count int, timeout time.Duration) {
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

// psql runs a query in a PostgreSQL container and returns the trimmed
// output.
func (d *pgiDocker) psql(id, db, query string) string {
	d.t.Helper()
	cmd := []string{"psql", "-X", "-U", pgiUser, "-d", db, "-tAc", query}
	ex, err := d.c.ExecCreate(d.t.Context(), id, client.ExecCreateOptions{User: "postgres", Cmd: cmd, AttachStdout: true, AttachStderr: true})
	if err != nil {
		d.t.Fatal(err)
	}
	att, err := d.c.ExecAttach(d.t.Context(), ex.ID, client.ExecAttachOptions{})
	if err != nil {
		d.t.Fatal(err)
	}
	defer att.Close()
	var out bytes.Buffer
	pgiIgnore2(stdcopy.StdCopy(&out, &out, att.Reader))
	ins, err := d.c.ExecInspect(d.t.Context(), ex.ID, client.ExecInspectOptions{})
	if err != nil {
		d.t.Fatal(err)
	}
	if ins.ExitCode != 0 {
		d.t.Fatalf("psql %q: exit %d: %s", query, ins.ExitCode, out.String())
	}
	return strings.TrimSpace(out.String())
}

// pgiEnv is the end-to-end environment.
type pgiEnv struct {
	*env
	dk       *pgiDocker
	src, dst string // containers
	runner   string
	rc       *runnerclient.Client
	conns    *connections.Service
	srcConn  uuid.UUID
	dstConn  uuid.UUID
	reg      *logging.Registry
	seq      int
}

func startPGEnv(t *testing.T) *pgiEnv {
	t.Helper()
	c, err := client.New(client.FromEnv)
	if err != nil {
		dockertest.Skipf(t, "docker client: %v", err)
	}
	t.Cleanup(func() { pgiIgnore(c.Close()) })
	ctx := t.Context()
	if _, err := c.Ping(ctx, client.PingOptions{}); err != nil {
		dockertest.Skipf(t, "docker not available: %v", err)
	}
	sv, err := c.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil || sv.Os != "linux" {
		dockertest.Skipf(t, "needs a linux Docker daemon (%v)", err)
	}
	dk := &pgiDocker{t: t, c: c}
	cat, err := engine.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	pg16, ok16 := cat.Lookup(engine.KindPostgres, "16")
	pg17, ok17 := cat.Lookup(engine.KindPostgres, "17")
	if !ok16 || !ok17 {
		t.Fatal("catalog lacks PostgreSQL 16 or 17")
	}
	ref16, ref17 := spec.ImageRef(pg16), spec.ImageRef(pg17)
	dk.ensureImage(ref16)
	dk.ensureImage(ref17)

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
	ctlID, _ := dk.network("bekup-it-restore-ctl-"+suffix, false)
	tgID, tgSubnet := dk.network("bekup-it-restore-targets-"+suffix, true)
	pg := func(name, ref, alias, seed string) string {
		id := dk.container(client.ContainerCreateOptions{
			Name:       name + "-" + suffix,
			Config:     &container.Config{Image: ref, Env: []string{"POSTGRES_USER=" + pgiUser, "POSTGRES_PASSWORD=" + pgiPass, "POSTGRES_DB=app"}},
			HostConfig: &container.HostConfig{NetworkMode: container.NetworkMode(tgID)},
			NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
				tgID: {Aliases: []string{alias}},
			}},
		})
		dk.copy(id, map[string][]byte{"docker-entrypoint-initdb.d/10-seed.sql": []byte(seed)}, nil)
		dk.start(id)
		return id
	}
	e := &pgiEnv{dk: dk, reg: logging.NewRegistry()}
	e.src = pg("bekup-it-restore-pg16", ref16, pgiSrcHost, pgiSrcSeed)
	// The target's POSTGRES_DB must differ from the seeded databases.
	e.dst = dk.container(client.ContainerCreateOptions{
		Name:       "bekup-it-restore-pg17-" + suffix,
		Config:     &container.Config{Image: ref17, Env: []string{"POSTGRES_USER=" + pgiUser, "POSTGRES_PASSWORD=" + pgiPass, "POSTGRES_DB=" + pgiUser}},
		HostConfig: &container.HostConfig{NetworkMode: container.NetworkMode(tgID)},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			tgID: {Aliases: []string{pgiDstHost}},
		}},
	})
	dk.copy(e.dst, map[string][]byte{"docker-entrypoint-initdb.d/10-seed.sql": []byte(pgiDstSeed)}, nil)
	dk.start(e.dst)

	pkiDir := filepath.Join(t.TempDir(), "pki")
	if _, err := certs.Init(certs.InitOptions{Dir: pkiDir, Components: []string{certs.ComponentRunner, certs.ComponentServe}, AllowUnencrypted: true}); err != nil {
		t.Fatal(err)
	}
	pki := certs.Layout{Dir: pkiDir}
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
		Name: "bekup-it-restore-runner-" + suffix,
		Config: &container.Config{
			Image: ref17, User: "0", Entrypoint: []string{"/bekup/bekup"},
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
		"bekup/runner.crt": read(pki.CertFile(certs.ComponentRunner)),
		"bekup/runner.key": read(pki.KeyFile(certs.ComponentRunner)),
		"bekup/ca.crt":     read(pki.CACert()),
	}, map[string]int64{"bekup/bekup": 0o755, "bekup/runner.key": 0o600})
	dk.start(e.runner)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("runner logs:\n%s", dk.logs(e.runner))
		}
	})
	dk.waitLog(e.src, "database system is ready to accept connections", 2, 5*time.Minute)
	dk.waitLog(e.dst, "database system is ready to accept connections", 2, 5*time.Minute)
	dk.waitLog(e.runner, "runner listening", 1, time.Minute)
	ins, err := c.ContainerInspect(ctx, e.runner, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b := ins.Container.NetworkSettings.Ports[port]
	if len(b) == 0 {
		t.Fatal("runner port not published")
	}
	addr := net.JoinHostPort("127.0.0.1", b[0].HostPort)

	e.env = newEnv(t)
	tlsCfg, err := certs.ClientTLSConfig(pki.CertFile(certs.ComponentServe), pki.KeyFile(certs.ComponentServe), pki.CACert(), "runner")
	if err != nil {
		t.Fatal(err)
	}
	if e.rc, err = runnerclient.NewWithTLS(addr, tlsCfg, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pgiIgnore(e.rc.Close()) })
	// "runner listening" precedes the executor's own-container detection.
	runnertest.WaitDockerOK(t, e.rc, 0, nil)
	e.conns, err = connections.New(connections.Options{
		DB: e.pool, Keyring: e.kr, Auditor: e.auditor, Runner: e.rc.RPC(), OperationTimeout: 3 * time.Minute,
		Limiter: func(string) (bool, time.Duration) { return true, 0 }, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(name, key, host string) uuid.UUID {
		cn, err := e.conns.Create(e.adminCtx(), connections.Actor{UserID: e.admin}, connections.CreateInput{
			Name: name, Engine: engine.KindPostgres, VersionKey: key, Host: host, Port: 5432, Username: pgiUser,
			Password: []byte(pgiPass), TLSMode: connections.TLSDisabled,
		})
		if err != nil {
			t.Fatal(err)
		}
		return cn.ID
	}
	e.srcConn = mk("pg16", "16", pgiSrcHost)
	e.dstConn = mk("pg17", "17", pgiDstHost)
	return e
}

// dump takes a real dump of the source (database, or globals when db is
// "").
func (e *pgiEnv) dump(db string) []byte {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(e.t.Context(), 5*time.Minute)
	defer cancel()
	target, release, err := e.conns.Target(ctx, e.srcConn)
	if err != nil {
		e.t.Fatal(err)
	}
	type msg interface {
		GetChunk() *runnerpb.DataChunk
		GetResult() *runnerpb.DumpResult
	}
	var recv func() (msg, error)
	rc := &runnerpb.RunContext{RunId: uuid.NewString()}
	if db == "" {
		s, err := e.rc.RPC().DumpGlobals(ctx, &runnerpb.DumpGlobalsRequest{Ctx: rc, Target: target})
		release()
		if err != nil {
			e.t.Fatal(err)
		}
		recv = func() (msg, error) { return s.Recv() }
	} else {
		s, err := e.rc.RPC().Dump(ctx, &runnerpb.DumpRequest{Ctx: rc, Target: target, Database: db})
		release()
		if err != nil {
			e.t.Fatal(err)
		}
		recv = func() (msg, error) { return s.Recv() }
	}
	var out bytes.Buffer
	var res *runnerpb.DumpResult
	for {
		m, err := recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			e.t.Fatal(err)
		}
		out.Write(m.GetChunk().GetData())
		if m.GetResult() != nil {
			res = m.GetResult()
		}
	}
	if res == nil || res.GetExitCode() != 0 || res.GetBytes() != uint64(out.Len()) { //nolint:gosec // G115: a buffer length is never negative
		e.t.Fatalf("dump result %v (%d bytes)", res, out.Len())
	}
	return out.Bytes()
}

// worker builds a restore worker on runner (the real client or a wrapper).
func (e *pgiEnv) worker(runner runnerpb.RunnerServiceClient) *restore.Worker {
	e.t.Helper()
	w, err := restore.NewWorker(restore.WorkerOptions{
		Service: e.svc, Slots: runstate.NewSlots(e.pool), Targets: e.conns, Runner: runner,
		Limits:      runstate.SlotLimits{Global: 4, PerConnection: 2},
		LogMaxLines: 10000, LogMaxLineBytes: 4096, Registry: e.reg, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return w
}

// seedRestore seeds dump as a backup of job j and requests its restore
// into the target as the admin. The harness role is a superuser: like the
// API, the request carries the probed verdict and the admin's
// acknowledgement (D-068), which the worker's re-check requires (D-173).
func (e *pgiEnv) seedRestore(j jobInfo, dump []byte, sp seedSpec, mode restore.Mode, dbName *string) uuid.UUID {
	e.t.Helper()
	return e.seedRestoreAck(j, dump, sp, mode, dbName, true)
}

// seedRestoreAck is seedRestore with or without the acknowledgement.
func (e *pgiEnv) seedRestoreAck(j jobInfo, dump []byte, sp seedSpec, mode restore.Mode, dbName *string, ack bool) uuid.UUID {
	e.t.Helper()
	e.seq++
	s := e.seedAs(j, e.diskID, e.disk, dump, time.Now().Add(time.Duration(e.seq)*time.Minute), sp)
	priv := e.targetPrivileges(e.dstConn)
	var id uuid.UUID
	err := dbtx.Tx(e.t.Context(), e.pool, func(q *sqlc.Queries) error {
		r, err := e.svc.CreateTx(e.t.Context(), q, restore.CreateInput{
			ArtifactID: s.artifact, TargetConnectionID: e.dstConn, TargetDBName: dbName, Mode: mode,
			RequestedBy: e.admin, Privileges: &priv, PrivilegedAck: ack && priv.Gated(),
		})
		id = r.ID
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *pgiEnv) work(ctx context.Context, w *restore.Worker, id uuid.UUID) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	return w.Work(ctx, &river.Job[queue.RestoreArgs]{JobRow: &rivertype.JobRow{ID: 1, Attempt: 1}, Args: queue.RestoreArgs{RestoreID: id}})
}

// bekupDBs lists leftover bekup temporary databases on the target.
func (e *pgiEnv) bekupDBs() string {
	return e.dk.psql(e.dst, pgiUser, `SELECT coalesce(string_agg(datname, ','), '') FROM pg_database WHERE datname LIKE '%\_\_bekup\_%'`)
}

// cancelingRunner cancels the restore after the n-th Restore chunk.
type cancelingRunner struct {
	runnerpb.RunnerServiceClient
	after  int32
	cancel context.CancelCauseFunc
}

func (c *cancelingRunner) Restore(ctx context.Context, opts ...grpc.CallOption) (grpc.BidiStreamingClient[runnerpb.RestoreRequest, runnerpb.RestoreResponse], error) {
	s, err := c.RunnerServiceClient.Restore(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &cancelingStream{BidiStreamingClient: s, c: c}, nil
}

type cancelingStream struct {
	grpc.BidiStreamingClient[runnerpb.RestoreRequest, runnerpb.RestoreResponse]
	c *cancelingRunner
	n atomic.Int32
}

func (s *cancelingStream) Send(m *runnerpb.RestoreRequest) error {
	if m.GetChunk() != nil && s.n.Add(1) == s.c.after {
		// Give pg_restore time to start writing into the temporary
		// database before the cancel.
		time.Sleep(2 * time.Second)
		s.c.cancel(rivertype.ErrJobCancelledRemotely)
	}
	return s.BidiStreamingClient.Send(m)
}

func TestIntegrationPGRestore(t *testing.T) {
	e := startPGEnv(t)
	w := e.worker(e.rc.RPC())
	j := e.job(jobkeys.ModePassphrase, e.diskID)
	pgCustom := seedSpec{format: pipeline.FormatCustom, engine: "postgres", versionKey: "16"}
	appDump := e.dump("app")

	t.Run("new database across versions", func(t *testing.T) {
		id := e.seedRestore(j, appDump, pgCustom, restore.ModeNewDB, strp("copy"))
		if err := e.work(t.Context(), w, id); err != nil {
			t.Fatal(err)
		}
		if st, code := e.status(id); st != "succeeded" {
			t.Fatalf("status %s %s\n%s", st, code, (&wenv{env: e.env}).logs(id))
		}
		if n := e.dk.psql(e.dst, "copy", "SELECT count(*) FROM items"); n != "1000" {
			t.Fatalf("rows = %s", n)
		}
		// The 16 dump was restored with the 17 client (max(dump, server)).
		found := false
		for _, l := range strings.Split(e.dk.logs(e.runner), "\n") {
			if strings.Contains(l, "runner_rpc_audit") && strings.Contains(l, id.String()) && strings.Contains(l, "Restore") &&
				(strings.Contains(l, `"version_key":"17"`) || strings.Contains(l, "version_key=17")) {
				found = true
			}
		}
		if !found {
			t.Fatal("no Restore audit line with version_key 17")
		}
	})

	t.Run("superuser target without acknowledgement is refused", func(t *testing.T) {
		id := e.seedRestoreAck(j, appDump, pgCustom, restore.ModeNewDB, strp("refused_copy"), false)
		if err := e.work(t.Context(), w, id); err != nil {
			t.Fatal(err)
		}
		if st, code := e.status(id); st != "failed" || code != restore.CodePrivilegedTarget {
			t.Fatalf("status %s %s", st, code)
		}
		if n := e.dk.psql(e.dst, pgiUser, "SELECT count(*) FROM pg_database WHERE datname = 'refused_copy'"); n != "0" {
			t.Fatal("the refused restore created its database")
		}
	})

	t.Run("overwrite via temporary database and swap", func(t *testing.T) {
		if n := e.dk.psql(e.dst, "app", "SELECT count(*) FROM items"); n != "3" {
			t.Fatalf("precondition rows = %s", n)
		}
		id := e.seedRestore(j, appDump, pgCustom, restore.ModeOverwrite, strp("app"))
		if err := e.work(t.Context(), w, id); err != nil {
			t.Fatal(err)
		}
		if st, code := e.status(id); st != "succeeded" {
			t.Fatalf("status %s %s\n%s", st, code, (&wenv{env: e.env}).logs(id))
		}
		if n := e.dk.psql(e.dst, "app", "SELECT count(*) FROM items"); n != "1000" {
			t.Fatalf("rows = %s", n)
		}
		if n := e.dk.psql(e.dst, "app", "SELECT count(*) FROM items WHERE name LIKE 'old%'"); n != "0" {
			t.Fatalf("old rows = %s", n)
		}
		if left := e.bekupDBs(); left != "" {
			t.Fatalf("leftover databases: %s", left)
		}
	})

	t.Run("truncated dump leaves the target intact", func(t *testing.T) {
		e.dk.psql(e.dst, "app", "INSERT INTO items VALUES (424242, 'marker')")
		id := e.seedRestore(j, appDump[:len(appDump)/2], pgCustom, restore.ModeOverwrite, strp("app"))
		if err := e.work(t.Context(), w, id); err != nil {
			t.Fatal(err)
		}
		if st, code := e.status(id); st != "failed" || code != restore.CodeRestoreFailed {
			t.Fatalf("status %s %s", st, code)
		}
		if n := e.dk.psql(e.dst, "app", "SELECT count(*) FROM items"); n != "1001" {
			t.Fatalf("rows = %s", n)
		}
		if left := e.bekupDBs(); left != "" {
			t.Fatalf("leftover databases: %s", left)
		}
	})

	t.Run("cancel drops the temporary database", func(t *testing.T) {
		bigDump := e.dump("big")
		id := e.seedRestore(j, bigDump, pgCustom, restore.ModeOverwrite, strp("big"))
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		cw := e.worker(&cancelingRunner{RunnerServiceClient: e.rc.RPC(), after: 3, cancel: cancel})
		err := e.work(ctx, cw, id)
		var jc *rivertype.JobCancelError
		if !errors.As(err, &jc) {
			t.Fatalf("err = %v", err)
		}
		if st, code := e.status(id); st != "canceled" || code != restore.CodeCanceled {
			t.Fatalf("status %s %s", st, code)
		}
		if n := e.dk.psql(e.dst, "big", "SELECT count(*) FROM blob"); n != "3" {
			t.Fatalf("rows = %s", n)
		}
		if left := e.bekupDBs(); left != "" {
			t.Fatalf("leftover databases: %s", left)
		}
	})

	t.Run("globals", func(t *testing.T) {
		if n := e.dk.psql(e.dst, pgiUser, "SELECT count(*) FROM pg_roles WHERE rolname = 'reporter'"); n != "0" {
			t.Fatalf("precondition: %s", n)
		}
		globals := e.dump("")
		id := e.seedRestore(j, globals, seedSpec{kind: pipeline.KindGlobals, format: pipeline.FormatSQL, engine: "postgres", versionKey: "16"},
			restore.ModeNewDB, nil)
		if err := e.work(t.Context(), w, id); err != nil {
			t.Fatal(err)
		}
		if st, code := e.status(id); st != "succeeded" {
			t.Fatalf("status %s %s\n%s", st, code, (&wenv{env: e.env}).logs(id))
		}
		if n := e.dk.psql(e.dst, pgiUser, "SELECT count(*) FROM pg_roles WHERE rolname = 'reporter' AND rolcanlogin"); n != "1" {
			t.Fatalf("role reporter: %s", n)
		}
	})

	// No credential reached a restore log.
	if n := e.count(`SELECT count(*) FROM restore_logs WHERE line LIKE '%' || $1 || '%'`, pgiPass); n != 0 {
		t.Fatalf("%d restore log lines carry the password", n)
	}
}
