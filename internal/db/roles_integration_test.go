//go:build integration

package db_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/logging"
)

// privileges returns the privilegeSnapshot of dbname (as the superuser).
func privileges(ctx context.Context, dbname string) (out string, err error) {
	conn, err := pgx.Connect(ctx, urlFor(superUser, superPassword, dbname))
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, conn.Close(ctx)) }()
	err = conn.QueryRow(ctx, privilegeSnapshot).Scan(&out)
	return out, err
}

// privilegeSnapshot renders every privilege migration 00001 touches: the
// database ACL, the public schema owner and ACL, and all default privileges.
const privilegeSnapshot = `
SELECT concat_ws(E'\n',
    'datacl=' || coalesce((SELECT datacl::text FROM pg_catalog.pg_database WHERE datname = current_database()), 'NULL'),
    'nspowner=' || (SELECT nspowner::regrole::text FROM pg_catalog.pg_namespace WHERE nspname = 'public'),
    'nspacl=' || coalesce((SELECT nspacl::text FROM pg_catalog.pg_namespace WHERE nspname = 'public'), 'NULL'),
    'defacl=' || coalesce((
        SELECT string_agg(concat_ws(':', defaclrole::regrole::text, defaclnamespace::regnamespace::text,
                                    defaclobjtype::text, defaclacl::text), ';' ORDER BY 1)
        FROM pg_catalog.pg_default_acl), ''))`

// TestDownRestoresInitScriptState checks that migrating up and all the way
// down leaves exactly the privileges the deploy init script created (no extra
// grants to PUBLIC), so up-down-up is faithful.
func TestDownRestoresInitScriptState(t *testing.T) {
	ctx := withTimeout(t, 2*time.Minute)
	d := newTestDB(t)
	before, err := privileges(ctx, d.name)
	if err != nil {
		t.Fatal(err)
	}
	// PUBLIC entries in an ACL have an empty grantee: "{=..." or ",=...".
	if strings.Contains(before, "{=") || strings.Contains(before, ",=") {
		t.Fatalf("init script state grants to PUBLIC:\n%s", before)
	}
	m := d.migrator(t)
	if _, err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}
	during, err := privileges(ctx, d.name)
	if err != nil {
		t.Fatal(err)
	}
	if during == before {
		t.Fatal("migration 00001 changed no privileges; the snapshot is not meaningful")
	}
	if _, err := m.DownTo(ctx, 0); err != nil {
		t.Fatal(err)
	}
	after, err := privileges(ctx, d.name)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("privileges after down differ from the init script state\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if _, err := m.Up(ctx); err != nil {
		t.Fatalf("up after down: %v", err)
	}
	again, err := privileges(ctx, d.name)
	if err != nil {
		t.Fatal(err)
	}
	if again != during {
		t.Fatalf("privileges after up-down-up differ\nfirst up:\n%s\nsecond up:\n%s", during, again)
	}
}

// TestInitScriptRejectsBootstrapSuperuserOverTCP checks the pg_hba rule of
// the init script: the bootstrap superuser only works on the local socket.
func TestInitScriptRejectsBootstrapSuperuserOverTCP(t *testing.T) {
	ctx := withTimeout(t, time.Minute)
	conn, err := pgx.Connect(ctx, urlFor("postgres", "postgres", "postgres"))
	if err == nil {
		t.Fatal(errors.Join(errors.New("bootstrap superuser connected over TCP"), conn.Close(ctx)))
	}
	if !strings.Contains(err.Error(), "pg_hba.conf rejects connection") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// weaken applies a superuser change for the duration of a subtest and
// reverts it afterwards (roles are shared by every test database).
func weaken(ctx context.Context, t *testing.T, apply, revert []string) {
	t.Helper()
	if err := superExec(ctx, "postgres", apply...); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := superExec(context.WithoutCancel(ctx), "postgres", revert...); err != nil {
			t.Errorf("revert: %v", err)
		}
	})
}

