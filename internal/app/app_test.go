package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
