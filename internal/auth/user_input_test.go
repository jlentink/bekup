package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeEmail(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"pat@example.com", "pat@example.com", true},
		{"  Pat.Smith+x@example.co.uk ", "Pat.Smith+x@example.co.uk", true},
		{"", "", false},
		{"a@", "", false},
		{"@b", "", false},
		{"no-at-sign", "", false},
		{"Pat <pat@example.com>", "", false},
		{"pat@exa mple.com", "", false},
		{"pat@example.com\n", "pat@example.com", true},
		{"pat\x00@example.com", "", false},
		{"a@b@c", "", false},
		{strings.Repeat("a", 250) + "@b.cd", "", false},
		{"\xff@example.com", "", false},
	} {
		got, err := NormalizeEmail("email", tc.in)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("NormalizeEmail(%q) = %q, %v", tc.in, got, err)
		}
		var fe *FieldError
		if err != nil && (!errors.As(err, &fe) || fe.Field != "email") {
			t.Errorf("error type: %v", err)
		}
	}
}

func TestNormalizeDisplayName(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"Pat", "Pat", true},
		{"  Ada Lovelace  ", "Ada Lovelace", true},
		{"", "", false},
		{"   ", "", false},
		{"a\tb", "", false},
		{strings.Repeat("é", 200), strings.Repeat("é", 200), true},
		{strings.Repeat("é", 201), "", false},
		{"\xff", "", false},
	} {
		got, err := NormalizeDisplayName("displayName", tc.in)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("NormalizeDisplayName(%q) = %q, %v", tc.in, got, err)
		}
	}
}

func TestRandomCode(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		c, err := randomCode(temporaryPasswordBytes)
		if err != nil {
			t.Fatal(err)
		}
		// 160 bits = 32 base32 characters in 8 groups of 4.
		if len(c) != 39 || strings.Count(c, "-") != 7 {
			t.Fatalf("format: %q", c)
		}
		raw := strings.ReplaceAll(c, "-", "")
		b, err := codeEncoding.DecodeString(raw)
		if err != nil {
			t.Fatalf("decode %q: %v", raw, err)
		}
		if len(b) != temporaryPasswordBytes {
			t.Fatalf("decoded %d bytes", len(b))
		}
		if seen[raw] {
			t.Fatal("duplicate code")
		}
		seen[raw] = true
	}
	if temporaryPasswordBytes*8 < 128 {
		t.Fatal("temporary passwords must carry at least 128 bits")
	}
}

func FuzzNormalizeEmail(f *testing.F) {
	f.Add("pat@example.com")
	f.Add("a <b@c>")
	f.Fuzz(func(t *testing.T, s string) {
		got, err := NormalizeEmail("email", s)
		if err != nil {
			return
		}
		if got != strings.TrimSpace(got) || !strings.Contains(got, "@") || len(got) > 4*maxEmailLength {
			t.Fatalf("accepted %q", got)
		}
	})
}
