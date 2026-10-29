package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/scheduler"
)

// Errors of the service. Presentable messages live in the API layer.
var (
	ErrNotFound  = errors.New("jobs: job not found")
	ErrNameTaken = errors.New("jobs: another job already uses this name")
	ErrSlugTaken = errors.New("jobs: another job already uses this slug")
	// ErrRunActive: the job already has a queued, running or retrying run
	// (River's unique insert refused a second one).
	ErrRunActive = errors.New("jobs: a run of this job is already active")
	// ErrDisabled: RunNow on a disabled job.
	ErrDisabled = errors.New("jobs: the job is disabled")
	// ErrNotOwner: RemoveOwner for a user who is not an owner.
	ErrNotOwner = errors.New("jobs: the user is not an owner of the job")
	// ErrActorMismatch: the actor is not the authenticated caller.
	ErrActorMismatch = errors.New("jobs: the actor is not the authenticated caller")
	// ErrOwnerFilterForbidden: List with an owner filter for a caller
	// limited to their own jobs (the filter is for admins and maintainers).
	ErrOwnerFilterForbidden = errors.New("jobs: the owner filter needs access to every job")
)

// AuditTargetType is the audit target type of job entries (target id =
// job id; the same as internal/jobkeys).
const AuditTargetType = jobkeys.AuditTargetType

// DB is what the service needs from the app database (*pgxpool.Pool).
type DB interface {
	db.TxBeginner
	sqlc.DBTX
}

// Authorizer is the central policy (*authz.Policy).
type Authorizer interface {
	Authorize(ctx context.Context, op authz.Operation, res ...authz.Resource) error
	AuthorizeOwnerAssignment(ctx context.Context, owners []uuid.UUID) error
}

// Inserter enqueues River jobs (*queue.Client).
type Inserter interface {
	InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, o queue.InsertOptions) (queue.InsertResult, error)
}

// Canceler cancels River jobs (*queue.Client).
type Canceler interface {
	CancelTx(ctx context.Context, tx pgx.Tx, jobID int64) (rivertype.JobState, error)
	// CancelPendingTx cancels the job only while it waits to be worked
	// (available, scheduled, pending, retryable).
	CancelPendingTx(ctx context.Context, tx pgx.Tx, jobID int64) (rivertype.JobState, bool, error)
}

// KeyInitializer creates the encryption configuration of a new job in its
// transaction (*jobkeys.Service).
type KeyInitializer interface {
	InitTx(ctx context.Context, q *sqlc.Queries, actor jobkeys.Actor, job uuid.UUID, in jobkeys.Init) (*jobkeys.InitResult, error)
}

// Options configure New.
type Options struct {
	DB      DB
	Auditor *audit.Auditor
	// Authorizer double-checks every change against the caller of the
	// context (the GraphQL layer checks first).
	Authorizer Authorizer
	// Keys initializes the encryption of new jobs (D-142).
	Keys KeyInitializer
	// Queue enqueues manual runs (nil: RunNow fails).
	Queue Inserter
	// Canceler cancels the River job of a canceled run (nil: CancelRun
	// fails).
	Canceler Canceler
	// Publisher sends run events (nil: NOTIFY only).
	Publisher *pubsub.Publisher
	// Limits is the schedule policy (scheduler.min_interval).
	Limits scheduler.Limits
	// MaxTimeout is workers.backup_timeout, the cap of a job's timeout.
	MaxTimeout time.Duration
	// TestTargets probes a scratch target's role when it is designated
	// (D-170; nil: every target is unknown, so only admins with the
	// acknowledgement can designate one).
	TestTargets TestTargetProber
	// Alerts receives security events (owner changes); nil logs them.
	Alerts AlertFunc
	// Now overrides the clock (tests).
	Now func() time.Time
	// Logger receives operational events.
	Logger *slog.Logger
}

// Actor is the user performing a change. It must be the authenticated
// caller of the context.
type Actor struct {
	UserID uuid.UUID
	IP     netip.Addr
}

func (a Actor) keys() jobkeys.Actor { return jobkeys.Actor{UserID: a.UserID, IP: a.IP} }

// Service manages backup jobs; see the package documentation.
type Service struct {
	o Options
}

