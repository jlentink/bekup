package imageref

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Grammar parts (no anchors, no capture groups). Lowercase repositories
// only; the registry host may carry a port.
const (
	// RepoPattern matches [registry[:port]/]path.
	RepoPattern = `(?:[a-z0-9][a-z0-9.-]*(?::[0-9]{1,5})?/)?[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*`
	// TagPattern matches a tag (without the colon).
	TagPattern = `[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}`
	// DigestPattern matches a sha256 digest.
	DigestPattern = `sha256:[a-f0-9]{64}`
)

// MaxLen bounds a reference.
const MaxLen = 512

var pinnedRe = regexp.MustCompile(`^(` + RepoPattern + `)(?::(` + TagPattern + `))?@(` + DigestPattern + `)$`)

// OfficialRepo is the repository the release workflow publishes the bekup
// image to (D-204). Only its tags may be followed without a digest
// (D-212).
const OfficialRepo = "ghcr.io/jlentink/bekup"

// LatestTag is the tag the release workflow moves to the newest release.
const LatestTag = "latest"

var followingRe = regexp.MustCompile(`^` + regexp.QuoteMeta(OfficialRepo) + `:(` + TagPattern + `)$`)

// ErrNotPinned is returned for a reference without a sha256 digest.
var ErrNotPinned = errors.New("image reference is not pinned by digest")

// ErrNotFollowable is returned by ParseFollowing for a reference that is
// not a tag of OfficialRepo.
var ErrNotFollowable = errors.New("only a tag of the official bekup repository " + OfficialRepo + " may be used without a digest")

// Ref is a parsed, digest-pinned image reference.
type Ref struct {
	// Repo is [registry[:port]/]path.
	Repo string
	// Tag is optional (empty when the reference has none).
	Tag string
	// Digest is "sha256:<64 hex>".
	Digest string
}

// IsZero reports the zero Ref (no reference).
func (r Ref) IsZero() bool { return r == Ref{} }

// IsMutable reports a reference without a digest (ParseFollowing): what
// it runs is decided by whoever moves the tag (D-212).
func (r Ref) IsMutable() bool { return !r.IsZero() && r.Digest == "" }

// String returns the canonical form repo[:tag]@digest (repo:tag for a
// mutable reference).
func (r Ref) String() string {
	if r.Digest == "" {
		if r.Repo == "" {
			return ""
		}
		return r.Repo + ":" + r.Tag
	}
	if r.Tag == "" {
		return r.Repo + "@" + r.Digest
	}
	return r.Repo + ":" + r.Tag + "@" + r.Digest
}

// ParsePinned parses s as [registry[:port]/]path[:tag]@sha256:<64 hex>.
// Anything else (no digest, upper case, whitespace, schemes, other digest
// algorithms) is refused; the error never echoes more than a bounded part
// of the input.
func ParsePinned(s string) (Ref, error) {
	if s == "" {
		return Ref{}, errors.New("image reference is empty")
	}
	if len(s) > MaxLen {
		return Ref{}, fmt.Errorf("image reference is longer than %d bytes", MaxLen)
	}
	m := pinnedRe.FindStringSubmatch(s)
	if m == nil {
		if !strings.Contains(s, "@sha256:") {
			return Ref{}, fmt.Errorf("%w: %q (expected [registry/]path[:tag]@sha256:<64 hex>)", ErrNotPinned, truncate(s))
		}
		return Ref{}, fmt.Errorf("invalid image reference %q (expected [registry/]path[:tag]@sha256:<64 hex>)", truncate(s))
	}
	return Ref{Repo: m[1], Tag: m[2], Digest: m[3]}, nil
}

// ParseFollowing parses s as OfficialRepo:<tag> without a digest: the
// bekup image following a tag such as "latest" (D-212). It is a separate
// path from ParsePinned on purpose: only the bekup image may follow a
// tag, and only one of the official repository (third-party images stay
// digest-pinned). The returned Ref has no Digest (IsMutable).
func ParseFollowing(s string) (Ref, error) {
	if s == "" {
		return Ref{}, errors.New("image reference is empty")
	}
	if len(s) > MaxLen {
		return Ref{}, fmt.Errorf("image reference is longer than %d bytes", MaxLen)
	}
	m := followingRe.FindStringSubmatch(s)
	if m == nil {
		return Ref{}, fmt.Errorf("%w: %q", ErrNotFollowable, truncate(s))
	}
	return Ref{Repo: OfficialRepo, Tag: m[1]}, nil
}

// ParseBekup parses the reference of the bekup image itself: digest-pinned
// (ParsePinned) or following a tag of OfficialRepo (ParseFollowing). A
// reference that is neither gets the ParsePinned error.
func ParseBekup(s string) (Ref, error) {
	r, err := ParsePinned(s)
	if err == nil {
		return r, nil
	}
	if f, ferr := ParseFollowing(s); ferr == nil {
		return f, nil
	}
	return Ref{}, err
}

func truncate(s string) string {
	const n = 160
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
