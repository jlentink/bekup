package config

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jlentink/bekup/internal/hostname"
)

// ExternalAPI is the configuration of `bekup external-api`
// (external-api.toml, D-220): the token-authenticated REST API for CI
// pipelines. It is a separate file, like runner.toml: the process never
// loads bekup.toml, the master key or the runner certificates, and it
// connects to the app DB only as bekup_external. Every key can be
// overridden by BEKUP_<SECTION>_<KEY>.
type ExternalAPI struct {
	Listen   ExternalAPIListen  `toml:"listen"`
	Database Database           `toml:"database"`
	Limits   ExternalAPILimits  `toml:"limits"`
	Logging  ExternalAPILogging `toml:"logging"`
}

// ExternalAPIListen configures the plain-HTTP listener behind the TLS
// reverse proxy and the proxies whose forwarded client addresses count.
type ExternalAPIListen struct {
	Address           string   `toml:"address" doc:"Address the plain-HTTP API listens on (host:port), behind the TLS reverse proxy; never publish it. Outside dev mode a wildcard address (:port, 0.0.0.0, [::]) is refused unless allow_wildcard is set; bind the internal network address instead."`
	AllowWildcard     bool     `toml:"allow_wildcard" doc:"Allow a wildcard/unspecified listen.address outside dev mode (logs a startup warning). Prefer binding the internal network address."`
	TrustedProxies    []string `toml:"trusted_proxies" doc:"CIDRs of the reverse proxies whose X-Forwarded-For header is trusted (the bundled proxy's fixed address as a /32). X-Forwarded-For from every other peer is ignored and the peer address is the client address, which the per-token address allowlists are checked against (D-220 amendment 1). Required outside dev mode (or trusted_proxy_hosts)."`
	TrustedProxyHosts []string `toml:"trusted_proxy_hosts" doc:"Container names of your own reverse proxy on a Docker network the external API shares (as server.trusted_proxy_hosts of bekup.toml, D-215): each name is resolved through Docker's embedded DNS and its private addresses inside the subnet of the external API's own interface on that network (found through its alias bekup-external-api) are trusted, at most one per address family; re-resolved every 20 seconds, untrusted when older than 30 seconds, an unresolvable or colliding name trusts nothing. At most 8 names; no IP addresses (use trusted_proxies), no wildcards, no dots and none of bekup's own service names. Counts as the required trusted proxy outside dev mode."`
	ReadHeaderTimeout Duration `toml:"read_header_timeout" doc:"Maximum time to read request headers (1s to 1m)."`
	ReadTimeout       Duration `toml:"read_timeout" doc:"Maximum time to read a whole request, body included (at least read_header_timeout, at most 5m)."`
	WriteTimeout      Duration `toml:"write_timeout" doc:"Maximum time to write a response (1s to 5m)."`
	IdleTimeout       Duration `toml:"idle_timeout" doc:"Maximum keep-alive idle time between requests (1s to 10m)."`
	ShutdownTimeout   Duration `toml:"shutdown_timeout" doc:"Grace period for in-flight requests on shutdown (1s to 5m)."`
}

// ExternalAPILimits bounds what clients can make the external API do.
type ExternalAPILimits struct {
	RateLimitPerSecond      float64  `toml:"rate_limit_per_second" doc:"Per-client-IP request rate (token bucket refill per second) for every request; exceeding it returns 429 with Retry-After."`
	RateLimitBurst          int      `toml:"rate_limit_burst" doc:"Per-client-IP burst size (bucket capacity)."`
	TokenRateLimitPerSecond float64  `toml:"token_rate_limit_per_second" doc:"Per-API-token request rate (token bucket refill per second) on top of the per-IP limit; exceeding it returns 429 with Retry-After. A CI job polling every 15 seconds needs far less than the default."`
	TokenRateLimitBurst     int      `toml:"token_rate_limit_burst" doc:"Per-API-token burst size (bucket capacity)."`
	RateLimitMaxClients     int      `toml:"rate_limit_max_clients" doc:"Maximum number of client IPs (and, separately, API tokens) tracked by each limiter; the least recently seen entries are evicted beyond it (memory bound)."`
	FailedAuthLimit         int      `toml:"failed_auth_limit" doc:"Failed authentications (malformed, unknown, wrong, revoked or expired tokens and refused client addresses) one client IP may have within failed_auth_window; further requests of that IP get 429 with Retry-After until the window frees up (1 to 100)."`
	FailedAuthWindow        Duration `toml:"failed_auth_window" doc:"Window of failed_auth_limit (1m to 1h)."`
	MaxBodyBytes            int64    `toml:"max_body_bytes" doc:"Maximum request body size in bytes (1024 to 65536); larger requests get 413. The API accepts only an empty body or an empty JSON object."`
}

