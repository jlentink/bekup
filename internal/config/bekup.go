package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Bekup is the configuration of `bekup serve` and `bekup worker`
// (bekup.toml). Every key can be overridden by BEKUP_<SECTION>_<KEY>.
type Bekup struct {
	Server        Server        `toml:"server"`
	Database      Database      `toml:"database"`
	Security      Security      `toml:"security"`
	Runner        RunnerClient  `toml:"runner"`
	Storage       Storage       `toml:"storage"`
	Workers       Workers       `toml:"workers"`
	Scheduler     Scheduler     `toml:"scheduler"`
	Notifications Notifications `toml:"notifications"`
	Netguard      Netguard      `toml:"netguard"`
	Metrics       Metrics       `toml:"metrics"`
	Log           Log           `toml:"log"`
	Dev           Dev           `toml:"dev"`
}

// Server configures the HTTP listener behind the reverse proxy.
type Server struct {
	Listen                  string   `toml:"listen" doc:"Address the plain-HTTP server listens on (host:port). Only reachable on the internal proxy network; never publish it. Outside dev mode a wildcard address (:port, 0.0.0.0, [::]) is refused unless allow_wildcard_listen is set; bind the internal network address instead."`
	AllowWildcardListen     bool     `toml:"allow_wildcard_listen" doc:"Allow a wildcard/unspecified server.listen address outside dev mode (logs a startup warning). Prefer binding the internal network address."`
	BaseURL                 string   `toml:"base_url" doc:"Public URL of the UI as served by the reverse proxy; all links are built from it. Must be https unless dev mode is on or allow_insecure_http is set."`
	AllowInsecureHTTP       bool     `toml:"allow_insecure_http" doc:"Accept a plain http base_url outside dev mode (D-095). Traffic, sessions and credentials are then NOT encrypted; bekup logs a warning at every startup and shows a permanent UI banner."`
	TrustedProxies          []string `toml:"trusted_proxies" doc:"CIDRs of the reverse proxies whose X-Forwarded-* headers are trusted. Required outside dev mode, except with an http base_url and allow_insecure_http (no proxy)."`
	ReadHeaderTimeout       Duration `toml:"read_header_timeout" doc:"Maximum time to read request headers."`
	ShutdownTimeout         Duration `toml:"shutdown_timeout" doc:"Grace period for in-flight requests on shutdown."`
	MaxBodyBytes            int64    `toml:"max_body_bytes" doc:"Maximum request body size in bytes."`
	MaxWSConnectionsPerUser int      `toml:"max_ws_connections_per_user" doc:"Maximum concurrent WebSocket subscriptions per user."`
}

// Database configures the connection to bekup's own Postgres app DB.
type Database struct {
	URLFile          string   `toml:"url_file" secret:"file" doc:"Path to a file containing the Postgres connection URL (Docker secret). The file must not be group/world-writable or world-readable."`
	URL              string   `toml:"url" secret:"inline" doc:"Inline connection URL. Dev mode only; rejected otherwise. Use url_file instead."`
	OwnerURLFile     string   `toml:"owner_url_file" secret:"file" doc:"Path to a file containing the Postgres connection URL of the bekup_owner role, used only to apply migrations (bekup migrate). Same file rules as url_file. Do not mount it into containers that never migrate."`
	OwnerURL         string   `toml:"owner_url" secret:"inline" doc:"Inline bekup_owner connection URL. Dev mode only; rejected otherwise. Use owner_url_file instead."`
	MaxConns         int      `toml:"max_conns" doc:"Maximum number of pooled connections."`
	MinConns         int      `toml:"min_conns" doc:"Minimum number of idle pooled connections."`
	ConnMaxLifetime  Duration `toml:"conn_max_lifetime" doc:"Maximum lifetime of a pooled connection."`
	ConnMaxIdleTime  Duration `toml:"conn_max_idle_time" doc:"Maximum idle time of a pooled connection."`
	ConnectTimeout   Duration `toml:"connect_timeout" doc:"Timeout for establishing a connection."`
	StatementTimeout Duration `toml:"statement_timeout" doc:"Server-side statement_timeout of app connections (1s to 1h). Migrations are not limited by it."`
}

