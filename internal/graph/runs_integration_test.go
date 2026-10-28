//go:build integration

package graph_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/calendar"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph"
	"github.com/jlentink/bekup/internal/graph/streams"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/scheduler"
)

// countingDB counts the queries of the run reader and the calendar (N+1
// checks), including those inside transactions it begins.
type countingDB struct {
	sqlc.DBTX
	begin interface {
		Begin(context.Context) (pgx.Tx, error)
	}
	n atomic.Int64
}

func (c *countingDB) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := c.begin.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &countingTx{Tx: tx, n: &c.n}, nil
}

// countingTx counts the statements of a transaction begun by countingDB.
type countingTx struct {
	pgx.Tx
	n *atomic.Int64
}

func (c *countingTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	c.n.Add(1)
	return c.Tx.Exec(ctx, sql, args...)
}

func (c *countingTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	c.n.Add(1)
	return c.Tx.Query(ctx, sql, args...)
}

func (c *countingTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	c.n.Add(1)
	return c.Tx.QueryRow(ctx, sql, args...)
}

func (c *countingDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	c.n.Add(1)
	return c.DBTX.Exec(ctx, sql, args...)
}

func (c *countingDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	c.n.Add(1)
	return c.DBTX.Query(ctx, sql, args...)
}

func (c *countingDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	c.n.Add(1)
	return c.DBTX.QueryRow(ctx, sql, args...)
}

// runEnv is a destEnv whose handler has the job service, the run reader
// and event streams on a real HTTP server (task 3.7).
type runEnv struct {
	*destEnv
	store   *runstate.Store
	events  *pubsub.Bus
	counter *countingDB
	url     string
	dest    uuid.UUID
	jobA    uuid.UUID // owned by owner@
	jobB    uuid.UUID
	river   int64
}

