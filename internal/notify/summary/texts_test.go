package summary

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/destinations"
	"github.com/jlentink/bekup/internal/i18n"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/notify/email"
	"github.com/jlentink/bekup/internal/riskaccept"
	"github.com/jlentink/bekup/internal/storage"
)

// TestTextsParity: every language has exactly the English keys, no empty
// text and the same formatting verbs (D-237).
func TestTextsParity(t *testing.T) {
	t.Parallel()
	for _, p := range texts.Problems() {
		t.Error(p)
	}
	// Plural keys come in pairs: ".one" without a count, ".other" with one
	// (the "+N more" lines and the ".one_job" sets choose by another count).
	for key, v := range texts[i18n.EN] {
		base, ok := strings.CutSuffix(key, ".one")
		if !ok {
			continue
		}
		if _, sets := texts[i18n.EN][base+".one_job"]; sets {
			continue
		}
		many, ok := texts[i18n.EN][base+".other"]
		if !ok {
			t.Errorf("%s has no .other form", key)
		}
		if strings.Contains(v, "%d") && !strings.HasPrefix(key, "jobs.more_problem") && !strings.HasPrefix(key, "storage.more") {
			t.Errorf("%s takes a count", key)
		}
		if !strings.Contains(many, "%d") {
			t.Errorf("%s.other takes no count", base)
		}
	}
}

// TestSpaceCodeTexts: the English space check reasons are the storage
// package's messages, for every storage code (read from the source).
func TestSpaceCodeTexts(t *testing.T) {
	t.Parallel()
	codes := stringConsts(t, filepath.Join("..", "..", "storage", "errors.go"), "Code")
	if len(codes) < 15 {
		t.Fatalf("found only %d storage codes", len(codes))
	}
	for name, c := range codes {
		key := "space_code." + c
		got, ok := texts[i18n.EN][key]
		if !ok {
			t.Errorf("no text for storage.%s", name)
			continue
		}
		if want := storage.ErrorCode(c).Message(); got != want {
			t.Errorf("%s = %q, storage says %q", key, got, want)
		}
	}
	for key := range texts[i18n.EN] {
		if c, ok := strings.CutPrefix(key, "space_code."); ok {
			found := false
			for _, v := range codes {
				found = found || v == c
			}
			if !found {
				t.Errorf("%s: no such storage code", key)
			}
		}
	}
	p := summaryPeriod{locale: i18n.NL}
	if got := p.spaceFailureText("no_such_code"); got != texts[i18n.NL]["space_code.internal"] {
		t.Errorf("unknown code = %q", got)
	}
}

func dutch(d *summaryData) *summaryData {
	d.Period.locale = i18n.NL
	return d
}

