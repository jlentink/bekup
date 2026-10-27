package dbprobe

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/pinning"
	"github.com/jlentink/bekup/internal/runnerpb"
)

const testPassword = "probe-Secret-pw-123"

func TestFilterDatabases(t *testing.T) {
	pg, err := engine.ForKind(engine.KindPostgres)
	if err != nil {
		t.Fatal(err)
	}
	got, skipped := filterDatabases(pg, []string{"zeta", "app", "template1", "rdsadmin", "bad name;", "app", "-x"})
	if !slices.Equal(got, []string{"app", "zeta"}) || skipped != 2 {
		t.Fatalf("pg: %v skipped %d", got, skipped)
	}
	my, err := engine.ForKind(engine.KindMySQL)
	if err != nil {
		t.Fatal(err)
	}
	got, skipped = filterDatabases(my, []string{"mysql", "sys", "information_schema", "performance_schema", "shop"})
	if !slices.Equal(got, []string{"shop"}) || skipped != 0 {
		t.Fatalf("mysql: %v skipped %d", got, skipped)
	}
}

func TestReasons(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want runnerpb.Reason
	}{
		{"pg auth", &pgconn.PgError{Code: "28P01"}, runnerpb.ReasonAuthFailed},
		{"pg hba", &pgconn.PgError{Code: "28000"}, runnerpb.ReasonAuthFailed},
		{"pg permission", &pgconn.PgError{Code: "42501"}, runnerpb.ReasonPermission},
		{"pg no db", &pgconn.PgError{Code: "3D000"}, runnerpb.ReasonDatabaseNotFound},
		{"pg too many", &pgconn.PgError{Code: "53300"}, runnerpb.ReasonUnreachable},
		{"pg other", &pgconn.PgError{Code: "XX000"}, runnerpb.ReasonFailed},
	}
	for _, tt := range tests {
		r, _, ok := pgReason(fmt.Errorf("wrapped: %w", tt.err))
		if !ok || r != tt.want {
			t.Errorf("%s: %v %v", tt.name, r, ok)
		}
	}
	my := []struct {
		err  error
		want runnerpb.Reason
	}{
		{&mysql.MySQLError{Number: 1045}, runnerpb.ReasonAuthFailed},
		{&mysql.MySQLError{Number: 1130}, runnerpb.ReasonAuthFailed},
		{&mysql.MySQLError{Number: 1044}, runnerpb.ReasonPermission},
		{&mysql.MySQLError{Number: 1227}, runnerpb.ReasonPermission},
		{&mysql.MySQLError{Number: 3159}, runnerpb.ReasonTLSFailed},
		{&mysql.MySQLError{Number: 1049}, runnerpb.ReasonDatabaseNotFound},
		{&mysql.MySQLError{Number: 1040}, runnerpb.ReasonUnreachable},
		{&mysql.MySQLError{Number: 1064}, runnerpb.ReasonFailed},
		{mysql.ErrNoTLS, runnerpb.ReasonTLSFailed},
		{mysql.ErrCleartextPassword, runnerpb.ReasonAuthFailed},
	}
	for _, tt := range my {
		r, _, ok := myReason(tt.err)
		if !ok || r != tt.want {
			t.Errorf("%v: %v %v", tt.err, r, ok)
		}
	}
	if _, _, ok := pgReason(errors.New("dial tcp: connection refused")); ok {
		t.Error("network error classified as a database answer")
	}
	tl := []struct {
		err  error
		want runnerpb.Reason
		det  string
	}{
		{&pinning.MismatchError{Reason: pinning.ReasonLeafSPKI}, runnerpb.ReasonPinMismatch, pinning.ReasonLeafSPKI},
		{x509.UnknownAuthorityError{}, runnerpb.ReasonPinMismatch, pinning.ReasonUntrusted},
		{x509.HostnameError{}, runnerpb.ReasonPinMismatch, pinning.ReasonHostname},
		{x509.CertificateInvalidError{Reason: x509.Expired}, runnerpb.ReasonPinMismatch, pinning.ReasonExpired},
		{errors.New("server refused TLS connection"), runnerpb.ReasonTLSFailed, "tls refused"},
	}
	for _, tt := range tl {
		r, d, ok := tlsReason(fmt.Errorf("x: %w", tt.err))
		if !ok || r != tt.want || d != tt.det {
			t.Errorf("%T: %v %q %v", tt.err, r, d, ok)
		}
	}
}

