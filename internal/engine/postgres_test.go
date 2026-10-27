package engine

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func pgTarget() Target {
	return Target{Host: "db.internal", Port: 5432, User: "backup", ServerVersion: Version{17, 2, 0}}
}

const (
	pgCIPinned = `dbname='shop' host='db.internal' port='5432' user='backup' passfile='/tmp/bekup/pgpass' ` +
		`sslmode='verify-full' sslrootcert='/tmp/bekup/ca.pem' ssl_min_protocol_version='TLSv1.2' ` +
		`gssencmode='disable' application_name='bekup' connect_timeout='10'`
	pgCIMaint = `dbname='postgres' host='db.internal' port='5432' user='backup' passfile='/tmp/bekup/pgpass' ` +
		`sslmode='verify-full' sslrootcert='/tmp/bekup/ca.pem' ssl_min_protocol_version='TLSv1.2' ` +
		`gssencmode='disable' application_name='bekup' connect_timeout='10'`
)

func pgEngine(t *testing.T) Engine {
	t.Helper()
	e, err := ForKind(KindPostgres)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func assertCommand(t *testing.T, name string, got Command, image string, argv []string, files []File, stdin bool) {
	t.Helper()
	if got.Image != image {
		t.Errorf("%s: image = %q, want %q", name, got.Image, image)
	}
	if !slices.Equal(got.Argv, argv) {
		t.Errorf("%s: argv\n got  %q\n want %q", name, got.Argv, argv)
	}
	if !slices.Equal(got.Files, files) {
		t.Errorf("%s: files = %v, want %v", name, got.Files, files)
	}
	if got.Stdin != stdin {
		t.Errorf("%s: stdin = %v, want %v", name, got.Stdin, stdin)
	}
	// Every psql run starts its stdin with a fresh \restrict line; no
	// other tool gets a prefix.
	if isPSQL := len(got.Argv) > 0 && got.Argv[0] == "psql"; isPSQL != IsPSQLRestrictPrefix(got.StdinPrefix) ||
		(!isPSQL && got.StdinPrefix != nil) {
		t.Errorf("%s: stdin prefix %q for argv[0] %q", name, got.StdinPrefix, got.Argv[0])
	}
}

// TestPostgresBuilders checks the golden argv of every builder for every
// PostgreSQL catalog version.
func TestPostgresBuilders(t *testing.T) {
	eng := pgEngine(t)
	tgt := pgTarget()
	files := []File{FilePGPass, FileCA}
	psql := []string{"psql", "--no-psqlrc", "--set=ON_ERROR_STOP=1", "--no-password", "--quiet", "--dbname=" + pgCIMaint, "--file=-"}
	for _, c := range mustEmbedded(t).EntriesFor(KindPostgres) {
		t.Run(c.Key(), func(t *testing.T) {
			img := c.Image()
			cmd, err := eng.Dump(c, tgt, "shop")
			if err != nil {
				t.Fatal(err)
			}
			assertCommand(t, "dump", cmd, img,
				[]string{"pg_dump", "--format=custom", "--compress=0", "--no-password", "--dbname=" + pgCIPinned}, files, false)

			p, fb, err := eng.DumpGlobals(c, tgt)
			if err != nil {
				t.Fatal(err)
			}
			g := []string{"pg_dumpall", "--globals-only", "--no-password", "--database=postgres", "--dbname=" + pgCIMaint}
			assertCommand(t, "globals", p, img, g, files, false)
			assertCommand(t, "globals fallback", fb, img, append(slices.Clone(g), "--no-role-passwords"), files, false)

			v, err := eng.VerifyDump(c)
			if err != nil {
				t.Fatal(err)
			}
			assertCommand(t, "verify", v, img, []string{"pg_restore", "--format=custom", "--file=/dev/null"}, nil, true)

			r, err := eng.Restore(c, tgt, "shop", RestoreOptions{NoOwner: true})
			if err != nil {
				t.Fatal(err)
			}
			assertCommand(t, "restore", r, img,
				[]string{"pg_restore", "--exit-on-error", "--no-password", "--format=custom", "--no-owner", "--dbname=" + pgCIPinned}, files, true)
			r, err = eng.Restore(c, tgt, "shop", RestoreOptions{})
			if err != nil || slices.Contains(r.Argv, "--no-owner") {
				t.Errorf("restore without NoOwner: %q %v", r.Argv, err)
			}

			rg, err := eng.RestoreGlobals(c, tgt)
			if err != nil {
				t.Fatal(err)
			}
			assertCommand(t, "restore globals", rg, img, slices.Clone(psql), files, true)

			cr, err := eng.CreateDatabase(c, tgt, "shop__bekup_restore_7")
			if err != nil {
				t.Fatal(err)
			}
			assertCommand(t, "create", cr, img,
				append(slices.Clone(psql), `--command=CREATE DATABASE "shop__bekup_restore_7" TEMPLATE template0`), files, false)

			sw, err := eng.RenameSwap(c, tgt, "shop", "7")
			if err != nil {
				t.Fatal(err)
			}
			term := `SELECT pg_catalog.pg_terminate_backend(pid) FROM pg_catalog.pg_stat_activity WHERE datname = 'shop' AND pid <> pg_catalog.pg_backend_pid()`
			assertCommand(t, "swap prepare", sw.Prepare, img,
				append(slices.Clone(psql), `--command=ALTER DATABASE "shop" WITH ALLOW_CONNECTIONS false`, "--command="+term), files, false)
			assertCommand(t, "swap", sw.Swap, img,
				append(slices.Clone(psql), `--command=BEGIN; `+term+`; ALTER DATABASE "shop" RENAME TO "shop__bekup_old_7"; ALTER DATABASE "shop__bekup_restore_7" RENAME TO "shop"; COMMIT`), files, false)
			assertCommand(t, "swap rollback", sw.Rollback, img,
				append(slices.Clone(psql), `--command=ALTER DATABASE "shop" WITH ALLOW_CONNECTIONS true`), files, false)
			if sw.OldName != "shop__bekup_old_7" {
				t.Errorf("old name = %q", sw.OldName)
			}

			d, err := eng.DropDatabase(c, tgt, "shop__bekup_old_7")
			if err != nil {
				t.Fatal(err)
			}
			assertCommand(t, "drop", d, img,
				append(slices.Clone(psql), `--command=DROP DATABASE IF EXISTS "shop__bekup_old_7" WITH (FORCE)`), files, false)
		})
	}
}

func TestPostgresTLSModes(t *testing.T) {
	eng := pgEngine(t)
	c, _ := mustEmbedded(t).Lookup(KindPostgres, "18")
	tests := []struct {
		name  string
		tgt   func(*Target)
		has   []string
		lacks []string
		files []File
	}{
		{"pinned", func(*Target) {}, []string{"sslmode='verify-full'", "sslrootcert='/tmp/bekup/ca.pem'"}, []string{"sslcert"}, []File{FilePGPass, FileCA}},
		{"pinned client cert", func(t *Target) { t.ClientCert = true },
			[]string{"sslmode='verify-full'", "sslcert='/tmp/bekup/client-cert.pem'", "sslkey='/tmp/bekup/client-key.pem'"}, nil,
			[]File{FilePGPass, FileCA, FileClientCert, FileClientKey}},
		{"unverified", func(t *Target) { t.TLS = TLSUnverified }, []string{"sslmode='require'", "ssl_min_protocol_version"}, []string{"sslrootcert", "verify"}, []File{FilePGPass}},
		{"disabled", func(t *Target) { t.TLS = TLSDisabled }, []string{"sslmode='disable'"}, []string{"sslrootcert", "ssl_min"}, []File{FilePGPass}},
		{"maintenance db", func(t *Target) { t.MaintenanceDB = "defaultdb" }, []string{"dbname='shop'"}, nil, []File{FilePGPass, FileCA}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tgt := pgTarget()
			tt.tgt(&tgt)
			cmd, err := eng.Dump(c, tgt, "shop")
			if err != nil {
				t.Fatal(err)
			}
			ci := strings.TrimPrefix(cmd.Argv[len(cmd.Argv)-1], "--dbname=")
			for _, s := range tt.has {
				if !strings.Contains(ci, s) {
					t.Errorf("conninfo %q lacks %q", ci, s)
				}
			}
			for _, s := range tt.lacks {
				if strings.Contains(ci, s) {
					t.Errorf("conninfo %q contains %q", ci, s)
				}
			}
			if !slices.Equal(cmd.Files, tt.files) {
				t.Errorf("files = %v, want %v", cmd.Files, tt.files)
			}
		})
	}
	tgt := pgTarget()
	tgt.MaintenanceDB = "defaultdb"
	p, _, err := eng.DumpGlobals(c, tgt)
	if err != nil || !slices.Contains(p.Argv, "--database=defaultdb") {
		t.Errorf("maintenance db in globals: %q %v", p.Argv, err)
	}
}

