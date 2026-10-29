package config

import (
	"fmt"
	"io"
	"strings"
)

// Markers delimiting the generated key reference in docs/configuration.md.
const (
	ReferenceBegin = "<!-- BEGIN GENERATED KEY REFERENCE (go run ./internal/config/gendocs) -->"
	ReferenceEnd   = "<!-- END GENERATED KEY REFERENCE -->"
)

// WriteReference writes the Markdown key reference for bekup.toml and
// runner.toml: every key with its env override, type, default and
// description; bekup.toml keys also say whether a runtime reload applies
// them (D-200).
func WriteReference(w io.Writer) error {
	var b strings.Builder
	bekup := DefaultBekup()
	runner := DefaultRunner()
	if err := writeReferenceFor(&b, "bekup.toml", fieldsOf(&bekup), true); err != nil {
		return err
	}
	if err := writeReferenceFor(&b, "runner.toml", fieldsOf(&runner), false); err != nil {
		return err
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func writeReferenceFor(b *strings.Builder, file string, fields []Field, reload bool) error {
	fmt.Fprintf(b, "\n### `%s`\n", file)
	section := ""
	for i := range fields {
		f := &fields[i]
		if f.Section != section {
			section = f.Section
			fmt.Fprintf(b, "\n#### `[%s]`\n\n", section)
			if reload {
				b.WriteString("| Key | Env override | Type | Default | Reload | Description |\n")
				b.WriteString("|---|---|---|---|---|---|\n")
			} else {
				b.WriteString("| Key | Env override | Type | Default | Description |\n")
				b.WriteString("|---|---|---|---|---|\n")
			}
		}
		def, err := tomlValue(f.Value())
		if err != nil {
			return err
		}
		if f.IsZero() && f.Type == "string" {
			def = "(empty)"
		}
		doc := f.Doc
		if f.InlineSecret {
			doc = "**Dev mode only.** " + doc
		}
		if f.SecretFile {
			doc = "**Secret file.** " + doc
		}
		doc = strings.ReplaceAll(doc, "|", `\|`)
		if !reload {
			fmt.Fprintf(b, "| `%s` | `%s` | %s | `%s` | %s |\n", f.Key, f.Env, f.Type, def, doc)
			continue
		}
		mode := "restart"
		if f.Live {
			mode = "live"
		}
		fmt.Fprintf(b, "| `%s` | `%s` | %s | `%s` | %s | %s |\n", f.Key, f.Env, f.Type, def, mode, doc)
	}
	return nil
}
