//go:build integration

package runner

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/runnerpb"
)

// SECURITY DEFINER escalation after a --no-owner restore (M4 review): a
// dump carries esc(), SECURITY DEFINER, which changes the password of the
// role it runs as. Restored with --no-owner by a non-superuser restore
// role, esc() belongs to that role, so any role that may connect and call
// it changes the restore role's password. NeutralizeRestoredDatabase
// switches it to SECURITY INVOKER and PUBLIC loses CONNECT (already at
// CREATE DATABASE): the low-privileged role can no longer connect, and
// even with CONNECT granted esc() runs as the caller and fails.
func (in *itInner) testSecdef(t *testing.T) {
	for _, name := range []string{"pg13", "pg16", "pg18"} {
		t.Run(name, func(t *testing.T) { in.testSecdefOn(t, name) })
	}
}

const secdefSource = `CREATE TABLE public.t (id int); INSERT INTO public.t VALUES (1);
CREATE FUNCTION public.esc() RETURNS void LANGUAGE plpgsql SECURITY DEFINER AS $$BEGIN ALTER ROLE CURRENT_USER PASSWORD 'pwned'; END$$;
CREATE PROCEDURE public.escp() LANGUAGE plpgsql SECURITY DEFINER AS $$BEGIN PERFORM 1; END$$;
CREATE FUNCTION public.whoami() RETURNS text LANGUAGE sql SECURITY DEFINER AS $$SELECT current_user::text$$;
CREATE SCHEMA "odd ""schema";
CREATE FUNCTION "odd ""schema"."f;x"(a integer, b text[]) RETURNS int LANGUAGE sql SECURITY DEFINER AS $$SELECT 1$$;
CREATE FUNCTION public.plain() RETURNS int LANGUAGE sql AS $$SELECT 1$$`

