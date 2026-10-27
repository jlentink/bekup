package db

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

func TestLatestVersion(t *testing.T) {
	got, err := latestVersion(fstest.MapFS{
		"00001_a.sql":  {},
		"00010_b.sql":  {},
		"00002_c.sql":  {},
		"embed.go":     {},
		"README.txt":   {},
		"00003_d.sql~": {},
	})
	if err != nil || got != 10 {
		t.Fatalf("latestVersion = %d, %v; want 10", got, err)
	}
	for name, fsys := range map[string]fstest.MapFS{
		"empty":      {},
		"no prefix":  {"init.sql": {}},
		"bad prefix": {"abc_x.sql": {}},
		"zero":       {"00000_x.sql": {}},
	} {
		if _, err := latestVersion(fsys); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestEmbeddedLatestMigrationVersion(t *testing.T) {
	v, err := LatestMigrationVersion()
	if err != nil || v < 6 {
		t.Fatalf("LatestMigrationVersion = %d, %v", v, err)
	}
}

func TestCompareSchemaVersion(t *testing.T) {
	if err := compareSchemaVersion(6, 6); err != nil {
		t.Fatal(err)
	}
	behind := compareSchemaVersion(4, 6)
	var sv *SchemaVersionError
	if !errors.Is(behind, ErrSchemaBehind) || !errors.As(behind, &sv) || sv.Applied != 4 || sv.Expected != 6 ||
		!strings.Contains(behind.Error(), "bekup migrate up") {
		t.Fatalf("behind: %v", behind)
	}
	ahead := compareSchemaVersion(7, 6)
	if !errors.Is(ahead, ErrSchemaAhead) || errors.Is(ahead, ErrSchemaBehind) || !strings.Contains(ahead.Error(), "newer") {
		t.Fatalf("ahead: %v", ahead)
	}
}
