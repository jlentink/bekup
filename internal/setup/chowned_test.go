package setup

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// chownedSys returns a fake root (CAP_CHOWN only: chmod of foreign files
// fails) on which every manifest entry of dir already belongs to its
// container user, as after `setup init` with --ownership chown.
func chownedSys(t *testing.T, dir string) *fakeSys {
	t.Helper()
	st := mustState(t, dir)
	var installed []string
	for _, p := range st.assetPaths() {
		if strings.HasPrefix(p, DeployDir+"/") {
			installed = append(installed, p)
		}
	}
	f := &fakeSys{euid: 0}
	for _, e := range Manifest(Layout{KeysDir: st.KeysDir}, installed, st.manifestOptions()) {
		if e.UID != 0 || e.GID != 0 {
			f.setOwner(e.Path, e.UID, e.GID)
		}
	}
	return f
}

// Regression: re-applying the manifest to a tree whose secrets already
// belong to 65532/70 must not chmod them (EPERM without CAP_FOWNER).
func TestOwnershipOnChownedTree(t *testing.T) {
	a := testAnswers(t, ProxyTraefik)
	t.Run("upgrade", func(t *testing.T) {
		dir := t.TempDir()
		mustInit(t, dir, a)
		f := chownedSys(t, dir)
		o := upgradeOptions(t, dir, a.Image.String())
		o.Ownership, o.sys = OwnershipChown, f.env()
		if err := Upgrade(o); err != nil {
			t.Fatalf("upgrade on a chowned tree: %v", err)
		}
		for _, c := range f.calls {
			if strings.HasPrefix(c, "chmod secrets/") {
				t.Fatalf("chmod of an applied secret: %v", f.calls)
			}
		}
	})
	t.Run("upgrade with a drifted mode", func(t *testing.T) {
		dir := t.TempDir()
		mustInit(t, dir, a)
		if err := os.Chmod(pathIn(dir, "secrets/bekup_master_key"), 0o600); err != nil {
			t.Fatal(err)
		}
		f := chownedSys(t, dir)
		o := upgradeOptions(t, dir, a.Image.String())
		o.Ownership, o.sys = OwnershipChown, f.env()
		if err := Upgrade(o); err != nil {
			t.Fatal(err)
		}
		if fi := mustStat(t, pathIn(dir, "secrets/bekup_master_key")); fi.Mode().Perm() != 0o400 {
			t.Fatalf("mode %v", fi.Mode())
		}
		if uid, gid := f.ownerOf("secrets/bekup_master_key"); uid != UIDBekup || gid != UIDBekup {
			t.Fatalf("owner %d:%d", uid, gid)
		}
	})
	t.Run("check --fix-ownership", func(t *testing.T) {
		dir := t.TempDir()
		mustInit(t, dir, a)
		if err := os.Chmod(pathIn(dir, "secrets/certs/postgres/postgres.key"), 0o440); err != nil { //nolint:gosec // G302: the test needs a wrong mode
			t.Fatal(err)
		}
		f := chownedSys(t, dir)
		_, out, err := runCheck(t, dir, func(o *CheckOptions) {
			o.FixOwnership, o.Ownership, o.sys = true, OwnershipChown, f.env()
		})
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if uid, _ := f.ownerOf("secrets/certs/postgres/postgres.key"); uid != UIDPostgres {
			t.Fatalf("owner %d", uid)
		}
	})
	for _, flag := range []string{"force", "keep-existing"} {
		t.Run("init --"+flag, func(t *testing.T) {
			dir := t.TempDir()
			mustInit(t, dir, a)
			f := chownedSys(t, dir)
			o := testOptions(t, dir, a)
			o.Ownership, o.sys = OwnershipChown, f.env()
			o.Force, o.KeepExisting = flag == "force", flag == "keep-existing"
			if err := Init(o); err != nil {
				t.Fatalf("init --%s on a chowned tree: %v", flag, err)
			}
		})
	}
}

// The deployment directory must belong to root and not be group/world
// writable.
func TestCheckDeploymentRoot(t *testing.T) {
	dir := t.TempDir()
	mustInit(t, dir, testAnswers(t, ProxyTraefik))
	if err := os.Chmod(dir, 0o777); err != nil { //nolint:gosec // G302: the test needs a wrong mode
		t.Fatal(err)
	}
	fs, out, err := runCheck(t, dir, nil)
	if !errors.Is(err, ErrCheckFailed) || !hasFinding(fs, SeverityError, ".: mode 0777 is group- or world-writable") {
		t.Fatalf("writable root not reported:\n%s", out)
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // G302: a narrower deployment directory
		t.Fatal(err)
	}
	f := chownedSys(t, dir)
	f.setOwner(".", 1000, 1000)
	fs, out, err = runCheck(t, dir, func(o *CheckOptions) { o.Ownership, o.sys = OwnershipChown, f.env() })
	if !errors.Is(err, ErrCheckFailed) || !hasFinding(fs, SeverityError, "the deployment directory belongs to 1000:1000") || hasFinding(fs, SeverityError, ".: mode") {
		t.Fatalf("foreign root owner:\n%s", out)
	}
}

// Init warns about a writable deployment directory and narrows it.
func TestInitWarnsWritableRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o777); err != nil { //nolint:gosec // G302: the test needs a wrong mode
		t.Fatal(err)
	}
	out := mustInit(t, dir, testAnswers(t, ProxyTraefik)).String()
	if !strings.Contains(out, "WARNING: the deployment directory") {
		t.Fatalf("no warning:\n%s", out)
	}
	if fi := mustStat(t, dir); fi.Mode().Perm()&0o022 != 0 {
		t.Fatalf("mode %v", fi.Mode())
	}
}

// An offline CA on its own mount (FAT/exFAT) only warns about its modes.
func TestCheckOfflineMediaModes(t *testing.T) {
	dir := t.TempDir()
	mustInit(t, dir, testAnswers(t, ProxyTraefik))
	if err := os.Chmod(pathIn(dir, "secrets/certs/ca/ca.key"), 0o755); err != nil { //nolint:gosec // G302: FAT-like mode
		t.Fatal(err)
	}
	f := &fakeSys{mounts: []Mount{{Point: "/", FSType: "ext4"}, {Point: pathIn(dir, "secrets/certs/ca"), FSType: "exfat"}}}
	fs, out, err := runCheck(t, dir, func(o *CheckOptions) { o.sys = f.env() })
	if err != nil || !hasFinding(fs, SeverityWarning, "ca.key: mode 0755, want 0600 (on the offline CA media") {
		t.Fatalf("%v\n%s", err, out)
	}
}