func (in *itInner) testSecdefOn(t *testing.T, name string) {
	tg := in.h.Targets[name]
	pw := strings.ReplaceAll(in.h.Password, "'", "")
	in.sql(t, name, "postgres", "CREATE ROLE sd_restorer LOGIN CREATEDB PASSWORD '"+pw+"'; CREATE ROLE sd_low LOGIN; "+
		"GRANT CONNECT ON DATABASE postgres TO sd_restorer")
	in.sql(t, name, "postgres", "CREATE DATABASE secsrc")
	in.sql(t, name, "secsrc", secdefSource)
	d, err := in.dump(t, in.target(name), "secsrc")
	if err != nil || d.result.GetExitCode() != 0 {
		t.Fatalf("dump: %v %v", d.result, err)
	}
	rt := in.target(name)
	rt.User = "sd_restorer"
	passwordOf := func() string {
		return in.sql(t, name, "postgres", "SELECT rolpassword FROM pg_authid WHERE rolname = 'sd_restorer'")
	}
	asLow := func(db, stmt string) (string, int) {
		return itExecCode(t, in.dc, tg.Container, "psql", "-U", "sd_low", "-d", db, "-At", "-v", "ON_ERROR_STOP=1", "-c", stmt)
	}
	restoreInto := func(db string) {
		t.Helper()
		if err := in.create(t, rt, db); err != nil {
			t.Fatalf("create %s: %v", db, err)
		}
		r, logs, err := in.restoreHeader(t, &runnerpb.RestoreHeader{
			Ctx: in.rc(), Target: rt, Database: db, SourceVersionKey: tg.Key, Kind: runnerpb.DumpKind_DUMP_KIND_DATABASE,
			NoOwner: true, TotalBytes: uint64(len(d.data)),
		}, d.data)
		if err != nil || r.GetExitCode() != 0 {
			t.Fatalf("restore %s: %v %v %v", db, r, err, logs)
		}
	}
	original := passwordOf()

	// Control: without the neutralization (and with CONNECT granted the
	// way PUBLIC had it before), the low role takes over the restore role.
	restoreInto("secctl")
	if v := in.sql(t, name, "postgres", "SELECT has_database_privilege('public', 'secctl', 'CONNECT')"); v != "f" {
		t.Fatalf("PUBLIC can connect to a database bekup created: %s", v)
	}
	in.sql(t, name, "postgres", "GRANT CONNECT ON DATABASE secctl TO sd_low")
	if out, code := asLow("secctl", "SELECT public.whoami()"); code != 0 || strings.TrimSpace(out) != "sd_restorer" {
		t.Fatalf("control: whoami() = %d %q", code, out)
	}
	if out, code := asLow("secctl", "SELECT public.esc()"); code != 0 {
		t.Fatalf("control: esc() failed: %s", out)
	}
	if passwordOf() == original {
		t.Fatal("control: the attack did not change the restore role's password")
	}
	in.sql(t, name, "postgres", "ALTER ROLE sd_restorer PASSWORD '"+pw+"'")
	original = passwordOf()

	// New database: neutralized.
	restoreInto("secnew")
	resp, err := in.rpc.NeutralizeRestoredDatabase(in.ctx(t, 5*time.Minute), &runnerpb.NeutralizeRestoredDatabaseRequest{
		Ctx: in.rc(), Target: rt, Database: "secnew", Kind: runnerpb.NeutralizeKind_NEUTRALIZE_KIND_NEW_DATABASE})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetNeutralized() != 4 || resp.GetDatabase() != "secnew" || !slices.Contains(resp.GetFunctions(), "public.esc()") ||
		!slices.Contains(resp.GetFunctions(), "public.escp()") || !slices.Contains(resp.GetFunctions(), `"odd ""schema"."f;x"(integer,text[])`) {
		t.Fatalf("response %v", resp)
	}
	if n := in.sql(t, name, "secnew", "SELECT count(*) FROM pg_proc WHERE prosecdef"); n != "0" {
		t.Fatalf("%s SECURITY DEFINER functions left", n)
	}
	if out, code := asLow("secnew", "SELECT 1"); code == 0 || !strings.Contains(out, "permission denied") {
		t.Fatalf("the low role connected: %d %s", code, out)
	}
	// Even with CONNECT granted, the functions now run as the caller:
	// esc() can only change the caller's own password.
	in.sql(t, name, "postgres", "GRANT CONNECT ON DATABASE secnew TO sd_low")
	if out, code := asLow("secnew", "SELECT public.whoami()"); code != 0 || strings.TrimSpace(out) != "sd_low" {
		t.Fatalf("whoami() after the neutralization = %d %q", code, out)
	}
	if out, code := asLow("secnew", "SELECT public.esc()"); code != 0 {
		t.Fatalf("esc() as the caller: %s", out)
	}
	if passwordOf() != original {
		t.Fatal("the restore role's password changed after the neutralization")
	}
	if in.sql(t, name, "postgres", "SELECT rolpassword IS NOT NULL FROM pg_authid WHERE rolname = 'sd_low'") != "t" {
		t.Fatal("esc() did not run as the caller")
	}
	if out, code := asLow("secnew", "SELECT public.plain()"); code != 0 || out == "" {
		t.Fatalf("plain functions still work: %d %s", code, out)
	}
	// Idempotent: nothing left to switch.
	resp, err = in.rpc.NeutralizeRestoredDatabase(in.ctx(t, 5*time.Minute), &runnerpb.NeutralizeRestoredDatabaseRequest{
		Ctx: in.rc(), Target: rt, Database: "secnew", Kind: runnerpb.NeutralizeKind_NEUTRALIZE_KIND_NEW_DATABASE})
	if err != nil || resp.GetNeutralized() != 0 {
		t.Fatalf("second run: %v %v", resp, err)
	}

	// Staging database of an overwrite (derived name).
	restoreInto("secapp__bekup_restore_n1")
	resp, err = in.rpc.NeutralizeRestoredDatabase(in.ctx(t, 5*time.Minute), &runnerpb.NeutralizeRestoredDatabaseRequest{
		Ctx: in.rc(), Target: rt, Database: "secapp", TempId: "n1", Kind: runnerpb.NeutralizeKind_NEUTRALIZE_KIND_RESTORE_STAGING})
	if err != nil || resp.GetNeutralized() != 4 || resp.GetDatabase() != "secapp__bekup_restore_n1" {
		t.Fatalf("staging: %v %v", resp, err)
	}

	// A database the restore role does not own is refused untouched.
	_, err = in.rpc.NeutralizeRestoredDatabase(in.ctx(t, 5*time.Minute), &runnerpb.NeutralizeRestoredDatabaseRequest{
		Ctx: in.rc(), Target: rt, Database: "secsrc", Kind: runnerpb.NeutralizeKind_NEUTRALIZE_KIND_NEW_DATABASE})
	itWantReason(t, err, runnerpb.ReasonPermission)
	if n := in.sql(t, name, "secsrc", "SELECT count(*) FROM pg_proc WHERE prosecdef"); n != "4" {
		t.Fatalf("secsrc changed: %s SECURITY DEFINER functions", n)
	}

	for _, db := range []string{"secsrc", "secctl", "secnew", "secapp__bekup_restore_n1"} {
		in.sql(t, name, "postgres", "DROP DATABASE "+db)
	}
	in.sql(t, name, "postgres", "REVOKE CONNECT ON DATABASE postgres FROM sd_restorer; DROP ROLE sd_restorer; DROP ROLE sd_low")
}
