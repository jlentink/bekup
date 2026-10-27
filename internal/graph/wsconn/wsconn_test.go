package wsconn

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/authz/authztest"
	"github.com/jlentink/bekup/internal/pubsub"
)

// fakeValidator answers Check from a mutable table.
type fakeValidator struct {
	mu     sync.Mutex
	states map[uuid.UUID]auth.Principal
	err    error
	calls  atomic.Int64
}

func newValidator() *fakeValidator {
	return &fakeValidator{states: map[uuid.UUID]auth.Principal{}}
}

func (v *fakeValidator) set(p auth.Principal) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.states[p.SessionID] = p
}

func (v *fakeValidator) revoke(id uuid.UUID) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.states, id)
}

func (v *fakeValidator) setRole(id uuid.UUID, r auth.Role) {
	v.mu.Lock()
	defer v.mu.Unlock()
	st := v.states[id]
	st.Role = r
	v.states[id] = st
}

func (v *fakeValidator) update(id uuid.UUID, f func(*auth.Principal)) {
	v.mu.Lock()
	defer v.mu.Unlock()
	st := v.states[id]
	f(&st)
	v.states[id] = st
}

func (v *fakeValidator) setErr(err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.err = err
}

func (v *fakeValidator) Check(_ context.Context, id uuid.UUID) (auth.Principal, error) {
	v.calls.Add(1)
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.err != nil {
		return auth.Principal{}, v.err
	}
	st, ok := v.states[id]
	if !ok {
		return auth.Principal{}, auth.ErrNoSession
	}
	return st, nil
}

// fakeSocket records the close frame.
type fakeSocket struct {
	mu     sync.Mutex
	code   int
	reason string
	closed chan struct{}
}

func newSocket() *fakeSocket { return &fakeSocket{closed: make(chan struct{})} }

func (s *fakeSocket) WriteClose(code int, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.code == 0 {
		s.code, s.reason = code, reason
		close(s.closed)
	}
	return nil
}