// Security configures the master key, sessions and password hashing.
type Security struct {
	MasterKeyFile          string   `toml:"master_key_file" secret:"file" doc:"Path to the master keyring file (created by bekup keys generate). Must not be group/world-writable or world-readable."`
	MasterKey              string   `toml:"master_key" secret:"inline" doc:"Inline master key. Dev mode only; rejected otherwise. Use master_key_file instead."`
	SessionIdleTimeout     Duration `toml:"session_idle_timeout" doc:"Session idle expiry."`
	SessionAbsoluteTimeout Duration `toml:"session_absolute_timeout" doc:"Session absolute expiry. Must be >= session_idle_timeout."`
	StepUpMaxAge           Duration `toml:"stepup_max_age" doc:"How long a step-up (fresh second factor) stays valid. At most 5m."`
	Argon2MemoryKiB        int      `toml:"argon2_memory_kib" doc:"argon2id memory in KiB. Can only be raised above the default (65536)."`
	Argon2Iterations       int      `toml:"argon2_iterations" doc:"argon2id iterations. Can only be raised above the default (3)."`
	Argon2Parallelism      int      `toml:"argon2_parallelism" doc:"argon2id parallelism (>= 1)."`
	Argon2MaxConcurrent    int      `toml:"argon2_max_concurrent" doc:"Maximum concurrent argon2id hash operations (memory bound)."`
	PasswordMinLength      int      `toml:"password_min_length" doc:"Minimum password length. Can only be raised above the default (12)."`
	SetupTokenTTL          Duration `toml:"setup_token_ttl" doc:"Lifetime of the first-run setup token. At most 1h."`
}

// RunnerClient configures how serve/worker reach the runner over mTLS gRPC.
type RunnerClient struct {
	Address     string   `toml:"address" doc:"host:port of the runner's mTLS gRPC endpoint."`
	ServerName  string   `toml:"server_name" doc:"Expected DNS SAN in the runner's certificate (bekup certs init always includes \"runner\")."`
	CertFile    string   `toml:"cert_file" doc:"Path to this component's client certificate (PEM)."`
	KeyFile     string   `toml:"key_file" secret:"file" doc:"Path to this component's client private key (PEM). Must not be group/world-writable or world-readable."`
	CAFile      string   `toml:"ca_file" doc:"Path to the private CA certificate (PEM) used to verify the runner."`
	DialTimeout Duration `toml:"dial_timeout" doc:"Timeout for connecting to the runner."`
}

// Storage configures local paths.
type Storage struct {
	DiskRoot   string `toml:"disk_root" doc:"Root directory that confines all disk destinations (os.Root)."`
	ScratchDir string `toml:"scratch_dir" doc:"Directory for temporary files (restore staging)."`
}

// Workers configures concurrency limits and timeouts of background work.
type Workers struct {
	MaxConcurrentRuns      int      `toml:"max_concurrent_runs" doc:"Maximum backup/restore runs at the same time (global)."`
	MaxRunsPerConnection   int      `toml:"max_runs_per_connection" doc:"Maximum concurrent runs against one connection."`
	MaxConcurrentDownloads int      `toml:"max_concurrent_downloads" doc:"Maximum concurrent artifact downloads."`
	BackupTimeout          Duration `toml:"backup_timeout" doc:"Default timeout of a backup run."`
	RestoreTimeout         Duration `toml:"restore_timeout" doc:"Timeout of a restore run."`
	NotifyTimeout          Duration `toml:"notify_timeout" doc:"Timeout of a single notification delivery."`
}

// Scheduler configures the job scheduler loop.
type Scheduler struct {
	Enabled      bool     `toml:"enabled" doc:"Run the scheduler loop in this process."`
	TickInterval Duration `toml:"tick_interval" doc:"How often due jobs are enqueued (>= 1s)."`
}

// Notifications configures delivery rate limits.
type Notifications struct {
	MaxPerUserPerHour       int      `toml:"max_per_user_per_hour" doc:"Maximum notifications per user per hour before digesting."`
	CollapseWindow          Duration `toml:"collapse_window" doc:"Repeated failures of the same job within this window collapse into one notification."`
	WebhookFailureThreshold int      `toml:"webhook_failure_threshold" doc:"Consecutive failures after which a user webhook is auto-disabled."`
}

// Netguard holds the bootstrap SSRF allowlist (the admin allowlist lives in
// the DB settings table).
type Netguard struct {
	AllowCIDRs []string `toml:"allow_cidrs" doc:"Private CIDRs that outbound connections (S3, SMTP, SFTP, webhooks) may reach. Empty denies all private ranges."`
	AllowHosts []string `toml:"allow_hosts" doc:"Host names that may resolve to private addresses."`
}

// Metrics configures the internal Prometheus endpoint.
type Metrics struct {
	Enabled bool   `toml:"enabled" doc:"Serve Prometheus /metrics."`
	Listen  string `toml:"listen" doc:"Internal address for /metrics (host:port). Never publish it."`
}

// Log configures logging.
type Log struct {
	Level  string `toml:"level" doc:"Log level: debug, info, warn or error."`
	Format string `toml:"format" doc:"Log format: json or text (text is the default in dev mode)."`
}

// Dev configures dev mode. Dev mode relaxes guardrails and must never be used
// in production.
type Dev struct {
	Enabled bool `toml:"enabled" doc:"Enable dev mode (also --dev or BEKUP_DEV=1): runs without a config file, allows http base_url, no trusted_proxies, wildcard listen addresses and inline secrets. Logs a loud warning. BEKUP_DEV is enable-only; BEKUP_DEV_ENABLED=false turns off enabled = true from the file (unless --dev or BEKUP_DEV=1 is also set)."`
}

