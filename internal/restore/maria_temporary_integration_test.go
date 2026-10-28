//go:build integration

package restore_test

// A MariaDB restore into a temporary restore connection (tasks 4.3/4.4
// coverage). As in the PostgreSQL harness (startPGEnv) `bekup runner`
// runs in a container with the Docker socket next to a MariaDB 11.8
// target on an internal network, and the restore worker runs in this
// process through the real connection service: a real dump of the
// source database is seeded as a signed, encrypted backup and restored
// into a new database through a temporary connection (D-163) to the
// server, and the restored rows are read back.

import (
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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/restore"
	"github.com/jlentink/bekup/internal/runner/spec"
	"github.com/jlentink/bekup/internal/runnerclient"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/testutil/dockertest"
	"github.com/jlentink/bekup/internal/testutil/runnertest"
)

const (
	mtHost = "maria.bekup.test"
	mtPass = "DB-PASSWORD-maria-temp-3c81d0"
	mtRows = 437
)

var mtSeed = `CREATE TABLE app.items (id int PRIMARY KEY, name varchar(64) NOT NULL);
INSERT INTO app.items WITH RECURSIVE s(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM s WHERE n < ` + strconv.Itoa(mtRows) + `)
  SELECT n, CONCAT('item ', n) FROM s;
`

type mtEnv struct {
	*env
	dk      *pgiDocker
	maria   string
	runner  string
	rc      *runnerclient.Client
	conns   *connections.Service
	srcConn uuid.UUID
	tmpConn uuid.UUID
	reg     *logging.Registry
}

// exec runs cmd in container id and returns its output.
func (d *pgiDocker) exec(id string, cmd ...string) string {
	d.t.Helper()
	ex, err := d.c.ExecCreate(d.t.Context(), id, client.ExecCreateOptions{Cmd: cmd, AttachStdout: true, AttachStderr: true})
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
		d.t.Fatalf("exec %v: exit %d: %s", cmd, ins.ExitCode, out.String())
	}
	return strings.TrimSpace(out.String())
}

func (e *mtEnv) sql(query string) string {
	e.t.Helper()
	return e.dk.exec(e.maria, "mariadb", "--skip-ssl", "-uroot", "-p"+mtPass, "-N", "-B", "-e", query)
}

