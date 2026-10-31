package setup

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jlentink/bekup"
)

func upgradeOptions(t *testing.T, dir, image string) UpgradeOptions {
	t.Helper()
	return UpgradeOptions{
		Dir: dir, Version: "1.2.3", Image: mustRef(t, image), Ownership: OwnershipNone, WriteCompose: true,
		Out: &bytes.Buffer{}, Now: func() time.Time { return time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC) },
		sys: (&fakeSys{}).env(), certsInit: fastCerts,
	}
}

// onlyChanged returns the snapshot differences outside the given paths.
func onlyChanged(before, after map[string]string, allowed ...string) []string {
	var bad []string
	for _, d := range diffSnapshots(before, after) {
		ok := false
		for _, a := range allowed {
			if strings.HasSuffix(d, " "+a) || strings.Contains(d, " "+a+".bak-") {
				ok = true
			}
		}
		if !ok {
			bad = append(bad, d)
		}
	}
	return bad
}

func TestUpgradeSameImageNoChanges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := testAnswers(t, ProxyTraefik)
	mustInit(t, dir, a)
	before := snapshot(t, dir)
	o := upgradeOptions(t, dir, a.Image.String())
	if err := Upgrade(o); err != nil {
		t.Fatal(err)
	}
	if d := diffSnapshots(before, snapshot(t, dir)); len(d) > 0 {
		t.Fatalf("changes: %v", d)
	}
	if !strings.Contains(bufOf(t, o.Out).String(), "no changes") {
		t.Fatalf("output: %s", o.Out)
	}
}

func TestUpgradeNewImage(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := testAnswers(t, ProxyTraefik)
	mustInit(t, dir, a)
	before := snapshot(t, dir)
	next := "ghcr.io/jlentink/bekup:1.2.3@" + testDigest2
	o := upgradeOptions(t, dir, next)
	if err := Upgrade(o); err != nil {
		t.Fatal(err)
	}
	if bad := onlyChanged(before, snapshot(t, dir), ComposeFile, StateFileName); len(bad) > 0 {
		t.Fatalf("unexpected changes: %v", bad)
	}
	if !hasLine(string(readFile(t, dir, ComposeFile)), "x-bekup-image: &bekup-image "+next) {
		t.Fatal("compose not updated")
	}
	out := bufOf(t, o.Out).String()
	if !strings.Contains(out, "+ x-bekup-image: &bekup-image "+next) || !strings.Contains(out, "- x-bekup-image: &bekup-image "+a.Image.String()) {
		t.Fatalf("no compose diff in the output:\n%s", out)
	}
	if st := mustState(t, dir); st.Image != next {
		t.Fatal("state not updated")
	}
}

func TestUpgradeLocalEdits(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := testAnswers(t, ProxyTraefik)
	mustInit(t, dir, a)
	edited := pathIn(dir, "deploy/traefik/dynamic/tls.yml")
	if err := os.WriteFile(edited, []byte("# mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, dir)
	o := upgradeOptions(t, dir, "ghcr.io/jlentink/bekup:1.2.3@"+testDigest2)
	if err := Upgrade(o); err == nil || !strings.Contains(err.Error(), "deploy/traefik/dynamic/tls.yml") {
		t.Fatalf("edit not detected: %v", err)
	}
	if d := diffSnapshots(before, snapshot(t, dir)); len(d) > 0 {
		t.Fatalf("refused upgrade changed files: %v", d)
	}
	o.Force = true
	if err := Upgrade(o); err != nil {
		t.Fatal(err)
	}
	bak := pathIn(dir, "deploy/traefik/dynamic/tls.yml.bak-20261101T080000Z")
	if b, err := os.ReadFile(filepath.Clean(bak)); err != nil || string(b) != "# mine\n" {
		t.Fatalf("backup: %q %v", b, err)
	}
	if string(readFile(t, dir, "deploy/traefik/dynamic/tls.yml")) == "# mine\n" {
		t.Fatal("edit not replaced")
	}
}

func TestUpgradeDowngradeAndInvalidConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := testAnswers(t, ProxyTraefik)
	mustInit(t, dir, a)
	o := upgradeOptions(t, dir, a.Image.String())
	o.Version, o.AllowVersionMismatch = "1.2.2", true
	if err := Upgrade(o); err == nil || !strings.Contains(err.Error(), "older") {
		t.Fatalf("downgrade: %v", err)
	}
	o.AllowDowngrade = true
	if err := Upgrade(o); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(pathIn(dir, BekupTOML), []byte("[server]\nbogus_key = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, dir)
	o = upgradeOptions(t, dir, "ghcr.io/jlentink/bekup:1.2.3@"+testDigest2)
	if err := Upgrade(o); err == nil || !strings.Contains(err.Error(), "fix the configuration") {
		t.Fatalf("invalid TOML: %v", err)
	}
	if d := diffSnapshots(before, snapshot(t, dir)); len(d) > 0 {
		t.Fatalf("changes: %v", d)
	}
	if err := Upgrade(UpgradeOptions{Dir: t.TempDir(), Version: "1.2.3", Image: a.Image}); err == nil || !strings.Contains(err.Error(), StateFileName) {
		t.Fatalf("no state: %v", err)
	}
}

// Stale assets are removed, new ones added, secrets and operator files
// untouched.
func TestUpgradeStaleAssets(t *testing.T) {
	t.Parallel()
	base := bekup.Assets()
	withExtra := fstest.MapFS{}
	err := fs.WalkDir(base, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(base, p)
		withExtra[p] = &fstest.MapFile{Data: b}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	withExtra["deploy/nginx/old.conf"] = &fstest.MapFile{Data: []byte("old\n")}

	dir := t.TempDir()
	io := testOptions(t, dir, testAnswers(t, ProxyNginx))
	io.assets = withExtra
	if err := Init(io); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, dir)
	o := upgradeOptions(t, dir, "ghcr.io/jlentink/bekup:1.2.3@"+testDigest)
	if err := Upgrade(o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pathIn(dir, "deploy/nginx/old.conf")); !os.IsNotExist(err) {
		t.Fatal("stale asset kept")
	}
	if bad := onlyChanged(before, snapshot(t, dir), "deploy/nginx/old.conf", StateFileName); len(bad) > 0 {
		t.Fatalf("unexpected: %v", bad)
	}
}

// A secret group that is new in the manifest is generated; existing ones
// are never touched.
func TestUpgradeGeneratesNewSecretGroups(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := testAnswers(t, ProxyTraefik)
	mustInit(t, dir, a)
	st := mustState(t, dir)
	st.SecretGroups = []string{groupCerts, groupDatabase}
	saveState(t, dir, st)
	if err := os.Remove(pathIn(dir, "secrets/bekup_master_key")); err != nil {
		t.Fatal(err)
	}
	db := readFile(t, dir, "secrets/bekup_database_url")
	if err := Upgrade(upgradeOptions(t, dir, a.Image.String())); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pathIn(dir, "secrets/bekup_master_key")); err != nil {
		t.Fatal("new group not generated")
	}
	if !bytes.Equal(db, readFile(t, dir, "secrets/bekup_database_url")) {
		t.Fatal("existing secret changed")
	}
}
