package email

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/i18n"
)

var update = flag.Bool("update", false, "rewrite the golden e-mails in testdata/")

// statusNotice is a notice with every optional part (the shape of the
// periodic summary).
func statusNotice() Notice {
	return Notice{
		Subject:   "[bekup] Daily summary: 2 FAILED, 1 missed",
		Heading:   "Daily summary: 2 failed, 1 missed",
		Preheader: "2 failed · 1 missed · 11 OK · last 24 hours",
		Banner:    &Banner{Level: LevelFailure, Word: "2 failed", Text: "2 of 14 jobs failed and 1 missed its backup in the last 24 hours."},
		Paragraphs: []string{
			"Sat 7 Nov 2026, 08:00 – Sun 8 Nov 2026, 08:00 (Europe/Amsterdam)",
		},
		Figures: []Figure{
			{Label: "Jobs OK", Value: "11/14", Level: LevelWarning},
			{Label: "Failed", Value: "2", Level: LevelFailure},
			{Label: "Written", Value: "38.2 GiB"},
			{Label: "Test restores", Value: "3/3", Level: LevelOK},
		},
		Tables: []Table{{
			Title:          "Jobs",
			Columns:        []string{"Status", "Job", "OK/Fail", "Last run", "Size", "Stored"},
			Status:         true,
			NumericColumns: []int{2, 3, 4, 5},
			NarrowHidden:   []int{2, 5},
			Rows: []Row{
				{Cells: []string{"Failed", "Billing <prod>", "0/3", "8 Nov 03:12", "0 B", "412.0 GiB"}, Level: LevelFailure, Note: "target unreachable"},
				{Cells: []string{"Failed", "CRM", "5/1", "8 Nov 06:00", "1.2 GiB", "88.4 GiB"}, Level: LevelFailure, Note: "dump failed"},
				{Cells: []string{"Missed", "Docs", "0/0", "5 Nov 04:00", "0 B", "3.1 GiB"}, Level: LevelFailure, Note: "missed since Thu 5 Nov, 04:00"},
				{Cells: []string{"Partial", "Events", "1/0", "8 Nov 02:00", "6.0 GiB", "120.9 GiB"}, Level: LevelWarning, Note: "partially failed"},
				{Cells: []string{"OK", "Forms", "24/0", "8 Nov 07:30", "12.7 GiB", "301.5 GiB"}, Level: LevelOK},
				{Cells: []string{"OK", "Garden", "1/0", "8 Nov 01:00", "18.3 GiB", "1.4 TiB"}, Level: LevelOK},
				{Cells: []string{"No run", "Handbook", "0/0", "–", "0 B", "512.0 MiB"}},
			},
		}, {
			Title:          "Storage",
			Columns:        []string{"Status", "Destination", "Stored", "Free", "Backups"},
			Status:         true,
			NumericColumns: []int{2, 3, 4},
			NarrowHidden:   []int{4},
			Rows: []Row{
				{Cells: []string{"Low space", "nas-01", "1.9 TiB", "96.0 GiB", "412"}, Level: LevelWarning, Note: "less than 10% free"},
				{Cells: []string{"OK", "s3 offsite", "2.4 TiB", "–", "512"}, Level: LevelOK},
			},
		}},
		Action: &Action{Label: "Open the dashboard", URL: "https://bekup.example.com/"},
	}
}

func goldenNotices() map[string]Notice {
	allOK := statusNotice()
	allOK.Subject, allOK.Heading = "[bekup] Daily summary: all 3 jobs OK", "Daily summary: all 3 jobs OK"
	allOK.Preheader = "All OK · 3 jobs · last 24 hours"
	allOK.Banner = &Banner{Level: LevelOK, Word: "All OK", Text: "All 3 jobs ran fine in the last 24 hours."}
	allOK.Figures = []Figure{{Label: "Jobs OK", Value: "3/3", Level: LevelOK}, {Label: "Failed", Value: "0"}, {Label: "Written", Value: "4.1 GiB"}}
	allOK.Tables = []Table{allOK.Tables[0]}
	allOK.Tables[0].Rows = allOK.Tables[0].Rows[4:6]
	return map[string]Notice{
		"status":          statusNotice(),
		"all_ok":          allOK,
		"verification":    verificationNotice("https://bekup.example.com/profile/notifications/verify#Zm9vYmFy", i18n.EN),
		"verification_nl": verificationNotice("https://bekup.example.com/profile/notifications/verify#Zm9vYmFy", i18n.NL),
		"security_alert": {
			Subject:    "[bekup] Security alert",
			Heading:    "Security alert",
			Paragraphs: []string{"The SSRF allowlist was widened: 10.0.0.0/8 now allowed for destinations. Review the change in the audit log."},
			Details:    []Detail{{Label: "Event", Value: "security.system"}, {Label: "Time", Value: "2026-11-08 07:12:03 UTC"}},
			Action:     &Action{Label: "Open in bekup", URL: "https://bekup.example.com/admin/audit"},
		},
	}
}

