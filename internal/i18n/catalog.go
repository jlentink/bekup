package i18n

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
)

// Catalog holds the server-rendered texts of one package in every
// language (D-237): key → fmt format string. English is complete by
// definition; every other language must have exactly the same keys and
// the same formatting verbs in the same order (Problems, run by each
// package's tests). Keys are stable identifiers, never shown.
type Catalog map[Locale]map[string]string

// T formats the text of key in l (always through fmt, so "%%" is a
// percent sign even without arguments). A key missing in l falls back to
// English; a key missing in English returns the key itself (a bug the
// parity test catches).
func (c Catalog) T(l Locale, key string, args ...any) string {
	format, ok := c[l][key]
	if !ok {
		format, ok = c[EN][key]
	}
	if !ok {
		return key
	}
	return fmt.Sprintf(format, args...)
}

// For returns a function that formats the texts of c in l.
func (c Catalog) For(l Locale) func(key string, args ...any) string {
	if _, ok := Parse(string(l)); !ok {
		l = Default
	}
	return func(key string, args ...any) string { return c.T(l, key, args...) }
}

// verbRE matches fmt verbs (flags, width, precision, explicit indexes);
// %% is not a verb.
var verbRE = regexp.MustCompile(`%(?:%|[-+# 0]*(?:\[\d+\])?(?:\d+|\*)?(?:\.(?:\d+|\*))?(?:\[\d+\])?[a-zA-Z])`)

// verbs lists the formatting verbs of a format string in order.
func verbs(format string) []string {
	var out []string
	for _, v := range verbRE.FindAllString(format, -1) {
		if v != "%%" {
			out = append(out, v)
		}
	}
	return out
}

// Problems reports the parity problems of c: languages missing, keys
// missing or extra, empty texts and differing formatting verbs. Every
// package with a catalog asserts that it is empty.
func (c Catalog) Problems() []string {
	var out []string
	en := c[EN]
	if len(en) == 0 {
		return []string{"en: no texts"}
	}
	for _, l := range Locales {
		tr, ok := c[l]
		if !ok {
			out = append(out, string(l)+": missing language")
			continue
		}
		for _, k := range sortedKeys(en) {
			v, ok := tr[k]
			switch {
			case !ok:
				out = append(out, fmt.Sprintf("%s.%s: missing", l, k))
			case v == "":
				out = append(out, fmt.Sprintf("%s.%s: empty", l, k))
			case !slices.Equal(verbs(v), verbs(en[k])):
				out = append(out, fmt.Sprintf("%s.%s: verbs %v, English has %v", l, k, verbs(v), verbs(en[k])))
			}
		}
		for _, k := range sortedKeys(tr) {
			if _, ok := en[k]; !ok {
				out = append(out, fmt.Sprintf("%s.%s: not in English", l, k))
			}
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
