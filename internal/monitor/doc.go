// Package monitor provides missed-backup (dead-man) detection, security
// alerts and Prometheus metrics.
//
// Metrics (task 5.6, D-176): Metrics owns a dedicated registry (never the
// global default one) with the Go and process collectors, bekup_build_info
// and the event counters fed through small hook interfaces (runstate
// FinishObserver, restore.Metrics, retention.Metrics, scheduler.Metrics).
// Health builds cached snapshots (runner Health, certificate expiry, River
// queue depth, pin expiry, last successful runs) for its Prometheus
// collector and for Query.systemHealth. Server serves only GET /metrics
// (optionally bearer-authenticated) and GET /healthz on metrics.listen, an
// internal address (wildcards are refused, D-098).
//
// Every label has a fixed value set (values outside it become "other");
// the only open label is job (the job slug) and only with
// metrics.per_job_labels. Never put user, secret or connection data in a
// label.
//
// Security checks (task 5.5, D-190): SecurityChecker runs hourly (River
// kind security_check) and raises security.system alerts for internal and
// pinned target certificates nearing expiry (30/7 days, expired; once
// each), audit chain verification failures (incremental, full daily) and
// weakened posture (daily reminder). The dead-man detection (task 5.4)
// lives in internal/notify/missed.
package monitor
