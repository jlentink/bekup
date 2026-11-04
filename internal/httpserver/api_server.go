package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/jlentink/bekup/internal/apikey"
	"github.com/jlentink/bekup/internal/config"
)

// The external API's HTTP server (`bekup external-api`, D-220). It shares
// serve's middleware where the semantics are the same: request IDs, the
// trusted-proxy client address rule (forwarding headers only from
// TrustedProxies and the resolved TrustedProxyHosts, then stripped), the
// access log, panic recovery, security headers, the per-IP rate limit and
// the body limit. It differs where a token-authenticated JSON API for CI
// differs from a browser application: errors are JSON, the CSP is
// default-src 'none', HSTS is always sent (the API is only served behind
// the TLS proxy), there is no Origin check (no cookies, no ambient
// credentials: the token travels only in the Authorization header), no
// CORS, no session middleware and no UI. The access log never shows a
// path that may carry a token.

// APIBasePath is where the external API's handler is mounted.
const APIBasePath = "/api/v1"

// APIOptions configure NewAPIServer.
type APIOptions struct {
	// TrustedProxies and the resolved addresses of TrustedProxyHosts are
	// the only peers whose X-Forwarded-For counts ([listen] of
	// external-api.toml).
	TrustedProxies    []netip.Prefix
	TrustedProxyHosts []string
	// ProxyHostResolver and InterfaceAddrs replace the process resolver
	// and net.InterfaceAddrs (tests).
	ProxyHostResolver ProxyHostResolver
	InterfaceAddrs    InterfaceAddrs
	// JoinMode: the own-proxy compose shape with the wildcard listener
	// (config.ExternalAPIJoinListen, D-220 amendment 4), as serve's
	// Options.JoinMode: the alias ExternalAPIAlias must resolve only to
	// this container (another container behind it fails /readyz),
	// Server.WrapListener serves only connections on its own address on
	// the proxy network and loopback, and Server.ProxyHosts must run.
	JoinMode bool
	// MaxBodyBytes caps request bodies.
	MaxBodyBytes int64
	// RateLimit configures the per-client-IP limiter.
	RateLimit RateLimitOptions
	// Logger receives access and error logs (required).
	Logger *slog.Logger
	// Ready backs /readyz (required).
	Ready ReadyFunc
	// ReadyTimeout bounds one readiness check (default 3s).
	ReadyTimeout time.Duration
	// API is mounted at APIBasePath (required).
	API http.Handler

	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
}

// apiMaxHeaderBytes bounds the request headers of the external API (a
// token is 83 bytes; CI clients send few headers).
const apiMaxHeaderBytes = 16 << 10

// APIOptionsFromConfig maps external-api.toml; the caller sets Logger,
// Ready and API.
func APIOptionsFromConfig(c *config.ExternalAPI) (APIOptions, error) {
	proxies, err := parseProxies(c.Listen.TrustedProxies)
	if err != nil {
		return APIOptions{}, err
	}
	return APIOptions{
		TrustedProxies:    proxies,
		TrustedProxyHosts: slices.Clone(c.Listen.TrustedProxyHosts),
		MaxBodyBytes:      c.Limits.MaxBodyBytes,
		RateLimit: RateLimitOptions{
			PerSecond:  c.Limits.RateLimitPerSecond,
			Burst:      c.Limits.RateLimitBurst,
			MaxClients: c.Limits.RateLimitMaxClients,
		},
		ReadHeaderTimeout: c.Listen.ReadHeaderTimeout.D(),
		ReadTimeout:       c.Listen.ReadTimeout.D(),
		WriteTimeout:      c.Listen.WriteTimeout.D(),
		IdleTimeout:       c.Listen.IdleTimeout.D(),
	}, nil
}

