package monitor

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/jlentink/bekup/internal/runnerpb"
)

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestHandlerRoutes(t *testing.T) {
	m := New(Options{Version: "v"})
	h := Handler(m.Registry(), nil, discard())
	for _, tc := range []struct {
		method, path string
		code         int
	}{
		{"GET", "/metrics", 200},
		{"HEAD", "/metrics", 200},
		{"GET", "/healthz", 200},
		{"POST", "/metrics", 405},
		{"GET", "/", 404},
		{"GET", "/graphql", 404},
		{"GET", "/debug/pprof/", 404},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, http.NoBody))
		if rec.Code != tc.code {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, rec.Code, tc.code)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s %s: security headers missing", tc.method, tc.path)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", http.NoBody))
	if !strings.Contains(rec.Body.String(), `bekup_build_info{goversion=`) {
		t.Errorf("metrics body lacks build info:\n%.500s", rec.Body.String())
	}
}

func TestHandlerBearer(t *testing.T) {
	const cred = "0123456789abcdef0123456789abcdef-x"
	sum := sha256.Sum256([]byte(cred))
	h := Handler(New(Options{}).Registry(), &sum, discard())
	for _, tc := range []struct {
		header string
		code   int
	}{
		{"", 401},
		{"Bearer", 401},
		{"Bearer ", 401},
		{"Basic " + cred, 401},
		{"Bearer " + cred + "x", 401},
		{"Bearer " + cred[:len(cred)-1], 401},
		{"Bearer " + cred, 200},
		{"bearer " + cred, 200},
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", http.NoBody)
		if tc.header != "" {
			req.Header.Set("Authorization", tc.header)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.code {
			t.Errorf("Authorization %q = %d, want %d", tc.header, rec.Code, tc.code)
		}
		if rec.Code == 401 && (rec.Header().Get("WWW-Authenticate") == "" || strings.Contains(rec.Body.String(), "bekup_")) {
			t.Errorf("401 leaks metrics or lacks the challenge")
		}
	}
	// /healthz needs no credential.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", http.NoBody))
	if rec.Code != 200 || rec.Body.String() != "ok\n" {
		t.Errorf("healthz = %d %q", rec.Code, rec.Body.String())
	}
}

func writeCredFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "metrics_bearer")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadBearer(t *testing.T) {
	if _, _, err := loadBearer(writeCredFile(t, "short\n")); err == nil {
		t.Error("short credential accepted")
	}
	if _, _, err := loadBearer(writeCredFile(t, "0123456789abcdef 0123456789abcdef\n")); err == nil {
		t.Error("credential with a space accepted")
	}
	cred := "0123456789abcdef0123456789abcdef"
	sum, unregister, err := loadBearer(writeCredFile(t, "  "+cred+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	defer unregister()
	if sum != sha256.Sum256([]byte(cred)) {
		t.Error("hash of the trimmed credential expected")
	}
	p := writeCredFile(t, cred)
	if err := os.Chmod(p, 0o644); err != nil { //nolint:gosec // G302: the test needs a world-readable file

		t.Fatal(err)
	}
	if _, _, err := loadBearer(p); err == nil {
		t.Error("world-readable credential file accepted")
	}
}

func TestServerRun(t *testing.T) {
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(context.Background(), ServerOptions{Listener: ln, Gatherer: New(Options{}).Registry(), Logger: discard()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+s.Addr().String()+"/metrics", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if cerr := resp.Body.Close(); err != nil || cerr != nil {
		t.Fatal(err, cerr)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "bekup_backup_runs_total") {
		t.Errorf("scrape = %d", resp.StatusCode)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop")
	}
	if err := s.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Error(err)
	}
}

// failingDB fails every query (the database is down).
type failingDB struct{}

var errDown = errors.New("down")

func (failingDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errDown
}
func (failingDB) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, errDown }
func (failingDB) QueryRow(context.Context, string, ...any) pgx.Row        { return errRow{} }

type errRow struct{}

func (errRow) Scan(...any) error { return errDown }

type fakeRunner struct {
	calls int
	resp  *runnerpb.HealthResponse
	err   error
}

func (f *fakeRunner) Health(context.Context) (*runnerpb.HealthResponse, error) {
	f.calls++
	return f.resp, f.err
}

func TestHealthSnapshotAndCollector(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	clock := now
	notAfter := now.Add(10 * 24 * time.Hour)
	r := &fakeRunner{resp: &runnerpb.HealthResponse{
		Version: "1.0.0", DockerStatus: runnerpb.DockerStatus_DOCKER_STATUS_OK, ActiveRuns: 2, MaxConcurrentRuns: 4,
		CertificateNotAfter: timestamppb.New(notAfter),
	}}
	m := New(Options{Now: func() time.Time { return clock }})
	h, err := NewHealth(HealthOptions{
		DB: failingDB{}, Runner: r, Metrics: m, SchedulerEnabled: true, Logger: discard(),
		Certs: []CertFile{{Component: CertRunnerCA, Path: filepath.Join(t.TempDir(), "missing.crt")}},
		Now:   func() time.Time { return clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	s := h.Snapshot(context.Background())
	if !s.Runner.Up || !s.Runner.DockerOK || s.Runner.Version != "1.0.0" || s.Runner.ActiveRuns != 2 {
		t.Errorf("runner = %+v", s.Runner)
	}
	if s.QueueOK || s.Queue != nil {
		t.Error("queue reported although the DB is down")
	}
	if len(s.Certificates) != 1 || s.Certificates[0].Component != CertRunnerServer || !s.Certificates[0].NotAfter.Equal(notAfter) {
		t.Errorf("certificates = %+v", s.Certificates)
	}
	if s.LastSchedulerTick != nil {
		t.Error("tick without a tick")
	}
	// Cached within the TTL.
	h.Snapshot(context.Background())
	if r.calls != 1 {
		t.Errorf("runner called %d times within the TTL", r.calls)
	}
	m.TickCompleted(time.Second, nil)
	if s := h.Snapshot(context.Background()); s.LastSchedulerTick == nil {
		t.Error("tick not reported")
	}
	clock = clock.Add(DefaultSnapshotTTL)
	r.err = errors.New("unavailable")
	s = h.Snapshot(context.Background())
	if r.calls != 2 || s.Runner.Up {
		t.Errorf("after the TTL: calls %d, runner %+v", r.calls, s.Runner)
	}

	m.Registry().MustRegister(h.Collector())
	mfs := gather(t, m.Registry())
	if v := mfs["bekup_runner_up"].GetMetric()[0].GetGauge().GetValue(); v != 0 {
		t.Errorf("runner_up = %v", v)
	}
	if v := mfs["bekup_monitor_db_ok"].GetMetric()[0].GetGauge().GetValue(); v != 0 {
		t.Errorf("monitor_db_ok = %v", v)
	}
	if mfs["bekup_backup_last_success_timestamp_seconds"] != nil {
		t.Error("per-job series without per_job_labels")
	}
}

func TestCollectorCertsAndPerJob(t *testing.T) {
	now := time.Now()
	h := &Health{o: HealthOptions{PerJobLabels: true, Now: func() time.Time { return now }, TTL: time.Hour}}
	pin := now.Add(48 * time.Hour)
	h.have = true
	h.last = Snapshot{
		CheckedAt:    now,
		Runner:       RunnerStatus{Up: true, DockerOK: true},
		Certificates: []CertExpiry{{Component: CertRunnerClient, NotAfter: now.Add(time.Hour)}},
		Queue:        []QueueDepth{{Queue: "backup", State: "available", Jobs: 3}}, QueueOK: true,
		EarliestPinNotAfter: &pin,
		LastSuccess:         []JobLastSuccess{{Slug: "nightly", At: now.Add(-time.Hour)}},
		OldestLastSuccess:   ptr(now.Add(-time.Hour)),
	}
	m := New(Options{})
	m.Registry().MustRegister(h.Collector())
	mfs := gather(t, m.Registry())
	if v := mfs["bekup_cert_expiry_seconds"].GetMetric()[0].GetGauge().GetValue(); v < 3590 || v > 3600 {
		t.Errorf("cert expiry = %v", v)
	}
	if v := mfs["bekup_db_pin_expiry_seconds_min"].GetMetric()[0].GetGauge().GetValue(); v < 47*3600 {
		t.Errorf("pin expiry = %v", v)
	}
	if v := mfs["bekup_queue_jobs"].GetMetric()[0].GetGauge().GetValue(); v != 3 {
		t.Errorf("queue jobs = %v", v)
	}
	ls := mfs["bekup_backup_last_success_timestamp_seconds"]
	if ls == nil || labelValue(ls.GetMetric()[0], "job") != "nightly" {
		t.Errorf("per-job series = %v", ls)
	}
	if mfs["bekup_runner_docker_ok"].GetMetric()[0].GetGauge().GetValue() != 1 {
		t.Error("docker_ok")
	}
}

func ptr[T any](v T) *T { return &v }
