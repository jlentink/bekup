package httpserver

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

type ctxKey int

const (
	requestIDKey ctxKey = iota
	clientKey
)

// RequestID returns the ID assigned to the request ("" outside a request).
func RequestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

// clientInfo is the resolved client address and forwarded scheme.
type clientInfo struct {
	ip    netip.Addr
	proto string
}

func clientFrom(ctx context.Context) clientInfo {
	if ci, ok := ctx.Value(clientKey).(clientInfo); ok {
		return ci
	}
	return clientInfo{}
}

// ClientIP returns the resolved client address (the peer, or the address
// forwarded by a trusted proxy). It is invalid when unknown.
func ClientIP(ctx context.Context) netip.Addr {
	return clientFrom(ctx).ip
}

// ForwardedProto returns "http" or "https" as reported by a trusted proxy,
// or "" when the peer is not a trusted proxy or sent nothing usable. Links
// and security decisions use server.base_url, never this value.
func ForwardedProto(ctx context.Context) string {
	return clientFrom(ctx).proto
}

// --- 1. request ID -------------------------------------------------------

const requestIDHeader = "X-Request-ID"

func newRequestID() string {
	var b [16]byte
	// crypto/rand.Read never returns an error (it panics instead).
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

// --- 2. client IP / trusted proxies --------------------------------------

// forwardingHeaders are removed from every request after resolution.
var forwardingHeaders = []string{
	"X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-Host", "X-Forwarded-Port",
	"X-Forwarded-Prefix", "X-Forwarded-Server", "Forwarded", "X-Real-Ip", "X-Client-Ip",
	"True-Client-Ip", "Cf-Connecting-Ip",
}

// missingXFFWarnInterval rate-limits the warning about requests from a
// trusted proxy without X-Forwarded-For.
const missingXFFWarnInterval = 5 * time.Minute

// xffWarner logs, at most once per missingXFFWarnInterval, that a trusted
// proxy sent a request without X-Forwarded-For (M1 review L4): every such
// request is attributed to the proxy itself, so per-IP rate limits and
// audit IPs then lump all clients together. Health checks are exempt
// (probes often come from the proxy host directly).
type xffWarner struct {
	logger *slog.Logger
	now    func() time.Time
	mu     sync.Mutex
	last   time.Time
}

func (x *xffWarner) check(r *http.Request, trusted []netip.Prefix) {
	if x == nil || x.logger == nil || len(r.Header.Values("X-Forwarded-For")) > 0 ||
		r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
		return
	}
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil || !isTrusted(ap.Addr().Unmap(), trusted) {
		return
	}
	now := x.now()
	x.mu.Lock()
	if !x.last.IsZero() && now.Sub(x.last) < missingXFFWarnInterval {
		x.mu.Unlock()
		return
	}
	x.last = now
	x.mu.Unlock()
	x.logger.LogAttrs(r.Context(), slog.LevelWarn,
		"request from a trusted proxy without X-Forwarded-For; the proxy address is used as the client address",
		slog.String("peer", ap.Addr().Unmap().String()), slog.String("request_id", RequestID(r.Context())))
}

func clientIP(trusted []netip.Prefix, warn *xffWarner) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			warn.check(r, trusted)
			ci := resolveClient(r, trusted)
			for _, h := range forwardingHeaders {
				r.Header.Del(h)
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientKey, ci)))
		})
	}
}

func isTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	if !a.IsValid() {
		return false
	}
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// resolveClient implements the trusted-proxy rule: forwarding headers count
// only when the TCP peer is a trusted proxy. X-Forwarded-For is walked from
// the right (the hop closest to us); trusted hops are skipped and the first
// untrusted address is the client. An unparsable entry stops the walk at the
// last trusted hop (the conservative choice).
func resolveClient(r *http.Request, trusted []netip.Prefix) clientInfo {
	var peer netip.Addr
	if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		peer = ap.Addr().Unmap()
	}
	ci := clientInfo{ip: peer}
	if !isTrusted(peer, trusted) {
		return ci
	}
	if p := lastListValue(r.Header.Values("X-Forwarded-Proto")); p == "http" || p == "https" {
		ci.proto = p
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(v, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		a = a.Unmap()
		ci.ip = a
		if !isTrusted(a, trusted) {
			break
		}
	}
	return ci
}

func lastListValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	parts := strings.Split(values[len(values)-1], ",")
	return strings.ToLower(strings.TrimSpace(parts[len(parts)-1]))
}

// --- 3. access log ---------------------------------------------------------

// statusWriter records the status and size of a response. It supports
// Unwrap (http.ResponseController), Flush and Hijack (WebSockets).
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) Flush() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if w.status == 0 {
		w.status = http.StatusSwitchingProtocols
	}
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

// maxLoggedPath bounds the logged path length.
const maxLoggedPath = 256

// logPath returns the path for the access log: never the query string;
// paths under a redacted prefix are cut to the prefix.
func logPath(path string, redacted []string) string {
	for _, p := range redacted {
		if strings.HasPrefix(path, p) {
			return p + "[redacted]"
		}
	}
	if len(path) > maxLoggedPath {
		return path[:maxLoggedPath] + "..."
	}
	return path
}

