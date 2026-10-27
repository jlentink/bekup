package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/pubsub"
)

// SessionEventKind says what happened to a session (task 1.10).
type SessionEventKind string

// Session event kinds.
const (
	// SessionRevoked: one session ended (logout, revoked by its user or an
	// admin, replaced by a login in the same browser).
	SessionRevoked SessionEventKind = "revoked"
	// SessionRotated: one session was replaced by a new one of the same
	// user (privilege change, AAL elevation); the browser that triggered
	// it has the new cookie.
	SessionRotated SessionEventKind = "rotated"
	// SessionsRevokedAll: every session of a user ended (password, role or
	// status change).
	SessionsRevokedAll SessionEventKind = "user_revoked"
)

// SessionEvent is a session change. It carries identifiers only (it
// travels through Postgres NOTIFY): receivers re-check the session in the
// database before acting on it. UserID may be uuid.Nil when the caller
// does not know the owner of SessionID; SessionID is uuid.Nil for
// SessionsRevokedAll.
type SessionEvent struct {
	Kind      SessionEventKind
	UserID    uuid.UUID
	SessionID uuid.UUID
}

// errBadSessionEvent rejects a malformed payload.
var errBadSessionEvent = errors.New("auth: malformed session event")

// Encode returns the NOTIFY payload "kind:user_id:session_id".
func (e SessionEvent) Encode() string {
	return string(e.Kind) + ":" + e.UserID.String() + ":" + e.SessionID.String()
}

// DecodeSessionEvent parses an Encode payload.
func DecodeSessionEvent(s string) (SessionEvent, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return SessionEvent{}, errBadSessionEvent
	}
	kind := SessionEventKind(parts[0])
	switch kind {
	case SessionRevoked, SessionRotated, SessionsRevokedAll:
	default:
		return SessionEvent{}, errBadSessionEvent
	}
	user, err := uuid.Parse(parts[1])
	if err != nil || len(parts[1]) != 36 {
		return SessionEvent{}, errBadSessionEvent
	}
	sess, err := uuid.Parse(parts[2])
	if err != nil || len(parts[2]) != 36 {
		return SessionEvent{}, errBadSessionEvent
	}
	ev := SessionEvent{Kind: kind, UserID: user, SessionID: sess}
	if (kind == SessionsRevokedAll) != (sess == uuid.Nil) || (kind == SessionsRevokedAll && user == uuid.Nil) {
		return SessionEvent{}, errBadSessionEvent
	}
	return ev, nil
}

// SessionEventPublisher receives session changes from the session store,
// inside the transaction that makes them (SessionOptions.Events).
type SessionEventPublisher interface {
	PublishSessionEventTx(ctx context.Context, q *sqlc.Queries, ev SessionEvent) error
}

// PubSubSessionEvents publishes session events on
// pubsub.SessionEvents: on the local bus at once and to every instance via
// NOTIFY on commit.
type PubSubSessionEvents struct {
	Publisher *pubsub.Publisher
}

// PublishSessionEventTx implements SessionEventPublisher.
func (p PubSubSessionEvents) PublishSessionEventTx(ctx context.Context, q *sqlc.Queries, ev SessionEvent) error {
	return p.Publisher.PublishTx(ctx, q, pubsub.SessionEvents.Name, ev.Encode())
}

// emit hands ev to the configured publisher (none: no-op). An error fails
// the caller's transaction, so a revocation never commits silently
// without its event.
func (s *Sessions) emit(ctx context.Context, q *sqlc.Queries, ev SessionEvent) error {
	if s.opts.Events == nil {
		return nil
	}
	return s.opts.Events.PublishSessionEventTx(ctx, q, ev)
}

// Check re-validates the session id without touching it (long-lived
// connections: an open event stream is not user activity, so the idle expiry
// keeps running) and rebuilds its principal from the current session and
// user rows (role, AAL, step-up and second-factor times, pending second
// factor, temporary password; the caller applies the 2FA enrollment
// policy). It returns ErrNoSession when the session is revoked or expired
// or its user is disabled, deleted or has an unknown role.
func (s *Sessions) Check(ctx context.Context, id uuid.UUID) (Principal, sqlc.User, error) {
	row, err := sqlc.New(s.db).CheckActiveSession(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Principal{}, sqlc.User{}, ErrNoSession
		}
		return Principal{}, sqlc.User{}, fmt.Errorf("auth: check session: %w", err)
	}
	user := row.User
	role := Role(user.Role)
	if user.Disabled || user.DeletedAt != nil || !role.Valid() {
		return Principal{}, sqlc.User{}, ErrNoSession
	}
	return principalOf(row.Session, user, role), user, nil
}
