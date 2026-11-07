//go:build integration

package riskaccept_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/riskaccept"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

type env struct {
	t       *testing.T
	ctx     context.Context
	d       dbtest.DB
	pool    *pgxpool.Pool
	q       *sqlc.Queries
	svc     *riskaccept.Service
	posture []riskaccept.Condition
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	t.Cleanup(cancel)
	d := dbtest.Migrated(t)
	e := &env{t: t, ctx: ctx, d: d, pool: d.AppPool(t)}
	e.q = sqlc.New(e.pool)
	auditor := audit.New(e.pool, audit.Options{Registry: logging.NewRegistry(), Observer: notify.ObserveAudit})
	svc, err := riskaccept.New(riskaccept.Options{
		DB: e.pool, Auditor: auditor, Authorizer: authz.New(authz.Options{Ownership: jobs.Ownership{DB: e.pool}}),
		Posture: func() []riskaccept.Condition { return e.posture },
	})
	if err != nil {
		t.Fatal(err)
	}
	e.svc = svc
	return e
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.ctx, sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(e.ctx, sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) user(role sqlc.UserRole) uuid.UUID {
	e.t.Helper()
	u, err := e.q.InsertUser(e.ctx, sqlc.InsertUserParams{Email: uuid.NewString()[:8] + "@example.com", Role: role})
	if err != nil {
		e.t.Fatal(err)
	}
	return u.ID
}

// job inserts a job (with its own connection) owned by owners whose
// encryption is opted out; it returns the job and its connection.
func (e *env) job(owners ...uuid.UUID) (uuid.UUID, uuid.UUID) {
	e.t.Helper()
	id, err := dbtest.InsertJob(e.ctx, e.pool, dbtest.JobRow{Enabled: true})
	if err != nil {
		e.t.Fatal(err)
	}
	for _, o := range owners {
		e.exec("INSERT INTO job_owners (job_id, user_id) VALUES ($1, $2)", id, o)
	}
	e.exec(`INSERT INTO job_encryption (job_id, mode, opted_out_at, opt_out_reason) VALUES ($1, 'none', now(), 'test')`, id)
	var conn uuid.UUID
	if err := e.pool.QueryRow(e.ctx, "SELECT connection_id FROM jobs WHERE id = $1", id).Scan(&conn); err != nil {
		e.t.Fatal(err)
	}
	return id, conn
}

func (e *env) as(id uuid.UUID, role auth.Role) context.Context {
	now := time.Now()
	return auth.WithPrincipal(e.ctx, auth.Principal{UserID: id, SessionID: uuid.New(), Role: role, AAL: auth.AAL1, StepUpAt: now})
}

func (e *env) alerts(kind string) int {
	return e.count(`SELECT count(*) FROM notification_events WHERE type = 'security.system' AND detail->>'kind' = $1`, kind)
}

func (e *env) auditCount(action string, id uuid.UUID) int {
	return e.count(`SELECT count(*) FROM audit_log WHERE action = $1 AND target_type = 'risk_acceptance' AND target_id = $2`, action, id.String())
}

func (e *env) auditDetails(action string, id uuid.UUID) map[string]any {
	e.t.Helper()
	var raw []byte
	err := e.pool.QueryRow(e.ctx, `SELECT details FROM audit_log WHERE action = $1 AND target_id = $2 ORDER BY id DESC LIMIT 1`, action, id.String()).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		e.t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(raw, &d); err != nil {
		e.t.Fatal(err)
	}
	return d
}

// backdated stores an encryption_opt_out acceptance created and expiring
// at the given SQL times, as the superuser with triggers off (the insert
// guard refuses backdating for every other role).
func (e *env) backdated(job uuid.UUID, hash [32]byte, by uuid.UUID, created, expires string) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	if err := e.d.SuperExec(e.ctx, "SET session_replication_role = replica",
		fmt.Sprintf(`INSERT INTO risk_acceptances (id, kind, job_id, condition_hash, condition, duration, reason, created_by, created_at, expires_at)
			VALUES ('%s', 'encryption_opt_out', '%s', decode('%x', 'hex'), 'x', 'month', 'r', '%s', %s, %s)`,
			id, job, hash[:], by, created, expires)); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func code(err error) apierror.Code {
	if a, ok := apierror.As(err); ok {
		return a.Code
	}
	return ""
}

func find(fs []riskaccept.Finding, k riskaccept.Kind, subject uuid.UUID) *riskaccept.Finding {
	for i := range fs {
		c := fs[i].Condition
		if c.Kind == k && (subject == uuid.Nil || c.ConnectionID == subject || c.JobID == subject) {
			return &fs[i]
		}
	}
	return nil
}

func TestIntegrationAcceptRevoke(t *testing.T) {
	e := newEnv(t)
	admin := e.user(sqlc.UserRoleAdmin)
	owner := e.user(sqlc.UserRoleUser)
	other := e.user(sqlc.UserRoleUser)
	maint := e.user(sqlc.UserRoleMaintainer)
	jobA, conn := e.job(owner)
	jobB, _ := e.job(other)
	e.exec("UPDATE connections SET tls_mode = 'unverified' WHERE id = $1", conn)
	adminCtx := e.as(admin, auth.RoleAdmin)

	// The finding is open; the admin accepts it for a month.
	fs, err := e.svc.Findings(adminCtx)
	if err != nil {
		t.Fatal(err)
	}
	f := find(fs, riskaccept.KindConnectionTLS, conn)
	if f == nil || f.Acceptance != nil || !f.CanAccept {
		t.Fatalf("open finding %+v", f)
	}
	a, err := e.svc.Accept(adminCtx, riskaccept.AcceptInput{
		Kind: riskaccept.KindConnectionTLS, ConnectionID: conn, ConditionID: f.Condition.ID(),
		Duration: riskaccept.DurationMonth, Reason: "legacy server without a certificate",
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.ExpiresAt == nil || a.ExpiresAt.Sub(a.CreatedAt) < 27*24*time.Hour || a.Status != riskaccept.StatusActive {
		t.Fatalf("acceptance %+v", a)
	}
	if d := e.auditDetails("risk.accepted", a.ID); d == nil || d["kind"] != "connection_tls" || d["duration"] != "month" ||
		d["note"] != "legacy server without a certificate" || d["connection_id"] != conn.String() {
		t.Fatalf("audit %v", d)
	}
	if e.alerts(notify.KindRiskAccepted) != 1 {
		t.Fatal("risk_accepted alert")
	}
	fs, err = e.svc.Findings(adminCtx)
	if err != nil {
		t.Fatal(err)
	}
	if f := find(fs, riskaccept.KindConnectionTLS, conn); f == nil || f.Acceptance == nil || f.Acceptance.ID != a.ID ||
		f.Acceptance.CreatedBy == nil || f.Acceptance.Reason == "" {
		t.Fatalf("covered finding %+v", f)
	}

	// Accepting again (forever) supersedes: one live acceptance.
	b, err := e.svc.Accept(adminCtx, riskaccept.AcceptInput{
		Kind: riskaccept.KindConnectionTLS, ConnectionID: conn, ConditionID: f.Condition.ID(),
		Duration: riskaccept.DurationForever, Reason: "no fix planned",
	})
	if err != nil || b.ExpiresAt != nil {
		t.Fatalf("forever: %+v %v", b, err)
	}
	if n := e.count("SELECT count(*) FROM risk_acceptances WHERE connection_id = $1 AND ended_at IS NULL", conn); n != 1 {
		t.Fatalf("%d live acceptances", n)
	}
	if n := e.count("SELECT count(*) FROM risk_acceptances WHERE id = $1 AND end_reason = 'superseded' AND ended_by = $2", a.ID, admin); n != 1 {
		t.Fatal("not superseded")
	}
	if e.alerts(notify.KindRiskAcceptedPermanently) != 1 {
		t.Fatal("permanent alert")
	}

	// A changed condition is open again and the old id is a CONFLICT.
	e.exec("UPDATE connections SET tls_mode = 'disabled' WHERE id = $1", conn)
	fs, err = e.svc.Findings(adminCtx)
	if err != nil {
		t.Fatal(err)
	}
	nf := find(fs, riskaccept.KindConnectionTLS, conn)
	if nf == nil || nf.Acceptance != nil || nf.Condition.ID() == f.Condition.ID() {
		t.Fatalf("changed condition %+v", nf)
	}
	if _, err := e.svc.Accept(adminCtx, riskaccept.AcceptInput{
		Kind: riskaccept.KindConnectionTLS, ConnectionID: conn, ConditionID: f.Condition.ID(), Duration: riskaccept.DurationYear, Reason: "x",
	}); !errors.Is(err, riskaccept.ErrConditionChanged) {
		t.Fatalf("stale condition: %v", err)
	}
	// The sweep ends it (condition_changed) and alerts.
	if err := e.svc.Sweep(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count("SELECT count(*) FROM risk_acceptances WHERE id = $1 AND end_reason = 'condition_changed'", b.ID); n != 1 {
		t.Fatal("sweep did not end the changed acceptance")
	}
	if e.alerts(notify.KindRiskAcceptanceEnded) != 1 {
		t.Fatal("ended alert")
	}

	// Owners: only their job's opt-out, never connection or instance kinds;
	// maintainers never.
	ownerCtx := e.as(owner, auth.RoleUser)
	ofs, err := e.svc.Findings(ownerCtx)
	if err != nil {
		t.Fatal(err)
	}
	of := find(ofs, riskaccept.KindEncryptionOptOut, jobA)
	if len(ofs) != 1 || of == nil || !of.CanAccept {
		t.Fatalf("owner findings %+v", ofs)
	}
	if _, err := e.svc.Accept(ownerCtx, riskaccept.AcceptInput{
		Kind: riskaccept.KindConnectionTLS, ConnectionID: conn, ConditionID: nf.Condition.ID(), Duration: riskaccept.DurationYear, Reason: "x",
	}); code(err) != apierror.Forbidden && !errors.Is(err, riskaccept.ErrForbidden) {
		t.Fatalf("owner on a connection: %v", err)
	}
	if _, err := e.svc.Accept(ownerCtx, riskaccept.AcceptInput{
		Kind: riskaccept.KindEncryptionOptOut, JobID: jobB, ConditionID: of.Condition.ID(), Duration: riskaccept.DurationYear, Reason: "x",
	}); code(err) != apierror.Forbidden {
		t.Fatalf("owner on a foreign job: %v", err)
	}
	if _, err := e.svc.Accept(e.as(maint, auth.RoleMaintainer), riskaccept.AcceptInput{
		Kind: riskaccept.KindEncryptionOptOut, JobID: jobA, ConditionID: of.Condition.ID(), Duration: riskaccept.DurationYear, Reason: "x",
	}); code(err) != apierror.Forbidden {
		t.Fatalf("maintainer: %v", err)
	}
	oa, err := e.svc.Accept(ownerCtx, riskaccept.AcceptInput{
		Kind: riskaccept.KindEncryptionOptOut, JobID: jobA, ConditionID: of.Condition.ID(), Duration: riskaccept.DurationYear, Reason: "test data only",
	})
	if err != nil {
		t.Fatal(err)
	}
	// The other owner sees nothing of it; maintainers read it.
	if l, err := e.svc.List(e.as(other, auth.RoleUser), riskaccept.ListActive, 0); err != nil || len(l) != 0 {
		t.Fatalf("other owner list %+v %v", l, err)
	}
	if l, err := e.svc.List(e.as(maint, auth.RoleMaintainer), riskaccept.ListActive, 0); err != nil || len(l) != 1 || l[0].ID != oa.ID ||
		l[0].CreatedBy == nil || l[0].CreatedBy.Email != "" || l[0].SubjectName == "" {
		t.Fatalf("maintainer list %+v %v", l, err)
	}
	if l, err := e.svc.List(adminCtx, riskaccept.ListAll, 0); err != nil || len(l) != 3 {
		t.Fatalf("admin list %d %v", len(l), err)
	}

	// Revoke: the admin revokes the owner's acceptance; a second revoke
	// is refused.
	r, err := e.svc.Revoke(adminCtx, oa.ID)
	if err != nil || r.Status != riskaccept.StatusRevoked {
		t.Fatalf("revoke %+v %v", r, err)
	}
	if e.auditCount("risk.acceptance_revoked", oa.ID) != 1 {
		t.Fatal("revoke audit")
	}
	if _, err := e.svc.Revoke(adminCtx, oa.ID); !errors.Is(err, riskaccept.ErrEnded) {
		t.Fatalf("second revoke: %v", err)
	}

	// The guard: immutable columns and no DELETE for bekup_app.
	if _, err := e.pool.Exec(e.ctx, "UPDATE risk_acceptances SET reason = 'x' WHERE id = $1", oa.ID); err == nil {
		t.Fatal("reason changed")
	}
	if _, err := e.pool.Exec(e.ctx, "UPDATE risk_acceptances SET end_reason = 'expired' WHERE id = $1", oa.ID); err == nil {
		t.Fatal("ended acceptance changed")
	}
	if _, err := e.pool.Exec(e.ctx, "DELETE FROM risk_acceptances WHERE id = $1", oa.ID); err == nil {
		t.Fatal("acceptance deleted")
	}
}

func TestIntegrationSweepAndPosture(t *testing.T) {
	e := newEnv(t)
	admin := e.user(sqlc.UserRoleAdmin)
	owner := e.user(sqlc.UserRoleUser)
	job, _ := e.job(owner)
	adminCtx := e.as(admin, auth.RoleAdmin)

	// Posture: accept this process's database TLS condition.
	e.posture = []riskaccept.Condition{riskaccept.InsecureDBTLS("require", false, "db")}
	if len(e.svc.AcceptedPosture(e.ctx)) != 0 {
		t.Fatal("accepted before acceptance")
	}
	p, err := e.svc.Accept(adminCtx, riskaccept.AcceptInput{
		Kind: riskaccept.KindInsecureDBTLS, ConditionID: e.posture[0].ID(), Duration: riskaccept.DurationYear, Reason: "managed database",
	})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := e.svc.AcceptedPosture(e.ctx)[string(riskaccept.KindInsecureDBTLS)]; !ok || v.AcceptanceID != p.ID {
		t.Fatal("posture not covered")
	}
	// Another process with another mode is not covered, and the sweep
	// does not end the acceptance for it.
	e.posture = []riskaccept.Condition{riskaccept.InsecureDBTLS("disable", false, "db")}
	if len(e.svc.AcceptedPosture(e.ctx)) != 0 {
		t.Fatal("a different posture is covered")
	}
	if err := e.svc.Sweep(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count("SELECT count(*) FROM risk_acceptances WHERE id = $1 AND ended_at IS NULL", p.ID); n != 1 {
		t.Fatal("posture acceptance ended by another condition")
	}

	// Expiring within 7 days: warned once; expired: ended.
	var opted time.Time
	if err := e.pool.QueryRow(e.ctx, "SELECT opted_out_at FROM job_encryption WHERE job_id = $1", job).Scan(&opted); err != nil {
		t.Fatal(err)
	}
	cond, _ := riskaccept.EncryptionOptOut(job, &opted)
	// bekup_app cannot backdate a row: the insert guard sets created_at to
	// now, so the old expiry no longer matches the duration.
	if _, err := e.pool.Exec(e.ctx, `INSERT INTO risk_acceptances (kind, job_id, condition_hash, condition, duration, reason, created_by, created_at, expires_at)
		VALUES ('encryption_opt_out', $1, $2, 'x', 'month', 'r', $3, now() - interval '2 months', now() - interval '1 month')`, job, cond.Hash[:], owner); err == nil {
		t.Fatal("a backdated acceptance was stored")
	}
	soon := e.backdated(job, cond.Hash, owner, "now() - interval '1 month' + interval '3 days'", "now() + interval '3 days'")
	for range 2 {
		if err := e.svc.Sweep(e.ctx); err != nil {
			t.Fatal(err)
		}
	}
	if e.auditCount("risk.acceptance_expiring", soon) != 1 || e.alerts(notify.KindRiskAcceptanceExpiring) != 1 {
		t.Fatal("expiry warning not exactly once")
	}
	if n := e.count(`SELECT count(*) FROM notification_events WHERE type = 'security.account' AND detail->>'kind' = $1 AND user_id = $2`,
		notify.KindRiskAcceptanceExpiring, owner); n != 1 {
		t.Fatal("the acceptor was not told")
	}
	// The owner loses the job: the acceptance covers nothing and ends.
	e.exec("DELETE FROM job_owners WHERE job_id = $1", job)
	fs, err := e.svc.Findings(adminCtx)
	if err != nil {
		t.Fatal(err)
	}
	if f := find(fs, riskaccept.KindEncryptionOptOut, job); f == nil || f.Acceptance != nil {
		t.Fatalf("lost rights still covers %+v", f)
	}
	if err := e.svc.Sweep(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count("SELECT count(*) FROM risk_acceptances WHERE id = $1 AND end_reason = 'acceptor_lost_rights'", soon); n != 1 {
		t.Fatal("not ended for lost rights")
	}

	// An acceptance whose end date passed ends as expired.
	old := e.backdated(job, cond.Hash, admin, "now() - interval '2 months'", "now() - interval '1 month'")
	if err := e.svc.Sweep(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count("SELECT count(*) FROM risk_acceptances WHERE id = $1 AND end_reason = 'expired'", old); n != 1 {
		t.Fatal("not ended as expired")
	}

	// A duration and an expiry that disagree are refused by the database.
	if _, err := e.pool.Exec(e.ctx, `INSERT INTO risk_acceptances (kind, condition_hash, condition, duration, reason, created_by, expires_at)
		VALUES ('insecure_db_roles', $1, 'x', 'forever', 'r', $2, now() + interval '1 day')`, cond.Hash[:], admin); err == nil {
		t.Fatal("forever with an end date stored")
	}

	// Counts for the summary line.
	c, err := riskaccept.CountsOf(e.ctx, e.q)
	if err != nil || c.Accepted != 1 || c.Forever != 0 {
		t.Fatalf("counts %+v %v", c, err)
	}
}

func TestIntegrationEndForConnection(t *testing.T) {
	e := newEnv(t)
	admin := e.user(sqlc.UserRoleAdmin)
	_, conn := e.job()
	e.exec("UPDATE connections SET tls_mode = 'unverified' WHERE id = $1", conn)
	adminCtx := e.as(admin, auth.RoleAdmin)
	fs, err := e.svc.Findings(adminCtx)
	if err != nil {
		t.Fatal(err)
	}
	f := find(fs, riskaccept.KindConnectionTLS, conn)
	a, err := e.svc.Accept(adminCtx, riskaccept.AcceptInput{
		Kind: riskaccept.KindConnectionTLS, ConnectionID: conn, ConditionID: f.Condition.ID(), Duration: riskaccept.DurationYear, Reason: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := e.pool.Begin(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Error(err)
		}
	}()
	if err := e.svc.EndForConnectionTx(adminCtx, sqlc.New(tx), conn, true, false); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count("SELECT count(*) FROM risk_acceptances WHERE id = $1 AND end_reason = 'condition_changed'", a.ID); n != 1 {
		t.Fatal("not ended")
	}
	if d := e.auditDetails("risk.acceptance_ended", a.ID); d == nil || d["end_reason"] != "condition_changed" {
		t.Fatalf("audit %v", d)
	}
	// A change back is open again (never silently covered).
	fs, err = e.svc.Findings(adminCtx)
	if err != nil {
		t.Fatal(err)
	}
	if f := find(fs, riskaccept.KindConnectionTLS, conn); f == nil || f.Acceptance != nil {
		t.Fatalf("re-covered %+v", f)
	}
}
