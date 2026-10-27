// Package app assembles the long-running components of `bekup serve` and
// runs them until shutdown (task 1.1).
//
// New performs the startup checks that make serve refuse to run (D-078,
// D-095, D-099, D-101): it opens the bekup_app pool (TLS per
// database.tls_mode, D-100), verifies the role with db.CheckAppRole,
// compares the schema version with the embedded migrations, records a
// plain-HTTP start outside dev mode in the audit log (fail closed), loads
// the master keyring and checks that it opens the active instance signing
// key, warns when no active admin exists (D-113) and binds the HTTP
// listener, so every configuration or environment error surfaces before
// anything is served. Run then starts every Component concurrently; the
// first one that fails, or cancellation of the context (SIGINT/SIGTERM in
// the CLI), stops all of them, each draining within
// server.shutdown_timeout, and the pool is closed last.
//
// Components today: the HTTP server (GraphQL, WebSocket subscriptions,
// embedded UI, health endpoints), the audit chain-head signer (task 1.3),
// the pubsub LISTEN/NOTIFY listener and the WebSocket hub (task 1.10), and
// the session cleanup. New also wires the authentication services
// (sessions, user administration, TOTP, passkeys, the 2FA policy and the
// second-factor/step-up flow; tasks 1.5–1.9) into the GraphQL resolvers.
//
// Extension points for later tasks: add River workers (3.4) and the
// scheduler (3.3) as further Components appended in New. A Component's Run
// must block until ctx is done and return nil after a clean shutdown.
package app
