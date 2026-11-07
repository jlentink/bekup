package summary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/destinations"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/riskaccept"
	"github.com/jlentink/bekup/internal/scheduler"
)

// Content (D-226): built when a summary.periodic event is fanned
// out and again when its e-mail is delivered, from the recipient's current
// access (authz.JobScope: staff see every job, backup owners their own;
// destination usage and the security alert count only with their view
// operations). Everything comes from bekup's own tables: the summary never
// asks a destination or the runner anything (free space only from the
// stored answer of the hourly space check, D-227).

// Bounds of the summary content.
const (
	// maxSummaryRows is how many job rows the e-mail lists: every problem
	// first, the rest counted in one line (D-226 amendment 1).
	maxSummaryRows = 100
	// maxSummaryHTML is the size budget of the HTML part as it is sent
	// (quoted-printable, email.EncodedLen, which is never smaller than the
	// decoded HTML), well under Gmail's clipping at 102 KB; fitJobs lists
	// fewer job rows when names that escape or encode large exceed it.
	maxSummaryHTML = 90_000
	// maxSummaryDestinations is how many destinations the usage lists.
	maxSummaryDestinations = 8
	// maxSummaryJobs bounds the jobs read for one summary.
	maxSummaryJobs = 5000
	// maxJobNameCell bounds a job name in the table (characters).
	maxJobNameCell = 64
	// noSuccessGrace: an occurrence this close to the end of the period
	// may still be running; it is not judged.
	noSuccessGrace = 15 * time.Minute
)

// Overall levels of a summary (banner and subject).
const (
	levelOK      = "ok"
	levelWarning = "warning"
	levelFailure = "failure"
)

// eventDetail is the event detail of a summary for (from, until]:
// catchUp is decide's CatchUp (more than one due time passed, so the
// summary covers the missed ones too); the e-mail says so.
func eventDetail(from, until time.Time, zone string, f Frequency, preview, catchUp bool) notify.Detail {
	return notify.Detail{
		"period_start": from.UTC().Format(time.RFC3339), "period_end": until.UTC().Format(time.RFC3339),
		"timezone": zone, "frequency": string(f), "preview": preview, "catch_up": catchUp,
	}
}

// dedupKey makes the summary of one user and period unique.
func dedupKey(user uuid.UUID, until time.Time) string {
	return fmt.Sprintf("summary:%s:%d", user, until.UTC().Unix())
}

// summaryPeriod is the period a stored summary event covers.
type summaryPeriod struct {
	from, until time.Time
	loc         *time.Location
	zone        string
	frequency   Frequency
	preview     bool
	// catchUp: the summary covers missed due times too.
	catchUp bool
}

// storedDetail is the stored detail of a summary event (written by
// notify.Emit, so small and clean).
type storedDetail struct {
	From      string `json:"period_start"`
	Until     string `json:"period_end"`
	Zone      string `json:"timezone"`
	Frequency string `json:"frequency"`
	Preview   bool   `json:"preview"`
	// CatchUp is absent (false) in events from before amendment 1.
	CatchUp bool `json:"catch_up"`
}

func periodOf(raw []byte) (summaryPeriod, bool) {
	var d storedDetail
	if err := json.Unmarshal(raw, &d); err != nil {
		return summaryPeriod{}, false
	}
	from, err1 := time.Parse(time.RFC3339, d.From)
	until, err2 := time.Parse(time.RFC3339, d.Until)
	if err1 != nil || err2 != nil || !from.Before(until) {
		return summaryPeriod{}, false
	}
	zone := d.Zone
	loc, err := scheduler.LoadZone(zone)
	if err != nil {
		zone, loc = "UTC", time.UTC
	}
	return summaryPeriod{
		from: from, until: until, loc: loc, zone: zone, frequency: Frequency(d.Frequency), preview: d.Preview,
		catchUp: d.CatchUp && !d.Preview,
	}, true
}

// jobStatus is a job's status for the period, worst first (the sort
// order of the jobs table).
type jobStatus int

