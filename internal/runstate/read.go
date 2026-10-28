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

// Read side of the run tables (task 3.7): the run history, artifacts and
// logs the GraphQL API shows. Reader never writes and never authorizes:
// listings take the caller's job scope (authz.JobScope) and single loads
// return the run's job so the caller can authorize it.

// Read limits.
const (
	// MaxPage caps a page of runs or artifacts.
	MaxPage = 200
	// MaxLogPage caps a page of log lines.
	MaxLogPage = 1000
	// MaxBatch caps the ids of one batch load.
	MaxBatch = 500
	// MaxRecentPerJob caps the runs per job of RecentRunsForJobs.
	MaxRecentPerJob = 20
)

// ErrBadPage reports an out-of-range page size or batch.
var ErrBadPage = errors.New("runstate: invalid page size")

// RunView is a run with the names it is shown with.
type RunView struct {
	sqlc.Run
	JobName    string
	JobSlug    string
	JobDeleted bool
	// TriggeredByName and TriggeredByEmail describe the user of a manual
	// run (empty when none or deleted); show them to staff only.
	TriggeredByName  string
	TriggeredByEmail string
}

// ArtifactView is an artifact with the names it is shown with.
type ArtifactView struct {
	sqlc.Artifact
	DestinationName    string
	DestinationType    sqlc.DestinationType
	DestinationDeleted bool
	// Database is the item's database (empty for globals and manifests).
	Database         string
	JobName          string
	JobSlug          string
	StorageTimestamp *time.Time
}

// Scope limits a listing: All, or the jobs Owner owns.
type Scope struct {
	All   bool
	Owner uuid.UUID
}

func (s Scope) valid() bool { return s.All || s.Owner != uuid.Nil }

// RunFilter narrows ListRuns; zero fields do not filter.
type RunFilter struct {
	JobID   *uuid.UUID
	Status  *sqlc.RunStatus
	Trigger *sqlc.RunTrigger
	// Since (inclusive) and Until (exclusive) bound queued_at.
	Since, Until *time.Time
}

// RunCursor is the position after which a run page starts.
type RunCursor struct {
	QueuedAt time.Time
	ID       uuid.UUID
}

// RunPage is one page of runs, newest first.
type RunPage struct {
	Runs []RunView
	More bool
}

// ArtifactFilter narrows ListArtifacts; zero fields do not filter.
type ArtifactFilter struct {
	JobID         *uuid.UUID
	DestinationID *uuid.UUID
	RunID         *uuid.UUID
	State         *sqlc.ArtifactState
	Kind          *sqlc.ArtifactKind
}

// ArtifactCursor is the position after which an artifact page starts.
type ArtifactCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// ArtifactPage is one page of artifacts, newest first.
type ArtifactPage struct {
	Artifacts []ArtifactView
	More      bool
}

// Reader loads runs, items, artifacts and logs.
type Reader struct {
	db sqlc.DBTX
}

// NewReader returns a reader on db (*pgxpool.Pool).
func NewReader(db sqlc.DBTX) *Reader { return &Reader{db: db} }

func runView(r sqlc.Run, jobName, jobSlug string, jobDeleted *time.Time, name, email *string) RunView {
	v := RunView{Run: r, JobName: jobName, JobSlug: jobSlug, JobDeleted: jobDeleted != nil}
	if name != nil {
		v.TriggeredByName = *name
	}
	if email != nil {
		v.TriggeredByEmail = *email
	}
	return v
}

func strOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func enumText[T ~string](v *T) *string {
	if v == nil {
		return nil
	}
	s := string(*v)
	return &s
}

