package engine

import (
	"errors"
	"strings"
	"testing"
)

// hostileNames are rejected by every name validator.
var hostileNames = []string{
	"", "-oops", "--all-databases", "a=b", "dbname=x host=evil", "postgres://evil/x",
	"a b", "a'b", `a"b`, "a`b", `a\b`, "a;b", "a\x00b", "a\nb", "a/b", "ä", "$x",
	".hidden", "a\tb", strings.Repeat("a", 64),
}

func TestValidateDBName(t *testing.T) {
	for _, ok := range []string{"app", "App_1", "_x", "a-b", "a.b", "a$b", "0db", strings.Repeat("a", 63)} {
		if err := ValidateDBName(ok); err != nil {
			t.Errorf("ValidateDBName(%q) = %v", ok, err)
		}
	}
	for _, bad := range hostileNames {
		if err := ValidateDBName(bad); !errors.Is(err, ErrInvalidName) {
			t.Errorf("ValidateDBName(%q) = %v", bad, err)
		}
	}
}

func TestValidateUser(t *testing.T) {
	for _, ok := range []string{"backup", "user@server", "a+b", "svc.backup", "x$y"} {
		if err := ValidateUser(ok); err != nil {
			t.Errorf("ValidateUser(%q) = %v", ok, err)
		}
	}
	for _, bad := range hostileNames {
		if err := ValidateUser(bad); !errors.Is(err, ErrInvalidName) {
			t.Errorf("ValidateUser(%q) = %v", bad, err)
		}
	}
}

func TestValidateHost(t *testing.T) {
	for _, ok := range []string{"db", "db.internal.example.com", "10.0.0.5", "2001:db8::1", "a-b.c", "xn--bcher-kva.example"} {
		if err := ValidateHost(ok); err != nil {
			t.Errorf("ValidateHost(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-db", "db-", "a..b", ".db", "db.", "fe80::1%eth0", "a_b", "a b", "1.2.3", "999.1.1.1",
		"db:5432", "http://db", strings.Repeat("a", 64) + ".com", strings.Repeat("a.", 127) + "aa"} {
		if err := ValidateHost(bad); !errors.Is(err, ErrInvalidName) {
			t.Errorf("ValidateHost(%q) = %v", bad, err)
		}
	}
}

func TestValidatePort(t *testing.T) {
	for _, p := range []int{1, 5432, 65535} {
		if err := ValidatePort(p); err != nil {
			t.Errorf("ValidatePort(%d) = %v", p, err)
		}
	}
	for _, p := range []int{0, -1, 65536} {
		if err := ValidatePort(p); !errors.Is(err, ErrInvalidName) {
			t.Errorf("ValidatePort(%d) = %v", p, err)
		}
	}
}

func TestQuote(t *testing.T) {
	if q, err := QuotePGIdent("my-db"); err != nil || q != `"my-db"` {
		t.Errorf("QuotePGIdent = %q, %v", q, err)
	}
	if q, err := QuoteMySQLIdent("my-db"); err != nil || q != "`my-db`" {
		t.Errorf("QuoteMySQLIdent = %q, %v", q, err)
	}
	if q, err := QuoteSQLLiteral("my-db"); err != nil || q != "'my-db'" {
		t.Errorf("QuoteSQLLiteral = %q, %v", q, err)
	}
	for _, bad := range hostileNames {
		if _, err := QuotePGIdent(bad); err == nil {
			t.Errorf("QuotePGIdent(%q) accepted", bad)
		}
		if _, err := QuoteMySQLIdent(bad); err == nil {
			t.Errorf("QuoteMySQLIdent(%q) accepted", bad)
		}
		if _, err := QuoteSQLLiteral(bad); err == nil {
			t.Errorf("QuoteSQLLiteral(%q) accepted", bad)
		}
	}
}

func TestTempDatabaseName(t *testing.T) {
	got, err := TempDatabaseName("shop", "42")
	if err != nil || got != "shop__bekup_restore_42" {
		t.Fatalf("TempDatabaseName = %q, %v", got, err)
	}
	got, err = OldDatabaseName("shop", "42")
	if err != nil || got != "shop__bekup_old_42" {
		t.Fatalf("OldDatabaseName = %q, %v", got, err)
	}
	long := strings.Repeat("x", 63)
	id := strings.Repeat("z", 26)
	for _, f := range []func(string, string) (string, error){TempDatabaseName, OldDatabaseName} {
		got, err := f(long, id)
		if err != nil || len(got) != MaxDBNameLen || !strings.HasSuffix(got, "_"+id) {
			t.Errorf("long name = %q (%d), %v", got, len(got), err)
		}
		if !IsBekupTempDatabase(got) || ValidateDBName(got) != nil {
			t.Errorf("%q must be a valid bekup temp name", got)
		}
	}
	for _, badID := range []string{"", "A", "1-2", strings.Repeat("a", 27), "a b"} {
		if _, err := TempDatabaseName("shop", badID); !errors.Is(err, ErrInvalidName) {
			t.Errorf("id %q: %v", badID, err)
		}
	}
	if _, err := TempDatabaseName("-x", "1"); !errors.Is(err, ErrInvalidName) {
		t.Errorf("bad db: %v", err)
	}
}

func TestIsBekupTempDatabase(t *testing.T) {
	for _, ok := range []string{"shop__bekup_restore_42", "a__bekup_old_x1"} {
		if !IsBekupTempDatabase(ok) {
			t.Errorf("%q", ok)
		}
	}
	for _, bad := range []string{"shop", "shop__bekup_restore_", "__bekup_restore_1", "shop__bekup_restore_1 x",
		"shop__bekup_backup_1", "shop__bekup_restore_A", "-x__bekup_old_1", strings.Repeat("a", 60) + "__bekup_old_1"} {
		if IsBekupTempDatabase(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func FuzzValidateDBName(f *testing.F) {
	for _, s := range append([]string{"app", "a.b-c$d"}, hostileNames...) {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if ValidateDBName(s) != nil {
			return
		}
		if s == "" || len(s) > MaxDBNameLen || s[0] == '-' || s[0] == '.' || s[0] == '$' {
			t.Fatalf("accepted %q", s)
		}
		if strings.ContainsAny(s, "=/\\'\"` ;:\x00\n\r\t") {
			t.Fatalf("accepted %q", s)
		}
		// Quoting a valid name never needs escaping.
		q, err := QuotePGIdent(s)
		if err != nil || q != `"`+s+`"` {
			t.Fatalf("QuotePGIdent(%q) = %q, %v", s, q, err)
		}
	})
}

func FuzzValidateHost(f *testing.F) {
	for _, s := range []string{"db", "10.0.0.1", "::1", "-x", "a..b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if ValidateHost(s) != nil {
			return
		}
		if s == "" || s[0] == '-' || strings.ContainsAny(s, " /\\'\"=@%\x00\n") {
			t.Fatalf("accepted %q", s)
		}
	})
}
