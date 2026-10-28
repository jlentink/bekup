//go:build integration

package restore_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/restore"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// probeRunner answers Probe with a fixed database list; every other RPC
// panics (the API never calls them).
type probeRunner struct {
	runnerpb.RunnerServiceClient
	mu    sync.Mutex
	dbs   []string
	calls int
	// priv is the privilege verdict (nil: not privileged).
	priv *runnerpb.TargetPrivileges
}

// setPrivileges sets the verdict of the next probes.
func (p *probeRunner) setPrivileges(v *runnerpb.TargetPrivileges) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.priv = v
}

func (p *probeRunner) Probe(context.Context, *runnerpb.ProbeRequest, ...grpc.CallOption) (*runnerpb.ProbeResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	priv := p.priv
	if priv == nil {
		priv = &runnerpb.TargetPrivileges{Status: runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_NOT_PRIVILEGED}
	}
	return &runnerpb.ProbeResponse{ServerVersion: "17.2", Databases: slices.Clone(p.dbs), Privileges: priv}, nil
}

// factorSet is the enrolled-factor lookup of the policy.
type factorSet struct {
	mu  sync.Mutex
	has map[uuid.UUID]bool
}

func (f *factorSet) HasSecondFactor(_ context.Context, id uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.has[id], nil
}

func (f *factorSet) set(id uuid.UUID, v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.has[id] = v
}

type apiEnv struct {
	*env
	runner  *probeRunner
	conns   *connections.Service
	qc      *queue.Client
	api     *restore.API
	pol     *authz.Policy
	factors *factorSet
	maint   uuid.UUID
	owner   uuid.UUID
	other   uuid.UUID
}

