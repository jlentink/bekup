package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fieldsTOML is a valid fields-mode [database] section with two roles; the
// password files live in dir.
func fieldsTOML(dir string) string {
	return fmt.Sprintf(`
[database]
host = "db.example.internal"
port = 6432
name = "bekup_prod"
password_file = %q
owner_password_file = %q
tls_ca_file = "/run/secrets/db_ca.pem"
`, filepath.Join(dir, "app_pw"), filepath.Join(dir, "owner_pw"))
}

func loadDB(t *testing.T, content string, env map[string]string) (*Result[Bekup], error) {
	t.Helper()
	p := writeFile(t, safeDir(t), BekupFileName, validBekupTOML+content, 0o600)
	return LoadBekup(opts([]string{p}, env))
}

func TestDatabaseFieldsMode(t *testing.T) {
	dir := safeDir(t)
	res, err := loadDB(t, fieldsTOML(dir), nil)
	if err != nil {
		t.Fatal(err)
	}
	d := res.Config.Database
	if !d.FieldsMode() || d.DatabaseMode() != "fields" {
		t.Fatalf("mode %q", d.DatabaseMode())
	}
	if d.URLFile != "" || d.OwnerURLFile != "" {
		t.Errorf("defaulted URL files not cleared: %q %q", d.URLFile, d.OwnerURLFile)
	}
	if d.User != DBAppRole || d.OwnerUser != DBOwnerRole || d.Port != 6432 || d.Name != "bekup_prod" {
		t.Errorf("unexpected %+v", d)
	}
}

func TestDatabaseModeExclusive(t *testing.T) {
	dir := safeDir(t)
	tests := []struct {
		name    string
		content string
		env     map[string]string
		wantErr []string // empty: valid
	}{
		{"url mode defaults", "", nil, nil},
		{"fields plus url_file", fieldsTOML(dir) + "url_file = \"/run/secrets/u\"\n", nil,
			[]string{"database.url_file: cannot be combined with database.host"}},
		{"fields plus owner_url_file from env", fieldsTOML(dir), map[string]string{"BEKUP_DATABASE_OWNER_URL_FILE": "/run/secrets/o"},
			[]string{"database.owner_url_file: cannot be combined"}},
		{"url mode with user", "[database]\nuser = \"bekup_app\"\n", nil, []string{"database.user: is a fields-mode key"}},
		{"url mode with password_file from env", "", map[string]string{"BEKUP_DATABASE_PASSWORD_FILE": "/run/secrets/p"},
			[]string{"database.password_file: is a fields-mode key"}},
		{"url mode with port", "[database]\nport = 6432\n", nil, []string{"database.port: is a fields-mode key"}},
		{"url mode with default fields (full dump)", "[database]\nport = 5432\nname = \"bekup\"\nuser = \"\"\npassword_file = \"\"\n", nil, nil},
		{"url mode with single_account", "[database]\nsingle_account = true\n", nil, []string{"database.single_account: is a fields-mode key"}},
		{"url mode with single_account false", "[database]\nsingle_account = false\n", nil, nil},
		{"fields without password_file", "[database]\nhost = \"db\"\ntls_ca_file = \"/ca.pem\"\n", nil,
			[]string{"database.password_file: is required"}},
		{"fields with wrong user", fieldsTOML(dir) + "user = \"admin\"\n", nil, []string{"database.user: must be bekup_app"}},
		{"fields with wrong owner", fieldsTOML(dir) + "owner_user = \"postgres\"\n", nil, []string{"database.owner_user: must be bekup_owner"}},
		{"fields bad host", strings.Replace(fieldsTOML(dir), "db.example.internal", "db.example.internal:5432", 1), nil,
			[]string{"database.host: must be a host name"}},
		{"fields socket host", strings.Replace(fieldsTOML(dir), "db.example.internal", "/var/run/postgresql", 1), nil,
			[]string{"database.host"}},
		{"fields bad port", strings.Replace(fieldsTOML(dir), "6432", "70000", 1), nil, []string{"database.port: must be between"}},
		{"fields reserved name", strings.Replace(fieldsTOML(dir), "bekup_prod", "postgres", 1), nil, []string{"database.name"}},
		{"fields quote in name", strings.Replace(fieldsTOML(dir), "bekup_prod", `bekup\"x`, 1), nil, []string{"database.name"}},
		{"same password files", strings.Replace(fieldsTOML(dir), "owner_pw", "app_pw", 1), nil,
			[]string{"database.owner_password_file: must differ"}},
		{"server name with require", fieldsTOML(dir) + "tls_mode = \"require\"\ntls_server_name = \"db.example\"\n", nil,
			[]string{"database.tls_server_name: only applies"}},
		{"server name invalid", fieldsTOML(dir) + "tls_server_name = \"db example\"\n", nil, []string{"database.tls_server_name: must be"}},
		{"server name ok", fieldsTOML(dir) + "tls_server_name = \"db.example\"\n", nil, nil},
		{"server name in url mode", "[database]\ntls_server_name = \"postgres\"\n", nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadDB(t, tc.content, tc.env)
			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			requireErrContains(t, err, tc.wantErr...)
		})
	}
}

