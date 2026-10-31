package resolvers

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/runstate"
)

// Run history resolvers (task 3.7). Listings filter by Policy.JobScope;
// a single run is authorized with the job loaded from its row (never from
// input), and an unknown run answers like a run of someone else's job for
// backup owners (FORBIDDEN), so ids cannot be probed. Run.triggeredBy is
// staff only (run.actor_view). Items, artifacts and recent runs are batch
// loaded per page (runLoader).

var errRunsNotConfigured = errors.New("resolvers: run reader not configured")

const (
	runCursorPrefix      = "run:"
	artifactCursorPrefix = "artifact:"
	maxRunCursorLen      = 256
	defaultRunPage       = 50
	defaultLogPage       = 200
	defaultRecentRuns    = 5
	maxLogIDLen          = 19
)

func (r *Resolver) runReader() (*runstate.Reader, error) {
	if r.Runs == nil {
		return nil, errRunsNotConfigured
	}
	return r.Runs, nil
}

// pageSize validates first (1..max, default def).
func runPageSize(first *int, def, maxN int) (int, error) {
	if first == nil {
		return def, nil
	}
	if *first < 1 || *first > maxN {
		return 0, apierror.Invalid("first", "first must be between 1 and "+strconv.Itoa(maxN))
	}
	return *first, nil
}

// --- cursors ------------------------------------------------------------------

func encodeTimeCursor(prefix string, t time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(prefix + strconv.FormatInt(t.UnixMicro(), 10) + ":" + id.String()))
}

func decodeTimeCursor(prefix string, after *string) (time.Time, uuid.UUID, bool, error) {
	if after == nil {
		return time.Time{}, uuid.Nil, false, nil
	}
	errBad := apierror.Invalid("after", "invalid cursor")
	if len(*after) > maxRunCursorLen {
		return time.Time{}, uuid.Nil, false, errBad
	}
	raw, err := base64.RawURLEncoding.DecodeString(*after)
	if err != nil {
		return time.Time{}, uuid.Nil, false, errBad
	}
	rest, ok := strings.CutPrefix(string(raw), prefix)
	if !ok {
		return time.Time{}, uuid.Nil, false, errBad
	}
	us, idStr, ok := strings.Cut(rest, ":")
	if !ok {
		return time.Time{}, uuid.Nil, false, errBad
	}
	n, err := strconv.ParseInt(us, 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, false, errBad
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return time.Time{}, uuid.Nil, false, errBad
	}
	return time.UnixMicro(n), id, true, nil
}

// parseLogID parses a run log line id (afterId).
func parseLogID(s *string) (int64, error) {
	if s == nil {
		return 0, nil
	}
	errBad := apierror.Invalid("afterId", "invalid log line id")
	if *s == "" || len(*s) > maxLogIDLen {
		return 0, errBad
	}
	n, err := strconv.ParseInt(*s, 10, 64)
	if err != nil || n < 0 {
		return 0, errBad
	}
	return n, nil
}

// --- scope and access ---------------------------------------------------------

// readScope returns the listing scope of the caller (authz.JobScope).
func (r *Resolver) readScope(ctx context.Context) (runstate.Scope, error) {
	scope, uid, err := r.Authz.JobScope(ctx)
	if err != nil {
		return runstate.Scope{}, err
	}
	switch scope {
	case authz.ScopeAll:
		return runstate.Scope{All: true}, nil
	case authz.ScopeOwned:
		return runstate.Scope{Owner: uid}, nil
	default:
		return runstate.Scope{}, apierror.New(apierror.Forbidden, "forbidden")
	}
}

// authorizeRun authorizes op on the run of jobID; found = false means the
// run does not exist: staff get (nil, ok) to answer null or NOT_FOUND,
// backup owners FORBIDDEN, exactly as for a run of another job.
func (r *Resolver) authorizeRun(ctx context.Context, op authz.Operation, jobID uuid.UUID, found bool) error {
	if !found {
		// uuid.Nil never passes an ownership check; staff pass on role.
		return r.Authz.Authorize(ctx, op, authz.Job{})
	}
	return r.Authz.Authorize(ctx, op, authz.Job{ID: jobID})
}

