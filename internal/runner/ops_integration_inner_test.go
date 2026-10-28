//go:build integration

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/executor"
	"github.com/jlentink/bekup/internal/runnerpb"
)

type itInner struct {
	t   *testing.T
	h   itHarness
	rpc runnerpb.RunnerServiceClient
	dc  *client.Client
	seq atomic.Int32
}

func (in *itInner) runID() string {
	return fmt.Sprintf("7b2e4d6f-0000-4000-8000-%012d", in.seq.Add(1))
}

func (in *itInner) ctx(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), d)
	t.Cleanup(cancel)
	return ctx
}

func engineOf(k engine.Kind) runnerpb.Engine {
	switch k {
	case engine.KindPostgres:
		return runnerpb.Engine_ENGINE_POSTGRES
	case engine.KindMySQL:
		return runnerpb.Engine_ENGINE_MYSQL
	default:
		return runnerpb.Engine_ENGINE_MARIADB
	}
}

// target returns the request target of a harness database with its
// configured TLS mode.
func (in *itInner) target(name string) *runnerpb.Target {
	tg := in.h.Targets[name]
	user := "root"
	if tg.Kind == engine.KindPostgres {
		user = itPGUser
	}
	rt := &runnerpb.Target{
		Engine: engineOf(tg.Kind), VersionKey: tg.Key, Host: tg.Host, Port: uint32(tg.Port), //nolint:gosec // test ports
		User: user, Password: []byte(in.h.Password),
	}
	switch tg.TLS {
	case "pinned":
		rt.TlsMode = runnerpb.TlsMode_TLS_MODE_PINNED
		rt.Pins = []*runnerpb.CertificatePin{{AnchorDer: in.h.CADER, LeafSpkiSha256: in.h.LeafSPKI}}
	case "unverified":
		rt.TlsMode = runnerpb.TlsMode_TLS_MODE_UNVERIFIED
	default:
		rt.TlsMode = runnerpb.TlsMode_TLS_MODE_DISABLED
	}
	return rt
}

func (in *itInner) rc() *runnerpb.RunContext { return &runnerpb.RunContext{RunId: in.runID()} }

// sql runs a statement inside a target container (test seeding/checks).
func (in *itInner) sql(t *testing.T, name, db, stmt string) string {
	t.Helper()
	tg := in.h.Targets[name]
	switch tg.Kind {
	case engine.KindPostgres:
		return strings.TrimSpace(itExec(t, in.dc, tg.Container, "psql", "-U", itPGUser, "-d", db, "-At", "-v", "ON_ERROR_STOP=1", "-c", stmt))
	case engine.KindMySQL:
		return strings.TrimSpace(itExec(t, in.dc, tg.Container, "mysql", "-uroot", "-p"+in.h.Password, "-N", "-D", db, "-e", stmt))
	default:
		return strings.TrimSpace(itExec(t, in.dc, tg.Container, "mariadb", "-uroot", "-p"+in.h.Password, "-N", "-D", db, "-e", stmt))
	}
}

func (in *itInner) checksum(t *testing.T, name, db string) string {
	t.Helper()
	if in.h.Targets[name].Kind == engine.KindPostgres {
		return in.sql(t, name, db, "SELECT count(*)||':'||md5(string_agg(id||'|'||name, ',' ORDER BY id)) FROM t")
	}
	return in.sql(t, name, db, "SELECT CONCAT(COUNT(*),':',SUM(CRC32(CONCAT(id,'|',name)))) FROM t")
}

type dumpOut struct {
	data   []byte
	logs   []string
	result *runnerpb.DumpResult
}

func (in *itInner) dump(t *testing.T, rt *runnerpb.Target, db string) (dumpOut, error) {
	t.Helper()
	var out dumpOut
	s, err := in.rpc.Dump(in.ctx(t, 5*time.Minute), &runnerpb.DumpRequest{Ctx: in.rc(), Target: rt, Database: db})
	if err != nil {
		return out, err
	}
	for {
		m, err := s.Recv()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		switch {
		case m.GetChunk() != nil:
			if out.result != nil {
				t.Fatal("data after the result")
			}
			out.data = append(out.data, m.GetChunk().GetData()...)
		case m.GetLog() != nil:
			out.logs = append(out.logs, m.GetLog().GetText())
		case m.GetResult() != nil:
			out.result = m.GetResult()
		}
	}
}

