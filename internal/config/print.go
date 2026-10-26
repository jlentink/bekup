package config

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Redacted is the placeholder printed instead of secret values.
const Redacted = "[REDACTED]"

// WriteRedacted writes the effective configuration as TOML with the source
// of every key as a trailing comment. Inline secret values are replaced by
// Redacted and credentials embedded in URLs are removed. There is no
// unredacted variant.
func (r *Result[T]) WriteRedacted(w io.Writer) error {
	var b strings.Builder
	b.WriteString("# bekup effective configuration (secrets redacted)\n")
	if r.Path != "" {
		fmt.Fprintf(&b, "# loaded from: %s\n", r.Path)
	} else {
		b.WriteString("# loaded from: none (dev mode, built-in defaults)\n")
	}
	fmt.Fprintf(&b, "# dev mode: %t\n", r.Dev)
	b.WriteString("# each key is followed by its source: default < file < env < flag\n")
	section := ""
	fields := r.Fields()
	for i := range fields {
		f := &fields[i]
		if f.Section != section {
			section = f.Section
			fmt.Fprintf(&b, "\n[%s]\n", section)
		}
		val, err := redactedValue(f)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s = %s  # %s\n", f.Key, val, r.Sources[f.Path])
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func redactedValue(f *Field) (string, error) {
	v := f.Value()
	if f.InlineSecret && !f.IsZero() {
		v = Redacted
	}
	switch t := v.(type) {
	case string:
		v = redactURLCredentials(t)
	case []string:
		items := make([]string, len(t))
		for i, s := range t {
			items[i] = redactURLCredentials(s)
		}
		v = items
	}
	return tomlValue(v)
}

// tomlValue renders a single value in TOML syntax.
func tomlValue(v any) (string, error) {
	out, err := toml.Marshal(map[string]any{"v": v})
	if err != nil {
		return "", fmt.Errorf("rendering value: %w", err)
	}
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "v = "), nil
}

// redactURLCredentials removes a password from URL-shaped values.
func redactURLCredentials(s string) string {
	if !strings.Contains(s, "://") || !strings.Contains(s, "@") {
		return s
	}
	u, err := url.Parse(s)
	if err != nil {
		return Redacted
	}
	if _, has := u.User.Password(); has {
		u.User = url.UserPassword(u.User.Username(), "REDACTED")
		return u.String()
	}
	return s
}
