package config

import (
	"fmt"
	"math"
	"net"
	"net/netip"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// validator collects every validation error so they are reported at once.
type validator struct {
	errs []error
}

// Upper bounds of the queue settings: River's per-queue maximum and a
// scheduler batch that keeps one transaction short.
const (
	maxQueueWorkers   = 10_000
	maxSchedulerBatch = 1000
	// maxRetentionDeletions bounds retention.max_deletions_per_sweep.
	maxRetentionDeletions = 100_000
	// maxConcurrentDownloads bounds workers.max_concurrent_downloads (a
	// passphrase decryption may need 256 MiB of memory).
	maxConcurrentDownloads = 64
)

func (v *validator) addf(path, format string, args ...any) {
	v.errs = append(v.errs, fmt.Errorf("%s: %s", path, fmt.Sprintf(format, args...)))
}

func (v *validator) positive(path string, n int64) {
	if n <= 0 {
		v.addf(path, "must be greater than 0 (got %d)", n)
	}
}

func (v *validator) atLeast(path string, n, minimum int64) {
	if n < minimum {
		v.addf(path, "must be at least %d (got %d)", minimum, n)
	}
}

func (v *validator) duration(path string, d Duration, minimum, maximum time.Duration) {
	switch {
	case d.D() < minimum || d.D() <= 0:
		v.addf(path, "must be at least %s (got %s)", max(minimum, time.Nanosecond), d)
	case maximum > 0 && d.D() > maximum:
		v.addf(path, "must be at most %s (got %s)", maximum, d)
	}
}

func (v *validator) absPath(path, value string, required bool) {
	if value == "" {
		if required {
			v.addf(path, "is required")
		}
		return
	}
	if !filepath.IsAbs(value) {
		v.addf(path, "must be an absolute path (got %q)", value)
		return
	}
	if filepath.Clean(value) != value {
		v.addf(path, "must be a clean path without '..', '.' or trailing slashes (got %q)", value)
	}
}

func (v *validator) listen(path, value string) {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		v.addf(path, "must be host:port (got %q)", value)
		return
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		v.addf(path, "invalid port %q", port)
	}
	if host != "" && strings.ContainsAny(host, " /") {
		v.addf(path, "invalid host %q", host)
	}
}

// listenNoWildcard refuses a wildcard/unspecified listen address outside dev
// mode unless the explicit override key is set.
func (v *validator) listenNoWildcard(path, value string, dev, allowed bool, overrideKey, example string) {
	if dev || allowed || !isWildcardListen(value) {
		return
	}
	v.addf(path, "%q listens on every interface; bind the internal network address (e.g. %s) or set %s = true", value, example, overrideKey)
}

// listenHostKind normalizes the host part of a listen address: "*" for the
// unspecified address (empty host, 0.0.0.0, ::, ::ffff:0.0.0.0, "0"),
// "loopback" for localhost/127.0.0.1/::1, the canonical IP otherwise and the
// lower-cased name for host names.
func listenHostKind(host string) string {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	switch h {
	case "", "0", "*":
		return "*"
	case "localhost":
		return "loopback"
	}
	ip, err := netip.ParseAddr(h)
	if err != nil {
		return h
	}
	ip = ip.Unmap().WithZone("")
	switch {
	case ip.IsUnspecified():
		return "*"
	case ip == netip.IPv6Loopback() || ip == netip.AddrFrom4([4]byte{127, 0, 0, 1}):
		return "loopback"
	}
	return ip.String()
}

// isWildcardListen reports whether a host:port listen address binds every
// interface.
func isWildcardListen(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	return err == nil && listenHostKind(host) == "*"
}

// listenersCollide reports whether two listen addresses would compete for
// the same socket: equal ports and equal hosts, where a wildcard host
// overlaps every host.
func listenersCollide(a, b string) bool {
	ha, pa, errA := net.SplitHostPort(a)
	hb, pb, errB := net.SplitHostPort(b)
	if errA != nil || errB != nil {
		return a == b
	}
	na, errA := strconv.Atoi(pa)
	nb, errB := strconv.Atoi(pb)
	if errA != nil || errB != nil || na != nb {
		return false
	}
	ka, kb := listenHostKind(ha), listenHostKind(hb)
	return ka == "*" || kb == "*" || ka == kb
}

