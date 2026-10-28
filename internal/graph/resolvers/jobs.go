package resolvers

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/destinations"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/scheduler"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// Job resolvers (task 3.5, D-146) and the job encryption fields of task 3.2
// (D-142). Owner-scoped operations (job.view, run.now) authorize with the
// job id from the arguments against job_owners (never trusting input for
// ownership); the list filters by Policy.JobScope. Key material leaves the
// server only where D-142 says so: a generated passphrase or identity once,
// and the admin passphrase export.

var (
	errJobsNotConfigured = errors.New("resolvers: job service not configured")
	errJobNotFound       = apierror.New(apierror.NotFound, "job not found")
)

const (
	jobCursorPrefix = "job:"
	maxJobCursorLen = 512
	defaultJobPage  = 50
)

// jobError maps service errors of internal/jobs and internal/jobkeys.
func jobError(err error) error {
	var fe *serviceutil.FieldError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &fe):
		return apierror.Invalid(fe.Field, fe.Message)
	case errors.Is(err, jobs.ErrNotFound):
		return errJobNotFound
	case errors.Is(err, jobs.ErrNameTaken):
		return apierror.New(apierror.Conflict, "another job already uses this name")
	case errors.Is(err, jobs.ErrSlugTaken):
		return apierror.New(apierror.Conflict, "another job already uses this slug")
	case errors.Is(err, jobs.ErrRunActive):
		return apierror.New(apierror.Conflict, "a run of this job is already active")
	case errors.Is(err, jobs.ErrDisabled):
		return apierror.New(apierror.Conflict, "the job is disabled")
	case errors.Is(err, jobs.ErrNotOwner):
		return apierror.New(apierror.NotFound, "the user is not an owner of this job")
	case errors.Is(err, jobkeys.ErrNotConfigured):
		return apierror.New(apierror.Conflict, "the job has no encryption configuration")
	case errors.Is(err, jobkeys.ErrNoActiveKey):
		return apierror.New(apierror.Conflict, "the job has no key for this mode; set or generate one")
	case errors.Is(err, jobkeys.ErrNotOptedOut):
		return apierror.New(apierror.Conflict, "encryption is already on")
	case errors.Is(err, jobkeys.ErrAlreadyOptedOut):
		return apierror.New(apierror.Conflict, "encryption is already off")
	case errors.Is(err, jobkeys.ErrNoPassphrases):
		return apierror.New(apierror.Conflict, "the job has no passphrases")
	case errors.Is(err, jobkeys.ErrAdminOnly):
		return apierror.New(apierror.Forbidden, "only an admin can change a job's encryption keys")
	default:
		return err
	}
}

// jobCaller returns the job service, the caller and its actor.
func (r *Resolver) jobCaller(ctx context.Context) (*jobs.Service, jobs.Actor, error) {
	if r.Jobs == nil || r.JobKeys == nil {
		return nil, jobs.Actor{}, errJobsNotConfigured
	}
	p, err := caller(ctx)
	if err != nil {
		return nil, jobs.Actor{}, err
	}
	return r.Jobs, jobs.Actor{UserID: p.UserID, IP: auth.RequestMetaFrom(ctx).IP}, nil
}

func keysActor(a jobs.Actor) jobkeys.Actor { return jobkeys.Actor{UserID: a.UserID, IP: a.IP} }

// --- queries -------------------------------------------------------------------

func encodeJobCursor(j *jobs.Job) string {
	return base64.RawURLEncoding.EncodeToString([]byte(jobCursorPrefix + j.ID.String() + ":" + strings.ToLower(j.Name)))
}

func decodeJobCursor(after *string) (*jobs.Cursor, error) {
	if after == nil {
		return nil, nil
	}
	errBad := apierror.Invalid("after", "invalid cursor")
	if len(*after) > maxJobCursorLen {
		return nil, errBad
	}
	raw, err := base64.RawURLEncoding.DecodeString(*after)
	if err != nil {
		return nil, errBad
	}
	rest, ok := strings.CutPrefix(string(raw), jobCursorPrefix)
	if !ok {
		return nil, errBad
	}
	idStr, name, ok := strings.Cut(rest, ":")
	if !ok {
		return nil, errBad
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil, errBad
	}
	return &jobs.Cursor{Name: name, ID: id}, nil
}

