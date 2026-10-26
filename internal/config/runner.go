package config

import (
	"fmt"
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
	SelfContainer string   `toml:"self_container" doc:"ID or name of the runner's own container, used to join the per-run networks. Empty = auto-detect from the container hostname; set it when detection fails (e.g. a custom hostname)."`
	MemoryMiB     int      `toml:"memory_mib" doc:"Memory limit per dump container in MiB."`
	CPUs          float64  `toml:"cpus" doc:"CPU limit per dump container (fractional CPUs)."`
	PidsLimit     int      `toml:"pids_limit" doc:"Process limit per dump container."`
	PullTimeout   Duration `toml:"pull_timeout" doc:"Timeout for pulling an allowlisted image."`
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
	AllowHosts []string `toml:"allow_hosts" doc:"Host names that dump containers may connect to."`
	DenyCIDRs  []string `toml:"deny_cidrs" doc:"CIDRs that are always denied (evaluated before the allow lists)."`
	DenyHosts  []string `toml:"deny_hosts" doc:"Host names that are always denied (evaluated before the allow lists)."`
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

// DefaultRunner returns the built-in defaults for runner.toml.
func DefaultRunner() Runner {
	return Runner{
		Listen: RunnerListen{Address: ":7443"},
		TLS: RunnerTLS{
			CertFile:          "/run/secrets/runner.crt",
			KeyFile:           "/run/secrets/runner.key",
			ClientCAFile:      "/run/secrets/bekup_ca.crt",
			AllowedClientSANs: []string{"spiffe://bekup/serve", "spiffe://bekup/worker"},
		},
		Docker: RunnerDocker{
			Host:        "unix:///var/run/docker.sock",
			GID:         0,
			MemoryMiB:   1024,
			CPUs:        1,
			PidsLimit:   256,
			PullTimeout: Duration(10 * time.Minute),
		},
		Targets: RunnerTargets{
			AllowCIDRs: []string{},
			AllowHosts: []string{},
			DenyCIDRs:  []string{},
			DenyHosts:  []string{},
		},
		Limits: RunnerLimits{
			MaxConcurrentRuns: 4,
			MaxRunDuration:    Duration(12 * time.Hour),
			MaxLogLines:       10000,
			MaxLogLineBytes:   4096,
		},
	}
}