func (in *itInner) dumpGlobals(t *testing.T, rt *runnerpb.Target) (dumpOut, error) {
	t.Helper()
	var out dumpOut
	s, err := in.rpc.DumpGlobals(in.ctx(t, 5*time.Minute), &runnerpb.DumpGlobalsRequest{Ctx: in.rc(), Target: rt})
	if err != nil {
		return out, err
	}
	for {
		m, err := s.Recv()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		switch {
		case m.GetChunk() != nil:
			out.data = append(out.data, m.GetChunk().GetData()...)
		case m.GetLog() != nil:
			out.logs = append(out.logs, m.GetLog().GetText())
		case m.GetResult() != nil:
			out.result = m.GetResult()
		}
	}
}

func chunksOf(data []byte) [][]byte {
	var out [][]byte
	for len(data) > 0 {
		n := min(len(data), runnerpb.MaxChunkBytes)
		out = append(out, data[:n])
		data = data[n:]
	}
	return out
}

func (in *itInner) verify(t *testing.T, kind engine.Kind, key string, dk runnerpb.DumpKind, data []byte) (*runnerpb.VerifyDumpResponse, error) {
	t.Helper()
	s, err := in.rpc.VerifyDump(in.ctx(t, 5*time.Minute))
	if err != nil {
		return nil, err
	}
	if err := s.Send(&runnerpb.VerifyDumpRequest{Msg: &runnerpb.VerifyDumpRequest_Header{Header: &runnerpb.VerifyDumpHeader{
		Ctx: in.rc(), Engine: engineOf(kind), VersionKey: key, Kind: dk, TotalBytes: uint64(len(data)),
	}}}); err != nil {
		return nil, err
	}
	for _, c := range chunksOf(data) {
		if err := s.Send(&runnerpb.VerifyDumpRequest{Msg: &runnerpb.VerifyDumpRequest_Chunk{Chunk: &runnerpb.DataChunk{Data: c}}}); err != nil {
			break // the server ended the call; CloseAndRecv reports why
		}
	}
	return s.CloseAndRecv()
}

func (in *itInner) restore(t *testing.T, rt *runnerpb.Target, db, src string, dk runnerpb.DumpKind, data []byte) (*runnerpb.RestoreResult, []string, error) {
	t.Helper()
	return in.restoreHeader(t, &runnerpb.RestoreHeader{
		Ctx: in.rc(), Target: rt, Database: db, SourceVersionKey: src, Kind: dk, TotalBytes: uint64(len(data)),
	}, data)
}

// restoreHeader runs a Restore with the given header.
func (in *itInner) restoreHeader(t *testing.T, h *runnerpb.RestoreHeader, data []byte) (*runnerpb.RestoreResult, []string, error) {
	t.Helper()
	s, err := in.rpc.Restore(in.ctx(t, 5*time.Minute))
	if err != nil {
		return nil, nil, err
	}
	if err := s.Send(&runnerpb.RestoreRequest{Msg: &runnerpb.RestoreRequest_Header{Header: h}}); err != nil {
		return nil, nil, err
	}
	go func() {
		for _, c := range chunksOf(data) {
			if s.Send(&runnerpb.RestoreRequest{Msg: &runnerpb.RestoreRequest_Chunk{Chunk: &runnerpb.DataChunk{Data: c}}}) != nil {
				return
			}
		}
		itIgnore(s.CloseSend())
	}()
	var logs []string
	for {
		m, err := s.Recv()
		if err != nil {
			return nil, logs, err
		}
		if l := m.GetLog(); l != nil {
			logs = append(logs, l.GetText())
		}
		if r := m.GetResult(); r != nil {
			return r, logs, nil
		}
	}
}

