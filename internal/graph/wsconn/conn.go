package wsconn

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
)

// Errors of the per-connection checks.
var (
	// ErrClosed: the connection is closing (session ended, access
	// changed, check failed).
	ErrClosed = errors.New("wsconn: connection closed")
	// ErrTooManySubscriptions: graphql.ws_max_subscriptions_per_connection
	// active subscriptions already.
	ErrTooManySubscriptions = errors.New("wsconn: too many active subscriptions on this connection")
	// ErrSubscribeRate: subscribe messages arrive too fast.
	ErrSubscribeRate = errors.New("wsconn: too many subscribe requests")
)

// Socket is the part of the WebSocket the watcher needs: sending a close
// frame with an application close code.
type Socket interface {
	WriteClose(code int, reason string) error
}

// SessionNotice tells a sessionEvents subscriber that one of its user's
// sessions changed (SessionID is uuid.Nil when all of them were revoked).
type SessionNotice struct {
	Kind      auth.SessionEventKind
	SessionID uuid.UUID
}

// Conn is one WebSocket connection of an authenticated session.
type Conn struct {
	hub    *Hub
	id     auth.Principal // the principal of the upgrade: identity fields only
	events chan auth.SessionEvent
	resync chan time.Duration
	done   chan struct{}

	checkMu sync.Mutex // serializes synchronous session checks

	mu sync.Mutex
	// p is the current principal: the upgrade's, refreshed by every
	// successful re-validation (step-up and second-factor times).
	p         auth.Principal
	sock      Socket
	closed    bool
	code      int
	reason    string
	lastOK    time.Time
	cancel    context.CancelCauseFunc
	started   bool
	subs      int
	tokens    float64
	refill    time.Time
	listeners map[chan SessionNotice]struct{}
}

func newConn(h *Hub, p auth.Principal) *Conn {
	now := time.Now()
	return &Conn{
		hub:    h,
		id:     p,
		p:      p,
		events: make(chan auth.SessionEvent, eventBuffer),
		resync: make(chan time.Duration, 1),
		done:   make(chan struct{}),
		// The session middleware resolved the principal for this request.
		lastOK:    now,
		tokens:    float64(h.o.SubscribeBurst),
		refill:    now,
		listeners: map[chan SessionNotice]struct{}{},
	}
}

type connKey struct{}

// FromContext returns the connection of a WebSocket operation (nil for
// HTTP requests).
func FromContext(ctx context.Context) *Conn {
	c, ok := ctx.Value(connKey{}).(*Conn)
	if !ok {
		return nil
	}
	return c
}

// Principal is the connection's caller as of the last successful
// session check: identity, role, AAL and restrictions never change (a
// change closes the connection with 4403); the step-up and second-factor
// times follow the session.
func (c *Conn) Principal() auth.Principal {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.p
}

// Start starts the session watcher and returns the connection context:
// it carries the Conn and is canceled when the connection must close.
// Call once.
func (c *Conn) Start(ctx context.Context) context.Context {
	ctx, cancel := context.WithCancelCause(context.WithValue(ctx, connKey{}, c))
	c.mu.Lock()
	c.cancel = cancel
	c.started = true
	c.mu.Unlock()
	go c.watch(ctx)
	return ctx
}

// Release stops the watcher and frees the connection slot. Call it when
// the socket is gone (also after a failed upgrade).
func (c *Conn) Release() {
	c.mu.Lock()
	cancel, started := c.cancel, c.started
	c.closed = true
	c.mu.Unlock()
	if started {
		cancel(ErrClosed)
		<-c.done
	}
	c.hub.release(c)
}

// Attach records the accepted socket. A connection that was terminated
// before the upgrade finished is closed at once with its code.
func (c *Conn) Attach(s Socket) {
	c.mu.Lock()
	c.sock = s
	closed, code, reason := c.closed, c.code, c.reason
	c.mu.Unlock()
	if closed && code != 0 {
		c.writeClose(s, code, reason)
	}
}

// Closed reports whether the connection is closing.
func (c *Conn) Closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// CloseCode returns the close code chosen by the watcher (0: none yet).
func (c *Conn) CloseCode() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.code
}

// terminate closes the socket with code and cancels the connection
// context (gqlgen then tears the connection down; its own close frame
// comes too late to replace ours).
func (c *Conn) terminate(code int, reason string) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed, c.code, c.reason = true, code, reason
	sock, cancel := c.sock, c.cancel
	c.mu.Unlock()
	c.hub.o.Logger.Info("websocket connection closed by the server",
		slog.Int("close_code", code), slog.String("reason", reason),
		slog.String("user_id", c.id.UserID.String()), slog.String("session_id", c.id.SessionID.String()))
	if sock != nil {
		c.writeClose(sock, code, reason)
	}
	if cancel != nil {
		cancel(ErrClosed)
	}
}

// writeClose sends the close frame; a failure means the peer is gone
// already.
func (c *Conn) writeClose(s Socket, code int, reason string) {
	if err := s.WriteClose(code, reason); err != nil {
		c.hub.o.Logger.Debug("websocket close frame not sent", slog.Any("error", err))
	}
}

// AcquireSubscription reserves one of the connection's subscription slots
// and spends a subscribe token. Call release when the subscription ends.
func (c *Conn) AcquireSubscription() (release func(), err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrClosed
	}
	now := time.Now()
	c.tokens = min(float64(c.hub.o.SubscribeBurst), c.tokens+now.Sub(c.refill).Seconds()*c.hub.o.SubscribeRatePerSec)
	c.refill = now
	if c.tokens < 1 {
		return nil, ErrSubscribeRate
	}
	if c.subs >= c.hub.o.MaxSubscriptions {
		return nil, ErrTooManySubscriptions
	}
	c.tokens--
	c.subs++
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			c.subs--
			c.mu.Unlock()
		})
	}, nil
}

