package email

// The e-mail palette: hex copies of the web theme's tokens
// (web/src/styles/theme.css, "Vault Ledger", D-021), because e-mail
// clients do not understand oklch(). Each value names its token; the
// conversion is the web's own (OKLCH → sRGB, gamut-clipped, as
// web/src/lib/color.ts), and TestPaletteMatchesTheme fails when a token in
// theme.css changes without this copy. TestPaletteContrast enforces WCAG
// AA for every text/background pair the layout uses, light and dark.

// swatch is one status color set: Solid for marks (the status strip, row
// rules), Fg for text on Soft (badges, the banner word) and Soft for
// tinted backgrounds.
type swatch struct {
	Solid, Fg, Soft string
}

// palette is one color scheme of the layout.
type palette struct {
	Bg        string // page background
	Surface   string // the card
	Text      string // body text
	Muted     string // secondary text
	Border    string // card border
	Strong    string // table header rule
	Rule      string // hairline row separators
	Primary   string // button and links
	OnPrimary string // button label
	OK        swatch
	Warning   swatch
	Failure   swatch
	Neutral   swatch
}

// lightPalette is the theme's :root (light) scheme.
var lightPalette = palette{
	Bg:        "#f9f8f4", // --background
	Surface:   "#fefdfc", // --surface
	Text:      "#141821", // --foreground
	Muted:     "#565b64", // --muted-foreground
	Border:    "#dbd9d3", // --border
	Strong:    "#7c8088", // --border-strong
	Rule:      "#e3e1dc", // --rule
	Primary:   "#2552aa", // --primary
	OnPrimary: "#fbfaf7", // --primary-foreground
	OK: swatch{
		Solid: "#2d8949", // --status-success
		Fg:    "#115629", // --status-success-fg
		Soft:  "#dff6e2", // --status-success-soft
	},
	Warning: swatch{
		Solid: "#b46e00", // --status-partial
		Fg:    "#794402", // --status-partial-fg
		Soft:  "#feefcf", // --status-partial-soft
	},
	Failure: swatch{
		Solid: "#c9302d", // --status-failed
		Fg:    "#9b1f1d", // --status-failed-fg
		Soft:  "#ffe9e6", // --status-failed-soft
	},
	Neutral: swatch{
		Solid: "#71757c", // --status-neutral
		Fg:    "#494d55", // --status-neutral-fg
		Soft:  "#edebe7", // --status-neutral-soft
	},
}

// darkPalette is the theme's .dark scheme.
var darkPalette = palette{
	Bg:        "#0b0f14", // --background
	Surface:   "#12161c", // --surface
	Text:      "#eae7e2", // --foreground
	Muted:     "#9fa5ae", // --muted-foreground
	Border:    "#2a2e35", // --border
	Strong:    "#5e646d", // --border-strong
	Rule:      "#1f2329", // --rule
	Primary:   "#7cb4fc", // --primary
	OnPrimary: "#090f1a", // --primary-foreground
	OK: swatch{
		Solid: "#4dbf74", // --status-success
		Fg:    "#8fe5a7", // --status-success-fg
		Soft:  "#112d1a", // --status-success-soft
	},
	Warning: swatch{
		Solid: "#f0b135", // --status-partial
		Fg:    "#fcd176", // --status-partial-fg
		Soft:  "#372508", // --status-partial-soft
	},
	Failure: swatch{
		Solid: "#f75d59", // --status-failed
		Fg:    "#ffb1aa", // --status-failed-fg
		Soft:  "#3f1917", // --status-failed-soft
	},
	Neutral: swatch{
		Solid: "#878d95", // --status-neutral
		Fg:    "#c0c4cb", // --status-neutral-fg
		Soft:  "#20242a", // --status-neutral-soft
	},
}

// paletteTokens maps the palette fields to their theme tokens (for
// TestPaletteMatchesTheme).
var paletteTokens = map[string]string{
	"Bg": "background", "Surface": "surface", "Text": "foreground", "Muted": "muted-foreground",
	"Border": "border", "Strong": "border-strong", "Rule": "rule", "Primary": "primary",
	"OnPrimary": "primary-foreground",
	"OK":        "status-success", "Warning": "status-partial", "Failure": "status-failed", "Neutral": "status-neutral",
}

// swatch returns the colors of a level.
func (p *palette) swatch(l Level) swatch {
	switch l {
	case LevelOK:
		return p.OK
	case LevelWarning:
		return p.Warning
	case LevelFailure:
		return p.Failure
	default:
		return p.Neutral
	}
}
