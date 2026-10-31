package runner

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/runner/executor"
	"github.com/jlentink/bekup/internal/runner/pinning"
	"github.com/jlentink/bekup/internal/runner/targets"
	"github.com/jlentink/bekup/internal/runner/validate"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// Transport policy (D-127).
const (
	// controlConcurrency bounds the short control RPCs (Probe,
	// FetchServerCertificate, FetchHostKey, PrePull).
	controlConcurrency = 8
	// maxConcurrentStreams bounds the streams per connection.
	maxConcurrentStreams = 64
	// maxHeaderListBytes bounds the request metadata.
	maxHeaderListBytes = 16 << 10
	// handshakeTimeout bounds the TLS handshake of a new connection.
	handshakeTimeout = 10 * time.Second
	// keepaliveMinTime is the shortest client ping interval accepted.
	keepaliveMinTime = 30 * time.Second
	// keepaliveTime / keepaliveTimeout: server pings idle connections.
	keepaliveTime    = 2 * time.Minute
	keepaliveTimeout = 20 * time.Second
	// maxConnectionIdle closes connections without RPCs.
	maxConnectionIdle = 15 * time.Minute
	// maxConnectionAge recycles every connection, so a client whose
	// certificate expired or was removed from the allowlist (after a restart
	// with a new config) cannot keep an old connection alive forever.
	maxConnectionAge = 30 * time.Minute
	// connectionAgeSlack is added to limits.max_run_duration as the grace
	// period after maxConnectionAge, so a recycled connection never cuts a
	// running dump/restore.
	connectionAgeSlack = time.Minute
	// shutdownGrace is the time in-flight RPCs get on shutdown before they
	// are canceled (compose stop_grace_period is 60s).
	shutdownGrace = 45 * time.Second
)

// DockerStatusFunc reports the state of the Docker daemon (task 2.6).
type DockerStatusFunc func(ctx context.Context) runnerpb.DockerStatus

// ImagePinnedFunc reports whether the runner's own container image is
// pinned by digest (D-212); known is false while that is not determined.
type ImagePinnedFunc func() (pinned, known bool)

// Options configures a Server.
type Options struct {
	Config  *config.Runner
	Dev     bool
	Logger  *slog.Logger
	Version string
	// Docker reports the daemon state for Health; nil = UNKNOWN (or the
	// executor's status when DockerClient is set).
	Docker DockerStatusFunc
	// ImagePinned reports the own image's pinning for Health; nil = the
	// executor's verdict when DockerClient is set, else unknown.
	ImagePinned ImagePinnedFunc
	// DockerClient enables the Docker executor (task 2.6): New builds it
	// with the validator's target guard, Serve starts its initialization
	// and orphan reaper. nil = no executor (tests).
	DockerClient executor.Docker
	// TLS overrides the TLS configuration built from Config.TLS (tests).
	TLS *tls.Config
	// Now overrides the clock (tests).
	Now func() time.Time

	// impl replaces the service implementation (tests).
	impl runnerpb.RunnerServiceServer
}

// Server is the runner's mTLS gRPC server.
type Server struct {
	cfg     *config.Runner
	dev     bool
	log     *slog.Logger
	tls     *tls.Config
	certEnd time.Time
	certBeg time.Time
	now     func() time.Time
	limits  *limiter
	grpc    *grpc.Server
	health  *healthState
	// validator checks every request against the merged image catalog
	// and the target allowlist (task 2.5); the RPCs use it from 2.7 on.
	validator *validate.Validator
	// exec runs containers (task 2.6); nil without Options.DockerClient.
	exec *executor.Executor
	// runPool is docker.run_subnet_pool: the gRPC listener refuses peers
	// from it even without an executor.
	runPool netip.Prefix
}