// ExternalAPILogging configures logging.
type ExternalAPILogging struct {
	Level  string `toml:"level" doc:"Log level: debug, info, warn or error (--log-level overrides it)."`
	Format string `toml:"format" doc:"Log format: json or text (text is the default in dev mode)."`
}

// ExternalAPIFileName is the external API's config file.
const ExternalAPIFileName = "external-api.toml"

// ExternalAPIJoinListen is the listen.address of the external API in the
// own-proxy compose shape (D-215, D-220 amendment 4): it joins the
// operator's proxy network, where Docker assigns its address, so it binds
// every interface of its container. In that shape (BEKUP_LISTEN_SHAPE)
// its listener serves only its own address on the proxy network (the
// alias bekup-external-api) and loopback, and the startup warning of
// allow_wildcard is dropped for exactly this value.
const ExternalAPIJoinListen = "0.0.0.0:8081"

// Bounds of the external API's settings.
const (
	maxExternalDBConns = 20 // the role's CONNECTION LIMIT (D-220)
	minAPIBodyBytes    = 1 << 10
	maxAPIBodyBytes    = 64 << 10
)

// DefaultExternalAPI returns the built-in defaults for external-api.toml.
func DefaultExternalAPI() ExternalAPI {
	return ExternalAPI{
		Listen: ExternalAPIListen{
			Address:           ":8081",
			TrustedProxies:    []string{},
			TrustedProxyHosts: []string{},
			ReadHeaderTimeout: Duration(5 * time.Second),
			ReadTimeout:       Duration(10 * time.Second),
			WriteTimeout:      Duration(15 * time.Second),
			IdleTimeout:       Duration(60 * time.Second),
			ShutdownTimeout:   Duration(15 * time.Second),
		},
		Database: Database{
			URLFile:          "/run/secrets/bekup_external_database_url",
			Port:             DefaultDBPort,
			Name:             DefaultDBName,
			MaxConns:         10,
			MinConns:         0,
			ConnMaxLifetime:  Duration(time.Hour),
			ConnMaxIdleTime:  Duration(10 * time.Minute),
			ConnectTimeout:   Duration(10 * time.Second),
			StatementTimeout: Duration(10 * time.Second),
			TLSMode:          DBTLSVerifyFull,
			TLSCAFile:        "/run/secrets/bekup_db_ca.crt",
		},
		Limits: ExternalAPILimits{
			RateLimitPerSecond:      5,
			RateLimitBurst:          20,
			TokenRateLimitPerSecond: 1,
			TokenRateLimitBurst:     10,
			RateLimitMaxClients:     10000,
			FailedAuthLimit:         10,
			FailedAuthWindow:        Duration(5 * time.Minute),
			MaxBodyBytes:            16 << 10,
		},
		Logging: ExternalAPILogging{Level: "info", Format: "json"},
	}
}

// LoadExternalAPI locates, parses, overrides and validates
// external-api.toml.
func LoadExternalAPI(opts Options) (*Result[ExternalAPI], error) {
	cfg := DefaultExternalAPI()
	return load(&cfg, ExternalAPIFileName, opts, nil, normalizeExternalAPI, validateExternalAPI, (*ExternalAPI).warnings)
}

