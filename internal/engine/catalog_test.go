package engine

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

// renovateRe is the customManagers regex from renovate.json; every image
// line in catalog.toml must stay updatable by Renovate.
var renovateRe = regexp.MustCompile(`image\s*=\s*"(?P<depName>[^:"@]+):(?P<currentValue>[^@"]+)@(?P<currentDigest>sha256:[a-f0-9]{64})"`)

func mustEmbedded(t *testing.T) *Catalog {
	t.Helper()
	c, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	return c
}

func TestEmbeddedCatalog(t *testing.T) {
	c := mustEmbedded(t)
	want := map[Kind][]string{
		KindPostgres: {"13", "14", "15", "16", "17", "18"},
		KindMySQL:    {"8.0", "8.4", "9.7"},
		KindMariaDB:  {"10.6", "10.11", "11.4", "11.8", "12.3"},
	}
	legacy := map[string]bool{"postgres/13": true, "mysql/8.0": true, "mariadb/10.6": true}
	for k, keys := range want {
		got := c.EntriesFor(k)
		if len(got) != len(keys) {
			t.Fatalf("%s: %d entries, want %d", k, len(got), len(keys))
		}
		for i, e := range got {
			if e.Key() != keys[i] {
				t.Errorf("%s[%d] key = %s, want %s (sorted by version)", k, i, e.Key(), keys[i])
			}
			if e.Legacy() != legacy[string(k)+"/"+e.Key()] {
				t.Errorf("%s %s legacy = %v", k, e.Key(), e.Legacy())
			}
			if e.Repository() != string(k) || e.Extra() {
				t.Errorf("%s %s: repo %q extra %v", k, e.Key(), e.Repository(), e.Extra())
			}
			if !strings.HasPrefix(e.Tag(), e.ClientVersion().String()[:len(e.Key())]) {
				t.Errorf("%s %s: tag %q vs version %s", k, e.Key(), e.Tag(), e.ClientVersion())
			}
			if wantUID := map[Kind]int{KindPostgres: 70, KindMySQL: 999, KindMariaDB: 999}[k]; e.UID() != wantUID {
				t.Errorf("%s %s: uid = %d, want %d", k, e.Key(), e.UID(), wantUID)
			}
			if e.Image() != e.Repository()+":"+e.Tag()+"@"+e.Digest() {
				t.Errorf("%s %s: image %q not reassembled", k, e.Key(), e.Image())
			}
		}
	}
	if n := len(c.Entries()); n != 14 {
		t.Errorf("entries = %d, want 14", n)
	}
	if n := len(c.Images()); n != 14 {
		t.Errorf("images = %d, want 14", n)
	}
	e, ok := c.Lookup(KindMySQL, "9.7")
	if !ok || e.ClientVersion() != (Version{9, 7, 4}) {
		t.Errorf("Lookup mysql 9.7 = %+v, %v", e, ok)
	}
	if _, ok := c.Lookup(KindMySQL, "10.6"); ok {
		t.Error("mysql must not see mariadb keys")
	}
	if !c.AllowsImage(e.Image()) || c.AllowsImage("mysql:9.7.4") || c.AllowsImage("alpine:3") {
		t.Error("AllowsImage must match exact pinned references only")
	}
}

func TestEmbeddedCatalogRenovateLines(t *testing.T) {
	lines := 0
	for line := range strings.SplitSeq(string(embeddedCatalog), "\n") {
		if !strings.HasPrefix(line, "image") {
			continue
		}
		lines++
		if !renovateRe.MatchString(line) {
			t.Errorf("line not matched by the Renovate regex: %q", line)
		}
	}
	if lines != 14 {
		t.Errorf("image lines = %d, want 14", lines)
	}
}

const pgBins = `binaries = { dump = "pg_dump", dump_globals = "pg_dumpall", restore = "pg_restore", client = "psql" }`
const myBins = `binaries = { dump = "mysqldump", client = "mysql" }`
const maBins = `binaries = { dump = "mariadb-dump", client = "mariadb" }`

const digestA = "sha256:" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const digestB = "sha256:" + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func catalogDoc(images ...string) string {
	return "schema_version = 1\n" + strings.Join(images, "\n")
}

func img(engine, key, image, bins string) string {
	return "[[images]]\nengine = \"" + engine + "\"\nkey = \"" + key + "\"\nimage = \"" + image + "\"\n" + bins + "\n"
}

