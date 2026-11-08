package i18n

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		want Locale
		ok   bool
	}{
		{"en", EN, true}, {"nl", NL, true}, {"NL", "", false}, {"nl-NL", "", false}, {"", "", false}, {"de", "", false},
	} {
		got, ok := Parse(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("Parse(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestFromAcceptLanguage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		header string
		want   Locale
		ok     bool
	}{
		{"nl-NL,nl;q=0.9,en-US;q=0.8,en;q=0.7", NL, true},
		{"en-GB,en;q=0.9,nl;q=0.8", EN, true},
		{"de-DE,de;q=0.9,nl;q=0.5,en;q=0.4", NL, true},
		{"en;q=0.3, nl;q=0.7", NL, true},
		{"NL-be", NL, true},
		{"de, fr", "", false},
		{"*", "", false},
		{"", "", false},
		{"nl;q=0", "", false},
		{"nl;q=2, en", EN, true},
		{"nl;q=abc, en;q=0.1", EN, true},
		{"nl;q=NaN, en;q=0.1", EN, true},
		{"nl;q=0x1p-1, en;q=0.1", EN, true},
		{"nl;q=1e-1, en;q=0.05", EN, true},
		{"nl;q=0.1234, en;q=0.05", EN, true},
		{"nl;q=1.000, en;q=0.9", NL, true},
		{"nl;q=0.5, en;q=0.500", NL, true},
		{"en, nl", EN, true},
		{strings.Repeat("de,", 20) + "nl", "", false},
	} {
		got, ok := FromAcceptLanguage(tc.header)
		if got != tc.want || ok != tc.ok {
			t.Errorf("FromAcceptLanguage(%q) = %q, %v; want %q, %v", tc.header, got, ok, tc.want, tc.ok)
		}
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()
	nl, bad := "nl", "xx"
	if got := Resolve(&nl, EN); got != NL {
		t.Errorf("stored choice must win, got %q", got)
	}
	if got := Resolve(nil, NL); got != NL {
		t.Errorf("request language without a stored choice, got %q", got)
	}
	if got := Resolve(&bad, ""); got != Default {
		t.Errorf("invalid stored and no request language, got %q", got)
	}
	if got := Resolve(nil, "de"); got != Default {
		t.Errorf("unsupported request language, got %q", got)
	}
}

func FuzzFromAcceptLanguage(f *testing.F) {
	for _, s := range []string{"nl-NL,nl;q=0.9", "en;q=0.5", "*;q=0.1", ";;;", "nl;q=1e400", "nl;q=NaN", "nl;q=0x1p-1"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, header string) {
		l, ok := FromAcceptLanguage(header)
		if ok {
			if _, valid := Parse(string(l)); !valid {
				t.Fatalf("returned unsupported locale %q", l)
			}
		} else if l != "" {
			t.Fatalf("locale %q without ok", l)
		}
	})
}
