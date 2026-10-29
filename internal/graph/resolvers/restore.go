package resolvers

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/graph/streams"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/restore"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// Restore resolvers (task 4.5, D-167). Authorization lives in
// internal/restore's API (the artifact's job and the target come from
// the database); the resolvers translate input and errors only. The age
// identity of restoreArtifact is copied into a byte slice that is
// registered with the log redaction registry for the request and zeroed
// right after it (the GraphQL string itself cannot be zeroed).

var errRestoresNotConfigured = errors.New("resolvers: restore API not configured")

const (
	restoreCursorPrefix = "restore:"
	restoreEventBuffer  = 64
	// restoreEventMinGap coalesces the events of one restore stream.
	restoreEventMinGap = 200 * time.Millisecond
)

func (r *Resolver) restoreAPI() (*restore.API, error) {
	if r.Restores == nil {
		return nil, errRestoresNotConfigured
	}
	return r.Restores, nil
}

// restoreError maps the restore API's errors to API errors.
func restoreError(err error) error {
	var (
		fe *serviceutil.FieldError
		ve *restore.VerificationError
	)
	switch {
	case err == nil:
		return nil
	case errors.As(err, &fe):
		return apierror.Invalid(fe.Field, fe.Message)
	case errors.As(err, &ve):
		return apierror.New(apierror.Conflict, "the backup failed its verification against the signed manifest and cannot be restored; this was recorded as a security event")
	case errors.Is(err, restore.ErrNotFound):
		return apierror.New(apierror.NotFound, "not found")
	case errors.Is(err, restore.ErrUnavailable):
		return apierror.New(apierror.Conflict, "the backup is not available for restores (deleted, missing, unverified or not covered by a signed manifest)")
	case errors.Is(err, restore.ErrKeyUnavailable):
		return apierror.New(apierror.Conflict, "the encryption key of this backup is not available")
	case errors.Is(err, restore.ErrDestination):
		return apierror.New(apierror.Conflict, "the destination holding the backup could not be read; try again later")
	case errors.Is(err, restore.ErrFinished):
		return apierror.New(apierror.Conflict, "the restore already finished")
	case errors.Is(err, restore.ErrTargetBusy):
		return apierror.New(apierror.Conflict, "another restore into this target is queued or running")
	case errors.Is(err, restore.ErrTargetChanged):
		return apierror.New(apierror.Conflict, "the target connection changed; check the restore again")
	case errors.Is(err, connections.ErrNotFound):
		return apierror.Invalid("targetConnectionId", "the target connection was deleted or has expired")
	default:
		return connectionError(err)
	}
}

// --- models -------------------------------------------------------------------

func restoreModeModel(m restore.Mode) *model.RestoreMode {
	var out model.RestoreMode
	switch m {
	case restore.ModeNewDB:
		out = model.RestoreModeNewDb
	case restore.ModeOverwrite:
		out = model.RestoreModeOverwrite
	default:
		return nil
	}
	return &out
}

func restoreModeFromModel(m model.RestoreMode) restore.Mode {
	switch m {
	case model.RestoreModeNewDb:
		return restore.ModeNewDB
	case model.RestoreModeOverwrite:
		return restore.ModeOverwrite
	default:
		return ""
	}
}

func strOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func restoreModel(v *restore.View, showActor bool) *model.Restore {
	running := !v.Finished() && v.Status != restore.StatusQueued
	m := &model.Restore{
		ID:         v.ID,
		Job:        &model.RunJobRef{ID: v.JobID, Name: v.JobName, Slug: v.JobSlug, Deleted: v.JobDeleted},
		ArtifactID: v.ArtifactID,
		Kind:       model.RunItemKind(strings.ToUpper(string(v.Kind))),
		Target: &model.RestoreTargetRef{
			ID: v.TargetConnectionID, Name: v.TargetName, Engine: engineModel(v.TargetEngine),
			Temporary: v.TargetTemporary, Deleted: v.TargetDeleted,
		},
		SourceDatabase:  strOrNil(v.SourceDatabase),
		TargetDatabase:  v.TargetDbName,
		KeepOwners:      !v.NoOwner,
		Status:          model.RestoreStatus(strings.ToUpper(string(v.Status))),
		CreatedAt:       v.CreatedAt,
		StartedAt:       v.StartedAt,
		FinishedAt:      v.FinishedAt,
		DurationSeconds: durationOf(v.StartedAt, v.FinishedAt, running),
		StagedBytes:     float64(v.StagedBytes),
		ErrorCode:       v.ErrorCode,
		ErrorMessage:    sanitizedOrNil(v.ErrorMessage),
		Warnings:        make([]model.RestoreOutcomeWarning, 0, len(v.Warnings)),
		LogLines:        int(v.LogLines),
		LogLinesDropped: int(v.LogLinesDropped),
		UpdatedAt:       v.UpdatedAt,
	}
	m.RunID = v.RunID
	if v.Kind == sqlc.RunItemKindDatabase {
		m.Mode = restoreModeModel(restore.Mode(v.Mode))
	} else {
		m.KeepOwners = false
	}
	for _, w := range v.Warnings {
		m.Warnings = append(m.Warnings, model.RestoreOutcomeWarning{Code: w, Message: restore.OutcomeWarningMessage(w)})
	}
	if showActor && v.RequestedBy != nil {
		m.RequestedBy = &model.RunActor{UserID: *v.RequestedBy, DisplayName: v.RequestedByName, Email: v.RequestedByEmail}
	}
	return m
}

func restoreSourceModel(s *restore.Source) *model.RestoreSource {
	return &model.RestoreSource{
		ArtifactID: s.ArtifactID, JobID: s.JobID, RunID: s.RunID,
		Kind: model.RunItemKind(strings.ToUpper(string(s.Kind))), Database: strOrNil(s.Database),
		Engine: engineModel(s.Engine), VersionKey: s.VersionKey, ServerVersion: s.ServerVersion, NeedsIdentity: s.NeedsIdentity(),
	}
}

func restoreTargetModel(t *restore.Target) model.RestoreTarget {
	c := &t.Connection
	return model.RestoreTarget{
		ID: c.ID, Name: c.Name, Engine: engineModel(c.Engine), VersionKey: c.VersionKey, Original: t.Original,
		Temporary: c.Temporary, ExpiresAt: c.ExpiresAt, Ready: c.Ready, NotReadyReason: strOrNil(c.NotReadyReason),
		InsecureTransport: c.InsecureTransport, NewDatabaseAllowed: t.NewDatabaseAllowed, ExistingRestricted: t.ExistingRestricted,
	}
}

func restorePreviewModel(p *restore.Preview, existingRestricted bool) *model.RestorePreview {
	t := restore.Target{
		Connection: p.Target, Original: p.Source.JobConnectionID != uuid.Nil && p.Target.ID == p.Source.JobConnectionID,
		NewDatabaseAllowed: p.Source.Kind == sqlc.ArtifactKindDatabase, ExistingRestricted: existingRestricted,
	}
	tm := restoreTargetModel(&t)
	out := &model.RestorePreview{
		Source: restoreSourceModel(&p.Source), Target: &tm, Warnings: make([]model.RestoreWarning, 0, len(p.Warnings)),
		ConfirmText: strOrNil(p.ConfirmText), TargetPrivileges: targetPrivilegesModel(p.Privileges), CrossSource: p.CrossSource,
	}
	if p.Source.Kind == sqlc.ArtifactKindDatabase {
		out.Mode = restoreModeModel(p.Mode)
		out.TargetDatabase = strOrNil(p.Database)
	}
	for _, w := range p.Warnings {
		out.Warnings = append(out.Warnings, model.RestoreWarning{Code: w.Code, Message: w.Message})
	}
	return out
}

// targetPrivilegesModel converts a privilege verdict (D-068).
func targetPrivilegesModel(p connections.Privileges) *model.TargetPrivileges {
	var st model.TargetPrivilegeStatus
	switch p.Status {
	case connections.PrivilegeNotPrivileged:
		st = model.TargetPrivilegeStatusNotPrivileged
	case connections.PrivilegePrivileged:
		st = model.TargetPrivilegeStatusPrivileged
	case connections.PrivilegeUnknown:
		st = model.TargetPrivilegeStatusUnknown
	default:
		st = model.TargetPrivilegeStatusUnknown
	}
	reasons := p.Reasons
	if reasons == nil {
		reasons = []string{}
	}
	return &model.TargetPrivileges{Status: st, Reasons: reasons, AcknowledgementRequired: p.Gated()}
}

