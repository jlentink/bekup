//go:build integration

package mysqlit

// Integration tests of the MySQL/MariaDB restorer (task 4.3). As in the
// runner's RPC harness (internal/runner/ops_integration_test.go) the
// runner runs inside a container so its executor's self-join is real: the
// outer test (on the host) builds this package's test binary for linux,
// starts MySQL and MariaDB targets on an internal network and runs the
// binary in a container on that network with the Docker socket. The inner
// test (TestInnerMySQLRestore, selected by BEKUP_MYSQL_RESTORE_INNER)
// starts a real runner.Server with the Docker executor and drives the
// restorer (restore.MySQLFamilyRestorer) through a real mTLS client.

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/restore"
	"github.com/jlentink/bekup/internal/runner"
	"github.com/jlentink/bekup/internal/runner/executor"
	"github.com/jlentink/bekup/internal/runner/spec"
	"github.com/jlentink/bekup/internal/runnerclient"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/testutil/dockertest"
)

const (
	myInnerEnv = "BEKUP_MYSQL_RESTORE_INNER"
	myDomain   = ".bekup.test"
)

// myTarget is one target database of the harness.
type myTarget struct {
	Name      string      `json:"name"`
	Host      string      `json:"host"`
	Kind      engine.Kind `json:"kind"`
	Key       string      `json:"key"`
	Container string      `json:"container"`
}

// myHarness is passed from the outer to the inner test (test-only
// credentials).
type myHarness struct {
	Password string     `json:"password"`
	Targets  []myTarget `json:"targets"`
	Subnet   string     `json:"subnet"`
}

var myDBs = []struct {
	name, key string
	kind      engine.Kind
}{
	{"mysql84", "8.4", engine.KindMySQL},
	{"mysql97", "9.7", engine.KindMySQL},
	{"maria114", "11.4", engine.KindMariaDB},
	{"maria118", "11.8", engine.KindMariaDB},
}

func myIgnore(error) {}

func myIgnore2[T any](_ T, err error) { myIgnore(err) }

func myRandHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func myTar(t *testing.T, files map[string][]byte, modes map[string]int64) io.Reader {
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

func myContainer(t *testing.T, c *client.Client, opts client.ContainerCreateOptions) string {
	t.Helper()
	res, err := c.ContainerCreate(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		myIgnore2(c.ContainerRemove(context.Background(), res.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}))
	})
	return res.ID
}

func myLogs(c *client.Client, id string) string {
	rc, err := c.ContainerLogs(context.Background(), id, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return ""
	}
	defer func() { myIgnore(rc.Close()) }()
	var out bytes.Buffer
	myIgnore2(stdcopy.StdCopy(&out, &out, rc))
	return out.String()
}

