package i18n

import (
	"strings"
	"time"
)

var (
	nlMonths     = [...]string{"januari", "februari", "maart", "april", "mei", "juni", "juli", "augustus", "september", "oktober", "november", "december"}
	nlMonthsAbbr = [...]string{"jan", "feb", "mrt", "apr", "mei", "jun", "jul", "aug", "sep", "okt", "nov", "dec"}
	nlDays       = [...]string{"zondag", "maandag", "dinsdag", "woensdag", "donderdag", "vrijdag", "zaterdag"}
	nlDaysAbbr   = [...]string{"zo", "ma", "di", "wo", "do", "vr", "za"}
)

// nameTokens are the name elements of Go time layouts, longest first so
// "January" is not read as "Jan" + "uary".
var nameTokens = []string{"January", "Monday", "Jan", "Mon"}

// FormatTime formats t with a Go time layout in l: month and weekday names
// are Dutch for NL ("2 jan 2026 14:05", "maandag"); every other element
// (numbers, zones) is formatted by the time package as usual.
func FormatTime(l Locale, t time.Time, layout string) string {
	if l != NL {
		return t.Format(layout)
	}
	var b strings.Builder
	rest := layout
	for rest != "" {
		tok, at := firstToken(rest)
		if at < 0 {
			b.WriteString(t.Format(rest))
			break
		}
		if at > 0 {
			b.WriteString(t.Format(rest[:at]))
		}
		b.WriteString(dutchName(t, tok))
		rest = rest[at+len(tok):]
	}
	return b.String()
}

// firstToken returns the earliest name token in s and its index (-1: none).
func firstToken(s string) (string, int) {
	best, at := "", -1
	for _, tok := range nameTokens {
		if i := strings.Index(s, tok); i >= 0 && (at < 0 || i < at || (i == at && len(tok) > len(best))) {
			best, at = tok, i
		}
	}
	return best, at
}

func dutchName(t time.Time, tok string) string {
	switch tok {
	case "January":
		return nlMonths[t.Month()-1]
	case "Jan":
		return nlMonthsAbbr[t.Month()-1]
	case "Monday":
		return nlDays[t.Weekday()]
	default:
		return nlDaysAbbr[t.Weekday()]
	}
}

// LocalizeDecimal writes the first decimal point of an already formatted
// number in l's style ("1.4 GiB" → "1,4 GiB" for NL). For values the
// shared formatters render in English style (storage.FormatSize).
func LocalizeDecimal(l Locale, s string) string {
	if l != NL {
		return s
	}
	return strings.Replace(s, ".", ",", 1)
}
