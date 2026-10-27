//go:build integration

package db_test

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

// pgImage matches the app DB image of docker-compose.yml.
const pgImage = "postgres:17-alpine@sha256:266d3c257594e195b309ae9d1805e54c788f1d7b375f56bf6c367aac6c917e7b"

const (
	ownerPassword = "owner-test-password"
	appPassword   = "app-test-password"
	// The init script's pg_hba rules reject the bootstrap superuser
	// ("postgres") on TCP, so the tests create their own superuser over the
	// container's Unix socket for TCP access.
	superUser     = "bekup_test_super"
	superPassword = "super-test-password"
)

var (
	pgContainer *postgres.PostgresContainer
	superURL    string // postgres superuser, database postgres
	dbCounter   atomic.Int64

	// The container runs the real deploy/postgres/entrypoint.sh with a
	// postgres certificate from internal/certs (SANs: "postgres" and the
	// Docker host name used to reach it), so every test connects with
	// verify-full like the shipped stack (D-100).
	pgHost    string        // host name in the connection URLs
	pkiDir    string        // certs.Init output
	tlsVerify db.TLSOptions // verify-full against the test CA
)

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

func runMain(m *testing.M) int {
	ctx := context.Background()
	files, err := setupPKI(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "test PKI: %v\n", err)
		return 1
	}
	defer os.RemoveAll(pkiDir)
	// Run the real deploy init scripts (as docker-compose.yml mounts them at
	// /docker-entrypoint-initdb.d) so that drift between them and the
	// migrations is caught. They read the role passwords from secret files.
	scripts, err := filepath.Glob(filepath.Join("..", "..", "deploy", "postgres", "init", "*"))
	if err != nil || len(scripts) == 0 {
		fmt.Fprintf(os.Stderr, "deploy/postgres/init scripts not found: %v\n", err)
		return 1
	}
	entrypoint, err := filepath.Abs(filepath.Join("..", "..", "deploy", "postgres", "entrypoint.sh"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "entrypoint: %v\n", err)
		return 1
	}
	files = append(files,
		secretFile("/run/secrets/bekup_owner_password", ownerPassword),
		secretFile("/run/secrets/bekup_app_password", appPassword),
		testcontainers.ContainerFile{HostFilePath: entrypoint, ContainerFilePath: "/usr/local/bin/bekup-entrypoint.sh", FileMode: 0o555},
	)
	c, err := postgres.Run(ctx, pgImage,
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		postgres.WithDatabase("postgres"),
		postgres.WithInitScripts(scripts...),
		testcontainers.WithFiles(files...),
		testcontainers.WithEnv(map[string]string{
			"POSTGRES_BEKUP_OWNER_PASSWORD_FILE": "/run/secrets/bekup_owner_password",
			"POSTGRES_BEKUP_APP_PASSWORD_FILE":   "/run/secrets/bekup_app_password",
			// As in docker-compose.yml for TCP (loopback included); the
			// local socket stays trust for the test's own psql calls.
			"POSTGRES_INITDB_ARGS": "--auth-host=scram-sha-256",
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
	pgContainer = c
	superURL, err = c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "connection string: %v\n", err)
		return 1
	}
	if _, err := containerExec(ctx, "psql", "--no-psqlrc", "-v", "ON_ERROR_STOP=1", "--username=postgres", "--dbname=postgres",
		"-c", "CREATE ROLE "+superUser+" LOGIN SUPERUSER PASSWORD '"+superPassword+"'"); err != nil {
		fmt.Fprintf(os.Stderr, "create test superuser: %v\n", err)
		return 1
	}
	// The init scripts created the roles and the bekup database.
	if err := superExec(ctx, "postgres", `DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_database d JOIN pg_roles r ON r.oid = d.datdba
		               WHERE d.datname = 'bekup' AND r.rolname = 'bekup_owner')
		   OR NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'bekup_app') THEN
			RAISE EXCEPTION 'init scripts did not create bekup_owner, bekup_app and the bekup database';
		END IF;
	END $$`); err != nil {
		fmt.Fprintf(os.Stderr, "check init script result: %v\n", err)
		return 1
	}
	return m.Run()
}

