package restore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
)

// The test-restore API behind GraphQL (task 4.6): runTestRestoreNow
// (test_restore.run: staff), testRestores / testRestore (test_restore.view:
// staff and the job's owners). Every operation authorizes with the job
// loaded from the database; an unknown test restore answers like one of
// another job for backup owners (FORBIDDEN).

// TestRestoreAPIOptions configure NewTestRestoreAPI.
type TestRestoreAPIOptions struct {
	Service *Service
	Policy  *authz.Policy
	// Queue inserts the test_restore job (resolved late).
	Queue  func() Inserter
	Logger *slog.Logger
}

// TestRestoreAPI is the test-restore API.
type TestRestoreAPI struct {
	o TestRestoreAPIOptions
}

// NewTestRestoreAPI validates o.
func NewTestRestoreAPI(o TestRestoreAPIOptions) (*TestRestoreAPI, error) {
	if o.Service == nil || o.Policy == nil || o.Queue == nil {
		return nil, errors.New("restore: the test-restore API needs the service, the policy and the queue")
	}
	if o.Logger == nil {
		o.Logger = o.Service.o.Logger
	}
	return &TestRestoreAPI{o: o}, nil
}

// TestRestoreView is a test restore with what the API shows about it.
type TestRestoreView struct {
	sqlc.TestRestore
	JobName          string
	JobSlug          string
	JobDeleted       bool
	TargetName       string
	TargetEngine     engine.Kind
	TargetTemporary  bool
	TargetDeleted    bool
	RequestedByName  string
	RequestedByEmail string
	// Items are loaded by Get only.
	Items []TestRestoreItemView
}

// Finished reports a terminal status.
func (v *TestRestoreView) Finished() bool {
	return v.Status != TRStatusQueued && v.Status != TRStatusRunning
}

// TestRestoreItemView is one database of a test restore with its parsed
// diff.
type TestRestoreItemView struct {
	sqlc.TestRestoreItem
	Diff Diff
}

func testViewOf(r *sqlc.GetTestRestoreViewRow) TestRestoreView {
	return TestRestoreView{
		TestRestore: r.TestRestore, JobName: r.JobName, JobSlug: r.JobSlug, JobDeleted: r.JobDeletedAt != nil,
		TargetName: r.TargetName, TargetEngine: engine.Kind(r.TargetEngine), TargetTemporary: r.TargetTemporary,
		TargetDeleted: r.TargetDeletedAt != nil, RequestedByName: r.RequestedByName, RequestedByEmail: r.RequestedByEmail,
	}
}

// RunNow queues a test restore of a job now (test_restore.run). The job
// needs a scratch target; ErrTestRestoreActive when one is queued or
// running. Audited as test_restore.requested (user actor).
func (a *TestRestoreAPI) RunNow(ctx context.Context, jobID uuid.UUID) (TestRestoreView, error) {
	if err := a.o.Policy.Authorize(ctx, authz.OpTestRestoreRun, authz.Job{ID: jobID}); err != nil {
		return TestRestoreView{}, err
	}
	pr, ok := auth.FromContext(ctx)
	if !ok {
		return TestRestoreView{}, a.o.Policy.Authenticated(ctx)
	}
	ins := a.o.Queue()
	if ins == nil {
		return TestRestoreView{}, errors.New("restore: no queue configured")
	}
	var id uuid.UUID
	err := db.TxWith(ctx, a.o.Service.o.DB, func(tx pgx.Tx, q *sqlc.Queries) error {
		job, err := q.GetJobForShare(ctx, jobID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("restore: load job: %w", err)
		}
		if job.TestRestoreConnectionID == nil {
			return ErrNoTestTarget
		}
		by := pr.UserID
		ev := &audit.Event{Actor: audit.UserActor(pr.UserID), IP: auth.RequestMetaFrom(ctx).IP}
		row, err := a.o.Service.enqueueTestRestoreTx(ctx, tx, q, ins, jobID, *job.TestRestoreConnectionID, TestTriggerManual, &by, nil, ev)
		if err != nil {
			return err
		}
		id = row.ID
		return nil
	})
	if err != nil {
		return TestRestoreView{}, err
	}
	return a.load(ctx, id, false)
}

// List returns a job's test restores, newest first (test_restore.view).
func (a *TestRestoreAPI) List(ctx context.Context, jobID uuid.UUID, after *Cursor, limit int) ([]TestRestoreView, bool, error) {
	if err := a.o.Policy.Authorize(ctx, authz.OpTestRestoreView, authz.Job{ID: jobID}); err != nil {
		return nil, false, err
	}
	limit = min(max(limit, 1), MaxPage)
	p := sqlc.ListTestRestoreViewsParams{JobID: jobID, MaxRows: int32(limit + 1)}
	if after != nil {
		at, id := after.CreatedAt, after.ID
		p.AfterCreated, p.AfterID = &at, &id
	}
	rows, err := sqlc.New(a.o.Service.o.DB).ListTestRestoreViews(ctx, p)
	if err != nil {
		return nil, false, fmt.Errorf("restore: list test restores: %w", err)
	}
	out := make([]TestRestoreView, 0, min(len(rows), limit))
	for i := range rows {
		if i == limit {
			return out, true, nil
		}
		r := sqlc.GetTestRestoreViewRow(rows[i])
		out = append(out, testViewOf(&r))
	}
	return out, false, nil
}

// Get returns one test restore with its databases (test_restore.view).
func (a *TestRestoreAPI) Get(ctx context.Context, id uuid.UUID) (TestRestoreView, error) {
	return a.load(ctx, id, true)
}

func (a *TestRestoreAPI) load(ctx context.Context, id uuid.UUID, items bool) (TestRestoreView, error) {
	q := sqlc.New(a.o.Service.o.DB)
	row, err := q.GetTestRestoreView(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		if aerr := a.o.Policy.Authorize(ctx, authz.OpTestRestoreView, authz.Job{}); aerr != nil {
			return TestRestoreView{}, aerr
		}
		return TestRestoreView{}, ErrNotFound
	}
	if err != nil {
		return TestRestoreView{}, fmt.Errorf("restore: load test restore: %w", err)
	}
	if err := a.o.Policy.Authorize(ctx, authz.OpTestRestoreView, authz.Job{ID: row.TestRestore.JobID}); err != nil {
		return TestRestoreView{}, err
	}
	v := testViewOf(&row)
	if !items {
		v.Items = []TestRestoreItemView{}
		return v, nil
	}
	its, err := q.ListTestRestoreItems(ctx, id)
	if err != nil {
		return TestRestoreView{}, fmt.Errorf("restore: load test restore items: %w", err)
	}
	v.Items = make([]TestRestoreItemView, 0, len(its))
	for i := range its {
		d, err := ParseDiff(its[i].Diff)
		if err != nil {
			a.o.Logger.WarnContext(ctx, "a stored test-restore diff is unreadable", slog.String("item_id", its[i].ID.String()))
		}
		v.Items = append(v.Items, TestRestoreItemView{TestRestoreItem: its[i], Diff: d})
	}
	return v, nil
}
