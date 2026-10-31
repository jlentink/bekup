// Package wizard is the interactive front end of `bekup setup` (D-208):
// the setup wizard (no subcommand on a terminal), the guided `setup renew`
// and the confirmation screens of `setup upgrade`.
//
// It only asks questions. The answers become exactly the setup.Options
// (or setup.RenewOptions / an upgrade Confirm hook) that the equivalent
// flags produce, so the files are written by the same library code as the
// flag mode; InitPlan.Flags names that command line.
//
// Two interfaces implement UI: TUI (Bubble Tea + huh, one form per
// screen, Esc goes back, Ctrl+C quits; NO_COLOR and TERM=dumb only drop
// the colors) and Plain (numbered choices and plain lines, only with
// --plain, e.g. for screen readers). Neither relies on color; secrets are
// read without echo into []byte buffers that are zeroed (never Go
// strings). A generated passphrase is written as bytes (never a string or
// an fmt buffer) once, on the alternate screen of the TUI; the plain
// interface warns before it offers one that it stays in the scrollback.
// The wizard runs only when stdin and stdout are terminals.
package wizard
