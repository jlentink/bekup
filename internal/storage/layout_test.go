package storage_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/storage"
)

func TestParseLayoutKey(t *testing.T) {
	ts := time.Date(2026, 10, 1, 3, 4, 5, 0, time.UTC)
	good := "nightly/" + storage.TimestampSegment(ts) + "/app.dump.zst.age"
	lk, ok := storage.ParseLayoutKey(storage.MustParseKey(good))
	if !ok || lk.JobSlug != "nightly" || !lk.Time.Equal(ts) || lk.File != "app.dump.zst.age" ||
		lk.RunDir != "nightly/"+storage.TimestampSegment(ts) || storage.RunDir("nightly", ts) != lk.RunDir {
		t.Fatalf("layout key = %+v, %v", lk, ok)
	}
	for _, k := range []string{
		"nightly/app.dump",                                 // two segments
		"bekup-probe/2026-10-01T030405Z-abc.probe",         // probe object
		"nightly/2026-10-01T030405Z/sub/app.dump",          // too deep
		"Nightly/2026-10-01T030405Z/app.dump",              // not a job slug
		"nightly/2026-10-01/app.dump",                      // not a timestamp
		"nightly/2026-13-01T030405Z/app.dump",              // invalid date
		"nightly/2026-10-01T030405Z",                       // run directory only
		"other/prefix/nightly/2026-10-01T030405Z/app.dump", // foreign tree
	} {
		if _, ok := storage.ParseLayoutKey(storage.MustParseKey(k)); ok {
			t.Errorf("%q accepted", k)
		}
	}
}

func TestPruneCandidate(t *testing.T) {
	jobs := func(s string) bool { return s == "nightly" }
	for _, c := range []struct {
		depth int
		name  string
		want  bool
	}{
		{1, "nightly", true}, {1, "other", false}, {1, "Nightly", false}, {1, ".hidden", false},
		{2, "2026-10-01T030405Z", true}, {2, "2026-10-01", false}, {2, "nightly", false}, {3, "x", false},
	} {
		if got := storage.PruneCandidate(c.depth, c.name, jobs); got != c.want {
			t.Errorf("PruneCandidate(%d, %q) = %v", c.depth, c.name, got)
		}
	}
	if !storage.PruneCandidate(1, "any-slug", nil) {
		t.Error("nil isJob must accept every slug")
	}
}

// mkdirOld creates the directories of rel below root with an old mtime.
func mkdirOld(t *testing.T, root, rel string, age time.Duration) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatal(err)
	}
	setAge(t, p, age)
	return p
}

func setAge(t *testing.T, p string, age time.Duration) {
	t.Helper()
	at := time.Now().Add(-age)
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func TestDiskPruneEmptyDirs(t *testing.T) {
	root := t.TempDir()
	d := openDisk(t, root, false)
	day := 24 * time.Hour
	old := 2 * day

	// An empty old run directory of an empty old job directory: both go.
	emptyRun := mkdirOld(t, root, "gone/2026-01-01T000000Z", old)
	setAge(t, filepath.Join(root, "gone"), old)
	// A fresh empty run directory stays (and keeps its job directory).
	fresh := mkdirOld(t, root, "fresh/2026-01-02T000000Z", time.Minute)
	setAge(t, filepath.Join(root, "fresh"), old)
	// A non-empty old run directory stays.
	full := mkdirOld(t, root, "full/2026-01-03T000000Z", old)
	if err := os.WriteFile(filepath.Join(full, "db.dump"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	setAge(t, full, old)
	setAge(t, filepath.Join(root, "full"), old)
	// A run directory with only a stray dot file stays (not empty).
	stray := mkdirOld(t, root, "stray/2026-01-04T000000Z", old)
	if err := os.WriteFile(filepath.Join(stray, ".DS_Store"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	setAge(t, stray, old)
	// Outside bekup's layout: a non-timestamp child, an unknown job and a
	// deeper directory stay.
	foreign := mkdirOld(t, root, "full/not-a-timestamp", old)
	unknown := mkdirOld(t, root, "unknown-job/2026-01-05T000000Z", old)
	deep := mkdirOld(t, root, "deep/2026-01-06T000000Z/sub", old)
	setAge(t, filepath.Join(root, "deep", "2026-01-06T000000Z"), old)
	// A symbolic link to an empty old directory is neither followed nor
	// removed.
	target := mkdirOld(t, t.TempDir(), "2026-01-07T000000Z", old)
	linkJob := mkdirOld(t, root, "linked", old)
	link := filepath.Join(linkJob, "2026-01-07T000000Z")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	setAge(t, linkJob, old)

	jobs := map[string]bool{"gone": true, "fresh": true, "full": true, "stray": true, "deep": true, "linked": true}
	n, err := d.PruneEmptyDirs(t.Context(), day, func(s string) bool { return jobs[s] })
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 2 {
		t.Errorf("removed %d directories, want 2", n)
	}
	if exists(emptyRun) || exists(filepath.Join(root, "gone")) {
		t.Error("empty old run/job directory kept")
	}
	for _, p := range []string{fresh, filepath.Join(root, "fresh"), full, stray, foreign, unknown, deep, link, target, linkJob} {
		if !exists(p) {
			t.Errorf("%s removed", p)
		}
	}
	if !exists(root) {
		t.Fatal("destination root removed")
	}

	// The root itself is never removed, even when empty and old.
	empty := t.TempDir()
	setAge(t, empty, old)
	d2 := openDisk(t, empty, false)
	if n, err := d2.PruneEmptyDirs(t.Context(), day, nil); err != nil || n != 0 || !exists(empty) {
		t.Fatalf("empty root: %d %v", n, err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	mkdirOld(t, root, "gone2/2026-01-01T000000Z", old)
	if _, err := d.PruneEmptyDirs(ctx, day, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled prune: %v", err)
	}
}

func TestDiskPruneEmptyDirsSymlinkedJob(t *testing.T) {
	root := t.TempDir()
	d := openDisk(t, root, false)
	old := 48 * time.Hour
	target := t.TempDir()
	run := mkdirOld(t, target, "2026-01-01T000000Z", old)
	if err := os.Symlink(target, filepath.Join(root, "job")); err != nil {
		t.Fatal(err)
	}
	if n, err := d.PruneEmptyDirs(t.Context(), 24*time.Hour, nil); err != nil || n != 0 {
		t.Fatalf("prune through a symlinked job directory: %d %v", n, err)
	}
	if !exists(run) {
		t.Fatal("directory behind a symbolic link removed")
	}
}

func TestDiskPruneClosed(t *testing.T) {
	d, err := storage.OpenDisk(storage.DiskConfig{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PruneEmptyDirs(t.Context(), time.Hour, nil); !errors.Is(err, storage.ErrClosed) {
		t.Fatalf("closed: %v", err)
	}
}