// cidrs validates a CIDR list; /0 prefixes are rejected because they disable
// the control the list exists for.
func (v *validator) cidrs(path string, values []string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(values))
	for _, s := range values {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			v.addf(path, "invalid CIDR %q (use e.g. 10.0.0.0/8 or 192.0.2.1/32)", s)
			continue
		}
		if p.Bits() == 0 {
			v.addf(path, "%q matches every address; list specific networks instead", s)
			continue
		}
		out = append(out, p.Masked())
	}
	return out
}

// containerRefRE matches Docker container IDs and names.
var containerRefRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// clientNameRE is the component name in a client identity spiffe://bekup/<name>.
var clientNameRE = regexp.MustCompile(`^[a-z0-9-]{1,63}$`)

var hostnameRE = regexp.MustCompile(`^[A-Za-z0-9_]([A-Za-z0-9_-]*[A-Za-z0-9_])?(\.[A-Za-z0-9_]([A-Za-z0-9_-]*[A-Za-z0-9_])?)*$`)

// targetHosts validates the runner's host lists: exact host names only.
// IP literals (and all-numeric names, ambiguous with them) are refused
// here so the runner does not fail only at startup; IPs belong in a CIDR
// list.
func (v *validator) targetHosts(path string, values []string) {
	for _, h := range values {
		name := strings.TrimSuffix(h, ".")
		if _, err := netip.ParseAddr(strings.Trim(name, "[]")); err == nil || strings.Trim(name, "0123456789.") == "" {
			v.addf(path, "%q is an IP address; list it in a CIDR list such as %s/32", h, strings.Trim(name, "[]"))
			continue
		}
		if len(h) > 253 || !hostnameRE.MatchString(name) {
			v.addf(path, "invalid host name %q (no scheme, port or wildcard)", h)
		}
	}
}

// bastionTargets validates "<bastion>=<CIDR or host name>" entries.
func (v *validator) bastionTargets(path string, values []string) {
	for _, e := range values {
		b, t, ok := strings.Cut(e, "=")
		b, t = strings.TrimSpace(b), strings.TrimSpace(t)
		if !ok || b == "" || t == "" {
			v.addf(path, "invalid entry %q (want <bastion>=<CIDR or host name>, e.g. bastion.example.com=10.20.0.0/16)", e)
			continue
		}
		if _, err := netip.ParseAddr(strings.Trim(b, "[]")); err != nil && (len(b) > 253 || !hostnameRE.MatchString(strings.TrimSuffix(b, ".")) || strings.Trim(b, "0123456789.") == "") {
			v.addf(path, "invalid bastion %q in %q (a host name or an IP address)", b, e)
			continue
		}
		if strings.Contains(t, "/") {
			p, err := netip.ParsePrefix(t)
			if err != nil {
				v.addf(path, "invalid CIDR %q in %q", t, e)
			} else if p.Bits() == 0 {
				v.addf(path, "%q in %q matches every address; list specific networks instead", t, e)
			}
			continue
		}
		name := strings.TrimSuffix(t, ".")
		if _, err := netip.ParseAddr(strings.Trim(name, "[]")); err == nil || strings.Trim(name, "0123456789.") == "" {
			v.addf(path, "target %q in %q is an IP address; use a CIDR such as %s/32", t, e, strings.Trim(name, "[]"))
			continue
		}
		if len(t) > 253 || !hostnameRE.MatchString(name) {
			v.addf(path, "invalid target host name %q in %q", t, e)
		}
	}
}

// hostPatterns validates host allowlist entries that may also be
// "*.suffix" wildcards with at least two labels after "*.".
// IP literals belong in a CIDR list.
func (v *validator) hostPatterns(path string, values []string) {
	for _, h := range values {
		name, wild := strings.CutPrefix(h, "*.")
		if len(name) > 253 || !hostnameRE.MatchString(name) || (wild && !strings.Contains(name, ".")) {
			v.addf(path, "invalid host name %q (exact name or *.example.com; no scheme or port)", h)
			continue
		}
		if _, err := netip.ParseAddr(name); err == nil {
			v.addf(path, "%q is an IP address; use a CIDR such as %s/32", h, name)
		}
	}
}