// setupPKI issues the CA and the postgres certificate with internal/certs
// (exactly like `bekup certs init --component postgres`) and returns the
// files to mount as Docker-secret-like files.
func setupPKI(ctx context.Context) ([]testcontainers.ContainerFile, error) {
	prov, err := testcontainers.NewDockerProvider()
	if err != nil {
		return nil, err
	}
	pgHost, err = prov.DaemonHost(ctx)
	if cerr := prov.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	pkiDir, err = os.MkdirTemp("", "bekup-pki-")
	if err != nil {
		return nil, err
	}
	if _, err := certs.Init(certs.InitOptions{
		Dir: pkiDir, Components: []string{certs.ComponentPostgres},
		PostgresNames: []string{pgHost}, AllowUnencrypted: true,
	}); err != nil {
		return nil, err
	}
	l := certs.Layout{Dir: pkiDir}
	cfg := config.DefaultBekup()
	cfg.Database.TLSCAFile = l.BundleFile(certs.ComponentPostgres)
	if tlsVerify, err = db.LoadTLSOptions(&cfg); err != nil {
		return nil, err
	}
	return []testcontainers.ContainerFile{
		{HostFilePath: l.CertFile(certs.ComponentPostgres), ContainerFilePath: "/run/secrets/postgres.crt", FileMode: 0o444},
		{HostFilePath: l.KeyFile(certs.ComponentPostgres), ContainerFilePath: "/run/secrets/postgres.key", FileMode: 0o444},
		{HostFilePath: l.BundleFile(certs.ComponentPostgres), ContainerFilePath: "/run/secrets/postgres_ca.crt", FileMode: 0o444},
	}, nil
}

// rootsOf returns a pool with the given CA only.
func rootsOf(ca *x509.Certificate) *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca)
	return p
}

// secretFile is a Docker-secret-like file (mode 0444, as compose mounts it).
func secretFile(path, content string) testcontainers.ContainerFile {
	return testcontainers.ContainerFile{Reader: strings.NewReader(content), ContainerFilePath: path, FileMode: 0o444}
}

func urlFor(user, password, dbname string) string {
	u := strings.Replace(superURL, "postgres:postgres@", user+":"+password+"@", 1)
	i := strings.LastIndex(u, "/postgres?")
	return u[:i] + "/" + dbname + u[i+len("/postgres"):]
}

func superExec(ctx context.Context, dbname string, stmts ...string) (err error) {
	conn, err := pgx.Connect(ctx, urlFor(superUser, superPassword, dbname))
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

// testDB is a fresh database in the state the deploy init script leaves the
// bekup database in.
type testDB struct {
	name     string
	ownerURL string
	appURL   string
}

// newTestDB clones the init script's bekup database: TEMPLATE copies the
// schema-level state (public owned by bekup_owner, no PUBLIC access) and the
// database ACL, which CREATE DATABASE does not copy, is taken over verbatim.
// Nothing ever connects to the bekup database itself, so it stays usable as a
// template.
func newTestDB(t *testing.T) testDB {
	t.Helper()
	ctx := t.Context()
	name := fmt.Sprintf("bekup_t%d", dbCounter.Add(1))
	if err := superExec(ctx, "postgres",
		"CREATE DATABASE "+name+" OWNER bekup_owner TEMPLATE bekup",
		"UPDATE pg_catalog.pg_database SET datacl = (SELECT datacl FROM pg_catalog.pg_database WHERE datname = 'bekup') WHERE datname = '"+name+"'",
	); err != nil {
		t.Fatal(err)
	}
	return testDB{
		name:     name,
		ownerURL: urlFor("bekup_owner", ownerPassword, name),
		appURL:   urlFor("bekup_app", appPassword, name),
	}
}

func (d testDB) migrator(t *testing.T) *db.Migrator {
	t.Helper()
	m, err := db.NewMigrator(t.Context(), []byte(d.ownerURL), tlsVerify, logging.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return m
}

// migratedDB returns a fresh database with all migrations applied.
func migratedDB(t *testing.T) testDB {
	t.Helper()
	d := newTestDB(t)
	if _, err := d.migrator(t).Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	return d
}

func (d testDB) appPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return d.pool(t, d.appURL)
}

func (d testDB) ownerPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return d.pool(t, d.ownerURL)
}

func (d testDB) pool(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	p, err := db.NewPool(t.Context(), []byte(url), db.PoolOptions{
		MaxConns:        20,
		ApplicationName: "bekup-test",
		Registry:        logging.NewRegistry(),
		TLS:             tlsVerify,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// containerExec runs a command in the Postgres container and returns stdout
// and stderr combined.
func containerExec(ctx context.Context, cmd ...string) (string, error) {
	code, r, err := pgContainer.Exec(ctx, cmd, tcexec.Multiplexed())
	if err != nil {
		return "", err
	}
	out, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return string(out), fmt.Errorf("%s exited with %d: %s", cmd[0], code, out)
	}
	return string(out), nil
}

func withTimeout(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), d)
	t.Cleanup(cancel)
	return ctx
}