// externalOnlyDBKeys are [database] keys of bekup.toml that the external
// API never uses: it has no owner login (it never migrates) and
// single-account mode does not support it (D-220 item 12).
var externalOnlyDBKeys = map[string]string{
	"owner_url_file":      "the external API never migrates; remove it",
	"owner_url":           "the external API never migrates; remove it",
	"owner_user":          "the external API never migrates; remove it",
	"owner_password_file": "the external API never migrates; remove it",
	"single_account":      "single-account mode does not support the external API (D-220); create the bekup_external role instead",
}

// normalizeExternalAPI resolves the [database] mode like bekup.toml
// (D-207): database.host selects fields mode, the two modes never mix, and
// in fields mode the login defaults to bekup_external. Keys the external
// API never uses must stay unset.
func normalizeExternalAPI(c *ExternalAPI, sources map[string]Source) []error {
	var v validator
	d := &c.Database
	def := DefaultExternalAPI()
	explicit := func(key string) bool {
		s, ok := sources["database."+key]
		return ok && s != SourceDefault
	}
	value := func(cfg *ExternalAPI, key string) Field { return externalFieldByPath(cfg, "database."+key) }
	for key, why := range externalOnlyDBKeys {
		if !value(c, key).IsZero() {
			v.addf("database."+key, "is not used by the external API: %s", why)
		}
	}
	if !d.FieldsMode() {
		for _, k := range []string{"port", "name", "user", "password_file"} {
			if explicit(k) && value(c, k).Value() != value(&def, k).Value() {
				v.addf("database."+k, "is a fields-mode key: set database.host to use fields mode (and remove database.url_file), or remove it")
			}
		}
		return v.errs
	}
	for _, k := range []string{"url_file", "url"} {
		if explicit(k) && !value(c, k).IsZero() {
			v.addf("database."+k, "cannot be combined with database.host: use either the connection URL file or the fields host/port/name/user/password_file, not both")
		}
	}
	d.URLFile = ""
	if d.User == "" {
		d.User = DBExternalRole
	}
	return v.errs
}

// externalFieldByPath returns the field of c at "section.key" (it must
// exist).
func externalFieldByPath(c *ExternalAPI, path string) Field {
	fields := fieldsOf(c)
	for i := range fields {
		if fields[i].Path == path {
			return fields[i]
		}
	}
	panic("config: unknown key " + path)
}