// Job statuses: one rule per job, the first that applies.
const (
	// statusFailed: a failed run or a failed test restore in the period.
	statusFailed jobStatus = iota
	// statusMissed: job_missed_state, or scheduled in the period without
	// a successful run.
	statusMissed
	// statusPartial: a partially failed run, or a run with warnings.
	statusPartial
	// statusHeld: retention keeps incomplete backups (retention holds).
	statusHeld
	// statusOK: at least one successful run and nothing above.
	statusOK
	// statusNoRun: no run and none due in the period.
	statusNoRun
	// statusDisabled: a disabled job without a run in the period.
	statusDisabled
)

// problem reports the statuses that need attention.
func (s jobStatus) problem() bool { return s <= statusHeld }

// jobRow is one job of the summary.
type jobRow struct {
	Name    string
	Enabled bool
	Status  jobStatus
	// Run counts in the period (OK: successful and partially failed
	// runs, which stored a backup; Warned: successful runs with warnings).
	OK, Failed, Partial, Warned, Canceled int64
	// Bytes is what the period's runs stored; Stored what the job's
	// present backups occupy now (HasStored false: none).
	Bytes, Stored int64
	HasStored     bool
	// LastAt is the newest run of the period (nil: none).
	LastAt *time.Time
	// FailureCode is the error code of the newest failed run.
	FailureCode        string
	MissedSince        *time.Time
	NoSuccess          bool
	HeldRuns           int64
	HoldDestinations   int
	TestRestoresFailed int64
}

// destinationUsage is one destination's stored backups (staff).
type destinationUsage struct {
	Name    string
	Type    destinations.Type
	Bytes   int64
	Backups int64
	// Space is the stored answer of the newest space check (D-227; not
	// Checked when never checked or when the settings changed since);
	// Space.Low is the D-225 low-space verdict of that check.
	Space destinations.StoredSpace
}

// summaryData is everything a summary shows (pure input of buildSummary).
type summaryData struct {
	Period summaryPeriod
	// Now is the render time: stored space answers are read when the
	// summary is rendered and judged against it (zero: the period end).
	Now time.Time
	// Jobs is the number of jobs the recipient may see.
	Jobs int
	// Runs, TestRestores and Restores count by status in the period.
	Runs         map[string]int64
	TestRestores map[string]int64
	Restores     map[string]int64
	// Written is what the period's runs stored.
	Written int64
	// JobRows lists the jobs, worst first, then by name.
	JobRows []jobRow
	// Staff: destination usage (operation destination.usage_view).
	ShowUsage    bool
	Destinations []destinationUsage
	// Admins: security alerts raised in the period (monitor.alert_list).
	ShowAlerts     bool
	SecurityAlerts int64
	// Admins: accepted risks and API tokens without expiry (D-228, D-229;
	// a count line, never problems).
	Risks riskaccept.Counts
}

// summaryScope is the recipient's view: their job scope and the staff
// parts of the summary they may see.
type summaryScope struct {
	user               uuid.UUID
	scopeAll           bool
	usage, alerts, any bool
}

// policySession is the placeholder session id of the principals built
// for the summary's access decisions (never a real session).
var policySession = uuid.MustParse("00000000-0000-4000-8000-0000000005e1")

// scopeOf derives the view of the active account (id, role) from its
// current role with the policy: a synthetic principal for the decision
// only (no session, step-up or restrictions; the view operations need no
// freshness), as notification delivery does.
func (s *Service) scopeOf(ctx context.Context, id uuid.UUID, role sqlc.UserRole) (summaryScope, error) {
	pctx := auth.WithPrincipal(ctx, auth.Principal{UserID: id, Role: auth.Role(role), SessionID: policySession})
	policy := s.o.Policy
	sc := summaryScope{user: id}
	scope, _, err := policy.JobScope(pctx)
	var ae *apierror.Error
	switch {
	case err == nil:
		sc.any, sc.scopeAll = true, scope == authz.ScopeAll
	case errors.As(err, &ae):
		return sc, nil
	default:
		return sc, fmt.Errorf("notify: job scope: %w", err)
	}
	for op, dst := range map[authz.Operation]*bool{authz.OpStorageUsageView: &sc.usage, authz.OpAlertList: &sc.alerts} {
		err := policy.Authorize(pctx, op)
		switch {
		case err == nil:
			*dst = true
		case errors.As(err, &ae):
		default:
			return sc, fmt.Errorf("notify: authorize summary part: %w", err)
		}
	}
	return sc, nil
}