func TestParseValid(t *testing.T) {
	c, err := Parse([]byte(catalogDoc(
		img("postgres", "17", "postgres:17.6@"+digestA, pgBins),
		img("mysql", "9.7", "mysql:9.7.0@"+digestB, myBins),
		img("mariadb", "12.3", "mariadb:12.3.1-noble@"+digestA, maBins),
	)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(c.Entries()) != 3 {
		t.Fatalf("entries = %d", len(c.Entries()))
	}
	e, _ := c.Lookup(KindMariaDB, "12.3")
	if e.ClientVersion() != (Version{12, 3, 1}) || e.Tag() != "12.3.1-noble" {
		t.Errorf("mariadb entry = %+v", e)
	}
}

func TestParseInvalid(t *testing.T) {
	tests := []struct {
		name string
		doc  string
	}{
		{"empty", "schema_version = 1\n"},
		{"schema version", "schema_version = 2\n" + img("postgres", "17", "postgres:17.6@"+digestA, pgBins)},
		{"no digest", catalogDoc(img("postgres", "17", "postgres:17.6", pgBins))},
		{"short digest", catalogDoc(img("postgres", "17", "postgres:17.6@sha256:abc", pgBins))},
		{"upper digest", catalogDoc(img("postgres", "17", "postgres:17.6@sha256:"+strings.Repeat("A", 64), pgBins))},
		{"digest only", catalogDoc(img("postgres", "17", "postgres@"+digestA, pgBins))},
		{"unknown field", catalogDoc(img("postgres", "17", "postgres:17.6@"+digestA, pgBins) + "command = \"sh\"\n")},
		{"unknown top-level", "schema_version = 1\nfoo = 1\n" + img("postgres", "17", "postgres:17.6@"+digestA, pgBins)},
		{"unknown engine", catalogDoc(img("oracle", "17", "postgres:17.6@"+digestA, pgBins))},
		{"key mismatch", catalogDoc(img("postgres", "16", "postgres:17.6@"+digestA, pgBins))},
		{"mysql key needs minor", catalogDoc(img("mysql", "8", "mysql:8.4.6@"+digestA, myBins))},
		{"mysql needs patch", catalogDoc(img("mysql", "8.4", "mysql:8.4@"+digestA, myBins))},
		{"tag without version", catalogDoc(img("postgres", "17", "postgres:latest@"+digestA, pgBins))},
		{"duplicate key", catalogDoc(
			img("postgres", "17", "postgres:17.6@"+digestA, pgBins),
			img("postgres", "17", "postgres:17.7@"+digestB, pgBins))},
		{"not official repo", catalogDoc(img("postgres", "17", "evil/postgres:17.6@"+digestA, pgBins))},
		{"registry not allowed embedded", catalogDoc(img("postgres", "17", "ghcr.io/postgres:17.6@"+digestA, pgBins))},
		{"cross engine repo", catalogDoc(img("mariadb", "8.4", "mysql:8.4.6@"+digestA, maBins))},
		{"wrong binaries", catalogDoc(img("postgres", "17", "postgres:17.6@"+digestA, `binaries = { dump = "sh", dump_globals = "pg_dumpall", restore = "pg_restore", client = "psql" }`))},
		{"missing binaries", catalogDoc(img("postgres", "17", "postgres:17.6@"+digestA, ""))},
		{"mariadb mysql names", catalogDoc(img("mariadb", "11.4", "mariadb:11.4.5@"+digestA, `binaries = { dump = "mysqldump", client = "mysql" }`))},
		{"mysql extra binary", catalogDoc(img("mysql", "8.4", "mysql:8.4.6@"+digestA, `binaries = { dump = "mysqldump", restore = "mysql", client = "mysql" }`))},
		{"pg below restrict fix", catalogDoc(img("postgres", "17", "postgres:17.5@"+digestA, pgBins))},
		{"pg 13 below floor", catalogDoc(img("postgres", "13", "postgres:13.21@"+digestA, pgBins))},
		{"pg 12 unsupported", catalogDoc(img("postgres", "12", "postgres:12.22@"+digestA, pgBins))},
		{"pg 9.6", catalogDoc(img("postgres", "9", "postgres:9.6.24@"+digestA, pgBins))},
		{"mysql 8.0 below floor", catalogDoc(img("mysql", "8.0", "mysql:8.0.39@"+digestA, myBins))},
		{"mysql 8.4 below floor", catalogDoc(img("mysql", "8.4", "mysql:8.4.5@"+digestA, myBins))},
		{"mysql 9.2 no commands", catalogDoc(img("mysql", "9.2", "mysql:9.2.0@"+digestA, myBins))},
		{"mysql 5.7", catalogDoc(img("mysql", "5.7", "mysql:5.7.44@"+digestA, myBins))},
		{"mariadb 10.6 no sandbox", catalogDoc(img("mariadb", "10.6", "mariadb:10.6.17@"+digestA, maBins))},
		{"mariadb 10.10 eol", catalogDoc(img("mariadb", "10.10", "mariadb:10.10.7@"+digestA, maBins))},
		{"mariadb 11.3 eol", catalogDoc(img("mariadb", "11.3", "mariadb:11.3.2@"+digestA, maBins))},
		{"mariadb 11.5 rc", catalogDoc(img("mariadb", "11.5", "mariadb:11.5.1@"+digestA, maBins))},
		{"injected quote", catalogDoc(img("postgres", "17", `postgres:17.6@`+digestA+`\" x`, pgBins))},
		{"not toml", "image = [\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.doc))
			if !errors.Is(err, ErrInvalidCatalog) {
				t.Fatalf("Parse err = %v, want ErrInvalidCatalog", err)
			}
		})
	}
}

func TestParseTooLarge(t *testing.T) {
	_, err := Parse(make([]byte, maxCatalogBytes+1))
	if !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("err = %v", err)
	}
}

