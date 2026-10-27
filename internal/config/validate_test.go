package config

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDefaultsValidateWithRequiredKeys(t *testing.T) {
	c := DefaultBekup()
	c.Server.BaseURL = "https://backup.example.com"
	c.Server.TrustedProxies = []string{"172.18.0.0/16"}
	c.Server.Listen = "10.213.250.10:8080"
	if errs := validateBekup(&c, false); len(errs) > 0 {
		t.Fatal(errs)
	}
	r := DefaultRunner()
	r.Listen.Address = "10.213.251.10:7443"
	if errs := validateRunner(&r, false); len(errs) > 0 {
		t.Fatal(errs)
	}
}

func TestDefaultsFailOutsideDevWithoutRequiredKeys(t *testing.T) {
	c := DefaultBekup()
	errs := validateBekup(&c, false)
	joined := joinErrs(errs)
	for _, want := range []string{"server.base_url: is required", "server.trusted_proxies: is required outside dev mode"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
	c.Server.BaseURL = DevBaseURL
	if errs := validateBekup(&c, true); len(errs) > 0 {
		t.Fatalf("defaults must validate in dev mode: %v", errs)
	}
}

func joinErrs(errs []error) string {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "\n")
}

func TestValidationReportsAllErrors(t *testing.T) {
	c := DefaultBekup()
	c.Server.Listen = "8080"
	c.Server.BaseURL = "http://user:pw@backup.example.com/?x=1"
	c.Server.TrustedProxies = []string{"not-a-cidr", "0.0.0.0/0"}
	c.Database.URLFile = "relative/url"
	c.Database.MaxConns = 0
	c.Security.StepUpMaxAge = Duration(time.Hour)
	c.Security.Argon2MemoryKiB = 1024
	c.Security.PasswordMinLength = 8
	c.Security.SessionAbsoluteTimeout = Duration(time.Minute)
	c.Storage.DiskRoot = "/"
	c.Storage.ScratchDir = "/var/lib/../tmp"
	c.Workers.MaxConcurrentRuns = 0
	c.Scheduler.TickInterval = 0
	c.Netguard.AllowHosts = []string{"https://evil"}
	c.Metrics.Listen = c.Server.Listen
	c.Log.Level = "verbose"
	c.Log.Format = "xml"

	want := []string{
		"server.listen: must be host:port",
		"server.base_url: must use https outside dev mode",
		"server.base_url: must not contain credentials",
		"server.base_url: must not contain a query or fragment",
		`server.trusted_proxies: invalid CIDR "not-a-cidr"`,
		`server.trusted_proxies: "0.0.0.0/0" matches every address`,
		"database.url_file: must be an absolute path",
		"database.max_conns: must be greater than 0",
		"security.stepup_max_age: must be at most 5m0s",
		"security.argon2_memory_kib: must be at least 65536",
		"security.password_min_length: must be at least 12",
		"security.session_absolute_timeout: must be >= security.session_idle_timeout",
		"storage.disk_root: must not be the filesystem root",
		"storage.scratch_dir: must be a clean path",
		"workers.max_concurrent_runs: must be greater than 0",
		"scheduler.tick_interval: must be at least 1s",
		`netguard.allow_hosts: invalid host name "https://evil"`,
		"metrics.listen: must be host:port",
		"log.level: must be one of",
		"log.format: must be one of",
	}
	errs := validateBekup(&c, false)
	joined := joinErrs(errs)
	for _, w := range want {
		if !strings.Contains(joined, w) {
			t.Errorf("missing %q", w)
		}
	}
	if t.Failed() {
		t.Logf("errors:\n%s", joined)
	}
}

func TestBaseURLRules(t *testing.T) {
	tests := []struct {
		url string
		dev bool
		ok  bool
	}{
		{"https://backup.example.com", false, true},
		{"https://backup.example.com/bekup", false, true},
		{"http://backup.example.com", false, false},
		{"http://localhost:8080", true, true},
		{"ftp://backup.example.com", true, false},
		{"https://", false, false},
		{"https://backup.example.com#x", false, false},
		{"", true, false},
	}
	for _, tc := range tests {
		var v validator
		validateBaseURL(&v, tc.url, tc.dev)
		if ok := len(v.errs) == 0; ok != tc.ok {
			t.Errorf("%q dev=%t: ok=%t, want %t (%v)", tc.url, tc.dev, ok, tc.ok, v.errs)
		}
	}
}

