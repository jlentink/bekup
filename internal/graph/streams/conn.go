package streams

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/graph/apierror"
)

// ErrClosed reports that the stream is ending (session ended, access
// changed, check failed, shutdown, client gone).
var ErrClosed = errors.New("streams: stream closed")

// SessionNotice tells a sessionEvents subscriber that one of its user's
// sessions changed (SessionID is uuid.Nil when all of them were revoked).
type SessionNotice struct {
	Kind      auth.SessionEventKind
	SessionID uuid.UUID
}

// Conn is one event stream of an authenticated session.
type Conn struct {
	hub    *Hub
	id     auth.Principal // the principal of the stream request: identity fields only
	events chan auth.SessionEvent
	resync chan time.Duration
	done   chan struct{}

	checkMu sync.Mutex // serializes synchronous session checks

	mu sync.Mutex
	// p is the current principal: the request's, refreshed by every
	// successful re-validation (step-up and second-factor times).
	p         auth.Principal
	closed    bool
	end       *apierror.Error
	lastOK    time.Time
	cancel    context.CancelCauseFunc
	started   bool
	listeners map[chan SessionNotice]struct{}
}

func newConn(h *Hub, p auth.Principal) *Conn {
	return &Conn{
		hub:    h,
		id:     p,
		p:      p,
		events: make(chan auth.SessionEvent, eventBuffer),
		resync: make(chan time.Duration, 1),
		done:   make(chan struct{}),
		// The session middleware resolved the principal for this request.
		lastOK:    time.Now(),
		listeners: map[chan SessionNotice]struct{}{},
	}
}

type connKey struct{}

// FromContext returns the stream of a subscription operation (nil for
// plain POST requests).
func FromContext(ctx context.Context) *Conn {
	c, ok := ctx.Value(connKey{}).(*Conn)
	if !ok {
		return nil
	}
	return c
}

// Principal is the stream's caller as of the last successful session
// check: identity, role, AAL and restrictions never change (a change ends
// the stream with FORBIDDEN); the step-up and second-factor times follow
// the session.
func (c *Conn) Principal() auth.Principal {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.p
}

// Start starts the session watcher and returns the stream context: it
// carries the Conn and is canceled when the stream must end. Call once.
// A stream terminated between Acquire and Start (e.g. by the shutdown's
// closeAll, which had no cancel function to call yet) starts canceled.
func (c *Conn) Start(ctx context.Context) context.Context {
	ctx, cancel := context.WithCancelCause(context.WithValue(ctx, connKey{}, c))
	c.mu.Lock()
	c.cancel = cancel
	c.started = true
	closed := c.closed
	c.mu.Unlock()
	if closed {
		cancel(ErrClosed)
	}
	go c.watch(ctx)
	return ctx
}

// Release stops the watcher and frees the stream slot. Call it when the
// response is finished (also when the stream never started).
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

// Closed reports whether the stream is ending.
func (c *Conn) Closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// End returns why the server ended the stream (nil: it did not, e.g. the
// client went away or the subscription completed). The transport sends it
// as the final error event.
func (c *Conn) End() *apierror.Error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.end
}

// terminate records end and cancels the stream context (the subscription
// resolver's channel then closes and the transport sends end).
func (c *Conn) terminate(end *apierror.Error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed, c.end = true, end
	cancel := c.cancel
	c.mu.Unlock()
	c.hub.o.Logger.Info("event stream ended by the server",
		slog.String("end_code", string(end.Code)),
		slog.String("user_id", c.id.UserID.String()), slog.String("session_id", c.id.SessionID.String()))
	if cancel != nil {
		cancel(ErrClosed)
	}
}

// EnsureValid is the per-event session check: nil when the last
// successful check is younger than graphql.stream_session_check_ttl or a
// new check passes; ErrClosed otherwise (the stream is then ending).
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

// Watching reports whether a subscription resolver listens for session
// notices (tests wait for it).
func (c *Conn) Watching() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.listeners) > 0
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

// revalidate checks the session and ends the stream when it ended
// (UNAUTHENTICATED), its user, role, AAL or a restriction changed
// (FORBIDDEN) or the check failed (UNAVAILABLE, fail closed).
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
		c.terminate(ErrSessionEnded)
	case err != nil:
		c.hub.o.Logger.WarnContext(ctx, "event stream session check failed", slog.Any("error", err),
			slog.String("session_id", c.id.SessionID.String()))
		c.terminate(ErrCheckFailed)
	case accessChanged(c.id, np):
		c.terminate(ErrAccessChanged)
	default:
		c.mu.Lock()
		c.p = np
		c.lastOK = time.Now()
		c.mu.Unlock()
	}
}

// accessChanged reports whether the re-validated principal np differs
// from the stream request's principal in anything that decides access:
// identity, role, AAL or a restriction (pending second factor, temporary
// password, required 2FA enrollment).
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

// handleEvent reacts to a session event of this stream's session or user.
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
		// the client opens the stream again with it. Harmless if the
		// rotation rolls back: the new stream then uses the old, still
		// valid cookie.
		c.terminate(ErrSessionRotated)
		return false
	}
	c.check(ctx)
	return !c.Closed()
}

// watch is the stream's session watcher: a periodic check, a check on
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