// showActor reports whether the caller may see who started a run.
func (r *Resolver) showActor(ctx context.Context) bool {
	return r.Authz.Authorize(ctx, authz.OpRunActorView) == nil
}

// --- models -------------------------------------------------------------------

func hexOrNil(b []byte) *string {
	if len(b) == 0 {
		return nil
	}
	s := hex.EncodeToString(b)
	return &s
}

func sanitizedOrNil(s *string) *string {
	if s == nil {
		return nil
	}
	v := runstate.SanitizeLine(*s)
	return &v
}

func durationOf(start, end *time.Time, running bool) *float64 {
	if start == nil {
		return nil
	}
	var stop time.Time
	switch {
	case end != nil:
		stop = *end
	case running:
		stop = time.Now()
	default:
		return nil
	}
	d := max(stop.Sub(*start).Seconds(), 0)
	return &d
}

// warningsModel decodes stored warnings (bounded: runstate.MaxWarnings).
func warningsModel(raw json.RawMessage) []model.RunWarning {
	var ws []runstate.Warning
	if len(raw) == 0 || json.Unmarshal(raw, &ws) != nil {
		return []model.RunWarning{}
	}
	out := make([]model.RunWarning, 0, min(len(ws), runstate.MaxWarnings))
	for _, w := range ws {
		if len(out) >= runstate.MaxWarnings {
			break
		}
		mw := model.RunWarning{Code: runstate.SanitizeLine(w.Code), Message: runstate.SanitizeLine(w.Message)}
		if w.Database != "" {
			db := runstate.SanitizeLine(w.Database)
			mw.Database = &db
		}
		if id, err := uuid.Parse(w.DestinationID); err == nil {
			mw.DestinationID = &id
		}
		out = append(out, mw)
	}
	return out
}

func runModel(v *runstate.RunView, showActor bool) *model.Run {
	running := v.Status == runstate.StatusRunning
	m := &model.Run{
		ID:                v.ID,
		Job:               &model.RunJobRef{ID: v.JobID, Name: v.JobName, Slug: v.JobSlug, Deleted: v.JobDeleted},
		Status:            model.RunStatus(strings.ToUpper(string(v.Status))),
		Trigger:           model.RunTrigger(strings.ToUpper(string(v.Trigger))),
		ScheduledFor:      v.ScheduledFor,
		Attempt:           int(v.Attempt),
		QueuedAt:          v.QueuedAt,
		StartedAt:         v.StartedAt,
		FinishedAt:        v.FinishedAt,
		DurationSeconds:   durationOf(v.StartedAt, v.FinishedAt, running),
		StorageTimestamp:  v.StorageTimestamp,
		ErrorCode:         v.ErrorCode,
		ErrorMessage:      sanitizedOrNil(v.ErrorMessage),
		Warnings:          warningsModel(v.Warnings),
		Notices:           warningsModel(v.Notices),
		BytesRaw:          float64(v.BytesRaw),
		BytesStored:       float64(v.BytesStored),
		CancelRequested:   v.CancelRequestedAt != nil,
		CancelRequestedAt: v.CancelRequestedAt,
		LogLines:          int(v.LogLines),
		LogLinesDropped:   int(v.LogLinesDropped),
		UpdatedAt:         v.UpdatedAt,
	}
	if showActor && v.TriggeredBy != nil {
		m.TriggeredBy = &model.RunActor{UserID: *v.TriggeredBy, DisplayName: v.TriggeredByName, Email: v.TriggeredByEmail}
	}
	return m
}

func itemModel(it *sqlc.RunItem) model.RunItem {
	return model.RunItem{
		ID:                  it.ID,
		RunID:               it.RunID,
		Kind:                model.RunItemKind(strings.ToUpper(string(it.Kind))),
		Database:            it.DbName,
		Position:            int(it.Position),
		Status:              model.RunItemStatus(strings.ToUpper(string(it.Status))),
		RawSize:             float64(it.RawSize),
		StoredSize:          float64(it.StoredSize),
		RawSha256:           hexOrNil(it.RawSha256),
		Sha256:              hexOrNil(it.Sha256),
		Verified:            it.Verified,
		StartedAt:           it.StartedAt,
		FinishedAt:          it.FinishedAt,
		DurationSeconds:     durationOf(&it.StartedAt, it.FinishedAt, it.Status == runstate.ItemRunning),
		ErrorCode:           it.ErrorCode,
		ErrorMessage:        sanitizedOrNil(it.ErrorMessage),
		Warnings:            warningsModel(it.Warnings),
		SchemaVersion:       it.SchemaVersion,
		SchemaVersionStatus: schemaVersionStatusModel(it.SchemaVersionStatus),
	}
}

