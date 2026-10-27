package wsconn

import (
	"context"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
)

// Deliver is the event pump of a subscription resolver. For every event
// from in it first re-validates the connection's session
// (Conn.EnsureValid: cached for graphql.ws_session_check_ttl), then runs
// check (the delivery-time authorization, see Authorize) and finally conv,
// both with the connection's current principal (Conn.Principal) in ctx;
// events failing check or conv are dropped, so a caller who loses access
// stops receiving events without the subscription failing visibly. The
// returned channel closes when in closes, ctx ends or the session check
// fails (the socket is then closing). Outside a WebSocket connection it
// is closed at once.
//
// Event sources must carry identifiers only and conv (or check) must load
// what it sends with the caller's authorization: never forward data that
// was prepared for someone else.
func Deliver[In, Out any](ctx context.Context, in <-chan In, check func(context.Context, In) error, conv func(context.Context, In) (Out, bool)) <-chan Out {
	out := make(chan Out)
	c := FromContext(ctx)
	if c == nil || check == nil || conv == nil {
		close(out)
		return out
	}
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-in:
				if !ok {
					return
				}
				if c.EnsureValid(ctx) != nil {
					return
				}
				// Authorize with the principal of the latest session
				// check, not the one of the upgrade request.
				ectx := auth.WithPrincipal(ctx, c.Principal())
				if check(ectx, ev) != nil {
					continue
				}
				v, ok := conv(ectx, ev)
				if !ok {
					continue
				}
				select {
				case out <- v:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

// Authorize returns a Deliver check that runs the central policy for op
// with the connection's principal for every event (resources may be nil;
// owner-scoped operations must return the event's job, loaded from the
// database by ID, never taken from the event payload).
func Authorize[T any](p *authz.Policy, op authz.Operation, resources func(context.Context, T) ([]authz.Resource, error)) func(context.Context, T) error {
	return func(ctx context.Context, ev T) error {
		var res []authz.Resource
		if resources != nil {
			r, err := resources(ctx, ev)
			if err != nil {
				return err
			}
			res = r
		}
		return p.Authorize(ctx, op, res...)
	}
}