func (v *validator) oneOf(path, value string, allowed ...string) {
	for _, a := range allowed {
		if value == a {
			return
		}
	}
	v.addf(path, "must be one of %s (got %q)", strings.Join(allowed, ", "), value)
}

func validateBekup(c *Bekup, dev bool) []error {
	var v validator

	// [server]
	v.listen("server.listen", c.Server.Listen)
	v.listenNoWildcard("server.listen", c.Server.Listen, dev, c.Server.AllowWildcardListen,
		"server.allow_wildcard_listen", "10.213.250.10:8080")
	httpAllowed := dev || c.Server.AllowInsecureHTTP
	validateBaseURL(&v, c.Server.BaseURL, httpAllowed)
	v.cidrs("server.trusted_proxies", c.Server.TrustedProxies)
	// Without TLS there is no reverse proxy to trust (D-095).
	noProxy := httpAllowed && c.InsecureTransport()
	if len(c.Server.TrustedProxies) == 0 && !dev && !noProxy {
		v.addf("server.trusted_proxies", "is required outside dev mode (CIDRs of the reverse proxy)")
	}
	v.duration("server.read_header_timeout", c.Server.ReadHeaderTimeout, time.Second, 0)
	v.duration("server.read_timeout", c.Server.ReadTimeout, time.Second, 0)
	if c.Server.ReadTimeout < c.Server.ReadHeaderTimeout {
		v.addf("server.read_timeout", "must be >= server.read_header_timeout")
	}
	v.duration("server.write_timeout", c.Server.WriteTimeout, time.Second, 0)
	v.duration("server.idle_timeout", c.Server.IdleTimeout, time.Second, 0)
	v.duration("server.shutdown_timeout", c.Server.ShutdownTimeout, time.Second, 0)
	if c.Server.MaxHeaderBytes < 4<<10 || c.Server.MaxHeaderBytes > 1<<20 {
		v.addf("server.max_header_bytes", "must be between 4096 and 1048576 (got %d)", c.Server.MaxHeaderBytes)
	}
	v.positive("server.max_body_bytes", c.Server.MaxBodyBytes)
	if r := c.Server.RateLimitPerSecond; math.IsNaN(r) || r <= 0 || r > 10000 {
		v.addf("server.rate_limit_per_second", "must be > 0 and <= 10000 (got %v)", c.Server.RateLimitPerSecond)
	}
	if c.Server.RateLimitBurst < 1 || c.Server.RateLimitBurst > 100000 {
		v.addf("server.rate_limit_burst", "must be between 1 and 100000 (got %d)", c.Server.RateLimitBurst)
	}
	if c.Server.RateLimitMaxClients < 100 || c.Server.RateLimitMaxClients > 10000000 {
		v.addf("server.rate_limit_max_clients", "must be between 100 and 10000000 (got %d)", c.Server.RateLimitMaxClients)
	}
	if r := c.Server.UserRateLimitPerSecond; math.IsNaN(r) || r <= 0 || r > 10000 {
		v.addf("server.user_rate_limit_per_second", "must be > 0 and <= 10000 (got %v)", c.Server.UserRateLimitPerSecond)
	}
	if c.Server.UserRateLimitBurst < 1 || c.Server.UserRateLimitBurst > 100000 {
		v.addf("server.user_rate_limit_burst", "must be between 1 and 100000 (got %d)", c.Server.UserRateLimitBurst)
	}

	// [graphql]
	if c.GraphQL.MaxComplexity < 50 || c.GraphQL.MaxComplexity > 100000 {
		v.addf("graphql.max_complexity", "must be between 50 and 100000 (got %d)", c.GraphQL.MaxComplexity)
	}
	if c.GraphQL.MaxDepth < 3 || c.GraphQL.MaxDepth > 50 {
		v.addf("graphql.max_depth", "must be between 3 and 50 (got %d)", c.GraphQL.MaxDepth)
	}
	if c.GraphQL.MaxBodyBytes < 1024 || c.GraphQL.MaxBodyBytes > c.Server.MaxBodyBytes {
		v.addf("graphql.max_body_bytes", "must be between 1024 and server.max_body_bytes (got %d)", c.GraphQL.MaxBodyBytes)
	}
	if c.GraphQL.StreamMaxPerUser < 1 || c.GraphQL.StreamMaxPerUser > 100 {
		v.addf("graphql.stream_max_per_user", "must be between 1 and 100 (got %d)", c.GraphQL.StreamMaxPerUser)
	}
	if c.GraphQL.StreamMaxPerSession < 1 || c.GraphQL.StreamMaxPerSession > 20 || c.GraphQL.StreamMaxPerSession > max(c.GraphQL.StreamMaxPerUser, 1) {
		v.addf("graphql.stream_max_per_session", "must be between 1 and 20 and at most graphql.stream_max_per_user (got %d)", c.GraphQL.StreamMaxPerSession)
	}
	if c.GraphQL.StreamMaxTotal < 1 || c.GraphQL.StreamMaxTotal > 100000 {
		v.addf("graphql.stream_max_total", "must be between 1 and 100000 (got %d)", c.GraphQL.StreamMaxTotal)
	}
	v.duration("graphql.stream_keepalive_interval", c.GraphQL.StreamKeepaliveInterval, 5*time.Second, time.Minute)
	v.duration("graphql.stream_revalidate_interval", c.GraphQL.StreamRevalidateInterval, 5*time.Second, 5*time.Minute)
	v.duration("graphql.stream_session_check_ttl", c.GraphQL.StreamSessionCheckTTL, time.Second, max(c.GraphQL.StreamRevalidateInterval.D(), time.Second))

	// [database]
	v.absPath("database.url_file", c.Database.URLFile, !dev || c.Database.URL == "")
	v.absPath("database.owner_url_file", c.Database.OwnerURLFile, !dev || c.Database.OwnerURL == "")
	v.positive("database.max_conns", int64(c.Database.MaxConns))
	if c.Database.MinConns < 0 || c.Database.MinConns > c.Database.MaxConns {
		v.addf("database.min_conns", "must be between 0 and database.max_conns (got %d)", c.Database.MinConns)
	}
	v.duration("database.conn_max_lifetime", c.Database.ConnMaxLifetime, time.Second, 0)
	v.duration("database.conn_max_idle_time", c.Database.ConnMaxIdleTime, time.Second, 0)
	v.duration("database.connect_timeout", c.Database.ConnectTimeout, time.Second, 0)
	v.duration("database.statement_timeout", c.Database.StatementTimeout, time.Second, time.Hour)
	validateDatabaseTLS(&v, &c.Database)

	// [security]
	v.absPath("security.master_key_file", c.Security.MasterKeyFile, !dev || c.Security.MasterKey == "")
	v.duration("security.session_idle_timeout", c.Security.SessionIdleTimeout, time.Minute, 0)
	v.duration("security.session_absolute_timeout", c.Security.SessionAbsoluteTimeout, time.Minute, 30*24*time.Hour)
	if c.Security.SessionAbsoluteTimeout < c.Security.SessionIdleTimeout {
		v.addf("security.session_absolute_timeout", "must be >= security.session_idle_timeout")
	}
	v.duration("security.stepup_max_age", c.Security.StepUpMaxAge, time.Second, 5*time.Minute)
	v.atLeast("security.argon2_memory_kib", int64(c.Security.Argon2MemoryKiB), 64*1024)
	if c.Security.Argon2MemoryKiB > 4*1024*1024 {
		v.addf("security.argon2_memory_kib", "must be at most 4194304 (4 GiB)")
	}
	v.atLeast("security.argon2_iterations", int64(c.Security.Argon2Iterations), 3)
	if c.Security.Argon2Iterations > 64 {
		v.addf("security.argon2_iterations", "must be at most 64 (got %d)", c.Security.Argon2Iterations)
	}
	if c.Security.Argon2Parallelism < 1 || c.Security.Argon2Parallelism > 255 {
		v.addf("security.argon2_parallelism", "must be between 1 and 255 (got %d)", c.Security.Argon2Parallelism)
	}
	if c.Security.Argon2MaxConcurrent < 1 || c.Security.Argon2MaxConcurrent > 64 {
		v.addf("security.argon2_max_concurrent", "must be between 1 and 64 (got %d)", c.Security.Argon2MaxConcurrent)
	}
	v.atLeast("security.password_min_length", int64(c.Security.PasswordMinLength), 12)
	if c.Security.PasswordMinLength > 128 {
		v.addf("security.password_min_length", "must be at most 128 (got %d)", c.Security.PasswordMinLength)
	}
	if c.Security.PasswordMinScore < 3 || c.Security.PasswordMinScore > 4 {
		v.addf("security.password_min_score", "can only be raised: must be 3 or 4 (got %d)", c.Security.PasswordMinScore)
	}
	v.duration("security.session_touch_interval", c.Security.SessionTouchInterval, 10*time.Second, 10*time.Minute)
	if c.Security.SessionTouchInterval >= c.Security.SessionIdleTimeout {
		v.addf("security.session_touch_interval", "must be less than security.session_idle_timeout")
	}
	if c.Security.LoginRateLimitPerMinute < 1 || c.Security.LoginRateLimitPerMinute > 600 {
		v.addf("security.login_rate_limit_per_minute", "must be between 1 and 600 (got %d)", c.Security.LoginRateLimitPerMinute)
	}
	if c.Security.LoginRateLimitBurst < 1 || c.Security.LoginRateLimitBurst > 100 {
		v.addf("security.login_rate_limit_burst", "must be between 1 and 100 (got %d)", c.Security.LoginRateLimitBurst)
	}
	if c.Security.LockoutThreshold < 3 || c.Security.LockoutThreshold > 20 {
		v.addf("security.lockout_threshold", "must be between 3 and 20 (got %d)", c.Security.LockoutThreshold)
	}
	v.duration("security.lockout_base_duration", c.Security.LockoutBaseDuration, 10*time.Second, time.Hour)
	v.duration("security.lockout_max_duration", c.Security.LockoutMaxDuration, 10*time.Second, 24*time.Hour)
	if c.Security.LockoutMaxDuration < c.Security.LockoutBaseDuration {
		v.addf("security.lockout_max_duration", "must be >= security.lockout_base_duration")
	}
	v.duration("security.audit_head_sign_interval", c.Security.AuditHeadSignInterval, time.Minute, 24*time.Hour)

	// [runner]
	v.listen("runner.address", c.Runner.Address)
	if c.Runner.ServerName == "" {
		v.addf("runner.server_name", "is required")
	}
	v.absPath("runner.cert_file", c.Runner.CertFile, true)
	v.absPath("runner.key_file", c.Runner.KeyFile, true)
	v.absPath("runner.ca_file", c.Runner.CAFile, true)
	v.duration("runner.dial_timeout", c.Runner.DialTimeout, time.Second, 0)

	// [storage]
	v.absPath("storage.disk_root", c.Storage.DiskRoot, true)
	if c.Storage.DiskRoot == "/" {
		v.addf("storage.disk_root", "must not be the filesystem root")
	}
	v.absPath("storage.scratch_dir", c.Storage.ScratchDir, true)
	if c.Storage.ScratchReserveBytes < 0 || c.Storage.ScratchReserveBytes > 1<<40 {
		v.addf("storage.scratch_reserve_bytes", "must be 0..1099511627776 (got %d)", c.Storage.ScratchReserveBytes)
	}
	v.duration("storage.scratch_stale_after", c.Storage.ScratchStaleAfter, time.Hour, 30*24*time.Hour)
	if c.Storage.ScratchStaleAfter.D() <= c.Workers.RestoreTimeout.D() {
		v.addf("storage.scratch_stale_after", "must be longer than workers.restore_timeout")
	}

	// [workers]
	v.positive("workers.max_concurrent_runs", int64(c.Workers.MaxConcurrentRuns))
	v.positive("workers.max_runs_per_connection", int64(c.Workers.MaxRunsPerConnection))
	if c.Workers.MaxRunsPerConnection > c.Workers.MaxConcurrentRuns {
		v.addf("workers.max_runs_per_connection", "must be <= workers.max_concurrent_runs")
	}
	v.positive("workers.max_concurrent_downloads", int64(c.Workers.MaxConcurrentDownloads))
	if c.Workers.MaxConcurrentDownloads > maxConcurrentDownloads {
		v.addf("workers.max_concurrent_downloads", "must be at most %d (got %d)", maxConcurrentDownloads, c.Workers.MaxConcurrentDownloads)
	}
	v.positive("workers.max_downloads_per_user", int64(c.Workers.MaxDownloadsPerUser))
	if c.Workers.MaxDownloadsPerUser > c.Workers.MaxConcurrentDownloads {
		v.addf("workers.max_downloads_per_user", "must be <= workers.max_concurrent_downloads")
	}
	v.duration("workers.backup_timeout", c.Workers.BackupTimeout, time.Minute, 0)
	v.duration("workers.restore_timeout", c.Workers.RestoreTimeout, time.Minute, 0)
	v.duration("workers.notify_timeout", c.Workers.NotifyTimeout, time.Second, 0)
	v.duration("workers.retention_timeout", c.Workers.RetentionTimeout, time.Minute, 0)
	v.duration("workers.shutdown_timeout", c.Workers.ShutdownTimeout, time.Second, time.Hour)
	for _, q := range []struct {
		key string
		n   int
	}{
		{"workers.backup_queue_workers", c.Workers.BackupQueueWorkers},
		{"workers.restore_queue_workers", c.Workers.RestoreQueueWorkers},
		{"workers.retention_queue_workers", c.Workers.RetentionQueueWorkers},
		{"workers.notify_queue_workers", c.Workers.NotifyQueueWorkers},
		{"workers.maintenance_queue_workers", c.Workers.MaintenanceQueueWorkers},
	} {
		v.positive(q.key, int64(q.n))
		if q.n > maxQueueWorkers {
			v.addf(q.key, "must be at most %d (got %d)", maxQueueWorkers, q.n)
		}
	}

	if n := c.Workers.RunLogMaxLines; n < 100 || n > 1_000_000 {
		v.addf("workers.run_log_max_lines", "must be 100..1000000 (got %d)", n)
	}
	if n := c.Workers.RunLogMaxLineBytes; n < 128 || n > 16384 {
		v.addf("workers.run_log_max_line_bytes", "must be 128..16384 (got %d)", n)
	}

	// [scheduler]
	v.duration("scheduler.tick_interval", c.Scheduler.TickInterval, time.Second, time.Hour)
	v.duration("scheduler.min_interval", c.Scheduler.MinInterval, time.Minute, 24*time.Hour)
	v.positive("scheduler.batch_size", int64(c.Scheduler.BatchSize))
	if c.Scheduler.BatchSize > maxSchedulerBatch {
		v.addf("scheduler.batch_size", "must be at most %d (got %d)", maxSchedulerBatch, c.Scheduler.BatchSize)
	}

	// [retention]
	if n := c.Retention.MaxDeletionsPerSweep; n < 1 || n > maxRetentionDeletions {
		v.addf("retention.max_deletions_per_sweep", "must be 1..%d (got %d)", maxRetentionDeletions, n)
	}
	v.duration("retention.partial_cleanup_age", c.Retention.PartialCleanupAge, time.Hour, 30*24*time.Hour)

	// [restore]
	v.duration("restore.temp_connection_max_ttl", c.Restore.TempConnectionMaxTTL, 5*time.Minute, 30*24*time.Hour)
	v.duration("restore.temp_connection_ttl", c.Restore.TempConnectionTTL, 5*time.Minute, 30*24*time.Hour)
	if c.Restore.TempConnectionTTL.D() > c.Restore.TempConnectionMaxTTL.D() {
		v.addf("restore.temp_connection_ttl", "must be <= restore.temp_connection_max_ttl")
	}

	// [notifications]
	v.positive("notifications.max_per_user_per_hour", int64(c.Notifications.MaxPerUserPerHour))
	v.duration("notifications.collapse_window", c.Notifications.CollapseWindow, time.Second, 0)
	v.positive("notifications.webhook_failure_threshold", int64(c.Notifications.WebhookFailureThreshold))

	// [netguard]
	v.cidrs("netguard.allow_cidrs", c.Netguard.AllowCIDRs)
	v.hostPatterns("netguard.allow_hosts", c.Netguard.AllowHosts)

	// [metrics]
	if c.Metrics.Enabled {
		v.listen("metrics.listen", c.Metrics.Listen)
		if listenersCollide(c.Metrics.Listen, c.Server.Listen) {
			v.addf("metrics.listen", "must differ from server.listen (metrics stay on an internal port)")
		}
	}

	// [log]
	v.oneOf("log.level", c.Log.Level, "debug", "info", "warn", "error")
	v.oneOf("log.format", c.Log.Format, "json", "text")

	return v.errs
}