// New validates o and returns the service.
func New(o Options) (*Service, error) {
	if o.DB == nil || o.Auditor == nil || o.Authorizer == nil || o.Keys == nil {
		return nil, errors.New("jobs: DB, Auditor, Authorizer and Keys are required")
	}
	if o.Limits.MinInterval <= 0 {
		o.Limits = scheduler.DefaultLimits()
	}
	if o.MaxTimeout <= 0 || o.MaxTimeout > HardMaxTimeout {
		o.MaxTimeout = min(max(o.MaxTimeout, 0), HardMaxTimeout)
		if o.MaxTimeout == 0 {
			o.MaxTimeout = 6 * time.Hour
		}
	}
	if o.MaxTimeout < MinTimeout {
		return nil, fmt.Errorf("jobs: the maximum timeout must be at least %s", MinTimeout)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Alerts == nil {
		o.Alerts = logAlert(o.Logger)
	}
	return &Service{o: o}, nil
}

// authorize checks op for the caller of ctx and that actor is that caller.
func (s *Service) authorize(ctx context.Context, actor Actor, op authz.Operation, res ...authz.Resource) error {
	if err := s.o.Authorizer.Authorize(ctx, op, res...); err != nil {
		return err
	}
	return checkActor(ctx, actor)
}

func checkActor(ctx context.Context, actor Actor) error {
	pr, ok := auth.FromContext(ctx)
	if !ok || actor.UserID == uuid.Nil || pr.UserID != actor.UserID {
		return ErrActorMismatch
	}
	return nil
}

// --- reading ---------------------------------------------------------------

// view converts a row plus its references.
func (s *Service) view(ctx context.Context, q *sqlc.Queries, row *sqlc.Job) (Job, error) {
	sched, err := scheduler.Parse(row.Schedule, scheduler.Limits{MinInterval: scheduler.AbsoluteMinInterval})
	var desc string
	if err != nil {
		// A stored schedule that no longer compiles (the scheduler marks it
		// unschedulable); keep the job readable.
		desc = "Invalid schedule"
		sched = scheduler.Schedule{}
	} else {
		desc = scheduler.Describe(sched)
	}
	j := Job{
		ID: row.ID, Name: row.Name, Slug: row.Slug, Description: row.Description,
		DBMode: DBMode(row.DbMode), Databases: nonNil(row.Databases), Exclude: nonNil(row.Exclude),
		IncludeGlobals: row.IncludeGlobals, Schedule: sched, ScheduleDescription: desc,
		Enabled: row.Enabled, NextRunAt: row.NextRunAt, LastScheduledAt: row.LastScheduledAt,
		Retention:    Retention{KeepLast: intPtr(row.RetentionKeepLast), KeepDays: intPtr(row.RetentionKeepDays)},
		Verification: Verification(row.Verification),
		CreatedBy:    row.CreatedBy, UpdatedBy: row.UpdatedBy, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if row.TimeoutSeconds != nil {
		j.Timeout = time.Duration(*row.TimeoutSeconds) * time.Second
	}
	if row.ExpectedWindowSeconds != nil {
		j.ExpectedWindow = time.Duration(*row.ExpectedWindowSeconds) * time.Second
	}
	c, err := q.GetJobConnectionInfo(ctx, row.ConnectionID)
	if err != nil {
		return Job{}, fmt.Errorf("jobs: load connection of %s: %w", row.ID, err)
	}
	j.Connection = ConnectionInfo{ID: c.ID, Name: c.Name, Engine: engine.Kind(c.Engine), VersionKey: c.VersionKey, Deleted: c.DeletedAt != nil}
	dests, err := q.ListJobDestinations(ctx, row.ID)
	if err != nil {
		return Job{}, fmt.Errorf("jobs: load destinations of %s: %w", row.ID, err)
	}
	j.Destinations = make([]DestinationRef, 0, len(dests))
	for _, d := range dests {
		j.Destinations = append(j.Destinations, DestinationRef{
			ID: d.DestinationID, Name: d.Name, Type: string(d.Type), Position: int(d.Position), Deleted: d.DeletedAt != nil,
		})
	}
	if j.TestRestore, err = testRestoreView(ctx, q, row); err != nil {
		return Job{}, err
	}
	return j, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func intPtr(v *int32) *int {
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}

func int32Ptr(v *int) *int32 {
	if v == nil {
		return nil
	}
	n := int32(*v) //nolint:gosec // G115: validated ranges (≤ 36500)
	return &n
}

func secondsPtr(d time.Duration) *int32 {
	if d <= 0 {
		return nil
	}
	n := int32(d / time.Second) //nolint:gosec // G115: validated ≤ 366 days
	return &n
}

// Get returns a job that is not deleted (ErrNotFound otherwise). The
// caller authorizes job.view with the job's id.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Job, error) {
	q := sqlc.New(s.o.DB)
	row, err := q.GetJob(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("jobs: load: %w", err)
	}
	return s.view(ctx, q, &row)
}

// List returns one page of the jobs the caller may see: every job for
// admins and maintainers, the owned ones for users (authz.JobScope, checked
// here). limit is 1..200.
func (s *Service) List(ctx context.Context, scope authz.Scope, userID uuid.UUID, f ListFilter, after *Cursor, limit int) (Page, error) {
	if limit < 1 || limit > 200 {
		return Page{}, fieldErr("first", "must be between 1 and 200")
	}
	p := sqlc.ListJobsParams{UserID: userID, OwnerID: f.OwnerID, Enabled: f.Enabled, ConnectionID: f.ConnectionID, MaxRows: int32(limit + 1)}
	switch scope {
	case authz.ScopeAll:
		p.ScopeAll = true
	case authz.ScopeOwned:
		if userID == uuid.Nil {
			return Page{}, ErrActorMismatch
		}
		if f.OwnerID != nil {
			return Page{}, ErrOwnerFilterForbidden
		}
	default:
		return Page{}, ErrActorMismatch
	}
	if f.Search != "" {
		search, err := cleanText("search", f.Search, 1, MaxNameRunes, false)
		if err != nil {
			return Page{}, err
		}
		p.Search = &search
	}
	if after != nil {
		name, id := after.Name, after.ID
		p.AfterName, p.AfterID = &name, &id
	}
	q := sqlc.New(s.o.DB)
	rows, err := q.ListJobs(ctx, p)
	if err != nil {
		return Page{}, fmt.Errorf("jobs: list: %w", err)
	}
	page := Page{More: len(rows) > limit}
	if page.More {
		rows = rows[:limit]
	}
	page.Jobs = make([]Job, 0, len(rows))
	for i := range rows {
		j, err := s.view(ctx, q, &rows[i])
		if err != nil {
			return Page{}, err
		}
		page.Jobs = append(page.Jobs, j)
	}
	return page, nil
}

// Owners lists the owners of a job (not deleted users). The caller
// authorizes viewing owners (admins and maintainers only).
func (s *Service) Owners(ctx context.Context, id uuid.UUID) ([]Owner, error) {
	rows, err := sqlc.New(s.o.DB).ListJobOwners(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("jobs: list owners: %w", err)
	}
	out := make([]Owner, 0, len(rows))
	for _, r := range rows {
		out = append(out, Owner{UserID: r.UserID, Email: r.Email, DisplayName: r.DisplayName, Role: string(r.Role), Disabled: r.Disabled, AddedAt: r.CreatedAt})
	}
	return out, nil
}

// --- schedules -------------------------------------------------------------

// PreviewSchedule validates a schedule like Create would and returns its
// next n occurrences after now (1..scheduler.MaxPreview; 0 = DefaultPreview)
// without storing anything.
func (s *Service) PreviewSchedule(sched scheduler.Schedule, n int) (Preview, error) {
	if n == 0 {
		n = DefaultPreview
	}
	if n < 1 || n > scheduler.MaxPreview {
		return Preview{}, fieldErr("count", "must be between 1 and %d", scheduler.MaxPreview)
	}
	canon, _, err := checkSchedule(sched, s.o.Limits)
	if err != nil {
		return Preview{}, err
	}
	p := Preview{Schedule: canon, Description: scheduler.Describe(canon), Next: []time.Time{}}
	if canon.Kind == scheduler.KindManual {
		return p, nil
	}
	next, err := scheduler.NextN(canon, s.o.Now(), n)
	if err != nil {
		return Preview{}, scheduleErr(err)
	}
	p.Next = next
	return p, nil
}

// nextRun is the next_run_at of an (enabled) job: nil for manual schedules
// and disabled jobs, else the first occurrence after now.
func (s *Service) nextRun(sched scheduler.Schedule, enabled bool) (*time.Time, error) {
	if !enabled || sched.Kind == scheduler.KindManual {
		return nil, nil
	}
	t, err := scheduler.Next(sched, s.o.Now())
	if err != nil {
		return nil, scheduleErr(err)
	}
	t = t.UTC()
	return &t, nil
}

// --- saving ----------------------------------------------------------------

// lockConnection checks (and share-locks) the connection of a job.
func lockConnection(ctx context.Context, q *sqlc.Queries, id uuid.UUID) (sqlc.LockConnectionForJobRow, error) {
	c, err := q.LockConnectionForJob(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && c.DeletedAt != nil) {
		return c, fieldErr("connectionId", "the connection does not exist")
	}
	if err != nil {
		return c, fmt.Errorf("jobs: load connection: %w", err)
	}
	if c.Temporary {
		return c, fieldErr("connectionId", "temporary restore connections cannot be backed up by a job")
	}
	return c, nil
}

// lockDestinations checks (and share-locks) the destinations of a job.
func lockDestinations(ctx context.Context, q *sqlc.Queries, ids []uuid.UUID) error {
	rows, err := q.LockDestinationsForJob(ctx, ids)
	if err != nil {
		return fmt.Errorf("jobs: load destinations: %w", err)
	}
	live := 0
	for _, r := range rows {
		if r.DeletedAt == nil {
			live++
		}
	}
	if live != len(ids) {
		return fieldErr("destinationIds", "a destination does not exist")
	}
	return nil
}

func replaceDestinations(ctx context.Context, q *sqlc.Queries, job uuid.UUID, ids []uuid.UUID) error {
	if err := q.DeleteJobDestinations(ctx, job); err != nil {
		return fmt.Errorf("jobs: replace destinations: %w", err)
	}
	for i, d := range ids {
		if err := q.InsertJobDestination(ctx, sqlc.InsertJobDestinationParams{JobID: job, DestinationID: d, Position: int32(i)}); err != nil {
			return fmt.Errorf("jobs: insert destination: %w", err)
		}
	}
	return nil
}

// checkGlobals applies the engine rule of include_globals.
func checkGlobals(eng engine.Kind, include bool) error {
	if include && eng != engine.KindPostgres {
		return fieldErr("includeGlobals", "only PostgreSQL has cluster globals")
	}
	return nil
}

// uniqueErr maps a unique violation of the jobs table.
func uniqueErr(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		switch pe.ConstraintName {
		case "jobs_name_active_idx":
			return ErrNameTaken
		case "jobs_slug_idx":
			return ErrSlugTaken
		}
	}
	return nil
}

// pickSlug returns the requested slug (ErrSlugTaken when used) or one
// derived from name with a numeric suffix when needed.
func pickSlug(ctx context.Context, q *sqlc.Queries, requested, name string) (string, error) {
	if requested != "" {
		taken, err := q.JobSlugTaken(ctx, requested)
		if err != nil {
			return "", fmt.Errorf("jobs: check slug: %w", err)
		}
		if taken {
			return "", ErrSlugTaken
		}
		return requested, nil
	}
	base := slugFromName(name)
	for i := 1; i <= 100; i++ {
		cand := base
		if i > 1 {
			cand = withSuffix(base, i)
		}
		taken, err := q.JobSlugTaken(ctx, cand)
		if err != nil {
			return "", fmt.Errorf("jobs: check slug: %w", err)
		}
		if !taken {
			return cand, nil
		}
	}
	return withSuffix(base, int(uuid.New().ID()%1000000)), nil
}

// Create validates and stores a new job together with its encryption
// configuration (jobkeys.InitTx, encryption on). The caller must Destroy
// the result (it may hold a generated passphrase or identity).
func (s *Service) Create(ctx context.Context, actor Actor, in CreateInput) (*Created, error) {
	defer in.Encryption.Passphrase.Destroy()
	if err := s.authorize(ctx, actor, authz.OpJobCreate); err != nil {
		return nil, err
	}
	name, err := normalizeName(in.Name)
	if err != nil {
		return nil, err
	}
	desc, err := normalizeDescription(in.Description)
	if err != nil {
		return nil, err
	}
	slug := ""
	if in.Slug != "" {
		if slug, err = checkSlug(in.Slug); err != nil {
			return nil, err
		}
	}
	if in.ConnectionID == uuid.Nil {
		return nil, fieldErr("connectionId", "select a connection")
	}
	if in.DBMode == "" {
		in.DBMode = DBModeAll
	}
	sel, err := checkSelection(in.DBMode, in.Databases, in.Exclude)
	if err != nil {
		return nil, err
	}
	sched, schedJSON, err := checkSchedule(in.Schedule, s.o.Limits)
	if err != nil {
		return nil, err
	}
	if err := checkRetention(in.Retention); err != nil {
		return nil, err
	}
	var timeout, window time.Duration
	if in.Timeout != nil {
		if err := checkTimeout(*in.Timeout, s.o.MaxTimeout); err != nil {
			return nil, err
		}
		timeout = *in.Timeout
	}
	if in.ExpectedWindow != nil {
		if err := checkExpectedWindow(*in.ExpectedWindow); err != nil {
			return nil, err
		}
		window = *in.ExpectedWindow
	}
	if in.Verification == "" {
		in.Verification = VerificationStandard
	}
	if !in.Verification.Valid() {
		return nil, fieldErr("verification", "must be STANDARD or DEEP")
	}
	if err := checkDestinations(in.DestinationIDs); err != nil {
		return nil, err
	}
	enabled := in.Enabled == nil || *in.Enabled
	next, err := s.nextRun(sched, enabled)
	if err != nil {
		return nil, err
	}
	id := uuid.New()
	enc := jobkeys.Init{Mode: in.Encryption.Mode, Passphrase: in.Encryption.Passphrase, Recipient: in.Encryption.Recipient, Label: in.Encryption.Label}
	out := &Created{}
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		conn, err := lockConnection(ctx, q, in.ConnectionID)
		if err != nil {
			return err
		}
		eng := engine.Kind(conn.Engine)
		globals := eng == engine.KindPostgres
		if in.IncludeGlobals != nil {
			globals = *in.IncludeGlobals
		}
		if err := checkGlobals(eng, globals); err != nil {
			return err
		}
		if err := lockDestinations(ctx, q, in.DestinationIDs); err != nil {
			return err
		}
		finalSlug, err := pickSlug(ctx, q, slug, name)
		if err != nil {
			return err
		}
		row, err := q.InsertJob(ctx, sqlc.InsertJobParams{
			ID: id, Name: name, Slug: finalSlug, Description: desc, ConnectionID: in.ConnectionID,
			DbMode: sqlc.JobDbMode(sel.mode), Databases: sel.databases, Exclude: sel.exclude, IncludeGlobals: globals,
			RetentionKeepLast: int32Ptr(in.Retention.KeepLast), RetentionKeepDays: int32Ptr(in.Retention.KeepDays),
			TimeoutSeconds: secondsPtr(timeout), Verification: sqlc.JobVerification(in.Verification),
			ExpectedWindowSeconds: secondsPtr(window), Schedule: schedJSON, Enabled: enabled, NextRunAt: next,
			CreatedBy: &actor.UserID,
		})
		if err != nil {
			if u := uniqueErr(err); u != nil {
				return u
			}
			return fmt.Errorf("jobs: insert: %w", err)
		}
		if err := replaceDestinations(ctx, q, id, in.DestinationIDs); err != nil {
			return err
		}
		res, err := s.o.Keys.InitTx(ctx, q, actor.keys(), id, enc)
		if err != nil {
			return err
		}
		out.Encryption = res
		details := audit.Details{
			"name": name, "slug": finalSlug, "connection_id": in.ConnectionID.String(), "db_mode": string(sel.mode),
			"databases": sel.databases, "exclude": sel.exclude, "include_globals": globals,
			"schedule": string(schedJSON), "enabled": enabled, "retention_keep_last": in.Retention.KeepLast,
			"retention_keep_days": in.Retention.KeepDays, "timeout_seconds": secondsPtr(timeout),
			"expected_window_seconds": secondsPtr(window), "verification": string(in.Verification),
			"destination_ids": uuidStrings(in.DestinationIDs), "encryption_mode": string(res.Status.Mode),
		}
		if err := s.record(ctx, q, actor, audit.ActionJobCreated, id, details); err != nil {
			return err
		}
		out.Job, err = s.view(ctx, q, &row)
		return err
	})
	if err != nil {
		out.Destroy()
		return nil, err
	}
	return out, nil
}

