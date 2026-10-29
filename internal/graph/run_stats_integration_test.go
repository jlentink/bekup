//go:build integration

package graph_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/runstate"
)

// Canonical stats objects as internal/jobs encodeStats writes them (D-151).
const (
	// PostgreSQL deep: exact counts inside the dump's snapshot.
	statsDeep = `{"consistency":"snapshot","level":"deep","objects":{"extensions":1,"functions":2,"materialized_views":0,"procedures":0,"schemas":1,"sequences":1,"tables":2,"triggers":0,"views":1},"tables":{"public.my%20table":{"count":"exact","rows":7},"public.users":{"count":"exact","rows":123}},"tables_total":2}`
	// MySQL standard: estimates, one table not counted, findings.
	statsStandard = `{"consistency":"approximate","level":"standard","objects":{"events":0,"functions":0,"procedures":0,"schemas":1,"sequences":0,"tables":3,"triggers":0,"views":0},"tables":{"logs":{"approximate":1,"count":"unknown"},"orders":{"approximate":1,"count":"estimate","rows":42}},"tables_total":3,"tables_truncated":1,"warnings":[{"code":"non_transactional_tables","count":1,"objects":["logs"]},{"code":"databases_excluded","count":2}]}`
)

// itemOf returns the single item of a completed run.
func (e *runEnv) itemOf(ref runstate.Ref) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(e.t.Context(), `SELECT id FROM run_items WHERE run_id = $1`, ref.RunID).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// addStats records the completeness evidence of a completed run's item.
func (e *runEnv) addStats(ref runstate.Ref, job uuid.UUID, level sqlc.JobVerification, consistency, stats string) {
	e.t.Helper()
	if err := sqlc.New(e.pool).InsertBackupStats(e.t.Context(), sqlc.InsertBackupStatsParams{
		RunItemID: e.itemOf(ref), RunID: ref.RunID, JobID: job, Level: level, Consistency: consistency,
		TablesTotal: 2, Stats: []byte(stats),
	}); err != nil {
		e.t.Fatal(err)
	}
}

type statsGQL struct {
	Level, Consistency string
	Objects            []struct {
		Kind  string
		Count float64
	}
	Tables []struct {
		Name                                  string
		Rows                                  *float64
		Count                                 string
		Exact, Estimate, Unknown, Approximate bool
	}
	TablesTotal     int
	TablesTruncated bool
	Warnings        []struct {
		Code      string
		Count     float64
		Objects   []string
		Truncated bool
	}
	Notices []struct{ Code string }
}

const runStatsQuery = `query($id: UUID!) { run(id: $id) { items { id stats { level consistency objects { kind count }
	tables { name rows count exact estimate unknown approximate } tablesTotal tablesTruncated
	warnings { code count objects truncated } notices { code } recordedAt } } } }`

