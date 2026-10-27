package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// Runner is the configuration of `bekup runner` (runner.toml). It is a
// separate file so that serve's configuration can never change the runner's
// allowlists. Every key can be overridden by BEKUP_<SECTION>_<KEY>.
type Runner struct {
	Listen  RunnerListen  `toml:"listen"`
	TLS     RunnerTLS     `toml:"tls"`
	Docker  RunnerDocker  `toml:"docker"`
	Images  RunnerImages  `toml:"images"`
	Targets RunnerTargets `toml:"targets"`
	Limits  RunnerLimits  `toml:"limits"`
}

// RunnerListen configures the mTLS gRPC listener.
type RunnerListen struct {
	Address       string `toml:"address" doc:"host:port of the mTLS gRPC listener. Only reachable on the internal runner network. Outside dev mode a wildcard address (:port, 0.0.0.0, [::]) is refused unless allow_wildcard is set; bind the internal network address instead."`
	AllowWildcard bool   `toml:"allow_wildcard" doc:"Allow a wildcard/unspecified listen.address outside dev mode (logs a startup warning). Prefer binding the internal network address."`
	HealthSocket  string `toml:"health_socket" doc:"Absolute path of the container-local unix socket that serves only the standard gRPC health service (no TLS) for bekup healthcheck runner. Its directory is created 0700 and must belong to the runner user; the socket is 0600. Keep it on the container's private tmpfs, never on a shared volume. Empty disables it."`
}

// RunnerTLS configures the runner's server certificate and client
// authentication.
type RunnerTLS struct {
	CertFile          string   `toml:"cert_file" doc:"Path to the runner's server certificate (PEM)."`
	KeyFile           string   `toml:"key_file" secret:"file" doc:"Path to the runner's server private key (PEM). Must not be group/world-writable or world-readable."`
	ClientCAFile      string   `toml:"client_ca_file" doc:"Path to the private CA certificate (PEM) that signs client certificates."`
	AllowedClientSANs []string `toml:"allowed_client_sans" doc:"Client identities (URI SANs of the form spiffe://bekup/<component>) that may call the runner."`
}

// RunnerDocker configures access to the Docker daemon and container limits.
type RunnerDocker struct {
	Host          string   `toml:"host" doc:"Docker daemon address. Only unix:// sockets are accepted."`
	GID           int      `toml:"gid" doc:"Group ID that owns the Docker socket (0 = do not add a group)."`
	SelfContainer string   `toml:"self_container" doc:"ID or name of the runner's own container, used to join the per-run networks. Empty = auto-detect from /proc/self/mountinfo, then the container hostname; the container must carry one of the runner's own addresses. Set it when detection fails (e.g. a custom hostname)."`
	MemoryMiB     int      `toml:"memory_mib" doc:"Memory limit per dump container in MiB."`
	CPUs          float64  `toml:"cpus" doc:"CPU limit per dump container (fractional CPUs)."`
	PidsLimit     int      `toml:"pids_limit" doc:"Process limit per dump container."`
	PullTimeout   Duration `toml:"pull_timeout" doc:"Timeout for pulling an allowlisted image."`
	RunSubnetPool string   `toml:"run_subnet_pool" doc:"Private IPv4 CIDR (inside 10.0.0.0/8, 172.16.0.0/12 or 192.168.0.0/16, prefix /8 to /24) from which every run gets its own /28 internal network. The runner treats the whole pool as its own network: targets inside it are always denied and no gRPC connection is accepted from it. Subnets already used by other Docker networks are skipped; an overlap with targets.allow_cidrs is allowed (the pool is denied first)."`
}

// RunnerImages configures additional allowlisted images on top of the
// embedded catalog.
type RunnerImages struct {
	ExtraFile string `toml:"extra_file" doc:"Optional path to an extra-images TOML file (entries pinned by digest). Empty = embedded catalog only."`
}