func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

// Update validates and applies in. A changed schedule is re-checked
// against scheduler.min_interval and re-planned (next_run_at from now); an
// unchanged schedule keeps its next_run_at. The slug never changes.
func (s *Service) Update(ctx context.Context, actor Actor, id uuid.UUID, in UpdateInput) (Job, error) {
	if err := s.authorize(ctx, actor, authz.OpJobUpdate); err != nil {
		return Job{}, err
	}
	if in.Retention != nil {
		if err := s.authorize(ctx, actor, authz.OpJobRetentionUpdate); err != nil {
			return Job{}, err
		}
	}
	var out Job
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		cur, err := q.GetJobForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("jobs: load: %w", err)
		}
		p, changed, err := s.applyUpdate(ctx, q, &cur, in)
		if err != nil {
			return err
		}
		if len(changed) == 0 {
			out, err = s.view(ctx, q, &cur)
			return err
		}
		p.ID, p.UpdatedBy = id, &actor.UserID
		row, err := q.UpdateJob(ctx, p)
		if err != nil {
			if u := uniqueErr(err); u != nil {
				return u
			}
			return fmt.Errorf("jobs: update: %w", err)
		}
		if _, ok := changed["destination_ids"]; ok {
			if err := replaceDestinations(ctx, q, id, *in.DestinationIDs); err != nil {
				return err
			}
		}
		keys := make([]string, 0, len(changed))
		for k := range changed {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if err := s.record(ctx, q, actor, audit.ActionJobUpdated, id, audit.Details{"name": row.Name, "fields": keys, "changes": changed}); err != nil {
			return err
		}
		out, err = s.view(ctx, q, &row)
		return err
	})
	if err != nil {
		return Job{}, err
	}
	return out, nil
}

