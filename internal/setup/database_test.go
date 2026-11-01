package setup

import (
	"bytes"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/imageref"
)

// testDBCA writes a CA bundle (one CA certificate valid from now-1h for
// validity) as rel below dir.
func testDBCA(t testing.TB, dir, rel string, now time.Time, validity time.Duration) {
	t.Helper()
	ca, err := certs.NewCA(certs.CAOptions{ServerNames: []string{"db.example.com"}, Validity: validity, Now: func() time.Time { return now.Add(-time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	clear(ca.Key)
	if err := os.MkdirAll(filepath.Dir(pathIn(dir, rel)), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathIn(dir, rel), ca.CertPEM, 0o600); err != nil {
		t.Fatal(err)
	}
}

// externalAnswers are setup-first answers with an external database; the
// CA bundle is written to dir/db-ca.pem.
func externalAnswers(t testing.TB, dir string, auth DBAuth) Answers {
	t.Helper()
	testDBCA(t, dir, "db-ca.pem", time.Now(), 0)
	a := testAnswers(t, ProxyTraefik)
	a.DB = Database{Mode: DBExternal, Host: "db.example.com", CAFile: "db-ca.pem", Auth: auth}
	if auth == DBAuthSingleAccount {
		a.DB.User = "acct_single"
	}
	if auth == DBAuthBootstrap {
		a.DB.AdminUser = "postgres"
	}
	return a
}

const (
	testAppPW   = "app-password-0123456789-abcdef"
	testOwnerPW = "owner-password-0123456789-abcdef"
)

func TestInitExternalDatabase(t *testing.T) {
	t.Parallel()
	for _, auth := range DBAuths {
		t.Run(string(auth), func(t *testing.T) {
			dir := t.TempDir()
			a := externalAnswers(t, dir, auth)
			o := testOptions(t, dir, a)
			o.DBPassword = func(role string) ([]byte, error) {
				switch role {
				case config.DBOwnerRole:
					return []byte(testOwnerPW), nil
				default:
					return []byte(testAppPW), nil
				}
			}
			if err := Init(o); err != nil {
				t.Fatalf("Init: %v", err)
			}
			out := bufOf(t, o.Out).String()
			st := mustState(t, dir)
			if st.DatabaseMode != DBExternal || st.DatabaseAuth != auth || !strings.Contains(strings.Join(st.SecretGroups, ","), groupDatabaseExternal) {
				t.Fatalf("state %+v", st)
			}
			// No bundled database material at all.
			for _, rel := range []string{"secrets/bekup_database_url", "secrets/bekup_database_owner_url", "secrets/postgres_password", "secrets/certs/postgres", "deploy/postgres"} {
				if _, err := os.Lstat(pathIn(dir, rel)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("%s exists (%v)", rel, err)
				}
			}
			compose := string(readFile(t, dir, ComposeFile))
			if strings.Contains(compose, "\n  postgres:\n") || !strings.Contains(compose, "bekup_database_password:") {
				t.Error("compose.yaml is not the external-database form")
			}
			if got := readFile(t, dir, "secrets/bekup_database_ca.crt"); !bytes.Equal(got, readFile(t, dir, "db-ca.pem")) {
				t.Error("CA bundle not copied")
			}
			if fi := mustStat(t, pathIn(dir, "secrets/bekup_database_ca.crt")); fi.Mode().Perm() != 0o644 {
				t.Errorf("CA mode %04o", fi.Mode().Perm())
			}
			app := readFile(t, dir, "secrets/bekup_database_password")
			owner := readFile(t, dir, "secrets/bekup_database_owner_password")
			switch auth {
			case DBAuthBootstrap, DBAuthDBASQL:
				if len(app) != 43 || len(owner) != 43 || bytes.Equal(app, owner) {
					t.Errorf("generated passwords: %d/%d", len(app), len(owner))
				}
			case DBAuthExistingRoles:
				if string(app) != testAppPW || string(owner) != testOwnerPW {
					t.Error("asked passwords not written")
				}
			case DBAuthSingleAccount:
				if string(app) != testAppPW || len(owner) != 0 {
					t.Errorf("single account: app %q, owner %d bytes", app, len(owner))
				}
			}
			roles, err := os.ReadFile(pathIn(dir, "secrets/bekup-roles.sql"))
			switch {
			case auth == DBAuthDBASQL && err != nil:
				t.Fatal(err)
			case auth == DBAuthDBASQL:
				if !bytes.Contains(roles, []byte("SCRAM-SHA-256$")) || bytes.Contains(roles, app) || bytes.Contains(roles, owner) {
					t.Error("roles SQL must carry verifiers, never the passwords")
				}
				if fi := mustStat(t, pathIn(dir, "secrets/bekup-roles.sql")); fi.Mode().Perm() != 0o600 {
					t.Errorf("roles SQL mode %04o", fi.Mode().Perm())
				}
			case !errors.Is(err, os.ErrNotExist):
				t.Errorf("roles SQL written for %s (%v)", auth, err)
			}
			// bekup.toml: fields mode with the mounted paths.
			res, err := config.LoadBekup(config.Options{ConfigPath: pathIn(dir, BekupTOML), LookupEnv: noEnv, Environ: func() []string { return nil }, SearchPaths: []string{}})
			if err != nil {
				t.Fatal(err)
			}
			d := res.Config.Database
			if !d.FieldsMode() || d.Host != "db.example.com" || d.Port != 5432 || d.Name != "bekup" || d.PasswordFile != dbPasswordTarget || d.TLSCAFile != dbCATarget || d.TLSMode != config.DBTLSVerifyFull {
				t.Fatalf("database config %+v", d)
			}
			if d.SingleAccount != (auth == DBAuthSingleAccount) {
				t.Fatalf("single_account %v", d.SingleAccount)
			}
			if auth != DBAuthSingleAccount && d.OwnerPasswordFile != dbOwnerPasswordTarget {
				t.Fatalf("owner_password_file %q", d.OwnerPasswordFile)
			}
			// The report names the next commands and never a secret.
			want := map[DBAuth]string{
				DBAuthBootstrap:     "docker compose run --rm migrate db bootstrap --admin-user postgres",
				DBAuthSingleAccount: "docker compose run --rm migrate db bootstrap\n",
				DBAuthDBASQL:        "psql -v ON_ERROR_STOP=1",
				DBAuthExistingRoles: "docker compose run --rm migrate migrate check",
			}[auth]
			if !strings.Contains(out, want) || !strings.Contains(out, "your PostgreSQL at db.example.com:5432") {
				t.Errorf("report lacks %q:\n%s", want, out)
			}
			for _, s := range [][]byte{app, owner, roles} {
				if len(s) > 0 && bytes.Contains([]byte(out), s) {
					t.Fatal("secret material in the output")
				}
			}
			if (auth == DBAuthSingleAccount) != strings.Contains(out, "EXPLICIT WEAKENING") {
				t.Error("single-account weakening warning")
			}
			fs, _, err := runCheck(t, dir, nil)
			if err != nil {
				t.Fatalf("check: %v %v", err, fs)
			}
			if (auth == DBAuthDBASQL) != hasFinding(fs, SeverityWarning, "roles SQL") {
				t.Errorf("roles SQL warning: %v", fs)
			}
		})
	}
}

func TestDatabaseAnswersValidate(t *testing.T) {
	t.Parallel()
	base := func() Database {
		return Database{Mode: DBExternal, Host: "db.example.com", CAFile: "ca.pem"}
	}
	cases := []struct {
		name string
		mut  func(*Database)
		want string
	}{
		{"ok", func(*Database) {}, ""},
		{"ipv4 host", func(d *Database) { d.Host = "10.1.2.3" }, ""},
		{"ipv6 host", func(d *Database) { d.Host = "2001:db8::5" }, ""},
		{"negative port", func(d *Database) { d.Port = -1 }, "db-port"},
		{"no host", func(d *Database) { d.Host = "" }, "db-host"},
		{"bad host", func(d *Database) { d.Host = "db.example.com:5432" }, "db-host"},
		{"quote host", func(d *Database) { d.Host = `db"x` }, "db-host"},
		{"bad port", func(d *Database) { d.Port = 70000 }, "db-port"},
		{"bad name", func(d *Database) { d.Name = "postgres" }, "db-name"},
		{"no ca", func(d *Database) { d.CAFile = "" }, "db-ca-file"},
		{"abs ca", func(d *Database) { d.CAFile = "/etc/ssl/certs/ca-certificates.crt" }, "db-ca-file"},
		{"dotdot ca", func(d *Database) { d.CAFile = "../ca.pem" }, "db-ca-file"},
		{"bad sni", func(d *Database) { d.TLSServerName = "x y" }, "db-tls-server-name"},
		{"bad auth", func(d *Database) { d.Auth = "trust" }, "db-auth"},
		{"single without user", func(d *Database) { d.Auth = DBAuthSingleAccount }, "db-user"},
		{"single bekup_app", func(d *Database) { d.Auth, d.User = DBAuthSingleAccount, "bekup_app" }, "db-user"},
		{"user without single", func(d *Database) { d.User = "x" }, "db-user"},
		{"single owner file", func(d *Database) { d.Auth, d.User, d.OwnerPasswordFile = DBAuthSingleAccount, "acct", "o" }, "db-owner-password-file"},
		{"same files", func(d *Database) { d.PasswordFile, d.OwnerPasswordFile = "p", "p" }, "db-owner-password-file"},
		{"admin user dba", func(d *Database) { d.Auth, d.AdminUser = DBAuthDBASQL, "postgres" }, "db-admin-user"},
		{"admin user pg_", func(d *Database) { d.AdminUser = "pg_monitor" }, "db-admin-user"},
		// D-215: location, Docker network and TLS choices.
		{"docker", func(d *Database) { d.Location, d.Network, d.Host = DBDocker, "dbnet", "postgres" }, ""},
		{"docker without network", func(d *Database) { d.Location, d.Host = DBDocker, "postgres" }, "db-network"},
		{"docker bad network", func(d *Database) { d.Location, d.Network = DBDocker, "net work" }, "db-network"},
		{"docker default bridge", func(d *Database) { d.Location, d.Network = DBDocker, "bridge" }, "db-network"},
		{"docker underscore host", func(d *Database) { d.Location, d.Network, d.Host = DBDocker, "dbnet", "app_db_1" }, "compose service name"},
		{"remote with network", func(d *Database) { d.Network = "dbnet" }, "db-network"},
		{"bad location", func(d *Database) { d.Location = "cloud" }, "db-location"},
		{"verify-system", func(d *Database) { d.TLS, d.CAFile = DBTLSVerifySystem, "" }, ""},
		{"verify-system with sni", func(d *Database) { d.TLS, d.CAFile, d.TLSServerName = DBTLSVerifySystem, "", "db.example.com" }, ""},
		{"verify-system with ca", func(d *Database) { d.TLS = DBTLSVerifySystem }, "db-ca-file"},
		{"require", func(d *Database) { d.TLS, d.CAFile = DBTLSRequire, "" }, ""},
		{"require with ca", func(d *Database) { d.TLS = DBTLSRequire }, "db-ca-file"},
		{"require with sni", func(d *Database) { d.TLS, d.CAFile, d.TLSServerName = DBTLSRequire, "", "db.example.com" }, "db-tls-server-name"},
		{"disable", func(d *Database) { d.TLS, d.CAFile = DBTLSDisable, "" }, ""},
		{"disable with ca", func(d *Database) { d.TLS = DBTLSDisable }, "db-ca-file"},
		{"bad tls", func(d *Database) { d.TLS, d.CAFile = "prefer", "" }, "db-tls"},
		{"verify-ca-file explicit", func(d *Database) { d.TLS = DBTLSVerifyCAFile }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := base()
			tc.mut(&d)
			err := d.validate()
			switch {
			case tc.want == "" && err != nil:
				t.Fatal(err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("err %v, want %q", err, tc.want)
			}
		})
	}
	// Bundled: no --db-* flags.
	d := Database{Host: "x"}
	if err := d.validate(); err == nil || !strings.Contains(err.Error(), "--database external") {
		t.Fatalf("bundled with host: %v", err)
	}
	d = Database{}
	if err := d.validate(); err != nil || d.Mode != DBBundled {
		t.Fatalf("zero value: %v %q", err, d.Mode)
	}
	d = base()
	if err := d.validate(); err != nil || d.Port != 5432 || d.Name != "bekup" || d.Auth != DBAuthBootstrap {
		t.Fatalf("defaults: %+v %v", d, err)
	}
}

func TestInitExternalDatabaseInputs(t *testing.T) {
	t.Parallel()
	t.Run("password files", func(t *testing.T) {
		dir := t.TempDir()
		a := externalAnswers(t, dir, DBAuthExistingRoles)
		a.DB.PasswordFile, a.DB.OwnerPasswordFile = "app.pw", "owner.pw"
		if err := os.WriteFile(pathIn(dir, "app.pw"), []byte("  "+testAppPW+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pathIn(dir, "owner.pw"), []byte(testOwnerPW), 0o600); err != nil {
			t.Fatal(err)
		}
		out := mustInit(t, dir, a)
		if string(readFile(t, dir, "secrets/bekup_database_password")) != testAppPW {
			t.Error("password file not trimmed and copied")
		}
		if !strings.Contains(out.String(), "app.pw was copied into secrets/: delete it now") {
			t.Errorf("no delete hint:\n%s", out)
		}
		st, err := ReadState(dir)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(st.ImportedFiles, ",") != "app.pw,owner.pw" {
			t.Errorf("imported_files = %v", st.ImportedFiles)
		}
		fs, cout := checkFindings(t, dir)
		if !hasFinding(fs, SeverityWarning, "setup copied this secret") {
			t.Errorf("no warning for the leftover password files:\n%s", cout)
		}
		if err := os.Remove(pathIn(dir, "app.pw")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(pathIn(dir, "owner.pw")); err != nil {
			t.Fatal(err)
		}
		if fs, cout := checkFindings(t, dir); hasFinding(fs, SeverityWarning, "setup copied this secret") {
			t.Errorf("warning after the deletion:\n%s", cout)
		}
	})
	refusals := []struct {
		name string
		mut  func(t *testing.T, dir string, a *Answers, o *Options)
		want string
	}{
		{"short password", func(t *testing.T, dir string, a *Answers, _ *Options) {
			a.DB.PasswordFile = "app.pw"
			if err := os.WriteFile(pathIn(dir, "app.pw"), []byte("short"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "16 to 1024 characters"},
		{"non-ascii password", func(_ *testing.T, _ string, _ *Answers, o *Options) {
			o.DBPassword = func(string) ([]byte, error) { return []byte("päßwörd-päßwörd-päßwörd"), nil }
		}, "printable ASCII"},
		{"existing roles without terminal", func(_ *testing.T, _ string, _ *Answers, o *Options) { o.DBPassword = nil }, "needs the password of bekup_app"},
		{"same passwords", func(_ *testing.T, _ string, _ *Answers, o *Options) {
			o.DBPassword = func(string) ([]byte, error) { return []byte(testAppPW), nil }
		}, "different passwords"},
		{"missing ca", func(_ *testing.T, _ string, a *Answers, _ *Options) { a.DB.CAFile = "nope.pem" }, "--db-ca-file nope.pem"},
		{"ca is a key", func(t *testing.T, dir string, _ *Answers, _ *Options) {
			if err := os.WriteFile(pathIn(dir, "db-ca.pem"), []byte("-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "unexpected PEM block"},
		{"ca symlink", func(t *testing.T, dir string, a *Answers, _ *Options) {
			if err := os.Symlink("/etc/hosts", pathIn(dir, "link.pem")); err != nil {
				t.Fatal(err)
			}
			a.DB.CAFile = "link.pem"
		}, "symlink"},
		{"password file below a symlinked directory", func(t *testing.T, dir string, a *Answers, _ *Options) {
			if err := os.Mkdir(pathIn(dir, "real"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(pathIn(dir, "real/app.pw"), []byte(testAppPW), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("real", pathIn(dir, "lnk")); err != nil {
				t.Fatal(err)
			}
			a.DB.PasswordFile = "lnk/app.pw"
		}, "symlink"},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			a := externalAnswers(t, dir, DBAuthExistingRoles)
			o := testOptions(t, dir, a)
			o.DBPassword = func(role string) ([]byte, error) {
				if role == config.DBOwnerRole {
					return []byte(testOwnerPW), nil
				}
				return []byte(testAppPW), nil
			}
			tc.mut(t, dir, &o.Answers, &o)
			before := snapshot(t, dir)
			err := Init(o)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want %q", err, tc.want)
			}
			if d := diffSnapshots(before, snapshot(t, dir)); len(d) > 0 {
				t.Fatalf("failed init changed the directory: %v", d)
			}
		})
	}
}

func TestComposeFirstExternalDatabase(t *testing.T) {
	t.Parallel()
	for _, v := range ReleaseVariants {
		t.Run(string(v), func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			img := mustRef(t, variantImage)
			data, err := RenderRelease(v, DBExternal, &img)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(pathIn(dir, ComposeFile), data, 0o644); err != nil { //nolint:gosec // G306: compose.yaml is public
				t.Fatal(err)
			}
			info, err := InspectCompose(dir, "", "")
			if err != nil || info.Variant != v || info.DB != DBExternal || info.Level != RecognizedExact || info.HasErrors() {
				t.Fatalf("inspect: %+v %v", info, err)
			}
			if _, err := InspectCompose(dir, "", DBBundled); err == nil || !strings.Contains(err.Error(), "uses the external database, not bundled") {
				t.Fatalf("db mismatch: %v", err)
			}
			// Without database answers, init refuses (the mode comes from
			// the file, the host must come from the operator).
			if _, err := composeFirstInit(t, dir, v, nil); err == nil || !strings.Contains(err.Error(), "db-host") {
				t.Fatalf("missing host: %v", err)
			}
			testDBCA(t, dir, "db-ca.pem", time.Now(), 0)
			out, err := composeFirstInit(t, dir, v, func(o *Options) {
				o.Answers.DB = Database{Host: "10.0.5.4", TLSServerName: "db.example.com", CAFile: "db-ca.pem"}
			})
			if err != nil {
				t.Fatalf("init: %v\n%s", err, out)
			}
			st := mustState(t, dir)
			if st.DatabaseMode != DBExternal || st.DatabaseAuth != DBAuthBootstrap {
				t.Fatalf("state %+v", st)
			}
			if _, err := os.Lstat(pathIn(dir, "deploy/postgres")); !errors.Is(err, os.ErrNotExist) {
				t.Error("deploy/postgres written for an external database")
			}
			if fs, out, err := runCheck(t, dir, nil); err != nil {
				t.Fatalf("check: %v\n%s", err, out)
			} else if !strings.Contains(out, "migrate migrate check") {
				t.Errorf("check output lacks the migrate check hint:\n%s", out)
			} else if hasErrors(fs) {
				t.Fatal(fs)
			}
			// Upgrade refuses a switch to the bundled database.
			bundled, err := RenderRelease(v, DBBundled, &img)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(pathIn(dir, ComposeFile), bundled, 0o644); err != nil { //nolint:gosec // G306: compose.yaml is public
				t.Fatal(err)
			}
			uo := upgradeOptions(t, dir, variantImage)
			uo.WriteCompose, uo.Image = false, imageref.Ref{}
			if err := Upgrade(uo); err == nil || !strings.Contains(err.Error(), "switching between the bundled and an external database is not supported") {
				t.Fatalf("upgrade with a db switch: %v", err)
			}
			if fs, _ := checkFindings(t, dir); !hasFinding(fs, SeverityError, "uses the bundled database, not external") {
				t.Errorf("check after a db switch: %v", fs)
			}
		})
	}
}

func TestCheckExternalDatabase(t *testing.T) {
	t.Parallel()
	setupDir := func(t *testing.T, auth DBAuth) string {
		dir := t.TempDir()
		a := externalAnswers(t, dir, auth)
		o := testOptions(t, dir, a)
		o.DBPassword = func(string) ([]byte, error) { return []byte(testAppPW), nil }
		if err := Init(o); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	edit := func(t *testing.T, dir, from, to string) {
		t.Helper()
		b := readFile(t, dir, BekupTOML)
		if !bytes.Contains(b, []byte(from)) {
			t.Fatalf("no %q in bekup.toml", from)
		}
		if err := os.WriteFile(pathIn(dir, BekupTOML), bytes.Replace(b, []byte(from), []byte(to), 1), 0o644); err != nil { //nolint:gosec // G306: public config
			t.Fatal(err)
		}
	}
	cases := []struct {
		name string
		auth DBAuth
		mut  func(t *testing.T, dir string)
		sev  Severity
		want string
	}{
		{"tls require", DBAuthBootstrap, func(t *testing.T, dir string) {
			edit(t, dir, `tls_mode = "verify-full"`, `tls_mode = "require"`)
		}, SeverityWarning, `tls_mode = "require" is weaker`},
		{"password file moved", DBAuthBootstrap, func(t *testing.T, dir string) {
			edit(t, dir, `password_file = "/run/secrets/bekup_database_password"`, `password_file = "/run/secrets/other"`)
		}, SeverityError, "database.password_file"},
		{"single account added", DBAuthBootstrap, func(t *testing.T, dir string) {
			edit(t, dir, `owner_user = "bekup_owner"`+"\n", "")
			edit(t, dir, `owner_password_file = "/run/secrets/bekup_database_owner_password"`+"\n", "")
			edit(t, dir, `user = "bekup_app"`, "single_account = true\nuser = \"acct\"")
		}, SeverityError, "single_account = true, but setup configured the two roles"},
		{"bundled toml", DBAuthBootstrap, func(t *testing.T, dir string) {
			b := readFile(t, dir, BekupTOML)
			i := bytes.Index(b, []byte("\n[database]"))
			if err := os.WriteFile(pathIn(dir, BekupTOML), b[:i+1], 0o644); err != nil { //nolint:gosec // G306: public config
				t.Fatal(err)
			}
		}, SeverityError, "[database] has no host"},
		{"pg env", DBAuthBootstrap, func(t *testing.T, dir string) {
			f, err := os.OpenFile(pathIn(dir, EnvFile), os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := f.Close(); err != nil {
					t.Error(err)
				}
			}()
			if _, err := f.WriteString("PGPASSWORD=hunter2\n"); err != nil {
				t.Fatal(err)
			}
		}, SeverityError, "PGPASSWORD: .env holds compose variables only"},
		{"expired ca", DBAuthBootstrap, func(t *testing.T, dir string) {
			if err := os.Chmod(pathIn(dir, "secrets/bekup_database_ca.crt"), 0o600); err != nil {
				t.Fatal(err)
			}
			testDBCA(t, dir, "secrets/bekup_database_ca.crt", time.Now().Add(-72*time.Hour), time.Hour)
			if err := os.Chmod(pathIn(dir, "secrets/bekup_database_ca.crt"), 0o644); err != nil { //nolint:gosec // G302: public CA bundle (manifest mode 0644)
				t.Fatal(err)
			}
		}, SeverityError, "no certificate in the CA bundle is valid now"},
		{"owner file missing", DBAuthBootstrap, func(t *testing.T, dir string) {
			if err := os.Remove(pathIn(dir, "secrets/bekup_database_owner_password")); err != nil {
				t.Fatal(err)
			}
		}, SeverityError, "missing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := setupDir(t, tc.auth)
			tc.mut(t, dir)
			fs, out := checkFindings(t, dir)
			if !hasFinding(fs, tc.sev, tc.want) {
				t.Fatalf("no %s %q:\n%s", tc.sev, tc.want, out)
			}
		})
	}
	// A bundled deployment with a fields-mode bekup.toml.
	dir := t.TempDir()
	mustInit(t, dir, testAnswers(t, ProxyTraefik))
	b := append(readFile(t, dir, BekupTOML), []byte("\n[database]\nhost = \"db.example.com\"\npassword_file = \"/run/secrets/x\"\n")...)
	if err := os.WriteFile(pathIn(dir, BekupTOML), b, 0o644); err != nil { //nolint:gosec // G306: public config
		t.Fatal(err)
	}
	if fs, out := checkFindings(t, dir); !hasFinding(fs, SeverityError, "runs the bundled postgres service") {
		t.Fatalf("bundled with fields mode:\n%s", out)
	}
}

func TestInitImportMasterKey(t *testing.T) {
	t.Parallel()
	k, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer k.Destroy()
	data, err := k.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	kcv := k.KeyCheckValue()
	t.Run("ok", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(pathIn(dir, "old_master_key"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		o := testOptions(t, dir, testAnswers(t, ProxyTraefik))
		o.ImportMasterKey, o.MasterKeyCheckValue = "old_master_key", kcv
		if err := Init(o); err != nil {
			t.Fatal(err)
		}
		got, err := crypto.ParseKeyring(readFile(t, dir, "secrets/bekup_master_key"))
		if err != nil {
			t.Fatal(err)
		}
		defer got.Destroy()
		if got.PrimaryID() != k.PrimaryID() {
			t.Fatal("imported keyring differs")
		}
		out := bufOf(t, o.Out).String()
		if !strings.Contains(out, "imported (primary key ID "+k.PrimaryID().String()) || !strings.Contains(out, "old_master_key was imported") {
			t.Errorf("report:\n%s", out)
		}
		if fs, cout := checkFindings(t, dir); !hasFinding(fs, SeverityWarning, "setup copied this secret") {
			t.Errorf("no warning for the leftover keyring:\n%s", cout)
		}
	})
	for _, tc := range []struct {
		name, kcv string
		file      []byte
		want      string
	}{
		{"wrong kcv", "kcv1:" + k.PrimaryID().String() + ":" + strings.Repeat("A", 43), data, "key-check value does not match"},
		{"garbage", "", []byte("not a keyring"), "not a bekup master keyring"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(pathIn(dir, "old_master_key"), tc.file, 0o600); err != nil {
				t.Fatal(err)
			}
			o := testOptions(t, dir, testAnswers(t, ProxyTraefik))
			o.ImportMasterKey, o.MasterKeyCheckValue = "old_master_key", tc.kcv
			if err := Init(o); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want %q", err, tc.want)
			}
			if _, err := os.Lstat(pathIn(dir, "secrets")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("a refused import wrote secrets")
			}
		})
	}
}

func TestInitUseExistingCA(t *testing.T) {
	t.Parallel()
	// The CA of an earlier deployment, as on the offline media.
	src := t.TempDir()
	mustInit(t, src, testAnswers(t, ProxyTraefik))
	caDir := pathIn(src, "secrets/certs/ca")
	for _, db := range DBModes {
		t.Run(string(db), func(t *testing.T) {
			dir := t.TempDir()
			copyCA(t, caDir, pathIn(dir, "secrets/certs/ca"))
			caBefore := readFile(t, dir, "secrets/certs/ca/ca.crt")
			a := testAnswers(t, ProxyTraefik)
			if db == DBExternal {
				a = externalAnswers(t, dir, DBAuthBootstrap)
			}
			o := testOptions(t, dir, a)
			o.UseExistingCA = true
			if err := Init(o); err != nil {
				t.Fatalf("Init: %v", err)
			}
			if !bytes.Equal(readFile(t, dir, "secrets/certs/ca/ca.crt"), caBefore) {
				t.Fatal("the existing CA was changed")
			}
			ca, err := certs.ParseCertificates(caBefore)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range certComponentsFor(db) {
				leaf, err := certs.ParseCertificates(readFile(t, dir, "secrets/certs/"+c+"/"+c+".crt"))
				if err != nil {
					t.Fatal(err)
				}
				roots, inter := x509.NewCertPool(), x509.NewCertPool()
				roots.AddCert(ca[0])
				for _, ic := range leaf[1:] {
					inter.AddCert(ic)
				}
				if _, err := leaf[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
					t.Errorf("%s not issued by the existing CA: %v", c, err)
				}
			}
			if !strings.Contains(bufOf(t, o.Out).String(), "issued from your existing CA") {
				t.Error("report does not say the existing CA issued the certificates")
			}
		})
	}
	t.Run("wrong passphrase rolls back", func(t *testing.T) {
		dir := t.TempDir()
		copyCA(t, caDir, pathIn(dir, "secrets/certs/ca"))
		before := snapshot(t, dir)
		o := testOptions(t, dir, testAnswers(t, ProxyTraefik))
		o.UseExistingCA = true
		o.certsRotate = func(certs.RotateOptions) (*certs.Result, error) {
			return nil, errors.New("decrypting the CA key: wrong passphrase")
		}
		if err := Init(o); err == nil || !strings.Contains(err.Error(), "wrong passphrase") {
			t.Fatalf("err %v", err)
		}
		if d := diffSnapshots(before, snapshot(t, dir)); len(d) > 0 {
			t.Fatalf("rollback left changes: %v", d)
		}
	})
	t.Run("no CA mounted", func(t *testing.T) {
		dir := t.TempDir()
		o := testOptions(t, dir, testAnswers(t, ProxyTraefik))
		o.UseExistingCA = true
		if err := Init(o); err == nil || !strings.Contains(err.Error(), "mount the CA media") {
			t.Fatalf("err %v", err)
		}
	})
}

// copyCA copies the CA directory (ca.crt, ca.key) like mounted media.
func copyCA(t *testing.T, from, to string) {
	t.Helper()
	if err := os.MkdirAll(to, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"ca.crt", "ca.key"} {
		b, err := os.ReadFile(filepath.Clean(filepath.Join(from, f)))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(to, f), b, 0o600); err != nil { //nolint:gosec // G703: paths below t.TempDir()
			t.Fatal(err)
		}
	}
}

// checkFindings runs check and returns its findings and output (a failed
// check is expected here; any other error is not).
func checkFindings(t *testing.T, dir string) ([]Finding, string) {
	t.Helper()
	fs, out, err := runCheck(t, dir, nil)
	if err != nil && !errors.Is(err, ErrCheckFailed) {
		t.Fatal(err)
	}
	return fs, out
}

// TestInitDatabaseTLSChoices: every TLS choice (D-215) writes its
// [database] keys, an empty CA file where none is used, the weakening
// warning, the bootstrap flag for the weak modes, and passes setup check
// (weak modes with a warning).
func TestInitDatabaseTLSChoices(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		tls      DBTLS
		lines    []string
		insecure bool
		emptyCA  bool
	}{
		{DBTLSVerifyCAFile, []string{`tls_mode = "verify-full"`, `tls_ca_file = "/run/secrets/bekup_db_ca.crt"`}, false, false},
		{DBTLSVerifySystem, []string{`tls_mode = "verify-full"`, `tls_ca_file = "` + config.SystemCABundle + `"`}, false, true},
		{DBTLSRequire, []string{`tls_mode = "require"`, `tls_ca_file = ""`}, true, true},
		{DBTLSDisable, []string{`tls_mode = "disable"`, `tls_ca_file = ""`}, true, true},
	} {
		t.Run(string(tc.tls), func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			a := externalAnswers(t, dir, DBAuthBootstrap)
			a.DB.TLS = tc.tls
			if tc.tls != DBTLSVerifyCAFile {
				a.DB.CAFile = ""
			}
			out := mustInit(t, dir, a).String()
			bt := string(readFile(t, dir, BekupTOML))
			for _, l := range tc.lines {
				if !hasLine(bt, l) {
					t.Errorf("bekup.toml lacks %s:\n%s", l, bt)
				}
			}
			ca := readFile(t, dir, Layout{KeysDir: DefaultKeysDir}.keys(dbCAName))
			if (len(ca) == 0) != tc.emptyCA {
				t.Errorf("CA file: %d bytes", len(ca))
			}
			if got := strings.Contains(out, "--allow-insecure-admin-connection"); got != tc.insecure {
				t.Errorf("bootstrap flag %v:\n%s", got, out)
			}
			if got := strings.Contains(out, "EXPLICIT WEAKENING"); got != tc.insecure {
				t.Errorf("weakening warning %v:\n%s", got, out)
			}
			if st := mustState(t, dir); st.DatabaseTLS != tc.tls || st.DatabaseLocation != DBRemote {
				t.Errorf("state %+v", st)
			}
			fs, report, err := runCheck(t, dir, nil)
			if err != nil {
				t.Fatalf("check: %v\n%s", err, report)
			}
			if got := hasFinding(fs, SeverityWarning, "weaker than verify-full"); got != tc.insecure {
				t.Errorf("weak TLS warning %v:\n%s", got, report)
			}
		})
	}
	// An emptied CA file of verify-ca-file is a check error.
	dir := t.TempDir()
	mustInit(t, dir, externalAnswers(t, dir, DBAuthBootstrap))
	if err := os.WriteFile(pathIn(dir, Layout{KeysDir: DefaultKeysDir}.keys(dbCAName)), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if fs, report, err := runCheck(t, dir, nil); !errors.Is(err, ErrCheckFailed) || !hasFinding(fs, SeverityError, "is empty, but setup configured --db-tls verify-ca-file") {
		t.Fatalf("empty CA:\n%s", report)
	}
}

// TestDBNextStepsPSQL: the DBA's psql line follows the TLS choice.
func TestDBNextStepsPSQL(t *testing.T) {
	t.Parallel()
	for tls, want := range map[DBTLS]string{
		"": "sslmode=verify-full sslrootcert=<ca.pem>", DBTLSVerifySystem: "sslmode=verify-full sslrootcert=system",
		DBTLSRequire: "sslmode=require", DBTLSDisable: "sslmode=disable",
	} {
		d := Database{Mode: DBExternal, Host: "db", Port: 5432, Name: "bekup", Auth: DBAuthDBASQL, TLS: tls}
		if got := strings.Join(dbNextSteps(&d, Layout{KeysDir: "secrets"}), "\n"); !strings.Contains(got, want) {
			t.Errorf("%q: %s", tls, got)
		}
	}
}

// TestComposeFirstDatabaseContainer: own proxy plus a database container
// on a Docker network (D-215): setup writes the overlay into a new
// compose.override.yaml, BEKUP_DB_NETWORK to .env, and check passes; an
// existing override without the blocks is refused with them.
func TestComposeFirstDatabaseContainer(t *testing.T) {
	t.Parallel()
	for _, v := range ReleaseVariants {
		t.Run(string(v), func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeReleaseDB(t, dir, v, DBExternal, variantImage)
			db := Database{Location: DBDocker, Network: "dbnet", Host: "postgres", TLS: DBTLSDisable}
			// An override of the operator's own without the blocks.
			if err := os.WriteFile(pathIn(dir, OverrideFile), []byte("services: {}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := composeFirstInit(t, dir, v, func(o *Options) { o.Answers.DB = db }); err == nil ||
				!strings.Contains(err.Error(), "add these blocks") || !strings.Contains(err.Error(), "BEKUP_DB_NETWORK") {
				t.Fatalf("existing override: %v", err)
			}
			if err := os.Remove(pathIn(dir, OverrideFile)); err != nil {
				t.Fatal(err)
			}
			out, err := composeFirstInit(t, dir, v, func(o *Options) { o.Answers.DB = db })
			if err != nil {
				t.Fatalf("init: %v\n%s", err, out)
			}
			ov := Overlays{DBNetwork: true}
			if got := string(readFile(t, dir, OverrideFile)); got != overlayHeader+overlayDoc(ov, "") {
				t.Fatalf("override:\n%s", got)
			}
			if env := string(readFile(t, dir, EnvFile)); !hasLine(env, "BEKUP_DB_NETWORK=dbnet") {
				t.Fatalf(".env:\n%s", env)
			}
			st := mustState(t, dir)
			if st.DatabaseLocation != DBDocker || st.DBNetwork != "dbnet" || st.DatabaseTLS != DBTLSDisable || !st.overlays().DBNetwork {
				t.Fatalf("state %+v", st)
			}
			fs, report, err := runCheck(t, dir, nil)
			if err != nil || hasErrors(fs) {
				t.Fatalf("check: %v\n%s", err, report)
			}
			// A foreign service on the database network, a renamed network.
			if err := os.WriteFile(pathIn(dir, OverrideFile), []byte(overlayHeader+overlayDoc(ov, "")+"  # end\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			over := string(readFile(t, dir, OverrideFile))
			over = strings.Replace(over, "services:\n", "services:\n"+evilService+"    networks: [database]\n", 1)
			if err := os.WriteFile(pathIn(dir, OverrideFile), []byte(over), 0o600); err != nil {
				t.Fatal(err)
			}
			if fs, report, err := runCheck(t, dir, nil); !errors.Is(err, ErrCheckFailed) || !hasFinding(fs, SeverityError, `may not join bekup's network "database"`) {
				t.Fatalf("foreign service:\n%s", report)
			}
			if err := os.WriteFile(pathIn(dir, OverrideFile), []byte(overlayHeader+overlayDoc(ov, "")), 0o600); err != nil {
				t.Fatal(err)
			}
			replaceIn(EnvFile, "BEKUP_DB_NETWORK=dbnet", "BEKUP_DB_NETWORK=other")(t, dir)
			if fs, report, err := runCheck(t, dir, nil); !errors.Is(err, ErrCheckFailed) || !hasFinding(fs, SeverityError, "BEKUP_DB_NETWORK") {
				t.Fatalf("renamed network:\n%s", report)
			}
		})
	}
}