func (in *itInner) probe(t *testing.T, rt *runnerpb.Target) (*runnerpb.ProbeResponse, error) {
	t.Helper()
	return in.rpc.Probe(in.ctx(t, 2*time.Minute), &runnerpb.ProbeRequest{Ctx: in.rc(), Target: rt})
}

func (in *itInner) create(t *testing.T, rt *runnerpb.Target, db string) error {
	t.Helper()
	_, err := in.rpc.CreateDatabase(in.ctx(t, 3*time.Minute), &runnerpb.CreateDatabaseRequest{Ctx: in.rc(), Target: rt, Database: db})
	return err
}

func itWantReason(t *testing.T, err error, reason runnerpb.Reason) {
	t.Helper()
	if got := runnerpb.ErrorReason(err); got != reason {
		t.Fatalf("reason %q (%v), want %q", got, err, reason)
	}
}

func TestInnerRunnerRPCs(t *testing.T) {
	path := os.Getenv(itInnerEnv)
	if path == "" {
		t.Skip("runs inside the harness container only")
	}
	raw, err := os.ReadFile(path) //nolint:gosec // harness file
	if err != nil {
		t.Fatal(err)
	}
	var h itHarness
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatal(err)
	}
	dc, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { itIgnore(dc.Close()) }()
	docker, closer, err := executor.NewMobyDocker("unix:///var/run/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { itIgnore(closer.Close()) }()
	ts := startServer(t, func(o *Options) {
		o.Config.Targets.AllowCIDRs = []string{h.TargetsSubnet}
		o.DockerClient = docker
	})
	c := ts.client(t, ts.pki.clientTLS(t, certs.ComponentServe))
	in := &itInner{t: t, h: h, rpc: c.RPC(), dc: dc}

	// Wait until the executor detected its own container.
	deadline := time.Now().Add(90 * time.Second)
	for {
		hr, err := c.Health(in.ctx(t, 10*time.Second))
		if err == nil && hr.GetDockerStatus() == runnerpb.DockerStatus_DOCKER_STATUS_OK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("executor not ready: %v %v\n%s", hr, err, ts.logs.String())
		}
		time.Sleep(time.Second)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("runner log:\n%s", ts.logs.String())
		}
		if strings.Contains(ts.logs.String(), h.Password) {
			t.Error("the runner log contains the database password")
		}
	})

	in.seed(t)
	t.Run("probe", in.testProbe)
	t.Run("privileges", in.testPrivileges)
	t.Run("compat", in.testCompat)
	t.Run("errors", in.testErrors)
	t.Run("roundtrip", in.testRoundTrip)
	t.Run("globals", in.testGlobals)
	t.Run("stats", in.testStats)
	t.Run("hostile", in.testHostile)
	t.Run("psql_restrict", in.testPSQLRestrict)
	t.Run("ssh", in.testSSH)
	t.Run("swap", in.testSwap)
	t.Run("secdef", in.testSecdef)
	t.Run("cancel", in.testCancel)
	t.Run("prepull", in.testPrePull)
	if !strings.Contains(ts.logs.String(), "runner_rpc_audit") {
		t.Error("no audit lines in the runner log")
	}
}

func (in *itInner) seed(t *testing.T) {
	pw := strings.ReplaceAll(in.h.Password, "'", "")
	in.sql(t, "pg16", "app", "CREATE TABLE t(id int PRIMARY KEY, name text NOT NULL); "+
		"INSERT INTO t SELECT g, md5(g::text) FROM generate_series(1, 5000) g; "+
		"CREATE VIEW v AS SELECT count(*) FROM t")
	in.sql(t, "pg16", "app", "CREATE ROLE backupro LOGIN PASSWORD '"+pw+"'; CREATE ROLE noconn LOGIN PASSWORD '"+pw+"'")
	in.sql(t, "pg16", "app", "REVOKE CONNECT ON DATABASE postgres FROM PUBLIC; GRANT CONNECT ON DATABASE postgres TO backupro")
	in.sql(t, "pg16", "postgres", "CREATE DATABASE bigdb")
	in.sql(t, "pg16", "bigdb", "CREATE TABLE big AS SELECT g AS id, repeat(md5(g::text), 16) AS name FROM generate_series(1, 400000) g")
	for _, name := range []string{"mysql84", "maria118"} {
		in.sql(t, name, "shop", "CREATE TABLE t (id INT PRIMARY KEY, name VARCHAR(64) NOT NULL) ENGINE=InnoDB; "+
			"INSERT INTO t SELECT n, MD5(n) FROM (WITH RECURSIVE s(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM s WHERE n < 900) SELECT n FROM s) x; "+
			"CREATE TABLE m (id INT) ENGINE=MyISAM; INSERT INTO m VALUES (1), (2)")
	}
}