func myEnsureImage(t *testing.T, c *client.Client, ref string) {
	t.Helper()
	if _, err := c.ImageInspect(t.Context(), ref); err == nil {
		return
	}
	resp, err := c.ImagePull(t.Context(), ref, client.ImagePullOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func myExecCode(t *testing.T, c *client.Client, id string, cmd ...string) (string, int) {
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
	myIgnore2(stdcopy.StdCopy(&out, &errOut, att.Reader))
	ins, err := c.ExecInspect(t.Context(), ex.ID, client.ExecInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ins.ExitCode != 0 {
		return out.String() + errOut.String(), ins.ExitCode
	}
	return out.String(), 0
}

func TestIntegrationMySQLRestore(t *testing.T) {
	if os.Getenv(myInnerEnv) != "" {
		t.Skip("outer harness")
	}
	c, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	t.Cleanup(func() { myIgnore(c.Close()) })
	ctx := t.Context()
	if _, err := c.Ping(ctx, client.PingOptions{}); err != nil {
		dockertest.Skipf(t, "docker not available: %v", err)
	}
	sv, err := c.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil || sv.Os != "linux" {
		dockertest.Skipf(t, "needs a linux Docker daemon (%v)", err)
	}
	cat, err := engine.Embedded()
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	bin := filepath.Join(dir, "mysqlit.test")
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

	suffix := myRandHex(t, 4)
	nres, err := c.NetworkCreate(ctx, "bekup-it-myrestore-"+suffix, client.NetworkCreateOptions{Driver: "bridge", Internal: true,
		Labels: map[string]string{"bekup.integration": "mysql-restore"}})
	if err != nil {
		t.Fatal(err)
	}
	netID := nres.ID
	t.Cleanup(func() { myIgnore2(c.NetworkRemove(context.Background(), netID, client.NetworkRemoveOptions{})) })
	nins, err := c.NetworkInspect(ctx, netID, client.NetworkInspectOptions{})
	if err != nil || len(nins.Network.IPAM.Config) == 0 {
		t.Fatalf("inspect network: %v", err)
	}
	pw := myRandHex(t, 16)

	targets := make([]myTarget, 0, len(myDBs))
	var baseRef string
	for _, d := range myDBs {
		ent, ok := cat.Lookup(d.kind, d.key)
		if !ok {
			t.Fatalf("no catalog entry %s %s", d.kind, d.key)
		}
		ref := spec.ImageRef(ent)
		myEnsureImage(t, c, ref)
		baseRef = ref
		host := d.name + myDomain
		var envs, entry []string
		if d.kind == engine.KindMySQL {
			// The server's auto-generated certificate: TLS mode UNVERIFIED.
			// local_infile is enabled on the server so only the client flag
			// stops LOAD DATA LOCAL.
			envs = []string{"MYSQL_ROOT_PASSWORD=" + pw, "MYSQL_ROOT_HOST=%"}
			entry = []string{"docker-entrypoint.sh", "mysqld", "--local-infile=1"}
		} else {
			envs = []string{"MARIADB_ROOT_PASSWORD=" + pw, "MARIADB_ROOT_HOST=%"}
			entry = []string{"docker-entrypoint.sh", "mariadbd", "--skip-ssl", "--local-infile=1"}
		}
		id := myContainer(t, c, client.ContainerCreateOptions{
			Name:       "bekup-it-myrestore-" + d.name + "-" + suffix,
			Config:     &container.Config{Image: ref, Env: envs, Entrypoint: entry[:1], Cmd: entry[1:]},
			HostConfig: &container.HostConfig{NetworkMode: container.NetworkMode(netID)},
			NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
				netID: {Aliases: []string{host}},
			}},
		})
		if _, err := c.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
			t.Fatal(err)
		}
		targets = append(targets, myTarget{Name: d.name, Host: host, Kind: d.kind, Key: d.key, Container: id})
	}
	for _, tg := range targets {
		needle := "port: 3306"
		if tg.Kind == engine.KindMySQL {
			needle = "port: 3306  MySQL"
		}
		deadline := time.Now().Add(4 * time.Minute)
		for !strings.Contains(myLogs(c, tg.Container), needle) {
			if time.Now().After(deadline) {
				t.Fatalf("%s not ready:\n%s", tg.Name, myLogs(c, tg.Container))
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	h, err := json.Marshal(myHarness{ //nolint:gosec // G117: test-only credentials for the inner harness container
		Password: pw, Targets: targets, Subnet: nins.Network.IPAM.Config[0].Subnet.String()})
	if err != nil {
		t.Fatal(err)
	}
	inner := myContainer(t, c, client.ContainerCreateOptions{
		Name: "bekup-it-myrestore-runner-" + suffix,
		Config: &container.Config{
			Image: baseRef, User: "0", Entrypoint: []string{"/harness/mysqlit.test"},
			Cmd: []string{"-test.run", "^TestInnerMySQLRestore$", "-test.v", "-test.timeout", "20m"},
			Env: []string{myInnerEnv + "=/harness/harness.json"},
		},
		HostConfig: &container.HostConfig{NetworkMode: container.NetworkMode(netID),
			Binds: []string{"/var/run/docker.sock:/var/run/docker.sock"}},
	})
	if _, err := c.CopyToContainer(ctx, inner, client.CopyToContainerOptions{DestinationPath: "/",
		Content: myTar(t, map[string][]byte{"harness/mysqlit.test": binData, "harness/harness.json": h},
			map[string]int64{"harness/mysqlit.test": 0o755, "harness/harness.json": 0o600})}); err != nil {
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
	out := myLogs(c, inner)
	t.Logf("inner test output:\n%s", out)
	if code != 0 {
		t.Fatalf("inner test failed (exit %d)", code)
	}
	if strings.Contains(out, pw) {
		t.Fatal("the database password appeared in the output")
	}
}

// --- inner test --------------------------------------------------------------

type myInner struct {
	h   myHarness
	rpc runnerpb.RunnerServiceClient
	dc  *client.Client
}

// mySink collects restore log lines.
type mySink struct {
	mu    sync.Mutex
	lines []string
}

func (s *mySink) Line(level, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, level+": "+msg)
}

func (s *mySink) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.lines, "\n")
}

func myAPIEngine(k engine.Kind) runnerpb.Engine {
	if k == engine.KindMySQL {
		return runnerpb.Engine_ENGINE_MYSQL
	}
	return runnerpb.Engine_ENGINE_MARIADB
}

func (in *myInner) target(tg myTarget) *runnerpb.Target {
	rt := &runnerpb.Target{
		Engine: myAPIEngine(tg.Kind), VersionKey: tg.Key, Host: tg.Host, Port: 3306,
		User: "root", Password: []byte(in.h.Password), TlsMode: runnerpb.TlsMode_TLS_MODE_DISABLED,
	}
	if tg.Kind == engine.KindMySQL {
		rt.TlsMode = runnerpb.TlsMode_TLS_MODE_UNVERIFIED
	}
	return rt
}

// sql runs statements inside a target container (seeding and checks).
func (in *myInner) sql(t *testing.T, tg myTarget, db, stmt string) string {
	t.Helper()
	out, code := in.sqlCode(t, tg, db, stmt)
	if code != 0 {
		t.Fatalf("%s: %q: exit %d: %s", tg.Name, stmt, code, out)
	}
	return out
}

func (in *myInner) sqlCode(t *testing.T, tg myTarget, db, stmt string) (string, int) {
	t.Helper()
	bin := "mysql"
	if tg.Kind == engine.KindMariaDB {
		bin = "mariadb"
	}
	args := []string{bin, "-uroot", "-p" + in.h.Password, "-N", "-B"}
	if tg.Kind == engine.KindMariaDB {
		args = append(args, "--skip-ssl")
	}
	if db != "" {
		args = append(args, "-D", db)
	}
	out, code := myExecCode(t, in.dc, tg.Container, append(args, "-e", stmt)...)
	var keep []string
	for _, l := range strings.Split(out, "\n") {
		if !strings.Contains(l, "Using a password on the command line") {
			keep = append(keep, l)
		}
	}
	return strings.TrimSpace(strings.Join(keep, "\n")), code
}

func (in *myInner) dump(t *testing.T, tg myTarget, db string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	s, err := in.rpc.Dump(ctx, &runnerpb.DumpRequest{Ctx: &runnerpb.RunContext{RunId: uuid.NewString()}, Target: in.target(tg), Database: db})
	if err != nil {
		t.Fatal(err)
	}
	var data []byte
	var res *runnerpb.DumpResult
	for {
		m, err := s.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if ch := m.GetChunk(); ch != nil {
			data = append(data, ch.GetData()...)
		}
		if r := m.GetResult(); r != nil {
			res = r
		}
	}
	if res.GetExitCode() != 0 || !res.GetVerified() {
		t.Fatalf("dump %s: %v", tg.Name, res)
	}
	return data
}

// restore runs the MySQL/MariaDB restorer.
func (in *myInner) restore(t *testing.T, tg myTarget, srcKind engine.Kind, db string, mode restore.Mode, kind restore.Kind, dump []byte) (restore.EngineResult, *mySink, error) {
	t.Helper()
	r, ok := restore.MySQLFamilyRestorer(tg.Kind)
	if !ok {
		t.Fatal("no restorer")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	sink := &mySink{}
	req := restore.EngineRequest{
		RestoreID: uuid.New(), Runner: in.rpc,
		Target: func(context.Context) (*runnerpb.Target, func(), error) {
			rt := in.target(tg)
			return rt, func() { runnerpb.ZeroSecrets(rt) }, nil
		},
		TargetEngine: tg.Kind, TargetDB: db, Mode: mode, Kind: kind,
		SourceEngine: srcKind, SourceVersion: tg.Key, TotalBytes: uint64(len(dump)),
	}
	res, err := r.Restore(ctx, req, bytes.NewReader(dump), sink)
	return res, sink, err
}

func TestInnerMySQLRestore(t *testing.T) {
	path := os.Getenv(myInnerEnv)
	if path == "" {
		t.Skip("runs inside the harness container only")
	}
	raw, err := os.ReadFile(path) //nolint:gosec // harness file
	if err != nil {
		t.Fatal(err)
	}
	var h myHarness
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatal(err)
	}
	dc, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { myIgnore(dc.Close()) }()
	docker, closer, err := executor.NewMobyDocker("unix:///var/run/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { myIgnore(closer.Close()) }()

	pkiDir := filepath.Join(t.TempDir(), "pki")
	if _, err := certs.Init(certs.InitOptions{Dir: pkiDir, Components: []string{certs.ComponentRunner, certs.ComponentServe},
		AllowUnencrypted: true}); err != nil {
		t.Fatal(err)
	}
	l := certs.Layout{Dir: pkiDir}
	cfg := config.DefaultRunner()
	cfg.Listen.Address = "127.0.0.1:0"
	cfg.Listen.HealthSocket = ""
	cfg.TLS.CertFile = l.CertFile(certs.ComponentRunner)
	cfg.TLS.KeyFile = l.KeyFile(certs.ComponentRunner)
	cfg.TLS.ClientCAFile = l.CACert()
	cfg.TLS.AllowedClientSANs = []string{"spiffe://bekup/serve"}
	cfg.Targets.AllowCIDRs = []string{h.Subnet}
	var logBuf mySyncBuf
	srv, err := runner.New(runner.Options{Config: &cfg, Version: "it", DockerClient: docker,
		Logger: slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	if err != nil {
		t.Fatal(err)
	}
	var lc net.ListenConfig
	lis, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sctx, scancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(sctx, lis) }()
	defer func() {
		scancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("runner did not stop")
		}
	}()
	tlsCfg, err := certs.ClientTLSConfig(l.CertFile(certs.ComponentServe), l.KeyFile(certs.ComponentServe), l.CACert(), "runner")
	if err != nil {
		t.Fatal(err)
	}
	rc, err := runnerclient.NewWithTLS(lis.Addr().String(), tlsCfg, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { myIgnore(rc.Close()) }()
	deadline := time.Now().Add(90 * time.Second)
	for {
		hctx, hcancel := context.WithTimeout(t.Context(), 10*time.Second)
		hr, err := rc.Health(hctx)
		hcancel()
		if err == nil && hr.GetDockerStatus() == runnerpb.DockerStatus_DOCKER_STATUS_OK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("executor not ready: %v %v\n%s", hr, err, logBuf.String())
		}
		time.Sleep(time.Second)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("runner log:\n%s", logBuf.String())
		}
		if strings.Contains(logBuf.String(), h.Password) {
			t.Error("the runner log contains the database password")
		}
	})
	in := &myInner{h: h, rpc: rc.RPC(), dc: dc}
	for _, tg := range h.Targets {
		t.Run(tg.Name, func(t *testing.T) { in.run(t, tg) })
	}
}

