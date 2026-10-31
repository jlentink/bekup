package db

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/logging"
)

// DefaultApplicationName is the application_name reported to Postgres when
// PoolOptions.ApplicationName is empty.
const DefaultApplicationName = "bekup"

// idleInTransactionTimeout ends sessions that sit idle inside a transaction,
// so a stuck caller cannot hold locks (for example the audit chain lock)
// forever.
const idleInTransactionTimeout = time.Minute

// ErrInvalidURL is returned for connection URLs that cannot be parsed. The
// parser's own error is withheld because it can echo the URL, password
// included.
var ErrInvalidURL = errors.New("database URL is not a valid PostgreSQL connection string (details withheld: they could contain the password)")

// PoolOptions configures NewPool. Zero values fall back to the defaults of
// config.DefaultBekup().Database.
type PoolOptions struct {
	MaxConns         int32
	MinConns         int32
	ConnMaxLifetime  time.Duration
	ConnMaxIdleTime  time.Duration
	ConnectTimeout   time.Duration
	StatementTimeout time.Duration
	// ApplicationName is reported as application_name (e.g. "bekup-serve").
	ApplicationName string
	// TLS is the required TLS policy (D-100); the zero value is rejected.
	TLS TLSOptions
	// Registry receives the URL and password so they are scrubbed from logs
	// (nil: logging.DefaultRegistry). They stay registered for the lifetime
	// of the process, like the pool.
	Registry *logging.Registry
}

// PoolOptionsFromConfig maps the [database] config section to PoolOptions,
// reading the database.tls_* files (LoadTLSOptions).
func PoolOptionsFromConfig(cfg *config.Bekup, applicationName string) (PoolOptions, error) {
	tlsOpts, err := LoadTLSOptions(cfg)
	if err != nil {
		return PoolOptions{}, err
	}
	c := &cfg.Database
	return PoolOptions{
		MaxConns:         clampInt32(c.MaxConns),
		MinConns:         clampInt32(c.MinConns),
		ConnMaxLifetime:  c.ConnMaxLifetime.D(),
		ConnMaxIdleTime:  c.ConnMaxIdleTime.D(),
		ConnectTimeout:   c.ConnectTimeout.D(),
		StatementTimeout: c.StatementTimeout.D(),
		ApplicationName:  applicationName,
		TLS:              tlsOpts,
	}, nil
}

func clampInt32(n int) int32 {
	switch {
	case n < 0:
		return 0
	case n > 1<<30:
		return 1 << 30
	default:
		return int32(n)
	}
}

// NewPool builds the runtime connection pool (role bekup_app) from the
// credentials (config.Bekup.AppDBCredentials; the caller zeroes them). The
// URL and the password are registered with the log redaction registry
// before anything else happens and are never logged. NewPool pings the
// database.
func NewPool(ctx context.Context, creds config.DBCredentials, opts PoolOptions) (*pgxpool.Pool, error) {
	cfg, err := poolConfig(creds, opts)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	return pool, nil
}

// poolConfig builds the connection config (connConfig, the one builder)
// and applies opts; it does not connect.
func poolConfig(creds config.DBCredentials, opts PoolOptions) (*pgxpool.Config, error) {
	cc, err := connConfig(creds, opts.TLS, opts.Registry)
	if err != nil {
		return nil, err
	}
	// pgxpool only accepts a config from its own parser; the neutral
	// string carries no settings that survive (the connection config is
	// replaced and the pool settings are set below).
	cfg, err := pgxpool.ParseConfig(neutralConnString)
	if err != nil {
		return nil, fmt.Errorf("build database pool config (check PGSERVICE/PGSERVICEFILE in the environment): %w", err)
	}
	cfg.ConnConfig = cc

	def := config.DefaultBekup().Database
	cfg.MaxConns = pick(opts.MaxConns, clampInt32(def.MaxConns))
	cfg.MinConns = opts.MinConns
	if cfg.MinConns > cfg.MaxConns {
		cfg.MinConns = cfg.MaxConns
	}
	cfg.MaxConnLifetime = pick(opts.ConnMaxLifetime, def.ConnMaxLifetime.D())
	cfg.MaxConnIdleTime = pick(opts.ConnMaxIdleTime, def.ConnMaxIdleTime.D())
	cfg.ConnConfig.ConnectTimeout = pick(opts.ConnectTimeout, def.ConnectTimeout.D())

	rp := cfg.ConnConfig.RuntimeParams
	rp["application_name"] = pick(opts.ApplicationName, DefaultApplicationName)
	rp["statement_timeout"] = millis(pick(opts.StatementTimeout, def.StatementTimeout.D()))
	rp["idle_in_transaction_session_timeout"] = millis(idleInTransactionTimeout)
	// The audit chain trigger requires READ COMMITTED; make it explicit.
	rp["default_transaction_isolation"] = "read committed"
	return cfg, nil
}

func registerPassword(reg *logging.Registry, cc *pgx.ConnConfig) {
	// Fallback hosts share the same password.
	if cc.Password != "" {
		reg.Register([]byte(cc.Password))
	}
}

func pick[T comparable](v, fallback T) T {
	var zero T
	if v == zero {
		return fallback
	}
	return v
}

func millis(d time.Duration) string {
	return strconv.FormatInt(d.Milliseconds(), 10)
}
