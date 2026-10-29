package monitor

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	descQueueJobs = prometheus.NewDesc(Namespace+"_queue_jobs",
		"River jobs per queue and state (finished rows are pruned by River).", []string{"queue", "state"}, nil)
	descRunnerUp = prometheus.NewDesc(Namespace+"_runner_up",
		"1 when the runner answered its Health RPC over mTLS, 0 otherwise.", nil, nil)
	descRunnerDocker = prometheus.NewDesc(Namespace+"_runner_docker_ok",
		"1 when the runner reports the Docker daemon reachable, 0 otherwise.", nil, nil)
	descRunnerActive = prometheus.NewDesc(Namespace+"_runner_active_runs",
		"Dump/restore containers the runner is running.", nil, nil)
	descCertExpiry = prometheus.NewDesc(Namespace+"_cert_expiry_seconds",
		"Seconds until the earliest certificate in the component's file expires (negative once expired).", []string{"component"}, nil)
	descPinExpiry = prometheus.NewDesc(Namespace+"_db_pin_expiry_seconds_min",
		"Seconds until the earliest pinned target-database certificate expires (absent without pins).", nil, nil)
	descOldestSuccess = prometheus.NewDesc(Namespace+"_backup_oldest_last_success_timestamp_seconds",
		"Unix time of the stalest latest successful run across enabled jobs that have one.", nil, nil)
	descLastSuccess = prometheus.NewDesc(Namespace+"_backup_last_success_timestamp_seconds",
		"Unix time of the job's latest successful run (metrics.per_job_labels; job = slug).", []string{"job"}, nil)
	descHealthDB = prometheus.NewDesc(Namespace+"_monitor_db_ok",
		"1 when the last health snapshot could read the app database, 0 otherwise.", nil, nil)
)

// collector exposes a Health snapshot as gauges.
type collector struct{ h *Health }

// Collector returns the Prometheus collector of the health snapshot
// (register it on Metrics.Registry in serve only: in a worker process the
// same gauges would be duplicated).
func (h *Health) Collector() prometheus.Collector { return collector{h} }

// Describe implements prometheus.Collector.
func (c collector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{descQueueJobs, descRunnerUp, descRunnerDocker, descRunnerActive,
		descCertExpiry, descPinExpiry, descOldestSuccess, descHealthDB} {
		ch <- d
	}
	if c.h.o.PerJobLabels {
		ch <- descLastSuccess
	}
}

// Collect implements prometheus.Collector.
func (c collector) Collect(ch chan<- prometheus.Metric) {
	s := c.h.Snapshot(context.Background())
	now := c.h.o.Now()
	for _, q := range s.Queue {
		ch <- prometheus.MustNewConstMetric(descQueueJobs, prometheus.GaugeValue, float64(q.Jobs), q.Queue, q.State)
	}
	ch <- prometheus.MustNewConstMetric(descRunnerUp, prometheus.GaugeValue, b2f(s.Runner.Up))
	ch <- prometheus.MustNewConstMetric(descRunnerDocker, prometheus.GaugeValue, b2f(s.Runner.Up && s.Runner.DockerOK))
	if s.Runner.Up {
		ch <- prometheus.MustNewConstMetric(descRunnerActive, prometheus.GaugeValue, float64(s.Runner.ActiveRuns))
	}
	for _, ce := range s.Certificates {
		ch <- prometheus.MustNewConstMetric(descCertExpiry, prometheus.GaugeValue, ce.NotAfter.Sub(now).Seconds(), ce.Component)
	}
	if s.EarliestPinNotAfter != nil {
		ch <- prometheus.MustNewConstMetric(descPinExpiry, prometheus.GaugeValue, s.EarliestPinNotAfter.Sub(now).Seconds())
	}
	if s.OldestLastSuccess != nil {
		ch <- prometheus.MustNewConstMetric(descOldestSuccess, prometheus.GaugeValue, unixSeconds(*s.OldestLastSuccess))
	}
	if c.h.o.PerJobLabels {
		for _, j := range s.LastSuccess {
			ch <- prometheus.MustNewConstMetric(descLastSuccess, prometheus.GaugeValue, unixSeconds(j.At), j.Slug)
		}
	}
	ch <- prometheus.MustNewConstMetric(descHealthDB, prometheus.GaugeValue, b2f(s.QueueOK))
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func unixSeconds(t interface{ UnixNano() int64 }) float64 { return float64(t.UnixNano()) / 1e9 }
