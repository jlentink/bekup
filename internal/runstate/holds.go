package runstate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Artifact holds (task 3.11): a lease that keeps retention from deleting an
// artifact while a download (or a restore, 4.x) reads it. A hold is taken
// under a share lock of the artifact row; retention locks the row FOR NO KEY
// UPDATE while it deletes the object (LockArtifactForDeletion), so a hold
// and a deletion never interleave. Holds expire on their own as a safety net
// when the holder dies; a holder renews its hold while it works.

// HoldKind is what holds an artifact.
type HoldKind string

// Hold kinds (artifact_holds.kind).
const (
	HoldDownload HoldKind = "download"
	HoldRestore  HoldKind = "restore"
)

// Hold lifetimes.
const (
	// DefaultHoldTTL is the lifetime of a hold between renewals.
	DefaultHoldTTL = 5 * time.Minute
	// MaxHoldTTL is the longest lifetime the table accepts.
	MaxHoldTTL = time.Hour
)

// ErrHoldLost reports that a hold expired (or was removed) before it was
// renewed.
var ErrHoldLost = errors.New("runstate: the artifact hold expired")

// Hold is an acquired hold.
type Hold struct {
	ID        uuid.UUID
	ExpiresAt time.Time
}

func holdSeconds(ttl time.Duration) float64 {
	if ttl <= 0 {
		ttl = DefaultHoldTTL
	}
	return min(ttl, MaxHoldTTL).Seconds()
}

// AcquireHoldTx takes a hold on a present artifact in the caller's
// transaction (a second call by the same holder renews it). ok is false
// when the artifact is no longer present.
func AcquireHoldTx(ctx context.Context, q *sqlc.Queries, artifactID uuid.UUID, kind HoldKind, holder uuid.UUID, ttl time.Duration) (Hold, bool, error) {
	row, err := q.AcquireArtifactHold(ctx, sqlc.AcquireArtifactHoldParams{
		ArtifactID: artifactID, Kind: string(kind), HolderID: holder, TtlSeconds: holdSeconds(ttl),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Hold{}, false, nil
	}
	if err != nil {
		return Hold{}, false, fmt.Errorf("runstate: acquire artifact hold: %w", err)
	}
	return Hold{ID: row.ID, ExpiresAt: row.ExpiresAt}, true, nil
}

// RenewHold moves an unexpired hold's expiry ttl ahead (ErrHoldLost when it
// already expired).
func RenewHold(ctx context.Context, d sqlc.DBTX, id uuid.UUID, ttl time.Duration) error {
	n, err := sqlc.New(d).RenewArtifactHold(ctx, sqlc.RenewArtifactHoldParams{ID: id, TtlSeconds: holdSeconds(ttl)})
	if err != nil {
		return fmt.Errorf("runstate: renew artifact hold: %w", err)
	}
	if n == 0 {
		return ErrHoldLost
	}
	return nil
}

// ReleaseHold removes a hold (idempotent).
func ReleaseHold(ctx context.Context, d sqlc.DBTX, id uuid.UUID) error {
	if _, err := sqlc.New(d).ReleaseArtifactHold(ctx, id); err != nil {
		return fmt.Errorf("runstate: release artifact hold: %w", err)
	}
	return nil
}

// DeleteExpiredHolds removes holds that expired before cutoff
// (housekeeping; expired holds never protect anything).
func DeleteExpiredHolds(ctx context.Context, d sqlc.DBTX, cutoff time.Time) (int64, error) {
	n, err := sqlc.New(d).DeleteExpiredArtifactHolds(ctx, cutoff)
	if err != nil {
		return 0, fmt.Errorf("runstate: delete expired artifact holds: %w", err)
	}
	return n, nil
}

// Deletion lock results.
var (
	// ErrArtifactHeld: an unexpired hold protects the artifact.
	ErrArtifactHeld = errors.New("runstate: the artifact is held")
)

// LockForDeletionTx locks a present artifact in the caller's transaction for
// a retention delete: until the transaction ends no hold can be taken on it.
// ErrArtifactNotPresent when it is no longer present, ErrArtifactHeld when an
// unexpired hold protects it.
func LockForDeletionTx(ctx context.Context, q *sqlc.Queries, artifactID uuid.UUID) error {
	row, err := q.LockArtifactForDeletion(ctx, artifactID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrArtifactNotPresent
	}
	if err != nil {
		return fmt.Errorf("runstate: lock artifact for deletion: %w", err)
	}
	if row.Held {
		return ErrArtifactHeld
	}
	return nil
}
