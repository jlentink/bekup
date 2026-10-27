// Package netguard is the dial-time SSRF guard for every outbound
// connection serve makes to admin- or user-supplied endpoints: webhooks,
// S3 endpoints, SMTP and SFTP (D-055, D-120).
//
// The authoritative check runs in the net.Dialer Control hook, on the IP
// address the socket is about to connect to, after DNS resolution. A name
// that resolves to a public address during validation and to a private one
// at dial time (DNS rebinding) is therefore still refused.
//
// By default the guard denies loopback, private (RFC 1918), shared (CGNAT),
// link-local (including cloud metadata), unique-local, multicast,
// broadcast, unspecified, documentation, benchmarking and reserved ranges,
// in every IPv4-mapped, IPv4-compatible, NAT64, 6to4 and Teredo form. An
// admin allowlist of CIDRs and host names (exact or "*.suffix") can
// re-allow specific soft-denied targets such as a MinIO on a private
// network; hard-denied ranges (unspecified, link-local, metadata,
// multicast, broadcast, reserved, tunnel/translation forms of denied
// addresses, and loopback outside dev mode) can never be allowlisted.
//
// HTTP clients from NewHTTPClient never follow redirects, use no proxy,
// require TLS 1.2 or later and come with response size limit helpers.
// Normalize maps dial errors to generic categories for non-admin callers so
// that "test connection" features cannot be used as a port scanner.
package netguard
