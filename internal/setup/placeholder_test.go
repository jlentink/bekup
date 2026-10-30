package setup

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jlentink/bekup"
)

// The committed compose files (deploy/compose/, D-205 amendment 3) and
// the placeholder mode: compose.yaml takes the image from BEKUP_IMAGE.

const committedDir = "deploy/compose"

// TestCommittedComposeFiles is the drift test: the committed files are
// exactly what `just gen` renders from docker-compose.yml, they differ from
// the release assets only in the x-bekup-image line, and they are neither
// embedded nor shipped as a deploy asset.
func TestCommittedComposeFiles(t *testing.T) {
	root := repoRoot(t)
	img := mustRef(t, variantImage)
	entries, err := os.ReadDir(filepath.Join(root, committedDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(ReleaseVariants) {
		t.Errorf("%s holds %d entries, want exactly the %d variant files", committedDir, len(entries), len(ReleaseVariants))
	}
	for _, v := range ReleaseVariants {
		want, err := RenderRelease(v, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := readFile(t, root, committedDir+"/"+v.AssetName())
		if !bytes.Equal(got, want) {
			t.Errorf("%s/%s is out of date (or was edited by hand): run `just gen`\n%s", committedDir, v.AssetName(), LineDiff(string(got), string(want), 20))
			continue
		}
		if !strings.Contains(string(got), "GENERATED from the repository's docker-compose.yml by `just gen`; never\n# edit it by hand.") {
			t.Errorf("%s: no generated-file header", v.AssetName())
		}
		if !hasLine(string(got), ComposePlaceholderLine) {
			t.Errorf("%s: no placeholder line", v.AssetName())
		}
		rel, err := RenderRelease(v, &img)
		if err != nil {
			t.Fatal(err)
		}
		gl, rl := strings.Split(string(got), "\n"), strings.Split(string(rel), "\n")
		if len(gl) != len(rl) {
			t.Fatalf("%s: %d lines, release file %d", v.AssetName(), len(gl), len(rl))
		}
		for i := range gl {
			if gl[i] != rl[i] && (gl[i] != ComposePlaceholderLine || rl[i] != composeImagePrefix+variantImage) {
				t.Errorf("%s line %d differs from the release file beyond the image line", v.AssetName(), i+1)
			}
		}
	}
	// Not embedded, not a deploy asset of any variant, not in the image.
	if _, err := fs.Stat(bekup.Assets(), committedDir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s is embedded (%v)", committedDir, err)
	}
	for _, v := range AllVariants {
		for _, d := range v.assetDirs() {
			if strings.HasPrefix(committedDir, d) || strings.HasPrefix(d, committedDir) {
				t.Errorf("variant %s ships %s", v, d)
			}
		}
	}
	if isAssetPath(committedDir + "/compose.yaml") {
		t.Error("deploy/compose/compose.yaml counts as a managed asset path")
	}
	if !hasLine(string(readFile(t, root, ".dockerignore")), committedDir+"/") {
		t.Errorf(".dockerignore does not exclude %s/", committedDir)
	}
	// The release workflow compares the release assets with the committed
	// files and needs the same placeholder line.
	if !strings.Contains(string(readFile(t, root, "scripts/release_ci.sh")), "placeholder='"+ComposePlaceholderLine+"'") {
		t.Error("scripts/release_ci.sh does not carry ComposePlaceholderLine")
	}
}

// writePlaceholder saves the committed file of v as compose.yaml.
func writePlaceholder(t *testing.T, dir string, v Variant) []byte {
	t.Helper()
	data := readFile(t, repoRoot(t), committedDir+"/"+v.AssetName())
	if err := os.WriteFile(pathIn(dir, ComposeFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(pathIn(dir, ComposeFile), 0o644); err != nil { //nolint:gosec // G302: compose.yaml is public (manifest mode 0644)
		t.Fatal(err)
	}
	return data
}

func TestComposeImagePlaceholder(t *testing.T) {
	src, err := RenderRelease(VariantNPM, nil)
	if err != nil {
		t.Fatal(err)
	}
	img, placeholder, err := composeImage(src)
	if err != nil || !placeholder || img.Digest != "" {
		t.Fatalf("placeholder: %+v %v %v", img, placeholder, err)
	}
	s := string(src)
	for name, bad := range map[string]string{
		"other default":  strings.Replace(s, ComposeImagePlaceholder, "${BEKUP_IMAGE:-ghcr.io/x/bekup:1@"+testDigest+"}", 1),
		"other message":  strings.Replace(s, ComposeImagePlaceholder, "${BEKUP_IMAGE:?set it}", 1),
		"other variable": strings.Replace(s, ComposeImagePlaceholder, strings.Replace(ComposeImagePlaceholder, "${BEKUP_IMAGE", "${IMG", 1), 1),
		"tagged":         strings.Replace(s, ComposeImagePlaceholder, "!!str "+ComposeImagePlaceholder, 1),
		"quoted":         strings.Replace(s, ComposeImagePlaceholder, `"`+ComposeImagePlaceholder+`"`, 1),
		"dev default":    strings.Replace(s, ComposeImagePlaceholder, ComposeImageVar, 1),
	} {
		if bad == s {
			t.Fatalf("%s: edit did not apply", name)
		}
		if _, _, err := composeImage([]byte(bad)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestInspectComposePlaceholder(t *testing.T) {
	fsys := bekup.Assets()
	src, err := RenderRelease(VariantTraefikExternal, nil)
	if err != nil {
		t.Fatal(err)
	}
	dotenv := map[string]string{ImageEnvVar: variantImage}
	info, err := inspectComposeData(fsys, composeInput{compose: src, dotenv: dotenv}, "")
	if err != nil || !info.FromEnv || info.Level != RecognizedExact || info.Variant != VariantTraefikExternal || info.Image.String() != variantImage || len(info.Findings) > 0 {
		t.Fatalf("exact: %+v %v", info, err)
	}
	// The environment (the setup container's BEKUP_IMAGE) beats .env.
	other := "ghcr.io/jlentink/bekup:1.2.4@" + testDigest2
	lookup := func(k string) (string, bool) {
		if k == ImageEnvVar {
			return other, true
		}
		return "", false
	}
	if info, err := inspectComposeData(fsys, composeInput{compose: src, dotenv: dotenv, lookup: lookup}, ""); err != nil || info.Image.String() != other || len(info.Findings) > 0 {
		t.Fatalf("environment: %+v %v", info, err)
	}
	// Formatting only: model.
	reformatted := strings.Replace(string(src), "cap_drop: [ALL]", "cap_drop:\n      - ALL", 1)
	if info, err := inspectComposeData(fsys, composeInput{compose: []byte(reformatted), dotenv: dotenv}, ""); err != nil || info.Level != RecognizedModel || !info.FromEnv {
		t.Fatalf("model: %+v %v", info, err)
	}
	// Missing or unpinned BEKUP_IMAGE.
	if _, err := inspectComposeData(fsys, composeInput{compose: src}, ""); err == nil || !strings.Contains(err.Error(), "it is not set") {
		t.Fatalf("missing: %v", err)
	}
	for _, bad := range []string{"ghcr.io/jlentink/bekup:1.2.3", "bekup:dev", "ghcr.io/jlentink/bekup@sha256:12"} {
		_, err := inspectComposeData(fsys, composeInput{compose: src, dotenv: map[string]string{ImageEnvVar: bad}}, "")
		if err == nil || !strings.Contains(err.Error(), "not digest-pinned") || !strings.Contains(err.Error(), "RepoDigests") {
			t.Errorf("unpinned %q: %v", bad, err)
		}
	}
	// BEKUP_IMAGE is allowed only in the placeholder line of compose.yaml.
	custom := strings.Replace(string(src), "\nname: bekup\n", "\nname: bekup\nx-extra: ${BEKUP_IMAGE}\n", 1)
	if custom == string(src) {
		t.Fatal("edit did not apply")
	}
	info, err = inspectComposeData(fsys, composeInput{compose: []byte(custom), dotenv: dotenv}, VariantTraefikExternal)
	if err != nil || !hasFinding(info.Findings, SeverityError, "${BEKUP_IMAGE} is not one of bekup's compose variables") {
		t.Fatalf("extra reference: %+v %v", info, err)
	}
	override := []byte("services:\n  serve:\n    image: ${BEKUP_IMAGE}\n")
	info, err = inspectComposeData(fsys, composeInput{compose: src, override: override, dotenv: dotenv}, "")
	if err != nil || !hasFinding(info.Findings, SeverityError, "compose.override.yaml: ${BEKUP_IMAGE} is not one of bekup's compose variables") {
		t.Fatalf("override reference: %+v %v", info, err)
	}
	// The release file (literal) keeps working and is not FromEnv.
	img := mustRef(t, variantImage)
	rel, err := RenderRelease(VariantTraefikExternal, &img)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := inspectComposeData(fsys, composeInput{compose: rel, dotenv: map[string]string{ImageEnvVar: other}}, ""); err != nil || info.FromEnv || info.Image.String() != variantImage || info.Level != RecognizedExact {
		t.Fatalf("literal: %+v %v", info, err)
	}
}

func TestSetEnvLine(t *testing.T) {
	for _, tc := range []struct{ in, value, want string }{
		{"", "x", "BEKUP_IMAGE=x\n"},
		{"A=1\n", "x", "A=1\nBEKUP_IMAGE=x\n"},
		{"A=1", "x", "A=1\nBEKUP_IMAGE=x\n"},
		{"# c\nBEKUP_IMAGE=old\nB=2\nBEKUP_IMAGE=dup\n", "x", "# c\nBEKUP_IMAGE=x\nB=2\n"},
		{"A=1\nBEKUP_IMAGE=old\n", "", "A=1\n"},
		{"A=1\n", "", "A=1\n"},
	} {
		got, err := setEnvImageLine([]byte(tc.in), tc.value)
		if err != nil || string(got) != tc.want {
			t.Errorf("setEnvImageLine(%q, %q) = %q, %v; want %q", tc.in, tc.value, got, err, tc.want)
		}
	}
	if _, err := setEnvImageLine(nil, "a b"); err == nil {
		t.Error("value with a space accepted")
	}
}

func TestInitPlaceholder(t *testing.T) {
	for _, v := range ReleaseVariants {
		t.Run(string(v), func(t *testing.T) {
			t.Setenv(ImageEnvVar, variantImage)
			dir := t.TempDir()
			compose := writePlaceholder(t, dir, v)
			out, err := composeFirstInit(t, dir, v, nil)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if !bytes.Equal(readFile(t, dir, ComposeFile), compose) {
				t.Fatal("compose.yaml was changed")
			}
			st := mustState(t, dir)
			if st.ImageSource != ImageSourceEnv || st.Image != variantImage || st.ComposeMode != ComposeOperator || st.Variant != v {
				t.Fatalf("state %+v", st)
			}
			env := string(readFile(t, dir, EnvFile))
			if !strings.HasPrefix(strings.SplitN(env, "\nBEKUP_IMAGE=", 2)[0], "#") || !hasLine(env, ImageEnvVar+"="+variantImage) || strings.Count(env, ImageEnvVar+"=") != 1 {
				t.Fatalf(".env:\n%s", env)
			}
			// check: clean with .env only and with the same value in the
			// environment (the setup service passes the resolved value).
			for name, lookup := range map[string]func(string) (string, bool){
				"no env":   func(string) (string, bool) { return "", false },
				"same env": func(k string) (string, bool) { return variantImage, k == ImageEnvVar },
			} {
				findings, report, err := runCheck(t, dir, func(o *CheckOptions) { o.LookupEnv = lookup })
				if err != nil {
					t.Fatalf("%s: %v\n%s", name, err, report)
				}
				for _, f := range findings {
					if !strings.Contains(f.Msg, "CA private key") {
						t.Errorf("%s: unexpected finding %s", name, f)
					}
				}
			}
		})
	}
}

func TestInitPlaceholderRefusals(t *testing.T) {
	dir := t.TempDir()
	writePlaceholder(t, dir, VariantNPM)
	t.Setenv(ImageEnvVar, "")
	if _, err := composeFirstInit(t, dir, VariantNPM, nil); err == nil || !strings.Contains(err.Error(), "it is not set") {
		t.Fatalf("missing: %v", err)
	}
	t.Setenv(ImageEnvVar, "ghcr.io/jlentink/bekup:1.2.3")
	if _, err := composeFirstInit(t, dir, VariantNPM, nil); err == nil || !strings.Contains(err.Error(), "not digest-pinned") {
		t.Fatalf("unpinned: %v", err)
	}
	t.Setenv(ImageEnvVar, variantImage)
	if _, err := composeFirstInit(t, dir, VariantNPM, func(o *Options) {
		o.Answers.Image = mustRef(t, "ghcr.io/jlentink/bekup:1.2.3@"+testDigest2)
	}); err == nil || !strings.Contains(err.Error(), "does not match BEKUP_IMAGE=") {
		t.Fatalf("mismatch: %v", err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Fatalf("refused init wrote files: %d entries (%v)", len(entries), err)
	}
	// --keep-existing with an existing .env: the line is added.
	if err := os.WriteFile(pathIn(dir, EnvFile), []byte("# mine\nBEKUP_DOMAIN=backup.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := composeFirstInit(t, dir, VariantNPM, func(o *Options) { o.KeepExisting = true }); err != nil {
		t.Fatal(err)
	}
	if env := string(readFile(t, dir, EnvFile)); env != "# mine\nBEKUP_DOMAIN=backup.example.com\nBEKUP_IMAGE="+variantImage+"\n" {
		t.Fatalf(".env:\n%s", env)
	}
}

func TestCheckPlaceholder(t *testing.T) {
	setupDir := func(t *testing.T) string {
		t.Setenv(ImageEnvVar, variantImage)
		dir := t.TempDir()
		writePlaceholder(t, dir, VariantTraefik)
		if _, err := composeFirstInit(t, dir, VariantTraefik, nil); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	newer := "ghcr.io/jlentink/bekup:1.2.4@" + testDigest2
	for _, tc := range []struct {
		name   string
		mut    func(t *testing.T, dir string)
		lookup map[string]string
		want   string
	}{
		{"missing in .env", replaceIn(EnvFile, ImageEnvVar+"="+variantImage+"\n", ""), nil, "but it is not set"},
		{"missing in .env, set in the shell", replaceIn(EnvFile, ImageEnvVar+"="+variantImage+"\n", ""), map[string]string{ImageEnvVar: variantImage}, "BEKUP_IMAGE is missing"},
		{"unpinned in .env", replaceIn(EnvFile, ImageEnvVar+"="+variantImage, ImageEnvVar+"=ghcr.io/jlentink/bekup:1.2.3"), nil, "is not digest-pinned"},
		{"newer in .env", replaceIn(EnvFile, ImageEnvVar+"="+variantImage, ImageEnvVar+"="+newer), nil, "run `docker compose run --rm setup upgrade`"},
		{"older in .env", replaceIn(EnvFile, ImageEnvVar+"="+variantImage, ImageEnvVar+"=ghcr.io/jlentink/bekup:1.2.2@"+testDigest2), nil, "is older than the image setup last ran with"},
		{"shell override", func(*testing.T, string) {}, map[string]string{ImageEnvVar: newer}, "overrides .env"},
		{"release file now", func(t *testing.T, dir string) { writeRelease(t, dir, VariantTraefik, variantImage) }, nil, "pins the image (a release file)"},
		{"override image", func(t *testing.T, dir string) {
			if err := os.WriteFile(pathIn(dir, OverrideFile), []byte("services:\n  serve:\n    image: "+newer+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, nil, "is not the x-bekup-image"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := setupDir(t)
			tc.mut(t, dir)
			findings, out, err := runCheck(t, dir, func(o *CheckOptions) {
				o.LookupEnv = func(k string) (string, bool) { v, ok := tc.lookup[k]; return v, ok }
			})
			if !hasFinding(findings, SeverityError, tc.want) || !errors.Is(err, ErrCheckFailed) {
				t.Fatalf("no error %q (%v):\n%s", tc.want, err, out)
			}
		})
	}
	// A deployment set up with the release file that now has the
	// committed file is told to run upgrade.
	dir := t.TempDir()
	writeRelease(t, dir, VariantNPM, variantImage)
	if _, err := composeFirstInit(t, dir, VariantNPM, nil); err != nil {
		t.Fatal(err)
	}
	writePlaceholder(t, dir, VariantNPM)
	findings, out, err := runCheck(t, dir, func(o *CheckOptions) {
		o.LookupEnv = func(k string) (string, bool) { return variantImage, k == ImageEnvVar }
	})
	if !hasFinding(findings, SeverityError, "takes the image from BEKUP_IMAGE") || !errors.Is(err, ErrCheckFailed) {
		t.Fatalf("mode switch:\n%s", out)
	}
}

func TestUpgradePlaceholder(t *testing.T) {
	t.Setenv(ImageEnvVar, variantImage)
	dir := t.TempDir()
	compose := writePlaceholder(t, dir, VariantNPM)
	if _, err := composeFirstInit(t, dir, VariantNPM, nil); err != nil {
		t.Fatal(err)
	}
	opts := func(image string) UpgradeOptions {
		o := upgradeOptions(t, dir, image)
		o.WriteCompose = false
		return o
	}
	// No-op.
	before := snapshot(t, dir)
	o := opts(variantImage)
	if err := Upgrade(o); err != nil {
		t.Fatal(err)
	}
	if d := diffSnapshots(before, snapshot(t, dir)); len(d) > 0 || !strings.Contains(bufOf(t, o.Out).String(), "no changes") {
		t.Fatalf("no-op upgrade changed %v\n%s", d, o.Out)
	}
	// --image without the new BEKUP_IMAGE for the setup container.
	next := "ghcr.io/jlentink/bekup:1.2.3@" + testDigest2
	if err := Upgrade(opts(next)); err == nil || !strings.Contains(err.Error(), "BEKUP_IMAGE="+next+" docker compose run --rm setup upgrade") {
		t.Fatalf("image mismatch: %v", err)
	}
	// BEKUP_IMAGE=<next> docker compose run --rm setup upgrade: .env and
	// the state get the new image, compose.yaml stays.
	t.Setenv(ImageEnvVar, next)
	before = snapshot(t, dir)
	o = opts(next)
	if err := Upgrade(o); err != nil {
		t.Fatal(err)
	}
	if bad := onlyChanged(before, snapshot(t, dir), StateFileName, EnvFile); len(bad) > 0 {
		t.Fatalf("unexpected changes: %v", bad)
	}
	if !bytes.Equal(readFile(t, dir, ComposeFile), compose) {
		t.Fatal("compose.yaml written")
	}
	env := string(readFile(t, dir, EnvFile))
	if !hasLine(env, ImageEnvVar+"="+next) || strings.Count(env, ImageEnvVar+"=") != 1 || !strings.Contains(bufOf(t, o.Out).String(), ".env: BEKUP_IMAGE="+next) {
		t.Fatalf(".env:\n%s\n%s", env, o.Out)
	}
	if st := mustState(t, dir); st.Image != next || st.ImageSource != ImageSourceEnv {
		t.Fatalf("state %+v", st)
	}
	if _, report, err := runCheck(t, dir, func(o *CheckOptions) {
		o.LookupEnv = func(k string) (string, bool) { return next, k == ImageEnvVar }
	}); err != nil {
		t.Fatalf("check: %v\n%s", err, report)
	}
	// An unpinned BEKUP_IMAGE is refused before anything changes.
	t.Setenv(ImageEnvVar, "ghcr.io/jlentink/bekup:1.2.3")
	before = snapshot(t, dir)
	o = opts(next)
	o.Image = mustRef(t, next)
	if err := Upgrade(o); err == nil || !strings.Contains(err.Error(), "not digest-pinned") {
		t.Fatalf("unpinned: %v", err)
	}
	if d := diffSnapshots(before, snapshot(t, dir)); len(d) > 0 {
		t.Fatalf("refused upgrade changed %v", d)
	}
	// Switching to the release file: the .env line goes, the state records
	// the pinned file.
	t.Setenv(ImageEnvVar, next)
	writeRelease(t, dir, VariantNPM, next)
	o = opts(next)
	if err := Upgrade(o); err != nil {
		t.Fatal(err)
	}
	if env := string(readFile(t, dir, EnvFile)); strings.Contains(env, ImageEnvVar+"=") {
		t.Fatalf(".env still names the image:\n%s", env)
	}
	if st := mustState(t, dir); st.ImageSource != ImageSourceCompose {
		t.Fatalf("state %+v", st)
	}
	if _, report, err := runCheck(t, dir, nil); err != nil {
		t.Fatalf("check after the switch: %v\n%s", err, report)
	}
}