func TestRunItemStats(t *testing.T) {
	e := newRunEnv(t)
	e.user("admin@example.com", sqlc.UserRoleAdmin)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	e.user("other@example.com", sqlc.UserRoleUser)
	e.own(e.jobA, owner.ID)
	adminC, ownerC, otherC := e.mustLogin("admin@example.com"), e.mustLogin("owner@example.com"), e.mustLogin("other@example.com")

	deep, standard, none := e.newRun(e.jobA, nil), e.newRun(e.jobB, nil), e.newRun(e.jobA, nil)
	for _, r := range []runstate.Ref{deep, standard, none} {
		e.completeRun(r)
	}
	e.addStats(deep, e.jobA, sqlc.JobVerificationDeep, "snapshot", statsDeep)
	e.addStats(standard, e.jobB, sqlc.JobVerificationStandard, "approximate", statsStandard)

	statsOf := func(cookie string, ref runstate.Ref) *statsGQL {
		t.Helper()
		var run struct {
			Items []struct {
				ID    string
				Stats *statsGQL
			}
		}
		e.mustGQL(cookie, runStatsQuery, map[string]any{"id": ref.RunID}).field(t, "run", &run)
		if len(run.Items) != 1 {
			t.Fatalf("items: %+v", run.Items)
		}
		return run.Items[0].Stats
	}

	// Deep PostgreSQL stats, read by the job's owner: exact counts.
	s := statsOf(ownerC, deep)
	if s == nil || s.Level != "DEEP" || s.Consistency != "SNAPSHOT" || s.TablesTotal != 2 || s.TablesTruncated {
		t.Fatalf("deep stats: %+v", s)
	}
	if len(s.Tables) != 2 || s.Tables[0].Name != "public.my table" || s.Tables[1].Name != "public.users" {
		t.Fatalf("tables (decoded, ordered): %+v", s.Tables)
	}
	for _, tb := range s.Tables {
		if tb.Count != "EXACT" || !tb.Exact || tb.Estimate || tb.Unknown || tb.Approximate || tb.Rows == nil {
			t.Fatalf("exact table: %+v", tb)
		}
	}
	if *s.Tables[1].Rows != 123 || len(s.Objects) != 9 || s.Objects[0].Kind != "extensions" || len(s.Warnings) != 0 || len(s.Notices) != 0 {
		t.Fatalf("deep details: %+v", s)
	}

	// Standard MySQL stats: estimates, an unknown count, findings split
	// into warnings and notices.
	s = statsOf(adminC, standard)
	if s == nil || s.Level != "STANDARD" || s.Consistency != "APPROXIMATE" || !s.TablesTruncated {
		t.Fatalf("standard stats: %+v", s)
	}
	if len(s.Tables) != 2 || s.Tables[0].Name != "logs" || s.Tables[0].Count != "UNKNOWN" || !s.Tables[0].Unknown || s.Tables[0].Rows != nil ||
		!s.Tables[0].Approximate || s.Tables[1].Count != "ESTIMATE" || !s.Tables[1].Estimate || s.Tables[1].Exact || *s.Tables[1].Rows != 42 {
		t.Fatalf("estimate tables: %+v", s.Tables)
	}
	if len(s.Warnings) != 1 || s.Warnings[0].Code != "non_transactional_tables" || len(s.Warnings[0].Objects) != 1 ||
		len(s.Notices) != 1 || s.Notices[0].Code != "databases_excluded" {
		t.Fatalf("findings: %+v / %+v", s.Warnings, s.Notices)
	}

	// No evidence recorded: null.
	if s := statsOf(adminC, none); s != nil {
		t.Fatalf("stats without a row: %+v", s)
	}

	// Same visibility as the run: other jobs' runs stay FORBIDDEN, and a
	// listing only carries the caller's runs.
	if r := e.gql(ownerC, runStatsQuery, map[string]any{"id": standard.RunID}); r.code() != "FORBIDDEN" {
		t.Fatalf("owner, other job: %+v", r.Errors)
	}
	if r := e.gql(otherC, runStatsQuery, map[string]any{"id": deep.RunID}); r.code() != "FORBIDDEN" {
		t.Fatalf("non-owner: %+v", r.Errors)
	}
	var page struct {
		Edges []struct {
			Node struct {
				Job   struct{ ID string }
				Items []struct{ Stats *struct{ Level string } }
			}
		}
	}
	e.mustGQL(ownerC, `{ runs { edges { node { job { id } items { stats { level } } } } } }`, nil).field(t, "runs", &page)
	if len(page.Edges) != 2 {
		t.Fatalf("owner page: %+v", page.Edges)
	}
	levels := 0
	for _, ed := range page.Edges {
		if ed.Node.Job.ID != e.jobA.String() {
			t.Fatalf("owner sees job %s", ed.Node.Job.ID)
		}
		for _, it := range ed.Node.Items {
			if it.Stats != nil {
				levels++
			}
		}
	}
	if levels != 1 {
		t.Fatalf("owner page stats: %d", levels)
	}
}

func TestRunItemStatsNoNPlusOne(t *testing.T) {
	e := newRunEnv(t)
	e.user("admin@example.com", sqlc.UserRoleAdmin)
	adminC := e.mustLogin("admin@example.com")
	for range 30 {
		r := e.newRun(e.jobA, nil)
		e.completeRun(r)
		e.addStats(r, e.jobA, sqlc.JobVerificationDeep, "snapshot", statsDeep)
	}
	before := e.counter.n.Load()
	var p struct {
		Edges []struct {
			Node struct {
				Items []struct {
					Stats *struct{ Tables []struct{ Name string } }
				}
			}
		}
	}
	e.mustGQL(adminC, `{ runs(first: 50) { edges { node { id items { id stats { level tables { name exact } warnings { code } } } } } } }`, nil).field(t, "runs", &p)
	if len(p.Edges) != 30 || p.Edges[29].Node.Items[0].Stats == nil || len(p.Edges[29].Node.Items[0].Stats.Tables) != 2 {
		t.Fatalf("page: %+v", p.Edges[29])
	}
	// One query for the page, one for all items, one for all stats.
	if n := e.counter.n.Load() - before; n != 3 {
		t.Fatalf("%d run reader queries for a page of 30 runs with stats, want 3", n)
	}
}
