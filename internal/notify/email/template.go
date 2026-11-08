package email

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"net/url"
	"strings"
	texttemplate "text/template"
	"unicode/utf8"

	"github.com/jlentink/bekup/internal/i18n"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

var (
	htmlNotice = htmltemplate.Must(htmltemplate.New("notice.html.tmpl").Option("missingkey=error").ParseFS(templateFS, "templates/notice.html.tmpl"))
	textNotice = texttemplate.Must(texttemplate.New("notice.txt.tmpl").Option("missingkey=error").ParseFS(templateFS, "templates/notice.txt.tmpl"))
)

// Limits of a notice.
const (
	maxParagraphs   = 20
	maxDetails      = 30
	maxNoticeField  = 2000
	maxActionURLLen = 2048
	// maxFigures is how many key-figure tiles fit in one row.
	maxFigures = 4
	// maxTables, maxColumns and maxRows bound the data tables; maxRowsTotal
	// bounds all of them together (the HTML must stay well below Gmail's
	// 102 KB clipping; see TestRenderSizeBudget).
	maxTables    = 4
	maxColumns   = 8
	maxRows      = 120
	maxRowsTotal = 160
	// maxShortField bounds labels, column titles, the banner word and
	// figure values (characters).
	maxShortField = 60
	// maxTextColumn is the widest column of a plain-text table; longer
	// cells overflow it (the row stays one line).
	maxTextColumn = 40
)

// Level is the status level of a banner, figure or table row. It is
// always paired with a word in the content (the banner Word, a status
// cell): color is never the only signal.
type Level int

// Status levels.
const (
	// LevelNone is neutral (no status color).
	LevelNone Level = iota
	// LevelOK is a good state (green).
	LevelOK
	// LevelWarning needs attention (amber).
	LevelWarning
	// LevelFailure is a failure (red).
	LevelFailure
)

func (l Level) valid() bool { return l >= LevelNone && l <= LevelFailure }

// key is the level's short CSS class suffix.
func (l Level) key() string {
	switch l {
	case LevelOK:
		return "o"
	case LevelWarning:
		return "w"
	case LevelFailure:
		return "f"
	default:
		return "n"
	}
}

// problem reports levels that mark a row (tint, rule, ">>").
func (l Level) problem() bool { return l == LevelWarning || l == LevelFailure }

// Notice is the content of an e-mail: plain values only. Render escapes
// everything (html/template for the HTML part); the HTML part has no
// remote images, no scripts and no tracking. Values must not contain
// control characters (line breaks included): split text into Paragraphs.
//
// Banner, Figures, Tables and Preheader are optional parts (the periodic
// summary fills them); every level they carry comes with a word.
type Notice struct {
	Subject string
	Heading string
	// Preheader is the hidden inbox preview line ("2 failed · 12 OK").
	Preheader string
	// Banner is the status strip and banner block under the heading.
	Banner     *Banner
	Paragraphs []string
	// Figures are up to four key-figure tiles.
	Figures []Figure
	// Tables are up to four data tables.
	Tables  []Table
	Details []Detail
	// Action is an optional link (absolute http(s) URL without user info,
	// normally built from server.base_url).
	Action *Action
	Footer string
	// Locale is the recipient's language: the layout's own texts (footer,
	// button fallback) and the HTML lang attribute (D-237). Empty means
	// i18n.Default. The caller writes every other text in it.
	Locale i18n.Locale
}

// Banner is the overall status: a colored strip and a block with the
// big status Word ("ALL OK", "2 FAILED") and one sentence.
type Banner struct {
	Level Level
	Word  string
	Text  string
}

// Figure is one key-figure tile: a big value and a small label.
type Figure struct {
	Label string
	Value string
	Level Level
}

// Table is a data table. With Status set, the first column is the status
// column: its cell is a word ("Failed", "OK") shown as a badge in the
// row's level color. Rows with a warning or failure level are tinted,
// get a left rule and a ">>" marker in the plain text.
type Table struct {
	Title   string
	Columns []string
	Rows    []Row
	Status  bool
	// NumericColumns are right-aligned in the monospace stack.
	NumericColumns []int
	// NarrowHidden columns are hidden on narrow screens (phones).
	NarrowHidden []int
}

// Row is one table row (exactly one cell per column). Note is an
// optional short line (a fixed reason such as "target unreachable") shown
// under the row's name (the cell after the status cell) in the level's
// color, in the plain text on the line below the row.
type Row struct {
	Cells []string
	Level Level
	Note  string
}

// Detail is one labeled value.
type Detail struct {
	Label string
	Value string
}

// Action is a link button.
type Action struct {
	Label string
	URL   string
}

// Content is a rendered e-mail.
type Content struct {
	Subject string
	Text    string
	HTML    string
}

