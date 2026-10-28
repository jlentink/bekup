package offline

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jlentink/bekup/internal/pipeline"
)

// SafeText escapes s for a terminal: control characters, invalid UTF-8
// and Unicode format/bidi characters become \xNN or \u{NNNN}, so text from
// a manifest (warnings, object names) can never inject terminal escape
// sequences or reorder the output. Backslashes are doubled.
func SafeText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size <= 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r == '\\':
			b.WriteString(`\\`)
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			fmt.Fprintf(&b, `\x%02x`, r)
		case unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029':
			fmt.Fprintf(&b, `\u{%04x}`, r)
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

// DisplayName returns the decoded, terminal-safe name of a manifest
// database entry ("(globals)" for the globals entry).
func DisplayName(e *pipeline.Entry) string {
	if e.Kind == pipeline.KindGlobals {
		return "(globals)"
	}
	name, err := pipeline.DecodeName(e.DB)
	if err != nil {
		return SafeText(e.DB)
	}
	return SafeText(name)
}
