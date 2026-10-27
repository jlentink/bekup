// Package connections manages database connections (task 2.11, D-135): the
// PostgreSQL, MySQL and MariaDB servers bekup backs up, stored in the
// connections table and administered through the GraphQL API.
//
// Service is the only code that reads or writes the table. It validates
// every connection when it is saved (engine, version and image keys exist
// in the image catalog, host names / IP literals, the D-132 rule that a
// pinned IP-literal target needs an SSH tunnel), keeps credentials
// write-only (callers get HasPassword / SSH auth flags, never values),
// seals them with the master keyring under AAD connections:<id>:secret and
// records every change in the audit log (changed field names only;
// credential fields appear by name and as a secret.changed entry).
//
// serve never connects to a target: Test, DetectVersion, ListDatabases,
// PrePull, FetchCertificate and FetchHostKey go through the runner's mTLS
// API (runnerpb), rate limited per user, with normalized results. The
// request builder (Target) unseals the credentials into a runnerpb.Target
// whose secrets the caller zeroes after the call. Certificate pins and
// SSH host keys are only set by ConfirmPin / StageBackupPin /
// ConfirmHostKey after an admin compared them out of band. A pin or
// host-key mismatch reported by the runner is audited and raised through
// the AlertFunc (task 5.5); TLS downgrades are audited as security.opt_out
// and alerted too.
package connections
