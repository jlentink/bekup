package db

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db/migrations"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/logging"
)

func TestPoolConfig(t *testing.T) {
	reg := logging.NewRegistry()
	cfg, err := poolConfig([]byte("postgres://bekup_app:s3cret-pass@db:5432/bekup?sslmode=verify-full"), PoolOptions{
		MaxConns: 7, MinConns: 9, StatementTimeout: 2 * time.Second, ApplicationName: "bekup-serve", Registry: reg,
		TLS: TLSOptions{Mode: config.DBTLSVerifyFull, RootCAs: x509.NewCertPool()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConns != 7 || cfg.MinConns != 7 {
		t.Errorf("conns = %d/%d", cfg.MaxConns, cfg.MinConns)
	}
	rp := cfg.ConnConfig.RuntimeParams
	want := map[string]string{
		"application_name":                    "bekup-serve",
		"statement_timeout":                   "2000",
		"idle_in_transaction_session_timeout": "60000",
		"default_transaction_isolation":       "read committed",
	}
	for k, v := range want {
		if rp[k] != v {
			t.Errorf("%s = %q, want %q", k, rp[k], v)
		}
	}
	if cfg.MaxConnLifetime != time.Hour || cfg.ConnConfig.ConnectTimeout != 10*time.Second {
		t.Errorf("defaults not applied: %v %v", cfg.MaxConnLifetime, cfg.ConnConfig.ConnectTimeout)
	}
	if got := reg.Scrub("dsn postgres://bekup_app:s3cret-pass@db:5432/bekup?sslmode=verify-full pw s3cret-pass"); strings.Contains(got, "s3cret") {
		t.Errorf("URL/password not registered for redaction: %s", got)
	}
}

func TestPoolConfigInvalidURLDoesNotLeak(t *testing.T) {
	for _, u := range []string{
		"postgres://user:hunter2-secret@host:notaport/db",
		"postgres://user:hunter2-secret@[::1/db",
		"host=x password=hunter2-secret port=abc",
	} {
		_, err := poolConfig([]byte(u), PoolOptions{Registry: logging.NewRegistry(), TLS: TLSOptions{Mode: config.DBTLSDisable}})
		if !errors.Is(err, ErrInvalidURL) || strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%q: %v", u, err)
		}
	}
}

func TestValidateRole(t *testing.T) {
	type row = sqlc.RoleSecurityCheckRow
	tests := []struct {
		name string
		row  row
		want []string // empty: valid
	}{
		{"plain role", row{RoleName: "bekup_owner"}, nil},
		{"superuser", row{RoleName: "bekup_owner", IsSuperuser: true}, []string{"SUPERUSER"}},
		{"bypassrls", row{RoleName: "bekup_owner", BypassRls: true}, []string{"BYPASSRLS"}},
		{"createrole", row{RoleName: "bekup_app", CreateRole: true}, []string{"CREATEROLE"}},
		{"replication", row{RoleName: "bekup_app", Replication: true}, []string{"REPLICATION"}},
		{"several", row{RoleName: "bekup_app", IsSuperuser: true, Replication: true}, []string{"REPLICATION, SUPERUSER"}},
		{"memberships", row{RoleName: "bekup_app", DangerousMemberships: []string{"bekup_owner", "pg_signal_backend"}},
			[]string{"member of bekup_owner, pg_signal_backend"}},
	}
	for _, tt := range tests {
		err := validateRole(tt.row)
		if (err == nil) != (len(tt.want) == 0) {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		for _, w := range tt.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: %v does not contain %q", tt.name, err, w)
			}
		}
	}
}

func TestMigrationFiles(t *testing.T) {
	name := regexp.MustCompile(`^\d{5}_[a-z][a-z0-9_]*\.sql$`)
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 4 {
		t.Fatalf("only %d migrations embedded", len(entries))
	}
	for i, e := range entries {
		if !name.MatchString(e.Name()) {
			t.Errorf("%s: name must match NNNNN_description.sql", e.Name())
		}
		if want := fmt.Sprintf("%05d_", i+1); !strings.HasPrefix(e.Name(), want) {
			t.Errorf("%s: expected prefix %s (no gaps)", e.Name(), want)
		}
		b, err := fs.ReadFile(migrations.FS, e.Name())
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		for _, m := range []string{"-- +goose Up", "-- +goose Down"} {
			if !strings.Contains(s, m) {
				t.Errorf("%s: missing %q", e.Name(), m)
			}
		}
	}
}

// fakeTx records Commit/Rollback; the embedded nil pgx.Tx is never used.
type fakeTx struct {
	pgx.Tx
	commitErr  error
	committed  bool
	rolledBack bool
}

func (f *fakeTx) Commit(context.Context) error {
	f.committed = true
	return f.commitErr
}

func (f *fakeTx) Rollback(context.Context) error {
	if f.committed {
		return pgx.ErrTxClosed
	}
	f.rolledBack = true
	return nil
}

type fakeBeginner struct {
	tx  *fakeTx
	err error
}

func (b fakeBeginner) Begin(context.Context) (pgx.Tx, error) { return b.tx, b.err }

func TestTx(t *testing.T) {
	ctx := context.Background()
	errBoom := errors.New("boom")

	t.Run("commit", func(t *testing.T) {
		tx := &fakeTx{}
		if err := Tx(ctx, fakeBeginner{tx: tx}, func(q *sqlc.Queries) error {
			if q == nil {
				t.Fatal("nil queries")
			}
			return nil
		}); err != nil || !tx.committed || tx.rolledBack {
			t.Fatalf("err=%v %+v", err, tx)
		}
	})
	t.Run("error rolls back", func(t *testing.T) {
		tx := &fakeTx{}
		err := Tx(ctx, fakeBeginner{tx: tx}, func(*sqlc.Queries) error { return errBoom })
		if !errors.Is(err, errBoom) || tx.committed || !tx.rolledBack {
			t.Fatalf("err=%v %+v", err, tx)
		}
	})
	t.Run("panic rolls back and propagates", func(t *testing.T) {
		tx := &fakeTx{}
		defer func() {
			if recover() == nil || !tx.rolledBack || tx.committed {
				t.Fatalf("panic not handled: %+v", tx)
			}
		}()
		err := Tx(ctx, fakeBeginner{tx: tx}, func(*sqlc.Queries) error { panic("boom") })
		t.Fatalf("Tx returned %v instead of panicking", err)
	})
	t.Run("commit error", func(t *testing.T) {
		tx := &fakeTx{commitErr: errBoom}
		if err := Tx(ctx, fakeBeginner{tx: tx}, func(*sqlc.Queries) error { return nil }); !errors.Is(err, errBoom) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("begin error", func(t *testing.T) {
		if err := Tx(ctx, fakeBeginner{err: errBoom}, func(*sqlc.Queries) error {
			t.Fatal("fn called")
			return nil
		}); !errors.Is(err, errBoom) {
			t.Fatalf("err=%v", err)
		}
	})
}
