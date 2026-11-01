package deployguard

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/mod/semver"
)

// Compose variables setup writes to .env and the compose files pass to
// migrate, serve and runner (D-212).
const (
	EnvSetupVersion = "BEKUP_SETUP_VERSION"
	EnvImageMode    = "BEKUP_IMAGE_MODE"
	// EnvListenShape names serve's listener shape in the compose file
	// (D-215): a literal in the shipped serve service, never a variable.
	EnvListenShape = "BEKUP_LISTEN_SHAPE"
)

// ListenShapeOwnProxyJoin is the BEKUP_LISTEN_SHAPE of the own-proxy
// compose files: serve joins the operator's proxy network, where Docker
// assigns its address, so it listens on 0.0.0.0:8080 inside its container.
const ListenShapeOwnProxyJoin = "own-proxy-join"

// Image modes (the values of BEKUP_IMAGE_MODE).
const (
	// ImageModePinned (or unset): the bekup image is pinned by digest.
	ImageModePinned = "pinned"
	// ImageModeFollowing: the bekup image follows a tag (D-212).
	ImageModeFollowing = "following"
)

// UpgradeCommand prepares a deployment for the image it runs.
const UpgradeCommand = "docker compose run --rm setup upgrade"

// versionRe is the SemVer form setup records (internal/setup state).
var versionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// Deployment are the facts of one process's environment.
type Deployment struct {
	// SetupVersion is BEKUP_SETUP_VERSION without a leading "v" ("" when
	// unset or empty).
	SetupVersion string
	// ImageFollowing: BEKUP_IMAGE_MODE=following.
	ImageFollowing bool
	// OwnProxyJoin: BEKUP_LISTEN_SHAPE=own-proxy-join (D-215).
	OwnProxyJoin bool
}

// FromEnv reads the deployment facts with lookup (os.LookupEnv). An empty
// value counts as unset (the compose files pass ${VAR:-}). A malformed
// value is an error: the process must not start on facts it cannot read.
func FromEnv(lookup func(string) (string, bool)) (Deployment, error) {
	var d Deployment
	if v, ok := lookup(EnvSetupVersion); ok {
		v = strings.TrimPrefix(strings.TrimSpace(v), "v")
		if v != "" && !versionRe.MatchString(v) {
			return Deployment{}, fmt.Errorf("%s=%q is not a version (x.y.z): it is written by `bekup setup`; run `%s` to rewrite it", EnvSetupVersion, bounded(v), UpgradeCommand)
		}
		d.SetupVersion = v
	}
	if v, ok := lookup(EnvImageMode); ok {
		switch strings.TrimSpace(v) {
		case "", ImageModePinned:
		case ImageModeFollowing:
			d.ImageFollowing = true
		default:
			return Deployment{}, fmt.Errorf("%s=%q: want %s or %s (it is written by `bekup setup`)", EnvImageMode, bounded(v), ImageModeFollowing, ImageModePinned)
		}
	}
	if v, ok := lookup(EnvListenShape); ok {
		switch strings.TrimSpace(v) {
		case "":
		case ListenShapeOwnProxyJoin:
			d.OwnProxyJoin = true
		default:
			return Deployment{}, fmt.Errorf("%s=%q: want %s or nothing (it is set by the own-proxy compose files)", EnvListenShape, bounded(v), ListenShapeOwnProxyJoin)
		}
	}
	return d, nil
}

// ExpectedWildcardListen reports whether a wildcard server.listen of
// exactly listen is the compose shape's own (D-215): the own-proxy join
// shape listens on joinListen because Docker assigns serve's address on
// the operator's proxy network. Only then the startup warning of
// allow_wildcard_listen is dropped (the listener itself is unchanged).
func (d Deployment) ExpectedWildcardListen(listen, joinListen string) bool {
	return d.OwnProxyJoin && listen == joinListen
}

// ErrVersionMismatch is wrapped by CheckVersion's refusal.
var ErrVersionMismatch = errors.New("bekup version differs from the version the deployment was set up for")

// CheckVersion compares this binary's version with the recorded setup
// version. It returns a warning (not an error) when no setup version is
// recorded, nil when both are equal and an error wrapping
// ErrVersionMismatch otherwise (newer and older binaries alike).
func (d Deployment) CheckVersion(binary string) (warning string, err error) {
	b := strings.TrimPrefix(strings.TrimSpace(binary), "v")
	s := d.SetupVersion
	if s == "" {
		return fmt.Sprintf("%s is not set, so this bekup %s cannot tell whether `bekup setup upgrade` prepared the deployment for it; setup writes it to .env (run %#q once, D-212)", EnvSetupVersion, b, UpgradeCommand), nil
	}
	if b == s {
		return "", nil
	}
	pb, ps := "v"+b, "v"+s
	switch {
	case semver.IsValid(pb) && semver.IsValid(ps) && semver.Compare(pb, ps) < 0:
		return "", fmt.Errorf("%w: this image is bekup %s, but the deployment was set up for the newer %s: a downgrade can break the database schema; run the image of %s (pin it by digest), or restore a matching database backup and run `%s --allow-downgrade` from this image", ErrVersionMismatch, b, s, s, UpgradeCommand)
	case semver.IsValid(pb) && semver.IsValid(ps) && semver.Compare(pb, ps) > 0:
		return "", fmt.Errorf("%w: this image is bekup %s, but the deployment was set up for %s: run `%s` first (it prepares the deployment for this version and records it), then `docker compose up -d`", ErrVersionMismatch, b, s, UpgradeCommand)
	default:
		return "", fmt.Errorf("%w: this image is bekup %s, but the deployment was set up for %s (another build of the same version counts as different): run `%s` from this image first, then `docker compose up -d`", ErrVersionMismatch, b, s, UpgradeCommand)
	}
}

// ErrFollowingUnversioned is returned by Check for a followed image
// without a recorded setup version (outside dev mode).
var ErrFollowingUnversioned = errors.New("the bekup image follows a tag, but no setup version is recorded")

// Check is the full startup guard of migrate up, serve, worker, runner and
// db bootstrap: CheckVersion, and outside dev mode a followed image
// (BEKUP_IMAGE_MODE=following) without BEKUP_SETUP_VERSION refuses to
// start: whoever moves the tag would decide what runs, and nothing would
// stop a version setup never prepared the deployment for. The warning is
// "" in dev mode.
func (d Deployment) Check(binary string, dev bool) (warning string, err error) {
	warning, err = d.CheckVersion(binary)
	if err != nil {
		return "", err
	}
	if dev {
		return "", nil
	}
	if d.ImageFollowing && d.SetupVersion == "" {
		return "", fmt.Errorf("%w: %s=%s needs %s, which `bekup setup` writes to .env next to it; run `%s` from this image", ErrFollowingUnversioned, EnvImageMode, ImageModeFollowing, EnvSetupVersion, UpgradeCommand)
	}
	return warning, nil
}

func bounded(s string) string {
	const n = 64
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
