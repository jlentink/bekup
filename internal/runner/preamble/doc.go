// Package preamble implements the stdin secret preamble of dump containers
// (D-032, D-130): a fixed shell wrapper that reads base64, length-prefixed
// records from stdin, writes them to 0600 files in the container's tmpfs and
// then execs the native tool with its fixed argv. Secrets therefore never
// appear in the container's environment, argv, labels or inspect output.
//
// # Framing (version 1)
//
// The preamble is ASCII text at the very start of the container's stdin:
//
//	BEKUP-PREAMBLE 1\n
//	<type> <length>\n          one header line per record
//	<base64>\n                 the record data, standard base64 with padding,
//	                           on one line (empty for length 0)
//	...                        at most MaxRecords records
//	END\n
//	<payload...>               restore/verify data, read by the tool itself
//
// <type> is one of the RecordType constants; each type appears at most once
// and maps to a fixed path under engine.TmpDir (never a path from the
// request). <length> is the decoded size in bytes: decimal, no sign, no
// leading zeros, at most MaxRecordBytes. The wrapper rejects anything else
// with exit code ExitCode before the tool runs, checks the decoded size and
// never echoes record data. The shell `read` builtin consumes stdin byte by
// byte on pipes, so the payload after END reaches the tool untouched.
//
// The record data is opaque to the framing (any bytes, including NUL,
// newlines and quotes); the file formats themselves (pgpass, my.cnf) are
// built and escaped by internal/engine.
package preamble