func TestRunnerValidation(t *testing.T) {
	r := DefaultRunner()
	r.Listen.Address = "nope"
	r.TLS.KeyFile = "runner.key"
	r.TLS.AllowedClientSANs = []string{"*.example.com"}
	r.Docker.Host = "tcp://docker:2375"
	r.Docker.GID = -1
	r.Docker.SelfContainer = "-bad name"
	r.Docker.CPUs = 0
	r.Targets.AllowCIDRs = []string{"127.0.0.1/32", "169.254.169.254/32", "::/0", "10.0.0.0/8"}
	r.Targets.DenyCIDRs = []string{"bad"}
	r.Images.ExtraFile = "images.toml"
	r.Limits.MaxLogLineBytes = 10
	r.Limits.MaxConcurrentRuns = 0

	want := []string{
		"listen.address: must be host:port",
		"tls.key_file: must be an absolute path",
		`tls.allowed_client_sans: invalid SAN "*.example.com"`,
		"docker.host: must be a unix:// socket",
		"docker.gid: must be at least 0",
		`docker.self_container: must be a container ID or name (got "-bad name")`,
		"docker.cpus: must be greater than 0",
		"targets.allow_cidrs: 127.0.0.1/32 overlaps the always-denied range 127.0.0.0/8",
		"targets.allow_cidrs: 169.254.169.254/32 overlaps the always-denied range 169.254.0.0/16",
		`targets.allow_cidrs: "::/0" matches every address`,
		`targets.deny_cidrs: invalid CIDR "bad"`,
		"images.extra_file: must be an absolute path",
		"limits.max_log_line_bytes: must be at least 256",
		"limits.max_concurrent_runs: must be greater than 0",
	}
	joined := joinErrs(validateRunner(&r, false))
	for _, w := range want {
		if !strings.Contains(joined, w) {
			t.Errorf("missing %q", w)
		}
	}
	if strings.Contains(joined, "targets.allow_cidrs: 10.0.0.0/8") {
		t.Errorf("10.0.0.0/8 must be allowed:\n%s", joined)
	}
	if t.Failed() {
		t.Logf("errors:\n%s", joined)
	}
}

func TestLoadAggregatesErrors(t *testing.T) {
	p := writeFile(t, safeDir(t), BekupFileName, "[server]\nbase_url = \"http://x\"\n[workers]\nmax_concurrent_runs = 0\n", 0o600)
	_, err := LoadBekup(opts([]string{p}, map[string]string{"BEKUP_LOG_LEVEL": "loud"}))
	requireErrContains(t, err, "server.base_url", "server.trusted_proxies", "workers.max_concurrent_runs", "log.level")
}

func FuzzValidateListen(f *testing.F) {
	for _, s := range []string{":8080", "0.0.0.0:1", "[::1]:443", "x", ":0", "host:99999"} {
		f.Add(s)
	}
	f.Fuzz(func(_ *testing.T, s string) {
		var v validator
		v.listen("x", s)
	})
}

func TestRunnerAllowedClientSANsScheme(t *testing.T) {
	for san, ok := range map[string]bool{
		"spiffe://bekup/serve":   true,
		"spiffe://bekup/worker":  true,
		"bekup-serve":            false,
		"spiffe://other/serve":   false,
		"spiffe://bekup/":        false,
		"spiffe://bekup/a/b":     false,
		"spiffe://bekup/Serve":   false,
		"spiffe://bekup/serve\t": false,
		"https://bekup/serve":    false,
	} {
		r := DefaultRunner()
		r.TLS.AllowedClientSANs = []string{san}
		errs := validateRunner(&r, false)
		got := !strings.Contains(fmt.Sprint(errs), "tls.allowed_client_sans")
		if got != ok {
			t.Errorf("%q: valid=%v, want %v (%v)", san, got, ok, errs)
		}
	}
}

func TestAuthKeyBounds(t *testing.T) {
	c := DefaultBekup()
	c.Server.UserRateLimitPerSecond = 0
	c.Server.UserRateLimitBurst = 0
	c.Security.PasswordMinLength = 129
	c.Security.PasswordMinScore = 2
	c.Security.Argon2Iterations = 65
	c.Security.Argon2MaxConcurrent = 65
	c.Security.SessionTouchInterval = Duration(time.Second)
	c.Security.LoginRateLimitPerMinute = 0
	c.Security.LoginRateLimitBurst = 101
	c.Security.LockoutThreshold = 2
	c.Security.LockoutBaseDuration = Duration(time.Hour)
	c.Security.LockoutMaxDuration = Duration(time.Minute)
	want := []string{
		"server.user_rate_limit_per_second: must be > 0",
		"server.user_rate_limit_burst: must be between 1 and 100000",
		"security.password_min_length: must be at most 128",
		"security.password_min_score: can only be raised",
		"security.argon2_iterations: must be at most 64",
		"security.argon2_max_concurrent: must be between 1 and 64",
		"security.session_touch_interval: must be at least 10s",
		"security.login_rate_limit_per_minute: must be between 1 and 600",
		"security.login_rate_limit_burst: must be between 1 and 100",
		"security.lockout_threshold: must be between 3 and 20",
		"security.lockout_max_duration: must be at least 10s",
	}
	c.Security.LockoutMaxDuration = Duration(5 * time.Second)
	joined := joinErrs(validateBekup(&c, true))
	for _, w := range want {
		if !strings.Contains(joined, w) {
			t.Errorf("missing %q", w)
		}
	}
	c = DefaultBekup()
	c.Security.SessionTouchInterval = c.Security.SessionIdleTimeout
	c.Security.LockoutBaseDuration = Duration(time.Hour)
	c.Security.LockoutMaxDuration = Duration(30 * time.Minute)
	joined = joinErrs(validateBekup(&c, true))
	for _, w := range []string{
		"security.session_touch_interval: must be at most 10m0s",
		"security.lockout_max_duration: must be >= security.lockout_base_duration",
	} {
		if !strings.Contains(joined, w) {
			t.Errorf("missing %q in:\n%s", w, joined)
		}
	}
}