func encryptionModeOf(mode *string) *model.JobEncryptionMode {
	if mode == nil {
		return nil
	}
	var m model.JobEncryptionMode
	switch *mode {
	case "passphrase":
		m = model.JobEncryptionModePassphrase
	case "recipients":
		m = model.JobEncryptionModeRecipient
	case "none":
		m = model.JobEncryptionModeNone
	default:
		return nil
	}
	return &m
}

func optInt32(v *int32) *int {
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}

func artifactModel(a *runstate.ArtifactView) model.Artifact {
	m := model.Artifact{
		ID:        a.ID,
		RunID:     a.RunID,
		RunItemID: a.RunItemID,
		Job:       &model.RunJobRef{ID: a.JobID, Name: a.JobName, Slug: a.JobSlug},
		Destination: &model.ArtifactDestinationRef{
			ID: a.DestinationID, Name: a.DestinationName,
			Type: model.DestinationType(strings.ToUpper(string(a.DestinationType))), Deleted: a.DestinationDeleted,
		},
		Kind:             model.ArtifactKind(strings.ToUpper(string(a.Kind))),
		ObjectKey:        a.ObjectKey,
		StorageTimestamp: a.StorageTimestamp,
		Size:             float64(a.Size),
		Sha256:           hex.EncodeToString(a.Sha256),
		VersionID:        a.VersionID,
		Encryption: &model.ArtifactEncryption{
			Mode: encryptionModeOf(a.EncryptionMode), PassphraseVersion: optInt32(a.PassphraseVersion),
			RecipientVersion: optInt32(a.RecipientVersion),
		},
		Verified:       a.Verified,
		Signed:         a.SigningKeyID != nil,
		SigningKeyID:   a.SigningKeyID,
		LockUntil:      a.LockUntil,
		State:          model.ArtifactState(strings.ToUpper(string(a.State))),
		StateChangedAt: a.StateChangedAt,
		CreatedAt:      a.CreatedAt,
		DeletedAt:      a.DeletedAt,
	}
	if a.Database != "" {
		db := a.Database
		m.Database = &db
	}
	return m
}

func logLineModel(l *sqlc.RunLog) model.RunLogLine {
	return model.RunLogLine{
		ID: strconv.FormatInt(l.ID, 10), Time: l.Ts,
		Level:  model.RunLogLevel(strings.ToUpper(string(l.Level))),
		Stream: model.RunLogStream(strings.ToUpper(string(l.Stream))),
		Line:   l.Line,
	}
}

// --- queries ------------------------------------------------------------------

func (r *Resolver) runs(ctx context.Context, filter *model.RunFilter, first *int, after *string) (*model.RunConnection, error) {
	rd, err := r.runReader()
	if err != nil {
		return nil, err
	}
	limit, err := runPageSize(first, defaultRunPage, runstate.MaxPage)
	if err != nil {
		return nil, err
	}
	at, id, ok, err := decodeTimeCursor(runCursorPrefix, after)
	if err != nil {
		return nil, err
	}
	scope, err := r.readScope(ctx)
	if err != nil {
		return nil, err
	}
	var f runstate.RunFilter
	if filter != nil {
		f.JobID, f.Since, f.Until = filter.JobID, filter.Since, filter.Until
		if filter.Status != nil {
			st := sqlc.RunStatus(strings.ToLower(string(*filter.Status)))
			f.Status = &st
		}
		if filter.Trigger != nil {
			tr := sqlc.RunTrigger(strings.ToLower(string(*filter.Trigger)))
			f.Trigger = &tr
		}
	}
	var cur *runstate.RunCursor
	if ok {
		cur = &runstate.RunCursor{QueuedAt: at, ID: id}
	}
	page, err := rd.ListRuns(ctx, scope, f, cur, limit)
	if err != nil {
		return nil, err
	}
	show := r.showActor(ctx)
	conn := &model.RunConnection{Edges: make([]model.RunEdge, 0, len(page.Runs)), PageInfo: &model.PageInfo{HasNextPage: page.More, HasPreviousPage: cur != nil}}
	ids := make([]uuid.UUID, 0, len(page.Runs))
	for i := range page.Runs {
		v := &page.Runs[i]
		ids = append(ids, v.ID)
		conn.Edges = append(conn.Edges, model.RunEdge{Cursor: encodeTimeCursor(runCursorPrefix, v.QueuedAt, v.ID), Node: runModel(v, show)})
	}
	hintRuns(ctx, ids)
	if n := len(conn.Edges); n > 0 {
		start, end := conn.Edges[0].Cursor, conn.Edges[n-1].Cursor
		conn.PageInfo.StartCursor, conn.PageInfo.EndCursor = &start, &end
	}
	return conn, nil
}

