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
	GraphQL       GraphQL       `toml:"graphql"`
	Database      Database      `toml:"database"`
	Security      Security      `toml:"security"`
	Runner        RunnerClient  `toml:"runner"`
	Storage       Storage       `toml:"storage"`
	Workers       Workers       `toml:"workers"`
	Scheduler     Scheduler     `toml:"scheduler"`
	Retention     Retention     `toml:"retention"`
	Restore       Restore       `toml:"restore"`
	Notifications Notifications `toml:"notifications"`
	Netguard      Netguard      `toml:"netguard"`
	Metrics       Metrics       `toml:"metrics"`
	Log           Log           `toml:"log"`
	Dev           Dev           `toml:"dev"`
}

// Server configures the HTTP listener behind the reverse proxy.
type Server struct {
	Listen                 string   `toml:"listen" doc:"Address the plain-HTTP server listens on (host:port). Only reachable on the internal proxy network; never publish it. Outside dev mode a wildcard address (:port, 0.0.0.0, [::]) is refused unless allow_wildcard_listen is set; bind the internal network address instead."`
	AllowWildcardListen    bool     `toml:"allow_wildcard_listen" doc:"Allow a wildcard/unspecified server.listen address outside dev mode (logs a startup warning). Prefer binding the internal network address."`
	BaseURL                string   `toml:"base_url" doc:"Public URL of the UI as served by the reverse proxy; all links are built from it. Must be https unless dev mode is on or allow_insecure_http is set."`
	AllowInsecureHTTP      bool     `toml:"allow_insecure_http" doc:"Accept a plain http base_url outside dev mode (D-095). Traffic, sessions and credentials are then NOT encrypted; bekup logs a warning at every startup and shows a permanent UI banner."`
	TrustedProxies         []string `toml:"trusted_proxies" reload:"live" doc:"CIDRs of the reverse proxies whose X-Forwarded-* headers are trusted. Required outside dev mode, except with an http base_url and allow_insecure_http (no proxy)."`
	ReadHeaderTimeout      Duration `toml:"read_header_timeout" doc:"Maximum time to read request headers."`
	ReadTimeout            Duration `toml:"read_timeout" doc:"Maximum time to read a whole request, body included (at least read_header_timeout)."`
	WriteTimeout           Duration `toml:"write_timeout" doc:"Maximum time to write a response. Long-lived routes (event streams of GraphQL subscriptions, artifact downloads) lift it per request."`
	IdleTimeout            Duration `toml:"idle_timeout" doc:"Maximum keep-alive idle time between requests."`
	ShutdownTimeout        Duration `toml:"shutdown_timeout" doc:"Grace period for in-flight requests on shutdown."`
	MaxHeaderBytes         int      `toml:"max_header_bytes" doc:"Maximum size of the request headers in bytes (4 KiB to 1 MiB)."`
	MaxBodyBytes           int64    `toml:"max_body_bytes" reload:"live" doc:"Maximum request body size in bytes."`
	RateLimitPerSecond     float64  `toml:"rate_limit_per_second" reload:"live" doc:"Per-client-IP request rate (token bucket refill per second) for all HTTP requests. Exceeding it returns 429 with Retry-After."`
	RateLimitBurst         int      `toml:"rate_limit_burst" reload:"live" doc:"Per-client-IP burst size (bucket capacity); must cover the asset requests of one UI page load."`
	RateLimitMaxClients    int      `toml:"rate_limit_max_clients" doc:"Maximum number of client IPs tracked by the rate limiter; the least recently seen entries are evicted beyond it (memory bound)."`
	UserRateLimitPerSecond float64  `toml:"user_rate_limit_per_second" reload:"live" doc:"Per-user request rate (token bucket refill per second) for authenticated /graphql and /api requests, on top of the per-IP limit. Exceeding it returns 429 with Retry-After."`
	UserRateLimitBurst     int      `toml:"user_rate_limit_burst" reload:"live" doc:"Per-user burst size (bucket capacity) for authenticated requests."`
}

