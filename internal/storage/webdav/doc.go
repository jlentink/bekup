// Package webdav is the WebDAV storage backend (D-222): a
// storage.Destination on a WebDAV server (Nextcloud, ownCloud and TransIP
// STACK; Apache mod_dav, nginx-dav, rclone, Caddy; Synology/QNAP NAS;
// hosted WebDAV such as Hetzner Storage Box). It is a small client of its
// own on net/http and encoding/xml, without a WebDAV dependency.
//
// Security properties:
//
//   - Every connection is dialed through the netguard SSRF guard
//     (Guard.NewHTTPClient: no proxy, no redirects, TLS ≥ 1.2); a
//     *netguard.Guard is mandatory.
//   - https only (http only with Config.AllowHTTP in dev mode, warned):
//     HTTP Basic credentials are never sent unencrypted, never to another
//     host (redirects are refused) and never to status.php.
//   - TLS trust is the system roots, or only an admin's CA bundle, or only
//     a pinned certificate (the certificate is the sole root and the leaf
//     SPKI must match, host name and validity still checked; D-222, a
//     WebDAV-specific exception to D-018). Verification is never skipped,
//     not even to show an untrusted certificate: Inspect reads it from the
//     failed verification. A pinned destination presenting another key is
//     storage.CodeTLSPinMismatch (a security event).
//   - Answers are bounded: PROPFIND bodies (16 MiB, 100 000 entries,
//     nesting), status.php (64 KiB), error bodies are only drained.
//     encoding/xml resolves no external entities or DTDs.
//   - Every resource is the base plus a validated storage.Key, each
//     segment percent-encoded; hrefs of answers are decoded and only
//     direct members of the listed collection are used.
//
// Put is atomic and never overwrites: the bytes go to a hidden
// ".<hex>.partial" (one PUT with chunked transfer encoding, or the
// Nextcloud/ownCloud chunked-upload API with 64 MiB chunks), are hashed
// with storage.CopyHashed, the stored size is checked, and the file is
// published with MOVE "Overwrite: F". Whether the server honors that
// header is proven at runtime by the overwrite probe before the first
// upload of every opened destination (see probe.go); a server that
// ignores it is never written to (storage.CodeOverwriteIgnored).
//
// Residual trust in the server: WebDAV has no exclusive create, so the
// refusal to overwrite is the server's MOVE implementation (most check and
// rename under their own locks); bekup never writes the same key twice
// concurrently. Data integrity against a malicious server comes from age
// encryption and the signed manifest, not from this backend.
package webdav
