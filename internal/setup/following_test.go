package setup

import (
	"errors"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/imageref"
)

// D-212: the bekup image may follow a tag of the official repository.
const latestImage = "ghcr.io/jlentink/bekup:latest"

func followingAnswers(t *testing.T) Answers {
	t.Helper()
	a := testAnswers(t, ProxyTraefik)
	a.Image = mustRef(t, latestImage)
	return a
}

func TestCheckVersionTagLatest(t *testing.T) {
	t.Parallel()
	if err := CheckVersionTag(mustRef(t, latestImage), "1.2.3"); err != nil {
		t.Fatalf("latest: %v", err)
	}
	// Another followed tag still names a version.
	if err := CheckVersionTag(mustRef(t, "ghcr.io/jlentink/bekup:1.2.2"), "1.2.3"); err == nil {
		t.Fatal("a followed version tag of another version was accepted")
	}
	if err := CheckVersionTag(mustRef(t, "ghcr.io/jlentink/bekup:1.2.3"), "1.2.3"); err != nil {
		t.Fatal(err)
	}
}

func TestStateImageMode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustInit(t, dir, followingAnswers(t))
	st := mustState(t, dir)
	if st.ImageMode != ImageModeFollowing || st.Image != latestImage {
		t.Fatalf("state %+v", st)
	}
	for name, mut := range map[string]func(*State){
		"following with a digest": func(s *State) { s.Image = "ghcr.io/jlentink/bekup:1.2.3@" + testDigest },
		"pinned without a digest": func(s *State) { s.ImageMode = ImageModePinned },
		"other repository":        func(s *State) { s.Image = "ghcr.io/other/bekup:latest" },
		"unknown mode":            func(s *State) { s.ImageMode = "floating" },
	} {
		s := *st
		mut(&s)
		data, err := s.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseState(data); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestInitFollowingSetupFirst(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := mustInit(t, dir, followingAnswers(t)).String()
	if !strings.Contains(out, "NOT pinned by digest") || !strings.Contains(out, "docker compose run --rm setup upgrade") {
		t.Fatalf("report:\n%s", out)
	}
	compose := string(readFile(t, dir, ComposeFile))
	if !strings.Contains(compose, "\nx-bekup-image: &bekup-image "+latestImage+"\n") {
		t.Fatal("compose.yaml does not name the followed tag")
	}
	env := string(readFile(t, dir, EnvFile))
	if !hasLine(env, "BEKUP_IMAGE_MODE=following") || !hasLine(env, "BEKUP_SETUP_VERSION=1.2.3") {
		t.Fatalf(".env:\n%s", env)
	}
	findings, report, err := runCheck(t, dir, nil)
	if err != nil {
		t.Fatalf("check: %v\n%s", err, report)
	}
	if !hasFinding(findings, SeverityWarning, "follows "+latestImage) {
		t.Fatalf("no following warning:\n%s", report)
	}
}

func TestCheckFollowingFindings(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		mut     func(t *testing.T, dir string)
		version string
		want    string
	}{
		{"mode missing in .env", replaceIn(EnvFile, "BEKUP_IMAGE_MODE=following\n", ""), "", "only once the runner reports it"},
		{"setup version differs", replaceIn(EnvFile, "BEKUP_SETUP_VERSION=1.2.3", "BEKUP_SETUP_VERSION=1.2.2"), "", "refuse to start"},
		{"setup version missing", replaceIn(EnvFile, "BEKUP_SETUP_VERSION=1.2.3\n", ""), "", "refuse to start without it"},
		{"tag moved", func(*testing.T, string) {}, "1.2.4", "the followed tag has moved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mustInit(t, dir, followingAnswers(t))
			tc.mut(t, dir)
			findings, out, err := runCheck(t, dir, func(o *CheckOptions) {
				if tc.version != "" {
					o.Version = tc.version
				}
			})
			if !hasFinding(findings, SeverityError, tc.want) || !errors.Is(err, ErrCheckFailed) {
				t.Fatalf("no error %q (%v):\n%s", tc.want, err, out)
			}
		})
	}
	// A pinned deployment: a missing setup version only warns; a followed
	// tag in compose.yaml is an error (not recorded as following).
	dir := t.TempDir()
	mustInit(t, dir, testAnswers(t, ProxyTraefik))
	replaceIn(EnvFile, "BEKUP_SETUP_VERSION=1.2.3\n", "")(t, dir)
	findings, out, err := runCheck(t, dir, nil)
	if err != nil || !hasFinding(findings, SeverityWarning, "BEKUP_SETUP_VERSION is missing") {
		t.Fatalf("missing setup version: %v\n%s", err, out)
	}
}

