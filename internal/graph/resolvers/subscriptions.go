package resolvers

import (
	"context"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/graph/streams"
)

// errNotStream answers a subscription outside an event stream (the
// transport layer already refuses them; defense in depth).
var errNotStream = apierror.New(apierror.Forbidden, "subscriptions are only available as event streams")

// sessionEvents streams notices about the caller's own sessions. Each
// notice is re-authorized at delivery (account.sessions_view) after the
// stream's session check.
func (r *Resolver) sessionEvents(ctx context.Context) (<-chan *model.SessionEvent, error) {
	c := streams.FromContext(ctx)
	if c == nil {
		return nil, errNotStream
	}
	self := c.Principal().SessionID
	src := c.WatchSessions(ctx)
	check := streams.Authorize[streams.SessionNotice](r.Authz, authz.OpAccountSessionsView, nil)
	return streams.Deliver(ctx, src, check, func(_ context.Context, n streams.SessionNotice) (*model.SessionEvent, bool) {
		return sessionEventOf(n, self)
	}), nil
}

func sessionEventOf(n streams.SessionNotice, self uuid.UUID) (*model.SessionEvent, bool) {
	ev := &model.SessionEvent{Current: n.SessionID == self || n.Kind == auth.SessionsRevokedAll}
	switch n.Kind {
	case auth.SessionRevoked:
		ev.Kind = model.SessionEventKindRevoked
	case auth.SessionRotated:
		ev.Kind = model.SessionEventKindRotated
	case auth.SessionsRevokedAll:
		ev.Kind = model.SessionEventKindAllRevoked
	default:
		return nil, false
	}
	if n.SessionID != uuid.Nil {
		id := n.SessionID
		ev.SessionID = &id
	}
	return ev, true
}
