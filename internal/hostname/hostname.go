// Package hostname is the single host-name validator of bekup (D-140).
//
// Every component that accepts a host from configuration or the API (the
// SSRF guard in internal/netguard, the SSH tunnel, the SFTP backend, the
// engine argv builder and the connection/destination services) checks it
// with [Validate], so no layer can accept a host that another one refuses
// (for example an SFTP destination the SSRF guard would not resolve).
package hostname

import (
	"errors"
	"net/netip"
	"strings"
)

// MaxLen is the longest accepted host (a DNS name of 253 bytes).
const MaxLen = 253

// maxLabelLen is the longest DNS label.
const maxLabelLen = 63

// ErrInvalid reports a host that is neither an accepted IP literal nor an
// accepted DNS name. Errors wrap it; their messages never repeat the input.
var ErrInvalid = errors.New("invalid host")

func invalid(reason string) error {
	return &hostError{reason: reason}
}

type hostError struct{ reason string }

func (e *hostError) Error() string { return e.reason }
func (e *hostError) Unwrap() error { return ErrInvalid }

// Validate accepts:
//
//   - an IP address literal (IPv4 dotted quad or IPv6) without a zone and
//     without brackets, or
//   - an RFC 1123 host name: dot-separated labels of 1..63 letters, digits
//     and hyphens, not starting or ending with a hyphen, at most 253 bytes,
//     no trailing dot, and not made of digits and dots only (such names are
//     read as IP addresses by some resolvers, for example "1.2.3").
//
// Underscores, brackets, ports, schemes, spaces and control bytes are
// refused. The error wraps [ErrInvalid] and does not contain the input.
func Validate(h string) error {
	switch {
	case h == "":
		return invalid("required")
	case len(h) > MaxLen:
		return invalid("too long")
	}
	if addr, err := netip.ParseAddr(h); err == nil {
		if addr.Zone() != "" {
			return invalid("zoned addresses are not accepted")
		}
		return nil
	}
	for label := range strings.SplitSeq(h, ".") {
		if !validLabel(label) {
			return invalid("invalid host name")
		}
	}
	if strings.Trim(h, "0123456789.") == "" {
		return invalid("invalid host name")
	}
	return nil
}

// validLabel reports an RFC 1123 label.
func validLabel(l string) bool {
	if l == "" || len(l) > maxLabelLen || l[0] == '-' || l[len(l)-1] == '-' {
		return false
	}
	for i := range len(l) {
		c := l[i]
		alnum := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !alnum && c != '-' {
			return false
		}
	}
	return true
}

// Normalize lowercases a host name and strips one trailing dot (the form
// used for allowlist matching). IP literals are returned unchanged apart
// from surrounding whitespace.
func Normalize(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}

// ValidateBracketed is Validate, additionally accepting an IPv6 literal in
// brackets ("[2001:db8::1]"), as host fields copied from URLs often are.
// It returns the host without brackets.
func ValidateBracketed(h string) (string, error) {
	if inner, ok := strings.CutPrefix(h, "["); ok {
		inner, ok = strings.CutSuffix(inner, "]")
		addr, err := netip.ParseAddr(inner)
		if !ok || err != nil || !addr.Is6() {
			return "", invalid("brackets are only accepted around an IPv6 address")
		}
		return inner, Validate(inner)
	}
	return h, Validate(h)
}
