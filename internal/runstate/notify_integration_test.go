//go:build integration

package runstate

import (
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

// TestRunNotificationEvents: starting and finishing a run add the D-161
// backup events to the notification outbox in the same transaction
// (task 5.3).
func TestRunNotificationEvents(t *testing.T) {
	e := newStEnv(t)
	ctx := t.Context()
	enq := e.enqueue(false)
	run, err := e.store.Begin(ctx, BeginParams{RiverJobID: enq.JobID, Attempt: 1, JobID: e.job, Trigger: sqlc.RunTriggerSchedule})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := e.store.Start(ctx, run.Ref, 1, now, now); err != nil {
		t.Fatal(err)
	}
	if err := e.store.Finish(ctx, run.Ref, Result{Status: StatusFailed, Failure: &Failure{Code: "dump_failed", Message: "x"}}); err != nil {
		t.Fatal(err)
	}
	rows, err := e.pool.Query(ctx, `SELECT type, job_id, run_id, detail->>'error_code' FROM notification_events ORDER BY created_at, type`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var typ string
		var job, runID [16]byte
		var code *string
		if err := rows.Scan(&typ, &job, &runID, &code); err != nil {
			t.Fatal(err)
		}
		if job != e.job || runID != run.RunID {
			t.Fatalf("%s: wrong subject", typ)
		}
		if typ == "backup.failed" && (code == nil || *code != "dump_failed") {
			t.Fatalf("error code %v", code)
		}
		got = append(got, typ)
	}
	if len(got) != 2 || got[0] != "backup.started" || got[1] != "backup.failed" {
		t.Fatalf("events %v", got)
	}
}
