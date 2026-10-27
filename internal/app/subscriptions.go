package app

import (
	"fmt"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/graph"
	"github.com/jlentink/bekup/internal/graph/wsconn"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/pubsub"
)

// ListenerApplicationName is reported to Postgres by the LISTEN connection.
const ListenerApplicationName = "bekup-serve-listen"

// subscriptions is the WebSocket subscription machinery (task 1.10): the
// in-process bus that the session store publishes to, the LISTEN/NOTIFY
// bridge that carries events from other instances into it, and the hub
// that watches the sessions of open sockets.
type subscriptions struct {
	bus      *pubsub.Bus
	listener *pubsub.Listener
	hub      *wsconn.Hub
	ws       *graph.WebSocketOptions
}

// newSubscriptions builds the listener and the hub (after newAuth and the
// 2FA policy: the hub re-checks sessions and rebuilds their principals
// like the session middleware) and registers both as components.
func (a *App) newSubscriptions() error {
	cfg := a.opts.Config
	poolOpts, err := db.PoolOptionsFromConfig(cfg, ListenerApplicationName)
	if err != nil {
		return err
	}
	url, err := cfg.DatabaseURL()
	if err != nil {
		return fmt.Errorf("read database URL: %w", err)
	}
	connect, err := db.NewConnector(url, poolOpts)
	config.Zero(url)
	if err != nil {
		return err
	}
	a.subs.listener, err = pubsub.NewListener(pubsub.ListenerOptions{
		Connect:  connect,
		Channels: []pubsub.Channel{pubsub.SessionEvents},
		Bus:      a.subs.bus,
		Logger:   a.opts.Logger,
	})
	if err != nil {
		return err
	}
	g := cfg.GraphQL
	a.subs.hub, err = wsconn.NewHub(wsconn.Options{
		Validator:          httpserver.SessionsResolver{Sessions: a.auth.Sessions(), Enrollment: a.twoFactorPolicy},
		Bus:                a.subs.bus,
		RevalidateInterval: g.WSRevalidateInterval.D(),
		CheckTTL:           g.WSSessionCheckTTL.D(),
		MaxPerUser:         cfg.Server.MaxWSConnectionsPerUser,
		MaxTotal:           g.WSMaxConnections,
		MaxSubscriptions:   g.WSMaxSubscriptionsPerConnection,
		Logger:             a.opts.Logger,
	})
	if err != nil {
		return err
	}
	origins, err := httpserver.AllowedOrigins(cfg.Server.BaseURL, a.opts.Dev)
	if err != nil {
		return err
	}
	a.subs.ws = &graph.WebSocketOptions{
		Hub:             a.subs.hub,
		AllowedOrigins:  origins,
		InitTimeout:     g.WSInitTimeout.D(),
		PingInterval:    g.WSPingInterval.D(),
		MaxMessageBytes: g.WSMaxMessageBytes,
	}
	a.components = append(a.components, a.subs.listener, a.subs.hub)
	return nil
}