func startMariaTempEnv(t *testing.T) *mtEnv {
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
	m118, ok := cat.Lookup(engine.KindMariaDB, "11.8")
	if !ok {
		t.Fatal("catalog lacks MariaDB 11.8")
	}
	ref := spec.ImageRef(m118)
	dk.ensureImage(ref)

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
	ctlID, _ := dk.network("bekup-it-restore-mt-ctl-"+suffix, false)
	tgID, tgSubnet := dk.network("bekup-it-restore-mt-targets-"+suffix, true)
	e := &mtEnv{dk: dk, reg: logging.NewRegistry()}
	e.maria = dk.container(client.ContainerCreateOptions{
		Name: "bekup-it-restore-maria-" + suffix,
		Config: &container.Config{Image: ref, Env: []string{"MARIADB_ROOT_PASSWORD=" + mtPass, "MARIADB_ROOT_HOST=%", "MARIADB_DATABASE=app"},
			Entrypoint: []string{"docker-entrypoint.sh"}, Cmd: []string{"mariadbd", "--skip-ssl"}},
		HostConfig: &container.HostConfig{NetworkMode: container.NetworkMode(tgID)},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			tgID: {Aliases: []string{mtHost}},
		}},
	})
	dk.copy(e.maria, map[string][]byte{"docker-entrypoint-initdb.d/10-seed.sql": []byte(mtSeed)}, nil)
	dk.start(e.maria)

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
		Name: "bekup-it-restore-mt-runner-" + suffix,
		Config: &container.Config{
			Image: ref, User: "0", Entrypoint: []string{"/bekup/bekup"},
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
	runnertest.WaitDockerOK(t, e.rc, 0, nil)
	e.conns, err = connections.New(connections.Options{
		DB: e.pool, Keyring: e.kr, Auditor: e.auditor, Runner: e.rc.RPC(), OperationTimeout: 3 * time.Minute,
		Limiter: func(string) (bool, time.Duration) { return true, 0 }, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	in := func(name string) connections.CreateInput {
		return connections.CreateInput{
			Name: name, Engine: engine.KindMariaDB, VersionKey: "11.8", Host: mtHost, Port: 3306, Username: "root",
			Password: []byte(mtPass), TLSMode: connections.TLSDisabled,
		}
	}
	actor := connections.Actor{UserID: e.admin, AllowTemporary: true}
	src, err := e.conns.Create(e.adminCtx(), actor, in("maria"))
	if err != nil {
		t.Fatal(err)
	}
	e.srcConn = src.ID
	tmp, err := e.conns.CreateTemporary(e.adminCtx(), actor, in(""))
	if err != nil {
		t.Fatal(err)
	}
	if !tmp.Temporary {
		t.Fatal("CreateTemporary returned a regular connection")
	}
	e.tmpConn = tmp.ID
	return e
}

// dump takes a real dump of database dbName on the source connection.
func (e *mtEnv) dump(dbName string) []byte {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(e.t.Context(), 5*time.Minute)
	defer cancel()
	target, release, err := e.conns.Target(ctx, e.srcConn)
	if err != nil {
		e.t.Fatal(err)
	}
	st, err := e.rc.RPC().Dump(ctx, &runnerpb.DumpRequest{Ctx: &runnerpb.RunContext{RunId: uuid.NewString()}, Target: target, Database: dbName})
	release()
	if err != nil {
		e.t.Fatal(err)
	}
	var out bytes.Buffer
	var res *runnerpb.DumpResult
	for {
		m, err := st.Recv()
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

// privileges probes the role of the temporary connection, as the API does
// for a request (RestoreTarget: temporary connections never go through
// Target).
func (e *mtEnv) privileges() connections.Privileges {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(e.t.Context(), 2*time.Minute)
	defer cancel()
	target, release, err := e.conns.RestoreTarget(ctx, e.tmpConn)
	if err != nil {
		e.t.Fatal(err)
	}
	resp, err := e.rc.RPC().Probe(ctx, &runnerpb.ProbeRequest{Ctx: &runnerpb.RunContext{RunId: uuid.NewString()}, Target: target})
	release()
	if err != nil {
		e.t.Fatal(err)
	}
	return connections.PrivilegesFromRunner(resp.GetPrivileges())
}

func TestIntegrationMariaDBRestoreTemporaryConnection(t *testing.T) {
	e := startMariaTempEnv(t)
	w, err := restore.NewWorker(restore.WorkerOptions{
		Service: e.svc, Slots: runstate.NewSlots(e.pool), Targets: e.conns, Runner: e.rc.RPC(),
		Limits:      runstate.SlotLimits{Global: 4, PerConnection: 2},
		LogMaxLines: 10000, LogMaxLineBytes: 4096, Registry: e.reg, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := e.sql("SELECT count(*) FROM app.items"); n != strconv.Itoa(mtRows) {
		t.Fatalf("seeded rows = %s", n)
	}
	dump := e.dump("app")

	// A job backing up the source connection, its dump seeded as a backup.
	j := e.job(jobkeys.ModePassphrase, e.diskID)
	e.exec("UPDATE jobs SET connection_id = $1 WHERE id = $2", e.srcConn, j.id)
	j.conn = e.srcConn
	s := e.seedAs(j, e.diskID, e.disk, dump, time.Now(), seedSpec{format: pipeline.FormatSQL, engine: string(engine.KindMariaDB), versionKey: "11.8"})

	priv := e.privileges()
	dbName := "copy_from_temp"
	var id uuid.UUID
	err = db.Tx(t.Context(), e.pool, func(q *sqlc.Queries) error {
		r, err := e.svc.CreateTx(t.Context(), q, restore.CreateInput{
			ArtifactID: s.artifact, TargetConnectionID: e.tmpConn, TargetDBName: &dbName, Mode: restore.ModeNewDB,
			RequestedBy: e.admin, Privileges: &priv, PrivilegedAck: priv.Gated(),
		})
		id = r.ID
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := (&pgiEnv{env: e.env}).work(t.Context(), w, id); err != nil {
		t.Fatal(err)
	}
	if st, code := e.status(id); st != "succeeded" {
		t.Fatalf("status %s %s\n%s", st, code, (&wenv{env: e.env}).logs(id))
	}
	if n := e.sql("SELECT count(*) FROM " + dbName + ".items"); n != strconv.Itoa(mtRows) {
		t.Fatalf("restored rows = %s, want %d", n, mtRows)
	}
	if n := e.sql("SELECT count(*) FROM " + dbName + ".items WHERE name = 'item 7'"); n != "1" {
		t.Fatalf("restored row 7: %s", n)
	}
	// The restore ran against the temporary connection and says so.
	if n := e.count(`SELECT count(*) FROM restores r JOIN connections c ON c.id = r.target_connection_id
		WHERE r.id = $1 AND c.temporary`, id); n != 1 {
		t.Fatal("the restore's target is not the temporary connection")
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE target_type = 'restore' AND target_id = $1
		AND (details->>'temporary_target')::boolean`, id.String()); n == 0 {
		t.Fatal("no audit entry marks the temporary target")
	}
	if n := e.count(`SELECT count(*) FROM restore_logs WHERE line LIKE '%' || $1 || '%'`, mtPass); n != 0 {
		t.Fatalf("%d restore log lines carry the password", n)
	}
}
