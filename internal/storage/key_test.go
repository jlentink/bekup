package storage_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/storage"
)

func TestParseKey(t *testing.T) {
	tests := []struct {
		in string
		ok bool
	}{
		{"a", true},
		{"a/b/c", true},
		{"job-1/2026-10-26T030000Z/db%2E1.dump.zst.age", true},
		{"A.b~c+d=e@f_g-h", true},
		{"", false},
		{"/a", false},
		{"a/", false},
		{"a//b", false},
		{".", false},
		{"..", false},
		{"a/../b", false},
		{"a/./b", false},
		{".hidden", false},
		{"a/.partial", false},
		{"a\\b", false},
		{"a b", false},
		{"a\x00b", false},
		{"a\nb", false},
		{"a:b", false},
		{"ä", false},
		{strings.Repeat("a", 255), true},
		{strings.Repeat("a", 256), false},
		{strings.Repeat("a/", 31) + "a", true},
		{strings.Repeat("a/", 32) + "a", false},
		{strings.Repeat(strings.Repeat("a", 200)+"/", 6) + "a", false},
	}
	for _, tc := range tests {
		k, err := storage.ParseKey(tc.in)
		if tc.ok != (err == nil) {
			t.Errorf("ParseKey(%q) err = %v, want ok=%v", tc.in, err, tc.ok)
			continue
		}
		if err != nil && !errors.Is(err, storage.ErrInvalidKey) {
			t.Errorf("ParseKey(%q) error not ErrInvalidKey: %v", tc.in, err)
		}
		if err == nil && k.String() != tc.in {
			t.Errorf("String() = %q", k.String())
		}
	}
}

func TestParsePrefix(t *testing.T) {
	for in, want := range map[string]string{"": "", "/": "", "a/": "a", "a/b": "a/b"} {
		p, err := storage.ParsePrefix(in)
		if err != nil || p.String() != want {
			t.Errorf("ParsePrefix(%q) = %q, %v", in, p, err)
		}
	}
	for _, in := range []string{"//", "a//", "../a", "/a"} {
		if _, err := storage.ParsePrefix(in); err == nil {
			t.Errorf("ParsePrefix(%q) accepted", in)
		}
	}
}

func TestKeyMethods(t *testing.T) {
	k := storage.MustParseKey("a/b/c")
	if k.Base() != "c" || k.Dir().String() != "a/b" || k.Dir().Dir().Dir().IsZero() != true {
		t.Errorf("base/dir: %q %q", k.Base(), k.Dir())
	}
	if got := k.Segments(); len(got) != 3 || got[0] != "a" {
		t.Errorf("segments = %v", got)
	}
	if (storage.Key{}).Segments() != nil || (storage.Key{}).Base() != "" {
		t.Error("root key")
	}
	cases := []struct {
		p    string
		want bool
	}{{"", true}, {"a", true}, {"a/b", true}, {"a/b/c", true}, {"a/bc", false}, {"a/b/c/d", false}, {"b", false}}
	for _, c := range cases {
		p, err := storage.ParsePrefix(c.p)
		if err != nil {
			t.Fatal(err)
		}
		if got := k.HasPrefix(p); got != c.want {
			t.Errorf("HasPrefix(%q) = %v", c.p, got)
		}
	}
	if _, err := k.Join("d/e"); err != nil {
		t.Errorf("join: %v", err)
	}
	if _, err := k.Join(".."); err == nil {
		t.Error("join .. accepted")
	}
	if j, err := (storage.Key{}).Join("x"); err != nil || j.String() != "x" {
		t.Errorf("root join = %q, %v", j, err)
	}

	type wrapper struct {
		K storage.Key `json:"K"`
	}
	var out wrapper
	b, err := json.Marshal(wrapper{k})
	if err != nil || string(b) != `{"K":"a/b/c"}` {
		t.Fatalf("marshal = %s, %v", b, err)
	}
	if err := json.Unmarshal(b, &out); err != nil || out.K != k {
		t.Errorf("unmarshal = %v, %v", out.K, err)
	}
	if err := json.Unmarshal([]byte(`{"K":"../x"}`), &out); !errors.Is(err, storage.ErrInvalidKey) {
		t.Errorf("unmarshal traversal: %v", err)
	}
}

