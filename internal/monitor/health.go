package monitor

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// Snapshot defaults.
const (
	// DefaultSnapshotTTL is how long a health snapshot is reused: scrapes
	// and systemHealth calls within it cost no database query and no
	// runner call.
	DefaultSnapshotTTL = 15 * time.Second
	// snapshotTimeout bounds one refresh; runnerTimeout the runner's
	// Health call inside it.
	snapshotTimeout = 10 * time.Second
	runnerTimeout   = 5 * time.Second
	// MaxJobSeries caps the per-job series (the query's LIMIT).
	MaxJobSeries = 1000
)

// Certificate components (label values of bekup_cert_expiry_seconds).
const (
	CertRunnerServer   = "runner_server"
	CertRunnerClient   = "runner_client"
	CertRunnerCA       = "runner_ca"
	CertPostgresCA     = "postgres_ca"
	CertPostgresClient = "postgres_client"
)

// riverStates are River's job states (label values of bekup_queue_jobs).
var riverStates = []string{
	"available",
	"cancelled", //nolint:misspell // River's enum value is spelled this way
	"completed", "discarded", "pending", "retryable", "running", "scheduled",
}

// RunnerHealth is the runner's Health RPC (*runnerclient.Client).
type RunnerHealth interface {
	Health(ctx context.Context) (*runnerpb.HealthResponse, error)
}

// CertFile is a PEM file whose earliest NotAfter is reported under
// Component.
type CertFile struct {
	Component string
	Path      string
}

// HealthOptions configure NewHealth.
type HealthOptions struct {
	// DB is the app pool (bekup_app); required.
	DB sqlc.DBTX
	// Runner answers the runner health (nil: the runner is not checked
	// and reported down).
	Runner RunnerHealth
	// Certs are the certificate files to watch.
	Certs []CertFile
	// Metrics provides the last scheduler tick (nil: none).
	Metrics *Metrics
	// SchedulerEnabled is scheduler.enabled of this process.
	SchedulerEnabled bool
	// PerJobLabels exposes bekup_backup_last_success_timestamp_seconds{job}
	// (metrics.per_job_labels).
	PerJobLabels bool
	// TTL of a snapshot (0: DefaultSnapshotTTL).
	TTL    time.Duration
	Now    func() time.Time
	Logger *slog.Logger
}

// Snapshot is the system health at CheckedAt. It holds no secrets and no
// per-user data; job slugs appear only in LastSuccess.
type Snapshot struct {
	CheckedAt time.Time
	Runner    RunnerStatus
	// Certificates are sorted by component; unreadable files are left out
	// (and logged).
	Certificates []CertExpiry
	// Queue is nil when the database could not be read (QueueOK false).
	Queue   []QueueDepth
	QueueOK bool
	// EarliestPinNotAfter is the earliest expiry of a pinned target
	// certificate (nil: no pins or not readable).
	EarliestPinNotAfter *time.Time
	// LastSuccess (stalest first, at most MaxJobSeries) and
	// OldestLastSuccess cover enabled jobs with a successful run.
	LastSuccess       []JobLastSuccess
	OldestLastSuccess *time.Time
	SchedulerEnabled  bool
	// LastSchedulerTick is this process's last completed tick (nil: none
	// yet, or the scheduler is off here).
	LastSchedulerTick *time.Time
}

// RunnerStatus is the result of the runner's Health RPC.
type RunnerStatus struct {
	Up                bool
	DockerOK          bool
	Version           string
	ActiveRuns        int
	MaxConcurrentRuns int
	// CertNotAfter is the runner's server certificate expiry.
	CertNotAfter *time.Time
}

// CertExpiry is the earliest NotAfter of one component's certificate file.
type CertExpiry struct {
	Component string
	NotAfter  time.Time
}

// QueueDepth is the number of River jobs of one queue in one state.
type QueueDepth struct {
	Queue string
	State string
	Jobs  int64
}

// JobLastSuccess is the finish time of a job's latest successful run.
type JobLastSuccess struct {
	Slug string
	At   time.Time
}

// Health builds cached health snapshots for /metrics and systemHealth.
type Health struct {
	o HealthOptions

	mu   sync.Mutex
	last Snapshot
	have bool
}

// NewHealth validates o.
func NewHealth(o HealthOptions) (*Health, error) {
	if o.DB == nil || o.Logger == nil {
		return nil, errors.New("monitor: DB and Logger are required")
	}
	if o.TTL <= 0 {
		o.TTL = DefaultSnapshotTTL
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Health{o: o}, nil
}

// Snapshot returns the cached snapshot or refreshes it. Concurrent callers
// wait for one refresh; the refresh is bounded (snapshotTimeout) and
// detached from the caller's cancellation so an impatient scraper cannot
// starve the cache.
func (h *Health) Snapshot(ctx context.Context) Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.o.Now()
	if !h.have || now.Sub(h.last.CheckedAt) >= h.o.TTL || now.Before(h.last.CheckedAt) {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), snapshotTimeout)
		h.last = h.refresh(rctx, now)
		cancel()
		h.have = true
	}
	out := h.last
	out.Certificates = slices.Clone(out.Certificates)
	out.Queue = slices.Clone(out.Queue)
	out.LastSuccess = slices.Clone(out.LastSuccess)
	out.SchedulerEnabled = h.o.SchedulerEnabled
	if h.o.SchedulerEnabled && h.o.Metrics != nil {
		if t, ok := h.o.Metrics.LastSchedulerTick(); ok {
			out.LastSchedulerTick = &t
		}
	}
	return out
}

