package engine

import (
	"errors"
	"strings"
	"testing"
)

func TestPostgresDumpSnapshot(t *testing.T) {
	eng := pgEngine(t)
	tgt := pgTarget()
	files := []File{FilePGPass, FileCA}
	for _, c := range mustEmbedded(t).EntriesFor(KindPostgres) {
		t.Run(c.Key(), func(t *testing.T) {
			cmd, err := eng.DumpSnapshot(c, tgt, "shop", "00000003-0000001B-1")
			if err != nil {
				t.Fatal(err)
			}
			assertCommand(t, "dump snapshot", cmd, c.Image(),
				[]string{"pg_dump", "--format=custom", "--compress=0", "--no-password", "--snapshot=00000003-0000001B-1", "--dbname=" + pgCIPinned}, files, false)
			plain, err := eng.DumpSnapshot(c, tgt, "shop", "")
			if err != nil {
				t.Fatal(err)
			}
			want, err := eng.Dump(c, tgt, "shop")
			if err != nil {
				t.Fatal(err)
			}
			assertCommand(t, "no snapshot", plain, want.Image, want.Argv, want.Files, false)
		})
	}
	c := mustEmbedded(t).EntriesFor(KindPostgres)[0]
	for _, bad := range []string{"--help", "0000-", "-1", "00000003-0000001b-1", "00000003 -1", "00000003-1;", "x", "00000003",
		"'00000003-1'", "00000003-0000001B-1\n", strings.Repeat("A", 17) + "-1"} {
		if _, err := eng.DumpSnapshot(c, tgt, "shop", bad); !errors.Is(err, ErrInvalidName) {
			t.Errorf("snapshot %q: err = %v", bad, err)
		}
	}
}

func TestMySQLDumpSnapshotRefused(t *testing.T) {
	for _, k := range []Kind{KindMySQL, KindMariaDB} {
		eng, err := ForKind(k)
		if err != nil {
			t.Fatal(err)
		}
		c := mustEmbedded(t).EntriesFor(k)[0]
		tgt := Target{Host: "db.internal", Port: 3306, User: "backup", ServerVersion: Version{8, 4, 0}}
		if _, err := eng.DumpSnapshot(c, tgt, "shop", "00000003-1"); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: err = %v", k, err)
		}
		got, err := eng.DumpSnapshot(c, tgt, "shop", "")
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		want, err := eng.Dump(c, tgt, "shop")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(got.Argv, " ") != strings.Join(want.Argv, " ") {
			t.Errorf("%s: argv %q, want %q", k, got.Argv, want.Argv)
		}
	}
}

func TestValidateSnapshotID(t *testing.T) {
	for _, ok := range []string{"00000003-0000001B-1", "000003A1-1", "00000004-00000002-17", "0000000A-0000000000000001-2"} {
		if err := ValidateSnapshotID(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
}

func TestQuoteObjects(t *testing.T) {
	pg := map[string]string{
		"users":        `"users"`,
		`we"ird`:       `"we""ird"`,
		"Mixed Case.x": `"Mixed Case.x"`,
		"ünï`code":     "\"ünï`code\"",
	}
	for in, want := range pg {
		got, err := QuotePGObject(in)
		if err != nil || got != want {
			t.Errorf("QuotePGObject(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	my := map[string]string{
		"users":   "`users`",
		"we`ird":  "`we``ird`",
		`dq"x`:    "`dq\"x`",
		"a b.c;d": "`a b.c;d`",
	}
	for in, want := range my {
		got, err := QuoteMySQLObject(in)
		if err != nil || got != want {
			t.Errorf("QuoteMySQLObject(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "a\x00b", "\xff\xfe", strings.Repeat("a", 64)} {
		if _, err := QuotePGObject(bad); !errors.Is(err, ErrInvalidName) {
			t.Errorf("QuotePGObject(%q) err = %v", bad, err)
		}
	}
	if _, err := QuoteMySQLObject(strings.Repeat("ü", 64)); err != nil {
		t.Errorf("64 characters: %v", err)
	}
	for _, bad := range []string{"", "a\x00b", "\xff", strings.Repeat("ü", 65)} {
		if _, err := QuoteMySQLObject(bad); !errors.Is(err, ErrInvalidName) {
			t.Errorf("QuoteMySQLObject(%q) err = %v", bad, err)
		}
	}
}

func FuzzQuotePGObject(f *testing.F) {
	for _, s := range []string{"t", `a"b`, `""`, "x\x00"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		q, err := QuotePGObject(s)
		if err != nil {
			return
		}
		inner := q[1 : len(q)-1]
		if q[0] != '"' || q[len(q)-1] != '"' || strings.ReplaceAll(inner, `""`, "") != strings.ReplaceAll(s, `"`, "") {
			t.Fatalf("bad quoting %q -> %q", s, q)
		}
		if strings.Count(inner, `"`)%2 != 0 {
			t.Fatalf("unbalanced quotes %q", q)
		}
	})
}