// GraphQL configures the limits of the /graphql endpoint (D-050, D-105).
type GraphQL struct {
	MaxComplexity int   `toml:"max_complexity" reload:"live" doc:"Maximum complexity of one GraphQL operation (1 per selected field, more for list fields that declare it); larger operations are rejected before execution (50 to 100000)."`
	MaxDepth      int   `toml:"max_depth" reload:"live" doc:"Maximum field nesting depth of one GraphQL operation (3 to 50)."`
	MaxBodyBytes  int64 `toml:"max_body_bytes" reload:"live" doc:"Maximum size of a GraphQL request body in bytes (1024 up to server.max_body_bytes); larger requests get 413."`
	// Live event streams: GraphQL subscriptions over Server-Sent Events
	// (task 2.13).
	StreamMaxPerSession      int      `toml:"stream_max_per_session" doc:"Maximum concurrent event streams (GraphQL subscriptions over SSE) of one session on one instance (1 to 20, at most stream_max_per_user); further streams get 429. Browsers open at most 6 connections per origin over HTTP/1.1, so keep this well below 6 when serving without an HTTP/2 proxy."`
	StreamMaxPerUser         int      `toml:"stream_max_per_user" doc:"Maximum concurrent event streams of one user on one instance, all sessions together (1 to 100); further streams get 429."`
	StreamMaxTotal           int      `toml:"stream_max_total" doc:"Maximum concurrent event streams on one instance, all users together (1 to 100000); further streams get 503."`
	StreamKeepaliveInterval  Duration `toml:"stream_keepalive_interval" doc:"Interval of keepalive comments on idle event streams (5s to 1m). A reverse proxy must allow at least three intervals without data (nginx proxy_read_timeout)."`
	StreamRevalidateInterval Duration `toml:"stream_revalidate_interval" doc:"Interval at which every event stream re-checks its session in the database (5s to 5m). Revocations normally end streams at once through session events; this is the fallback bound."`
	StreamSessionCheckTTL    Duration `toml:"stream_session_check_ttl" doc:"Maximum age of a session check before a subscription event is delivered without a new one (1s up to stream_revalidate_interval)."`
}