// RunnerTargets is the target allowlist. Loopback, link-local and metadata
// addresses are always denied regardless of these settings.
type RunnerTargets struct {
	AllowCIDRs []string `toml:"allow_cidrs" doc:"CIDRs that dump containers may connect to. Empty = no CIDR is allowed."`
	AllowHosts []string `toml:"allow_hosts" doc:"Host names that dump containers may connect to. IP addresses are refused here; list them in allow_cidrs (e.g. 192.0.2.10/32)."`
	DenyCIDRs  []string `toml:"deny_cidrs" doc:"CIDRs that are always denied (evaluated before the allow lists)."`
	DenyHosts  []string `toml:"deny_hosts" doc:"Host names that are always denied (evaluated before the allow lists)."`
	// BastionTargets restricts what an SSH bastion may forward to.
	BastionTargets []string `toml:"bastion_targets" doc:"Per-bastion target allowlist: entries <bastion>=<CIDR or host name> (e.g. bastion.example.com=10.20.0.0/16, bastion.example.com=db.internal, 203.0.113.7=127.0.0.1/32). A bastion (matched by its name or any of its addresses) that appears here may only forward to its listed targets: IP-literal targets must lie in one of its CIDRs, host names must be listed exactly. Bastions not listed may forward to any host (only deny_cidrs/deny_hosts apply; a startup warning says so)."`
}

// RunnerLimits bounds the runner's work.
type RunnerLimits struct {
	MaxConcurrentRuns int      `toml:"max_concurrent_runs" doc:"Maximum dump/restore containers at the same time."`
	MaxRunDuration    Duration `toml:"max_run_duration" doc:"Hard upper bound for a single container run."`
	MaxLogLines       int      `toml:"max_log_lines" doc:"Maximum log lines kept per run."`
	MaxLogLineBytes   int      `toml:"max_log_line_bytes" doc:"Maximum length of a single log line in bytes (longer lines are truncated)."`
}

func (c *Runner) warnings(dev bool) []string {
	if !dev && c.Listen.AllowWildcard && isWildcardListen(c.Listen.Address) {
		return []string{fmt.Sprintf("listen.address %q listens on every interface (listen.allow_wildcard = true); bind the internal network address instead", c.Listen.Address)}
	}
	return nil
}

// DefaultRunSubnetPool is the default docker.run_subnet_pool.
const DefaultRunSubnetPool = "10.214.0.0/16"

// DefaultRunner returns the built-in defaults for runner.toml.
func DefaultRunner() Runner {
	return Runner{
		Listen: RunnerListen{Address: ":7443", HealthSocket: "/tmp/bekup-runner/health.sock"},
		TLS: RunnerTLS{
			CertFile:          "/run/secrets/runner.crt",
			KeyFile:           "/run/secrets/runner.key",
			ClientCAFile:      "/run/secrets/bekup_ca.crt",
			AllowedClientSANs: []string{"spiffe://bekup/serve", "spiffe://bekup/worker"},
		},
		Docker: RunnerDocker{
			Host:          "unix:///var/run/docker.sock",
			GID:           0,
			MemoryMiB:     1024,
			CPUs:          1,
			PidsLimit:     256,
			PullTimeout:   Duration(10 * time.Minute),
			RunSubnetPool: DefaultRunSubnetPool,
		},
		Targets: RunnerTargets{
			AllowCIDRs: []string{},
			AllowHosts: []string{},
			DenyCIDRs:  []string{},
			DenyHosts:  []string{},

			BastionTargets: []string{},
		},
		Limits: RunnerLimits{
			MaxConcurrentRuns: 4,
			MaxRunDuration:    Duration(12 * time.Hour),
			MaxLogLines:       10000,
			MaxLogLineBytes:   4096,
		},
	}
}

// maxUnixSocketPath keeps a socket path below the sun_path limit (104 bytes
// on macOS/BSD, 108 on Linux, including the NUL).
const maxUnixSocketPath = 100

// validateHealthSocket checks listen.health_socket (D-125): empty (off) or
// a clean absolute path in a dedicated directory (the runner creates it 0700
// and refuses one owned by someone else), never under /run/secrets.
func validateHealthSocket(v *validator, path, value string) {
	if value == "" {
		return
	}
	v.absPath(path, value, false)
	if len(value) > maxUnixSocketPath {
		v.addf(path, "must be at most %d bytes (unix socket path limit)", maxUnixSocketPath)
	}
	dir := filepath.Dir(value)
	switch dir {
	case "/", "/tmp", "/var/tmp", "/run", "/var/run", "/dev/shm":
		v.addf(path, "must be in a dedicated directory (e.g. /tmp/bekup-runner/health.sock), not directly in %s", dir)
	}
	if dir == "/run/secrets" || strings.HasPrefix(dir, "/run/secrets/") {
		v.addf(path, "must not be under /run/secrets")
	}
}
