// Package tunnel provides SSH tunnels (golang.org/x/crypto/ssh) with a
// mandatory host-key pin and the TOFU fingerprint confirmation flow (D-035,
// D-122).
//
// The runner (task 2.6) uses it to reach a target database through an SSH
// bastion: [Dial] opens an authenticated SSH connection to the bastion whose
// host key must match the configured [HostKeyPin], and the [Tunnel] then
// forwards connections to exactly one target host:port ([Tunnel.DialTarget],
// [Tunnel.Forward]). There is no insecure mode: without a pin, Dial fails.
//
// [FetchHostKey] implements the trust-on-first-use step for the UI (task
// 2.11): it performs the key exchange only, captures the presented host key
// and aborts before any authentication. The result is shown to an admin for
// out-of-band confirmation; it is never trusted automatically.
//
// Only modern algorithms are negotiated (curve25519 / ML-KEM hybrid key
// exchange, chacha20-poly1305 / AES-GCM, hmac-sha2-*-etm), and the accepted
// host-key algorithms are restricted to the pinned key's type, so a server
// cannot downgrade to a different (unpinned) key type. The policy itself
// (pin format, algorithms, client keys, handshake, keepalive) lives in
// internal/sshpolicy and is shared with the SFTP backend (D-140); hosts are
// checked with internal/hostname.
//
// Every network connection goes through a caller-supplied [Dialer], so the
// runner can enforce its target allowlist (task 2.5) for the bastion address.
// Errors are normalized into [Error] values with a Kind of
// [ErrHostKeyMismatch] (a security event), [ErrAuthFailed], [ErrUnreachable],
// [ErrTimeout], [ErrHandshake], [ErrUnsupportedHostKey], [ErrInvalidConfig] or
// [ErrClosed]; they never contain secret material.
package tunnel