func (r *Resolver) run(ctx context.Context, id uuid.UUID) (*model.Run, error) {
	rd, err := r.runReader()
	if err != nil {
		return nil, err
	}
	v, err := rd.Run(ctx, id)
	if errors.Is(err, runstate.ErrRunNotFound) {
		return nil, r.authorizeRun(ctx, authz.OpRunView, uuid.Nil, false)
	}
	if err != nil {
		return nil, err
	}
	if err := r.authorizeRun(ctx, authz.OpRunView, v.JobID, true); err != nil {
		return nil, err
	}
	return runModel(&v, r.showActor(ctx)), nil
}

func (r *Resolver) runLogs(ctx context.Context, runID uuid.UUID, afterID *string, first *int) (*model.RunLogPage, error) {
	rd, err := r.runReader()
	if err != nil {
		return nil, err
	}
	limit, err := runPageSize(first, defaultLogPage, runstate.MaxLogPage)
	if err != nil {
		return nil, err
	}
	after, err := parseLogID(afterID)
	if err != nil {
		return nil, err
	}
	st, err := rd.LogState(ctx, runID)
	if errors.Is(err, runstate.ErrRunNotFound) {
		return nil, r.authorizeRun(ctx, authz.OpRunLogsView, uuid.Nil, false)
	}
	if err != nil {
		return nil, err
	}
	if err := r.authorizeRun(ctx, authz.OpRunLogsView, st.JobID, true); err != nil {
		return nil, err
	}
	lines, more, err := rd.Logs(ctx, runID, after, limit)
	if err != nil {
		return nil, err
	}
	return &model.RunLogPage{RunID: runID, Lines: logLinesModel(lines), EndID: endID(lines, afterID), HasMore: more, Dropped: st.Dropped}, nil
}

func logLinesModel(lines []sqlc.RunLog) []model.RunLogLine {
	out := make([]model.RunLogLine, 0, len(lines))
	for i := range lines {
		out = append(out, logLineModel(&lines[i]))
	}
	return out
}

// endID is the id of the last line, or afterID when there are none.
func endID(lines []sqlc.RunLog, afterID *string) *string {
	if n := len(lines); n > 0 {
		s := strconv.FormatInt(lines[n-1].ID, 10)
		return &s
	}
	return afterID
}