// isOwnerCaller reports a backup owner (role user).
func (r *Resolver) isOwnerCaller(ctx context.Context) bool {
	p, err := caller(ctx)
	return err == nil && p.Role == auth.RoleUser
}

// --- queries ------------------------------------------------------------------

func (r *Resolver) restoreTargets(ctx context.Context, artifactID uuid.UUID) (*model.RestoreTargets, error) {
	api, err := r.restoreAPI()
	if err != nil {
		return nil, err
	}
	list, err := api.Targets(ctx, artifactID)
	if err != nil {
		return nil, restoreError(err)
	}
	out := &model.RestoreTargets{Source: restoreSourceModel(&list.Source), Targets: make([]model.RestoreTarget, 0, len(list.Targets))}
	for i := range list.Targets {
		out.Targets = append(out.Targets, restoreTargetModel(&list.Targets[i]))
	}
	return out, nil
}

func (r *Resolver) restoreTargetDatabases(ctx context.Context, artifactID, connID uuid.UUID) (*model.RestoreTargetDatabases, error) {
	api, err := r.restoreAPI()
	if err != nil {
		return nil, err
	}
	d, err := api.Databases(ctx, artifactID, connID)
	if err != nil {
		return nil, restoreError(err)
	}
	t := restoreTargetModel(&d.Target)
	warnings := d.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	return &model.RestoreTargetDatabases{
		Target: &t, Databases: d.Databases, Warnings: warnings, TargetPrivileges: targetPrivilegesModel(d.Privileges), ListedAt: d.ListedAt,
	}, nil
}

func (r *Resolver) restorePreview(ctx context.Context, in model.RestorePreviewInput) (*model.RestorePreview, error) {
	api, err := r.restoreAPI()
	if err != nil {
		return nil, err
	}
	var p restore.Preview
	if in.Mode == nil && in.TargetDatabase == nil {
		if in.KeepOwners != nil && *in.KeepOwners {
			return nil, apierror.Invalid("keepOwners", "not allowed for globals restores")
		}
		p, err = api.PreviewGlobals(ctx, restore.GlobalsInput{ArtifactID: in.ArtifactID, TargetConnectionID: in.TargetConnectionID})
	} else {
		if in.Mode == nil || in.TargetDatabase == nil {
			return nil, apierror.Invalid("mode", "mode and targetDatabase are required for database backups")
		}
		p, err = api.Preview(ctx, restore.RequestInput{
			ArtifactID: in.ArtifactID, TargetConnectionID: in.TargetConnectionID, Mode: restoreModeFromModel(*in.Mode),
			TargetDatabase: *in.TargetDatabase, KeepOwners: in.KeepOwners != nil && *in.KeepOwners,
		})
	}
	if err != nil {
		return nil, restoreError(err)
	}
	return restorePreviewModel(&p, r.isOwnerCaller(ctx)), nil
}

func (r *Resolver) restores(ctx context.Context, filter *model.RestoreFilter, first *int, after *string) (*model.RestoreConnection, error) {
	api, err := r.restoreAPI()
	if err != nil {
		return nil, err
	}
	limit, err := runPageSize(first, defaultRunPage, restore.MaxPage)
	if err != nil {
		return nil, err
	}
	at, id, ok, err := decodeTimeCursor(restoreCursorPrefix, after)
	if err != nil {
		return nil, err
	}
	var f restore.ListFilter
	if filter != nil {
		f.JobID, f.ArtifactID, f.TargetConnectionID = filter.JobID, filter.ArtifactID, filter.TargetConnectionID
		if filter.Status != nil {
			st := restore.Status(strings.ToLower(string(*filter.Status)))
			f.Status = &st
		}
	}
	var cur *restore.Cursor
	if ok {
		cur = &restore.Cursor{CreatedAt: at, ID: id}
	}
	page, err := api.List(ctx, f, cur, limit)
	if err != nil {
		return nil, restoreError(err)
	}
	show := r.showActor(ctx)
	conn := &model.RestoreConnection{Edges: make([]model.RestoreEdge, 0, len(page.Restores)), PageInfo: &model.PageInfo{HasNextPage: page.More, HasPreviousPage: cur != nil}}
	for i := range page.Restores {
		v := &page.Restores[i]
		conn.Edges = append(conn.Edges, model.RestoreEdge{Cursor: encodeTimeCursor(restoreCursorPrefix, v.CreatedAt, v.ID), Node: restoreModel(v, show)})
	}
	if n := len(conn.Edges); n > 0 {
		start, end := conn.Edges[0].Cursor, conn.Edges[n-1].Cursor
		conn.PageInfo.StartCursor, conn.PageInfo.EndCursor = &start, &end
	}
	return conn, nil
}

