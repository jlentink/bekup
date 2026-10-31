// A local stand-in for github.com/atotto/clipboard (D-208). charm.land's
// bubbles text inputs import the real package only for an optional paste
// command; its package init runs exec.LookPath for clipboard helpers in
// every process of the single bekup binary. This module keeps the API and
// does nothing: no init, no exec, every call reports that no clipboard is
// available.
module github.com/atotto/clipboard

go 1.24