func (in *itInner) testProbe(t *testing.T) {
	for name := range in.h.Targets {
		tg := in.h.Targets[name]
		t.Run(name, func(t *testing.T) {
			resp, err := in.probe(t, in.target(name))
			if err != nil {
				t.Fatal(err)
			}
			if resp.GetServerVersion() == "" || resp.GetDetectedVersionKey() != tg.Key || !resp.GetVersionKeyCompatible() {
				t.Fatalf("resp = %v", resp)
			}
			want, system := "shop", "mysql"
			if tg.Kind == engine.KindPostgres {
				want, system = "app", "template1"
			}
			if !slices.Contains(resp.GetDatabases(), want) || slices.Contains(resp.GetDatabases(), system) {
				t.Fatalf("databases = %v", resp.GetDatabases())
			}
		})
	}
}

// testCompat: pg_dump 17 must not dump a PostgreSQL 18 server.
func (in *itInner) testCompat(t *testing.T) {
	rt := in.target("pg18")
	rt.VersionKey = "17"
	resp, err := in.probe(t, rt)
	if err != nil || resp.GetVersionKeyCompatible() || resp.GetDetectedVersionKey() != "18" {
		t.Fatalf("probe: %v %v", resp, err)
	}
	rt = in.target("pg18")
	rt.VersionKey = "17"
	_, err = in.dump(t, rt, "app")
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("dump: %v", err)
	}
	itWantReason(t, err, runnerpb.ReasonUnsupportedServer)
}

func (in *itInner) testErrors(t *testing.T) {
	subnet := netip.MustParsePrefix(in.h.TargetsSubnet)
	unused := subnet.Addr().Next()
	for range 200 {
		unused = unused.Next()
	}
	tests := []struct {
		name   string
		mut    func(*runnerpb.Target)
		reason runnerpb.Reason
	}{
		{"wrong password", func(rt *runnerpb.Target) { rt.Password = []byte("wrong-password") }, runnerpb.ReasonAuthFailed},
		{"closed port", func(rt *runnerpb.Target) { rt.Port = 5999 }, runnerpb.ReasonUnreachable},
		{"not a database", func(rt *runnerpb.Target) {
			rt.Host, rt.Port, rt.TlsMode, rt.Pins = in.h.BastionHost, 2222, runnerpb.TlsMode_TLS_MODE_DISABLED, nil
		}, runnerpb.ReasonUnreachable},
		{"not a database pinned", func(rt *runnerpb.Target) { rt.Host, rt.Port = in.h.BastionHost, 2222 }, runnerpb.ReasonUnreachable},
		{"filtered", func(rt *runnerpb.Target) {
			rt.Host, rt.TlsMode, rt.Pins = unused.String(), runnerpb.TlsMode_TLS_MODE_DISABLED, nil
		}, runnerpb.ReasonUnreachable},
		{"pin mismatch", func(rt *runnerpb.Target) {
			rt.Pins = []*runnerpb.CertificatePin{{AnchorDer: in.h.OtherCADER}}
		}, runnerpb.ReasonPinMismatch},
		{"permission", func(rt *runnerpb.Target) { rt.User = "noconn" }, runnerpb.ReasonPermission},
	}
	msgs := map[runnerpb.Reason]string{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := in.target("pg16")
			tt.mut(rt)
			_, err := in.probe(t, rt)
			itWantReason(t, err, tt.reason)
			m := status.Convert(err).Message()
			if prev, ok := msgs[tt.reason]; ok && prev != m {
				t.Fatalf("two messages for %s: %q / %q", tt.reason, prev, m)
			}
			msgs[tt.reason] = m
		})
	}
	t.Run("tls refused", func(t *testing.T) {
		rt := in.target("maria1011")
		rt.TlsMode = runnerpb.TlsMode_TLS_MODE_UNVERIFIED
		_, err := in.probe(t, rt)
		itWantReason(t, err, runnerpb.ReasonTLSFailed)
	})
	t.Run("missing database", func(t *testing.T) {
		_, err := in.dump(t, in.target("pg16"), "nope")
		itWantReason(t, err, runnerpb.ReasonDatabaseNotFound)
	})
}

