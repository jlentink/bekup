//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pelletier/go-toml/v2"

	"github.com/jlentink/bekup/internal/app"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz/authztest"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/configreload"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/pubsub"
)

// reloadEnv runs a serve and a worker process from one bekup.toml (task
// 6.13, D-200).
type reloadEnv struct {
	t      *testing.T
	db     dbtest.DB
	pool   *pgxpool.Pool
	path   string
	cfg    *config.Bekup
	base   app.Options
	res    *config.Result[config.Bekup]
	serve  *app.App
	worker *app.App
	// stopWorker stops the worker process (idempotent).
	stopWorker func()
	slogs      *syncBuffer
	wlogs      *syncBuffer
	admin      context.Context
}

func writeTOML(t *testing.T, path string, cfg *config.Bekup) {
	t.Helper()
	b, err := toml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func isolatedLoad(path string) config.Options {
	return config.Options{
		ConfigPath: path, Dev: true,
		LookupEnv: func(string) (string, bool) { return "", false },
		Environ:   func() []string { return nil },
	}
}

func newReloadEnv(t *testing.T) *reloadEnv {
	t.Helper()
	d := dbtest.Migrated(t)
	e := &reloadEnv{t: t, db: d, pool: d.AppPool(t), slogs: &syncBuffer{}, wlogs: &syncBuffer{}}
	base := options(t, d.AppURL, e.slogs)
	// A loadable file: concrete addresses (serve gets a bound listener),
	// no metrics endpoint.
	base.Config.Server.Listen = "127.0.0.1:18080"
	base.Config.Metrics.Enabled = false
	e.path = filepath.Join(t.TempDir(), "bekup.toml")
	writeTOML(t, e.path, base.Config)
	res, err := config.LoadBekup(isolatedLoad(e.path))
	if err != nil {
		t.Fatal(err)
	}
	e.cfg, e.base, e.res = res.Config, base, res
	e.serve, _ = e.start(app.ModeServe, false, e.slogs)
	e.worker, e.stopWorker = e.start(app.ModeWorker, false, e.wlogs)
	// Both LISTEN connections up (the broadcast reaches both processes).
	waitFor(t, "both listeners", func() bool { return e.listeners() == 2 })
	time.Sleep(300 * time.Millisecond)

	u, err := sqlc.New(e.pool).InsertUser(t.Context(), sqlc.InsertUserParams{Email: "admin@example.com", DisplayName: "Admin", Role: sqlc.UserRoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	p := authztest.Principal(auth.RoleAdmin)
	p.UserID = u.ID
	e.admin = authztest.Context(t.Context(), p)
	return e
}

// start runs one more process from the env's bekup.toml (serve with
// noWorker: serve --no-worker); stop ends it (idempotent).
func (e *reloadEnv) start(mode app.Mode, noWorker bool, logs *syncBuffer) (*app.App, func()) {
	t := e.t
	t.Helper()
	o := e.base
	o.Config = e.res.Config.Clone()
	o.Mode, o.NoWorker = mode, noWorker
	o.Logger = slogFor(logs)
	// log.level is applied only by a process with a level to set.
	o.LogLevel = new(slog.LevelVar)
	o.LoadOptions = isolatedLoad(e.path)
	o.ConfigPath = e.path
	o.Warnings = e.res.Warnings
	if mode == app.ModeWorker {
		o.UI = nil
	} else {
		var lc net.ListenConfig
		ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		o.Listener = ln
	}
	return startReloadApp(t, o)
}

func startReloadApp(t *testing.T, o app.Options) (*app.App, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	a, err := app.New(ctx, o)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				t.Error("process did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return a, stop
}

func slogFor(logs *syncBuffer) *slog.Logger { return slog.New(slog.NewJSONHandler(logs, nil)) }

func (e *reloadEnv) listeners() int {
	ids, err := configreload.Listening(e.t.Context(), sqlc.New(e.pool))
	if err != nil {
		e.t.Fatal(err)
	}
	return len(ids)
}

func (e *reloadEnv) results(id uuid.UUID) []sqlc.ConfigReloadResult {
	rows, err := sqlc.New(e.pool).ConfigReloadResultsByRequest(e.t.Context(), &id)
	if err != nil {
		e.t.Fatal(err)
	}
	out := make([]sqlc.ConfigReloadResult, len(rows))
	for i := range rows {
		out[i] = rows[i].ConfigReloadResult
	}
	return out
}

func (e *reloadEnv) edit(mutate func(c *config.Bekup)) {
	c := e.cfg.Clone()
	mutate(c)
	writeTOML(e.t, e.path, c)
}

func resultOf(rows []sqlc.ConfigReloadResult, kind string) *sqlc.ConfigReloadResult {
	for i := range rows {
		if rows[i].ProcessKind == kind {
			return &rows[i]
		}
	}
	return nil
}

// TestConfigReloadBroadcast: an admin's request reloads serve at once and
// the worker through NOTIFY; both apply the live key and report the
// restart-only one; the startup rows register both processes.
func TestConfigReloadBroadcast(t *testing.T) {
	e := newReloadEnv(t)
	st, err := app.ReloadAPI(e.serve).Status(e.admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Processes) != 2 || st.Listening != 2 || st.LastRequest != nil || len(st.LiveKeys) == 0 {
		t.Fatalf("status before %+v", st)
	}
	for _, p := range st.Processes {
		if !p.Listening || p.Status != configreload.StatusLoaded || p.Trigger != "" {
			t.Fatalf("startup row %+v", p)
		}
	}
	e.edit(func(c *config.Bekup) {
		c.Log.Level = "debug"
		c.Workers.MaxConcurrentRuns = 7 // restart-only
	})
	v, err := app.ReloadAPI(e.serve).Request(e.admin)
	if err != nil {
		t.Fatal(err)
	}
	// The returned view has the local answer.
	if !slices.ContainsFunc(v.Results, func(r configreload.ProcessResult) bool { return r.Kind == configreload.KindServe }) {
		t.Fatalf("no local answer in %+v", v)
	}
	if v.Expected != 2 || v.Listening != 2 {
		t.Fatalf("expected %d listening %d", v.Expected, v.Listening)
	}
	waitFor(t, "the worker's answer", func() bool { return len(e.results(v.ID)) == 2 })
	byID, err := app.ReloadAPI(e.serve).RequestByID(e.admin, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range byID.Results {
		if r.Trigger != configreload.TriggerUI || !r.Listening {
			t.Fatalf("answer %+v", r)
		}
	}
	for _, kind := range []string{"serve", "worker"} {
		r := resultOf(e.results(v.ID), kind)
		if r == nil || r.Status != "restart_required" || !slices.Equal(r.AppliedKeys, []string{"log.level"}) ||
			!slices.Equal(r.RestartKeys, []string{"workers.max_concurrent_runs"}) {
			t.Fatalf("%s result %+v", kind, r)
		}
	}
	if e.worker.Reloader().Running().Log.Level != "debug" || e.serve.Reloader().Running().Workers.MaxConcurrentRuns == 7 {
		t.Fatal("running configs not as expected")
	}
	// A second request within 10 s is rate limited.
	if _, err := app.ReloadAPI(e.serve).Request(e.admin); err == nil || !strings.Contains(err.Error(), "10 seconds") {
		t.Fatalf("second request: %v", err)
	}
	// Audit: one request entry, two reload entries, names only.
	var n int
	if err := e.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_log WHERE action = 'config.reloaded' AND details->>'request_id' = $1`, v.ID.String()).Scan(&n); err != nil || n != 2 {
		t.Fatalf("config.reloaded entries %d %v", n, err)
	}
	// The key names survive the audit redaction (no "*_keys" names).
	var applied, restart string
	if err := e.pool.QueryRow(t.Context(), `SELECT min(details->>'applied'), min(details->>'restart_required') FROM audit_log
		WHERE action = 'config.reloaded' AND details->>'request_id' = $1`, v.ID.String()).Scan(&applied, &restart); err != nil ||
		applied != `["log.level"]` || restart != `["workers.max_concurrent_runs"]` {
		t.Fatalf("config.reloaded details applied %q restart %q %v", applied, restart, err)
	}
	var details string
	if err := e.pool.QueryRow(t.Context(), `SELECT string_agg(details::text, ' ') FROM audit_log WHERE action LIKE 'config.%'`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(details, "REDACTED") {
		t.Fatalf("audit details redacted a key name: %s", details)
	}
	if strings.Contains(details, e.path) || strings.Contains(details, "debug") {
		t.Fatalf("audit details carry a value or the path: %s", details)
	}
}

// TestConfigReloadIgnoresForgedNotify: a NOTIFY without a matching recent
// UI request row (or naming a signal request) does nothing.
func TestConfigReloadIgnoresForgedNotify(t *testing.T) {
	e := newReloadEnv(t)
	e.edit(func(c *config.Bekup) { c.Log.Level = "debug" })
	q := sqlc.New(e.pool)
	forged := uuid.New()
	signal, err := q.InsertConfigReloadSignalRequest(t.Context(), sqlc.InsertConfigReloadSignalRequestParams{ID: uuid.New(), InstanceID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{forged, signal.ID} {
		if err := q.Notify(t.Context(), sqlc.NotifyParams{Channel: pubsub.SettingsEvents.Name, Payload: configreload.PayloadPrefix + id.String()}); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "the ignore log", func() bool {
		return strings.Count(e.wlogs.String(), "ignoring a config reload notification") >= 2
	})
	if len(e.results(forged)) != 0 || len(e.results(signal.ID)) != 0 {
		t.Fatal("a forged notification was answered")
	}
	if e.worker.Reloader().Running().Log.Level != "info" {
		t.Fatal("a forged notification changed the config")
	}
}

// TestConfigReloadCatchUp: a request whose NOTIFY was lost is answered
// after the listener reconnects (Resync).
func TestConfigReloadCatchUp(t *testing.T) {
	e := newReloadEnv(t)
	e.edit(func(c *config.Bekup) { c.Log.Level = "warn" })
	u, err := sqlc.New(e.pool).GetUserByEmail(t.Context(), "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	req, err := sqlc.New(e.pool).InsertConfigReloadUIRequest(t.Context(), sqlc.InsertConfigReloadUIRequestParams{RequestedBy: u.ID, InstanceID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	// Drop the worker's LISTEN connection: it reconnects and resyncs.
	if _, err := e.pool.Exec(t.Context(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name LIKE $1 || ':%'`, app.WorkerListenerApplicationName); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the catch-up answer", func() bool { return resultOf(e.results(req.ID), "worker") != nil })
	if r := resultOf(e.results(req.ID), "worker"); r.Status != "applied" || e.worker.Reloader().Running().Log.Level != "warn" {
		t.Fatalf("catch-up result %+v", r)
	}
}

// TestConfigReloadReplayAppliesNothing: a request this process has an
// answer row for already (but no longer remembers) rolls back: no second
// audit entry, nothing applied.
func TestConfigReloadReplayAppliesNothing(t *testing.T) {
	e := newReloadEnv(t)
	u, err := sqlc.New(e.pool).GetUserByEmail(t.Context(), "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	req, err := sqlc.New(e.pool).InsertConfigReloadUIRequest(t.Context(), sqlc.InsertConfigReloadUIRequestParams{RequestedBy: u.ID, InstanceID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	w := e.worker.Reloader()
	if _, err := sqlc.New(e.pool).InsertConfigReloadResult(t.Context(), sqlc.InsertConfigReloadResultParams{
		RequestID: &req.ID, InstanceID: w.InstanceID(), ProcessKind: "worker", Hostname: "h", Version: "v",
		ProcessStartedAt: w.StartedAt(), Status: "unchanged",
		AppliedKeys: []string{}, RestartKeys: []string{}, ErrorKeys: []string{}, WeakenedKeys: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	e.edit(func(c *config.Bekup) { c.Log.Level = "warn" })
	res, err := w.Reload(t.Context(), configreload.Request{ID: req.ID, Trigger: configreload.TriggerUI})
	if err != nil || !res.AlreadyAnswered {
		t.Fatalf("replay %+v %v", res, err)
	}
	if w.Running().Log.Level != "info" {
		t.Fatal("a replayed request applied the file")
	}
	var n int
	if err := e.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_log WHERE action LIKE 'config.reload%' AND details->>'request_id' = $1`,
		req.ID.String()).Scan(&n); err != nil || n != 0 {
		t.Fatalf("audit entries for the replay %d %v", n, err)
	}
}

// TestConfigReloadWeakeningAlertsOnce: a widened bootstrap SSRF list is
// applied by both processes and raises exactly one security.system alert.
func TestConfigReloadWeakeningAlertsOnce(t *testing.T) {
	e := newReloadEnv(t)
	e.edit(func(c *config.Bekup) { c.Netguard.AllowCIDRs = []string{"10.42.0.0/16"} })
	v, err := app.ReloadAPI(e.serve).Request(e.admin)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "both answers", func() bool { return len(e.results(v.ID)) == 2 })
	for _, r := range e.results(v.ID) {
		if r.Status != "applied" || !slices.Equal(r.WeakenedKeys, []string{"netguard.allow_cidrs"}) {
			t.Fatalf("result %+v", r)
		}
	}
	var applied, weakened string
	if err := e.pool.QueryRow(t.Context(), `SELECT min(details->>'applied'), min(details->>'weakened') FROM audit_log
		WHERE action = 'config.reloaded' AND details->>'request_id' = $1`, v.ID.String()).Scan(&applied, &weakened); err != nil ||
		applied != `["netguard.allow_cidrs"]` || weakened != `["netguard.allow_cidrs"]` {
		t.Fatalf("config.reloaded details applied %q weakened %q %v", applied, weakened, err)
	}
	var n int
	var detail json.RawMessage
	if err := e.pool.QueryRow(t.Context(), `SELECT count(*), max(detail::text)::jsonb FROM notification_events
		WHERE type = 'security.system' AND detail->>'kind' = 'config_reload_weakened'`).Scan(&n, &detail); err != nil {
		t.Fatal(err)
	}
	if n != 1 || !strings.Contains(string(detail), `"settings": "netguard.allow_cidrs"`) || strings.Contains(string(detail), "10.42") {
		t.Fatalf("alerts %d %s", n, detail)
	}
}

// TestConfigReloadRejectedAuditNames: a rejected file is audited by every
// process with the key names in error_settings (never redacted).
func TestConfigReloadRejectedAuditNames(t *testing.T) {
	e := newReloadEnv(t)
	b, err := toml.Marshal(e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "[log]\n") {
		t.Fatalf("no [log] table in %s", b)
	}
	if err := os.WriteFile(e.path, []byte(strings.Replace(string(b), "[log]\n", "[log]\nverbosity = 'loud'\n", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := app.ReloadAPI(e.serve).Request(e.admin)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "both answers", func() bool { return len(e.results(v.ID)) == 2 })
	for _, r := range e.results(v.ID) {
		if r.Status != "rejected" || !slices.Equal(r.ErrorKeys, []string{"log.unknown_key"}) {
			t.Fatalf("result %+v", r)
		}
	}
	rows, err := e.pool.Query(t.Context(), `SELECT details->>'error_settings', details::text FROM audit_log
		WHERE action = 'config.reload_failed' AND details->>'request_id' = $1`, v.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var names, all string
		if err := rows.Scan(&names, &all); err != nil {
			t.Fatal(err)
		}
		if names != `["log.unknown_key"]` || strings.Contains(all, "REDACTED") || strings.Contains(all, "loud") {
			t.Fatalf("config.reload_failed details %s", all)
		}
		n++
	}
	if err := rows.Err(); err != nil || n != 2 {
		t.Fatalf("config.reload_failed entries %d %v", n, err)
	}
}

// TestConfigReloadLiveness: the status lists every listening process at
// any age, keeps stopped ones for 7 days marked as not listening, and
// pruning keeps the newest row of a listening process.
func TestConfigReloadLiveness(t *testing.T) {
	e := newReloadEnv(t)
	serveID, workerID := e.serve.Reloader().InstanceID(), e.worker.Reloader().InstanceID()
	e.stopWorker()
	waitFor(t, "the worker's listener to close", func() bool { return e.listeners() == 1 })
	status := func() map[uuid.UUID]configreload.ProcessResult {
		st, err := app.ReloadAPI(e.serve).Status(e.admin)
		if err != nil {
			t.Fatal(err)
		}
		if st.Listening != 1 {
			t.Fatalf("listening %d", st.Listening)
		}
		out := map[uuid.UUID]configreload.ProcessResult{}
		for _, p := range st.Processes {
			out[p.InstanceID] = p
		}
		return out
	}
	ps := status()
	if !ps[serveID].Listening || ps[workerID].Listening || ps[workerID].Kind != configreload.KindWorker {
		t.Fatalf("processes %+v", ps)
	}
	backdate := func(id uuid.UUID, days int) {
		t.Helper()
		if err := e.db.SuperExec(t.Context(), fmt.Sprintf(
			`UPDATE config_reload_results SET finished_at = now() - interval '%d days' WHERE instance_id = '%s'`, days, id)); err != nil {
			t.Fatal(err)
		}
	}
	// 8 days without a report: the listening serve stays, the stopped
	// worker is gone.
	backdate(serveID, 8)
	backdate(workerID, 8)
	ps = status()
	if _, ok := ps[serveID]; !ok || len(ps) != 1 {
		t.Fatalf("after 8 days %+v", ps)
	}
	// 40 days: pruning keeps the listening process's newest row only.
	backdate(serveID, 40)
	backdate(workerID, 40)
	configreload.NewPruner(e.pool, nil, 0).Prune(t.Context())
	var serveRows, workerRows int
	if err := e.pool.QueryRow(t.Context(), `SELECT count(*) FILTER (WHERE instance_id = $1), count(*) FILTER (WHERE instance_id = $2)
		FROM config_reload_results`, serveID, workerID).Scan(&serveRows, &workerRows); err != nil {
		t.Fatal(err)
	}
	if serveRows != 1 || workerRows != 0 {
		t.Fatalf("after pruning: serve %d worker %d rows", serveRows, workerRows)
	}
	if _, ok := status()[serveID]; !ok {
		t.Fatal("the long-running serve is no longer listed")
	}
}

// TestConfigReloadExpectedProcesses: one rule (DB time on both sides)
// decides who answers a request: a process whose startup row is more than
// StartupGrace younger than the request listens but neither answers nor
// is waited for; one started within the grace answers it (catch-up at
// start) and is waited for.
func TestConfigReloadExpectedProcesses(t *testing.T) {
	e := newReloadEnv(t)
	u, err := sqlc.New(e.pool).GetUserByEmail(t.Context(), "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	insert := func() uuid.UUID {
		t.Helper()
		req, err := sqlc.New(e.pool).InsertConfigReloadUIRequest(t.Context(), sqlc.InsertConfigReloadUIRequestParams{RequestedBy: u.ID, InstanceID: uuid.New()})
		if err != nil {
			t.Fatal(err)
		}
		return req.ID
	}
	// The running serve and worker started 10 minutes ago, the request 2
	// minutes ago (still acted on): a process started now is outside the
	// grace, deterministically.
	if err := e.db.SuperExec(t.Context(), `UPDATE config_reload_results SET finished_at = now() - interval '10 minutes'`); err != nil {
		t.Fatal(err)
	}
	early := insert()
	if err := e.db.SuperExec(t.Context(), fmt.Sprintf(
		`UPDATE config_reload_requests SET requested_at = now() - interval '%d seconds' WHERE id = '%s'`,
		int((configreload.StartupGrace+time.Minute).Seconds()), early)); err != nil {
		t.Fatal(err)
	}
	late, _ := e.start(app.ModeWorker, false, &syncBuffer{})
	waitFor(t, "three listeners", func() bool { return e.listeners() == 3 })
	// A request just before a start (inside the grace) is answered by the
	// starting process's catch-up.
	recent := insert()
	inGrace, _ := e.start(app.ModeWorker, false, &syncBuffer{})
	waitFor(t, "four listeners", func() bool { return e.listeners() == 4 })
	waitFor(t, "the in-grace process's catch-up answer", func() bool {
		return slices.ContainsFunc(e.results(recent), func(r sqlc.ConfigReloadResult) bool {
			return r.InstanceID == inGrace.Reloader().InstanceID()
		})
	})
	v, err := app.ReloadAPI(e.serve).RequestByID(e.admin, early)
	if err != nil {
		t.Fatal(err)
	}
	if v.Listening != 4 || v.Expected != 2 || len(v.Results) != 0 {
		t.Fatalf("early request: listening %d expected %d results %d", v.Listening, v.Expected, len(v.Results))
	}
	// Run the late process's catch-up again: still not responsible.
	app.CatchUp(t.Context(), late)
	if len(e.results(early)) != 0 {
		t.Fatal("a process started after the request (beyond the grace) answered it")
	}
	v, err = app.ReloadAPI(e.serve).RequestByID(e.admin, recent)
	if err != nil {
		t.Fatal(err)
	}
	// serve, worker and the late worker started before it, the in-grace
	// worker within the grace: all four are waited for.
	if v.Expected != 4 {
		t.Fatalf("recent request: expected %d", v.Expected)
	}
}

// TestConfigReloadEveryApplierGroup: one key of every applier group is
// applied in serve, worker and serve --no-worker (the wiring of every
// applier); keys a process does not use are not reported by it.
func TestConfigReloadEveryApplierGroup(t *testing.T) {
	e := newReloadEnv(t)
	noWorker, _ := e.start(app.ModeServe, true, &syncBuffer{})
	waitFor(t, "three listeners", func() bool { return e.listeners() == 3 })
	time.Sleep(300 * time.Millisecond)
	e.edit(func(c *config.Bekup) {
		c.Log.Level = "debug"
		c.Server.MaxBodyBytes--
		c.GraphQL.MaxDepth--
		c.Netguard.AllowHosts = append(c.Netguard.AllowHosts, "s3.internal")
		c.Security.PasswordMinLength++
		c.Notifications.CollapseWindow = config.Duration(c.Notifications.CollapseWindow.D() + time.Minute)
		c.Retention.PartialCleanupAge = config.Duration(c.Retention.PartialCleanupAge.D() + time.Hour)
	})
	all := []string{
		"log.level", "server.max_body_bytes", "graphql.max_depth", "netguard.allow_hosts",
		"security.password_min_length", "notifications.collapse_window", "retention.partial_cleanup_age",
	}
	without := func(drop ...string) []string {
		return slices.DeleteFunc(slices.Clone(all), func(k string) bool { return slices.Contains(drop, k) })
	}
	want := map[uuid.UUID][]string{
		e.serve.Reloader().InstanceID():  all,
		e.worker.Reloader().InstanceID(): without("server.max_body_bytes", "graphql.max_depth", "security.password_min_length"),
		noWorker.Reloader().InstanceID(): without("retention.partial_cleanup_age"),
	}
	v, err := app.ReloadAPI(e.serve).Request(e.admin)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "three answers", func() bool { return len(e.results(v.ID)) == 3 })
	for _, r := range e.results(v.ID) {
		got := slices.Sorted(slices.Values(r.AppliedKeys))
		exp := slices.Sorted(slices.Values(want[r.InstanceID]))
		if r.Status != "applied" || r.ErrorKind != nil || !slices.Equal(got, exp) {
			t.Fatalf("%s (%s): status %s error %v applied %v, want %v", r.ProcessKind, r.InstanceID, r.Status, r.ErrorKind, got, exp)
		}
		wantWeak := []string{"netguard.allow_hosts"}
		if !slices.Equal(r.WeakenedKeys, wantWeak) {
			t.Fatalf("%s weakened %v", r.ProcessKind, r.WeakenedKeys)
		}
	}
	for _, a := range []*app.App{e.serve, e.worker, noWorker} {
		if a.Reloader().Running().Retention.PartialCleanupAge.D() != e.cfg.Retention.PartialCleanupAge.D()+time.Hour {
			t.Fatal("the running config of a process did not take the live keys")
		}
	}
	// Every applier group reached its component (one observable value
	// each, in the processes that run it).
	for _, p := range []struct {
		name            string
		a               *app.App
		http, retention bool
	}{{"serve", e.serve, true, true}, {"worker", e.worker, false, true}, {"serve --no-worker", noWorker, true, false}} {
		v := app.Live(p.a)
		if v.LogLevel != slog.LevelDebug {
			t.Errorf("%s: log level %v", p.name, v.LogLevel)
		}
		if !v.AllowsHost("s3.internal") {
			t.Errorf("%s: the SSRF guard does not allow the added host", p.name)
		}
		if v.CollapseWindow != e.cfg.Notifications.CollapseWindow.D()+time.Minute {
			t.Errorf("%s: collapse window %v", p.name, v.CollapseWindow)
		}
		if v.HTTP != p.http || v.Retention != p.retention {
			t.Fatalf("%s: http %v retention %v", p.name, v.HTTP, v.Retention)
		}
		if p.http && (v.MaxBodyBytes != e.cfg.Server.MaxBodyBytes-1 || v.MaxDepth != e.cfg.GraphQL.MaxDepth-1 ||
			v.PasswordMinLength != e.cfg.Security.PasswordMinLength+1) {
			t.Errorf("%s: body %d depth %d password length %d", p.name, v.MaxBodyBytes, v.MaxDepth, v.PasswordMinLength)
		}
		if p.retention && v.PartialCleanupAge != e.cfg.Retention.PartialCleanupAge.D()+time.Hour {
			t.Errorf("%s: partial cleanup age %v", p.name, v.PartialCleanupAge)
		}
	}
}