// validateDatabaseTLS checks the database.tls_* keys (D-100).
func validateDatabaseTLS(v *validator, d *Database) {
	v.oneOf("database.tls_mode", d.TLSMode, DBTLSVerifyFull, DBTLSRequire, DBTLSDisable)
	if d.TLSMode == DBTLSVerifyFull && d.TLSCAFile == "" {
		v.addf("database.tls_ca_file", `is required with tls_mode = "verify-full" (the CA that signs the Postgres server certificate; system roots are never used)`)
	}
	v.absPath("database.tls_ca_file", d.TLSCAFile, false)
	v.absPath("database.tls_client_cert_file", d.TLSClientCertFile, false)
	v.absPath("database.tls_client_key_file", d.TLSClientKeyFile, false)
	if (d.TLSClientCertFile == "") != (d.TLSClientKeyFile == "") {
		v.addf("database.tls_client_cert_file", "must be set together with database.tls_client_key_file")
	}
	if d.TLSMode == DBTLSDisable && d.TLSClientCertFile != "" {
		v.addf("database.tls_client_cert_file", `has no effect with tls_mode = "disable"`)
	}
}

// validateBaseURL checks server.base_url; httpAllowed is true in dev mode or
// with server.allow_insecure_http (D-095).
func validateBaseURL(v *validator, raw string, httpAllowed bool) {
	const path = "server.base_url"
	if raw == "" {
		v.addf(path, "is required (the public https URL served by the reverse proxy)")
		return
	}
	u, err := url.Parse(raw)
	if err != nil {
		v.addf(path, "is not a valid URL")
		return
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && httpAllowed:
	case u.Scheme == "http":
		v.addf(path, "must use https outside dev mode (or set server.allow_insecure_http = true to knowingly run without TLS)")
	default:
		v.addf(path, "must be an https URL (got scheme %q)", u.Scheme)
	}
	if u.Host == "" {
		v.addf(path, "must include a host")
	}
	if u.User != nil {
		v.addf(path, "must not contain credentials")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		v.addf(path, "must not contain a query or fragment")
	}
}

