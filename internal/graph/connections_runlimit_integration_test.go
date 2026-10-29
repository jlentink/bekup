//go:build integration

package graph_test

// The per-connection run limit over GraphQL (task 3.13, D-156): visible to
// admins and maintainers, changed only by admins with a fresh step-up,
// validated against workers.max_concurrent_runs and audited by field name.
// No runner is needed: create/update never call it.

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// noRunner panics on any runner RPC (create/update never make one).
type noRunner struct{ runnerpb.RunnerServiceClient }

func newRunLimitEnv(t *testing.T) *destEnv {
	t.Helper()
	return newRunnerlessConnEnv(t, noRunner{})
}

// newRunnerlessConnEnv is a GraphQL environment whose connection service
// talks to the given in-process runner fake.
func newRunnerlessConnEnv(t *testing.T, runner runnerpb.RunnerServiceClient) *destEnv {
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
	conns, err := connections.New(connections.Options{
		DB: e.pool, Keyring: kr, Auditor: e.auditor, Runner: runner,
		Limiter:           func(string) (bool, time.Duration) { return true, 0 },
		MaxConcurrentRuns: 6, DefaultRunsPerConnection: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	pol := authz.New(authz.Options{})
	logger := slog.New(slog.DiscardHandler)
	gql, err := graph.New(graph.Options{
		Logger: logger, MaxComplexity: 2000, MaxDepth: 12, MaxBodyBytes: 1 << 16,
		Access: graph.NewPolicyAccess(pol), Policy: pol, AuditLog: e.auditor, Auth: svc, Connections: conns,
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
	return &destEnv{authEnv: e}
}

func TestConnectionRunLimitGraphQL(t *testing.T) {
	e := newRunLimitEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	e.user("plain@example.com", sqlc.UserRoleUser)
	adminC := e.mustLogin("admin@example.com")
	maintC := e.mustLogin("maint@example.com")
	plainC := e.mustLogin("plain@example.com")

	const fields = `id maxConcurrentRuns effectiveMaxConcurrentRuns`
	const createMut = `mutation($in: CreateConnectionInput!) { createConnection(input: $in) { ` + fields + ` } }`
	const updateMut = `mutation($id: UUID!, $in: UpdateConnectionInput!) { updateConnection(id: $id, input: $in) { ` + fields + ` } }`
	const getQuery = `query($id: UUID!) { connection(id: $id) { ` + fields + ` } }`
	type limits struct {
		ID                         string
		MaxConcurrentRuns          *int
		EffectiveMaxConcurrentRuns int
	}
	in := func(n any) map[string]any {
		m := map[string]any{"name": "Primary PG", "engine": "POSTGRES", "versionKey": "17", "host": "db.example.test", "username": "u"}
		if n != nil {
			m["maxConcurrentRuns"] = n
		}
		return map[string]any{"in": m}
	}

	// Without the field: the global default applies.
	e.freshStepUp(admin.ID)
	var c limits
	e.mustGQL(adminC, createMut, in(nil)).field(t, "createConnection", &c)
	if c.MaxConcurrentRuns != nil || c.EffectiveMaxConcurrentRuns != 2 {
		t.Fatalf("default: %+v", c)
	}
	id := c.ID

	// Bounds: 0, 65 and above the global cap (6) are VALIDATION errors.
	for _, bad := range []int{0, 65, 7} {
		if r := e.gql(adminC, updateMut, map[string]any{"id": id, "in": map[string]any{"maxConcurrentRuns": bad}}); r.code() != "VALIDATION" {
			t.Fatalf("update with %d: %+v", bad, r.Errors)
		}
		r := e.gql(adminC, createMut, in(bad))
		if r.code() != "VALIDATION" {
			t.Fatalf("create with %d: %+v", bad, r.Errors)
		}
	}

	// Step-up is required; maintainers and users cannot change it.
	set3 := map[string]any{"id": id, "in": map[string]any{"maxConcurrentRuns": 3}}
	e.staleStepUp(admin.ID)
	if r := e.gql(adminC, updateMut, set3); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("update without step-up: %+v", r.Errors)
	}
	for name, ck := range map[string]string{"maintainer": maintC, "user": plainC} {
		if r := e.gql(ck, updateMut, set3); r.code() != "FORBIDDEN" {
			t.Fatalf("%s update: %+v", name, r.Errors)
		}
	}
	e.freshStepUp(admin.ID)
	e.mustGQL(adminC, updateMut, set3).field(t, "updateConnection", &c)
	if c.MaxConcurrentRuns == nil || *c.MaxConcurrentRuns != 3 || c.EffectiveMaxConcurrentRuns != 3 {
		t.Fatalf("set: %+v", c)
	}

	// Maintainers see both fields (metadata); users see nothing.
	var m limits
	e.mustGQL(maintC, getQuery, map[string]any{"id": id}).field(t, "connection", &m)
	if m.MaxConcurrentRuns == nil || *m.MaxConcurrentRuns != 3 || m.EffectiveMaxConcurrentRuns != 3 {
		t.Fatalf("maintainer view: %+v", m)
	}
	if r := e.gql(plainC, getQuery, map[string]any{"id": id}); r.code() != "FORBIDDEN" {
		t.Fatalf("user view: %+v", r.Errors)
	}

	// The change is audited by field name with old/new values.
	page, err := e.auditor.List(t.Context(), audit.Filter{Action: audit.ActionConnectionUpdated}, 0, 10)
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("audit: %v %d", err, len(page.Entries))
	}
	var det struct {
		Changed []string `json:"changed"`
		Values  map[string]struct {
			Old *int `json:"old"`
			New *int `json:"new"`
		} `json:"values"`
	}
	if err := json.Unmarshal(page.Entries[0].Details, &det); err != nil {
		t.Fatal(err)
	}
	v, ok := det.Values["max_concurrent_runs"]
	if !strings.Contains(strings.Join(det.Changed, ","), "max_concurrent_runs") || !ok || v.Old != nil || v.New == nil || *v.New != 3 {
		t.Fatalf("audit details: %s", page.Entries[0].Details)
	}

	// Clearing returns to the global default.
	e.mustGQL(adminC, updateMut, map[string]any{"id": id, "in": map[string]any{"clearMaxConcurrentRuns": true}}).field(t, "updateConnection", &c)
	if c.MaxConcurrentRuns != nil || c.EffectiveMaxConcurrentRuns != 2 {
		t.Fatalf("cleared: %+v", c)
	}
}