// TestSummaryGoldenDutch pins the Dutch renderings of the golden cases
// (go test -update writes them; review like the English ones).
func TestSummaryGoldenDutch(t *testing.T) {
	t.Parallel()
	for name, d := range goldenSummaries(t) {
		name += "_nl"
		out := render(t, buildSummary(dutch(d), "https://bekup.example.com/"))
		if !strings.Contains(out.HTML, `lang="nl"`) {
			t.Errorf("%s: the HTML is not marked Dutch", name)
		}
		if enc := email.EncodedLen(out.HTML); enc > maxSummaryHTML {
			t.Errorf("%s is %d bytes of HTML as sent (budget %d)", name, enc, maxSummaryHTML)
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

// textPieces are the visible texts of a summary that carry language:
// inbox title and body, subject, heading, preheader, banner, paragraphs,
// button, footer, figure labels, table titles, columns, status words,
// "+N more" lines and notes.
func textPieces(c *notify.SummaryContent) []string {
	n := &c.Notice
	out := []string{c.Title, c.Body, n.Subject, n.Heading, n.Preheader, n.Footer}
	if n.Banner != nil {
		out = append(out, n.Banner.Word, n.Banner.Text)
	}
	if n.Action != nil {
		out = append(out, n.Action.Label)
	}
	out = append(out, n.Paragraphs...)
	for _, f := range n.Figures {
		out = append(out, f.Label)
	}
	for _, tb := range n.Tables {
		out = append(out, tb.Title)
		out = append(out, tb.Columns...)
		for _, r := range tb.Rows {
			out = append(out, r.Cells[0], r.Note)
			if strings.HasPrefix(r.Cells[1], "+") || len(tb.Columns) == 2 || len(tb.Columns) == 3 {
				out = append(out, r.Cells[1])
			}
		}
	}
	return out
}

// sameInBoth are the pieces that are the same word in both languages.
var sameInBoth = map[string]bool{"": true, "OK": true, "Status": true}

// TestSummaryDutchHasNoEnglish: a Dutch summary shares no text with the
// English one of the same data (beyond OK, Status and names), so no
// English sentence is left; and every time is formatted in Dutch.
func TestSummaryDutchHasNoEnglish(t *testing.T) {
	t.Parallel()
	cases := map[string]func() *summaryData{}
	for name := range goldenSummaries(t) {
		cases[name] = func() *summaryData { return goldenSummaries(t)[name] }
	}
	cases["preview"] = func() *summaryData { return allOK(t, period(t, true)) }
	cases["catch_up"] = func() *summaryData {
		p := dailyPeriod(t)
		p.catchUp = true
		return allOK(t, p)
	}
	cases["no_jobs"] = func() *summaryData { return newData(dailyPeriod(t), &summaryInput{}) }
	cases["risks"] = func() *summaryData {
		d := allOK(t, dailyPeriod(t))
		d.ShowAlerts, d.ShowUsage = true, true
		d.Risks = riskaccept.Counts{Accepted: 3, Forever: 1, TokensNeverExpiring: 2}
		return d
	}
	cases["one_risk"] = func() *summaryData {
		d := allOK(t, dailyPeriod(t))
		d.ShowAlerts = true
		d.Risks = riskaccept.Counts{Accepted: 1, TokensNeverExpiring: 1}
		return d
	}
	englishMonth := regexp.MustCompile(`\b(Mon|Tue|Wed|Thu|Fri|Sat|Sun|Mar|May|Oct)\b`)
	for name, mk := range cases {
		en := buildSummary(mk(), "https://bekup.example.com/")
		nl := buildSummary(dutch(mk()), "https://bekup.example.com/")
		english := map[string]bool{}
		for _, s := range textPieces(&en) {
			english[s] = true
		}
		for _, s := range textPieces(&nl) {
			if english[s] && !sameInBoth[s] {
				t.Errorf("%s: %q is the same in English", name, s)
			}
			if englishMonth.MatchString(s) {
				t.Errorf("%s: %q has an English date", name, s)
			}
		}
		// The rendered text has none of the English paragraphs either.
		out := render(t, nl)
		for _, s := range en.Notice.Paragraphs {
			if strings.Contains(out.Text, s) {
				t.Errorf("%s: English paragraph %q in the Dutch text", name, s)
			}
		}
	}
}

// TestSummaryDutchForms: Dutch plurals, dates and sizes.
func TestSummaryDutchForms(t *testing.T) {
	t.Parallel()
	p := dailyPeriod(t)
	p.locale = i18n.NL
	for _, c := range []struct{ got, want string }{
		{p.plural(1, "runs.count"), "1 run"},
		{p.plural(2, "note.held_backups"), "2 onvolledige back-ups"},
		{p.plural(1, "security.risks"), "1 geaccepteerd risico"},
		{p.plural(4, "security.risks"), "4 geaccepteerde risico's"},
		{p.joinAnd([]string{"a", "b", "c"}), "a, b en c"},
		{p.time(at(t, "2026-11-07T07:00:00Z")), "za 7 nov 2026, 08:00"},
		{p.cellTime(at(t, "2026-03-07T07:00:00Z")), "7 mrt 08:00"},
		{p.size(10 << 29), "5,0 GiB"},
		{p.size(900), "900 B"},
		{p.checkedNote(p.until.Add(-time.Hour), p.until), "gecontroleerd om 07:00"},
		{failureText(i18n.NL, "dump_failed"), "dump mislukt"},
		{failureText(i18n.NL, "unknown_code"), ""},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

// TestSummaryWorstCaseSizeDutch: the longer Dutch texts keep the worst
// case within the budget too, with everything after the jobs table.
func TestSummaryWorstCaseSizeDutch(t *testing.T) {
	t.Parallel()
	p := dailyPeriod(t)
	p.locale = i18n.NL
	name := func(i int) string { return strings.Repeat(`&"<😀`, 25) + fmt.Sprintf("%03d", i) }
	d := newData(p, bigInput(t, p, 120, 120, name))
	d.ShowUsage, d.ShowAlerts, d.SecurityAlerts = true, true, 9
	d.Restores["failed"], d.TestRestores["error"] = 3, 2
	d.Risks = riskaccept.Counts{Accepted: 4, Forever: 2, TokensNeverExpiring: 3}
	for i := range 12 {
		d.Destinations = append(d.Destinations, destinationUsage{
			Name: name(i), Bytes: 1 << 40, Backups: 999,
			Space: checkedSpace(1<<50, destinations.LowBelowNewestSet, p.until.Add(-30*time.Hour)),
		})
	}
	out := render(t, buildSummary(d, "https://bekup.example.com/"))
	if enc := email.EncodedLen(out.HTML); enc > maxSummaryHTML {
		t.Errorf("HTML as sent is %d bytes (budget %d)", enc, maxSummaryHTML)
	}
	for _, want := range []string{">Herstel</h2>", ">Opslag</h2>", ">Beveiliging</h2>", "Dashboard openen", "Tijden zijn in Europe/Amsterdam."} {
		if !strings.Contains(out.HTML, want) {
			t.Errorf("HTML lacks %q", want)
		}
	}
}