func (r *Resolver) jobs(ctx context.Context, filter *model.JobFilter, first *int, after *string) (*model.JobConnection, error) {
	svc, _, err := r.jobCaller(ctx)
	if err != nil {
		return nil, err
	}
	limit := defaultJobPage
	if first != nil {
		if *first < 1 || *first > 200 {
			return nil, apierror.Invalid("first", "first must be between 1 and 200")
		}
		limit = *first
	}
	cur, err := decodeJobCursor(after)
	if err != nil {
		return nil, err
	}
	scope, uid, err := r.Authz.JobScope(ctx)
	if err != nil {
		return nil, err
	}
	var f jobs.ListFilter
	if filter != nil {
		f.Enabled, f.ConnectionID = filter.Enabled, filter.ConnectionID
		if filter.Search != nil {
			f.Search = *filter.Search
		}
	}
	page, err := svc.List(ctx, scope, uid, f, cur, limit)
	if err != nil {
		return nil, jobError(err)
	}
	conn := &model.JobConnection{Edges: make([]model.JobEdge, 0, len(page.Jobs)), PageInfo: &model.PageInfo{HasNextPage: page.More, HasPreviousPage: cur != nil}}
	ids := make([]uuid.UUID, 0, len(page.Jobs))
	for i := range page.Jobs {
		ids = append(ids, page.Jobs[i].ID)
		conn.Edges = append(conn.Edges, model.JobEdge{Cursor: encodeJobCursor(&page.Jobs[i]), Node: jobModel(&page.Jobs[i])})
	}
	// Job.lastRun / recentRuns load for the whole page at once (task 3.7).
	hintJobs(ctx, ids)
	if n := len(conn.Edges); n > 0 {
		start, end := conn.Edges[0].Cursor, conn.Edges[n-1].Cursor
		conn.PageInfo.StartCursor, conn.PageInfo.EndCursor = &start, &end
	}
	return conn, nil
}

func (r *Resolver) job(ctx context.Context, id uuid.UUID) (*model.Job, error) {
	svc, _, err := r.jobCaller(ctx)
	if err != nil {
		return nil, err
	}
	// Owners: the ownership check runs before the lookup, so a non-owner
	// learns nothing about whether the job exists.
	if err := r.Authz.Authorize(ctx, authz.OpJobView, authz.Job{ID: id}); err != nil {
		return nil, err
	}
	j, err := svc.Get(ctx, id)
	if errors.Is(err, jobs.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, jobError(err)
	}
	return jobModel(&j), nil
}

// jobEncryption is Job.encryption (inherits job.view of the parent field).
func (r *Resolver) jobEncryption(ctx context.Context, obj *model.Job) (*model.JobEncryption, error) {
	if r.JobKeys == nil {
		return nil, errJobsNotConfigured
	}
	st, err := r.JobKeys.Status(ctx, obj.ID)
	if err != nil {
		return nil, jobError(err)
	}
	return encryptionModel(&st), nil
}

// jobOwners is Job.owners: admins and maintainers only, null otherwise.
func (r *Resolver) jobOwners(ctx context.Context, obj *model.Job) ([]model.JobOwner, error) {
	if r.Jobs == nil {
		return nil, errJobsNotConfigured
	}
	if err := r.Authz.Authorize(ctx, authz.OpJobOwnersView); err != nil {
		return nil, nil //nolint:nilerr // not visible to this role: null, not an error
	}
	owners, err := r.Jobs.Owners(ctx, obj.ID)
	if err != nil {
		return nil, jobError(err)
	}
	return ownersModel(owners), nil
}

func (r *Resolver) previewSchedule(ctx context.Context, in model.ScheduleInput, count *int) (*model.SchedulePreview, error) {
	svc, _, err := r.jobCaller(ctx)
	if err != nil {
		return nil, err
	}
	n := 0
	if count != nil {
		n = *count
		if n < 1 {
			return nil, apierror.Invalid("count", "count must be between 1 and 50")
		}
	}
	sched, err := scheduleFromModel(&in)
	if err != nil {
		return nil, err
	}
	p, err := svc.PreviewSchedule(sched, n)
	if err != nil {
		return nil, jobError(err)
	}
	return &model.SchedulePreview{Schedule: scheduleModel(p.Schedule), Description: p.Description, Next: p.Next}, nil
}

