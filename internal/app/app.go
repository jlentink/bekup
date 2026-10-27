package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/auth/passkey"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/destinations"
	"github.com/jlentink/bekup/internal/graph"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/runnerclient"
)

// ApplicationName is reported to Postgres by serve's pool.
const ApplicationName = "bekup-serve"

// Component is a long-running part of serve (HTTP server, later River
// workers and the scheduler).
type Component interface {
	// Name identifies the component in logs.
	Name() string
	// Run blocks until ctx is canceled (then shuts down gracefully and
	// returns nil) or the component fails.
	Run(ctx context.Context) error
}

// Options configure New.
type Options struct {
	// Config is the loaded, validated bekup.toml.
	Config *config.Bekup
	// Dev is the effective dev mode.
	Dev bool
	// Logger is the redacting process logger.
	Logger *slog.Logger
	// UI is the embedded SPA (web.DistFS()); nil serves no UI.
	UI fs.FS
	// Listener replaces binding server.listen (tests).
	Listener net.Listener
	// Version is the bekup version reported by Query.serverInfo.
	Version string
}

// App is a started-up serve instance.
type App struct {
	opts    Options
	pool    *pgxpool.Pool
	keyring *crypto.Keyring
	auditor *audit.Auditor
	auth    *auth.Service
	users   *auth.UserAdmin
	// factors aggregates the second-factor checkers (passkeys: task 1.8,
	// TOTP: task 1.7); see secondFactors.
	factors  *auth.SecondFactors
	passkeys *passkey.Service
	// totp is the TOTP / recovery-code service (task 1.7).
	totp *auth.TOTP
	// twoFactorPolicy is the 2FA policy (task 1.9): enrollment check of
	// every request and the last-factor guard.
	twoFactorPolicy *auth.TwoFactorPolicies
	// secondFactor is the two-step login and step-up (task 1.9).
	secondFactor *auth.SecondFactorFlow
	// destinations is the destination service (task 2.12).
	destinations *destinations.Service
	// runner is the mTLS runner client and connections the connection
	// service on top of it (task 2.11).
	runner      *runnerclient.Client
	connections *connections.Service
	// subs is the subscription machinery (tasks 1.10, 2.13).
	subs       subscriptions
	addr       net.Addr
	components []Component
}

// New runs the startup checks and prepares the components. Close the App
// (or call Run, which closes it) when done.
func New(ctx context.Context, opts Options) (_ *App, err error) {
	if opts.Config == nil || opts.Logger == nil {
		return nil, errors.New("app: Config and Logger are required")
	}
	a := &App{opts: opts}
	defer func() {
		if err != nil {
			a.Close()
		}
	}()
	if err := a.openDB(ctx); err != nil {
		return nil, err
	}
	a.auditor = audit.New(a.pool, audit.Options{})
	if err := a.auditInsecureTransport(ctx); err != nil {
		return nil, err
	}
	if err := a.auditInsecureDBTLS(ctx); err != nil {
		return nil, err
	}
	signer, err := a.newAuditHeadSigner(ctx)
	if err != nil {
		return nil, err
	}
	a.subs.bus = pubsub.NewBus()
	if a.auth, err = a.newAuth(); err != nil {
		return nil, err
	}
	if err := a.newUserAdmin(ctx); err != nil {
		return nil, err
	}
	if a.twoFactorPolicy, err = a.newTwoFactorPolicy(); err != nil {
		return nil, err
	}
	// After the 2FA policy: open sockets re-check their principal with
	// the enrollment check.
	if err := a.newSubscriptions(); err != nil {
		return nil, err
	}
	if a.passkeys, err = a.newPasskeys(); err != nil {
		return nil, err
	}
	if a.totp, err = a.newTOTP(a.secondFactors()); err != nil {
		return nil, err
	}
	if a.secondFactor, err = a.newSecondFactorFlow(); err != nil {
		return nil, err
	}
	if a.destinations, err = a.newDestinations(); err != nil {
		return nil, err
	}
	if a.connections, err = a.newConnections(); err != nil {
		return nil, err
	}
	h, err := a.newHTTP(ctx)
	if err != nil {
		return nil, err
	}
	a.components = append(a.components, h, signer, &sessionCleanupComponent{sessions: a.auth.Sessions(), logger: a.opts.Logger})
	return a, nil
}

