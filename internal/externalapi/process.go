package externalapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/deployguard"
	"github.com/jlentink/bekup/internal/httpserver"
)

// ApplicationName is the external API's application_name in PostgreSQL.
const ApplicationName = "bekup-external-api"

// roleCheckTTL is how long a successful privilege audit of bekup_external
// counts for /readyz (the schema version is checked on every probe).
const roleCheckTTL = time.Minute

// RunOptions configure Run.
type RunOptions struct {
	Config *config.ExternalAPI
	Logger *slog.Logger
	// Listener replaces listening on listen.address (tests).
	Listener net.Listener
	// Deployment are the compose facts (deployguard.FromEnv): in the
	// own-proxy join shape (BEKUP_LISTEN_SHAPE) with listen.address
	// config.ExternalAPIJoinListen the listener serves only the external
	// API's own address on the proxy network (alias bekup-external-api)
	// and loopback (D-220 amendment 4).
	Deployment deployguard.Deployment
}

// JoinMode reports whether the external API runs in the own-proxy join
// shape: the compose service sets BEKUP_LISTEN_SHAPE and listen.address is
// exactly config.ExternalAPIJoinListen.
func JoinMode(cfg *config.ExternalAPI, dep deployguard.Deployment) bool {
	return dep.ExpectedWildcardListen(cfg.Listen.Address, config.ExternalAPIJoinListen)
}