func newRunEnv(t *testing.T) *runEnv {
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
	sessions := auth.NewSessions(e.pool, auth.SessionOptions{Events: auth.PubSubSessionEvents{Publisher: &pubsub.Publisher{Bus: bus}}})
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
	resolver := httpserver.SessionsResolver{Sessions: sessions}
	hub, err := streams.NewHub(streams.Options{Validator: resolver, Bus: bus, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	counter := &countingDB{DBTX: e.pool, begin: e.pool}
	gql, err := graph.New(graph.Options{
		Logger: logger, MaxComplexity: 5000, MaxDepth: 12, MaxBodyBytes: 1 << 16,
		Access: graph.NewPolicyAccess(pol), Policy: pol, AuditLog: e.auditor, Auth: svc,
		Jobs: js, JobKeys: keys, Runs: runstate.NewReader(counter), Bus: bus, Calendar: calendar.New(counter),
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

	re := &runEnv{destEnv: &destEnv{authEnv: e}, store: runstate.NewStore(e.pool, &pubsub.Publisher{Bus: bus}, nil), events: bus, counter: counter, url: ts.URL + "/graphql"}
	re.dest = uuid.New()
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO destinations (id, name, type, config) VALUES ($1, 'Local disk', 'disk', '{"path":"x"}')`, re.dest); err != nil {
		t.Fatal(err)
	}
	for _, j := range []*uuid.UUID{&re.jobA, &re.jobB} {
		if *j, err = dbtest.InsertJob(t.Context(), e.pool, dbtest.JobRow{Enabled: true, DestinationIDs: []uuid.UUID{re.dest}}); err != nil {
			t.Fatal(err)
		}
	}
	return re
}

func (e *runEnv) own(job, user uuid.UUID) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.t.Context(), `INSERT INTO job_owners (job_id, user_id) VALUES ($1, $2)`, job, user); err != nil {
		e.t.Fatal(err)
	}
}

// newRun creates a queued run of job (manual, triggered by by).
func (e *runEnv) newRun(job uuid.UUID, by *uuid.UUID) runstate.Ref {
	e.t.Helper()
	e.river++
	r, err := e.store.Begin(e.t.Context(), runstate.BeginParams{RiverJobID: e.river, Attempt: 1, JobID: job, Trigger: sqlc.RunTriggerManual, TriggeredBy: by})
	if err != nil {
		e.t.Fatal(err)
	}
	return r.Ref
}

// completeRun runs ref through one verified, signed database item.
func (e *runEnv) completeRun(ref runstate.Ref) {
	e.t.Helper()
	ctx := e.t.Context()
	now := time.Now()
	if err := e.store.Start(ctx, ref, 1, now, now); err != nil {
		e.t.Fatal(err)
	}
	it, err := e.store.AddItem(ctx, ref, sqlc.RunItemKindDatabase, "app", 0)
	if err != nil {
		e.t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(ref.RunID.String()))
	if err := e.store.FinishItem(ctx, ref, it.ID, runstate.ItemResult{
		Status: runstate.ItemSuccess, RawSize: 100, StoredSize: 40, SHA256: sum[:], Verified: true,
		Warnings: []runstate.Warning{{Code: "approximate_counts", Message: "row counts are estimates", Database: "app"}},
	}); err != nil {
		e.t.Fatal(err)
	}
	item := it.ID
	artID, err := e.store.AddArtifact(ctx, ref, runstate.Artifact{
		ItemID: &item, DestinationID: e.dest, Kind: sqlc.ArtifactKindDatabase, Key: "job/" + ref.RunID.String() + "/app.dump.zst.age",
		Size: 40, SHA256: sum[:], EncryptionMode: "passphrase", PassphraseVersion: 1, Verified: true,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.store.MarkSigned(ctx, ref, runstate.Artifact{
		DestinationID: e.dest, Kind: sqlc.ArtifactKindManifest, Key: "job/" + ref.RunID.String() + "/manifest.json",
		Size: 900, SHA256: sum[:], SigningKeyID: "0123456789abcdef",
	}, []uuid.UUID{artID}); err != nil {
		e.t.Fatal(err)
	}
	if err := e.store.Finish(ctx, ref, runstate.Result{Status: runstate.StatusSuccess, BytesRaw: 100, BytesStored: 40}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *runEnv) logs(ref runstate.Ref, lines ...string) {
	e.t.Helper()
	ll := make([]runstate.LogLine, 0, len(lines))
	for _, l := range lines {
		ll = append(ll, runstate.LogLine{Time: time.Now(), Level: runstate.LevelInfo, Stream: runstate.StreamWorker, Text: l})
	}
	if err := e.store.AppendLogs(e.t.Context(), ref, ll); err != nil {
		e.t.Fatal(err)
	}
}

type runPageGQL struct {
	Edges []struct {
		Cursor string
		Node   struct {
			ID          string
			Status      string
			Job         struct{ ID string }
			TriggeredBy *struct{ UserID, DisplayName string }
		}
	}
	PageInfo struct {
		HasNextPage bool
		EndCursor   *string
	}
}

const (
	runsQuery = `query($f: RunFilter, $first: Int, $after: String) { runs(filter: $f, first: $first, after: $after) {
		edges { cursor node { id status job { id } triggeredBy { userId displayName } } } pageInfo { hasNextPage endCursor } } }`
	runQuery = `query($id: UUID!) { run(id: $id) { id status trigger durationSeconds bytesStored errorCode
		warnings { code } triggeredBy { userId }
		items { database status verified sha256 warnings { code database } artifacts { id kind } }
		artifacts { kind verified signed state encryption { mode passphraseVersion } destination { name type } database }
		manifest { signed signingKeyId coveredItems totalItems copies { kind } } } }`
	runLogsQuery  = `query($id: UUID!, $after: ID, $first: Int) { runLogs(runId: $id, afterId: $after, first: $first) { lines { id line level stream } endId hasMore dropped } }`
	artifactsList = `query($f: ArtifactFilter, $first: Int, $after: String) { artifacts(filter: $f, first: $first, after: $after) {
		edges { node { id kind job { id } destination { id } } } pageInfo { hasNextPage endCursor } } }`
)

func (e *runEnv) runPage(cookie string, vars map[string]any) runPageGQL {
	e.t.Helper()
	var p runPageGQL
	e.mustGQL(cookie, runsQuery, vars).field(e.t, "runs", &p)
	return p
}

func TestRunsGraphQL(t *testing.T) {
	e := newRunEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	e.user("other@example.com", sqlc.UserRoleUser)
	e.own(e.jobA, owner.ID)
	adminC, maintC := e.mustLogin("admin@example.com"), e.mustLogin("maint@example.com")
	ownerC, otherC := e.mustLogin("owner@example.com"), e.mustLogin("other@example.com")

	aRuns, bRuns := make([]runstate.Ref, 0, 5), make([]runstate.Ref, 0, 3)
	for range 5 {
		aRuns = append(aRuns, e.newRun(e.jobA, &admin.ID))
	}
	for range 3 {
		bRuns = append(bRuns, e.newRun(e.jobB, &admin.ID))
	}
	done := aRuns[0]
	e.completeRun(done)

	// Pagination: newest first, no duplicates, every run once.
	seen := map[string]bool{}
	var after any
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("pagination does not end")
		}
		p := e.runPage(adminC, map[string]any{"first": 3, "after": after})
		for _, ed := range p.Edges {
			if seen[ed.Node.ID] {
				t.Fatalf("duplicate %s", ed.Node.ID)
			}
			seen[ed.Node.ID] = true
		}
		if !p.PageInfo.HasNextPage {
			break
		}
		after = *p.PageInfo.EndCursor
	}
	if len(seen) != 8 {
		t.Fatalf("admin saw %d runs", len(seen))
	}
	first := e.runPage(maintC, map[string]any{"first": 1})
	if first.Edges[0].Node.ID != bRuns[2].RunID.String() || first.Edges[0].Node.TriggeredBy == nil || first.Edges[0].Node.TriggeredBy.UserID != admin.ID.String() {
		t.Fatalf("newest first / triggeredBy for staff: %+v", first.Edges)
	}
	if r := e.gql(adminC, runsQuery, map[string]any{"after": "bm9wZQ"}); r.code() != "VALIDATION" {
		t.Fatalf("bad cursor: %+v", r.Errors)
	}
	if r := e.gql(adminC, runsQuery, map[string]any{"first": 201}); r.code() != "VALIDATION" {
		t.Fatalf("page size: %+v", r.Errors)
	}

	// Filters.
	for name, tc := range map[string]struct {
		f    map[string]any
		want int
	}{
		"job":            {map[string]any{"jobId": e.jobA}, 5},
		"status":         {map[string]any{"status": "SUCCESS"}, 1},
		"queued":         {map[string]any{"status": "QUEUED", "jobId": e.jobB}, 3},
		"manual":         {map[string]any{"trigger": "MANUAL"}, 8},
		"schedule":       {map[string]any{"trigger": "SCHEDULE"}, 0},
		"since future":   {map[string]any{"since": time.Now().Add(time.Hour).Format(time.RFC3339)}, 0},
		"until future":   {map[string]any{"until": time.Now().Add(time.Hour).Format(time.RFC3339)}, 8},
		"since and past": {map[string]any{"since": time.Now().Add(-time.Hour).Format(time.RFC3339)}, 8},
	} {
		if got := len(e.runPage(adminC, map[string]any{"f": tc.f}).Edges); got != tc.want {
			t.Errorf("%s: %d runs, want %d", name, got, tc.want)
		}
	}

	// Owner scope: own job's runs only, never who triggered them.
	op := e.runPage(ownerC, nil)
	if len(op.Edges) != 5 {
		t.Fatalf("owner saw %d runs", len(op.Edges))
	}
	for _, ed := range op.Edges {
		if ed.Node.Job.ID != e.jobA.String() || ed.Node.TriggeredBy != nil {
			t.Fatalf("owner run: %+v", ed.Node)
		}
	}
	if n := len(e.runPage(ownerC, map[string]any{"f": map[string]any{"jobId": e.jobB}}).Edges); n != 0 {
		t.Fatalf("owner filtered to another job: %d", n)
	}
	if n := len(e.runPage(otherC, nil).Edges); n != 0 {
		t.Fatalf("other user saw %d runs", n)
	}
	if r := e.gql("", runsQuery, nil); r.code() != "UNAUTHENTICATED" {
		t.Fatalf("anonymous: %+v", r.Errors)
	}

	// Single run: owners get FORBIDDEN for other jobs and unknown ids alike.
	for _, id := range []uuid.UUID{bRuns[0].RunID, uuid.New()} {
		if r := e.gql(ownerC, runQuery, map[string]any{"id": id}); r.code() != "FORBIDDEN" {
			t.Fatalf("owner run %s: %+v", id, r.Errors)
		}
	}
	if r := e.mustGQL(adminC, runQuery, map[string]any{"id": uuid.New()}); string(r.Data["run"]) != "null" {
		t.Fatalf("unknown run for admin: %s", r.Data["run"])
	}
	var detail struct {
		ID, Status, Trigger string
		DurationSeconds     *float64
		BytesStored         float64
		TriggeredBy         *struct{ UserID string }
		Items               []struct {
			Database, Status, Sha256 string
			Verified                 bool
			Warnings                 []struct{ Code, Database string }
			Artifacts                []struct{ ID, Kind string }
		}
		Artifacts []struct {
			Kind, State string
			Verified    bool
			Signed      bool
			Database    *string
			Encryption  struct{ Mode *string }
			Destination struct{ Name, Type string }
		}
		Manifest struct {
			Signed       bool
			SigningKeyID string
			CoveredItems int
			TotalItems   int
			Copies       []struct{ Kind string }
		}
	}
	e.mustGQL(ownerC, runQuery, map[string]any{"id": done.RunID}).field(t, "run", &detail)
	if detail.Status != "SUCCESS" || detail.Trigger != "MANUAL" || detail.TriggeredBy != nil || detail.BytesStored != 40 || detail.DurationSeconds == nil {
		t.Fatalf("run detail: %+v", detail)
	}
	if len(detail.Items) != 1 || detail.Items[0].Database != "app" || !detail.Items[0].Verified || len(detail.Items[0].Sha256) != 64 ||
		len(detail.Items[0].Warnings) != 1 || detail.Items[0].Warnings[0].Database != "app" || len(detail.Items[0].Artifacts) != 1 {
		t.Fatalf("items: %+v", detail.Items)
	}
	if len(detail.Artifacts) != 2 || !detail.Manifest.Signed || detail.Manifest.SigningKeyID != "0123456789abcdef" ||
		detail.Manifest.CoveredItems != 1 || detail.Manifest.TotalItems != 1 || len(detail.Manifest.Copies) != 1 {
		t.Fatalf("artifacts/manifest: %+v %+v", detail.Artifacts, detail.Manifest)
	}
	for _, a := range detail.Artifacts {
		if !a.Verified || !a.Signed || a.State != "PRESENT" || a.Destination.Type != "DISK" {
			t.Fatalf("artifact: %+v", a)
		}
		if a.Kind == "DATABASE" && (a.Encryption.Mode == nil || *a.Encryption.Mode != "PASSPHRASE" || a.Database == nil || *a.Database != "app") {
			t.Fatalf("dump artifact: %+v", a)
		}
	}
	e.mustGQL(adminC, runQuery, map[string]any{"id": done.RunID}).field(t, "run", &detail)
	if detail.TriggeredBy == nil || detail.TriggeredBy.UserID != admin.ID.String() {
		t.Fatalf("admin triggeredBy: %+v", detail.TriggeredBy)
	}

	// Logs: pagination, access.
	e.logs(aRuns[1], "one", "two", "three", "four", "five")
	type logPage struct {
		Lines   []struct{ ID, Line, Level, Stream string }
		EndID   *string
		HasMore bool
	}
	var lp logPage
	e.mustGQL(ownerC, runLogsQuery, map[string]any{"id": aRuns[1].RunID, "first": 2}).field(t, "runLogs", &lp)
	if len(lp.Lines) != 2 || lp.Lines[0].Line != "one" || !lp.HasMore || lp.Lines[0].Level != "INFO" || lp.Lines[0].Stream != "WORKER" {
		t.Fatalf("logs page 1: %+v", lp)
	}
	e.mustGQL(ownerC, runLogsQuery, map[string]any{"id": aRuns[1].RunID, "first": 2, "after": *lp.EndID}).field(t, "runLogs", &lp)
	if len(lp.Lines) != 2 || lp.Lines[0].Line != "three" || !lp.HasMore {
		t.Fatalf("logs page 2: %+v", lp)
	}
	e.mustGQL(ownerC, runLogsQuery, map[string]any{"id": aRuns[1].RunID, "first": 2, "after": *lp.EndID}).field(t, "runLogs", &lp)
	if len(lp.Lines) != 1 || lp.Lines[0].Line != "five" || lp.HasMore {
		t.Fatalf("logs page 3: %+v", lp)
	}
	for _, c := range []string{otherC} {
		if r := e.gql(c, runLogsQuery, map[string]any{"id": aRuns[1].RunID}); r.code() != "FORBIDDEN" {
			t.Fatalf("other logs: %+v", r.Errors)
		}
	}
	if r := e.gql(adminC, runLogsQuery, map[string]any{"id": aRuns[1].RunID, "after": "-1"}); r.code() != "VALIDATION" {
		t.Fatalf("bad afterId: %+v", r.Errors)
	}

	// Artifacts (backup browser).
	e.completeRun(bRuns[0])
	type artPage struct {
		Edges []struct {
			Node struct {
				ID, Kind string
				Job      struct{ ID string }
			}
		}
	}
	var ap artPage
	e.mustGQL(adminC, artifactsList, nil).field(t, "artifacts", &ap)
	if len(ap.Edges) != 4 {
		t.Fatalf("admin artifacts: %d", len(ap.Edges))
	}
	e.mustGQL(ownerC, artifactsList, nil).field(t, "artifacts", &ap)
	if len(ap.Edges) != 2 {
		t.Fatalf("owner artifacts: %d", len(ap.Edges))
	}
	for _, ed := range ap.Edges {
		if ed.Node.Job.ID != e.jobA.String() {
			t.Fatalf("owner sees artifact of job %s", ed.Node.Job.ID)
		}
	}
	e.mustGQL(adminC, artifactsList, map[string]any{"f": map[string]any{"kind": "MANIFEST", "destinationId": e.dest, "state": "PRESENT"}}).field(t, "artifacts", &ap)
	if len(ap.Edges) != 2 {
		t.Fatalf("manifest filter: %d", len(ap.Edges))
	}
	e.mustGQL(otherC, artifactsList, nil).field(t, "artifacts", &ap)
	if len(ap.Edges) != 0 {
		t.Fatalf("other artifacts: %d", len(ap.Edges))
	}

	// Job.lastRun / recentRuns.
	var jv struct {
		LastRun    *struct{ ID string }
		RecentRuns []struct{ ID string }
	}
	e.mustGQL(ownerC, `query($id: UUID!) { job(id: $id) { lastRun { id } recentRuns(first: 2) { id } } }`, map[string]any{"id": e.jobA}).field(t, "job", &jv)
	if jv.LastRun == nil || jv.LastRun.ID != aRuns[4].RunID.String() || len(jv.RecentRuns) != 2 || jv.RecentRuns[1].ID != aRuns[3].RunID.String() {
		t.Fatalf("job runs: %+v", jv)
	}
}

func TestRunsNoNPlusOne(t *testing.T) {
	e := newRunEnv(t)
	e.user("admin@example.com", sqlc.UserRoleAdmin)
	adminC := e.mustLogin("admin@example.com")
	for range 50 {
		e.completeRun(e.newRun(e.jobA, nil))
	}
	for range 3 {
		if _, err := dbtest.InsertJob(t.Context(), e.pool, dbtest.JobRow{Enabled: true, DestinationIDs: []uuid.UUID{e.dest}}); err != nil {
			t.Fatal(err)
		}
	}
	before := e.counter.n.Load()
	var p struct {
		Edges []struct {
			Node struct {
				Items     []struct{ Artifacts []struct{ ID string } }
				Artifacts []struct{ ID string }
				Manifest  struct{ Signed bool }
			}
		}
	}
	e.mustGQL(adminC, `{ runs(first: 50) { edges { node { id items { id artifacts { id } } artifacts { id } manifest { signed } } } } }`, nil).field(t, "runs", &p)
	if len(p.Edges) != 50 || len(p.Edges[49].Node.Items) != 1 || len(p.Edges[49].Node.Items[0].Artifacts) != 1 || !p.Edges[0].Node.Manifest.Signed {
		t.Fatalf("page: %+v", p.Edges[0])
	}
	// One query for the page, one for all items, one for all artifacts.
	if n := e.counter.n.Load() - before; n != 3 {
		t.Fatalf("%d run reader queries for a page of 50 runs, want 3", n)
	}
	before = e.counter.n.Load()
	e.mustGQL(adminC, `{ jobs(first: 50) { edges { node { id lastRun { id } recentRuns { id } } } } }`, nil)
	// One query per distinct per-job count (lastRun, recentRuns).
	if n := e.counter.n.Load() - before; n != 2 {
		t.Fatalf("%d run reader queries for a page of jobs, want 2", n)
	}
}

// --- event streams ---------------------------------------------------------------

type sseEv struct{ event, data string }

// openStream opens an SSE subscription with the session cookie and
// returns its events.
func (e *runEnv) openStream(cookie, query string, vars map[string]any) <-chan sseEv {
	e.t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		e.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Origin", testBaseURL)
	req.Header.Set("Cookie", auth.SessionCookieName+"="+cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		e.t.Fatal(err)
	}
	var wg sync.WaitGroup
	e.t.Cleanup(func() {
		cancel()
		closeResp(e.t, resp)
		wg.Wait()
	})
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("stream status %d", resp.StatusCode)
	}
	ch := make(chan sseEv, 64)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(ch)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		var ev sseEv
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if ev.event != "" || ev.data != "" {
					ch <- ev
				}
				ev = sseEv{}
			case strings.HasPrefix(line, "event:"):
				ev.event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				ev.data += strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
	}()
	return ch
}

func closeResp(t *testing.T, resp *http.Response) {
	if err := resp.Body.Close(); err != nil {
		t.Log(err)
	}
}

// nextEv returns the next event within d (ok false on timeout or close).
func nextEv(ch <-chan sseEv, d time.Duration) (sseEv, bool) {
	select {
	case ev, ok := <-ch:
		return ev, ok
	case <-time.After(d):
		return sseEv{}, false
	}
}

// nextData decodes the next "next" event into v.
func nextData(t *testing.T, ch <-chan sseEv, field string, v any) {
	t.Helper()
	for {
		ev, ok := nextEv(ch, 10*time.Second)
		if !ok {
			t.Fatalf("no %s event", field)
		}
		if ev.event != "next" {
			continue
		}
		var r gqlResp
		if err := json.Unmarshal([]byte(ev.data), &r); err != nil {
			t.Fatalf("decode %q: %v", ev.data, err)
		}
		if len(r.Errors) > 0 {
			t.Fatalf("stream error: %+v", r.Errors)
		}
		r.field(t, field, v)
		return
	}
}

const (
	runUpdatedSub = `subscription($job: UUID, $run: UUID) { runUpdated(jobId: $job, runId: $run) { id status job { id } triggeredBy { userId } items { database } } }`
	runLogSub     = `subscription($run: UUID!, $after: ID) { runLogAppended(runId: $run, afterId: $after) { runId lines { id line } endId done } }`
)

type runEvGQL struct {
	ID, Status  string
	Job         struct{ ID string }
	TriggeredBy *struct{ UserID string }
	Items       []struct{ Database string }
}

func TestRunEventStreams(t *testing.T) {
	e := newRunEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	e.user("other@example.com", sqlc.UserRoleUser)
	e.own(e.jobA, owner.ID)
	adminC, ownerC, otherC := e.mustLogin("admin@example.com"), e.mustLogin("owner@example.com"), e.mustLogin("other@example.com")

	// Filters the caller may not use are refused up front.
	b := e.newRun(e.jobB, nil)
	for name, vars := range map[string]map[string]any{
		"other job":   {"job": e.jobB},
		"other run":   {"run": b.RunID},
		"unknown run": {"run": uuid.New()},
	} {
		ch := e.openStream(ownerC, runUpdatedSub, vars)
		ev, ok := nextEv(ch, 5*time.Second)
		if !ok || !strings.Contains(ev.data, "FORBIDDEN") {
			t.Fatalf("%s: %+v", name, ev)
		}
	}

	ownerAll := e.openStream(ownerC, runUpdatedSub, nil)
	adminAll := e.openStream(adminC, runUpdatedSub, nil)
	otherAll := e.openStream(otherC, runUpdatedSub, nil)
	waitSubscribers(t, e, 3)

	// A run of another job is not delivered to the owner; the next event
	// is the owner's own run.
	e.newRun(e.jobB, &admin.ID)
	ref := e.newRun(e.jobA, &admin.ID)
	var got runEvGQL
	nextData(t, ownerAll, "runUpdated", &got)
	if got.ID != ref.RunID.String() || got.Status != "QUEUED" || got.TriggeredBy != nil {
		t.Fatalf("owner event: %+v", got)
	}
	// Staff receive both, with the actor.
	nextData(t, adminAll, "runUpdated", &got)
	if got.Job.ID != e.jobB.String() || got.TriggeredBy == nil {
		t.Fatalf("admin event 1: %+v", got)
	}
	nextData(t, adminAll, "runUpdated", &got)
	if got.ID != ref.RunID.String() {
		t.Fatalf("admin event 2: %+v", got)
	}

	// Log stream: batches, then done once the run finished.
	logs := e.openStream(ownerC, runLogSub, map[string]any{"run": ref.RunID})
	waitSubscribers(t, e, 4)
	e.logs(ref, "starting", "dumping app")
	var batch struct {
		RunID string
		Lines []struct{ ID, Line string }
		EndID *string
		Done  bool
	}
	lines := []string{}
	for len(lines) < 2 {
		nextData(t, logs, "runLogAppended", &batch)
		for _, l := range batch.Lines {
			lines = append(lines, l.Line)
		}
	}
	if lines[0] != "starting" || lines[1] != "dumping app" || batch.Done {
		t.Fatalf("log batches: %v %+v", lines, batch)
	}
	e.completeRun(ref)
	for !batch.Done {
		nextData(t, logs, "runLogAppended", &batch)
	}
	if ev, ok := nextEv(logs, 5*time.Second); !ok || ev.event != "complete" {
		t.Fatalf("log stream did not complete: %+v", ev)
	}
	// The owner saw the run's progress (running, items, success).
	for got.Status != "SUCCESS" {
		nextData(t, ownerAll, "runUpdated", &got)
	}
	if len(got.Items) != 1 || got.Items[0].Database != "app" {
		t.Fatalf("final event: %+v", got)
	}
	// The other user never received anything.
	if ev, ok := nextEv(otherAll, 300*time.Millisecond); ok {
		t.Fatalf("other user received %+v", ev)
	}

	// Losing ownership stops delivery at once (delivery-time authz).
	if _, err := e.pool.Exec(t.Context(), `DELETE FROM job_owners WHERE job_id = $1 AND user_id = $2`, e.jobA, owner.ID); err != nil {
		t.Fatal(err)
	}
	drain(ownerAll)
	e.newRun(e.jobA, nil)
	// The admin still receives it (after the progress events of ref).
	for got = (runEvGQL{}); got.Job.ID != e.jobA.String() || got.Status != "QUEUED"; {
		nextData(t, adminAll, "runUpdated", &got)
	}
	if ev, ok := nextEv(ownerAll, time.Second); ok {
		t.Fatalf("former owner received %+v", ev)
	}

	// Logout ends the stream with UNAUTHENTICATED.
	e.own(e.jobA, owner.ID)
	if r := e.gql(ownerC, logoutMutation, nil); len(r.Errors) > 0 {
		t.Fatal(r.Errors)
	}
	code := ""
	for {
		ev, ok := nextEv(ownerAll, 10*time.Second)
		if !ok {
			t.Fatalf("stream did not end (code %q)", code)
		}
		if ev.event == "complete" {
			break
		}
		if strings.Contains(ev.data, `"UNAUTHENTICATED"`) {
			code = "UNAUTHENTICATED"
		}
	}
	if code != "UNAUTHENTICATED" {
		t.Fatalf("end code %q", code)
	}
}

// waitSubscribers waits until n streams subscribed to run events.
func waitSubscribers(t *testing.T, e *runEnv, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for e.events.Subscribers(pubsub.RunEvents.Name) < n {
		if time.Now().After(deadline) {
			t.Fatalf("%d run event subscribers, want %d", e.events.Subscribers(pubsub.RunEvents.Name), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func drain(ch <-chan sseEv) {
	for {
		select {
		case <-ch:
		case <-time.After(300 * time.Millisecond):
			return
		}
	}
}