// TestRenderGolden pins the rendered layout (regenerate with -update and
// review the files by eye, in a browser at 600 and 375 px, light and dark).
func TestRenderGolden(t *testing.T) {
	t.Parallel()
	for name, n := range goldenNotices() {
		c, err := Render(n)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for ext, got := range map[string]string{".html": c.HTML, ".txt": c.Text} {
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
				t.Errorf("%s differs from the rendered e-mail (run go test -update and review)", path)
			}
		}
	}
}

func TestRenderStatusParts(t *testing.T) {
	t.Parallel()
	c, err := Render(statusNotice())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<th scope="col"`, ">2 failed</p>", "2 of 14 jobs failed", "Billing &lt;prod&gt;", ">target unreachable</td>",
		`bgcolor="` + lightPalette.Failure.Soft + `"`, "border-left:4px solid " + lightPalette.Failure.Solid,
		`class="mn hs"`, "11/14", "2 failed · 1 missed", `lang="en"`, `name="color-scheme"`, `name="supported-color-schemes"`,
		"prefers-color-scheme:dark", "[data-ogsc]", string(msoOpen), string(msoClose), string(msoHead),
	} {
		if !strings.Contains(c.HTML, want) {
			t.Errorf("HTML lacks %q", want)
		}
	}
	// Outlook for Windows (Word) ignores row backgrounds and span
	// backgrounds: every cell of a problem row carries the tint, and a
	// status badge is a cell with its own bgcolor.
	tint := `bgcolor="` + lightPalette.Failure.Soft + `"`
	rows := regexp.MustCompile(`(?s)<tr class="rf[^"]*">.*?</tr>\n`).FindAllString(c.HTML, -1)
	if len(rows) < 2 {
		t.Fatalf("%d failure rows found", len(rows))
	}
	for _, row := range rows {
		if n := strings.Count(row, "<td"); strings.Count(row, tint) != n-1 && strings.Count(row, tint) != n {
			t.Errorf("a failure row has %d cells but %d tinted:\n%s", n, strings.Count(row, tint), row)
		}
	}
	if !strings.Contains(c.HTML, `<td class="pl qf" bgcolor="`+lightPalette.Surface+`"`) ||
		!strings.Contains(c.HTML, `<td class="pl po" bgcolor="`+lightPalette.OK.Soft+`"`) || strings.Contains(c.HTML, "<span class=\"pl") {
		t.Error("status badges must be cells with a bgcolor")
	}
	for _, want := range []string{
		"!! 2 failed: 2 of 14 jobs failed and 1 missed its backup in the last 24 hours. !!",
		"Jobs OK: 11/14  |  Failed: 2  |  Written: 38.2 GiB  |  Test restores: 3/3",
		">> Failed   Billing <prod>      0/3  8 Nov 03:12       0 B  412.0 GiB\n            target unreachable\n",
		">> Partial  Events",
		"   OK       Forms              24/0",
		">> Low space  nas-01       1.9 TiB  96.0 GiB      412\n              less than 10% free\n",
		"Jobs\n   Status",
	} {
		if !strings.Contains(c.Text, want) {
			t.Errorf("text lacks %q:\n%s", want, c.Text)
		}
	}
	// Every problem row carries the marker and a word.
	for _, line := range strings.Split(c.Text, "\n") {
		if strings.HasPrefix(line, ">> ") && !regexp.MustCompile(`^>> (Failed|Missed|Partial|Low space)`).MatchString(line) {
			t.Errorf("problem row without its word: %q", line)
		}
	}
	// Numeric columns are right-aligned.
	if !strings.Contains(c.Text, "Billing <prod>      0/3  8 Nov 03:12       0 B  412.0 GiB") || !strings.Contains(c.Text, "Handbook            0/0            –       0 B") {
		t.Errorf("numeric alignment:\n%s", c.Text)
	}
}

