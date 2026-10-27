package runnerclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// Client policy (D-127).
const (
	// keepaliveTime must stay >= the runner's enforcement MinTime (30s).
	keepaliveTime    = time.Minute
	keepaliveTimeout = 20 * time.Second
	// healthTimeout is the default deadline of Health.
	healthTimeout = 5 * time.Second
	// unaryTimeout is the default deadline of other unary calls without
	// one. Streaming calls must always set their own deadline.
	unaryTimeout = 2 * time.Minute
	// healthAttempts / healthBackoff: Health is the only call that is
	// retried, and only on Unavailable.
	healthAttempts = 3
	healthBackoff  = 200 * time.Millisecond
	// defaultDialTimeout applies when the config has none.
	defaultDialTimeout = 10 * time.Second
)

// Client is an mTLS gRPC connection to the runner.
type Client struct {
	conn *grpc.ClientConn
	rpc  runnerpb.RunnerServiceClient
}

// New builds a client from bekup.toml [runner]: TLS 1.3, the private CA as
// the only root, the runner's DNS SAN from server_name and this component's
// client certificate. The connection is established lazily.
func New(cfg config.RunnerClient) (*Client, error) {
	tlsCfg, err := certs.ClientTLSConfig(cfg.CertFile, cfg.KeyFile, cfg.CAFile, cfg.ServerName)
	if err != nil {
		return nil, fmt.Errorf("runner client: %w", err)
	}
	return NewWithTLS(cfg.Address, tlsCfg, cfg.DialTimeout.D())
}

// NewWithTLS builds a client with an explicit TLS configuration (tests and
// New). tlsCfg must require TLS 1.3 and verify the runner against the
// private CA.
func NewWithTLS(address string, tlsCfg *tls.Config, dialTimeout time.Duration) (*Client, error) {
	if address == "" {
		return nil, errors.New("runner client: address is required")
	}
	if tlsCfg == nil || tlsCfg.MinVersion < tls.VersionTLS13 || tlsCfg.RootCAs == nil || tlsCfg.InsecureSkipVerify {
		return nil, errors.New("runner client: a verifying TLS 1.3 configuration with the private CA is required")
	}
	if dialTimeout <= 0 {
		dialTimeout = defaultDialTimeout
	}
	conn, err := grpc.NewClient(address,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
		// Never route the runner connection through an HTTP proxy from the
		// environment.
		grpc.WithNoProxy(),
		grpc.WithDisableRetry(),
		grpc.WithDisableServiceConfig(),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                keepaliveTime,
			Timeout:             keepaliveTimeout,
			PermitWithoutStream: true,
		}),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff:           backoff.DefaultConfig,
			MinConnectTimeout: dialTimeout,
		}),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(runnerpb.MaxMessageBytes),
			grpc.MaxCallSendMsgSize(runnerpb.MaxMessageBytes),
		),
		grpc.WithChainUnaryInterceptor(defaultDeadline),
		grpc.WithChainStreamInterceptor(requireDeadline),
	)
	if err != nil {
		return nil, fmt.Errorf("runner client: %w", err)
	}
	return &Client{conn: conn, rpc: runnerpb.NewRunnerServiceClient(conn)}, nil
}

// RPC returns the generated client for the typed runner API.
func (c *Client) RPC() runnerpb.RunnerServiceClient { return c.rpc }

// Close closes the connection.
func (c *Client) Close() error { return c.conn.Close() }

// Health calls the runner's Health RPC. It is the only call that is
// retried: up to three attempts with a jittered backoff, and only when the
// runner is Unavailable.
func (c *Client) Health(ctx context.Context) (*runnerpb.HealthResponse, error) {
	var lastErr error
	for attempt := range healthAttempts {
		if attempt > 0 {
			d := healthBackoff << (attempt - 1)
			// Jitter only spreads retries; it is not security relevant.
			d += time.Duration(rand.Int64N(int64(d))) //nolint:gosec // G404: non-cryptographic retry jitter
			t := time.NewTimer(d)
			select {
			case <-ctx.Done():
				t.Stop()
				return nil, errors.Join(lastErr, ctx.Err())
			case <-t.C:
			}
		}
		resp, err := c.rpc.Health(ctx, &runnerpb.HealthRequest{})
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if status.Code(err) != codes.Unavailable {
			break
		}
	}
	return nil, lastErr
}

// defaultDeadline gives unary calls without a deadline a default one (the
// runner rejects calls without a deadline).
func defaultDeadline(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	if _, ok := ctx.Deadline(); !ok {
		d := unaryTimeout
		if method == runnerpb.RunnerService_Health_FullMethodName {
			d = healthTimeout
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}
	return invoker(ctx, method, req, reply, cc, opts...)
}

// requireDeadline refuses streaming calls without a deadline: a dump or
// restore must be bounded by the caller's run timeout.
func requireDeadline(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, status.Error(codes.InvalidArgument, "runner client: streaming calls need a deadline")
	}
	return streamer(ctx, desc, cc, method, opts...)
}

// ListImages returns the runner's merged image allowlist (embedded
// catalog plus the runner's images.extra_file), so serve can offer every
// allowlisted client version (including the operator's extra images).
func (c *Client) ListImages(ctx context.Context) ([]*runnerpb.AllowlistedImage, error) {
	resp, err := c.rpc.ListImages(ctx, &runnerpb.ListImagesRequest{})
	if err != nil {
		return nil, err
	}
	return resp.GetImages(), nil
}
