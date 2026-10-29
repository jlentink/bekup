package monitor

import (
	"context"
	"runtime"
	"slices"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/jlentink/bekup/internal/configreload"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/restore"
	"github.com/jlentink/bekup/internal/retention"
	"github.com/jlentink/bekup/internal/scheduler"
)

// Namespace prefixes every bekup metric.
const Namespace = "bekup"

// OtherLabel replaces a label value outside its fixed set, UnknownLabel a
// value that could not be determined. Together with the fixed sets they
// bound the cardinality of every metric (D-176).
const (
	OtherLabel   = "other"
	UnknownLabel = "unknown"
)

// lookupTimeout bounds the label lookup of a finished run.
const lookupTimeout = 2 * time.Second

// Fixed label value sets.
var (
	runStatuses = []string{
		string(sqlc.RunStatusSuccess), string(sqlc.RunStatusPartial), string(sqlc.RunStatusFailed),
		string(sqlc.RunStatusCanceled), string(sqlc.RunStatusSkipped),
	}
	runTriggers = []string{
		string(sqlc.RunTriggerSchedule), string(sqlc.RunTriggerCatchUp), string(sqlc.RunTriggerManual),
		string(sqlc.RunTriggerEarly),
	}
	engines         = append(kindsOf(engine.Kinds()), UnknownLabel)
	restoreStatuses = []string{string(restore.StatusSucceeded), string(restore.StatusFailed), string(restore.StatusCanceled)}
	restoreKinds    = []string{string(sqlc.RestoreModeNewDb), string(sqlc.RestoreModeOverwrite), UnknownLabel}
	testStatuses    = []string{
		string(restore.TRStatusPassed), string(restore.TRStatusWarnings), string(restore.TRStatusFailed),
		string(restore.TRStatusError), string(restore.TRStatusCanceled),
	}
	skipReasons = []string{string(scheduler.SkipStillRunning), string(scheduler.SkipUnschedulable)}
	// enqueueTriggers are the triggers the scheduler loop enqueues.
	enqueueTriggers = []string{string(queue.TriggerSchedule), string(queue.TriggerCatchUp)}
	// reloadStatuses are the outcomes of a runtime config reload (D-200).
	reloadStatuses = statusesOf(configreload.Statuses())
)

func statusesOf(in []configreload.Status) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = string(s)
	}
	return out
}

func kindsOf(ks []engine.Kind) []string {
	out := make([]string, 0, len(ks)+1)
	for _, k := range ks {
		out = append(out, string(k))
	}
	return out
}

// bound maps v into its fixed set (OtherLabel otherwise).
func bound(v string, set []string) string {
	if slices.Contains(set, v) {
		return v
	}
	return OtherLabel
}

// withOther is set plus OtherLabel (the label values a bounded label can
// take).
func withOther(set []string) []string { return append(slices.Clone(set), OtherLabel) }

// Options configure New.
type Options struct {
	// Version is the bekup version (bekup_build_info).
	Version string
	// DB resolves the engine label of finished runs (nil: "unknown").
	DB sqlc.DBTX
	// Now is the clock (tests); nil = time.Now.
	Now func() time.Time
}

// Metrics owns bekup's dedicated Prometheus registry and the event
// counters. It implements the hook interfaces of runstate
// (FinishObserver), restore (Metrics), retention (Metrics) and scheduler
// (Metrics); every method is safe for concurrent use and never fails.
type Metrics struct {
	reg *prometheus.Registry
	db  sqlc.DBTX
	now func() time.Time

	runs          *prometheus.CounterVec
	runDuration   *prometheus.HistogramVec
	bytesStored   prometheus.Counter
	bytesRaw      prometheus.Counter
	restores      *prometheus.CounterVec
	testRestores  *prometheus.CounterVec
	retDeleted    prometheus.Counter
	retMissing    prometheus.Counter
	ticks         prometheus.Counter
	tickErrors    prometheus.Counter
	skips         *prometheus.CounterVec
	enqueued      *prometheus.CounterVec
	reloads       *prometheus.CounterVec
	lastTickNanos atomic.Int64
}

