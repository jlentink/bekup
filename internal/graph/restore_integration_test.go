//go:build integration

package graph_test

// Restores over GraphQL (task 4.5, D-167): target listing and the
// one-runner-call database list, the preview warnings, the mandatory fresh
// passkey/TOTP check on real sessions (a password step-up is not enough,
// no factor means enrollment first), owners' target rules, single-root
// requests, staff-only cancel and admin-only globals, scoped reads and the
// restoreUpdated stream with delivery-time authorization.

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
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
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/storage"
)

// listRunner answers Probe with a fixed database list and the privilege
// verdict of priv (not privileged when unset).
type listRunner struct {
	runnerpb.RunnerServiceClient
	dbs  []string
	priv *runnerPrivileges
}

func (l listRunner) Probe(context.Context, *runnerpb.ProbeRequest, ...grpc.CallOption) (*runnerpb.ProbeResponse, error) {
	return &runnerpb.ProbeResponse{ServerVersion: "17.2", Databases: slices.Clone(l.dbs), Privileges: l.priv.get()}, nil
}

// runnerPrivileges is the verdict the test runner reports (D-068).
type runnerPrivileges struct {
	mu sync.Mutex
	v  *runnerpb.TargetPrivileges
}

func (p *runnerPrivileges) get() *runnerpb.TargetPrivileges {
	if p != nil {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.v != nil {
			return p.v
		}
	}
	return &runnerpb.TargetPrivileges{Status: runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_NOT_PRIVILEGED}
}

func (p *runnerPrivileges) set(v *runnerpb.TargetPrivileges) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.v = v
}

// diskOpener opens the test disk destination.
type diskOpener struct {
	id   uuid.UUID
	root string
}

func (o diskOpener) Open(_ context.Context, id uuid.UUID) (storage.Destination, error) {
	if id != o.id {
		return nil, os.ErrNotExist
	}
	return storage.OpenDisk(storage.DiskConfig{Root: o.root, Dir: "local"})
}

type factorMap struct {
	mu sync.Mutex
	m  map[uuid.UUID]bool
}

func (f *factorMap) HasSecondFactor(_ context.Context, id uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m[id], nil
}

func (f *factorMap) set(id uuid.UUID, v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[id] = v
}

type restoreEnv struct {
	*runEnv
	svc     *restore.Service
	factors *factorMap
	keys    *jobkeys.Service
	kr      *crypto.Keyring
	destID  uuid.UUID
	disk    storage.Destination
	job     uuid.UUID
	conn    uuid.UUID
	art     uuid.UUID
	globals uuid.UUID
	priv    *runnerPrivileges
}

