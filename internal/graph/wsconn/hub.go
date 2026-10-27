// Package wsconn holds the per-connection state of GraphQL WebSocket
// subscriptions (task 1.10, D-108): connection caps, the session watcher
// that closes a socket when its session ends or its access changes, and
// the delivery-time checks subscription resolvers run before sending each
// event.
//
// Close codes (graphql-transport-ws allows 4000-4999 for applications):
//
//	4401 the session ended (logout, revoked, expired, user disabled):
//	     sign in again
//	4403 the session's access changed (role): sign in again
//	4001 the session was rotated (privilege change): reconnect, the
//	     browser already has the new cookie
//	4400 a bad connection_init (credentials in the payload)
//	1013 the session could not be checked (database error; fail closed):
//	     reconnect later
//	1001 the server is shutting down
//
// A Conn is created per upgrade (Hub.Acquire) with the principal the
// session middleware resolved from the cookie; nothing the client sends
// over the socket can change it.
package wsconn

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/pubsub"
)

// Close codes.
const (
	CloseSessionEnded   = 4401
	CloseAccessChanged  = 4403
	CloseSessionRotated = 4001
	CloseBadInit        = 4400
	CloseCheckFailed    = 1013
	CloseGoingAway      = 1001
)

// Defaults of Options.
const (
	DefaultRevalidateInterval   = 30 * time.Second
	DefaultCheckTTL             = 5 * time.Second
	DefaultMaxPerUser           = 5
	DefaultMaxTotal             = 1000
	DefaultMaxSubscriptions     = 20
	DefaultSubscribeRatePerSec  = 5
	DefaultSubscribeBurst       = 20
	eventBuffer                 = 16
	maxResyncSpread             = 5 * time.Second
	sessionNoticeBuffer         = 8
	minRevalidateInterval       = time.Second
	followUpAfterEventFirst     = 200 * time.Millisecond
	followUpAfterEventSecond    = time.Second
	followUpAfterEventThird     = 3 * time.Second
	sessionEventSubscriberQueue = 256
)

// followUps are the extra re-checks after a session event that did not
// (yet) invalidate the session: a local event can arrive before the
// revoking transaction commits.
var followUps = []time.Duration{followUpAfterEventFirst, followUpAfterEventSecond, followUpAfterEventThird}

// Errors of Acquire.
var (
	// ErrTooManyForUser: the user is at graphql.ws_max_connections_per_user.
	ErrTooManyForUser = errors.New("wsconn: too many WebSocket connections for this user")
	// ErrTooMany: the instance is at graphql.ws_max_connections.
	ErrTooMany = errors.New("wsconn: too many WebSocket connections")
	// ErrHubClosed: the hub stopped (serve is shutting down); no new
	// connections are accepted.
	ErrHubClosed = errors.New("wsconn: the server is shutting down")
)

// Validator re-checks a session by id without touching it and rebuilds
// its principal the way the session middleware does for a request (incl.
// the 2FA enrollment check; httpserver.SessionsResolver). It returns
// auth.ErrNoSession when the session ended.
type Validator interface {
	Check(ctx context.Context, sessionID uuid.UUID) (auth.Principal, error)
}

// Options configure NewHub.
type Options struct {
	// Validator re-checks sessions (required).
	Validator Validator
	// Bus delivers session events (pubsub.SessionEvents); nil: only the
	// periodic and per-event re-checks close sockets.
	Bus *pubsub.Bus
	// RevalidateInterval is graphql.ws_revalidate_interval.
	RevalidateInterval time.Duration
	// CheckTTL is graphql.ws_session_check_ttl: how old a successful check
	// may be before an event is delivered without a new one.
	CheckTTL time.Duration
	// MaxPerUser and MaxTotal cap concurrent connections.
	MaxPerUser, MaxTotal int
	// MaxSubscriptions caps active subscriptions per connection.
	MaxSubscriptions int
	// SubscribeRatePerSec and SubscribeBurst bound subscribe messages per
	// connection.
	SubscribeRatePerSec float64
	SubscribeBurst      int
	// Logger (nil: discarded). Never receives tokens or payloads.
	Logger *slog.Logger
}

// Hub tracks the open connections of this instance.
type Hub struct {
	o Options

	mu sync.Mutex
	// closed is set when Run ends (shutdown): Acquire refuses from then
	// on, so no connection can slip in after closeAll.
	closed    bool
	total     int
	perUser   map[uuid.UUID]int
	bySession map[uuid.UUID]map[*Conn]struct{}
	byUser    map[uuid.UUID]map[*Conn]struct{}
}

