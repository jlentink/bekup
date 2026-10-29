package restore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
)

// Restore history reads (task 4.5): listings are scoped by the policy's
// JobScope (backup owners see the restores of the jobs they own), a
// single restore is authorized (restore.view) with the job loaded from its
// row, and an unknown id answers like a restore of another job for backup
// owners (FORBIDDEN).

// Read limits.
const (
	MaxPage    = 200
	MaxLogPage = 1000
)

// View is a restore with what the API shows about it.
type View struct {
	sqlc.Restore
	JobName      string
	JobSlug      string
	JobDeleted   bool
	TargetName   string
	TargetEngine engine.Kind
	// TargetTemporary / TargetDeleted describe the target connection.
	TargetTemporary bool
	TargetDeleted   bool
	Kind            sqlc.RunItemKind
	// RunID is the run of the restored artifact.
	RunID uuid.UUID
	// SourceDatabase is the backed-up database ("" for globals).
	SourceDatabase   string
	RequestedByName  string
	RequestedByEmail string
}

// Finished reports a terminal status.
func (v *View) Finished() bool { return isTerminal(v.Status) }

func viewOf(r sqlc.Restore, jobName, jobSlug string, jobDeleted *time.Time, targetName string, targetEngine sqlc.ConnectionEngine,
	targetTemp bool, targetDeleted *time.Time, kind sqlc.RunItemKind, runID uuid.UUID, srcDB, byName, byEmail *string,
) View {
	v := View{
		Restore: r, JobName: jobName, JobSlug: jobSlug, JobDeleted: jobDeleted != nil, TargetName: targetName,
		TargetEngine: engine.Kind(targetEngine), TargetTemporary: targetTemp, TargetDeleted: targetDeleted != nil, Kind: kind, RunID: runID,
	}
	if srcDB != nil {
		v.SourceDatabase = *srcDB
	}
	if byName != nil {
		v.RequestedByName = *byName
	}
	if byEmail != nil {
		v.RequestedByEmail = *byEmail
	}
	return v
}

// ListFilter filters restores; unset fields do not filter.
type ListFilter struct {
	JobID              *uuid.UUID
	ArtifactID         *uuid.UUID
	TargetConnectionID *uuid.UUID
	Status             *Status
}

// Cursor is the position after the last restore of a page.
type Cursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// Page is a page of restores.
type Page struct {
	Restores []View
	More     bool
}

// List returns restores newest first, scoped to the caller's jobs.
func (a *API) List(ctx context.Context, f ListFilter, after *Cursor, limit int) (Page, error) {
	// The coarse check; backup owners are limited by the scope below.
	if err := a.o.Policy.Precheck(ctx, authz.OpRestoreView); err != nil {
		return Page{}, err
	}
	scope, uid, err := a.o.Policy.JobScope(ctx)
	if err != nil {
		return Page{}, err
	}
	limit = min(max(limit, 1), MaxPage)
	p := sqlc.ListRestoresPageParams{
		ScopeAll: scope == authz.ScopeAll, UserID: uid, JobID: f.JobID, ArtifactID: f.ArtifactID,
		TargetConnectionID: f.TargetConnectionID, MaxRows: int32(limit + 1),
	}
	if f.Status != nil {
		st := string(*f.Status)
		p.Status = &st
	}
	if after != nil {
		at, id := after.CreatedAt, after.ID
		p.AfterCreatedAt, p.AfterID = &at, &id
	}
	rows, err := sqlc.New(a.svc.o.DB).ListRestoresPage(ctx, p)
	if err != nil {
		return Page{}, fmt.Errorf("restore: list restores: %w", err)
	}
	out := Page{Restores: make([]View, 0, min(len(rows), limit))}
	for i := range rows {
		if i == limit {
			out.More = true
			break
		}
		r := &rows[i]
		out.Restores = append(out.Restores, viewOf(r.Restore, r.JobName, r.JobSlug, r.JobDeletedAt, r.TargetName, r.TargetEngine,
			r.TargetTemporary, r.TargetDeletedAt, r.ItemKind, r.RunID, r.SourceDbName, r.RequestedByName, r.RequestedByEmail))
	}
	return out, nil
}

