package config

import (
	"errors"
	"regexp"
	"slices"
	"strings"
)

// Error kinds of a failed load (ErrorKindOf). They, and the key names of
// ErrorKeys, are all a runtime reload records or shows: error texts may
// contain values (D-200).
const (
	// ErrorKindFile: the file could not be found, opened or passed the
	// ownership/mode checks (D-041).
	ErrorKindFile = "file"
	// ErrorKindSyntax: the file is not valid TOML.
	ErrorKindSyntax = "syntax"
	// ErrorKindInvalid: the file parsed but a key is unknown or a value
	// failed validation.
	ErrorKindInvalid = "invalid"
)

// KeyError is a problem with one key: an unknown key, a value of the
// wrong type or a value that failed validation. Path names the key
// ("section.key"); Msg is the full error text, which may contain the
// value and must only go to the redacting log.
type KeyError struct {
	Path string
	Msg  string
}

func (e *KeyError) Error() string { return e.Msg }

// FileError wraps a failure to locate, open or safely read a config file.
type FileError struct{ Err error }

func (e *FileError) Error() string { return e.Err.Error() }

// Unwrap returns the wrapped error.
func (e *FileError) Unwrap() error { return e.Err }

// SyntaxError wraps a TOML parse error that names no key.
type SyntaxError struct{ Err error }

func (e *SyntaxError) Error() string { return e.Err.Error() }

// Unwrap returns the wrapped error.
func (e *SyntaxError) Unwrap() error { return e.Err }

// UnknownKey is the key part ErrorKeys reports for a key that is not part
// of the schema: the name itself came from the file and is never echoed.
const UnknownKey = "unknown_key"

// UnknownSection replaces a section name that is not part of the schema.
const UnknownSection = "unknown_section"

// keyPathRE is the shape of every path ErrorKeys returns.
var keyPathRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}\.[a-z][a-z0-9_]{0,63}$`)

// walkErrors calls fn for err and every error it wraps (errors.Join and
// %w trees).
func walkErrors(err error, fn func(error)) {
	if err == nil {
		return
	}
	fn(err)
	switch u := err.(type) { //nolint:errorlint // walking the tree by hand on purpose
	case interface{ Unwrap() []error }:
		for _, e := range u.Unwrap() {
			walkErrors(e, fn)
		}
	case interface{ Unwrap() error }:
		walkErrors(u.Unwrap(), fn)
	}
}

// ErrorKeys returns the sorted, de-duplicated key paths named by the
// *KeyError values in err. Paths outside the bekup.toml schema are
// reported as "<section>.unknown_key" (or "unknown_section.unknown_key"),
// never with the name from the file.
func ErrorKeys(err error) []string {
	known := KeyPaths()
	sections := map[string]bool{}
	for _, p := range known {
		s, _, _ := strings.Cut(p, ".")
		sections[s] = true
	}
	seen := map[string]bool{}
	walkErrors(err, func(e error) {
		ke, ok := e.(*KeyError) //nolint:errorlint // walkErrors visits every wrapped error
		if !ok {
			return
		}
		p := ke.Path
		if !slices.Contains(known, p) {
			section, _, _ := strings.Cut(p, ".")
			if !sections[section] {
				section = UnknownSection
			}
			p = section + "." + UnknownKey
		}
		if keyPathRE.MatchString(p) {
			seen[p] = true
		}
	})
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

// ErrorKindOf classifies a load error: ErrorKindFile, ErrorKindSyntax or
// ErrorKindInvalid (anything else, including validation errors). It
// returns "" for nil.
func ErrorKindOf(err error) string {
	if err == nil {
		return ""
	}
	var fe *FileError
	if errors.As(err, &fe) {
		return ErrorKindFile
	}
	var se *SyntaxError
	if errors.As(err, &se) {
		return ErrorKindSyntax
	}
	return ErrorKindInvalid
}
