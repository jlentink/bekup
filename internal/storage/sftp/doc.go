// Package sftp is the SFTP storage backend (task 2.10, D-128): a
// storage.Destination on a remote SSH/SFTP server, built on
// github.com/pkg/sftp over golang.org/x/crypto/ssh.
//
// Security properties:
//
//   - Every TCP connection is dialed through the netguard SSRF guard
//     (D-120); a *netguard.Guard is mandatory, so private hosts need the
//     admin allowlist.
//   - The server's host key is always pinned (type + SHA-256 fingerprint,
//     constant-time compare); there is no insecure or trust-on-first-use
//     mode here (the TOFU fetch lives in internal/tunnel). Only the pinned
//     key type is offered as host-key algorithm, and only strong transport
//     algorithms are negotiated (internal/sshpolicy, shared with
//     internal/tunnel; D-140). A mismatch is storage.CodeHostKeyMismatch.
//   - Client authentication is a private key (optionally passphrase
//     protected; ed25519, ECDSA or RSA ≥ 2048 with SHA-2 signatures) or a
//     password, both passed as crypto.Secret.
//   - Every object path is the resolved base directory plus a validated
//     storage.Key (no "..", no absolute keys). Every directory below the
//     base is checked with Lstat before use, so server-side symbolic links
//     below the base are never followed for writes, reads, lists or
//     deletes. See Destination for the residual trust in the server.
//   - Put writes a hidden ".<hex>.partial" file (0600, directories 0700),
//     fsyncs it when the server supports fsync@openssh.com, and publishes
//     it with hardlink@openssh.com, which fails if the key exists, so
//     objects are never overwritten and never visible half-written.
package sftp