func (r *Resolver) artifacts(ctx context.Context, filter *model.ArtifactFilter, first *int, after *string) (*model.ArtifactConnection, error) {
	rd, err := r.runReader()
	if err != nil {
		return nil, err
	}
	limit, err := runPageSize(first, defaultRunPage, runstate.MaxPage)
	if err != nil {
		return nil, err
	}
	at, id, ok, err := decodeTimeCursor(artifactCursorPrefix, after)
	if err != nil {
		return nil, err
	}
	scope, err := r.readScope(ctx)
	if err != nil {
		return nil, err
	}
	var f runstate.ArtifactFilter
	if filter != nil {
		f.JobID, f.DestinationID, f.RunID = filter.JobID, filter.DestinationID, filter.RunID
		if filter.State != nil {
			s := sqlc.ArtifactState(strings.ToLower(string(*filter.State)))
			f.State = &s
		}
		if filter.Kind != nil {
			k := sqlc.ArtifactKind(strings.ToLower(string(*filter.Kind)))
			f.Kind = &k
		}
	}
	var cur *runstate.ArtifactCursor
	if ok {
		cur = &runstate.ArtifactCursor{CreatedAt: at, ID: id}
	}
	page, err := rd.ListArtifacts(ctx, scope, f, cur, limit)
	if err != nil {
		return nil, err
	}
	conn := &model.ArtifactConnection{Edges: make([]model.ArtifactEdge, 0, len(page.Artifacts)), PageInfo: &model.PageInfo{HasNextPage: page.More, HasPreviousPage: cur != nil}}
	for i := range page.Artifacts {
		a := &page.Artifacts[i]
		node := artifactModel(a)
		conn.Edges = append(conn.Edges, model.ArtifactEdge{Cursor: encodeTimeCursor(artifactCursorPrefix, a.CreatedAt, a.ID), Node: &node})
	}
	if n := len(conn.Edges); n > 0 {
		start, end := conn.Edges[0].Cursor, conn.Edges[n-1].Cursor
		conn.PageInfo.StartCursor, conn.PageInfo.EndCursor = &start, &end
	}
	return conn, nil
}

// --- run fields (inherit the run's authorization) -------------------------------

func (r *Resolver) runItems(ctx context.Context, obj *model.Run) ([]model.RunItem, error) {
	if _, err := r.runReader(); err != nil {
		return nil, err
	}
	items, err := r.loadItems(ctx, obj.ID)
	if err != nil {
		return nil, err
	}
	out := make([]model.RunItem, 0, len(items))
	for i := range items {
		out = append(out, itemModel(&items[i]))
	}
	return out, nil
}

func (r *Resolver) runArtifacts(ctx context.Context, runID uuid.UUID, item *uuid.UUID, kind *sqlc.ArtifactKind) ([]model.Artifact, error) {
	if _, err := r.runReader(); err != nil {
		return nil, err
	}
	arts, err := r.loadArtifacts(ctx, runID)
	if err != nil {
		return nil, err
	}
	out := make([]model.Artifact, 0, len(arts))
	for i := range arts {
		a := &arts[i]
		if item != nil && (a.RunItemID == nil || *a.RunItemID != *item) {
			continue
		}
		if kind != nil && a.Kind != *kind {
			continue
		}
		out = append(out, artifactModel(a))
	}
	return out, nil
}

func (r *Resolver) runManifest(ctx context.Context, obj *model.Run) (*model.RunManifest, error) {
	if _, err := r.runReader(); err != nil {
		return nil, err
	}
	items, err := r.loadItems(ctx, obj.ID)
	if err != nil {
		return nil, err
	}
	arts, err := r.loadArtifacts(ctx, obj.ID)
	if err != nil {
		return nil, err
	}
	m := &model.RunManifest{Copies: []model.Artifact{}, TotalItems: len(items)}
	covered := map[uuid.UUID]bool{}
	for i := range arts {
		a := &arts[i]
		switch {
		case a.Kind == sqlc.ArtifactKindManifest:
			m.Copies = append(m.Copies, artifactModel(a))
			if a.SigningKeyID != nil && m.SigningKeyID == nil {
				m.Signed, m.SigningKeyID = true, a.SigningKeyID
			}
		case a.RunItemID != nil && a.Verified && a.SigningKeyID != nil:
			covered[*a.RunItemID] = true
		}
	}
	m.CoveredItems = len(covered)
	return m, nil
}

// jobRecentRuns serves Job.lastRun and Job.recentRuns. The job itself was
// authorized by its parent field (job.view / JobScope), which grants its
// runs to the same callers (run.view, run.list).
func (r *Resolver) jobRecentRuns(ctx context.Context, obj *model.Job, n int) ([]runstate.RunView, error) {
	if _, err := r.runReader(); err != nil {
		return nil, err
	}
	if err := r.Authz.Authorize(ctx, authz.OpRunList); err != nil {
		return nil, err
	}
	return r.loadRecentRuns(ctx, obj.ID, n)
}