func (s *fakeSocket) wait(t *testing.T, within time.Duration) int {
	t.Helper()
	select {
	case <-s.closed:
	case <-time.After(within):
		t.Fatalf("socket not closed within %s", within)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.code
}

func (s *fakeSocket) isClosed() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

func newHub(t *testing.T, v Validator, mutate func(*Options)) *Hub {
	t.Helper()
	o := Options{Validator: v, RevalidateInterval: time.Hour, CheckTTL: time.Hour}
	if mutate != nil {
		mutate(&o)
	}
	h, err := NewHub(o)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// open acquires and starts a connection with an attached fake socket.
func open(t *testing.T, h *Hub, p auth.Principal) (*Conn, *fakeSocket, context.Context) {
	t.Helper()
	c, err := h.Acquire(p)
	if err != nil {
		t.Fatal(err)
	}
	ctx := c.Start(t.Context())
	s := newSocket()
	c.Attach(s)
	t.Cleanup(c.Release)
	return c, s, ctx
}

func TestAcquireCaps(t *testing.T) {
	h := newHub(t, newValidator(), func(o *Options) { o.MaxPerUser = 2; o.MaxTotal = 3 })
	a := authztest.Principal(auth.RoleUser)
	b := authztest.Principal(auth.RoleUser)
	conns := make([]*Conn, 0, 3)
	for range 2 {
		c, err := h.Acquire(a)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	if _, err := h.Acquire(a); !errors.Is(err, ErrTooManyForUser) {
		t.Fatalf("third connection of a user: %v, want ErrTooManyForUser", err)
	}
	c, err := h.Acquire(b)
	if err != nil {
		t.Fatal(err)
	}
	conns = append(conns, c)
	if _, err := h.Acquire(authztest.Principal(auth.RoleAdmin)); !errors.Is(err, ErrTooMany) {
		t.Fatalf("connection over the global cap: %v, want ErrTooMany", err)
	}
	conns[0].Release()
	conns[0].Release() // idempotent
	if total, user := h.Count(a.UserID); total != 2 || user != 1 {
		t.Fatalf("after release: total %d user %d", total, user)
	}
	if _, err := h.Acquire(a); err != nil {
		t.Fatalf("slot not freed: %v", err)
	}
	if _, err := h.Acquire(auth.Principal{}); err == nil {
		t.Fatal("invalid principal accepted")
	}
}

func TestRevokeEventClosesImmediately(t *testing.T) {
	v := newValidator()
	h := newHub(t, v, nil)
	p := authztest.Principal(auth.RoleUser)
	v.set(p)
	_, s, _ := open(t, h, p)
	other := authztest.Principal(auth.RoleUser)
	v.set(other)
	_, so, _ := open(t, h, other)

	v.revoke(p.SessionID)
	h.Dispatch(auth.SessionEvent{Kind: auth.SessionRevoked, UserID: p.UserID, SessionID: p.SessionID})
	if code := s.wait(t, time.Second); code != CloseSessionEnded {
		t.Fatalf("close code %d, want %d", code, CloseSessionEnded)
	}
	time.Sleep(50 * time.Millisecond)
	if so.isClosed() {
		t.Fatal("another session's socket was closed")
	}
}

func TestEventBeforeCommitIsFollowedUp(t *testing.T) {
	// A local event can arrive before the revoking transaction commits:
	// the first check still sees the session, a follow-up closes it.
	v := newValidator()
	h := newHub(t, v, nil)
	p := authztest.Principal(auth.RoleUser)
	v.set(p)
	_, s, _ := open(t, h, p)
	h.Dispatch(auth.SessionEvent{Kind: auth.SessionsRevokedAll, UserID: p.UserID})
	time.Sleep(50 * time.Millisecond)
	if s.isClosed() {
		t.Fatal("closed although the session is still valid")
	}
	v.revoke(p.SessionID) // the commit
	if code := s.wait(t, 2*time.Second); code != CloseSessionEnded {
		t.Fatalf("close code %d, want %d", code, CloseSessionEnded)
	}
}

func TestBusEventsReachConnections(t *testing.T) {
	v := newValidator()
	bus := pubsub.NewBus()
	h := newHub(t, v, func(o *Options) { o.Bus = bus })
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := h.Run(ctx); err != nil {
			t.Error(err)
		}
	}()
	defer func() { cancel(); <-done }()
	for bus.Subscribers(pubsub.SessionEvents.Name) == 0 {
		time.Sleep(time.Millisecond)
	}
	p := authztest.Principal(auth.RoleMaintainer)
	v.set(p)
	_, s, _ := open(t, h, p)
	v.revoke(p.SessionID)
	bus.Publish(pubsub.SessionEvents.Name, "garbage") // dropped
	bus.Publish(pubsub.SessionEvents.Name, auth.SessionEvent{Kind: auth.SessionRevoked, UserID: p.UserID, SessionID: p.SessionID}.Encode())
	if code := s.wait(t, time.Second); code != CloseSessionEnded {
		t.Fatalf("close code %d", code)
	}
}

func TestShutdownClosesAll(t *testing.T) {
	v := newValidator()
	h := newHub(t, v, nil)
	p := authztest.Principal(auth.RoleUser)
	v.set(p)
	_, s, _ := open(t, h, p)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := h.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if code := s.wait(t, time.Second); code != CloseGoingAway {
		t.Fatalf("close code %d", code)
	}
}

func TestRotationClosesForReconnect(t *testing.T) {
	v := newValidator()
	h := newHub(t, v, nil)
	p := authztest.Principal(auth.RoleUser)
	v.set(p)
	_, s, ctx := open(t, h, p)
	h.Dispatch(auth.SessionEvent{Kind: auth.SessionRotated, UserID: p.UserID, SessionID: p.SessionID})
	if code := s.wait(t, time.Second); code != CloseSessionRotated {
		t.Fatalf("close code %d", code)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("connection context not canceled")
	}
}

func TestPeriodicCheckFallback(t *testing.T) {
	cases := []struct {
		name   string
		change func(v *fakeValidator, p auth.Principal)
		code   int
	}{
		{"revoked", func(v *fakeValidator, p auth.Principal) { v.revoke(p.SessionID) }, CloseSessionEnded},
		{"role changed", func(v *fakeValidator, p auth.Principal) { v.setRole(p.SessionID, auth.RoleUser) }, CloseAccessChanged},
		{"check failed", func(v *fakeValidator, _ auth.Principal) { v.setErr(errors.New("db down")) }, CloseCheckFailed},
		// M1 review L2: the principal is rebuilt, restrictions included.
		{"enrollment now required", func(v *fakeValidator, p auth.Principal) {
			v.update(p.SessionID, func(np *auth.Principal) { np.EnrollmentRequired = true })
		}, CloseAccessChanged},
		{"temporary password set", func(v *fakeValidator, p auth.Principal) {
			v.update(p.SessionID, func(np *auth.Principal) { np.PasswordChangeRequired = true })
		}, CloseAccessChanged},
		{"aal changed", func(v *fakeValidator, p auth.Principal) {
			v.update(p.SessionID, func(np *auth.Principal) { np.AAL = auth.AAL1 })
		}, CloseAccessChanged},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newValidator()
			// No events at all: only the periodic check notices.
			h := newHub(t, v, func(o *Options) { o.RevalidateInterval = time.Second })
			p := authztest.Principal(auth.RoleAdmin)
			v.set(p)
			_, s, _ := open(t, h, p)
			tc.change(v, p)
			if code := s.wait(t, 3*time.Second); code != tc.code {
				t.Fatalf("close code %d, want %d", code, tc.code)
			}
		})
	}
}

