package jobs

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/scheduler"
	"github.com/jlentink/bekup/internal/schemaversion"
	"github.com/jlentink/bekup/internal/serviceutil"
)

func fieldOf(t *testing.T, err error) string {
	t.Helper()
	var fe *serviceutil.FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("want a field error, got %v", err)
	}
	return fe.Field
}

func TestSlugFromName(t *testing.T) {
	cases := map[string]string{
		"Production DB":                "production-db",
		"  --Crème brûlée!! 2026":      "cr-me-br-l-e-2026",
		"___":                          "job",
		"ÄÖÜ":                          "job",
		"a":                            "a",
		strings.Repeat("ab", 50):       strings.Repeat("ab", 31) + "a",
		"x " + strings.Repeat("y", 70): "x-" + strings.Repeat("y", 61),
	}
	for in, want := range cases {
		got := slugFromName(in)
		if got != want {
			t.Errorf("slugFromName(%q) = %q, want %q", in, got, want)
		}
		if !validSlug(got) {
			t.Errorf("slugFromName(%q) = %q is not a valid slug", in, got)
		}
	}
}

func TestWithSuffix(t *testing.T) {
	if got := withSuffix("prod", 2); got != "prod-2" {
		t.Fatal(got)
	}
	long := strings.Repeat("a", 62) + "b"
	got := withSuffix(long, 12)
	if len(got) > MaxSlugLen || !strings.HasSuffix(got, "-12") || !validSlug(got) {
		t.Fatalf("withSuffix = %q", got)
	}
}

