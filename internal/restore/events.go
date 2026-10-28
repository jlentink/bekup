package restore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/runstate"
)

// Restore events (task 4.2): every status change and log batch of a
// restore publishes the restore id on bekup_restore_events
// (pubsub.RestoreEvents) in the same transaction, identifiers only. Live
// views subscribe and re-load the restore with the caller's rights.

// Event kinds of the bekup_restore_events payload.
const (
	// EventRestore: the restore row changed (status, outcome).
	EventRestore = "restore"
	// EventLog: log lines were appended to the restore.
	EventLog = "log"
)

// Payload is the restore event payload: "<kind>:<restore id>".
func Payload(kind string, id uuid.UUID) string { return kind + ":" + id.String() }

// ErrBadPayload reports an undecodable restore event payload.
var ErrBadPayload = errors.New("restore: invalid restore event payload")

// ParsePayload decodes a restore event payload.
func ParsePayload(p string) (kind string, id uuid.UUID, err error) {
	kind, rest, ok := strings.Cut(p, ":")
	if !ok || (kind != EventRestore && kind != EventLog) || len(rest) != 36 {
		return "", uuid.Nil, ErrBadPayload
	}
	if id, err = uuid.Parse(rest); err != nil {
		return "", uuid.Nil, ErrBadPayload
	}
	return kind, id, nil
}

// publish sends a restore event in q's transaction.
func (s *Service) publish(ctx context.Context, q *sqlc.Queries, kind string, id uuid.UUID) error {
	pub := s.o.Publisher
	if pub == nil {
		pub = &pubsub.Publisher{}
	}
	return pub.PublishTx(ctx, q, pubsub.RestoreEvents.Name, Payload(kind, id))
}

// AppendLogs stores a batch of (already sanitized) log lines of a restore
// and publishes a log event.
func (s *Service) AppendLogs(ctx context.Context, id uuid.UUID, lines []runstate.LogLine) error {
	if len(lines) == 0 {
		return nil
	}
	p := sqlc.InsertRestoreLogsParams{RestoreID: id}
	for _, l := range lines {
		p.Ts = append(p.Ts, l.Time)
		p.Levels = append(p.Levels, string(l.Level))
		p.Streams = append(p.Streams, string(l.Stream))
		p.Lines = append(p.Lines, l.Text)
	}
	return db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		if err := q.InsertRestoreLogs(ctx, p); err != nil {
			return fmt.Errorf("restore: insert restore logs: %w", err)
		}
		return s.publish(ctx, q, EventLog, id)
	})
}