func validateExternalAPI(c *ExternalAPI, dev bool) []error {
	var v validator

	// [listen]
	l := &c.Listen
	v.listen("listen.address", l.Address)
	v.listenNoWildcard("listen.address", l.Address, dev, l.AllowWildcard, "listen.allow_wildcard", "10.213.250.11:8081")
	v.cidrs("listen.trusted_proxies", l.TrustedProxies)
	v.trustedProxyHostsFor("listen.trusted_proxy_hosts", "listen.trusted_proxies", l.TrustedProxyHosts)
	if len(l.TrustedProxies) == 0 && len(l.TrustedProxyHosts) == 0 && !dev {
		v.addf("listen.trusted_proxies", "is required outside dev mode (CIDRs of the TLS reverse proxy, or its container names in listen.trusted_proxy_hosts): the external API serves plain HTTP and checks each token's client address allowlist against the address the proxy forwards")
	}
	v.duration("listen.read_header_timeout", l.ReadHeaderTimeout, time.Second, time.Minute)
	v.duration("listen.read_timeout", l.ReadTimeout, time.Second, 5*time.Minute)
	if l.ReadTimeout < l.ReadHeaderTimeout {
		v.addf("listen.read_timeout", "must be >= listen.read_header_timeout")
	}
	v.duration("listen.write_timeout", l.WriteTimeout, time.Second, 5*time.Minute)
	v.duration("listen.idle_timeout", l.IdleTimeout, time.Second, 10*time.Minute)
	v.duration("listen.shutdown_timeout", l.ShutdownTimeout, time.Second, 5*time.Minute)

	// [database]
	d := &c.Database
	if !d.FieldsMode() {
		v.absPath("database.url_file", d.URLFile, !dev || d.URL == "")
	}
	if d.MaxConns < 1 || d.MaxConns > maxExternalDBConns {
		v.addf("database.max_conns", "must be between 1 and %d, the connection limit of the %s role (got %d)", maxExternalDBConns, DBExternalRole, d.MaxConns)
	}
	if d.MinConns < 0 || d.MinConns > d.MaxConns {
		v.addf("database.min_conns", "must be between 0 and database.max_conns (got %d)", d.MinConns)
	}
	v.duration("database.conn_max_lifetime", d.ConnMaxLifetime, time.Second, 0)
	v.duration("database.conn_max_idle_time", d.ConnMaxIdleTime, time.Second, 0)
	v.duration("database.connect_timeout", d.ConnectTimeout, time.Second, time.Minute)
	v.duration("database.statement_timeout", d.StatementTimeout, time.Second, time.Minute)
	validateDatabaseTLS(&v, d)
	validateTLSServerName(&v, d)
	if d.FieldsMode() {
		validateExternalDatabaseFields(&v, d)
	}

	// [limits]
	m := &c.Limits
	if r := m.RateLimitPerSecond; math.IsNaN(r) || r <= 0 || r > 1000 {
		v.addf("limits.rate_limit_per_second", "must be > 0 and <= 1000 (got %v)", m.RateLimitPerSecond)
	}
	if m.RateLimitBurst < 1 || m.RateLimitBurst > 10000 {
		v.addf("limits.rate_limit_burst", "must be between 1 and 10000 (got %d)", m.RateLimitBurst)
	}
	if r := m.TokenRateLimitPerSecond; math.IsNaN(r) || r <= 0 || r > 1000 {
		v.addf("limits.token_rate_limit_per_second", "must be > 0 and <= 1000 (got %v)", m.TokenRateLimitPerSecond)
	}
	if m.TokenRateLimitBurst < 1 || m.TokenRateLimitBurst > 10000 {
		v.addf("limits.token_rate_limit_burst", "must be between 1 and 10000 (got %d)", m.TokenRateLimitBurst)
	}
	if m.RateLimitMaxClients < 100 || m.RateLimitMaxClients > 1000000 {
		v.addf("limits.rate_limit_max_clients", "must be between 100 and 1000000 (got %d)", m.RateLimitMaxClients)
	}
	if m.FailedAuthLimit < 1 || m.FailedAuthLimit > 100 {
		v.addf("limits.failed_auth_limit", "must be between 1 and 100 (got %d)", m.FailedAuthLimit)
	}
	v.duration("limits.failed_auth_window", m.FailedAuthWindow, time.Minute, time.Hour)
	if m.MaxBodyBytes < minAPIBodyBytes || m.MaxBodyBytes > maxAPIBodyBytes {
		v.addf("limits.max_body_bytes", "must be between %d and %d (got %d)", minAPIBodyBytes, maxAPIBodyBytes, m.MaxBodyBytes)
	}

	// [logging]
	v.oneOf("logging.level", c.Logging.Level, "debug", "info", "warn", "error")
	v.oneOf("logging.format", c.Logging.Format, "json", "text")
	return v.errs
}

// validateExternalDatabaseFields checks the fields-mode keys: the login
// is always bekup_external.
func validateExternalDatabaseFields(v *validator, d *Database) {
	if err := hostname.Validate(d.Host); err != nil {
		v.addf("database.host", "must be a host name or IP address without port or scheme (%v); Unix sockets are not supported in fields mode", err)
	}
	if d.Port < 1 || d.Port > 65535 {
		v.addf("database.port", "must be between 1 and 65535 (got %d)", d.Port)
	}
	if !ValidDBName(d.Name) {
		v.addf("database.name", "must be 1 to 63 characters of letters, digits, _, - or $ starting with a letter or _, and not postgres or a template database (got %q)", d.Name)
	}
	v.absPath("database.password_file", d.PasswordFile, true)
	if d.User != DBExternalRole {
		v.addf("database.user", "must be %s (role names are fixed, D-220; got %q)", DBExternalRole, d.User)
	}
}

