package summary

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/destinations"
	"github.com/jlentink/bekup/internal/i18n"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/notify/email"
	"github.com/jlentink/bekup/internal/riskaccept"
	"github.com/jlentink/bekup/internal/storage"
)

var update = flag.Bool("update", false, "rewrite the golden summary e-mails in testdata/")

func period(t *testing.T, preview bool) summaryPeriod {
	t.Helper()
	loc := mustZone(t, "Europe/Amsterdam")
	return summaryPeriod{
		from: at(t, "2026-11-02T07:00:00Z"), until: at(t, "2026-11-09T07:00:00Z"),
		loc: loc, zone: "Europe/Amsterdam", frequency: Weekly, preview: preview,
	}
}

func dailyPeriod(t *testing.T) summaryPeriod {
	t.Helper()
	return summaryPeriod{
		from: at(t, "2026-11-07T07:00:00Z"), until: at(t, "2026-11-08T07:00:00Z"),
		loc: mustZone(t, "Europe/Amsterdam"), zone: "Europe/Amsterdam", frequency: Daily,
	}
}

// checkedSpace is a stored available answer with a known free space,
// checked at at.
func checkedSpace(free int64, low destinations.LowCause, at time.Time) destinations.StoredSpace {
	return destinations.StoredSpace{
		Status: destinations.SpaceAvailable, Space: storage.Space{Free: free, FreeKnown: true}, Low: low, CheckedAt: at,
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

func job(name string, enabled bool, schedule string, created time.Time) sqlc.ListSummaryJobsRow {
	return sqlc.ListSummaryJobsRow{ID: uuid.New(), Name: name, Enabled: enabled, Schedule: json.RawMessage(schedule), CreatedAt: created}
}

const daily3 = `{"kind":"interval","every":1,"unit":"days","anchor":"2026-01-01T03:00","timezone":"UTC"}`

func newData(p summaryPeriod, in *summaryInput) *summaryData {
	d := &summaryData{Period: p, Jobs: len(in.jobs), Runs: map[string]int64{}, TestRestores: map[string]int64{}, Restores: map[string]int64{}}
	in.fill(d)
	return d
}

func activity(id uuid.UUID, ok, failed, bytes int64, last time.Time, code string) sqlc.SummaryJobActivityRow {
	return sqlc.SummaryJobActivityRow{JobID: id, Succeeded: ok, Failed: failed, BytesStored: bytes, LastFinishedAt: last, LastFailureCode: code}
}

// allOK is a summary of three good jobs.
func allOK(t *testing.T, p summaryPeriod) *summaryData {
	t.Helper()
	old := at(t, "2026-01-01T00:00:00Z")
	a, b, c := job("Billing", true, daily3, old), job("CRM", true, daily3, old), job("Docs", true, daily3, old)
	last := p.until.Add(-4 * time.Hour)
	in := summaryInput{
		jobs: []sqlc.ListSummaryJobsRow{a, b, c},
		activity: []sqlc.SummaryJobActivityRow{
			activity(a.ID, 7, 0, 7<<30, last, ""), activity(b.ID, 7, 0, 3<<20, last, ""), activity(c.ID, 1, 0, 900, last, ""),
		},
		stored: []sqlc.JobStorageTotalsRow{{JobID: a.ID, StoredBytes: 50 << 30}, {JobID: b.ID, StoredBytes: 21 << 20}},
		tests:  []sqlc.SummaryTestRestoreCountsRow{{JobID: a.ID, Status: "passed", TestRestores: 2}},
	}
	return newData(p, &in)
}

func TestSummaryAllOK(t *testing.T) {
	t.Parallel()
	c := buildSummary(allOK(t, period(t, false)), "https://bekup.example/")
	if c.Title != "Weekly summary: all 3 jobs OK" || c.Notice.Subject != "[bekup] Weekly summary: all 3 jobs OK" {
		t.Fatalf("title %q, subject %q", c.Title, c.Notice.Subject)
	}
	if b := c.Notice.Banner; b == nil || b.Level != email.LevelOK || b.Word != "All OK" || b.Text != "All 3 backup jobs ran fine in the last 7 days." {
		t.Fatalf("banner = %+v", c.Notice.Banner)
	}
	if c.Notice.Preheader != "All OK · 3 OK · the last 7 days" {
		t.Fatalf("preheader = %q", c.Notice.Preheader)
	}
	out := render(t, c)
	for _, want := range []string{
		"All OK: All 3 backup jobs ran fine in the last 7 days.",
		"Period: Mon 2 Nov 2026, 08:00 – Mon 9 Nov 2026, 08:00 (Europe/Amsterdam).",
		"Backups: 15 runs finished (15 succeeded).",
		"Jobs OK: 3/3  |  Failed: 0  |  Written: 7.0 GiB  |  Test restores: 2/2",
		"   OK      Billing      7/0  9 Nov 04:00  7.0 GiB  50.0 GiB",
		"   OK      CRM          7/0  9 Nov 04:00  3.0 MiB  21.0 MiB",
		"   OK      Docs         1/0  9 Nov 04:00    900 B         –",
		"Passed  Test restores      2",
		"Times are in Europe/Amsterdam.",
		"https://bekup.example/",
	} {
		if !strings.Contains(out.Text, want) {
			t.Errorf("text lacks %q:\n%s", want, out.Text)
		}
	}
	if strings.Contains(out.Text, ">>") || strings.Contains(out.Text, "Storage") || strings.Contains(out.Text, "Security") {
		t.Errorf("unexpected parts:\n%s", out.Text)
	}
	if strings.Contains(out.HTML, "<img") || strings.Contains(strings.ToLower(out.HTML), "<script") {
		t.Error("the e-mail must have no images or scripts")
	}
	if !strings.HasPrefix(c.Body, "This summary covers") || !strings.Contains(c.Body, "Jobs: 3 OK.") || !strings.Contains(c.Body, "Backups: 15 succeeded.") {
		t.Fatalf("body = %q", c.Body)
	}

	one := buildSummary(newData(period(t, false), &summaryInput{jobs: []sqlc.ListSummaryJobsRow{job("a", true, `{"kind":"manual"}`, time.Time{})}}), "https://b.example/")
	if one.Title != "Weekly summary: no problems" || one.Notice.Banner.Text != "No problems in the last 7 days (0 OK, 1 without a run)." {
		t.Fatalf("one = %q / %+v", one.Title, one.Notice.Banner)
	}
	prev := buildSummary(allOK(t, period(t, true)), "https://b.example/")
	if prev.Title != "Summary preview: all 3 jobs OK" || !strings.Contains(render(t, prev).Text, "This is a preview you requested: it covers the last 7 days up to now.") {
		t.Fatalf("preview = %q", prev.Title)
	}
}

// mixedInput has every status of the rule.
func mixedInput(t *testing.T, p summaryPeriod) *summaryInput {
	t.Helper()
	old := at(t, "2026-01-01T00:00:00Z")
	last := p.until.Add(-time.Hour)
	failed := job("Billing", true, daily3, old)
	missed := job("CRM", true, daily3, old)
	silent := job("Docs", true, daily3, old)
	held := job("Events", true, daily3, old)
	tested := job("Forms", true, daily3, old)
	fine := job("Garden", true, daily3, old)
	manual := job("Handbook", true, `{"kind":"manual"}`, old)
	disabled := job("Inventory", false, daily3, old)
	running := job("Journal", true, daily3, old)
	fresh := job("Kiosk", true, daily3, p.until.Add(-10*time.Minute))
	partial := job("Ledger", true, daily3, old)
	warned := job("Mail", true, daily3, old)
	since := at(t, "2026-11-05T03:00:00Z")
	pa := activity(partial.ID, 1, 0, 1<<30, last, "")
	pa.Partial, pa.Succeeded = 1, 0
	wa := activity(warned.ID, 1, 0, 2<<30, last, "")
	wa.WithWarnings = 1
	fa := activity(failed.ID, 0, 2, 0, last, "target_unreachable")
	fa.Partial = 1
	return &summaryInput{
		jobs: []sqlc.ListSummaryJobsRow{failed, missed, silent, held, tested, fine, manual, disabled, running, fresh, partial, warned},
		activity: []sqlc.SummaryJobActivityRow{
			fa, activity(held.ID, 7, 0, 1<<30, last, ""), activity(tested.ID, 7, 0, 1<<30, last, ""),
			activity(fine.ID, 7, 0, 5<<30, last, ""), pa, wa,
		},
		tests: []sqlc.SummaryTestRestoreCountsRow{
			{JobID: tested.ID, Status: "failed", TestRestores: 1}, {JobID: tested.ID, Status: "error", TestRestores: 1},
			{JobID: fine.ID, Status: "passed", TestRestores: 3},
		},
		restores: []sqlc.SummaryRestoreCountsRow{{Status: "succeeded", Restores: 2}, {Status: "failed", Restores: 1}},
		missed:   []sqlc.ListJobMissedStateRow{{JobID: missed.ID, MissedSince: since}},
		holds:    []sqlc.ListRetentionHoldsForJobsRow{{JobID: held.ID, HeldRuns: 2}, {JobID: held.ID, HeldRuns: 1}},
		active:   []uuid.UUID{running.ID},
	}
}

// TestSummaryStatusRule: one status per job, worst first, with fixed
// reasons (error-code texts, never server output).
func TestSummaryStatusRule(t *testing.T) {
	t.Parallel()
	d := newData(period(t, false), mixedInput(t, period(t, false)))
	got := make([]string, 0, len(d.JobRows))
	for _, r := range d.JobRows {
		got = append(got, r.Name+"="+r.word(&d.Period))
	}
	want := "Billing=Failed,Forms=Failed,CRM=Missed,Docs=Missed,Ledger=Partial,Mail=Warnings,Events=Held,Garden=OK," +
		"Handbook=No run,Journal=No run,Kiosk=No run,Inventory=Disabled"
	if strings.Join(got, ",") != want {
		t.Fatalf("rows = %s", strings.Join(got, ","))
	}
	c := buildSummary(d, "https://bekup.example/")
	if c.Notice.Subject != "[bekup] Weekly summary: 2 FAILED, 2 missed" || c.Title != "Weekly summary: 2 failed, 2 missed, 2 partial, 1 held, 1 OK" {
		t.Fatalf("subject %q, title %q", c.Notice.Subject, c.Title)
	}
	b := c.Notice.Banner
	if b.Level != email.LevelFailure || b.Word != "2 failed" ||
		b.Text != "2 of 12 jobs failed, 2 jobs missed their backups and 3 more need attention in the last 7 days." {
		t.Fatalf("banner = %+v", b)
	}
	if c.Notice.Preheader != "2 failed · 2 missed · 2 partial · 1 held · 1 OK · the last 7 days" {
		t.Fatalf("preheader = %q", c.Notice.Preheader)
	}
	out := render(t, c)
	for _, s := range []string{
		"!! 2 failed: 2 of 12 jobs failed",
		">> Failed    Billing        1/2  9 Nov 07:00      0 B       –\n             target unreachable (2 failed runs); 1 partially failed run\n",
		">> Failed    Forms          7/0  9 Nov 07:00  1.0 GiB       –\n             2 test restores failed\n",
		">> Missed    CRM            0/0            –        –       –\n             missed since Thu 5 Nov 2026, 04:00\n",
		">> Missed    Docs           0/0            –        –       –\n             no successful backup in this period\n",
		">> Partial   Ledger",
		">> Warnings  Mail",
		">> Held      Events         7/0  9 Nov 07:00  1.0 GiB       –\n             retention keeps 3 incomplete backups on 2 destinations\n",
		"   OK        Garden",
		"   Disabled  Inventory",
		"Succeeded      Restores           2",
		">> Failed         Restores           1",
		">> Could not run  Test restores      1",
		"Jobs OK: 1/12  |  Failed/missed: 2/2",
	} {
		if !strings.Contains(out.Text, s) {
			t.Errorf("text lacks %q:\n%s", s, out.Text)
		}
	}
	// The inbox row carries counts, never job names.
	for _, r := range d.JobRows {
		if strings.Contains(c.Body, r.Name) {
			t.Fatalf("inbox body names job %s: %q", r.Name, c.Body)
		}
	}
	if !strings.Contains(c.Body, "7 jobs need attention") || !strings.Contains(c.Body, "Jobs: 2 failed, 2 missed, 2 partial, 1 held, 1 OK.") {
		t.Fatalf("body = %q", c.Body)
	}
	if d.overall().level != levelFailure {
		t.Fatal("level")
	}
}

func TestSummaryWarningLevels(t *testing.T) {
	t.Parallel()
	d := allOK(t, dailyPeriod(t))
	d.ShowUsage, d.ShowAlerts, d.SecurityAlerts = true, true, 2
	checked := d.Period.until.Add(-20 * time.Minute)
	d.Destinations = []destinationUsage{
		{Name: "nas", Bytes: 1 << 40, Backups: 9, Space: checkedSpace(10<<30, destinations.LowFraction, checked)},
		{Name: "s3", Type: destinations.TypeS3, Bytes: 5, Backups: 1},
	}
	c := buildSummary(d, "https://bekup.example/")
	o := d.overall()
	if o.level != levelWarning || o.word != "Attention" ||
		o.text != "All 3 backup jobs ran fine in the last 24 hours, but 1 destination is low on space and 2 security alerts were raised." {
		t.Fatalf("overall = %+v", o)
	}
	if c.Notice.Subject != "[bekup] Daily summary: needs attention" {
		t.Fatalf("subject %q", c.Notice.Subject)
	}
	out := render(t, c)
	for _, s := range []string{
		">> Low space    nas",
		"less than 10% free; checked 07:40\n",
		"   Not checked  s3",
		">> Review  2 security alerts in this period\n           review them in bekup\n",
	} {
		if !strings.Contains(out.Text, s) {
			t.Errorf("text lacks %q:\n%s", s, out.Text)
		}
	}
	d.SecurityAlerts = 0
	d.Destinations[0].Space.Low = destinations.LowNone
	if o := d.overall(); o.level != levelOK {
		t.Fatalf("overall = %+v", o)
	}
	if out := render(t, buildSummary(d, "https://bekup.example/")); !strings.Contains(out.Text, "   OK      No security alerts in this period") {
		t.Errorf("security row:\n%s", out.Text)
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

// bigInput has n jobs, the first `problems` of them failed (names sort
// after the good ones, so the problems must move up).
func bigInput(t *testing.T, p summaryPeriod, n, problems int, name func(int) string) *summaryInput {
	t.Helper()
	in := &summaryInput{}
	last := p.until.Add(-time.Hour)
	for i := range n {
		j := job(name(i), true, daily3, at(t, "2026-01-01T00:00:00Z"))
		in.jobs = append(in.jobs, j)
		if i < problems {
			in.activity = append(in.activity, activity(j.ID, 0, 1, 0, last, "dump_failed"))
		} else {
			in.activity = append(in.activity, activity(j.ID, 1, 0, 12<<30, last, ""))
		}
		in.stored = append(in.stored, sqlc.JobStorageTotalsRow{JobID: j.ID, StoredBytes: 300 << 30})
	}
	return in
}

// TestSummaryRowCap: at most 100 rows; every problem is listed (up to 99,
// then counted), the rest becomes one line.
func TestSummaryRowCap(t *testing.T) {
	t.Parallel()
	p := dailyPeriod(t)
	name := func(i int) string { return fmt.Sprintf("job-%03d", 999-i) }
	d := newData(p, bigInput(t, p, 150, 20, name))
	tbl := d.jobsTable(defaultListed(len(d.JobRows)))
	if len(tbl.Rows) != maxSummaryRows {
		t.Fatalf("rows = %d", len(tbl.Rows))
	}
	for i := range 20 {
		if tbl.Rows[i].Cells[0] != "Failed" || tbl.Rows[i].Note != "dump failed" {
			t.Fatalf("row %d = %+v", i, tbl.Rows[i])
		}
	}
	if last := tbl.Rows[99]; last.Cells[1] != "+51 more jobs without problems" || last.Level != email.LevelOK {
		t.Fatalf("last row = %+v", last)
	}
	d = newData(p, bigInput(t, p, 130, 120, name))
	tbl = d.jobsTable(defaultListed(len(d.JobRows)))
	if last := tbl.Rows[99]; len(tbl.Rows) != 100 || last.Cells[1] != "+31 more jobs, 21 of them need attention" || last.Level != email.LevelFailure {
		t.Fatalf("last row = %+v (%d rows)", last, len(tbl.Rows))
	}
	if _, err := email.Render(buildSummary(d, "https://bekup.example/").Notice); err != nil {
		t.Fatal(err)
	}
}

// TestSummaryScopeParts: staff-only tables appear only with their view
// operations (role user sees neither).
func TestSummaryScopeParts(t *testing.T) {
	t.Parallel()
	d := allOK(t, dailyPeriod(t))
	for _, tb := range buildSummary(d, "https://bekup.example/").Notice.Tables {
		if tb.Title == "Storage" || tb.Title == "Security" {
			t.Fatalf("staff table %s for a user", tb.Title)
		}
	}
	tables := func(d *summaryData) string {
		titles := make([]string, 0, 4)
		for _, tb := range buildSummary(d, "https://bekup.example/").Notice.Tables {
			titles = append(titles, tb.Title)
		}
		return strings.Join(titles, ",")
	}
	d.Restores["succeeded"] = 1
	dests := []destinationUsage{{Name: "nas", Bytes: 1 << 30, Backups: 3, Space: checkedSpace(1<<40, destinations.LowNone, d.Period.until)}}
	for _, c := range []struct {
		name          string
		usage, alerts bool
		raised        int64
		dests         []destinationUsage
		want          string
	}{
		{"user", false, false, 4, dests, "Jobs,Restores"},
		{"maintainer", true, false, 4, dests, "Jobs,Restores,Storage"},
		{"maintainer, no destinations", true, false, 0, nil, "Jobs,Restores"},
		{"admin", true, true, 0, dests, "Jobs,Restores,Storage,Security"},
		// Raised alerts lead the tables (problems first; never past a
		// long jobs table).
		{"admin, alerts", true, true, 4, dests, "Security,Jobs,Restores,Storage"},
		// No destinations: the storage part is a sentence.
		{"admin, no destinations", true, true, 0, nil, "Jobs,Restores,Security"},
		{"alerts only", false, true, 1, dests, "Security,Jobs,Restores"},
	} {
		d.ShowUsage, d.ShowAlerts, d.SecurityAlerts, d.Destinations = c.usage, c.alerts, c.raised, c.dests
		if got := tables(d); got != c.want {
			t.Errorf("%s: tables = %s, want %s", c.name, got, c.want)
		}
	}
	// The stored inbox body has no staff part.
	d.SecurityAlerts = 3
	if body := buildSummary(d, "https://bekup.example/").Body; strings.Contains(body, "ecurity") || strings.Contains(body, "nas") {
		t.Errorf("inbox body = %q", body)
	}
}

func TestSummaryWindow(t *testing.T) {
	t.Parallel()
	ams := mustZone(t, "Europe/Amsterdam")
	for _, c := range []struct {
		from, until string
		f           Frequency
		want        string
	}{
		{"2026-11-07T07:00:00Z", "2026-11-08T07:00:00Z", Daily, "the last 24 hours"},
		{"2026-11-06T07:00:00Z", "2026-11-08T07:00:00Z", EveryOtherDay, "the last 48 hours"},
		{"2026-03-28T07:00:00Z", "2026-03-29T06:00:00Z", Daily, "the last day"},
		{"2026-11-01T07:00:00Z", "2026-11-08T07:00:00Z", Weekly, "the last 7 days"},
		{"2026-10-25T06:00:00Z", "2026-11-01T07:00:00Z", Weekly, "the last 7 days"},
		{"2026-11-04T07:00:00Z", "2026-11-07T07:00:00Z", Daily, "the last 3 days"},
		{"2026-10-01T06:00:00Z", "2026-11-01T07:00:00Z", Monthly, "the last month"},
		{"2026-11-07T07:00:00Z", "2026-11-07T11:00:00Z", Daily, "this period"},
	} {
		p := summaryPeriod{from: at(t, c.from), until: at(t, c.until), loc: ams, frequency: c.f}
		if got := p.window(); got != c.want {
			t.Errorf("%s..%s: %q, want %q", c.from, c.until, got, c.want)
		}
	}
}

// TestSummaryCatchUpNote: the "could not be sent on time" note follows
// decide's CatchUp only, never the period length: a switch from monthly to
// daily (the first daily summary starts at the last monthly one, a month
// back) does not claim a missed summary, an outage does.
func TestSummaryCatchUpNote(t *testing.T) {
	t.Parallel()
	const note = "This summary covers more than one period: the previous one could not be sent on time."
	last := at(t, "2026-11-01T08:00:00Z")
	switched := settings(Daily, 8, "UTC")
	dec, err := decide(switched, at(t, "2026-11-20T08:00:00Z"), &last, at(t, "2026-11-20T08:03:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if dec.CatchUp || !dec.PeriodStart.Equal(last) {
		t.Fatalf("switch decision = %+v", dec)
	}
	late, err := decide(switched, at(t, "2026-11-20T08:00:00Z"), &last, at(t, "2026-11-23T09:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if !late.CatchUp {
		t.Fatalf("outage decision = %+v", late)
	}
	for _, c := range []struct {
		name string
		dec  decision
		want bool
	}{{"frequency switch", dec, false}, {"outage", late, true}} {
		raw, err := notify.ValidateDetail(eventDetail(c.dec.PeriodStart, c.dec.PeriodEnd, "UTC", Daily, false, c.dec.CatchUp))
		if err != nil {
			t.Fatal(err)
		}
		p, ok := periodOf(raw)
		if !ok {
			t.Fatal("detail does not read back")
		}
		d := allOK(t, p)
		if got := slices.Contains(buildSummary(d, "https://bekup.example/").Notice.Paragraphs, note); got != c.want {
			t.Errorf("%s: note = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestSummaryEventDetail: the event detail passes notify's detail rules,
// the dedup key its key rule, and the period reads back.
func TestSummaryEventDetail(t *testing.T) {
	t.Parallel()
	from, until := at(t, "2026-11-02T07:00:00Z"), at(t, "2026-11-09T07:00:00Z")
	d := eventDetail(from, until, "America/Argentina/ComodRivadavia", EveryOtherDay, true, false)
	raw, err := notify.ValidateDetail(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"level"`) || !strings.Contains(string(raw), `"catch_up":false`) {
		t.Fatalf("detail = %s", raw)
	}
	p, ok := periodOf(raw)
	if !ok || !p.from.Equal(from) || !p.until.Equal(until) || p.frequency != EveryOtherDay || !p.preview || p.catchUp ||
		p.zone != "America/Argentina/ComodRivadavia" {
		t.Fatalf("period = %+v %v", p, ok)
	}
	raw, err = notify.ValidateDetail(eventDetail(from, until, "UTC", Weekly, false, true))
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := periodOf(raw); !ok || !p.catchUp {
		t.Fatalf("catch-up period = %+v %v", p, ok)
	}
	// Events from before amendment 1 have no catch_up: no note.
	if p, ok := periodOf([]byte(`{"period_start":"2026-11-02T07:00:00Z","period_end":"2026-11-09T07:00:00Z","timezone":"UTC","frequency":"weekly","preview":false}`)); !ok || p.catchUp {
		t.Fatalf("old detail = %+v %v", p, ok)
	}
	user := uuid.New()
	if k := dedupKey(user, until); !regexp.MustCompile(`^[a-z][a-z0-9_.:-]*$`).MatchString(k) || len(k) > 200 || k != "summary:"+user.String()+":1794207600" {
		t.Fatalf("dedup key = %q", k)
	}
	// A broken detail (or an unknown zone) renders without failing.
	if _, ok := periodOf([]byte(`{"period_start":"x"}`)); ok {
		t.Fatal("broken detail accepted")
	}
	raw2, err := notify.ValidateDetail(eventDetail(from, until, "Nowhere/Zone", Weekly, false, false))
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

// stringConsts parses a Go file and returns its string constants whose
// names start with prefix (values of other constants are their selector
// text, as "runstate.CodeCanceled").
func stringConsts(t *testing.T, path, prefix string) map[string]string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, decl := range f.Decls {
		g, ok := decl.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, spec := range g.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, n := range vs.Names {
				if !strings.HasPrefix(n.Name, prefix) || i >= len(vs.Values) {
					continue
				}
				switch v := vs.Values[i].(type) {
				case *ast.BasicLit:
					val, err := strconv.Unquote(v.Value)
					if err != nil {
						t.Fatalf("%s: %s: %v", path, n.Name, err)
					}
					out[n.Name] = val
				case *ast.SelectorExpr:
					if x, ok := v.X.(*ast.Ident); ok {
						out[n.Name] = x.Name + "." + v.Sel.Name
					}
				default:
					t.Fatalf("%s: %s has an unexpected value", path, n.Name)
				}
			}
		}
	}
	return out
}

// TestFailureTextsCoverCodes: every run error code has a fixed text: every
// Code* constant of the backup worker (jobs/backup_errors.go, those taken
// from runstate resolved from runstate/store.go) and every runner reason
// (runnerpb/reasons.go, lower-cased), read from the sources.
func TestFailureTextsCoverCodes(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	jobs := stringConsts(t, filepath.Join(root, "jobs", "backup_errors.go"), "Code")
	rs := stringConsts(t, filepath.Join(root, "runstate", "store.go"), "Code")
	reasons := stringConsts(t, filepath.Join(root, "runnerpb", "reasons.go"), "Reason")
	if len(jobs) < 25 || len(rs) < 2 || len(reasons) < 10 {
		t.Fatalf("found only %d job codes, %d runstate codes, %d reasons", len(jobs), len(rs), len(reasons))
	}
	codes := map[string]string{}
	for name, v := range jobs {
		if ref, ok := strings.CutPrefix(v, "runstate."); ok {
			if v, ok = rs[ref]; !ok {
				t.Fatalf("jobs.%s refers to unknown runstate.%s", name, ref)
			}
		} else if strings.Contains(v, ".") {
			t.Fatalf("jobs.%s = %s: unresolved", name, v)
		}
		codes[v] = "jobs." + name
	}
	for name, v := range rs {
		codes[v] = "runstate." + name
	}
	for name, v := range reasons {
		codes[strings.ToLower(v)] = "runnerpb." + name
	}
	for _, l := range i18n.Locales {
		for c, from := range codes {
			if failureText(l, c) == "" {
				t.Errorf("%s: no failure text for %q (%s)", l, c, from)
			}
		}
		for key, txt := range texts[l] {
			c, ok := strings.CutPrefix(key, "failure.")
			if !ok {
				continue
			}
			if txt == "" || len(txt) > 60 || strings.ContainsAny(txt, "\r\n") {
				t.Errorf("%s.%s: bad text %q", l, c, txt)
			}
			if _, ok := codes[c]; !ok {
				t.Errorf("%s: failure text for %q, which no source defines", l, c)
			}
		}
	}
}

// --- golden e-mails -----------------------------------------------------------------

func goldenSummaries(t *testing.T) map[string]*summaryData {
	t.Helper()
	p := dailyPeriod(t)
	ok := allOK(t, p)
	mixed := newData(p, mixedInput(t, p))
	mixed.ShowUsage, mixed.ShowAlerts, mixed.SecurityAlerts = true, true, 1
	checked := p.until.Add(-35 * time.Minute)
	mixed.Destinations = []destinationUsage{
		{Name: "nas-01", Type: destinations.TypeDisk, Bytes: 19 << 37, Backups: 412, Space: checkedSpace(96<<30, destinations.LowFraction, checked)},
		{Name: "offsite s3", Type: destinations.TypeS3, Bytes: 24 << 37, Backups: 512, Space: destinations.StoredSpace{
			Status: destinations.SpaceUnsupported, Code: destinations.SpaceCodeUnsupported, CheckedAt: p.until.Add(-72 * time.Hour),
		}},
		{Name: "sftp backup host", Type: destinations.TypeSFTP, Bytes: 3 << 40, Backups: 380, Space: checkedSpace(4<<40, destinations.LowNone, checked)},
		{Name: "webdav archive", Type: destinations.TypeWebDAV, Bytes: 5 << 36, Backups: 61, Space: destinations.StoredSpace{
			Status: destinations.SpaceUnavailable, Code: string(storage.CodeNetwork), CheckedAt: checked,
		}},
	}
	maxed := newData(p, bigInput(t, p, 140, 12, func(i int) string {
		return fmt.Sprintf("postgres-production-cluster-%03d", i)
	}))
	maxed.ShowUsage, maxed.ShowAlerts = true, true
	for i := range 10 {
		maxed.Destinations = append(maxed.Destinations, destinationUsage{
			Name: fmt.Sprintf("destination-%02d", i), Type: destinations.TypeDisk, Bytes: int64(i) << 38, Backups: int64(i * 40),
			Space: checkedSpace(int64(i+1)<<40, destinations.LowNone, p.until.Add(-time.Duration(i)*time.Hour)),
		})
	}
	// Every storage state in one table (the phone-width check of the web
	// recipe loads it at 375 px; the status words must fit).
	states := allOK(t, p)
	states.ShowUsage = true
	stale := p.until.Add(-5 * time.Hour)
	states.Destinations = []destinationUsage{
		{Name: "nas-01", Type: destinations.TypeDisk, Bytes: 19 << 37, Backups: 412, Space: checkedSpace(96<<30, destinations.LowFraction, checked)},
		{Name: "nas-02", Type: destinations.TypeDisk, Bytes: 3 << 40, Backups: 380, Space: checkedSpace(4<<40, destinations.LowNone, checked)},
		{Name: "nas-03 (stopped check)", Type: destinations.TypeDisk, Bytes: 7 << 38, Backups: 40, Space: checkedSpace(2<<40, destinations.LowNone, stale)},
		{Name: "offsite s3", Type: destinations.TypeS3, Bytes: 24 << 37, Backups: 512, Space: destinations.StoredSpace{
			Status: destinations.SpaceUnsupported, Code: destinations.SpaceCodeUnsupported, CheckedAt: checked,
		}},
		{Name: "webdav without quota", Type: destinations.TypeWebDAV, Bytes: 5 << 36, Backups: 61, Space: destinations.StoredSpace{
			Status: destinations.SpaceUnsupported, Code: destinations.SpaceCodeUnsupported, CheckedAt: stale,
		}},
		{Name: "sftp archive", Type: destinations.TypeSFTP, Bytes: 5 << 36, Backups: 61, Space: destinations.StoredSpace{
			Status: destinations.SpaceUnavailable, Code: string(storage.CodeNetwork), CheckedAt: checked,
		}},
		{Name: "new destination", Type: destinations.TypeSFTP, Bytes: 0, Backups: 0},
		{Name: "moved destination", Type: destinations.TypeDisk, Bytes: 1 << 30, Backups: 1, Space: destinations.StoredSpace{SettingsChanged: true}},
	}
	return map[string]*summaryData{
		"summary_all_ok": ok, "summary_failures": mixed, "summary_max": maxed, "summary_storage_states": states,
	}
}

// TestSummaryGolden pins the rendered summaries (go test -update, then
// review them in a browser at 600 and 375 px, light and dark).
func TestSummaryGolden(t *testing.T) {
	t.Parallel()
	for name, d := range goldenSummaries(t) {
		out := render(t, buildSummary(d, "https://bekup.example.com/"))
		if name == "summary_max" && email.EncodedLen(out.HTML) > maxSummaryHTML {
			t.Errorf("the largest summary is %d bytes of HTML as sent (budget %d)", email.EncodedLen(out.HTML), maxSummaryHTML)
		}
		for ext, got := range map[string]string{".html": out.HTML, ".txt": out.Text} {
			path := filepath.Join("testdata", name+ext)
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
					t.Fatal(err)
				}
				continue
			}
			want, err := os.ReadFile(path) //nolint:gosec // G304: fixed test file names
			if err != nil {
				t.Fatalf("%s (run go test -update): %v", path, err)
			}
			if got != string(want) {
				t.Errorf("%s differs (run go test -update and review)", path)
			}
		}
	}
}

// TestSummaryWorstCaseSize: whatever the names, the HTML part stays under
// Gmail's clipping (102 KB) both decoded and as sent (quoted-printable),
// with everything after the jobs table present: 120 failed jobs and 12
// destinations whose names escape to the most bytes (&, " and < take four
// or five bytes in html/template, emoji four bytes and twelve encoded),
// ASCII and umlaut names; the jobs table lists as many rows as fit.
func TestSummaryWorstCaseSize(t *testing.T) {
	t.Parallel()
	p := dailyPeriod(t)
	for _, c := range []struct {
		name string
		unit string
		// minRows is the least the table must still list.
		minRows int
	}{
		{"ascii", "x", 55},
		{"umlaut", "ü", 30},
		{"ampersand", "&", 30},
		{"quote", `"`, 30},
		{"less than", "<", 30},
		{"emoji", "😀", 30},
		{"mixed", `&"<😀`, 30},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			name := func(i int) string { return strings.Repeat(c.unit, 100) + fmt.Sprintf("%03d", i) }
			d := newData(p, bigInput(t, p, 120, 120, name))
			d.ShowUsage, d.ShowAlerts, d.SecurityAlerts = true, true, 9
			d.Restores["failed"], d.TestRestores["error"] = 3, 2
			big := int64(1) << 50
			for i := range 12 {
				// The longest storage note: low, stale.
				d.Destinations = append(d.Destinations, destinationUsage{
					Name: name(i), Bytes: 1 << 40, Backups: 999,
					Space: checkedSpace(big, destinations.LowBelowNewestSet, p.until.Add(-30*time.Hour)),
				})
			}
			sc := buildSummary(d, "https://bekup.example.com/")
			out := render(t, sc)
			if len(out.HTML) >= 100_000 {
				t.Errorf("HTML is %d bytes", len(out.HTML))
			}
			if enc := email.EncodedLen(out.HTML); enc > maxSummaryHTML || enc >= 102_000 {
				t.Errorf("HTML as sent is %d bytes (budget %d)", enc, maxSummaryHTML)
			}
			var jobs email.Table
			for _, tb := range sc.Notice.Tables {
				if tb.Title == "Jobs" {
					jobs = tb
				}
			}
			listed := len(jobs.Rows) - 1
			if listed < c.minRows || listed > maxSummaryRows-1 {
				t.Errorf("the jobs table lists %d rows", listed)
			}
			if last := jobs.Rows[listed]; last.Cells[1] != fmt.Sprintf("+%d more jobs, %d of them need attention", 120-listed, 120-listed) {
				t.Errorf("last row = %q", last.Cells[1])
			}
			// Everything after the jobs table made it into the budget.
			for _, want := range []string{">Restores</h2>", ">Storage</h2>", "Open the dashboard", "Times are in Europe/Amsterdam."} {
				if !strings.Contains(out.HTML, want) {
					t.Errorf("HTML lacks %q", want)
				}
			}
			if !strings.Contains(out.HTML, ">Security</h2>") || strings.Index(out.HTML, ">Security</h2>") > strings.Index(out.HTML, ">Jobs</h2>") {
				t.Error("the raised alerts must come before the jobs table")
			}
			t.Logf("%s: %d rows listed, HTML %d bytes, %d as sent", c.name, listed, len(out.HTML), email.EncodedLen(out.HTML))
		})
	}
}

// TestSummaryFitDropsOKRowsFirst: over the budget, the OK rows leave the
// jobs table before any problem row.
func TestSummaryFitDropsOKRowsFirst(t *testing.T) {
	t.Parallel()
	p := dailyPeriod(t)
	d := newData(p, bigInput(t, p, 100, 10, func(i int) string { return strings.Repeat("😀", 100) + fmt.Sprintf("%03d", i) }))
	sc := buildSummary(d, "https://bekup.example.com/")
	jobs := sc.Notice.Tables[0]
	listed := len(jobs.Rows) - 1
	if jobs.Title != "Jobs" || listed < 10 || listed >= 99 {
		t.Fatalf("%s lists %d rows", jobs.Title, listed)
	}
	for i := range 10 {
		if jobs.Rows[i].Cells[0] != "Failed" {
			t.Fatalf("row %d = %v", i, jobs.Rows[i].Cells)
		}
	}
	if last := jobs.Rows[listed]; last.Cells[1] != fmt.Sprintf("+%d more jobs without problems", 100-listed) || last.Level != email.LevelOK {
		t.Fatalf("last row = %+v", last)
	}
	if enc := email.EncodedLen(render(t, sc).HTML); enc > maxSummaryHTML {
		t.Fatalf("HTML as sent is %d bytes", enc)
	}
	// Within the budget nothing is dropped.
	d = newData(p, bigInput(t, p, 100, 10, func(i int) string { return fmt.Sprintf("job-%03d", i) }))
	if rows := buildSummary(d, "https://bekup.example.com/").Notice.Tables[0].Rows; len(rows) != 100 || rows[99].Cells[1] == "" || strings.HasPrefix(rows[99].Cells[1], "+") {
		t.Fatalf("%d rows, last %v", len(rows), rows[len(rows)-1].Cells)
	}
}

// TestSummaryStorageRows: every stored space state (D-227) has its words,
// level and note; stale answers warn whatever they report, unsupported and
// unchecked ones are neutral while fresh, low space counts towards the
// banner.
func TestSummaryStorageRows(t *testing.T) {
	t.Parallel()
	p := dailyPeriod(t)
	now := p.until
	fresh, yesterday, stale := now.Add(-20*time.Minute), now.Add(-20*time.Hour), now.Add(-4*time.Hour)
	failed := func(code string, at time.Time) destinations.StoredSpace {
		return destinations.StoredSpace{Status: destinations.SpaceUnavailable, Code: code, CheckedAt: at}
	}
	for _, c := range []struct {
		name       string
		typ        destinations.Type
		space      destinations.StoredSpace
		word, free string
		level      email.Level
		note       string
		low        bool
	}{
		{"ok", destinations.TypeDisk, checkedSpace(5<<30, destinations.LowNone, fresh), "OK", "5.0 GiB", email.LevelOK, "checked 07:40", false},
		{"ok yesterday", destinations.TypeDisk, checkedSpace(5<<30, destinations.LowNone, yesterday.Add(-12*time.Hour)), "Stale", "5.0 GiB", email.LevelWarning,
			"last checked 7 Nov 00:00, more than 3 hours ago", false},
		{"ok stale", destinations.TypeSFTP, checkedSpace(5<<30, destinations.LowNone, stale), "Stale", "5.0 GiB", email.LevelWarning,
			"last checked 8 Nov 04:00, more than 3 hours ago", false},
		{"low fraction", destinations.TypeDisk, checkedSpace(1<<30, destinations.LowFraction, fresh), "Low space", "1.0 GiB", email.LevelWarning,
			"less than 10% free; checked 07:40", true},
		{"low below set, stale", destinations.TypeWebDAV, checkedSpace(1<<30, destinations.LowBelowNewestSet, stale), "Low space", "1.0 GiB", email.LevelWarning,
			"less free than the newest backup set needs; last checked 8 Nov 04:00, more than 3 hours ago", true},
		{"check failed", destinations.TypeSFTP, failed(string(storage.CodeNetwork), fresh), "Check failed", "–", email.LevelWarning,
			"the destination could not be reached; checked 07:40", false},
		{"check failed, not ready", destinations.TypeSFTP, failed(destinations.SpaceCodeNotReady, fresh), "Check failed", "–", email.LevelWarning,
			"the destination was not ready; checked 07:40", false},
		{"check failed, stale", destinations.TypeWebDAV, failed(destinations.SpaceCodeFailed, stale), "Check failed", "–", email.LevelWarning,
			"the space could not be checked; last checked 8 Nov 04:00, more than 3 hours ago", false},
		{"check failed, unknown code", destinations.TypeWebDAV, failed("something_new", fresh), "Check failed", "–", email.LevelWarning,
			storage.ErrorCode("something_new").Message() + "; checked 07:40", false},
		{"s3", destinations.TypeS3, destinations.StoredSpace{Status: destinations.SpaceUnsupported, CheckedAt: fresh},
			"No limit", "–", email.LevelNone, "no limit reported: S3 buckets have no fixed size; checked 07:40", false},
		{"s3, stale", destinations.TypeS3, destinations.StoredSpace{Status: destinations.SpaceUnsupported, CheckedAt: stale},
			"Stale", "–", email.LevelWarning, "no limit reported: S3 buckets have no fixed size; last checked 8 Nov 04:00, more than 3 hours ago", false},
		{"no quota", destinations.TypeWebDAV, destinations.StoredSpace{Status: destinations.SpaceUnsupported, CheckedAt: fresh},
			"No limit", "–", email.LevelNone, "no limit reported: the server reports no free space or quota; checked 07:40", false},
		{"used only", destinations.TypeWebDAV, destinations.StoredSpace{Status: destinations.SpaceAvailable, Space: storage.Space{Used: 9, UsedKnown: true}, CheckedAt: fresh},
			"No limit", "–", email.LevelNone, "no limit reported: the destination reports no free space; checked 07:40", false},
		{"used only, stale", destinations.TypeWebDAV, destinations.StoredSpace{Status: destinations.SpaceAvailable, Space: storage.Space{Used: 9, UsedKnown: true}, CheckedAt: stale},
			"Stale", "–", email.LevelWarning, "no limit reported: the destination reports no free space; last checked 8 Nov 04:00, more than 3 hours ago", false},
		{"never", destinations.TypeDisk, destinations.StoredSpace{}, "Not checked", "–", email.LevelNone, "the hourly space check has not run yet", false},
		{"settings changed", destinations.TypeDisk, destinations.StoredSpace{SettingsChanged: true}, "Not checked", "–", email.LevelNone,
			"not checked since the settings changed", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d := allOK(t, p)
			d.ShowUsage = true
			d.Destinations = []destinationUsage{{Name: "dest", Type: c.typ, Bytes: 1 << 30, Backups: 2, Space: c.space}}
			row := d.storageTable().Rows[0]
			if row.Cells[0] != c.word || row.Cells[3] != c.free || row.Level != c.level || row.Note != c.note {
				t.Errorf("row = %q level %v note %q; want %q %q level %v note %q", row.Cells, row.Level, row.Note, c.word, c.free, c.level, c.note)
			}
			if got := d.lowDestinations() == 1; got != c.low {
				t.Errorf("counted low = %v, want %v", got, c.low)
			}
			if o := d.overall(); (o.level == levelWarning) != c.low {
				t.Errorf("overall = %+v", o)
			}
		})
	}
}

// TestSummaryStorageRenderClock: the stored answers are read when the
// summary is rendered, so their age and "checked HH:MM" follow the render
// time, not the end of the period: a summary delivered hours late shows a
// then-fresh answer as fresh with the time of day of the render's day, and
// an answer that was fresh at the period end but is old at render as
// stale.
func TestSummaryStorageRenderClock(t *testing.T) {
	t.Parallel()
	p := dailyPeriod(t)
	render := p.until.Add(20 * time.Hour) // 9 Nov 04:00 in Amsterdam
	for _, c := range []struct {
		name       string
		at         time.Time
		word, note string
	}{
		{"fresh at render", render.Add(-30 * time.Minute), "OK", "checked 03:30"},
		{"fresh at the period end, old at render", p.until.Add(-10 * time.Minute), "Stale", "last checked 8 Nov 07:50, more than 3 hours ago"},
	} {
		d := allOK(t, p)
		d.Now, d.ShowUsage = render, true
		d.Destinations = []destinationUsage{{Name: "dest", Type: destinations.TypeDisk, Bytes: 1, Space: checkedSpace(5<<30, destinations.LowNone, c.at)}}
		if row := d.storageTable().Rows[0]; row.Cells[0] != c.word || row.Note != c.note {
			t.Errorf("%s: %q, note %q; want %q, %q", c.name, row.Cells[0], row.Note, c.word, c.note)
		}
	}
}

// TestSummaryAcceptedRisks: admins see the accepted risks and the API
// tokens without expiry as one neutral row; it never changes the level
// (D-228, D-229).
func TestSummaryAcceptedRisks(t *testing.T) {
	t.Parallel()
	d := allOK(t, dailyPeriod(t))
	d.ShowAlerts = true
	before := d.overall()
	d.Risks = riskaccept.Counts{Accepted: 3, Forever: 1, TokensNeverExpiring: 2}
	if o := d.overall(); o != before {
		t.Fatalf("accepted risks changed the level: %+v", o)
	}
	out := render(t, buildSummary(d, "https://bekup.example/"))
	if !strings.Contains(out.Text, "3 accepted risks (1 without end date); 2 API tokens without expiry") {
		t.Fatalf("text lacks the accepted risks:\n%s", out.Text)
	}
	d.Risks = riskaccept.Counts{}
	if out := render(t, buildSummary(d, "https://bekup.example/")); strings.Contains(out.Text, "accepted risk") {
		t.Fatal("an empty count is listed")
	}
}
