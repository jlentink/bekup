package resolvers

import (
	"context"
	"errors"

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

// sessionsFeed is the session half of accountEvents: notices about the
// caller's own sessions, each re-authorized at delivery
// (account.sessions_view) after the stream's session check.
func (r *Resolver) sessionsFeed(ctx context.Context, c *streams.Conn) streams.Feed[streams.SessionNotice, *model.SessionEvent] {
	self := c.Principal().SessionID
	return streams.Feed[streams.SessionNotice, *model.SessionEvent]{
		Src:   c.WatchSessions(ctx),
		Check: streams.Authorize[streams.SessionNotice](r.Authz, authz.OpAccountSessionsView, nil),
		Conv: func(_ context.Context, n streams.SessionNotice) (*model.SessionEvent, bool) {
			return sessionEventOf(n, self)
		},
	}
}

// accountEvents is the account stream (D-197): the caller's session
// notices and new inbox notifications in one stream, each kind checked
// with its own operation per event. Without the notification service
// (or bus) only session notices are sent; the terminal codes come from
// the stream itself (streams.Conn).
func (r *Resolver) accountEvents(ctx context.Context) (<-chan *model.AccountEvent, error) {
	c := streams.FromContext(ctx)
	if c == nil {
		return nil, errNotStream
	}
	sessions := r.sessionsFeed(ctx, c)
	inbox, err := r.inboxFeed(ctx, c.Principal().UserID)
	switch {
	case errors.Is(err, errNotificationsNotConfigured):
		inbox = streams.Feed[*inboxEvent, *model.Notification]{}
	case err != nil:
		return nil, err
	}
	if inbox.Src == nil {
		// Never selected; keeps Combine's checks total.
		inbox.Check = func(context.Context, *inboxEvent) error { return errNotificationNotFound }
		inbox.Conv = func(context.Context, *inboxEvent) (*model.Notification, bool) { return nil, false }
	}
	f := streams.Combine(ctx, sessions, inbox,
		func(s *model.SessionEvent) *model.AccountEvent { return &model.AccountEvent{Session: s} },
		func(n *model.Notification) *model.AccountEvent { return &model.AccountEvent{Notification: n} },
		streams.CombineOptions[streams.SessionNotice]{})
	return f.Deliver(ctx), nil
}

// activityUpdated is the dashboard stream (D-197): every run (as an
// unfiltered runUpdated) and every restore (as restoresUpdated) the
// caller may view, each event re-loaded and authorized with its own
// operation at delivery.
func (r *Resolver) activityUpdated(ctx context.Context) (<-chan *model.ActivityEvent, error) {
	if streams.FromContext(ctx) == nil {
		return nil, errNotStream
	}
	fctx, cancel := context.WithCancel(ctx)
	runs, err := r.runUpdatesFeed(fctx, nil, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	restores, err := r.restoresUpdatesFeed(fctx)
	if err != nil {
		cancel()
		return nil, err
	}
	f := streams.Combine(ctx, runs, restores,
		func(v *model.Run) *model.ActivityEvent { return &model.ActivityEvent{Run: v} },
		func(v *model.Restore) *model.ActivityEvent { return &model.ActivityEvent{Restore: v} },
		streams.CombineOptions[*runEvent]{})
	// fctx ends with the stream (ctx); cancel only frees it early.
	context.AfterFunc(ctx, cancel)
	return f.Deliver(ctx), nil
}

// runActivity is the run page stream (D-197): changes of one run and
// its log batches in one stream (run.view and run.logs_view are both
// checked at the start and per event kind). After the done batch it
// sends the run once more (re-loaded and authorized) and completes.
func (r *Resolver) runActivity(ctx context.Context, runID uuid.UUID, afterLogID *string) (<-chan *model.RunActivity, error) {
	if streams.FromContext(ctx) == nil {
		return nil, errNotStream
	}
	fctx, cancel := context.WithCancel(ctx)
	logs, err := r.runLogsFeed(fctx, runID, afterLogID)
	if err != nil {
		cancel()
		return nil, err
	}
	runs, err := r.runUpdatesFeed(fctx, nil, &runID)
	if err != nil {
		cancel()
		return nil, err
	}
	f := streams.Combine(ctx, runs, logs,
		func(v *model.Run) *model.RunActivity { return &model.RunActivity{Run: v} },
		func(v *model.RunLogBatch) *model.RunActivity { return &model.RunActivity{Logs: v} },
		streams.CombineOptions[*runEvent]{EndAfterB: func() (*runEvent, bool) {
			// Stop the run source; the final state is loaded at delivery.
			cancel()
			return &runEvent{runID: runID}, true
		}})
	context.AfterFunc(ctx, cancel)
	return f.Deliver(ctx), nil
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