// alwaysDenied are the target ranges the runner denies regardless of config
// (loopback, link-local incl. cloud metadata, unspecified).
var alwaysDenied = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("fd00:ec2::254/128"),
}

// ipv4MappedPrefix is ::ffff:0:0/96, the IPv4-mapped IPv6 range.
var ipv4MappedPrefix = netip.MustParsePrefix("::ffff:0:0/96")

// checkTargetAllow rejects a target allow prefix that overlaps an
// always-denied range. IPv4-mapped IPv6 prefixes are unmapped first so that
// ::ffff:127.0.0.1/128 is treated as 127.0.0.1/32; a prefix that covers the
// whole mapped range (or more) is rejected because it includes the mapped
// loopback and link-local ranges.
func checkTargetAllow(v *validator, path string, p netip.Prefix) {
	if p.Addr().Is6() && p.Overlaps(ipv4MappedPrefix) {
		if p.Bits() <= ipv4MappedPrefix.Bits() {
			v.addf(path, "%s covers the IPv4-mapped range %s, which includes always-denied ranges (loopback/link-local/metadata)", p, ipv4MappedPrefix)
			return
		}
		p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96).Masked()
	}
	for _, d := range alwaysDenied {
		if p.Overlaps(d) {
			v.addf(path, "%s overlaps the always-denied range %s (loopback/link-local/metadata)", p, d)
		}
	}
}