// jobMissed serves Job.missed and Job.missedSince (D-196): the state the
// missed-backup check recorded, batch-loaded per page of jobs. Same access
// as the job's runs.
func (r *Resolver) jobMissed(ctx context.Context, obj *model.Job) (*runstate.MissedState, error) {
	if _, err := r.runReader(); err != nil {
		return nil, err
	}
	if err := r.Authz.Authorize(ctx, authz.OpRunList); err != nil {
		return nil, err
	}
	return r.loadMissed(ctx, obj.ID)
}

func (r *Resolver) jobLastRun(ctx context.Context, obj *model.Job) (*model.Run, error) {
	runs, err := r.jobRecentRuns(ctx, obj, 1)
	if err != nil || len(runs) == 0 {
		return nil, err
	}
	return runModel(&runs[0], r.showActor(ctx)), nil
}

func (r *Resolver) jobRecentRunsList(ctx context.Context, obj *model.Job, first *int) ([]model.Run, error) {
	n, err := runPageSize(first, defaultRecentRuns, runstate.MaxRecentPerJob)
	if err != nil {
		return nil, err
	}
	runs, err := r.jobRecentRuns(ctx, obj, n)
	if err != nil {
		return nil, err
	}
	show := r.showActor(ctx)
	out := make([]model.Run, 0, len(runs))
	for i := range runs {
		out = append(out, *runModel(&runs[i], show))
	}
	return out, nil
}

func idString(id int64) string { return strconv.FormatInt(id, 10) }

// runItemStats returns the completeness evidence of an item (D-187). It
// inherits the run's authorization (RunItem is reachable through Run only)
// and is batch-loaded for every hinted run of the operation.
func (r *Resolver) runItemStats(ctx context.Context, obj *model.RunItem) (*model.RunItemStats, error) {
	if _, err := r.runReader(); err != nil {
		return nil, err
	}
	byItem, err := r.loadStats(ctx, obj.RunID)
	if err != nil {
		return nil, err
	}
	st := byItem[obj.ID]
	if st == nil {
		return nil, nil
	}
	return itemStatsModel(st), nil
}

func itemStatsModel(st *runstate.ItemStats) *model.RunItemStats {
	m := &model.RunItemStats{
		Level: model.StatsLevelStandard, Consistency: model.StatsConsistencyApproximate,
		Objects: make([]model.StatsObjectCount, 0, len(st.Objects)), Tables: make([]model.TableStats, 0, len(st.Tables)),
		TablesTotal: int(min(st.TablesTotal, math.MaxInt32)), TablesTruncated: st.TablesTruncated,
		Warnings: findingsModel(st.Warnings), Notices: findingsModel(st.Notices), RecordedAt: st.RecordedAt,
	}
	if st.Level == "deep" {
		m.Level = model.StatsLevelDeep
	}
	if st.Consistency == "snapshot" {
		m.Consistency = model.StatsConsistencySnapshot
	}
	for _, o := range st.Objects {
		m.Objects = append(m.Objects, model.StatsObjectCount{Kind: o.Kind, Count: float64(o.Count)})
	}
	for i := range st.Tables {
		t := &st.Tables[i]
		tm := model.TableStats{Name: t.Name, Approximate: t.Approximate}
		switch t.Count {
		case runstate.RowCountExact:
			tm.Count, tm.Exact = model.RowCountKindExact, true
		case runstate.RowCountEstimate:
			tm.Count, tm.Estimate = model.RowCountKindEstimate, true
		default:
			tm.Count, tm.Unknown = model.RowCountKindUnknown, true
		}
		if t.Rows != nil && !tm.Unknown {
			rows := float64(*t.Rows)
			tm.Rows = &rows
		}
		m.Tables = append(m.Tables, tm)
	}
	return m
}

func findingsModel(fs []runstate.StatsFinding) []model.StatsFinding {
	out := make([]model.StatsFinding, 0, len(fs))
	for _, f := range fs {
		out = append(out, model.StatsFinding{
			Code: f.Code, Count: float64(f.Count), Objects: append([]string{}, f.Objects...), Truncated: f.Truncated,
		})
	}
	return out
}
