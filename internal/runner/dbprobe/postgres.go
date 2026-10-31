package dbprobe

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// pgConninfo is parsed by pgconn.ParseConfig only so that ConnectConfig
// accepts the config; every relevant field is overwritten afterwards.
// sslmode=disable keeps ParseConfig from loading TLS files (TLS is set up
// by the caller), and passfile / servicefile point at /dev/null so no
// ~/.pgpass or service file of the runner's environment is read.
const pgConninfo = "host=bekup.invalid port=5432 dbname=postgres user=bekup sslmode=disable " +
	"passfile=/dev/null servicefile=/dev/null"

// Session hardening (D-152). A target database's owner is not trusted:
// it can create functions, aggregates and operators that shadow the
// built-ins (CREATE AGGREGATE evil.count(*)) and point the database's
// search_path at them (ALTER DATABASE ... SET search_path = evil,
// pg_catalog), and it can attach row-level security policies calling its
// functions. Code reached that way would run as the backup role, which is
// often a superuser. Every session therefore
//   - sends search_path and row_security as startup parameters (they take
//     precedence over ALTER DATABASE / ALTER ROLE settings) and verifies
//     after authentication that they are in effect;
//   - sets both again with pg_catalog.set_config (belt and braces);
//   - sets restrict_nonsystem_relation_kind = 'view, foreign-table' where
//     the server has it (CVE-2024-7348 fix: 12.20, 13.16, 14.13, 15.8,
//     16.4, 17+), so a table swapped for a view or foreign table between
//     the catalog read and a row count cannot run code; without it the
//     connection refuses row counts (SafeRowCounts);
//   - runs only schema-qualified SQL (pg_catalog functions, OPERATOR(
//     pg_catalog.op), pg_catalog types and collations).
//
// pg_temp is listed last: temporary objects never shadow pg_catalog, and
// functions and operators are never looked up in pg_temp.
const (
	pgSearchPath  = "pg_catalog, pg_temp"
	pgRowSecurity = "off"
	// pgCheckSettings reads the settings that the startup parameters set.
	pgCheckSettings = `SELECT pg_catalog.current_setting('search_path'), pg_catalog.current_setting('row_security')`
	// pgPinSettings sets them again for the session.
	pgPinSettings = `SELECT pg_catalog.set_config('search_path', 'pg_catalog, pg_temp', false), ` +
		`pg_catalog.set_config('row_security', 'off', false)`
	// pgRestrictKinds blocks non-system views and foreign tables where the
	// server knows the setting (no row otherwise), like pg_dump does.
	pgRestrictKinds = `SELECT pg_catalog.set_config(s.name, 'view, foreign-table', false) FROM pg_catalog.pg_settings s ` +
		`WHERE s.name OPERATOR(pg_catalog.=) 'restrict_nonsystem_relation_kind'`
)

// pgMaxMessageBytes caps the body of every protocol message a probe
// session receives. Catalog rows, counts and settings are far smaller.
const pgMaxMessageBytes = 1 << 20

type pgConn struct {
	c   *pgconn.PgConn
	eng engine.Engine
	// restricted: restrict_nonsystem_relation_kind is in effect.
	restricted bool
}

func openPG(cctx, ctx context.Context, p Params, eng engine.Engine) (Conn, error) {
	cfg, err := pgconn.ParseConfig(pgConninfo)
	if err != nil {
		return nil, newError(runnerpb.ReasonFailed, "config", errorf("pg config: %w", err))
	}
	db := p.Database
	if db == "" {
		db = "postgres"
	}
	cfg.Host = p.Host
	cfg.Port = uint16(p.Port) //nolint:gosec // validated 1-65535
	cfg.Database = db
	cfg.User = p.User
	cfg.Password = string(p.Password)
	cfg.TLSConfig = p.TLS
	cfg.Fallbacks = nil
	cfg.SSLNegotiation = "postgres"
	cfg.KerberosSrvName, cfg.KerberosSpn = "", ""
	cfg.ConnectTimeout = 0 // the context bounds the connect
	// Startup parameters beat per-database and per-role settings.
	cfg.RuntimeParams = map[string]string{
		"application_name": "bekup-probe",
		"search_path":      pgSearchPath,
		"row_security":     pgRowSecurity,
	}
	cfg.LookupFunc = func(_ context.Context, host string) ([]string, error) {
		return []string{host}, nil // the caller's dialer resolves (guard / tunnel)
	}
	dial := p.Dial
	cfg.DialFunc = func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) }
	cfg.OnNotice = nil
	cfg.OnNotification = nil
	cfg.AfterConnect, cfg.ValidateConnect = nil, nil
	// No server message may make the runner buffer more than
	// pgMaxMessageBytes (a huge row of a version query, D-210): a longer
	// one breaks the session instead.
	cfg.BuildFrontend = func(r io.Reader, w io.Writer) *pgproto3.Frontend {
		f := pgproto3.NewFrontend(r, w)
		f.SetMaxBodyLen(pgMaxMessageBytes)
		return f
	}

	c, err := pgconn.ConnectConfig(cctx, cfg)
	cfg.Password = ""
	if err != nil {
		return nil, connectFailure(ctx, err, pgReason)
	}
	pc := &pgConn{c: c, eng: eng}
	if err := pc.harden(cctx); err != nil {
		ectx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
		discardErr(c.Close(ectx))
		cancel()
		return nil, err
	}
	return pc, nil
}

