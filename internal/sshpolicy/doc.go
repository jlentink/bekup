// Package sshpolicy is bekup's single SSH client policy (D-122, D-128,
// D-140), shared by the SSH tunnel (internal/tunnel), the SFTP backend
// (internal/storage/sftp) and the services that store SSH settings
// (internal/connections, internal/destinations).
//
// It owns:
//
//   - the transport algorithms ([Algorithms]: curve25519 / ML-KEM hybrid
//     key exchange, chacha20-poly1305 / AES-GCM, hmac-sha2-*-etm only);
//   - the accepted host keys ([CheckHostKey]: ed25519, ECDSA, RSA ≥ 2048
//     bits with SHA-2 signatures; no DSA, security-key or certificate host
//     keys) and the host-key algorithms offered for a pinned key type
//     ([HostKeyAlgorithms]), so a server cannot downgrade to another type;
//   - the host-key pin format ([Pin], [Pin.Normalize]) and the pin check
//     during the handshake ([NewPinCallback], [MismatchError]);
//   - authorized_keys host-key lines as entered by admins
//     ([ParseHostKeyLine], at most [MaxAuthorizedKeyLineLen] bytes);
//   - client private keys ([ParseClientKey], with best-effort zeroing of
//     the parsed key through [ClientKey.Destroy] / [ZeroPrivateKey]);
//   - the bounded handshake ([Handshake]) and the keepalive loop
//     ([Keepalive]).
//
// Errors never contain key material or passwords; fingerprints and key
// types are public data.
package sshpolicy
