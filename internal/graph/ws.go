package graph

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	coderws "github.com/coder/websocket"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/wsconn"
	"github.com/jlentink/bekup/internal/httpserver"
)

// WebSocketSubprotocol is the only accepted subprotocol
// (graphql-transport-ws of the graphql-ws library). The legacy
// subscriptions-transport-ws protocol ("graphql-ws") is refused.
const WebSocketSubprotocol = "graphql-transport-ws"

// WebSocket defaults (graphql.ws_* keys).
const (
	DefaultWSInitTimeout     = 10 * time.Second
	DefaultWSPingInterval    = 25 * time.Second
	DefaultWSMaxMessageBytes = 64 << 10
)

// WebSocketOptions enable subscriptions over WebSocket (task 1.10).
type WebSocketOptions struct {
	// Hub tracks connections, enforces the caps and watches sessions
	// (required).
	Hub *wsconn.Hub
	// AllowedOrigins are the exact origins allowed to upgrade
	// (httpserver.AllowedOrigins; required).
	AllowedOrigins []string
	// InitTimeout bounds the wait for connection_init.
	InitTimeout time.Duration
	// PingInterval is the server ping interval; a peer that does not
	// answer within twice the interval is dropped.
	PingInterval time.Duration
	// MaxMessageBytes caps one client message.
	MaxMessageBytes int64
}

func (o *WebSocketOptions) validate() error {
	if o.Hub == nil || len(o.AllowedOrigins) == 0 {
		return errors.New("graph: WebSocket.Hub and WebSocket.AllowedOrigins are required")
	}
	if o.InitTimeout <= 0 {
		o.InitTimeout = DefaultWSInitTimeout
	}
	if o.PingInterval <= 0 {
		o.PingInterval = DefaultWSPingInterval
	}
	if o.MaxMessageBytes <= 0 {
		o.MaxMessageBytes = DefaultWSMaxMessageBytes
	}
	return nil
}

// websocketTransport returns gqlgen's WebSocket transport restricted to
// graphql-transport-ws, with the init timeout, the ping keepalive, the
// message size limit and an InitFunc that refuses credentials in the
// connection_init payload: the caller is always the cookie session of the
// upgrade request.
func websocketTransport(o WebSocketOptions, logger *slog.Logger) transport.Websocket {
	limit := o.MaxMessageBytes
	return transport.Websocket{
		Implementation:   acceptor{originHosts: originHosts(o.AllowedOrigins)},
		InitTimeout:      o.InitTimeout,
		PingPongInterval: o.PingInterval,
		PayloadReadLimit: &limit,
		InitFunc: func(ctx context.Context, payload transport.InitPayload) (context.Context, *transport.InitPayload, error) {
			c := wsconn.FromContext(ctx)
			if c == nil || c.Closed() {
				return transport.WithWebsocketCloseCode(ctx, wsconn.CloseSessionEnded), nil, errors.New("unauthorized")
			}
			if len(payload) > 0 {
				ctx = transport.AppendCloseReason(ctx, "connection_init payload not accepted")
				return transport.WithWebsocketCloseCode(ctx, wsconn.CloseBadInit), nil, errors.New("connection_init payload not accepted")
			}
			return ctx, nil, nil
		},
		ErrorFunc: func(ctx context.Context, err error) {
			logger.DebugContext(ctx, "websocket error", slog.Any("error", err),
				slog.String("request_id", httpserver.RequestID(ctx)))
		},
	}
}

// acceptor upgrades with github.com/coder/websocket offering only
// graphql-transport-ws and without compression, and hands the socket to
// the connection's session watcher. The Origin was already checked exactly
// (serveWebSocket); the library checks it again against the hosts of the
// allowed origins (its default compares with the Host header, which is the
// internal name behind the reverse proxy).
type acceptor struct{ originHosts []string }