// visibleJobs counts the jobs the scope may see.
func visibleJobs(ctx context.Context, q *sqlc.Queries, sc summaryScope) (int64, error) {
	if !sc.any {
		return 0, nil
	}
	n, err := q.CountSummaryJobs(ctx, sqlc.CountSummaryJobsParams{ScopeAll: sc.scopeAll, UserID: sc.user})
	if err != nil {
		return 0, fmt.Errorf("notify: count jobs: %w", err)
	}
	return n, nil
}

// Render builds the content of a summary.periodic event for its user
// (nil when the user is gone or the detail is unusable); it implements
// notify.SummaryRenderer and runs at fan-out and at e-mail delivery, so
// the content always follows the recipient's current access.
func (s *Service) Render(ctx context.Context, q *sqlc.Queries, ev *sqlc.NotificationEvent) (*notify.SummaryContent, error) {
	if ev.UserID == nil || notify.EventType(ev.Type) != notify.SummaryPeriodic {
		return nil, nil
	}
	p, ok := periodOf(ev.Detail)
	if !ok {
		return nil, nil
	}
	u, err := q.GetNotifiableUser(ctx, *ev.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("notify: load summary user: %w", err)
	}
	sc, err := s.scopeOf(ctx, u.ID, u.Role)
	if err != nil {
		return nil, err
	}
	d, err := s.summaryData(ctx, q, sc, p, s.o.Now())
	if err != nil {
		return nil, err
	}
	c := buildSummary(d, s.dashboard)
	return &c, nil
}

// summaryData reads the summary of scope sc for period p, rendered at now.
func (s *Service) summaryData(ctx context.Context, q *sqlc.Queries, sc summaryScope, p summaryPeriod, now time.Time) (*summaryData, error) {
	d := &summaryData{Period: p, Now: now, Runs: map[string]int64{}, TestRestores: map[string]int64{}, Restores: map[string]int64{}}
	if !sc.any {
		return d, nil
	}
	n, err := visibleJobs(ctx, q, sc)
	if err != nil {
		return nil, err
	}
	d.Jobs = int(n)
	jobs, err := q.ListSummaryJobs(ctx, sqlc.ListSummaryJobsParams{ScopeAll: sc.scopeAll, UserID: sc.user, MaxRows: maxSummaryJobs})
	if err != nil {
		return nil, fmt.Errorf("notify: list summary jobs: %w", err)
	}
	ids := make([]uuid.UUID, len(jobs))
	for i := range jobs {
		ids[i] = jobs[i].ID
	}
	from, until := p.from, p.until
	in := summaryInput{jobs: jobs}
	if in.activity, err = q.SummaryJobActivity(ctx, sqlc.SummaryJobActivityParams{PeriodFrom: from, PeriodUntil: until, ScopeAll: sc.scopeAll, UserID: sc.user}); err != nil {
		return nil, fmt.Errorf("notify: summary job activity: %w", err)
	}
	if in.tests, err = q.SummaryTestRestoreCounts(ctx, sqlc.SummaryTestRestoreCountsParams{PeriodFrom: from, PeriodUntil: until, ScopeAll: sc.scopeAll, UserID: sc.user}); err != nil {
		return nil, fmt.Errorf("notify: summary test restores: %w", err)
	}
	if in.restores, err = q.SummaryRestoreCounts(ctx, sqlc.SummaryRestoreCountsParams{PeriodFrom: from, PeriodUntil: until, ScopeAll: sc.scopeAll, UserID: sc.user}); err != nil {
		return nil, fmt.Errorf("notify: summary restores: %w", err)
	}
	if in.stored, err = q.JobStorageTotals(ctx, sqlc.JobStorageTotalsParams{JobIds: ids, ScopeAll: sc.scopeAll, UserID: sc.user}); err != nil {
		return nil, fmt.Errorf("notify: summary job storage: %w", err)
	}
	if in.missed, err = q.ListJobMissedState(ctx, ids); err != nil {
		return nil, fmt.Errorf("notify: summary missed state: %w", err)
	}
	if in.holds, err = q.ListRetentionHoldsForJobs(ctx, ids); err != nil {
		return nil, fmt.Errorf("notify: summary retention holds: %w", err)
	}
	if in.active, err = q.ListActiveRunJobs(ctx, ids); err != nil {
		return nil, fmt.Errorf("notify: summary active runs: %w", err)
	}
	if sc.usage {
		if in.usage, err = q.DestinationUsage(ctx); err != nil {
			return nil, fmt.Errorf("notify: summary destination usage: %w", err)
		}
		d.ShowUsage = true
		if in.space, err = destinations.StoredSpaces(ctx, q); err != nil {
			return nil, fmt.Errorf("notify: summary destination space: %w", err)
		}
	}
	if sc.alerts {
		if d.SecurityAlerts, err = q.CountSecurityAlertsBetween(ctx, sqlc.CountSecurityAlertsBetweenParams{PeriodFrom: from, PeriodUntil: until}); err != nil {
			return nil, fmt.Errorf("notify: summary security alerts: %w", err)
		}
		if d.Risks, err = riskaccept.CountsOf(ctx, q); err != nil {
			return nil, fmt.Errorf("notify: summary accepted risks: %w", err)
		}
		d.ShowAlerts = true
	}
	in.fill(d)
	return d, nil
}

