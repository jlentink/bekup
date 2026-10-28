package runstate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/pubsub"
)

// ErrArtifactNotPresent reports that the artifact does not exist or is no
// longer present (already deleted or missing).
var ErrArtifactNotPresent = errors.New("runstate: the artifact is not present")

// MarkArtifactDeletedTx records in the caller's transaction that retention
// deleted the object of a present artifact (state deleted, final; the row
// stays, D-147) and publishes the run event.
func MarkArtifactDeletedTx(ctx context.Context, q *sqlc.Queries, pub *pubsub.Publisher, id uuid.UUID, at time.Time) (Ref, error) {
	row, err := q.MarkArtifactDeleted(ctx, sqlc.MarkArtifactDeletedParams{ID: id, At: at.UTC()})
	if errors.Is(err, pgx.ErrNoRows) {
		return Ref{}, ErrArtifactNotPresent
	}
	if err != nil {
		return Ref{}, fmt.Errorf("runstate: mark artifact deleted: %w", err)
	}
	r := Ref{RunID: row.RunID, JobID: row.JobID}
	return r, publish(ctx, pub, q, EventRun, r)
}

// MarkArtifactMissingTx records in the caller's transaction that the object
// of a present artifact vanished without bekup deleting it, and publishes the
// run event. The caller audits and alerts (an unexpected deletion).
func MarkArtifactMissingTx(ctx context.Context, q *sqlc.Queries, pub *pubsub.Publisher, id uuid.UUID, at time.Time) (Ref, error) {
	row, err := q.MarkArtifactMissing(ctx, sqlc.MarkArtifactMissingParams{ID: id, At: at.UTC()})
	if errors.Is(err, pgx.ErrNoRows) {
		return Ref{}, ErrArtifactNotPresent
	}
	if err != nil {
		return Ref{}, fmt.Errorf("runstate: mark artifact missing: %w", err)
	}
	r := Ref{RunID: row.RunID, JobID: row.JobID}
	return r, publish(ctx, pub, q, EventRun, r)
}