func (r *Resolver) restore(ctx context.Context, id uuid.UUID) (*model.Restore, error) {
	api, err := r.restoreAPI()
	if err != nil {
		return nil, err
	}
	v, err := api.Get(ctx, id)
	if errors.Is(err, restore.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, restoreError(err)
	}
	return restoreModel(&v, r.showActor(ctx)), nil
}

func (r *Resolver) restoreLogs(ctx context.Context, id uuid.UUID, afterID *string, first *int) (*model.RestoreLogPage, error) {
	api, err := r.restoreAPI()
	if err != nil {
		return nil, err
	}
	limit, err := runPageSize(first, defaultLogPage, restore.MaxLogPage)
	if err != nil {
		return nil, err
	}
	after, err := parseLogID(afterID)
	if err != nil {
		return nil, err
	}
	page, err := api.Logs(ctx, id, after, limit)
	if errors.Is(err, restore.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, restoreError(err)
	}
	lines := restoreLogLinesModel(page.Lines)
	var end *string
	if n := len(page.Lines); n > 0 {
		s := idString(page.Lines[n-1].ID)
		end = &s
	} else {
		end = afterID
	}
	return &model.RestoreLogPage{RestoreID: id, Lines: lines, EndID: end, HasMore: page.More, Dropped: page.Dropped}, nil
}

func restoreLogLinesModel(lines []sqlc.RestoreLog) []model.RunLogLine {
	out := make([]model.RunLogLine, 0, len(lines))
	for i := range lines {
		l := &lines[i]
		out = append(out, model.RunLogLine{
			ID: idString(l.ID), Time: l.Ts,
			Level:  model.RunLogLevel(strings.ToUpper(string(l.Level))),
			Stream: model.RunLogStream(strings.ToUpper(string(l.Stream))),
			Line:   l.Line,
		})
	}
	return out
}

// --- mutations ------------------------------------------------------------------

func (r *Resolver) restoreArtifact(ctx context.Context, in model.RestoreArtifactInput) (*model.Restore, error) {
	api, err := r.restoreAPI()
	if err != nil {
		return nil, err
	}
	req := restore.RequestInput{
		ArtifactID: in.ArtifactID, TargetConnectionID: in.TargetConnectionID, Mode: restoreModeFromModel(in.Mode),
		TargetDatabase: in.TargetDatabase, KeepOwners: in.KeepOwners != nil && *in.KeepOwners,
	}
	if in.ConfirmTargetName != nil {
		req.ConfirmTargetName = *in.ConfirmTargetName
	}
	req.AcknowledgePrivilegedTarget = in.AcknowledgePrivilegedTarget != nil && *in.AcknowledgePrivilegedTarget
	if in.AgeIdentity != nil && *in.AgeIdentity != "" {
		req.Identity = []byte(*in.AgeIdentity)
		// Redacted from every log line while the request runs; the
		// registry zeroes its copy when it is unregistered.
		defer logging.DefaultRegistry.Register(req.Identity)()
		defer crypto.Zero(req.Identity)
	}
	res, _, err := api.Request(ctx, req)
	if err != nil {
		return nil, restoreError(err)
	}
	return r.restoreAfterRequest(ctx, api, res.ID)
}

func (r *Resolver) restoreGlobals(ctx context.Context, in model.RestoreGlobalsInput) (*model.Restore, error) {
	api, err := r.restoreAPI()
	if err != nil {
		return nil, err
	}
	res, _, err := api.RequestGlobals(ctx, restore.GlobalsInput{
		ArtifactID: in.ArtifactID, TargetConnectionID: in.TargetConnectionID, ConfirmPhrase: in.ConfirmPhrase,
		AcknowledgePrivilegedTarget: in.AcknowledgePrivilegedTarget != nil && *in.AcknowledgePrivilegedTarget,
	})
	if err != nil {
		return nil, restoreError(err)
	}
	return r.restoreAfterRequest(ctx, api, res.ID)
}