// Run is `bekup external-api`: it connects as bekup_external, refuses to
// start unless the role holds exactly its allowlisted privileges
// (db.CheckExternalRole) and the schema is the newest embedded migration
// (db.CheckExternalSchemaVersion), then serves the API until ctx ends and
// drains in-flight requests within listen.shutdown_timeout.
func Run(ctx context.Context, o RunOptions) error {
	cfg, log := o.Config, o.Logger
	if cfg == nil || log == nil {
		return errors.New("externalapi: Config and Logger are required")
	}
	pool, err := OpenDB(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer pool.Close()

	join := JoinMode(cfg, o.Deployment)
	srv, err := newServer(cfg, DBStore{DB: pool}, newReadiness(pool).check, log, join)
	if err != nil {
		return err
	}
	ln := o.Listener
	if ln == nil {
		var lc net.ListenConfig
		if ln, err = lc.Listen(ctx, "tcp", cfg.Listen.Address); err != nil {
			return fmt.Errorf("listen on listen.address %s: %w", cfg.Listen.Address, err)
		}
	}
	if join {
		log.InfoContext(ctx, "own-proxy join shape: the external API accepts connections only on its address on the proxy network (alias bekup-external-api) and on loopback; every other connection is closed (D-220)")
	}
	ln = srv.WrapListener(ln)
	hs := srv.NewHTTPServer(cfg.Listen.Address)
	g, gctx := errgroup.WithContext(ctx)
	if len(cfg.Listen.TrustedProxyHosts) > 0 || join {
		hosts := srv.ProxyHosts()
		g.Go(func() error { return hosts.Run(gctx) })
	}
	g.Go(func() error { return serve(gctx, hs, ln, cfg.Listen.ShutdownTimeout.D(), log) })
	return g.Wait()
}

// OpenDB connects as bekup_external and runs the startup checks: the role
// (session_user, attributes, memberships, privilege audit) and the schema
// version. The connection facts (TLS, never credentials) are logged.
func OpenDB(ctx context.Context, cfg *config.ExternalAPI, log *slog.Logger) (*pgxpool.Pool, error) {
	poolOpts, err := db.PoolOptionsFromDatabase(&cfg.Database, ApplicationName)
	if err != nil {
		return nil, err
	}
	poolOpts.TLS.LogSystemBundle(ctx, log)
	creds, err := cfg.DBCredentials()
	if err != nil {
		return nil, fmt.Errorf("read database credentials: %w", err)
	}
	cctx, cancel := context.WithTimeout(ctx, max(2*cfg.Database.ConnectTimeout.D(), 10*time.Second))
	defer cancel()
	pool, err := db.NewPool(cctx, creds, poolOpts)
	creds.Zero()
	if err != nil {
		return nil, err
	}
	if err := CheckDatabase(cctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	conn, err := pool.Acquire(cctx)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("refusing to start: %w", err)
	}
	f := db.ConnTLSFacts(conn.Conn().PgConn(), poolOpts.TLS)
	conn.Release()
	log.InfoContext(ctx, "app database connected as bekup_external",
		slog.String("config_mode", cfg.Database.DatabaseMode()),
		slog.String("tls_mode", f.Mode), slog.Bool("encrypted", f.Encrypted), slog.String("tls_version", f.Version),
		slog.Bool("chain_verified", f.ChainVerified), slog.Bool("host_verified", f.HostVerified),
		slog.Bool("channel_binding", f.ChannelBinding))
	return pool, nil
}

// CheckDatabase is the startup check of the external API: the connection
// logged in as bekup_external holding only its allowlisted privileges, and
// the schema at the newest embedded migration.
func CheckDatabase(ctx context.Context, pool *pgxpool.Pool) error {
	if err := db.CheckExternalRole(ctx, pool); err != nil {
		return fmt.Errorf("refusing to start: database role check failed: %w", err)
	}
	if err := db.CheckExternalSchemaVersion(ctx, pool); err != nil {
		return fmt.Errorf("refusing to start: %w", err)
	}
	return nil
}

// NewServer builds the HTTP server of the external API from the config.
func NewServer(cfg *config.ExternalAPI, store Store, ready httpserver.ReadyFunc, log *slog.Logger) (*httpserver.Server, error) {
	return newServer(cfg, store, ready, log, false)
}

// newServer is NewServer, in the own-proxy join shape with join.
func newServer(cfg *config.ExternalAPI, store Store, ready httpserver.ReadyFunc, log *slog.Logger, join bool) (*httpserver.Server, error) {
	api, err := NewHandler(Options{
		Store:  store,
		Logger: log,
		TokenLimit: httpserver.RateLimitOptions{
			PerSecond: cfg.Limits.TokenRateLimitPerSecond, Burst: cfg.Limits.TokenRateLimitBurst,
			MaxClients: cfg.Limits.RateLimitMaxClients,
		},
		FailedAuth: FailedAuthOptions{
			Limit: cfg.Limits.FailedAuthLimit, Window: cfg.Limits.FailedAuthWindow.D(),
			MaxClients: cfg.Limits.RateLimitMaxClients,
		},
	})
	if err != nil {
		return nil, err
	}
	hopts, err := httpserver.APIOptionsFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	hopts.Logger, hopts.Ready, hopts.API = log, ready, api
	hopts.JoinMode = join
	return httpserver.NewAPIServer(hopts)
}

// readiness is /readyz: the schema version on every probe (it also proves
// the database answers) and the role's privilege audit at most every
// roleCheckTTL.
type readiness struct {
	pool *pgxpool.Pool
	now  func() time.Time

	mu      sync.Mutex
	okUntil time.Time
}

func newReadiness(pool *pgxpool.Pool) *readiness { return &readiness{pool: pool, now: time.Now} }

func (r *readiness) check(ctx context.Context) error {
	if err := db.CheckExternalSchemaVersion(ctx, r.pool); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.now().Before(r.okUntil) {
		return nil
	}
	if err := db.CheckExternalRole(ctx, r.pool); err != nil {
		return err
	}
	r.okUntil = r.now().Add(roleCheckTTL)
	return nil
}

// serve runs hs on ln until ctx ends, then shuts it down gracefully.
func serve(ctx context.Context, hs *http.Server, ln net.Listener, shutdown time.Duration, log *slog.Logger) error {
	log.InfoContext(ctx, "bekup external-api listening", slog.String("address", ln.Addr().String()))
	errCh := make(chan error, 1)
	go func() { errCh <- hs.Serve(ln) }()
	select {
	case err := <-errCh:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}
	log.InfoContext(context.WithoutCancel(ctx), "shutting down the external API", slog.Duration("timeout", shutdown))
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdown)
	defer cancel()
	err := hs.Shutdown(sctx)
	if err != nil {
		log.WarnContext(sctx, "graceful shutdown timed out; closing remaining connections", slog.Any("error", err))
		err = hs.Close()
	}
	if serr := <-errCh; serr != nil && !errors.Is(serr, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", serr)
	}
	if err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
