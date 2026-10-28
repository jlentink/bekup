//go:build integration

package graph_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/scheduler"
)

// jobEnv is a destEnv (response recording, step-up helpers) whose handler
// has the job and job encryption services (task 3.5).
type jobEnv struct {
	*destEnv
	pg, my, dest uuid.UUID
}

func newJobEnv(t *testing.T) *jobEnv {
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
	sessions := auth.NewSessions(e.pool, auth.SessionOptions{})
	svc, err := auth.NewService(auth.ServiceOptions{DB: e.pool, Hasher: e.hasher, Sessions: sessions, Auditor: e.auditor, Factors: auth.NewSecondFactors()})
	if err != nil {
		t.Fatal(err)
	}
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kr.Destroy)
	logger := slog.New(slog.DiscardHandler)
	pol := authz.New(authz.Options{Ownership: jobs.Ownership{DB: e.pool}})
	keys, err := jobkeys.New(jobkeys.Options{DB: e.pool, Keyring: kr, Auditor: e.auditor, Authorizer: pol, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	qc, err := queue.New(e.pool, queue.Options{Settings: queue.SettingsFromConfig(config.DefaultBekup().Workers), Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	js, err := jobs.New(jobs.Options{
		DB: e.pool, Auditor: e.auditor, Authorizer: pol, Keys: keys, Queue: qc,
		Limits: scheduler.Limits{MinInterval: 5 * time.Minute}, MaxTimeout: 6 * time.Hour, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	gql, err := graph.New(graph.Options{
		Logger: logger, MaxComplexity: 2000, MaxDepth: 12, MaxBodyBytes: 1 << 16,
		Access: graph.NewPolicyAccess(pol), Policy: pol, AuditLog: e.auditor, Auth: svc,
		Jobs: js, JobKeys: keys,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := httpserver.New(httpserver.Options{
		BaseURL: testBaseURL, MaxBodyBytes: 1 << 20, Logger: logger,
		Ready:     func(context.Context) error { return nil },
		RateLimit: httpserver.RateLimitOptions{PerSecond: 1000, Burst: 1000, MaxClients: 100},
		GraphQL:   gql,
		Sessions: &httpserver.SessionOptions{
			Resolver: httpserver.SessionsResolver{Sessions: sessions},
			Cookies:  auth.CookiePolicyFor(testBaseURL),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.h = srv.Handler()
	je := &jobEnv{destEnv: &destEnv{authEnv: e}}
	je.pg = je.exec(`INSERT INTO connections (id, name, engine, version_key, image_key, host, port, username)
		VALUES ($1, 'Primary PG', 'postgres', '17', '17', 'db.example.test', 5432, 'backup')`)
	je.my = je.exec(`INSERT INTO connections (id, name, engine, version_key, image_key, host, port, username)
		VALUES ($1, 'Shop MySQL', 'mysql', '8.4', '8.4', 'my.example.test', 3306, 'backup')`)
	je.dest = je.exec(`INSERT INTO destinations (id, name, type, config) VALUES ($1, 'Local disk', 'disk', '{"path":"local"}')`)
	return je
}

func (e *jobEnv) exec(stmt string) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	if _, err := e.pool.Exec(e.t.Context(), stmt, id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

const (
	jobFields = `id name slug description dbMode databases exclude includeGlobals enabled nextRunAt scheduleDescription
		verification timeoutSeconds expectedWindowSeconds
		schedule { kind every unit anchor timezone cron }
		retention { keepLast keepDays }
		connection { id name engine }
		destinations { id name type position }
		encryption { configured mode optedOut passphrase { version } recipient { version recipient fingerprint } }
		owners { userId email role }`
	createJobMut = `mutation($in: CreateJobInput!) { createJob(input: $in) {
		job { ` + jobFields + ` } generatedPassphrase generatedKeypair { recipient identity } } }`
	updateJobMut   = `mutation($id: UUID!, $in: UpdateJobInput!) { updateJob(id: $id, input: $in) { ` + jobFields + ` } }`
	viewJobQuery   = `query($id: UUID!) { job(id: $id) { ` + jobFields + ` } }`
	listJobsQuery  = `query($f: JobFilter, $first: Int, $after: String) { jobs(filter: $f, first: $first, after: $after) { edges { cursor node { id name owners { userId } } } pageInfo { hasNextPage endCursor } } }`
	runNowMut      = `mutation($id: UUID!) { runJobNow(id: $id) { queuedAt job { id } } }`
	assignMut      = `mutation($id: UUID!, $u: [UUID!]!) { assignJobOwners(jobId: $id, users: $u) { userId email role } }`
	removeOwnerMut = `mutation($id: UUID!, $u: UUID!) { removeJobOwner(jobId: $id, userId: $u) { userId } }`
	previewQuery   = `query($in: ScheduleInput!, $n: Int) { previewSchedule(input: $in, count: $n) { description next schedule { kind weekday } } }`
)

type jobGQL struct {
	ID                  string
	Name                string
	Slug                string
	DbMode              string
	Databases           []string
	IncludeGlobals      bool
	Enabled             bool
	NextRunAt           *string
	ScheduleDescription string
	Verification        string
	TimeoutSeconds      *int
	Retention           struct{ KeepLast, KeepDays *int }
	Connection          struct{ ID, Name, Engine string }
	Destinations        []struct {
		ID, Name, Type string
		Position       int
	}
	Encryption struct {
		Configured bool
		Mode       *string
		OptedOut   bool
		Passphrase *struct{ Version int }
		Recipient  *struct{ Version int }
	}
	Owners *[]struct{ UserID, Email, Role string }
}

func (e *jobEnv) createInput(name string) map[string]any {
	return map[string]any{
		"name": name, "connectionId": e.pg, "destinationIds": []string{e.dest.String()},
		"schedule":  map[string]any{"kind": "INTERVAL", "every": 1, "unit": "DAYS", "anchor": "2026-01-01T03:00", "timezone": "Europe/Amsterdam"},
		"retention": map[string]any{"keepLast": 7},
	}
}

func TestJobsGraphQLCRUD(t *testing.T) {
	e := newJobEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	e.user("plain@example.com", sqlc.UserRoleUser)
	adminC := e.mustLogin("admin@example.com")
	maintC := e.mustLogin("maint@example.com")
	plainC := e.mustLogin("plain@example.com")
	e.freshStepUp(admin.ID)

	var created struct {
		Job                 jobGQL
		GeneratedPassphrase *string
		GeneratedKeypair    *struct{ Recipient, Identity string }
	}
	e.mustGQL(adminC, createJobMut, map[string]any{"in": e.createInput("Nightly PG")}).field(t, "createJob", &created)
	j := created.Job
	if j.Slug != "nightly-pg" || j.DbMode != "ALL" || !j.IncludeGlobals || !j.Enabled || j.NextRunAt == nil ||
		j.Connection.Engine != "POSTGRES" || len(j.Destinations) != 1 || j.Destinations[0].Type != "DISK" ||
		!j.Encryption.Configured || j.Encryption.Mode == nil || *j.Encryption.Mode != "PASSPHRASE" ||
		j.Encryption.Passphrase == nil || j.Encryption.Passphrase.Version != 1 || j.Owners == nil || *j.Retention.KeepLast != 7 {
		t.Fatalf("created: %+v", j)
	}
	if created.GeneratedPassphrase == nil || len(strings.Fields(*created.GeneratedPassphrase)) < 10 || created.GeneratedKeypair != nil {
		t.Fatalf("generated passphrase: %+v", created)
	}
	generated := *created.GeneratedPassphrase
	// The passphrase is shown once: never again in a later response.
	e.bodies = nil

	// Validation and conflicts.
	for name, mod := range map[string]func(map[string]any){
		"globals on mysql": func(in map[string]any) { in["connectionId"] = e.my; in["includeGlobals"] = true },
		"no retention":     func(in map[string]any) { in["retention"] = map[string]any{} },
		"dense cron": func(in map[string]any) {
			in["schedule"] = map[string]any{"kind": "CRON", "cron": "* * * * *", "timezone": "UTC"}
		},
		"timeout over cap": func(in map[string]any) { in["timeoutSeconds"] = 7 * 3600 },
		"no destinations":  func(in map[string]any) { in["destinationIds"] = []string{} },
		"bad database": func(in map[string]any) {
			in["dbMode"] = "SELECTED"
			in["databases"] = []string{"x; drop"}
		},
	} {
		in := e.createInput("Job " + name)
		mod(in)
		if r := e.gql(adminC, createJobMut, map[string]any{"in": in}); r.code() != "VALIDATION" {
			t.Errorf("%s: %+v", name, r.Errors)
		}
	}
	if r := e.gql(adminC, createJobMut, map[string]any{"in": e.createInput("nightly pg")}); r.code() != "CONFLICT" {
		t.Fatalf("dup name: %+v", r.Errors)
	}
	// Users cannot create; anonymous callers nothing.
	if r := e.gql(plainC, createJobMut, map[string]any{"in": e.createInput("X")}); r.code() != "FORBIDDEN" {
		t.Fatalf("user create: %+v", r.Errors)
	}
	if r := e.gql("", listJobsQuery, nil); r.code() != "UNAUTHENTICATED" {
		t.Fatalf("anonymous list: %+v", r.Errors)
	}

	// Maintainers manage jobs.
	var upd jobGQL
	e.mustGQL(maintC, updateJobMut, map[string]any{"id": j.ID, "in": map[string]any{
		"dbMode": "SELECTED", "databases": []string{"app", "billing"}, "verification": "DEEP", "timeoutSeconds": 3600,
		"schedule": map[string]any{"kind": "CRON", "cron": "0 4 * * 1-5", "timezone": "UTC"},
	}}).field(t, "updateJob", &upd)
	if upd.DbMode != "SELECTED" || len(upd.Databases) != 2 || upd.Verification != "DEEP" || upd.TimeoutSeconds == nil ||
		*upd.TimeoutSeconds != 3600 || !strings.HasPrefix(upd.ScheduleDescription, "Cron") || upd.Slug != "nightly-pg" {
		t.Fatalf("update: %+v", upd)
	}
	var off jobGQL
	e.mustGQL(maintC, `mutation($id: UUID!) { disableJob(id: $id) { `+jobFields+` } }`, map[string]any{"id": j.ID}).field(t, "disableJob", &off)
	if off.Enabled || off.NextRunAt != nil {
		t.Fatalf("disable: %+v", off)
	}
	e.mustGQL(maintC, `mutation($id: UUID!) { enableJob(id: $id) { `+jobFields+` } }`, map[string]any{"id": j.ID}).field(t, "enableJob", &off)
	if !off.Enabled || off.NextRunAt == nil {
		t.Fatalf("enable: %+v", off)
	}

	// previewSchedule.
	var prev struct {
		Description string
		Next        []string
		Schedule    struct{ Kind, Weekday string }
	}
	e.mustGQL(maintC, previewQuery, map[string]any{"in": map[string]any{
		"kind": "INTERVAL", "every": 1, "unit": "WEEKS", "anchor": "2026-01-05T03:00", "timezone": "UTC",
	}, "n": 3}).field(t, "previewSchedule", &prev)
	if len(prev.Next) != 3 || prev.Schedule.Weekday != "monday" || !strings.Contains(prev.Description, "03:00") {
		t.Fatalf("preview: %+v", prev)
	}
	for _, n := range prev.Next {
		ts, err := time.Parse(time.RFC3339, n)
		if err != nil || ts.Weekday() != time.Monday || ts.Hour() != 3 {
			t.Fatalf("preview occurrence %q", n)
		}
	}
	if r := e.gql(plainC, previewQuery, map[string]any{"in": map[string]any{"kind": "MANUAL"}}); r.code() != "FORBIDDEN" {
		t.Fatalf("user preview: %+v", r.Errors)
	}
	if r := e.gql(maintC, previewQuery, map[string]any{"in": map[string]any{"kind": "INTERVAL", "every": 1, "unit": "MINUTES", "anchor": "2026-01-01T00:00", "timezone": "UTC"}}); r.code() != "VALIDATION" {
		t.Fatalf("short preview: %+v", r.Errors)
	}

	// Delete: soft; null afterwards.
	var ok bool
	e.mustGQL(maintC, `mutation($id: UUID!) { deleteJob(id: $id) }`, map[string]any{"id": j.ID}).field(t, "deleteJob", &ok)
	var gone *jobGQL
	e.mustGQL(adminC, viewJobQuery, map[string]any{"id": j.ID}).field(t, "job", &gone)
	if !ok || gone != nil {
		t.Fatalf("after delete: %v %+v", ok, gone)
	}
	e.assertNoSecrets(generated)
}

func TestJobsGraphQLOwners(t *testing.T) {
	e := newJobEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	maint := e.user("maint@example.com", sqlc.UserRoleMaintainer)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	e.user("other@example.com", sqlc.UserRoleUser)
	adminC := e.mustLogin("admin@example.com")
	maintC := e.mustLogin("maint@example.com")
	ownerC := e.mustLogin("owner@example.com")
	otherC := e.mustLogin("other@example.com")

	var a, b struct{ Job jobGQL }
	e.mustGQL(adminC, createJobMut, map[string]any{"in": e.createInput("Alpha")}).field(t, "createJob", &a)
	e.mustGQL(adminC, createJobMut, map[string]any{"in": e.createInput("Beta")}).field(t, "createJob", &b)

	// Owner assignment needs a fresh step-up; a maintainer cannot add
	// themselves.
	e.staleStepUp(maint.ID)
	if r := e.gql(maintC, assignMut, map[string]any{"id": a.Job.ID, "u": []string{owner.ID.String()}}); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("stale assign: %+v", r.Errors)
	}
	e.freshStepUp(maint.ID)
	if r := e.gql(maintC, assignMut, map[string]any{"id": a.Job.ID, "u": []string{maint.ID.String()}}); r.code() != "FORBIDDEN" {
		t.Fatalf("self assign: %+v", r.Errors)
	}
	var owners []struct{ UserID, Email, Role string }
	e.mustGQL(maintC, assignMut, map[string]any{"id": a.Job.ID, "u": []string{owner.ID.String()}}).field(t, "assignJobOwners", &owners)
	if len(owners) != 1 || owners[0].Email != "owner@example.com" || owners[0].Role != "USER" {
		t.Fatalf("owners: %+v", owners)
	}

	// The owner sees only their job, without the owner list.
	var page struct {
		Edges []struct {
			Node struct {
				ID     string
				Owners *[]struct{ UserID string }
			}
		}
	}
	e.mustGQL(ownerC, listJobsQuery, nil).field(t, "jobs", &page)
	if len(page.Edges) != 1 || page.Edges[0].Node.ID != a.Job.ID || page.Edges[0].Node.Owners != nil {
		t.Fatalf("owner list: %+v", page)
	}
	var mine *jobGQL
	e.mustGQL(ownerC, viewJobQuery, map[string]any{"id": a.Job.ID}).field(t, "job", &mine)
	if mine == nil || mine.Owners != nil || mine.Connection.Name != "Primary PG" || !mine.Encryption.Configured {
		t.Fatalf("owner view: %+v", mine)
	}
	for _, id := range []string{b.Job.ID, uuid.NewString()} {
		if r := e.gql(ownerC, viewJobQuery, map[string]any{"id": id}); r.code() != "FORBIDDEN" {
			t.Fatalf("owner views %s: %+v", id, r.Errors)
		}
	}
	// Other users see nothing.
	e.mustGQL(otherC, listJobsQuery, nil).field(t, "jobs", &page)
	if len(page.Edges) != 0 {
		t.Fatalf("other list: %+v", page)
	}
	if r := e.gql(otherC, viewJobQuery, map[string]any{"id": a.Job.ID}); r.code() != "FORBIDDEN" {
		t.Fatalf("other view: %+v", r.Errors)
	}
	if r := e.gql(otherC, runNowMut, map[string]any{"id": a.Job.ID}); r.code() != "FORBIDDEN" {
		t.Fatalf("other run: %+v", r.Errors)
	}
	// The owner can run but not edit, delete or manage keys/owners.
	var run struct {
		QueuedAt string
		Job      struct{ ID string }
	}
	e.mustGQL(ownerC, runNowMut, map[string]any{"id": a.Job.ID}).field(t, "runJobNow", &run)
	if run.Job.ID != a.Job.ID || run.QueuedAt == "" {
		t.Fatalf("run: %+v", run)
	}
	if r := e.gql(adminC, runNowMut, map[string]any{"id": a.Job.ID}); r.code() != "CONFLICT" {
		t.Fatalf("overlapping run: %+v", r.Errors)
	}
	if r := e.gql(ownerC, runNowMut, map[string]any{"id": b.Job.ID}); r.code() != "FORBIDDEN" {
		t.Fatalf("owner runs other job: %+v", r.Errors)
	}
	for name, q := range map[string]string{
		"update":   `mutation($id: UUID!) { updateJob(id: $id, input: {name: "X"}) { id } }`,
		"delete":   `mutation($id: UUID!) { deleteJob(id: $id) }`,
		"disable":  `mutation($id: UUID!) { disableJob(id: $id) { id } }`,
		"generate": `mutation($id: UUID!) { generateJobPassphrase(jobId: $id) { version } }`,
		"export":   `mutation($id: UUID!) { exportJobPassphrases(jobId: $id) { fileName } }`,
	} {
		if r := e.gql(ownerC, q, map[string]any{"id": a.Job.ID}); r.code() != "FORBIDDEN" {
			t.Errorf("owner %s: %+v", name, r.Errors)
		}
	}
	if r := e.gql(ownerC, assignMut, map[string]any{"id": a.Job.ID, "u": []string{owner.ID.String()}}); r.code() != "FORBIDDEN" {
		t.Fatalf("owner assigns: %+v", r.Errors)
	}
	// Admin sees owners; removal ends access.
	var adminView *jobGQL
	e.mustGQL(adminC, viewJobQuery, map[string]any{"id": a.Job.ID}).field(t, "job", &adminView)
	if adminView == nil || adminView.Owners == nil || len(*adminView.Owners) != 1 {
		t.Fatalf("admin view owners: %+v", adminView)
	}
	e.freshStepUp(admin.ID)
	e.mustGQL(adminC, removeOwnerMut, map[string]any{"id": a.Job.ID, "u": owner.ID.String()})
	if r := e.gql(ownerC, viewJobQuery, map[string]any{"id": a.Job.ID}); r.code() != "FORBIDDEN" {
		t.Fatalf("view after removal: %+v", r.Errors)
	}
}

func TestJobsGraphQLEncryptionKeys(t *testing.T) {
	e := newJobEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	maint := e.user("maint@example.com", sqlc.UserRoleMaintainer)
	adminC := e.mustLogin("admin@example.com")
	maintC := e.mustLogin("maint@example.com")

	const chosen = "Correct-Horse-Battery-Staple-Quantum-Fjord-91"
	in := e.createInput("Keys")
	in["encryption"] = map[string]any{"mode": "PASSPHRASE", "passphrase": chosen}
	var c struct {
		Job                 jobGQL
		GeneratedPassphrase *string
	}
	e.freshStepUp(admin.ID)
	e.mustGQL(adminC, createJobMut, map[string]any{"in": in}).field(t, "createJob", &c)
	if c.GeneratedPassphrase != nil || c.Job.Encryption.Passphrase == nil {
		t.Fatalf("chosen passphrase: %+v", c)
	}
	id := c.Job.ID

	// A maintainer cannot choose a job's key at creation (user decision,
	// M3 review); the default generated passphrase is fine.
	e.freshStepUp(maint.ID)
	maintIn := e.createInput("Keys by maintainer")
	maintIn["encryption"] = map[string]any{"mode": "PASSPHRASE", "passphrase": chosen}
	if r := e.gql(maintC, createJobMut, map[string]any{"in": maintIn}); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer chosen key at creation: %+v", r.Errors)
	}
	maintIn["encryption"] = map[string]any{"mode": "RECIPIENT"}
	if r := e.gql(maintC, createJobMut, map[string]any{"in": maintIn}); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer recipient mode at creation: %+v", r.Errors)
	}
	delete(maintIn, "encryption")
	var mc struct{ GeneratedPassphrase *string }
	e.mustGQL(maintC, createJobMut, map[string]any{"in": maintIn}).field(t, "createJob", &mc)
	if mc.GeneratedPassphrase == nil {
		t.Fatal("maintainer default creation: no generated passphrase")
	}

	// Key changes are admin only, with step-up.
	genMut := `mutation($id: UUID!) { generateJobPassphrase(jobId: $id) { version passphrase encryption { mode passphrase { version } } } }`
	keyMuts := map[string]string{
		"generateJobPassphrase": genMut,
		"generateAgeKeypair":    `mutation($id: UUID!) { generateAgeKeypair(jobId: $id, label: "ops") { version } }`,
		"setJobPassphrase":      `mutation($id: UUID!) { setJobPassphrase(jobId: $id, passphrase: "Another-Very-Strong-Passphrase-Zebra-Kiwi-77") { mode } }`,
		"setJobRecipient":       `mutation($id: UUID!) { setJobRecipient(jobId: $id, recipient: "age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p") { mode } }`,
		"enableJobEncryption":   `mutation($id: UUID!) { enableJobEncryption(jobId: $id, mode: PASSPHRASE) { mode } }`,
	}
	for name, m := range keyMuts {
		if r := e.gql(maintC, m, map[string]any{"id": id}); r.code() != "FORBIDDEN" {
			t.Fatalf("maintainer %s: %+v", name, r.Errors)
		}
	}
	e.staleStepUp(admin.ID)
	if r := e.gql(adminC, genMut, map[string]any{"id": id}); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("stale generate: %+v", r.Errors)
	}
	e.freshStepUp(admin.ID)
	var gen struct {
		Version    int
		Passphrase string
	}
	e.mustGQL(adminC, genMut, map[string]any{"id": id}).field(t, "generateJobPassphrase", &gen)
	if gen.Version != 2 || len(strings.Fields(gen.Passphrase)) < 10 {
		t.Fatalf("generated: %+v", gen)
	}
	var kp struct {
		Version             int
		Recipient, Identity string
		Encryption          struct{ Mode string }
	}
	e.mustGQL(adminC, `mutation($id: UUID!) { generateAgeKeypair(jobId: $id, label: "ops") { version recipient identity encryption { mode } } }`,
		map[string]any{"id": id}).field(t, "generateAgeKeypair", &kp)
	if !strings.HasPrefix(kp.Recipient, "age1") || !strings.HasPrefix(kp.Identity, "AGE-SECRET-KEY-1") || kp.Encryption.Mode != "RECIPIENT" {
		t.Fatalf("keypair: %+v", kp)
	}
	identity := kp.Identity
	var enc struct{ Mode string }
	e.mustGQL(adminC, keyMuts["setJobPassphrase"], map[string]any{"id": id}).field(t, "setJobPassphrase", &enc)
	if enc.Mode != "PASSPHRASE" {
		t.Fatalf("set passphrase: %+v", enc)
	}

	// Opt-out and export are admin only.
	optOut := `mutation($id: UUID!) { disableJobEncryption(jobId: $id, reason: "data is public") { mode optedOut } }`
	if r := e.gql(maintC, optOut, map[string]any{"id": id}); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer opt-out: %+v", r.Errors)
	}
	var out struct {
		Mode     string
		OptedOut bool
	}
	e.mustGQL(adminC, optOut, map[string]any{"id": id}).field(t, "disableJobEncryption", &out)
	if out.Mode != "NONE" || !out.OptedOut {
		t.Fatalf("opt-out: %+v", out)
	}
	var view *jobGQL
	e.mustGQL(maintC, viewJobQuery, map[string]any{"id": id}).field(t, "job", &view)
	if !view.Encryption.OptedOut {
		t.Fatal("badge missing")
	}
	e.mustGQL(adminC, `mutation($id: UUID!) { enableJobEncryption(jobId: $id, mode: PASSPHRASE) { mode } }`, map[string]any{"id": id}).field(t, "enableJobEncryption", &enc)
	if enc.Mode != "PASSPHRASE" {
		t.Fatalf("opt-in: %+v", enc)
	}
	export := `mutation($id: UUID!) { exportJobPassphrases(jobId: $id) { fileName content versions } }`
	if r := e.gql(maintC, export, map[string]any{"id": id}); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer export: %+v", r.Errors)
	}
	var ex struct {
		FileName, Content string
		Versions          []int
	}
	e.mustGQL(adminC, export, map[string]any{"id": id}).field(t, "exportJobPassphrases", &ex)
	if len(ex.Versions) != 3 || !strings.Contains(ex.Content, chosen) || !strings.HasSuffix(ex.FileName, ".txt") {
		t.Fatalf("export: %+v", ex)
	}
	// Key operations on a deleted job: not found.
	e.mustGQL(adminC, `mutation($id: UUID!) { deleteJob(id: $id) }`, map[string]any{"id": id})
	if r := e.gql(adminC, genMut, map[string]any{"id": id}); r.code() != "NOT_FOUND" {
		t.Fatalf("deleted job key change: %+v", r.Errors)
	}

	// The chosen passphrase and the identity only ever appeared where D-142
	// allows (the export); never in the audit log.
	page, err := e.auditor.List(t.Context(), audit.Filter{}, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	for i := range page.Entries {
		d := string(page.Entries[i].Details)
		if strings.Contains(d, chosen) || strings.Contains(d, identity) || strings.Contains(d, gen.Passphrase) {
			t.Fatal("key material in the audit log")
		}
	}
	for _, body := range e.bodies {
		if strings.Contains(body, chosen) && !strings.Contains(body, "exportJobPassphrases") {
			t.Fatalf("chosen passphrase echoed: %s", body)
		}
	}
}
