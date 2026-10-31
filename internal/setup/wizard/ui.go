package wizard

import (
	"errors"
	"io"
)

// ErrBack is returned by a screen when the operator goes back (Esc, or
// "<" in the plain interface, or an empty secret).
var ErrBack = errors.New("back")

// ErrAborted is returned when the operator quits (Ctrl+C, end of input).
var ErrAborted = errors.New("setup wizard aborted: nothing was written")

// Option is one choice of a Select screen.
type Option struct {
	Value string
	Label string
}

// UI is one question per screen. Every screen can go back (ErrBack) or
// quit (ErrAborted); nothing relies on color alone (choices are marked
// with a cursor or a number, errors are spelled out).
type UI interface {
	// Note shows text; Enter continues.
	Note(title, body string) error
	// Sensitive shows secret (a generated passphrase) with title and body
	// (plain text); the operator acknowledges it, then it is cleared where
	// the terminal allows. The secret is written as bytes, never converted
	// to a Go string or formatted through fmt.
	Sensitive(title string, secret []byte, body string) error
	// Select returns the value of the chosen option.
	Select(title, body string, opts []Option, def string) (string, error)
	// Input returns a line of text that validate accepts.
	Input(title, body, def string, validate func(string) error) (string, error)
	// Secret reads a value without echo (empty input goes back); the
	// caller zeroes it.
	Secret(title, body string, validate func([]byte) error) ([]byte, error)
	// Confirm returns yes or no.
	Confirm(title, body string, def bool) (bool, error)
	// Out receives the reports after the screens.
	Out() io.Writer
}
