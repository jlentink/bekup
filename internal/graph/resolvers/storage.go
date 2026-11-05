package resolvers

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/destinations"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/runstate"
)

// Storage resolvers (D-225): the space behind a destination
// (Query.destinationSpace, staff) and what a job's backups occupy
// (Job.storage, with the job's view rules).

// destinationSpace serves Query.destinationSpace. The directive has
// authorized destination.space_view (staff); the service rate limits,
// bounds and caches the backend call.
func (r *Resolver) destinationSpace(ctx context.Context, id uuid.UUID) (*model.DestinationSpace, error) {
	svc, _, actor, err := r.destinationCaller(ctx)
	if err != nil {
		return nil, err
	}
	if err := r.Authz.Authorize(ctx, authz.OpDestinationSpaceView); err != nil {
		return nil, err
	}
	rep, err := svc.Space(ctx, actor, id)
	if err != nil {
		return nil, destinationError(err)
	}
	return destinationSpaceModel(&rep), nil
}

func destinationSpaceModel(rep *destinations.SpaceReport) *model.DestinationSpace {
	m := &model.DestinationSpace{
		DestinationID: rep.DestinationID, Status: model.DestinationSpaceStatusUnavailable,
		NewestSetBytes: float64(rep.NewestSetBytes), Low: rep.Low,
	}
	switch rep.Status {
	case destinations.SpaceAvailable:
		m.Status = model.DestinationSpaceStatusAvailable
	case destinations.SpaceUnsupported:
		m.Status = model.DestinationSpaceStatusUnsupported
	case destinations.SpaceUnavailable:
	}
	if rep.Reason != "" {
		reason := rep.Reason
		m.Reason = &reason
	}
	sp := rep.Space
	if sp.TotalKnown {
		v := float64(sp.Total)
		m.TotalBytes = &v
	}
	if sp.FreeKnown {
		v := float64(sp.Free)
		m.FreeBytes = &v
	}
	if sp.UsedKnown {
		v := float64(sp.Used)
		m.UsedBytes = &v
	}
	if !rep.CheckedAt.IsZero() {
		at := rep.CheckedAt
		m.CheckedAt = &at
	}
	return m
}

func clampInt(n int64) int { return int(min(max(n, 0), math.MaxInt32)) }

// jobStorage serves Job.storage: the totals, batch-loaded per page of
// jobs (one aggregate). The job was authorized by its parent field
// (job.view / JobScope); the reads are scoped again (defense in depth).
func (r *Resolver) jobStorage(ctx context.Context, obj *model.Job) (*model.JobStorage, error) {
	if _, err := r.runReader(); err != nil {
		return nil, err
	}
	if err := r.Authz.Authorize(ctx, authz.OpJobList); err != nil {
		return nil, err
	}
	scope, err := r.readScope(ctx)
	if err != nil {
		return nil, err
	}
	fetch := func(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]runstate.JobStorageTotals, error) {
		return r.Runs.StorageForJobs(ctx, scope, ids)
	}
	t, err := batchLoad(ctx, loaderFrom(ctx), func(l *runLoader) map[uuid.UUID]runstate.JobStorageTotals { return l.storage },
		jobHintsOf, obj.ID, fetch)
	if err != nil {
		return nil, err
	}
	return model.NewJobStorage(obj.ID, float64(t.StoredBytes), clampInt(t.ArtifactCount), clampInt(t.BackupCount)), nil
}

// jobStorageScope checks the detail fields of JobStorage: same access as
// the totals.
func (r *Resolver) jobStorageScope(ctx context.Context) (*runstate.Reader, runstate.Scope, error) {
	rd, err := r.runReader()
	if err != nil {
		return nil, runstate.Scope{}, err
	}
	if err := r.Authz.Authorize(ctx, authz.OpJobList); err != nil {
		return nil, runstate.Scope{}, err
	}
	scope, err := r.readScope(ctx)
	return rd, scope, err
}

// jobStoragePerDestination serves JobStorage.perDestination. Backup
// owners see the destination names and types of their own jobs' files,
// as the artifacts of their runs already show them (Artifact.destination).
func (r *Resolver) jobStoragePerDestination(ctx context.Context, obj *model.JobStorage) ([]model.JobStorageDestination, error) {
	rd, scope, err := r.jobStorageScope(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := rd.StorageByDestination(ctx, scope, obj.JobID())
	if err != nil {
		return nil, err
	}
	out := make([]model.JobStorageDestination, 0, len(rows))
	for i := range rows {
		x := &rows[i]
		out = append(out, model.JobStorageDestination{
			DestinationID: x.DestinationID, Name: x.DestinationName,
			Type: destinationTypeModel(destinations.Type(x.DestinationType)), Deleted: x.DestinationDeleted,
			StoredBytes: float64(x.StoredBytes), ArtifactCount: clampInt(x.ArtifactCount), BackupCount: clampInt(x.BackupCount),
		})
	}
	return out, nil
}

func jobStorageRunModel(x *runstate.JobStorageRun) model.JobStorageRun {
	return model.JobStorageRun{
		RunID: x.RunID, Status: model.RunStatus(strings.ToUpper(string(x.Status))),
		At: x.At.UTC().Truncate(time.Microsecond), Bytes: float64(x.Bytes),
	}
}

// jobStorageNewestRun serves JobStorage.newestRun.
func (r *Resolver) jobStorageNewestRun(ctx context.Context, obj *model.JobStorage) (*model.JobStorageRun, error) {
	rd, scope, err := r.jobStorageScope(ctx)
	if err != nil {
		return nil, err
	}
	x, err := rd.NewestStoredSuccess(ctx, scope, obj.JobID())
	if err != nil || x == nil {
		return nil, err
	}
	m := jobStorageRunModel(x)
	return &m, nil
}

// jobStorageTrend serves JobStorage.trend.
func (r *Resolver) jobStorageTrend(ctx context.Context, obj *model.JobStorage) ([]model.JobStorageRun, error) {
	rd, scope, err := r.jobStorageScope(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := rd.StorageTrend(ctx, scope, obj.JobID())
	if err != nil {
		return nil, err
	}
	out := make([]model.JobStorageRun, 0, len(rows))
	for i := range rows {
		out = append(out, jobStorageRunModel(&rows[i]))
	}
	return out, nil
}