// New validates the options and builds the server; it does not listen yet.
func New(opts Options) (*Server, error) {
	if opts.Config == nil {
		return nil, errors.New("runner: config is required")
	}
	if opts.Logger == nil {
		return nil, errors.New("runner: logger is required")
	}
	cfg := opts.Config
	if err := checkListen(cfg.Listen, opts.Dev); err != nil {
		return nil, err
	}
	if cfg.Limits.MaxConcurrentRuns <= 0 || cfg.Limits.MaxRunDuration.D() <= 0 {
		return nil, errors.New("runner: limits.max_concurrent_runs and limits.max_run_duration must be positive")
	}
	tlsCfg := opts.TLS
	if tlsCfg == nil {
		var err error
		tlsCfg, err = certs.ServerTLSConfig(cfg.TLS.CertFile, cfg.TLS.KeyFile, cfg.TLS.ClientCAFile, cfg.TLS.AllowedClientSANs)
		if err != nil {
			return nil, fmt.Errorf("runner: %w", err)
		}
	}
	if len(tlsCfg.Certificates) != 1 || tlsCfg.Certificates[0].Leaf == nil {
		return nil, errors.New("runner: exactly one parsed server certificate is required")
	}
	allowed, err := canonicalIDs(cfg.TLS.AllowedClientSANs)
	if err != nil {
		return nil, err
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	val, err := newValidator(cfg)
	if err != nil {
		return nil, err
	}
	for _, w := range val.Guard().Warnings() {
		opts.Logger.Warn("runner startup warning", slog.String("warning", w))
	}
	leaf := tlsCfg.Certificates[0].Leaf
	poolStr := cfg.Docker.RunSubnetPool
	if poolStr == "" {
		poolStr = config.DefaultRunSubnetPool
	}
	runPool, err := netip.ParsePrefix(poolStr)
	if err != nil {
		return nil, fmt.Errorf("runner: docker.run_subnet_pool: %w", err)
	}
	var exec *executor.Executor
	if opts.DockerClient != nil {
		exec, err = executor.New(executor.Options{
			Config: cfg, Docker: opts.DockerClient, Guard: val.Guard(), Logger: opts.Logger,
		})
		if err != nil {
			return nil, fmt.Errorf("runner: %w", err)
		}
	} else if err := val.Guard().SetOwnNetworks([]netip.Prefix{runPool.Masked()}); err != nil {
		return nil, fmt.Errorf("runner: %w", err)
	}
	docker := opts.Docker
	if docker == nil && exec != nil {
		docker = exec.Status
	}
	imagePinned := opts.ImagePinned
	if imagePinned == nil && exec != nil {
		imagePinned = exec.ImagePinned
	}
	s := &Server{
		exec:      exec,
		runPool:   runPool.Masked(),
		validator: val,
		cfg:       cfg,
		dev:       opts.Dev,
		log:       opts.Logger,
		tls:       tlsCfg,
		certBeg:   leaf.NotBefore,
		certEnd:   leaf.NotAfter,
		now:       now,
		limits:    newLimiter(cfg.Limits.MaxConcurrentRuns, controlConcurrency),
	}
	s.health = newHealthState(s.certValid)
	ic := &interceptors{
		log:         opts.Logger,
		allowed:     allowed,
		maxDuration: cfg.Limits.MaxRunDuration.D(),
		limits:      s.limits,
	}
	s.grpc = grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsCfg)),
		grpc.MaxRecvMsgSize(runnerpb.MaxMessageBytes),
		grpc.MaxSendMsgSize(runnerpb.MaxMessageBytes),
		grpc.MaxConcurrentStreams(maxConcurrentStreams),
		grpc.MaxHeaderListSize(maxHeaderListBytes),
		grpc.ConnectionTimeout(handshakeTimeout),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             keepaliveMinTime,
			PermitWithoutStream: true,
		}),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle:     maxConnectionIdle,
			MaxConnectionAge:      maxConnectionAge,
			MaxConnectionAgeGrace: cfg.Limits.MaxRunDuration.D() + connectionAgeSlack,
			Time:                  keepaliveTime,
			Timeout:               keepaliveTimeout,
		}),
		grpc.ChainUnaryInterceptor(ic.unary()...),
		grpc.ChainStreamInterceptor(ic.stream()...),
	)
	impl := opts.impl
	if impl == nil {
		svc := newService(opts.Version, docker, s.limits, s.certEnd)
		svc.imagePinned = imagePinned
		checker := pinning.New(pinning.Options{})
		var runner runExecutor
		if exec != nil {
			runner = exec
		}
		svc.ops = newOperations(val, runner, checker, opts.Logger)
		impl = svc
	}
	runnerpb.RegisterRunnerServiceServer(s.grpc, impl)
	return s, nil
}

// newValidator loads the image allowlist (embedded catalog + images.extra_file)
// and the target allowlist; an invalid extra file or allowlist fails startup.
func newValidator(cfg *config.Runner) (*validate.Validator, error) {
	cat, err := validate.LoadCatalog(cfg)
	if err != nil {
		return nil, fmt.Errorf("runner: %w", err)
	}
	guard, err := targets.FromConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("runner: %w", err)
	}
	return validate.New(cat, guard)
}

// Validator returns the request validator (image and target allowlists).
func (s *Server) Validator() *validate.Validator { return s.validator }

// Executor returns the Docker executor (nil without Options.DockerClient).
func (s *Server) Executor() *executor.Executor { return s.exec }