func (h *Health) refresh(ctx context.Context, now time.Time) Snapshot {
	s := Snapshot{CheckedAt: now.UTC()}
	s.Runner = h.runner(ctx)
	s.Certificates = h.certs()
	if s.Runner.CertNotAfter != nil {
		s.Certificates = append(s.Certificates, CertExpiry{Component: CertRunnerServer, NotAfter: *s.Runner.CertNotAfter})
	}
	slices.SortFunc(s.Certificates, func(a, b CertExpiry) int {
		if a.Component < b.Component {
			return -1
		}
		if a.Component > b.Component {
			return 1
		}
		return 0
	})
	q := sqlc.New(h.o.DB)
	if rows, err := q.MonitorQueueJobs(ctx); err != nil {
		h.o.Logger.WarnContext(ctx, "monitor: reading the queue depth failed", slog.Any("error", err))
	} else {
		s.Queue, s.QueueOK = boundQueue(rows), true
	}
	if pins, err := q.MonitorEarliestPinNotAfter(ctx); err != nil {
		h.o.Logger.WarnContext(ctx, "monitor: reading the certificate pins failed", slog.Any("error", err))
	} else if len(pins) == 1 && pins[0] != nil {
		t := pins[0].UTC()
		s.EarliestPinNotAfter = &t
	}
	if rows, err := q.MonitorLastSuccessPerJob(ctx); err != nil {
		h.o.Logger.WarnContext(ctx, "monitor: reading the last successful runs failed", slog.Any("error", err))
	} else {
		for _, r := range rows {
			if r.FinishedAt == nil {
				continue
			}
			s.LastSuccess = append(s.LastSuccess, JobLastSuccess{Slug: r.Slug, At: r.FinishedAt.UTC()})
		}
		if len(s.LastSuccess) > 0 {
			t := s.LastSuccess[0].At
			s.OldestLastSuccess = &t
		}
		if len(s.LastSuccess) > MaxJobSeries {
			s.LastSuccess = s.LastSuccess[:MaxJobSeries]
		}
	}
	return s
}

// boundQueue maps queue and state names into their fixed sets and sums
// what falls outside into "other".
func boundQueue(rows []sqlc.MonitorQueueJobsRow) []QueueDepth {
	queues := make([]string, 0, len(queue.Queues()))
	for _, q := range queue.Queues() {
		queues = append(queues, string(q))
	}
	sums := map[[2]string]int64{}
	for _, r := range rows {
		k := [2]string{bound(r.Queue, queues), bound(r.State, riverStates)}
		sums[k] += r.Jobs
	}
	out := make([]QueueDepth, 0, len(sums))
	for k, n := range sums {
		out = append(out, QueueDepth{Queue: k[0], State: k[1], Jobs: n})
	}
	slices.SortFunc(out, func(a, b QueueDepth) int {
		switch {
		case a.Queue != b.Queue:
			if a.Queue < b.Queue {
				return -1
			}
			return 1
		case a.State < b.State:
			return -1
		case a.State > b.State:
			return 1
		}
		return 0
	})
	return out
}

func (h *Health) runner(ctx context.Context) RunnerStatus {
	if h.o.Runner == nil {
		return RunnerStatus{}
	}
	rctx, cancel := context.WithTimeout(ctx, runnerTimeout)
	defer cancel()
	resp, err := h.o.Runner.Health(rctx)
	if err != nil {
		// The error text may carry addresses; the code is enough here.
		h.o.Logger.WarnContext(ctx, "monitor: runner health check failed")
		return RunnerStatus{}
	}
	st := RunnerStatus{
		Up:                true,
		DockerOK:          resp.GetDockerStatus() == runnerpb.DockerStatus_DOCKER_STATUS_OK,
		Version:           clampLabel(resp.GetVersion()),
		ActiveRuns:        int(min(resp.GetActiveRuns(), 1<<20)),
		MaxConcurrentRuns: int(min(resp.GetMaxConcurrentRuns(), 1<<20)),
	}
	if ts := resp.GetCertificateNotAfter(); ts != nil && ts.IsValid() {
		t := ts.AsTime().UTC()
		st.CertNotAfter = &t
	}
	return st
}

// clampLabel bounds a remote-provided string (the runner version).
func clampLabel(s string) string {
	const maxLen = 64
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	return s
}

func (h *Health) certs() []CertExpiry {
	out := make([]CertExpiry, 0, len(h.o.Certs))
	for _, cf := range h.o.Certs {
		if cf.Path == "" {
			continue
		}
		infos, err := certs.InspectFile(cf.Path)
		if err != nil || len(infos) == 0 {
			h.o.Logger.Warn("monitor: reading a certificate file failed", slog.String("component", cf.Component))
			continue
		}
		earliest := infos[0].NotAfter
		for i := range infos {
			if infos[i].NotAfter.Before(earliest) {
				earliest = infos[i].NotAfter
			}
		}
		out = append(out, CertExpiry{Component: cf.Component, NotAfter: earliest.UTC()})
	}
	return out
}
