package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ConnectFunc opens one dedicated connection.
type ConnectFunc func(ctx context.Context) (*pgx.Conn, error)

// NewConnector returns a ConnectFunc for dedicated, non-pooled connections
// with exactly the settings of NewPool (TLS policy, application name,
// timeouts, log redaction of the URL and password). It is meant for
// LISTEN (internal/pubsub), which needs one long-lived session that a pool
// would recycle. The caller zeroes url; the parsed config keeps what it
// needs.
func NewConnector(url []byte, opts PoolOptions) (ConnectFunc, error) {
	cfg, err := poolConfig(url, opts)
	if err != nil {
		return nil, err
	}
	cc := cfg.ConnConfig
	return func(ctx context.Context) (*pgx.Conn, error) {
		conn, err := pgx.ConnectConfig(ctx, cc.Copy())
		if err != nil {
			return nil, fmt.Errorf("connect to database: %w", err)
		}
		return conn, nil
	}, nil
}
