package setup

import (
	"strings"
	"testing"
)

const outlineSrc = `# header line 1
# header line 2

name: x

# lead of a
a:
  # lead of b
  b: 1
  c:
    d: [1, 2]
    # trailing comment of c
  e: 2

# free block
# marker here
# end of block

f:
  g: 3
`

func TestOutlineFind(t *testing.T) {
	t.Parallel()
	o, err := newOutline(outlineSrc)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path            string
		lead, key, end  int
		wantErrContains string
	}{
		{path: "a", lead: 5, key: 6, end: 13},
		{path: "a/b", lead: 7, key: 8, end: 9},
		{path: "a/c", lead: 9, key: 9, end: 12},
		{path: "a/c/d", lead: 10, key: 10, end: 11},
		{path: "a/e", lead: 12, key: 12, end: 13},
		{path: "f/g", lead: 19, key: 19, end: 20},
		{path: "name", lead: 3, key: 3, end: 4},
		{path: "a/x", wantErrContains: `key "x" not found`},
		{path: "a/b/z", wantErrContains: "has no block children"},
		{path: "a/c/d/e", wantErrContains: "has no block children"},
		{path: "", wantErrContains: "empty path"},
	} {
		s, err := o.find(tc.path)
		if tc.wantErrContains != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErrContains) {
				t.Errorf("find(%q) err = %v", tc.path, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("find(%q): %v", tc.path, err)
			continue
		}
		if s != (nodeSpan{tc.lead, tc.key, tc.end}) {
			t.Errorf("find(%q) = %+v, want {%d %d %d}", tc.path, s, tc.lead, tc.key, tc.end)
		}
	}
	dup, err := newOutline("a:\n  b: 1\n  b: 2\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dup.find("a/b"); err == nil || !strings.Contains(err.Error(), "not unique") {
		t.Fatalf("duplicate key: %v", err)
	}
	if _, err := newOutline("a:\n\tb: 1\n"); err == nil {
		t.Fatal("tab accepted")
	}
}

func TestOutlineEdits(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		edit func(o *outline) error
		want string // "" = error expected
	}{
		{"remove with lead and blank collapse", func(o *outline) error { return o.remove("a") },
			"# header line 1\n# header line 2\n\nname: x\n\n# free block\n# marker here\n# end of block\n\nf:\n  g: 3\n"},
		{"remove child with lead", func(o *outline) error { return o.remove("a/b") },
			strings.Replace(outlineSrc, "  # lead of b\n  b: 1\n", "", 1)},
		{"remove block with trailing comment", func(o *outline) error { return o.remove("a/c") },
			strings.Replace(outlineSrc, "  c:\n    d: [1, 2]\n    # trailing comment of c\n", "", 1)},
		{"replace keeps siblings", func(o *outline) error { return o.replace("a/c", "  # new\n  c: 9\n") },
			strings.Replace(outlineSrc, "  c:\n    d: [1, 2]\n    # trailing comment of c\n", "  # new\n  c: 9\n", 1)},
		{"replace wrong indent", func(o *outline) error { return o.replace("a/c", "c: 9\n") }, ""},
		{"insert after", func(o *outline) error { return o.insertAfter("a/b", "  bb: 1\n") },
			strings.Replace(outlineSrc, "  b: 1\n", "  b: 1\n  bb: 1\n", 1)},
		{"insert empty", func(o *outline) error { return o.insertAfter("a/b", "") }, ""},
		{"replace lead", func(o *outline) error { return o.replaceLead("a", "# other lead\n") },
			strings.Replace(outlineSrc, "# lead of a\n", "# other lead\n", 1)},
		{"remove lead", func(o *outline) error { return o.replaceLead("a/b", "") },
			strings.Replace(outlineSrc, "  # lead of b\n", "", 1)},
		{"lead must be comments", func(o *outline) error { return o.replaceLead("a", "x: 1\n") }, ""},
		{"replace comment", func(o *outline) error { return o.replaceComment("marker", "# new block\n") },
			strings.Replace(outlineSrc, "# free block\n# marker here\n# end of block\n", "# new block\n", 1)},
		{"remove comment", func(o *outline) error { return o.replaceComment("marker", "") },
			strings.Replace(outlineSrc, "# free block\n# marker here\n# end of block\n\n", "", 1)},
		{"comment not found", func(o *outline) error { return o.replaceComment("absent", "") }, ""},
		{"comment not unique", func(o *outline) error { return o.replaceComment("lead of", "") }, ""},
		{"comment fragment content", func(o *outline) error { return o.replaceComment("marker", "x: 1\n") }, ""},
		{"header", func(o *outline) error { return o.replaceHeader("# new header\n") },
			strings.Replace(outlineSrc, "# header line 1\n# header line 2\n", "# new header\n", 1)},
		{"header empty", func(o *outline) error { return o.replaceHeader("") }, ""},
		{"header content", func(o *outline) error { return o.replaceHeader("a: 1\n") }, ""},
		{"remove missing", func(o *outline) error { return o.remove("zz") }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, err := newOutline(outlineSrc)
			if err != nil {
				t.Fatal(err)
			}
			err = tc.edit(o)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("no error; got:\n%s", o)
				}
				if o.String() != outlineSrc {
					t.Fatal("a failed edit changed the text")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := o.String(); got != tc.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
	if _, err := newOutline(""); err != nil {
		t.Fatal(err)
	}
	o, err := newOutline("a: 1\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := o.replaceHeader("# h\n"); err == nil {
		t.Fatal("header replaced in a file without one")
	}
}

// FuzzOutline: the editor never panics, a removal only drops lines (the
// result is a subsequence of the input), and failed edits change nothing.
func FuzzOutline(f *testing.F) {
	f.Add(outlineSrc, "a/c", "marker")
	f.Add("a:\n  b:\n    c: 1\n# x\n", "a/b/c", "x")
	f.Add("\n\n#\n a:\n b: 1\n", "a", "")
	f.Fuzz(func(t *testing.T, src, path, marker string) {
		o, err := newOutline(src)
		if err != nil {
			return
		}
		before := o.String()
		if err := o.remove(path); err != nil {
			if o.String() != before {
				t.Fatal("failed removal changed the text")
			}
		} else if !isSubsequence(splitLines(o.String()), splitLines(before)) {
			t.Fatal("removal produced lines that were not in the input")
		}
		for _, edit := range []func() error{
			func() error { return o.replace(path, "x: 1\n") },
			func() error { return o.insertAfter(path, "  y: 2\n") },
			func() error { return o.replaceLead(path, "# z\n") },
			func() error { return o.replaceComment(marker, "") },
			func() error { return o.replaceHeader("# h\n") },
		} {
			before := o.String()
			if err := edit(); err != nil && o.String() != before {
				t.Fatal("a failed edit changed the text")
			}
		}
	})
}

// splitLines splits a text into lines; the empty text has none.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func isSubsequence(sub, full []string) bool {
	i := 0
	for _, l := range full {
		if i < len(sub) && sub[i] == l {
			i++
		}
	}
	return i == len(sub)
}
