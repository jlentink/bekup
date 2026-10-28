package jobkeys

import (
	"context"
	"log/slog"
	"sort"

	"github.com/google/uuid"
)

// AlertKind names a security event of job encryption.
type AlertKind string

// Security events. Task 5.5 delivers them as security alerts to admins;
// until then the default AlertFunc logs them.
const (
	// AlertEncryptionOptOut: an admin turned a job's encryption off.
	AlertEncryptionOptOut AlertKind = "job_encryption_opt_out"
	// AlertPassphraseExport: an admin exported a job's passphrases.
	AlertPassphraseExport AlertKind = "job_passphrase_export"
)

// Alert is one security event (public data only; never key material).
type Alert struct {
	Kind    AlertKind
	JobID   uuid.UUID
	ActorID uuid.UUID
	Details map[string]any
}

// AlertFunc receives security events after the action committed. It must
// not block for long and cannot fail the operation (the audit entry is the
// durable record).
type AlertFunc func(ctx context.Context, a Alert)

// logAlert is the default AlertFunc: a WARN log line per event.
func logAlert(l *slog.Logger) AlertFunc {
	return func(ctx context.Context, a Alert) {
		keys := make([]string, 0, len(a.Details))
		for k := range a.Details {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		attrs := make([]slog.Attr, 0, 3+len(keys))
		attrs = append(attrs,
			slog.String("kind", string(a.Kind)),
			slog.String("job_id", a.JobID.String()),
			slog.String("actor_id", a.ActorID.String()))
		for _, k := range keys {
			attrs = append(attrs, slog.Any(k, a.Details[k]))
		}
		l.LogAttrs(ctx, slog.LevelWarn, "job encryption security alert", attrs...)
	}
}
