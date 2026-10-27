package engine

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func TestPSQLRestrictPrefix(t *testing.T) {
	a, b := PSQLRestrictPrefix(), PSQLRestrictPrefix()
	if !IsPSQLRestrictPrefix(a) || !IsPSQLRestrictPrefix(b) {
		t.Fatalf("invalid prefixes %q %q", a, b)
	}
	if bytes.Equal(a, b) {
		t.Fatal("restrict keys repeat")
	}
	if len(a) != len(`\restrict `)+64+1 {
		t.Fatalf("length %d", len(a))
	}
	for _, bad := range []string{
		"", `\restrict ` + "\n", `\restrict abc` + "\n", `\unrestrict ` + strings.Repeat("a", 64) + "\n",
		`\restrict ` + strings.Repeat("A", 64) + "\n", `\restrict ` + strings.Repeat("g", 64) + "\n",
		`\restrict ` + strings.Repeat("a", 64), `\restrict ` + strings.Repeat("a", 64) + "\nx",
	} {
		if IsPSQLRestrictPrefix([]byte(bad)) {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestStripRestrictLines(t *testing.T) {
	key := "RjwAHK4dlyfweQOdGwUHL9dTvcPm094w3eB3NAahm7wW3iDJWJFGaqmW1nY6pdI"
	long := strings.Repeat("x", 200<<10)
	cases := []struct{ name, in, want string }{
		{"pg_dumpall", "--\n-- PostgreSQL database cluster dump\n--\n\n\\restrict " + key + "\nSET x = 1;\nCREATE ROLE a;\n\\unrestrict " + key + "\n-- done\n",
			"--\n-- PostgreSQL database cluster dump\n--\n\nSET x = 1;\nCREATE ROLE a;\n-- done\n"},
		{"first line", "\\restrict abc\nSELECT 1;\n", "SELECT 1;\n"},
		{"crlf", "\\restrict abc\r\nSELECT 1;\r\n", "SELECT 1;\r\n"},
		{"last line without newline is kept", "SELECT 1;\n\\unrestrict abc", "SELECT 1;\n\\unrestrict abc"},
		{"meta command kept", "\\! id\n\\restrict a b\n\\restrict a;\\! id\n", "\\! id\n\\restrict a b\n\\restrict a;\\! id\n"},
		{"not at line start", "SELECT '\n\\restrict' ; \\restrict abc\n", "SELECT '\n\\restrict' ; \\restrict abc\n"},
		{"mid-line kept", "x \\restrict abc\n", "x \\restrict abc\n"},
		{"empty key kept", "\\restrict \n", "\\restrict \n"},
		{"oversized key kept", "\\restrict " + strings.Repeat("a", 257) + "\n", "\\restrict " + strings.Repeat("a", 257) + "\n"},
		{"long lines", long + "\n\\restrict abc\n" + long, long + "\n" + long},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for name, r := range map[string]io.Reader{
				"plain":    strings.NewReader(tc.in),
				"one byte": iotest.OneByteReader(strings.NewReader(tc.in)),
				"half":     iotest.HalfReader(strings.NewReader(tc.in)),
			} {
				got, err := io.ReadAll(StripRestrictLines(r))
				if err != nil || string(got) != tc.want {
					t.Errorf("%s: got %q (%v), want %q", name, trunc(string(got)), err, trunc(tc.want))
				}
			}
		})
	}
}

func TestStripRestrictLinesError(t *testing.T) {
	boom := errors.New("boom")
	r := io.MultiReader(strings.NewReader("SELECT 1;\n"), iotest.ErrReader(boom))
	got, err := io.ReadAll(StripRestrictLines(r))
	if !errors.Is(err, boom) || string(got) != "SELECT 1;\n" {
		t.Fatalf("got %q %v", got, err)
	}
}

func trunc(s string) string {
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}
