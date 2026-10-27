package pubsub

import (
	"context"
	"fmt"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Publisher sends notifications to every instance (pg_notify) and to the
// local Bus.
type Publisher struct {
	// Bus is the local bus (nil: NOTIFY only).
	Bus *Bus
}

// PublishTx validates the message, queues it with pg_notify in q's
// transaction (delivered to every listening instance on commit, dropped on
// rollback) and publishes it on the local Bus immediately. Local receivers
// may therefore see the message before the transaction commits (and again
// when the NOTIFY arrives): they must treat it as a hint to re-check, and
// re-check again shortly if nothing changed yet. A pg_notify failure fails
// the caller's transaction.
func (p *Publisher) PublishTx(ctx context.Context, q *sqlc.Queries, channel, payload string) error {
	if err := Validate(channel, payload); err != nil {
		return err
	}
	if err := q.Notify(ctx, sqlc.NotifyParams{Channel: channel, Payload: payload}); err != nil {
		return fmt.Errorf("pubsub: notify %s: %w", channel, err)
	}
	if p.Bus != nil {
		p.Bus.Publish(channel, payload)
	}
	return nil
}