// change is an audited old/new pair.
type change struct {
	Old any `json:"old"`
	New any `json:"new"`
}

// applyUpdate computes the next row state of cur and the changed fields.
func (s *Service) applyUpdate(ctx context.Context, q *sqlc.Queries, cur *sqlc.Job, in UpdateInput) (sqlc.UpdateJobParams, map[string]any, error) {
	p := sqlc.UpdateJobParams{
		Name: cur.Name, Description: cur.Description, ConnectionID: cur.ConnectionID, DbMode: cur.DbMode,
		Databases: nonNil(cur.Databases), Exclude: nonNil(cur.Exclude), IncludeGlobals: cur.IncludeGlobals,
		RetentionKeepLast: cur.RetentionKeepLast, RetentionKeepDays: cur.RetentionKeepDays,
		TimeoutSeconds: cur.TimeoutSeconds, Verification: cur.Verification,
		ExpectedWindowSeconds: cur.ExpectedWindowSeconds, Schedule: cur.Schedule, NextRunAt: cur.NextRunAt,
	}
	changed := map[string]any{}
	if in.Name != nil {
		name, err := normalizeName(*in.Name)
		if err != nil {
			return p, nil, err
		}
		if name != p.Name {
			changed["name"] = change{p.Name, name}
			p.Name = name
		}
	}
	if in.Description != nil {
		desc, err := normalizeDescription(*in.Description)
		if err != nil {
			return p, nil, err
		}
		if desc != p.Description {
			changed["description"] = true
			p.Description = desc
		}
	}
	if in.ConnectionID != nil && *in.ConnectionID != p.ConnectionID {
		if *in.ConnectionID == uuid.Nil {
			return p, nil, fieldErr("connectionId", "select a connection")
		}
		changed["connection_id"] = change{p.ConnectionID.String(), in.ConnectionID.String()}
		p.ConnectionID = *in.ConnectionID
	}
	// Database selection: re-validated as a whole when any part changes.
	mode, dbs, ex := DBMode(p.DbMode), p.Databases, p.Exclude
	if in.DBMode != nil {
		mode = *in.DBMode
		if mode == DBModeAll && in.Databases == nil {
			dbs = nil
		}
		if mode == DBModeSelected && in.Exclude == nil {
			ex = nil
		}
	}
	if in.Databases != nil {
		dbs = *in.Databases
	}
	if in.Exclude != nil {
		ex = *in.Exclude
	}
	sel, err := checkSelection(mode, dbs, ex)
	if err != nil {
		return p, nil, err
	}
	if string(sel.mode) != string(p.DbMode) {
		changed["db_mode"] = change{string(p.DbMode), string(sel.mode)}
	}
	if !slices.Equal(sel.databases, p.Databases) {
		changed["databases"] = change{p.Databases, sel.databases}
	}
	if !slices.Equal(sel.exclude, p.Exclude) {
		changed["exclude"] = change{p.Exclude, sel.exclude}
	}
	p.DbMode, p.Databases, p.Exclude = sqlc.JobDbMode(sel.mode), sel.databases, sel.exclude
	if in.IncludeGlobals != nil && *in.IncludeGlobals != p.IncludeGlobals {
		changed["include_globals"] = change{p.IncludeGlobals, *in.IncludeGlobals}
		p.IncludeGlobals = *in.IncludeGlobals
	}
	// The connection is checked (and share-locked) on every update: the
	// engine rule of include_globals depends on it.
	conn, err := lockConnection(ctx, q, p.ConnectionID)
	if err != nil {
		return p, nil, err
	}
	if in.IncludeGlobals != nil || p.ConnectionID != cur.ConnectionID {
		if err := checkGlobals(engine.Kind(conn.Engine), p.IncludeGlobals); err != nil {
			return p, nil, err
		}
	}
	if in.Schedule != nil {
		sched, enc, err := checkSchedule(*in.Schedule, s.o.Limits)
		if err != nil {
			return p, nil, err
		}
		if string(enc) != string(p.Schedule) {
			changed["schedule"] = change{string(p.Schedule), string(enc)}
			p.Schedule = enc
			if p.NextRunAt, err = s.nextRun(sched, cur.Enabled); err != nil {
				return p, nil, err
			}
		}
	}
	if in.Retention != nil {
		if err := checkRetention(*in.Retention); err != nil {
			return p, nil, err
		}
		kl, kd := int32Ptr(in.Retention.KeepLast), int32Ptr(in.Retention.KeepDays)
		if !eqInt32(kl, p.RetentionKeepLast) || !eqInt32(kd, p.RetentionKeepDays) {
			changed["retention"] = change{
				map[string]any{"keep_last": p.RetentionKeepLast, "keep_days": p.RetentionKeepDays},
				map[string]any{"keep_last": kl, "keep_days": kd},
			}
			p.RetentionKeepLast, p.RetentionKeepDays = kl, kd
		}
	}
	switch {
	case in.ClearTimeout && in.Timeout != nil:
		return p, nil, fieldErr("timeoutSeconds", "set a timeout or clear it, not both")
	case in.ClearTimeout:
		if p.TimeoutSeconds != nil {
			changed["timeout_seconds"] = change{p.TimeoutSeconds, nil}
			p.TimeoutSeconds = nil
		}
	case in.Timeout != nil:
		if err := checkTimeout(*in.Timeout, s.o.MaxTimeout); err != nil {
			return p, nil, err
		}
		if t := secondsPtr(*in.Timeout); !eqInt32(t, p.TimeoutSeconds) {
			changed["timeout_seconds"] = change{p.TimeoutSeconds, t}
			p.TimeoutSeconds = t
		}
	}
	switch {
	case in.ClearExpectedWindow && in.ExpectedWindow != nil:
		return p, nil, fieldErr("expectedWindowSeconds", "set a window or clear it, not both")
	case in.ClearExpectedWindow:
		if p.ExpectedWindowSeconds != nil {
			changed["expected_window_seconds"] = change{p.ExpectedWindowSeconds, nil}
			p.ExpectedWindowSeconds = nil
		}
	case in.ExpectedWindow != nil:
		if err := checkExpectedWindow(*in.ExpectedWindow); err != nil {
			return p, nil, err
		}
		if w := secondsPtr(*in.ExpectedWindow); !eqInt32(w, p.ExpectedWindowSeconds) {
			changed["expected_window_seconds"] = change{p.ExpectedWindowSeconds, w}
			p.ExpectedWindowSeconds = w
		}
	}
	if in.Verification != nil {
		if !in.Verification.Valid() {
			return p, nil, fieldErr("verification", "must be STANDARD or DEEP")
		}
		if string(*in.Verification) != string(p.Verification) {
			changed["verification"] = change{string(p.Verification), string(*in.Verification)}
			p.Verification = sqlc.JobVerification(*in.Verification)
		}
	}
	if in.DestinationIDs != nil {
		ids := *in.DestinationIDs
		if err := checkDestinations(ids); err != nil {
			return p, nil, err
		}
		if err := lockDestinations(ctx, q, ids); err != nil {
			return p, nil, err
		}
		curDests, err := q.ListJobDestinations(ctx, cur.ID)
		if err != nil {
			return p, nil, fmt.Errorf("jobs: load destinations: %w", err)
		}
		old := make([]uuid.UUID, len(curDests))
		for i, d := range curDests {
			old[i] = d.DestinationID
		}
		if !slices.Equal(old, ids) {
			changed["destination_ids"] = change{uuidStrings(old), uuidStrings(ids)}
		}
	}
	return p, changed, nil
}

