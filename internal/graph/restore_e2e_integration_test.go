//go:build integration

package graph_test

// restoreArtifact end to end (task 4.5 coverage): a real `bekup runner`
// and PostgreSQL 17 target (the task 2.11 connection harness), a real dump
// of the target's "app" database seeded as a signed, encrypted backup, and
// the full serve stack in this process: password login, TOTP enrollment,
// a two-step login with a TOTP code (the fresh AAL2 that restores need),
// the restoreArtifact mutation (NEW_DB), the River job it enqueued worked
// by the restore worker against the real runner, and the restored row
// count read back from the target.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/auth/passkey"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/graph"
	"github.com/jlentink/bekup/internal/graph/streams"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/restore"
	"github.com/jlentink/bekup/internal/restore/restoretest"
	"github.com/jlentink/bekup/internal/runnerclient"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/testutil/runnertest"
)

const e2eRows = 321

type e2eEnv struct {
	*mfaEnv
	h      *crHarness
	rc     *runnerclient.Client
	conns  *connections.Service
	worker *restore.Worker
	conn   uuid.UUID
	art    uuid.UUID
	reg    *logging.Registry
}

func newRestoreE2EEnv(t *testing.T, h *crHarness) *e2eEnv {
	t.Helper()
	d := dbtest.Migrated(t)
	me := &mfaEnv{authEnv: &authEnv{t: t, d: d, pool: d.AppPool(t)}, clock: &mfaClock{}}
	e := &e2eEnv{mfaEnv: me, h: h, reg: logging.NewRegistry()}
	pool := me.pool
	me.auditor = audit.New(pool, audit.Options{Registry: e.reg})
	var err error
	me.hasher, err = auth.NewHasher(auth.HasherOptions{
		Params: auth.Argon2Params{MemoryKiB: 64, Iterations: 1, Parallelism: 1}, MaxConcurrent: 4, WeakForTests: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.DiscardHandler)
	lockout := auth.LockoutPolicy{Threshold: 3, Base: time.Minute, Max: time.Hour}
	bus := pubsub.NewBus()
	pub := &pubsub.Publisher{Bus: bus}
	sessions := auth.NewSessions(pool, auth.SessionOptions{Events: auth.PubSubSessionEvents{Publisher: pub}})
	factors := auth.NewSecondFactors()
	svc, err := auth.NewService(auth.ServiceOptions{
		DB: pool, Hasher: me.hasher, Sessions: sessions, Auditor: me.auditor, Factors: factors, Lockout: lockout,
		LoginLimiter: func(netip.Addr) (bool, time.Duration) { return true, 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	me.policy, err = auth.NewTwoFactorPolicies(auth.TwoFactorPoliciesOptions{DB: pool, Auditor: me.auditor, Factors: factors})
	if err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kr.Destroy)
	tsvc, err := auth.NewTOTP(auth.TOTPOptions{
		DB: pool, Keyring: kr, Auditor: me.auditor, Factors: factors, RemovalGuard: me.policy, Lockout: lockout, Now: me.clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	factors.Register(tsvc)
	pk, err := passkey.New(passkey.Options{DB: pool, Auditor: me.auditor, BaseURL: testBaseURL, Factors: factors, RemovalGuard: me.policy})
	if err != nil {
		t.Fatal(err)
	}
	factors.Register(pk)
	flow, err := auth.NewSecondFactorFlow(auth.SecondFactorFlowOptions{
		DB: pool, Sessions: sessions, Auditor: me.auditor, Service: svc, TOTP: tsvc, Passkeys: pk, Factors: factors,
	})
	if err != nil {
		t.Fatal(err)
	}
	pol := authz.New(authz.Options{Ownership: jobs.Ownership{DB: pool}, SecondFactors: factors})
	keys, err := jobkeys.New(jobkeys.Options{DB: pool, Keyring: kr, Auditor: me.auditor, Authorizer: pol, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}

	// The real runner.
	tlsCfg, err := certs.ClientTLSConfig(h.pki.CertFile(certs.ComponentServe), h.pki.KeyFile(certs.ComponentServe), h.pki.CACert(), "runner")
	if err != nil {
		t.Fatal(err)
	}
	e.rc, err = runnerclient.NewWithTLS(h.runnerAddr, tlsCfg, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { crIgnore(e.rc.Close()) })
	runnertest.WaitDockerOK(t, e.rc, 0, nil)
	e.conns, err = connections.New(connections.Options{
		DB: pool, Keyring: kr, Auditor: me.auditor, Runner: e.rc.RPC(), OperationTimeout: crRunnerTO,
		Limiter: func(string) (bool, time.Duration) { return true, 0 }, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}

	// A disk destination and the restore service, API and worker.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "local"), 0o700); err != nil {
		t.Fatal(err)
	}
	destID := uuid.New()
	if _, err := pool.Exec(t.Context(), `INSERT INTO destinations (id, name, type, config) VALUES ($1, 'Local disk', 'disk', '{"path":"x"}')`, destID); err != nil {
		t.Fatal(err)
	}
	op := diskOpener{id: destID, root: root}
	disk, err := op.Open(t.Context(), destID)
	if err != nil {
		t.Fatal(err)
	}
	rsvc, err := restore.New(restore.Options{
		DB: pool, Keyring: kr, Auditor: me.auditor, Destinations: op, Keys: keys,
		ScratchDir: filepath.Join(t.TempDir(), "scratch"), ScratchReserve: 0, Publisher: pub, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	qc, err := queue.New(pool, queue.Options{Settings: queue.SettingsFromConfig(config.DefaultBekup().Workers), Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	api, err := restore.NewAPI(restore.APIOptions{
		Service: rsvc, Policy: pol, Connections: e.conns, Owners: jobs.Ownership{DB: pool},
		Queue: func() restore.Inserter { return qc }, Canceler: func() restore.Canceler { return qc }, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.worker, err = restore.NewWorker(restore.WorkerOptions{
		Service: rsvc, Slots: runstate.NewSlots(pool), Targets: e.conns, Runner: e.rc.RPC(),
		Limits:      runstate.SlotLimits{Global: 4, PerConnection: 2},
		LogMaxLines: 10000, LogMaxLineBytes: 4096, Registry: e.reg, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}

	resolver := httpserver.SessionsResolver{Sessions: sessions, Enrollment: me.policy}
	hub, err := streams.NewHub(streams.Options{Validator: resolver, Bus: bus, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	gql, err := graph.New(graph.Options{
		Logger: logger, MaxComplexity: 5000, MaxDepth: 12, MaxBodyBytes: 1 << 16,
		Access: graph.NewPolicyAccess(pol), Policy: pol, AuditLog: me.auditor, Auth: svc, TOTP: tsvc, Passkeys: pk,
		SecondFactor: flow, TwoFactorPolicy: me.policy, Restores: api, Bus: bus,
		Streams: &graph.StreamOptions{Hub: hub, AllowedOrigins: []string{testBaseURL}},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := httpserver.New(httpserver.Options{
		BaseURL: testBaseURL, MaxBodyBytes: 1 << 20, Logger: logger,
		Ready:     func(context.Context) error { return nil },
		RateLimit: httpserver.RateLimitOptions{PerSecond: 1000, Burst: 1000, MaxClients: 100},
		GraphQL:   gql,
		Sessions:  &httpserver.SessionOptions{Resolver: resolver, Cookies: auth.CookiePolicyFor(testBaseURL)},
	})
	if err != nil {
		t.Fatal(err)
	}
	me.h = srv.Handler()
	ts := httptest.NewServer(me.h)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := hub.Run(ctx); err != nil {
			t.Error(err)
		}
	}()
	t.Cleanup(func() { cancel(); <-done; ts.Close() })

	// The real target connection (TLS without a pin: the pin flow has its
	// own test) and a job backing up its "app" database.
	seeder := e.user("seed@example.com", sqlc.UserRoleAdmin)
	actx := auth.WithPrincipal(t.Context(), auth.Principal{UserID: seeder.ID, Role: auth.RoleAdmin, SessionID: uuid.New(), AAL: 2,
		StepUpAt: time.Now(), AAL2At: time.Now()})
	cn, err := e.conns.Create(actx, connections.Actor{UserID: seeder.ID}, connections.CreateInput{
		Name: "pg17", Engine: engine.KindPostgres, VersionKey: "17", Host: crPGHost, Port: 5432, Username: crPGUser,
		Password: []byte(crDBPass), TLSMode: connections.TLSUnverified,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.conn = cn.ID
	job, err := dbtest.InsertJob(t.Context(), pool, dbtest.JobRow{Enabled: true, ConnectionID: e.conn, DestinationIDs: []uuid.UUID{destID}})
	if err != nil {
		t.Fatal(err)
	}
	var slug string
	if err := pool.QueryRow(t.Context(), `UPDATE jobs SET db_mode = 'selected', databases = '{app}' WHERE id = $1 RETURNING slug`, job).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	err = db.Tx(actx, pool, func(q *sqlc.Queries) error {
		res, err := keys.InitTx(actx, q, jobkeys.Actor{UserID: seeder.ID}, job, jobkeys.Init{Mode: jobkeys.ModePassphrase})
		if err == nil {
			res.Destroy()
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	h.exec(h.pg, "psql", "-X", "-v", "ON_ERROR_STOP=1", "-U", crPGUser, "-d", "app", "-c",
		"CREATE TABLE items (id int PRIMARY KEY, name text NOT NULL); INSERT INTO items SELECT g, 'item ' || g FROM generate_series(1, "+
			strconv.Itoa(e2eRows)+") g;")
	dump := e.dump("app")
	s, err := restoretest.Seed(t.Context(), pool, kr, keys, job, slug, destID, disk, dump, time.Now(),
		restoretest.Spec{Format: pipeline.FormatCustom, Engine: "postgres", VersionKey: "17"})
	if err != nil {
		t.Fatal(err)
	}
	e.art = s.Artifact
	return e
}

// dump takes a real dump of db on the target through the runner.
func (e *e2eEnv) dump(dbName string) []byte {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(e.t.Context(), 5*time.Minute)
	defer cancel()
	target, release, err := e.conns.Target(ctx, e.conn)
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

// psql runs a query on the target and returns the trimmed output.
func (e *e2eEnv) psql(dbName, query string) string {
	e.t.Helper()
	return strings.TrimSpace(e.h.exec(e.h.pg, "psql", "-X", "-U", crPGUser, "-d", dbName, "-tAc", query))
}

func TestRestoreArtifactEndToEnd(t *testing.T) {
	h := startConnHarness(t)
	e := newRestoreE2EEnv(t, h)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)

	// Password login, TOTP enrollment, then the two-step login with a
	// TOTP code: the session's fresh AAL2 comes from the real factor.
	_, cookie := e.mfaLogin(admin.Email)
	key, _ := e.enrollTOTP(cookie)
	out, pending := e.mfaLogin(admin.Email)
	if !out.SecondFactorRequired {
		t.Fatal("login with TOTP enrolled needs a second step")
	}
	r, full := e.complete(pending, map[string]any{"m": "TOTP", "c": e.code(key)})
	if len(r.Errors) > 0 || full == "" || full == "-" {
		t.Fatalf("complete second factor: %+v cookie %q", r.Errors, full)
	}
	if s := e.latestSession(admin.ID); s.Aal2At == nil {
		t.Fatal("the TOTP login left no second-factor time")
	}

	in := map[string]any{"in": map[string]any{
		"artifactId": e.art, "targetConnectionId": e.conn, "mode": "NEW_DB", "targetDatabase": "restored",
		// The harness role is a superuser (D-068): an admin acknowledges it.
		"acknowledgePrivilegedTarget": true,
	}}
	r, _ = e.do(full, "", restoreArtMut, in)
	if len(r.Errors) > 0 {
		t.Fatalf("restoreArtifact: %+v", r.Errors)
	}
	var created struct{ ID, Status, Mode, TargetDatabase string }
	r.field(t, "restoreArtifact", &created)
	if created.Status != "QUEUED" || created.Mode != "NEW_DB" || created.TargetDatabase != "restored" {
		t.Fatalf("restore %+v", created)
	}
	id := uuid.MustParse(created.ID)

	// The worker runs the River job the mutation enqueued.
	var raw []byte
	if err := e.pool.QueryRow(t.Context(), `SELECT args FROM river_job WHERE kind = $1 ORDER BY id DESC LIMIT 1`, queue.KindRestore).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var args queue.RestoreArgs
	if err := json.Unmarshal(raw, &args); err != nil || args.RestoreID != id {
		t.Fatalf("enqueued args %s (%v), want restore %s", raw, err, id)
	}
	wctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	if err := e.worker.Work(wctx, &river.Job[queue.RestoreArgs]{JobRow: &rivertype.JobRow{ID: 1, Attempt: 1}, Args: args}); err != nil {
		t.Fatal(err)
	}

	r, _ = e.do(full, "", `query($id: UUID!) { restore(id: $id) { id status errorCode } }`, map[string]any{"id": id})
	var got struct {
		Status    string
		ErrorCode *string
	}
	r.field(t, "restore", &got)
	if got.Status != "SUCCEEDED" {
		r, _ = e.do(full, "", restoreLogsQ, map[string]any{"id": id})
		t.Fatalf("restore %+v, logs %s", got, r.Data["restoreLogs"])
	}
	if n := e.psql("restored", "SELECT count(*) FROM items"); n != strconv.Itoa(e2eRows) {
		t.Fatalf("restored rows = %s, want %d", n, e2eRows)
	}
	if n := e.psql("app", "SELECT count(*) FROM items"); n != strconv.Itoa(e2eRows) {
		t.Fatalf("source rows changed: %s", n)
	}
	var leaked int
	if err := e.pool.QueryRow(t.Context(), `SELECT count(*) FROM restore_logs WHERE line LIKE '%' || $1 || '%'`, crDBPass).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatalf("%d restore log lines carry the password", leaked)
	}
}