// summaryInput are the rows of one summary.
type summaryInput struct {
	jobs     []sqlc.ListSummaryJobsRow
	activity []sqlc.SummaryJobActivityRow
	tests    []sqlc.SummaryTestRestoreCountsRow
	restores []sqlc.SummaryRestoreCountsRow
	stored   []sqlc.JobStorageTotalsRow
	missed   []sqlc.ListJobMissedStateRow
	holds    []sqlc.ListRetentionHoldsForJobsRow
	active   []uuid.UUID
	usage    []sqlc.DestinationUsageRow
	space    map[uuid.UUID]destinations.StoredSpace
}

// fill computes the totals and the job rows (pure).
func (in *summaryInput) fill(d *summaryData) {
	act := map[uuid.UUID]*sqlc.SummaryJobActivityRow{}
	for i := range in.activity {
		a := &in.activity[i]
		act[a.JobID] = a
		for status, n := range map[string]int64{
			string(sqlc.RunStatusSuccess): a.Succeeded, string(sqlc.RunStatusPartial): a.Partial,
			string(sqlc.RunStatusFailed): a.Failed, string(sqlc.RunStatusCanceled): a.Canceled,
			string(sqlc.RunStatusSkipped): a.Skipped,
		} {
			if n > 0 {
				d.Runs[status] += n
			}
		}
		d.Written += a.BytesStored
	}
	testsFailed := map[uuid.UUID]int64{}
	for _, t := range in.tests {
		d.TestRestores[t.Status] += t.TestRestores
		if t.Status == string(sqlc.TestRestoreStatusFailed) || t.Status == string(sqlc.TestRestoreStatusError) {
			testsFailed[t.JobID] += t.TestRestores
		}
	}
	for _, r := range in.restores {
		d.Restores[r.Status] += r.Restores
	}
	stored := map[uuid.UUID]int64{}
	for _, st := range in.stored {
		stored[st.JobID] = st.StoredBytes
	}
	missed := map[uuid.UUID]time.Time{}
	for _, m := range in.missed {
		missed[m.JobID] = m.MissedSince
	}
	type hold struct {
		runs  int64
		dests int
	}
	holds := map[uuid.UUID]*hold{}
	for i := range in.holds {
		h := &in.holds[i]
		if holds[h.JobID] == nil {
			holds[h.JobID] = &hold{}
		}
		holds[h.JobID].runs += int64(h.HeldRuns)
		holds[h.JobID].dests++
	}
	active := map[uuid.UUID]bool{}
	for _, id := range in.active {
		active[id] = true
	}
	for _, j := range in.jobs {
		r := jobRow{Name: notify.DisplayName(j.Name), Enabled: j.Enabled, TestRestoresFailed: testsFailed[j.ID]}
		if a := act[j.ID]; a != nil {
			r.OK, r.Failed, r.Partial, r.Warned, r.Canceled = a.Succeeded+a.Partial, a.Failed, a.Partial, a.WithWarnings, a.Canceled
			r.Bytes, r.FailureCode = a.BytesStored, a.LastFailureCode
			last := a.LastFinishedAt
			r.LastAt = &last
		}
		r.Stored, r.HasStored = stored[j.ID]
		if since, ok := missed[j.ID]; ok {
			r.MissedSince = &since
		}
		if h := holds[j.ID]; h != nil {
			r.HeldRuns, r.HoldDestinations = h.runs, h.dests
		}
		r.NoSuccess = r.OK == 0 && r.MissedSince == nil && !active[j.ID] && scheduledIn(j, d.Period.from, d.Period.until)
		r.Status = r.status()
		d.JobRows = append(d.JobRows, r)
	}
	// Worst first, then by name (ListSummaryJobs is ordered by name).
	slices.SortStableFunc(d.JobRows, func(a, b jobRow) int { return int(a.Status) - int(b.Status) })
	for _, u := range in.usage {
		du := destinationUsage{
			Name: notify.DisplayName(u.Name), Type: destinations.Type(u.Type), Bytes: u.StoredBytes, Backups: u.BackupCount,
			Space: in.space[u.ID],
		}
		d.Destinations = append(d.Destinations, du)
	}
}

