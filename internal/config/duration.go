package config

import (
	"fmt"
	"time"
)

// Duration is a time.Duration written as a Go duration string in TOML and
// env vars, for example "15m" or "1h30m".
type Duration time.Duration

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// String formats the duration like time.Duration.
func (d Duration) String() string { return time.Duration(d).String() }

// MarshalText implements encoding.TextMarshaler.
func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return fmt.Errorf("invalid duration %q (use e.g. \"30s\", \"15m\", \"2h\")", string(b))
	}
	*d = Duration(v)
	return nil
}
