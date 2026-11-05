package summary

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/notify/email"
)

func period(t *testing.T, preview bool) summaryPeriod {
	t.Helper()
	loc := mustZone(t, "Europe/Amsterdam")
	return summaryPeriod{
		from: at(t, "2026-11-02T07:00:00Z"), until: at(t, "2026-11-09T07:00:00Z"),
		loc: loc, zone: "Europe/Amsterdam", frequency: Weekly, preview: preview,
	}
}

func render(t *testing.T, c notify.SummaryContent) email.Content {
	t.Helper()
	out, err := email.Render(c.Notice)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return out
}

func TestSummaryAllFine(t *testing.T) {
	t.Parallel()
	d := &summaryData{
		Period: period(t, false), Jobs: 5,
		Runs:         map[string]int64{"success": 35},
		TestRestores: map[string]int64{"passed": 2},
	}
	c := buildSummary(d, "https://bekup.example/")
	if c.Title != "Weekly summary: All 5 backup jobs ran fine" {
		t.Fatalf("title = %q", c.Title)
	}
	if c.Notice.Subject != "[bekup] Weekly summary: All 5 backup jobs ran fine" || len(c.Notice.Details) != 0 {
		t.Fatalf("notice = %+v", c.Notice)
	}
	out := render(t, c)
	for _, want := range []string{
		"This summary covers Mon 2 Nov 2026, 08:00 to Mon 9 Nov 2026, 08:00 (Europe/Amsterdam).",
		"All 5 backup jobs you can see ran without problems.",
		"Backups: 35 runs finished (35 succeeded).",
		"Test restores: 2 test restores (2 passed).",
		"Times are in Europe/Amsterdam.",
		"https://bekup.example/",
	} {
		if !strings.Contains(out.Text, want) {
			t.Errorf("text lacks %q:\n%s", want, out.Text)
		}
	}
	if strings.Contains(out.Text, "Security alerts") || strings.Contains(out.Text, "Storage:") {
		t.Error("staff parts in a summary without them")
	}
	if strings.Contains(out.HTML, "<img") || strings.Contains(strings.ToLower(out.HTML), "<script") {
		t.Error("the e-mail must have no images or scripts")
	}
	if !strings.HasPrefix(c.Body, "This summary covers") || !strings.Contains(c.Body, "Backups: 35 succeeded.") {
		t.Fatalf("body = %q", c.Body)
	}
	one := buildSummary(&summaryData{Period: period(t, false), Jobs: 1, Runs: map[string]int64{}, TestRestores: map[string]int64{}}, "https://b.example/")
	if one.Title != "Weekly summary: Your backup job ran fine" {
		t.Fatalf("title = %q", one.Title)
	}
	if !strings.Contains(render(t, one).Text, "Backups: no backup finished in this period.") {
		t.Fatal("no-runs sentence missing")
	}
	prev := buildSummary(&summaryData{Period: period(t, true), Jobs: 2, Runs: map[string]int64{}, TestRestores: map[string]int64{}}, "https://b.example/")
	if prev.Title != "Summary preview: All 2 backup jobs ran fine" || !strings.Contains(render(t, prev).Text, "This is a preview") {
		t.Fatalf("preview = %q", prev.Title)
	}
}

func job(name string, enabled bool, schedule string, created time.Time) sqlc.ListSummaryJobsRow {
	return sqlc.ListSummaryJobsRow{ID: uuid.New(), Name: name, Enabled: enabled, Schedule: json.RawMessage(schedule), CreatedAt: created}
}

const daily3 = `{"kind":"interval","every":1,"unit":"days","anchor":"2026-01-01T03:00","timezone":"UTC"}`