// harden verifies and pins the session settings (see pgSearchPath). It
// fails closed: a session whose settings cannot be confirmed is closed.
func (c *pgConn) harden(ctx context.Context) error {
	for _, q := range []string{pgCheckSettings, pgPinSettings} {
		r, err := c.rows(ctx, q, 1)
		if err != nil {
			return err
		}
		if len(r) != 1 || len(r[0]) != 2 || !sameSearchPath(r[0][0]) || r[0][1] != pgRowSecurity {
			return newError(runnerpb.ReasonFailed, "session settings", nil)
		}
	}
	r, err := c.rows(ctx, pgRestrictKinds, 1)
	if err != nil {
		return err
	}
	switch {
	case len(r) == 0:
		// Unpatched server: catalog reads stay safe (pg_catalog only),
		// row counts are refused.
	case len(r[0]) == 1 && strings.Contains(r[0][0], "view") && strings.Contains(r[0][0], "foreign-table"):
		c.restricted = true
	default:
		return newError(runnerpb.ReasonFailed, "session settings", nil)
	}
	return nil
}

// sameSearchPath reports whether a reported search_path is pgSearchPath
// (the server may re-format the list's spacing).
func sameSearchPath(s string) bool {
	return strings.ReplaceAll(s, " ", "") == strings.ReplaceAll(pgSearchPath, " ", "")
}

// pgReason maps PostgreSQL error responses.
func pgReason(err error) (runnerpb.Reason, string, bool) {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		d := "sqlstate=" + pe.Code
		switch pe.Code {
		case "28P01", "28000":
			return runnerpb.ReasonAuthFailed, d, true
		case "42501":
			return runnerpb.ReasonPermission, d, true
		case "3D000":
			return runnerpb.ReasonDatabaseNotFound, d, true
		case "53300", "57P03", "53000":
			return runnerpb.ReasonUnreachable, d, true
		case "57014":
			return runnerpb.ReasonTimeout, d, true
		default:
			return runnerpb.ReasonFailed, d, true
		}
	}
	return "", "", false
}

// query runs one simple-protocol statement and returns the first column of
// every row (bounded).
func (c *pgConn) query(ctx context.Context, sql string, limit int) ([]string, error) {
	rr := c.c.ExecParams(ctx, sql, nil, nil, nil, nil)
	var out []string
	for rr.NextRow() {
		if len(out) >= limit {
			discardResult(rr.Close())
			return nil, newError(runnerpb.ReasonFailed, "too many rows", nil)
		}
		v := rr.Values()
		if len(v) == 0 {
			continue
		}
		out = append(out, string(v[0]))
	}
	if _, err := rr.Close(); err != nil {
		return nil, queryFailure(ctx, err, pgReason)
	}
	return out, nil
}

func (c *pgConn) ServerVersion(ctx context.Context) (string, engine.Version, error) {
	rows, err := c.query(ctx, c.eng.VersionQuery(), 1)
	if err != nil {
		return "", engine.Version{}, err
	}
	if len(rows) != 1 {
		return "", engine.Version{}, newError(runnerpb.ReasonFailed, "version rows", nil)
	}
	v, err := parseVersion(c.eng, cleanValue(rows[0]))
	if err != nil {
		return "", engine.Version{}, err
	}
	raw := cleanValue(c.c.ParameterStatus("server_version"))
	if raw == "" {
		raw = strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor)
	}
	return raw, v, nil
}

func (c *pgConn) Databases(ctx context.Context) ([]string, int, error) {
	rows, err := c.query(ctx, c.eng.ListDatabasesQuery(), MaxDatabases)
	if err != nil {
		return nil, 0, err
	}
	names, skipped := filterDatabases(c.eng, rows)
	return names, skipped, nil
}

func (c *pgConn) NonInnoDBTables(context.Context, string) (int, error) { return 0, nil }

func (c *pgConn) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	return c.c.Close(ctx)
}
