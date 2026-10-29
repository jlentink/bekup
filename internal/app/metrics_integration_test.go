//go:build integration

package app_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/app"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// scrape GETs /metrics with an optional bearer credential.
func scrape(t *testing.T, addr, bearer string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/metrics", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b)
}

// runMetricsApp starts a and stops it at the end of the test.
func runMetricsApp(t *testing.T, a *app.App) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("serve did not stop")
		}
	})
}

func TestMetricsEndpoint(t *testing.T) {
	d := dbtest.Migrated(t)
	a, err := app.New(t.Context(), options(t, d.AppURL, &syncBuffer{}))
	if err != nil {
		t.Fatal(err)
	}
	runMetricsApp(t, a)
	addr := a.MetricsAddr()
	if addr == nil || addr.String() == a.Addr().String() {
		t.Fatalf("metrics address %v (serve %v)", addr, a.Addr())
	}

	// A fake finished run is counted (the engine of an unknown job is
	// "unknown").
	start := time.Now().Add(-time.Minute)
	end := time.Now()
	app.Metrics(a).RunFinished(t.Context(), sqlc.Run{
		JobID: uuid.New(), Status: sqlc.RunStatusSuccess, Trigger: sqlc.RunTriggerManual,
		StartedAt: &start, FinishedAt: &end, BytesStored: 1234, BytesRaw: 5678,
	})
	code, body := scrape(t, addr.String(), "")
	if code != http.StatusOK {
		t.Fatalf("scrape: %d", code)
	}
	for _, want := range []string{
		`bekup_backup_runs_total{engine="unknown",status="success",trigger="manual"} 1`,
		`bekup_backup_bytes_stored_total 1234`,
		`bekup_backup_bytes_raw_total 5678`,
		`bekup_backup_run_duration_seconds_count{engine="unknown"} 1`,
		`version="9.9.9"} 1`,
		`bekup_runner_up 0`,
		`bekup_runner_docker_ok 0`,
		`bekup_monitor_db_ok 1`,
		`bekup_cert_expiry_seconds{component="runner_client"}`,
		`bekup_cert_expiry_seconds{component="runner_ca"}`,
		`bekup_cert_expiry_seconds{component="postgres_ca"}`,
		`bekup_scheduler_ticks_total`,
		`go_goroutines`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape lacks %q", want)
		}
	}
	// Per-job series are off by default.
	if strings.Contains(body, "bekup_backup_last_success_timestamp_seconds{") {
		t.Error("per-job series without metrics.per_job_labels")
	}
	// Only /metrics and /healthz; serve's routes are not here.
	for path, want := range map[string]int{"/healthz": 200, "/graphql": 404, "/": 404, "/readyz": 404} {
		resp := get(t, "http://"+addr.String()+path)
		if resp.StatusCode != want {
			t.Errorf("GET %s: %d, want %d", path, resp.StatusCode, want)
		}
	}
	// And /metrics is not on serve's listener (it may answer with the SPA
	// shell, never with metrics).
	if _, body := scrape(t, a.Addr().String(), ""); strings.Contains(body, "bekup_build_info") {
		t.Error("serve's listener exposes the metrics")
	}
}

func TestMetricsBearer(t *testing.T) {
	d := dbtest.Migrated(t)
	o := options(t, d.AppURL, &syncBuffer{})
	const cred = "f3b1c2d4e5f60718293a4b5c6d7e8f90-metrics"
	p := filepath.Join(t.TempDir(), "metrics_bearer")
	if err := os.WriteFile(p, []byte(cred+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	o.Config.Metrics.AuthBearerFile = p
	a, err := app.New(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	runMetricsApp(t, a)
	addr := a.MetricsAddr().String()
	if code, body := scrape(t, addr, ""); code != http.StatusUnauthorized || strings.Contains(body, "bekup_") {
		t.Errorf("without credential: %d", code)
	}
	if code, _ := scrape(t, addr, "wrong-wrong-wrong-wrong-wrong-wrong"); code != http.StatusUnauthorized {
		t.Errorf("wrong credential: %d", code)
	}
	if code, body := scrape(t, addr, cred); code != http.StatusOK || !strings.Contains(body, "bekup_build_info") {
		t.Errorf("with credential: %d", code)
	}
	if resp := get(t, "http://"+addr+"/healthz"); resp.StatusCode != http.StatusOK {
		t.Errorf("healthz: %d", resp.StatusCode)
	}

	// A too short credential refuses startup.
	short := filepath.Join(t.TempDir(), "metrics_bearer")
	if err := os.WriteFile(short, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	o2 := options(t, d.AppURL, &syncBuffer{})
	o2.Config.Metrics.AuthBearerFile = short
	if _, err := app.New(t.Context(), o2); err == nil || !strings.Contains(err.Error(), "metrics.auth_bearer_file") {
		t.Errorf("short credential: %v", err)
	}
}

func TestMetricsDisabled(t *testing.T) {
	d := dbtest.Migrated(t)
	o := options(t, d.AppURL, &syncBuffer{})
	o.Config.Metrics.Enabled = false
	a, err := app.New(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.MetricsAddr() != nil {
		t.Error("metrics server bound although disabled")
	}
}