func eqInt32(a, b *int32) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// Delete soft deletes a job. Its schedule stops at once (the scheduler
// ignores deleted jobs); a run already in progress finishes (the backup
// worker of task 3.6 re-checks the job when it starts). Keys, owners and
// destination links stay for the job's artifacts and the audit log.
func (s *Service) Delete(ctx context.Context, actor Actor, id uuid.UUID) error {
	if err := s.authorize(ctx, actor, authz.OpJobDelete); err != nil {
		return err
	}
	return db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		cur, err := q.GetJobForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("jobs: load: %w", err)
		}
		n, err := q.SoftDeleteJob(ctx, sqlc.SoftDeleteJobParams{ID: id, UpdatedBy: &actor.UserID})
		if err != nil {
			return fmt.Errorf("jobs: delete: %w", err)
		}
		if n != 1 {
			return ErrNotFound
		}
		return s.record(ctx, q, actor, audit.ActionJobDeleted, id, audit.Details{"name": cur.Name, "slug": cur.Slug})
	})
}

// SetEnabled enables or disables a job (job.update). Enabling plans the
// next occurrence from now (periods while disabled are not caught up);
// disabling unschedules the job. A no-op changes and records nothing.
func (s *Service) SetEnabled(ctx context.Context, actor Actor, id uuid.UUID, enabled bool) (Job, error) {
	if err := s.authorize(ctx, actor, authz.OpJobUpdate); err != nil {
		return Job{}, err
	}
	var out Job
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		cur, err := q.GetJobForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("jobs: load: %w", err)
		}
		if cur.Enabled == enabled {
			out, err = s.view(ctx, q, &cur)
			return err
		}
		var next *time.Time
		if enabled {
			sched, err := scheduler.Parse(cur.Schedule, scheduler.Limits{MinInterval: scheduler.AbsoluteMinInterval})
			if err != nil {
				return scheduleErr(err)
			}
			if next, err = s.nextRun(sched, true); err != nil {
				return err
			}
		}
		row, err := q.SetJobEnabled(ctx, sqlc.SetJobEnabledParams{ID: id, Enabled: enabled, NextRunAt: next, UpdatedBy: &actor.UserID})
		if err != nil {
			return fmt.Errorf("jobs: set enabled: %w", err)
		}
		action := audit.ActionJobDisabled
		if enabled {
			action = audit.ActionJobEnabled
		}
		if err := s.record(ctx, q, actor, action, id, audit.Details{"name": cur.Name}); err != nil {
			return err
		}
		out, err = s.view(ctx, q, &row)
		return err
	})
	if err != nil {
		return Job{}, err
	}
	return out, nil
}

