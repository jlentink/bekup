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
	"github.com/jlentink/bekup/internal/calendar"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/destinations"
	"github.com/jlentink/bekup/internal/downloads"
	"github.com/jlentink/bekup/internal/graph"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/restore"
	"github.com/jlentink/bekup/internal/runnerclient"
	"github.com/jlentink/bekup/internal/runstate"
)

// ApplicationName is reported to Postgres by serve's pool,
// WorkerApplicationName by the pool of `bekup worker`.
const (
	ApplicationName       = "bekup-serve"
	WorkerApplicationName = "bekup-worker"
)

// Mode selects what a process runs.
type Mode int

const (
	// ModeServe runs the HTTP server, the scheduler (scheduler.enabled)
	// and, unless Options.NoWorker, the River workers.
	ModeServe Mode = iota
	// ModeWorker (`bekup worker`) runs only the River workers: no HTTP, no
	// scheduler, same startup checks.
	ModeWorker
)

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
	// Mode is ModeServe (default) or ModeWorker.
	Mode Mode
	// NoWorker (serve --no-worker) runs serve without River workers; jobs
	// are still inserted and the scheduler still runs.
	NoWorker bool
	// QueueFetchPollInterval overrides River's poll interval (tests).
	QueueFetchPollInterval time.Duration
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
	// queue is the River client (task 3.4): workers unless NoWorker,
	// insert-only otherwise.
	queue *queue.Client
	// policy is the central authz policy (tasks 1.4, 3.5), shared by the
	// GraphQL layer and the services that re-check their caller.
	policy *authz.Policy
	// jobKeys and jobs are the job encryption and job services (tasks
	// 3.2, 3.5).
	jobKeys *jobkeys.Service
	jobs    *jobs.Service
	// downloads serves download tickets (task 3.9, serve only).
	downloads *downloads.Service
	// restore stages and verifies restores (task 4.1).
	restore *restore.Service
	// restoreAPI backs the restore GraphQL fields (task 4.5).
	restoreAPI *restore.API
	// testRestoreAPI backs the test-restore GraphQL fields (task 4.6).
	testRestoreAPI *restore.TestRestoreAPI
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
	if opts.Mode == ModeWorker && opts.NoWorker {
		return nil, errors.New("app: a worker process cannot disable its workers")
	}
	if err := a.openDB(ctx); err != nil {
		return nil, err
	}
	a.auditor = audit.New(a.pool, audit.Options{})
	if opts.Mode == ModeWorker {
		if err := a.newWorkerMode(ctx); err != nil {
			return nil, err
		}
		return a, nil
	}
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
	a.policy = a.newPolicy()
	// The job keys come before the queue (the backup worker reads them),
	// the queue before the job service (runJobNow inserts runs).
	if err := a.newJobKeys(); err != nil {
		return nil, err
	}
	// The restore service before the queue: the restore worker uses it.
	if err := a.newRestore(); err != nil {
		return nil, err
	}
	if err := a.newQueue(!opts.NoWorker); err != nil {
		return nil, err
	}
	if err := a.newJobs(); err != nil {
		return nil, err
	}
	if err := a.newDownloads(); err != nil {
		return nil, err
	}
	if err := a.newRestoreAPI(); err != nil {
		return nil, err
	}
	if err := a.newTestRestoreAPI(); err != nil {
		return nil, err
	}
	h, err := a.newHTTP(ctx)
	if err != nil {
		return nil, err
	}
	a.components = append(a.components, h, signer, &sessionCleanupComponent{sessions: a.auth.Sessions(), logger: a.opts.Logger},
		downloadCleanupComponent{a.downloads}, restoreHousekeepingComponent{a.restore})
	if err := a.newScheduler(); err != nil {
		return nil, err
	}
	return a, nil
}

// newWorkerMode prepares `bekup worker`: the same database and keyring
// checks as serve, the services the workers need (destinations, the runner
// client and connections, job keys), then only the River workers.
func (a *App) newWorkerMode(ctx context.Context) error {
	if err := a.auditInsecureDBTLS(ctx); err != nil {
		return err
	}
	if err := a.loadKeyring(ctx); err != nil {
		return err
	}
	var err error
	if a.destinations, err = a.newDestinations(); err != nil {
		return err
	}
	if a.connections, err = a.newConnections(); err != nil {
		return err
	}
	a.policy = a.newPolicy()
	if err := a.newJobKeys(); err != nil {
		return err
	}
	if err := a.newRestore(); err != nil {
		return err
	}
	a.components = append(a.components, restoreHousekeepingComponent{a.restore})
	return a.newQueue(true)
}

// openDB connects as bekup_app and refuses to continue unless the role is
// least-privileged (D-099) and the schema matches this binary (D-078).
func (a *App) openDB(ctx context.Context) error {
	cfg := a.opts.Config
	appName := ApplicationName
	if a.opts.Mode == ModeWorker {
		appName = WorkerApplicationName
	}
	poolOpts, err := db.PoolOptionsFromConfig(cfg, appName)
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
	if a.opts.Mode == ModeWorker {
		a.opts.Logger.InfoContext(context.WithoutCancel(ctx), "bekup worker stopped")
	} else {
		a.opts.Logger.InfoContext(context.WithoutCancel(ctx), "bekup serve stopped")
	}
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
	// GET /api/download/{ticket} (task 3.9).
	hopts.Download = a.downloads
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
// policy (task 1.4, with the DB-backed job ownership of task 3.5) and the
// auth service (task 1.5); the session middleware of internal/httpserver
// attaches the principal.
func (a *App) newGraphQL() (*graph.Handler, error) {
	cfg := a.opts.Config
	policy := a.policy
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
		Jobs:            a.jobs,
		JobKeys:         a.jobKeys,
		Runs:            runstate.NewReader(a.pool),
		Bus:             a.subs.bus,
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
		Downloads:     a.downloads,
		Calendar:      calendar.New(a.pool),
		Restores:      a.restoreAPI,
		TestRestores:  a.testRestoreAPI,
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