// ListRuns returns the page of runs after cur (nil: the first page).
func (r *Reader) ListRuns(ctx context.Context, s Scope, f RunFilter, cur *RunCursor, limit int) (RunPage, error) {
	if limit < 1 || limit > MaxPage || !s.valid() {
		return RunPage{}, ErrBadPage
	}
	p := sqlc.ListRunsPageParams{
		ScopeAll: s.All, UserID: s.Owner, JobID: f.JobID, Status: enumText(f.Status), RunTrigger: enumText(f.Trigger),
		Since: f.Since, Until: f.Until, MaxRows: int32(limit + 1),
	}
	if cur != nil {
		at, id := cur.QueuedAt, cur.ID
		p.AfterQueuedAt, p.AfterID = &at, &id
	}
	rows, err := sqlc.New(r.db).ListRunsPage(ctx, p)
	if err != nil {
		return RunPage{}, fmt.Errorf("runstate: list runs: %w", err)
	}
	page := RunPage{More: len(rows) > limit}
	if page.More {
		rows = rows[:limit]
	}
	page.Runs = make([]RunView, 0, len(rows))
	for i := range rows {
		x := &rows[i]
		page.Runs = append(page.Runs, runView(x.Run, x.JobName, x.JobSlug, x.JobDeletedAt, x.TriggeredByName, x.TriggeredByEmail))
	}
	return page, nil
}

// Run loads one run (ErrRunNotFound).
func (r *Reader) Run(ctx context.Context, id uuid.UUID) (RunView, error) {
	x, err := sqlc.New(r.db).GetRunView(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunView{}, ErrRunNotFound
	}
	if err != nil {
		return RunView{}, fmt.Errorf("runstate: load run: %w", err)
	}
	return runView(x.Run, x.JobName, x.JobSlug, x.JobDeletedAt, x.TriggeredByName, x.TriggeredByEmail), nil
}

// RecentRunsForJobs returns the newest perJob runs of each job, newest
// first; every requested job has an entry.
func (r *Reader) RecentRunsForJobs(ctx context.Context, jobIDs []uuid.UUID, perJob int) (map[uuid.UUID][]RunView, error) {
	if len(jobIDs) > MaxBatch || perJob < 1 || perJob > MaxRecentPerJob {
		return nil, ErrBadPage
	}
	out := make(map[uuid.UUID][]RunView, len(jobIDs))
	for _, id := range jobIDs {
		out[id] = nil
	}
	if len(jobIDs) == 0 {
		return out, nil
	}
	rows, err := sqlc.New(r.db).ListRecentRunsForJobs(ctx, sqlc.ListRecentRunsForJobsParams{
		JobIds: jobIDs, PerJob: int32(perJob),
	})
	if err != nil {
		return nil, fmt.Errorf("runstate: recent runs: %w", err)
	}
	for i := range rows {
		x := &rows[i]
		out[x.Run.JobID] = append(out[x.Run.JobID], runView(x.Run, x.JobName, x.JobSlug, x.JobDeletedAt, x.TriggeredByName, x.TriggeredByEmail))
	}
	return out, nil
}