// mySyncBuf is a log sink safe for concurrent use.
type mySyncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *mySyncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *mySyncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

const mySeed = `CREATE TABLE t (id INT PRIMARY KEY, name VARCHAR(64) NOT NULL) ENGINE=InnoDB;
INSERT INTO t SELECT n, LEFT(SHA2(n, 256), 32) FROM (WITH RECURSIVE s(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM s WHERE n < 900) SELECT n FROM s) x;
CREATE TABLE b (id INT PRIMARY KEY, data TEXT NOT NULL) ENGINE=InnoDB;
INSERT INTO b SELECT n, REPEAT(SHA2(n, 256), 32) FROM (WITH RECURSIVE s(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM s WHERE n < 950) SELECT n FROM s) x;
CREATE TABLE m (id INT) ENGINE=MyISAM; INSERT INTO m VALUES (1), (2);
CREATE VIEW v AS SELECT COUNT(*) AS c FROM t;
CREATE PROCEDURE p() SELECT 1;
CREATE TRIGGER tr BEFORE INSERT ON m FOR EACH ROW SET NEW.id = NEW.id + 0;`

// counts are the row counts and a checksum of the seeded tables.
func (in *myInner) counts(t *testing.T, tg myTarget, db string) string {
	t.Helper()
	return in.sql(t, tg, db, "SELECT CONCAT((SELECT COUNT(*) FROM t), ':', (SELECT SUM(CRC32(CONCAT(id,'|',name))) FROM t), ':', "+
		"(SELECT COUNT(*) FROM b), ':', (SELECT SUM(CRC32(data)) FROM b), ':', (SELECT COUNT(*) FROM m), ':', (SELECT c FROM v))")
}

