package setup

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const mountinfoSample = `22 1 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw
600 590 0:52 / /deploy rw,relatime - fakeowner grpcfuse rw
601 590 0:53 / /deploy/secrets/certs/ca rw,nosuid - ext4 /dev/sdb1 rw
602 590 0:54 / /with\040space rw - virtiofs x rw
603 590 0:55 / /linux rw shared:5 master:2 - xfs /dev/sdc rw
`

func TestParseMountinfo(t *testing.T) {
	t.Parallel()
	ms, err := ParseMountinfo(strings.NewReader(mountinfoSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 5 || ms[1] != (Mount{"/deploy", "fakeowner"}) || ms[3].Point != "/with space" || ms[4].FSType != "xfs" {
		t.Fatalf("%+v", ms)
	}
	for _, bad := range []string{"1 2 3\n", "1 2 3 4 5 6 - \n"} {
		if _, err := ParseMountinfo(strings.NewReader(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	m, ok, point := mountOf(ms, "/deploy/secrets/certs/ca")
	if !ok || !point || m.FSType != "ext4" {
		t.Fatalf("CA mount: %+v %v %v", m, ok, point)
	}
	m, _, point = mountOf(ms, "/deploy/secrets")
	if point || m.Point != "/deploy" {
		t.Fatalf("secrets: %+v %v", m, point)
	}
	if m, _, _ := mountOf(ms, "/deployx"); m.Point != "/" {
		t.Fatalf("prefix match without separator: %+v", m)
	}
}

func FuzzParseMountinfo(f *testing.F) {
	f.Add(mountinfoSample)
	f.Add(`1 2 3 4 \0 x - y`)
	f.Fuzz(func(_ *testing.T, s string) {
		ms, err := ParseMountinfo(strings.NewReader(s))
		if err == nil {
			for _, m := range ms {
				_, _, _ = mountOf(ms, m.Point)
			}
		}
	})
}

func TestResolveOwnership(t *testing.T) {
	t.Parallel()
	ms, err := ParseMountinfo(strings.NewReader(mountinfoSample))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		mode OwnershipMode
		dir  string
		euid int
		want OwnershipMode
	}{
		{OwnershipAuto, "/deploy", 0, OwnershipNone},
		{OwnershipAuto, "/linux/bekup", 0, OwnershipChown},
		{OwnershipAuto, "/linux/bekup", 1000, OwnershipPrint},
		{OwnershipChown, "/deploy", 0, OwnershipChown},
		{OwnershipPrint, "/linux", 0, OwnershipPrint},
	} {
		f := &fakeSys{mounts: ms, euid: tc.euid}
		got, why := resolveOwnership(tc.mode, tc.dir, *f.env())
		if got != tc.want || why == "" {
			t.Errorf("%s %s euid %d: %s (%s), want %s", tc.mode, tc.dir, tc.euid, got, why, tc.want)
		}
	}
	if _, err := ParseOwnershipMode("root"); err == nil {
		t.Error("accepted an unknown mode")
	}
}

func openRoot(t *testing.T, dir string) *os.Root {
	t.Helper()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return r
}

func TestApplyOwnershipChmodBeforeChown(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "s"), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"a", "s/k"} {
		if err := os.WriteFile(pathIn(dir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entries := make([]Entry, 0, 5)
	entries = append(entries,
		Entry{Path: "a", Kind: KindFile, Mode: 0o644},
		Entry{Path: "s", Kind: KindDir, Mode: 0o711},
		Entry{Path: "s/k", Kind: KindFile, Mode: 0o400, UID: 65532, GID: 65532},
		Entry{Path: "opt", Kind: KindFile, Mode: 0o644, Optional: true},
	)
	f := &fakeSys{}
	r := openRoot(t, dir)
	if _, err := applyOwnership(r, entries, OwnershipChown, *f.env(), nil); err != nil {
		t.Fatal(err)
	}
	// Owners 0:0 are the fake's euid, so only s/k is chowned.
	want := []string{"chmod a 0644", "chmod s 0711", "chmod s/k 0400", "chown s/k 65532:65532"}
	if strings.Join(f.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls %v, want %v", f.calls, want)
	}
	if fi := mustStat(t, pathIn(dir, "s/k")); fi.Mode().Perm() != 0o400 {
		t.Fatalf("mode %v", fi.Mode())
	}
	// A second run changes nothing: modes and owners already match.
	f.calls = nil
	if _, err := applyOwnership(r, entries, OwnershipChown, *f.env(), nil); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("calls on an applied tree: %v", f.calls)
	}
	// Missing required entries and symlinks are refused.
	if _, err := applyOwnership(r, append(entries, Entry{Path: "missing", Kind: KindFile}), OwnershipNone, *f.env(), nil); err == nil {
		t.Fatal("missing entry accepted")
	}
	if err := os.Symlink("a", filepath.Join(dir, "l")); err != nil {
		t.Fatal(err)
	}
	if _, err := applyOwnership(r, []Entry{{Path: "l", Kind: KindFile}}, OwnershipNone, *f.env(), nil); err == nil {
		t.Fatal("symlink accepted")
	}
}

// A file already handed to another UID cannot be chmod'ed with CAP_CHOWN
// alone: setup takes it over, chmods it and hands it back.
func TestApplyOwnershipChownedFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, f := range []string{"k", "ok"} {
		if err := os.WriteFile(pathIn(dir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entries := []Entry{
		{Path: "k", Kind: KindFile, Mode: 0o400, UID: 65532, GID: 65532},
		{Path: "ok", Kind: KindFile, Mode: 0o600, UID: 70, GID: 70},
	}
	f := &fakeSys{euid: 0}
	f.setOwner("k", 65532, 65532)
	f.setOwner("ok", 70, 70)
	r := openRoot(t, dir)
	if _, err := applyOwnership(r, entries, OwnershipChown, *f.env(), nil); err != nil {
		t.Fatal(err)
	}
	want := []string{"chown k 0:-1", "chmod k 0400", "chown k 65532:65532"}
	if strings.Join(f.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls %v, want %v", f.calls, want)
	}
	if fi := mustStat(t, pathIn(dir, "k")); fi.Mode().Perm() != 0o400 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if uid, gid := f.ownerOf("k"); uid != 65532 || gid != 65532 {
		t.Fatalf("owner %d:%d", uid, gid)
	}
	// Without chown mode the EPERM is reported with a hint.
	if err := os.Chmod(pathIn(dir, "ok"), 0o644); err != nil { //nolint:gosec // G302: the test needs a wrong mode
		t.Fatal(err)
	}
	if _, err := applyOwnership(r, entries, OwnershipPrint, *f.env(), io.Discard); err == nil || !strings.Contains(err.Error(), "belongs to UID 70") {
		t.Fatalf("print mode on a foreign file: %v", err)
	}
}

// The deployment directory itself only loses group/world write bits.
func TestApplyOwnershipRootUpperBound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o777); err != nil { //nolint:gosec // G302: the test needs a wrong mode
		t.Fatal(err)
	}
	f := &fakeSys{}
	entries := []Entry{{Path: ".", Kind: KindDir, Mode: 0o755, ModeIsMax: true}}
	if _, err := applyOwnership(openRoot(t, dir), entries, OwnershipNone, *f.env(), nil); err != nil {
		t.Fatal(err)
	}
	if fi := mustStat(t, dir); fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // G302: a narrower deployment directory
		t.Fatal(err)
	}
	f.calls = nil
	if _, err := applyOwnership(openRoot(t, dir), entries, OwnershipNone, *f.env(), nil); err != nil || len(f.calls) != 0 {
		t.Fatalf("a narrower mode was changed: %v %v", f.calls, err)
	}
}

// Offline CA media without Unix modes/owners (FAT/exFAT) are tolerated with
// a warning; the same errors elsewhere are not.
func TestApplyOwnershipOfflineMedia(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(pathIn(dir, "secrets/certs/ca"), 0o755); err != nil { //nolint:gosec // G301: FAT-like modes
		t.Fatal(err)
	}
	for _, f := range []string{"secrets/certs/ca/ca.key", "secrets/x"} {
		if err := os.WriteFile(pathIn(dir, f), []byte("x"), 0o644); err != nil { //nolint:gosec // G306: FAT-like modes
			t.Fatal(err)
		}
	}
	ca := Entry{Path: "secrets/certs/ca/ca.key", Kind: KindFile, Mode: 0o600, Offline: true, Optional: true}
	caDir := Entry{Path: "secrets/certs/ca", Kind: KindDir, Mode: 0o700, Offline: true, Optional: true}
	mounts := []Mount{{Point: "/", FSType: "ext4"}, {Point: pathIn(dir, "secrets/certs/ca"), FSType: "vfat"}}
	for _, errno := range []error{syscall.EPERM, syscall.ENOTSUP} {
		f := &fakeSys{euid: 0, mounts: mounts, failOn: map[string]error{
			ca.Path:    &fs.PathError{Op: "chmod", Path: ca.Path, Err: errno},
			caDir.Path: &fs.PathError{Op: "chmod", Path: caDir.Path, Err: errno},
		}}
		warnings, err := applyOwnership(openRoot(t, dir), []Entry{caDir, ca}, OwnershipChown, *f.env(), nil)
		if err != nil || len(warnings) != 2 || !strings.Contains(warnings[0], "POSIX file system") {
			t.Fatalf("%v: warnings %q, err %v", errno, warnings, err)
		}
		// Not on a separate mount: an error.
		f.mounts = mounts[:1]
		if _, err := applyOwnership(openRoot(t, dir), []Entry{ca}, OwnershipChown, *f.env(), nil); err == nil {
			t.Fatalf("%v accepted without the offline mount", errno)
		}
		// Not an offline entry: an error.
		f.mounts = mounts
		f.failOn["secrets/x"] = &fs.PathError{Op: "chmod", Path: "secrets/x", Err: errno}
		if _, err := applyOwnership(openRoot(t, dir), []Entry{{Path: "secrets/x", Kind: KindFile, Mode: 0o400}}, OwnershipChown, *f.env(), nil); err == nil {
			t.Fatalf("%v accepted for an online entry", errno)
		}
	}
}

func TestPrintChownGolden(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	entries := Manifest(Layout{KeysDir: "secrets"}, []string{"deploy/postgres/entrypoint.sh"}, ManifestOptions{})
	if err := printChown(&b, entries); err != nil {
		t.Fatal(err)
	}
	const want = `Run as root in the deployment directory on the host:
  chown -h 0:0 -- . compose.yaml bekup-setup.json .env bekup.toml runner.toml
  chown -h 0:0 -- compose.override.yaml reference reference/bekup.example.toml reference/runner.example.toml deploy deploy/postgres
  chown -h 0:0 -- deploy/postgres/entrypoint.sh secrets secrets/certs secrets/certs/serve secrets/certs/serve/serve.crt secrets/certs/serve/ca.crt
  chown -h 0:0 -- secrets/certs/runner secrets/certs/runner/runner.crt secrets/certs/runner/ca.crt secrets/certs/postgres secrets/certs/postgres/postgres.crt secrets/certs/postgres/ca.crt
  chown -h 0:0 -- secrets/certs/ca secrets/certs/ca/ca.crt secrets/certs/ca/ca.key
  chown -h 65532:65532 -- secrets/bekup_master_key secrets/bekup_database_url secrets/bekup_database_owner_url secrets/certs/serve/serve.key secrets/certs/runner/runner.key
  chown -h 70:70 -- secrets/postgres_password secrets/bekup_owner_password secrets/bekup_app_password secrets/certs/postgres/postgres.key
`
	if b.String() != want {
		t.Fatalf("got:\n%s", b.String())
	}
}

// L2: ownership changes go through a descriptor opened without following
// symlinks, with the entry type re-checked on the descriptor.
func TestEntryFDRefusesSymlinksAndTypeChanges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "target"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := entryChmod(r, "link", false, 0o644); err == nil {
		t.Fatal("chmod followed a symlink")
	}
	if fi, err := os.Stat(filepath.Join(dir, "target")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("symlink target changed: %v %v", fi.Mode(), err)
	}
	if err := entryChmod(r, "target", true, 0o700); err == nil {
		t.Fatal("a file was accepted as a directory")
	}
	if err := entryChmod(r, "d", false, 0o700); err == nil {
		t.Fatal("a directory was accepted as a file")
	}
	if err := entryChmod(r, "target", false, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := entryChmod(r, "d", true, 0o750); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "d")); err != nil || fi.Mode().Perm() != 0o750 {
		t.Fatalf("dir mode %v %v", fi.Mode(), err)
	}
	if err := entryChown(r, "link", false, os.Getuid(), os.Getgid()); err == nil {
		t.Fatal("chown followed a symlink")
	}
	if err := entryChown(r, "target", false, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	// A hard link (Nlink != 1) could be any file of the host on the same
	// file system: refused on the descriptor.
	if err := os.Link(filepath.Join(dir, "target"), filepath.Join(dir, "hard")); err != nil {
		t.Fatal(err)
	}
	if err := entryChmod(r, "hard", false, 0o600); err == nil || !strings.Contains(err.Error(), "hard links") {
		t.Fatalf("hard link accepted: %v", err)
	}
	if err := entryChown(r, "target", false, os.Getuid(), os.Getgid()); err == nil {
		t.Fatal("chown of a file with two links")
	}
}

// TestApplyOwnershipRealChown runs the real descriptor-based chmod/chown
// as root (in CI's setup container: CAP_CHOWN only), twice: the second run
// re-applies the manifest to an already chowned tree (setup check
// --fix-ownership).
func TestApplyOwnershipRealChown(t *testing.T) {
	t.Parallel()
	if os.Geteuid() != 0 {
		t.Skip("needs root (CAP_CHOWN)")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil { //nolint:gosec // G302: the deployment directory is 0755 (manifest)
		t.Fatal(err)
	}
	a := testAnswers(t, ProxyTraefik)
	mustInit(t, dir, a)
	st := mustState(t, dir)
	var installed []string
	for _, p := range st.assetPaths() {
		if strings.HasPrefix(p, DeployDir+"/") {
			installed = append(installed, p)
		}
	}
	entries := Manifest(Layout{KeysDir: st.KeysDir}, installed, st.manifestOptions())
	env := defaultSysEnv()
	env.mountinfo = func() ([]Mount, error) { return nil, nil }
	for run := range 2 {
		r, err := os.OpenRoot(dir)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := applyOwnership(r, entries, OwnershipChown, env, io.Discard); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range entries {
		fi, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(e.Path)))
		if err != nil {
			if e.Optional {
				continue
			}
			t.Fatal(err)
		}
		if uid, gid, ok := fileOwner(fi); ok && (uid != e.UID || gid != e.GID) {
			t.Errorf("%s owner %d:%d, want %d:%d", e.Path, uid, gid, e.UID, e.GID)
		}
		if !e.ModeIsMax && fi.Mode().Perm() != e.Mode {
			t.Errorf("%s mode %04o, want %04o", e.Path, fi.Mode().Perm(), e.Mode)
		}
	}
}
