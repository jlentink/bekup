//go:build integration

package jobs

import (
	"testing"

	"github.com/jlentink/bekup/internal/scheduler"
)

// retentionSweeps returns the reasons of the queued retention sweeps of job.
func (e *env) retentionSweeps(job string) []string {
	e.t.Helper()
	rows, err := e.pool.Query(e.t.Context(), `SELECT args->>'reason' FROM river_job
		WHERE kind = 'retention_sweep' AND args->>'job_id' = $1 ORDER BY id`, job)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// TestRetentionEditEnqueuesSweep: a changed retention policy enqueues a
// retention sweep of the job in the update's transaction (D-209); other
// changes do not, and a sweep still queued absorbs the next change.
func TestRetentionEditEnqueuesSweep(t *testing.T) {
	e := newEnv(t)
	j := e.create("Nightly")
	if s := e.retentionSweeps(j.ID.String()); len(s) != 0 {
		t.Fatalf("sweeps after create: %v", s)
	}
	desc := "described"
	if _, err := e.svc.Update(e.admin.ctx, e.admin.actor, j.ID, UpdateInput{Description: &desc}); err != nil {
		t.Fatal(err)
	}
	if s := e.retentionSweeps(j.ID.String()); len(s) != 0 {
		t.Fatalf("sweeps after a description change: %v", s)
	}
	// The same retention again is no change.
	if _, err := e.svc.Update(e.admin.ctx, e.admin.actor, j.ID, UpdateInput{Retention: &Retention{KeepLast: keep(7)}}); err != nil {
		t.Fatal(err)
	}
	if s := e.retentionSweeps(j.ID.String()); len(s) != 0 {
		t.Fatalf("sweeps after an unchanged retention: %v", s)
	}
	if _, err := e.svc.Update(e.admin.ctx, e.admin.actor, j.ID, UpdateInput{Retention: &Retention{KeepLast: keep(3)}}); err != nil {
		t.Fatal(err)
	}
	if s := e.retentionSweeps(j.ID.String()); len(s) != 1 || s[0] != "policy_changed" {
		t.Fatalf("sweeps after a retention change: %v", s)
	}
	// Still queued: the next change is absorbed by it (unique per job).
	if _, err := e.svc.Update(e.admin.ctx, e.admin.actor, j.ID, UpdateInput{Retention: &Retention{KeepDays: keep(10)}}); err != nil {
		t.Fatal(err)
	}
	if s := e.retentionSweeps(j.ID.String()); len(s) != 1 {
		t.Fatalf("duplicate sweep: %v", s)
	}
	// A failed update enqueues nothing (same transaction).
	if _, err := e.svc.Update(e.admin.ctx, e.admin.actor, j.ID, UpdateInput{Retention: &Retention{KeepLast: keep(0)}}); fieldOf(t, err) != "retention.keepLast" {
		t.Fatalf("invalid retention: %v", err)
	}
}

func TestEstimateRetention(t *testing.T) {
	e := newEnv(t)
	got, err := e.svc.EstimateRetention(daily("03:00", "UTC"), Retention{KeepLast: keep(7), KeepDays: keep(30)})
	if err != nil || !got.Known || got.Kept != 30 || got.ByKeepLast != 7 || got.ByKeepDays != 30 {
		t.Fatalf("daily 7 + 30 days: %+v %v", got, err)
	}
	m, err := e.svc.EstimateRetention(scheduler.Schedule{Kind: scheduler.KindManual}, Retention{KeepDays: keep(3)})
	if err != nil || m.Known {
		t.Fatalf("manual with days: %+v %v", m, err)
	}
	if _, err := e.svc.EstimateRetention(daily("03:00", "UTC"), Retention{}); fieldOf(t, err) != "retention" {
		t.Fatalf("no limits: %v", err)
	}
	if _, err := e.svc.EstimateRetention(scheduler.Schedule{Kind: scheduler.KindCron, Cron: "* * * * *", Timezone: "UTC"},
		Retention{KeepLast: keep(1)}); fieldOf(t, err) != "schedule" {
		t.Fatalf("dense cron: %v", err)
	}
}