// openDB connects as bekup_app and refuses to continue unless the role is
// least-privileged (D-099) and the schema matches this binary (D-078).
func (a *App) openDB(ctx context.Context) error {
	cfg := a.opts.Config
	poolOpts, err := db.PoolOptionsFromConfig(cfg, ApplicationName)
	if err != nil {
		return err
	}
	url, err := cfg.DatabaseURL()
	if err != nil {
		return fmt.Errorf("read database URL: %w", err)
	}
	cctx, cancel := context.WithTimeout(ctx, startupTimeout(cfg))
	defer cancel()
	a.pool, err = db.NewPool(cctx, url, poolOpts)
	config.Zero(url)
	if err != nil {
		return err
	}
	if err := db.CheckAppRole(cctx, a.pool); err != nil {
		return fmt.Errorf("refusing to start: database role check failed: %w", err)
	}
	if err := db.CheckSchemaVersion(cctx, a.pool); err != nil {
		return fmt.Errorf("refusing to start: %w", err)
	}
	return nil
}

func startupTimeout(cfg *config.Bekup) time.Duration {
	return max(2*cfg.Database.ConnectTimeout.D(), 10*time.Second)
}

// ready is the /readyz check: the DB answers and the schema still matches
// (a newer `bekup migrate` run makes this instance not ready).
func (a *App) ready(ctx context.Context) error {
	if err := a.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	return db.CheckSchemaVersion(ctx, a.pool)
}

// Addr is the bound HTTP address.
func (a *App) Addr() net.Addr { return a.addr }

// Run runs every component until ctx is canceled or one of them fails, then
// closes the App. It returns the first component error (nil on a clean
// shutdown).
func (a *App) Run(ctx context.Context) error {
	defer a.Close()
	g, gctx := errgroup.WithContext(ctx)
	for _, c := range a.components {
		g.Go(func() error {
			if err := c.Run(gctx); err != nil {
				return fmt.Errorf("%s: %w", c.Name(), err)
			}
			return nil
		})
	}
	err := g.Wait()
	a.opts.Logger.InfoContext(context.WithoutCancel(ctx), "bekup serve stopped")
	return err
}

// Close releases what New acquired; safe to call more than once.
func (a *App) Close() {
	for _, c := range a.components {
		if cl, ok := c.(interface{ close() error }); ok {
			if err := cl.close(); err != nil && !errors.Is(err, net.ErrClosed) {
				a.opts.Logger.Warn("closing component failed", slog.String("component", c.Name()), slog.Any("error", err))
			}
		}
	}
	if a.runner != nil {
		if err := a.runner.Close(); err != nil {
			a.opts.Logger.Warn("closing the runner client failed", slog.Any("error", err))
		}
		a.runner = nil
	}
	if a.pool != nil {
		a.pool.Close()
		a.pool = nil
	}
	if a.keyring != nil {
		a.keyring.Destroy()
		a.keyring = nil
	}
}

// Run is New followed by (*App).Run.
func Run(ctx context.Context, opts Options) error {
	a, err := New(ctx, opts)
	if err != nil {
		return err
	}
	return a.Run(ctx)
}

// --- HTTP component --------------------------------------------------------

type httpComponent struct {
	srv             *http.Server
	ln              net.Listener
	shutdownTimeout time.Duration
	logger          *slog.Logger
	insecure        bool
	baseURL         string
}