// --- job mutations -------------------------------------------------------------

func (r *Resolver) createJob(ctx context.Context, in model.CreateJobInput) (*model.CreateJobResult, error) {
	svc, actor, err := r.jobCaller(ctx)
	if err != nil {
		return nil, err
	}
	ci := jobs.CreateInput{
		Name: in.Name, ConnectionID: in.ConnectionID, Databases: in.Databases, Exclude: in.Exclude,
		IncludeGlobals: in.IncludeGlobals, Enabled: in.Enabled, DestinationIDs: in.DestinationIds,
	}
	if in.Slug != nil {
		ci.Slug = *in.Slug
	}
	if in.Description != nil {
		ci.Description = *in.Description
	}
	if in.DbMode != nil {
		ci.DBMode = dbModeFromModel(*in.DbMode)
	}
	if in.Schedule == nil {
		return nil, apierror.Invalid("schedule", "a schedule is required")
	}
	if ci.Schedule, err = scheduleFromModel(in.Schedule); err != nil {
		return nil, err
	}
	if in.Retention != nil {
		ci.Retention = jobs.Retention{KeepLast: in.Retention.KeepLast, KeepDays: in.Retention.KeepDays}
	}
	ci.Timeout = secondsDuration(in.TimeoutSeconds)
	ci.ExpectedWindow = secondsDuration(in.ExpectedWindowSeconds)
	if in.Verification != nil {
		ci.Verification = verificationFromModel(*in.Verification)
	}
	if e := in.Encryption; e != nil {
		if e.Mode != nil {
			ci.Encryption.Mode = keyModeFromModel(*e.Mode)
		}
		if e.Recipient != nil {
			ci.Encryption.Recipient = *e.Recipient
		}
		if e.Label != nil {
			ci.Encryption.Label = *e.Label
		}
		if e.Passphrase != nil {
			undo := logging.DefaultRegistry.Register([]byte(*e.Passphrase))
			defer undo()
			ci.Encryption.Passphrase = crypto.NewSecret([]byte(*e.Passphrase))
		}
	}
	created, err := svc.Create(ctx, actor, ci)
	if err != nil {
		return nil, jobError(err)
	}
	defer created.Destroy()
	out := &model.CreateJobResult{Job: jobModel(&created.Job)}
	if res := created.Encryption; res != nil {
		enc := encryptionModel(&res.Status)
		if res.Passphrase != nil {
			s := string(res.Passphrase.Passphrase.Bytes())
			out.GeneratedPassphrase = &s
		}
		if res.Keypair != nil {
			out.GeneratedKeypair = keypairModel(res.Keypair, enc)
		}
		out.Job.Encryption = enc
	}
	return out, nil
}

func (r *Resolver) updateJob(ctx context.Context, id uuid.UUID, in model.UpdateJobInput) (*model.Job, error) {
	svc, actor, err := r.jobCaller(ctx)
	if err != nil {
		return nil, err
	}
	ui := jobs.UpdateInput{
		Name: in.Name, Description: in.Description, ConnectionID: in.ConnectionID, IncludeGlobals: in.IncludeGlobals,
		ClearTimeout: isTrue(in.ClearTimeout), ClearExpectedWindow: isTrue(in.ClearExpectedWindow),
		Timeout: secondsDuration(in.TimeoutSeconds), ExpectedWindow: secondsDuration(in.ExpectedWindowSeconds),
	}
	if in.DbMode != nil {
		m := dbModeFromModel(*in.DbMode)
		ui.DBMode = &m
	}
	// gqlgen decodes an omitted list as nil and [] as an empty slice.
	if in.Databases != nil {
		ui.Databases = &in.Databases
	}
	if in.Exclude != nil {
		ui.Exclude = &in.Exclude
	}
	if in.DestinationIds != nil {
		ui.DestinationIDs = &in.DestinationIds
	}
	if in.Schedule != nil {
		s, err := scheduleFromModel(in.Schedule)
		if err != nil {
			return nil, err
		}
		ui.Schedule = &s
	}
	if in.Retention != nil {
		ui.Retention = &jobs.Retention{KeepLast: in.Retention.KeepLast, KeepDays: in.Retention.KeepDays}
	}
	if in.Verification != nil {
		v := verificationFromModel(*in.Verification)
		ui.Verification = &v
	}
	j, err := svc.Update(ctx, actor, id, ui)
	if err != nil {
		return nil, jobError(err)
	}
	return jobModel(&j), nil
}

