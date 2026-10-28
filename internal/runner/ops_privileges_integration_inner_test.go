//go:build integration

package runner

import (
	"slices"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// testPrivileges: Probe's privilege verdict of the target role (task 4.8,
// D-068) against real servers.
func (in *itInner) testPrivileges(t *testing.T) {
	t.Run("pg16", func(t *testing.T) { in.testPrivilegesPG(t, "pg16") })
	for _, name := range []string{"mysql84", "maria118"} {
		t.Run(name, func(t *testing.T) { in.testPrivilegesMy(t, name) })
	}
}

// privProbe probes name as user and returns the verdict.
func (in *itInner) privProbe(t *testing.T, name, user, pw string) *runnerpb.TargetPrivileges {
	t.Helper()
	rt := in.target(name)
	if user != "" {
		rt.User, rt.Password = user, []byte(pw)
	}
	resp, err := in.probe(t, rt)
	if err != nil {
		t.Fatalf("probe as %q: %v", user, err)
	}
	return resp.GetPrivileges()
}

func wantPrivileges(t *testing.T, got *runnerpb.TargetPrivileges, st runnerpb.PrivilegeStatus, reasons ...string) {
	t.Helper()
	if got.GetStatus() != st {
		t.Fatalf("status = %v (%q), want %v", got.GetStatus(), got.GetReasons(), st)
	}
	for _, r := range reasons {
		if !slices.Contains(got.GetReasons(), r) {
			t.Fatalf("reasons = %q, want %q", got.GetReasons(), r)
		}
	}
	if st == runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_NOT_PRIVILEGED && len(got.GetReasons()) != 0 {
		t.Fatalf("reasons without privilege: %q", got.GetReasons())
	}
}

func (in *itInner) testPrivilegesPG(t *testing.T, name string) {
	pw := strings.ReplaceAll(in.h.Password, "'", "")
	in.sql(t, name, "postgres", "CREATE ROLE privplain LOGIN CREATEDB PASSWORD '"+pw+"'; "+
		"CREATE ROLE privexec LOGIN PASSWORD '"+pw+"'; CREATE ROLE privgroup NOLOGIN; "+
		"GRANT pg_execute_server_program TO privgroup; GRANT privgroup TO privexec; "+
		"CREATE ROLE privsu NOLOGIN SUPERUSER; CREATE ROLE privviasu LOGIN PASSWORD '"+pw+"'; GRANT privsu TO privviasu; "+
		"CREATE ROLE privcr LOGIN CREATEROLE PASSWORD '"+pw+"'; "+
		"CREATE ROLE privread LOGIN PASSWORD '"+pw+"'; GRANT pg_read_all_data TO privread; "+
		"CREATE ROLE privwrite LOGIN PASSWORD '"+pw+"'; GRANT pg_write_all_data TO privwrite; "+
		"GRANT CONNECT ON DATABASE postgres TO privplain, privexec, privviasu, privcr, privread, privwrite")
	in.sql(t, name, "postgres", "CREATE DATABASE privowned OWNER privplain")

	wantPrivileges(t, in.privProbe(t, name, "", ""), runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED, "superuser")
	// Owning a database and CREATEDB are not privileged.
	wantPrivileges(t, in.privProbe(t, name, "privplain", pw), runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_NOT_PRIVILEGED)
	wantPrivileges(t, in.privProbe(t, name, "privexec", pw), runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED,
		"member_of:pg_execute_server_program")
	wantPrivileges(t, in.privProbe(t, name, "privviasu", pw), runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED,
		"member_of_role_with:superuser")
	wantPrivileges(t, in.privProbe(t, name, "privcr", pw), runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED, "createrole")
	// Reading or writing every database is gated too.
	wantPrivileges(t, in.privProbe(t, name, "privread", pw), runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED,
		"member_of:pg_read_all_data")
	wantPrivileges(t, in.privProbe(t, name, "privwrite", pw), runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED,
		"member_of:pg_write_all_data")
}

func (in *itInner) testPrivilegesMy(t *testing.T, name string) {
	pw := strings.ReplaceAll(in.h.Password, "'", "")
	super := "SUPER"
	if in.h.Targets[name].Kind == engine.KindMySQL {
		super = "SYSTEM_VARIABLES_ADMIN"
	}
	in.sql(t, name, "shop", "CREATE USER 'privplain'@'%' IDENTIFIED BY '"+pw+"'; "+
		"GRANT ALL PRIVILEGES ON `shop`.* TO 'privplain'@'%'; "+
		"GRANT SHOW DATABASES ON *.* TO 'privplain'@'%'; "+
		"CREATE USER 'privdata'@'%' IDENTIFIED BY '"+pw+"'; GRANT SELECT, INSERT, CREATE, DROP ON *.* TO 'privdata'@'%'; "+
		"CREATE USER 'privfile'@'%' IDENTIFIED BY '"+pw+"'; GRANT FILE ON *.* TO 'privfile'@'%'; "+
		"CREATE USER 'privsuper'@'%' IDENTIFIED BY '"+pw+"'; GRANT "+super+" ON *.* TO 'privsuper'@'%'; "+
		"CREATE ROLE 'privrole'; GRANT SELECT ON `shop`.* TO 'privrole'; "+
		"CREATE USER 'privroled'@'%' IDENTIFIED BY '"+pw+"'; GRANT 'privrole' TO 'privroled'@'%'")

	wantPrivileges(t, in.privProbe(t, name, "", ""), runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED)
	wantPrivileges(t, in.privProbe(t, name, "privplain", pw), runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_NOT_PRIVILEGED)
	// Global data privileges reach every schema.
	wantPrivileges(t, in.privProbe(t, name, "privdata", pw), runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED,
		"global_data:SELECT", "global_data:INSERT", "global_data:CREATE", "global_data:DROP")
	wantPrivileges(t, in.privProbe(t, name, "privfile", pw), runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED, "global:FILE")
	wantPrivileges(t, in.privProbe(t, name, "privsuper", pw), runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED, "global:"+super)
	wantPrivileges(t, in.privProbe(t, name, "privroled", pw), runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_UNKNOWN, "roles_granted")
}
