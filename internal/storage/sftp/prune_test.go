package sftp

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func agedDir(t *testing.T, root, rel string, age time.Duration) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatal(err)
	}
	ageOf(t, p, age)
	return p
}

func ageOf(t *testing.T, p string, age time.Duration) {
	t.Helper()
	at := time.Now().Add(-age)
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
}

func present(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func TestPruneEmptyDirs(t *testing.T) {
	srv := startServer(t)
	root := t.TempDir()
	base := filepath.Join(root, "base")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	old := 48 * time.Hour

	emptyRun := agedDir(t, base, "gone/2026-01-01T000000Z", old)
	ageOf(t, filepath.Join(base, "gone"), old)
	fresh := agedDir(t, base, "fresh/2026-01-02T000000Z", time.Minute)
	ageOf(t, filepath.Join(base, "fresh"), old)
	full := agedDir(t, base, "full/2026-01-03T000000Z", old)
	if err := os.WriteFile(filepath.Join(full, "db.dump"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ageOf(t, full, old)
	foreign := agedDir(t, base, "gone2/not-a-timestamp", old)
	unknown := agedDir(t, base, "unknown/2026-01-04T000000Z", old)
	// A symbolic link (to an empty old directory outside the base) is
	// neither followed nor removed.
	outside := agedDir(t, root, "outside/2026-01-05T000000Z", old)
	linkJob := agedDir(t, base, "linked", old)
	link := filepath.Join(linkJob, "2026-01-05T000000Z")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "outside"), filepath.Join(base, "jobsym")); err != nil {
		t.Fatal(err)
	}
	ageOf(t, linkJob, old)

	d := mustOpen(t, srv.keyConfig(t, base))
	jobs := map[string]bool{"gone": true, "gone2": true, "fresh": true, "full": true, "linked": true, "jobsym": true}
	n, err := d.PruneEmptyDirs(ctxT(t), 24*time.Hour, func(s string) bool { return jobs[s] })
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 2 {
		t.Errorf("removed %d, want 2", n)
	}
	if present(emptyRun) || present(filepath.Join(base, "gone")) {
		t.Error("empty old run/job directory kept")
	}
	for _, p := range []string{fresh, full, foreign, unknown, link, outside, linkJob, filepath.Join(base, "jobsym"), base} {
		if !present(p) {
			t.Errorf("%s removed", p)
		}
	}

	// No-op on an empty base; the base is never removed.
	ageOf(t, base, old)
	empty := t.TempDir()
	ageOf(t, empty, old)
	d2 := mustOpen(t, srv.keyConfig(t, empty))
	if n, err := d2.PruneEmptyDirs(ctxT(t), time.Hour, nil); err != nil || n != 0 || !present(empty) {
		t.Fatalf("empty base: %d %v", n, err)
	}
}