// objects lists a database's tables, views, routines and triggers.
func (in *myInner) objects(t *testing.T, tg myTarget, db string) string {
	t.Helper()
	q := "SELECT CONCAT_WS(',', " +
		"(SELECT GROUP_CONCAT(TABLE_NAME, '/', TABLE_TYPE ORDER BY TABLE_NAME) FROM information_schema.TABLES WHERE TABLE_SCHEMA = '" + db + "'), " +
		"(SELECT GROUP_CONCAT(ROUTINE_NAME ORDER BY ROUTINE_NAME) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = '" + db + "'), " +
		"(SELECT GROUP_CONCAT(TRIGGER_NAME ORDER BY TRIGGER_NAME) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = '" + db + "'))"
	return in.sql(t, tg, "", q)
}

func (in *myInner) run(t *testing.T, tg myTarget) {
	in.sql(t, tg, "", "CREATE DATABASE shop")
	in.sql(t, tg, "shop", mySeed)
	want, wantObjs := in.counts(t, tg, "shop"), in.objects(t, tg, "shop")
	if !strings.HasPrefix(want, "900:") || !strings.Contains(wantObjs, "p") {
		t.Fatalf("seed: %s %s", want, wantObjs)
	}
	dump := in.dump(t, tg, "shop")
	if len(dump) <= runnerpb.MaxChunkBytes {
		t.Fatalf("dump of %d bytes does not span several chunks", len(dump))
	}

	t.Run("new_db", func(t *testing.T) {
		res, sink, err := in.restore(t, tg, tg.Kind, "shop_copy", restore.ModeNewDB, restore.KindDatabase, dump)
		if err != nil {
			t.Fatalf("restore: %v\n%s", err, sink.text())
		}
		if got := in.counts(t, tg, "shop_copy"); got != want {
			t.Fatalf("counts %s, want %s", got, want)
		}
		if got := in.objects(t, tg, "shop_copy"); got != wantObjs {
			t.Fatalf("objects %s, want %s", got, wantObjs)
		}
		t.Logf("warnings %v", res.Warnings)
		// The database now exists: a second new-database restore is refused.
		_, _, err = in.restore(t, tg, tg.Kind, "shop_copy", restore.ModeNewDB, restore.KindDatabase, dump)
		if runnerpb.ErrorReason(err) != runnerpb.ReasonDatabaseExists {
			t.Fatalf("second new-db restore: %v", err)
		}
	})

	t.Run("overwrite", func(t *testing.T) {
		in.sql(t, tg, "", "CREATE DATABASE victim CHARACTER SET latin1 COLLATE latin1_swedish_ci")
		in.sql(t, tg, "victim", "CREATE TABLE t (id INT PRIMARY KEY, name VARCHAR(64)); INSERT INTO t VALUES (1, 'old'), (2, 'old'), (99999, 'old'); "+
			"CREATE TABLE extra (id INT); INSERT INTO extra VALUES (1); CREATE VIEW extrav AS SELECT 1 AS one; CREATE PROCEDURE extrap() SELECT 2")
		_, sink, err := in.restore(t, tg, tg.Kind, "victim", restore.ModeOverwrite, restore.KindDatabase, dump)
		if err != nil {
			t.Fatalf("restore: %v\n%s", err, sink.text())
		}
		if got := in.counts(t, tg, "victim"); got != want {
			t.Fatalf("counts %s, want %s", got, want)
		}
		if got := in.objects(t, tg, "victim"); got != wantObjs {
			t.Fatalf("objects %s, want %s (extra objects must be gone)", got, wantObjs)
		}
		cs := in.sql(t, tg, "", "SELECT CONCAT(DEFAULT_CHARACTER_SET_NAME, '/', DEFAULT_COLLATION_NAME) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = 'victim'")
		if cs != "latin1/latin1_swedish_ci" {
			t.Fatalf("database defaults %q", cs)
		}
		if !strings.Contains(sink.text(), "warn: overwriting database victim in place") {
			t.Fatalf("logs:\n%s", sink.text())
		}
		// Overwriting a database that does not exist is refused before
		// anything runs.
		_, _, err = in.restore(t, tg, tg.Kind, "missingdb", restore.ModeOverwrite, restore.KindDatabase, dump)
		if runnerpb.ErrorReason(err) != runnerpb.ReasonDatabaseNotFound {
			t.Fatalf("overwrite of a missing database: %v", err)
		}
	})

	t.Run("truncated", func(t *testing.T) {
		cut := dump[:len(dump)/2]
		_, sink, err := in.restore(t, tg, tg.Kind, "cut_db", restore.ModeNewDB, restore.KindDatabase, cut)
		if !errors.Is(err, restore.ErrRestoreIncomplete) || !strings.Contains(sink.text(), "does not end with the") {
			t.Fatalf("err %v\n%s", err, sink.text())
		}
	})

	t.Run("corrupt", func(t *testing.T) {
		bad := bytes.Clone(dump)
		i := bytes.Index(bad, []byte("INSERT INTO `b`"))
		if i < 0 {
			t.Fatal("no INSERT INTO `b` in the dump")
		}
		copy(bad[i:], "INSERTX INTO")
		_, sink, err := in.restore(t, tg, tg.Kind, "corrupt_db", restore.ModeNewDB, restore.KindDatabase, bad)
		if !errors.Is(err, restore.ErrRestoreFailed) || !strings.Contains(sink.text(), "ERROR 1064") {
			t.Fatalf("err %v\n%s", err, sink.text())
		}
	})

	t.Run("refused", func(t *testing.T) {
		other := engine.KindMariaDB
		if tg.Kind == engine.KindMariaDB {
			other = engine.KindMySQL
		}
		if _, _, err := in.restore(t, tg, other, "mismatch_db", restore.ModeNewDB, restore.KindDatabase, dump); !errors.Is(err, engine.ErrWrongEngine) {
			t.Fatalf("engine mismatch: %v", err)
		}
		if _, _, err := in.restore(t, tg, tg.Kind, "", restore.ModeNewDB, restore.KindGlobals, dump); !errors.Is(err, engine.ErrUnsupported) {
			t.Fatalf("globals: %v", err)
		}
		if out := in.sql(t, tg, "", "SHOW DATABASES LIKE 'mismatch_db'"); out != "" {
			t.Fatalf("mismatch_db was created: %q", out)
		}
	})

	t.Run("hostile", func(t *testing.T) { in.hostile(t, tg) })
	t.Run("test_restore", func(t *testing.T) { in.testRestore(t, tg) })
}