// DurationBuckets are the buckets of bekup_backup_run_duration_seconds:
// 10 s up to 12 h.
var DurationBuckets = []float64{10, 30, 60, 120, 300, 600, 1800, 3600, 7200, 14400, 28800, 43200}

// New builds the registry with the Go and process collectors, the build
// info and the event metrics (all label combinations pre-initialized, so
// increase() sees the first event).
func New(o Options) *Metrics {
	if o.Now == nil {
		o.Now = time.Now
	}
	m := &Metrics{reg: prometheus.NewRegistry(), db: o.DB, now: o.Now}
	m.reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{Namespace: Namespace}),
	)
	build := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: Namespace, Name: "build_info",
		Help: "Always 1; the labels carry the bekup version and the Go version.",
	}, []string{"version", "goversion"})
	version := o.Version
	if version == "" {
		version = UnknownLabel
	}
	build.WithLabelValues(version, runtime.Version()).Set(1)

	m.runs = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Name: "backup_runs_total",
		Help: "Backup runs that reached a terminal status in this process, by status, trigger and engine.",
	}, []string{"status", "trigger", "engine"})
	m.runDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: Namespace, Name: "backup_run_duration_seconds",
		Help:    "Duration of started backup runs from start to terminal status, by engine.",
		Buckets: DurationBuckets,
	}, []string{"engine"})
	m.bytesStored = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: Namespace, Name: "backup_bytes_stored_total",
		Help: "Bytes written to destinations by finished backup runs (compressed and encrypted, per copy).",
	})
	m.bytesRaw = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: Namespace, Name: "backup_bytes_raw_total",
		Help: "Uncompressed dump bytes of finished backup runs.",
	})
	m.restores = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Name: "restores_total",
		Help: "Restores that reached a terminal status, by status and kind (restore mode).",
	}, []string{"status", "kind"})
	m.testRestores = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Name: "test_restores_total",
		Help: "Test restores that reached a terminal status, by status.",
	}, []string{"status"})
	m.retDeleted = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: Namespace, Name: "retention_deleted_total",
		Help: "Objects deleted by retention sweeps.",
	})
	m.retMissing = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: Namespace, Name: "retention_missing_total",
		Help: "Objects retention found missing from their destination (deleted outside bekup).",
	})
	m.ticks = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: Namespace, Name: "scheduler_ticks_total",
		Help: "Scheduler ticks completed by this process.",
	})
	m.tickErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: Namespace, Name: "scheduler_tick_errors_total",
		Help: "Scheduler ticks that ended with an error.",
	})
	m.skips = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Name: "scheduler_skips_total",
		Help: "Scheduled occurrences that were not enqueued, by reason.",
	}, []string{"reason"})
	m.enqueued = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Name: "scheduler_enqueued_total",
		Help: "Backup runs enqueued by the scheduler, by trigger.",
	}, []string{"trigger"})
	m.reloads = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Name: "config_reloads_total",
		Help: "Runtime config reloads of this process, by outcome (D-200).",
	}, []string{"status"})
	lastTick := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: Namespace, Name: "scheduler_last_tick_timestamp_seconds",
		Help: "Unix time of the last completed scheduler tick in this process (0: none yet or scheduler off).",
	}, func() float64 {
		n := m.lastTickNanos.Load()
		if n == 0 {
			return 0
		}
		return float64(n) / 1e9
	})
	m.reg.MustRegister(build, m.runs, m.runDuration, m.bytesStored, m.bytesRaw, m.restores, m.testRestores,
		m.retDeleted, m.retMissing, m.ticks, m.tickErrors, m.skips, m.enqueued, m.reloads, lastTick)
	m.preinit()
	return m
}

func (m *Metrics) preinit() {
	for _, s := range withOther(runStatuses) {
		for _, t := range withOther(runTriggers) {
			for _, e := range engines {
				m.runs.WithLabelValues(s, t, e)
			}
		}
	}
	for _, e := range engines {
		m.runDuration.WithLabelValues(e)
	}
	for _, s := range withOther(restoreStatuses) {
		for _, k := range restoreKinds {
			m.restores.WithLabelValues(s, k)
		}
	}
	for _, s := range withOther(testStatuses) {
		m.testRestores.WithLabelValues(s)
	}
	for _, r := range withOther(skipReasons) {
		m.skips.WithLabelValues(r)
	}
	for _, t := range withOther(enqueueTriggers) {
		m.enqueued.WithLabelValues(t)
	}
	for _, s := range withOther(reloadStatuses) {
		m.reloads.WithLabelValues(s)
	}
}

