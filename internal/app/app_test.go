package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/deployguard"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/runnerpb"
)

func TestNewRequiresConfigAndLogger(t *testing.T) {
	if _, err := New(t.Context(), Options{}); err == nil {
		t.Fatal("New without config accepted")
	}
}

func newTestHTTP(t *testing.T, h http.Handler, shutdown time.Duration) *httpComponent {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return &httpComponent{
		srv:             &http.Server{Handler: h, ReadHeaderTimeout: time.Second},
		ln:              ln,
		shutdownTimeout: shutdown,
		logger:          slog.New(slog.DiscardHandler),
		baseURL:         "http://127.0.0.1",
	}
}

func TestHTTPComponentDrainsOnShutdown(t *testing.T) {
	started := make(chan struct{})
	h := newTestHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		time.Sleep(200 * time.Millisecond)
		if _, err := io.WriteString(w, "done"); err != nil {
			return
		}
	}), 5*time.Second)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx) }()

	body := make(chan string, 1)
	go func() {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+h.ln.Addr().String()+"/", http.NoBody)
		if err != nil {
			body <- err.Error()
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			body <- err.Error()
			return
		}
		b, err := io.ReadAll(resp.Body)
		if cerr := resp.Body.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			body <- err.Error()
			return
		}
		body <- string(b)
	}()
	<-started
	cancel()
	if got := <-body; got != "done" {
		t.Fatalf("in-flight request not drained: %q", got)
	}
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestHTTPComponentShutdownTimeout(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	h := newTestHTTP(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(started)
		<-release
	}), 50*time.Millisecond)
	defer close(release)
	var logs strings.Builder
	var mu sync.Mutex
	h.logger = slog.New(slog.NewTextHandler(lockedWriter{&mu, &logs}, nil))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx) }()
	go func() {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+h.ln.Addr().String()+"/", http.NoBody)
		if err != nil {
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return // expected: the connection is force-closed
		}
		if err := resp.Body.Close(); err != nil {
			return
		}
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		if !strings.Contains(logs.String(), "graceful shutdown timed out") {
			t.Fatalf("forced close not logged: %s", logs.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the shutdown timeout")
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

type fakeComponent struct {
	err     error
	stopped atomic.Bool
}

func (f *fakeComponent) Name() string { return "fake" }

func (f *fakeComponent) Run(ctx context.Context) error {
	if f.err != nil {
		return f.err
	}
	<-ctx.Done()
	f.stopped.Store(true)
	return nil
}

func TestRunStopsAllOnFirstFailure(t *testing.T) {
	boom := errors.New("boom")
	ok, bad := &fakeComponent{}, &fakeComponent{err: boom}
	a := &App{opts: Options{Logger: slog.New(slog.DiscardHandler)}, components: []Component{ok, bad}}
	err := a.Run(t.Context())
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "fake") {
		t.Fatalf("Run: %v", err)
	}
	if !ok.stopped.Load() {
		t.Fatal("healthy component not stopped")
	}
}

// D-212: another version than BEKUP_SETUP_VERSION refuses to start before
// anything connects.
func TestNewRefusesSetupVersionMismatch(t *testing.T) {
	cfg := config.DefaultBekup()
	_, err := New(t.Context(), Options{
		Config: &cfg, Logger: slog.New(slog.DiscardHandler), Version: "1.2.4",
		Deployment: deployguard.Deployment{SetupVersion: "1.2.3"},
	})
	if !errors.Is(err, deployguard.ErrVersionMismatch) || !strings.Contains(err.Error(), "setup upgrade") {
		t.Fatalf("err = %v", err)
	}
}

// D-212 L1: a followed image without a setup version refuses to start
// outside dev mode (before anything connects); dev mode only skips it.
func TestNewRefusesFollowingWithoutSetupVersion(t *testing.T) {
	cfg := config.DefaultBekup()
	for _, mode := range []Mode{ModeServe, ModeWorker} {
		_, err := New(t.Context(), Options{
			Config: &cfg, Logger: slog.New(slog.DiscardHandler), Version: "1.2.4", Mode: mode,
			Deployment: deployguard.Deployment{ImageFollowing: true},
		})
		if !errors.Is(err, deployguard.ErrFollowingUnversioned) {
			t.Fatalf("mode %v: err = %v", mode, err)
		}
	}
}

// D-212 M2: the runner's verdict on its own image ORs with
// BEKUP_IMAGE_MODE: an unpinned runner image counts as following whatever
// the variable says, an unset verdict changes nothing, dev mode ignores it.
func TestRunnerImageVerdict(t *testing.T) {
	cfg := config.DefaultBekup()
	cfg.Server.BaseURL = "https://backup.example.com"
	newApp := func(dev, envFollowing bool) *App {
		return &App{opts: Options{Config: &cfg, Dev: dev, Logger: slog.New(slog.DiscardHandler), Version: "1.2.4",
			Deployment: deployguard.Deployment{SetupVersion: "1.2.4", ImageFollowing: envFollowing}}}
	}
	pinned := func(v bool) *runnerpb.HealthResponse { return &runnerpb.HealthResponse{ImagePinned: proto.Bool(v)} }
	hasKind := func(a *App) bool { return slices.Contains(a.weakenedPosture(), notify.KindUnpinnedImage) }

	a := newApp(false, false)
	if a.imageFollowing() || hasKind(a) || a.imagePosture().Following {
		t.Fatal("following without any evidence")
	}
	if a.setRunnerImage(t.Context(), &runnerpb.HealthResponse{}) || a.imageFollowing() {
		t.Fatal("an unset verdict changed the posture")
	}
	if a.setRunnerImage(t.Context(), pinned(true)) || a.imageFollowing() {
		t.Fatal("a pinned runner image counts as following")
	}
	if !a.setRunnerImage(t.Context(), pinned(false)) || !a.imageFollowing() || !hasKind(a) || !a.imagePosture().Following || a.imageSource() != "runner" {
		t.Fatal("an unpinned runner image does not count as following")
	}
	if a.setRunnerImage(t.Context(), pinned(false)) {
		t.Fatal("the same verdict reported as new")
	}
	if a.setRunnerImage(t.Context(), &runnerpb.HealthResponse{}) || !a.imageFollowing() {
		t.Fatal("an unset verdict cleared the last one")
	}
	// A later pinned verdict (the runner was re-created pinned) ends it.
	if a.setRunnerImage(t.Context(), pinned(true)) || a.imageFollowing() {
		t.Fatal("a pinned verdict did not end following")
	}

	env := newApp(false, true)
	if env.setRunnerImage(t.Context(), pinned(false)) || !env.imageFollowing() || env.imageSource() != "env_and_runner" {
		t.Fatal("set-up following: the runner verdict is not new evidence")
	}
	env.setRunnerImage(t.Context(), pinned(true))
	if !env.imageFollowing() || env.imageSource() != "env" {
		t.Fatal("a pinned runner image hides BEKUP_IMAGE_MODE=following")
	}

	dev := newApp(true, false)
	if dev.setRunnerImage(t.Context(), pinned(false)) || dev.imageFollowing() || hasKind(dev) {
		t.Fatal("dev mode took the runner verdict")
	}
	// Without an auditor (no database) observing never panics and never
	// marks the audit as done.
	obs := newApp(false, false)
	obs.observeRunnerImage(t.Context(), pinned(false))
	if !obs.imageFollowing() || obs.image.audited {
		t.Fatal("observe")
	}
}
