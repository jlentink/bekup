// Package clipboard is bekup's inert replacement for
// github.com/atotto/clipboard (wired in with a replace directive in
// bekup's go.mod, D-208). The setup wizard never reads or writes the
// system clipboard, and bekup must not look up or run clipboard helpers
// (xclip, xsel, wl-paste, ...) from a package init in every command of its
// single binary. The API matches the original; every call fails.
package clipboard

import "errors"

// errUnsupported is returned by every call.
var errUnsupported = errors.New("clipboard: not available in bekup")

// Unsupported is always true: callers should not offer clipboard actions.
var Unsupported = true

// ReadAll never reads the clipboard.
func ReadAll() (string, error) { return "", errUnsupported }

// WriteAll never writes the clipboard.
func WriteAll(string) error { return errUnsupported }