// restoreAfterRequest loads the new restore with the caller's rights (it
// may already have moved on).
func (r *Resolver) restoreAfterRequest(ctx context.Context, api *restore.API, id uuid.UUID) (*model.Restore, error) {
	v, err := api.Get(ctx, id)
	if err != nil {
		return nil, restoreError(err)
	}
	return restoreModel(&v, r.showActor(ctx)), nil
}

func (r *Resolver) cancelRestore(ctx context.Context, id uuid.UUID) (*model.RestoreCancellation, error) {
	api, err := r.restoreAPI()
	if err != nil {
		return nil, err
	}
	c, err := api.Cancel(ctx, id)
	if err != nil {
		return nil, restoreError(err)
	}
	return &model.RestoreCancellation{
		RestoreID: c.RestoreID, JobID: c.JobID, Status: model.RestoreStatus(strings.ToUpper(string(c.Status))), RequestedAt: c.RequestedAt,
	}, nil
}

// --- subscription -----------------------------------------------------------------

// restoreEvent is one delivery of a restoreUpdated stream; check fills
// view with the row it authorized, so conv sends exactly that.
type restoreEvent struct {
	view *restore.View
}

// restoreUpdated streams the changes of one restore: bekup_restore_events
// carry "<kind>:<restore id>" only; every event re-loads the restore and
// authorizes restore.view with its job from the row (streams.Deliver
// re-checks the session first). The stream ends after the terminal status
// was delivered.
func (r *Resolver) restoreUpdated(ctx context.Context, id uuid.UUID) (<-chan *model.Restore, error) {
	if streams.FromContext(ctx) == nil {
		return nil, errNotStream
	}
	api, err := r.restoreAPI()
	if err != nil {
		return nil, err
	}
	if r.Bus == nil {
		return nil, errRunEventsNotConfigured
	}
	if _, err := api.Get(ctx, id); err != nil {
		if errors.Is(err, restore.ErrNotFound) {
			return nil, apierror.New(apierror.NotFound, "restore not found")
		}
		return nil, restoreError(err)
	}
	sub := r.Bus.Subscribe(pubsub.RestoreEvents.Name, restoreEventBuffer)
	src := make(chan *restoreEvent)
	done := make(chan struct{})
	go pumpRestoreEvents(ctx, sub, src, done, id, restoreEventMinGap)
	check := streams.Authorize(r.Authz, authz.OpRestoreView, func(ctx context.Context, ev *restoreEvent) ([]authz.Resource, error) {
		v, err := api.LoadForEvent(ctx, id)
		if err != nil {
			return nil, err
		}
		ev.view = &v
		return []authz.Resource{authz.Job{ID: v.JobID}}, nil
	})
	// conv runs on Deliver's goroutine only.
	finished := false
	return streams.Deliver(ctx, src, check, func(ctx context.Context, ev *restoreEvent) (*model.Restore, bool) {
		if ev.view == nil || finished {
			return nil, false
		}
		if ev.view.Finished() {
			finished = true
			close(done)
		}
		return restoreModel(ev.view, r.showActor(ctx)), true
	}), nil
}

// pumpRestoreEvents sends a (coalesced) event at the start, for every
// event of the restore and after possibly lost events, at most every gap,
// until ctx ends or done closes.
func pumpRestoreEvents(ctx context.Context, sub *pubsub.Subscription, src chan<- *restoreEvent, done <-chan struct{}, id uuid.UUID, gap time.Duration) {
	defer close(src)
	defer sub.Close()
	pending, ready := true, true
	timer := time.NewTimer(gap)
	timer.Stop()
	defer timer.Stop()
	for {
		var out chan<- *restoreEvent
		if pending && ready {
			out = src
		}
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case out <- &restoreEvent{}:
			pending, ready = false, false
			timer.Reset(gap)
		case <-timer.C:
			ready = true
		case m := <-sub.C():
			if _, rid, err := restore.ParsePayload(m.Payload); err == nil && rid == id {
				pending = true
			}
		case <-sub.Resync():
			pending = true
		}
	}
}

// restoreIDEvent is one delivery of a restoresUpdated stream.
type restoreIDEvent struct {
	id   uuid.UUID
	view *restore.View
}

