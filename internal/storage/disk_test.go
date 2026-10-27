package storage_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/storage"
	"github.com/jlentink/bekup/internal/storage/storagetest"
)

func newDisk(t *testing.T, o storagetest.Options) storage.Destination {
	t.Helper()
	d, err := storage.OpenDisk(storage.DiskConfig{Root: t.TempDir(), Dir: "dest/sub", NoDelete: o.NoDelete})
	if err != nil {
		t.Fatalf("open disk: %v", err)
	}
	return d
}

func TestDiskConformance(t *testing.T) {
	size := int64(50 << 20)
	if testing.Short() {
		size = 4 << 20
	}
	storagetest.Run(t, storagetest.Config{New: newDisk, LargeSize: size})
}

func openDisk(t *testing.T, root string, noDelete bool) *storage.Disk {
	t.Helper()
	d, err := storage.OpenDisk(storage.DiskConfig{Root: root, NoDelete: noDelete})
	if err != nil {
		t.Fatalf("open disk: %v", err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return d
}

func diskPut(t *testing.T, d *storage.Disk, key, data string) error {
	t.Helper()
	_, err := d.Put(t.Context(), storage.MustParseKey(key), bytes.NewReader([]byte(data)), storage.PutOptions{})
	return err
}

func TestOpenDiskValidation(t *testing.T) {
	root := t.TempDir()
	for _, cfg := range []storage.DiskConfig{
		{Root: ""},
		{Root: "relative/dir"},
		{Root: root, Dir: "../escape"},
		{Root: root, Dir: "/abs"},
		{Root: filepath.Join(root, "missing")},
	} {
		if d, err := storage.OpenDisk(cfg); err == nil {
			_ = d.Close() //nolint:gosec // test cleanup
			t.Errorf("OpenDisk(%+v) succeeded", cfg)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if d, err := storage.OpenDisk(storage.DiskConfig{Root: root, Dir: "link"}); err == nil {
		_ = d.Close() //nolint:gosec // test cleanup
		t.Error("OpenDisk through a symlinked directory succeeded")
	}
}

func TestDiskSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "victim"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "dirlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "victim"), filepath.Join(root, "filelink")); err != nil {
		t.Fatal(err)
	}
	d := openDisk(t, root, false)
	ctx := t.Context()

	if err := diskPut(t, d, "dirlink/new", "x"); err == nil {
		t.Error("put through a directory symlink succeeded")
	}
	if _, err := os.Stat(filepath.Join(outside, "new")); !errors.Is(err, os.ErrNotExist) {
		t.Error("file written outside the root")
	}
	if _, _, err := d.Get(ctx, storage.MustParseKey("dirlink/victim")); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("get through dir symlink: %v", err)
	}
	if _, _, err := d.Get(ctx, storage.MustParseKey("filelink")); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("get file symlink: %v", err)
	}
	if _, err := d.Stat(ctx, storage.MustParseKey("filelink")); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("stat file symlink: %v", err)
	}
	if err := diskPut(t, d, "filelink", "x"); !errors.Is(err, storage.ErrAlreadyExists) {
		t.Errorf("put over a file symlink: %v", err)
	}
	if err := d.Delete(ctx, storage.MustParseKey("filelink")); err != nil {
		t.Errorf("delete symlink: %v", err)
	}
	if err := d.Delete(ctx, storage.MustParseKey("dirlink/victim")); err != nil {
		t.Errorf("delete through dir symlink: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(outside, "victim")) //nolint:gosec // G304: test temp dir
	if err != nil || string(b) != "outside" {
		t.Errorf("victim changed: %q %v", b, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "filelink")); err != nil {
		t.Errorf("symlink was removed: %v", err)
	}
	if err := diskPut(t, d, "real/obj", "x"); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for o, err := range storage.All(ctx, d, storage.Key{}) {
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, o.Key.String())
	}
	if !slices.Equal(keys, []string{"real/obj"}) {
		t.Errorf("list = %v (symlinks must not be listed)", keys)
	}
	page, err := d.List(ctx, storage.MustParseKey("dirlink"), storage.ListOptions{})
	if err != nil || len(page.Objects) != 0 {
		t.Errorf("list symlinked prefix = %v, %v", page.Objects, err)
	}
}

