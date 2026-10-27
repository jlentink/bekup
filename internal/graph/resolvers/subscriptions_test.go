package resolvers

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/graph/streams"
)

func TestSessionEventOf(t *testing.T) {
	self, other := uuid.New(), uuid.New()
	cases := []struct {
		n       streams.SessionNotice
		kind    model.SessionEventKind
		current bool
		hasID   bool
	}{
		{streams.SessionNotice{Kind: auth.SessionRevoked, SessionID: other}, model.SessionEventKindRevoked, false, true},
		{streams.SessionNotice{Kind: auth.SessionRevoked, SessionID: self}, model.SessionEventKindRevoked, true, true},
		{streams.SessionNotice{Kind: auth.SessionRotated, SessionID: other}, model.SessionEventKindRotated, false, true},
		{streams.SessionNotice{Kind: auth.SessionsRevokedAll}, model.SessionEventKindAllRevoked, true, false},
	}
	for _, c := range cases {
		ev, ok := sessionEventOf(c.n, self)
		if !ok || ev.Kind != c.kind || ev.Current != c.current || (ev.SessionID != nil) != c.hasID {
			t.Errorf("%+v: got %+v", c.n, ev)
		}
		if c.hasID && *ev.SessionID != c.n.SessionID {
			t.Errorf("%+v: session id %s", c.n, ev.SessionID)
		}
	}
	if _, ok := sessionEventOf(streams.SessionNotice{Kind: "bogus"}, self); ok {
		t.Error("unknown kind converted")
	}
}

func TestSessionEventsNeedsStream(t *testing.T) {
	r := &Resolver{}
	if _, err := r.sessionEvents(context.Background()); !apierror.IsCode(err, apierror.Forbidden) {
		t.Fatalf("outside an event stream: %v", err)
	}
}