// Database configures the connection to bekup's own Postgres app DB.
//
// Two mutually exclusive ways to name the login (D-207): URL mode
// (url_file / owner_url_file, the bundled stack) and fields mode (host,
// port, name, user, password_file, owner_user, owner_password_file; an
// external Postgres). Setting host selects fields mode; mixing the two is
// a configuration error (see normalizeBekup).
type Database struct {
	URLFile           string   `toml:"url_file" secret:"file" doc:"URL mode: path to a file containing the Postgres connection URL (Docker secret). The file must not be group/world-writable or world-readable. Cannot be combined with database.host (fields mode)."`
	URL               string   `toml:"url" secret:"inline" doc:"Inline connection URL. Dev mode only; rejected otherwise. Use url_file instead."`
	OwnerURLFile      string   `toml:"owner_url_file" secret:"file" doc:"URL mode: path to a file containing the Postgres connection URL of the bekup_owner role, used only to apply migrations (bekup migrate). Same file rules as url_file. Do not mount it into containers that never migrate."`
	OwnerURL          string   `toml:"owner_url" secret:"inline" doc:"Inline bekup_owner connection URL. Dev mode only; rejected otherwise. Use owner_url_file instead."`
	Host              string   `toml:"host" doc:"Fields mode (D-207): host name or IP address of an external Postgres (15 to 17). Setting it selects fields mode, which builds the connection from the database.* keys only (never from a URL or the PG* environment); url_file, owner_url_file, url and owner_url must then be unset. Connect directly to Postgres: poolers that terminate TLS break the required SCRAM channel binding."`
	Port              int      `toml:"port" doc:"Fields mode: TCP port of the Postgres server (1 to 65535; default 5432)."`
	Name              string   `toml:"name" doc:"Fields mode: name of bekup's own database (default bekup; letters, digits, _, - and $, at most 63 characters; never postgres or a template database)."`
	User              string   `toml:"user" doc:"Fields mode: login role of serve and worker. With two roles (the default) it must be bekup_app (the default). With single_account = true it is the one account bekup uses for everything."`
	PasswordFile      string   `toml:"password_file" secret:"file" doc:"Fields mode: path to a file holding the password of database.user (required in fields mode; surrounding whitespace is trimmed). Same file rules as url_file."`
	OwnerUser         string   `toml:"owner_user" doc:"Fields mode with two roles: login role of bekup migrate; must be bekup_owner (the default). Not used with single_account."`
	OwnerPasswordFile string   `toml:"owner_password_file" secret:"file" doc:"Fields mode with two roles: path to a file holding the bekup_owner password, needed only by bekup migrate and bekup db bootstrap. Do not mount it into containers that never migrate. Not used with single_account."`
	SingleAccount     bool     `toml:"single_account" doc:"Fields mode only. EXPLICIT WEAKENING (D-207): bekup uses the one account database.user for migrations and the app; its sessions switch to the NOLOGIN roles bekup_owner/bekup_app with SET ROLE, so a compromised serve or worker can switch back and alter the schema and the audit log. Logged at every startup, audited (security.insecure_db_roles) and alerted daily. Prefer the two least-privilege login roles (bekup db bootstrap)."`
	MaxConns          int      `toml:"max_conns" doc:"Maximum number of pooled connections (worker processes: at least the sum of the [workers] *_queue_workers plus 4)."`
	MinConns          int      `toml:"min_conns" doc:"Minimum number of idle pooled connections."`
	ConnMaxLifetime   Duration `toml:"conn_max_lifetime" doc:"Maximum lifetime of a pooled connection."`
	ConnMaxIdleTime   Duration `toml:"conn_max_idle_time" doc:"Maximum idle time of a pooled connection."`
	ConnectTimeout    Duration `toml:"connect_timeout" doc:"Timeout for establishing a connection."`
	StatementTimeout  Duration `toml:"statement_timeout" doc:"Server-side statement_timeout of app connections (1s to 1h). Migrations are not limited by it."`
	TLSMode           string   `toml:"tls_mode" doc:"TLS for the app DB connection (D-100): verify-full (default; encrypted, server certificate verified against tls_ca_file and the host name: the URL host, database.host or tls_server_name), require (encrypted; with tls_ca_file the chain is verified but not the host name, without it nothing is verified) or disable (plain text). require and disable log a warning at every startup. With TLS, SCRAM channel binding is always required. With TLS or without, only SCRAM-SHA-256 authentication is accepted (D-207). Applies to serve, worker, migrate and db bootstrap; an sslmode in the URL may not be stricter than this and is otherwise overridden."`
	TLSCAFile         string   `toml:"tls_ca_file" doc:"Path to the CA certificate bundle (PEM) that signs the Postgres server certificate (bekup certs: <dir>/postgres/ca.crt). Only the certificates in this file are trusted (never the built-in system roots); for a managed Postgres name the provider's CA bundle, or the system bundle file only if the provider uses a public CA (D-207). Required for verify-full."`
	TLSClientCertFile string   `toml:"tls_client_cert_file" doc:"Optional client certificate (PEM) presented to Postgres. Set together with tls_client_key_file."`
	TLSClientKeyFile  string   `toml:"tls_client_key_file" secret:"file" doc:"Optional client private key (PEM) for tls_client_cert_file. Same file rules as url_file."`
	TLSServerName     string   `toml:"tls_server_name" doc:"Optional host name the Postgres server certificate is verified against instead of the connection host (verify-full only), e.g. when database.host is an IP address but the certificate names a DNS name. Also sent as SNI."`
}

