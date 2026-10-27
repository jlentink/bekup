//go:build integration

// Package dbtest starts a PostgreSQL testcontainer set up by the real deploy
// init scripts and hands out fresh, migrated bekup databases to integration
// tests of other packages (internal/audit, cmd/bekup). It mirrors the
// TestMain of internal/db's own integration tests, including TLS (D-100):
// the container runs deploy/postgres/entrypoint.sh with a postgres
// certificate from internal/certs, and every bekup-role connection uses
// verify-full against the test CA.
//
// Usage: func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }, then
// dbtest.Migrated(t) in each test.
package dbtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/logging"
)

// PGImage matches the app DB image of docker-compose.yml.
const PGImage = "postgres:17-alpine@sha256:266d3c257594e195b309ae9d1805e54c788f1d7b375f56bf6c367aac6c917e7b"

const (
	ownerPassword = "owner-test-password"
	appPassword   = "app-test-password"
	superUser     = "bekup_test_super"
	superPassword = "super-test-password"
)

var (
	superURL  string
	dbCounter atomic.Int64
	caFile    string
	tlsVerify db.TLSOptions
)

// Main starts the container, runs the tests and terminates it.
func Main(m *testing.M) int {
	ctx := context.Background()
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	scripts, err := filepath.Glob(filepath.Join(root, "deploy", "postgres", "init", "*"))
	if err != nil || len(scripts) == 0 {
		fmt.Fprintf(os.Stderr, "deploy/postgres/init scripts not found: %v\n", err)
		return 1
	}
	pkiDir, err := os.MkdirTemp("", "bekup-pki-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "pki dir: %v\n", err)
		return 1
	}
	defer func() {
		if err := os.RemoveAll(pkiDir); err != nil {
			fmt.Fprintf(os.Stderr, "remove pki dir: %v\n", err)
		}
	}()
	files, err := setupPKI(ctx, pkiDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "test PKI: %v\n", err)
		return 1
	}
	files = append(files,
		secretFile("/run/secrets/bekup_owner_password", ownerPassword),
		secretFile("/run/secrets/bekup_app_password", appPassword),
		testcontainers.ContainerFile{HostFilePath: filepath.Join(root, "deploy", "postgres", "entrypoint.sh"),
			ContainerFilePath: "/usr/local/bin/bekup-entrypoint.sh", FileMode: 0o555},
	)
	c, err := postgres.Run(ctx, PGImage,
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		postgres.WithDatabase("postgres"),
		postgres.WithInitScripts(scripts...),
		testcontainers.WithFiles(files...),
		testcontainers.WithEnv(map[string]string{
			"POSTGRES_BEKUP_OWNER_PASSWORD_FILE": "/run/secrets/bekup_owner_password",
			"POSTGRES_BEKUP_APP_PASSWORD_FILE":   "/run/secrets/bekup_app_password",
		}),
		// As in docker-compose.yml: the image's postgres user and the
		// wrapper entrypoint that enables TLS.
		testcontainers.WithConfigModifier(func(cfg *container.Config) { cfg.User = "70:70" }),
		testcontainers.WithTmpfs(map[string]string{"/var/lib/postgresql/tls": "rw,noexec,nosuid,nodev,size=1m,uid=70,gid=70,mode=0700"}),
		testcontainers.WithEntrypoint("/usr/local/bin/bekup-entrypoint.sh"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start postgres: %v\n", err)
		return 1
	}
	defer func() {
		if err := testcontainers.TerminateContainer(c); err != nil {
			fmt.Fprintf(os.Stderr, "terminate postgres: %v\n", err)
		}
	}()
	if superURL, err = c.ConnectionString(ctx, "sslmode=disable"); err != nil {
		fmt.Fprintf(os.Stderr, "connection string: %v\n", err)
		return 1
	}
	// The bootstrap superuser is refused over TCP (D-096): create a test
	// superuser over the container's Unix socket.
	code, out, err := c.Exec(ctx, []string{"psql", "--no-psqlrc", "-v", "ON_ERROR_STOP=1", "--username=postgres", "--dbname=postgres",
		"-c", "CREATE ROLE " + superUser + " LOGIN SUPERUSER PASSWORD '" + superPassword + "'"}, tcexec.Multiplexed())
	if err != nil || code != 0 {
		var msg []byte
		var rerr error
		if out != nil {
			msg, rerr = io.ReadAll(out)
		}
		fmt.Fprintf(os.Stderr, "create test superuser: exit %d: %v: %s\n", code, errors.Join(err, rerr), msg)
		return 1
	}
	return m.Run()
}

