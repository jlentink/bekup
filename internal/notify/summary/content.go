package summary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/notify/email"
	"github.com/jlentink/bekup/internal/scheduler"
)

// Content (D-226): built when a summary.periodic event is fanned
// out and again when its e-mail is delivered, from the recipient's current
// access (authz.JobScope: staff see every job, backup owners their own;
// destination usage and the security alert count only with their view
// operations). Everything comes from bekup's own tables: the summary never
// asks a destination or the runner anything (free space only when a
// recent answer is cached in this process).

// Bounds of the summary content.
const (
	// maxSummaryAttention is how many jobs needing attention are listed
	// (the rest is counted).
	maxSummaryAttention = 20
	// maxSummaryDestinations is how many destinations the usage lists.
	maxSummaryDestinations = 8
	// maxSummaryJobs bounds the jobs read for one summary.
	maxSummaryJobs = 5000
	// noSuccessGrace: an occurrence this close to the end of the period
	// may still be running; it is not judged.
	noSuccessGrace = 15 * time.Minute
)

// eventDetail is the event detail of a summary for (from, until].
func eventDetail(from, until time.Time, zone string, f Frequency, preview bool) notify.Detail {
	return notify.Detail{
		"period_start": from.UTC().Format(time.RFC3339), "period_end": until.UTC().Format(time.RFC3339),
		"timezone": zone, "frequency": string(f), "preview": preview,
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
}

// storedDetail is the stored detail of a summary event (written by
// notify.Emit, so small and clean).
type storedDetail struct {
	From      string `json:"period_start"`
	Until     string `json:"period_end"`
	Zone      string `json:"timezone"`
	Frequency string `json:"frequency"`
	Preview   bool   `json:"preview"`
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
	return summaryPeriod{from: from, until: until, loc: loc, zone: zone, frequency: Frequency(d.Frequency), preview: d.Preview}, true
}

// attentionJob is a job that needs attention in the period.
type attentionJob struct {
	Name               string
	Failed, Partial    int64
	MissedSince        *time.Time
	NoSuccess          bool
	HeldRuns           int64
	HoldDestinations   int
	TestRestoresFailed int64
}

// destinationUsage is one destination's stored backups (staff).
type destinationUsage struct {
	Name    string
	Bytes   int64
	Backups int64
	// Free is the free space when a recent answer is cached (nil: not
	// known here).
	Free *int64
}

// summaryData is everything a summary shows (pure input of buildSummary).
type summaryData struct {
	Period summaryPeriod
	// Jobs is the number of jobs the recipient may see.
	Jobs int
	// Runs and TestRestores count by status in the period.
	Runs         map[string]int64
	TestRestores map[string]int64
	// Attention lists every job needing attention (sorted by name).
	Attention []attentionJob
	// Staff: destination usage (operation destination.usage_view).
	ShowUsage    bool
	Destinations []destinationUsage
	// Admins: security alerts raised in the period (monitor.alert_list).
	ShowAlerts     bool
	SecurityAlerts int64
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
	d, err := s.summaryData(ctx, q, sc, p)
	if err != nil {
		return nil, err
	}
	c := buildSummary(d, s.dashboard)
	return &c, nil
}

// summaryData reads the summary of scope sc for period p.
func (s *Service) summaryData(ctx context.Context, q *sqlc.Queries, sc summaryScope, p summaryPeriod) (*summaryData, error) {
	d := &summaryData{Period: p, Runs: map[string]int64{}, TestRestores: map[string]int64{}}
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
	runs, err := q.SummaryRunCounts(ctx, sqlc.SummaryRunCountsParams{PeriodFrom: p.from, PeriodUntil: p.until, ScopeAll: sc.scopeAll, UserID: sc.user})
	if err != nil {
		return nil, fmt.Errorf("notify: summary runs: %w", err)
	}
	tests, err := q.SummaryTestRestoreCounts(ctx, sqlc.SummaryTestRestoreCountsParams{PeriodFrom: p.from, PeriodUntil: p.until, ScopeAll: sc.scopeAll, UserID: sc.user})
	if err != nil {
		return nil, fmt.Errorf("notify: summary test restores: %w", err)
	}
	missed, err := q.ListJobMissedState(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("notify: summary missed state: %w", err)
	}
	holds, err := q.ListRetentionHoldsForJobs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("notify: summary retention holds: %w", err)
	}
	active, err := q.ListActiveRunJobs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("notify: summary active runs: %w", err)
	}
	in := summaryInput{
		jobs: jobs, runs: runs, tests: tests, missed: missed, holds: holds, active: active,
	}
	if sc.usage {
		usage, err := q.DestinationUsage(ctx)
		if err != nil {
			return nil, fmt.Errorf("notify: summary destination usage: %w", err)
		}
		in.usage, d.ShowUsage = usage, true
	}
	if sc.alerts {
		if d.SecurityAlerts, err = q.CountSecurityAlertsBetween(ctx, sqlc.CountSecurityAlertsBetweenParams{PeriodFrom: p.from, PeriodUntil: p.until}); err != nil {
			return nil, fmt.Errorf("notify: summary security alerts: %w", err)
		}
		d.ShowAlerts = true
	}
	in.fill(d, s.o.CachedFreeSpace)
	return d, nil
}

