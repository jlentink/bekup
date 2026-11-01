package setup

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jlentink/bekup"
	"github.com/jlentink/bekup/internal/imageref"
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

// TestUpgradeLegacyVariant (D-215): a deployment recorded with the retired
// npm or traefik-external variant upgrades to own-proxy in link mode: the
// operator replaced compose.yaml with compose.own-proxy.yaml, upgrade
// writes the link (and label) overlay into a new compose.override.yaml and
// rewrites the state; check then passes. Before the upgrade check names
// the retired variant.
func TestUpgradeLegacyVariant(t *testing.T) {
	t.Parallel()
	for _, legacy := range []Variant{legacyVariantNPM, legacyVariantTraefikExternal} {
		t.Run(string(legacy), func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeRelease(t, dir, VariantOwnProxy, variantImage)
			labels := legacy == legacyVariantTraefikExternal
			if _, err := composeFirstInit(t, dir, VariantOwnProxy, func(o *Options) {
				o.Answers.ProxyNetwork, o.Answers.ProxyContainer, o.Answers.ProxyMode, o.Answers.ProxyLabels = "", "", ProxyLink, labels
			}); err != nil {
				t.Fatal(err)
			}
			// What the earlier version recorded: the old variant name, no
			// proxy_network_mode / proxy_labels, no override.
			raw := string(readFile(t, dir, StateFileName))
			raw = strings.Replace(raw, `"variant": "own-proxy"`, `"variant": "`+string(legacy)+`"`, 1)
			raw = strings.Replace(raw, "  \"proxy_network_mode\": \"link\",\n", "", 1)
			raw = strings.Replace(raw, "  \"proxy_labels\": true,\n", "", 1)
			if strings.Contains(raw, "proxy_network_mode") || strings.Contains(raw, "proxy_labels") {
				t.Fatalf("state edit failed:\n%s", raw)
			}
			if err := os.WriteFile(pathIn(dir, StateFileName), []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(pathIn(dir, OverrideFile)); err != nil {
				t.Fatal(err)
			}
			if findings, report, err := runCheck(t, dir, nil); !errors.Is(err, ErrCheckFailed) || !hasFinding(findings, SeverityError, "retired "+string(legacy)+" variant") {
				t.Fatalf("check before the upgrade:\n%s", report)
			}
			o := upgradeOptions(t, dir, variantImage)
			o.WriteCompose, o.Image = false, imageref.Ref{}
			if err := Upgrade(o); err != nil {
				t.Fatalf("upgrade: %v", err)
			}
			if out := bufOf(t, o.Out).String(); !strings.Contains(out, "NOTICE: the "+string(legacy)+" variant was retired") {
				t.Fatalf("no notice:\n%s", out)
			}
			ov := Overlays{ProxyLink: true, ProxyLabels: labels}
			if got := string(readFile(t, dir, OverrideFile)); got != overlayHeader+overlayDoc(ov, "") {
				t.Fatalf("override:\n%s", got)
			}
			st := mustState(t, dir)
			if st.Variant != VariantOwnProxy || st.ProxyNetworkMode != ProxyLink || st.ProxyLabels != labels || st.Legacy() != "" {
				t.Fatalf("state %+v", st)
			}
			if findings, report, err := runCheck(t, dir, nil); err != nil || hasErrors(findings) {
				t.Fatalf("check after the upgrade: %v\n%s", err, report)
			}
		})
	}
}

// TestOverrideNeverRewritten (D-215 review L1, L7): setup never rewrites
// an existing compose.override.yaml. `init --force` (setup-first) keeps it
// and requires the overlay blocks in it; a setup-first upgrade of a
// retired variant checks an existing override for the link (and label)
// blocks instead of skipping it.
func TestOverrideNeverRewritten(t *testing.T) {
	t.Parallel()
	t.Run("init force", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		a := testAnswers(t, ProxyNone)
		ownProxyJoin(&a)
		a.ProxyLabels = true
		mustInit(t, dir, a)
		ov := Overlays{ProxyLabels: true}
		mine := overlayHeader + overlayDoc(ov, "") + "# mine\n"
		if err := os.WriteFile(pathIn(dir, OverrideFile), []byte(mine), 0o600); err != nil {
			t.Fatal(err)
		}
		o := testOptions(t, dir, a)
		o.Force = true
		if err := Init(o); err != nil {
			t.Fatalf("init --force: %v", err)
		}
		if got := string(readFile(t, dir, OverrideFile)); got != mine {
			t.Fatalf("override rewritten:\n%s", got)
		}
		if out := bufOf(t, o.Out).String(); !strings.Contains(out, "exists and was kept") {
			t.Fatalf("no note:\n%s", out)
		}
		if err := os.WriteFile(pathIn(dir, OverrideFile), []byte("services: {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		o = testOptions(t, dir, a)
		o.Force = true
		if err := Init(o); err == nil || !strings.Contains(err.Error(), "add these blocks") || !strings.Contains(err.Error(), "traefik.enable") {
			t.Fatalf("override without the blocks: %v", err)
		}
		if got := string(readFile(t, dir, OverrideFile)); got != "services: {}\n" {
			t.Fatalf("override touched:\n%s", got)
		}
	})
	t.Run("setup-first legacy upgrade", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		a := testAnswers(t, ProxyNone)
		ownProxyLink(&a)
		a.ProxyLabels = true
		mustInit(t, dir, a)
		raw := string(readFile(t, dir, StateFileName))
		raw = strings.Replace(raw, `"variant": "own-proxy"`, `"variant": "`+string(legacyVariantTraefikExternal)+`"`, 1)
		raw = strings.Replace(raw, "  \"proxy_network_mode\": \"link\",\n", "", 1)
		raw = strings.Replace(raw, "  \"proxy_labels\": true,\n", "", 1)
		if err := os.WriteFile(pathIn(dir, StateFileName), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if findings, report, err := runCheck(t, dir, nil); !errors.Is(err, ErrCheckFailed) ||
			!hasFinding(findings, SeverityError, "docker compose run --rm setup upgrade --write-compose --image <image>") {
			t.Fatalf("check hint (L6):\n%s", report)
		}
		if err := os.WriteFile(pathIn(dir, OverrideFile), []byte("services: {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		img := a.Image.String()
		if err := Upgrade(upgradeOptions(t, dir, img)); err == nil || !strings.Contains(err.Error(), "add these blocks") ||
			!strings.Contains(err.Error(), "retired traefik-external") {
			t.Fatalf("upgrade with an override without the blocks: %v", err)
		}
		ov := Overlays{ProxyLink: true, ProxyLabels: true}
		want := overlayHeader + overlayDoc(ov, "")
		if err := os.WriteFile(pathIn(dir, OverrideFile), []byte(want), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := Upgrade(upgradeOptions(t, dir, img)); err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		if got := string(readFile(t, dir, OverrideFile)); got != want {
			t.Fatalf("override rewritten:\n%s", got)
		}
		if st := mustState(t, dir); st.Variant != VariantOwnProxy || st.ProxyNetworkMode != ProxyLink || !st.ProxyLabels {
			t.Fatalf("state %+v", st)
		}
	})
}
