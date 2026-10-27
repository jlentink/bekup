package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/errcode"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/streams"
	"github.com/jlentink/bekup/internal/httpserver"
)

// EventStreamMediaType is the Accept value that asks for a subscription as
// a stream of Server-Sent Events (graphql-sse, distinct connections mode).
const EventStreamMediaType = "text/event-stream"

// Event stream defaults and fixed limits.
const (
	// DefaultStreamKeepalive is the default of graphql.stream_keepalive_interval.
	DefaultStreamKeepalive = 25 * time.Second
	// streamWriteTimeout bounds every single write to a stream: a client
	// that stops reading is dropped instead of pinning the handler.
	streamWriteTimeout = 30 * time.Second
)

// StreamOptions enable GraphQL subscriptions as Server-Sent Events on
// POST /graphql (task 2.13).
type StreamOptions struct {
	// Hub tracks streams, enforces the caps and watches sessions
	// (required).
	Hub *streams.Hub
	// AllowedOrigins are the exact origins allowed to open a stream
	// (httpserver.AllowedOrigins; required).
	AllowedOrigins []string
	// KeepaliveInterval is the interval of keepalive comments on an idle
	// stream (graphql.stream_keepalive_interval).
	KeepaliveInterval time.Duration
}

func (o *StreamOptions) validate() error {
	if o.Hub == nil || len(o.AllowedOrigins) == 0 {
		return errors.New("graph: Streams.Hub and Streams.AllowedOrigins are required")
	}
	if o.KeepaliveInterval <= 0 {
		o.KeepaliveInterval = DefaultStreamKeepalive
	}
	return nil
}

// acceptsEventStream reports whether r asks for an event stream: one of
// its Accept media types is text/event-stream.
func acceptsEventStream(r *http.Request) bool {
	for _, v := range r.Header.Values("Accept") {
		for part := range strings.SplitSeq(v, ",") {
			mt, _, err := mime.ParseMediaType(strings.TrimSpace(part))
			if err == nil && mt == EventStreamMediaType {
				return true
			}
		}
	}
	return false
}

// rawParams decodes the request body checkRequestBody accepted.
func rawParams(body []byte, h http.Header) (*graphql.RawParams, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var req request
	if err := dec.Decode(&req); err != nil {
		return nil, errBadBody
	}
	start := graphql.Now()
	return &graphql.RawParams{
		Query:         req.Query,
		OperationName: req.OperationName,
		Variables:     req.Variables,
		Extensions:    req.Extensions,
		Headers:       h,
		ReadTime:      graphql.TraceTiming{Start: start, End: start},
	}, nil
}

// serveJSON runs a query or mutation and answers with one JSON response
// (gqlgen's POST transport, without the Accept negotiation: always
// application/json). Operation errors from parsing, validation and the
// limit extensions get 422, like gqlgen.
func (h *Handler) serveJSON(w http.ResponseWriter, r *http.Request, body []byte) {
	ctx := graphql.StartOperationTrace(r.Context())
	params, err := rawParams(body, r.Header)
	if err != nil {
		writeTransportError(w, http.StatusBadRequest, err.Error())
		return
	}
	rc, opErr := h.srv.CreateOperationContext(ctx, params)
	if opErr != nil {
		resp := h.srv.DispatchError(graphql.WithOperationContext(ctx, rc), opErr)
		writeJSON(w, plainStatus(opErr), resp)
		return
	}
	responses, ctx := h.srv.DispatchOperation(ctx, rc)
	writeJSON(w, http.StatusOK, responses(ctx))
}

