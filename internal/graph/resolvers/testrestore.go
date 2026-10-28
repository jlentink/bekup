package resolvers

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/restore"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// Test-restore resolvers (task 4.6). The settings go through
// jobs.Service.SetTestRestore (job.test_restore_edit, fresh second factor),
// the test restores through restore.TestRestoreAPI (authorized with the
// job loaded from the database); the resolvers translate only.

var errTestRestoresNotConfigured = errors.New("resolvers: test-restore API not configured")

const testRestoreCursorPrefix = "testrestore:"

func (r *Resolver) testRestoreAPI() (*restore.TestRestoreAPI, error) {
	if r.TestRestores == nil {
		return nil, errTestRestoresNotConfigured
	}
	return r.TestRestores, nil
}

// testRestoreError maps the test-restore API's errors.
func testRestoreError(err error) error {
	var fe *serviceutil.FieldError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &fe):
		return apierror.Invalid(fe.Field, fe.Message)
	case errors.Is(err, restore.ErrNotFound):
		return apierror.New(apierror.NotFound, "not found")
	case errors.Is(err, restore.ErrTestRestoreActive):
		return apierror.New(apierror.Conflict, "a test restore of this job is already queued or running")
	case errors.Is(err, restore.ErrNoTestTarget):
		return apierror.New(apierror.Conflict, "the job has no test-restore target; set one with setJobTestRestore first")
	default:
		return err
	}
}

// setTestRestoreError maps setJobTestRestore's errors: the D-068 admin
// rule, the scratch target's probe (connection errors), then the job
// errors.
func setTestRestoreError(err error) error {
	var (
		nr *connections.NotReadyError
		rl *connections.RateLimitedError
		te *connections.TargetError
		pm *connections.PinMismatchError
		hm *connections.HostKeyMismatchError
	)
	switch {
	case errors.Is(err, jobs.ErrPrivilegedTargetAdminOnly):
		return apierror.New(apierror.Forbidden,
			"the scratch connection's role is privileged or its privileges are unknown: only an admin can designate it")
	case errors.As(err, &nr), errors.As(err, &rl), errors.As(err, &te), errors.As(err, &pm), errors.As(err, &hm),
		errors.Is(err, connections.ErrRunnerUnavailable):
		return connectionError(err)
	default:
		return jobError(err)
	}
}

func testRestoreJobModel(t *jobs.TestRestoreSettings) *model.JobTestRestore {
	m := &model.JobTestRestore{
		Enabled: t.Enabled, Schedule: scheduleModel(t.Schedule), ScheduleDescription: t.ScheduleDescription, NextAt: t.NextAt,
		PrivilegedTargetAcknowledgedAt: t.PrivilegedAckAt,
	}
	if t.TargetPrivileges != nil {
		m.TargetPrivileges = targetPrivilegesModel(*t.TargetPrivileges)
	}
	if c := t.Target; c != nil {
		m.Target = &model.JobConnectionRef{ID: c.ID, Name: c.Name, Engine: engineModel(c.Engine), VersionKey: c.VersionKey, Deleted: c.Deleted}
	}
	return m
}

func testStatusModel(s string) model.TestRestoreStatus {
	return model.TestRestoreStatus(strings.ToUpper(s))
}

func intOrZero[T ~int32 | ~int](n T) int { return int(n) }

func testRestoreModel(v *restore.TestRestoreView, showActor bool) *model.TestRestore {
	running := v.Status == restore.TRStatusRunning
	m := &model.TestRestore{
		ID:  v.ID,
		Job: &model.RunJobRef{ID: v.JobID, Name: v.JobName, Slug: v.JobSlug, Deleted: v.JobDeleted},
		Target: &model.RestoreTargetRef{
			ID: v.TargetConnectionID, Name: v.TargetName, Engine: engineModel(v.TargetEngine),
			Temporary: v.TargetTemporary, Deleted: v.TargetDeleted,
		},
		RunID: v.RunID, Trigger: model.TestRestoreTrigger(strings.ToUpper(string(v.Trigger))),
		Status: testStatusModel(string(v.Status)), ScheduledFor: v.ScheduledFor, CreatedAt: v.CreatedAt,
		StartedAt: v.StartedAt, FinishedAt: v.FinishedAt, DurationSeconds: durationOf(v.StartedAt, v.FinishedAt, running),
		ErrorCode: v.ErrorCode, ErrorMessage: sanitizedOrNil(v.ErrorMessage),
		DatabaseCount: intOrZero(v.Databases), Passed: intOrZero(v.Passed), Warned: intOrZero(v.Warned), Failed: intOrZero(v.Failed),
		Databases: make([]model.TestRestoreDatabase, 0, len(v.Items)),
	}
	if showActor && v.RequestedBy != nil {
		m.RequestedBy = &model.RunActor{UserID: *v.RequestedBy, DisplayName: v.RequestedByName, Email: v.RequestedByEmail}
	}
	for i := range v.Items {
		m.Databases = append(m.Databases, testRestoreDatabaseModel(&v.Items[i]))
	}
	return m
}