func newRestoreEnv(t *testing.T) *restoreEnv {
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
	bus := pubsub.NewBus()
	pub := &pubsub.Publisher{Bus: bus}
	sessions := auth.NewSessions(e.pool, auth.SessionOptions{Events: auth.PubSubSessionEvents{Publisher: pub}})
	authSvc, err := auth.NewService(auth.ServiceOptions{DB: e.pool, Hasher: e.hasher, Sessions: sessions, Auditor: e.auditor, Factors: auth.NewSecondFactors()})
	if err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kr.Destroy)
	logger := slog.New(slog.DiscardHandler)
	factors := &factorMap{m: map[uuid.UUID]bool{}}
	pol := authz.New(authz.Options{Ownership: jobs.Ownership{DB: e.pool}, SecondFactors: factors})
	keys, err := jobkeys.New(jobkeys.Options{DB: e.pool, Keyring: kr, Auditor: e.auditor, Authorizer: pol, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "local"), 0o700); err != nil {
		t.Fatal(err)
	}
	destID := uuid.New()
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO destinations (id, name, type, config) VALUES ($1, 'Local disk', 'disk', '{"path":"x"}')`, destID); err != nil {
		t.Fatal(err)
	}
	op := diskOpener{id: destID, root: root}
	disk, err := op.Open(t.Context(), destID)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := restore.New(restore.Options{
		DB: e.pool, Keyring: kr, Auditor: e.auditor, Destinations: op, Keys: keys,
		ScratchDir: filepath.Join(t.TempDir(), "scratch"), ScratchReserve: 0, Publisher: pub, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	priv := &runnerPrivileges{}
	conns, err := connections.New(connections.Options{
		DB: e.pool, Keyring: kr, Auditor: e.auditor, Runner: listRunner{dbs: []string{"app", "other"}, priv: priv}, Logger: logger,
		Limiter: func(string) (bool, time.Duration) { return true, 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	qc, err := queue.New(e.pool, queue.Options{Settings: queue.SettingsFromConfig(config.DefaultBekup().Workers), Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	api, err := restore.NewAPI(restore.APIOptions{
		Service: svc, Policy: pol, Connections: conns, Owners: jobs.Ownership{DB: e.pool},
		Queue: func() restore.Inserter { return qc }, Canceler: func() restore.Canceler { return qc }, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver := httpserver.SessionsResolver{Sessions: sessions}
	hub, err := streams.NewHub(streams.Options{Validator: resolver, Bus: bus, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	gql, err := graph.New(graph.Options{
		Logger: logger, MaxComplexity: 5000, MaxDepth: 12, MaxBodyBytes: 1 << 16,
		Access: graph.NewPolicyAccess(pol), Policy: pol, AuditLog: e.auditor, Auth: authSvc,
		Restores: api, Bus: bus, Streams: &graph.StreamOptions{Hub: hub, AllowedOrigins: []string{testBaseURL}},
		Runs: runstate.NewReader(e.pool),
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
	e.h = srv.Handler()
	ts := httptest.NewServer(e.h)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := hub.Run(ctx); err != nil {
			t.Error(err)
		}
	}()
	t.Cleanup(func() { cancel(); <-done; ts.Close() })

	re := &restoreEnv{
		runEnv: &runEnv{destEnv: &destEnv{authEnv: e}, events: bus, url: ts.URL + "/graphql", store: runstate.NewStore(e.pool, pub, nil)},
		svc:    svc, factors: factors, keys: keys, kr: kr, destID: destID, disk: disk, priv: priv,
	}
	// A passphrase job backing up only "app", with a database and a
	// globals artifact.
	if re.job, err = dbtest.InsertJob(t.Context(), e.pool, dbtest.JobRow{Enabled: true, DestinationIDs: []uuid.UUID{destID}}); err != nil {
		t.Fatal(err)
	}
	var slug string
	if err := e.pool.QueryRow(t.Context(), `UPDATE jobs SET db_mode = 'selected', databases = '{app}' WHERE id = $1 RETURNING slug, connection_id`, re.job).Scan(&slug, &re.conn); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), `UPDATE connections SET tls_mode = 'unverified' WHERE id = $1`, re.conn); err != nil {
		t.Fatal(err)
	}
	var seeder uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO users (email, display_name, role) VALUES ('seed@example.com', 'Seed', 'admin') RETURNING id`).Scan(&seeder); err != nil {
		t.Fatal(err)
	}
	sctx := auth.WithPrincipal(t.Context(), auth.Principal{UserID: seeder, Role: auth.RoleAdmin, SessionID: uuid.New(), AAL: 2, StepUpAt: time.Now(), AAL2At: time.Now()})
	err = db.Tx(sctx, e.pool, func(q *sqlc.Queries) error {
		res, err := keys.InitTx(sctx, q, jobkeys.Actor{UserID: seeder}, re.job, jobkeys.Init{Mode: jobkeys.ModePassphrase})
		if err == nil {
			res.Destroy()
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := restoretest.Seed(t.Context(), e.pool, kr, keys, re.job, slug, destID, disk, []byte("-- dump"), time.Now(),
		restoretest.Spec{Format: pipeline.FormatCustom})
	if err != nil {
		t.Fatal(err)
	}
	re.art = s.Artifact
	g, err := restoretest.Seed(t.Context(), e.pool, kr, keys, re.job, slug, destID, disk, []byte("CREATE ROLE r;\n"), time.Now().Add(time.Second),
		restoretest.Spec{Kind: pipeline.KindGlobals})
	if err != nil {
		t.Fatal(err)
	}
	re.globals = g.Artifact
	return re
}

// fresh2FA marks the user's sessions as checked with a passkey (UV) or
// TOTP just now.
func (e *restoreEnv) fresh2FA(id uuid.UUID) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.t.Context(), "UPDATE sessions SET aal = 2, stepup_at = now(), aal2_at = now() WHERE user_id = $1", id); err != nil {
		e.t.Fatal(err)
	}
}