func (r *Resolver) deleteJob(ctx context.Context, id uuid.UUID) (bool, error) {
	svc, actor, err := r.jobCaller(ctx)
	if err != nil {
		return false, err
	}
	if err := svc.Delete(ctx, actor, id); err != nil {
		return false, jobError(err)
	}
	return true, nil
}

func (r *Resolver) setJobEnabled(ctx context.Context, id uuid.UUID, enabled bool) (*model.Job, error) {
	svc, actor, err := r.jobCaller(ctx)
	if err != nil {
		return nil, err
	}
	j, err := svc.SetEnabled(ctx, actor, id, enabled)
	if err != nil {
		return nil, jobError(err)
	}
	return jobModel(&j), nil
}

func (r *Resolver) assignJobOwners(ctx context.Context, jobID uuid.UUID, users []uuid.UUID) ([]model.JobOwner, error) {
	svc, actor, err := r.jobCaller(ctx)
	if err != nil {
		return nil, err
	}
	owners, err := svc.AssignOwners(ctx, actor, jobID, users)
	if err != nil {
		return nil, jobError(err)
	}
	return ownersModel(owners), nil
}

func (r *Resolver) removeJobOwner(ctx context.Context, jobID, user uuid.UUID) ([]model.JobOwner, error) {
	svc, actor, err := r.jobCaller(ctx)
	if err != nil {
		return nil, err
	}
	owners, err := svc.RemoveOwner(ctx, actor, jobID, user)
	if err != nil {
		return nil, jobError(err)
	}
	return ownersModel(owners), nil
}

func (r *Resolver) runJobNow(ctx context.Context, id uuid.UUID) (*model.RunJobNowResult, error) {
	svc, actor, err := r.jobCaller(ctx)
	if err != nil {
		return nil, err
	}
	// The ownership check (owners) runs before anything is loaded.
	if err := r.Authz.Authorize(ctx, authz.OpRunNow, authz.Job{ID: id}); err != nil {
		return nil, err
	}
	req, err := svc.RunNow(ctx, actor, id)
	if err != nil {
		return nil, jobError(err)
	}
	j, err := svc.Get(ctx, id)
	if err != nil {
		return nil, jobError(err)
	}
	return &model.RunJobNowResult{Job: jobModel(&j), RunID: req.RunID, QueuedAt: req.RequestedAt}, nil
}

func (r *Resolver) startNextRunEarly(ctx context.Context, id uuid.UUID) (*model.RunJobNowResult, error) {
	svc, actor, err := r.jobCaller(ctx)
	if err != nil {
		return nil, err
	}
	// The ownership check (owners) runs before anything is loaded.
	if err := r.Authz.Authorize(ctx, authz.OpRunStartEarly, authz.Job{ID: id}); err != nil {
		return nil, err
	}
	req, err := svc.StartNextRunEarly(ctx, actor, id)
	if err != nil {
		return nil, jobError(err)
	}
	j, err := svc.Get(ctx, id)
	if err != nil {
		return nil, jobError(err)
	}
	return &model.RunJobNowResult{Job: jobModel(&j), RunID: req.RunID, QueuedAt: req.RequestedAt}, nil
}

// --- encryption mutations (task 3.2 wiring) --------------------------------

// keyJob checks that the job exists and is not deleted before a key change
// (internal/jobkeys only knows the encryption rows).
func (r *Resolver) keyJob(ctx context.Context, id uuid.UUID) (*jobkeys.Service, jobs.Actor, error) {
	svc, actor, err := r.jobCaller(ctx)
	if err != nil {
		return nil, jobs.Actor{}, err
	}
	if _, err := svc.Get(ctx, id); err != nil {
		return nil, jobs.Actor{}, jobError(err)
	}
	return r.JobKeys, actor, nil
}

