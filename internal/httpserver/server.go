package httpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/config"
)

// DevOrigins are the Vite dev server origins accepted in dev mode only (the
// Vite proxy forwards the browser's real Origin; docs/development.md).
var DevOrigins = []string{"http://localhost:5173", "http://127.0.0.1:5173"}

// DefaultRedactedPathPrefixes are path prefixes whose remainder can carry
// one-time values (download tickets, setup and verification links) and is
// therefore never logged.
var DefaultRedactedPathPrefixes = []string{
	"/api/download/",
	"/api/downloads/",
	"/setup/",
	"/verify-email/",
	"/reset-password/",
}

// ReadyFunc reports whether the service can serve traffic (DB reachable,
// schema current). It must honor ctx.
type ReadyFunc func(ctx context.Context) error

// Options configure New. Use OptionsFromConfig for the [server] section and
// fill in the components.
type Options struct {
	// BaseURL is server.base_url; it defines the allowed origin and whether
	// HSTS is sent (https only, D-095).
	BaseURL string
	// Dev additionally allows the Vite dev server origins (DevOrigins).
	Dev bool
	// TrustedProxies are the only peers whose forwarding headers are used.
	TrustedProxies []netip.Prefix
	// MaxBodyBytes caps request bodies.
	MaxBodyBytes int64
	// RateLimit configures the per-client-IP limiter.
	RateLimit RateLimitOptions
	// RedactedPathPrefixes replaces DefaultRedactedPathPrefixes when non-nil.
	RedactedPathPrefixes []string

	// Logger receives access and error logs (required).
	Logger *slog.Logger
	// UI is the built SPA (web.DistFS()); nil serves no UI.
	UI fs.FS
	// Ready backs /readyz (required).
	Ready ReadyFunc
	// ReadyTimeout bounds one readiness check (default 3s).
	ReadyTimeout time.Duration
	// GraphQL is mounted at exactly /graphql (internal/graph); nil: 404.
	GraphQL http.Handler
	// API is mounted at /api (later tasks); nil: 404 placeholder.
	API http.Handler
	// Download serves GET /api/download/{ticket} (task 3.9, D-150) behind
	// the session middleware; nil: 404. The access log never shows the
	// ticket (DefaultRedactedPathPrefixes).
	Download DownloadHandler
	// Sessions enables the session middleware on /graphql and /api (task
	// 1.5); nil: every request there is anonymous and cannot set cookies.
	Sessions *SessionOptions

	// http.Server settings (NewHTTPServer).
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
}

// OptionsFromConfig maps the [server] section; dev is the effective dev mode.
// The caller sets Logger, UI, Ready, the mounted handlers and
// Sessions.Resolver (Sessions comes back with the cookie policy and the
// per-user limit filled in).
func OptionsFromConfig(c *config.Server, dev bool) (Options, error) {
	proxies := make([]netip.Prefix, 0, len(c.TrustedProxies))
	for _, s := range c.TrustedProxies {
		p, err := netip.ParsePrefix(strings.TrimSpace(s))
		if err != nil {
			return Options{}, fmt.Errorf("server.trusted_proxies: invalid CIDR %q", s)
		}
		proxies = append(proxies, p.Masked())
	}
	return Options{
		BaseURL:        c.BaseURL,
		Dev:            dev,
		TrustedProxies: proxies,
		MaxBodyBytes:   c.MaxBodyBytes,
		RateLimit: RateLimitOptions{
			PerSecond:  c.RateLimitPerSecond,
			Burst:      c.RateLimitBurst,
			MaxClients: c.RateLimitMaxClients,
		},
		Sessions: &SessionOptions{
			Cookies: auth.CookiePolicyFor(c.BaseURL),
			UserLimit: RateLimitOptions{
				PerSecond:  c.UserRateLimitPerSecond,
				Burst:      c.UserRateLimitBurst,
				MaxClients: c.RateLimitMaxClients,
			},
		},
		ReadHeaderTimeout: c.ReadHeaderTimeout.D(),
		ReadTimeout:       c.ReadTimeout.D(),
		WriteTimeout:      c.WriteTimeout.D(),
		IdleTimeout:       c.IdleTimeout.D(),
		MaxHeaderBytes:    c.MaxHeaderBytes,
	}, nil
}

// DownloadHandler serves one download ticket (internal/downloads). The
// handler owns the response, including lifting the write deadline for the
// stream (LiftDeadlines).
type DownloadHandler interface {
	ServeDownload(w http.ResponseWriter, r *http.Request, ticket string)
}

