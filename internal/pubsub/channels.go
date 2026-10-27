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
)
