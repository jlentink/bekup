package connections

import (
	"context"
	"log/slog"
	"sort"

	"github.com/google/uuid"
)

// AlertKind names a security event of a connection.
type AlertKind string

// Security events. Task 5.5 delivers them as security alerts to admins;
// until then the default AlertFunc logs them.
const (
	// AlertTLSPinMismatch: the runner refused the target because its
	// certificate does not match the pin (possible impersonation).
	AlertTLSPinMismatch AlertKind = "tls_pin_mismatch"
	// AlertSSHHostKeyMismatch: the bastion presented another host key than
	// the pinned one (possible impersonation).
	AlertSSHHostKeyMismatch AlertKind = "ssh_host_key_mismatch"
	// AlertTLSDowngrade: an admin weakened the TLS mode (opt-out).
	AlertTLSDowngrade AlertKind = "tls_downgrade"
)

// Alert is one security event (public data only; never credentials).
type Alert struct {
	Kind           AlertKind
	ConnectionID   uuid.UUID
	ConnectionName string
	Details        map[string]any
}

// AlertFunc receives security events. It must not block for long and must
// not fail the operation (the audit entry is the durable record).
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
			slog.String("connection_id", a.ConnectionID.String()),
			slog.String("connection_name", a.ConnectionName))
		for _, k := range keys {
			attrs = append(attrs, slog.Any(k, a.Details[k]))
		}
		l.LogAttrs(ctx, slog.LevelWarn, "connection security alert", attrs...)
	}
}