// ConfigReloadFinished implements configreload.Metrics.
func (m *Metrics) ConfigReloadFinished(status string) {
	m.reloads.WithLabelValues(bound(status, reloadStatuses)).Inc()
}

// Registry is the dedicated registry (never prometheus.DefaultRegisterer).
func (m *Metrics) Registry() *prometheus.Registry { return m.reg }

// RunFinished implements runstate.FinishObserver.
func (m *Metrics) RunFinished(ctx context.Context, run sqlc.Run) {
	eng := m.engineOf(ctx, run.JobID)
	m.runs.WithLabelValues(bound(string(run.Status), runStatuses), bound(string(run.Trigger), runTriggers), eng).Inc()
	if run.StartedAt != nil && run.FinishedAt != nil {
		if d := run.FinishedAt.Sub(*run.StartedAt); d >= 0 {
			m.runDuration.WithLabelValues(eng).Observe(d.Seconds())
		}
	}
	if run.BytesStored > 0 {
		m.bytesStored.Add(float64(run.BytesStored))
	}
	if run.BytesRaw > 0 {
		m.bytesRaw.Add(float64(run.BytesRaw))
	}
}

// engineOf reads the engine of the job's connection (best effort).
func (m *Metrics) engineOf(ctx context.Context, jobID uuid.UUID) string {
	if m.db == nil {
		return UnknownLabel
	}
	lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lookupTimeout)
	defer cancel()
	row, err := sqlc.New(m.db).GetJobForRun(lctx, jobID)
	if err != nil {
		return UnknownLabel
	}
	e := bound(string(row.ConnectionEngine), engines)
	if e == OtherLabel {
		return UnknownLabel
	}
	return e
}

// RestoreFinished implements restore.Metrics.
func (m *Metrics) RestoreFinished(mode sqlc.RestoreMode, status restore.Status) {
	kind := bound(string(mode), restoreKinds)
	if kind == OtherLabel {
		kind = UnknownLabel
	}
	m.restores.WithLabelValues(bound(string(status), restoreStatuses), kind).Inc()
}

// TestRestoreFinished implements restore.Metrics.
func (m *Metrics) TestRestoreFinished(status sqlc.TestRestoreStatus) {
	m.testRestores.WithLabelValues(bound(string(status), testStatuses)).Inc()
}

// SweepFinished implements retention.Metrics (dry runs delete nothing and
// are not counted).
func (m *Metrics) SweepFinished(r retention.Result) {
	if r.DryRun {
		return
	}
	var deleted, missing int
	for i := range r.Destinations {
		deleted += max(r.Destinations[i].Deleted, 0)
		missing += max(r.Destinations[i].Missing, 0)
	}
	m.retDeleted.Add(float64(deleted))
	m.retMissing.Add(float64(missing))
}

// TickCompleted implements scheduler.Metrics.
func (m *Metrics) TickCompleted(_ time.Duration, err error) {
	m.ticks.Inc()
	if err != nil {
		m.tickErrors.Inc()
	}
	m.lastTickNanos.Store(m.now().UnixNano())
}

// Enqueued implements scheduler.Metrics.
func (m *Metrics) Enqueued(trigger queue.Trigger) {
	m.enqueued.WithLabelValues(bound(string(trigger), enqueueTriggers)).Inc()
}

// Skipped implements scheduler.Metrics.
func (m *Metrics) Skipped(reason scheduler.SkipReason) {
	m.skips.WithLabelValues(bound(string(reason), skipReasons)).Inc()
}

// LastSchedulerTick is the time of the last completed scheduler tick in
// this process (false: none yet).
func (m *Metrics) LastSchedulerTick() (time.Time, bool) {
	n := m.lastTickNanos.Load()
	if n == 0 {
		return time.Time{}, false
	}
	return time.Unix(0, n).UTC(), true
}
