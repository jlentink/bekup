package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthzURL(t *testing.T) {
	for in, want := range map[string]string{
		":8080":                     "http://127.0.0.1:8080/healthz",
		"0.0.0.0:8080":              "http://127.0.0.1:8080/healthz",
		"[::]:8080":                 "http://[::1]:8080/healthz",
		"[::ffff:0.0.0.0]:8080":     "http://127.0.0.1:8080/healthz",
		"10.213.250.10:8080":        "http://10.213.250.10:8080/healthz",
		"localhost:9000":            "http://localhost:9000/healthz",
		"[fd00::10]:8080":           "http://[fd00::10]:8080/healthz",
		"bekup.internal.example:80": "http://bekup.internal.example:80/healthz",
	} {
		got, err := healthzURL(in)
		if err != nil || got != want {
			t.Errorf("healthzURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "8080", "host:", "[::1"} {
		if _, err := healthzURL(bad); err == nil {
			t.Errorf("healthzURL(%q) accepted", bad)
		}
	}
}

func TestHealthcheckServe(t *testing.T) {
	status := http.StatusOK
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(status)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	cfg := writeConfig(t, "bekup.toml", strings.Replace(validConfig, "10.0.0.10:8080", addr, 1))

	out, _, err := run(t, "--config", cfg, "healthcheck", "serve")
	if err != nil || out != "ok\n" || path != "/healthz" {
		t.Fatalf("healthy: out=%q path=%q err=%v", out, path, err)
	}
	status = http.StatusServiceUnavailable
	if _, _, err := run(t, "--config", cfg, "healthcheck", "serve"); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("unhealthy: %v", err)
	}
	// A redirect is not followed and counts as unhealthy.
	status = http.StatusFound
	if _, _, err := run(t, "--config", cfg, "healthcheck", "serve"); err == nil {
		t.Fatal("redirect accepted")
	}
	srv.Close()
	if _, _, err := run(t, "--config", cfg, "healthcheck", "serve"); err == nil || !strings.Contains(err.Error(), "unhealthy") {
		t.Fatalf("down: %v", err)
	}
}
