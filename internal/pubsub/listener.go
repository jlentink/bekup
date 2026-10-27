package pubsub

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Backoff defaults of the Listener.
const (
	DefaultMinBackoff = 500 * time.Millisecond
	DefaultMaxBackoff = 30 * time.Second
	// healthyAfter is how long a connection must have lived for the
	// backoff to start again from MinBackoff.
	healthyAfter = time.Minute
)

// ListenerOptions configure NewListener.
type ListenerOptions struct {
	// Connect opens the dedicated connection (db.NewConnector; required).
	Connect db.ConnectFunc
	// Channels to LISTEN on (at least one).
	Channels []Channel
	// Bus receives every notification (required).
	Bus *Bus
	// Logger receives connection problems (nil: discarded). Payloads are
	// never logged.
	Logger *slog.Logger
	// MinBackoff and MaxBackoff bound the reconnect delay (defaults
	// DefaultMinBackoff, DefaultMaxBackoff).
	MinBackoff, MaxBackoff time.Duration
}

// Listener is the LISTEN/NOTIFY bridge into a Bus.
type Listener struct {
	o         ListenerOptions
	names     map[string]bool
	connected atomic.Bool
	connects  atomic.Int64
}

// NewListener validates o.
func NewListener(o ListenerOptions) (*Listener, error) {
	if o.Connect == nil || o.Bus == nil || len(o.Channels) == 0 {
		return nil, errors.New("pubsub: Connect, Bus and Channels are required")
	}
	names := map[string]bool{}
	for _, c := range o.Channels {
		if c.Listen == nil || Validate(c.Name, "") != nil {
			return nil, fmt.Errorf("pubsub: invalid channel %q", c.Name)
		}
		names[c.Name] = true
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.MinBackoff <= 0 {
		o.MinBackoff = DefaultMinBackoff
	}
	if o.MaxBackoff < o.MinBackoff {
		o.MaxBackoff = max(DefaultMaxBackoff, o.MinBackoff)
	}
	return &Listener{o: o, names: names}, nil
}

// Name identifies the component in logs (internal/app).
func (l *Listener) Name() string { return "pubsub listener" }

// Connected reports whether the LISTEN connection is up.
func (l *Listener) Connected() bool { return l.connected.Load() }

// Connects returns how many times a connection was established (tests).
func (l *Listener) Connects() int64 { return l.connects.Load() }

// Run listens until ctx is canceled (then it returns nil). Connection
// failures are logged and retried with exponential backoff and jitter;
// after every successful (re)connect each channel's subscribers get a
// Resync signal, since notifications sent in between are lost.
func (l *Listener) Run(ctx context.Context) error {
	delay := l.o.MinBackoff
	for {
		start := time.Now()
		err := l.session(ctx)
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if time.Since(start) >= healthyAfter {
			delay = l.o.MinBackoff
		}
		l.o.Logger.WarnContext(ctx, "pubsub listener disconnected; reconnecting",
			slog.Any("error", err), slog.Duration("retry_in", delay))
		t := time.NewTimer(jitter(delay))
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
		delay = min(2*delay, l.o.MaxBackoff)
	}
}

// session runs one connection until it fails or ctx ends.
func (l *Listener) session(ctx context.Context) (err error) {
	conn, err := l.o.Connect(ctx)
	if err != nil {
		return err
	}
	defer func() {
		l.connected.Store(false)
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if cerr := conn.Close(cctx); cerr != nil && err == nil {
			err = cerr
		}
	}()
	q := sqlc.New(conn)
	for _, c := range l.o.Channels {
		if err := c.Listen(ctx, q); err != nil {
			return fmt.Errorf("listen on %s: %w", c.Name, err)
		}
	}
	l.connected.Store(true)
	l.connects.Add(1)
	for _, c := range l.o.Channels {
		l.o.Bus.Resync(c.Name)
	}
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		l.dispatch(ctx, n)
	}
}

func (l *Listener) dispatch(ctx context.Context, n *pgconn.Notification) {
	if !l.names[n.Channel] {
		return
	}
	if err := Validate(n.Channel, n.Payload); err != nil {
		l.o.Logger.WarnContext(ctx, "pubsub notification dropped: invalid payload",
			slog.String("channel", n.Channel), slog.Int("payload_bytes", len(n.Payload)))
		return
	}
	l.o.Bus.Publish(n.Channel, n.Payload)
}

// jitter returns a random delay in [d/2, d].
func jitter(d time.Duration) time.Duration {
	if d <= 1 {
		return d
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(d/2)+1))
	if err != nil {
		return d
	}
	return d/2 + time.Duration(n.Int64())
}
