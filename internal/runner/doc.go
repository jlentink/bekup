// Package runner implements the bekup runner sidecar: the mTLS gRPC server,
// request validation, the target allowlist, hardened container specs,
// per-run internal networks and proxies, and the Docker executor. It is the
// only component with Docker access.
//
// Task 2.4 provides the server skeleton (D-125, D-126, D-127): TLS 1.3 mTLS
// with the private CA and the client allowlist (internal/certs), keepalive
// and connection recycling, an interceptor chain (logging without payloads,
// panic recovery, caller identity, mandatory and clamped deadlines, a
// deny-by-default method table, fail-fast concurrency limits, zeroing of
// secret fields), the Health RPC and the container-local health socket for
// `bekup healthcheck runner`. Task 2.5 adds the pure building blocks
// (D-130): request validation (validate), the target allowlist (targets),
// the hardened container spec (spec) and the stdin secret preamble
// (preamble); New loads the image and target allowlists and fails startup on
// invalid ones. Task 2.6 adds the Docker executor (executor), task 2.7a
// certificate pinning (pinning, FetchServerCertificate) and task 2.7 the
// RPC handlers in ops*.go (D-134): Probe with the Go drivers of dbprobe,
// Dump, DumpGlobals (with the --no-role-passwords fallback), VerifyDump
// (Go trailer checks or an offline pg_restore container), Restore,
// CreateDatabase, RenameSwapDatabase, DropTempDatabase and PrePull, all with normalized
// target errors (runnerpb.Reason) and one audit line per call. Task 2.11
// adds FetchHostKey (ops_hostkey.go): the TOFU capture of a bastion's host
// key through the target-allowlist dialer, without authentication. Task 4.6
// adds ScratchStats and DropScratchDatabase (ops_scratch.go): the
// statistics and the drop of a test restore's runner-derived scratch
// database bekup_test_<id>.
package runner