func TestPostgresDropOldServer(t *testing.T) {
	eng := pgEngine(t)
	c, _ := mustEmbedded(t).Lookup(KindPostgres, "18")
	for _, sv := range []Version{{12, 9, 0}, {}} {
		tgt := pgTarget()
		tgt.ServerVersion = sv
		d, err := eng.DropDatabase(c, tgt, "shop__bekup_restore_1")
		if err != nil {
			t.Fatal(err)
		}
		if last := d.Argv[len(d.Argv)-1]; last != `--command=DROP DATABASE IF EXISTS "shop__bekup_restore_1"` {
			t.Errorf("server %v: %q", sv, last)
		}
	}
}

func TestPGQuoteValue(t *testing.T) {
	if got := pgQuoteValue(`a'b\c`); got != `'a\'b\\c'` {
		t.Errorf("pgQuoteValue = %s", got)
	}
}

func TestPostgresRejects(t *testing.T) {
	eng := pgEngine(t)
	cl := mustEmbedded(t)
	c, _ := cl.Lookup(KindPostgres, "18")
	my, _ := cl.Lookup(KindMySQL, "8.4")
	tgt := pgTarget()

	for _, bad := range hostileNames {
		if _, err := eng.Dump(c, tgt, bad); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Dump(%q) err = %v", bad, err)
		}
		if _, err := eng.Restore(c, tgt, bad, RestoreOptions{}); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Restore(%q) err = %v", bad, err)
		}
		if _, err := eng.CreateDatabase(c, tgt, bad); !errors.Is(err, ErrInvalidName) {
			t.Errorf("CreateDatabase(%q) err = %v", bad, err)
		}
		if _, err := eng.RenameSwap(c, tgt, bad, "1"); !errors.Is(err, ErrInvalidName) {
			t.Errorf("RenameSwap(%q) err = %v", bad, err)
		}
	}
	// Wrong or forged entries.
	if _, err := eng.Dump(my, tgt, "shop"); !errors.Is(err, ErrInvalidEntry) {
		t.Errorf("mysql entry: %v", err)
	}
	if _, err := eng.Dump(Entry{}, tgt, "shop"); !errors.Is(err, ErrInvalidEntry) {
		t.Errorf("zero entry: %v", err)
	}
	if _, err := eng.VerifyDump(my); !errors.Is(err, ErrInvalidEntry) {
		t.Errorf("verify mysql entry: %v", err)
	}
	// System and maintenance databases are never written.
	for _, db := range []string{"template0", "template1", "postgres", "rdsadmin"} {
		if _, err := eng.Restore(c, tgt, db, RestoreOptions{}); !errors.Is(err, ErrSystemDatabase) {
			t.Errorf("Restore(%q) err = %v", db, err)
		}
		if _, err := eng.CreateDatabase(c, tgt, db); !errors.Is(err, ErrSystemDatabase) {
			t.Errorf("CreateDatabase(%q) err = %v", db, err)
		}
		if _, err := eng.RenameSwap(c, tgt, db, "1"); !errors.Is(err, ErrSystemDatabase) {
			t.Errorf("RenameSwap(%q) err = %v", db, err)
		}
	}
	// Only bekup temporary databases can be dropped.
	for _, db := range []string{"shop", "postgres", "template1", "shop__bekup_restore_", "-x__bekup_old_1"} {
		if _, err := eng.DropDatabase(c, tgt, db); !errors.Is(err, ErrNotBekupDatabase) {
			t.Errorf("DropDatabase(%q) err = %v", db, err)
		}
	}
	if _, err := eng.RenameSwap(c, tgt, "shop", "BAD"); !errors.Is(err, ErrInvalidName) {
		t.Errorf("bad restore id: %v", err)
	}
	// A maintenance DB that equals the temp name is refused.
	m := tgt
	m.MaintenanceDB = "shop__bekup_restore_1"
	if _, err := eng.RenameSwap(c, m, "shop", "1"); !errors.Is(err, ErrInvalidName) {
		t.Errorf("maintenance collision: %v", err)
	}
}

