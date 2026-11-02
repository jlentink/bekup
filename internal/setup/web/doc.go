// Package web is the browser setup mode, `bekup setup web` (D-217): the
// setup wizard's screens served over HTTPS to one browser session. UI
// implements wizard.UI step by step, Gate turns the one-time access code
// into the only session, and Run serves the flow (questions, setup.Init,
// setup.Check, the database step) with a self-signed in-memory
// certificate until it is finished, idle or out of time.
package web