func (r *Resolver) jobStatus(ctx context.Context, keys *jobkeys.Service, id uuid.UUID) (*model.JobEncryption, error) {
	st, err := keys.Status(ctx, id)
	if err != nil {
		return nil, jobError(err)
	}
	return encryptionModel(&st), nil
}

func (r *Resolver) setJobPassphrase(ctx context.Context, id uuid.UUID, passphrase string) (*model.JobEncryption, error) {
	undo := logging.DefaultRegistry.Register([]byte(passphrase))
	defer undo()
	keys, actor, err := r.keyJob(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, err := keys.SetPassphrase(ctx, keysActor(actor), id, crypto.NewSecret([]byte(passphrase))); err != nil {
		return nil, jobError(err)
	}
	return r.jobStatus(ctx, keys, id)
}

func (r *Resolver) generateJobPassphrase(ctx context.Context, id uuid.UUID) (*model.GeneratedJobPassphrase, error) {
	keys, actor, err := r.keyJob(ctx, id)
	if err != nil {
		return nil, err
	}
	g, err := keys.GeneratePassphrase(ctx, keysActor(actor), id)
	if err != nil {
		return nil, jobError(err)
	}
	defer g.Destroy()
	enc, err := r.jobStatus(ctx, keys, id)
	if err != nil {
		return nil, err
	}
	return &model.GeneratedJobPassphrase{Encryption: enc, Version: g.Info.Version, Passphrase: string(g.Passphrase.Bytes())}, nil
}

func (r *Resolver) setJobRecipient(ctx context.Context, id uuid.UUID, recipient string, label *string) (*model.JobEncryption, error) {
	keys, actor, err := r.keyJob(ctx, id)
	if err != nil {
		return nil, err
	}
	l := ""
	if label != nil {
		l = *label
	}
	if _, err := keys.SetRecipient(ctx, keysActor(actor), id, recipient, l); err != nil {
		return nil, jobError(err)
	}
	return r.jobStatus(ctx, keys, id)
}

func (r *Resolver) generateAgeKeypair(ctx context.Context, id uuid.UUID, label *string) (*model.GeneratedAgeKeypair, error) {
	keys, actor, err := r.keyJob(ctx, id)
	if err != nil {
		return nil, err
	}
	l := ""
	if label != nil {
		l = *label
	}
	kp, err := keys.GenerateAgeKeypair(ctx, keysActor(actor), id, l)
	if err != nil {
		return nil, jobError(err)
	}
	defer kp.Destroy()
	enc, err := r.jobStatus(ctx, keys, id)
	if err != nil {
		return nil, err
	}
	return keypairModel(kp, enc), nil
}

func (r *Resolver) enableJobEncryption(ctx context.Context, id uuid.UUID, mode model.JobKeyMode) (*model.JobEncryption, error) {
	keys, actor, err := r.keyJob(ctx, id)
	if err != nil {
		return nil, err
	}
	st, err := keys.OptIn(ctx, keysActor(actor), id, keyModeFromModel(mode))
	if err != nil {
		return nil, jobError(err)
	}
	return encryptionModel(&st), nil
}

func (r *Resolver) disableJobEncryption(ctx context.Context, id uuid.UUID, reason string) (*model.JobEncryption, error) {
	keys, actor, err := r.keyJob(ctx, id)
	if err != nil {
		return nil, err
	}
	st, err := keys.OptOut(ctx, keysActor(actor), id, reason)
	if err != nil {
		return nil, jobError(err)
	}
	return encryptionModel(&st), nil
}

func (r *Resolver) exportJobPassphrases(ctx context.Context, id uuid.UUID) (*model.JobPassphraseExport, error) {
	keys, actor, err := r.keyJob(ctx, id)
	if err != nil {
		return nil, err
	}
	ex, err := keys.ExportPassphrases(ctx, keysActor(actor), id)
	if err != nil {
		return nil, jobError(err)
	}
	defer ex.Destroy()
	file := ex.File()
	defer file.Destroy()
	versions := make([]int, len(ex.Versions))
	for i, v := range ex.Versions {
		versions[i] = v.Version
	}
	return &model.JobPassphraseExport{FileName: ex.FileName(), Content: string(file.Bytes()), Versions: versions}, nil
}

// --- conversions -----------------------------------------------------------------

func secondsDuration(s *int) *time.Duration {
	if s == nil {
		return nil
	}
	d := time.Duration(*s) * time.Second
	return &d
}

func durationSeconds(d time.Duration) *int {
	if d <= 0 {
		return nil
	}
	s := int(d / time.Second)
	return &s
}

func dbModeFromModel(m model.JobDbMode) jobs.DBMode {
	if m == model.JobDbModeSelected {
		return jobs.DBModeSelected
	}
	if m == model.JobDbModeAll {
		return jobs.DBModeAll
	}
	return jobs.DBMode(m)
}

func dbModeModel(m jobs.DBMode) model.JobDbMode {
	if m == jobs.DBModeSelected {
		return model.JobDbModeSelected
	}
	return model.JobDbModeAll
}

func verificationFromModel(v model.JobVerification) jobs.Verification {
	switch v {
	case model.JobVerificationDeep:
		return jobs.VerificationDeep
	case model.JobVerificationStandard:
		return jobs.VerificationStandard
	default:
		return jobs.Verification(v)
	}
}

func verificationModel(v jobs.Verification) model.JobVerification {
	if v == jobs.VerificationDeep {
		return model.JobVerificationDeep
	}
	return model.JobVerificationStandard
}

func keyModeFromModel(m model.JobKeyMode) jobkeys.Mode {
	switch m {
	case model.JobKeyModeRecipient:
		return jobkeys.ModeRecipient
	case model.JobKeyModePassphrase:
		return jobkeys.ModePassphrase
	default:
		return jobkeys.Mode(m)
	}
}

var scheduleKinds = map[model.ScheduleKind]scheduler.Kind{
	model.ScheduleKindManual: scheduler.KindManual, model.ScheduleKindInterval: scheduler.KindInterval, model.ScheduleKindCron: scheduler.KindCron,
}

var scheduleUnits = map[model.ScheduleUnit]scheduler.Unit{
	model.ScheduleUnitMinutes: scheduler.UnitMinutes, model.ScheduleUnitHours: scheduler.UnitHours,
	model.ScheduleUnitDays: scheduler.UnitDays, model.ScheduleUnitWeeks: scheduler.UnitWeeks, model.ScheduleUnitMonths: scheduler.UnitMonths,
}

func scheduleFromModel(in *model.ScheduleInput) (scheduler.Schedule, error) {
	k, ok := scheduleKinds[in.Kind]
	if !ok {
		return scheduler.Schedule{}, apierror.Invalid("schedule.kind", "unknown schedule kind")
	}
	s := scheduler.Schedule{Kind: k}
	if in.Every != nil {
		s.Every = *in.Every
	}
	if in.Unit != nil {
		u, ok := scheduleUnits[*in.Unit]
		if !ok {
			return scheduler.Schedule{}, apierror.Invalid("schedule.unit", "unknown unit")
		}
		s.Unit = u
	}
	if in.Anchor != nil {
		s.Anchor = *in.Anchor
	}
	if in.Weekday != nil {
		s.Weekday = *in.Weekday
	}
	if in.DayOfMonth != nil {
		s.DayOfMonth = *in.DayOfMonth
	}
	if in.Cron != nil {
		s.Cron = *in.Cron
	}
	if in.Timezone != nil {
		s.Timezone = *in.Timezone
	}
	return s, nil
}

func strPtrIf(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func intPtrIf(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}

func scheduleModel(s scheduler.Schedule) *model.Schedule {
	out := &model.Schedule{
		Every: intPtrIf(s.Every), Anchor: strPtrIf(s.Anchor), Weekday: strPtrIf(s.Weekday),
		DayOfMonth: intPtrIf(s.DayOfMonth), Cron: strPtrIf(s.Cron), Timezone: strPtrIf(s.Timezone),
	}
	out.Kind = model.ScheduleKindManual
	for mk, k := range scheduleKinds {
		if k == s.Kind {
			out.Kind = mk
		}
	}
	for mu, u := range scheduleUnits {
		if u == s.Unit && s.Unit != "" {
			mu := mu
			out.Unit = &mu
		}
	}
	return out
}

func jobModel(j *jobs.Job) *model.Job {
	m := &model.Job{
		ID: j.ID, Name: j.Name, Slug: j.Slug, Description: j.Description,
		Connection: &model.JobConnectionRef{
			ID: j.Connection.ID, Name: j.Connection.Name, Engine: engineModel(j.Connection.Engine),
			VersionKey: j.Connection.VersionKey, Deleted: j.Connection.Deleted,
		},
		DbMode: dbModeModel(j.DBMode), Databases: j.Databases, Exclude: j.Exclude, IncludeGlobals: j.IncludeGlobals,
		Schedule: scheduleModel(j.Schedule), ScheduleDescription: j.ScheduleDescription, Enabled: j.Enabled,
		NextRunAt: j.NextRunAt, LastScheduledAt: j.LastScheduledAt,
		Retention:      &model.JobRetention{KeepLast: j.Retention.KeepLast, KeepDays: j.Retention.KeepDays},
		TimeoutSeconds: durationSeconds(j.Timeout), ExpectedWindowSeconds: durationSeconds(j.ExpectedWindow),
		Verification: verificationModel(j.Verification), CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt,
		Destinations: make([]model.JobDestinationRef, 0, len(j.Destinations)),
		TestRestore:  testRestoreJobModel(&j.TestRestore),
	}
	for _, d := range j.Destinations {
		m.Destinations = append(m.Destinations, model.JobDestinationRef{
			ID: d.ID, Name: d.Name, Type: destinationTypeModel(destinations.Type(d.Type)), Position: d.Position, Deleted: d.Deleted,
		})
	}
	return m
}

func ownersModel(owners []jobs.Owner) []model.JobOwner {
	out := make([]model.JobOwner, 0, len(owners))
	for _, o := range owners {
		role := model.RoleUser
		switch auth.Role(o.Role) {
		case auth.RoleAdmin:
			role = model.RoleAdmin
		case auth.RoleMaintainer:
			role = model.RoleMaintainer
		case auth.RoleUser:
		}
		out = append(out, model.JobOwner{UserID: o.UserID, Email: o.Email, DisplayName: o.DisplayName, Role: role, Disabled: o.Disabled, AddedAt: o.AddedAt})
	}
	return out
}

func encryptionModel(st *jobkeys.Status) *model.JobEncryption {
	m := &model.JobEncryption{Configured: st.Configured, OptedOut: st.OptedOut, OptedOutAt: st.OptedOutAt}
	if !st.Configured {
		return m
	}
	mode := model.JobEncryptionModePassphrase
	switch st.Mode {
	case jobkeys.ModeRecipient:
		mode = model.JobEncryptionModeRecipient
	case jobkeys.ModeNone:
		mode = model.JobEncryptionModeNone
	case jobkeys.ModePassphrase:
	}
	m.Mode = &mode
	m.OptOutReason = strPtrIf(st.OptOutReason)
	if !st.UpdatedAt.IsZero() {
		t := st.UpdatedAt
		m.UpdatedAt = &t
	}
	if p := st.Passphrase; p != nil {
		m.Passphrase = &model.JobPassphraseInfo{Version: p.Version, CreatedAt: p.CreatedAt}
	}
	if rc := st.Recipient; rc != nil {
		m.Recipient = &model.JobRecipientInfo{
			Version: rc.Version, Recipient: rc.Recipient, Fingerprint: rc.Fingerprint, Label: rc.Label,
			Generated: rc.Generated, CreatedAt: rc.CreatedAt,
		}
	}
	return m
}

func keypairModel(kp *jobkeys.GeneratedKeypair, enc *model.JobEncryption) *model.GeneratedAgeKeypair {
	return &model.GeneratedAgeKeypair{
		Encryption: enc, Version: kp.Info.Version, Recipient: kp.Info.Recipient, Fingerprint: kp.Info.Fingerprint,
		Identity: string(kp.Identity.Bytes()), IdentityFile: string(kp.IdentityFile.Bytes()), FileName: kp.FileName,
	}
}