func (a acceptor) Accept(w http.ResponseWriter, r *http.Request, _ transport.WebsocketAcceptOptions) (transport.WebsocketConn, error) {
	inner := transport.CoderWebsocketImplementation{AcceptOptions: coderws.AcceptOptions{
		Subprotocols:    []string{WebSocketSubprotocol},
		OriginPatterns:  a.originHosts,
		CompressionMode: coderws.CompressionDisabled,
	}}
	sock, err := inner.Accept(w, r, transport.WebsocketAcceptOptions{ResponseHeader: http.Header{}, Subprotocols: []string{WebSocketSubprotocol}})
	if err != nil {
		return nil, err
	}
	if c := wsconn.FromContext(r.Context()); c != nil {
		c.Attach(sock)
	}
	return sock, nil
}

// originHosts returns the host[:port] part of each origin; the patterns
// are matched literally (path.Match without metacharacters).
func originHosts(origins []string) []string {
	out := make([]string, 0, len(origins))
	for _, o := range origins {
		_, host, ok := strings.Cut(o, "://")
		if ok && host != "" && !strings.ContainsAny(host, "*?[]\\") {
			out = append(out, host)
		}
	}
	return out
}

// isWebSocketRequest is a GET asking for a WebSocket upgrade.
func isWebSocketRequest(r *http.Request) bool {
	return r.Method == http.MethodGet && httpserver.IsWebSocketUpgrade(r)
}

// offersSubprotocol reports whether the client offers p.
func offersSubprotocol(r *http.Request, p string) bool {
	for _, v := range r.Header.Values("Sec-WebSocket-Protocol") {
		for s := range strings.SplitSeq(v, ",") {
			if strings.TrimSpace(s) == p {
				return true
			}
		}
	}
	return false
}