func TestDatabaseSingleAccount(t *testing.T) {
	dir := safeDir(t)
	base := fmt.Sprintf(`
[database]
host = "10.0.0.5"
user = "acct_admin"
password_file = %q
single_account = true
tls_ca_file = "/run/secrets/db_ca.pem"
`, filepath.Join(dir, "pw"))
	res, err := loadDB(t, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Database.DatabaseMode() != "single_account" || res.Config.Database.OwnerUser != "" {
		t.Fatalf("unexpected %+v", res.Config.Database)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "single-account mode") {
		t.Errorf("no single-account warning: %v", res.Warnings)
	}
	for name, tc := range map[string]struct{ extra, want string }{
		"owner_user":          {"owner_user = \"bekup_owner\"\n", "database.owner_user: is not used with database.single_account"},
		"owner_password_file": {"owner_password_file = \"/run/secrets/o\"\n", "database.owner_password_file: is not used"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadDB(t, base+tc.extra, nil)
			requireErrContains(t, err, tc.want)
		})
	}
	for _, user := range []string{"bekup_app", "bekup_owner", "pg_monitor", "bad name", ""} {
		t.Run("user "+user, func(t *testing.T) {
			_, err := loadDB(t, strings.Replace(base, `"acct_admin"`, fmt.Sprintf("%q", user), 1), nil)
			requireErrContains(t, err, "database.user")
		})
	}
}

func TestDBCredentials(t *testing.T) {
	dir := safeDir(t)
	appPW := `app-p@ss'w"o\rd:/?#%-0123456789`
	writeFile(t, dir, "app_pw", appPW+"\n", 0o600)
	writeFile(t, dir, "owner_pw", "owner-password-0123456789", 0o600)
	res, err := loadDB(t, fieldsTOML(dir), nil)
	if err != nil {
		t.Fatal(err)
	}
	app, err := res.Config.AppDBCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if !app.FieldsMode() || app.Host != "db.example.internal" || app.Port != 6432 || app.Database != "bekup_prod" ||
		app.User != DBAppRole || string(app.Password) != appPW || app.SetRole != "" {
		t.Fatalf("app credentials %#v", app)
	}
	for _, s := range []string{app.String(), fmt.Sprintf("%v|", app), fmt.Sprintf("%+v", app), fmt.Sprintf("%#v", app)} {
		if strings.Contains(s, "p@ss") {
			t.Errorf("formatting leaks the password: %s", s)
		}
	}
	owner, err := res.Config.OwnerDBCredentials()
	if err != nil || owner.User != DBOwnerRole || string(owner.Password) != "owner-password-0123456789" {
		t.Fatalf("owner credentials %v, %v", owner, err)
	}
	pw := app.Password
	app.Zero()
	if app.Password != nil || strings.Trim(string(pw), "\x00") != "" {
		t.Error("Zero did not clear the password")
	}

	// Missing owner file: a clear error, no secret.
	res.Config.Database.OwnerPasswordFile = ""
	if _, err := res.Config.OwnerDBCredentials(); err == nil || !strings.Contains(err.Error(), "owner_password_file is not set") {
		t.Fatalf("got %v", err)
	}
	// Unsafe file mode: refused like every secret file.
	if err := os.Chmod(filepath.Join(dir, "app_pw"), 0o644); err != nil { //nolint:gosec // G302: the test needs a world-readable file
		t.Fatal(err)
	}
	if _, err := res.Config.AppDBCredentials(); !errors.Is(err, ErrUnsafeFile) || strings.Contains(err.Error(), "p@ss") {
		t.Fatalf("got %v", err)
	}

	// Single account: one password, SET ROLE per purpose.
	res.Config.Database.SingleAccount = true
	res.Config.Database.User = "acct"
	if err := os.Chmod(filepath.Join(dir, "app_pw"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := res.Config.AppDBCredentials()
	if err != nil || a.SetRole != DBAppRole || a.User != "acct" {
		t.Fatalf("single app %v %v", a, err)
	}
	o, err := res.Config.OwnerDBCredentials()
	if err != nil || o.SetRole != DBOwnerRole || o.User != "acct" || string(o.Password) != appPW {
		t.Fatalf("single owner %v %v", o, err)
	}
}

func TestValidDBNames(t *testing.T) {
	for _, ok := range []string{"bekup", "bekup_prod", "Bekup-1", "_x", "a$b", strings.Repeat("a", 63)} {
		if !ValidDBName(ok) {
			t.Errorf("ValidDBName(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "postgres", "Template1", "template0", "1db", "a b", `a"b`, "a;b", "a'b", strings.Repeat("a", 64), "ä"} {
		if ValidDBName(bad) {
			t.Errorf("ValidDBName(%q) = true", bad)
		}
	}
	for _, ok := range []string{"admin", "postgres", "user@server", "app.user", "x-y", "bekup", "bekupadmin", "my_bekup_x"} {
		if !ValidDBRoleName(ok) {
			t.Errorf("ValidDBRoleName(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "bekup_app", "bekup_owner", "bekup_admin", "BEKUP_x", "Bekup_app", "pg_read_all_data", "PG_x", "a b", `a"b`, "a'b", strings.Repeat("a", 64)} {
		if ValidDBRoleName(bad) {
			t.Errorf("ValidDBRoleName(%q) = true", bad)
		}
	}
}

func FuzzValidDBNames(f *testing.F) {
	for _, s := range []string{"bekup", "a\"b", "postgres", "user@x"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for name, ok := range map[string]bool{"db": ValidDBName(s), "role": ValidDBRoleName(s)} {
			if ok && (len(s) > 63 || strings.ContainsAny(s, "\"'\\; \x00\n")) {
				t.Fatalf("%s name %q accepted", name, s)
			}
		}
	})
}