func TestLintFollowingAllowance(t *testing.T) {
	t.Parallel()
	img := mustRef(t, latestImage)
	src, err := RenderRelease(VariantOwnProxy, DBBundled, &img)
	if err != nil {
		t.Fatal(err)
	}
	fsys := testOptionsAssets()
	// Recorded as pinned: every bekup service is an error.
	info, err := inspectComposeData(fsys, composeInput{compose: src}, "", "")
	if err != nil || !hasFinding(info.Findings, SeverityError, "follows a tag, but this deployment is recorded as digest-pinned") {
		t.Fatalf("pinned: %+v %v", info, err)
	}
	// Following: allowed, recognized as the release file with that image.
	info, err = inspectComposeData(fsys, composeInput{compose: src, following: true}, "", "")
	if err != nil || hasErrors(info.Findings) || info.Level != RecognizedExact || !info.Image.IsMutable() {
		t.Fatalf("following: %+v %v", info, err)
	}
	// Following never covers other images: an added service with a
	// tag-only image, or another bekup tag, stays an error.
	for name, edit := range map[string]string{
		"third-party": "  extra:\n    image: alpine:3\n" + hardened,
		"other tag":   "  extra:\n    image: ghcr.io/jlentink/bekup:1.2.3\n" + hardened,
	} {
		s := strings.Replace(string(src), "\nservices:\n", "\nservices:\n"+edit, 1)
		info, err := inspectComposeData(fsys, composeInput{compose: []byte(s), following: true}, VariantOwnProxy, "")
		if err != nil || !hasFinding(info.Findings, SeverityError, "is not digest-pinned") {
			t.Fatalf("%s: %+v %v", name, info, err)
		}
	}
}

func TestUpgradeFollowing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustInit(t, dir, followingAnswers(t))
	// The plain `docker compose run --rm setup upgrade` of the new image:
	// no --write-compose and the image from the setup service.
	o := upgradeOptions(t, dir, latestImage)
	o.WriteCompose, o.Version = false, "1.2.4"
	if err := Upgrade(o); err != nil {
		t.Fatal(err)
	}
	report := bufOf(t, o.Out).String()
	if !strings.Contains(report, "BEKUP_SETUP_VERSION=1.2.4") || !strings.Contains(report, "`docker compose up -d` (the image you pulled") {
		t.Fatalf("report:\n%s", report)
	}
	st := mustState(t, dir)
	env := string(readFile(t, dir, EnvFile))
	if st.BekupVersion != "1.2.4" || st.ImageMode != ImageModeFollowing || !hasLine(env, "BEKUP_SETUP_VERSION=1.2.4") || !hasLine(env, "BEKUP_IMAGE_MODE=following") {
		t.Fatalf("state %+v\n.env:\n%s", st, env)
	}
	// Without an image at all the recorded tag is used.
	o = upgradeOptions(t, dir, latestImage)
	o.Image, o.WriteCompose, o.Version = imageref.Ref{}, false, "1.2.4"
	if err := Upgrade(o); err != nil {
		t.Fatal(err)
	}
	// Back to a digest: --image pinned.
	pinned := "ghcr.io/jlentink/bekup:1.2.4@" + testDigest2
	o = upgradeOptions(t, dir, pinned)
	o.WriteCompose, o.Version = false, "1.2.4"
	if err := Upgrade(o); err != nil {
		t.Fatal(err)
	}
	st = mustState(t, dir)
	env = string(readFile(t, dir, EnvFile))
	if st.ImageMode != ImageModePinned || st.Image != pinned || strings.Contains(env, "\nBEKUP_IMAGE_MODE=") ||
		!strings.Contains(string(readFile(t, dir, ComposeFile)), "x-bekup-image: &bekup-image "+pinned+"\n") {
		t.Fatalf("pinned again: %+v\n%s", st, env)
	}
	if !strings.Contains(bufOf(t, o.Out).String(), "pinned by digest again") {
		t.Fatal("no note about pinning again")
	}
	if _, out, err := runCheck(t, dir, func(o *CheckOptions) { o.Version = "1.2.4" }); err != nil {
		t.Fatalf("check after pinning: %v\n%s", err, out)
	}
	// Pinned deployments still need --write-compose (setup-first).
	o = upgradeOptions(t, dir, pinned)
	o.WriteCompose, o.Version = false, "1.2.4"
	if err := Upgrade(o); err == nil || !strings.Contains(err.Error(), "--write-compose") {
		t.Fatalf("pinned without --write-compose: %v", err)
	}
}

func TestPlaceholderFollowing(t *testing.T) {
	dir := t.TempDir()
	writePlaceholder(t, dir, VariantTraefik)
	t.Setenv(ImageEnvVar, latestImage)
	if _, err := composeFirstInit(t, dir, VariantTraefik, nil); err != nil {
		t.Fatal(err)
	}
	env := string(readFile(t, dir, EnvFile))
	if !hasLine(env, ImageEnvVar+"="+latestImage) || !hasLine(env, "BEKUP_IMAGE_MODE=following") {
		t.Fatalf(".env:\n%s", env)
	}
	if st := mustState(t, dir); st.ImageMode != ImageModeFollowing || st.ImageSource != ImageSourceEnv {
		t.Fatalf("state %+v", st)
	}
	findings, out, err := runCheck(t, dir, func(o *CheckOptions) {
		o.LookupEnv = func(string) (string, bool) { return "", false }
	})
	if err != nil || !hasFinding(findings, SeverityWarning, "follows "+latestImage) {
		t.Fatalf("check: %v\n%s", err, out)
	}
	// Pinning again through BEKUP_IMAGE in the shell.
	pinned := "ghcr.io/jlentink/bekup:1.2.3@" + testDigest2
	t.Setenv(ImageEnvVar, pinned)
	o := upgradeOptions(t, dir, pinned)
	o.WriteCompose = false
	if err := Upgrade(o); err != nil {
		t.Fatal(err)
	}
	env = string(readFile(t, dir, EnvFile))
	if !hasLine(env, ImageEnvVar+"="+pinned) || strings.Contains(env, "\nBEKUP_IMAGE_MODE=") {
		t.Fatalf(".env after pinning:\n%s", env)
	}
}
