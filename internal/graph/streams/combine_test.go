package streams

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/authz/authztest"
	"github.com/jlentink/bekup/internal/graph/apierror"
)

type combined struct {
	run string
	log string
}

func recvCombined(t *testing.T, out <-chan combined) (combined, bool) {
	t.Helper()
	select {
	case v, ok := <-out:
		return v, ok
	case <-time.After(2 * time.Second):
		t.Fatal("no event within 2s")
		return combined{}, false
	}
}

// TestCombineChecksEachKindWithItsOwnFeed: both kinds arrive on one stream
// and each event is authorized by its own feed's check (here: B loses
// access while A keeps it).
func TestCombineChecksEachKindWithItsOwnFeed(t *testing.T) {
	v := newValidator()
	h := newHub(t, v, nil)
	p := authztest.Principal(auth.RoleUser)
	v.set(p)
	_, _, ctx := open(t, h, p)
	own := &toggleOwnership{}
	own.owned.Store(true)
	policy := authz.New(authz.Options{Ownership: own})
	job := uuid.New()

	a, b := make(chan string), make(chan string)
	fa := Feed[string, string]{
		Src:   a,
		Check: Authorize[string](policy, authz.OpAccountSessionsView, nil),
		Conv:  func(_ context.Context, s string) (string, bool) { return "a:" + s, true },
	}
	fb := Feed[string, string]{
		Src: b,
		Check: Authorize(policy, authz.OpRunLogsView, func(context.Context, string) ([]authz.Resource, error) {
			return []authz.Resource{authz.Job{ID: job}}, nil
		}),
		Conv: func(_ context.Context, s string) (string, bool) { return "b:" + s, s != "skip" },
	}
	f := Combine(ctx, fa, fb,
		func(s string) combined { return combined{run: s} },
		func(s string) combined { return combined{log: s} },
		CombineOptions[string]{})
	out := f.Deliver(ctx)

	a <- "1"
	if got, _ := recvCombined(t, out); got.run != "a:1" || got.log != "" {
		t.Fatalf("A event: %+v", got)
	}
	b <- "1"
	if got, _ := recvCombined(t, out); got.log != "b:1" || got.run != "" {
		t.Fatalf("B event: %+v", got)
	}
	b <- "skip" // conv drops it
	own.owned.Store(false)
	b <- "2" // no longer authorized: dropped
	a <- "2" // still authorized
	if got, _ := recvCombined(t, out); got.run != "a:2" {
		t.Fatalf("expected only the authorized A event, got %+v", got)
	}
	close(a)
	close(b)
	if got, ok := recvCombined(t, out); ok {
		t.Fatalf("stream not closed after both sources closed: %+v", got)
	}
}

func TestCombineEndAfterB(t *testing.T) {
	v := newValidator()
	h := newHub(t, v, nil)
	p := authztest.Principal(auth.RoleUser)
	v.set(p)
	_, _, ctx := open(t, h, p)
	allow := func(context.Context, string) error { return nil }
	id := func(_ context.Context, s string) (string, bool) { return s, true }
	a, b := make(chan string), make(chan string)
	f := Combine(ctx, Feed[string, string]{a, allow, id}, Feed[string, string]{b, allow, id},
		func(s string) combined { return combined{run: s} },
		func(s string) combined { return combined{log: s} },
		CombineOptions[string]{EndAfterB: func() (string, bool) { return "final", true }})
	out := f.Deliver(ctx)
	b <- "done"
	if got, _ := recvCombined(t, out); got.log != "done" {
		t.Fatalf("got %+v", got)
	}
	close(b)
	if got, _ := recvCombined(t, out); got.run != "final" {
		t.Fatalf("final A event: %+v", got)
	}
	if got, ok := recvCombined(t, out); ok {
		t.Fatalf("stream not completed after B ended: %+v", got)
	}
	// A is never read again (its producer stops with ctx).
}

func TestCombineNilSource(t *testing.T) {
	v := newValidator()
	h := newHub(t, v, nil)
	p := authztest.Principal(auth.RoleUser)
	v.set(p)
	_, _, ctx := open(t, h, p)
	allow := func(context.Context, string) error { return nil }
	deny := func(context.Context, string) error { return errors.New("never") }
	id := func(_ context.Context, s string) (string, bool) { return s, true }
	a := make(chan string)
	f := Combine(ctx, Feed[string, string]{a, allow, id}, Feed[string, string]{nil, deny, id},
		func(s string) combined { return combined{run: s} },
		func(s string) combined { return combined{log: s} },
		CombineOptions[string]{EndAfterB: func() (string, bool) { return "", false }})
	out := f.Deliver(ctx)
	a <- "x"
	if got, _ := recvCombined(t, out); got.run != "x" {
		t.Fatalf("got %+v", got)
	}
	close(a)
	if _, ok := recvCombined(t, out); ok {
		t.Fatal("stream not closed")
	}
}

// TestCombineStreamEndsWithSession: the combined stream ends with the
// stream's terminal code like a single one.
func TestCombineStreamEndsWithSession(t *testing.T) {
	v := newValidator()
	h := newHub(t, v, nil)
	p := authztest.Principal(auth.RoleUser)
	v.set(p)
	c, s, ctx := open(t, h, p)
	allow := func(context.Context, SessionNotice) error { return nil }
	conv := func(_ context.Context, n SessionNotice) (string, bool) { return string(n.Kind), true }
	f := Combine(ctx, Feed[SessionNotice, string]{c.WatchSessions(ctx), allow, conv},
		Feed[string, string]{make(chan string), func(context.Context, string) error { return nil },
			func(_ context.Context, s string) (string, bool) { return s, true }},
		func(s string) combined { return combined{run: s} },
		func(s string) combined { return combined{log: s} },
		CombineOptions[SessionNotice]{})
	out := f.Deliver(ctx)
	v.revoke(p.SessionID)
	h.Dispatch(auth.SessionEvent{Kind: auth.SessionRevoked, UserID: p.UserID, SessionID: p.SessionID})
	if code := s.wait(t, 2*time.Second); code != apierror.Unauthenticated {
		t.Fatalf("end code %s", code)
	}
	select {
	case _, ok := <-out:
		if ok {
			t.Fatal("event after the session ended")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("combined stream not closed after the session ended")
	}
}