// DefaultFooter is the footer of a Notice without one, in l.
func DefaultFooter(l i18n.Locale) string { return layoutTexts.T(l, "footer") }

// Render validates n and renders its text and HTML parts.
func Render(n Notice) (Content, error) {
	if _, ok := i18n.Parse(string(n.Locale)); !ok {
		n.Locale = i18n.Default
	}
	if n.Footer == "" {
		n.Footer = DefaultFooter(n.Locale)
	}
	if err := n.validate(); err != nil {
		return Content{}, err
	}
	v := newView(&n)
	var h, t bytes.Buffer
	if err := htmlNotice.Execute(&h, v); err != nil {
		return Content{}, fmt.Errorf("email: render html: %w", err)
	}
	if err := textNotice.Execute(&t, v); err != nil {
		return Content{}, fmt.Errorf("email: render text: %w", err)
	}
	return Content{Subject: n.Subject, Text: t.String(), HTML: h.String()}, nil
}

func invalid(msg string) error { return fmt.Errorf("%w: %s", ErrInvalidMessage, msg) }

func short(s string) bool {
	return strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= maxShortField
}

func (n *Notice) validate() error {
	if strings.TrimSpace(n.Subject) == "" || strings.TrimSpace(n.Heading) == "" {
		return invalid("subject and heading are required")
	}
	if utf8.RuneCountInString(n.Subject) > MaxSubjectLen {
		return invalid("subject too long")
	}
	if len(n.Paragraphs) > maxParagraphs || len(n.Details) > maxDetails {
		return invalid("too many paragraphs or details")
	}
	fields := []string{n.Subject, n.Heading, n.Footer, n.Preheader}
	fields = append(fields, n.Paragraphs...)
	for _, d := range n.Details {
		fields = append(fields, d.Label, d.Value)
	}
	if n.Action != nil {
		if err := checkActionURL(n.Action.URL); err != nil {
			return err
		}
		if strings.TrimSpace(n.Action.Label) == "" {
			return invalid("the action needs a label")
		}
		fields = append(fields, n.Action.Label, n.Action.URL)
	}
	if b := n.Banner; b != nil {
		if !b.Level.valid() || !short(b.Word) || strings.TrimSpace(b.Text) == "" {
			return invalid("the banner needs a level, a short word and a text")
		}
		fields = append(fields, b.Word, b.Text)
	}
	if len(n.Figures) > maxFigures {
		return invalid("too many figures")
	}
	for _, f := range n.Figures {
		if !f.Level.valid() || !short(f.Label) || !short(f.Value) {
			return invalid("a figure needs a level, a short label and a short value")
		}
		fields = append(fields, f.Label, f.Value)
	}
	tf, err := n.validateTables()
	if err != nil {
		return err
	}
	fields = append(fields, tf...)
	for _, f := range fields {
		if hasControl(f) || len(f) > maxNoticeField {
			return invalid("a field is too long or contains control characters")
		}
	}
	return nil
}

// validateTables checks the table limits and returns the cell texts.
func (n *Notice) validateTables() ([]string, error) {
	if len(n.Tables) > maxTables {
		return nil, invalid("too many tables")
	}
	var fields []string
	total := 0
	for i := range n.Tables {
		t := &n.Tables[i]
		if !short(t.Title) || len(t.Columns) == 0 || len(t.Columns) > maxColumns || len(t.Rows) > maxRows {
			return nil, invalid("a table needs a title, 1 to 8 columns and at most 120 rows")
		}
		total += len(t.Rows)
		if total > maxRowsTotal {
			return nil, invalid("too many table rows")
		}
		for _, c := range t.Columns {
			if !short(c) {
				return nil, invalid("a column title is empty or too long")
			}
		}
		fields = append(fields, t.Title)
		fields = append(fields, t.Columns...)
		for _, idx := range append(append([]int(nil), t.NumericColumns...), t.NarrowHidden...) {
			if idx < 0 || idx >= len(t.Columns) {
				return nil, invalid("a column index is out of range")
			}
		}
		for _, r := range t.Rows {
			if !r.Level.valid() || len(r.Cells) != len(t.Columns) {
				return nil, invalid("a row needs a level and one cell per column")
			}
			if t.Status && strings.TrimSpace(r.Cells[0]) == "" {
				return nil, invalid("the status cell needs a word")
			}
			fields = append(fields, r.Cells...)
			fields = append(fields, r.Note)
		}
	}
	return fields, nil
}

// checkActionURL allows absolute http(s) URLs without user info only.
func checkActionURL(raw string) error {
	if len(raw) > maxActionURLLen {
		return fmt.Errorf("%w: link too long", ErrInvalidMessage)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.Opaque != "" {
		return fmt.Errorf("%w: %w", ErrInvalidMessage, errors.New("the link must be an absolute http(s) URL"))
	}
	return nil
}