// testRoundTrip: Dump -> VerifyDump -> CreateDatabase -> Restore -> equal
// data, per engine.
func (in *itInner) testRoundTrip(t *testing.T) {
	for _, tc := range []struct{ name, db string }{{"pg16", "app"}, {"mysql84", "shop"}, {"maria118", "shop"}} {
		t.Run(tc.name, func(t *testing.T) {
			tg := in.h.Targets[tc.name]
			d, err := in.dump(t, in.target(tc.name), tc.db)
			if err != nil {
				t.Fatal(err)
			}
			res := d.result
			if res == nil || res.GetExitCode() != 0 || res.GetBytes() != uint64(len(d.data)) || len(d.data) == 0 {
				t.Fatalf("dump result %v (%d bytes) logs %v", res, len(d.data), d.logs)
			}
			if tg.Kind != engine.KindPostgres {
				if !res.GetVerified() || !slices.Contains(res.GetWarnings(), warnNonInnoDB) {
					t.Fatalf("mysql result %v", res)
				}
			}
			v, err := in.verify(t, tg.Kind, tg.Key, runnerpb.DumpKind_DUMP_KIND_DATABASE, d.data)
			if err != nil || !v.GetVerified() {
				t.Fatalf("verify: %v %v", v, err)
			}
			bad, err := in.verify(t, tg.Kind, tg.Key, runnerpb.DumpKind_DUMP_KIND_DATABASE, d.data[:len(d.data)/2])
			if err != nil || bad.GetVerified() {
				t.Fatalf("truncated dump verified: %v %v", bad, err)
			}
			if err := in.create(t, in.target(tc.name), "restored"); err != nil {
				t.Fatal(err)
			}
			err = in.create(t, in.target(tc.name), "restored")
			itWantReason(t, err, runnerpb.ReasonDatabaseExists)
			r, logs, err := in.restore(t, in.target(tc.name), "restored", tg.Key, runnerpb.DumpKind_DUMP_KIND_DATABASE, d.data)
			if err != nil || r.GetExitCode() != 0 || r.GetBytesReceived() != uint64(len(d.data)) {
				t.Fatalf("restore: %v %v logs %v", r, err, logs)
			}
			if a, b := in.checksum(t, tc.name, tc.db), in.checksum(t, tc.name, "restored"); a != b || a == "" {
				t.Fatalf("data differs: %q vs %q", a, b)
			}
		})
	}
}

func (in *itInner) testGlobals(t *testing.T) {
	rt := in.target("pg16")
	rt.User = "backupro"
	d, err := in.dumpGlobals(t, rt)
	if err != nil {
		t.Fatal(err)
	}
	if r := d.result; r == nil || r.GetExitCode() != 0 || !r.GetRolesWithoutPasswords() || !r.GetVerified() ||
		!strings.Contains(string(d.data), "CREATE ROLE backupro") {
		t.Fatalf("non-superuser globals: %v logs %v", d.result, d.logs)
	}
	v, err := in.verify(t, engine.KindPostgres, "16", runnerpb.DumpKind_DUMP_KIND_GLOBALS, d.data)
	if err != nil || !v.GetVerified() {
		t.Fatalf("verify globals: %v %v", v, err)
	}
	d, err = in.dumpGlobals(t, in.target("pg16"))
	if err != nil || d.result.GetRolesWithoutPasswords() || !d.result.GetVerified() {
		t.Fatalf("superuser globals: %v %v", d.result, err)
	}
	_, err = in.dumpGlobals(t, in.target("mysql84"))
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("mysql globals: %v", err)
	}
}