// NewAPIServer builds the external API's router: /healthz, /readyz and the
// API under APIBasePath, behind the middleware described above. Run
// Server.ProxyHosts next to it when TrustedProxyHosts are set.
func NewAPIServer(o APIOptions) (*Server, error) {
	switch {
	case o.Logger == nil:
		return nil, errors.New("httpserver: Logger is required")
	case o.Ready == nil:
		return nil, errors.New("httpserver: Ready is required")
	case o.API == nil:
		return nil, errors.New("httpserver: API is required")
	case o.MaxBodyBytes <= 0:
		return nil, errors.New("httpserver: MaxBodyBytes must be positive")
	}
	if o.ReadyTimeout <= 0 {
		o.ReadyTimeout = 3 * time.Second
	}
	limiter, err := newRateLimiter(o.RateLimit)
	if err != nil {
		return nil, err
	}
	if err := validateProxyHosts(o.TrustedProxyHosts); err != nil {
		return nil, err
	}
	proxies := slices.Clone(o.TrustedProxies)
	for i, p := range proxies {
		if !p.IsValid() {
			return nil, errors.New("httpserver: invalid trusted proxy")
		}
		proxies[i] = p.Masked()
	}
	live := newLive(proxies, liveOptions{hosts: o.TrustedProxyHosts, ph: proxyHostsOptions{
		alias: ExternalAPIAlias, resolve: o.ProxyHostResolver, ifaces: o.InterfaceAddrs, logger: o.Logger,
		checkSelf: o.JoinMode, strict: o.JoinMode,
	}}, o.MaxBodyBytes, limiter, nil)

	r := chi.NewRouter()
	r.Use(
		requestID,
		clientIP(live.trustedProxies, &xffWarner{logger: o.Logger, setting: "listen.trusted_proxies", now: time.Now, untrusted: live.hosts.kick, peer: live.seenPeer}),
		accessLog(o.Logger, apiLogPath),
		recoverer(o.Logger, WriteAPIError),
		securityHeaders(true, APIContentSecurityPolicy),
		rateLimit(limiter, WriteAPIError),
		bodyLimit(live.bodyLimit.Load, WriteAPIError),
	)
	r.Get("/healthz", healthz)
	r.Head("/healthz", healthz)
	ready := readyz(func(ctx context.Context) error {
		if err := live.hosts.readyErr(); err != nil {
			return err
		}
		return o.Ready(ctx)
	}, o.ReadyTimeout, o.Logger)
	r.Get("/readyz", ready)
	r.Head("/readyz", ready)
	r.Mount(APIBasePath, o.API)
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) { WriteAPIError(w, http.StatusNotFound) })
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) { WriteAPIError(w, http.StatusMethodNotAllowed) })
	return &Server{
		opts: Options{
			Logger:            o.Logger,
			ReadHeaderTimeout: o.ReadHeaderTimeout,
			ReadTimeout:       o.ReadTimeout,
			WriteTimeout:      o.WriteTimeout,
			IdleTimeout:       o.IdleTimeout,
			MaxHeaderBytes:    apiMaxHeaderBytes,
			OwnProxy:          o.JoinMode,
			JoinMode:          o.JoinMode,
		},
		handler: r,
		live:    live,
	}, nil
}

// apiLogPath is the access-log form of an external API path: a path that
// may carry a token (someone put it into the URL) is never logged, others
// are bounded like serve's.
func apiLogPath(path string) string {
	if apikey.Contains(path) {
		return "[redacted]"
	}
	return logPath(path, nil)
}

// APIError is the JSON body of every external API error.
type APIError struct {
	// Error is a stable machine-readable code.
	Error string `json:"error"`
	// Message is a fixed human-readable text (never request data).
	Message string `json:"message"`
}

// apiErrorCodes are the codes of the generic errors (WriteAPIError).
var apiErrorCodes = map[int]string{
	http.StatusBadRequest:            "invalid_request",
	http.StatusUnauthorized:          "unauthenticated",
	http.StatusNotFound:              "not_found",
	http.StatusMethodNotAllowed:      "method_not_allowed",
	http.StatusRequestEntityTooLarge: "body_too_large",
	http.StatusUnsupportedMediaType:  "unsupported_media_type",
	http.StatusTooManyRequests:       "rate_limited",
	http.StatusInternalServerError:   "internal_error",
	http.StatusServiceUnavailable:    "unavailable",
}

// WriteAPIError writes the generic JSON error of status (no internal
// details).
func WriteAPIError(w http.ResponseWriter, status int) {
	code, ok := apiErrorCodes[status]
	if !ok {
		code = "error"
	}
	WriteAPIErrorCode(w, status, code, http.StatusText(status))
}

// WriteAPIErrorCode writes a JSON error with a specific code and fixed
// message.
func WriteAPIErrorCode(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, APIError{Error: code, Message: message})
}

// WriteJSON writes v as the JSON response with status. A write error means
// the client went away.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte(`{"error":"internal_error","message":"Internal Server Error"}`)
	}
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	h.Del("Content-Length")
	w.WriteHeader(status)
	if _, err := w.Write(append(body, '\n')); err != nil {
		return
	}
}
