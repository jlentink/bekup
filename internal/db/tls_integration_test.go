//go:build integration

package db_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/logging"
)

func connectWith(ctx context.Context, url string, o db.TLSOptions) (*pgxpool.Pool, error) {
	return db.NewPool(ctx, []byte(url), db.PoolOptions{
		MaxConns: 2, ConnectTimeout: 10 * time.Second, Registry: logging.NewRegistry(), TLS: o,
	})
}

// sslState returns whether the pool's session uses TLS and its version.
func sslState(ctx context.Context, t *testing.T, p *pgxpool.Pool) (bool, string) {
	t.Helper()
	var on bool
	var version *string
	if err := p.QueryRow(ctx, "SELECT ssl, version FROM pg_stat_ssl WHERE pid = pg_backend_pid()").Scan(&on, &version); err != nil {
		t.Fatal(err)
	}
	if version == nil {
		return on, ""
	}
	return on, *version
}

func otherCA(t *testing.T) *certs.CA {
	t.Helper()
	ca, err := certs.NewCA(certs.CAOptions{ServerNames: []string{"runner", "postgres", pgHost}})
	if err != nil {
		t.Fatal(err)
	}
	return ca
}

func TestTLSVerifyFull(t *testing.T) {
	ctx := withTimeout(t, time.Minute)
	d := migratedDB(t) // the migrator connected as bekup_owner with verify-full
	// d.appURL carries sslmode=disable: the configured mode must win.
	if !strings.Contains(d.appURL, "sslmode=disable") {
		t.Fatalf("test URL lost its sslmode: the override is not exercised")
	}
	for name, url := range map[string]string{"app": d.appURL, "owner": d.ownerURL} {
		p, err := connectWith(ctx, url, tlsVerify)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		on, version := sslState(ctx, t, p)
		p.Close()
		if !on || (version != "TLSv1.2" && version != "TLSv1.3") {
			t.Fatalf("%s: ssl=%t version=%q", name, on, version)
		}
	}
}

func TestTLSRejectsCertificateFromAnotherCA(t *testing.T) {
	ctx := withTimeout(t, time.Minute)
	d := newTestDB(t)
	o := db.TLSOptions{Mode: config.DBTLSVerifyFull, RootCAs: rootsOf(otherCA(t).Cert)}
	if p, err := connectWith(ctx, d.appURL, o); err == nil {
		p.Close()
		t.Fatal("verify-full accepted a server certificate from another CA")
	} else if !strings.Contains(err.Error(), "certificate signed by unknown authority") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := db.NewMigrator(ctx, []byte(d.ownerURL), o, logging.NewRegistry()); err == nil ||
		!strings.Contains(err.Error(), "unknown authority") {
		t.Fatalf("migrator: %v", err)
	}
	// require + CA verifies the chain as well (verify-ca behavior).
	o.Mode = config.DBTLSRequire
	if p, err := connectWith(ctx, d.appURL, o); err == nil {
		p.Close()
		t.Fatal("require with tls_ca_file accepted a certificate from another CA")
	}
}

func TestTLSRejectsWrongHostName(t *testing.T) {
	if pgHost != "localhost" {
		t.Skipf("needs a Docker host reachable as localhost and 127.0.0.1 (got %q)", pgHost)
	}
	ctx := withTimeout(t, time.Minute)
	d := migratedDB(t)
	// 127.0.0.1 reaches the same server but is not in its SANs.
	url := strings.Replace(d.appURL, "@localhost:", "@127.0.0.1:", 1)
	if url == d.appURL {
		t.Fatalf("could not rewrite the host")
	}
	if p, err := connectWith(ctx, url, tlsVerify); err == nil {
		p.Close()
		t.Fatal("verify-full accepted a host name outside the certificate's SANs")
	} else if !strings.Contains(err.Error(), "127.0.0.1") || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("unexpected error: %v", err)
	}
	// require (with the CA) does not check the host name: it connects.
	o := tlsVerify
	o.Mode = config.DBTLSRequire
	p, err := connectWith(ctx, url, o)
	if err != nil {
		t.Fatalf("require: %v", err)
	}
	p.Close()
}

func TestTLSDisableRefusedForBekupRoles(t *testing.T) {
	ctx := withTimeout(t, time.Minute)
	d := migratedDB(t)
	o := db.TLSOptions{Mode: config.DBTLSDisable}
	for name, url := range map[string]string{"app": d.appURL, "owner": d.ownerURL} {
		p, err := connectWith(ctx, url, o)
		if err == nil {
			p.Close()
			t.Fatalf("%s: plain-text connection accepted", name)
		}
		if !strings.Contains(err.Error(), "pg_hba.conf rejects connection") || !strings.Contains(err.Error(), "no encryption") {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
	}
	if _, err := db.NewMigrator(ctx, []byte(d.ownerURL), o, logging.NewRegistry()); err == nil {
		t.Fatal("migrator connected without TLS")
	}
}

func TestTLSRequireWithoutCA(t *testing.T) {
	ctx := withTimeout(t, time.Minute)
	d := migratedDB(t)
	cfg := config.DefaultBekup()
	cfg.Database.TLSMode, cfg.Database.TLSCAFile = config.DBTLSRequire, ""
	if ws := cfg.Warnings(); len(ws) == 0 || !strings.Contains(strings.Join(ws, "\n"), config.DBTLSRequireWarning) {
		t.Fatalf("require must warn: %v", ws)
	}
	o, err := db.LoadTLSOptions(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := connectWith(ctx, d.appURL, o)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if on, _ := sslState(ctx, t, p); !on {
		t.Fatal("require connected without TLS")
	}
}

func TestTLSStricterURLSSLModeRejected(t *testing.T) {
	ctx := withTimeout(t, time.Minute)
	d := newTestDB(t)
	url := strings.Replace(d.appURL, "sslmode=disable", "sslmode=verify-full", 1)
	_, err := connectWith(ctx, url, db.TLSOptions{Mode: config.DBTLSRequire})
	if err == nil || errors.Is(err, db.ErrInvalidURL) || !strings.Contains(err.Error(), "stricter") {
		t.Fatalf("got %v", err)
	}
}

// TestTLSChannelBindingLibpq proves with libpq (psql inside the container)
// that the postgres certificate chain from internal/certs supports SCRAM
// channel binding (D-104), and that manual psql sessions work with libpq's
// defaults. pgx connections in every other test already run with
// channel_binding=require.
func TestTLSChannelBindingLibpq(t *testing.T) {
	ctx := withTimeout(t, time.Minute)
	d := migratedDB(t)
	for name, conninfo := range map[string]string{
		"required": "host=localhost sslmode=verify-ca sslrootcert=/run/secrets/postgres_ca.crt channel_binding=require",
		"defaults": "host=localhost sslmode=require",
	} {
		out, err := containerExec(ctx, "sh", "-c",
			`PGPASSWORD="$1" psql --no-psqlrc -At -d "$2 dbname=$3 user=bekup_app" -c "SELECT ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid()"`,
			"psql", appPassword, conninfo, d.name)
		if err != nil || strings.TrimSpace(out) != "t" {
			t.Fatalf("%s: %q %v", name, out, err)
		}
	}
	// pgx: a URL that tries to switch binding off is refused.
	url := strings.Replace(d.appURL, "sslmode=disable", "channel_binding=disable", 1)
	if _, err := connectWith(ctx, url, tlsVerify); err == nil || !strings.Contains(err.Error(), "would weaken") {
		t.Fatalf("channel_binding=disable: %v", err)
	}
}