func (a *App) newHTTP(ctx context.Context) (*httpComponent, error) {
	cfg := a.opts.Config
	hopts, err := httpserver.OptionsFromConfig(&cfg.Server, a.opts.Dev)
	if err != nil {
		return nil, err
	}
	hopts.Logger = a.opts.Logger
	hopts.UI = a.opts.UI
	hopts.Ready = a.ready
	gql, err := a.newGraphQL()
	if err != nil {
		return nil, err
	}
	hopts.GraphQL = gql
	// Every principal passes the 2FA policy's enrollment check (task 1.9).
	hopts.Sessions.Resolver = httpserver.SessionsResolver{Sessions: a.auth.Sessions(), Enrollment: a.twoFactorPolicy}
	// Mount point: /api (later tasks) stays 404 until its handler is set here.
	s, err := httpserver.New(hopts)
	if err != nil {
		return nil, err
	}
	ln := a.opts.Listener
	if ln == nil {
		var lc net.ListenConfig
		ln, err = lc.Listen(ctx, "tcp", cfg.Server.Listen)
		if err != nil {
			return nil, fmt.Errorf("listen on server.listen %s: %w", cfg.Server.Listen, err)
		}
	}
	a.addr = ln.Addr()
	return &httpComponent{
		srv:             s.NewHTTPServer(cfg.Server.Listen),
		ln:              ln,
		shutdownTimeout: cfg.Server.ShutdownTimeout.D(),
		logger:          a.opts.Logger,
		insecure:        cfg.InsecureTransport(),
		baseURL:         cfg.Server.BaseURL,
	}, nil
}

// newGraphQL builds the /graphql handler (task 1.2) with the central authz
// policy (task 1.4) and the auth service (task 1.5); the session middleware
// of internal/httpserver attaches the principal. Job ownership (task 3.5)
// uses the deny-by-default placeholder of internal/authz until then;
// enrolled second factors come from secondFactors (tasks 1.7/1.8).
func (a *App) newGraphQL() (*graph.Handler, error) {
	cfg := a.opts.Config
	policy := authz.New(authz.Options{StepUpWindow: cfg.Security.StepUpMaxAge.D(), SecondFactors: a.secondFactors()})
	return graph.New(graph.Options{
		Access:   graph.NewPolicyAccess(policy),
		Policy:   policy,
		AuditLog: a.auditor,
		Auth:     a.auth,
		Users:    a.users,
		Passkeys: a.passkeys,
		TOTP:     a.totp,
		Logger:   a.opts.Logger,

		SecondFactor:    a.secondFactor,
		TwoFactorPolicy: a.twoFactorPolicy,
		Destinations:    a.destinations,
		Connections:     a.connections,
		Dev:             a.opts.Dev,
		ServerInfo: model.ServerInfo{
			Version:           a.opts.Version,
			InsecureTransport: cfg.InsecureTransport(),
			DevMode:           a.opts.Dev,
		},
		MaxComplexity: cfg.GraphQL.MaxComplexity,
		MaxDepth:      cfg.GraphQL.MaxDepth,
		MaxBodyBytes:  cfg.GraphQL.MaxBodyBytes,
		Streams:       a.subs.streams,
	})
}

func (h *httpComponent) Name() string { return "http server" }

func (h *httpComponent) close() error { return h.ln.Close() }

func (h *httpComponent) Run(ctx context.Context) error {
	h.logger.InfoContext(ctx, "bekup serve listening",
		slog.String("address", h.ln.Addr().String()),
		slog.String("base_url", h.baseURL),
		slog.Bool("insecure_transport", h.insecure))
	errCh := make(chan error, 1)
	go func() { errCh <- h.srv.Serve(h.ln) }()
	select {
	case err := <-errCh:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}
	h.logger.InfoContext(context.WithoutCancel(ctx), "shutting down HTTP server",
		slog.Duration("timeout", h.shutdownTimeout))
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), h.shutdownTimeout)
	defer cancel()
	err := h.srv.Shutdown(sctx)
	if err != nil {
		h.logger.WarnContext(sctx, "graceful shutdown timed out; closing remaining connections", slog.Any("error", err))
		err = h.srv.Close()
	}
	if serr := <-errCh; serr != nil && !errors.Is(serr, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", serr)
	}
	if err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
