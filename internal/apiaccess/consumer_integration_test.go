//go:build integration

package apiaccess_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"net/netip"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jlentink/bekup/internal/apiaccess"
	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/runstate"
)

// The external API's run-request consumer (D-220) against a real
// database: requests are filed as bekup_external through api.request_run
// (as `bekup external-api` does), tokens are created with the app-side
// queries.

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

// counts records the metrics outcomes.
type counts struct {
	mu sync.Mutex
	n  map[string]int
}

func (c *counts) RunRequestDecided(o string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n[o]++
}

func (c *counts) get(o string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n[o]
}

type env struct {
	t       *testing.T
	ctx     context.Context
	d       dbtest.DB
	pool    *pgxpool.Pool
	q, extQ *sqlc.Queries
	svc     *jobs.Service
	metrics *counts
	// alerting audits like serve and the workers: with the notify
	// observer, so audited denials raise their security alerts.
	alerting *audit.Auditor
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	t.Cleanup(cancel)
	d := dbtest.Migrated(t)
	e := &env{t: t, ctx: ctx, d: d, pool: d.AppPool(t), metrics: &counts{n: map[string]int{}}}
	e.q, e.extQ = sqlc.New(e.pool), sqlc.New(d.ExternalPool(t))
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kr.Destroy)
	auditor := audit.New(e.pool, audit.Options{Registry: logging.NewRegistry()})
	e.alerting = audit.New(e.pool, audit.Options{Registry: logging.NewRegistry(), Observer: notify.ObserveAudit})
	policy := authz.New(authz.Options{Ownership: jobs.Ownership{DB: e.pool}})
	keys, err := jobkeys.New(jobkeys.Options{DB: e.pool, Keyring: kr, Auditor: auditor, Authorizer: policy})
	if err != nil {
		t.Fatal(err)
	}
	qc, err := queue.New(e.pool, queue.Options{Settings: queue.SettingsFromConfig(config.DefaultBekup().Workers), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	if e.svc, err = jobs.New(jobs.Options{DB: e.pool, Auditor: auditor, Authorizer: policy, Keys: keys, Queue: qc, Canceler: qc}); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) consumer(bus *pubsub.Bus, sweep time.Duration) *apiaccess.Consumer {
	e.t.Helper()
	c, err := apiaccess.NewConsumer(apiaccess.ConsumerOptions{
		DB: e.pool, Runner: e.svc, Auditor: e.alerting, Bus: bus, SweepInterval: sweep, Metrics: e.metrics,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.ctx, sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

func (e *env) user(role sqlc.UserRole) uuid.UUID {
	e.t.Helper()
	u, err := e.q.InsertUser(e.ctx, sqlc.InsertUserParams{Email: uuid.NewString()[:8] + "@example.com", Role: role})
	if err != nil {
		e.t.Fatal(err)
	}
	return u.ID
}

func (e *env) job(owners ...uuid.UUID) uuid.UUID {
	e.t.Helper()
	id, err := dbtest.InsertJob(e.ctx, e.pool, dbtest.JobRow{Enabled: true})
	if err != nil {
		e.t.Fatal(err)
	}
	for _, o := range owners {
		e.exec("INSERT INTO job_owners (job_id, user_id) VALUES ($1, $2)", id, o)
	}
	return id
}

type token struct {
	id   uuid.UUID
	name string
	hash []byte
}

func (e *env) token(createdBy uuid.UUID, jobIDs ...uuid.UUID) token {
	e.t.Helper()
	return e.tokenFrom(createdBy, nil, jobIDs...)
}

// tokenFrom creates a token limited to cidrs (nil: any address).
func (e *env) tokenFrom(createdBy uuid.UUID, cidrs []netip.Prefix, jobIDs ...uuid.UUID) token {
	e.t.Helper()
	tok := token{id: uuid.New(), hash: make([]byte, 32)}
	tok.name = "ci " + tok.id.String()[:8]
	if _, err := rand.Read(tok.hash); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.q.InsertAPIToken(e.ctx, sqlc.InsertAPITokenParams{
		ID: tok.id, Name: tok.name, SecretHash: tok.hash, CreatedBy: createdBy, ExpiresAt: new(time.Now().Add(30 * 24 * time.Hour)),
		AllowedCidrs: cidrs, AllowAnyAddress: cidrs == nil,
	}); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.q.InsertAPITokenJobs(e.ctx, sqlc.InsertAPITokenJobsParams{TokenID: tok.id, JobIds: jobIDs}); err != nil {
		e.t.Fatal(err)
	}
	return tok
}

var clientIP = netip.MustParseAddr("2001:db8::7")

// request files a run request as bekup_external (api.request_run) and
// returns its id.
func (e *env) request(tok token, job uuid.UUID) uuid.UUID {
	e.t.Helper()
	ip := clientIP
	r, err := e.extQ.ExternalRequestRun(e.ctx, sqlc.ExternalRequestRunParams{TokenID: tok.id, SecretHash: tok.hash, JobID: job, Ip: &ip})
	if err != nil {
		e.t.Fatalf("request_run: %v", err)
	}
	if r.Refusal != nil || r.RequestID == nil {
		e.t.Fatalf("request refused: %v", *r.Refusal)
	}
	return *r.RequestID
}

type decision struct {
	outcome, reason string
	riverJob        *int64
	run             *uuid.UUID
}

func (e *env) decision(id uuid.UUID) decision {
	e.t.Helper()
	var d decision
	var reason *string
	if err := e.pool.QueryRow(e.ctx, `SELECT outcome::text, reason, river_job_id, run_id FROM run_requests WHERE id = $1`, id).
		Scan(&d.outcome, &reason, &d.riverJob, &d.run); err != nil {
		e.t.Fatal(err)
	}
	if reason != nil {
		d.reason = *reason
	}
	return d
}

func (e *env) wantRejected(id uuid.UUID, reason apiaccess.Reason) {
	e.t.Helper()
	if d := e.decision(id); d.outcome != "rejected" || d.reason != string(reason) {
		e.t.Fatalf("request %s: %+v, want rejected %s", id, d, reason)
	}
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(e.ctx, sql, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// TestConsumerAccepts: token (app side) -> api.request_run (bekup_external)
// -> consumer -> River backup_run + queued run with trigger api, audited
// with actor kind api; the run history names the token.
func TestConsumerAccepts(t *testing.T) {
	e := newEnv(t)
	admin := e.user(sqlc.UserRoleAdmin)
	job := e.job()
	tok := e.token(admin, job)
	req := e.request(tok, job)

	e.consumer(nil, 0).Drain(e.ctx)
	d := e.decision(req)
	if d.outcome != "accepted" || d.riverJob == nil || d.run == nil {
		t.Fatalf("decision %+v", d)
	}
	var args []byte
	var kind, state string
	if err := e.pool.QueryRow(e.ctx, `SELECT kind, state::text, args FROM river_job WHERE id = $1`, *d.riverJob).Scan(&kind, &state, &args); err != nil {
		t.Fatal(err)
	}
	var a queue.BackupRunArgs
	if err := json.Unmarshal(args, &a); err != nil {
		t.Fatal(err)
	}
	if kind != queue.KindBackupRun || state != "available" || a.JobID != job || a.Trigger != queue.TriggerAPI || a.TriggeredBy != nil {
		t.Fatalf("river job %s %s %+v", kind, state, a)
	}
	run, err := runstate.NewReader(e.pool).Run(e.ctx, *d.run)
	if err != nil {
		t.Fatal(err)
	}
	if run.Trigger != sqlc.RunTriggerApi || run.Status != runstate.StatusQueued || *run.RiverJobID != *d.riverJob ||
		run.TriggeredBy != nil || run.APITokenID == nil || *run.APITokenID != tok.id || run.APITokenName != tok.name ||
		run.ActorUserID == nil || *run.ActorUserID != admin {
		t.Fatalf("run view %+v", run)
	}

	var kindA string
	var actor *uuid.UUID
	var ip *netip.Addr
	var details []byte
	if err := e.pool.QueryRow(e.ctx, `SELECT actor_kind, actor_user_id, ip, details FROM audit_log
		WHERE action = 'job.run_requested' AND target_id = $1`, job.String()).Scan(&kindA, &actor, &ip, &details); err != nil {
		t.Fatal(err)
	}
	var det map[string]any
	if err := json.Unmarshal(details, &det); err != nil {
		t.Fatal(err)
	}
	if kindA != "api" || actor != nil || ip == nil || *ip != clientIP || det["api_token_id"] != tok.id.String() ||
		det["api_token_name"] != tok.name || det["request_id"] != req.String() || det["run_id"] != d.run.String() {
		t.Fatalf("audit %s %v %v %v", kindA, actor, ip, det)
	}
	if e.metrics.get(apiaccess.OutcomeAccepted) != 1 {
		t.Fatalf("metrics %v", e.metrics.n)
	}
	// Decided requests are not claimed again.
	e.consumer(nil, 0).Drain(e.ctx)
	if n := e.count(`SELECT count(*) FROM runs WHERE job_id = $1`, job); n != 1 {
		t.Fatalf("%d runs", n)
	}
}

// TestConsumerRejections: run_active, job_disabled, expired, revoked,
// demoted (token_invalid / job_gone), deleted job.
func TestConsumerRejections(t *testing.T) {
	e := newEnv(t)
	admin := e.user(sqlc.UserRoleAdmin)
	owner := e.user(sqlc.UserRoleUser)
	c := e.consumer(nil, 0)

	// Two pending requests for one job: the second finds the first's run.
	busy := e.job()
	tb := e.token(admin, busy)
	first, second := e.request(tb, busy), e.request(tb, busy)
	c.Drain(e.ctx)
	if d := e.decision(first); d.outcome != "accepted" {
		t.Fatalf("first %+v", d)
	}
	e.wantRejected(second, apiaccess.ReasonRunActive)

	// Disabled after the request was filed.
	off := e.job()
	toff := e.token(admin, off)
	r := e.request(toff, off)
	e.exec("UPDATE jobs SET enabled = false WHERE id = $1", off)
	c.Drain(e.ctx)
	e.wantRejected(r, apiaccess.ReasonJobDisabled)

	// Older than 10 minutes (by the database clock).
	late := e.job()
	tl := e.token(admin, late)
	r = e.request(tl, late)
	if err := e.d.SuperExec(e.ctx, "SET session_replication_role = replica",
		"UPDATE run_requests SET created_at = now() - interval '11 minutes' WHERE id = '"+r.String()+"'"); err != nil {
		t.Fatal(err)
	}
	c.Drain(e.ctx)
	e.wantRejected(r, apiaccess.ReasonExpired)

	// Revoked after the request.
	rev := e.job()
	trev := e.token(admin, rev)
	r = e.request(trev, rev)
	if _, err := e.q.RevokeAPIToken(e.ctx, sqlc.RevokeAPITokenParams{ID: trev.id, RevokedBy: &admin}); err != nil {
		t.Fatal(err)
	}
	c.Drain(e.ctx)
	e.wantRejected(r, apiaccess.ReasonTokenInvalid)

	// A user's token: demoted to maintainer -> token_invalid.
	owned := e.job(owner)
	town := e.token(owner, owned)
	r = e.request(town, owned)
	e.exec("UPDATE users SET role = 'maintainer' WHERE id = $1", owner)
	c.Drain(e.ctx)
	e.wantRejected(r, apiaccess.ReasonTokenInvalid)

	// An admin's token for a job the admin does not own: demoted to user
	// -> the token stays valid but the job leaves its scope -> job_gone.
	admin2 := e.user(sqlc.UserRoleAdmin)
	foreign := e.job()
	tf := e.token(admin2, foreign)
	r = e.request(tf, foreign)
	e.exec("UPDATE users SET role = 'user' WHERE id = $1", admin2)
	c.Drain(e.ctx)
	e.wantRejected(r, apiaccess.ReasonJobGone)

	// Deleted job.
	gone := e.job()
	tg := e.token(admin, gone)
	r = e.request(tg, gone)
	e.exec("UPDATE jobs SET deleted_at = now(), enabled = false, next_run_at = NULL WHERE id = $1", gone)
	c.Drain(e.ctx)
	e.wantRejected(r, apiaccess.ReasonJobGone)

	// Nothing but the accepted request started a run or wrote an audit
	// entry.
	if n := e.count(`SELECT count(*) FROM runs`); n != 1 {
		t.Fatalf("%d runs", n)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'job.run_requested'`); n != 1 {
		t.Fatalf("%d job.run_requested entries", n)
	}
	for _, o := range []string{"run_active", "job_disabled", "expired"} {
		if e.metrics.get(o) != 1 {
			t.Errorf("metric %s = %d", o, e.metrics.get(o))
		}
	}
	if e.metrics.get("token_invalid") != 2 || e.metrics.get("job_gone") != 2 {
		t.Errorf("metrics %v", e.metrics.n)
	}
}

// TestTwoConsumersNoDoubleProcessing: concurrent consumers decide every
// request exactly once.
func TestTwoConsumersNoDoubleProcessing(t *testing.T) {
	e := newEnv(t)
	admin := e.user(sqlc.UserRoleAdmin)
	reqs := make([]uuid.UUID, 0, 45)
	for range 15 {
		a, b, c := e.job(), e.job(), e.job()
		tok := e.token(admin, a, b, c)
		reqs = append(reqs, e.request(tok, a), e.request(tok, b), e.request(tok, c))
	}
	var wg sync.WaitGroup
	for range 3 {
		c := e.consumer(nil, 0)
		wg.Go(func() { c.Drain(e.ctx) })
	}
	wg.Wait()
	for _, r := range reqs {
		if d := e.decision(r); d.outcome != "accepted" {
			t.Fatalf("request %s: %+v", r, d)
		}
	}
	if n := e.count(`SELECT count(*) FROM runs WHERE trigger = 'api'`); n != len(reqs) {
		t.Fatalf("%d runs for %d requests", n, len(reqs))
	}
	if n := e.count(`SELECT count(DISTINCT details->>'request_id') FROM audit_log WHERE action = 'job.run_requested' AND actor_kind = 'api'`); n != len(reqs) {
		t.Fatalf("%d audited requests", n)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'job.run_requested'`); n != len(reqs) {
		t.Fatalf("%d audit entries", n)
	}
	if e.metrics.get(apiaccess.OutcomeAccepted) != len(reqs) {
		t.Fatalf("metrics %v", e.metrics.n)
	}
}

// TestConsumerWakeup: a request is decided on its NOTIFY (no sweep), and
// forged notifications from bekup_external change nothing.
func TestConsumerWakeup(t *testing.T) {
	e := newEnv(t)
	bus := pubsub.NewBus()
	connect, err := db.NewConnector(config.DBCredentials{URL: []byte(e.d.AppURL)}, db.PoolOptions{
		MaxConns: 1, ApplicationName: "bekup-test-listener", Registry: logging.NewRegistry(), TLS: dbtest.TLS(),
	})
	if err != nil {
		t.Fatal(err)
	}
	l, err := pubsub.NewListener(pubsub.ListenerOptions{Connect: connect, Channels: []pubsub.Channel{pubsub.APIRequests}, Bus: bus})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(e.ctx)
	var wg sync.WaitGroup
	runErrs := make(chan error, 2)
	defer func() {
		cancel()
		wg.Wait()
		close(runErrs)
		for err := range runErrs {
			if err != nil {
				t.Error(err)
			}
		}
	}()
	wg.Go(func() { runErrs <- l.Run(ctx) })
	waitFor(t, "listener connected", l.Connected)
	admin := e.user(sqlc.UserRoleAdmin)
	idle, job := e.job(), e.job()
	tok := e.token(admin, idle, job)
	// A request filed before the consumer starts is picked up at start.
	early := e.request(tok, idle)
	c := e.consumer(bus, time.Hour)
	wg.Go(func() { runErrs <- c.Run(ctx) })
	waitFor(t, "start pass", func() bool { return e.decision(early).outcome == "accepted" })

	// Forged wake-ups (garbage, an unknown id, a real but foreign id)
	// only make the consumer look for pending rows.
	for _, p := range []string{"x", uuid.NewString(), early.String(), `{"action":"cancel"}`} {
		if _, err := e.d.ExternalPool(t).Exec(e.ctx, "SELECT pg_notify('bekup_api_requests', $1)", p); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(500 * time.Millisecond)
	if n := e.count(`SELECT count(*) FROM runs`); n != 1 {
		t.Fatalf("%d runs after forged notifications", n)
	}
	// The real NOTIFY of api.request_run wakes it (the sweep is an hour).
	r := e.request(tok, job)
	waitFor(t, "notified request", func() bool { return e.decision(r).outcome == "accepted" })

	// So does the denial of a valid token from a refused address
	// ('denial:<id>'): audited and alerted without a sweep.
	limited := e.tokenFrom(admin, []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}, job)
	e.refused(limited, netip.MustParseAddr("198.51.100.20"))
	waitFor(t, "notified denial", func() bool {
		return e.count(`SELECT count(*) FROM api_access_denials WHERE token_id = $1 AND processed_at IS NOT NULL`, limited.id) == 1
	})
}

// refused calls api.authenticate for tok from ip as bekup_external and
// expects the generic refusal.
func (e *env) refused(tok token, ip netip.Addr) {
	e.t.Helper()
	if _, err := e.extQ.ExternalAuthenticate(e.ctx, sqlc.ExternalAuthenticateParams{TokenID: tok.id, SecretHash: tok.hash, ClientIp: &ip}); !errors.Is(err, pgx.ErrNoRows) {
		e.t.Fatalf("authenticate from %s: %v", ip, err)
	}
}

// TestConsumerDenials: an access denial becomes one api_token.ip_denied
// audit entry (actor kind api) and the api_token_ip_denied security
// alerts to the admins and the token's creator, once, also with two
// consumers; the denial is marked processed and pruned after 30 days.
func TestConsumerDenials(t *testing.T) {
	e := newEnv(t)
	creator := e.user(sqlc.UserRoleUser)
	job := e.job(creator)
	tok := e.tokenFrom(creator, []netip.Prefix{netip.MustParsePrefix("2001:db8:1::/48")}, job)
	from := netip.MustParseAddr("::ffff:198.51.100.7")
	e.refused(tok, from)
	e.refused(tok, from) // within 10 minutes: no second denial
	var denial uuid.UUID
	if err := e.pool.QueryRow(e.ctx, `SELECT id FROM api_access_denials WHERE token_id = $1`, tok.id).Scan(&denial); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for range 2 {
		c := e.consumer(nil, 0)
		wg.Go(func() { c.Drain(e.ctx) })
	}
	wg.Wait()
	if n := e.count(`SELECT count(*) FROM api_access_denials WHERE id = $1 AND processed_at IS NOT NULL`, denial); n != 1 {
		t.Fatal("denial not processed")
	}

	var kind, targetType, targetID string
	var actor *uuid.UUID
	var ip *netip.Addr
	var details []byte
	if err := e.pool.QueryRow(e.ctx, `SELECT actor_kind, actor_user_id, ip, target_type, target_id, details FROM audit_log
		WHERE action = 'api_token.ip_denied'`).Scan(&kind, &actor, &ip, &targetType, &targetID, &details); err != nil {
		t.Fatal(err)
	}
	var det map[string]any
	if err := json.Unmarshal(details, &det); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"api_token_id": tok.id.String(), "api_token_name": tok.name, "denial_id": denial.String(),
		"creator_id": creator.String(), "client_ip": "198.51.100.7",
	}
	if kind != "api" || actor != nil || ip == nil || *ip != netip.MustParseAddr("198.51.100.7") ||
		targetType != apiaccess.AuditTargetType || targetID != tok.id.String() || len(det) != len(want) {
		t.Fatalf("audit %s %v %v %s %s %v", kind, actor, ip, targetType, targetID, det)
	}
	for k, v := range want {
		if det[k] != v {
			t.Errorf("detail %s = %v, want %v", k, det[k], v)
		}
	}

	// The security alerts: one to the admins, one to the creator.
	rows, err := e.pool.Query(e.ctx, `SELECT type, severity, user_id, detail FROM notification_events
		WHERE detail->>'kind' = 'api_token_ip_denied' ORDER BY type`)
	if err != nil {
		t.Fatal(err)
	}
	type alert struct {
		typ, sev string
		user     *uuid.UUID
		detail   map[string]any
	}
	alerts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (alert, error) {
		var a alert
		var raw []byte
		if err := r.Scan(&a.typ, &a.sev, &a.user, &raw); err != nil {
			return a, err
		}
		return a, json.Unmarshal(raw, &a.detail)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 2 || alerts[0].typ != string(notify.SecurityAccount) || alerts[0].user == nil || *alerts[0].user != creator ||
		alerts[1].typ != string(notify.SecuritySystem) || alerts[1].user != nil {
		t.Fatalf("alerts %+v", alerts)
	}
	for _, a := range alerts {
		if a.sev != "error" || a.detail["ip"] != "198.51.100.7" || a.detail["api_name"] != tok.name || a.detail["api_id"] != tok.id.String() {
			t.Errorf("alert %+v", a)
		}
	}

	// Processed denials are not processed again.
	e.consumer(nil, 0).Drain(e.ctx)
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'api_token.ip_denied'`); n != 1 {
		t.Fatalf("%d audit entries", n)
	}

	// Pruned after 30 days (processed ones only).
	e.refused(e.tokenFrom(creator, []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}, job), from)
	w := &apiaccess.PruneWorker{DB: e.pool, Now: func() time.Time { return time.Now().Add(apiaccess.RequestRetention + time.Hour) }}
	if err := w.Work(e.ctx, nil); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM api_access_denials`); n != 1 {
		t.Fatalf("%d denials left, want the unprocessed one", n)
	}
	if n := e.count(`SELECT count(*) FROM api_access_denials WHERE id = $1`, denial); n != 0 {
		t.Fatal("processed denial not pruned")
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestPrune: decided requests older than 30 days are deleted, pending
// ones are kept.
func TestPrune(t *testing.T) {
	e := newEnv(t)
	admin := e.user(sqlc.UserRoleAdmin)
	a, b := e.job(), e.job()
	tok := e.token(admin, a, b)
	decided := e.request(tok, a)
	e.consumer(nil, 0).Drain(e.ctx)
	pending := e.request(tok, b)
	w := &apiaccess.PruneWorker{DB: e.pool}
	if err := w.Work(e.ctx, nil); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM run_requests`); n != 2 {
		t.Fatalf("fresh requests pruned: %d left", n)
	}
	w.Now = func() time.Time { return time.Now().Add(apiaccess.RequestRetention + time.Hour) }
	if err := w.Work(e.ctx, nil); err != nil {
		t.Fatal(err)
	}
	var left []uuid.UUID
	rows, err := e.pool.Query(e.ctx, `SELECT id FROM run_requests`)
	if err != nil {
		t.Fatal(err)
	}
	left, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0] != pending || left[0] == decided {
		t.Fatalf("left %v", left)
	}
}
