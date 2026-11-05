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

// Storage per job (D-225): what a job's present backup files occupy, for
// Job.storage. Every read is scoped like the run listings (Scope); the
// API authorizes the job itself first.

// MaxStorageTrend is the number of backups in a job's size trend.
const MaxStorageTrend = 30

// JobStorageTotals are a job's present backup files on all destinations.
type JobStorageTotals struct {
	StoredBytes   int64
	ArtifactCount int64
	BackupCount   int64
}

// JobStorageDestination is a job's present backup files on one
// destination.
type JobStorageDestination struct {
	DestinationID      uuid.UUID
	DestinationName    string
	DestinationType    sqlc.DestinationType
	DestinationDeleted bool
	JobStorageTotals
}

// JobStorageRun is the size of one backup: one copy (the largest
// per-destination sum of its files).
type JobStorageRun struct {
	RunID  uuid.UUID
	Status sqlc.RunStatus
	// At is when the run finished (its queue time when unknown).
	At    time.Time
	Bytes int64
}

// StorageForJobs returns the totals of jobIDs (at most MaxBatch; jobs
// without present files map to zero totals). One aggregate query.
func (r *Reader) StorageForJobs(ctx context.Context, s Scope, jobIDs []uuid.UUID) (map[uuid.UUID]JobStorageTotals, error) {
	if !s.valid() { // an invalid scope is refused like a bad page
		return nil, ErrBadPage
	}
	if len(jobIDs) > MaxBatch {
		return nil, ErrBadPage
	}
	out := make(map[uuid.UUID]JobStorageTotals, len(jobIDs))
	for _, id := range jobIDs {
		out[id] = JobStorageTotals{}
	}
	if len(jobIDs) == 0 {
		return out, nil
	}
	rows, err := sqlc.New(r.db).JobStorageTotals(ctx, sqlc.JobStorageTotalsParams{JobIds: jobIDs, ScopeAll: s.All, UserID: s.Owner})
	if err != nil {
		return nil, fmt.Errorf("runstate: job storage: %w", err)
	}
	for _, x := range rows {
		out[x.JobID] = JobStorageTotals{StoredBytes: x.StoredBytes, ArtifactCount: x.ArtifactCount, BackupCount: x.BackupCount}
	}
	return out, nil
}

// StorageByDestination returns a job's present files per destination,
// ordered by destination name.
func (r *Reader) StorageByDestination(ctx context.Context, s Scope, jobID uuid.UUID) ([]JobStorageDestination, error) {
	if !s.valid() { // an invalid scope is refused like a bad page
		return nil, ErrBadPage
	}
	rows, err := sqlc.New(r.db).JobStorageByDestination(ctx, sqlc.JobStorageByDestinationParams{JobID: jobID, ScopeAll: s.All, UserID: s.Owner})
	if err != nil {
		return nil, fmt.Errorf("runstate: job storage per destination: %w", err)
	}
	out := make([]JobStorageDestination, 0, len(rows))
	for _, x := range rows {
		out = append(out, JobStorageDestination{
			DestinationID: x.DestinationID, DestinationName: x.DestinationName, DestinationType: x.DestinationType,
			DestinationDeleted: x.DestinationDeletedAt != nil,
			JobStorageTotals:   JobStorageTotals{StoredBytes: x.StoredBytes, ArtifactCount: x.ArtifactCount, BackupCount: x.BackupCount},
		})
	}
	return out, nil
}

// StorageTrend returns the sizes of a job's newest MaxStorageTrend
// successful or partial backups, oldest first.
func (r *Reader) StorageTrend(ctx context.Context, s Scope, jobID uuid.UUID) ([]JobStorageRun, error) {
	if !s.valid() { // an invalid scope is refused like a bad page
		return nil, ErrBadPage
	}
	rows, err := sqlc.New(r.db).JobStorageRuns(ctx, sqlc.JobStorageRunsParams{JobID: jobID, ScopeAll: s.All, UserID: s.Owner, MaxRows: MaxStorageTrend})
	if err != nil {
		return nil, fmt.Errorf("runstate: job storage trend: %w", err)
	}
	out := make([]JobStorageRun, len(rows))
	for i, x := range rows {
		out[len(rows)-1-i] = JobStorageRun{RunID: x.RunID, Status: x.Status, At: finishedOr(x.FinishedAt, x.QueuedAt), Bytes: x.RunBytes}
	}
	return out, nil
}

// NewestStoredSuccess returns the newest successful backup of a job that
// still has present files, with the size of one copy (nil: none).
func (r *Reader) NewestStoredSuccess(ctx context.Context, s Scope, jobID uuid.UUID) (*JobStorageRun, error) {
	if !s.valid() { // an invalid scope is refused like a bad page
		return nil, ErrBadPage
	}
	x, err := sqlc.New(r.db).JobStorageNewestSuccess(ctx, sqlc.JobStorageNewestSuccessParams{JobID: jobID, ScopeAll: s.All, UserID: s.Owner})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("runstate: newest stored backup: %w", err)
	}
	return &JobStorageRun{RunID: x.RunID, Status: sqlc.RunStatusSuccess, At: finishedOr(x.FinishedAt, x.QueuedAt), Bytes: x.RunBytes}, nil
}

func finishedOr(finished *time.Time, queued time.Time) time.Time {
	if finished != nil {
		return *finished
	}
	return queued
}
