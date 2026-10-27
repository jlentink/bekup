// Package audit appends entries to the hash-chained, tamper-evident audit
// log (D-054, D-092), verifies the chain (`bekup audit verify`), lists it
// for the UI, and signs and exports the chain head (D-101).
//
// Writing: Auditor.Record and Auditor.RecordTx insert through sqlc; the
// database trigger assigns id, ts, prev_hash and hash, so the client never
// supplies a hash. Details pass through Redact before they are stored.
//
// Failure policy (fail closed): an audit write error is returned to the
// caller, which must fail the audited operation. Use RecordTx to make the
// audit entry and the action commit or roll back together.
package audit