// --- owners ----------------------------------------------------------------

// AssignOwners adds backup owners to a job (job.owners_assign, step-up; a
// maintainer cannot add themselves, D-067). Users must exist and not be
// deleted; owners already listed are skipped. Every new owner is audited
// and raises a security alert. It returns the job's owners afterwards.
func (s *Service) AssignOwners(ctx context.Context, actor Actor, id uuid.UUID, users []uuid.UUID) ([]Owner, error) {
	if err := s.o.Authorizer.AuthorizeOwnerAssignment(ctx, users); err != nil {
		return nil, err
	}
	if err := checkActor(ctx, actor); err != nil {
		return nil, err
	}
	if len(users) == 0 || len(users) > MaxOwners {
		return nil, fieldErr("users", "list 1-%d users", MaxOwners)
	}
	for i, u := range users {
		if u == uuid.Nil || slices.Contains(users[:i], u) {
			return nil, fieldErr("users", "invalid or repeated user")
		}
	}
	var added []sqlc.GetOwnerCandidatesRow
	var name string
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		job, err := q.GetJobForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("jobs: load: %w", err)
		}
		name = job.Name
		cands, err := q.GetOwnerCandidates(ctx, users)
		if err != nil {
			return fmt.Errorf("jobs: load users: %w", err)
		}
		if len(cands) != len(users) {
			return fieldErr("users", "a user does not exist")
		}
		for _, c := range cands {
			n, err := q.InsertJobOwner(ctx, sqlc.InsertJobOwnerParams{JobID: id, UserID: c.ID, CreatedBy: &actor.UserID})
			if err != nil {
				return fmt.Errorf("jobs: add owner: %w", err)
			}
			if n == 0 {
				continue
			}
			added = append(added, c)
			if err := s.record(ctx, q, actor, audit.ActionJobOwnerAdded, id, audit.Details{
				"name": job.Name, "user_id": c.ID.String(), "role": string(c.Role),
			}); err != nil {
				return err
			}
		}
		count, err := q.CountJobOwners(ctx, id)
		if err != nil {
			return fmt.Errorf("jobs: count owners: %w", err)
		}
		if count > MaxOwners {
			return fieldErr("users", "a job has at most %d owners", MaxOwners)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, c := range added {
		s.o.Alerts(ctx, Alert{Kind: AlertOwnerAdded, JobID: id, ActorID: actor.UserID,
			Details: map[string]any{"job_name": name, "user_id": c.ID.String(), "role": string(c.Role)}})
	}
	return s.Owners(ctx, id)
}

