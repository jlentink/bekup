// Package httpserver is the HTTP layer of `bekup serve` (task 1.1): the chi
// router, the hardening middleware, the embedded SPA and the health
// endpoints. It knows nothing about the database or GraphQL; those are
// plugged in through Options (Ready, GraphQL, API).
//
// # Middleware order
//
// Outermost first; every request passes through all of them in this order:
//
//  1. requestID: a fresh 128-bit crypto/rand ID per request (X-Request-ID
//     response header, RequestID(ctx)). Client-supplied IDs are ignored.
//  2. clientIP: the client address is the TCP peer, unless the peer is in
//     server.trusted_proxies; only then are X-Forwarded-For (walked right to
//     left, skipping trusted hops) and X-Forwarded-Proto used. All
//     forwarding headers (X-Forwarded-*, Forwarded, X-Real-IP) are then
//     removed from the request so nothing downstream can read spoofed values
//     (ClientIP(ctx), ForwardedProto(ctx)).
//  3. accessLog: one structured slog line per request after it completes
//     (method, path, status, bytes, duration, client IP, request ID). The
//     query string is never logged (it can carry tickets) and paths under
//     the redacted prefixes are cut to the prefix.
//  4. recoverer: a panic becomes a 500 with a generic body; the stack goes
//     to the log only. http.ErrAbortHandler is re-raised.
//  5. securityHeaders: the D-082 CSP, nosniff, Referrer-Policy, X-Frame-Options,
//     Permissions-Policy, COOP/CORP, HSTS only when base_url is https
//     (D-095), and Cache-Control: no-store as the default (static assets
//     override it). Set before the handler runs so that error responses of
//     the later middleware carry them too.
//  6. rateLimit: per-client-IP token bucket (IPv6 keyed by /64), bounded
//     LRU map; 429 with Retry-After.
//  7. originCheck: state-changing methods (anything but GET/HEAD/OPTIONS) and
//     WebSocket upgrades must come from the base_url origin (plus the Vite
//     dev server origins in dev mode), judged by Origin and Sec-Fetch-Site;
//     403 otherwise.
//  8. bodyLimit: Content-Length above the limit is a 413 up front; the body
//     is wrapped in http.MaxBytesReader so streamed bodies are capped too
//     (handlers map the read error with IsBodyTooLarge).
//
// # Sessions
//
// /graphql and /api (only those, so static files and health checks never
// cost a lookup) additionally run the session middleware when
// Options.Sessions is set (task 1.5, session.go): it puts a cookie setter
// and the client facts into the context, resolves the session cookie to
// an auth.Principal (no cookie: no lookup; unknown, expired, disabled or
// duplicate: anonymous and the cookie is cleared; lookup error: 503; a
// lockout does not affect existing sessions) and applies the
// per-user rate limit to authenticated requests.
//
// # Timeouts
//
// NewHTTPServer sets ReadHeaderTimeout, ReadTimeout, WriteTimeout,
// IdleTimeout and MaxHeaderBytes from [server]. WriteTimeout is a deadline
// for the whole response, so long-lived routes added later (WebSocket
// subscriptions, task 1.10; artifact downloads, task 3.9) must extend or clear
// it per request with http.NewResponseController(w).SetWriteDeadline (and
// SetReadDeadline for WebSockets). The response writer wrappers here support
// Unwrap, Flush and Hijack for that.
//
// # Routes
//
// GET /healthz (liveness, no I/O), GET /readyz (Options.Ready: DB ping and
// schema version), /graphql (exactly; the internal/graph handler, sub-paths
// 404), /api (mount point: 404 until a later task provides a handler), and
// everything else is the SPA: embedded files (hashed files under /assets/ cached immutable, the rest no-cache) with an
// index.html fallback for GET/HEAD navigations that accept text/html and are
// not under a reserved prefix.
package httpserver
