package config

import (
	"errors"
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
	c.Scheduler.MinInterval = Duration(30 * time.Second)
	c.Retention.MaxDeletionsPerSweep = 0
	c.Retention.PartialCleanupAge = Duration(time.Minute)
	c.Restore.TempConnectionTTL = Duration(48 * time.Hour)
	c.Restore.TempConnectionMaxTTL = Duration(24 * time.Hour)
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
		"scheduler.min_interval: must be at least 1m0s",
		"retention.max_deletions_per_sweep: must be 1..100000 (got 0)",
		"retention.partial_cleanup_age: must be at least 1h0m0s",
		"restore.temp_connection_ttl: must be <= restore.temp_connection_max_ttl",
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

func TestRunSubnetPool(t *testing.T) {
	for _, tc := range []struct {
		pool string
		ok   bool
	}{
		{"10.214.0.0/16", true},
		{"172.16.0.0/12", true},
		{"192.168.200.0/24", true},
		{"10.0.0.0/8", true},
		{"10.214.0.1/16", false}, // not canonical
		{"10.214.0.0/25", false}, // too small
		{"8.8.0.0/16", false},    // public
		{"172.0.0.0/8", false},   // wider than the private range
		{"fd00::/64", false},     // IPv6
		{"", false},
		{"nope", false},
	} {
		var v validator
		validateRunSubnetPool(&v, "docker.run_subnet_pool", tc.pool)
		if ok := len(v.errs) == 0; ok != tc.ok {
			t.Errorf("%q: ok=%t, want %t (%v)", tc.pool, ok, tc.ok, v.errs)
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
	r.Docker.RunSubnetPool = "8.8.0.0/16"
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
		"docker.run_subnet_pool: must lie inside 10.0.0.0/8",
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

// TestRunnerTargetHostLists: IP literals in the runner's host lists are
// rejected at config validation (they would fail only at runner start),
// and bastion_targets entries are checked.
func TestRunnerTargetHostLists(t *testing.T) {
	r := DefaultRunner()
	r.Targets.AllowHosts = []string{"10.0.0.5", "db.example.com", "[2001:db8::1]", "1234", "bad host"}
	r.Targets.DenyHosts = []string{"192.0.2.1."}
	r.Targets.BastionTargets = []string{
		"bastion.example.com=10.30.0.0/24", "bastion.example.com=db.internal", "203.0.113.7=127.0.0.1/32",
		"nobastion", "b.example.com=10.0.0.1", "b.example.com=0.0.0.0/0", "bad host=db", "b.example.com=x/y",
	}
	joined := joinErrs(validateRunner(&r, false))
	for _, w := range []string{
		`targets.allow_hosts: "10.0.0.5" is an IP address; list it in a CIDR list such as 10.0.0.5/32`,
		`targets.allow_hosts: "[2001:db8::1]" is an IP address`,
		`targets.allow_hosts: "1234" is an IP address`,
		`targets.allow_hosts: invalid host name "bad host"`,
		`targets.deny_hosts: "192.0.2.1." is an IP address`,
		`targets.bastion_targets: invalid entry "nobastion"`,
		`targets.bastion_targets: target "10.0.0.1" in "b.example.com=10.0.0.1" is an IP address`,
		`targets.bastion_targets: "0.0.0.0/0" in "b.example.com=0.0.0.0/0" matches every address`,
		`targets.bastion_targets: invalid bastion "bad host"`,
		`targets.bastion_targets: invalid CIDR "x/y"`,
	} {
		if !strings.Contains(joined, w) {
			t.Errorf("missing %q in:\n%s", w, joined)
		}
	}
	for _, ok := range []string{"db.example.com", "bastion.example.com=10.30.0.0/24", "db.internal", "203.0.113.7"} {
		if strings.Contains(joined, `"`+ok+`"`) {
			t.Errorf("%q rejected:\n%s", ok, joined)
		}
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

func TestHostPatterns(t *testing.T) {
	tests := []struct {
		host string
		ok   bool
	}{
		{"minio.internal", true},
		{"nas", true},
		{"*.corp.example", true},
		{"*.example", false},
		{"*", false},
		{"a.*.example.com", false},
		{"*.*.example.com", false},
		{"https://evil", false},
		{"host:9000", false},
		{"10.0.0.1", false},
		{"*.10.0.0.1", false},
	}
	for _, tc := range tests {
		var v validator
		v.hostPatterns("netguard.allow_hosts", []string{tc.host})
		if (len(v.errs) == 0) != tc.ok {
			t.Errorf("hostPatterns(%q) errors = %v, want ok=%v", tc.host, v.errs, tc.ok)
		}
	}
}

func TestDownloadLimits(t *testing.T) {
	c := DefaultBekup()
	c.Workers.MaxConcurrentDownloads = 65
	c.Workers.MaxDownloadsPerUser = 66
	requireErrContains(t, errors.Join(validateBekup(&c, true)...),
		"workers.max_concurrent_downloads: must be at most 64",
		"workers.max_downloads_per_user: must be <= workers.max_concurrent_downloads")
	c.Workers.MaxConcurrentDownloads, c.Workers.MaxDownloadsPerUser = 4, 0
	requireErrContains(t, errors.Join(validateBekup(&c, true)...), "workers.max_downloads_per_user")
}