// isRunPeer reports whether a gRPC peer address belongs to a per-run
// network (the pool or an active per-run subnet). Such connections are
// closed before the TLS handshake: a dump container never reaches the API.
func (s *Server) isRunPeer(a netip.Addr) bool {
	if s.runPool.Contains(a.Unmap()) {
		return true
	}
	return s.exec != nil && s.exec.IsRunAddress(a)
}

// certValid reports whether the runner's own certificate is valid now.
func (s *Server) certValid() bool {
	now := s.now()
	return !now.Before(s.certBeg) && now.Before(s.certEnd)
}

// Run listens on listen.address and serves until ctx is canceled, then
// shuts down gracefully (shutdownGrace, then in-flight RPCs are canceled).
func (s *Server) Run(ctx context.Context) error {
	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", s.cfg.Listen.Address)
	if err != nil {
		return fmt.Errorf("runner: listen on %s: %w", s.cfg.Listen.Address, err)
	}
	return s.Serve(ctx, lis)
}

// Serve serves on lis (Run, tests) until ctx is canceled.
func (s *Server) Serve(ctx context.Context, lis net.Listener) error {
	if !s.certValid() {
		return errors.Join(fmt.Errorf("runner: the server certificate is not valid now (valid %s to %s)",
			s.certBeg.Format(time.RFC3339), s.certEnd.Format(time.RFC3339)), lis.Close())
	}
	var hs *healthSocket
	if p := s.cfg.Listen.HealthSocket; p != "" {
		var err error
		hs, err = listenHealthSocket(ctx, p, s.health)
		if err != nil {
			return errors.Join(err, lis.Close())
		}
		defer func() {
			if err := hs.close(); err != nil {
				s.log.WarnContext(ctx, "runner health socket cleanup failed", slog.String("error", err.Error()))
			}
		}()
	}

	lis = executor.FilterListener(lis, s.isRunPeer, s.log)
	if s.exec != nil {
		execCtx, stopExec := context.WithCancel(ctx)
		execDone := make(chan struct{})
		go func() {
			defer close(execDone)
			s.exec.Start(execCtx)
		}()
		defer func() {
			stopExec()
			<-execDone
		}()
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- s.grpc.Serve(lis) }()
	if hs != nil {
		go hs.serve(ctx, s.log)
	}
	s.health.setListening(true)
	s.log.InfoContext(ctx, "runner listening",
		slog.String("address", lis.Addr().String()),
		slog.String("health_socket", s.cfg.Listen.HealthSocket),
		slog.Time("certificate_not_after", s.certEnd))
	if s.now().Add(30 * 24 * time.Hour).After(s.certEnd) {
		s.log.WarnContext(ctx, "runner certificate expires within 30 days",
			slog.Time("certificate_not_after", s.certEnd))
	}

	stopWatch := make(chan struct{})
	defer close(stopWatch)
	go s.health.watch(stopWatch, time.Minute)

	select {
	case err := <-serveErr:
		s.health.shutdown()
		if err != nil {
			return fmt.Errorf("runner: serve: %w", err)
		}
		return nil
	case <-ctx.Done():
	}
	s.health.shutdown()
	s.log.InfoContext(ctx, "runner shutting down", slog.Duration("grace", shutdownGrace))
	s.stop(shutdownGrace)
	<-serveErr
	return nil
}

// stop drains in-flight RPCs for at most grace, then cancels them.
func (s *Server) stop(grace time.Duration) {
	done := make(chan struct{})
	go func() {
		s.grpc.GracefulStop()
		close(done)
	}()
	t := time.NewTimer(grace)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
		s.grpc.Stop()
		<-done
	}
}

// checkListen re-checks the D-098 rule (config validation enforces it too).
func checkListen(l config.RunnerListen, dev bool) error {
	host, _, err := net.SplitHostPort(l.Address)
	if err != nil {
		return fmt.Errorf("runner: listen.address %q: %w", l.Address, err)
	}
	if dev || l.AllowWildcard {
		return nil
	}
	if host == "" {
		return fmt.Errorf("runner: listen.address %q listens on every interface; bind the internal network address (or set listen.allow_wildcard)", l.Address)
	}
	if a, err := netip.ParseAddr(host); err == nil && a.Unmap().IsUnspecified() {
		return fmt.Errorf("runner: listen.address %q listens on every interface; bind the internal network address (or set listen.allow_wildcard)", l.Address)
	}
	return nil
}

func canonicalIDs(ids []string) (map[string]bool, error) {
	if len(ids) == 0 {
		return nil, errors.New("runner: tls.allowed_client_sans is empty")
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		c, err := certs.ParseClientID(id)
		if err != nil {
			return nil, fmt.Errorf("runner: %w", err)
		}
		out[c] = true
	}
	return out, nil
}