// Fixed names of bekup's database roles (D-054). Released migrations
// grant to them literally, so they cannot be renamed (one bekup per
// cluster, D-207).
const (
	// DBOwnerRole owns the database and runs the migrations.
	DBOwnerRole = "bekup_owner"
	// DBAppRole is the runtime role of serve and worker.
	DBAppRole = "bekup_app"
	// DefaultDBPort and DefaultDBName apply in fields mode.
	DefaultDBPort = 5432
	DefaultDBName = "bekup"
)

// FieldsMode reports whether the connection is configured with the
// database.host/port/name/user/password_file keys instead of URL files
// (D-207).
func (d *Database) FieldsMode() bool { return d.Host != "" }

// Values of database.tls_mode (D-100).
const (
	DBTLSVerifyFull = "verify-full"
	DBTLSRequire    = "require"
	DBTLSDisable    = "disable"
)

// TLSWeakened reports whether database.tls_mode is weaker than verify-full
// (require or disable), which must be surfaced as a warning (D-100).
func (d *Database) TLSWeakened() bool { return d.TLSMode != DBTLSVerifyFull }

// Security configures the master key, sessions and password hashing.
type Security struct {
	MasterKeyFile           string   `toml:"master_key_file" secret:"file" doc:"Path to the master keyring file (created by bekup keys generate). Must not be group/world-writable or world-readable."`
	MasterKey               string   `toml:"master_key" secret:"inline" doc:"Inline master key. Dev mode only; rejected otherwise. Use master_key_file instead."`
	SessionIdleTimeout      Duration `toml:"session_idle_timeout" doc:"Session idle expiry."`
	SessionAbsoluteTimeout  Duration `toml:"session_absolute_timeout" doc:"Session absolute expiry. Must be >= session_idle_timeout."`
	SessionTouchInterval    Duration `toml:"session_touch_interval" doc:"Minimum time between two writes of a session's last-seen time and idle expiry (sliding idle refresh without a database write on every request); 10s to 10m and less than session_idle_timeout."`
	StepUpMaxAge            Duration `toml:"stepup_max_age" doc:"How long a step-up (fresh second factor) stays valid. At most 5m."`
	Argon2MemoryKiB         int      `toml:"argon2_memory_kib" doc:"argon2id memory in KiB. Can only be raised above the default (65536)."`
	Argon2Iterations        int      `toml:"argon2_iterations" doc:"argon2id iterations. Can only be raised above the default (3)."`
	Argon2Parallelism       int      `toml:"argon2_parallelism" doc:"argon2id parallelism (>= 1)."`
	Argon2MaxConcurrent     int      `toml:"argon2_max_concurrent" doc:"Maximum concurrent argon2id hash operations (memory bound, 1 to 64). Requests beyond it wait briefly and are then refused with RATE_LIMITED; they never queue without bound."`
	PasswordMinLength       int      `toml:"password_min_length" reload:"live" doc:"Minimum password length in characters. Can only be raised above the default (12); at most 128."`
	PasswordMinScore        int      `toml:"password_min_score" reload:"live" doc:"Minimum zxcvbn strength score (0-4) of a new password. Can only be raised above the default (3)."`
	LoginRateLimitPerMinute int      `toml:"login_rate_limit_per_minute" reload:"live" doc:"Login attempts per client IP per minute (a stricter limiter on top of the server-wide one; 1 to 600); the same rate also limits password verifications per account (logins, password step-ups and changes). Excess attempts get RATE_LIMITED."`
	LoginRateLimitBurst     int      `toml:"login_rate_limit_burst" reload:"live" doc:"Burst of login attempts per client IP, and of password verifications per account (1 to 100)."`
	LockoutThreshold        int      `toml:"lockout_threshold" reload:"live" doc:"Consecutive failed logins after which an account is temporarily locked (3 to 20)."`
	LockoutBaseDuration     Duration `toml:"lockout_base_duration" reload:"live" doc:"Lock duration after lockout_threshold failures; it doubles with every further failure (10s to 1h)."`
	LockoutMaxDuration      Duration `toml:"lockout_max_duration" reload:"live" doc:"Upper bound of the exponential lockout backoff (lockout_base_duration to 24h)."`
	AuditHeadSignInterval   Duration `toml:"audit_head_sign_interval" doc:"How often serve signs the audit chain head with the instance key when the chain has grown (1m to 24h)."`
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
	ScratchDir string `toml:"scratch_dir" doc:"Directory for temporary files (restore staging). Each restore stages its artifact in a private 0700 subdirectory, encrypted at rest with a key held only in memory, and removes it on every path."`
	// ScratchReserveBytes is kept free on the scratch file system.
	ScratchReserveBytes int64 `toml:"scratch_reserve_bytes" doc:"Bytes that must stay free on the scratch_dir file system after a restore staged its artifact (0 to 1 TiB). A restore whose artifact does not fit with this reserve is refused before anything is downloaded."`
	// ScratchStaleAfter: leftover staging directories of restores that are
	// no longer running are removed at startup and periodically; this age
	// removes even those of restores still recorded as running.
	ScratchStaleAfter Duration `toml:"scratch_stale_after" doc:"Staging directories older than this are removed by the scratch sweeper even when their restore is still recorded as running (1h to 30 days; must exceed workers.restore_timeout). Directories of restores that are no longer running are removed at startup and every 10 minutes."`
}

