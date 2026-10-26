package crypto

import (
	"fmt"
	"log/slog"

	"github.com/jlentink/bekup/internal/logging"
)

// Zero overwrites b with zero bytes. Use it on every buffer that held key
// material or a secret once it is no longer needed.
//
// Limitations of zeroing in Go: the garbage collector may move or copy
// heap objects, the runtime may have spilled values to the stack or
// registers, append and string conversions create copies the caller never
// sees, and the OS can swap pages to disk. Zeroing therefore shortens the
// time a secret lives in memory and removes the copies we own; it is not a
// guarantee that no copy remains. Strings cannot be zeroed at all, so keep
// secrets in []byte wherever an API allows it.
func Zero(b []byte) {
	clear(b)
}

// Secret wraps sensitive bytes so they cannot be printed, logged or
// marshaled by accident. Every formatting path (fmt verbs, slog, JSON, text
// marshaling) yields logging.Redacted. The zero value is an empty secret.
//
// A Secret owns its slice: call Destroy when done to zero it. See Zero for
// the limitations of zeroing memory in Go.
type Secret struct {
	b []byte
}

// NewSecret wraps b. The Secret takes ownership of b; the caller must not
// keep using or modifying it.
func NewSecret(b []byte) *Secret {
	return &Secret{b: b}
}

// Bytes returns the underlying bytes (not a copy). The slice is zeroed by
// Destroy; do not retain it past that point.
func (s *Secret) Bytes() []byte {
	if s == nil {
		return nil
	}
	return s.b
}

// Len returns the length of the secret.
func (s *Secret) Len() int {
	if s == nil {
		return 0
	}
	return len(s.b)
}

// Destroy zeroes the secret and releases it. It is safe to call more than
// once and on a nil Secret.
func (s *Secret) Destroy() {
	if s == nil {
		return
	}
	Zero(s.b)
	s.b = nil
}

// String implements fmt.Stringer and always returns logging.Redacted.
func (Secret) String() string { return logging.Redacted }

// GoString implements fmt.GoStringer and always returns logging.Redacted.
func (Secret) GoString() string { return logging.Redacted }

// Format implements fmt.Formatter so that every verb (%v, %+v, %#v, %s, %q,
// %x, %d, ...) prints logging.Redacted instead of the bytes.
func (Secret) Format(f fmt.State, _ rune) {
	fmt.Fprint(f, logging.Redacted)
}

// LogValue implements slog.LogValuer.
func (Secret) LogValue() slog.Value { return slog.StringValue(logging.Redacted) }

// MarshalJSON implements json.Marshaler and always emits "[REDACTED]".
func (Secret) MarshalJSON() ([]byte, error) {
	return []byte(`"` + logging.Redacted + `"`), nil
}

// MarshalText implements encoding.TextMarshaler and always emits
// logging.Redacted (this also covers TOML/YAML encoders).
func (Secret) MarshalText() ([]byte, error) {
	return []byte(logging.Redacted), nil
}
