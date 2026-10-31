package db

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/logging"
)

// requireAuth is the only authentication method bekup accepts from the app
// DB server (D-207): SCRAM-SHA-256 (with TLS always the channel-bound
// SCRAM-SHA-256-PLUS, D-104). A server asking for a cleartext or MD5
// password, or for none at all, is refused before anything is sent.
const requireAuth = "scram-sha-256"

// searchPath is the search_path startup parameter of every bekup session
// (D-207): the migrations create everything in public, and a
// pg_db_role_setting entry or a server default must not redirect
// unqualified names.
const searchPath = "public"

// neutralConnString is parsed by pgx only so that the returned config is
// "created by ParseConfig" (pgx requires it). It names a Unix socket, so
// pgx reads no TLS files, and it overrides every PG* environment variable
// that could otherwise fail the parse; everything it sets is replaced in
// fieldsConfig. It never carries user data.
const neutralConnString = "host=/nonexistent-bekup-socket port=5432 dbname=bekup user=bekup password=unused " +
	"sslmode=disable connect_timeout=10 target_session_attrs=any channel_binding=disable " +
	"min_protocol_version=3.0 max_protocol_version=3.0 require_auth=scram-sha-256 sslnegotiation=postgres"

// pgx's defaults for the settings fieldsConfig resets (pgx.ParseConfig
// without overrides).
const (
	defaultStatementCacheCapacity   = 512
	defaultDescriptionCacheCapacity = 512
)