// passwordStepUp is what a password, recovery-code or UP-only passkey
// step-up leaves (D-116): a fresh step-up but no second-factor time.
func (e *restoreEnv) passwordStepUp(id uuid.UUID) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.t.Context(), "UPDATE sessions SET aal = 2, stepup_at = now(), aal2_at = NULL WHERE user_id = $1", id); err != nil {
		e.t.Fatal(err)
	}
}

const (
	restoreFields     = `id status kind mode targetDatabase sourceDatabase keepOwners job { id } target { id temporary } requestedBy { userId } errorCode`
	restoreArtMut     = `mutation($in: RestoreArtifactInput!) { restoreArtifact(input: $in) { ` + restoreFields + ` } }`
	restoreGlobalsMut = `mutation($in: RestoreGlobalsInput!) { restoreGlobals(input: $in) { ` + restoreFields + ` } }`
	restorePreviewQ   = `query($in: RestorePreviewInput!) { restorePreview(input: $in) { mode targetDatabase confirmText warnings { code message } source { engine database needsIdentity } target { id original } } }`
	restoreTargetsQ   = `query($a: UUID!) { restoreTargets(artifactId: $a) { source { engine versionKey kind database } targets { id original temporary newDatabaseAllowed existingRestricted } } }`
	restoreDBsQ       = `query($a: UUID!, $c: UUID!) { restoreTargetDatabases(artifactId: $a, targetConnectionId: $c) { databases warnings target { id } } }`
	restoresQ         = `{ restores { edges { node { id requestedBy { userId } } } } }`
	restoreQ          = `query($id: UUID!) { restore(id: $id) { id status } }`
	restoreLogsQ      = `query($id: UUID!) { restoreLogs(restoreId: $id) { restoreId lines { line } dropped } }`
	cancelRestoreMut  = `mutation($id: UUID!) { cancelRestore(id: $id) { restoreId status } }`
	restoreUpdatedSub = `subscription($id: UUID!) { restoreUpdated(restoreId: $id) { id status } }`
)

