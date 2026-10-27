// Package pinning implements target-database certificate pinning in the
// runner (task 2.7a, D-034, D-123, D-133):
//
//   - Fetch captures a target's TLS certificate chain for an admin to
//     confirm (FetchServerCertificate). It performs the protocol-specific
//     TLS upgrade (PostgreSQL SSLRequest or direct TLS, the MySQL/MariaDB
//     in-protocol SSL request), records the chain in VerifyConnection and
//     aborts the handshake right there: no credentials are ever sent and no
//     authentication is ever started.
//   - Checker.Preflight runs before every PINNED connection (Probe, Dump,
//     DumpGlobals, Restore, CreateDatabase, RenameSwap): the same TLS
//     upgrade, then the presented chain must verify against one pin's
//     anchor (the only root; host name or IP checked, verify-full
//     semantics) AND the leaf's SPKI SHA-256 must equal that pin's leaf
//     SPKI (constant-time). A failure is ErrPinMismatch (a security
//     alert in serve).
//   - Checker.TrustAnchor returns the PEM the dump container receives
//     through the stdin preamble as its only CA file (sslrootcert /
//     --ssl-ca); the system roots are never used anywhere.
//   - ClientTLSConfig gives the runner's own Go drivers (Probe) the same
//     verification.
//
// The package never logs or returns certificate contents of a failed
// check beyond fixed reason codes.
package pinning
