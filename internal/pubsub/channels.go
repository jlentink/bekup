package pubsub

import (
	"context"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Channel is a NOTIFY channel with its LISTEN query. LISTEN cannot take a
// parameter, so every channel has its own fixed query in
// internal/db/queries/pubsub.sql (no SQL is built from strings).
type Channel struct {
	Name   string
	Listen func(ctx context.Context, q *sqlc.Queries) error
}

// The channel catalog. Add a channel here together with its LISTEN query.
var (
	// SessionEvents carries session revocations and rotations
	// (internal/auth.SessionEvent: kind, session id, user id).
	SessionEvents = Channel{
		Name:   "bekup_session_events",
		Listen: func(ctx context.Context, q *sqlc.Queries) error { return q.ListenSessionEvents(ctx) },
	}

	// RunEvents carries backup run changes (internal/runstate: event
	// kind, run id, job id; task 3.6). The live run views of task 3.7
	// subscribe to it.
	RunEvents = Channel{
		Name:   "bekup_run_events",
		Listen: func(ctx context.Context, q *sqlc.Queries) error { return q.ListenRunEvents(ctx) },
	}

	// RestoreEvents carries restore changes (internal/restore: event kind
	// and restore id, "restore:<id>" or "log:<id>"; task 4.2). Live
	// restore views re-load the restore with the caller's rights.
	RestoreEvents = Channel{
		Name:   "bekup_restore_events",
		Listen: func(ctx context.Context, q *sqlc.Queries) error { return q.ListenRestoreEvents(ctx) },
	}

	// NotificationEvents carries new in-app inbox rows (internal/notify:
	// "inbox:<notification id>:<user id>"; task 5.3b). The
	// accountEvents stream re-loads the row scoped to the stream's
	// own user.
	NotificationEvents = Channel{
		Name:   "bekup_notification_events",
		Listen: func(ctx context.Context, q *sqlc.Queries) error { return q.ListenNotificationEvents(ctx) },
	}

	// SettingsEvents carries changes of runtime settings that every
	// process applies in memory (payload: the settings key, e.g.
	// "netguard.allowlist"; D-196). Receivers re-read the setting from the
	// database.
	SettingsEvents = Channel{
		Name:   "bekup_settings_events",
		Listen: func(ctx context.Context, q *sqlc.Queries) error { return q.ListenSettingsEvents(ctx) },
	}
)
