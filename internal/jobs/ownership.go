package jobs

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Ownership answers the policy's owner questions (authz.Ownership, D-106)
// from job_owners and jobs. It is deny by default: a lookup error denies.
// Deleted users own nothing; IsJobOwner also holds for a soft-deleted job
// (its artifacts stay restorable by its owners), BacksUpDatabase and
// UsesConnection only consider non-deleted jobs.
type Ownership struct {
	DB sqlc.DBTX
}

var _ authz.Ownership = Ownership{}

// IsJobOwner reports whether userID is listed as an owner of jobID.
func (o Ownership) IsJobOwner(ctx context.Context, userID, jobID uuid.UUID) (bool, error) {
	if userID == uuid.Nil || jobID == uuid.Nil {
		return false, nil
	}
	ok, err := sqlc.New(o.DB).IsJobOwner(ctx, sqlc.IsJobOwnerParams{JobID: jobID, UserID: userID})
	if err != nil {
		return false, fmt.Errorf("jobs: owner lookup: %w", err)
	}
	return ok, nil
}

// BacksUpDatabase reports whether a non-deleted job owned by userID backs
// up database on connectionID.
func (o Ownership) BacksUpDatabase(ctx context.Context, userID, connectionID uuid.UUID, database string) (bool, error) {
	if userID == uuid.Nil || connectionID == uuid.Nil || database == "" {
		return false, nil
	}
	ok, err := sqlc.New(o.DB).OwnerBacksUpDatabase(ctx, sqlc.OwnerBacksUpDatabaseParams{UserID: userID, ConnectionID: connectionID, Database: database})
	if err != nil {
		return false, fmt.Errorf("jobs: owner database lookup: %w", err)
	}
	return ok, nil
}

// UsesConnection reports whether a non-deleted job owned by userID uses
// connectionID.
func (o Ownership) UsesConnection(ctx context.Context, userID, connectionID uuid.UUID) (bool, error) {
	if userID == uuid.Nil || connectionID == uuid.Nil {
		return false, nil
	}
	ok, err := sqlc.New(o.DB).OwnerUsesConnection(ctx, sqlc.OwnerUsesConnectionParams{UserID: userID, ConnectionID: connectionID})
	if err != nil {
		return false, fmt.Errorf("jobs: owner connection lookup: %w", err)
	}
	return ok, nil
}
