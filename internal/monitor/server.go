package monitor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/logging"
)

// Server limits (fixed: a scrape is a small GET).
const (
	// MinBearerLen is the minimum length of the metrics bearer credential.
	MinBearerLen       = 32
	readHeaderTimeout  = 5 * time.Second
	readTimeout        = 10 * time.Second
	writeTimeout       = 30 * time.Second
	idleTimeout        = 60 * time.Second
	maxHeaderBytes     = 16 << 10
	scrapeTimeout      = 20 * time.Second
	maxScrapesInFlight = 4
	shutdownTimeout    = 5 * time.Second
)

// ServerOptions configure NewServer.
type ServerOptions struct {
	// Listen is metrics.listen (validated by internal/config, D-098).
	Listen string
	// Listener replaces binding Listen (tests).
	Listener net.Listener
	// Gatherer is the registry to expose (Metrics.Registry).
	Gatherer prometheus.Gatherer
	// BearerFile is metrics.auth_bearer_file ("" = no authentication).
	BearerFile string
	Logger     *slog.Logger
}

// Server is the internal metrics HTTP server: GET /metrics (optionally
// bearer-authenticated) and GET /healthz, nothing else.
type Server struct {
	srv        *http.Server
	ln         net.Listener
	logger     *slog.Logger
	authed     bool
	unregister func()
}

// NewServer reads the bearer credential (if configured) and binds the
// listener, so a bad file or a taken port fails startup.
func NewServer(ctx context.Context, o ServerOptions) (*Server, error) {
	if o.Gatherer == nil || o.Logger == nil {
		return nil, errors.New("monitor: Gatherer and Logger are required")
	}
	s := &Server{logger: o.Logger, unregister: func() {}}
	var want *[sha256.Size]byte
	if o.BearerFile != "" {
		sum, unregister, err := loadBearer(o.BearerFile)
		if err != nil {
			return nil, err
		}
		want, s.unregister, s.authed = &sum, unregister, true
	}
	ln := o.Listener
	if ln == nil {
		var lc net.ListenConfig
		var err error
		ln, err = lc.Listen(ctx, "tcp", o.Listen)
		if err != nil {
			s.unregister()
			return nil, fmt.Errorf("listen on metrics.listen %s: %w", o.Listen, err)
		}
	}
	s.ln = ln
	s.srv = &http.Server{
		Handler:           Handler(o.Gatherer, want, o.Logger),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(o.Logger.Handler(), slog.LevelDebug),
	}
	return s, nil
}

// loadBearer reads the credential file, registers the value with the log
// redaction and returns only its SHA-256 (the bytes are zeroed).
func loadBearer(path string) ([sha256.Size]byte, func(), error) {
	raw, err := config.ReadSecretFile(path)
	if err != nil {
		return [sha256.Size]byte{}, nil, fmt.Errorf("metrics.auth_bearer_file: %w", err)
	}
	defer config.Zero(raw)
	v := bytes.TrimSpace(raw)
	if len(v) < MinBearerLen {
		return [sha256.Size]byte{}, nil, fmt.Errorf("metrics.auth_bearer_file: the credential must be at least %d characters", MinBearerLen)
	}
	for _, c := range v {
		if c <= ' ' || c > '~' {
			return [sha256.Size]byte{}, nil, errors.New("metrics.auth_bearer_file: the credential must be printable ASCII without spaces")
		}
	}
	unregister := logging.DefaultRegistry.Register(v)
	return sha256.Sum256(v), unregister, nil
}

// Handler serves GET/HEAD /metrics from g and GET/HEAD /healthz. With want
// set, /metrics requires "Authorization: Bearer <credential>" whose SHA-256
// equals want (constant-time compare); /healthz never needs it and says
// nothing but "ok".
func Handler(g prometheus.Gatherer, want *[sha256.Size]byte, logger *slog.Logger) http.Handler {
	metrics := promhttp.HandlerFor(g, promhttp.HandlerOpts{
		ErrorLog:            promErrorLog{logger},
		ErrorHandling:       promhttp.ContinueOnError,
		MaxRequestsInFlight: maxScrapesInFlight,
		Timeout:             scrapeTimeout,
	})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if _, err := w.Write([]byte("ok\n")); err != nil {
			return
		}
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		if want != nil && !bearerOK(r.Header.Get("Authorization"), want) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="bekup metrics"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		metrics.ServeHTTP(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		mux.ServeHTTP(w, r)
	})
}

// bearerOK compares the presented credential's SHA-256 with want in
// constant time (hashing first hides the length too).
func bearerOK(header string, want *[sha256.Size]byte) bool {
	const prefix = "bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return false
	}
	got := sha256.Sum256([]byte(strings.TrimSpace(header[len(prefix):])))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

// promErrorLog adapts promhttp's error log (collection failures) to slog.
type promErrorLog struct{ l *slog.Logger }

func (p promErrorLog) Println(v ...any) {
	p.l.Warn("metrics collection failed", slog.String("detail", fmt.Sprint(v...)))
}

// Name implements app.Component.
func (s *Server) Name() string { return "metrics server" }

// Addr is the bound address.
func (s *Server) Addr() net.Addr { return s.ln.Addr() }

// Close closes the listener and forgets the credential.
func (s *Server) Close() error {
	s.unregister()
	return s.ln.Close()
}

// Run serves until ctx is canceled, then shuts down.
func (s *Server) Run(ctx context.Context) error {
	s.logger.InfoContext(ctx, "metrics server listening",
		slog.String("address", s.ln.Addr().String()), slog.Bool("authenticated", s.authed))
	errCh := make(chan error, 1)
	go func() { errCh <- s.srv.Serve(s.ln) }()
	select {
	case err := <-errCh:
		return fmt.Errorf("metrics server: %w", err)
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	err := s.srv.Shutdown(sctx)
	if err != nil {
		err = s.srv.Close()
	}
	if serr := <-errCh; serr != nil && !errors.Is(serr, http.ErrServerClosed) {
		return fmt.Errorf("metrics server: %w", serr)
	}
	return err
}