// hostile restores dumps that try to run shell commands, read local files
// with source or LOAD DATA LOCAL, or write files: MySQL's
// --system-command=OFF / --commands=OFF / --local-infile=0 and MariaDB's
// --sandbox / --local-infile=0 must refuse every one of them. A shell
// command would print "bekup-pwned uid=" to stderr (relayed to the logs)
// and create the database pwned_<n> through the client's own option file;
// a sourced /etc/passwd would show "root:x:0:0" in the server's error.
func (in *myInner) hostile(t *testing.T, tg myTarget) {
	bin := "mysql"
	if tg.Kind == engine.KindMariaDB {
		bin = "mariadb"
	}
	shell := func(n int) string {
		return fmt.Sprintf("echo bekup-$(printf pwned) $(id) >&2; %s --defaults-file=%s --protocol=TCP --host=%s --user=root --skip-ssl -e 'CREATE DATABASE pwned_%d'",
			bin, engine.FileMyCnf, tg.Host, n)
	}
	cases := []struct{ name, line string }{
		{"bang", `\! ` + shell(1)},
		{"system", "system " + shell(2)},
		{"source", "source /etc/passwd"},
		{"source_short", `\. /etc/passwd`},
		{"load_data_local", "LOAD DATA LOCAL INFILE '/etc/passwd' INTO TABLE loot;"},
		{"tee", "tee /tmp/bekup-tee"},
		{"tee_short", `\T /tmp/bekup-tee`},
		{"pager", "pager id >&2\nSELECT 1;"},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := fmt.Sprintf("hostile_%d", i)
			dump := "CREATE TABLE loot (l TEXT);\n" + c.line + "\nCREATE TABLE after_hostile (id INT);\n-- Dump completed on 2026-10-28  8:00:00\n"
			_, sink, err := in.restore(t, tg, tg.Kind, db, restore.ModeNewDB, restore.KindDatabase, []byte(dump))
			logs := sink.text()
			if strings.Contains(logs, "bekup-pwned") || strings.Contains(logs, "uid=") || strings.Contains(logs, "root:x:0") {
				t.Fatalf("the hostile command ran:\n%s", logs)
			}
			if out := in.sql(t, tg, "", "SHOW DATABASES LIKE 'pwned%'"); out != "" {
				t.Fatalf("a shell command created %q", out)
			}
			if n := in.sql(t, tg, db, "SELECT COUNT(*) FROM loot"); n != "0" {
				t.Fatalf("loot has %s rows", n)
			}
			after := in.sql(t, tg, db, "SHOW TABLES LIKE 'after_hostile'")
			switch {
			case c.name == "pager" && tg.Kind == engine.KindMariaDB:
				// MariaDB's batch mode ignores pager (no pager is ever
				// started); the dump continues.
				if err != nil || after == "" {
					t.Fatalf("pager: %v %q\n%s", err, after, logs)
				}
			default:
				if !errors.Is(err, restore.ErrRestoreFailed) || after != "" {
					t.Fatalf("not refused: %v (after_hostile %q)\n%s", err, after, logs)
				}
			}
			t.Logf("%s: %v", c.name, err)
		})
	}
}
