package summary

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/destinations"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/notify/email"
	"github.com/jlentink/bekup/internal/storage"
)

// Rendering of the summary (D-226 amendment 1): the subject and heading
// name the worst state first, the banner carries the overall level with a
// word, the key figures, one row per job (worst first) and the restores,
// storage and security tables. Every text is fixed or a number; job and
// destination names go through notify.DisplayName; error codes become
// bekup's own short texts (failureText), never server output.

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

// cellTime is a short local time for a table cell ("3 Nov 08:00").
func cellTime(t time.Time, loc *time.Location) string {
	return t.In(loc).Format("2 Jan 15:04")
}

var frequencyLabels = map[Frequency]string{
	Daily: "Daily", EveryOtherDay: "Every-other-day", Weekly: "Weekly", Monthly: "Monthly",
}

// statusWords are the words of the job statuses (the status cell).
var statusWords = [...]string{
	statusFailed: "Failed", statusMissed: "Missed", statusPartial: "Partial", statusHeld: "Held",
	statusOK: "OK", statusNoRun: "No run", statusDisabled: "Disabled",
}

// statusLevels are the e-mail levels of the job statuses.
var statusLevels = [...]email.Level{
	statusFailed: email.LevelFailure, statusMissed: email.LevelFailure, statusPartial: email.LevelWarning,
	statusHeld: email.LevelWarning, statusOK: email.LevelOK, statusNoRun: email.LevelNone, statusDisabled: email.LevelNone,
}

// window is the period in words ("the last 24 hours", "the last 7
// days", "the last month"; "this period" when it is none of them).
func (p *summaryPeriod) window() string {
	d := p.until.Sub(p.from)
	switch d {
	case 24 * time.Hour:
		return "the last 24 hours"
	case 48 * time.Hour:
		return "the last 48 hours"
	default:
	}
	hours := d.Hours()
	days := int(math.Round(hours / 24))
	exactDays := math.Abs(hours-float64(days)*24) <= 1
	switch {
	case exactDays && days == 1:
		return "the last day"
	case exactDays && days == 2:
		return "the last 2 days"
	case exactDays && days == 7:
		return "the last 7 days"
	case p.frequency == Monthly && days >= 28 && days <= 31 && periodBefore(p.until, Monthly, p.loc).Equal(p.from):
		return "the last month"
	case exactDays && days > 2:
		return fmt.Sprintf("the last %d days", days)
	default:
		return "this period"
	}
}

// overall is the summary's overall state.
type overall struct {
	// level is the event detail value (ok, warning, failure).
	level string
	email email.Level
	// word is the banner word, text the banner sentence.
	word, text string
}

func (d *summaryData) overall() overall {
	c := d.counts()
	w := d.Period.window()
	low := d.lowDestinations()
	alerts := d.ShowAlerts && d.SecurityAlerts > 0
	extra := []string{}
	if low > 0 {
		extra = append(extra, plural(int64(low), "destination is", "destinations are")+" low on space")
	}
	if alerts {
		extra = append(extra, plural(d.SecurityAlerts, "security alert was", "security alerts were")+" raised")
	}
	tail := ""
	if len(extra) > 0 {
		tail = "; " + joinAnd(extra)
	}
	switch {
	case d.Jobs == 0 || len(d.JobRows) == 0:
		return overall{level: levelOK, email: email.LevelNone, word: "No jobs", text: "You can see no backup jobs."}
	case c[statusFailed] > 0 || c[statusMissed] > 0:
		var parts []string
		if n := c[statusFailed]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d of %d %s failed", n, d.Jobs, pick(d.Jobs == 1, "job", "jobs")))
		}
		if n := c[statusMissed]; n > 0 {
			parts = append(parts, plural(int64(n), "job missed its backups", "jobs missed their backups"))
		}
		if n := c[statusPartial] + c[statusHeld]; n > 0 {
			parts = append(parts, plural(int64(n), "more needs attention", "more need attention"))
		}
		word := fmt.Sprintf("%d failed", c[statusFailed])
		if c[statusFailed] == 0 {
			word = fmt.Sprintf("%d missed", c[statusMissed])
		}
		return overall{level: levelFailure, email: email.LevelFailure, word: word, text: upperFirst(joinAnd(parts)) + " in " + w + tail + "."}
	case c.problems() > 0:
		n := c.problems()
		return overall{level: levelWarning, email: email.LevelWarning, word: "Attention",
			text: fmt.Sprintf("%d of %d %s %s attention in %s%s.", n, d.Jobs, pick(d.Jobs == 1, "job", "jobs"), pick(n == 1, "needs", "need"), w, tail)}
	}
	var text string
	switch {
	case c[statusOK] == len(d.JobRows) && d.Jobs == 1:
		text = "Your backup job ran fine in " + w
	case c[statusOK] == len(d.JobRows):
		text = fmt.Sprintf("All %d backup jobs ran fine in %s", d.Jobs, w)
	default:
		parts := []string{fmt.Sprintf("%d OK", c[statusOK])}
		if n := c[statusNoRun]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d without a run", n))
		}
		if n := c[statusDisabled]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d disabled", n))
		}
		text = fmt.Sprintf("No problems in %s (%s)", w, strings.Join(parts, ", "))
	}
	if len(extra) > 0 {
		return overall{level: levelWarning, email: email.LevelWarning, word: "Attention", text: text + ", but " + joinAnd(extra) + "."}
	}
	return overall{level: levelOK, email: email.LevelOK, word: "All OK", text: text + "."}
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// countWords are the job counts, worst first ("2 failed", "1 missed",
// "11 OK"); upper writes the failed count in capitals (the subject).
func (c counts) words(upper bool) []string {
	var out []string
	for _, x := range []struct {
		s    jobStatus
		word string
	}{{statusFailed, "failed"}, {statusMissed, "missed"}, {statusPartial, "partial"}, {statusHeld, "held"}, {statusOK, "OK"}} {
		if n := c[x.s]; n > 0 {
			w := x.word
			if upper && x.s == statusFailed {
				w = "FAILED"
			}
			out = append(out, fmt.Sprintf("%d %s", n, w))
		}
	}
	return out
}