// serveStream opens an event stream for one subscription: exact Origin,
// an authenticated, unrestricted cookie session (the session middleware
// ran), a free stream slot and an operation the transport gate admits
// (only a subscription). It then lifts the server deadlines, starts the
// session watcher and writes the graphql-sse events until the
// subscription ends, the client goes away or the watcher ends the stream;
// in the last case a final error event carries the reason
// (streams.Conn.End) before the complete event.
func (h *Handler) serveStream(w http.ResponseWriter, r *http.Request, body []byte) {
	o := h.opts.Streams
	if r.Header.Get("Origin") == "" || !httpserver.OriginAllowed(r, o.AllowedOrigins) {
		writeTransportError(w, http.StatusForbidden, "origin not allowed")
		return
	}
	p, ok := auth.FromContext(r.Context())
	if !ok {
		writeTransportError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if p.MFAPending {
		// A login waiting for its second factor gets no stream (task
		// 1.9); completing it rotates the session.
		writeTransportError(w, http.StatusUnauthorized, "complete the sign-in with your second factor first")
		return
	}
	if p.EnrollmentRequired {
		// The 2FA policy requires a factor first (task 1.9): no
		// subscription is allowed to such a principal.
		writeTransportError(w, http.StatusForbidden, "set up a second factor first")
		return
	}
	c, err := o.Hub.Acquire(p)
	switch {
	case errors.Is(err, streams.ErrTooManyForUser), errors.Is(err, streams.ErrTooManyForSession):
		w.Header().Set("Retry-After", "30")
		writeTransportError(w, http.StatusTooManyRequests, "too many event streams")
		return
	case errors.Is(err, streams.ErrHubClosed):
		w.Header().Set("Retry-After", "5")
		writeTransportError(w, http.StatusServiceUnavailable, "the server is shutting down")
		return
	case err != nil:
		w.Header().Set("Retry-After", "30")
		writeTransportError(w, http.StatusServiceUnavailable, "too many event streams")
		return
	}
	defer c.Release()

	ctx := c.Start(graphql.StartOperationTrace(r.Context()))
	params, err := rawParams(body, r.Header)
	if err != nil {
		writeTransportError(w, http.StatusBadRequest, err.Error())
		return
	}
	rc, opErr := h.srv.CreateOperationContext(ctx, params)
	if opErr != nil {
		// Refused before the stream starts: a plain JSON error.
		resp := h.srv.DispatchError(graphql.WithOperationContext(ctx, rc), opErr)
		writeJSON(w, streamRefusalStatus(opErr), resp)
		return
	}

	if err := httpserver.LiftDeadlines(w); err != nil {
		// The stream then ends at server.write_timeout; the client
		// opens it again.
		h.opts.Logger.WarnContext(ctx, "could not lift the connection deadlines for an event stream",
			slog.Any("error", err), slog.String("request_id", httpserver.RequestID(ctx)))
	}
	sw := &sseWriter{w: w, rc: http.NewResponseController(w)}
	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream; charset=utf-8")
	hdr.Set("Cache-Control", "no-store")
	// nginx must not buffer the stream (the shipped config also turns
	// proxy_buffering off for /graphql).
	hdr.Set("X-Accel-Buffering", "no")
	hdr.Del("Content-Length")
	w.WriteHeader(http.StatusOK)
	if !sw.comment("") {
		return
	}

	responses, dctx := h.srv.DispatchOperation(ctx, rc)
	stop := sw.keepalive(dctx, o.KeepaliveInterval)
	defer stop()
	for {
		resp := responses(dctx)
		if resp == nil {
			break
		}
		if !sw.next(resp) {
			return
		}
	}
	stop()
	if end := c.End(); end != nil {
		if !sw.next(&graphql.Response{Errors: gqlerror.List{protocolError(end.Code, end.Message)}}) {
			return
		}
	}
	sw.complete()
}

// sseWriter writes graphql-sse events; writes are serialized (the
// keepalive runs concurrently) and each one is flushed with its own write
// deadline. After the first failed write every further write is skipped.
type sseWriter struct {
	w      http.ResponseWriter
	rc     *http.ResponseController
	mu     sync.Mutex
	failed bool
}

func (s *sseWriter) write(b []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed {
		return false
	}
	if err := s.rc.SetWriteDeadline(time.Now().Add(streamWriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		s.failed = true
		return false
	}
	if _, err := s.w.Write(b); err != nil {
		s.failed = true
		return false
	}
	if err := s.rc.Flush(); err != nil {
		s.failed = true
		return false
	}
	return true
}

// comment writes an SSE comment (ignored by clients; keeps proxies and
// the connection alive).
func (s *sseWriter) comment(text string) bool {
	return s.write([]byte(":" + text + "\n\n"))
}

// next writes one execution result as a "next" event.
func (s *sseWriter) next(resp *graphql.Response) bool {
	b, err := json.Marshal(resp)
	if err != nil {
		s.mu.Lock()
		s.failed = true
		s.mu.Unlock()
		return false
	}
	return s.write(append(append([]byte("event: next\ndata: "), b...), '\n', '\n'))
}

// complete writes the final "complete" event.
func (s *sseWriter) complete() bool {
	return s.write([]byte("event: complete\ndata:\n\n"))
}

// keepalive writes a comment every interval until ctx ends or the
// returned stop is called; stop waits for the goroutine (no write after
// the handler returned).
func (s *sseWriter) keepalive(ctx context.Context, interval time.Duration) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if !s.comment(" keepalive") {
					return
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
}

// isProtocolError reports whether errs are protocol errors (parse,
// validation and the limit extensions; gqlgen answers them with 422).
func isProtocolError(errs gqlerror.List) bool {
	return errcode.GetErrorKind(errs) == errcode.KindProtocol
}

// plainStatus is the status of a plain POST whose operation was refused
// before execution: 422 for protocol errors, 200 otherwise (GraphQL errors
// travel in the body, like gqlgen's POST transport).
func plainStatus(errs gqlerror.List) int {
	if isProtocolError(errs) {
		return http.StatusUnprocessableEntity
	}
	return http.StatusOK
}

// streamRefusalStatus is the status of a stream request refused before
// the stream started (graphql-sse clients only read a stream from a 2xx):
// the typed access codes map to 401/403/429, protocol errors to 422,
// anything else to 400.
func streamRefusalStatus(errs gqlerror.List) int {
	for _, e := range errs {
		code, ok := e.Extensions[ExtCode].(string)
		if !ok {
			continue
		}
		switch apierror.Code(code) {
		case apierror.Unauthenticated, apierror.SecondFactorRequired:
			return http.StatusUnauthorized
		case apierror.Forbidden, apierror.StepUpRequired, apierror.Requires2FAEnrollment, apierror.PasswordChangeRequired:
			return http.StatusForbidden
		case apierror.RateLimited:
			return http.StatusTooManyRequests
		default:
		}
	}
	if isProtocolError(errs) {
		return http.StatusUnprocessableEntity
	}
	return http.StatusBadRequest
}

// writeJSON writes resp with status.
func writeJSON(w http.ResponseWriter, status int, resp *graphql.Response) {
	b, err := json.Marshal(resp)
	if err != nil {
		writeTransportError(w, http.StatusInternalServerError, MaskedMessage)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(b); err != nil {
		return
	}
}

// transportGate decides which operations may run on which transport:
// over an event stream only subscriptions (queries and mutations use
// plain POST, where the cookie can be set and the response is bounded),
// and subscriptions only over an event stream (a plain POST would hold
// the request open). Subscription root fields get the same
// deny-by-default operation gate as query and mutation fields (gqlgen
// applies root-field middleware to those only).
type transportGate struct {
	public map[string]bool
	ops    map[string]authz.Operation
	access Access
}

var _ interface {
	graphql.HandlerExtension
	graphql.OperationContextMutator
} = transportGate{}

func (transportGate) ExtensionName() string { return "TransportGate" }

func (transportGate) Validate(graphql.ExecutableSchema) error { return nil }

func (g transportGate) MutateOperationContext(ctx context.Context, opCtx *graphql.OperationContext) *gqlerror.Error {
	if opCtx.Operation == nil {
		return nil
	}
	overStream := streams.FromContext(ctx) != nil
	isSub := opCtx.Operation.Operation == ast.Subscription
	switch {
	case overStream && !isSub:
		return protocolError(apierror.Forbidden, "only subscriptions are accepted as event streams")
	case !overStream && isSub:
		return protocolError(apierror.Forbidden, "subscriptions are only available as event streams (Accept: text/event-stream)")
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
