//go:build integration

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/rivertype"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/authz/authztest"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/scheduler"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

type user struct {
	actor Actor
	pr    auth.Principal
	ctx   context.Context
}

type env struct {
	t        *testing.T
	d        dbtest.DB
	pool     *pgxpool.Pool
	q        *sqlc.Queries
	auditor  *audit.Auditor
	keys     *jobkeys.Service
	svc      *Service
	policy   *authz.Policy
	mu       sync.Mutex
	now      time.Time
	alerts   []Alert
	admin    user
	maint    user
	owner    user
	other    user
	pg       uuid.UUID
	my       uuid.UUID
	temp     uuid.UUID
	dest1    uuid.UUID
	dest2    uuid.UUID
	destGone uuid.UUID
}

func (e *env) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

func (e *env) setNow(t time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.now = t
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, d: dbtest.Migrated(t), now: time.Date(2026, 3, 28, 12, 0, 0, 0, time.UTC)}
	e.pool = e.d.AppPool(t)
	e.q = sqlc.New(e.pool)
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kr.Destroy)
	e.auditor = audit.New(e.pool, audit.Options{Registry: logging.NewRegistry()})
	e.policy = authz.New(authz.Options{Ownership: Ownership{DB: e.pool}})
	e.keys, err = jobkeys.New(jobkeys.Options{DB: e.pool, Keyring: kr, Auditor: e.auditor, Authorizer: e.policy})
	if err != nil {
		t.Fatal(err)
	}
	qc, err := queue.New(e.pool, queue.Options{Settings: queue.SettingsFromConfig(config.DefaultBekup().Workers), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	e.svc, err = New(Options{
		DB: e.pool, Auditor: e.auditor, Authorizer: e.policy, Keys: e.keys, Queue: qc, Canceler: qc,
		Limits: scheduler.Limits{MinInterval: 5 * time.Minute}, MaxTimeout: 6 * time.Hour, Now: e.clock,
		Alerts: func(_ context.Context, a Alert) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.alerts = append(e.alerts, a)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(email string, role auth.Role) user {
		u, err := e.q.InsertUser(t.Context(), sqlc.InsertUserParams{Email: email, DisplayName: email, Role: sqlc.UserRole(role)})
		if err != nil {
			t.Fatal(err)
		}
		pr := authztest.Principal(role)
		pr.UserID = u.ID
		return user{actor: Actor{UserID: u.ID}, pr: pr, ctx: authztest.Context(t.Context(), pr)}
	}
	e.admin = mk("admin@example.com", auth.RoleAdmin)
	e.maint = mk("maint@example.com", auth.RoleMaintainer)
	e.owner = mk("owner@example.com", auth.RoleUser)
	e.other = mk("other@example.com", auth.RoleUser)
	e.pg = e.insertConnection("pg", "postgres", false)
	e.my = e.insertConnection("my", "mysql", false)
	e.temp = e.insertConnection("temp", "postgres", true)
	e.dest1 = e.insertDestination("disk-1")
	e.dest2 = e.insertDestination("disk-2")
	e.destGone = e.insertDestination("gone")
	if _, err := e.pool.Exec(t.Context(), "UPDATE destinations SET deleted_at = now() WHERE id = $1", e.destGone); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) insertConnection(name, eng string, temporary bool) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	var expires *time.Time
	if temporary {
		x := time.Now().Add(time.Hour)
		expires = &x
	}
	if _, err := e.pool.Exec(e.t.Context(), `INSERT INTO connections (id, name, engine, version_key, image_key, host, port, username, temporary, expires_at)
		VALUES ($1, $2, $3, '17', '17', 'db.example.test', 5432, 'backup', $4, $5)`, id, name, eng, temporary, expires); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) insertDestination(name string) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	if _, err := e.pool.Exec(e.t.Context(), `INSERT INTO destinations (id, name, type, config) VALUES ($1, $2, 'disk', '{"path":"x"}')`, id, name); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func keep(n int) *int { return &n }

func daily(at, tz string) scheduler.Schedule {
	return scheduler.Schedule{Kind: scheduler.KindInterval, Every: 1, Unit: scheduler.UnitDays, Anchor: "2026-01-01T" + at, Timezone: tz}
}

func (e *env) input(name string) CreateInput {
	return CreateInput{
		Name: name, ConnectionID: e.pg, Schedule: daily("03:00", "Europe/Amsterdam"),
		Retention: Retention{KeepLast: keep(7)}, DestinationIDs: []uuid.UUID{e.dest1},
	}
}

func (e *env) create(name string) Job {
	e.t.Helper()
	c, err := e.svc.Create(e.admin.ctx, e.admin.actor, e.input(name))
	if err != nil {
		e.t.Fatal(err)
	}
	c.Destroy()
	return c.Job
}

func (e *env) audits(action audit.Action) []map[string]any {
	e.t.Helper()
	page, err := e.auditor.List(e.t.Context(), audit.Filter{Action: action}, 0, 200)
	if err != nil {
		e.t.Fatal(err)
	}
	out := make([]map[string]any, 0, len(page.Entries))
	for i := range page.Entries {
		m := map[string]any{}
		if err := json.Unmarshal(page.Entries[i].Details, &m); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func apiCode(err error) apierror.Code {
	var ae *apierror.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

func TestCreateDefaultsKeysAndAudit(t *testing.T) {
	e := newEnv(t)
	c, err := e.svc.Create(e.admin.ctx, e.admin.actor, e.input("Production DB"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Destroy()
	j := c.Job
	if j.Slug != "production-db" || j.DBMode != DBModeAll || !j.IncludeGlobals || !j.Enabled || j.Verification != VerificationStandard {
		t.Fatalf("defaults: %+v", j)
	}
	if j.Connection.ID != e.pg || j.Connection.Engine != "postgres" || len(j.Destinations) != 1 || j.Destinations[0].Name != "disk-1" {
		t.Fatalf("references: %+v", j)
	}
	if j.ScheduleDescription == "" || j.Schedule.Kind != scheduler.KindInterval {
		t.Fatalf("schedule: %+v", j)
	}
	// 2026-03-28 12:00Z → next 03:00 Amsterdam on 2026-03-29 (CEST since
	// 02:00 that night) = 01:00Z.
	if j.NextRunAt == nil || !j.NextRunAt.Equal(time.Date(2026, 3, 29, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("next_run_at = %v", j.NextRunAt)
	}
	// Encryption on by default: a generated passphrase v1, sealed.
	if c.Encryption == nil || c.Encryption.Passphrase == nil || c.Encryption.Passphrase.Passphrase.Len() == 0 {
		t.Fatal("no generated passphrase")
	}
	pass := string(c.Encryption.Passphrase.Passphrase.Bytes())
	st, err := e.keys.Status(t.Context(), j.ID)
	if err != nil || !st.Configured || st.Mode != jobkeys.ModePassphrase || st.Passphrase == nil || st.Passphrase.Version != 1 {
		t.Fatalf("status: %+v %v", st, err)
	}
	var sealed []byte
	if err := e.pool.QueryRow(t.Context(), "SELECT sealed_passphrase FROM job_passphrases WHERE job_id = $1 AND version = 1", j.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), pass) {
		t.Fatal("passphrase stored in plaintext")
	}
	if got, err := e.keys.PassphraseForVersion(t.Context(), j.ID, 1); err != nil || string(got.Bytes()) != pass {
		t.Fatalf("passphrase v1 does not open: %v", err)
	}
	a := e.audits(audit.ActionJobCreated)
	if len(a) != 1 || a[0]["slug"] != "production-db" || a[0]["encryption_mode"] != "passphrase" {
		t.Fatalf("audit: %v", a)
	}
	all, err := e.auditor.List(t.Context(), audit.Filter{}, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	for i := range all.Entries {
		if strings.Contains(string(all.Entries[i].Details), pass) {
			t.Fatal("the passphrase is in the audit log")
		}
	}

	// Slugs: derived with a suffix on collision; an explicit taken slug
	// and a taken name (case-insensitive) are conflicts.
	in := e.input("production db!")
	c2, err := e.svc.Create(e.admin.ctx, e.admin.actor, in)
	if err != nil {
		t.Fatal(err)
	}
	c2.Destroy()
	if c2.Job.Slug != "production-db-2" {
		t.Fatalf("slug %q", c2.Job.Slug)
	}
	in = e.input("Other")
	in.Slug = "production-db"
	if _, err := e.svc.Create(e.admin.ctx, e.admin.actor, in); !errors.Is(err, ErrSlugTaken) {
		t.Fatalf("taken slug: %v", err)
	}
	if _, err := e.svc.Create(e.admin.ctx, e.admin.actor, e.input("PRODUCTION DB")); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("taken name: %v", err)
	}

	// A maintainer may create jobs; a user may not.
	if _, err := e.svc.Create(e.maint.ctx, e.maint.actor, e.input("By maint")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Create(e.owner.ctx, e.owner.actor, e.input("By user")); apiCode(err) != apierror.Forbidden {
		t.Fatalf("user create: %v", err)
	}
	// The actor must be the caller.
	if _, err := e.svc.Create(e.admin.ctx, e.maint.actor, e.input("Spoofed")); !errors.Is(err, ErrActorMismatch) {
		t.Fatalf("spoofed actor: %v", err)
	}
}

func TestCreateValidation(t *testing.T) {
	e := newEnv(t)
	dur := func(d time.Duration) *time.Duration { return &d }
	no := false
	yes := true
	tests := []struct {
		name  string
		mod   func(*CreateInput)
		field string
	}{
		{"empty name", func(in *CreateInput) { in.Name = " " }, "name"},
		{"bad slug", func(in *CreateInput) { in.Slug = "Bad_Slug" }, "slug"},
		{"no connection", func(in *CreateInput) { in.ConnectionID = uuid.Nil }, "connectionId"},
		{"unknown connection", func(in *CreateInput) { in.ConnectionID = uuid.New() }, "connectionId"},
		{"temporary connection", func(in *CreateInput) { in.ConnectionID = e.temp }, "connectionId"},
		{"globals on mysql", func(in *CreateInput) { in.ConnectionID = e.my; in.IncludeGlobals = &yes }, "includeGlobals"},
		{"bad database", func(in *CreateInput) { in.DBMode = DBModeSelected; in.Databases = []string{"a b"} }, "databases"},
		{"selected without databases", func(in *CreateInput) { in.DBMode = DBModeSelected }, "databases"},
		{"short interval", func(in *CreateInput) {
			in.Schedule = scheduler.Schedule{Kind: scheduler.KindInterval, Every: 2, Unit: scheduler.UnitMinutes, Anchor: "2026-01-01T00:00", Timezone: "UTC"}
		}, "schedule"},
		{"bad timezone", func(in *CreateInput) { in.Schedule = daily("03:00", "Mars/Olympus") }, "schedule"},
		{"no retention", func(in *CreateInput) { in.Retention = Retention{} }, "retention"},
		{"timeout above cap", func(in *CreateInput) { in.Timeout = dur(7 * time.Hour) }, "timeoutSeconds"},
		{"timeout too short", func(in *CreateInput) { in.Timeout = dur(10 * time.Second) }, "timeoutSeconds"},
		{"window too short", func(in *CreateInput) { in.ExpectedWindow = dur(time.Second) }, "expectedWindowSeconds"},
		{"bad verification", func(in *CreateInput) { in.Verification = "paranoid" }, "verification"},
		{"no destinations", func(in *CreateInput) { in.DestinationIDs = nil }, "destinationIds"},
		{"unknown destination", func(in *CreateInput) { in.DestinationIDs = []uuid.UUID{uuid.New()} }, "destinationIds"},
		{"deleted destination", func(in *CreateInput) { in.DestinationIDs = []uuid.UUID{e.destGone} }, "destinationIds"},
		{"weak passphrase", func(in *CreateInput) { in.Encryption.Passphrase = crypto.NewSecret([]byte("short")) }, "passphrase"},
		{"bad recipient", func(in *CreateInput) {
			in.Encryption = Encryption{Mode: jobkeys.ModeRecipient, Recipient: "age1nope"}
		}, "recipient"},
		{"opt-out at creation", func(in *CreateInput) { in.Encryption.Mode = jobkeys.ModeNone }, "mode"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := e.input("Job " + tc.name)
			tc.mod(&in)
			_, err := e.svc.Create(e.admin.ctx, e.admin.actor, in)
			if got := fieldOf(t, err); got != tc.field {
				t.Fatalf("field %q, want %q (%v)", got, tc.field, err)
			}
		})
	}
	// Nothing was stored by the failed creations (incl. keys: one tx).
	var n int
	if err := e.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM jobs) + (SELECT count(*) FROM job_encryption)").Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows left behind: %d %v", n, err)
	}
	// MySQL defaults to no globals; a recipient job keeps the identity out
	// of the DB.
	in := e.input("MySQL")
	in.ConnectionID = e.my
	in.Encryption = Encryption{Mode: jobkeys.ModeRecipient}
	c, err := e.svc.Create(e.admin.ctx, e.admin.actor, in)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Destroy()
	if c.Job.IncludeGlobals || c.Encryption.Keypair == nil {
		t.Fatalf("mysql job: %+v", c.Job)
	}
	in = e.input("PG no globals")
	in.IncludeGlobals = &no
	in.Enabled = &no
	in.Schedule = scheduler.Schedule{Kind: scheduler.KindManual}
	c2, err := e.svc.Create(e.admin.ctx, e.admin.actor, in)
	if err != nil {
		t.Fatal(err)
	}
	c2.Destroy()
	if c2.Job.IncludeGlobals || c2.Job.Enabled || c2.Job.NextRunAt != nil {
		t.Fatalf("manual job: %+v", c2.Job)
	}
}

func TestUpdateReplanAndSlug(t *testing.T) {
	e := newEnv(t)
	j := e.create("Nightly")
	first := *j.NextRunAt

	// No-op update: nothing recorded, next run kept.
	same := "Nightly"
	got, err := e.svc.Update(e.maint.ctx, e.maint.actor, j.ID, UpdateInput{Name: &same})
	if err != nil || !got.NextRunAt.Equal(first) || len(e.audits(audit.ActionJobUpdated)) != 0 {
		t.Fatalf("no-op: %v %v", got.NextRunAt, err)
	}
	// A non-schedule change keeps next_run_at even when the clock moved.
	e.setNow(e.clock().Add(48 * time.Hour))
	desc := "the nightly one"
	got, err = e.svc.Update(e.maint.ctx, e.maint.actor, j.ID, UpdateInput{Description: &desc})
	if err != nil || !got.NextRunAt.Equal(first) || got.Description != desc {
		t.Fatalf("description: %+v %v", got, err)
	}
	// A schedule change re-plans from now, across DST: 02:30 Amsterdam on
	// the spring-forward night (2026-03-29) does not exist → 03:30 CEST.
	e.setNow(time.Date(2026, 3, 28, 12, 0, 0, 0, time.UTC))
	s := daily("02:30", "Europe/Amsterdam")
	got, err = e.svc.Update(e.maint.ctx, e.maint.actor, j.ID, UpdateInput{Schedule: &s})
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 3, 29, 1, 30, 0, 0, time.UTC); !got.NextRunAt.Equal(want) {
		t.Fatalf("DST re-plan: %v, want %v", got.NextRunAt, want)
	}
	// Changing to manual unschedules.
	manual := scheduler.Schedule{Kind: scheduler.KindManual}
	if got, err = e.svc.Update(e.maint.ctx, e.maint.actor, j.ID, UpdateInput{Schedule: &manual}); err != nil || got.NextRunAt != nil {
		t.Fatalf("manual: %v %v", got.NextRunAt, err)
	}
	// The selection, retention, timeouts, destinations.
	mode := DBModeSelected
	dbs := []string{"app", "billing"}
	ids := []uuid.UUID{e.dest2, e.dest1}
	tmo := 2 * time.Hour
	deep := VerificationDeep
	got, err = e.svc.Update(e.admin.ctx, e.admin.actor, j.ID, UpdateInput{
		DBMode: &mode, Databases: &dbs, DestinationIDs: &ids, Timeout: &tmo, Verification: &deep,
		Retention: &Retention{KeepDays: keep(30)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.DBMode != DBModeSelected || len(got.Databases) != 2 || got.Destinations[0].ID != e.dest2 ||
		got.Timeout != tmo || got.Verification != deep || got.Retention.KeepLast != nil || *got.Retention.KeepDays != 30 {
		t.Fatalf("update: %+v", got)
	}
	a := e.audits(audit.ActionJobUpdated)
	if len(a) == 0 || !strings.Contains(strings.Join(anyStrings(a[0]["fields"]), ","), "destination_ids") {
		t.Fatalf("audit: %v", a)
	}
	// A temporary restore connection is refused on update too (task 4.4).
	temp := e.temp
	if _, err := e.svc.Update(e.admin.ctx, e.admin.actor, j.ID, UpdateInput{ConnectionID: &temp}); fieldOf(t, err) != "connectionId" {
		t.Fatalf("temporary connection: %v", err)
	}
	// Moving to MySQL with globals still on is refused; with globals off
	// it works.
	my := e.my
	if _, err := e.svc.Update(e.admin.ctx, e.admin.actor, j.ID, UpdateInput{ConnectionID: &my}); fieldOf(t, err) != "includeGlobals" {
		t.Fatalf("mysql with globals: %v", err)
	}
	off := false
	if _, err := e.svc.Update(e.admin.ctx, e.admin.actor, j.ID, UpdateInput{ConnectionID: &my, IncludeGlobals: &off}); err != nil {
		t.Fatal(err)
	}
	if got, err = e.svc.Update(e.admin.ctx, e.admin.actor, j.ID, UpdateInput{ClearTimeout: true}); err != nil || got.Timeout != 0 {
		t.Fatalf("clear timeout: %v %v", got.Timeout, err)
	}
	// Owners and plain users cannot edit.
	name := "Hijack"
	if _, err := e.svc.Update(e.owner.ctx, e.owner.actor, j.ID, UpdateInput{Name: &name}); apiCode(err) != apierror.Forbidden {
		t.Fatalf("user update: %v", err)
	}
	// The slug is immutable, also for bekup_app directly in SQL.
	if _, err := e.pool.Exec(t.Context(), "UPDATE jobs SET slug = 'renamed' WHERE id = $1", j.ID); err == nil {
		t.Fatal("slug changed")
	}
	if g, err := e.svc.Get(t.Context(), j.ID); err != nil || g.Slug != "nightly" {
		t.Fatalf("slug %q", g.Slug)
	}
}

func TestEnableDisableDelete(t *testing.T) {
	e := newEnv(t)
	j := e.create("Hourly-ish")
	got, err := e.svc.SetEnabled(e.maint.ctx, e.maint.actor, j.ID, false)
	if err != nil || got.Enabled || got.NextRunAt != nil {
		t.Fatalf("disable: %+v %v", got, err)
	}
	e.setNow(e.clock().Add(72 * time.Hour))
	got, err = e.svc.SetEnabled(e.maint.ctx, e.maint.actor, j.ID, true)
	if err != nil || !got.Enabled || got.NextRunAt == nil || !got.NextRunAt.After(e.clock()) {
		t.Fatalf("enable: %+v %v", got, err)
	}
	if _, err := e.svc.SetEnabled(e.maint.ctx, e.maint.actor, j.ID, true); err != nil {
		t.Fatal(err)
	}
	if len(e.audits(audit.ActionJobEnabled)) != 1 || len(e.audits(audit.ActionJobDisabled)) != 1 {
		t.Fatal("enable/disable audits")
	}

	if err := e.svc.Delete(e.owner.ctx, e.owner.actor, j.ID); apiCode(err) != apierror.Forbidden {
		t.Fatalf("user delete: %v", err)
	}
	if err := e.svc.Delete(e.maint.ctx, e.maint.actor, j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Get(t.Context(), j.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted: %v", err)
	}
	if err := e.svc.Delete(e.maint.ctx, e.maint.actor, j.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	// The scheduler no longer sees it; the row, keys and links stay.
	due, err := e.q.LockDueJobs(t.Context(), sqlc.LockDueJobsParams{Now: e.clock().Add(1000 * time.Hour), BatchSize: 10})
	if err != nil || len(due) != 0 {
		t.Fatalf("due after delete: %v %v", due, err)
	}
	if st, err := e.keys.Status(t.Context(), j.ID); err != nil || !st.Configured {
		t.Fatal("keys gone")
	}
	// bekup_app cannot hard delete or revive.
	if _, err := e.pool.Exec(t.Context(), "DELETE FROM jobs WHERE id = $1", j.ID); err == nil {
		t.Fatal("hard delete allowed")
	}
	if _, err := e.pool.Exec(t.Context(), "UPDATE jobs SET deleted_at = NULL WHERE id = $1", j.ID); err == nil {
		t.Fatal("revive allowed")
	}
	// The name can be reused, the slug cannot.
	c := e.create("Hourly-ish")
	if c.Slug != "hourly-ish-2" {
		t.Fatalf("slug %q", c.Slug)
	}
}

func TestOwnersAndScope(t *testing.T) {
	e := newEnv(t)
	j1 := e.create("Owned")
	j2 := e.create("Not owned")

	// Step-up required; a maintainer cannot add themselves; unknown users
	// are refused.
	staleCtx := authztest.Context(t.Context(), authztest.Stale(e.maint.pr))
	if _, err := e.svc.AssignOwners(staleCtx, e.maint.actor, j1.ID, []uuid.UUID{e.owner.actor.UserID}); apiCode(err) != apierror.StepUpRequired {
		t.Fatalf("stale step-up: %v", err)
	}
	if _, err := e.svc.AssignOwners(e.maint.ctx, e.maint.actor, j1.ID, []uuid.UUID{e.maint.actor.UserID}); apiCode(err) != apierror.Forbidden {
		t.Fatalf("maintainer self-assign: %v", err)
	}
	if _, err := e.svc.AssignOwners(e.maint.ctx, e.maint.actor, j1.ID, []uuid.UUID{uuid.New()}); fieldOf(t, err) != "users" {
		t.Fatalf("unknown user: %v", err)
	}
	if _, err := e.svc.AssignOwners(e.owner.ctx, e.owner.actor, j1.ID, []uuid.UUID{e.owner.actor.UserID}); apiCode(err) != apierror.Forbidden {
		t.Fatalf("user self-assign: %v", err)
	}
	owners, err := e.svc.AssignOwners(e.maint.ctx, e.maint.actor, j1.ID, []uuid.UUID{e.owner.actor.UserID})
	if err != nil || len(owners) != 1 || owners[0].Email != "owner@example.com" {
		t.Fatalf("assign: %v %v", owners, err)
	}
	// Repeating is a no-op (no second audit/alert).
	if _, err := e.svc.AssignOwners(e.maint.ctx, e.maint.actor, j1.ID, []uuid.UUID{e.owner.actor.UserID}); err != nil {
		t.Fatal(err)
	}
	if n := len(e.audits(audit.ActionJobOwnerAdded)); n != 1 {
		t.Fatalf("owner audits: %d", n)
	}
	e.mu.Lock()
	nAlerts := len(e.alerts)
	e.mu.Unlock()
	if nAlerts != 1 {
		t.Fatalf("alerts: %d", nAlerts)
	}

	// Ownership drives the policy: view/run own job only.
	if err := e.policy.Authorize(e.owner.ctx, authz.OpJobView, authz.Job{ID: j1.ID}); err != nil {
		t.Fatal(err)
	}
	if err := e.policy.Authorize(e.owner.ctx, authz.OpJobView, authz.Job{ID: j2.ID}); apiCode(err) != apierror.Forbidden {
		t.Fatalf("view not owned: %v", err)
	}
	if err := e.policy.Authorize(e.other.ctx, authz.OpJobView, authz.Job{ID: j1.ID}); apiCode(err) != apierror.Forbidden {
		t.Fatalf("other user: %v", err)
	}
	// JobScope filtering.
	page, err := e.svc.List(t.Context(), authz.ScopeOwned, e.owner.actor.UserID, ListFilter{}, nil, 50)
	if err != nil || len(page.Jobs) != 1 || page.Jobs[0].ID != j1.ID {
		t.Fatalf("owned list: %+v %v", page, err)
	}
	page, err = e.svc.List(t.Context(), authz.ScopeOwned, e.other.actor.UserID, ListFilter{}, nil, 50)
	if err != nil || len(page.Jobs) != 0 {
		t.Fatalf("other list: %+v %v", page, err)
	}
	page, err = e.svc.List(t.Context(), authz.ScopeAll, e.maint.actor.UserID, ListFilter{}, nil, 1)
	if err != nil || len(page.Jobs) != 1 || !page.More || page.Jobs[0].Name != "Not owned" {
		t.Fatalf("all list page 1: %+v %v", page, err)
	}
	page, err = e.svc.List(t.Context(), authz.ScopeAll, e.maint.actor.UserID, ListFilter{}, &Cursor{Name: "not owned", ID: page.Jobs[0].ID}, 1)
	if err != nil || len(page.Jobs) != 1 || page.More || page.Jobs[0].ID != j1.ID {
		t.Fatalf("all list page 2: %+v %v", page, err)
	}
	page, err = e.svc.List(t.Context(), authz.ScopeAll, e.maint.actor.UserID, ListFilter{Search: "NOT"}, nil, 10)
	if err != nil || len(page.Jobs) != 1 {
		t.Fatalf("search: %+v %v", page, err)
	}

	// Restore-target facts (mode all: every database except excluded).
	own := Ownership{DB: e.pool}
	if ok, err := own.BacksUpDatabase(t.Context(), e.owner.actor.UserID, e.pg, "app"); err != nil || !ok {
		t.Fatalf("backs up (all): %v %v", ok, err)
	}
	ex := []string{"secret_db"}
	if _, err := e.svc.Update(e.admin.ctx, e.admin.actor, j1.ID, UpdateInput{Exclude: &ex}); err != nil {
		t.Fatal(err)
	}
	if ok, err := own.BacksUpDatabase(t.Context(), e.owner.actor.UserID, e.pg, "secret_db"); err != nil || ok {
		t.Fatal("excluded database counted")
	}
	if ok, err := own.UsesConnection(t.Context(), e.owner.actor.UserID, e.pg); err != nil || !ok {
		t.Fatal("uses connection")
	}
	if ok, err := own.UsesConnection(t.Context(), e.other.actor.UserID, e.pg); err != nil || ok {
		t.Fatal("other uses connection")
	}

	// Removal: audited, alerted; access ends.
	if _, err := e.svc.RemoveOwner(e.maint.ctx, e.maint.actor, j1.ID, e.other.actor.UserID); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("remove non-owner: %v", err)
	}
	if owners, err = e.svc.RemoveOwner(e.maint.ctx, e.maint.actor, j1.ID, e.owner.actor.UserID); err != nil || len(owners) != 0 {
		t.Fatalf("remove: %v %v", owners, err)
	}
	if err := e.policy.Authorize(e.owner.ctx, authz.OpJobView, authz.Job{ID: j1.ID}); apiCode(err) != apierror.Forbidden {
		t.Fatalf("view after removal: %v", err)
	}
	if len(e.audits(audit.ActionJobOwnerRemoved)) != 1 {
		t.Fatal("removal audit")
	}
	// A deleted user owns nothing.
	if _, err := e.svc.AssignOwners(e.admin.ctx, e.admin.actor, j1.ID, []uuid.UUID{e.owner.actor.UserID}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), "UPDATE users SET disabled = true, deleted_at = now() WHERE id = $1", e.owner.actor.UserID); err != nil {
		t.Fatal(err)
	}
	if ok, err := own.IsJobOwner(t.Context(), e.owner.actor.UserID, j1.ID); err != nil || ok {
		t.Fatal("deleted user still owns")
	}
}

func TestRunNow(t *testing.T) {
	e := newEnv(t)
	j := e.create("Run me")
	if _, err := e.svc.AssignOwners(e.admin.ctx, e.admin.actor, j.ID, []uuid.UUID{e.owner.actor.UserID}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RunNow(e.other.ctx, e.other.actor, j.ID); apiCode(err) != apierror.Forbidden {
		t.Fatalf("non-owner run: %v", err)
	}
	req, err := e.svc.RunNow(e.owner.ctx, e.owner.actor, j.ID)
	if err != nil || req.QueueJobID == 0 {
		t.Fatalf("owner run: %+v %v", req, err)
	}
	var args queue.BackupRunArgs
	var raw []byte
	if err := e.pool.QueryRow(t.Context(), "SELECT args FROM river_job WHERE id = $1", req.QueueJobID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		t.Fatal(err)
	}
	if args.JobID != j.ID || args.Trigger != queue.TriggerManual || args.TriggeredBy == nil || *args.TriggeredBy != e.owner.actor.UserID {
		t.Fatalf("args: %+v", args)
	}
	// The queued run row was created with the River job.
	run, err := e.q.GetRun(t.Context(), req.RunID)
	if err != nil || run.Status != sqlc.RunStatusQueued || run.RiverJobID == nil || *run.RiverJobID != req.QueueJobID ||
		run.Trigger != sqlc.RunTriggerManual || run.TriggeredBy == nil || *run.TriggeredBy != e.owner.actor.UserID {
		t.Fatalf("queued run: %+v %v", run, err)
	}
	// A second run while the first is queued: refused, nothing recorded.
	if _, err := e.svc.RunNow(e.admin.ctx, e.admin.actor, j.ID); !errors.Is(err, ErrRunActive) {
		t.Fatalf("overlap: %v", err)
	}
	if n := len(e.audits(audit.ActionJobRunRequested)); n != 1 {
		t.Fatalf("run audits: %d", n)
	}
	// Once finished, another run is accepted.
	if _, err := e.pool.Exec(t.Context(), "UPDATE river_job SET state = 'completed', finalized_at = now() WHERE id = $1", req.QueueJobID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RunNow(e.maint.ctx, e.maint.actor, j.ID); err != nil {
		t.Fatal(err)
	}
	// Disabled and deleted jobs do not run.
	j2 := e.create("Off")
	if _, err := e.svc.SetEnabled(e.admin.ctx, e.admin.actor, j2.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RunNow(e.admin.ctx, e.admin.actor, j2.ID); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled: %v", err)
	}
	if err := e.svc.Delete(e.admin.ctx, e.admin.actor, j2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RunNow(e.admin.ctx, e.admin.actor, j2.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
}

func TestCancelRun(t *testing.T) {
	e := newEnv(t)
	j := e.create("Cancel me")
	if _, err := e.svc.AssignOwners(e.admin.ctx, e.admin.actor, j.ID, []uuid.UUID{e.owner.actor.UserID}); err != nil {
		t.Fatal(err)
	}
	req, err := e.svc.RunNow(e.owner.ctx, e.owner.actor, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Owners may start runs of their jobs but not cancel them (D-106).
	if _, err := e.svc.CancelRun(e.owner.ctx, e.owner.actor, req.RunID); apiCode(err) != apierror.Forbidden {
		t.Fatalf("owner cancel: %v", err)
	}
	if _, err := e.svc.CancelRun(e.maint.ctx, e.admin.actor, req.RunID); !errors.Is(err, ErrActorMismatch) {
		t.Fatalf("actor mismatch: %v", err)
	}
	// A queued run: its River job is canceled and the run finished at once.
	c, err := e.svc.CancelRun(e.maint.ctx, e.maint.actor, req.RunID)
	if err != nil || c.Status != "canceled" || c.JobID != j.ID {
		t.Fatalf("cancel queued: %+v %v", c, err)
	}
	var state string
	if err := e.pool.QueryRow(t.Context(), "SELECT state FROM river_job WHERE id = $1", req.QueueJobID).Scan(&state); err != nil || state != string(rivertype.JobStateCancelled) {
		t.Fatalf("river state %q %v", state, err)
	}
	run, err := e.q.GetRun(t.Context(), req.RunID)
	if err != nil || run.Status != sqlc.RunStatusCanceled || run.FinishedAt == nil || run.CancelRequestedBy == nil || *run.CancelRequestedBy != e.maint.actor.UserID {
		t.Fatalf("run %+v %v", run, err)
	}
	if _, err := e.svc.CancelRun(e.admin.ctx, e.admin.actor, req.RunID); !errors.Is(err, ErrRunFinished) {
		t.Fatalf("second cancel: %v", err)
	}
	if _, err := e.svc.CancelRun(e.admin.ctx, e.admin.actor, uuid.New()); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("unknown run: %v", err)
	}
	// A running run is only marked: its worker stops it.
	req2, err := e.svc.RunNow(e.admin.ctx, e.admin.actor, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), "UPDATE river_job SET state = 'running', attempt = 1, attempted_at = now() WHERE id = $1", req2.QueueJobID); err != nil {
		t.Fatal(err)
	}
	c, err = e.svc.CancelRun(e.admin.ctx, e.admin.actor, req2.RunID)
	if err != nil || c.Status != "queued" {
		t.Fatalf("cancel running: %+v %v", c, err)
	}
	run, err = e.q.GetRun(t.Context(), req2.RunID)
	if err != nil || run.Status != sqlc.RunStatusQueued || run.CancelRequestedAt == nil {
		t.Fatalf("running run %+v %v", run, err)
	}
	if ok, err := e.q.RunCancelRequested(t.Context(), req2.RunID); err != nil || !ok {
		t.Fatalf("cancel requested %v %v", ok, err)
	}
	audits := e.audits(audit.ActionRunCancelRequested)
	if len(audits) != 2 || audits[0]["job_id"] != j.ID.String() {
		t.Fatalf("audits %v", audits)
	}
}

// TestCancelRunPendingRetry: a run whose attempt failed while its River job
// waits for the retry: cancelRun cancels that retry (no new attempt), the
// run keeps its final status, and the action is audited.
func TestCancelRunPendingRetry(t *testing.T) {
	e := newEnv(t)
	j := e.create("Retry me")
	req, err := e.svc.RunNow(e.admin.ctx, e.admin.actor, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	store := runstate.NewStore(e.pool, nil, nil)
	if err := store.Finish(t.Context(), runstate.Ref{RunID: req.RunID, JobID: j.ID}, runstate.Result{
		Status: runstate.StatusFailed, Failure: &runstate.Failure{Code: "runner_unavailable", Message: "the runner is unavailable"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), "UPDATE river_job SET state = 'retryable', attempt = 1, attempted_at = now(), scheduled_at = now() + interval '1 minute' WHERE id = $1", req.QueueJobID); err != nil {
		t.Fatal(err)
	}
	c, err := e.svc.CancelRun(e.maint.ctx, e.maint.actor, req.RunID)
	if err != nil || !c.RetryCanceled || c.Status != "failed" || c.RunID != req.RunID {
		t.Fatalf("cancel pending retry: %+v %v", c, err)
	}
	var state string
	if err := e.pool.QueryRow(t.Context(), "SELECT state FROM river_job WHERE id = $1", req.QueueJobID).Scan(&state); err != nil || state != string(rivertype.JobStateCancelled) {
		t.Fatalf("river state %q %v", state, err)
	}
	if run, err := e.q.GetRun(t.Context(), req.RunID); err != nil || run.Status != sqlc.RunStatusFailed {
		t.Fatalf("run %+v %v", run, err)
	}
	// Nothing is pending any more.
	if _, err := e.svc.CancelRun(e.admin.ctx, e.admin.actor, req.RunID); !errors.Is(err, ErrRunFinished) {
		t.Fatalf("second cancel: %v", err)
	}
	audits := e.audits(audit.ActionRunCancelRequested)
	if len(audits) != 1 || audits[0]["retry_canceled"] != true || audits[0]["river_state"] != "retryable" {
		t.Fatalf("audits %v", audits)
	}
	// A finished run whose River job completed cannot be canceled.
	req2, err := e.svc.RunNow(e.admin.ctx, e.admin.actor, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Finish(t.Context(), runstate.Ref{RunID: req2.RunID, JobID: j.ID}, runstate.Result{Status: runstate.StatusSuccess}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), "UPDATE river_job SET state = 'completed', attempt = 1, attempted_at = now(), finalized_at = now() WHERE id = $1", req2.QueueJobID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CancelRun(e.admin.ctx, e.admin.actor, req2.RunID); !errors.Is(err, ErrRunFinished) {
		t.Fatalf("cancel of a completed run: %v", err)
	}
}

func TestPreviewSchedule(t *testing.T) {
	e := newEnv(t)
	p, err := e.svc.PreviewSchedule(daily("02:30", "Europe/Amsterdam"), 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Time{
		time.Date(2026, 3, 29, 1, 30, 0, 0, time.UTC), // 03:30 CEST (02:30 does not exist)
		time.Date(2026, 3, 30, 0, 30, 0, 0, time.UTC),
		time.Date(2026, 3, 31, 0, 30, 0, 0, time.UTC),
	}
	if len(p.Next) != 3 {
		t.Fatalf("next: %v", p.Next)
	}
	for i := range want {
		if !p.Next[i].Equal(want[i]) {
			t.Fatalf("next[%d] = %v, want %v", i, p.Next[i], want[i])
		}
	}
	if !strings.Contains(p.Description, "02:30") {
		t.Fatalf("description %q", p.Description)
	}
	m, err := e.svc.PreviewSchedule(scheduler.Schedule{Kind: scheduler.KindManual}, 0)
	if err != nil || len(m.Next) != 0 {
		t.Fatalf("manual: %v %v", m, err)
	}
	if _, err := e.svc.PreviewSchedule(daily("03:00", "UTC"), 51); fieldOf(t, err) != "count" {
		t.Fatal("count 51 accepted")
	}
	if _, err := e.svc.PreviewSchedule(scheduler.Schedule{Kind: scheduler.KindCron, Cron: "* * * * *", Timezone: "UTC"}, 5); fieldOf(t, err) != "schedule" {
		t.Fatal("dense cron accepted")
	}
}

func anyStrings(v any) []string {
	l, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(l))
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
