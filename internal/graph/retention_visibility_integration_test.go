//go:build integration

package graph_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

const unrecordedQuery = `query($d: UUID!, $first: Int, $after: String) { destinationUnrecordedFiles(destinationId: $d, first: $first, after: $after) {
	destinationId scannedAt totalCount totalBytes truncated prunedDirectories nextCursor
	files { id key size modifiedAt firstSeenAt lastSeenAt } } }`

const deleteUnrecordedMut = `mutation($d: UUID!, $id: UUID!) { deleteUnrecordedFile(destinationId: $d, id: $id) { ok code message } }`

type unrecordedModel struct {
	TotalCount int
	TotalBytes float64
	ScannedAt  *string
	NextCursor *string
	Files      []struct {
		ID   string
		Key  string
		Size float64
	}
}

// TestUnrecordedFilesGraphQL: the unrecorded-file query and delete are
// admin only, the delete needs step-up and is the only field of its
// operation (D-209).
func TestUnrecordedFilesGraphQL(t *testing.T) {
	e := newDestEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	e.user("plain@example.com", sqlc.UserRoleUser)
	adminC := e.mustLogin("admin@example.com")
	maintC := e.mustLogin("maint@example.com")
	plainC := e.mustLogin("plain@example.com")
	e.freshStepUp(admin.ID)

	var disk destModel
	e.mustGQL(adminC, createDestMut, map[string]any{"in": map[string]any{
		"name": "Local disk", "type": "DISK", "disk": map[string]any{"path": "local"},
	}}).field(t, "createDestination", &disk)
	key := "nightly/2026-01-01T000000Z/orphan.dump"
	p := filepath.Join(e.root, "local", filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
	id := uuid.MustParse(disk.ID)
	d, err := e.svc.Open(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ScanUnrecorded(t.Context(), id, d); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	vars := map[string]any{"d": disk.ID}
	var page unrecordedModel
	e.mustGQL(adminC, unrecordedQuery, vars).field(t, "destinationUnrecordedFiles", &page)
	if page.TotalCount != 1 || page.ScannedAt == nil || len(page.Files) != 1 || page.Files[0].Key != key || page.Files[0].Size != 6 || page.NextCursor != nil {
		t.Fatalf("page = %+v", page)
	}
	file := page.Files[0].ID
	delVars := map[string]any{"d": disk.ID, "id": file}
	for who, c := range map[string]string{"maintainer": maintC, "user": plainC} {
		if r := e.gql(c, unrecordedQuery, vars); r.code() != "FORBIDDEN" {
			t.Errorf("%s query: %+v", who, r.Errors)
		}
		if r := e.gql(c, deleteUnrecordedMut, delVars); r.code() != "FORBIDDEN" {
			t.Errorf("%s delete: %+v", who, r.Errors)
		}
	}
	if r := e.gql("", unrecordedQuery, vars); r.code() != "UNAUTHENTICATED" {
		t.Errorf("anonymous: %+v", r.Errors)
	}
	if r := e.gql(adminC, unrecordedQuery, map[string]any{"d": uuid.NewString()}); r.code() != "NOT_FOUND" {
		t.Errorf("unknown destination: %+v", r.Errors)
	}
	if r := e.gql(adminC, unrecordedQuery, map[string]any{"d": disk.ID, "first": 500}); r.code() != "VALIDATION" {
		t.Errorf("first 500: %+v", r.Errors)
	}
	// Single root field.
	both := `mutation($d: UUID!, $id: UUID!) { a: deleteUnrecordedFile(destinationId: $d, id: $id) { ok } b: deleteUnrecordedFile(destinationId: $d, id: $id) { ok } }`
	if r := e.gql(adminC, both, delVars); r.code() != "VALIDATION" {
		t.Errorf("two root fields: %+v", r.Errors)
	}
	// Step-up.
	e.staleStepUp(admin.ID)
	if r := e.gql(adminC, deleteUnrecordedMut, delVars); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("stale step-up: %+v", r.Errors)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("file deleted without step-up")
	}
	e.freshStepUp(admin.ID)
	var res struct {
		OK      bool `json:"ok"`
		Code    string
		Message string
	}
	e.mustGQL(adminC, deleteUnrecordedMut, delVars).field(t, "deleteUnrecordedFile", &res)
	if !res.OK || res.Code != "ok" || res.Message == "" {
		t.Fatalf("delete = %+v", res)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("file kept: %v", err)
	}
	if r := e.gql(adminC, deleteUnrecordedMut, delVars); r.code() != "NOT_FOUND" {
		t.Fatalf("second delete: %+v", r.Errors)
	}
}

// TestRetentionHoldsGraphQL: Job.retentionHolds is visible to everyone who
// sees the job (owners included) and previewSchedule estimates the kept
// backups (D-209).
func TestRetentionHoldsGraphQL(t *testing.T) {
	e := newJobEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	e.user("other@example.com", sqlc.UserRoleUser)
	adminC := e.mustLogin("admin@example.com")
	ownerC := e.mustLogin("owner@example.com")
	otherC := e.mustLogin("other@example.com")
	e.freshStepUp(admin.ID)

	var created struct{ Job struct{ ID string } }
	e.mustGQL(adminC, createJobMut, map[string]any{"in": e.createInput("Nightly")}).field(t, "createJob", &created)
	jobID := created.Job.ID
	e.mustGQL(adminC, assignMut, map[string]any{"id": jobID, "u": []string{owner.ID.String()}})
	since := time.Date(2026, 9, 1, 3, 0, 0, 0, time.UTC)
	newest := time.Date(2026, 8, 31, 3, 0, 0, 0, time.UTC)
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO retention_holds (job_id, destination_id, reason, held_runs, held_since, newest_complete_at, first_seen_at, checked_at)
		VALUES ($1, $2, 'newer_than_newest_complete', 4, $3, $4, now(), now())`, jobID, e.dest, since, newest); err != nil {
		t.Fatal(err)
	}
	type hold struct {
		DestinationID    string
		DestinationName  string
		DestinationType  string
		Reason           string
		HeldRuns         int
		Since            time.Time
		NewestCompleteAt *time.Time
	}
	q := `query($id: UUID!) { job(id: $id) { retentionHolds { destinationId destinationName destinationType destinationDeleted reason heldRuns since newestCompleteAt checkedAt } } }`
	for who, c := range map[string]string{"admin": adminC, "owner": ownerC} {
		var j struct{ RetentionHolds []hold }
		e.mustGQL(c, q, map[string]any{"id": jobID}).field(t, "job", &j)
		if len(j.RetentionHolds) != 1 {
			t.Fatalf("%s holds: %+v", who, j)
		}
		h := j.RetentionHolds[0]
		if h.DestinationID != e.dest.String() || h.DestinationName != "Local disk" || h.DestinationType != "DISK" ||
			h.Reason != "NEWER_THAN_NEWEST_COMPLETE" || h.HeldRuns != 4 || !h.Since.Equal(since) || h.NewestCompleteAt == nil || !h.NewestCompleteAt.Equal(newest) {
			t.Fatalf("%s hold: %+v", who, h)
		}
	}
	if r := e.gql(otherC, q, map[string]any{"id": jobID}); r.code() != "FORBIDDEN" {
		t.Fatalf("non-owner: %+v", r.Errors)
	}
	// The list batch-loads them per page.
	var list struct {
		Edges []struct {
			Node struct {
				ID             string
				RetentionHolds []hold
			}
		}
	}
	e.mustGQL(ownerC, `{ jobs { edges { node { id retentionHolds { reason heldRuns } } } } }`, nil).field(t, "jobs", &list)
	if len(list.Edges) != 1 || len(list.Edges[0].Node.RetentionHolds) != 1 {
		t.Fatalf("list: %+v", list)
	}

	// previewSchedule with retention.
	pq := `query($in: ScheduleInput!, $r: JobRetentionInput) { previewSchedule(input: $in, count: 1, retention: $r) { next retention { kept byKeepLast byKeepDays atLeast } } }`
	sched := map[string]any{"kind": "INTERVAL", "every": 1, "unit": "DAYS", "anchor": "2026-01-01T03:00", "timezone": "UTC"}
	var prev struct {
		Retention *struct {
			Kept       *int
			ByKeepLast int
			ByKeepDays *int
			AtLeast    bool
		}
	}
	e.mustGQL(adminC, pq, map[string]any{"in": sched, "r": map[string]any{"keepLast": 7, "keepDays": 30}}).field(t, "previewSchedule", &prev)
	if prev.Retention == nil || prev.Retention.Kept == nil || *prev.Retention.Kept != 30 || prev.Retention.ByKeepLast != 7 ||
		prev.Retention.ByKeepDays == nil || *prev.Retention.ByKeepDays != 30 || prev.Retention.AtLeast {
		t.Fatalf("estimate: %+v", prev.Retention)
	}
	e.mustGQL(adminC, pq, map[string]any{"in": sched}).field(t, "previewSchedule", &prev)
	if prev.Retention != nil {
		t.Fatalf("estimate without retention: %+v", prev.Retention)
	}
	if r := e.gql(adminC, pq, map[string]any{"in": sched, "r": map[string]any{}}); r.code() != "VALIDATION" {
		t.Fatalf("empty retention: %+v", r.Errors)
	}
}