func TestTerminateBeforeAttach(t *testing.T) {
	v := newValidator()
	h := newHub(t, v, nil)
	p := authztest.Principal(auth.RoleUser)
	c, err := h.Acquire(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Release()
	c.Start(t.Context())
	c.terminate(CloseSessionEnded, "session ended")
	s := newSocket()
	c.Attach(s)
	if code := s.wait(t, time.Second); code != CloseSessionEnded {
		t.Fatalf("close code %d", code)
	}
}

func TestEnsureValidCachesForTTL(t *testing.T) {
	v := newValidator()
	h := newHub(t, v, func(o *Options) { o.CheckTTL = 50 * time.Millisecond })
	p := authztest.Principal(auth.RoleUser)
	v.set(p)
	c, s, ctx := open(t, h, p)
	for range 10 {
		if err := c.EnsureValid(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := v.calls.Load(); n != 0 {
		t.Fatalf("%d checks within the TTL, want 0", n)
	}
	time.Sleep(60 * time.Millisecond)
	if err := c.EnsureValid(ctx); err != nil || v.calls.Load() != 1 {
		t.Fatalf("after the TTL: err %v, %d checks", err, v.calls.Load())
	}
	v.revoke(p.SessionID)
	time.Sleep(60 * time.Millisecond)
	if err := c.EnsureValid(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("revoked session: %v", err)
	}
	if code := s.wait(t, time.Second); code != CloseSessionEnded {
		t.Fatalf("close code %d", code)
	}
}

func TestSubscriptionCapAndRate(t *testing.T) {
	v := newValidator()
	h := newHub(t, v, func(o *Options) { o.MaxSubscriptions = 2; o.SubscribeBurst = 3; o.SubscribeRatePerSec = 0.001 })
	p := authztest.Principal(auth.RoleUser)
	c, _, _ := open(t, h, p)
	r1, err := c.AcquireSubscription()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AcquireSubscription(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AcquireSubscription(); !errors.Is(err, ErrTooManySubscriptions) {
		t.Fatalf("third subscription: %v", err)
	}
	r1()
	r1() // idempotent
	if _, err := c.AcquireSubscription(); err != nil {
		t.Fatalf("slot not freed: %v", err)
	}
	if _, err := c.AcquireSubscription(); !errors.Is(err, ErrSubscribeRate) {
		t.Fatalf("burst exhausted: %v", err)
	}
}

// toggleOwnership owns every job while owned is true.
type toggleOwnership struct {
	authz.NoOwnership
	owned atomic.Bool
}

func (o *toggleOwnership) IsJobOwner(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return o.owned.Load(), nil
}

func TestDeliverDropsEventsAfterAccessLoss(t *testing.T) {
	v := newValidator()
	h := newHub(t, v, nil)
	p := authztest.Principal(auth.RoleUser)
	v.set(p)
	_, _, ctx := open(t, h, p)
	own := &toggleOwnership{}
	own.owned.Store(true)
	policy := authz.New(authz.Options{Ownership: own})
	job := uuid.New()
	in := make(chan int)
	check := Authorize(policy, authz.OpRunView, func(context.Context, int) ([]authz.Resource, error) {
		return []authz.Resource{authz.Job{ID: job}}, nil
	})
	out := Deliver(auth.WithPrincipal(ctx, p), in, check, func(_ context.Context, n int) (int, bool) { return n * 10, true })

	in <- 1
	if got := <-out; got != 10 {
		t.Fatalf("got %d", got)
	}
	own.owned.Store(false) // ownership removed
	in <- 2
	in <- 3
	close(in)
	if got, ok := <-out; ok {
		t.Fatalf("event %d delivered after the access was lost", got)
	}
}

func TestDeliverStopsWhenSessionEnds(t *testing.T) {
	v := newValidator()
	h := newHub(t, v, func(o *Options) { o.CheckTTL = time.Millisecond; o.RevalidateInterval = time.Hour })
	p := authztest.Principal(auth.RoleUser)
	v.set(p)
	_, s, ctx := open(t, h, p)
	in := make(chan int, 1)
	out := Deliver(ctx, in, func(context.Context, int) error { return nil }, func(_ context.Context, n int) (int, bool) { return n, true })
	time.Sleep(5 * time.Millisecond)
	v.revoke(p.SessionID)
	in <- 1
	if _, ok := <-out; ok {
		t.Fatal("event delivered for a revoked session")
	}
	if code := s.wait(t, time.Second); code != CloseSessionEnded {
		t.Fatalf("close code %d", code)
	}
}

func TestDeliverOutsideWebSocket(t *testing.T) {
	out := Deliver(t.Context(), make(chan int), func(context.Context, int) error { return nil }, func(_ context.Context, n int) (int, bool) { return n, true })
	if _, ok := <-out; ok {
		t.Fatal("Deliver without a connection must close at once")
	}
}

func TestWatchSessions(t *testing.T) {
	v := newValidator()
	h := newHub(t, v, nil)
	p := authztest.Principal(auth.RoleUser)
	v.set(p)
	c, s, ctx := open(t, h, p)
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch := c.WatchSessions(wctx)
	other := uuid.New()
	h.Dispatch(auth.SessionEvent{Kind: auth.SessionRevoked, UserID: p.UserID, SessionID: other})
	select {
	case n := <-ch:
		if n.SessionID != other || n.Kind != auth.SessionRevoked {
			t.Fatalf("notice %+v", n)
		}
	case <-time.After(time.Second):
		t.Fatal("no notice")
	}
	// Another user's session: nothing.
	h.Dispatch(auth.SessionEvent{Kind: auth.SessionRevoked, UserID: uuid.New(), SessionID: uuid.New()})
	select {
	case n := <-ch:
		t.Fatalf("unexpected notice %+v", n)
	case <-time.After(50 * time.Millisecond):
	}
	if s.isClosed() {
		t.Fatal("closed by another session's event")
	}
}

// A re-validation that finds only new step-up/second-factor times keeps
// the socket open and refreshes the principal used for delivery-time
// authorization (M1 review L2).
func TestRevalidationRefreshesPrincipal(t *testing.T) {
	v := newValidator()
	h := newHub(t, v, func(o *Options) { o.CheckTTL = time.Millisecond })
	p := authztest.Principal(auth.RoleAdmin)
	p.StepUpAt = time.Time{}
	v.set(p)
	c, s, ctx := open(t, h, p)
	stepUp := time.Now().Truncate(time.Second)
	v.update(p.SessionID, func(np *auth.Principal) { np.StepUpAt = stepUp; np.AAL2At = stepUp })
	time.Sleep(5 * time.Millisecond)
	if err := c.EnsureValid(ctx); err != nil {
		t.Fatal(err)
	}
	if s.isClosed() {
		t.Fatal("socket closed for a step-up")
	}
	if got := c.Principal(); !got.StepUpAt.Equal(stepUp) || !got.AAL2At.Equal(stepUp) {
		t.Fatalf("principal not refreshed: %+v", got)
	}
	// Deliver authorizes with the refreshed principal.
	in := make(chan int, 1)
	seen := make(chan auth.Principal, 1)
	out := Deliver(ctx, in, func(ctx context.Context, _ int) error {
		pr, _ := auth.FromContext(ctx)
		seen <- pr
		return nil
	}, func(_ context.Context, v int) (int, bool) { return v, true })
	time.Sleep(5 * time.Millisecond)
	in <- 1
	select {
	case <-out:
	case <-time.After(time.Second):
		t.Fatal("event not delivered")
	}
	if pr := <-seen; !pr.StepUpAt.Equal(stepUp) {
		t.Fatalf("delivery used a stale principal: %+v", pr)
	}
}

// After shutdown no connection can be acquired (M1 code review: an
// upgrade racing the shutdown must not leave a socket open).
func TestAcquireAfterShutdown(t *testing.T) {
	h := newHub(t, newValidator(), nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := h.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Acquire(authztest.Principal(auth.RoleUser)); !errors.Is(err, ErrHubClosed) {
		t.Fatalf("Acquire after shutdown: %v, want ErrHubClosed", err)
	}
}

// Upgrades racing the shutdown either get a slot before closeAll (and are
// then closed by it) or are refused; none stays open.
func TestAcquireRacingShutdown(t *testing.T) {
	for range 20 {
		v := newValidator()
		h := newHub(t, v, nil)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			if err := h.Run(ctx); err != nil {
				t.Error(err)
			}
		}()
		var wg sync.WaitGroup
		var mu sync.Mutex
		var conns []*Conn
		for range 10 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				p := authztest.Principal(auth.RoleUser)
				v.set(p)
				c, err := h.Acquire(p)
				if err != nil {
					if !errors.Is(err, ErrHubClosed) {
						t.Error(err)
					}
					return
				}
				c.Start(t.Context())
				mu.Lock()
				conns = append(conns, c)
				mu.Unlock()
			}()
		}
		cancel()
		wg.Wait()
		<-done
		for _, c := range conns {
			if !c.Closed() {
				t.Fatal("a connection acquired during shutdown is still open")
			}
			c.Release()
		}
	}
}
