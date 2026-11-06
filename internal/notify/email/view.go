package email

import (
	"fmt"
	htmltemplate "html/template"
	"strings"
	"unicode/utf8"
)

// The rendering model of a Notice: everything the templates need, computed
// once (colors per level, column flags, the plain-text table lines), so
// the templates stay declarative. Values from the notice stay plain
// strings (html/template escapes them); colors come only from the fixed
// palettes.

// Outlook for Windows (the Word engine) ignores max-width and falls back
// to Times for font stacks it does not know: a fixed-width wrapper table
// and a font block in conditional comments. html/template drops HTML
// comments, so they are fixed template.HTML constants (never built from
// data; TestOutlookConstantsFixed).
const (
	msoHead htmltemplate.HTML = `<!--[if mso]><xml><o:OfficeDocumentSettings><o:AllowPNG/><o:PixelsPerInch>96</o:PixelsPerInch></o:OfficeDocumentSettings></xml>` +
		`<style>table,td,th,p,a,span,div{font-family:'Segoe UI',Arial,sans-serif!important}h1,h2,.sr{font-family:Georgia,serif!important}.mn,.mn span{font-family:Consolas,'Courier New',monospace!important}</style><![endif]-->`
	msoOpen  htmltemplate.HTML = `<!--[if mso]><table role="presentation" align="center" width="600" cellpadding="0" cellspacing="0" border="0"><tr><td><![endif]-->`
	msoClose htmltemplate.HTML = `<!--[if mso]></td></tr></table><![endif]-->`
	// preheaderFill keeps clients from appending body text to the
	// preheader in the inbox preview.
	preheaderFill htmltemplate.HTML = `&#847;&zwnj;&nbsp;&#847;&zwnj;&nbsp;&#847;&zwnj;&nbsp;&#847;&zwnj;&nbsp;&#847;&zwnj;&nbsp;&#847;&zwnj;&nbsp;&#847;&zwnj;&nbsp;&#847;&zwnj;&nbsp;&#847;&zwnj;&nbsp;&#847;&zwnj;&nbsp;&#847;&zwnj;&nbsp;&#847;&zwnj;&nbsp;`
)

type view struct {
	Subject, Heading, Preheader, Footer string
	Paragraphs                          []string
	Details                             []Detail
	Action                              *Action
	L, D                                palette
	Levels                              []levelView
	Banner                              *bannerView
	Figures                             []figureView
	FigureWidth                         string
	Tables                              []tableView
	MSOHead, MSOOpen, MSOClose, PreFill htmltemplate.HTML
	// TextBanner and TextFigures are the plain-text lines (empty: none).
	TextBanner, TextFigures string
}

// levelView carries one level's colors for the dark-mode style block.
type levelView struct {
	K       string
	L, D    swatch
	Problem bool
}

type bannerView struct {
	Word, Text string
	K          string
	S          swatch
	// Loud: a warning or failure banner gets the big word.
	Loud bool
}

type figureView struct {
	// Value is shown big, Unit (the part after the last space, as in
	// "38.2 GiB") small after it.
	Label, Value, Unit string
	// Class is the dark-mode color class ("tx" for neutral figures).
	Class string
	Color string
}

type tableView struct {
	Title string
	Head  []headView
	Rows  []rowView
	// Text is the aligned plain-text table.
	Text []string
}

type headView struct {
	Text      string
	Num, Hide bool
}

type rowView struct {
	Cells []cellView
	K     string
	// Class is the row's classes (r<k> for a problem row's dark tint, nr
	// when a note row follows).
	Class string
	// Note is a short line in its own row under the row, from the name
	// column (NoteCell) on, spanning NoteSpan columns, colored by the
	// level (NoteColor, NoteClass for dark mode).
	Note, NoteColor, NoteClass string
	NoteCell, NoteSpan         int
	// Tint is the soft background of a problem row ("" for plain rows),
	// Rule its left rule color.
	Tint, Rule string
}

type cellView struct {
	Text             string
	Num, Hide, First bool
	// Class lists the cell's classes (mn monospace, hs hidden on phones,
	// c1 the first cell of a problem row, nm the name cell, which may
	// break long names).
	Class string
	// Pill is set for the status cell.
	Pill *pillView
}

// pillView is a status badge: soft tint on plain rows; on a tinted
// problem row the card color with a rule (Border) in the status color,
// so the badge stands out from the row (Class q<k> for dark mode).
type pillView struct {
	Bg, Fg, Border, Class string
}

func newView(n *Notice) *view {
	v := &view{
		Subject: n.Subject, Heading: n.Heading, Preheader: n.Preheader, Footer: n.Footer,
		Paragraphs: n.Paragraphs, Details: n.Details, Action: n.Action,
		L: lightPalette, D: darkPalette,
		MSOHead: msoHead, MSOOpen: msoOpen, MSOClose: msoClose, PreFill: preheaderFill,
	}
	for _, l := range []Level{LevelNone, LevelOK, LevelWarning, LevelFailure} {
		v.Levels = append(v.Levels, levelView{K: l.key(), L: lightPalette.swatch(l), D: darkPalette.swatch(l), Problem: l.problem()})
	}
	if b := n.Banner; b != nil {
		v.Banner = &bannerView{Word: b.Word, Text: b.Text, K: b.Level.key(), S: lightPalette.swatch(b.Level), Loud: b.Level.problem()}
		v.TextBanner = textBanner(b)
	}
	if len(n.Figures) > 0 {
		parts := make([]string, 0, len(n.Figures))
		for _, f := range n.Figures {
			fv := figureView{Label: f.Label, Value: f.Value, Class: "tx", Color: lightPalette.Text}
			if f.Level != LevelNone {
				fv.Class, fv.Color = "v"+f.Level.key(), lightPalette.swatch(f.Level).Fg
			}
			if i := strings.LastIndexByte(f.Value, ' '); i > 0 && i < len(f.Value)-1 {
				fv.Value, fv.Unit = f.Value[:i], f.Value[i+1:]
			}
			v.Figures = append(v.Figures, fv)
			parts = append(parts, f.Label+": "+f.Value)
		}
		v.FigureWidth = fmt.Sprintf("%d%%", 100/len(n.Figures))
		v.TextFigures = strings.Join(parts, "  |  ")
	}
	for i := range n.Tables {
		v.Tables = append(v.Tables, newTableView(&n.Tables[i]))
	}
	return v
}

