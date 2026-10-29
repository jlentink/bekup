// Package email is bekup's outbound e-mail (task 5.1, D-178): the SMTP
// settings (admin-managed, stored in the settings table with the password
// sealed under AAD settings:smtp:password), the SMTP sender, the message
// builder with header-injection protection, the multipart text + HTML
// template renderer and the user e-mail verification.
//
// Security rules:
//
//   - Every SMTP connection is dialed through internal/netguard (dial-time
//     SSRF guard); private relays must be allowlisted.
//   - Transport security follows the server (D-180): mode auto (default)
//     uses STARTTLS when offered and otherwise sends unencrypted; starttls
//     requires it; implicit_tls speaks TLS from the first byte; none never
//     encrypts. Whenever TLS is used: TLS >= 1.2 and server-name
//     verification against the system roots, or only against the admin's
//     CA bundle when one is set; a failed STARTTLS handshake is an error,
//     never a fallback. auto/none are flagged mayBeUnencrypted (UI badge),
//     audited with the mode, and an unencrypted session logs one WARN per
//     process and server.
//   - AUTH runs over TLS, or without TLS only in auto/none when the session
//     ended up unencrypted (then the credentials travel in clear text). The
//     password is write-only in the API and never logged.
//   - Every header value is validated (no CR/LF or other control
//     characters) and encoded; bodies are rendered with html/template
//     (auto-escaping), carry no remote images and no tracking.
//   - Verification links carry a 256-bit code; the database stores only a
//     keyed MAC bound to the user and the address. Codes are single use,
//     expire after 24 hours and can only be redeemed by the signed-in
//     account they were issued to.
package email
