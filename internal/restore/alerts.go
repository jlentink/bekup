package restore

import (
	"context"
	"log/slog"
	"sort"

	"github.com/google/uuid"
)

// AlertKind names a security event of restores.
type AlertKind string

// Security events. Each one is audited; the audit observer
// (notify.ObserveAudit, task 5.5) delivers it as a security.system alert to
// the admins. The default AlertFunc adds an ERROR log line.
const (
	// AlertArtifactTampered: a staged artifact failed the verification
	// (digest, signature, replayed or mismatching manifest).
	AlertArtifactTampered AlertKind = "restore_artifact_verification_failed"
	// AlertPrivilegedTarget: an admin queued a restore into a connection
	// whose role is privileged or whose privileges are unknown, with the
	// acknowledgement (D-068).
	AlertPrivilegedTarget AlertKind = "restore_privileged_target"
)

// Alert is one security event (public data only; never key material or
// database content).
type Alert struct {
	Kind       AlertKind
	RestoreID  uuid.UUID
	JobID      uuid.UUID
	ArtifactID uuid.UUID
	Details    map[string]any
}

// AlertFunc receives security events. It must not block for long and
// cannot fail the operation (the audit entry is the durable record).
type AlertFunc func(ctx context.Context, a Alert)

// logAlert is the default AlertFunc: an ERROR log line per event.
func logAlert(l *slog.Logger) AlertFunc {
	return func(ctx context.Context, a Alert) {
		keys := make([]string, 0, len(a.Details))
		for k := range a.Details {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		attrs := make([]slog.Attr, 0, 4+len(keys))
		attrs = append(attrs,
			slog.String("kind", string(a.Kind)),
			slog.String("restore_id", a.RestoreID.String()),
			slog.String("job_id", a.JobID.String()),
			slog.String("artifact_id", a.ArtifactID.String()))
		for _, k := range keys {
			attrs = append(attrs, slog.Any(k, a.Details[k]))
		}
		l.LogAttrs(ctx, slog.LevelError, "restore security alert", attrs...)
	}
}