// Server is the assembled HTTP handler.
type Server struct {
	opts    Options
	handler http.Handler
}

// New validates opts and builds the router with the middleware chain
// documented in the package comment.
func New(opts Options) (*Server, error) {
	if opts.Logger == nil {
		return nil, errors.New("httpserver: Logger is required")
	}
	if opts.Ready == nil {
		return nil, errors.New("httpserver: Ready is required")
	}
	if opts.MaxBodyBytes <= 0 {
		return nil, errors.New("httpserver: MaxBodyBytes must be positive")
	}
	if opts.ReadyTimeout <= 0 {
		opts.ReadyTimeout = 3 * time.Second
	}
	if opts.RedactedPathPrefixes == nil {
		opts.RedactedPathPrefixes = DefaultRedactedPathPrefixes
	}
	base, err := url.Parse(opts.BaseURL)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return nil, errors.New("httpserver: BaseURL must be an absolute http(s) URL")
	}
	origins := []string{originOf(base)}
	if opts.Dev {
		origins = append(origins, DevOrigins...)
	}
	limiter, err := newRateLimiter(opts.RateLimit)
	if err != nil {
		return nil, err
	}
	spa, err := newSPA(opts.UI)
	if err != nil {
		return nil, err
	}

	r := chi.NewRouter()
	r.Use(
		requestID,
		clientIP(opts.TrustedProxies, &xffWarner{logger: opts.Logger, now: time.Now}),
		accessLog(opts.Logger, opts.RedactedPathPrefixes),
		recoverer(opts.Logger),
		securityHeaders(strings.EqualFold(base.Scheme, "https")),
		rateLimit(limiter),
		originCheck(origins),
		bodyLimit(opts.MaxBodyBytes),
	)
	r.Get("/healthz", healthz)
	r.Head("/healthz", healthz)
	ready := readyz(opts.Ready, opts.ReadyTimeout, opts.Logger)
	r.Get("/readyz", ready)
	r.Head("/readyz", ready)
	// Exactly /graphql; no sub-paths (the GraphQL handler enforces POST +
	// JSON itself, internal/graph).
	// The session middleware runs only on the API routes: static files,
	// health checks and 404s never cost a session lookup.
	withSession := func(h http.Handler) http.Handler { return h }
	if opts.Sessions != nil {
		mw, err := sessionMiddleware(*opts.Sessions, opts.Logger)
		if err != nil {
			return nil, err
		}
		withSession = mw
	}
	r.Handle("/graphql", withSession(orNotFound(opts.GraphQL)))
	r.Handle("/graphql/*", orNotFound(nil))
	api := chi.NewRouter()
	if opts.Download != nil {
		dl := opts.Download
		api.Get("/download/{ticket}", func(w http.ResponseWriter, r *http.Request) {
			dl.ServeDownload(w, r, chi.URLParam(r, "ticket"))
		})
	}
	api.Mount("/", orNotFound(opts.API))
	r.Mount("/api", withSession(api))
	r.NotFound(spa.ServeHTTP)
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed)
	})
	return &Server{opts: opts, handler: r}, nil
}

// Handler returns the root handler.
func (s *Server) Handler() http.Handler { return s.handler }

// NewHTTPServer returns an http.Server for addr with the configured timeouts
// and header limit. WriteTimeout applies to every response; long-lived routes
// must override it per request (see the package comment).
func (s *Server) NewHTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           s.handler,
		ReadHeaderTimeout: s.opts.ReadHeaderTimeout,
		ReadTimeout:       s.opts.ReadTimeout,
		WriteTimeout:      s.opts.WriteTimeout,
		IdleTimeout:       s.opts.IdleTimeout,
		MaxHeaderBytes:    s.opts.MaxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(s.opts.Logger.Handler(), slog.LevelWarn),
	}
}

// originOf returns the serialized origin of u (scheme://host[:port], lower
// case, default port omitted), as browsers send it in the Origin header.
func originOf(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := u.Port()
	if port == "" || (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		return scheme + "://" + host
	}
	return scheme + "://" + host + ":" + port
}

func orNotFound(h http.Handler) http.Handler {
	if h != nil {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound)
	})
}

// writeError writes a short, generic plain-text error body. It never
// includes internal details.
func writeError(w http.ResponseWriter, status int) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Del("Content-Length")
	h.Del("ETag")
	h.Del("Last-Modified")
	writeText(w, status, http.StatusText(status)+"\n")
}

// writeText writes a plain-text response. A write error means the client
// went away; there is nothing left to tell it.
func writeText(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	if _, err := io.WriteString(w, body); err != nil {
		return
	}
}