var weakenings = []struct {
	name          string
	apply, revert []string
	want          string
	affectsApp    bool
}{
	{"owner superuser", []string{"ALTER ROLE bekup_owner SUPERUSER"}, []string{"ALTER ROLE bekup_owner NOSUPERUSER"}, "bekup_owner must not have SUPERUSER", false},
	{"owner createrole", []string{"ALTER ROLE bekup_owner CREATEROLE"}, []string{"ALTER ROLE bekup_owner NOCREATEROLE"}, "bekup_owner must not have CREATEROLE", false},
	{"app bypassrls", []string{"ALTER ROLE bekup_app BYPASSRLS"}, []string{"ALTER ROLE bekup_app NOBYPASSRLS"}, "bekup_app must not have BYPASSRLS", true},
	{"app replication", []string{"ALTER ROLE bekup_app REPLICATION"}, []string{"ALTER ROLE bekup_app NOREPLICATION"}, "bekup_app must not have REPLICATION", true},
	{"app member of owner", []string{"GRANT bekup_owner TO bekup_app"}, []string{"REVOKE bekup_owner FROM bekup_app"}, "bekup_app must not be a member of bekup_owner", true},
	{"app pg_signal_backend", []string{"GRANT pg_signal_backend TO bekup_app"}, []string{"REVOKE pg_signal_backend FROM bekup_app"}, "member of pg_signal_backend", true},
	{"app pg_write_all_data", []string{"GRANT pg_write_all_data TO bekup_app"}, []string{"REVOKE pg_write_all_data FROM bekup_app"}, "member of pg_write_all_data", true},
	{"app pg_read_all_data", []string{"GRANT pg_read_all_data TO bekup_app"}, []string{"REVOKE pg_read_all_data FROM bekup_app"}, "member of pg_read_all_data", true},
	{"owner pg_execute_server_program", []string{"GRANT pg_execute_server_program TO bekup_owner"}, []string{"REVOKE pg_execute_server_program FROM bekup_owner"}, "bekup_owner must not be a member of pg_execute_server_program", false},
	{"owner pg_read_server_files", []string{"GRANT pg_read_server_files TO bekup_owner"}, []string{"REVOKE pg_read_server_files FROM bekup_owner"}, "member of pg_read_server_files", false},
	{"app pg_monitor (transitive pg_read_all_settings)", []string{"GRANT pg_monitor TO bekup_app"}, []string{"REVOKE pg_monitor FROM bekup_app"}, "pg_read_all_settings", true},
	{"app via intermediate superuser role", []string{
		"CREATE ROLE bekup_test_admin NOLOGIN SUPERUSER", "CREATE ROLE bekup_test_group NOLOGIN",
		"GRANT bekup_test_admin TO bekup_test_group", "GRANT bekup_test_group TO bekup_app",
	}, []string{"DROP ROLE bekup_test_group", "DROP ROLE bekup_test_admin"}, "member of bekup_test_admin", true},
}

// TestMigratorChecksRolesOnEveryRun weakens a role after the first apply;
// every later migrate run must refuse to start.
func TestMigratorChecksRolesOnEveryRun(t *testing.T) {
	ctx := withTimeout(t, 2*time.Minute)
	d := migratedDB(t)
	for _, w := range weakenings {
		t.Run(w.name, func(t *testing.T) {
			weaken(ctx, t, w.apply, w.revert)
			m, err := db.NewMigrator(ctx, []byte(d.ownerURL), tlsVerify, logging.NewRegistry())
			if err == nil {
				t.Fatal(errors.Join(errors.New("migrator accepted a weakened role"), m.Close(ctx)))
			}
			if !strings.Contains(err.Error(), w.want) {
				t.Fatalf("got %v, want %q", err, w.want)
			}
		})
	}
	// After every revert, migrating works again.
	m, err := db.NewMigrator(ctx, []byte(d.ownerURL), tlsVerify, logging.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestCheckAppRole(t *testing.T) {
	ctx := withTimeout(t, 2*time.Minute)
	d := migratedDB(t)
	app := d.appPool(t)
	if err := db.CheckAppRole(ctx, app); err != nil {
		t.Fatalf("plain bekup_app rejected: %v", err)
	}
	if err := db.CheckAppRole(ctx, d.ownerPool(t)); err == nil || !strings.Contains(err.Error(), "must connect as bekup_app") {
		t.Fatalf("owner connection: %v", err)
	}
	if err := db.CheckAppRole(ctx, d.pool(t, urlFor(superUser, superPassword, d.name))); err == nil || !strings.Contains(err.Error(), "must connect as bekup_app") {
		t.Fatalf("superuser connection: %v", err)
	}
	for _, w := range weakenings {
		if !w.affectsApp {
			continue
		}
		t.Run(w.name, func(t *testing.T) {
			weaken(ctx, t, w.apply, w.revert)
			err := db.CheckAppRole(ctx, app)
			if err == nil || !strings.Contains(err.Error(), w.want) {
				t.Fatalf("got %v, want %q", err, w.want)
			}
		})
	}
}