func TestValidateJobSlug(t *testing.T) {
	for _, s := range []string{"a", "job-1", "x_y", "0abc", strings.Repeat("a", 100)} {
		if err := storage.ValidateJobSlug(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	for _, s := range []string{"", "-a", "_a", "A", "a.b", "a/b", "a b", strings.Repeat("a", 101)} {
		if err := storage.ValidateJobSlug(s); err == nil {
			t.Errorf("%q accepted", s)
		}
	}
}

func TestTimestampSegment(t *testing.T) {
	ts := time.Date(2026, 10, 26, 4, 0, 0, 999, time.FixedZone("CET", 3600))
	seg := storage.TimestampSegment(ts)
	if seg != "2026-10-26T030000Z" {
		t.Fatalf("segment = %q", seg)
	}
	back, err := storage.ParseTimestampSegment(seg)
	if err != nil || !back.Equal(ts.Truncate(time.Second)) {
		t.Errorf("parse = %v, %v", back, err)
	}
	if _, err := storage.ParseTimestampSegment("2026-10-26"); !errors.Is(err, storage.ErrInvalidKey) {
		t.Errorf("bad segment: %v", err)
	}
}

func TestBuildKey(t *testing.T) {
	ts := time.Date(2026, 10, 26, 3, 0, 0, 0, time.UTC)
	name, err := storage.ArtifactFileName("my.db", "dump.zst.age")
	if err != nil {
		t.Fatal(err)
	}
	k, err := storage.BuildKey("backups/prod/", "nightly", ts, name)
	if err != nil || k.String() != "backups/prod/nightly/2026-10-26T030000Z/my%2Edb.dump.zst.age" {
		t.Fatalf("BuildKey = %q, %v", k, err)
	}
	if k, err := storage.BuildKey("", "nightly", ts, storage.ManifestFileName); err != nil || k.String() != "nightly/2026-10-26T030000Z/manifest.json" {
		t.Errorf("manifest key = %q, %v", k, err)
	}
	bad := []struct {
		prefix, slug string
		ts           time.Time
		file         string
	}{
		{"../x", "j", ts, "f"},
		{"", "J", ts, "f"},
		{"", "j", time.Time{}, "f"},
		{"", "j", ts, "a/b"},
		{"", "j", ts, ".f"},
	}
	for _, b := range bad {
		if _, err := storage.BuildKey(b.prefix, b.slug, b.ts, b.file); !errors.Is(err, storage.ErrInvalidKey) {
			t.Errorf("BuildKey(%+v) = %v", b, err)
		}
	}
}

func TestArtifactFileName(t *testing.T) {
	for _, ext := range []string{"", "Dump", "a..b", "a.b.c.d.e", "dump-1", strings.Repeat("a", 33)} {
		if _, err := storage.ArtifactFileName("db", ext); err == nil {
			t.Errorf("extension %q accepted", ext)
		}
	}
	if _, err := storage.ArtifactFileName("manifest", "json"); err == nil {
		t.Error("manifest collision accepted")
	}
	if n, err := storage.ArtifactFileName("globals", "sql.zst"); err != nil || n != "globals.sql.zst" {
		t.Errorf("= %q, %v", n, err)
	}
}

func TestEncodeDBName(t *testing.T) {
	tests := map[string]string{
		"app":      "app",
		"App_DB-1": "App_DB-1",
		"my.db":    "my%2Edb",
		"a b/c":    "a%20b%2Fc",
		"50%":      "50%25",
		"~x":       "%7Ex",
		"..":       "%2E%2E",
		"café":     "caf%C3%A9",
		"\x00\xff": "%00%FF",
		"manifest": "manifest",
	}
	for in, want := range tests {
		got, err := storage.EncodeDBName(in)
		if err != nil || got != want {
			t.Errorf("EncodeDBName(%q) = %q, %v; want %q", in, got, err, want)
			continue
		}
		back, err := storage.DecodeDBName(got)
		if err != nil || back != in {
			t.Errorf("DecodeDBName(%q) = %q, %v", got, back, err)
		}
	}
	long := strings.Repeat("é", 40) // 80 bytes -> 240 encoded
	enc, err := storage.EncodeDBName(long)
	if err != nil || !strings.HasPrefix(enc, "~") || len(enc) != 53 {
		t.Fatalf("hashed = %q, %v", enc, err)
	}
	if err := storage.ValidateSegment(enc); err != nil {
		t.Errorf("hashed form invalid segment: %v", err)
	}
	if _, err := storage.DecodeDBName(enc); !errors.Is(err, storage.ErrDBNameHashed) {
		t.Errorf("decode hashed: %v", err)
	}
	for _, in := range []string{"", strings.Repeat("a", 1025)} {
		if _, err := storage.EncodeDBName(in); !errors.Is(err, storage.ErrInvalidKey) {
			t.Errorf("EncodeDBName(len %d) = %v", len(in), err)
		}
	}
	for _, bad := range []string{"", "%", "%2", "a%", "a%2", "%2e", "%41", "%GG", "a.b", "a%2Eb.c", strings.Repeat("a", 181)} {
		if _, err := storage.DecodeDBName(bad); !errors.Is(err, storage.ErrInvalidKey) {
			t.Errorf("DecodeDBName(%q) = %v", bad, err)
		}
	}
	if !storage.ValidUTF8DBName("café") || storage.ValidUTF8DBName("\xff") {
		t.Error("ValidUTF8DBName")
	}
}