// summaryInput are the rows of one summary.
type summaryInput struct {
	jobs   []sqlc.ListSummaryJobsRow
	runs   []sqlc.SummaryRunCountsRow
	tests  []sqlc.SummaryTestRestoreCountsRow
	missed []sqlc.ListJobMissedStateRow
	holds  []sqlc.ListRetentionHoldsForJobsRow
	active []uuid.UUID
	usage  []sqlc.DestinationUsageRow
}

// fill computes the totals and the jobs needing attention (pure apart
// from the cached free space).
func (in *summaryInput) fill(d *summaryData, free func(uuid.UUID) (int64, bool)) {
	type perJob struct {
		failed, partial, ok, testsFailed int64
	}
	by := map[uuid.UUID]*perJob{}
	get := func(id uuid.UUID) *perJob {
		if by[id] == nil {
			by[id] = &perJob{}
		}
		return by[id]
	}
	for _, r := range in.runs {
		d.Runs[r.Status] += r.Runs
		j := get(r.JobID)
		switch r.Status {
		case string(sqlc.RunStatusFailed):
			j.failed += r.Runs
		case string(sqlc.RunStatusPartial):
			j.partial += r.Runs
			j.ok += r.Runs
		case string(sqlc.RunStatusSuccess):
			j.ok += r.Runs
		}
	}
	for _, t := range in.tests {
		d.TestRestores[t.Status] += t.TestRestores
		if t.Status == string(sqlc.TestRestoreStatusFailed) || t.Status == string(sqlc.TestRestoreStatusError) {
			get(t.JobID).testsFailed += t.TestRestores
		}
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
		pj := get(j.ID)
		a := attentionJob{Name: notify.DisplayName(j.Name), Failed: pj.failed, Partial: pj.partial, TestRestoresFailed: pj.testsFailed}
		if since, ok := missed[j.ID]; ok {
			a.MissedSince = &since
		}
		if h := holds[j.ID]; h != nil {
			a.HeldRuns, a.HoldDestinations = h.runs, h.dests
		}
		a.NoSuccess = pj.ok == 0 && a.MissedSince == nil && !active[j.ID] && scheduledIn(j, d.Period.from, d.Period.until)
		if a.Failed > 0 || a.Partial > 0 || a.MissedSince != nil || a.NoSuccess || a.HeldRuns > 0 || a.TestRestoresFailed > 0 {
			d.Attention = append(d.Attention, a)
		}
	}
	for _, u := range in.usage {
		du := destinationUsage{Name: notify.DisplayName(u.Name), Bytes: u.StoredBytes, Backups: u.BackupCount}
		if free != nil {
			if f, ok := free(u.ID); ok {
				du.Free = &f
			}
		}
		d.Destinations = append(d.Destinations, du)
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

// --- rendering --------------------------------------------------------------------

func clampRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

func pick(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}

func plural(n int64, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// summaryTime is a local time of the summary ("Mon 3 Nov 2026, 08:00").
func summaryTime(t time.Time, loc *time.Location) string {
	return t.In(loc).Format("Mon 2 Jan 2006, 15:04")
}

// formatBytes formats a size with binary (IEC) units.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	v, i := float64(n)/unit, 0
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

var frequencyLabels = map[Frequency]string{
	Daily: "Daily", EveryOtherDay: "Every-other-day", Weekly: "Weekly", Monthly: "Monthly",
}

// headline is "All 5 backup jobs ran fine" or "Needs attention: 2 of 5 jobs".
func (d *summaryData) headline() string {
	switch {
	case d.Jobs == 0:
		return "No backup jobs to summarize"
	case len(d.Attention) == 0 && d.Jobs == 1:
		return "Your backup job ran fine"
	case len(d.Attention) == 0:
		return fmt.Sprintf("All %d backup jobs ran fine", d.Jobs)
	default:
		return fmt.Sprintf("Needs attention: %d of %d jobs", len(d.Attention), d.Jobs)
	}
}

func (d *summaryData) title() string {
	label := frequencyLabels[d.Period.frequency]
	if label == "" {
		label = "Periodic"
	}
	prefix := label + " summary"
	if d.Period.preview {
		prefix = "Summary preview"
	}
	return prefix + ": " + d.headline()
}

var runStatusOrder = []struct{ status, label string }{
	{string(sqlc.RunStatusSuccess), "succeeded"},
	{string(sqlc.RunStatusPartial), "partially failed"},
	{string(sqlc.RunStatusFailed), "failed"},
	{string(sqlc.RunStatusCanceled), "canceled"},
	{string(sqlc.RunStatusSkipped), "skipped"},
}

var testStatusOrder = []struct{ status, label string }{
	{string(sqlc.TestRestoreStatusPassed), "passed"},
	{string(sqlc.TestRestoreStatusWarnings), "passed with warnings"},
	{string(sqlc.TestRestoreStatusFailed), "failed"},
	{string(sqlc.TestRestoreStatusError), "could not run"},
	{string(sqlc.TestRestoreStatusCanceled), "canceled"},
}

func countsSentence(counts map[string]int64, order []struct{ status, label string }) (int64, string) {
	var total int64
	var parts []string
	for _, o := range order {
		if n := counts[o.status]; n > 0 {
			total += n
			parts = append(parts, fmt.Sprintf("%d %s", n, o.label))
		}
	}
	return total, strings.Join(parts, ", ")
}

func (d *summaryData) runsSentence() string {
	total, parts := countsSentence(d.Runs, runStatusOrder)
	if total == 0 {
		return "Backups: no backup finished in this period."
	}
	return fmt.Sprintf("Backups: %s finished (%s).", plural(total, "run", "runs"), parts)
}

func (d *summaryData) testsSentence() string {
	total, parts := countsSentence(d.TestRestores, testStatusOrder)
	if total == 0 {
		return "Test restores: none in this period."
	}
	return fmt.Sprintf("Test restores: %s (%s).", plural(total, "test restore", "test restores"), parts)
}

// reasons explains why a job needs attention (fixed text and numbers).
func (a *attentionJob) reasons(loc *time.Location) string {
	var r []string
	if a.Failed > 0 {
		r = append(r, plural(a.Failed, "failed backup", "failed backups"))
	}
	if a.Partial > 0 {
		r = append(r, plural(a.Partial, "partially failed backup", "partially failed backups"))
	}
	if a.MissedSince != nil {
		r = append(r, "backups missed since "+summaryTime(*a.MissedSince, loc))
	}
	if a.NoSuccess {
		r = append(r, "no successful backup in this period")
	}
	if a.HeldRuns > 0 {
		r = append(r, fmt.Sprintf("retention keeps %s on %s", plural(a.HeldRuns, "incomplete backup", "incomplete backups"),
			plural(int64(a.HoldDestinations), "destination", "destinations")))
	}
	if a.TestRestoresFailed > 0 {
		r = append(r, plural(a.TestRestoresFailed, "failed test restore", "failed test restores"))
	}
	return strings.Join(r, "; ")
}

// buildSummary renders d (pure): the inbox title and body (counts only,
// no job or destination names, so the row says nothing a later role
// change would hide) and the e-mail (email.Render limits: at most 20
// paragraphs and 30 details).
func buildSummary(d *summaryData, dashboard string) notify.SummaryContent {
	p := d.Period
	title := d.title()
	period := fmt.Sprintf("This summary covers %s to %s (%s).", summaryTime(p.from, p.loc), summaryTime(p.until, p.loc), p.zone)
	paragraphs := []string{}
	if p.preview {
		paragraphs = append(paragraphs, "This is a preview you requested: it covers the current period so far.")
	}
	paragraphs = append(paragraphs, period)
	one := len(d.Attention) == 1
	switch {
	case len(d.Attention) == 0 && d.Jobs == 1:
		paragraphs = append(paragraphs, "The backup job you can see ran without problems.")
	case len(d.Attention) == 0:
		paragraphs = append(paragraphs, fmt.Sprintf("All %d backup jobs you can see ran without problems.", d.Jobs))
	default:
		paragraphs = append(paragraphs, fmt.Sprintf("%s of the %s you can see %s attention; %s listed below.",
			plural(int64(len(d.Attention)), "job", "jobs"), plural(int64(d.Jobs), "backup job", "backup jobs"),
			pick(one, "needs", "need"), pick(one, "it is", "they are")))
	}
	paragraphs = append(paragraphs, d.runsSentence(), d.testsSentence())
	if d.ShowAlerts {
		if d.SecurityAlerts == 0 {
			paragraphs = append(paragraphs, "Security alerts: none in this period.")
		} else {
			paragraphs = append(paragraphs, fmt.Sprintf("Security alerts: %s in this period; review them in bekup.", plural(d.SecurityAlerts, "alert", "alerts")))
		}
	}
	var details []email.Detail
	shown := min(len(d.Attention), maxSummaryAttention)
	for i := range shown {
		a := &d.Attention[i]
		details = append(details, email.Detail{Label: a.Name, Value: a.reasons(p.loc)})
	}
	if more := len(d.Attention) - shown; more > 0 {
		paragraphs = append(paragraphs, fmt.Sprintf("And %d more %s attention; see the dashboard.", more, pick(more == 1, "job needs", "jobs need")))
	}
	if d.ShowUsage {
		if len(d.Destinations) == 0 {
			paragraphs = append(paragraphs, "Destination usage: no destinations.")
		} else {
			paragraphs = append(paragraphs, "Destination usage (backups bekup keeps on each destination) is listed below.")
		}
		n := min(len(d.Destinations), maxSummaryDestinations)
		for i := range n {
			du := &d.Destinations[i]
			v := fmt.Sprintf("%s in %s", formatBytes(du.Bytes), plural(du.Backups, "backup", "backups"))
			if du.Free != nil {
				v += fmt.Sprintf(", %s free", formatBytes(*du.Free))
			}
			details = append(details, email.Detail{Label: "Storage: " + du.Name, Value: v})
		}
		if more := len(d.Destinations) - n; more > 0 {
			paragraphs = append(paragraphs, fmt.Sprintf("%d more %s not listed.", more, pick(more == 1, "destination is", "destinations are")))
		}
	}
	paragraphs = append(paragraphs, fmt.Sprintf("Times are in %s.", p.zone),
		"Change how often you get this summary, or turn it off, in your notification settings.")

	_, runParts := countsSentence(d.Runs, runStatusOrder)
	body := period
	if runParts != "" {
		body += " Backups: " + runParts + "."
	} else {
		body += " No backup finished."
	}
	if _, tp := countsSentence(d.TestRestores, testStatusOrder); tp != "" {
		body += " Test restores: " + tp + "."
	}
	if len(d.Attention) > 0 {
		body += fmt.Sprintf(" %s %s attention: see the dashboard.", plural(int64(len(d.Attention)), "job", "jobs"), pick(one, "needs", "need"))
	}
	if d.ShowAlerts && d.SecurityAlerts > 0 {
		body += fmt.Sprintf(" Security alerts: %d.", d.SecurityAlerts)
	}
	return notify.SummaryContent{
		Title: clampRunes(title, 200),
		Body:  clampRunes(body, 1000),
		Notice: email.Notice{
			Subject: "[bekup] " + clampRunes(title, 180), Heading: clampRunes(title, 200),
			Paragraphs: paragraphs, Details: details,
			Action: &email.Action{Label: "Open the dashboard", URL: dashboard},
		},
	}
}
