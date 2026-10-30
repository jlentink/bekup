//go:build unix

package setup

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestWriterCreate(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	dir := t.TempDir()
	w, err := newWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.create("a/b/c.txt", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(pathIn(dir, "a/b/c.txt"))
	if err != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v (umask must not narrow it): %v", fi.Mode(), err)
	}
	if err := w.create("a/b/c.txt", []byte("y"), 0o644); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("O_EXCL: %v", err)
	}
	if err := w.mkdirAll("k", 0o711); err != nil {
		t.Fatal(err)
	}
	if fi := mustStat(t, filepath.Join(dir, "k")); fi.Mode().Perm() != 0o711 {
		t.Fatalf("dir mode %v", fi.Mode())
	}
}

func TestWriterRefusesSymlinks(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "f"), filepath.Join(dir, "file")); err != nil {
		t.Fatal(err)
	}
	w, err := newWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"link/x", "link/sub/x"} {
		if err := w.create(rel, []byte("x"), 0o644); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Errorf("create(%s) = %v, want symlink refusal", rel, err)
		}
	}
	if err := w.create("file", []byte("x"), 0o644); err == nil {
		t.Error("create over a dangling symlink succeeded")
	}
	if err := w.replace("file", []byte("x"), 0o644); err == nil {
		t.Error("replace through a symlink succeeded")
	}
	if _, err := os.Stat(filepath.Join(outside, "f")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("a file was written outside the directory")
	}
	if _, err := newWriter(filepath.Join(dir, "link")); err == nil {
		t.Error("a symlinked deployment directory was accepted")
	}
	for _, bad := range []string{"../x", "/abs", "a/../../x", ""} {
		if _, err := w.abs(bad); err == nil {
			t.Errorf("abs(%q) accepted", bad)
		}
	}
}

func TestWriterRollback(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gone.txt"), []byte("gone"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, dir)
	w, err := newWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.create("new/deep/file", []byte("n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := w.replace("keep.txt", []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.remove("gone.txt"); err != nil {
		t.Fatal(err)
	}
	if got := string(readFile(t, dir, "keep.txt")); got != "new" {
		t.Fatalf("replace: %q", got)
	}
	if err := w.rollback(); err != nil {
		t.Fatal(err)
	}
	if d := diffSnapshots(before, snapshot(t, dir)); len(d) > 0 {
		t.Fatalf("rollback left changes: %v", d)
	}
}