func newAPIEnv(t *testing.T, mods ...func(*restore.Options)) *apiEnv {
	t.Helper()
	e := &apiEnv{env: newEnv(t, mods...), runner: &probeRunner{dbs: []string{"app", "other", "app__bekup_old_0123456789ab"}},
		factors: &factorSet{has: map[uuid.UUID]bool{}}}
	logger := slog.New(slog.DiscardHandler)
	var err error
	e.conns, err = connections.New(connections.Options{
		DB: e.pool, Keyring: e.kr, Auditor: e.auditor, Runner: e.runner, Logger: logger,
		Limiter: func(string) (bool, time.Duration) { return true, 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	if e.qc, err = queue.New(e.pool, queue.Options{Settings: queue.SettingsFromConfig(config.DefaultBekup().Workers), Logger: logger}); err != nil {
		t.Fatal(err)
	}
	pol := authz.New(authz.Options{Ownership: jobs.Ownership{DB: e.pool}, SecondFactors: e.factors})
	e.pol = pol
	e.api, err = restore.NewAPI(restore.APIOptions{
		Service: e.svc, Policy: pol, Connections: e.conns, Owners: jobs.Ownership{DB: e.pool},
		Queue:    func() restore.Inserter { return e.qc },
		Canceler: func() restore.Canceler { return e.qc },
		Logger:   logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.factors.set(e.admin, true)
	e.maint = e.newUser("maint@example.com", "maintainer")
	e.owner = e.newUser("owner@example.com", "user")
	e.other = e.newUser("other@example.com", "user")
	return e
}

func (e *apiEnv) newUser(email, role string) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(e.t.Context(), `INSERT INTO users (email, display_name, role) VALUES ($1, 'U', $2) RETURNING id`, email, role).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	e.factors.set(id, true)
	return id
}

// as is the context of a user with a fresh passkey/TOTP check.
func (e *apiEnv) as(id uuid.UUID, role auth.Role) context.Context {
	return auth.WithPrincipal(e.t.Context(), auth.Principal{
		UserID: id, Role: role, SessionID: uuid.New(), AAL: 2, StepUpAt: time.Now(), AAL2At: time.Now(),
	})
}

// stepUpOnly is a session whose last re-authentication was a password,
// recovery code or a passkey without user verification (D-116: no aal2_at).
func (e *apiEnv) stepUpOnly(id uuid.UUID, role auth.Role) context.Context {
	return auth.WithPrincipal(e.t.Context(), auth.Principal{
		UserID: id, Role: role, SessionID: uuid.New(), AAL: 2, StepUpAt: time.Now(), AAL2At: time.Now().Add(-time.Hour),
	})
}

func (e *apiEnv) insertConn(engineKind string, temporary bool) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	var exp *time.Time
	if temporary {
		x := time.Now().Add(time.Hour)
		exp = &x
	}
	e.exec(`INSERT INTO connections (id, name, engine, version_key, image_key, host, port, username, tls_mode, temporary, expires_at)
		VALUES ($1, $2, $3, $4, $4, 'db2.example.test', 5432, 'restore', 'unverified', $5, $6)`,
		id, "conn-"+id.String()[:8], engineKind, map[string]string{"postgres": "17", "mysql": "8.4", "mariadb": "11.8"}[engineKind], temporary, exp)
	return id
}

func isCode(err error, code apierror.Code) bool { return apierror.IsCode(err, code) }

func fieldOf(err error) string {
	var fe *serviceutil.FieldError
	if errors.As(err, &fe) {
		return fe.Field
	}
	return ""
}

// pgJob creates a passphrase job owned by owner that backs up only "app"
// on its connection, with a verified, signed PostgreSQL artifact.
func (e *apiEnv) pgJob() (jobInfo, seeded) {
	e.t.Helper()
	j := e.job(jobkeys.ModePassphrase, e.diskID)
	e.exec(`UPDATE jobs SET db_mode = 'selected', databases = '{app}' WHERE id = $1`, j.id)
	e.exec(`UPDATE connections SET tls_mode = 'unverified' WHERE id = $1`, j.conn)
	e.exec(`INSERT INTO job_owners (job_id, user_id) VALUES ($1, $2)`, j.id, e.owner)
	s := e.seedAs(j, e.diskID, e.disk, []byte("-- dump"), time.Now(), seedSpec{format: pipeline.FormatCustom, engine: "postgres", versionKey: "17"})
	return j, s
}

func newDB(art, conn uuid.UUID, name string) restore.RequestInput {
	return restore.RequestInput{ArtifactID: art, TargetConnectionID: conn, Mode: restore.ModeNewDB, TargetDatabase: name}
}

func overwrite(art, conn uuid.UUID, name string) restore.RequestInput {
	return restore.RequestInput{ArtifactID: art, TargetConnectionID: conn, Mode: restore.ModeOverwrite, TargetDatabase: name, ConfirmTargetName: name}
}

func TestAPIRequestRules(t *testing.T) {
	e := newAPIEnv(t)
	j, s := e.pgJob()
	pg2 := e.insertConn("postgres", false)
	my := e.insertConn("mysql", false)
	tmp := e.insertConn("postgres", true)
	admin, maint, owner, other := e.as(e.admin, auth.RoleAdmin), e.as(e.maint, auth.RoleMaintainer), e.as(e.owner, auth.RoleUser), e.as(e.other, auth.RoleUser)

	// Admin restores into a new database: queued, River job, audit, no
	// identity anywhere.
	r, p, err := e.api.Request(admin, newDB(s.artifact, j.conn, "fresh_copy"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != restore.StatusQueued || r.RiverJobID == nil || r.TargetConnectionID != j.conn || *r.TargetDbName != "fresh_copy" {
		t.Fatalf("restore %+v", r)
	}
	if !hasWarning(p.Warnings, restore.WarnNewDatabasePartial) || hasWarning(p.Warnings, restore.WarnCrossSource) {
		t.Fatalf("warnings %+v", p.Warnings)
	}
	if n := e.count(`SELECT count(*) FROM river_job WHERE id = $1 AND kind = 'restore' AND args->>'restore_id' = $2`, *r.RiverJobID, r.ID.String()); n != 1 {
		t.Fatalf("river job rows %d", n)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'restore.requested' AND target_id = $1 AND actor_user_id = $2
		AND details->>'mode' = 'new_db' AND details->>'target_database' = 'fresh_copy'`, r.ID.String(), e.admin); n != 1 {
		t.Fatalf("restore.requested rows %d", n)
	}

	// Owners: their own job's database (allowed), another database on the
	// same server (refused), a new database there (allowed), another
	// connection (refused); a user who owns nothing is refused.
	if _, _, err := e.api.Request(owner, overwrite(s.artifact, j.conn, "app")); err != nil {
		t.Fatalf("owner overwrite of own database: %v", err)
	}
	if _, _, err := e.api.Request(owner, overwrite(s.artifact, j.conn, "other")); !isCode(err, apierror.Forbidden) {
		t.Fatalf("owner overwrite of another database: %v", err)
	}
	if _, _, err := e.api.Request(owner, newDB(s.artifact, j.conn, "owner_copy")); err != nil {
		t.Fatalf("owner new database: %v", err)
	}
	if _, _, err := e.api.Request(owner, newDB(s.artifact, pg2, "owner_copy")); !isCode(err, apierror.Forbidden) {
		t.Fatalf("owner new database on another connection: %v", err)
	}
	if _, _, err := e.api.Request(other, newDB(s.artifact, j.conn, "x")); !isCode(err, apierror.Forbidden) {
		t.Fatalf("non-owner: %v", err)
	}

	// Maintainers: any database on any same-engine connection; never a
	// temporary one; admins may.
	if _, _, err := e.api.Request(maint, overwrite(s.artifact, pg2, "anything")); err != nil {
		t.Fatalf("maintainer overwrite: %v", err)
	}
	if _, _, err := e.api.Request(maint, newDB(s.artifact, tmp, "t1")); !isCode(err, apierror.Forbidden) {
		t.Fatalf("maintainer temporary target: %v", err)
	}
	if _, p, err := e.api.Request(admin, newDB(s.artifact, tmp, "t1")); err != nil || !hasWarning(p.Warnings, restore.WarnTemporaryTarget) {
		t.Fatalf("admin temporary target: %v %+v", err, p.Warnings)
	}
	// A temporary target must outlive workers.restore_timeout (it
	// expires in an hour here).
	for timeout, ok := range map[time.Duration]bool{2 * time.Hour: false, 30 * time.Minute: true} {
		api, err := restore.NewAPI(restore.APIOptions{
			Service: e.svc, Policy: e.pol, Connections: e.conns, Owners: jobs.Ownership{DB: e.pool},
			Queue: func() restore.Inserter { return e.qc }, Canceler: func() restore.Canceler { return e.qc },
			RestoreTimeout: timeout,
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = api.Preview(admin, newDB(s.artifact, tmp, "t2"))
		if ok != (err == nil) || !ok && fieldOf(err) != "targetConnectionId" {
			t.Fatalf("restore timeout %s: %v", timeout, err)
		}
		if _, _, err := api.Request(admin, newDB(s.artifact, tmp, "t2")); !ok && fieldOf(err) != "targetConnectionId" {
			t.Fatalf("request, restore timeout %s: %v", timeout, err)
		}
	}

	// Fresh second factor: none -> STEP_UP_REQUIRED; a password (or UP-only
	// passkey) step-up is not enough; no factor -> REQUIRES_2FA_ENROLLMENT.
	stale := auth.WithPrincipal(t.Context(), auth.Principal{UserID: e.owner, Role: auth.RoleUser, SessionID: uuid.New(), AAL: 2})
	if _, _, err := e.api.Request(stale, newDB(s.artifact, j.conn, "c2")); !isCode(err, apierror.StepUpRequired) {
		t.Fatalf("no fresh 2FA: %v", err)
	}
	if _, _, err := e.api.Request(e.stepUpOnly(e.owner, auth.RoleUser), newDB(s.artifact, j.conn, "c2")); !isCode(err, apierror.StepUpRequired) {
		t.Fatalf("password step-up only: %v", err)
	}
	if _, _, err := e.api.Request(e.stepUpOnly(e.maint, auth.RoleMaintainer), newDB(s.artifact, pg2, "c2")); !isCode(err, apierror.StepUpRequired) {
		t.Fatalf("maintainer password step-up only: %v", err)
	}
	e.factors.set(e.owner, false)
	if _, _, err := e.api.Request(stale, newDB(s.artifact, j.conn, "c2")); !isCode(err, apierror.Requires2FAEnrollment) {
		t.Fatalf("no factor: %v", err)
	}
	e.factors.set(e.owner, true)

	// Type-to-confirm, names, engines.
	in := overwrite(s.artifact, pg2, "anything2")
	in.ConfirmTargetName = "Anything2"
	if _, _, err := e.api.Request(admin, in); fieldOf(err) != "confirmTargetName" {
		t.Fatalf("wrong confirmation: %v", err)
	}
	in.ConfirmTargetName = ""
	if _, _, err := e.api.Request(admin, in); fieldOf(err) != "confirmTargetName" {
		t.Fatalf("missing confirmation: %v", err)
	}
	if _, _, err := e.api.Request(admin, newDB(s.artifact, my, "copy")); fieldOf(err) != "targetConnectionId" || !errors.Is(err, restore.ErrEngineMismatch) {
		t.Fatalf("engine mismatch: %v", err)
	}
	for _, bad := range []string{"", "x;drop", "app__bekup_restore_1", "-x", strings.Repeat("a", 64)} {
		if _, _, err := e.api.Request(admin, newDB(s.artifact, pg2, bad)); fieldOf(err) != "targetDatabase" {
			t.Fatalf("name %q: %v", bad, err)
		}
	}
	if _, _, err := e.api.Request(admin, newDB(s.artifact, pg2, "template1")); fieldOf(err) != "targetDatabase" {
		t.Fatalf("system database: %v", err)
	}
	if _, _, err := e.api.Request(admin, newDB(uuid.New(), pg2, "x")); !errors.Is(err, restore.ErrNotFound) {
		t.Fatalf("unknown artifact (admin): %v", err)
	}
	if _, _, err := e.api.Request(owner, newDB(uuid.New(), j.conn, "x")); !isCode(err, apierror.Forbidden) {
		t.Fatalf("unknown artifact (owner): %v", err)
	}
	if _, _, err := e.api.Request(admin, newDB(s.artifact, uuid.New(), "x")); fieldOf(err) != "targetConnectionId" {
		t.Fatalf("unknown target (admin): %v", err)
	}
	if _, _, err := e.api.Request(owner, newDB(s.artifact, uuid.New(), "x")); !isCode(err, apierror.Forbidden) {
		t.Fatalf("unknown target (owner): %v", err)
	}

	// One active restore per target database.
	if _, _, err := e.api.Request(admin, newDB(s.artifact, j.conn, "fresh_copy")); !errors.Is(err, restore.ErrTargetBusy) {
		t.Fatalf("second restore into the same database: %v", err)
	}

	// The audit entries never carry an identity or the dump.
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'restore.requested' AND details::text LIKE '%AGE-SECRET%'`); n != 0 {
		t.Fatal("identity in the audit log")
	}
}

func hasWarning(ws []restore.Warning, code string) bool {
	return slices.ContainsFunc(ws, func(w restore.Warning) bool { return w.Code == code })
}

func TestAPIPreviewTargetsAndDatabases(t *testing.T) {
	e := newAPIEnv(t)
	j, s := e.pgJob()
	pg2 := e.insertConn("postgres", false)
	e.insertConn("mysql", false)
	tmp := e.insertConn("postgres", true)
	admin, maint, owner := e.as(e.admin, auth.RoleAdmin), e.as(e.maint, auth.RoleMaintainer), e.as(e.owner, auth.RoleUser)

	ids := func(l restore.TargetList) []uuid.UUID {
		out := make([]uuid.UUID, 0, len(l.Targets))
		for _, t := range l.Targets {
			out = append(out, t.Connection.ID)
		}
		return out
	}
	l, err := e.api.Targets(admin, s.artifact)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(l); len(got) != 3 || got[0] != j.conn || !slices.Contains(got, pg2) || !slices.Contains(got, tmp) || !l.Targets[0].Original {
		t.Fatalf("admin targets %v", got)
	}
	if l.Source.Engine != "postgres" || l.Source.Database != "app" || l.Source.VersionKey != "17" {
		t.Fatalf("source %+v", l.Source)
	}
	if l, err = e.api.Targets(maint, s.artifact); err != nil || len(l.Targets) != 2 || slices.Contains(ids(l), tmp) {
		t.Fatalf("maintainer targets %v %v", ids(l), err)
	}
	if l, err = e.api.Targets(owner, s.artifact); err != nil || len(l.Targets) != 1 || l.Targets[0].Connection.ID != j.conn || !l.Targets[0].ExistingRestricted {
		t.Fatalf("owner targets %+v %v", l.Targets, err)
	}
	if _, err := e.api.Targets(e.as(e.other, auth.RoleUser), s.artifact); !isCode(err, apierror.Forbidden) {
		t.Fatalf("non-owner targets: %v", err)
	}

	// Databases: owners only their own, bekup's temporary names hidden,
	// cached between calls.
	d, err := e.api.Databases(owner, s.artifact, j.conn)
	if err != nil || !slices.Equal(d.Databases, []string{"app"}) {
		t.Fatalf("owner databases %v %v", d.Databases, err)
	}
	if d, err = e.api.Databases(maint, s.artifact, j.conn); err != nil || !slices.Equal(d.Databases, []string{"app", "other"}) {
		t.Fatalf("maintainer databases %v %v", d.Databases, err)
	}
	if e.runner.calls != 1 {
		t.Fatalf("runner calls %d (cache)", e.runner.calls)
	}
	if _, err := e.api.Databases(owner, s.artifact, pg2); !isCode(err, apierror.Forbidden) {
		t.Fatalf("owner databases of another connection: %v", err)
	}
	if _, err := e.api.Databases(maint, s.artifact, tmp); fieldOf(err) != "targetConnectionId" {
		t.Fatalf("maintainer databases of a temporary connection: %v", err)
	}

	// Preview: warnings and confirmation text, no second factor needed.
	stale := auth.WithPrincipal(t.Context(), auth.Principal{UserID: e.owner, Role: auth.RoleUser, SessionID: uuid.New(), AAL: 1})
	p, err := e.api.Preview(stale, overwrite(s.artifact, j.conn, "app"))
	if err != nil || p.ConfirmText != "app" || !hasWarning(p.Warnings, restore.WarnOverwritePostgres) || !hasWarning(p.Warnings, restore.WarnClientVersion) {
		t.Fatalf("preview %+v %v", p, err)
	}
	if _, err := e.api.Preview(stale, overwrite(s.artifact, j.conn, "other")); !isCode(err, apierror.Forbidden) {
		t.Fatalf("preview of a forbidden target: %v", err)
	}
	if p, err = e.api.Preview(admin, newDB(s.artifact, pg2, "copy")); err != nil || p.ConfirmText != "" || !hasWarning(p.Warnings, restore.WarnCrossSource) {
		t.Fatalf("cross-source preview %+v %v", p, err)
	}

	// MySQL in-place overwrite warning.
	mj := e.job(jobkeys.ModePassphrase, e.diskID)
	e.exec(`UPDATE connections SET engine = 'mysql', version_key = '8.4', image_key = '8.4' WHERE id = $1`, mj.conn)
	ms := e.seedAs(mj, e.diskID, e.disk, []byte("-- mysql dump"), time.Now(), seedSpec{format: pipeline.FormatSQL, engine: "mysql", versionKey: "8.4"})
	p, err = e.api.Preview(admin, overwrite(ms.artifact, mj.conn, "shop"))
	if err != nil {
		t.Fatal(err)
	}
	var mw string
	for _, w := range p.Warnings {
		if w.Code == restore.WarnOverwriteMySQL {
			mw = w.Message
		}
	}
	if mw != restore.MySQLOverwriteWarning {
		t.Fatalf("MySQL warning %q", mw)
	}
	in := overwrite(ms.artifact, mj.conn, "shop")
	in.KeepOwners = true
	if _, err := e.api.Preview(admin, in); fieldOf(err) != "keepOwners" {
		t.Fatalf("keepOwners on MySQL: %v", err)
	}
}

func TestAPIGlobalsIdentityAndCancel(t *testing.T) {
	e := newAPIEnv(t)
	admin, maint, owner := e.as(e.admin, auth.RoleAdmin), e.as(e.maint, auth.RoleMaintainer), e.as(e.owner, auth.RoleUser)

	// Globals: admin only, PostgreSQL only, the exact phrase.
	gj := e.job(jobkeys.ModePassphrase, e.diskID)
	gs := e.seedAs(gj, e.diskID, e.disk, []byte("CREATE ROLE reader;\n"), time.Now(), seedSpec{kind: pipeline.KindGlobals, format: pipeline.FormatSQL, engine: "postgres", versionKey: "17"})
	c, err := e.conns.GetForRestore(t.Context(), gj.conn)
	if err != nil {
		t.Fatal(err)
	}
	phrase := restore.GlobalsConfirmPhrase(c.Name)
	// The target is not ready, so its role's privileges are unknown
	// (D-068): an admin acknowledges that.
	gin := restore.GlobalsInput{ArtifactID: gs.artifact, TargetConnectionID: gj.conn, ConfirmPhrase: phrase, AcknowledgePrivilegedTarget: true}
	if _, _, err := e.api.RequestGlobals(maint, gin); !isCode(err, apierror.Forbidden) {
		t.Fatalf("maintainer globals: %v", err)
	}
	if _, _, err := e.api.RequestGlobals(e.stepUpOnly(e.admin, auth.RoleAdmin), gin); !isCode(err, apierror.StepUpRequired) {
		t.Fatalf("globals without a fresh factor: %v", err)
	}
	bad := gin
	bad.ConfirmPhrase = "restore globals"
	if _, _, err := e.api.RequestGlobals(admin, bad); fieldOf(err) != "confirmPhrase" {
		t.Fatalf("wrong phrase: %v", err)
	}
	if _, _, err := e.api.Request(admin, newDB(gs.artifact, gj.conn, "x")); fieldOf(err) != "artifactId" {
		t.Fatalf("globals through restoreArtifact: %v", err)
	}
	p, err := e.api.PreviewGlobals(admin, restore.GlobalsInput{ArtifactID: gs.artifact, TargetConnectionID: gj.conn})
	if err != nil || p.ConfirmText != phrase || !hasWarning(p.Warnings, restore.WarnGlobalsRoles) {
		t.Fatalf("globals preview %+v %v", p, err)
	}
	gr, _, err := e.api.RequestGlobals(admin, gin)
	if err != nil || gr.TargetDbName != nil || gr.Mode != sqlc.RestoreModeOverwrite {
		t.Fatalf("globals restore %+v %v", gr, err)
	}

	// Recipient-encrypted backup: the identity is required, matched and
	// stored only sealed; Stage takes (deletes) it.
	rj := e.job(jobkeys.ModeRecipient, e.diskID)
	e.exec(`UPDATE connections SET tls_mode = 'unverified' WHERE id = $1`, rj.conn) // ready: a plain role (D-068)
	rs := e.seed(rj, e.diskID, e.disk, []byte("-- recipient dump"), time.Now())
	if _, _, err := e.api.Request(admin, newDB(rs.artifact, rj.conn, "r1")); fieldOf(err) != "ageIdentity" {
		t.Fatalf("missing identity: %v", err)
	}
	in := newDB(rs.artifact, rj.conn, "r1")
	in.Identity = []byte(rj.identity)
	r, p2, err := e.api.Request(admin, in)
	if err != nil || !hasWarning(p2.Warnings, restore.WarnIdentityRequired) {
		t.Fatalf("restore with identity: %v", err)
	}
	var sealed []byte
	if err := e.pool.QueryRow(t.Context(), `SELECT sealed_identity FROM restore_identity_handoff WHERE restore_id = $1`, r.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte(rj.identity)) || bytes.Contains(sealed, []byte("AGE-SECRET-KEY")) {
		t.Fatal("the identity is stored in plaintext")
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE details::text LIKE '%AGE-SECRET-KEY%' OR details::text ILIKE $1`, "%"+rj.identity[len(rj.identity)-20:]+"%"); n != 0 {
		t.Fatal("identity in the audit log")
	}
	st, err := e.svc.Stage(t.Context(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	st.Cleanup()
	if n := e.count(`SELECT count(*) FROM restore_identity_handoff WHERE restore_id = $1`, r.ID); n != 0 {
		t.Fatal("the identity handoff survived the staging")
	}

	// Cancel: staff only; a queued restore is finished at once with its
	// River job and identity handoff; a finished one is a conflict.
	in.TargetDatabase = "r2"
	r2, _, err := e.api.Request(admin, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.api.Cancel(owner, r2.ID); !isCode(err, apierror.Forbidden) {
		t.Fatalf("owner cancel: %v", err)
	}
	cx, err := e.api.Cancel(maint, r2.ID)
	if err != nil || cx.Status != restore.StatusCanceled {
		t.Fatalf("cancel %+v %v", cx, err)
	}
	if n := e.count(`SELECT count(*) FROM river_job WHERE id = $1 AND state = 'cancelled'`, *r2.RiverJobID); n != 1 {
		t.Fatal("River job not canceled")
	}
	if n := e.count(`SELECT count(*) FROM restore_identity_handoff WHERE restore_id = $1`, r2.ID); n != 0 {
		t.Fatal("handoff of a canceled restore kept")
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'restore.cancel_requested' AND target_id = $1`, r2.ID.String()); n != 1 {
		t.Fatal("cancel not audited")
	}
	if _, err := e.api.Cancel(maint, r2.ID); !errors.Is(err, restore.ErrFinished) {
		t.Fatalf("second cancel: %v", err)
	}
	if _, err := e.api.Cancel(admin, uuid.New()); !errors.Is(err, restore.ErrNotFound) {
		t.Fatalf("unknown cancel: %v", err)
	}

	// Reads are scoped: the owner of nothing sees nothing.
	e.exec(`INSERT INTO job_owners (job_id, user_id) VALUES ($1, $2)`, rj.id, e.owner)
	page, err := e.api.List(owner, restore.ListFilter{}, nil, 50)
	if err != nil || len(page.Restores) != 2 {
		t.Fatalf("owner list %d %v", len(page.Restores), err)
	}
	if page, err = e.api.List(e.as(e.other, auth.RoleUser), restore.ListFilter{}, nil, 50); err != nil || len(page.Restores) != 0 {
		t.Fatalf("other list %d %v", len(page.Restores), err)
	}
	if page, err = e.api.List(maint, restore.ListFilter{}, nil, 2); err != nil || len(page.Restores) != 2 || !page.More {
		t.Fatalf("maintainer list %d %v", len(page.Restores), err)
	}
	if _, err := e.api.Get(e.as(e.other, auth.RoleUser), gr.ID); !isCode(err, apierror.Forbidden) {
		t.Fatalf("other get: %v", err)
	}
	if _, err := e.api.Get(owner, gr.ID); !isCode(err, apierror.Forbidden) {
		t.Fatalf("owner get of another job's restore: %v", err)
	}
	if v, err := e.api.Get(owner, r2.ID); err != nil || v.Status != restore.StatusCanceled || v.TargetName == "" {
		t.Fatalf("owner get %+v %v", v, err)
	}
}

func TestStuckSweep(t *testing.T) {
	var shift time.Duration
	var mu sync.Mutex
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return time.Now().Add(shift)
	}
	e := newAPIEnv(t, func(o *restore.Options) { o.Now = clock })
	j, s := e.pgJob()
	admin := e.as(e.admin, auth.RoleAdmin)
	queued, _, err := e.api.Request(admin, newDB(s.artifact, j.conn, "queued_one"))
	if err != nil {
		t.Fatal(err)
	}
	running, _, err := e.api.Request(admin, newDB(s.artifact, j.conn, "running_one"))
	if err != nil {
		t.Fatal(err)
	}
	// A worker took "running" (staging, hold, slot, scratch dir) and died.
	st, err := e.svc.Stage(t.Context(), running.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = st // the crashed worker never cleans up
	if ok, err := runstate.NewSlots(e.pool).TryAcquire(t.Context(), running.ID, j.conn, runstate.SlotRestore, runstate.SlotLimits{Global: 4, PerConnection: 4}); err != nil || !ok {
		t.Fatal("slot", err)
	}
	if e.holds(s.artifact) != 1 {
		t.Fatal("no hold")
	}

	// Nothing is stuck yet.
	if res, err := e.svc.SweepStuck(t.Context(), time.Hour, time.Minute); err != nil || res.Abandoned != 0 {
		t.Fatalf("early sweep %+v %v", res, err)
	}
	// Past the restore timeout + grace: the started one is abandoned; the
	// queued one (River job still available) is left alone.
	mu.Lock()
	shift = 2 * time.Hour
	mu.Unlock()
	res, err := e.svc.SweepStuck(t.Context(), time.Hour, time.Minute)
	if err != nil || res.Abandoned != 1 {
		t.Fatalf("sweep %+v %v", res, err)
	}
	if st, code := e.status(running.ID); st != "failed" || code != restore.CodeAbandoned {
		t.Fatalf("stuck restore %s %s", st, code)
	}
	if st, _ := e.status(queued.ID); st != "queued" {
		t.Fatalf("queued restore %s", st)
	}
	if e.holds(s.artifact) != 0 || e.count(`SELECT count(*) FROM run_slots WHERE holder_id = $1`, running.ID) != 0 {
		t.Fatal("hold or slot not released")
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'restore.abandoned' AND target_id = $1 AND details->>'previous_status' = 'verifying'`, running.ID.String()); n != 1 {
		t.Fatalf("abandon audit rows %d", n)
	}
	st.Cleanup()
	for _, name := range e.scratchEntries() {
		if strings.Contains(name, running.ID.String()) {
			t.Fatal("staging directory kept")
		}
	}

	// A queued restore whose River job ended (discarded attempt) is
	// abandoned once idle for the grace period.
	tx, err := e.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.qc.CancelTx(t.Context(), tx, *queued.RiverJobID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if res, err := e.svc.SweepStuck(t.Context(), time.Hour, time.Minute); err != nil || res.Abandoned != 1 {
		t.Fatalf("sweep of a queued restore %+v %v", res, err)
	}
	if st, code := e.status(queued.ID); st != "failed" || code != restore.CodeAbandoned {
		t.Fatalf("queued restore after its job ended %s %s", st, code)
	}
	// Idempotent.
	if res, err := e.svc.SweepStuck(t.Context(), time.Hour, time.Minute); err != nil || res.Abandoned != 0 {
		t.Fatalf("repeat sweep %+v %v", res, err)
	}
}