// status applies the job status rule (the first that applies).
func (r *jobRow) status() jobStatus {
	switch {
	case r.Failed > 0 || r.TestRestoresFailed > 0:
		return statusFailed
	case r.MissedSince != nil || r.NoSuccess:
		return statusMissed
	case r.Partial > 0 || r.Warned > 0:
		return statusPartial
	case r.HeldRuns > 0:
		return statusHeld
	case r.OK > 0:
		return statusOK
	case !r.Enabled:
		return statusDisabled
	default:
		return statusNoRun
	}
}

// scheduledIn reports whether an enabled job had a scheduled occurrence in
// (from, until - noSuccessGrace] since it exists.
func scheduledIn(j sqlc.ListSummaryJobsRow, from, until time.Time) bool {
	if !j.Enabled {
		return false
	}
	var sch scheduler.Schedule
	if err := json.Unmarshal(j.Schedule, &sch); err != nil || sch.Kind == scheduler.KindManual {
		return false
	}
	p, err := scheduler.Compile(sch)
	if err != nil {
		return false
	}
	after := from
	if j.CreatedAt.After(after) {
		after = j.CreatedAt
	}
	n, err := p.Next(after)
	return err == nil && !n.After(until.Add(-noSuccessGrace))
}

// counts are the number of jobs per status.
type counts [statusDisabled + 1]int

func (d *summaryData) counts() counts {
	var c counts
	for i := range d.JobRows {
		c[d.JobRows[i].Status]++
	}
	return c
}

func (c counts) problems() int {
	return c[statusFailed] + c[statusMissed] + c[statusPartial] + c[statusHeld]
}

// lowDestinations counts the listed destinations low on space.
func (d *summaryData) lowDestinations() int {
	n := 0
	for i := range d.Destinations[:min(len(d.Destinations), maxSummaryDestinations)] {
		if sp := d.Destinations[i].Space; sp.Checked() && sp.Low != destinations.LowNone {
			n++
		}
	}
	return n
}

// joinAnd joins parts as "a, b and c".
func joinAnd(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}