// DevBaseURL is the base URL used in dev mode when none is configured.
const DevBaseURL = "http://localhost:8080"

// DefaultBekup returns the built-in defaults for bekup.toml.
func DefaultBekup() Bekup {
	return Bekup{
		Server: Server{
			Listen:                  ":8080",
			TrustedProxies:          []string{},
			ReadHeaderTimeout:       Duration(10 * time.Second),
			ShutdownTimeout:         Duration(30 * time.Second),
			MaxBodyBytes:            1 << 20,
			MaxWSConnectionsPerUser: 5,
		},
		Database: Database{
			URLFile:          "/run/secrets/bekup_database_url",
			OwnerURLFile:     "/run/secrets/bekup_database_owner_url",
			MaxConns:         10,
			MinConns:         0,
			ConnMaxLifetime:  Duration(time.Hour),
			ConnMaxIdleTime:  Duration(30 * time.Minute),
			ConnectTimeout:   Duration(10 * time.Second),
			StatementTimeout: Duration(30 * time.Second),
		},
		Security: Security{
			MasterKeyFile:          "/run/secrets/bekup_master_key",
			SessionIdleTimeout:     Duration(30 * time.Minute),
			SessionAbsoluteTimeout: Duration(12 * time.Hour),
			StepUpMaxAge:           Duration(5 * time.Minute),
			Argon2MemoryKiB:        64 * 1024,
			Argon2Iterations:       3,
			Argon2Parallelism:      1,
			Argon2MaxConcurrent:    4,
			PasswordMinLength:      12,
			SetupTokenTTL:          Duration(time.Hour),
		},
		Runner: RunnerClient{
			Address:     "runner:7443",
			ServerName:  "runner",
			CertFile:    "/run/secrets/bekup_client.crt",
			KeyFile:     "/run/secrets/bekup_client.key",
			CAFile:      "/run/secrets/bekup_ca.crt",
			DialTimeout: Duration(10 * time.Second),
		},
		Storage: Storage{
			DiskRoot:   "/var/lib/bekup/backups",
			ScratchDir: "/var/lib/bekup/scratch",
		},
		Workers: Workers{
			MaxConcurrentRuns:      4,
			MaxRunsPerConnection:   1,
			MaxConcurrentDownloads: 4,
			BackupTimeout:          Duration(6 * time.Hour),
			RestoreTimeout:         Duration(12 * time.Hour),
			NotifyTimeout:          Duration(30 * time.Second),
		},
		Scheduler: Scheduler{
			Enabled:      true,
			TickInterval: Duration(15 * time.Second),
		},
		Notifications: Notifications{
			MaxPerUserPerHour:       30,
			CollapseWindow:          Duration(15 * time.Minute),
			WebhookFailureThreshold: 5,
		},
		Netguard: Netguard{
			AllowCIDRs: []string{},
			AllowHosts: []string{},
		},
		Metrics: Metrics{
			Enabled: true,
			Listen:  "127.0.0.1:9464",
		},
		Log: Log{
			Level:  "info",
			Format: "json",
		},
	}
}

// InsecureTransport reports whether the effective base URL is not https, so
// that traffic between browsers and bekup is not encrypted (D-095). It is
// true for an unparsable URL as well (the safe direction).
func (c *Bekup) InsecureTransport() bool {
	u, err := url.Parse(c.Server.BaseURL)
	return err != nil || !strings.EqualFold(u.Scheme, "https")
}

// Warnings returns the configuration-derived warnings that must be logged at
// every startup (plain HTTP, wildcard listeners). Dev mode is read from
// dev.enabled, which the loader sets to the effective value. Result.Warnings
// already contains these plus the loader's own warnings (dev mode, inline
// secrets, unknown BEKUP_* environment variables).
func (c *Bekup) Warnings() []string {
	return c.warnings(c.Dev.Enabled)
}

// InsecureHTTPWarning is the startup warning for a non-https base URL.
const InsecureHTTPWarning = "plain HTTP: traffic not encrypted (server.base_url is not https; sessions and credentials travel in clear text). Put bekup behind the TLS reverse proxy." //nolint:gosec // G101: a warning text, not a credential

func (c *Bekup) warnings(dev bool) []string {
	var out []string
	if c.InsecureTransport() {
		out = append(out, InsecureHTTPWarning)
	}
	if !dev && c.Server.AllowWildcardListen && isWildcardListen(c.Server.Listen) {
		out = append(out, fmt.Sprintf("server.listen %q listens on every interface (server.allow_wildcard_listen = true); bind the internal network address instead", c.Server.Listen))
	}
	return out
}

// DatabaseOwnerURL returns the bekup_owner connection URL used for migrations
// from database.owner_url_file (or the dev-only inline database.owner_url).
// Zero the result after use.
func (c *Bekup) DatabaseOwnerURL() ([]byte, error) {
	return resolveSecret(c.Database.OwnerURL, c.Database.OwnerURLFile)
}