// RemoveOwner removes one backup owner (job.owners_assign, step-up),
// audited and alerted.
func (s *Service) RemoveOwner(ctx context.Context, actor Actor, id, user uuid.UUID) ([]Owner, error) {
	if err := s.o.Authorizer.AuthorizeOwnerAssignment(ctx, nil); err != nil {
		return nil, err
	}
	if err := checkActor(ctx, actor); err != nil {
		return nil, err
	}
	var name string
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		job, err := q.GetJobForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("jobs: load: %w", err)
		}
		name = job.Name
		n, err := q.DeleteJobOwner(ctx, sqlc.DeleteJobOwnerParams{JobID: id, UserID: user})
		if err != nil {
			return fmt.Errorf("jobs: remove owner: %w", err)
		}
		if n == 0 {
			return ErrNotOwner
		}
		return s.record(ctx, q, actor, audit.ActionJobOwnerRemoved, id, audit.Details{"name": job.Name, "user_id": user.String()})
	})
	if err != nil {
		return nil, err
	}
	s.o.Alerts(ctx, Alert{Kind: AlertOwnerRemoved, JobID: id, ActorID: actor.UserID,
		Details: map[string]any{"job_name": name, "user_id": user.String()}})
	return s.Owners(ctx, id)
}

// --- runs ------------------------------------------------------------------

// RunNow enqueues a manual backup run (run.now: admins, maintainers and the
// job's owners). The River unique key (job id) refuses a second active
// run: ErrRunActive. Disabled jobs do not run (ErrDisabled). Audited as
// job.run_requested in the same transaction as the insert.
func (s *Service) RunNow(ctx context.Context, actor Actor, id uuid.UUID) (RunRequest, error) {
	if err := s.authorize(ctx, actor, authz.OpRunNow, authz.Job{ID: id}); err != nil {
		return RunRequest{}, err
	}
	if s.o.Queue == nil {
		return RunRequest{}, errors.New("jobs: no queue configured")
	}
	req := RunRequest{JobID: id, RequestedAt: s.o.Now().UTC()}
	err := db.TxWith(ctx, s.o.DB, func(tx pgx.Tx, q *sqlc.Queries) error {
		job, err := q.GetJobForShare(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("jobs: load: %w", err)
		}
		if !job.Enabled {
			return ErrDisabled
		}
		by := actor.UserID
		res, err := runstate.EnqueueTx(ctx, tx, s.o.Queue, s.o.Publisher, queue.BackupRunArgs{JobID: id, Trigger: queue.TriggerManual, TriggeredBy: &by})
		if err != nil {
			return fmt.Errorf("jobs: enqueue run: %w", err)
		}
		if res.Duplicate {
			return ErrRunActive
		}
		req.QueueJobID, req.RunID = res.JobID, res.RunID
		return s.record(ctx, q, actor, audit.ActionJobRunRequested, id, audit.Details{"name": job.Name, "queue_job_id": res.JobID, "run_id": res.RunID.String()})
	})
	if err != nil {
		return RunRequest{}, err
	}
	return req, nil
}

