package engine

import (
	"errors"
	"slices"
	"testing"
)

func mustEngine(t *testing.T, k Kind) Engine {
	t.Helper()
	e, err := ForKind(k)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func myTarget() Target {
	return Target{Host: "db.internal", Port: 3306, User: "backup"}
}

func myConn(bin string) []string {
	return []string{bin, "--defaults-file=/tmp/bekup/my.cnf", "--protocol=TCP", "--host=db.internal", "--port=3306", "--user=backup"}
}

var dumpFlags = []string{"--single-transaction", "--routines", "--triggers", "--events", "--hex-blob", "--no-tablespaces", "--default-character-set=utf8mb4"}

func concat(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// TestMySQLFamilyBuilders checks the golden argv of every builder for every
// MySQL and MariaDB catalog version.
func TestMySQLFamilyBuilders(t *testing.T) {
	type family struct {
		kind               Kind
		dumpBin, clientBin string
		tls, dumpExtra     []string
		safety             []string
	}
	fams := []family{
		{
			kind: KindMySQL, dumpBin: "mysqldump", clientBin: "mysql",
			tls:       []string{"--ssl-mode=VERIFY_IDENTITY", "--ssl-ca=/tmp/bekup/ca.pem", "--tls-version=TLSv1.2,TLSv1.3"},
			dumpExtra: []string{"--set-gtid-purged=OFF"},
			safety:    []string{"--system-command=OFF", "--commands=OFF", "--local-infile=0"},
		},
		{
			kind: KindMariaDB, dumpBin: "mariadb-dump", clientBin: "mariadb",
			tls:    []string{"--ssl", "--ssl-ca=/tmp/bekup/ca.pem", "--ssl-verify-server-cert", "--tls-version=TLSv1.2,TLSv1.3"},
			safety: []string{"--sandbox", "--local-infile=0"},
		},
	}
	files := []File{FileMyCnf, FileCA}
	tgt := myTarget()
	for _, f := range fams {
		eng, err := ForKind(f.kind)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range mustEmbedded(t).EntriesFor(f.kind) {
			t.Run(string(f.kind)+"/"+c.Key(), func(t *testing.T) {
				img := c.Image()
				d, err := eng.Dump(c, tgt, "shop")
				if err != nil {
					t.Fatal(err)
				}
				assertCommand(t, "dump", d, img,
					concat(myConn(f.dumpBin), f.tls, dumpFlags, f.dumpExtra, []string{"--", "shop"}), files, false)

				r, err := eng.Restore(c, tgt, "shop", RestoreOptions{NoOwner: true})
				if err != nil {
					t.Fatal(err)
				}
				assertCommand(t, "restore", r, img,
					concat(myConn(f.clientBin), f.tls, f.safety, []string{"--default-character-set=utf8mb4", "--database=shop"}), files, true)

				cr, err := eng.CreateDatabase(c, tgt, "shop__bekup_restore_9")
				if err != nil {
					t.Fatal(err)
				}
				assertCommand(t, "create", cr, img,
					concat(myConn(f.clientBin), f.tls, f.safety, []string{"--execute=CREATE DATABASE `shop__bekup_restore_9`"}), files, false)

				dr, err := eng.DropDatabase(c, tgt, "shop__bekup_restore_9")
				if err != nil {
					t.Fatal(err)
				}
				assertCommand(t, "drop", dr, img,
					concat(myConn(f.clientBin), f.tls, f.safety, []string{"--execute=DROP DATABASE IF EXISTS `shop__bekup_restore_9`"}), files, false)
				ds, err := eng.DropDatabase(c, tgt, "bekup_test_0123456789ab")
				if err != nil {
					t.Fatal(err)
				}
				assertCommand(t, "drop scratch", ds, img,
					concat(myConn(f.clientBin), f.tls, f.safety, []string{"--execute=DROP DATABASE IF EXISTS `bekup_test_0123456789ab`"}), files, false)

				if _, _, err := eng.DumpGlobals(c, tgt); !errors.Is(err, ErrUnsupported) {
					t.Errorf("DumpGlobals: %v", err)
				}
				if _, err := eng.RestoreGlobals(c, tgt); !errors.Is(err, ErrUnsupported) {
					t.Errorf("RestoreGlobals: %v", err)
				}
				if _, err := eng.VerifyDump(c); !errors.Is(err, ErrUnsupported) {
					t.Errorf("VerifyDump: %v", err)
				}
				if _, err := eng.RenameSwap(c, tgt, "shop", "1"); !errors.Is(err, ErrUnsupported) {
					t.Errorf("RenameSwap: %v", err)
				}
			})
		}
	}
}

func TestMySQLFamilyTLSModes(t *testing.T) {
	cases := []struct {
		kind  Kind
		key   string
		mod   func(*Target)
		tls   []string
		files []File
	}{
		{KindMySQL, "9.7", func(t *Target) { t.TLS = TLSUnverified }, []string{"--ssl-mode=REQUIRED", "--tls-version=TLSv1.2,TLSv1.3"}, []File{FileMyCnf}},
		{KindMySQL, "9.7", func(t *Target) { t.TLS = TLSDisabled }, []string{"--ssl-mode=DISABLED"}, []File{FileMyCnf}},
		{KindMySQL, "9.7", func(t *Target) { t.ClientCert = true },
			[]string{"--ssl-mode=VERIFY_IDENTITY", "--ssl-ca=/tmp/bekup/ca.pem", "--tls-version=TLSv1.2,TLSv1.3", "--ssl-cert=/tmp/bekup/client-cert.pem", "--ssl-key=/tmp/bekup/client-key.pem"},
			[]File{FileMyCnf, FileCA, FileClientCert, FileClientKey}},
		{KindMariaDB, "11.8", func(t *Target) { t.TLS = TLSUnverified }, []string{"--ssl", "--skip-ssl-verify-server-cert", "--tls-version=TLSv1.2,TLSv1.3"}, []File{FileMyCnf}},
		{KindMariaDB, "11.8", func(t *Target) { t.TLS = TLSDisabled }, []string{"--skip-ssl"}, []File{FileMyCnf}},
		{KindMariaDB, "11.8", func(t *Target) { t.TLS = TLSUnverified; t.ClientCert = true },
			[]string{"--ssl", "--skip-ssl-verify-server-cert", "--tls-version=TLSv1.2,TLSv1.3", "--ssl-cert=/tmp/bekup/client-cert.pem", "--ssl-key=/tmp/bekup/client-key.pem"},
			[]File{FileMyCnf, FileClientCert, FileClientKey}},
	}
	for _, tt := range cases {
		eng := mustEngine(t, tt.kind)
		c, _ := mustEmbedded(t).Lookup(tt.kind, tt.key)
		tgt := myTarget()
		tt.mod(&tgt)
		d, err := eng.Dump(c, tgt, "shop")
		if err != nil {
			t.Fatal(err)
		}
		bin := "mysqldump"
		if tt.kind == KindMariaDB {
			bin = "mariadb-dump"
		}
		want := concat(myConn(bin), tt.tls)
		if !slices.Equal(d.Argv[:len(want)], want) {
			t.Errorf("%s %v:\n got  %q\n want %q...", tt.kind, tgt.TLS, d.Argv, want)
		}
		if !slices.Equal(d.Files, tt.files) {
			t.Errorf("%s %v: files %v, want %v", tt.kind, tgt.TLS, d.Files, tt.files)
		}
	}
}

func TestMySQLFamilyRejects(t *testing.T) {
	cl := mustEmbedded(t)
	pg, _ := cl.Lookup(KindPostgres, "18")
	for _, k := range []Kind{KindMySQL, KindMariaDB} {
		eng := mustEngine(t, k)
		c := cl.EntriesFor(k)[0]
		other := KindMariaDB
		if k == KindMariaDB {
			other = KindMySQL
		}
		wrong := cl.EntriesFor(other)[0]
		tgt := myTarget()
		for _, bad := range hostileNames {
			if _, err := eng.Dump(c, tgt, bad); !errors.Is(err, ErrInvalidName) {
				t.Errorf("%s Dump(%q) err = %v", k, bad, err)
			}
			if _, err := eng.Restore(c, tgt, bad, RestoreOptions{}); !errors.Is(err, ErrInvalidName) {
				t.Errorf("%s Restore(%q) err = %v", k, bad, err)
			}
			if _, err := eng.CreateDatabase(c, tgt, bad); !errors.Is(err, ErrInvalidName) {
				t.Errorf("%s CreateDatabase(%q) err = %v", k, bad, err)
			}
		}
		for _, sys := range []string{"mysql", "sys", "information_schema", "performance_schema"} {
			if _, err := eng.Restore(c, tgt, sys, RestoreOptions{}); !errors.Is(err, ErrSystemDatabase) {
				t.Errorf("%s Restore(%q) err = %v", k, sys, err)
			}
			if _, err := eng.CreateDatabase(c, tgt, sys); !errors.Is(err, ErrSystemDatabase) {
				t.Errorf("%s CreateDatabase(%q) err = %v", k, sys, err)
			}
			if _, err := eng.DropDatabase(c, tgt, sys); !errors.Is(err, ErrNotBekupDatabase) {
				t.Errorf("%s DropDatabase(%q) err = %v", k, sys, err)
			}
		}
		if _, err := eng.DropDatabase(c, tgt, "shop"); !errors.Is(err, ErrNotBekupDatabase) {
			t.Errorf("%s DropDatabase(shop): %v", k, err)
		}
		// MySQL and MariaDB never share entries; PG entries never fit.
		for _, e := range []Entry{wrong, pg, {}} {
			if _, err := eng.Dump(e, tgt, "shop"); !errors.Is(err, ErrInvalidEntry) {
				t.Errorf("%s Dump with %s entry: %v", k, e.Kind(), err)
			}
			if _, err := eng.Restore(e, tgt, "shop", RestoreOptions{}); !errors.Is(err, ErrInvalidEntry) {
				t.Errorf("%s Restore with %s entry: %v", k, e.Kind(), err)
			}
		}
		bad := tgt
		bad.Host = "-h"
		if _, err := eng.Dump(c, bad, "shop"); !errors.Is(err, ErrInvalidTarget) {
			t.Errorf("%s bad target: %v", k, err)
		}
	}
}

func TestMySQLFamilyProbe(t *testing.T) {
	my := mustEngine(t, KindMySQL)
	ma := mustEngine(t, KindMariaDB)
	if _, err := my.ParseServerVersion("11.4.2-MariaDB"); !errors.Is(err, ErrWrongEngine) {
		t.Errorf("mysql engine accepted MariaDB: %v", err)
	}
	if _, err := ma.ParseServerVersion("8.4.2"); !errors.Is(err, ErrWrongEngine) {
		t.Errorf("mariadb engine accepted MySQL: %v", err)
	}
	if v, err := ma.ParseServerVersion("5.5.5-11.4.2-MariaDB"); err != nil || v != (Version{11, 4, 2}) {
		t.Errorf("mariadb version: %v %v", v, err)
	}
	if v, err := my.ParseServerVersion("8.4.2"); err != nil || v != (Version{8, 4, 2}) {
		t.Errorf("mysql version: %v %v", v, err)
	}
	if !my.IsSystemDatabase("sys") || my.IsSystemDatabase("shop") || my.VersionQuery() != "SELECT VERSION()" || my.ListDatabasesQuery() != "SHOW DATABASES" {
		t.Error("probe helpers")
	}
	s := ma.SystemDatabases()
	s[0] = "x"
	if ma.SystemDatabases()[0] == "x" {
		t.Error("SystemDatabases must return a copy")
	}
}

func TestDumpTrailerOK(t *testing.T) {
	ok := []string{
		"INSERT INTO t VALUES (1);\n/*!40101 SET SQL_MODE=@OLD_SQL_MODE */;\n\n-- Dump completed on 2026-10-27 12:00:00\n",
		"-- Dump completed on 2026-10-27  3:00:01",
		"x\n-- Dump completed\r\n",
	}
	for _, s := range ok {
		if !DumpTrailerOK([]byte(s)) {
			t.Errorf("DumpTrailerOK(%q) = false", s)
		}
	}
	bad := []string{
		"", "INSERT INTO t VALUES (1);\n", "-- Dump completed on 2026\nINSERT INTO t VALUES (2);\n",
		"INSERT INTO t VALUES ('-- Dump completed');", "x -- Dump completed\n",
	}
	for _, s := range bad {
		if DumpTrailerOK([]byte(s)) {
			t.Errorf("DumpTrailerOK(%q) = true", s)
		}
	}
}