func TestRenderEscapesNewParts(t *testing.T) {
	t.Parallel()
	evil := `<script>alert(1)</script>"'`
	n := statusNotice()
	n.Preheader = evil
	n.Banner.Word, n.Banner.Text = "<b>x</b>", evil
	n.Figures[0].Label, n.Figures[0].Value = evil[:20], "<i>1</i>"
	n.Tables[0].Title = "<h1>t</h1>"
	n.Tables[0].Columns[1] = "<u>Job</u>"
	n.Tables[0].Rows[0].Cells[1] = evil
	n.Tables[0].Rows[0].Note = evil
	c, err := Render(n)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"<script", "<b>x", "<i>1", "<h1>t", "<u>Job"} {
		if strings.Contains(c.HTML, bad) {
			t.Errorf("HTML contains %q", bad)
		}
	}
	if !strings.Contains(c.HTML, "&lt;script&gt;alert(1)&lt;/script&gt;&#34;&#39;") {
		t.Error("escaped value missing")
	}
	if !strings.Contains(c.Text, evil) {
		t.Error("the text part carries values verbatim")
	}
}

func TestRenderRejectsParts(t *testing.T) {
	t.Parallel()
	base := func(f func(*Notice)) Notice {
		n := statusNotice()
		f(&n)
		return n
	}
	cols := func(k int) []string {
		out := make([]string, k)
		for i := range out {
			out[i] = fmt.Sprintf("c%d", i)
		}
		return out
	}
	many := func(k int) []Row {
		out := make([]Row, k)
		for i := range out {
			out[i] = Row{Cells: []string{"OK", "j", "1/0", "x", "1 B", "1 B"}, Level: LevelOK}
		}
		return out
	}
	cases := map[string]Notice{
		"banner level":      base(func(n *Notice) { n.Banner.Level = 7 }),
		"banner no word":    base(func(n *Notice) { n.Banner.Word = " " }),
		"banner no text":    base(func(n *Notice) { n.Banner.Text = "" }),
		"banner long word":  base(func(n *Notice) { n.Banner.Word = strings.Repeat("x", maxShortField+1) }),
		"banner control":    base(func(n *Notice) { n.Banner.Text = "a\nb" }),
		"preheader control": base(func(n *Notice) { n.Preheader = "a\rb" }),
		"too many figures":  base(func(n *Notice) { n.Figures = append(n.Figures, Figure{Label: "x", Value: "1"}) }),
		"figure no value":   base(func(n *Notice) { n.Figures[0].Value = "" }),
		"figure level":      base(func(n *Notice) { n.Figures[0].Level = -1 }),
		"too many tables": base(func(n *Notice) {
			n.Tables = append(n.Tables, n.Tables[1], n.Tables[1], n.Tables[1])
		}),
		"no title":         base(func(n *Notice) { n.Tables[0].Title = "" }),
		"no columns":       base(func(n *Notice) { n.Tables[1].Columns, n.Tables[1].Rows = nil, nil }),
		"too many columns": base(func(n *Notice) { n.Tables[1].Columns, n.Tables[1].Rows = cols(maxColumns+1), nil }),
		"too many rows":    base(func(n *Notice) { n.Tables[0].Rows = many(maxRows + 1) }),
		"too many rows in total": base(func(n *Notice) {
			n.Tables[0].Rows = many(maxRows)
			n.Tables[1].Rows = nil
			n.Tables = append(n.Tables, Table{Title: "x", Columns: n.Tables[0].Columns, Rows: many(maxRowsTotal - maxRows + 1)})
		}),
		"short row":        base(func(n *Notice) { n.Tables[0].Rows[0].Cells = n.Tables[0].Rows[0].Cells[:2] }),
		"row level":        base(func(n *Notice) { n.Tables[0].Rows[0].Level = 9 }),
		"empty status":     base(func(n *Notice) { n.Tables[0].Rows[0].Cells[0] = "" }),
		"cell control":     base(func(n *Notice) { n.Tables[0].Rows[0].Cells[1] = "a\x1bb" }),
		"cell too long":    base(func(n *Notice) { n.Tables[0].Rows[0].Cells[1] = strings.Repeat("x", maxNoticeField+1) }),
		"note control":     base(func(n *Notice) { n.Tables[0].Rows[0].Note = "a\u2028b" }),
		"numeric index":    base(func(n *Notice) { n.Tables[0].NumericColumns = []int{6} }),
		"hidden index":     base(func(n *Notice) { n.Tables[0].NarrowHidden = []int{-1} }),
		"column too long":  base(func(n *Notice) { n.Tables[0].Columns[0] = strings.Repeat("x", maxShortField+1) }),
		"empty column":     base(func(n *Notice) { n.Tables[0].Columns[0] = "" }),
		"title control":    base(func(n *Notice) { n.Tables[0].Title = "a\tb" }),
		"invalid utf-8":    base(func(n *Notice) { n.Tables[0].Rows[0].Cells[1] = "\xff" }),
		"figure too long":  base(func(n *Notice) { n.Figures[0].Value = strings.Repeat("9", maxShortField+1) }),
		"label whitespace": base(func(n *Notice) { n.Figures[0].Label = "  " }),
	}
	for name, n := range cases {
		if _, err := Render(n); !errors.Is(err, ErrInvalidMessage) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestRenderSizeBudget: a large summary with plain names (100 job rows
// plus the other tables) stays below 90 KB of HTML as sent
// (quoted-printable, EncodedLen), well under Gmail's 102 KB clipping
// without any row dropped. Names that escape or encode large are fitted
// by the summary itself (summary.TestSummaryWorstCaseSize).
func TestRenderSizeBudget(t *testing.T) {
	t.Parallel()
	n := statusNotice()
	jobs := &n.Tables[0]
	jobs.Rows = nil
	for i := range 100 {
		r := Row{Cells: []string{"OK", fmt.Sprintf("postgres-production-cluster-%03d", i), "24/0", "8 Nov 07:30", "12.7 GiB", "301.5 GiB"}, Level: LevelOK}
		if i%10 == 0 {
			r = Row{Cells: []string{"Failed", fmt.Sprintf("mysql-legacy-%03d", i), "0/3", "8 Nov 03:12", "0 B", "1.2 TiB"}, Level: LevelFailure, Note: "the database server could not be reached"}
		}
		jobs.Rows = append(jobs.Rows, r)
	}
	n.Tables = append(n.Tables, Table{
		Title: "Restores", Columns: []string{"Result", "Kind", "Count"}, Status: true, NumericColumns: []int{2},
		Rows: []Row{{Cells: []string{"Succeeded", "Restores", "4"}, Level: LevelOK}, {Cells: []string{"Failed", "Test restores", "1"}, Level: LevelFailure}},
	}, Table{
		Title: "Security", Columns: []string{"Status", "Security alerts"}, Status: true,
		Rows: []Row{{Cells: []string{"Review", "3 security alerts in this period"}, Level: LevelWarning}},
	})
	for i := range 6 {
		n.Tables[1].Rows = append(n.Tables[1].Rows, Row{Cells: []string{"OK", fmt.Sprintf("destination-%d", i), "1.9 TiB", "96.0 GiB", "412"}, Level: LevelOK})
	}
	c, err := Render(n)
	if err != nil {
		t.Fatal(err)
	}
	if enc := EncodedLen(c.HTML); enc >= 90_000 {
		t.Fatalf("HTML is %d bytes as sent (%d decoded), budget 90000", enc, len(c.HTML))
	}
	t.Logf("HTML %d bytes (%d as sent), text %d bytes", len(c.HTML), EncodedLen(c.HTML), len(c.Text))
}

// TestOutlookConstantsFixed: the raw HTML of the layout is fixed text (no
// template input), copied verbatim into every e-mail.
func TestOutlookConstantsFixed(t *testing.T) {
	t.Parallel()
	for name, s := range map[string]string{"msoHead": string(msoHead), "msoOpen": string(msoOpen), "msoClose": string(msoClose), "preheaderFill": string(preheaderFill)} {
		if strings.Contains(s, "{{") || strings.Contains(s, "%") || strings.Contains(s, "<script") || strings.Contains(strings.ToLower(s), "src=") ||
			strings.Contains(s, "url(") || strings.Contains(s, "href") {
			t.Errorf("%s is not a fixed, inert string: %q", name, s)
		}
		if name != "preheaderFill" && (!strings.HasPrefix(s, "<!--[if mso]>") || !strings.HasSuffix(s, "<![endif]-->")) {
			t.Errorf("%s is not an Outlook-only conditional comment", name)
		}
	}
	hostile := statusNotice()
	hostile.Heading = "<!--[if mso]><script>x</script><![endif]-->"
	c, err := Render(hostile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(c.HTML, "<!--[if mso]>") != 3 || strings.Contains(c.HTML, "<script>x") {
		t.Fatal("conditional comments must come only from the constants")
	}
}

// --- colors ----------------------------------------------------------------

func luminance(hex string) float64 {
	v, err := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	if err != nil || len(hex) != 7 {
		panic("bad hex " + hex)
	}
	ch := func(shift uint) float64 {
		c := float64((v>>shift)&0xff) / 255
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(16) + 0.7152*ch(8) + 0.0722*ch(0)
}

func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

// TestPaletteContrast: WCAG AA (4.5:1) for every text/background pair the
// layout uses and 3:1 for the status marks, in both schemes.
func TestPaletteContrast(t *testing.T) {
	t.Parallel()
	for name, p := range map[string]palette{"light": lightPalette, "dark": darkPalette} {
		type pair struct {
			what   string
			fg, bg string
			min    float64
		}
		pairs := []pair{
			{"text on page", p.Text, p.Bg, 4.5}, {"text on card", p.Text, p.Surface, 4.5},
			{"muted on page", p.Muted, p.Bg, 4.5}, {"muted on card", p.Muted, p.Surface, 4.5},
			{"link on card", p.Primary, p.Surface, 4.5}, {"button label", p.OnPrimary, p.Primary, 4.5},
			{"header rule on card", p.Strong, p.Surface, 3},
		}
		for _, l := range []Level{LevelNone, LevelOK, LevelWarning, LevelFailure} {
			s := p.swatch(l)
			k := l.key()
			pairs = append(pairs,
				pair{k + " label on soft", s.Fg, s.Soft, 4.5},
				pair{k + " label on card", s.Fg, p.Surface, 4.5},
				pair{k + " text on soft", p.Text, s.Soft, 4.5},
				pair{k + " mark on card", s.Solid, p.Surface, 3},
			)
			if l.problem() {
				pairs = append(pairs, pair{k + " badge rule on soft", s.Solid, s.Soft, 3})
			}
		}
		for _, x := range pairs {
			if got := contrast(x.fg, x.bg); got < x.min {
				t.Errorf("%s %s: %s on %s = %.2f, want >= %.1f", name, x.what, x.fg, x.bg, got, x.min)
			}
		}
	}
}

var (
	themeBlock = regexp.MustCompile(`(?s)(:root|\.dark) \{(.*?)\n\}`)
	themeToken = regexp.MustCompile(`--([a-z0-9-]+):\s*oklch\(([\d.]+) ([\d.]+) ([\d.]+)\)`)
)

// oklchHex converts like web/src/lib/color.ts (OKLab → linear sRGB,
// clipped) and encodes sRGB.
func oklchHex(l, c, h float64) string {
	a, b := c*math.Cos(h*math.Pi/180), c*math.Sin(h*math.Pi/180)
	cube := func(x float64) float64 { return x * x * x }
	l3 := cube(l + 0.3963377774*a + 0.2158037573*b)
	m3 := cube(l - 0.1055613458*a - 0.0638541728*b)
	s3 := cube(l - 0.0894841775*a - 1.291485548*b)
	lin := []float64{
		4.0767416621*l3 - 3.3077115913*m3 + 0.2309699292*s3,
		-1.2684380046*l3 + 2.6097574011*m3 - 0.3413193965*s3,
		-0.0041960863*l3 - 0.7034186147*m3 + 1.707614701*s3,
	}
	var out strings.Builder
	out.WriteString("#")
	for _, x := range lin {
		x = min(1, max(0, x))
		if x <= 0.0031308 {
			x *= 12.92
		} else {
			x = 1.055*math.Pow(x, 1/2.4) - 0.055
		}
		fmt.Fprintf(&out, "%02x", int(math.Round(x*255)))
	}
	return out.String()
}

func near(a, b string) bool {
	for i := 1; i < 7; i += 2 {
		x, errX := strconv.ParseUint(a[i:i+2], 16, 8)
		y, errY := strconv.ParseUint(b[i:i+2], 16, 8)
		if d := int(x) - int(y); errX != nil || errY != nil || d < -1 || d > 1 {
			return false
		}
	}
	return true
}

// TestPaletteMatchesTheme: the hex palettes are the theme's tokens
// (web/src/styles/theme.css); change both together.
func TestPaletteMatchesTheme(t *testing.T) {
	t.Parallel()
	css, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "src", "styles", "theme.css"))
	if err != nil {
		t.Fatal(err)
	}
	tokens := map[string]map[string]string{}
	for _, m := range themeBlock.FindAllStringSubmatch(string(css), -1) {
		scheme := map[string]string{":root": "light", ".dark": "dark"}[m[1]]
		tokens[scheme] = map[string]string{}
		for _, tm := range themeToken.FindAllStringSubmatch(m[2], -1) {
			f := func(s string) float64 {
				v, err := strconv.ParseFloat(s, 64)
				if err != nil {
					t.Fatalf("theme token %s: %v", tm[1], err)
				}
				return v
			}
			tokens[scheme][tm[1]] = oklchHex(f(tm[2]), f(tm[3]), f(tm[4]))
		}
	}
	checked := 0
	for name, p := range map[string]palette{"light": lightPalette, "dark": darkPalette} {
		pv := reflect.ValueOf(p)
		for i := range pv.NumField() {
			field := pv.Type().Field(i).Name
			token := paletteTokens[field]
			if token == "" {
				t.Fatalf("palette field %s has no token", field)
			}
			check := func(tok, got string) {
				want, ok := tokens[name][tok]
				if !ok {
					t.Errorf("%s: token --%s not found in theme.css", name, tok)
					return
				}
				if !near(got, want) {
					t.Errorf("%s %s: %s, theme --%s is %s", name, field, got, tok, want)
				}
				checked++
			}
			if s, ok := pv.Field(i).Interface().(swatch); ok {
				check(token, s.Solid)
				check(token+"-fg", s.Fg)
				check(token+"-soft", s.Soft)
				continue
			}
			check(token, pv.Field(i).String())
		}
	}
	if checked != 2*(9+4*3) {
		t.Fatalf("checked %d colors", checked)
	}
}

// TestTextsParity: the layout's and this package's e-mail texts exist in
// every language with the same formatting verbs (D-237).
func TestTextsParity(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]i18n.Catalog{"layoutTexts": layoutTexts, "mailTexts": mailTexts} {
		if p := c.Problems(); len(p) > 0 {
			t.Errorf("%s: %q", name, p)
		}
	}
}

// TestRenderLocale: the layout's own texts and the lang attribute follow
// Notice.Locale; an unknown locale falls back to English.
func TestRenderLocale(t *testing.T) {
	t.Parallel()
	n := Notice{Subject: "s", Heading: "h", Action: &Action{Label: "a", URL: "https://bekup.example.com/"}}
	for _, tc := range []struct {
		l          i18n.Locale
		lang, text string
	}{
		{i18n.NL, `lang="nl"`, "Werkt de knop niet?"},
		{i18n.EN, `lang="en"`, "If the button does not work"},
		{"", `lang="en"`, "If the button does not work"},
		{"de", `lang="en"`, "If the button does not work"},
	} {
		n.Locale = tc.l
		c, err := Render(n)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(c.HTML, tc.lang) || !strings.Contains(c.HTML, tc.text) {
			t.Errorf("locale %q: lang or fallback text missing", tc.l)
		}
		if !strings.Contains(c.Text, DefaultFooter(i18n.Resolve(nil, tc.l))) {
			t.Errorf("locale %q: footer missing from the text part", tc.l)
		}
	}
}
