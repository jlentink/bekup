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

func TestCombinedSubscriptionsNeedStream(t *testing.T) {
	r := &Resolver{}
	ctx := context.Background()
	if _, err := r.accountEvents(ctx); !apierror.IsCode(err, apierror.Forbidden) {
		t.Fatalf("accountEvents outside an event stream: %v", err)
	}
	if _, err := r.activityUpdated(ctx); !apierror.IsCode(err, apierror.Forbidden) {
		t.Fatalf("activityUpdated outside an event stream: %v", err)
	}
	if _, err := r.runActivity(ctx, uuid.New(), nil); !apierror.IsCode(err, apierror.Forbidden) {
		t.Fatalf("runActivity outside an event stream: %v", err)
	}
}