// testPSQLRestrict: every pinned psql client (13-18, chosen through the
// restore client selection against the pg13 server) runs under the
// runner's \restrict key: a hostile globals script cannot run a shell
// command (\!, \o |cmd, \copy ... program) or lift the restriction,
// while a pg_dumpall-style script with its own \restrict lines restores.
func (in *itInner) testPSQLRestrict(t *testing.T) {
	const marker = "BEKUP_PWNED_42"
	hostile := map[string]string{
		"shell":        "SELECT 1;\n\\! echo BEKUP_PWNED_$((6*7)) >&2\n",
		"pipe output":  "\\o |echo BEKUP_PWNED_$((6*7)) >&2\nSELECT 1;\n",
		"copy program": "\\copy (SELECT 1) TO PROGRAM 'echo BEKUP_PWNED_$((6*7)) >&2'\n",
		"unrestrict":   "\\unrestrict 0123456789abcdef\n\\! echo BEKUP_PWNED_$((6*7)) >&2\n",
	}
	for _, src := range []string{"13", "14", "15", "16", "17", "18"} {
		t.Run("client"+src, func(t *testing.T) {
			for name, script := range hostile {
				r, logs, err := in.restore(t, in.target("pg13"), "", src, runnerpb.DumpKind_DUMP_KIND_GLOBALS, []byte(script))
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				joined := strings.Join(logs, "\n")
				if r.GetExitCode() == 0 || strings.Contains(joined, marker) {
					t.Fatalf("%s: exit %d, logs %q", name, r.GetExitCode(), logs)
				}
				if !strings.Contains(joined, "restricted") && !strings.Contains(joined, "wrong key") {
					t.Fatalf("%s: not refused by the restriction: %q", name, logs)
				}
			}
			benign := "--\n\\restrict Abc123xyz\nSELECT 1;\n\\unrestrict Abc123xyz\n-- PostgreSQL database cluster dump complete\n"
			r, logs, err := in.restore(t, in.target("pg13"), "", src, runnerpb.DumpKind_DUMP_KIND_GLOBALS, []byte(benign))
			if err != nil || r.GetExitCode() != 0 {
				t.Fatalf("pg_dumpall-style script: %v %v %q", r, err, logs)
			}
		})
	}
}

func (in *itInner) tunneled(name string) *runnerpb.Target {
	rt := in.target(name)
	rt.SshTunnel = &runnerpb.SshTunnel{
		Host: in.h.BastionHost, Port: 2222, User: itSSHUser, PrivateKeyPem: []byte(in.h.SSHKeyPEM),
		HostKeyPin: &runnerpb.HostKeyPin{AuthorizedKey: in.h.HostKey},
	}
	return rt
}

func (in *itInner) testSSH(t *testing.T) {
	resp, err := in.probe(t, in.tunneled("pg16"))
	if err != nil || resp.GetDetectedVersionKey() != "16" {
		t.Fatalf("tunneled probe: %v %v", resp, err)
	}
	d, err := in.dump(t, in.tunneled("pg16"), "app")
	if err != nil || d.result.GetExitCode() != 0 {
		t.Fatalf("tunneled dump: %v %v %v", d.result, err, d.logs)
	}
	v, err := in.verify(t, engine.KindPostgres, "16", runnerpb.DumpKind_DUMP_KIND_DATABASE, d.data)
	if err != nil || !v.GetVerified() {
		t.Fatalf("verify tunneled dump: %v %v", v, err)
	}
	rt := in.tunneled("pg16")
	rt.SshTunnel.HostKeyPin = &runnerpb.HostKeyPin{
		Type: "ssh-ed25519", FingerprintSha256: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	}
	_, err = in.probe(t, rt)
	itWantReason(t, err, runnerpb.ReasonSSHHostKey)
}