func logRequest(ctx context.Context, logger *slog.Logger, r *http.Request, sw *statusWriter, path string, start time.Time) {
	status := sw.status
	if status == 0 {
		status = http.StatusOK
	}
	logger.LogAttrs(ctx, slog.LevelInfo, "http request",
		slog.String("method", r.Method),
		slog.String("path", path),
		slog.Int("status", status),
		slog.Int64("bytes", sw.bytes),
		slog.Duration("duration", time.Since(start)),
		slog.String("client_ip", ClientIP(ctx).String()),
		slog.String("request_id", RequestID(ctx)),
	)
}

func accessLog(logger *slog.Logger, redacted []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := &statusWriter{ResponseWriter: w}
			defer logRequest(r.Context(), logger, r, sw, logPath(r.URL.Path, redacted), time.Now())
			next.ServeHTTP(sw, r)
		})
	}
}

// --- 4. panic recovery -----------------------------------------------------

// handlePanic must be deferred directly (it calls recover).
func handlePanic(ctx context.Context, logger *slog.Logger, w http.ResponseWriter) {
	p := recover()
	if p == nil {
		return
	}
	if err, ok := p.(error); ok && errors.Is(err, http.ErrAbortHandler) {
		panic(p)
	}
	logger.LogAttrs(ctx, slog.LevelError, "http handler panic",
		slog.String("panic", fmt.Sprint(p)),
		slog.String("stack", string(debug.Stack())),
		slog.String("request_id", RequestID(ctx)),
	)
	if sw, ok := w.(*statusWriter); ok && sw.status != 0 {
		// The response has started; abort the connection instead.
		panic(http.ErrAbortHandler)
	}
	writeError(w, http.StatusInternalServerError)
}

func recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer handlePanic(r.Context(), logger, w)
			next.ServeHTTP(w, r)
		})
	}
}

// --- 5. security headers ---------------------------------------------------

// ContentSecurityPolicy is the exact policy of D-082.
const ContentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; worker-src 'none'; manifest-src 'self'; require-trusted-types-for 'script'; trusted-types 'none'"

// PermissionsPolicy disables powerful features bekup never uses. WebAuthn
// (publickey-credentials-*) keeps its default allowlist ('self').
const PermissionsPolicy = "accelerometer=(), camera=(), display-capture=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), midi=(), payment=(), usb=()"

// HSTSValue is sent only when base_url is https (D-095).
const HSTSValue = "max-age=63072000; includeSubDomains"

func securityHeaders(https bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", ContentSecurityPolicy)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Permissions-Policy", PermissionsPolicy)
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Cross-Origin-Resource-Policy", "same-origin")
			if https {
				h.Set("Strict-Transport-Security", HSTSValue)
			}
			// Default for every response (API, errors, health); the static
			// file handler overrides it for cacheable assets.
			h.Set("Cache-Control", "no-store")
			next.ServeHTTP(w, r)
		})
	}
}

// --- 7. Origin / Sec-Fetch-Site --------------------------------------------

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func isWebSocketUpgrade(r *http.Request) bool {
	return headerHasToken(r.Header, "Connection", "upgrade") && headerHasToken(r.Header, "Upgrade", "websocket")
}

func headerHasToken(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for t := range strings.SplitSeq(v, ",") {
			if strings.EqualFold(strings.TrimSpace(t), token) {
				return true
			}
		}
	}
	return false
}

// originAllowed decides a state-changing request or WebSocket upgrade. A
// present Sec-Fetch-Site must be "same-origin"; a present Origin must be one
// of the allowed origins exactly; with neither header the request is
// refused (every current browser sends Origin on such requests, so this only
// affects non-browser clients, which have no business here yet).
func originAllowed(r *http.Request, allowed []string) bool {
	if sfs := r.Header.Values("Sec-Fetch-Site"); len(sfs) > 0 {
		if len(sfs) != 1 || !strings.EqualFold(sfs[0], "same-origin") {
			return false
		}
	}
	origins := r.Header.Values("Origin")
	switch len(origins) {
	case 0:
		return len(r.Header.Values("Sec-Fetch-Site")) == 1
	case 1:
	default:
		return false
	}
	o := strings.ToLower(strings.TrimSpace(origins[0]))
	for _, a := range allowed {
		if o == a {
			return true
		}
	}
	return false
}

func originCheck(allowed []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if (!isSafeMethod(r.Method) || isWebSocketUpgrade(r)) && !originAllowed(r, allowed) {
				writeError(w, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// --- 8. body limit ---------------------------------------------------------

// IsBodyTooLarge reports whether err comes from reading a request body past
// the configured limit; handlers answer it with 413.
func IsBodyTooLarge(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}

func bodyLimit(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > limit {
				writeError(w, http.StatusRequestEntityTooLarge)
				return
			}
			if r.Body != nil && r.Body != http.NoBody {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}