// StartNextRunEarly starts the job's next planned run now (run.start_early:
// admins, maintainers and the job's owners; task 3.12, D-156). In one
// transaction under the job's row lock it enqueues a backup_run with
// trigger early and scheduled_for = the stored next_run_at, together with
// its queued run row, and advances next_run_at by exactly that one
// occurrence (see earlyNextRun), so the scheduler never runs the original
// slot. Disabled jobs and jobs without a planned run (manual schedule) are
// refused (validation errors), and so is a job whose next_run_at is already
// past the schedule's natural next occurrence: only the genuinely next
// occurrence can be pulled forward, so repeated calls cannot postpone the
// planned backups (D-160). An active run (River's unique key) is
// ErrRunActive and changes nothing. Audited as job.run_started_early.
func (s *Service) StartNextRunEarly(ctx context.Context, actor Actor, id uuid.UUID) (EarlyStart, error) {
	if err := s.authorize(ctx, actor, authz.OpRunStartEarly, authz.Job{ID: id}); err != nil {
		return EarlyStart{}, err
	}
	if s.o.Queue == nil {
		return EarlyStart{}, errors.New("jobs: no queue configured")
	}
	now := s.o.Now().UTC()
	out := EarlyStart{RunRequest: RunRequest{JobID: id, RequestedAt: now}}
	err := db.TxWith(ctx, s.o.DB, func(tx pgx.Tx, q *sqlc.Queries) error {
		// FOR UPDATE: the scheduler locks due jobs with SKIP LOCKED, so it
		// cannot handle the occurrence concurrently, and once next_run_at
		// moved the original slot is no longer due.
		job, err := q.GetJobForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("jobs: load: %w", err)
		}
		if !job.Enabled {
			return fieldErr("id", "the job is disabled; enable it first")
		}
		sched, err := scheduler.Parse(job.Schedule, scheduler.Limits{MinInterval: scheduler.AbsoluteMinInterval})
		if err != nil {
			return scheduleErr(err)
		}
		if sched.Kind == scheduler.KindManual {
			return fieldErr("id", "the job has a manual schedule and no planned run; use runJobNow for an extra run")
		}
		if job.NextRunAt == nil {
			return fieldErr("id", "the job has no planned run")
		}
		original := job.NextRunAt.UTC()
		next, err := earlyNextRun(sched, original, now)
		if errors.Is(err, errAlreadyEarly) {
			return fieldErr("id", "the next planned run was already started early")
		}
		if err != nil {
			return scheduleErr(err)
		}
		by := actor.UserID
		res, err := runstate.EnqueueTx(ctx, tx, s.o.Queue, s.o.Publisher, queue.BackupRunArgs{
			JobID: id, ScheduledFor: original, Trigger: queue.TriggerEarly, TriggeredBy: &by,
		})
		if err != nil {
			return fmt.Errorf("jobs: enqueue run: %w", err)
		}
		if res.Duplicate {
			// Nothing was written: the transaction rolls back anyway.
			return ErrRunActive
		}
		if err := q.AdvanceJobSchedule(ctx, sqlc.AdvanceJobScheduleParams{ID: id, NextRunAt: &next, ScheduledAt: now}); err != nil {
			return fmt.Errorf("jobs: advance next_run_at: %w", err)
		}
		out.QueueJobID, out.RunID = res.JobID, res.RunID
		out.ScheduledFor, out.NextRunAt = original, next
		return s.record(ctx, q, actor, audit.ActionJobRunStartedEarly, id, audit.Details{
			"name": job.Name, "queue_job_id": res.JobID, "run_id": res.RunID.String(),
			"scheduled_for": original.Format(time.RFC3339), "next_run_at": next.Format(time.RFC3339),
		})
	})
	if err != nil {
		return EarlyStart{}, err
	}
	return out, nil
}

// earlyNextRun is next_run_at after the planned occurrence `original` was
// started early: the occurrence after it (Plan.Next, exactly one
// occurrence, D-156). When the job was already overdue (original <= now,
// the scheduler simply had not ticked yet) and that occurrence has passed
// as well, it is the first occurrence after now, as the scheduler collapses
// missed occurrences (D-143, D-157). A schedule without a further
// occurrence is an error: the scheduler would not run it either.
//
// An occurrence later than the schedule's natural next one after now
// (Next(now)) was already pulled forward once: starting it early as well
// would push next_run_at another occurrence ahead and, repeated, pause the
// planned backups. That is errAlreadyEarly, so next_run_at stays at most
// one occurrence ahead of the natural schedule (D-160).
func earlyNextRun(sched scheduler.Schedule, original, now time.Time) (time.Time, error) {
	natural, err := scheduler.Next(sched, now)
	if err != nil {
		return time.Time{}, err
	}
	if original.After(natural) {
		return time.Time{}, errAlreadyEarly
	}
	next, err := scheduler.Next(sched, original)
	if err != nil {
		return time.Time{}, err
	}
	if !next.After(now) {
		next = natural
	}
	return next.UTC(), nil
}

// errAlreadyEarly: the stored next_run_at is already past the schedule's
// natural next occurrence (see earlyNextRun).
var errAlreadyEarly = errors.New("jobs: the next planned run was already started early")

// record writes one audit entry in the action's transaction.
func (s *Service) record(ctx context.Context, q *sqlc.Queries, actor Actor, action audit.Action, id uuid.UUID, details audit.Details) error {
	_, err := s.o.Auditor.RecordTx(ctx, q, audit.Event{
		Actor: audit.UserActor(actor.UserID), Action: action, TargetType: AuditTargetType,
		TargetID: id.String(), IP: actor.IP, Details: details,
	})
	return err
}