func testRestoreDatabaseModel(it *restore.TestRestoreItemView) model.TestRestoreDatabase {
	d := model.TestRestoreDatabase{
		ID: it.ID, RunItemID: it.RunItemID, Database: it.DbName, RestoreID: it.RestoreID,
		Status: testStatusModel(string(it.Status)), ErrorCode: it.ErrorCode, ScratchDropped: it.ScratchDropped,
		Findings: []model.TestRestoreFinding{}, StartedAt: it.StartedAt, FinishedAt: it.FinishedAt,
		RestoreStatus: strOrNil(it.Diff.RestoreStatus), RestoreErrorCode: strOrNil(it.Diff.RestoreErrorCode),
	}
	if it.ErrorCode != nil {
		msg := restore.TestErrorMessage(*it.ErrorCode)
		d.ErrorMessage = &msg
	}
	if c := it.Diff.Comparison; c != nil {
		d.FindingsTotal, d.TablesCompared, d.ExactTables = c.FindingsTotal, c.TablesCompared, c.ExactTables
		for _, f := range c.Findings {
			d.Findings = append(d.Findings, model.TestRestoreFinding{
				Severity: strings.ToUpper(f.Severity), Code: f.Code, Object: strOrNil(f.Object),
				Source: floatPtr(f.Source), Restored: floatPtr(f.Restored), Delta: floatPtr(f.Delta),
			})
		}
	}
	return d
}

func floatPtr(n *int64) *float64 {
	if n == nil {
		return nil
	}
	f := float64(*n)
	return &f
}

func (r *Resolver) setJobTestRestore(ctx context.Context, jobID uuid.UUID, in model.JobTestRestoreInput) (*model.Job, error) {
	svc, actor, err := r.jobCaller(ctx)
	if err != nil {
		return nil, err
	}
	ti := jobs.TestRestoreInput{Enabled: in.Enabled, TargetConnectionID: in.TargetConnectionID}
	if in.AcknowledgePrivilegedTarget != nil {
		ti.AcknowledgePrivilegedTarget = *in.AcknowledgePrivilegedTarget
	}
	if in.Schedule != nil {
		s, err := scheduleFromModel(in.Schedule)
		if err != nil {
			return nil, err
		}
		ti.Schedule = &s
	}
	j, err := svc.SetTestRestore(ctx, actor, jobID, ti)
	if err != nil {
		return nil, setTestRestoreError(err)
	}
	return jobModel(&j), nil
}

func (r *Resolver) runTestRestoreNow(ctx context.Context, jobID uuid.UUID) (*model.TestRestore, error) {
	api, err := r.testRestoreAPI()
	if err != nil {
		return nil, err
	}
	v, err := api.RunNow(ctx, jobID)
	if err != nil {
		return nil, testRestoreError(err)
	}
	return testRestoreModel(&v, r.showActor(ctx)), nil
}

func (r *Resolver) testRestores(ctx context.Context, jobID uuid.UUID, first *int, after *string) (*model.TestRestoreConnection, error) {
	api, err := r.testRestoreAPI()
	if err != nil {
		return nil, err
	}
	limit, err := runPageSize(first, defaultRunPage, restore.MaxPage)
	if err != nil {
		return nil, err
	}
	at, id, ok, err := decodeTimeCursor(testRestoreCursorPrefix, after)
	if err != nil {
		return nil, err
	}
	var cur *restore.Cursor
	if ok {
		cur = &restore.Cursor{CreatedAt: at, ID: id}
	}
	views, more, err := api.List(ctx, jobID, cur, limit)
	if err != nil {
		return nil, testRestoreError(err)
	}
	show := r.showActor(ctx)
	conn := &model.TestRestoreConnection{Edges: make([]model.TestRestoreEdge, 0, len(views)), PageInfo: &model.PageInfo{HasNextPage: more, HasPreviousPage: cur != nil}}
	for i := range views {
		v := &views[i]
		conn.Edges = append(conn.Edges, model.TestRestoreEdge{Cursor: encodeTimeCursor(testRestoreCursorPrefix, v.CreatedAt, v.ID), Node: testRestoreModel(v, show)})
	}
	if n := len(conn.Edges); n > 0 {
		start, end := conn.Edges[0].Cursor, conn.Edges[n-1].Cursor
		conn.PageInfo.StartCursor, conn.PageInfo.EndCursor = &start, &end
	}
	return conn, nil
}

func (r *Resolver) testRestore(ctx context.Context, id uuid.UUID) (*model.TestRestore, error) {
	api, err := r.testRestoreAPI()
	if err != nil {
		return nil, err
	}
	v, err := api.Get(ctx, id)
	if errors.Is(err, restore.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, testRestoreError(err)
	}
	return testRestoreModel(&v, r.showActor(ctx)), nil
}