func TestValidSlug(t *testing.T) {
	for _, s := range []string{"a", "prod-db", "0", "a-b-c", strings.Repeat("a", 63)} {
		if !validSlug(s) {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range []string{"", "-a", "a-", "A", "a_b", "a/b", "a.b", "..", strings.Repeat("a", 64), "ä"} {
		if validSlug(s) {
			t.Errorf("%q should be invalid", s)
		}
	}
}

func TestCleanText(t *testing.T) {
	if _, err := normalizeName("  "); fieldOf(t, err) != "name" {
		t.Fatal("empty name accepted")
	}
	if _, err := normalizeName("a\nb"); err == nil {
		t.Fatal("newline in name accepted")
	}
	if _, err := normalizeName(strings.Repeat("x", MaxNameRunes+1)); err == nil {
		t.Fatal("long name accepted")
	}
	if got, err := normalizeName("  Prod  "); err != nil || got != "Prod" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := normalizeDescription("line 1\nline 2\tx"); err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeDescription("bad\x00"); err == nil {
		t.Fatal("NUL accepted")
	}
	if _, err := normalizeName("a\u2028b"); err == nil {
		t.Fatal("line separator accepted")
	}
}

func TestCheckSelection(t *testing.T) {
	tests := []struct {
		name      string
		mode      DBMode
		dbs, ex   []string
		wantField string
	}{
		{"all", DBModeAll, nil, nil, ""},
		{"all with exclude", DBModeAll, nil, []string{"tmp", "scratch"}, ""},
		{"selected", DBModeSelected, []string{"app", "billing"}, nil, ""},
		{"unknown mode", "some", nil, nil, "dbMode"},
		{"all with databases", DBModeAll, []string{"app"}, nil, "databases"},
		{"selected empty", DBModeSelected, nil, nil, "databases"},
		{"selected with exclude", DBModeSelected, []string{"app"}, []string{"x"}, "exclude"},
		{"bad name", DBModeSelected, []string{"app; DROP"}, nil, "databases"},
		{"option-like name", DBModeSelected, []string{"-Fc"}, nil, "databases"},
		{"duplicate", DBModeSelected, []string{"a", "a"}, nil, "databases"},
		{"bad exclude", DBModeAll, nil, []string{"x y"}, "exclude"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sel, err := checkSelection(tc.mode, tc.dbs, tc.ex)
			if tc.wantField == "" {
				if err != nil {
					t.Fatal(err)
				}
				if sel.databases == nil || sel.exclude == nil {
					t.Fatal("nil lists")
				}
				return
			}
			if got := fieldOf(t, err); got != tc.wantField {
				t.Fatalf("field %q, want %q", got, tc.wantField)
			}
		})
	}
	many := make([]string, MaxDatabases+1)
	for i := range many {
		many[i] = "db" + strings.Repeat("x", i%10) + string(rune('a'+i%26)) + uuid.NewString()[:8]
	}
	if _, err := checkSelection(DBModeSelected, many, nil); err == nil {
		t.Fatal("too many databases accepted")
	}
	sel, err := checkSelection(DBModeSelected, []string{"b", "a"}, nil)
	if err != nil || sel.databases[0] != "a" {
		t.Fatal("databases not sorted")
	}
}

func TestCheckRetention(t *testing.T) {
	n := func(v int) *int { return &v }
	if err := checkRetention(Retention{}); fieldOf(t, err) != "retention" {
		t.Fatal("empty retention accepted")
	}
	for _, r := range []Retention{{KeepLast: n(1)}, {KeepDays: n(30)}, {KeepLast: n(10), KeepDays: n(MaxKeepDays)}} {
		if err := checkRetention(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := checkRetention(Retention{KeepLast: n(0)}); fieldOf(t, err) != "retention.keepLast" {
		t.Fatal("keepLast 0 accepted")
	}
	if err := checkRetention(Retention{KeepDays: n(MaxKeepDays + 1)}); fieldOf(t, err) != "retention.keepDays" {
		t.Fatal("keepDays too large accepted")
	}
}

func TestCheckTimeoutAndWindow(t *testing.T) {
	maxT := 6 * time.Hour
	for _, d := range []time.Duration{time.Minute, maxT, 90 * time.Minute} {
		if err := checkTimeout(d, maxT); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []time.Duration{0, 59 * time.Second, maxT + time.Second, 90*time.Second + time.Millisecond} {
		if err := checkTimeout(d, maxT); err == nil {
			t.Fatalf("timeout %s accepted", d)
		}
	}
	if err := checkExpectedWindow(time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := checkExpectedWindow(30 * time.Second); err == nil {
		t.Fatal("short window accepted")
	}
	if err := checkExpectedWindow(MaxExpectedWindow + time.Hour); err == nil {
		t.Fatal("long window accepted")
	}
}

func TestCheckSchedule(t *testing.T) {
	lim := scheduler.Limits{MinInterval: 5 * time.Minute}
	s, enc, err := checkSchedule(scheduler.Schedule{Kind: scheduler.KindInterval, Every: 1, Unit: scheduler.UnitWeeks, Anchor: "2026-01-05T03:00", Timezone: "UTC"}, lim)
	if err != nil {
		t.Fatal(err)
	}
	if s.Weekday != "monday" || !strings.Contains(string(enc), `"weekday":"monday"`) {
		t.Fatalf("not canonical: %+v %s", s, enc)
	}
	bad := []scheduler.Schedule{
		{Kind: scheduler.KindInterval, Every: 1, Unit: scheduler.UnitMinutes, Anchor: "2026-01-01T00:00", Timezone: "UTC"},
		{Kind: scheduler.KindCron, Cron: "*/2 * * * *", Timezone: "UTC"},
		{Kind: scheduler.KindCron, Cron: "0 3 * * *", Timezone: "Local"},
		{Kind: "weekly"},
	}
	for _, b := range bad {
		if _, _, err := checkSchedule(b, lim); fieldOf(t, err) != "schedule" {
			t.Fatalf("%+v accepted", b)
		}
	}
}

func TestCheckDestinations(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	if err := checkDestinations([]uuid.UUID{a, b}); err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][]uuid.UUID{nil, {a, a}, {uuid.Nil}, make([]uuid.UUID, MaxDestinations+1)} {
		if err := checkDestinations(ids); fieldOf(t, err) != "destinationIds" {
			t.Fatalf("%v accepted", ids)
		}
	}
}

func FuzzSlugFromName(f *testing.F) {
	for _, s := range []string{"Production", "--", "ä-b", strings.Repeat("z", 200)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if s := slugFromName(name); !validSlug(s) {
			t.Fatalf("slugFromName(%q) = %q", name, s)
		}
		if s := withSuffix(slugFromName(name), 999999); !validSlug(s) {
			t.Fatalf("withSuffix = %q", s)
		}
	})
}

func FuzzCheckSelection(f *testing.F) {
	f.Add("app", "tmp")
	f.Add("-x", "a b")
	f.Fuzz(func(t *testing.T, db, ex string) {
		sel, err := checkSelection(DBModeAll, nil, []string{ex})
		if err == nil && len(sel.exclude) != 1 {
			t.Fatal("exclude lost")
		}
		if _, err := checkSelection(DBModeSelected, []string{db}, nil); err == nil {
			if strings.ContainsAny(db, " ;'\"\\/=\x00") || strings.HasPrefix(db, "-") {
				t.Fatalf("unsafe name %q accepted", db)
			}
		}
	})
}

func TestCheckSchemaVersion(t *testing.T) {
	got, err := checkSchemaVersion(SchemaVersionInput{Mode: schemaversion.ModePreset, Preset: "goose"})
	if err != nil || got.Schema != "public" || got.Preset != "goose" {
		t.Fatalf("default schema: %+v %v", got, err)
	}
	got, err = checkSchemaVersion(SchemaVersionInput{Mode: schemaversion.ModeNone, Preset: "goose", CustomSQL: "SELECT 1", Schema: "x"})
	if err != nil || got.Preset != "" || got.CustomSQL != "" || got.Schema != "public" {
		t.Fatalf("none clears: %+v %v", got, err)
	}
	got, err = checkSchemaVersion(SchemaVersionInput{Mode: schemaversion.ModeCustom, CustomSQL: "SELECT 1", Preset: "goose", Schema: "x"})
	if err != nil || got.Preset != "" || got.CustomSQL != "SELECT 1" || got.Schema != "public" {
		t.Fatalf("custom: %+v %v", got, err)
	}
	for field, in := range map[string]SchemaVersionInput{
		"preset":    {Mode: schemaversion.ModePreset},
		"schema":    {Mode: schemaversion.ModePreset, Preset: "goose", Schema: "1x"},
		"customSql": {Mode: schemaversion.ModeCustom, CustomSQL: "SELECT pg_read_file('/etc/passwd')"},
		"mode":      {Mode: ""},
	} {
		if _, err := checkSchemaVersion(in); fieldOf(t, err) != field {
			t.Errorf("%s: %v", field, err)
		}
	}
	if sqlDigest("") != "" || len(sqlDigest("SELECT 1")) != 64 {
		t.Error("digest")
	}
}