func (c *ExternalAPI) warnings(dev bool) []string {
	var out []string
	if w := c.wildcardListenWarning(dev); w != "" {
		out = append(out, w)
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

// WildcardListenWarning is the startup warning for a wildcard
// listen.address allowed by listen.allow_wildcard outside dev mode (""
// when there is none). The external API drops exactly this warning in the
// own-proxy join shape (ExternalAPIJoinListen).
func (c *ExternalAPI) WildcardListenWarning(dev bool) string { return c.wildcardListenWarning(dev) }

func (c *ExternalAPI) wildcardListenWarning(dev bool) string {
	if dev || !c.Listen.AllowWildcard || !isWildcardListen(c.Listen.Address) {
		return ""
	}
	return fmt.Sprintf("listen.address %q listens on every interface (listen.allow_wildcard = true); bind the internal network address instead", c.Listen.Address)
}

// DBCredentials returns the login of the external API: the URL from
// database.url_file (or the dev-only inline database.url) in URL mode, or
// bekup_external with database.password_file in fields mode. Zero the
// result. The role itself is verified after connecting
// (db.CheckExternalRole: session_user must be bekup_external).
func (c *ExternalAPI) DBCredentials() (DBCredentials, error) {
	d := &c.Database
	if !d.FieldsMode() {
		u, err := resolveSecret(d.URL, d.URLFile)
		if err != nil {
			return DBCredentials{}, err
		}
		return DBCredentials{URL: u}, nil
	}
	if d.User != DBExternalRole {
		return DBCredentials{}, errors.New("database.user must be " + DBExternalRole)
	}
	pw, err := ReadSecretFile(d.PasswordFile)
	if err != nil {
		return DBCredentials{}, fmt.Errorf("database.password_file: %w", err)
	}
	return d.fieldCredentials(d.User, pw), nil
}

// externalAPIDocs replace the bekup.toml descriptions of the shared
// [database] keys whose meaning differs for the external API (the
// reference of external-api.toml).
var externalAPIDocs = map[string]string{ //nolint:gosec // G101: key descriptions for the reference, no credentials
	"database.url_file":            "URL mode: path to a file containing the connection URL of the bekup_external role (Docker secret). The file must not be group/world-writable or world-readable. Cannot be combined with database.host (fields mode).",
	"database.user":                "Fields mode: the login role; must be bekup_external (the default).",
	"database.password_file":       "Fields mode: path to a file holding the bekup_external password (required in fields mode; surrounding whitespace is trimmed). Same file rules as url_file.",
	"database.max_conns":           "Maximum number of pooled connections (1 to 20, the role's connection limit).",
	"database.connect_timeout":     "Timeout for establishing a connection (1s to 1m).",
	"database.statement_timeout":   "Server-side statement_timeout of the external API's connections (1s to 1m).",
	"database.owner_url_file":      "Not used: the external API never migrates. Must stay unset.",
	"database.owner_url":           "Not used: the external API never migrates. Must stay unset.",
	"database.owner_user":          "Not used: the external API never migrates. Must stay unset.",
	"database.owner_password_file": "Not used: the external API never migrates. Must stay unset.",
	"database.single_account":      "Not supported: single-account mode cannot isolate the external API (D-220). Must stay false.",
	"database.tls_mode":            "TLS for the connection (D-100): verify-full (default; server certificate verified against tls_ca_file and the host name), require or disable (both log a warning at every startup). SCRAM-SHA-256 only, with channel binding under TLS; the bundled database refuses bekup_external without TLS.",
}
