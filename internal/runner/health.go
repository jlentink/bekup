package runner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"

	"github.com/jlentink/bekup/internal/runnerpb"
)

// healthState drives the standard gRPC health service on the local socket
// (D-125): SERVING only while the mTLS listener is up, the runner's own
// certificate is valid and no shutdown has started.
type healthState struct {
	mu        sync.Mutex
	listening bool
	stopping  bool
	certValid func() bool
	srv       *health.Server
}

func newHealthState(certValid func() bool) *healthState {
	h := &healthState{certValid: certValid, srv: health.NewServer()}
	h.updateLocked()
	return h
}

func (h *healthState) setListening(v bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.listening = v
	h.updateLocked()
}

func (h *healthState) update() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.updateLocked()
}

func (h *healthState) updateLocked() {
	if h.stopping {
		return
	}
	st := healthpb.HealthCheckResponse_NOT_SERVING
	if h.listening && h.certValid() {
		st = healthpb.HealthCheckResponse_SERVING
	}
	h.srv.SetServingStatus("", st)
	h.srv.SetServingStatus(runnerpb.ServiceName, st)
}

// shutdown switches to NOT_SERVING for good.
func (h *healthState) shutdown() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopping {
		return
	}
	h.stopping = true
	h.srv.Shutdown()
}

// serving reports the current state (tests).
func (h *healthState) serving() bool {
	resp, err := h.srv.Check(context.Background(), &healthpb.HealthCheckRequest{Service: runnerpb.ServiceName})
	return err == nil && resp.GetStatus() == healthpb.HealthCheckResponse_SERVING
}

// watch re-evaluates the state (certificate expiry) every interval until
// stop is closed.
func (h *healthState) watch(stop <-chan struct{}, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			h.update()
		}
	}
}

// healthSocket is the plaintext gRPC server on the container-local unix
// socket. It serves only grpc.health.v1.Health.
type healthSocket struct {
	path string
	lis  net.Listener
	srv  *grpc.Server
}

// listenHealthSocket prepares the directory and listens on path: the
// directory is created 0700 and must be a real directory owned by this
// process's user without group/other permissions; a stale socket is removed
// only if it is a socket owned by this user; the socket is chmod 0600.
func listenHealthSocket(ctx context.Context, path string, state *healthState) (*healthSocket, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("runner: health socket directory: %w", err)
	}
	if err := checkPrivateDir(dir); err != nil {
		return nil, fmt.Errorf("runner: health socket directory %s: %w", dir, err)
	}
	if err := removeStaleSocket(path); err != nil {
		return nil, fmt.Errorf("runner: health socket %s: %w", path, err)
	}
	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("runner: health socket: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, errors.Join(fmt.Errorf("runner: health socket: %w", err), lis.Close())
	}
	srv := grpc.NewServer(
		grpc.MaxRecvMsgSize(4<<10),
		grpc.MaxSendMsgSize(4<<10),
		grpc.MaxConcurrentStreams(4),
		grpc.MaxHeaderListSize(8<<10),
		grpc.ConnectionTimeout(3*time.Second),
		grpc.KeepaliveParams(keepalive.ServerParameters{MaxConnectionIdle: time.Minute, MaxConnectionAge: 5 * time.Minute, MaxConnectionAgeGrace: 5 * time.Second}),
	)
	healthpb.RegisterHealthServer(srv, state.srv)
	return &healthSocket{path: path, lis: lis, srv: srv}, nil
}

func (h *healthSocket) serve(ctx context.Context, log *slog.Logger) {
	if err := h.srv.Serve(h.lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		log.ErrorContext(ctx, "runner health socket stopped", slog.String("error", err.Error()))
	}
}

func (h *healthSocket) close() error {
	h.srv.Stop() // closes the listener, which unlinks the socket
	if err := os.Remove(h.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", h.path, err)
	}
	return nil
}

func removeStaleSocket(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&fs.ModeSocket == 0 {
		return errors.New("exists and is not a socket")
	}
	if err := checkOwner(fi); err != nil {
		return err
	}
	return os.Remove(path)
}

func checkPrivateDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return errors.New("is not a directory (symlinks are refused)")
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("mode %04o grants group/other access; it must be 0700", fi.Mode().Perm())
	}
	return checkOwner(fi)
}
