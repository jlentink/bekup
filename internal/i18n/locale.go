package i18n

import (
	"regexp"
	"strconv"
	"strings"
)

// qvalueRE is the RFC 9110 qvalue grammar: 0 to 1 with at most three
// decimals (no NaN, exponents or hex floats).
var qvalueRE = regexp.MustCompile(`^(?:0(?:\.\d{0,3})?|1(?:\.0{0,3})?)$`)

// Locale is a supported language: English or Dutch.
type Locale string

// The supported languages; English is the default.
const (
	EN Locale = "en"
	NL Locale = "nl"
)

// Default is the language when nothing else decides.
const Default = EN

// Locales lists the supported languages in display order.
var Locales = []Locale{EN, NL}

// Parse accepts exactly a supported locale code ("en", "nl").
func Parse(s string) (Locale, bool) {
	switch Locale(s) {
	case EN, NL:
		return Locale(s), true
	default:
		return "", false
	}
}

// Bounds of the Accept-Language parser: longer headers and further
// entries are ignored (a client gains nothing from a huge header).
const (
	maxAcceptLanguageBytes   = 512
	maxAcceptLanguageEntries = 16
)

// FromAcceptLanguage returns the supported language the client prefers
// most (by q-value, then order), and false when the header names none.
// "*" matches nothing in particular; malformed entries are skipped.
func FromAcceptLanguage(header string) (Locale, bool) {
	if len(header) > maxAcceptLanguageBytes {
		header = header[:maxAcceptLanguageBytes]
	}
	best, bestQ := Locale(""), 0.0
	for i, entry := range strings.Split(header, ",") {
		if i >= maxAcceptLanguageEntries {
			break
		}
		tag, q, ok := parseEntry(entry)
		if !ok || q <= bestQ {
			continue
		}
		base, _, _ := strings.Cut(tag, "-")
		if l, ok := Parse(strings.ToLower(base)); ok {
			best, bestQ = l, q
		}
	}
	return best, best != ""
}

// parseEntry splits "nl-NL;q=0.8" into the tag and its weight (1 when
// absent). A weight outside 0..1 or unparsable makes the entry invalid.
func parseEntry(entry string) (string, float64, bool) {
	tag, params, _ := strings.Cut(strings.TrimSpace(entry), ";")
	tag = strings.TrimSpace(tag)
	if tag == "" || len(tag) > 35 {
		return "", 0, false
	}
	q := 1.0
	for _, p := range strings.Split(params, ";") {
		k, v, found := strings.Cut(strings.TrimSpace(p), "=")
		if !found || !strings.EqualFold(strings.TrimSpace(k), "q") {
			continue
		}
		v = strings.TrimSpace(v)
		if !qvalueRE.MatchString(v) {
			return "", 0, false
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return "", 0, false
		}
		q = f
	}
	return tag, q, true
}

// Resolve picks the language of a server-rendered text: the account's
// stored choice, else the request's Accept-Language, else Default.
func Resolve(stored *string, requested Locale) Locale {
	if stored != nil {
		if l, ok := Parse(*stored); ok {
			return l
		}
	}
	if _, ok := Parse(string(requested)); ok {
		return requested
	}
	return Default
}
