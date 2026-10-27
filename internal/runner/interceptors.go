package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// Interceptor chain (D-127), identical for unary and streaming calls:
//
//	log -> recover -> guard (identity, deadline, method policy, limiter)
//	    -> zero secrets -> handler
//
// Logs carry the method, caller, status code and duration only: never
// payloads or status messages.
type interceptors struct {
	log         *slog.Logger
	allowed     map[string]bool
	maxDuration time.Duration
	limits      *limiter
}

type callerKey struct{}

// CallerFromContext returns the verified mTLS identity
// (spiffe://bekup/<component>) of the caller of the current RPC.
func CallerFromContext(ctx context.Context) string {
	s, ok := ctx.Value(callerKey{}).(string)
	if !ok {
		return ""
	}
	return s
}

func (ic *interceptors) unary() []grpc.UnaryServerInterceptor {
	return []grpc.UnaryServerInterceptor{ic.logUnary, ic.recoverUnary, ic.guardUnary, zeroUnary}
}

func (ic *interceptors) stream() []grpc.StreamServerInterceptor {
	return []grpc.StreamServerInterceptor{ic.logStream, ic.recoverStream, ic.guardStream, zeroStream}
}

// --- logging ---------------------------------------------------------------

func (ic *interceptors) logUnary(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
	start := time.Now()
	resp, err := h(ctx, req)
	ic.logCall(ctx, info.FullMethod, start, err)
	return resp, err
}

func (ic *interceptors) logStream(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, h grpc.StreamHandler) error {
	start := time.Now()
	err := h(srv, ss)
	ic.logCall(ss.Context(), info.FullMethod, start, err)
	return err
}

func (ic *interceptors) logCall(ctx context.Context, method string, start time.Time, err error) {
	caller, idErr := peerIdentity(ctx)
	if idErr != nil {
		caller = "unknown"
	}
	code := status.Code(err)
	level := slog.LevelInfo
	switch {
	case method == runnerpb.RunnerService_Health_FullMethodName && code == codes.OK:
		level = slog.LevelDebug
	case code == codes.Internal || code == codes.Unknown || code == codes.DataLoss:
		level = slog.LevelError
	case code != codes.OK:
		level = slog.LevelWarn
	}
	ic.log.LogAttrs(ctx, level, "runner rpc",
		slog.String("method", method),
		slog.String("caller", caller),
		slog.String("code", code.String()),
		slog.Duration("duration", time.Since(start)))
}

// --- panic recovery ----------------------------------------------------------

func (ic *interceptors) recoverUnary(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (resp any, err error) {
	defer func() {
		if r := recover(); r != nil {
			resp, err = nil, ic.panicked(ctx, info.FullMethod, r)
		}
	}()
	return h(ctx, req)
}

func (ic *interceptors) recoverStream(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, h grpc.StreamHandler) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = ic.panicked(ss.Context(), info.FullMethod, r)
		}
	}()
	return h(srv, ss)
}

// panicked logs a recovered handler panic and returns Internal. The panic
// value is not logged (it might contain request data); its type and the
// stack are.
func (ic *interceptors) panicked(ctx context.Context, method string, r any) error {
	ic.log.ErrorContext(ctx, "runner rpc panicked",
		slog.String("method", method),
		slog.String("panic_type", fmt.Sprintf("%T", r)),
		slog.String("stack", string(debug.Stack())))
	return status.Error(codes.Internal, "internal error")
}

// --- guard: identity, deadline, method policy, limiter ----------------------

func (ic *interceptors) guardUnary(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
	ctx, done, err := ic.admit(ctx, info.FullMethod)
	if err != nil {
		return nil, err
	}
	defer done()
	return h(ctx, req)
}

func (ic *interceptors) guardStream(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, h grpc.StreamHandler) error {
	ctx, done, err := ic.admit(ss.Context(), info.FullMethod)
	if err != nil {
		return err
	}
	defer done()
	return h(srv, &ctxStream{ServerStream: ss, ctx: ctx})
}

// admit authorizes a call and returns its context (caller identity, clamped
// deadline) and a release function.
func (ic *interceptors) admit(ctx context.Context, method string) (context.Context, func(), error) {
	id, err := peerIdentity(ctx)
	if err != nil || !ic.allowed[id] {
		// The TLS handshake already enforces the allowlist; this is
		// defense in depth.
		return nil, nil, status.Error(codes.PermissionDenied, "caller not allowed")
	}
	class, ok := methodClasses[method]
	if !ok {
		return nil, nil, status.Error(codes.PermissionDenied, "method not allowed")
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil, nil, status.Error(codes.InvalidArgument, "a deadline is required")
	}
	cancel := context.CancelFunc(func() {})
	if time.Until(deadline) > ic.maxDuration {
		ctx, cancel = context.WithTimeout(ctx, ic.maxDuration)
	}
	release, ok := ic.limits.acquire(class)
	if !ok {
		cancel()
		return nil, nil, status.Error(codes.ResourceExhausted, "runner busy: concurrency limit reached")
	}
	ctx = context.WithValue(ctx, callerKey{}, id)
	return ctx, func() { release(); cancel() }, nil
}

// peerIdentity returns the verified client identity of the connection.
func peerIdentity(ctx context.Context) (string, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return "", errors.New("no peer")
	}
	ti, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return "", errors.New("not a TLS connection")
	}
	return certs.PeerIdentity(ti.State)
}

// ctxStream replaces the context of a server stream.
type ctxStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *ctxStream) Context() context.Context { return s.ctx }

// --- zeroing of secret fields ------------------------------------------------

// zeroUnary zeroes the request's secret fields once the handler returns.
func zeroUnary(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
	if m, ok := req.(proto.Message); ok {
		defer runnerpb.ZeroSecrets(m)
	}
	return h(ctx, req)
}

// maxHeldMessages bounds the received messages with credentials per stream
// (a stream carries one header; more is a protocol violation).
const maxHeldMessages = 4

// zeroStream remembers every received message that actually carries secret
// fields (the header; never the data chunks) and zeroes them once the
// handler returns.
func zeroStream(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, h grpc.StreamHandler) error {
	zs := &zeroingStream{ServerStream: ss}
	defer zs.zero()
	return h(srv, zs)
}

// zeroingStream holds the received messages with credentials. RecvMsg
// may run in the handler's pump goroutine while zero runs after the
// handler returned, so held is guarded by mu; a message received after
// zero is zeroed at once.
type zeroingStream struct {
	grpc.ServerStream
	mu     sync.Mutex
	held   []proto.Message
	zeroed bool
}

func (s *zeroingStream) RecvMsg(m any) error {
	err := s.ServerStream.RecvMsg(m)
	pm, ok := m.(proto.Message)
	if !ok || !runnerpb.ContainsSecrets(pm) {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.zeroed {
		runnerpb.ZeroSecrets(pm)
		return err
	}
	if len(s.held) >= maxHeldMessages {
		runnerpb.ZeroSecrets(pm)
		return status.Error(codes.InvalidArgument, "too many messages with credentials")
	}
	s.held = append(s.held, pm)
	return err
}

func (s *zeroingStream) zero() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.held {
		runnerpb.ZeroSecrets(m)
	}
	s.held, s.zeroed = nil, true
}
