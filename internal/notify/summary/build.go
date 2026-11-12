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
	"github.com/jlentink/bekup/internal/i18n"
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

// prefixKeys are the heading keys per frequency.
var prefixKeys = map[Frequency]string{
	Daily: "prefix.daily", EveryOtherDay: "prefix.every_other_day", Weekly: "prefix.weekly", Monthly: "prefix.monthly",
}

// statusKeys are the text keys of the job status words (the status cell).
var statusKeys = [...]string{
	statusFailed: "status.failed", statusMissed: "status.missed", statusPartial: "status.partial", statusHeld: "status.held",
	statusOK: "status.ok", statusNoRun: "status.no_run", statusDisabled: "status.disabled",
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
		return p.t("window.24h")
	case 48 * time.Hour:
		return p.t("window.48h")
	default:
	}
	hours := d.Hours()
	days := int(math.Round(hours / 24))
	exactDays := math.Abs(hours-float64(days)*24) <= 1
	switch {
	case exactDays && days == 1:
		return p.t("window.day")
	case exactDays && days == 2:
		return p.t("window.2days")
	case exactDays && days == 7:
		return p.t("window.7days")
	case p.frequency == Monthly && days >= 28 && days <= 31 && periodBefore(p.until, Monthly, p.loc).Equal(p.from):
		return p.t("window.month")
	case exactDays && days > 2:
		return p.t("window.days", days)
	default:
		return p.t("window.period")
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

// ofJobs picks the ".one_job" (one job in total), ".one" (n == 1) or
// ".other" form of key ("1 of 5 jobs needs attention").
func ofJobs(key string, n, jobs int) string {
	switch {
	case jobs == 1:
		return key + ".one_job"
	case n == 1:
		return key + ".one"
	default:
		return key + ".other"
	}
}

func (d *summaryData) overall() overall {
	p := &d.Period
	c := d.counts()
	w := p.window()
	low := d.lowDestinations()
	alerts := d.ShowAlerts && d.SecurityAlerts > 0
	extra := []string{}
	if low > 0 {
		extra = append(extra, p.plural(int64(low), "banner.low"))
	}
	if alerts {
		extra = append(extra, p.plural(d.SecurityAlerts, "banner.alerts"))
	}
	tail := ""
	if len(extra) > 0 {
		tail = "; " + p.joinAnd(extra)
	}
	switch {
	case d.Jobs == 0 || len(d.JobRows) == 0:
		return overall{level: levelOK, email: email.LevelNone, word: p.t("banner.no_jobs.word"), text: p.t("banner.no_jobs.text")}
	case c[statusFailed] > 0 || c[statusMissed] > 0:
		var parts []string
		if n := c[statusFailed]; n > 0 {
			parts = append(parts, p.t(ofJobs("banner.failed", n, d.Jobs), n, d.Jobs))
		}
		if n := c[statusMissed]; n > 0 {
			parts = append(parts, p.plural(int64(n), "banner.missed"))
		}
		if n := c[statusPartial] + c[statusHeld]; n > 0 {
			parts = append(parts, p.plural(int64(n), "banner.more"))
		}
		word := p.t("banner.failed.word", c[statusFailed])
		if c[statusFailed] == 0 {
			word = p.t("banner.missed.word", c[statusMissed])
		}
		return overall{level: levelFailure, email: email.LevelFailure, word: word,
			text: p.t("banner.failure.text", upperFirst(p.joinAnd(parts)), w, tail)}
	case c.problems() > 0:
		n := c.problems()
		return overall{level: levelWarning, email: email.LevelWarning, word: p.t("banner.attention.word"),
			text: p.t(ofJobs("banner.attention", n, d.Jobs), n, d.Jobs, w, tail)}
	}
	var text string
	switch {
	case c[statusOK] == len(d.JobRows) && d.Jobs == 1:
		text = p.t("banner.ok.one_job", w)
	case c[statusOK] == len(d.JobRows):
		text = p.t("banner.ok.all", d.Jobs, w)
	default:
		parts := []string{p.t("banner.ok.count", c[statusOK])}
		if n := c[statusNoRun]; n > 0 {
			parts = append(parts, p.t("banner.ok.no_run", n))
		}
		if n := c[statusDisabled]; n > 0 {
			parts = append(parts, p.t("banner.ok.disabled", n))
		}
		text = p.t("banner.ok.no_problems", w, strings.Join(parts, ", "))
	}
	if len(extra) > 0 {
		return overall{level: levelWarning, email: email.LevelWarning, word: p.t("banner.attention.word"), text: p.t("banner.ok.but", text, p.joinAnd(extra))}
	}
	return overall{level: levelOK, email: email.LevelOK, word: p.t("banner.all_ok.word"), text: p.t("banner.ok.end", text)}
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// words are the job counts, worst first ("2 failed", "1 missed", "11
// OK"); upper writes the failed count in capitals (the subject).
func (c counts) words(p *summaryPeriod, upper bool) []string {
	var out []string
	for _, x := range []struct {
		s   jobStatus
		key string
	}{{statusFailed, "count.failed"}, {statusMissed, "count.missed"}, {statusPartial, "count.partial"}, {statusHeld, "count.held"}, {statusOK, "count.ok"}} {
		if n := c[x.s]; n > 0 {
			key := x.key
			if upper && x.s == statusFailed {
				key = "count.failed_upper"
			}
			out = append(out, p.t(key, n))
		}
	}
	return out
}

// prefix is "Daily summary" (or "Summary preview").
func (d *summaryData) prefix() string {
	if d.Period.preview {
		return d.Period.t("prefix.preview")
	}
	key := prefixKeys[d.Period.frequency]
	if key == "" {
		key = "prefix.periodic"
	}
	return d.Period.t(key)
}

// title is the inbox title: the counts, worst first ("Daily summary: 2
// failed, 1 missed, 11 OK"). The e-mail heading is prefix() alone (the
// banner and figures carry the counts there).
func (d *summaryData) title() string {
	p := &d.Period
	c := d.counts()
	var state string
	switch {
	case len(d.JobRows) == 0:
		state = p.t("state.no_jobs")
	case c[statusOK] == len(d.JobRows) && d.Jobs == 1:
		state = p.t("state.one_ok")
	case c[statusOK] == len(d.JobRows):
		state = p.t("state.all_ok", d.Jobs)
	default:
		words := c.words(p, false)
		switch {
		case len(words) == 0:
			state = p.t("state.no_problems")
		case c.problems() == 0:
			state = p.t("state.no_problems_with", strings.Join(words, ", "))
		default:
			state = strings.Join(words, ", ")
		}
	}
	return p.t("title", d.prefix(), state)
}

// subject names the worst state first: "2 FAILED, 1 missed", "needs
// attention" or "all 14 jobs OK".
func (d *summaryData) subject(o overall) string {
	p := &d.Period
	c := d.counts()
	var s string
	switch o.level {
	case levelFailure:
		var parts []string
		if n := c[statusFailed]; n > 0 {
			parts = append(parts, p.t("count.failed_upper", n))
		}
		if n := c[statusMissed]; n > 0 {
			parts = append(parts, p.t("count.missed", n))
		}
		s = strings.Join(parts, ", ")
	case levelWarning:
		s = p.t("state.attention")
	default:
		switch {
		case len(d.JobRows) == 0:
			s = p.t("state.no_jobs")
		case c[statusOK] == len(d.JobRows) && d.Jobs == 1:
			s = p.t("state.one_ok")
		case c[statusOK] == len(d.JobRows):
			s = p.t("state.all_ok", d.Jobs)
		default:
			s = p.t("state.no_problems")
		}
	}
	return p.t("subject", d.prefix(), s)
}

// preheader is the inbox preview line ("2 failed · 11 OK · the last 24
// hours").
func (d *summaryData) preheader(o overall) string {
	parts := d.counts().words(&d.Period, false)
	if o.level == levelOK && len(parts) > 0 {
		parts = append([]string{d.Period.t("banner.all_ok.word")}, parts...)
	}
	return strings.Join(append(parts, d.Period.window()), " · ")
}

var runStatusOrder = []struct{ status, key string }{
	{string(sqlc.RunStatusSuccess), "run.succeeded"},
	{string(sqlc.RunStatusPartial), "run.partial"},
	{string(sqlc.RunStatusFailed), "run.failed"},
	{string(sqlc.RunStatusCanceled), "run.canceled"},
	{string(sqlc.RunStatusSkipped), "run.skipped"},
}

var testStatusOrder = []struct{ status, key string }{
	{string(sqlc.TestRestoreStatusPassed), "test.passed"},
	{string(sqlc.TestRestoreStatusWarnings), "test.warnings"},
	{string(sqlc.TestRestoreStatusFailed), "test.failed"},
	{string(sqlc.TestRestoreStatusError), "test.error"},
	{string(sqlc.TestRestoreStatusCanceled), "test.canceled"},
}

func (p *summaryPeriod) countsSentence(counts map[string]int64, order []struct{ status, key string }) (int64, string) {
	var total int64
	var parts []string
	for _, o := range order {
		if n := counts[o.status]; n > 0 {
			total += n
			parts = append(parts, p.t("status_count", n, p.t(o.key)))
		}
	}
	return total, strings.Join(parts, ", ")
}

func (d *summaryData) runsSentence() string {
	p := &d.Period
	total, parts := p.countsSentence(d.Runs, runStatusOrder)
	if total == 0 {
		return p.t("runs.none")
	}
	return p.t("runs.finished", p.plural(total, "runs.count"), parts)
}

// note explains a job's status in fixed words and numbers.
func (r *jobRow) note(p *summaryPeriod) string {
	var n []string
	if r.Failed > 0 {
		reason := failureText(p.locale, r.FailureCode)
		if reason == "" {
			reason = p.t("note.backup_failed")
		}
		if r.Failed > 1 {
			reason = p.t("note.failed_runs", reason, r.Failed)
		}
		n = append(n, reason)
	}
	if r.TestRestoresFailed > 0 {
		n = append(n, p.plural(r.TestRestoresFailed, "note.tests_failed"))
	}
	if r.MissedSince != nil {
		n = append(n, p.t("note.missed_since", p.time(*r.MissedSince)))
	}
	if r.NoSuccess && r.Failed == 0 {
		n = append(n, p.t("note.no_success"))
	}
	if r.Partial > 0 {
		n = append(n, p.plural(r.Partial, "note.partial"))
	}
	if r.Warned > 0 {
		n = append(n, p.plural(r.Warned, "note.warned"))
	}
	if r.HeldRuns > 0 {
		n = append(n, p.t("note.held", p.plural(r.HeldRuns, "note.held_backups"),
			p.plural(int64(r.HoldDestinations), "note.destinations")))
	}
	return strings.Join(n, "; ")
}

// word is the status cell ("Warnings" for a job whose only issue is runs
// with warnings).
func (r *jobRow) word(p *summaryPeriod) string {
	if r.Status == statusPartial && r.Partial == 0 {
		return p.t("status.warnings")
	}
	return p.t(statusKeys[r.Status])
}

// jobsTable lists the first `listed` jobs (worst first, so OK rows go
// before problem rows); the rest becomes one line.
func (d *summaryData) jobsTable(listed int) email.Table {
	p := &d.Period
	t := email.Table{
		Title: p.t("jobs.title"), Status: true,
		Columns: []string{p.t("jobs.col.status"), p.t("jobs.col.job"), p.t("jobs.col.ok_fail"), p.t("jobs.col.last_run"),
			p.t("jobs.col.size"), p.t("jobs.col.stored")},
		NumericColumns: []int{2, 3, 4, 5}, NarrowHidden: []int{2, 5},
	}
	rows := d.JobRows
	var rest []jobRow
	if len(rows) > listed {
		rows, rest = rows[:listed], rows[listed:]
	}
	for i := range rows {
		r := &rows[i]
		last, stored := "–", "–"
		if r.LastAt != nil {
			last = p.cellTime(*r.LastAt)
		}
		if r.HasStored {
			stored = p.size(r.Stored)
		}
		size := "–"
		if r.LastAt != nil {
			size = p.size(r.Bytes)
		}
		t.Rows = append(t.Rows, email.Row{
			Cells: []string{r.word(p), clampRunes(r.Name, maxJobNameCell), fmt.Sprintf("%d/%d", r.OK, r.Failed), last, size, stored},
			Level: statusLevels[r.Status], Note: r.note(p),
		})
	}
	if len(rest) > 0 {
		problems := 0
		for i := range rest {
			if rest[i].Status.problem() {
				problems++
			}
		}
		row := email.Row{Cells: []string{p.t("status.ok"), p.t("jobs.more_ok", len(rest)), "", "", "", ""}, Level: email.LevelOK}
		if problems > 0 {
			key := "jobs.more_problem.other"
			if problems == 1 {
				key = "jobs.more_problem.one"
			}
			row = email.Row{
				Cells: []string{p.t("status.more"), p.t(key, len(rest), problems), "", "", "", ""},
				Level: email.LevelFailure, Note: p.t("jobs.more_note"),
			}
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}

var restoreStatusRows = []struct {
	status, key string
	level       email.Level
}{
	{string(sqlc.RestoreStatusSucceeded), "restores.word.succeeded", email.LevelOK},
	{string(sqlc.RestoreStatusFailed), "restores.word.failed", email.LevelFailure},
	{string(sqlc.RestoreStatusCanceled), "restores.word.canceled", email.LevelNone},
}

var testStatusRows = []struct {
	status, key string
	level       email.Level
}{
	{string(sqlc.TestRestoreStatusPassed), "restores.word.passed", email.LevelOK},
	{string(sqlc.TestRestoreStatusWarnings), "restores.word.warnings", email.LevelWarning},
	{string(sqlc.TestRestoreStatusFailed), "restores.word.failed", email.LevelFailure},
	{string(sqlc.TestRestoreStatusError), "restores.word.error", email.LevelFailure},
	{string(sqlc.TestRestoreStatusCanceled), "restores.word.canceled", email.LevelNone},
}

// restoresTable counts restores and test restores by result (nil when
// there were none).
func (d *summaryData) restoresTable() *email.Table {
	p := &d.Period
	t := email.Table{
		Title: p.t("restores.title"), Status: true,
		Columns:        []string{p.t("restores.col.result"), p.t("restores.col.kind"), p.t("restores.col.count")},
		NumericColumns: []int{2},
	}
	for _, s := range restoreStatusRows {
		if n := d.Restores[s.status]; n > 0 {
			t.Rows = append(t.Rows, email.Row{Cells: []string{p.t(s.key), p.t("restores.kind.restores"), strconv.FormatInt(n, 10)}, Level: s.level})
		}
	}
	for _, s := range testStatusRows {
		if n := d.TestRestores[s.status]; n > 0 {
			t.Rows = append(t.Rows, email.Row{Cells: []string{p.t(s.key), p.t("restores.kind.tests"), strconv.FormatInt(n, 10)}, Level: s.level})
		}
	}
	if len(t.Rows) == 0 {
		return nil
	}
	return &t
}

// storageTable is the destination usage (staff): free space only from
// the stored answer of the hourly space check (D-227), low space as a
// warning row (the D-225 rule), a failed or stale check as a warning row,
// a backend that reports no space and a destination not checked yet as
// neutral rows. Status words stay short (the column must fit a phone
// screen); the note explains them.
func (d *summaryData) storageTable() email.Table {
	p := &d.Period
	t := email.Table{
		Title: p.t("storage.title"), Status: true,
		Columns: []string{p.t("storage.col.status"), p.t("storage.col.destination"), p.t("storage.col.stored"),
			p.t("storage.col.free"), p.t("storage.col.backups")},
		NumericColumns: []int{2, 3, 4}, NarrowHidden: []int{4},
	}
	n := min(len(d.Destinations), maxSummaryDestinations)
	for i := range n {
		t.Rows = append(t.Rows, d.storageRow(&d.Destinations[i]))
	}
	if more := len(d.Destinations) - n; more > 0 {
		key := "storage.more.other"
		if more == 1 {
			key = "storage.more.one"
		}
		t.Rows = append(t.Rows, email.Row{Cells: []string{p.t("status.more"), p.t(key, more), "", "", ""}})
	}
	return t
}

// checkedNote says when a space check ran: the time of day when it was
// on the day of now (the render time), else the short date and time.
func (p *summaryPeriod) checkedNote(at, now time.Time) string {
	a, n := at.In(p.loc), now.In(p.loc)
	if a.Year() == n.Year() && a.YearDay() == n.YearDay() {
		return p.t("storage.checked.today", a.Format("15:04"))
	}
	return p.t("storage.checked.earlier", p.cellTime(at))
}

// spaceFailureText is the short reason of a failed space check (fixed
// texts per stored code, never server text).
func (p *summaryPeriod) spaceFailureText(code string) string {
	switch code {
	case destinations.SpaceCodeNotReady:
		return p.t("storage.fail.not_ready")
	case destinations.SpaceCodeFailed, "":
		return p.t("storage.fail.unknown")
	default:
		if _, ok := texts[i18n.EN]["space_code."+code]; ok {
			return p.t("space_code." + code)
		}
		// storage.ErrorCode.Message's text for an unknown code.
		return p.t("space_code." + string(storage.CodeInternal))
	}
}

// renderNow is the time stored space answers are judged against: the
// render time (the summary reads them when it is rendered, possibly hours
// after the period ended), else the end of the period.
func (d *summaryData) renderNow() time.Time {
	if d.Now.IsZero() {
		return d.Period.until
	}
	return d.Now
}

// storageRow is one destination's row.
func (d *summaryData) storageRow(du *destinationUsage) email.Row {
	p := &d.Period
	sp := du.Space
	row := email.Row{
		Cells: []string{"", clampRunes(du.Name, maxJobNameCell), p.size(du.Bytes), "–", strconv.FormatInt(du.Backups, 10)},
	}
	now := d.renderNow()
	stale := sp.Stale(now)
	when := p.checkedNote(sp.CheckedAt, now)
	if stale {
		when = p.t("storage.checked.stale", p.cellTime(sp.CheckedAt), int(destinations.SpaceStaleAfter/time.Hour))
	}
	// staleWarns turns a neutral or OK row of an old answer into a warning:
	// the hourly check stopped (for every destination, whatever it reports).
	staleWarns := func() {
		if stale {
			row.Cells[0], row.Level = p.t("storage.word.stale"), email.LevelWarning
		}
	}
	var notes []string
	switch {
	case !sp.Checked():
		row.Cells[0] = p.t("storage.word.not_checked")
		if sp.SettingsChanged {
			notes = append(notes, p.t("storage.note.settings"))
		} else {
			notes = append(notes, p.t("storage.note.not_run"))
		}
	case sp.Status == destinations.SpaceUnsupported:
		row.Cells[0] = p.t("storage.word.no_limit")
		if du.Type == destinations.TypeS3 {
			notes = append(notes, p.t("storage.note.no_limit_s3"))
		} else {
			notes = append(notes, p.t("storage.note.no_limit_server"))
		}
		staleWarns()
		notes = append(notes, when)
	case sp.Status == destinations.SpaceUnavailable:
		row.Cells[0], row.Level = p.t("storage.word.failed"), email.LevelWarning
		notes = append(notes, p.spaceFailureText(sp.Code), when)
	case !sp.Space.FreeKnown:
		row.Cells[0] = p.t("storage.word.no_limit")
		notes = append(notes, p.t("storage.note.no_free"))
		staleWarns()
		notes = append(notes, when)
	default:
		row.Cells[3] = p.size(sp.Space.Free)
		row.Cells[0], row.Level = p.t("storage.word.ok"), email.LevelOK
		switch sp.Low {
		case destinations.LowFraction:
			row.Cells[0], row.Level = p.t("storage.word.low"), email.LevelWarning
			notes = append(notes, p.t("storage.note.low_fraction"))
		case destinations.LowBelowNewestSet:
			row.Cells[0], row.Level = p.t("storage.word.low"), email.LevelWarning
			notes = append(notes, p.t("storage.note.low_newest"))
		default:
			staleWarns()
		}
		notes = append(notes, when)
	}
	row.Note = strings.Join(notes, "; ")
	return row
}

// securityTable is the admins' security alert count.
func (d *summaryData) securityTable() email.Table {
	p := &d.Period
	t := email.Table{Title: p.t("security.title"), Status: true, Columns: []string{p.t("security.col.status"), p.t("security.col.alerts")}}
	if d.SecurityAlerts == 0 {
		t.Rows = []email.Row{{Cells: []string{p.t("security.word.ok"), p.t("security.none")}, Level: email.LevelOK}}
	} else {
		t.Rows = []email.Row{{
			Cells: []string{p.t("security.word.review"), p.plural(d.SecurityAlerts, "security.alerts")},
			Level: email.LevelWarning, Note: p.t("security.review_note"),
		}}
	}
	// Accepted risks (D-228) and tokens without expiry (D-229): neutral,
	// never a problem, but always visible to admins.
	if r := d.Risks; r.Accepted > 0 || r.TokensNeverExpiring > 0 {
		text := p.plural(r.Accepted, "security.risks")
		if r.Forever > 0 {
			text = p.t("security.risks.forever", text, r.Forever)
		}
		if r.TokensNeverExpiring > 0 {
			text += "; " + p.plural(r.TokensNeverExpiring, "security.tokens")
		}
		t.Rows = append(t.Rows, email.Row{Cells: []string{p.t("security.word.accepted"), text}, Level: email.LevelNone, Note: p.t("security.risks_note")})
	}
	return t
}

// figures are the key-figure tiles.
func (d *summaryData) figures(o overall) []email.Figure {
	p := &d.Period
	c := d.counts()
	okLevel := email.LevelNone
	if o.level == levelOK {
		okLevel = email.LevelOK
	}
	figs := []email.Figure{{Label: p.t("figure.jobs_ok"), Value: fmt.Sprintf("%d/%d", c[statusOK], d.Jobs), Level: okLevel}}
	failed := email.Figure{Label: p.t("figure.failed"), Value: strconv.Itoa(c[statusFailed])}
	if c[statusMissed] > 0 {
		failed = email.Figure{Label: p.t("figure.failed_missed"), Value: fmt.Sprintf("%d/%d", c[statusFailed], c[statusMissed])}
	}
	if c[statusFailed]+c[statusMissed] > 0 {
		failed.Level = email.LevelFailure
	}
	figs = append(figs, failed, email.Figure{Label: p.t("figure.written"), Value: p.size(d.Written)})
	total, _ := p.countsSentence(d.TestRestores, testStatusOrder)
	if total > 0 {
		good := d.TestRestores[string(sqlc.TestRestoreStatusPassed)] + d.TestRestores[string(sqlc.TestRestoreStatusWarnings)]
		f := email.Figure{Label: p.t("figure.tests"), Value: fmt.Sprintf("%d/%d", good, total), Level: email.LevelOK}
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
	p := &d.Period
	o := d.overall()
	title := d.title()
	periodLine := p.t("period", p.time(p.from), p.time(p.until), p.zone)
	var paragraphs []string
	switch {
	case p.preview:
		paragraphs = append(paragraphs, p.t("preview", p.window()))
	case p.catchUp:
		paragraphs = append(paragraphs, p.t("catch_up"))
	}
	paragraphs = append(paragraphs, periodLine, d.runsSentence())
	n := email.Notice{
		Subject: clampRunes(d.subject(o), email.MaxSubjectLen), Heading: d.prefix(),
		Preheader: d.preheader(o),
		Banner:    &email.Banner{Level: o.email, Word: o.word, Text: o.text},
		Figures:   d.figures(o),
		Action:    &email.Action{Label: p.t("action"), URL: dashboard},
		Locale:    p.locale,
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
			paragraphs = append(paragraphs, p.t("storage.none"))
		} else {
			n.Tables = append(n.Tables, d.storageTable())
		}
	}
	if d.ShowAlerts && !alertsFirst {
		n.Tables = append(n.Tables, d.securityTable())
	}
	n.Paragraphs = paragraphs
	n.Footer = p.t("footer", p.zone, email.DefaultFooter(p.locale))
	if jobsAt >= 0 {
		fitJobs(d, &n, jobsAt)
	}

	c := d.counts()
	body := p.t("body.period", p.time(p.from), p.time(p.until), p.zone)
	if words := c.words(p, false); len(words) > 0 {
		body += " " + p.t("body.jobs", strings.Join(words, ", "))
	}
	_, runParts := p.countsSentence(d.Runs, runStatusOrder)
	if runParts != "" {
		body += " " + p.t("body.backups", runParts)
	} else {
		body += " " + p.t("body.no_backup")
	}
	if _, tp := p.countsSentence(d.TestRestores, testStatusOrder); tp != "" {
		body += " " + p.t("body.tests", tp)
	}
	if c.problems() > 0 {
		body += " " + p.plural(int64(c.problems()), "body.attention")
	}
	return notify.SummaryContent{Title: clampRunes(title, 200), Body: clampRunes(body, 1000), Notice: n}
}