func dialTo(addr string) DialFunc {
	return func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	}
}

// listen serves every connection with h until the test ends.
func listen(t *testing.T, h func(net.Conn)) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	t.Cleanup(func() {
		discardErr(ln.Close())
		wg.Wait()
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { discardErr(c.Close()) }()
				discardErr(c.SetDeadline(time.Now().Add(5 * time.Second)))
				h(c)
			}()
		}
	}()
	return ln.Addr().String()
}

func params(kind engine.Kind, dial DialFunc) Params {
	return Params{Kind: kind, Host: "db.example.test", Port: 5432, User: "backup", Password: []byte(testPassword),
		Dial: dial, ConnectTimeout: 2 * time.Second}
}

// TestNoPortScanning: a closed port, a silent (filtered) port and a
// non-database service yield the same normalized error.
func TestNoPortScanning(t *testing.T) {
	closed := func() string {
		var lc net.ListenConfig
		ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		a := ln.Addr().String()
		discardErr(ln.Close())
		return a
	}()
	silent := listen(t, func(c net.Conn) { discardResult(c.Read(make([]byte, 1))); time.Sleep(3 * time.Second) })
	httpish := listen(t, func(c net.Conn) {
		discardResult(c.Read(make([]byte, 512)))
		discardResult(c.Write([]byte("HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")))
	})
	sshish := listen(t, func(c net.Conn) { discardResult(c.Write([]byte("SSH-2.0-OpenSSH_9.9\r\n"))) })
	for _, kind := range []engine.Kind{engine.KindPostgres, engine.KindMySQL, engine.KindMariaDB} {
		for name, addr := range map[string]string{"closed": closed, "silent": silent, "http": httpish, "ssh": sshish} {
			_, err := Open(t.Context(), params(kind, dialTo(addr)))
			if r := ReasonOf(err); r != runnerpb.ReasonUnreachable {
				t.Errorf("%s %s: reason %q (%v)", kind, name, r, err)
			}
			// The error (runner log only) carries a fixed class, never the
			// address or a server answer.
			_, port, _ := strings.Cut(addr, ":")
			if msg := err.Error(); strings.Contains(msg, port) || strings.Contains(msg, "HTTP") || strings.Contains(msg, "SSH-") {
				t.Errorf("%s %s: error leaks details: %q", kind, name, msg)
			}
		}
	}
}

// fakePG is a minimal PostgreSQL server: password authentication (the
// password must equal want) and answers to the probe queries.
func fakePG(t *testing.T, want string, dbs []string) string {
	t.Helper()
	return listen(t, func(c net.Conn) {
		be := pgproto3.NewBackend(c, c)
		msg, err := be.ReceiveStartupMessage()
		if err != nil {
			return
		}
		if _, ok := msg.(*pgproto3.SSLRequest); ok {
			discardResult(c.Write([]byte("N")))
			if _, err := be.ReceiveStartupMessage(); err != nil {
				return
			}
		}
		be.Send(&pgproto3.AuthenticationCleartextPassword{})
		if be.Flush() != nil {
			return
		}
		if be.SetAuthType(pgproto3.AuthTypeCleartextPassword) != nil {
			return
		}
		pm, err := be.Receive()
		if err != nil {
			return
		}
		if p, ok := pm.(*pgproto3.PasswordMessage); !ok || p.Password != want {
			be.Send(&pgproto3.ErrorResponse{Severity: "FATAL", Code: "28P01", Message: "password authentication failed for user \"backup\""})
			discardErr(be.Flush())
			return
		}
		be.Send(&pgproto3.AuthenticationOk{})
		be.Send(&pgproto3.ParameterStatus{Name: "server_version", Value: "16.4 (Debian)"})
		be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		if be.Flush() != nil {
			return
		}
		var query string
		for {
			m, err := be.Receive()
			if err != nil {
				return
			}
			switch m := m.(type) {
			case *pgproto3.Parse:
				query = m.Query
			case *pgproto3.Sync:
				be.Send(&pgproto3.ParseComplete{})
				be.Send(&pgproto3.BindComplete{})
				be.Send(&pgproto3.RowDescription{Fields: []pgproto3.FieldDescription{{Name: []byte("v"), DataTypeOID: 25, Format: 0}}})
				rows := dbs
				if strings.Contains(query, "server_version_num") {
					rows = []string{"160004"}
				}
				for _, r := range rows {
					be.Send(&pgproto3.DataRow{Values: [][]byte{[]byte(r)}})
				}
				be.Send(&pgproto3.CommandComplete{CommandTag: []byte("SELECT 1")})
				be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
				if be.Flush() != nil {
					return
				}
			case *pgproto3.Terminate:
				return
			}
		}
	})
}

