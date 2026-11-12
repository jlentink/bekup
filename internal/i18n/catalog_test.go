package i18n

import (
	"slices"
	"testing"
)

func TestCatalogT(t *testing.T) {
	t.Parallel()
	c := Catalog{
		EN: {"hi": "Hello %s", "only_en": "English only", "plain": "100%% sure"},
		NL: {"hi": "Hallo %s", "plain": "100%% zeker"},
	}
	for _, tc := range []struct {
		l    Locale
		key  string
		args []any
		want string
	}{
		{NL, "hi", []any{"Ann"}, "Hallo Ann"},
		{EN, "hi", []any{"Ann"}, "Hello Ann"},
		{NL, "only_en", nil, "English only"},
		{NL, "unknown", nil, "unknown"},
		{NL, "plain", nil, "100% zeker"},
		{"de", "hi", []any{"Ann"}, "Hello Ann"},
	} {
		if got := c.T(tc.l, tc.key, tc.args...); got != tc.want {
			t.Errorf("T(%s, %s) = %q, want %q", tc.l, tc.key, got, tc.want)
		}
	}
	if got := c.For("xx")("hi", "Bo"); got != "Hello Bo" {
		t.Errorf("For(invalid) = %q, want the default language", got)
	}
	if got := c.For(NL)("hi", "Bo"); got != "Hallo Bo" {
		t.Errorf("For(nl) = %q", got)
	}
}

func TestCatalogProblems(t *testing.T) {
	t.Parallel()
	c := Catalog{
		EN: {"a": "%s has %d", "b": "B", "c": "C", "w": "%5.2f%%"},
		NL: {"a": "%d heeft %s", "b": "", "d": "D", "w": "%5.2f%%"},
	}
	want := []string{
		"nl.a: verbs [%d %s], English has [%s %d]",
		"nl.b: empty",
		"nl.c: missing",
		"nl.d: not in English",
	}
	if got := c.Problems(); !slices.Equal(got, want) {
		t.Errorf("Problems() =\n%q\nwant\n%q", got, want)
	}
	if got := (Catalog{EN: {"a": "A"}}).Problems(); !slices.Equal(got, []string{"nl: missing language"}) {
		t.Errorf("missing language: %q", got)
	}
	if got := (Catalog{}).Problems(); len(got) != 1 {
		t.Errorf("empty catalog: %q", got)
	}
}

func TestVerbs(t *testing.T) {
	t.Parallel()
	if got := verbs("%q at %[2]d%% %-8s %.1f %v"); !slices.Equal(got, []string{"%q", "%[2]d", "%-8s", "%.1f", "%v"}) {
		t.Errorf("verbs = %q", got)
	}
}
