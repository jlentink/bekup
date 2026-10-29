package monitor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/restore"
	"github.com/jlentink/bekup/internal/retention"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/scheduler"
)

// The hook interfaces are implemented.
var (
	_ runstate.FinishObserver = (*Metrics)(nil)
	_ restore.Metrics         = (*Metrics)(nil)
	_ retention.Metrics       = (*Metrics)(nil)
	_ scheduler.Metrics       = (*Metrics)(nil)
)

func gather(t *testing.T, g prometheus.Gatherer) map[string]*dto.MetricFamily {
	t.Helper()
	mfs, err := g.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	out := map[string]*dto.MetricFamily{}
	for _, mf := range mfs {
		out[mf.GetName()] = mf
	}
	return out
}

func TestRegistration(t *testing.T) {
	m := New(Options{Version: "1.2.3"})
	mfs := gather(t, m.Registry())
	for _, name := range []string{
		"bekup_build_info", "bekup_backup_runs_total", "bekup_backup_run_duration_seconds",
		"bekup_backup_bytes_stored_total", "bekup_backup_bytes_raw_total", "bekup_restores_total",
		"bekup_test_restores_total", "bekup_retention_deleted_total", "bekup_retention_missing_total",
		"bekup_scheduler_ticks_total", "bekup_scheduler_skips_total", "bekup_scheduler_enqueued_total",
		"bekup_scheduler_last_tick_timestamp_seconds", "bekup_config_reloads_total", "go_goroutines",
	} {
		if mfs[name] == nil {
			t.Errorf("metric %s not registered", name)
		}
	}
	bi := mfs["bekup_build_info"].GetMetric()[0]
	if got := labelValue(bi, "version"); got != "1.2.3" {
		t.Errorf("build_info version = %q", got)
	}
	// Per-job series do not exist on the event registry.
	if mfs["bekup_backup_last_success_timestamp_seconds"] != nil {
		t.Error("per-job series registered without per_job_labels")
	}
	// The registry is dedicated: nothing is registered globally.
	for name := range gather(t, prometheus.DefaultGatherer) {
		if strings.HasPrefix(name, "bekup_") {
			t.Errorf("%s leaked into the default registry", name)
		}
	}
}

func labelValue(m *dto.Metric, name string) string {
	for _, l := range m.GetLabel() {
		if l.GetName() == name {
			return l.GetValue()
		}
	}
	return ""
}

func TestLabelCardinalityBounded(t *testing.T) {
	m := New(Options{})
	before := testutil.CollectAndCount(m.runs)
	// Hostile or unexpected values never create new series.
	for i := range 50 {
		v := "x" + strings.Repeat("y", i)
		m.RunFinished(context.Background(), sqlc.Run{JobID: uuid.New(), Status: sqlc.RunStatus(v), Trigger: sqlc.RunTrigger(v)})
		m.RestoreFinished(sqlc.RestoreMode(v), restore.Status(v))
		m.TestRestoreFinished(sqlc.TestRestoreStatus(v))
		m.Skipped(scheduler.SkipReason(v))
		m.Enqueued(queue.Trigger(v))
		m.ConfigReloadFinished(v)
	}
	if after := testutil.CollectAndCount(m.runs); after != before {
		t.Errorf("runs series grew from %d to %d", before, after)
	}
	if got := testutil.ToFloat64(m.runs.WithLabelValues(OtherLabel, OtherLabel, UnknownLabel)); got != 50 {
		t.Errorf("other/other/unknown = %v, want 50", got)
	}
	if got := testutil.ToFloat64(m.restores.WithLabelValues(OtherLabel, UnknownLabel)); got != 50 {
		t.Errorf("restores other/unknown = %v, want 50", got)
	}
	for name, c := range map[string]prometheus.Collector{
		"restores": m.restores, "test_restores": m.testRestores, "skips": m.skips, "enqueued": m.enqueued,
		"config_reloads": m.reloads,
	} {
		n := testutil.CollectAndCount(c)
		if n > 32 {
			t.Errorf("%s has %d series", name, n)
		}
	}
	// 6 statuses x 5 triggers x 4 engines.
	if before != 6*5*4 {
		t.Errorf("runs pre-initialized series = %d, want %d", before, 6*5*4)
	}
}