// TestSummaryAttention: every reason a job needs attention, and the jobs
// that do not.
func TestSummaryAttention(t *testing.T) {
	t.Parallel()
	p := period(t, false)
	old := at(t, "2026-01-01T00:00:00Z")
	failed := job("Billing", true, daily3, old)
	missed := job("CRM", true, daily3, old)
	silent := job("Docs", true, daily3, old)
	held := job("Events", true, daily3, old)
	tested := job("Forms", true, daily3, old)
	fine := job("Garden", true, daily3, old)
	manual := job("Handbook", true, `{"kind":"manual"}`, old)
	disabled := job("Inventory", false, daily3, old)
	running := job("Journal", true, daily3, old)
	fresh := job("Kiosk", true, daily3, at(t, "2026-11-09T06:50:00Z"))
	since := at(t, "2026-11-05T03:00:00Z")
	in := summaryInput{
		jobs: []sqlc.ListSummaryJobsRow{failed, missed, silent, held, tested, fine, manual, disabled, running, fresh},
		runs: []sqlc.SummaryRunCountsRow{
			{JobID: failed.ID, Status: "failed", Runs: 2}, {JobID: failed.ID, Status: "partial", Runs: 1},
			{JobID: held.ID, Status: "success", Runs: 7}, {JobID: tested.ID, Status: "success", Runs: 7},
			{JobID: fine.ID, Status: "success", Runs: 7}, {JobID: fine.ID, Status: "canceled", Runs: 1},
		},
		tests: []sqlc.SummaryTestRestoreCountsRow{
			{JobID: tested.ID, Status: "failed", TestRestores: 1}, {JobID: tested.ID, Status: "error", TestRestores: 1},
			{JobID: fine.ID, Status: "passed", TestRestores: 3},
		},
		missed: []sqlc.ListJobMissedStateRow{{JobID: missed.ID, MissedSince: since}},
		holds: []sqlc.ListRetentionHoldsForJobsRow{
			{JobID: held.ID, HeldRuns: 2}, {JobID: held.ID, HeldRuns: 1},
		},
		active: []uuid.UUID{running.ID},
	}
	d := &summaryData{Period: p, Jobs: len(in.jobs), Runs: map[string]int64{}, TestRestores: map[string]int64{}}
	in.fill(d, nil)
	names := make([]string, 0, len(d.Attention))
	for _, a := range d.Attention {
		names = append(names, a.Name)
	}
	if got := strings.Join(names, ","); got != "Billing,CRM,Docs,Events,Forms" {
		t.Fatalf("attention = %s", got)
	}
	if d.Runs["success"] != 21 || d.Runs["failed"] != 2 || d.Runs["partial"] != 1 || d.Runs["canceled"] != 1 || d.TestRestores["passed"] != 3 {
		t.Fatalf("totals = %v %v", d.Runs, d.TestRestores)
	}
	c := buildSummary(d, "https://bekup.example/")
	if c.Title != "Weekly summary: Needs attention: 5 of 10 jobs" {
		t.Fatalf("title = %q", c.Title)
	}
	want := map[string]string{
		"Billing": "2 failed backups; 1 partially failed backup",
		"CRM":     "backups missed since Thu 5 Nov 2026, 04:00",
		"Docs":    "no successful backup in this period",
		"Events":  "retention keeps 3 incomplete backups on 2 destinations",
		"Forms":   "2 failed test restores",
	}
	if len(c.Notice.Details) != len(want) {
		t.Fatalf("details = %+v", c.Notice.Details)
	}
	for _, dt := range c.Notice.Details {
		if want[dt.Label] != dt.Value {
			t.Errorf("%s: %q, want %q", dt.Label, dt.Value, want[dt.Label])
		}
	}
	out := render(t, c)
	for _, s := range []string{
		"5 jobs of the 10 backup jobs you can see need attention; they are listed below.",
		"Backups: 25 runs finished (21 succeeded, 1 partially failed, 2 failed, 1 canceled).",
		"Test restores: 5 test restores (3 passed, 1 failed, 1 could not run).",
	} {
		if !strings.Contains(out.Text, s) {
			t.Errorf("text lacks %q:\n%s", s, out.Text)
		}
	}
	// The inbox row carries counts, never job names.
	for _, n := range names {
		if strings.Contains(c.Body, n) {
			t.Fatalf("inbox body names job %s: %q", n, c.Body)
		}
	}
	if !strings.Contains(c.Body, "5 jobs need attention") {
		t.Fatalf("body = %q", c.Body)
	}
}