func TestRestoreGraphQL(t *testing.T) {
	e := newRestoreEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	maint := e.user("maint@example.com", sqlc.UserRoleMaintainer)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	other := e.user("other@example.com", sqlc.UserRoleUser)
	for _, u := range []sqlc.User{admin, maint, owner, other} {
		e.factors.set(u.ID, true)
	}
	e.own(e.job, owner.ID)
	adminC, maintC := e.mustLogin(admin.Email), e.mustLogin(maint.Email)
	ownerC, otherC := e.mustLogin(owner.Email), e.mustLogin(other.Email)

	// Targets and databases.
	var targets struct {
		Source  struct{ Engine, VersionKey, Kind, Database string }
		Targets []struct {
			ID                                                          string
			Original, Temporary, NewDatabaseAllowed, ExistingRestricted bool
		}
	}
	e.mustGQL(ownerC, restoreTargetsQ, map[string]any{"a": e.art}).field(t, "restoreTargets", &targets)
	if targets.Source.Engine != "POSTGRES" || targets.Source.Database != "app" || len(targets.Targets) != 1 ||
		targets.Targets[0].ID != e.conn.String() || !targets.Targets[0].Original || !targets.Targets[0].ExistingRestricted {
		t.Fatalf("owner targets %+v", targets)
	}
	if r := e.gql(otherC, restoreTargetsQ, map[string]any{"a": e.art}); r.code() != "FORBIDDEN" {
		t.Fatalf("non-owner targets: %+v", r.Errors)
	}
	var dbs struct{ Databases []string }
	e.mustGQL(ownerC, restoreDBsQ, map[string]any{"a": e.art, "c": e.conn}).field(t, "restoreTargetDatabases", &dbs)
	if !slices.Equal(dbs.Databases, []string{"app"}) {
		t.Fatalf("owner databases %v", dbs.Databases)
	}
	e.mustGQL(maintC, restoreDBsQ, map[string]any{"a": e.art, "c": e.conn}).field(t, "restoreTargetDatabases", &dbs)
	if !slices.Equal(dbs.Databases, []string{"app", "other"}) {
		t.Fatalf("maintainer databases %v", dbs.Databases)
	}
	// One runner call per request (D-135): not combinable.
	if r := e.gql(maintC, `query($a: UUID!, $c: UUID!) { a: restoreTargetDatabases(artifactId: $a, targetConnectionId: $c) { databases }
		b: restoreTargetDatabases(artifactId: $a, targetConnectionId: $c) { databases } }`, map[string]any{"a": e.art, "c": e.conn}); r.code() != "VALIDATION" {
		t.Fatalf("aliased database lists: %+v", r.Errors)
	}

	// Preview (no second factor needed).
	var pv struct {
		Mode, TargetDatabase string
		ConfirmText          *string
		Warnings             []struct{ Code, Message string }
	}
	e.mustGQL(ownerC, restorePreviewQ, map[string]any{"in": map[string]any{
		"artifactId": e.art, "targetConnectionId": e.conn, "mode": "OVERWRITE", "targetDatabase": "app",
	}}).field(t, "restorePreview", &pv)
	if pv.ConfirmText == nil || *pv.ConfirmText != "app" || len(pv.Warnings) == 0 || pv.Warnings[0].Code != restore.WarnOverwritePostgres {
		t.Fatalf("preview %+v", pv)
	}
	if r := e.gql(ownerC, restorePreviewQ, map[string]any{"in": map[string]any{
		"artifactId": e.art, "targetConnectionId": e.conn, "mode": "OVERWRITE", "targetDatabase": "other",
	}}); r.code() != "FORBIDDEN" {
		t.Fatalf("owner preview of another database: %+v", r.Errors)
	}

	// The fresh passkey/TOTP check (AC): none -> STEP_UP_REQUIRED, a
	// password step-up is not enough, no factor -> REQUIRES_2FA_ENROLLMENT.
	ownIn := map[string]any{"in": map[string]any{
		"artifactId": e.art, "targetConnectionId": e.conn, "mode": "OVERWRITE", "targetDatabase": "app", "confirmTargetName": "app",
	}}
	if r := e.gql(ownerC, restoreArtMut, ownIn); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("restore without a fresh 2FA check: %+v", r.Errors)
	}
	e.passwordStepUp(owner.ID)
	if r := e.gql(ownerC, restoreArtMut, ownIn); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("restore after a password step-up: %+v", r.Errors)
	}
	e.passwordStepUp(maint.ID)
	maintIn := map[string]any{"in": map[string]any{"artifactId": e.art, "targetConnectionId": e.conn, "mode": "NEW_DB", "targetDatabase": "m1"}}
	if r := e.gql(maintC, restoreArtMut, maintIn); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("maintainer restore after a password step-up: %+v", r.Errors)
	}
	e.factors.set(owner.ID, false)
	if r := e.gql(ownerC, restoreArtMut, ownIn); r.code() != "REQUIRES_2FA_ENROLLMENT" {
		t.Fatalf("restore without a factor: %+v", r.Errors)
	}
	e.factors.set(owner.ID, true)
	e.fresh2FA(owner.ID)
	// Single root: the request may carry an identity.
	if r := e.gql(ownerC, `mutation($in: RestoreArtifactInput!) { restoreArtifact(input: $in) { id } me: __typename x: restoreArtifact(input: $in) { id } }`, ownIn); r.code() != "VALIDATION" {
		t.Fatalf("aliased restoreArtifact: %+v", r.Errors)
	}
	bad := map[string]any{"in": map[string]any{
		"artifactId": e.art, "targetConnectionId": e.conn, "mode": "OVERWRITE", "targetDatabase": "app", "confirmTargetName": "ap",
	}}
	if r := e.gql(ownerC, restoreArtMut, bad); r.code() != "VALIDATION" {
		t.Fatalf("wrong confirmation: %+v", r.Errors)
	}
	if r := e.gql(ownerC, restoreArtMut, map[string]any{"in": map[string]any{
		"artifactId": e.art, "targetConnectionId": e.conn, "mode": "OVERWRITE", "targetDatabase": "other", "confirmTargetName": "other",
	}}); r.code() != "FORBIDDEN" {
		t.Fatalf("owner overwrite of another database: %+v", r.Errors)
	}
	type restoreGQL struct {
		ID, Status, Kind, Mode, TargetDatabase string
		RequestedBy                            *struct{ UserID string }
	}
	var own restoreGQL
	e.mustGQL(ownerC, restoreArtMut, ownIn).field(t, "restoreArtifact", &own)
	if own.Status != "QUEUED" || own.Mode != "OVERWRITE" || own.TargetDatabase != "app" || own.RequestedBy != nil {
		t.Fatalf("owner restore %+v", own)
	}
	if r := e.gql(ownerC, restoreArtMut, map[string]any{"in": map[string]any{
		"artifactId": e.art, "targetConnectionId": e.conn, "mode": "NEW_DB", "targetDatabase": "owner_new",
	}}); len(r.Errors) > 0 {
		t.Fatalf("owner new database: %+v", r.Errors)
	}
	e.fresh2FA(maint.ID)
	var m restoreGQL
	e.mustGQL(maintC, restoreArtMut, maintIn).field(t, "restoreArtifact", &m)
	if m.RequestedBy == nil || m.RequestedBy.UserID != maint.ID.String() {
		t.Fatalf("maintainer restore %+v", m)
	}

	// Globals: admin only (directive), the exact phrase.
	c := e.connName()
	gIn := map[string]any{"in": map[string]any{"artifactId": e.globals, "targetConnectionId": e.conn, "confirmPhrase": restore.GlobalsConfirmPhrase(c)}}
	if r := e.gql(maintC, restoreGlobalsMut, gIn); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer globals: %+v", r.Errors)
	}
	if r := e.gql(adminC, restoreGlobalsMut, gIn); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("admin globals without a fresh check: %+v", r.Errors)
	}
	e.fresh2FA(admin.ID)
	var g restoreGQL
	e.mustGQL(adminC, restoreGlobalsMut, gIn).field(t, "restoreGlobals", &g)
	if g.Kind != "GLOBALS" || g.Mode != "" || g.TargetDatabase != "" {
		t.Fatalf("globals restore %+v", g)
	}

	// Reads: scoped.
	var page struct {
		Edges []struct {
			Node struct {
				ID          string
				RequestedBy *struct{ UserID string }
			}
		}
	}
	e.mustGQL(ownerC, restoresQ, nil).field(t, "restores", &page)
	if len(page.Edges) != 4 { // the job's restores: owner x2, maintainer, globals
		t.Fatalf("owner restores %d", len(page.Edges))
	}
	for _, ed := range page.Edges {
		if ed.Node.RequestedBy != nil {
			t.Fatal("owners see who requested a restore")
		}
	}
	e.mustGQL(otherC, restoresQ, nil).field(t, "restores", &page)
	if len(page.Edges) != 0 {
		t.Fatalf("non-owner restores %d", len(page.Edges))
	}
	if r := e.gql(otherC, restoreQ, map[string]any{"id": own.ID}); r.code() != "FORBIDDEN" {
		t.Fatalf("non-owner restore: %+v", r.Errors)
	}
	if r := e.mustGQL(adminC, restoreQ, map[string]any{"id": uuid.New()}); string(r.Data["restore"]) != "null" {
		t.Fatalf("unknown restore for an admin: %s", r.Data["restore"])
	}
	e.mustGQL(ownerC, restoreLogsQ, map[string]any{"id": own.ID})

	// Cancel: staff only.
	if r := e.gql(ownerC, cancelRestoreMut, map[string]any{"id": own.ID}); r.code() != "FORBIDDEN" {
		t.Fatalf("owner cancel: %+v", r.Errors)
	}
	var cx struct{ RestoreID, Status string }
	e.mustGQL(maintC, cancelRestoreMut, map[string]any{"id": own.ID}).field(t, "cancelRestore", &cx)
	if cx.Status != "CANCELED" {
		t.Fatalf("cancel %+v", cx)
	}
	if r := e.gql(maintC, cancelRestoreMut, map[string]any{"id": own.ID}); r.code() != "CONFLICT" {
		t.Fatalf("second cancel: %+v", r.Errors)
	}
	if strings.Contains(strings.Join(e.bodies, "\n"), "AGE-SECRET-KEY") {
		t.Fatal("a response carries an identity")
	}
}