// prefix is "Daily summary" (or "Summary preview").
func (d *summaryData) prefix() string {
	if d.Period.preview {
		return "Summary preview"
	}
	label := frequencyLabels[d.Period.frequency]
	if label == "" {
		label = "Periodic"
	}
	return label + " summary"
}

// title is the inbox title: the counts, worst first ("Daily summary: 2
// failed, 1 missed, 11 OK"). The e-mail heading is prefix() alone (the
// banner and figures carry the counts there).
func (d *summaryData) title() string {
	c := d.counts()
	switch {
	case len(d.JobRows) == 0:
		return d.prefix() + ": no backup jobs"
	case c[statusOK] == len(d.JobRows) && d.Jobs == 1:
		return d.prefix() + ": your backup job is OK"
	case c[statusOK] == len(d.JobRows):
		return fmt.Sprintf("%s: all %d jobs OK", d.prefix(), d.Jobs)
	}
	words := c.words(false)
	switch {
	case len(words) == 0:
		return d.prefix() + ": no problems"
	case c.problems() == 0:
		return d.prefix() + ": no problems (" + strings.Join(words, ", ") + ")"
	}
	return d.prefix() + ": " + strings.Join(words, ", ")
}

// subject names the worst state first: "2 FAILED, 1 missed", "needs
// attention" or "all 14 jobs OK".
func (d *summaryData) subject(o overall) string {
	c := d.counts()
	var s string
	switch o.level {
	case levelFailure:
		var parts []string
		if n := c[statusFailed]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d FAILED", n))
		}
		if n := c[statusMissed]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d missed", n))
		}
		s = strings.Join(parts, ", ")
	case levelWarning:
		s = "needs attention"
	default:
		switch {
		case len(d.JobRows) == 0:
			s = "no backup jobs"
		case c[statusOK] == len(d.JobRows) && d.Jobs == 1:
			s = "your backup job is OK"
		case c[statusOK] == len(d.JobRows):
			s = fmt.Sprintf("all %d jobs OK", d.Jobs)
		default:
			s = "no problems"
		}
	}
	return "[bekup] " + d.prefix() + ": " + s
}

