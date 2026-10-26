package config

import (
	"fmt"
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

func (v *validator) hosts(path string, values []string) {
	for _, h := range values {
		if len(h) > 253 || !hostnameRE.MatchString(h) {
			v.addf(path, "invalid host name %q (no scheme, port or wildcard)", h)
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
	v.duration("server.shutdown_timeout", c.Server.ShutdownTimeout, time.Second, 0)
	v.positive("server.max_body_bytes", c.Server.MaxBodyBytes)
	v.positive("server.max_ws_connections_per_user", int64(c.Server.MaxWSConnectionsPerUser))

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
	if c.Security.Argon2Parallelism < 1 || c.Security.Argon2Parallelism > 255 {
		v.addf("security.argon2_parallelism", "must be between 1 and 255 (got %d)", c.Security.Argon2Parallelism)
	}
	v.positive("security.argon2_max_concurrent", int64(c.Security.Argon2MaxConcurrent))
	v.atLeast("security.password_min_length", int64(c.Security.PasswordMinLength), 12)
	v.duration("security.setup_token_ttl", c.Security.SetupTokenTTL, time.Minute, time.Hour)

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

	// [workers]
	v.positive("workers.max_concurrent_runs", int64(c.Workers.MaxConcurrentRuns))
	v.positive("workers.max_runs_per_connection", int64(c.Workers.MaxRunsPerConnection))
	if c.Workers.MaxRunsPerConnection > c.Workers.MaxConcurrentRuns {
		v.addf("workers.max_runs_per_connection", "must be <= workers.max_concurrent_runs")
	}
	v.positive("workers.max_concurrent_downloads", int64(c.Workers.MaxConcurrentDownloads))
	v.duration("workers.backup_timeout", c.Workers.BackupTimeout, time.Minute, 0)
	v.duration("workers.restore_timeout", c.Workers.RestoreTimeout, time.Minute, 0)
	v.duration("workers.notify_timeout", c.Workers.NotifyTimeout, time.Second, 0)

	// [scheduler]
	v.duration("scheduler.tick_interval", c.Scheduler.TickInterval, time.Second, time.Hour)

	// [notifications]
	v.positive("notifications.max_per_user_per_hour", int64(c.Notifications.MaxPerUserPerHour))
	v.duration("notifications.collapse_window", c.Notifications.CollapseWindow, time.Second, 0)
	v.positive("notifications.webhook_failure_threshold", int64(c.Notifications.WebhookFailureThreshold))

	// [netguard]
	v.cidrs("netguard.allow_cidrs", c.Netguard.AllowCIDRs)
	v.hosts("netguard.allow_hosts", c.Netguard.AllowHosts)

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

func validateRunner(c *Runner, dev bool) []error {
	var v validator

	v.listen("listen.address", c.Listen.Address)
	v.listenNoWildcard("listen.address", c.Listen.Address, dev, c.Listen.AllowWildcard,
		"listen.allow_wildcard", "10.213.251.10:7443")

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

	v.absPath("images.extra_file", c.Images.ExtraFile, false)

	for _, p := range v.cidrs("targets.allow_cidrs", c.Targets.AllowCIDRs) {
		checkTargetAllow(&v, "targets.allow_cidrs", p)
	}
	v.hosts("targets.allow_hosts", c.Targets.AllowHosts)
	v.cidrs("targets.deny_cidrs", c.Targets.DenyCIDRs)
	v.hosts("targets.deny_hosts", c.Targets.DenyHosts)

	v.positive("limits.max_concurrent_runs", int64(c.Limits.MaxConcurrentRuns))
	v.duration("limits.max_run_duration", c.Limits.MaxRunDuration, time.Minute, 0)
	v.positive("limits.max_log_lines", int64(c.Limits.MaxLogLines))
	v.atLeast("limits.max_log_line_bytes", int64(c.Limits.MaxLogLineBytes), 256)

	return v.errs
}