// privateIPv4 are the ranges docker.run_subnet_pool must lie in.
var privateIPv4 = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
}

// validateRunSubnetPool checks docker.run_subnet_pool: a canonical private
// IPv4 CIDR with room for at least sixteen /28 per-run networks. Overlaps
// with targets.allow_cidrs are allowed: the pool is always denied first.
func validateRunSubnetPool(v *validator, path, value string) {
	p, err := netip.ParsePrefix(value)
	if err != nil || !p.Addr().Is4() || p.Masked() != p {
		v.addf(path, "must be a canonical IPv4 CIDR such as %s (got %q)", DefaultRunSubnetPool, value)
		return
	}
	if p.Bits() < 8 || p.Bits() > 24 {
		v.addf(path, "prefix length must be between /8 and /24 (got /%d)", p.Bits())
		return
	}
	for _, r := range privateIPv4 {
		if r.Bits() <= p.Bits() && r.Contains(p.Addr()) {
			return
		}
	}
	v.addf(path, "must lie inside 10.0.0.0/8, 172.16.0.0/12 or 192.168.0.0/16 (got %s)", p)
}

func validateRunner(c *Runner, dev bool) []error {
	var v validator

	v.listen("listen.address", c.Listen.Address)
	v.listenNoWildcard("listen.address", c.Listen.Address, dev, c.Listen.AllowWildcard,
		"listen.allow_wildcard", "10.213.251.10:7443")
	validateHealthSocket(&v, "listen.health_socket", c.Listen.HealthSocket)

	v.absPath("tls.cert_file", c.TLS.CertFile, true)
	v.absPath("tls.key_file", c.TLS.KeyFile, true)
	v.absPath("tls.client_ca_file", c.TLS.ClientCAFile, true)
	if len(c.TLS.AllowedClientSANs) == 0 {
		v.addf("tls.allowed_client_sans", "must list at least one client SAN")
	}
	for _, san := range c.TLS.AllowedClientSANs {
		// Identity scheme of internal/certs (D-089): spiffe://bekup/<name>.
		name, ok := strings.CutPrefix(san, "spiffe://bekup/")
		if !ok || !clientNameRE.MatchString(name) {
			v.addf("tls.allowed_client_sans", "invalid SAN %q (no wildcards; must be spiffe://bekup/<name> with name [a-z0-9-])", san)
		}
	}

	if p, ok := strings.CutPrefix(c.Docker.Host, "unix://"); !ok || !filepath.IsAbs(p) {
		v.addf("docker.host", "must be a unix:// socket with an absolute path (got %q)", c.Docker.Host)
	}
	v.atLeast("docker.gid", int64(c.Docker.GID), 0)
	if c.Docker.SelfContainer != "" && !containerRefRE.MatchString(c.Docker.SelfContainer) {
		v.addf("docker.self_container", "must be a container ID or name (got %q)", c.Docker.SelfContainer)
	}
	v.atLeast("docker.memory_mib", int64(c.Docker.MemoryMiB), 64)
	if c.Docker.CPUs <= 0 {
		v.addf("docker.cpus", "must be greater than 0")
	}
	v.positive("docker.pids_limit", int64(c.Docker.PidsLimit))
	v.duration("docker.pull_timeout", c.Docker.PullTimeout, time.Second, 0)
	validateRunSubnetPool(&v, "docker.run_subnet_pool", c.Docker.RunSubnetPool)

	v.absPath("images.extra_file", c.Images.ExtraFile, false)

	for _, p := range v.cidrs("targets.allow_cidrs", c.Targets.AllowCIDRs) {
		checkTargetAllow(&v, "targets.allow_cidrs", p)
	}
	v.targetHosts("targets.allow_hosts", c.Targets.AllowHosts)
	v.cidrs("targets.deny_cidrs", c.Targets.DenyCIDRs)
	v.targetHosts("targets.deny_hosts", c.Targets.DenyHosts)
	v.bastionTargets("targets.bastion_targets", c.Targets.BastionTargets)

	v.positive("limits.max_concurrent_runs", int64(c.Limits.MaxConcurrentRuns))
	v.duration("limits.max_run_duration", c.Limits.MaxRunDuration, time.Minute, 0)
	v.positive("limits.max_log_lines", int64(c.Limits.MaxLogLines))
	v.atLeast("limits.max_log_line_bytes", int64(c.Limits.MaxLogLineBytes), 256)

	return v.errs
}