func (e *restoreEnv) connName() string {
	e.t.Helper()
	var n string
	if err := e.pool.QueryRow(e.t.Context(), `SELECT name FROM connections WHERE id = $1`, e.conn).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestRestoreUpdatedStream(t *testing.T) {
	e := newRestoreEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	other := e.user("other@example.com", sqlc.UserRoleUser)
	e.factors.set(admin.ID, true)
	e.own(e.job, owner.ID)
	adminC, ownerC, otherC := e.mustLogin(admin.Email), e.mustLogin(owner.Email), e.mustLogin(other.Email)
	e.fresh2FA(admin.ID)
	var r struct{ ID string }
	e.mustGQL(adminC, restoreArtMut, map[string]any{"in": map[string]any{
		"artifactId": e.art, "targetConnectionId": e.conn, "mode": "NEW_DB", "targetDatabase": "live",
	}}).field(t, "restoreArtifact", &r)
	id := uuid.MustParse(r.ID)

	// Others may not subscribe; unknown ids are refused.
	for name, ck := range map[string]string{"non-owner": otherC} {
		ch := e.openStream(ck, restoreUpdatedSub, map[string]any{"id": id})
		if ev, ok := nextEv(ch, 5*time.Second); !ok || !strings.Contains(ev.data, "FORBIDDEN") {
			t.Fatalf("%s: %+v", name, ev)
		}
	}
	if ev, ok := nextEv(e.openStream(adminC, restoreUpdatedSub, map[string]any{"id": uuid.New()}), 5*time.Second); !ok || !strings.Contains(ev.data, "NOT_FOUND") {
		t.Fatalf("unknown restore: %+v", ev)
	}

	type evGQL struct{ ID, Status string }
	ownerCh := e.openStream(ownerC, restoreUpdatedSub, map[string]any{"id": id})
	adminCh := e.openStream(adminC, restoreUpdatedSub, map[string]any{"id": id})
	var ev evGQL
	nextData(t, ownerCh, "restoreUpdated", &ev)
	if ev.Status != "QUEUED" {
		t.Fatalf("first owner event %+v", ev)
	}
	nextData(t, adminCh, "restoreUpdated", &ev)

	// Status changes are delivered.
	st, err := e.svc.Stage(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	st.Cleanup()
	for ev.Status != "VERIFYING" {
		nextData(t, ownerCh, "restoreUpdated", &ev)
	}

	// The owner loses the job: no further events for them; the admin sees
	// the terminal status and the stream completes.
	if _, err := e.pool.Exec(t.Context(), `DELETE FROM job_owners WHERE job_id = $1 AND user_id = $2`, e.job, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Finish(t.Context(), id, restore.StatusFailed, restore.ErrInterrupted); err != nil {
		t.Fatal(err)
	}
	for ev.Status != "FAILED" {
		nextData(t, adminCh, "restoreUpdated", &ev)
	}
	for {
		x, ok := nextEv(adminCh, 5*time.Second)
		if !ok {
			t.Fatal("the stream did not complete after the terminal status")
		}
		if x.event == "complete" {
			break
		}
	}
	for {
		x, ok := nextEv(ownerCh, time.Second)
		if !ok {
			break
		}
		if x.event == "next" {
			t.Fatalf("an owner without access received %+v", x)
		}
	}
}
