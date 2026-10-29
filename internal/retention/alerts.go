package retention

import (
	"context"
	"log/slog"
	"sort"

	"github.com/google/uuid"
)

// AlertKind names a retention anomaly.
type AlertKind string

// Retention anomalies. Each one is audited; the audit observer
// (notify.ObserveAudit, task 5.5) delivers it as a security.system alert
// (and serve adds artifact.missing for the job's audience). The default
// AlertFunc logs them at WARN.
const (
	// AlertArtifactMissing: an object vanished from its destination
	// without bekup deleting it (immutable backups: unexpected deletion).
	AlertArtifactMissing AlertKind = "retention_artifact_missing"
	// AlertSweepAborted: a safety check stopped retention on a destination
	// (the newest complete backup is missing or changed, or the plan
	// violated its invariants).
	AlertSweepAborted AlertKind = "retention_sweep_aborted"
)

// Alert is one retention anomaly (identifiers only).
type Alert struct {
	Kind          AlertKind
	JobID         uuid.UUID
	DestinationID uuid.UUID
	RunID         uuid.UUID
	ArtifactID    uuid.UUID
	Details       map[string]any
}

// AlertFunc receives anomalies after they were recorded. It must not block
// for long and cannot fail the sweep (the audit entry is the durable
// record).
type AlertFunc func(ctx context.Context, a Alert)

// LogAlert is the default AlertFunc (a WARN log line per anomaly), for
// callers that add to it.
func LogAlert(l *slog.Logger) AlertFunc { return logAlert(l) }

func logAlert(l *slog.Logger) AlertFunc {
	return func(ctx context.Context, a Alert) {
		keys := make([]string, 0, len(a.Details))
		for k := range a.Details {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		attrs := []slog.Attr{
			slog.String("kind", string(a.Kind)),
			slog.String("job_id", a.JobID.String()),
			slog.String("destination_id", a.DestinationID.String()),
		}
		if a.RunID != uuid.Nil {
			attrs = append(attrs, slog.String("run_id", a.RunID.String()))
		}
		if a.ArtifactID != uuid.Nil {
			attrs = append(attrs, slog.String("artifact_id", a.ArtifactID.String()))
		}
		for _, k := range keys {
			attrs = append(attrs, slog.Any(k, a.Details[k]))
		}
		l.LogAttrs(ctx, slog.LevelWarn, "retention alert", attrs...)
	}
}