func TestWithExtra(t *testing.T) {
	base := mustEmbedded(t)
	c, err := base.WithExtra([]byte(catalogDoc(
		img("postgres", "19", "registry.example.com:5000/mirror/postgres:19.1-alpine@"+digestA, pgBins),
		img("mariadb", "11.5", "mariadb:11.5.2@"+digestB, maBins),
	)))
	if err != nil {
		t.Fatalf("WithExtra: %v", err)
	}
	e, ok := c.Lookup(KindPostgres, "19")
	if !ok || !e.Extra() || e.Repository() != "registry.example.com:5000/mirror/postgres" {
		t.Fatalf("extra entry = %+v %v", e, ok)
	}
	if len(c.Entries()) != len(base.Entries())+2 {
		t.Errorf("merged entries = %d", len(c.Entries()))
	}
	if _, ok := base.Lookup(KindPostgres, "19"); ok {
		t.Error("WithExtra must not modify the base catalog")
	}
	if e.UID() != 70 {
		t.Errorf("extra entry default uid = %d, want 70", e.UID())
	}
	c2, err := base.WithExtra([]byte(catalogDoc(img("postgres", "19", "postgres:19.1@"+digestA, pgBins) + "uid = 999\n")))
	if err != nil {
		t.Fatalf("WithExtra uid: %v", err)
	}
	if e, _ := c2.Lookup(KindPostgres, "19"); e.UID() != 999 {
		t.Errorf("explicit uid = %d, want 999", e.UID())
	}
	// Empty extra file is fine.
	if _, err := base.WithExtra([]byte("schema_version = 1\n")); err != nil {
		t.Errorf("empty extra: %v", err)
	}
}

func TestWithExtraInvalid(t *testing.T) {
	base := mustEmbedded(t)
	tests := map[string]string{
		"override embedded": catalogDoc(img("postgres", "18", "postgres:18.7-alpine@"+digestA, pgBins)),
		"no digest":         catalogDoc(img("postgres", "19", "postgres:19.1", pgBins)),
		"below floor":       catalogDoc(img("mysql", "8.4", "mirror/mysql:8.4.2@"+digestA, myBins)),
		"unknown field":     catalogDoc(img("postgres", "19", "postgres:19.1@"+digestA, pgBins) + "privileged = true\n"),
		"no schema":         img("postgres", "19", "postgres:19.1@"+digestA, pgBins),
		"root uid":          catalogDoc(img("postgres", "19", "postgres:19.1@"+digestA, pgBins) + "uid = -1\n"),
		"huge uid":          catalogDoc(img("postgres", "19", "postgres:19.1@"+digestA, pgBins) + "uid = 4294967296\n"),
	}
	for name, doc := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := base.WithExtra([]byte(doc)); !errors.Is(err, ErrInvalidCatalog) {
				t.Fatalf("err = %v, want ErrInvalidCatalog", err)
			}
		})
	}
}

func TestCheckFloorUnknownEngine(t *testing.T) {
	if err := checkFloor("oracle", Version{1, 0, 0}); !errors.Is(err, ErrUnknownEngine) {
		t.Fatalf("err = %v", err)
	}
}
