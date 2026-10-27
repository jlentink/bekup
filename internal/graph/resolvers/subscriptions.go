package resolvers

import (
	"context"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/graph/wsconn"
)

// errNotWebSocket answers a subscription outside a WebSocket connection
// (the transport layer already refuses them; defense in depth).
var errNotWebSocket = apierror.New(apierror.Forbidden, "subscriptions are only available over WebSocket")

// sessionEvents streams notices about the caller's own sessions. Each
// notice is re-authorized at delivery (account.sessions_view) after the
// connection's session check.
func (r *Resolver) sessionEvents(ctx context.Context) (<-chan *model.SessionEvent, error) {
	c := wsconn.FromContext(ctx)
	if c == nil {
		return nil, errNotWebSocket
	}
	self := c.Principal().SessionID
	src := c.WatchSessions(ctx)
	check := wsconn.Authorize[wsconn.SessionNotice](r.Authz, authz.OpAccountSessionsView, nil)
	return wsconn.Deliver(ctx, src, check, func(_ context.Context, n wsconn.SessionNotice) (*model.SessionEvent, bool) {
		return sessionEventOf(n, self)
	}), nil
}

func sessionEventOf(n wsconn.SessionNotice, self uuid.UUID) (*model.SessionEvent, bool) {
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