// Workers configures concurrency limits and timeouts of background work.
type Workers struct {
	MaxConcurrentRuns       int      `toml:"max_concurrent_runs" doc:"Maximum backup/restore runs at the same time (global)."`
	MaxRunsPerConnection    int      `toml:"max_runs_per_connection" doc:"Maximum concurrent runs against one connection."`
	MaxConcurrentDownloads  int      `toml:"max_concurrent_downloads" doc:"Maximum concurrent artifact downloads served by this process (1-64; a plain download of a passphrase-encrypted artifact needs up to 256 MiB for scrypt)."`
	MaxDownloadsPerUser     int      `toml:"max_downloads_per_user" doc:"Maximum concurrent artifact downloads of one user in this process (1..max_concurrent_downloads)."`
	BackupTimeout           Duration `toml:"backup_timeout" doc:"Default timeout of a backup run and the cap on each job's timeoutSeconds."`
	RestoreTimeout          Duration `toml:"restore_timeout" doc:"Timeout of a restore run."`
	NotifyTimeout           Duration `toml:"notify_timeout" doc:"Timeout of a single notification delivery."`
	RetentionTimeout        Duration `toml:"retention_timeout" doc:"Timeout of a single retention pass of one job."`
	ShutdownTimeout         Duration `toml:"shutdown_timeout" doc:"On shutdown, how long running jobs may finish before they are canceled (River soft stop, then hard stop)."`
	BackupQueueWorkers      int      `toml:"backup_queue_workers" doc:"Backup jobs this process works at the same time (River queue backup, 1-10000)."`
	RestoreQueueWorkers     int      `toml:"restore_queue_workers" doc:"Restore jobs this process works at the same time (River queue restore, 1-10000)."`
	RetentionQueueWorkers   int      `toml:"retention_queue_workers" doc:"Retention jobs this process works at the same time (River queue retention, 1-10000)."`
	NotifyQueueWorkers      int      `toml:"notify_queue_workers" doc:"Notification deliveries this process works at the same time (River queue notify, 1-10000)."`
	MaintenanceQueueWorkers int      `toml:"maintenance_queue_workers" doc:"Housekeeping jobs this process works at the same time (River queue maintenance, 1-10000)."`
	RunLogMaxLines          int      `toml:"run_log_max_lines" doc:"Log lines stored per backup run (runner output and worker events, 100-1000000); further lines are counted and dropped."`
	RunLogMaxLineBytes      int      `toml:"run_log_max_line_bytes" doc:"Maximum bytes of one stored run-log line (128-16384); longer lines are truncated after redaction."`
}

