package hostname

import (
	"errors"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	for _, ok := range []string{
		"db", "db.internal.example.com", "DB.Example.COM", "10.0.0.5", "2001:db8::1", "::1",
		"a-b.c", "xn--bcher-kva.example", "1a.example", "123.example", strings.Repeat("a", 63) + ".com",
	} {
		if err := Validate(ok); err != nil {
			t.Errorf("Validate(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "-db", "db-", "a..b", ".db", "db.", "fe80::1%eth0", "a_b", "a b", "1.2.3", "999.1.1.1", "1.2.3.4.5",
		"db:5432", "http://db", "[::1]", "db/x", "db\x00", "db\n", strings.Repeat("a", 64) + ".com",
		strings.Repeat("a.", 127) + "aa",
	} {
		err := Validate(bad)
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("Validate(%q) = %v, want ErrInvalid", bad, err)
		}
		if err != nil && bad != "" && strings.Contains(err.Error(), bad) {
			t.Errorf("error %q repeats the input", err)
		}
	}
}

func TestValidateBracketed(t *testing.T) {
	cases := []struct {
		in, out string
		ok      bool
	}{
		{"[::1]", "::1", true},
		{"[2001:db8::1]", "2001:db8::1", true},
		{"db.example", "db.example", true},
		{"[10.0.0.1]", "", false},
		{"[db]", "", false},
		{"[::1", "", false},
		{"[fe80::1%eth0]", "", false},
	}
	for _, c := range cases {
		got, err := ValidateBracketed(c.in)
		if (err == nil) != c.ok || (c.ok && got != c.out) {
			t.Errorf("ValidateBracketed(%q) = %q, %v", c.in, got, err)
		}
	}
}

func TestNormalize(t *testing.T) {
	if got := Normalize(" DB.Example.com. "); got != "db.example.com" {
		t.Errorf("Normalize = %q", got)
	}
}

func FuzzValidate(f *testing.F) {
	for _, s := range []string{"db", "10.0.0.1", "::1", "-x", "a..b", "a_b", "1.2.3"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if Validate(s) != nil {
			return
		}
		if s == "" || len(s) > MaxLen || s[0] == '-' || strings.ContainsAny(s, " /\\'\"=@%_[]\x00\n\r") {
			t.Fatalf("accepted %q", s)
		}
	})
}