// NewHub validates o and fills in defaults.
func NewHub(o Options) (*Hub, error) {
	if o.Validator == nil {
		return nil, errors.New("wsconn: Validator is required")
	}
	if o.RevalidateInterval <= 0 {
		o.RevalidateInterval = DefaultRevalidateInterval
	}
	o.RevalidateInterval = max(o.RevalidateInterval, minRevalidateInterval)
	if o.CheckTTL <= 0 {
		o.CheckTTL = DefaultCheckTTL
	}
	o.CheckTTL = min(o.CheckTTL, o.RevalidateInterval)
	if o.MaxPerUser <= 0 {
		o.MaxPerUser = DefaultMaxPerUser
	}
	if o.MaxTotal <= 0 {
		o.MaxTotal = DefaultMaxTotal
	}
	if o.MaxSubscriptions <= 0 {
		o.MaxSubscriptions = DefaultMaxSubscriptions
	}
	if o.SubscribeRatePerSec <= 0 {
		o.SubscribeRatePerSec = DefaultSubscribeRatePerSec
	}
	if o.SubscribeBurst <= 0 {
		o.SubscribeBurst = DefaultSubscribeBurst
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return &Hub{
		o:         o,
		perUser:   map[uuid.UUID]int{},
		bySession: map[uuid.UUID]map[*Conn]struct{}{},
		byUser:    map[uuid.UUID]map[*Conn]struct{}{},
	}, nil
}

// Name identifies the component in logs (internal/app).
func (h *Hub) Name() string { return "websocket session watcher" }

// Run routes session events from the Bus to the affected connections
// until ctx ends; then it closes every connection with 1001 (hijacked
// WebSocket connections are not covered by http.Server.Shutdown).
func (h *Hub) Run(ctx context.Context) error {
	defer h.closeAll(CloseGoingAway, "server shutting down")
	if h.o.Bus == nil {
		<-ctx.Done()
		return nil
	}
	sub := h.o.Bus.Subscribe(pubsub.SessionEvents.Name, sessionEventSubscriberQueue)
	defer sub.Close()
	for {
		select {
		case <-ctx.Done():
			return nil
		case m := <-sub.C():
			ev, err := auth.DecodeSessionEvent(m.Payload)
			if err != nil {
				h.o.Logger.WarnContext(ctx, "malformed session event dropped")
				continue
			}
			h.Dispatch(ev)
		case <-sub.Resync():
			h.resyncAll()
		}
	}
}

// Dispatch hands ev to every connection of its session and user.
func (h *Hub) Dispatch(ev auth.SessionEvent) {
	h.mu.Lock()
	targets := map[*Conn]struct{}{}
	if ev.SessionID != uuid.Nil {
		for c := range h.bySession[ev.SessionID] {
			targets[c] = struct{}{}
		}
	}
	if ev.UserID != uuid.Nil {
		for c := range h.byUser[ev.UserID] {
			targets[c] = struct{}{}
		}
	}
	h.mu.Unlock()
	for c := range targets {
		c.enqueue(ev)
	}
}

// closeAll stops accepting connections and terminates every open one.
func (h *Hub) closeAll(code int, reason string) {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	for _, c := range h.conns() {
		c.terminate(code, reason)
	}
}

func (h *Hub) conns() []*Conn {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*Conn, 0, h.total)
	for _, m := range h.bySession {
		for c := range m {
			out = append(out, c)
		}
	}
	return out
}

// resyncAll makes every connection re-check its session soon (events may
// have been lost), spread out to avoid a burst of queries.
func (h *Hub) resyncAll() {
	conns := h.conns()
	spread := min(h.o.RevalidateInterval, maxResyncSpread)
	for _, c := range conns {
		c.requestResync(time.Duration(rand.Int64N(int64(spread)))) //nolint:gosec // G404: load spreading, not security
	}
}

// Count returns the number of open connections (total) and of userID.
func (h *Hub) Count(userID uuid.UUID) (total, user int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.total, h.perUser[userID]
}

// Acquire reserves a connection slot for p. The caller must Release the
// Conn when the socket is gone. After shutdown (Run returned) it answers
// ErrHubClosed.
func (h *Hub) Acquire(p auth.Principal) (*Conn, error) {
	if !p.Valid() {
		return nil, errors.New("wsconn: invalid principal")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, ErrHubClosed
	}
	if h.total >= h.o.MaxTotal {
		return nil, ErrTooMany
	}
	if h.perUser[p.UserID] >= h.o.MaxPerUser {
		return nil, ErrTooManyForUser
	}
	c := newConn(h, p)
	h.total++
	h.perUser[p.UserID]++
	add(h.bySession, p.SessionID, c)
	add(h.byUser, p.UserID, c)
	return c, nil
}

func (h *Hub) release(c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.bySession[c.id.SessionID][c]; !ok {
		return
	}
	h.total--
	h.perUser[c.id.UserID]--
	if h.perUser[c.id.UserID] <= 0 {
		delete(h.perUser, c.id.UserID)
	}
	remove(h.bySession, c.id.SessionID, c)
	remove(h.byUser, c.id.UserID, c)
}

func add(m map[uuid.UUID]map[*Conn]struct{}, k uuid.UUID, c *Conn) {
	s := m[k]
	if s == nil {
		s = map[*Conn]struct{}{}
		m[k] = s
	}
	s[c] = struct{}{}
}

func remove(m map[uuid.UUID]map[*Conn]struct{}, k uuid.UUID, c *Conn) {
	if s := m[k]; s != nil {
		delete(s, c)
		if len(s) == 0 {
			delete(m, k)
		}
	}
}