// Scheduler configures the job scheduler loop.
type Scheduler struct {
	Enabled      bool     `toml:"enabled" doc:"Run the scheduler loop in this process."`
	TickInterval Duration `toml:"tick_interval" doc:"How often due jobs are enqueued (>= 1s)."`
	MinInterval  Duration `toml:"min_interval" doc:"Shortest allowed schedule interval for new or changed jobs, incl. the gap between cron occurrences (1m to 24h)."`
	BatchSize    int      `toml:"batch_size" doc:"Due jobs locked and enqueued per scheduler transaction (1-1000)."`
}

// Retention configures the retention worker (task 3.8). The policy itself
// (keep last N / keep N days) is set per job.
type Retention struct {
	DryRun               bool     `toml:"dry_run" reload:"live" doc:"Compute and log every retention plan without deleting anything (instance-wide dry run)."`
	MaxDeletionsPerSweep int      `toml:"max_deletions_per_sweep" reload:"live" doc:"Hard cap on the objects one retention sweep of a job may delete (1-100000); the rest waits for the next sweep."`
	PartialCleanupAge    Duration `toml:"partial_cleanup_age" reload:"live" doc:"The daily sweep removes leftover temporary upload files on disk and SFTP destinations older than this (1h to 30 days)."`
}

// Restore configures restores. Temporary restore connections (task 4.4,
// admin only) live for TempConnectionTTL after their creation; an admin may
// move the expiry, but never beyond TempConnectionMaxTTL after the creation.
type Restore struct {
	TempConnectionTTL    Duration `toml:"temp_connection_ttl" doc:"Lifetime of a temporary restore connection after its creation (5m up to temp_connection_max_ttl). Expired temporary connections are deleted and their stored credentials destroyed."`
	TempConnectionMaxTTL Duration `toml:"temp_connection_max_ttl" doc:"Longest lifetime of a temporary restore connection counted from its creation, including extensions (at least temp_connection_ttl, at most 30 days)."`
}

// Notifications configures the noise rules of the notification core
// (task 5.3, D-183).
type Notifications struct {
	MaxPerUserPerHour       int      `toml:"max_per_user_per_hour" reload:"live" doc:"Per-user flood rule: e-mail (or webhook) notifications one user gets within an hour; the next one becomes a single summary and further ones stay in the in-app inbox until the rate drops. Security alerts are exempt (1-10000)."`
	CollapseWindow          Duration `toml:"collapse_window" reload:"live" doc:"Low-priority *.started events (backup.started, restore.started) of the same job within this window collapse into the first one (1s-24h)."`
	WebhookFailureThreshold int      `toml:"webhook_failure_threshold" reload:"live" doc:"Permanent webhook failures (redirect, 4xx, refused address, TLS error) since the last success after which a webhook (personal or shared) is auto-disabled; 10 consecutive failures spanning at least 24 hours also disable it (1-1000)."`
}

// Netguard holds the bootstrap SSRF allowlist (the admin allowlist lives in
// the DB settings table, managed under Admin → Settings → Network, D-196;
// both apply together). The bootstrap list never applies to personal
// webhooks (D-198).
type Netguard struct {
	AllowCIDRs []string `toml:"allow_cidrs" reload:"live" doc:"Private CIDRs that outbound connections (S3, SFTP, SMTP, shared webhooks; never users' personal webhooks) may reach, in addition to the admin allowlist managed in the UI (Admin, Settings, Network). Empty denies all private ranges not allowlisted there."`
	AllowHosts []string `toml:"allow_hosts" reload:"live" doc:"Host names (exact, or *.example.com for any subdomain; not over a public suffix or a wildcard-DNS service such as nip.io) that may resolve to private addresses, for the same uses as allow_cidrs. Never re-allows loopback (outside dev mode), link-local, metadata or other always-denied ranges."`
}