func (in *itInner) testSwap(t *testing.T) {
	before := in.checksum(t, "pg16", "app")
	d, err := in.dump(t, in.target("pg16"), "app")
	if err != nil || d.result.GetExitCode() != 0 {
		t.Fatalf("dump: %v %v", d.result, err)
	}
	temp := "app__bekup_restore_r1"
	if err := in.create(t, in.target("pg16"), temp); err != nil {
		t.Fatal(err)
	}
	r, logs, err := in.restore(t, in.target("pg16"), temp, "16", runnerpb.DumpKind_DUMP_KIND_DATABASE, d.data)
	if err != nil || r.GetExitCode() != 0 {
		t.Fatalf("restore: %v %v %v", r, err, logs)
	}
	_, err = in.rpc.RenameSwapDatabase(in.ctx(t, 5*time.Minute), &runnerpb.RenameSwapDatabaseRequest{
		Ctx: in.rc(), Target: in.target("pg16"), Database: "app", TempId: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := in.probe(t, in.target("pg16"))
	if err != nil || !slices.Contains(resp.GetDatabases(), "app") || slices.Contains(resp.GetDatabases(), temp) {
		t.Fatalf("after swap: %v %v", resp, err)
	}
	// The old database is kept (connections disallowed, so not listed).
	if n := in.sql(t, "pg16", "postgres", "SELECT count(*) FROM pg_database WHERE datname = 'app__bekup_old_r1'"); n != "1" {
		t.Fatalf("old database kept: %s", n)
	}
	if after := in.checksum(t, "pg16", "app"); after != before {
		t.Fatalf("data after swap %q, want %q", after, before)
	}
	_, err = in.rpc.RenameSwapDatabase(in.ctx(t, time.Minute), &runnerpb.RenameSwapDatabaseRequest{
		Ctx: in.rc(), Target: in.target("mysql84"), Database: "shop", TempId: "r1"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("mysql swap: %v", err)
	}
}

// testCancel: canceling a running dump kills its container.
func (in *itInner) testCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	id := in.runID()
	s, err := in.rpc.Dump(ctx, &runnerpb.DumpRequest{Ctx: &runnerpb.RunContext{RunId: id}, Target: in.target("pg16"), Database: "bigdb"})
	if err != nil {
		t.Fatal(err)
	}
	for {
		m, err := s.Recv()
		if err != nil {
			t.Fatalf("no data before cancel: %v", err)
		}
		if m.GetChunk() != nil {
			break
		}
	}
	// The container must be running now (backpressure keeps pg_dump busy).
	f := client.Filters{}.Add("label", "bekup.run_id="+id)
	cl, err := in.dc.ContainerList(t.Context(), client.ContainerListOptions{All: true, Filters: f})
	if err != nil || len(cl.Items) != 1 {
		t.Fatalf("dump container: %v %v", cl, err)
	}
	cancel()
	for {
		_, err := s.Recv()
		if err == nil {
			continue // already buffered messages
		}
		if status.Code(err) != codes.Canceled {
			t.Fatalf("after cancel: %v", err)
		}
		break
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		cl, err := in.dc.ContainerList(t.Context(), client.ContainerListOptions{All: true, Filters: f})
		if err == nil && len(cl.Items) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("container of the canceled run still exists: %v", cl.Items)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (in *itInner) testPrePull(t *testing.T) {
	resp, err := in.rpc.PrePull(in.ctx(t, 5*time.Minute), &runnerpb.PrePullRequest{Engine: runnerpb.Engine_ENGINE_POSTGRES, VersionKey: "16"})
	if err != nil || !resp.GetAlreadyPresent() || !strings.Contains(resp.GetImageRef(), "@sha256:") {
		t.Fatalf("prepull: %v %v", resp, err)
	}
}
