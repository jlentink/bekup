//go:build integration

package graph_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

const startEarlyMut = `mutation($id: UUID!) { startNextRunEarly(id: $id) { runId queuedAt job { id nextRunAt } } }`

type startEarlyGQL struct {
	RunID    string
	QueuedAt string
	Job      struct {
		ID        string
		NextRunAt *string
	}
}

// TestJobsGraphQLStartNextRunEarly: task 3.12 (D-156) end to end.
func TestJobsGraphQLStartNextRunEarly(t *testing.T) {
	e := newJobEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	e.user("other@example.com", sqlc.UserRoleUser)
	adminC := e.mustLogin("admin@example.com")
	ownerC := e.mustLogin("owner@example.com")
	otherC := e.mustLogin("other@example.com")

	var a, b struct{ Job jobGQL }
	e.mustGQL(adminC, createJobMut, map[string]any{"in": e.createInput("Alpha")}).field(t, "createJob", &a)
	e.mustGQL(adminC, createJobMut, map[string]any{"in": e.createInput("Beta")}).field(t, "createJob", &b)
	e.freshStepUp(admin.ID)
	e.mustGQL(adminC, assignMut, map[string]any{"id": a.Job.ID, "u": []string{owner.ID.String()}})

	if a.Job.NextRunAt == nil {
		t.Fatal("no next run")
	}
	before, err := time.Parse(time.RFC3339, *a.Job.NextRunAt)
	if err != nil {
		t.Fatal(err)
	}
	// Other users and owners of other jobs are denied.
	if r := e.gql(otherC, startEarlyMut, map[string]any{"id": a.Job.ID}); r.code() != "FORBIDDEN" {
		t.Fatalf("other: %+v", r.Errors)
	}
	if r := e.gql(ownerC, startEarlyMut, map[string]any{"id": b.Job.ID}); r.code() != "FORBIDDEN" {
		t.Fatalf("owner of another job: %+v", r.Errors)
	}

	var res startEarlyGQL
	e.mustGQL(ownerC, startEarlyMut, map[string]any{"id": a.Job.ID}).field(t, "startNextRunEarly", &res)
	if res.Job.ID != a.Job.ID || res.QueuedAt == "" || res.Job.NextRunAt == nil {
		t.Fatalf("result: %+v", res)
	}
	after, err := time.Parse(time.RFC3339, *res.Job.NextRunAt)
	if err != nil {
		t.Fatal(err)
	}
	// Daily: one occurrence later (23-25 hours around a DST change).
	if d := after.Sub(before); d < 23*time.Hour || d > 25*time.Hour {
		t.Fatalf("next run moved by %s (%s -> %s)", d, before, after)
	}
	var trigger string
	var scheduledFor time.Time
	if err := e.pool.QueryRow(t.Context(), "SELECT trigger::text, scheduled_for FROM runs WHERE id = $1",
		uuid.MustParse(res.RunID)).Scan(&trigger, &scheduledFor); err != nil {
		t.Fatal(err)
	}
	if trigger != "early" || !scheduledFor.Equal(before) {
		t.Fatalf("run: %s %s", trigger, scheduledFor)
	}

	// The next planned run was already started early: VALIDATION (the
	// schedule is never pushed more than one occurrence ahead, D-160), next
	// run unchanged.
	if r := e.gql(ownerC, startEarlyMut, map[string]any{"id": a.Job.ID}); r.code() != "VALIDATION" {
		t.Fatalf("second early start: %+v", r.Errors)
	}
	var view *jobGQL
	e.mustGQL(adminC, viewJobQuery, map[string]any{"id": a.Job.ID}).field(t, "job", &view)
	if view == nil || view.NextRunAt == nil || *view.NextRunAt != *res.Job.NextRunAt {
		t.Fatalf("next run after refusal: %+v", view)
	}

	// A run is active: CONFLICT, next run unchanged.
	e.mustGQL(adminC, runNowMut, map[string]any{"id": b.Job.ID})
	if r := e.gql(adminC, startEarlyMut, map[string]any{"id": b.Job.ID}); r.code() != "CONFLICT" {
		t.Fatalf("overlap: %+v", r.Errors)
	}
	var viewB *jobGQL
	e.mustGQL(adminC, viewJobQuery, map[string]any{"id": b.Job.ID}).field(t, "job", &viewB)
	if viewB == nil || viewB.NextRunAt == nil || b.Job.NextRunAt == nil || *viewB.NextRunAt != *b.Job.NextRunAt {
		t.Fatalf("next run after conflict: %+v", viewB)
	}

	// Disabled and manual jobs: VALIDATION.
	e.mustGQL(adminC, `mutation($id: UUID!) { disableJob(id: $id) { id } }`, map[string]any{"id": b.Job.ID})
	if r := e.gql(adminC, startEarlyMut, map[string]any{"id": b.Job.ID}); r.code() != "VALIDATION" {
		t.Fatalf("disabled: %+v", r.Errors)
	}
	in := e.createInput("Manual")
	in["schedule"] = map[string]any{"kind": "MANUAL"}
	var m struct{ Job jobGQL }
	e.mustGQL(adminC, createJobMut, map[string]any{"in": in}).field(t, "createJob", &m)
	if r := e.gql(adminC, startEarlyMut, map[string]any{"id": m.Job.ID}); r.code() != "VALIDATION" {
		t.Fatalf("manual: %+v", r.Errors)
	}
	// Unknown job: NOT_FOUND for staff.
	if r := e.gql(adminC, startEarlyMut, map[string]any{"id": uuid.NewString()}); r.code() != "NOT_FOUND" {
		t.Fatalf("unknown: %+v", r.Errors)
	}
}
