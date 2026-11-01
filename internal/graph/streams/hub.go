// Package streams holds the per-stream state of GraphQL subscriptions
// delivered as Server-Sent Events (task 2.13, superseding the WebSocket
// transport of task 1.10): stream caps, the session watcher that ends a
// stream when its session ends or its access changes, and the
// delivery-time checks subscription resolvers run before sending each
// event. It is transport-agnostic; internal/graph writes the events.
//
// Every stream is one POST /graphql request carrying one subscription
// ("distinct connections" mode of the graphql-sse protocol). A Conn is
// created per stream (Hub.Acquire) with the principal the session
// middleware resolved from the cookie; nothing the client sends later can
// change it.
//
// A stream the server ends gets a final GraphQL error event whose
// extensions.code says why (see Conn.End), then it completes:
//
//	UNAUTHENTICATED the session ended (logout, revoked, expired, user
//	                disabled): sign in again
//	FORBIDDEN       the session's access changed (role, AAL, a
//	                restriction): sign in again
//	SESSION_ROTATED the session was rotated (privilege change): open the
//	                stream again, the browser already has the new cookie
//	UNAVAILABLE     the session could not be checked (database error;
//	                fail closed) or the server is shutting down: retry
//	                later
package streams

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/pubsub"
)

// Terminal errors of a stream (Conn.End), sent as its final error event.
// The messages are shown to users.
var (
	ErrSessionEnded   = apierror.New(apierror.Unauthenticated, "your session ended; sign in again")
	ErrAccessChanged  = apierror.New(apierror.Forbidden, "your access changed; sign in again")
	ErrSessionRotated = apierror.New(apierror.SessionRotated, "your session was renewed; open the stream again")
	ErrCheckFailed    = apierror.New(apierror.Unavailable, "your session could not be checked; try again later")
	ErrShutdown       = apierror.New(apierror.Unavailable, "the server is shutting down; try again later")
)

// Defaults of Options.
const (
	DefaultRevalidateInterval   = 30 * time.Second
	DefaultCheckTTL             = 5 * time.Second
	DefaultMaxPerSession        = 4
	DefaultMaxPerUser           = 10
	DefaultMaxTotal             = 1000
	eventBuffer                 = 16
	maxResyncSpread             = 5 * time.Second
	sessionNoticeBuffer         = 8
	minRevalidateInterval       = time.Second
	followUpAfterEventFirst     = 200 * time.Millisecond
	followUpAfterEventSecond    = time.Second //nolint:revive // "Second" is the ordinal (first, second, third follow-up), not a unit
	followUpAfterEventThird     = 3 * time.Second
	sessionEventSubscriberQueue = 256
)

// followUps are the extra re-checks after a session event that did not
// (yet) invalidate the session: a local event can arrive before the
// revoking transaction commits.
var followUps = []time.Duration{followUpAfterEventFirst, followUpAfterEventSecond, followUpAfterEventThird}

// Errors of Acquire.
var (
	// ErrTooManyForSession: the session is at graphql.stream_max_per_session.
	ErrTooManyForSession = errors.New("streams: too many event streams for this session")
	// ErrTooManyForUser: the user is at graphql.stream_max_per_user.
	ErrTooManyForUser = errors.New("streams: too many event streams for this user")
	// ErrTooMany: the instance is at graphql.stream_max_total.
	ErrTooMany = errors.New("streams: too many event streams")
	// ErrHubClosed: the hub stopped (serve is shutting down); no new
	// streams are accepted.
	ErrHubClosed = errors.New("streams: the server is shutting down")
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
	// periodic and per-event re-checks end streams.
	Bus *pubsub.Bus
	// RevalidateInterval is graphql.stream_revalidate_interval.
	RevalidateInterval time.Duration
	// CheckTTL is graphql.stream_session_check_ttl: how old a successful
	// check may be before an event is delivered without a new one.
	CheckTTL time.Duration
	// MaxPerSession, MaxPerUser and MaxTotal cap concurrent streams.
	MaxPerSession, MaxPerUser, MaxTotal int
	// Logger (nil: discarded). Never receives tokens or payloads.
	Logger *slog.Logger
}

// Hub tracks the open streams of this instance.
type Hub struct {
	o Options

	mu sync.Mutex
	// closed is set when Run ends (shutdown): Acquire refuses from then
	// on, so no stream can slip in after closeAll.
	closed    bool
	total     int
	perUser   map[uuid.UUID]int
	bySession map[uuid.UUID]map[*Conn]struct{}
	byUser    map[uuid.UUID]map[*Conn]struct{}
}

// NewHub validates o and fills in defaults.
func NewHub(o Options) (*Hub, error) {
	if o.Validator == nil {
		return nil, errors.New("streams: Validator is required")
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
	if o.MaxPerSession <= 0 {
		o.MaxPerSession = DefaultMaxPerSession
	}
	o.MaxPerSession = min(o.MaxPerSession, o.MaxPerUser)
	if o.MaxTotal <= 0 {
		o.MaxTotal = DefaultMaxTotal
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
func (h *Hub) Name() string { return "event stream session watcher" }

// Run routes session events from the Bus to the affected streams until
// ctx ends; then it ends every stream with ErrShutdown (http.Server.Shutdown
// would otherwise wait for them until its timeout).
func (h *Hub) Run(ctx context.Context) error {
	defer h.closeAll(ErrShutdown)
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

// Dispatch hands ev to every stream of its session and user.
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

// closeAll stops accepting streams and ends every open one.
func (h *Hub) closeAll(end *apierror.Error) {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	for _, c := range h.conns() {
		c.terminate(end)
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

// resyncAll makes every stream re-check its session soon (events may have
// been lost), spread out to avoid a burst of queries.
func (h *Hub) resyncAll() {
	conns := h.conns()
	spread := min(h.o.RevalidateInterval, maxResyncSpread)
	for _, c := range conns {
		c.requestResync(time.Duration(rand.Int64N(int64(spread)))) //nolint:gosec // G404: load spreading, not security
	}
}

// Count returns the number of open streams (total) and of userID.
func (h *Hub) Count(userID uuid.UUID) (total, user int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.total, h.perUser[userID]
}

// Acquire reserves a stream slot for p. The caller must Release the Conn
// when the stream is over. After shutdown (Run returned) it answers
// ErrHubClosed.
func (h *Hub) Acquire(p auth.Principal) (*Conn, error) {
	if !p.Valid() {
		return nil, errors.New("streams: invalid principal")
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
	if len(h.bySession[p.SessionID]) >= h.o.MaxPerSession {
		return nil, ErrTooManyForSession
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
