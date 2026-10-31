// Package schemaversion defines how bekup captures the schema (migration)
// version of every backed-up database (D-210, proposed): the fixed preset
// queries for the common migration frameworks, the validation of a job's
// custom version query, the schema-name rule and the check of a captured
// value.
//
// The package is pure (no I/O). The server (internal/jobs, the GraphQL
// layer) uses it to validate job settings and list the presets; the runner
// (internal/runner/validate, internal/runner/dbprobe) validates a request
// again and builds the statements it runs on the target from it.
//
// A version query is the only user-defined SQL bekup ever runs on a target
// database (an exception to D-152/D-153 documented in D-210). Presets are
// reviewed constant SQL with the table name quoted by the engine helpers;
// a custom query is never trusted as safe: the strict whole-text check
// here (ValidateCustomSQL) is a guardrail, the boundary is how the runner
// executes it (a READ ONLY transaction on the dump's snapshot, statement
// and lock timeouts, one statement, the first column of the first row
// only, a bounded printable result).
package schemaversion