func TestPostgresProbe(t *testing.T) {
	addr := fakePG(t, testPassword, []string{"app", "postgres", "template1", "zz"})
	c, err := Open(t.Context(), params(engine.KindPostgres, dialTo(addr)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { discardErr(c.Close()) }()
	raw, v, err := c.ServerVersion(t.Context())
	if err != nil || raw != "16.4 (Debian)" || v != (engine.Version{Major: 16, Minor: 4}) {
		t.Fatalf("version %q %v %v", raw, v, err)
	}
	dbs, skipped, err := c.Databases(t.Context())
	if err != nil || !slices.Equal(dbs, []string{"app", "postgres", "zz"}) || skipped != 0 {
		t.Fatalf("databases %v %d %v", dbs, skipped, err)
	}
	if n, err := c.NonInnoDBTables(t.Context(), "app"); n != 0 || err != nil {
		t.Fatalf("non-innodb on pg: %d %v", n, err)
	}
}

func TestPostgresAuthFailed(t *testing.T) {
	addr := fakePG(t, "other", nil)
	_, err := Open(t.Context(), params(engine.KindPostgres, dialTo(addr)))
	if r := ReasonOf(err); r != runnerpb.ReasonAuthFailed {
		t.Fatalf("reason %q (%v)", r, err)
	}
	if strings.Contains(err.Error(), testPassword) || strings.Contains(err.Error(), "backup") {
		t.Fatalf("error leaks details: %q", err.Error())
	}
}

func TestPostgresTLSRefused(t *testing.T) {
	addr := fakePG(t, testPassword, nil)
	p := params(engine.KindPostgres, dialTo(addr))
	p.TLS = UnverifiedTLS("db.example.test")
	_, err := Open(t.Context(), p)
	if r := ReasonOf(err); r != runnerpb.ReasonTLSFailed {
		t.Fatalf("reason %q (%v)", r, err)
	}
}

func TestOpenRejectsInvalidParams(t *testing.T) {
	p := params(engine.KindPostgres, nil)
	if _, err := Open(t.Context(), p); ReasonOf(err) != runnerpb.ReasonFailed {
		t.Fatalf("nil dial: %v", err)
	}
	p = params("oracle", dialTo("127.0.0.1:1"))
	if _, err := Open(t.Context(), p); ReasonOf(err) != runnerpb.ReasonFailed {
		t.Fatalf("bad kind: %v", err)
	}
}

func TestMismatchCause(t *testing.T) {
	err := newError(runnerpb.ReasonPinMismatch, pinning.ReasonHostname, x509.HostnameError{})
	me, ok := MismatchCause(err)
	if !ok || me.Reason != pinning.ReasonHostname {
		t.Fatalf("%v %v", me, ok)
	}
	if me, ok := MismatchCause(newError(runnerpb.ReasonAuthFailed, "", nil)); ok {
		t.Fatalf("auth failure is not a mismatch: %v", me)
	}
}