// TestSummaryNoSuccessRule: only enabled, scheduled jobs with an
// occurrence in the period (not at its very end, not before they existed)
// are judged.
func TestSummaryNoSuccessRule(t *testing.T) {
	t.Parallel()
	from, until := at(t, "2026-11-08T00:00:00Z"), at(t, "2026-11-09T00:00:00Z")
	old := at(t, "2026-01-01T00:00:00Z")
	weeklyFri := `{"kind":"interval","every":1,"unit":"weeks","anchor":"2026-01-02T03:00","weekday":"friday","timezone":"UTC"}`
	lateDaily := `{"kind":"interval","every":1,"unit":"days","anchor":"2026-01-01T23:50","timezone":"UTC"}`
	cases := []struct {
		name string
		j    sqlc.ListSummaryJobsRow
		want bool
	}{
		{"daily", job("a", true, daily3, old), true},
		{"weekly, not this day", job("b", true, weeklyFri, old), false},
		{"manual", job("c", true, `{"kind":"manual"}`, old), false},
		{"disabled", job("d", false, daily3, old), false},
		{"created after the occurrence", job("e", true, daily3, at(t, "2026-11-08T04:00:00Z")), false},
		{"occurrence within the grace", job("f", true, lateDaily, old), false},
		{"broken schedule", job("g", true, `{"kind":"interval"}`, old), false},
		{"not JSON", job("h", true, `nope`, old), false},
	}
	for _, c := range cases {
		if got := scheduledIn(c.j, from, until); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

// TestSummaryTruncationAndLimits: at most 20 jobs and 8 destinations are
// listed (the rest counted), and the e-mail stays within email.Render's
// limits (20 paragraphs, 30 details) with the longest names.
func TestSummaryTruncationAndLimits(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("ü", 99)
	d := &summaryData{
		Period: period(t, true), Jobs: 40, Runs: map[string]int64{"failed": 25, "success": 3, "partial": 2, "canceled": 1, "skipped": 1},
		TestRestores: map[string]int64{"passed": 1, "warnings": 1, "failed": 1, "error": 1, "canceled": 1},
		ShowUsage:    true, ShowAlerts: true, SecurityAlerts: 3,
	}
	since := at(t, "2026-11-03T03:00:00Z")
	for i := range 25 {
		d.Attention = append(d.Attention, attentionJob{
			Name: fmt.Sprintf("%s %02d", long, i), Failed: 3, Partial: 2, MissedSince: &since, NoSuccess: true,
			HeldRuns: 4, HoldDestinations: 2, TestRestoresFailed: 1,
		})
	}
	free := int64(5 << 30)
	for i := range 12 {
		du := destinationUsage{Name: fmt.Sprintf("%s d%02d", long, i), Bytes: int64(i) << 30, Backups: int64(i)}
		if i%2 == 0 {
			du.Free = &free
		}
		d.Destinations = append(d.Destinations, du)
	}
	c := buildSummary(d, "https://bekup.example/")
	if len(c.Notice.Paragraphs) > 20 || len(c.Notice.Details) > 30 {
		t.Fatalf("paragraphs %d, details %d", len(c.Notice.Paragraphs), len(c.Notice.Details))
	}
	if len(c.Notice.Details) != maxSummaryAttention+maxSummaryDestinations {
		t.Fatalf("details = %d", len(c.Notice.Details))
	}
	out := render(t, c)
	for _, s := range []string{
		"And 5 more jobs need attention; see the dashboard.",
		"4 more destinations are not listed.",
		"Security alerts: 3 alerts in this period; review them in bekup.",
		"Storage: " + long + " d02: 2.0 GiB in 2 backups, 5.0 GiB free",
		"Storage: " + long + " d01: 1.0 GiB in 1 backup",
	} {
		if !strings.Contains(out.Text, s) {
			t.Errorf("text lacks %q", s)
		}
	}
	if strings.Contains(out.Text, long+" 20") || strings.Contains(out.Text, long+" d08") {
		t.Error("truncated entries are listed")
	}
	if len([]rune(c.Title)) > 200 || len([]rune(c.Body)) > 1000 {
		t.Fatalf("inbox title/body too long: %d/%d", len([]rune(c.Title)), len([]rune(c.Body)))
	}
}

// TestSummaryEventDetail: the event detail passes notify's detail rules,
// the dedup key its key rule, and the period reads back.
func TestSummaryEventDetail(t *testing.T) {
	t.Parallel()
	from, until := at(t, "2026-11-02T07:00:00Z"), at(t, "2026-11-09T07:00:00Z")
	d := eventDetail(from, until, "America/Argentina/ComodRivadavia", EveryOtherDay, true)
	raw, err := notify.ValidateDetail(d)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := periodOf(raw)
	if !ok || !p.from.Equal(from) || !p.until.Equal(until) || p.frequency != EveryOtherDay || !p.preview || p.zone != "America/Argentina/ComodRivadavia" {
		t.Fatalf("period = %+v %v", p, ok)
	}
	user := uuid.New()
	if k := dedupKey(user, until); !regexp.MustCompile(`^[a-z][a-z0-9_.:-]*$`).MatchString(k) || len(k) > 200 || k != "summary:"+user.String()+":1794207600" {
		t.Fatalf("dedup key = %q", k)
	}
	// A broken detail (or an unknown zone) renders without failing.
	if _, ok := periodOf([]byte(`{"period_start":"x"}`)); ok {
		t.Fatal("broken detail accepted")
	}
	raw2, err := notify.ValidateDetail(eventDetail(from, until, "Nowhere/Zone", Weekly, false))
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := periodOf(raw2); !ok || p.zone != "UTC" {
		t.Fatalf("unknown zone = %+v", p)
	}
}

type noOwners struct{}

func (noOwners) IsJobOwner(context.Context, uuid.UUID, uuid.UUID) (bool, error) { return false, nil }
func (noOwners) BacksUpDatabase(context.Context, uuid.UUID, uuid.UUID, string) (bool, error) {
	return false, nil
}
func (noOwners) UsesConnection(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return false, nil
}

// TestSummaryScope: staff see every job and the destination usage, only
// admins the security alert count, backup owners their own jobs only.
func TestSummaryScope(t *testing.T) {
	t.Parallel()
	s := &Service{o: Options{Policy: authz.New(authz.Options{Ownership: noOwners{}})}}
	for _, c := range []struct {
		role                    sqlc.UserRole
		all, usage, alerts, any bool
	}{
		{sqlc.UserRoleAdmin, true, true, true, true},
		{sqlc.UserRoleMaintainer, true, true, false, true},
		{sqlc.UserRoleUser, false, false, false, true},
		{sqlc.UserRole("bogus"), false, false, false, false},
	} {
		sc, err := s.scopeOf(t.Context(), uuid.New(), c.role)
		if err != nil {
			t.Fatal(err)
		}
		if sc.scopeAll != c.all || sc.usage != c.usage || sc.alerts != c.alerts || sc.any != c.any {
			t.Errorf("%s: %+v", c.role, sc)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	t.Parallel()
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 1536: "1.5 KiB", 5 << 30: "5.0 GiB", 3 << 40: "3.0 TiB"} {
		if got := formatBytes(n); got != want {
			t.Errorf("%d = %q, want %q", n, got, want)
		}
	}
}