// EnsureValid is the per-event session check: nil when the last
// successful check is younger than graphql.ws_session_check_ttl or a new
// check passes; ErrClosed otherwise (the socket is then closing).
func (c *Conn) EnsureValid(ctx context.Context) error {
	if c.fresh() {
		return nil
	}
	c.checkMu.Lock()
	defer c.checkMu.Unlock()
	if c.fresh() {
		return nil
	}
	c.revalidate(ctx)
	if c.Closed() {
		return ErrClosed
	}
	return nil
}

func (c *Conn) fresh() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed && time.Since(c.lastOK) < c.hub.o.CheckTTL
}

// WatchSessions returns the notices about the user's sessions until ctx
// ends (sessionEvents). Notices that do not fit the buffer are dropped
// (they are hints to reload the session list).
func (c *Conn) WatchSessions(ctx context.Context) <-chan SessionNotice {
	ch := make(chan SessionNotice, sessionNoticeBuffer)
	c.mu.Lock()
	c.listeners[ch] = struct{}{}
	c.mu.Unlock()
	context.AfterFunc(ctx, func() {
		c.mu.Lock()
		delete(c.listeners, ch)
		c.mu.Unlock()
	})
	return ch
}

func (c *Conn) notify(n SessionNotice) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for ch := range c.listeners {
		select {
		case ch <- n:
		default:
		}
	}
}

// enqueue hands a session event to the watcher; when its queue is full the
// watcher re-checks instead.
func (c *Conn) enqueue(ev auth.SessionEvent) {
	select {
	case c.events <- ev:
	default:
		c.requestResync(0)
	}
}

func (c *Conn) requestResync(after time.Duration) {
	select {
	case c.resync <- after:
	default:
	}
}

// revalidate checks the session and terminates the connection when it
// ended (4401), its user or role changed (4403) or the check failed
// (1013, fail closed).
func (c *Conn) revalidate(ctx context.Context) {
	if c.Closed() {
		return
	}
	np, err := c.hub.o.Validator.Check(ctx, c.id.SessionID)
	if ctx.Err() != nil {
		return
	}
	switch {
	case errors.Is(err, auth.ErrNoSession):
		c.terminate(CloseSessionEnded, "session ended")
	case err != nil:
		c.hub.o.Logger.WarnContext(ctx, "websocket session check failed", slog.Any("error", err),
			slog.String("session_id", c.id.SessionID.String()))
		c.terminate(CloseCheckFailed, "session check failed")
	case accessChanged(c.id, np):
		c.terminate(CloseAccessChanged, "access changed")
	default:
		c.mu.Lock()
		c.p = np
		c.lastOK = time.Now()
		c.mu.Unlock()
	}
}

// accessChanged reports whether the re-validated principal np differs
// from the upgrade's principal in anything that decides access: identity,
// role, AAL or a restriction (pending second factor, temporary password,
// required 2FA enrollment).
func accessChanged(old, np auth.Principal) bool {
	return np.UserID != old.UserID || np.SessionID != old.SessionID || np.Role != old.Role || np.AAL != old.AAL ||
		np.MFAPending != old.MFAPending || np.PasswordChangeRequired != old.PasswordChangeRequired ||
		np.EnrollmentRequired != old.EnrollmentRequired
}

func (c *Conn) check(ctx context.Context) {
	c.checkMu.Lock()
	defer c.checkMu.Unlock()
	c.revalidate(ctx)
}

// handleEvent reacts to a session event of this connection's session or
// user.
func (c *Conn) handleEvent(ctx context.Context, ev auth.SessionEvent) (recheck bool) {
	own := ev.SessionID == c.id.SessionID ||
		(ev.Kind == auth.SessionsRevokedAll && ev.UserID == c.id.UserID)
	if ev.UserID == c.id.UserID || own {
		c.notify(SessionNotice{Kind: ev.Kind, SessionID: ev.SessionID})
	}
	if !own {
		return false
	}
	if ev.Kind == auth.SessionRotated {
		// The browser that rotated the session holds the new cookie;
		// the client reconnects with it. Harmless if the rotation rolls
		// back: the reconnect then uses the old, still valid cookie.
		c.terminate(CloseSessionRotated, "session rotated")
		return false
	}
	c.check(ctx)
	return !c.Closed()
}

// watch is the connection's session watcher: a periodic check, a check on
// every session event (with follow-ups, since a local event can precede
// the commit) and a spread-out check after lost events.
func (c *Conn) watch(ctx context.Context) {
	defer close(c.done)
	tick := time.NewTicker(c.hub.o.RevalidateInterval)
	defer tick.Stop()
	var (
		follow    *time.Timer
		followC   <-chan time.Time
		followIdx int
		resync    *time.Timer
		resyncC   <-chan time.Time
	)
	stop := func(t *time.Timer) {
		if t != nil {
			t.Stop()
		}
	}
	defer func() { stop(follow); stop(resync) }()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			c.check(ctx)
		case ev := <-c.events:
			if c.handleEvent(ctx, ev) {
				stop(follow)
				followIdx = 0
				follow = time.NewTimer(followUps[0])
				followC = follow.C
			}
		case <-followC:
			c.check(ctx)
			followIdx++
			if followIdx < len(followUps) && !c.Closed() {
				follow = time.NewTimer(followUps[followIdx] - followUps[followIdx-1])
				followC = follow.C
			} else {
				followC = nil
			}
		case d := <-c.resync:
			if resyncC == nil {
				resync = time.NewTimer(d)
				resyncC = resync.C
			}
		case <-resyncC:
			resyncC = nil
			c.check(ctx)
		}
		if c.Closed() {
			return
		}
	}
}
