// Package dbprobe connects to a target database with the native Go driver
// inside the runner (pgx for PostgreSQL, go-sql-driver/mysql for MySQL and
// MariaDB) over a connection the caller dials (target allowlist guard or
// SSH tunnel). It detects the server version, lists the databases and runs
// the few read-only checks the runner RPCs need (D-033, D-134).
//
// Every error is an *Error carrying a normalized runnerpb.Reason: refused,
// filtered, timed-out and non-database endpoints all look the same
// (ReasonUnreachable), so the runner cannot be used as a port scanner.
package dbprobe

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// Limits.
const (
	// MaxDatabases bounds the list a server may report.
	MaxDatabases = 10000
	// DefaultConnectTimeout bounds dial + TLS + authentication when the
	// context has no earlier deadline.
	DefaultConnectTimeout = 15 * time.Second
	// maxValueBytes bounds a single reported value (version, name).
	maxValueBytes = 256
)

// DialFunc opens the raw TCP connection to the target.
type DialFunc func(ctx context.Context) (net.Conn, error)

// Params describe one connection. Password is borrowed: the drivers need a
// Go string, which cannot be zeroed (documented limitation, like the SSH
// password in internal/tunnel); the caller zeroes its own bytes.
type Params struct {
	Kind engine.Kind
	// Host and Port are the configured target (PostgreSQL startup and TLS
	// server name come from the TLS config).
	Host string
	Port int
	User string
	// Password may be empty (trust / certificate authentication).
	Password []byte
	// Database is the PostgreSQL database to connect to (default
	// "postgres"); ignored for MySQL and MariaDB.
	Database string
	// TLS is nil for TLS mode DISABLED. The caller builds it: pinned
	// (pinning.ClientTLSConfig) or unverified.
	TLS *tls.Config
	// Dial opens the connection (mandatory).
	Dial DialFunc
	// ConnectTimeout overrides DefaultConnectTimeout.
	ConnectTimeout time.Duration
}

// Conn is an open probe connection. It is used by one goroutine.
type Conn interface {
	// ServerVersion returns the version string as reported and parsed.
	ServerVersion(ctx context.Context) (string, engine.Version, error)
	// Databases lists the server's databases without the engine's system
	// databases, sorted. Names that fail engine.ValidateDBName are
	// skipped and counted.
	Databases(ctx context.Context) (names []string, skipped int, err error)
	// NonInnoDBTables counts the base tables of db that do not use InnoDB
	// (MySQL/MariaDB; 0 for PostgreSQL).
	NonInnoDBTables(ctx context.Context, db string) (int, error)
	// Close closes the connection.
	Close() error
}

// Error is a normalized probe error. Its message is the reason plus a
// fixed detail (SQLSTATE / error number / error class); it never contains
// server messages, names or credentials.
type Error struct {
	Reason runnerpb.Reason
	// Detail is a safe classification for the runner log (for example
	// "sqlstate=28P01", "mysql=1045", "net").
	Detail string
	cause  error
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return "dbprobe: " + string(e.Reason)
	}
	return "dbprobe: " + string(e.Reason) + " (" + e.Detail + ")"
}

// Unwrap returns the driver error (for errors.Is / errors.As only; never
// log or return its message).
func (e *Error) Unwrap() error { return e.cause }

// ReasonOf returns the reason of a probe error ("" for other errors).
func ReasonOf(err error) runnerpb.Reason {
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Reason
	}
	return ""
}

func newError(r runnerpb.Reason, detail string, cause error) *Error {
	return &Error{Reason: r, Detail: detail, cause: cause}
}

// Open connects and authenticates. The returned error is an *Error.
func Open(ctx context.Context, p Params) (Conn, error) {
	if p.Dial == nil || !p.Kind.Valid() || engine.ValidateHost(p.Host) != nil ||
		engine.ValidatePort(p.Port) != nil || engine.ValidateUser(p.User) != nil {
		return nil, newError(runnerpb.ReasonFailed, "invalid parameters", nil)
	}
	timeout := p.ConnectTimeout
	if timeout <= 0 {
		timeout = DefaultConnectTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	eng, err := engine.ForKind(p.Kind)
	if err != nil {
		return nil, newError(runnerpb.ReasonFailed, "engine", err)
	}
	if p.Kind == engine.KindPostgres {
		return openPG(cctx, ctx, p, eng)
	}
	return openMySQL(cctx, ctx, p, eng)
}

// filterDatabases drops system databases and invalid names and sorts.
func filterDatabases(eng engine.Engine, names []string) ([]string, int) {
	out := make([]string, 0, len(names))
	skipped := 0
	for _, n := range names {
		switch {
		case eng.IsSystemDatabase(n):
		case engine.ValidateDBName(n) != nil:
			skipped++
		default:
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return slices.Compact(out), skipped
}

// cleanValue bounds and trims a reported value.
func cleanValue(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxValueBytes {
		s = s[:maxValueBytes]
	}
	return s
}

// parseVersion maps a version parse error to ReasonUnsupportedServer.
func parseVersion(eng engine.Engine, raw string) (engine.Version, error) {
	v, err := eng.ParseServerVersion(raw)
	if err != nil {
		return engine.Version{}, newError(runnerpb.ReasonUnsupportedServer, "version", err)
	}
	return v, nil
}

// connectFailure classifies an error during connect + authentication.
// Anything that is not a recognized database answer is "unreachable".
func connectFailure(ctx context.Context, err error, dbReason func(error) (runnerpb.Reason, string, bool)) *Error {
	if r, d, ok := tlsReason(err); ok {
		return newError(r, d, err)
	}
	if r, d, ok := dbReason(err); ok {
		return newError(r, d, err)
	}
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return newError(runnerpb.ReasonFailed, "canceled", err)
	}
	return newError(runnerpb.ReasonUnreachable, "connect: "+errorClass(err), err)
}

// errorClass names the kind of a connection failure for the runner log
// (fixed words and Go type names only, never messages).
func errorClass(err error) string {
	var (
		ne    net.Error
		alert tls.AlertError
		rhe   tls.RecordHeaderError
	)
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused"
	case errors.Is(err, syscall.ECONNRESET):
		return "reset"
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return "no route"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "eof"
	case errors.As(err, &alert):
		return "tls alert " + strconv.Itoa(int(alert))
	case errors.As(err, &rhe):
		return "tls record header"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	}
	inner := err
	for {
		u := errors.Unwrap(inner)
		if u == nil {
			break
		}
		inner = u
	}
	return fmt.Sprintf("%T", inner)
}

// queryFailure classifies an error after authentication.
func queryFailure(ctx context.Context, err error, dbReason func(error) (runnerpb.Reason, string, bool)) *Error {
	if r, d, ok := dbReason(err); ok {
		return newError(r, d, err)
	}
	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		return newError(runnerpb.ReasonTimeout, "query", err)
	}
	return newError(runnerpb.ReasonFailed, "query", err)
}

// discardErr marks errors of best-effort cleanup as intentionally ignored.
func discardErr(error) {}

// discardResult ignores a result and its error (best-effort cleanup).
func discardResult[T any](_ T, err error) { discardErr(err) }

// errorf builds an unclassified internal error (never request data).
func errorf(format string, args ...any) error { return fmt.Errorf("dbprobe: "+format, args...) }