// textBanner is the first line of the plain-text part.
func textBanner(b *Banner) string {
	switch b.Level {
	case LevelFailure:
		return "!! " + b.Word + ": " + b.Text + " !!"
	case LevelWarning:
		return "! " + b.Word + ": " + b.Text
	default:
		return b.Word + ": " + b.Text
	}
}

func indexSet(idx []int) map[int]bool {
	m := make(map[int]bool, len(idx))
	for _, i := range idx {
		m[i] = true
	}
	return m
}

func newTableView(t *Table) tableView {
	num, hide := indexSet(t.NumericColumns), indexSet(t.NarrowHidden)
	tv := tableView{Title: t.Title}
	for i, c := range t.Columns {
		tv.Head = append(tv.Head, headView{Text: c, Num: num[i], Hide: hide[i]})
	}
	for _, r := range t.Rows {
		s := lightPalette.swatch(r.Level)
		rv := rowView{K: r.Level.key(), Note: r.Note}
		if r.Level.problem() {
			rv.Tint, rv.Rule = s.Soft, s.Solid
		}
		rv.NoteColor, rv.NoteClass = lightPalette.Muted, "mu"
		if r.Level != LevelNone {
			rv.NoteColor, rv.NoteClass = s.Fg, "n"+r.Level.key()
		}
		if t.Status && len(r.Cells) > 1 {
			rv.NoteCell = 1
		}
		rv.NoteSpan = len(r.Cells) - rv.NoteCell
		var rc []string
		if r.Level.problem() {
			rc = append(rc, "r"+r.Level.key())
		}
		if r.Note != "" {
			rc = append(rc, "nr")
		}
		rv.Class = strings.Join(rc, " ")
		for i, c := range r.Cells {
			cv := cellView{Text: c, Num: num[i], Hide: hide[i], First: i == 0}
			var cls []string
			if cv.Num {
				cls = append(cls, "mn")
			}
			if cv.Hide {
				cls = append(cls, "hs")
			}
			if cv.First && r.Level.problem() {
				cls = append(cls, "c1")
			}
			if i == rv.NoteCell && !cv.Num {
				cls = append(cls, "nm")
			}
			cv.Class = strings.Join(cls, " ")
			if t.Status && i == 0 {
				cv.Pill = &pillView{Bg: s.Soft, Fg: s.Fg, Class: "p" + r.Level.key()}
				if r.Level.problem() {
					cv.Pill = &pillView{Bg: lightPalette.Surface, Fg: s.Fg, Border: s.Solid, Class: "q" + r.Level.key()}
				}
			}
			rv.Cells = append(rv.Cells, cv)
		}
		tv.Rows = append(tv.Rows, rv)
	}
	tv.Text = textTable(t, num)
	return tv
}

// textTable lays a table out in aligned columns (at most maxTextColumn
// wide; longer cells overflow), with ">>" in front of problem rows and a
// row's note on the next line under its name.
func textTable(t *Table, num map[int]bool) []string {
	width := make([]int, len(t.Columns))
	measure := func(i int, s string) {
		width[i] = min(max(width[i], utf8.RuneCountInString(s)), maxTextColumn)
	}
	for i, c := range t.Columns {
		measure(i, c)
	}
	for _, r := range t.Rows {
		for i, c := range r.Cells {
			measure(i, c)
		}
	}
	line := func(marker string, cells []string) string {
		var b strings.Builder
		b.WriteString(marker)
		for i, c := range cells {
			if i > 0 {
				b.WriteString("  ")
			}
			pad := max(width[i]-utf8.RuneCountInString(c), 0)
			last := i == len(cells)-1
			switch {
			case num[i]:
				b.WriteString(strings.Repeat(" ", pad) + c)
			case last:
				b.WriteString(c)
			default:
				b.WriteString(c + strings.Repeat(" ", pad))
			}
		}
		return strings.TrimRight(b.String(), " ")
	}
	out := []string{line("   ", t.Columns)}
	rule := make([]string, len(t.Columns))
	for i := range rule {
		rule[i] = strings.Repeat("-", width[i])
	}
	out = append(out, line("   ", rule))
	noteCell := 0
	if t.Status && len(t.Columns) > 1 {
		noteCell = 1
	}
	indent := 3
	for i := range noteCell {
		indent += width[i] + 2
	}
	for _, r := range t.Rows {
		marker := "   "
		if r.Level.problem() {
			marker = ">> "
		}
		out = append(out, line(marker, r.Cells))
		if r.Note != "" {
			out = append(out, strings.Repeat(" ", indent)+r.Note)
		}
	}
	return out
}