// RunIDsUpdatedSince returns up to limit runs changed at or after since,
// optionally of one job or one run.
func (r *Reader) RunIDsUpdatedSince(ctx context.Context, since time.Time, jobID, runID *uuid.UUID, limit int) ([]uuid.UUID, error) {
	if limit < 1 || limit > MaxBatch {
		return nil, ErrBadPage
	}
	ids, err := sqlc.New(r.db).ListRunIDsUpdatedSince(ctx, sqlc.ListRunIDsUpdatedSinceParams{
		Since: since, JobID: jobID, RunID: runID, MaxRows: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("runstate: changed runs: %w", err)
	}
	return ids, nil
}

// Items returns the items of runs by run id (every requested run has an
// entry), in position order.
func (r *Reader) Items(ctx context.Context, runIDs []uuid.UUID) (map[uuid.UUID][]sqlc.RunItem, error) {
	if len(runIDs) > MaxBatch {
		return nil, ErrBadPage
	}
	out := make(map[uuid.UUID][]sqlc.RunItem, len(runIDs))
	for _, id := range runIDs {
		out[id] = nil
	}
	if len(runIDs) == 0 {
		return out, nil
	}
	rows, err := sqlc.New(r.db).ListRunItemsForRuns(ctx, runIDs)
	if err != nil {
		return nil, fmt.Errorf("runstate: list items: %w", err)
	}
	for i := range rows {
		out[rows[i].RunID] = append(out[rows[i].RunID], rows[i])
	}
	return out, nil
}

// Artifacts returns the artifacts of runs by run id (every requested run
// has an entry), oldest first.
func (r *Reader) Artifacts(ctx context.Context, runIDs []uuid.UUID) (map[uuid.UUID][]ArtifactView, error) {
	if len(runIDs) > MaxBatch {
		return nil, ErrBadPage
	}
	out := make(map[uuid.UUID][]ArtifactView, len(runIDs))
	for _, id := range runIDs {
		out[id] = nil
	}
	if len(runIDs) == 0 {
		return out, nil
	}
	rows, err := sqlc.New(r.db).ListArtifactsForRuns(ctx, runIDs)
	if err != nil {
		return nil, fmt.Errorf("runstate: list artifacts: %w", err)
	}
	for i := range rows {
		x := &rows[i]
		out[x.Artifact.RunID] = append(out[x.Artifact.RunID], ArtifactView{
			Artifact: x.Artifact, DestinationName: x.DestinationName, DestinationType: x.DestinationType,
			DestinationDeleted: x.DestinationDeletedAt != nil, Database: strOrEmpty(x.ItemDbName),
			JobName: x.JobName, JobSlug: x.JobSlug, StorageTimestamp: x.RunStorageTimestamp,
		})
	}
	return out, nil
}

// ListArtifacts returns the page of artifacts after cur (nil: the first
// page).
func (r *Reader) ListArtifacts(ctx context.Context, s Scope, f ArtifactFilter, cur *ArtifactCursor, limit int) (ArtifactPage, error) {
	if limit < 1 || limit > MaxPage || !s.valid() {
		return ArtifactPage{}, ErrBadPage
	}
	p := sqlc.ListArtifactsPageParams{
		ScopeAll: s.All, UserID: s.Owner, JobID: f.JobID, DestinationID: f.DestinationID, RunID: f.RunID,
		State: enumText(f.State), Kind: enumText(f.Kind), MaxRows: int32(limit + 1),
	}
	if cur != nil {
		at, id := cur.CreatedAt, cur.ID
		p.AfterCreatedAt, p.AfterID = &at, &id
	}
	rows, err := sqlc.New(r.db).ListArtifactsPage(ctx, p)
	if err != nil {
		return ArtifactPage{}, fmt.Errorf("runstate: list artifacts: %w", err)
	}
	page := ArtifactPage{More: len(rows) > limit}
	if page.More {
		rows = rows[:limit]
	}
	page.Artifacts = make([]ArtifactView, 0, len(rows))
	for i := range rows {
		x := &rows[i]
		page.Artifacts = append(page.Artifacts, ArtifactView{
			Artifact: x.Artifact, DestinationName: x.DestinationName, DestinationType: x.DestinationType,
			DestinationDeleted: x.DestinationDeletedAt != nil, Database: strOrEmpty(x.ItemDbName),
			JobName: x.JobName, JobSlug: x.JobSlug, StorageTimestamp: x.RunStorageTimestamp,
		})
	}
	return page, nil
}

// LogState is what a log reader needs to know about a run.
type LogState struct {
	JobID   uuid.UUID
	Status  sqlc.RunStatus
	Dropped int
}

// Finished reports whether the run reached a terminal status (no more
// lines will be appended once its writer flushed).
func (s LogState) Finished() bool { return s.Status != StatusQueued && s.Status != StatusRunning }

// LogState loads the job, status and dropped-line count of a run
// (ErrRunNotFound).
func (r *Reader) LogState(ctx context.Context, runID uuid.UUID) (LogState, error) {
	x, err := sqlc.New(r.db).GetRunLogCounts(ctx, runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return LogState{}, ErrRunNotFound
	}
	if err != nil {
		return LogState{}, fmt.Errorf("runstate: load run: %w", err)
	}
	return LogState{JobID: x.JobID, Status: x.Status, Dropped: int(x.LogLinesDropped)}, nil
}

// Logs returns up to limit lines of a run with an id above afterID, and
// whether more follow.
func (r *Reader) Logs(ctx context.Context, runID uuid.UUID, afterID int64, limit int) ([]sqlc.RunLog, bool, error) {
	if limit < 1 || limit > MaxLogPage || afterID < 0 {
		return nil, false, ErrBadPage
	}
	rows, err := sqlc.New(r.db).ListRunLogs(ctx, sqlc.ListRunLogsParams{
		RunID: runID, AfterID: afterID, MaxRows: int32(limit + 1),
	})
	if err != nil {
		return nil, false, fmt.Errorf("runstate: list logs: %w", err)
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	return rows, more, nil
}
