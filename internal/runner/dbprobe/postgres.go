package dbprobe

import (
	"context"
	"errors"
	"net"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"

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

type pgConn struct {
	c   *pgconn.PgConn
	eng engine.Engine
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
	cfg.RuntimeParams = map[string]string{"application_name": "bekup-probe"}
	cfg.LookupFunc = func(_ context.Context, host string) ([]string, error) {
		return []string{host}, nil // the caller's dialer resolves (guard / tunnel)
	}
	dial := p.Dial
	cfg.DialFunc = func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) }
	cfg.OnNotice = nil
	cfg.OnNotification = nil
	cfg.AfterConnect, cfg.ValidateConnect = nil, nil

	c, err := pgconn.ConnectConfig(cctx, cfg)
	cfg.Password = ""
	if err != nil {
		return nil, connectFailure(ctx, err, pgReason)
	}
	return &pgConn{c: c, eng: eng}, nil
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