// Metrics configures the internal Prometheus endpoint.
type Metrics struct {
	Enabled bool   `toml:"enabled" doc:"Serve Prometheus /metrics."`
	Listen  string `toml:"listen" doc:"Internal address for /metrics and /healthz (host:port). Never publish it. Outside dev mode a wildcard address (:port, 0.0.0.0, [::]) is refused unless allow_wildcard_listen is set; bind loopback or an internal network address (compose: the metrics network address)."`
	// AllowWildcardListen mirrors server.allow_wildcard_listen (D-098).
	AllowWildcardListen bool `toml:"allow_wildcard_listen" doc:"Allow a wildcard/unspecified metrics.listen address outside dev mode (logs a startup warning). Prefer binding an internal address."`
	// AuthBearerFile enables bearer authentication of /metrics.
	AuthBearerFile string `toml:"auth_bearer_file" secret:"file" doc:"Optional path to a file holding the bearer credential Prometheus must send (Authorization: Bearer ...; at least 32 characters, surrounding whitespace trimmed). Empty: /metrics is unauthenticated and protected only by binding an internal address. Same file rules as database.url_file."`
	// PerJobLabels adds per-job series (label job = the job slug).
	PerJobLabels bool `toml:"per_job_labels" doc:"Add per-job series (bekup_backup_last_success_timestamp_seconds with label job = job slug, at most 1000 jobs). Off by default: job slugs are names chosen by admins and one series per job grows with the installation."`
}