// setupPKI issues the CA and the postgres certificate (as `bekup certs init
// --component postgres`) for the Docker host name and returns the files to
// mount.
func setupPKI(ctx context.Context, dir string) ([]testcontainers.ContainerFile, error) {
	prov, err := testcontainers.NewDockerProvider()
	if err != nil {
		return nil, err
	}
	host, err := prov.DaemonHost(ctx)
	if cerr := prov.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	if _, err := certs.Init(certs.InitOptions{
		Dir: dir, Components: []string{certs.ComponentPostgres},
		PostgresNames: []string{host}, AllowUnencrypted: true,
	}); err != nil {
		return nil, err
	}
	l := certs.Layout{Dir: dir}
	caFile = l.BundleFile(certs.ComponentPostgres)
	cfg := config.DefaultBekup()
	cfg.Database.TLSCAFile = caFile
	if tlsVerify, err = db.LoadTLSOptions(&cfg); err != nil {
		return nil, err
	}
	return []testcontainers.ContainerFile{
		{HostFilePath: l.CertFile(certs.ComponentPostgres), ContainerFilePath: "/run/secrets/postgres.crt", FileMode: 0o444},
		{HostFilePath: l.KeyFile(certs.ComponentPostgres), ContainerFilePath: "/run/secrets/postgres.key", FileMode: 0o444},
		{HostFilePath: caFile, ContainerFilePath: "/run/secrets/postgres_ca.crt", FileMode: 0o444},
	}, nil
}

// TLS returns the verify-full TLS options for bekup-role connections.
func TLS() db.TLSOptions { return tlsVerify }

// CAFile returns the path of the test CA bundle (database.tls_ca_file). It
// exists while the tests run.
func CAFile() string { return caFile }

func secretFile(path, content string) testcontainers.ContainerFile {
	return testcontainers.ContainerFile{Reader: strings.NewReader(content), ContainerFilePath: path, FileMode: 0o444}
}

func urlFor(user, password, dbname string) string {
	u := strings.Replace(superURL, "postgres:postgres@", user+":"+password+"@", 1)
	i := strings.LastIndex(u, "/postgres?")
	return u[:i] + "/" + dbname + u[i+len("/postgres"):]
}

// DB is a fresh, fully migrated bekup database.
type DB struct {
	Name     string
	OwnerURL string
	AppURL   string
	SuperURL string
}

// Migrated returns a new database cloned from the init script's bekup
// database with all migrations applied.
func Migrated(t testing.TB) DB {
	t.Helper()
	ctx := t.Context()
	name := fmt.Sprintf("bekup_dbt%d", dbCounter.Add(1))
	d := DB{
		Name:     name,
		OwnerURL: urlFor("bekup_owner", ownerPassword, name),
		AppURL:   urlFor("bekup_app", appPassword, name),
		SuperURL: urlFor(superUser, superPassword, name),
	}
	if err := exec(ctx, urlFor(superUser, superPassword, "postgres"),
		"CREATE DATABASE "+name+" OWNER bekup_owner TEMPLATE bekup",
		"UPDATE pg_catalog.pg_database SET datacl = (SELECT datacl FROM pg_catalog.pg_database WHERE datname = 'bekup') WHERE datname = '"+name+"'",
	); err != nil {
		t.Fatal(err)
	}
	m, err := db.NewMigrator(ctx, []byte(d.OwnerURL), tlsVerify, logging.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := m.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	if _, err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}
	return d
}

// AppPool returns a pool connected as bekup_app (closed on cleanup).
func (d DB) AppPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	p, err := db.NewPool(t.Context(), []byte(d.AppURL), db.PoolOptions{
		MaxConns: 20, ApplicationName: "bekup-test", Registry: logging.NewRegistry(), TLS: tlsVerify,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// SuperExec runs statements as a superuser in d (tamper simulation).
func (d DB) SuperExec(ctx context.Context, stmts ...string) error {
	return exec(ctx, d.SuperURL, stmts...)
}

func exec(ctx context.Context, url string, stmts ...string) (err error) {
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, conn.Close(ctx)) }()
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	return nil
}
