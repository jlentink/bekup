package dbprobe

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// closeTimeout bounds closing a connection.
const closeTimeout = 5 * time.Second

// discardLogger drops the driver's own log lines (they may carry
// addresses and server messages; the runner logs the normalized error).
type discardLogger struct{}

func (discardLogger) Print(...any) {}

type myConn struct {
	db  *sql.DB
	c   *sql.Conn
	eng engine.Engine
}

func openMySQL(cctx, ctx context.Context, p Params, eng engine.Engine) (Conn, error) {
	cfg := mysql.NewConfig()
	cfg.User = p.User
	cfg.Passwd = string(p.Password)
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
	cfg.Logger = discardLogger{}
	cfg.AllowAllFiles = false
	cfg.AllowCleartextPasswords = false
	cfg.AllowOldPasswords = false
	cfg.AllowFallbackToPlaintext = false
	cfg.MultiStatements = false
	cfg.InterpolateParams = false
	cfg.Params = nil
	cfg.ConnectionAttributes = "program_name:bekup-probe"
	if p.TLS != nil {
		cfg.TLS = p.TLS
	} else {
		cfg.TLSConfig = "false"
	}
	dial := p.Dial
	cfg.DialFunc = func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) }
	// The connector keeps its own copy of the configuration (and the
	// password string, which Go cannot zero; D-134 limitation).
	conn, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, newError(runnerpb.ReasonFailed, "config", errorf("mysql config: %w", err))
	}
	db := sql.OpenDB(conn)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	c, err := db.Conn(cctx)
	if err != nil {
		discardErr(db.Close())
		return nil, connectFailure(ctx, err, myReason)
	}
	return &myConn{db: db, c: c, eng: eng}, nil
}

// myReason maps MySQL/MariaDB server errors and the driver's
// authentication refusals.
func myReason(err error) (runnerpb.Reason, string, bool) {
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		d := "mysql=" + strconv.Itoa(int(me.Number))
		switch me.Number {
		case 1045, 1130, 1251, 1698, 1862:
			return runnerpb.ReasonAuthFailed, d, true
		case 1044, 1142, 1143, 1227, 1370:
			return runnerpb.ReasonPermission, d, true
		case 3159:
			return runnerpb.ReasonTLSFailed, d, true
		case 1049:
			return runnerpb.ReasonDatabaseNotFound, d, true
		case 1040, 1129, 1203:
			return runnerpb.ReasonUnreachable, d, true
		case 3024, 1969:
			return runnerpb.ReasonTimeout, d, true
		default:
			return runnerpb.ReasonFailed, d, true
		}
	}
	switch {
	case errors.Is(err, mysql.ErrNoTLS):
		return runnerpb.ReasonTLSFailed, "no tls", true
	case errors.Is(err, mysql.ErrCleartextPassword), errors.Is(err, mysql.ErrNativePassword),
		errors.Is(err, mysql.ErrOldPassword), errors.Is(err, mysql.ErrUnknownPlugin):
		return runnerpb.ReasonAuthFailed, "auth method", true
	}
	return "", "", false
}

func (c *myConn) column(ctx context.Context, limit int, q string, args ...any) ([]string, error) {
	rows, err := c.c.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, queryFailure(ctx, err, myReason)
	}
	defer func() { discardErr(rows.Close()) }()
	var out []string
	for rows.Next() {
		if len(out) >= limit {
			return nil, newError(runnerpb.ReasonFailed, "too many rows", nil)
		}
		var s sql.NullString
		if err := rows.Scan(&s); err != nil {
			return nil, queryFailure(ctx, err, myReason)
		}
		out = append(out, s.String)
	}
	if err := rows.Err(); err != nil {
		return nil, queryFailure(ctx, err, myReason)
	}
	return out, nil
}

func (c *myConn) ServerVersion(ctx context.Context) (string, engine.Version, error) {
	rows, err := c.column(ctx, 1, c.eng.VersionQuery())
	if err != nil {
		return "", engine.Version{}, err
	}
	if len(rows) != 1 {
		return "", engine.Version{}, newError(runnerpb.ReasonFailed, "version rows", nil)
	}
	raw := cleanValue(rows[0])
	v, err := parseVersion(c.eng, raw)
	if err != nil {
		return "", engine.Version{}, err
	}
	return raw, v, nil
}

func (c *myConn) Databases(ctx context.Context) ([]string, int, error) {
	rows, err := c.column(ctx, MaxDatabases, c.eng.ListDatabasesQuery())
	if err != nil {
		return nil, 0, err
	}
	names, skipped := filterDatabases(c.eng, rows)
	return names, skipped, nil
}

// nonInnoDBQuery counts base tables with another storage engine (views
// have no engine).
const nonInnoDBQuery = "SELECT CAST(COUNT(*) AS CHAR) FROM information_schema.TABLES " +
	"WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE' AND ENGINE IS NOT NULL AND ENGINE <> 'InnoDB'"

func (c *myConn) NonInnoDBTables(ctx context.Context, db string) (int, error) {
	rows, err := c.column(ctx, 1, nonInnoDBQuery, db)
	if err != nil {
		return 0, err
	}
	if len(rows) != 1 {
		return 0, newError(runnerpb.ReasonFailed, "count rows", nil)
	}
	n, err := strconv.Atoi(rows[0])
	if err != nil || n < 0 {
		return 0, newError(runnerpb.ReasonFailed, "count", nil)
	}
	return n, nil
}

func (c *myConn) Close() error {
	return errors.Join(c.c.Close(), c.db.Close())
}
