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

// ErrNotPinned is returned for a reference without a sha256 digest.
var ErrNotPinned = errors.New("image reference is not pinned by digest")

// Ref is a parsed, digest-pinned image reference.
type Ref struct {
	// Repo is [registry[:port]/]path.
	Repo string
	// Tag is optional (empty when the reference has none).
	Tag string
	// Digest is "sha256:<64 hex>".
	Digest string
}

// String returns the canonical form repo[:tag]@digest.
func (r Ref) String() string {
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

func truncate(s string) string {
	const n = 160
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