// load reads one restore view (ErrNotFound when missing), unauthorized.
func (a *API) load(ctx context.Context, id uuid.UUID) (View, error) {
	r, err := sqlc.New(a.svc.o.DB).GetRestoreView(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return View{}, ErrNotFound
	}
	if err != nil {
		return View{}, fmt.Errorf("restore: load restore: %w", err)
	}
	return viewOf(r.Restore, r.JobName, r.JobSlug, r.JobDeletedAt, r.TargetName, r.TargetEngine,
		r.TargetTemporary, r.TargetDeletedAt, r.ItemKind, r.RunID, r.SourceDbName, r.RequestedByName, r.RequestedByEmail), nil
}

// Get returns one restore the caller may view: ErrNotFound for staff when
// it does not exist, FORBIDDEN for backup owners (unknown or another
// job's).
func (a *API) Get(ctx context.Context, id uuid.UUID) (View, error) {
	v, err := a.load(ctx, id)
	if errors.Is(err, ErrNotFound) {
		if aerr := a.o.Policy.Authorize(ctx, authz.OpRestoreView, authz.Job{}); aerr != nil {
			return View{}, aerr
		}
		return View{}, ErrNotFound
	}
	if err != nil {
		return View{}, err
	}
	if err := a.o.Policy.Authorize(ctx, authz.OpRestoreView, authz.Job{ID: v.JobID}); err != nil {
		return View{}, err
	}
	return v, nil
}

// LoadForEvent re-loads a restore for a live event (the stream authorizes
// the returned job itself).
func (a *API) LoadForEvent(ctx context.Context, id uuid.UUID) (View, error) { return a.load(ctx, id) }

// LogPage is a page of restore log lines.
type LogPage struct {
	Lines   []sqlc.RestoreLog
	More    bool
	Dropped int
}

// Logs returns the log of a restore after afterID (same access as Get).
func (a *API) Logs(ctx context.Context, id uuid.UUID, afterID int64, limit int) (LogPage, error) {
	v, err := a.Get(ctx, id)
	if err != nil {
		return LogPage{}, err
	}
	return a.logs(ctx, &v, afterID, limit)
}

// LogsForEvent reads log lines of an already authorized restore.
func (a *API) LogsForEvent(ctx context.Context, v *View, afterID int64, limit int) (LogPage, error) {
	return a.logs(ctx, v, afterID, limit)
}

func (a *API) logs(ctx context.Context, v *View, afterID int64, limit int) (LogPage, error) {
	limit = min(max(limit, 1), MaxLogPage)
	rows, err := sqlc.New(a.svc.o.DB).ListRestoreLogs(ctx, sqlc.ListRestoreLogsParams{
		RestoreID: v.ID, AfterID: max(afterID, 0), MaxRows: int32(limit + 1),
	})
	if err != nil {
		return LogPage{}, fmt.Errorf("restore: restore logs: %w", err)
	}
	out := LogPage{Dropped: int(v.LogLinesDropped)}
	if len(rows) > limit {
		rows, out.More = rows[:limit], true
	}
	out.Lines = rows
	return out, nil
}

// IDsUpdatedSince returns up to limit (1..MaxPage) restores the caller
// may view that changed at or after since, newest change first (the
// resync of the restoresUpdated stream; D-196). Delivery re-loads and
// re-authorizes each one.
func (a *API) IDsUpdatedSince(ctx context.Context, since time.Time, limit int) ([]uuid.UUID, error) {
	if err := a.o.Policy.Precheck(ctx, authz.OpRestoreView); err != nil {
		return nil, err
	}
	scope, uid, err := a.o.Policy.JobScope(ctx)
	if err != nil {
		return nil, err
	}
	ids, err := sqlc.New(a.svc.o.DB).ListRestoreIDsUpdatedSince(ctx, sqlc.ListRestoreIDsUpdatedSinceParams{
		Since: since, ScopeAll: scope == authz.ScopeAll, UserID: uid, MaxRows: int32(min(max(limit, 1), MaxPage)),
	})
	if err != nil {
		return nil, fmt.Errorf("restore: restores updated since: %w", err)
	}
	return ids, nil
}
