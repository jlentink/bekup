package app

import (
	"fmt"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/graph"
	"github.com/jlentink/bekup/internal/graph/streams"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/pubsub"
)

// ListenerApplicationName is reported to Postgres by the LISTEN connection.
const ListenerApplicationName = "bekup-serve-listen"

// subscriptions is the subscription machinery (tasks 1.10, 2.13): the
// in-process bus that the session store publishes to, the LISTEN/NOTIFY
// bridge that carries events from other instances into it, and the hub
// that watches the sessions of open event streams.
type subscriptions struct {
	bus      *pubsub.Bus
	listener *pubsub.Listener
	hub      *streams.Hub
	streams  *graph.StreamOptions
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
		Channels: []pubsub.Channel{pubsub.SessionEvents, pubsub.RunEvents},
		Bus:      a.subs.bus,
		Logger:   a.opts.Logger,
	})
	if err != nil {
		return err
	}
	g := cfg.GraphQL
	a.subs.hub, err = streams.NewHub(streams.Options{
		Validator:          httpserver.SessionsResolver{Sessions: a.auth.Sessions(), Enrollment: a.twoFactorPolicy},
		Bus:                a.subs.bus,
		RevalidateInterval: g.StreamRevalidateInterval.D(),
		CheckTTL:           g.StreamSessionCheckTTL.D(),
		MaxPerSession:      g.StreamMaxPerSession,
		MaxPerUser:         g.StreamMaxPerUser,
		MaxTotal:           g.StreamMaxTotal,
		Logger:             a.opts.Logger,
	})
	if err != nil {
		return err
	}
	origins, err := httpserver.AllowedOrigins(cfg.Server.BaseURL, a.opts.Dev)
	if err != nil {
		return err
	}
	a.subs.streams = &graph.StreamOptions{
		Hub:               a.subs.hub,
		AllowedOrigins:    origins,
		KeepaliveInterval: g.StreamKeepaliveInterval.D(),
	}
	a.components = append(a.components, a.subs.listener, a.subs.hub)
	return nil
}