// preheader is the inbox preview line ("2 failed · 11 OK · the last 24
// hours").
func (d *summaryData) preheader(o overall) string {
	parts := d.counts().words(false)
	if o.level == levelOK && len(parts) > 0 {
		parts = append([]string{"All OK"}, parts...)
	}
	return strings.Join(append(parts, d.Period.window()), " · ")
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

// note explains a job's status in fixed words and numbers.
func (r *jobRow) note(loc *time.Location) string {
	var n []string
	if r.Failed > 0 {
		reason := failureText(r.FailureCode)
		if reason == "" {
			reason = "backup failed"
		}
		if r.Failed > 1 {
			reason = fmt.Sprintf("%s (%d failed runs)", reason, r.Failed)
		}
		n = append(n, reason)
	}
	if r.TestRestoresFailed > 0 {
		n = append(n, plural(r.TestRestoresFailed, "test restore failed", "test restores failed"))
	}
	if r.MissedSince != nil {
		n = append(n, "missed since "+summaryTime(*r.MissedSince, loc))
	}
	if r.NoSuccess && r.Failed == 0 {
		n = append(n, "no successful backup in this period")
	}
	if r.Partial > 0 {
		n = append(n, plural(r.Partial, "partially failed run", "partially failed runs"))
	}
	if r.Warned > 0 {
		n = append(n, plural(r.Warned, "run with warnings", "runs with warnings"))
	}
	if r.HeldRuns > 0 {
		n = append(n, fmt.Sprintf("retention keeps %s on %s", plural(r.HeldRuns, "incomplete backup", "incomplete backups"),
			plural(int64(r.HoldDestinations), "destination", "destinations")))
	}
	return strings.Join(n, "; ")
}

// word is the status cell ("Warnings" for a job whose only issue is runs
// with warnings).
func (r *jobRow) word() string {
	if r.Status == statusPartial && r.Partial == 0 {
		return "Warnings"
	}
	return statusWords[r.Status]
}

// jobsTable lists the first `listed` jobs (worst first, so OK rows go
// before problem rows); the rest becomes one line.
func (d *summaryData) jobsTable(listed int) email.Table {
	t := email.Table{
		Title: "Jobs", Status: true,
		Columns:        []string{"Status", "Job", "OK/Fail", "Last run", "Size", "Stored"},
		NumericColumns: []int{2, 3, 4, 5}, NarrowHidden: []int{2, 5},
	}
	loc := d.Period.loc
	rows := d.JobRows
	var rest []jobRow
	if len(rows) > listed {
		rows, rest = rows[:listed], rows[listed:]
	}
	for i := range rows {
		r := &rows[i]
		last, stored := "–", "–"
		if r.LastAt != nil {
			last = cellTime(*r.LastAt, loc)
		}
		if r.HasStored {
			stored = storage.FormatSize(r.Stored)
		}
		size := "–"
		if r.LastAt != nil {
			size = storage.FormatSize(r.Bytes)
		}
		t.Rows = append(t.Rows, email.Row{
			Cells: []string{r.word(), clampRunes(r.Name, maxJobNameCell), fmt.Sprintf("%d/%d", r.OK, r.Failed), last, size, stored},
			Level: statusLevels[r.Status], Note: r.note(loc),
		})
	}
	if len(rest) > 0 {
		problems := 0
		for i := range rest {
			if rest[i].Status.problem() {
				problems++
			}
		}
		row := email.Row{Cells: []string{"OK", fmt.Sprintf("+%d more jobs without problems", len(rest)), "", "", "", ""}, Level: email.LevelOK}
		if problems > 0 {
			row = email.Row{
				Cells: []string{"More", fmt.Sprintf("+%d more jobs, %d of them %s attention", len(rest), problems, pick(problems == 1, "needs", "need")), "", "", "", ""},
				Level: email.LevelFailure, Note: "see the dashboard for the full list",
			}
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}

var restoreStatusRows = []struct {
	status, word string
	level        email.Level
}{
	{string(sqlc.RestoreStatusSucceeded), "Succeeded", email.LevelOK},
	{string(sqlc.RestoreStatusFailed), "Failed", email.LevelFailure},
	{string(sqlc.RestoreStatusCanceled), "Canceled", email.LevelNone},
}

var testStatusRows = []struct {
	status, word string
	level        email.Level
}{
	{string(sqlc.TestRestoreStatusPassed), "Passed", email.LevelOK},
	{string(sqlc.TestRestoreStatusWarnings), "Warnings", email.LevelWarning},
	{string(sqlc.TestRestoreStatusFailed), "Failed", email.LevelFailure},
	{string(sqlc.TestRestoreStatusError), "Could not run", email.LevelFailure},
	{string(sqlc.TestRestoreStatusCanceled), "Canceled", email.LevelNone},
}

// restoresTable counts restores and test restores by result (nil when
// there were none).
func (d *summaryData) restoresTable() *email.Table {
	t := email.Table{Title: "Restores", Status: true, Columns: []string{"Result", "Kind", "Count"}, NumericColumns: []int{2}}
	for _, s := range restoreStatusRows {
		if n := d.Restores[s.status]; n > 0 {
			t.Rows = append(t.Rows, email.Row{Cells: []string{s.word, "Restores", strconv.FormatInt(n, 10)}, Level: s.level})
		}
	}
	for _, s := range testStatusRows {
		if n := d.TestRestores[s.status]; n > 0 {
			t.Rows = append(t.Rows, email.Row{Cells: []string{s.word, "Test restores", strconv.FormatInt(n, 10)}, Level: s.level})
		}
	}
	if len(t.Rows) == 0 {
		return nil
	}
	return &t
}

// storageTable is the destination usage (staff): free space only from
// the cache, low space as a warning row (the D-225 rule).
func (d *summaryData) storageTable() email.Table {
	t := email.Table{
		Title: "Storage", Status: true, Columns: []string{"Status", "Destination", "Stored", "Free", "Backups"},
		NumericColumns: []int{2, 3, 4}, NarrowHidden: []int{4},
	}
	n := min(len(d.Destinations), maxSummaryDestinations)
	for i := range n {
		du := &d.Destinations[i]
		row := email.Row{
			Cells: []string{"Not checked", clampRunes(du.Name, maxJobNameCell), storage.FormatSize(du.Bytes), "–", strconv.FormatInt(du.Backups, 10)},
		}
		if du.Free != nil {
			row.Cells[0], row.Cells[3], row.Level = "OK", storage.FormatSize(*du.Free), email.LevelOK
			switch du.Low {
			case destinations.LowFraction:
				row.Cells[0], row.Level, row.Note = "Low space", email.LevelWarning, "less than 10% free"
			case destinations.LowBelowNewestSet:
				row.Cells[0], row.Level, row.Note = "Low space", email.LevelWarning, "less free than the newest backup set needs"
			default:
			}
		}
		t.Rows = append(t.Rows, row)
	}
	if more := len(d.Destinations) - n; more > 0 {
		t.Rows = append(t.Rows, email.Row{Cells: []string{"More", fmt.Sprintf("+%d more %s", more, pick(more == 1, "destination", "destinations")), "", "", ""}})
	}
	return t
}

// securityTable is the admins' security alert count.
func (d *summaryData) securityTable() email.Table {
	t := email.Table{Title: "Security", Status: true, Columns: []string{"Status", "Security alerts"}}
	if d.SecurityAlerts == 0 {
		t.Rows = []email.Row{{Cells: []string{"OK", "No security alerts in this period"}, Level: email.LevelOK}}
	} else {
		t.Rows = []email.Row{{
			Cells: []string{"Review", plural(d.SecurityAlerts, "security alert", "security alerts") + " in this period"},
			Level: email.LevelWarning, Note: "review them in bekup",
		}}
	}
	return t
}

// figures are the key-figure tiles.
func (d *summaryData) figures(o overall) []email.Figure {
	c := d.counts()
	okLevel := email.LevelNone
	if o.level == levelOK {
		okLevel = email.LevelOK
	}
	figs := []email.Figure{{Label: "Jobs OK", Value: fmt.Sprintf("%d/%d", c[statusOK], d.Jobs), Level: okLevel}}
	failed := email.Figure{Label: "Failed", Value: strconv.Itoa(c[statusFailed])}
	if c[statusMissed] > 0 {
		failed = email.Figure{Label: "Failed/missed", Value: fmt.Sprintf("%d/%d", c[statusFailed], c[statusMissed])}
	}
	if c[statusFailed]+c[statusMissed] > 0 {
		failed.Level = email.LevelFailure
	}
	figs = append(figs, failed, email.Figure{Label: "Written", Value: storage.FormatSize(d.Written)})
	total, _ := countsSentence(d.TestRestores, testStatusOrder)
	if total > 0 {
		good := d.TestRestores[string(sqlc.TestRestoreStatusPassed)] + d.TestRestores[string(sqlc.TestRestoreStatusWarnings)]
		f := email.Figure{Label: "Test restores", Value: fmt.Sprintf("%d/%d", good, total), Level: email.LevelOK}
		if d.TestRestores[string(sqlc.TestRestoreStatusFailed)]+d.TestRestores[string(sqlc.TestRestoreStatusError)] > 0 {
			f.Level = email.LevelFailure
		}
		figs = append(figs, f)
	}
	return figs
}

// defaultListed is how many of n jobs the table lists by itself: all up
// to maxSummaryRows, else maxSummaryRows-1 and the "+N more" line.
func defaultListed(n int) int {
	if n <= maxSummaryRows {
		return n
	}
	return maxSummaryRows - 1
}

// fitJobs keeps the e-mail within maxSummaryHTML: escaping (& and " take
// five bytes, < four) and multi-byte names can make the 100 rows of the
// jobs table larger than Gmail shows before it clips the rest (the
// restores, storage and security tables, the button and the footer). When
// the HTML part as sent is over the budget, the table lists fewer jobs
// (the most it can, found by bisection; OK rows go first, then the excess
// problem rows) and counts the others in its "+N more" line. A notice that
// does not render is left as it is (its delivery reports the error).
func fitJobs(d *summaryData, n *email.Notice, at int) {
	fits := func(listed int) (bool, error) {
		n.Tables[at] = d.jobsTable(listed)
		c, err := email.Render(*n)
		if err != nil {
			return false, err
		}
		return email.EncodedLen(c.HTML) <= maxSummaryHTML, nil
	}
	hi := defaultListed(len(d.JobRows))
	if ok, err := fits(hi); ok || err != nil {
		n.Tables[at] = d.jobsTable(hi)
		return
	}
	// The largest listed count in [0, hi) that fits (0: only the line).
	lo := 0
	for lo+1 < hi {
		mid := (lo + hi) / 2
		// A render error cannot come from the row count; treat it as too
		// large (fewer rows).
		if ok, err := fits(mid); err == nil && ok {
			lo = mid
		} else {
			hi = mid
		}
	}
	n.Tables[at] = d.jobsTable(lo)
}

// buildSummary renders d (pure): the inbox title and body and the e-mail
// (email.Render limits). The inbox row is stored and stays readable after
// a role change, so its title and body carry only the job and run counts
// of the recipient's own scope: no job or destination names and none of
// the staff parts (storage, security alerts; admins see those alerts as
// their own inbox rows, hidden by the read-time visibility once the role
// is gone).
func buildSummary(d *summaryData, dashboard string) notify.SummaryContent {
	p := d.Period
	o := d.overall()
	title := d.title()
	periodLine := fmt.Sprintf("Period: %s – %s (%s).", summaryTime(p.from, p.loc), summaryTime(p.until, p.loc), p.zone)
	var paragraphs []string
	switch {
	case p.preview:
		paragraphs = append(paragraphs, fmt.Sprintf("This is a preview you requested: it covers %s up to now.", p.window()))
	case p.catchUp:
		paragraphs = append(paragraphs, "This summary covers more than one period: the previous one could not be sent on time.")
	}
	paragraphs = append(paragraphs, periodLine, d.runsSentence())
	n := email.Notice{
		Subject: clampRunes(d.subject(o), email.MaxSubjectLen), Heading: d.prefix(),
		Preheader: d.preheader(o),
		Banner:    &email.Banner{Level: o.email, Word: o.word, Text: o.text},
		Figures:   d.figures(o),
		Action:    &email.Action{Label: "Open the dashboard", URL: dashboard},
	}
	// Raised security alerts (admins) lead the tables: problems first, and
	// a one-row table never ends up below a long jobs table. Without
	// alerts the "no security alerts" row closes the e-mail.
	alertsFirst := d.ShowAlerts && d.SecurityAlerts > 0
	if alertsFirst {
		n.Tables = append(n.Tables, d.securityTable())
	}
	jobsAt := -1
	if len(d.JobRows) > 0 {
		jobsAt = len(n.Tables)
		n.Tables = append(n.Tables, d.jobsTable(defaultListed(len(d.JobRows))))
	}
	if t := d.restoresTable(); t != nil {
		n.Tables = append(n.Tables, *t)
	}
	if d.ShowUsage {
		if len(d.Destinations) == 0 {
			paragraphs = append(paragraphs, "Storage: no destinations.")
		} else {
			n.Tables = append(n.Tables, d.storageTable())
		}
	}
	if d.ShowAlerts && !alertsFirst {
		n.Tables = append(n.Tables, d.securityTable())
	}
	n.Paragraphs = paragraphs
	n.Footer = fmt.Sprintf("Times are in %s. Change how often you get this summary, or turn it off, in your notification settings. %s",
		p.zone, email.DefaultFooter)
	if jobsAt >= 0 {
		fitJobs(d, &n, jobsAt)
	}

	c := d.counts()
	body := fmt.Sprintf("This summary covers %s to %s (%s).", summaryTime(p.from, p.loc), summaryTime(p.until, p.loc), p.zone)
	if words := c.words(false); len(words) > 0 {
		body += " Jobs: " + strings.Join(words, ", ") + "."
	}
	_, runParts := countsSentence(d.Runs, runStatusOrder)
	if runParts != "" {
		body += " Backups: " + runParts + "."
	} else {
		body += " No backup finished."
	}
	if _, tp := countsSentence(d.TestRestores, testStatusOrder); tp != "" {
		body += " Test restores: " + tp + "."
	}
	if c.problems() > 0 {
		body += fmt.Sprintf(" %s %s attention: see the dashboard.", plural(int64(c.problems()), "job", "jobs"), pick(c.problems() == 1, "needs", "need"))
	}
	return notify.SummaryContent{Title: clampRunes(title, 200), Body: clampRunes(body, 1000), Notice: n}
}