func TestDiskPermissions(t *testing.T) {
	root := t.TempDir()
	d := openDisk(t, root, false)
	if err := diskPut(t, d, "job/ts/file", "x"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(root, "job", "ts", "file"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("file mode %v", fi.Mode().Perm())
	}
	for _, dir := range []string{"job", "job/ts"} {
		fi, err := os.Stat(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("dir %s mode %v", dir, fi.Mode().Perm())
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "job", "ts"))
	if err != nil || len(entries) != 1 {
		t.Errorf("leftover temporary files: %v %v", entries, err)
	}
}

func TestDiskDeleteRemovesEmptyDirs(t *testing.T) {
	root := t.TempDir()
	d := openDisk(t, root, false)
	if err := diskPut(t, d, "a/b/c/obj", "x"); err != nil {
		t.Fatal(err)
	}
	if err := diskPut(t, d, "a/keep", "x"); err != nil {
		t.Fatal(err)
	}
	if err := d.Delete(t.Context(), storage.MustParseKey("a/b/c/obj")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "a", "b")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("empty directory kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "a", "keep")); err != nil {
		t.Errorf("sibling removed: %v", err)
	}
}

func TestDiskSourceErrorLeavesNoPartial(t *testing.T) {
	root := t.TempDir()
	d := openDisk(t, root, false)
	boom := errors.New("boom")
	_, err := d.Put(t.Context(), storage.MustParseKey("x/obj"), &errReader{err: boom}, storage.PutOptions{})
	if !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "x"))
	if err != nil || len(entries) != 0 {
		t.Errorf("leftovers: %v %v", entries, err)
	}
}

type errReader struct{ err error }

func (e *errReader) Read(b []byte) (int, error) {
	copy(b, "data")
	return min(len(b), 4), e.err
}

func TestDiskCleanupPartials(t *testing.T) {
	root := t.TempDir()
	d := openDisk(t, root, true)
	if err := os.MkdirAll(filepath.Join(root, "job", "ts"), 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(root, "job", "ts", ".0123456789abcdef0123456789abcdef.partial")
	fresh := filepath.Join(root, "job", "ts", ".fedcba9876543210fedcba9876543210.partial")
	other := filepath.Join(root, "job", "ts", ".not-a-partial")
	for _, p := range []string{old, fresh, other} {
		if err := os.WriteFile(p, []byte("crash"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(other, past, past); err != nil {
		t.Fatal(err)
	}
	page, err := d.List(t.Context(), storage.Key{}, storage.ListOptions{})
	if err != nil || len(page.Objects) != 0 {
		t.Fatalf("partials listed: %v %v", page.Objects, err)
	}
	n, err := d.CleanupPartials(t.Context(), time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("cleanup = %d, %v", n, err)
	}
	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Error("old partial kept")
	}
	for _, p := range []string{fresh, other} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s removed: %v", filepath.Base(p), err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := d.CleanupPartials(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled cleanup: %v", err)
	}
}

func TestDiskFreeSpace(t *testing.T) {
	d := openDisk(t, t.TempDir(), false)
	n, err := d.FreeSpace()
	if errors.Is(err, storage.ErrUnsupported) {
		t.Skip("free space unsupported on this platform")
	}
	if err != nil || n == 0 {
		t.Fatalf("free space = %d, %v", n, err)
	}
	dir := t.TempDir()
	if err := storage.CheckFreeSpace(dir, 1, 0); err != nil {
		t.Errorf("1 byte: %v", err)
	}
	if err := storage.CheckFreeSpace(dir, 1<<62, 1<<62); !errors.Is(err, storage.ErrInsufficientSpace) {
		t.Errorf("huge: %v", err)
	}
	if err := storage.CheckFreeSpace(dir, ^uint64(0), 2); !errors.Is(err, storage.ErrInsufficientSpace) {
		t.Errorf("overflow: %v", err)
	}
	_, err = d.Put(t.Context(), storage.MustParseKey("big"), bytes.NewReader(nil), storage.PutOptions{SizeHint: 1 << 62})
	if !errors.Is(err, storage.ErrInsufficientSpace) {
		t.Errorf("put with huge size hint: %v", err)
	}
}

func TestDiskClosed(t *testing.T) {
	d, err := storage.OpenDisk(storage.DiskConfig{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Errorf("second close: %v", err)
	}
	if err := diskPut(t, d, "a", "x"); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("put after close: %v", err)
	}
	if _, err := d.List(t.Context(), storage.Key{}, storage.ListOptions{}); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("list after close: %v", err)
	}
}