// restoresUpdated streams the changes of every restore the caller may
// view (D-196): bekup_restore_events "restore:<id>" hints, coalesced per
// restore; every event re-loads the restore and authorizes restore.view
// with its job from the row. After possibly lost events the restores
// changed since the last resync (scoped like Query.restores) are re-sent.
func (r *Resolver) restoresUpdated(ctx context.Context) (<-chan *model.Restore, error) {
	if streams.FromContext(ctx) == nil {
		return nil, errNotStream
	}
	f, err := r.restoresUpdatesFeed(ctx)
	if err != nil {
		return nil, err
	}
	return f.Deliver(ctx), nil
}

// restoresUpdatesFeed checks access and starts the restore event source
// of restoresUpdated (and activityUpdated): it runs until ctx ends.
func (r *Resolver) restoresUpdatesFeed(ctx context.Context) (streams.Feed[*restoreIDEvent, *model.Restore], error) {
	var none streams.Feed[*restoreIDEvent, *model.Restore]
	api, err := r.restoreAPI()
	if err != nil {
		return none, err
	}
	if r.Bus == nil {
		return none, errRunEventsNotConfigured
	}
	if err := r.Authz.Precheck(ctx, authz.OpRestoreView); err != nil {
		return none, err
	}
	sub := r.Bus.Subscribe(pubsub.RestoreEvents.Name, runEventBuffer)
	src := make(chan *restoreIDEvent)
	go pumpCoalescedIDs(ctx, sub, src, func(id uuid.UUID) *restoreIDEvent { return &restoreIDEvent{id: id} },
		func(payload string) (uuid.UUID, bool) {
			kind, id, err := restore.ParsePayload(payload)
			return id, err == nil && kind == restore.EventRestore
		}, api.IDsUpdatedSince, runEventMinGap)
	check := streams.Authorize(r.Authz, authz.OpRestoreView, func(ctx context.Context, ev *restoreIDEvent) ([]authz.Resource, error) {
		v, err := api.LoadForEvent(ctx, ev.id)
		if err != nil {
			return nil, err
		}
		ev.view = &v
		return []authz.Resource{authz.Job{ID: v.JobID}}, nil
	})
	conv := func(ctx context.Context, ev *restoreIDEvent) (*model.Restore, bool) {
		if ev.view == nil {
			return nil, false
		}
		return restoreModel(ev.view, r.showActor(ctx)), true
	}
	return streams.Feed[*restoreIDEvent, *model.Restore]{Src: src, Check: check, Conv: conv}, nil
}

// pumpCoalescedIDs turns bus messages into events (parse filters and
// extracts the id, wrap builds the event) until ctx ends, closing out at
// the end. Like the run
// stream: an id already pending is not queued again, at most resyncMax
// ids are pending (beyond that the stream resyncs), ids are sent at most
// every gap, and after possibly lost messages the ids changed since the
// last resync (resync) are queued.
func pumpCoalescedIDs[T any](ctx context.Context, sub *pubsub.Subscription, out chan<- T, wrap func(uuid.UUID) T,
	parse func(string) (uuid.UUID, bool), resync func(context.Context, time.Time, int) ([]uuid.UUID, error), gap time.Duration,
) {
	defer close(out)
	defer sub.Close()
	since := time.Now()
	var pending []uuid.UUID
	queued := map[uuid.UUID]bool{}
	needResync := false
	add := func(id uuid.UUID) {
		if queued[id] {
			return
		}
		if len(pending) >= resyncMax {
			needResync = true
			return
		}
		queued[id] = true
		pending = append(pending, id)
	}
	ready := true
	timer := time.NewTimer(gap)
	timer.Stop()
	defer timer.Stop()
	for {
		if needResync && len(pending) == 0 {
			now := time.Now()
			list, err := resync(ctx, since.Add(-resyncMargin), resyncMax)
			if ctx.Err() != nil {
				return
			}
			// On an error since stays, so the next resync covers the gap.
			needResync = false
			if err == nil {
				since = now
				for _, id := range list {
					add(id)
				}
			}
		}
		var send chan<- T
		var next T
		if ready && len(pending) > 0 {
			send, next = out, wrap(pending[0])
		}
		select {
		case <-ctx.Done():
			return
		case send <- next:
			delete(queued, pending[0])
			pending = pending[1:]
			ready = false
			timer.Reset(gap)
		case <-timer.C:
			ready = true
		case m := <-sub.C():
			if id, ok := parse(m.Payload); ok {
				add(id)
			}
		case <-sub.Resync():
			needResync = true
		}
	}
}