func TestEventCounters(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	m := New(Options{Now: func() time.Time { return now }})
	start, end := now.Add(-90*time.Second), now
	m.RunFinished(context.Background(), sqlc.Run{
		JobID: uuid.New(), Status: sqlc.RunStatusSuccess, Trigger: sqlc.RunTriggerSchedule,
		StartedAt: &start, FinishedAt: &end, BytesRaw: 1000, BytesStored: 400,
	})
	m.RunFinished(context.Background(), sqlc.Run{JobID: uuid.New(), Status: sqlc.RunStatusSkipped, Trigger: sqlc.RunTriggerManual})
	if got := testutil.ToFloat64(m.runs.WithLabelValues("success", "schedule", UnknownLabel)); got != 1 {
		t.Errorf("success runs = %v", got)
	}
	if got := testutil.ToFloat64(m.bytesStored); got != 400 {
		t.Errorf("bytes stored = %v", got)
	}
	if got := testutil.ToFloat64(m.bytesRaw); got != 1000 {
		t.Errorf("bytes raw = %v", got)
	}
	if n := testutil.CollectAndCount(m.runDuration); n != len(engines) {
		t.Errorf("duration series = %d", n)
	}
	m.RestoreFinished(sqlc.RestoreModeNewDb, restore.StatusSucceeded)
	if got := testutil.ToFloat64(m.restores.WithLabelValues("succeeded", "new_db")); got != 1 {
		t.Errorf("restores = %v", got)
	}
	m.TestRestoreFinished(restore.TRStatusPassed)
	if got := testutil.ToFloat64(m.testRestores.WithLabelValues("passed")); got != 1 {
		t.Errorf("test restores = %v", got)
	}
	m.SweepFinished(retention.Result{Destinations: []retention.DestinationResult{{Deleted: 3, Missing: 1}, {Deleted: 2}}})
	m.SweepFinished(retention.Result{DryRun: true, Destinations: []retention.DestinationResult{{Deleted: 7}}})
	if got := testutil.ToFloat64(m.retDeleted); got != 5 {
		t.Errorf("retention deleted = %v", got)
	}
	if got := testutil.ToFloat64(m.retMissing); got != 1 {
		t.Errorf("retention missing = %v", got)
	}
	if _, ok := m.LastSchedulerTick(); ok {
		t.Error("tick before any tick")
	}
	m.TickCompleted(time.Second, nil)
	m.TickCompleted(time.Second, errors.New("boom"))
	m.Skipped(scheduler.SkipStillRunning)
	m.Enqueued(queue.TriggerCatchUp)
	if got := testutil.ToFloat64(m.ticks); got != 2 {
		t.Errorf("ticks = %v", got)
	}
	if got := testutil.ToFloat64(m.tickErrors); got != 1 {
		t.Errorf("tick errors = %v", got)
	}
	if got := testutil.ToFloat64(m.skips.WithLabelValues("still_running")); got != 1 {
		t.Errorf("skips = %v", got)
	}
	if got := testutil.ToFloat64(m.enqueued.WithLabelValues("catch_up")); got != 1 {
		t.Errorf("enqueued = %v", got)
	}
	m.ConfigReloadFinished("applied")
	m.ConfigReloadFinished("rejected")
	m.ConfigReloadFinished("rejected")
	if got := testutil.ToFloat64(m.reloads.WithLabelValues("rejected")); got != 2 {
		t.Errorf("config reloads rejected = %v", got)
	}
	if n := testutil.CollectAndCount(m.reloads); n != 6 {
		t.Errorf("config reload series = %d, want 6 (5 statuses + other)", n)
	}
	if tick, ok := m.LastSchedulerTick(); !ok || !tick.Equal(now) {
		t.Errorf("last tick = %v %v", tick, ok)
	}
}

func TestBound(t *testing.T) {
	set := []string{"a", "b"}
	for in, want := range map[string]string{"a": "a", "b": "b", "": OtherLabel, "A": OtherLabel, "a\n": OtherLabel} {
		if got := bound(in, set); got != want {
			t.Errorf("bound(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBoundQueue(t *testing.T) {
	got := boundQueue([]sqlc.MonitorQueueJobsRow{
		{Queue: "backup", State: "available", Jobs: 2},
		{Queue: "evil-queue", State: "available", Jobs: 1},
		{Queue: "other-evil", State: "available", Jobs: 4},
		{Queue: "backup", State: "weird", Jobs: 3},
	})
	want := []QueueDepth{
		{Queue: "backup", State: "available", Jobs: 2},
		{Queue: "backup", State: OtherLabel, Jobs: 3},
		{Queue: OtherLabel, State: "available", Jobs: 5},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