// Log configures logging.
type Log struct {
	Level  string `toml:"level" reload:"live" doc:"Log level: debug, info, warn or error."`
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
			Listen:                 ":8080",
			TrustedProxies:         []string{},
			ReadHeaderTimeout:      Duration(10 * time.Second),
			ReadTimeout:            Duration(30 * time.Second),
			WriteTimeout:           Duration(60 * time.Second),
			IdleTimeout:            Duration(120 * time.Second),
			ShutdownTimeout:        Duration(30 * time.Second),
			MaxHeaderBytes:         32 << 10,
			MaxBodyBytes:           1 << 20,
			RateLimitPerSecond:     20,
			RateLimitBurst:         100,
			RateLimitMaxClients:    10000,
			UserRateLimitPerSecond: 20,
			UserRateLimitBurst:     100,
		},
		GraphQL: GraphQL{
			MaxComplexity: 1000,
			MaxDepth:      12,
			MaxBodyBytes:  256 << 10,

			StreamMaxPerSession:      4,
			StreamMaxPerUser:         10,
			StreamMaxTotal:           1000,
			StreamKeepaliveInterval:  Duration(25 * time.Second),
			StreamRevalidateInterval: Duration(30 * time.Second),
			StreamSessionCheckTTL:    Duration(5 * time.Second),
		},
		Database: Database{
			URLFile:          "/run/secrets/bekup_database_url",
			OwnerURLFile:     "/run/secrets/bekup_database_owner_url",
			Port:             DefaultDBPort,
			Name:             DefaultDBName,
			MaxConns:         25,
			MinConns:         0,
			ConnMaxLifetime:  Duration(time.Hour),
			ConnMaxIdleTime:  Duration(30 * time.Minute),
			ConnectTimeout:   Duration(10 * time.Second),
			StatementTimeout: Duration(30 * time.Second),
			TLSMode:          DBTLSVerifyFull,
			TLSCAFile:        "/run/secrets/bekup_db_ca.crt",
		},
		Security: Security{
			MasterKeyFile:           "/run/secrets/bekup_master_key",
			SessionIdleTimeout:      Duration(30 * time.Minute),
			SessionAbsoluteTimeout:  Duration(12 * time.Hour),
			SessionTouchInterval:    Duration(time.Minute),
			StepUpMaxAge:            Duration(5 * time.Minute),
			Argon2MemoryKiB:         64 * 1024,
			Argon2Iterations:        3,
			Argon2Parallelism:       1,
			Argon2MaxConcurrent:     4,
			PasswordMinLength:       12,
			PasswordMinScore:        3,
			LoginRateLimitPerMinute: 10,
			LoginRateLimitBurst:     5,
			LockoutThreshold:        5,
			LockoutBaseDuration:     Duration(time.Minute),
			LockoutMaxDuration:      Duration(time.Hour),
			AuditHeadSignInterval:   Duration(time.Hour),
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
			DiskRoot:            "/var/lib/bekup/backups",
			ScratchDir:          "/var/lib/bekup/scratch",
			ScratchReserveBytes: 1 << 30,
			ScratchStaleAfter:   Duration(24 * time.Hour),
		},
		Workers: Workers{
			MaxConcurrentRuns:       4,
			MaxRunsPerConnection:    1,
			MaxConcurrentDownloads:  4,
			MaxDownloadsPerUser:     2,
			BackupTimeout:           Duration(6 * time.Hour),
			RestoreTimeout:          Duration(12 * time.Hour),
			NotifyTimeout:           Duration(30 * time.Second),
			RetentionTimeout:        Duration(time.Hour),
			ShutdownTimeout:         Duration(2 * time.Minute),
			BackupQueueWorkers:      4,
			RestoreQueueWorkers:     2,
			RetentionQueueWorkers:   2,
			NotifyQueueWorkers:      8,
			MaintenanceQueueWorkers: 1,
			RunLogMaxLines:          10000,
			RunLogMaxLineBytes:      4096,
		},
		Scheduler: Scheduler{
			Enabled:      true,
			TickInterval: Duration(15 * time.Second),
			MinInterval:  Duration(5 * time.Minute),
			BatchSize:    100,
		},
		Retention: Retention{
			DryRun:               false,
			MaxDeletionsPerSweep: 500,
			PartialCleanupAge:    Duration(24 * time.Hour),
		},
		Restore: Restore{
			TempConnectionTTL:    Duration(24 * time.Hour),
			TempConnectionMaxTTL: Duration(7 * 24 * time.Hour),
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

// Startup warnings for a weakened database.tls_mode (D-100).
const (
	DBTLSDisableWarning   = `app database TLS disabled: database.tls_mode = "disable", so traffic to Postgres (credentials, sessions, audit log) is NOT encrypted and can be read or altered on the network. Use "verify-full".`
	DBTLSRequireWarning   = `app database TLS not verified: database.tls_mode = "require" without tls_ca_file encrypts but does not verify the Postgres server certificate, so a network attacker can impersonate the database. Use "verify-full".`
	DBTLSRequireCAWarning = `app database TLS host name not verified: database.tls_mode = "require" verifies the Postgres server certificate against tls_ca_file but not its host name. Use "verify-full".`
	// DBSingleAccountWarning is the startup warning of single-account
	// mode (D-207).
	DBSingleAccountWarning = `app database single-account mode: database.single_account = true, so bekup runs migrations and the app as one database account that can switch to bekup_owner; a compromised serve or worker can alter the schema and the audit log. Create the two least-privilege roles instead (bekup db bootstrap).`
)

func (c *Bekup) warnings(dev bool) []string {
	var out []string
	if c.InsecureTransport() {
		out = append(out, InsecureHTTPWarning)
	}
	if !dev && c.Server.AllowWildcardListen && isWildcardListen(c.Server.Listen) {
		out = append(out, fmt.Sprintf("server.listen %q listens on every interface (server.allow_wildcard_listen = true); bind the internal network address instead", c.Server.Listen))
	}
	if !dev && c.Metrics.Enabled && c.Metrics.AllowWildcardListen && isWildcardListen(c.Metrics.Listen) {
		out = append(out, fmt.Sprintf("metrics.listen %q listens on every interface (metrics.allow_wildcard_listen = true); bind an internal address instead", c.Metrics.Listen))
	}
	if c.Database.SingleAccount {
		out = append(out, DBSingleAccountWarning)
	}
	switch c.Database.TLSMode {
	case DBTLSDisable:
		out = append(out, DBTLSDisableWarning)
	case DBTLSRequire:
		if c.Database.TLSCAFile == "" {
			out = append(out, DBTLSRequireWarning)
		} else {
			out = append(out, DBTLSRequireCAWarning)
		}
	}
	return out
}
