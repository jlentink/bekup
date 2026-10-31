//go:build integration

package externaltest

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/jlentink/bekup/internal/testutil/dockertest"
)

// Pinned images of the supported major versions (the digests of
// internal/engine/catalog.toml; Renovate keeps both current).
const (
	PG15 = "postgres:15.19-alpine@sha256:54e0dd8e8cddcdbb9156c2486eed5a43eccb7214d7c6828097d41ce049fb3984"
	PG16 = "postgres:16.15-alpine@sha256:0cefaabd882e42bcb2a2f3dc56d3ce4770560ba62fd7f197f1a2e008fd5731ab"
	PG17 = "postgres:17.11-alpine@sha256:266d3c257594e195b309ae9d1805e54c788f1d7b375f56bf6c367aac6c917e7b"
)

// ServerName is the DNS SAN of every test server certificate besides the
// Docker host name and 127.0.0.1 (tls_server_name tests).
const ServerName = "pg.bekup.test"

// SuperPassword is the password of the image's superuser "postgres".
const SuperPassword = "postgres-super-password" //nolint:gosec // G101: a throw-away test container password

// Options configure Start.
type Options struct {
	// RSA issues an RSA-2048 CA and leaf instead of ECDSA P-256.
	RSA bool
}

// Server is a running external test PostgreSQL.
type Server struct {
	Host   string // Docker host as seen from the test (a SAN of the cert)
	Port   uint16
	CAFile string // PEM CA bundle (database.tls_ca_file)
	c      *postgres.PostgresContainer
}

// Start runs image and terminates it when t ends. It skips (fails in CI)
// without Docker.
func Start(t *testing.T, image string, o Options) *Server {
	t.Helper()
	ctx := t.Context()
	prov, err := testcontainers.NewDockerProvider()
	if err != nil {
		dockertest.Skipf(t, "docker: %v", err)
	}
	host, err := prov.DaemonHost(ctx)
	if cerr := prov.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		dockertest.Skipf(t, "docker host: %v", err)
	}
	dir := t.TempDir()
	certPEM, keyPEM, caPEM, err := issue(host, o.RSA)
	if err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caFile, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	entrypoint := filepath.Join(filepath.Dir(file), "..", "..", "..", "deploy", "postgres", "entrypoint.sh")
	c, err := postgres.Run(ctx, image,
		postgres.WithUsername("postgres"),
		postgres.WithPassword(SuperPassword),
		postgres.WithDatabase("postgres"),
		testcontainers.WithFiles(
			testcontainers.ContainerFile{Reader: strings.NewReader(string(certPEM)), ContainerFilePath: "/run/secrets/postgres.crt", FileMode: 0o444},
			testcontainers.ContainerFile{Reader: strings.NewReader(string(keyPEM)), ContainerFilePath: "/run/secrets/postgres.key", FileMode: 0o444},
			testcontainers.ContainerFile{Reader: strings.NewReader(string(caPEM)), ContainerFilePath: "/run/secrets/postgres_ca.crt", FileMode: 0o444},
			testcontainers.ContainerFile{HostFilePath: entrypoint, ContainerFilePath: "/usr/local/bin/bekup-entrypoint.sh", FileMode: 0o555},
		),
		testcontainers.WithEnv(map[string]string{"POSTGRES_INITDB_ARGS": "--auth-host=scram-sha-256"}),
		testcontainers.WithConfigModifier(func(cfg *container.Config) { cfg.User = "70:70" }),
		testcontainers.WithTmpfs(map[string]string{"/var/lib/postgresql/tls": "rw,noexec,nosuid,nodev,size=1m,uid=70,gid=70,mode=0700"}),
		testcontainers.WithEntrypoint("/usr/local/bin/bekup-entrypoint.sh"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		dockertest.Skipf(t, "start postgres %s: %v", image, err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(c); err != nil {
			t.Errorf("terminate postgres: %v", err)
		}
	})
	p, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(p.Port(), 10, 16)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{Host: host, Port: uint16(port), CAFile: caFile, c: c}
}

// SuperExec runs statements as the superuser in dbname (plain TCP).
func (s *Server) SuperExec(ctx context.Context, dbname string, stmts ...string) (err error) {
	conn, err := pgx.Connect(ctx, fmt.Sprintf("postgres://postgres:%s@%s/%s?sslmode=disable",
		SuperPassword, net.JoinHostPort(s.Host, strconv.Itoa(int(s.Port))), dbname))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, conn.Close(context.WithoutCancel(ctx))) }()
	for _, st := range stmts {
		if _, err := conn.Exec(ctx, st); err != nil {
			return fmt.Errorf("%s: %w", st, err)
		}
	}
	return nil
}

// CreateAdmin creates a non-superuser admin with CREATEROLE and CREATEDB
// (the shape of managed providers' admin accounts).
func (s *Server) CreateAdmin(ctx context.Context, name, password string, extra ...string) error {
	return s.SuperExec(ctx, "postgres", append([]string{
		fmt.Sprintf("CREATE ROLE %s LOGIN CREATEROLE CREATEDB PASSWORD '%s'", pgx.Identifier{name}.Sanitize(), password),
	}, extra...)...)
}

// Exec runs a command in the container (as user, "" for the default) and
// returns its combined output.
func (s *Server) Exec(ctx context.Context, user string, cmd ...string) (string, error) {
	opts := []tcexec.ProcessOption{tcexec.Multiplexed()}
	if user != "" {
		opts = append(opts, tcexec.WithUser(user))
	}
	code, r, err := s.c.Exec(ctx, cmd, opts...)
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

// CopyFile copies content into the container at path (mode 0444).
func (s *Server) CopyFile(ctx context.Context, path string, content []byte) error {
	return s.c.CopyToContainer(ctx, content, path, 0o444)
}

// issue creates a CA and a server certificate for host, 127.0.0.1 and
// ServerName.
func issue(host string, useRSA bool) (certPEM, keyPEM, caPEM []byte, err error) {
	newKey := func() (crypto.Signer, error) {
		if useRSA {
			return rsa.GenerateKey(rand.Reader, 2048)
		}
		return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	}
	caKey, err := newKey()
	if err != nil {
		return nil, nil, nil, err
	}
	now := time.Now()
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "bekup external test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, caKey.Public(), caKey)
	if err != nil {
		return nil, nil, nil, err
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, nil, nil, err
	}
	leafKey, err := newKey()
	if err != nil {
		return nil, nil, nil, err
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: ServerName},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{ServerName}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	if useRSA {
		leaf.KeyUsage |= x509.KeyUsageKeyEncipherment
	}
	if ip := net.ParseIP(host); ip != nil {
		leaf.IPAddresses = append(leaf.IPAddresses, ip)
	} else {
		leaf.DNSNames = append(leaf.DNSNames, host)
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, leafKey.Public(), caKey)
	if err != nil {
		return nil, nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		return nil, nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), nil
}