func TestTargetValidate(t *testing.T) {
	bad := []func(*Target){
		func(t *Target) { t.Host = "-evil" },
		func(t *Target) { t.Host = "" },
		func(t *Target) { t.Port = 0 },
		func(t *Target) { t.User = "--user=root" },
		func(t *Target) { t.User = "" },
		func(t *Target) { t.TLS = TLSMode(9) },
		func(t *Target) { t.TLS = TLSDisabled; t.ClientCert = true },
		func(t *Target) { t.MaintenanceDB = "a=b" },
	}
	eng := pgEngine(t)
	c, _ := mustEmbedded(t).Lookup(KindPostgres, "18")
	for i, mod := range bad {
		tgt := pgTarget()
		mod(&tgt)
		if err := tgt.Validate(); !errors.Is(err, ErrInvalidTarget) {
			t.Errorf("case %d: Validate = %v", i, err)
		}
		if _, err := eng.Dump(c, tgt, "shop"); !errors.Is(err, ErrInvalidTarget) {
			t.Errorf("case %d: Dump = %v", i, err)
		}
	}
	if err := pgTarget().Validate(); err != nil {
		t.Errorf("valid target: %v", err)
	}
}

func TestTLSModeString(t *testing.T) {
	for m, want := range map[TLSMode]string{TLSPinned: "pinned", TLSUnverified: "unverified", TLSDisabled: "disabled", 7: "invalid(7)"} {
		if m.String() != want {
			t.Errorf("%d: %s", m, m.String())
		}
	}
	var zero TLSMode
	if zero != TLSPinned {
		t.Error("the zero TLS mode must be the secure default")
	}
}

func TestForKind(t *testing.T) {
	for _, k := range Kinds() {
		e, err := ForKind(k)
		if err != nil || e.Kind() != k {
			t.Errorf("ForKind(%s) = %v, %v", k, e, err)
		}
		if !k.Valid() {
			t.Errorf("%s not valid", k)
		}
	}
	if _, err := ForKind("oracle"); !errors.Is(err, ErrUnknownEngine) {
		t.Errorf("unknown: %v", err)
	}
}

func TestPostgresProbe(t *testing.T) {
	eng := pgEngine(t)
	if !eng.IsSystemDatabase("template0") || eng.IsSystemDatabase("shop") {
		t.Error("IsSystemDatabase")
	}
	sys := eng.SystemDatabases()
	sys[0] = "changed"
	if eng.SystemDatabases()[0] == "changed" {
		t.Error("SystemDatabases must return a copy")
	}
	if eng.VersionQuery() != "SHOW server_version_num" || !strings.Contains(eng.ListDatabasesQuery(), "NOT datistemplate") {
		t.Error("queries")
	}
	if v, err := eng.ParseServerVersion("170002"); err != nil || v != (Version{17, 2, 0}) {
		t.Errorf("ParseServerVersion = %v %v", v, err)
	}
}