// serveWebSocket admits an upgrade: exact Origin, graphql-transport-ws
// offered, an authenticated cookie session (the session middleware ran),
// a free connection slot. It then lifts the server deadlines, starts the
// session watcher and lets gqlgen run the connection until it ends.
func (h *Handler) serveWebSocket(w http.ResponseWriter, r *http.Request) {
	o := h.opts.WebSocket
	if r.Header.Get("Origin") == "" || !httpserver.OriginAllowed(r, o.AllowedOrigins) {
		writeTransportError(w, http.StatusForbidden, "origin not allowed")
		return
	}
	if !offersSubprotocol(r, WebSocketSubprotocol) {
		writeTransportError(w, http.StatusBadRequest, "the WebSocket subprotocol must be "+WebSocketSubprotocol)
		return
	}
	p, ok := auth.FromContext(r.Context())
	if !ok {
		writeTransportError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if p.MFAPending {
		// A login waiting for its second factor gets no socket (task 1.9);
		// completing it rotates the session and the client reconnects.
		writeTransportError(w, http.StatusUnauthorized, "complete the sign-in with your second factor first")
		return
	}
	if p.EnrollmentRequired {
		// The 2FA policy requires a factor first (task 1.9): no
		// subscription is allowed to such a principal (M1 review L2).
		writeTransportError(w, http.StatusForbidden, "set up a second factor first")
		return
	}
	c, err := o.Hub.Acquire(p)
	switch {
	case errors.Is(err, wsconn.ErrTooManyForUser):
		w.Header().Set("Retry-After", "30")
		writeTransportError(w, http.StatusTooManyRequests, "too many WebSocket connections")
		return
	case errors.Is(err, wsconn.ErrHubClosed):
		w.Header().Set("Retry-After", "5")
		writeTransportError(w, http.StatusServiceUnavailable, "the server is shutting down")
		return
	case err != nil:
		w.Header().Set("Retry-After", "30")
		writeTransportError(w, http.StatusServiceUnavailable, "too many WebSocket connections")
		return
	}
	defer c.Release()
	if err := httpserver.LiftDeadlines(w); err != nil {
		// The socket then ends at server.write_timeout; the client
		// reconnects.
		h.opts.Logger.WarnContext(r.Context(), "could not lift the connection deadlines for a WebSocket",
			slog.Any("error", err), slog.String("request_id", httpserver.RequestID(r.Context())))
	}
	ctx := c.Start(r.Context())
	h.srv.ServeHTTP(w, r.WithContext(ctx))
}

// wsOperationGate decides which operations may run on which transport:
// over a WebSocket only subscriptions (queries and mutations use POST,
// where the cookie can be set and the per-user rate limit applies), and
// subscriptions only over a WebSocket (POST would hold the request open).
// Subscription root fields get the same deny-by-default operation gate as
// query and mutation fields (gqlgen applies root-field middleware to
// those only).
type wsOperationGate struct {
	public map[string]bool
	ops    map[string]authz.Operation
	access Access
}

var _ interface {
	graphql.HandlerExtension
	graphql.OperationContextMutator
} = wsOperationGate{}

func (wsOperationGate) ExtensionName() string { return "WebSocketOperationGate" }

func (wsOperationGate) Validate(graphql.ExecutableSchema) error { return nil }

func (g wsOperationGate) MutateOperationContext(ctx context.Context, opCtx *graphql.OperationContext) *gqlerror.Error {
	if opCtx.Operation == nil {
		return nil
	}
	overWS := wsconn.FromContext(ctx) != nil
	isSub := opCtx.Operation.Operation == ast.Subscription
	switch {
	case overWS && !isSub:
		return protocolError(apierror.Forbidden, "only subscriptions are accepted over WebSocket")
	case !overWS && isSub:
		return protocolError(apierror.Forbidden, "subscriptions are only available over WebSocket")
	case !isSub:
		return nil
	}
	for _, f := range graphql.CollectFields(opCtx, opCtx.Operation.SelectionSet, []string{"Subscription"}) {
		if strings.HasPrefix(f.Name, "__") {
			continue
		}
		name := "Subscription." + f.Name
		if g.public[name] {
			continue
		}
		op, mapped := g.ops[name]
		if !mapped || f.Definition == nil || !isGuarded(f.Definition) {
			return protocolError(apierror.Forbidden, "operation not allowed")
		}
		if err := g.access.CheckOperation(ctx, op); err != nil {
			if ae, ok := apierror.As(err); ok {
				return protocolError(ae.Code, ae.Message)
			}
			return protocolError(apierror.Forbidden, "operation not allowed")
		}
	}
	return nil
}

func pubSet(public []string) map[string]bool {
	m := make(map[string]bool, len(public))
	for _, p := range public {
		m[p] = true
	}
	return m
}

// subscriptionSlots reserves one of the connection's subscription slots
// for every subscription and frees it when the subscription ends.
type subscriptionSlots struct{}

var _ interface {
	graphql.HandlerExtension
	graphql.OperationInterceptor
} = subscriptionSlots{}

func (subscriptionSlots) ExtensionName() string { return "SubscriptionSlots" }

func (subscriptionSlots) Validate(graphql.ExecutableSchema) error { return nil }

func (subscriptionSlots) InterceptOperation(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
	c := wsconn.FromContext(ctx)
	opCtx := graphql.GetOperationContext(ctx)
	if c == nil || opCtx == nil || opCtx.Operation == nil || opCtx.Operation.Operation != ast.Subscription {
		return next(ctx)
	}
	release, err := c.AcquireSubscription()
	if err != nil {
		code := apierror.RateLimited
		msg := "too many active subscriptions on this connection"
		switch {
		case errors.Is(err, wsconn.ErrSubscribeRate):
			msg = "too many subscribe requests"
		case errors.Is(err, wsconn.ErrClosed):
			code, msg = apierror.Unauthenticated, "connection closed"
		}
		return graphql.OneShot(&graphql.Response{Errors: gqlerror.List{protocolError(code, msg)}})
	}
	stop := context.AfterFunc(ctx, release)
	handler := next(ctx)
	return func(ctx context.Context) *graphql.Response {
		resp := handler(ctx)
		if resp == nil {
			stop()
			release()
		}
		return resp
	}
}