// connConfig is the one builder of every app DB connection (D-207): the
// runtime pool, the LISTEN connector and the migrator (and db bootstrap's
// admin connection). Fields-mode credentials never pass through a
// connection string and ignore the PG* environment; URL-mode credentials
// are parsed by pgx (keys absent from the URL keep pgx's libpq defaults,
// but runtime parameters only ever come from the URL). Both get the TLS
// policy o, SCRAM-only authentication, search_path=public and, in
// single-account mode, the SET ROLE of creds.SetRole on every new session.
// The URL and the password are registered with reg for log redaction
// before anything else; errors never echo them.
func connConfig(creds config.DBCredentials, o TLSOptions, reg *logging.Registry) (*pgx.ConnConfig, error) {
	if reg == nil {
		reg = logging.DefaultRegistry
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	var (
		cc  *pgx.ConnConfig
		q   url.Values
		err error
	)
	if !creds.FieldsMode() {
		reg.Register(creds.URL)
		if q, err = checkURLTLS(creds.URL, o.Mode); err != nil {
			return nil, err
		}
		if cc, err = pgx.ParseConfig(string(creds.URL)); err != nil {
			return nil, ErrInvalidURL
		}
		// Runtime parameters only from the URL itself, never from PG*
		// variables (PGOPTIONS, PGAPPNAME, PGTZ).
		for k := range cc.RuntimeParams {
			if !q.Has(k) {
				delete(cc.RuntimeParams, k)
			}
		}
		cc.KerberosSrvName, cc.KerberosSpn = "", ""
		if !q.Has("target_session_attrs") {
			// Not from PGTARGETSESSIONATTRS or a service file either.
			cc.ValidateConnect = nil
		}
		resetPgxSettings(cc)
	} else {
		reg.Register(creds.Password)
		if cc, err = fieldsConfig(creds); err != nil {
			return nil, err
		}
	}
	registerPassword(reg, cc)
	if err := applyTLSPolicy(&cc.Config, q, o); err != nil {
		return nil, err
	}
	cc.RequireAuth = requireAuth
	cc.OAuthTokenProvider = nil
	cc.RuntimeParams["search_path"] = searchPath
	if creds.SetRole != "" {
		role, err := setRoleStatement(creds.SetRole)
		if err != nil {
			return nil, err
		}
		cc.AfterConnect = func(ctx context.Context, c *pgconn.PgConn) error {
			if _, err := c.Exec(ctx, role).ReadAll(); err != nil {
				return fmt.Errorf("switch session to role %s (single-account mode): %w", creds.SetRole, err)
			}
			return nil
		}
	}
	return cc, nil
}

// setRoleStatement returns the SET ROLE statement of single-account mode;
// only bekup's two fixed roles are accepted.
func setRoleStatement(role string) (string, error) {
	switch role {
	case OwnerRole, AppRole:
		return "SET ROLE " + pgx.Identifier{role}.Sanitize(), nil
	default:
		return "", fmt.Errorf("single-account mode can only switch to %s or %s", OwnerRole, AppRole)
	}
}

// fieldsConfig builds the pgx config of fields-mode credentials without a
// connection string: pgx parses neutralConnString (pgx only connects
// configs from its parser), then every field that the parser, a PG*
// variable or a service file could have influenced is set explicitly.
func fieldsConfig(creds config.DBCredentials) (*pgx.ConnConfig, error) {
	switch {
	case creds.Host == "" || creds.Port == 0 || creds.Database == "" || creds.User == "":
		return nil, errors.New("database connection fields incomplete (host, port, name and user are required)")
	case len(creds.Password) == 0:
		return nil, errors.New("database password is empty")
	case isSocket(creds.Host):
		return nil, errors.New("database.host must be a TCP host name or IP address, not a socket path")
	}
	cc, err := pgx.ParseConfigWithOptions(neutralConnString, pgx.ParseConfigOptions{})
	if err != nil {
		// Only a malformed PG* service variable can get here; no user data.
		return nil, fmt.Errorf("build database connection config (check PGSERVICE/PGSERVICEFILE in the environment): %w", err)
	}
	c := &cc.Config
	c.Host = creds.Host
	c.Port = creds.Port
	c.Database = creds.Database
	c.User = creds.User
	c.Password = string(creds.Password)
	c.TLSConfig = nil
	c.Fallbacks = nil
	c.ConnectTimeout = 0
	c.DialFunc = (&net.Dialer{KeepAlive: 5 * time.Minute}).DialContext
	c.LookupFunc = net.DefaultResolver.LookupHost
	c.RuntimeParams = map[string]string{}
	c.KerberosSrvName, c.KerberosSpn = "", ""
	c.SSLNegotiation = ""
	c.AfterNetConnect = nil
	c.ValidateConnect = nil
	c.AfterConnect = nil
	c.OnNotice = nil
	c.OnNotification = nil
	c.OAuthTokenProvider = nil
	c.MinProtocolVersion, c.MaxProtocolVersion = "3.0", "3.0"
	c.ChannelBinding = "require"
	c.RequireAuth = requireAuth
	c.MaxProtocolMessageBodyLen = 0
	resetPgxSettings(cc)
	return cc, nil
}

// resetPgxSettings puts pgx's own settings back to its defaults: a service
// file (PGSERVICE/PGSERVICEFILE) can set them as statement_cache_capacity,
// description_cache_capacity and default_query_exec_mode, and the URL may
// not (allowedURLParams).
func resetPgxSettings(cc *pgx.ConnConfig) {
	cc.Tracer = nil
	cc.StatementCacheCapacity = defaultStatementCacheCapacity
	cc.DescriptionCacheCapacity = defaultDescriptionCacheCapacity
	cc.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
}

// TLSFacts describe the TLS state of an established app DB connection for
// the startup log and `bekup migrate check` (no host, no credentials).
type TLSFacts struct {
	// Mode is the configured database.tls_mode.
	Mode string
	// Encrypted: the session runs over TLS.
	Encrypted bool
	// Version is the TLS version ("TLS 1.3"), empty without TLS.
	Version string
	// ChainVerified: the server certificate chains to tls_ca_file.
	ChainVerified bool
	// HostVerified: the certificate also matches the host name (or
	// tls_server_name).
	HostVerified bool
	// ServerNameOverride: database.tls_server_name is set.
	ServerNameOverride bool
	// ChannelBinding: SCRAM channel binding was required (always with TLS).
	ChannelBinding bool
}

// ConnTLSFacts reports the TLS facts of c under the policy o.
func ConnTLSFacts(c *pgconn.PgConn, o TLSOptions) TLSFacts {
	f := TLSFacts{Mode: o.Mode, ServerNameOverride: o.ServerName != ""}
	tc, ok := c.Conn().(*tls.Conn)
	if !ok {
		return f
	}
	st := tc.ConnectionState()
	f.Encrypted = true
	f.Version = tls.VersionName(st.Version)
	f.ChannelBinding = true
	switch {
	case o.Mode == config.DBTLSVerifyFull:
		f.ChainVerified, f.HostVerified = true, true
	case o.Mode == config.DBTLSRequire && o.RootCAs != nil:
		f.ChainVerified = true
	}
	return f
}
