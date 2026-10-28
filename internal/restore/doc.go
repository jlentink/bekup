// Package restore implements staged restores (tasks 4.1 to 4.5, D-162):
// stage the artifact on the scratch volume, verify it in full against the
// signed manifest and the database records, then hand a bounded plaintext
// stream to the runner's Restore.
//
// Task 4.1 (this file set) provides:
//
//   - the restores table API: CreateTx (restoreArtifact, task 4.5, after
//     authorization and the fresh 2FA check) inserts a queued restore and,
//     for recipient-encrypted artifacts, seals the user's age identity into
//     restore_identity_handoff (AAD restore_identity_handoff:<id>:identity,
//     15-minute TTL). Identities never go into River job args.
//   - Stage (the restore worker's first step): moves the restore from
//     queued to staging exactly once, holds the artifact against retention
//     for the whole restore (runstate hold kind restore, renewed), takes the
//     identity handoff (DELETE ... RETURNING: read once), checks the free
//     space of storage.scratch_dir before downloading, streams the stored
//     object into a 0600 file in a private 0700 directory while hashing it,
//     wrapping it with an ephemeral age key that exists only in memory (so
//     the staged copy is neither readable nor silently replaceable on
//     disk), then verifies the stored SHA-256 against the artifact record
//     and the signed manifest (signature, binding to job, run and entries,
//     encryption facts). A mismatch fails with a *VerificationError, is
//     audited (restore.verification_failed) and raised as a security alert.
//   - Staged.Open: the decrypted, decompressed dump as a bounded stream
//     (pipeline.OpenArtifact; job passphrase by version or the handoff
//     identity), whose end re-checks the stored and the dump digests.
//   - Staged.Cleanup: removes the staging directory, releases the hold and
//     destroys the in-memory keys; it runs on every path (errors, panics,
//     the end of the worker's context), and a sweeper removes leftovers at
//     startup and every 10 minutes.
//
// Task 4.2 (D-166) adds the restore worker (worker.go: River kind restore,
// run slot of the target connection, target and engine re-checks, audit,
// restore_logs, bekup_restore_events, the Notifier hook), the
// EngineRestorer registry and the shared runner calls (engine.go,
// runner_calls.go) and the PostgreSQL restorer (postgres.go: new database,
// overwrite through a temporary database and a rename swap, globals with
// the hardened psql). Task 4.3 adds the MySQL/MariaDB restorer (mysql.go).
//
// Task 4.5 (D-167) adds the API behind GraphQL (api.go, read.go): target
// listings, the preview warnings, Request/RequestGlobals (authorized with
// the artifact's job and the target from the DB, fresh passkey/TOTP
// check, request-time manifest verification, type-to-confirm, one
// transaction with the target's share lock), Cancel and the scoped reads;
// and the stuck-restore sweep (sweep.go, River kind restore_stuck_sweep).
//
// Task 4.6 (D-169) adds scheduled test restores (testrestore*.go): internal
// restores into runner-derived scratch databases (CreateInternalTx,
// Worker.RunInternal), the scratch statistics, CompareStats against the
// backup's source statistics, the always-drop rule, the verdicts and
// results, the schedule worker (River kinds test_restore and
// test_restore_schedule) and the TestRestoreAPI behind GraphQL.
package restore
