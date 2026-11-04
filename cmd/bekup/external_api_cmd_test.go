package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const validExternalConfig = `
[listen]
address = "10.0.0.11:8081"
trusted_proxies = ["10.0.0.5/32"]
`

func TestExternalAPIConfigCommands(t *testing.T) {
	p := writeConfig(t, "external-api.toml", validExternalConfig)
	out, _, err := run(t, "--config", p, "config", "validate", "--external-api")
	if err != nil || !strings.Contains(out, "configuration OK") {
		t.Fatalf("validate: %q %v", out, err)
	}
	out, _, err = run(t, "--config", p, "config", "print", "--external-api")
	if err != nil || !strings.Contains(out, "BEKUP_LIMITS_FAILED_AUTH_LIMIT") && !strings.Contains(out, "failed_auth_limit") {
		t.Fatalf("print: %q %v", out, err)
	}
	if _, _, err := run(t, "--config", p, "config", "validate", "--external-api", "--runner"); err == nil {
		t.Fatal("--runner with --external-api accepted")
	}
	// A bekup.toml is not an external-api.toml.
	bekup := writeConfig(t, "bekup.toml", validConfig)
	if _, _, err := run(t, "--config", bekup, "config", "validate", "--external-api"); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("bekup.toml as external-api.toml: %v", err)
	}
	// The process refuses an invalid file before it connects anywhere.
	bad := writeConfig(t, "external-api.toml", "[listen]\naddress = \"10.0.0.11:8081\"\n")
	if _, _, err := run(t, "--config", bad, "external-api"); err == nil || !strings.Contains(err.Error(), "listen.trusted_proxies") {
		t.Fatalf("external-api with an invalid config: %v", err)
	}
}

func TestHealthcheckExternalAPI(t *testing.T) {
	status := http.StatusOK
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(status)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	p := writeConfig(t, "external-api.toml", strings.Replace(validExternalConfig, "10.0.0.11:8081", addr, 1))
	out, _, err := run(t, "--config", p, "healthcheck", "external-api")
	if err != nil || out != "ok\n" || path != "/healthz" {
		t.Fatalf("healthy: %q %q %v", out, path, err)
	}
	status = http.StatusServiceUnavailable
	if _, _, err := run(t, "--config", p, "healthcheck", "external-api"); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("unhealthy: %v", err)
	}
}
