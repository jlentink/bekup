package config

import (
	"strings"
	"testing"
)

func TestDatabaseTLSValidation(t *testing.T) {
	tests := []struct {
		name    string
		dev     bool
		mutate  func(d *Database)
		wantErr string // empty: valid
	}{
		{"defaults", false, func(*Database) {}, ""},
		{"verify-full without CA", false, func(d *Database) { d.TLSCAFile = "" }, "database.tls_ca_file: is required"},
		{"verify-full without CA in dev", true, func(d *Database) { d.TLSCAFile = "" }, "database.tls_ca_file: is required"},
		{"relative CA", false, func(d *Database) { d.TLSCAFile = "ca.crt" }, "database.tls_ca_file: must be an absolute path"},
		{"unknown mode", false, func(d *Database) { d.TLSMode = "prefer" }, "database.tls_mode: must be one of"},
		{"empty mode", false, func(d *Database) { d.TLSMode = "" }, "database.tls_mode: must be one of"},
		{"require without CA", false, func(d *Database) { d.TLSMode = DBTLSRequire; d.TLSCAFile = "" }, ""},
		{"disable without CA", false, func(d *Database) { d.TLSMode = DBTLSDisable; d.TLSCAFile = "" }, ""},
		{"client cert and key", false, func(d *Database) {
			d.TLSClientCertFile, d.TLSClientKeyFile = "/run/secrets/c.crt", "/run/secrets/c.key"
		}, ""},
		{"client cert without key", false, func(d *Database) { d.TLSClientCertFile = "/run/secrets/c.crt" }, "must be set together"},
		{"client key without cert", false, func(d *Database) { d.TLSClientKeyFile = "/run/secrets/c.key" }, "must be set together"},
		{"relative client key", false, func(d *Database) {
			d.TLSClientCertFile, d.TLSClientKeyFile = "/run/secrets/c.crt", "c.key"
		}, "database.tls_client_key_file: must be an absolute path"},
		{"client cert with disable", false, func(d *Database) {
			d.TLSMode = DBTLSDisable
			d.TLSClientCertFile, d.TLSClientKeyFile = "/run/secrets/c.crt", "/run/secrets/c.key"
		}, "has no effect"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := baseBekup()
			tc.mutate(&c.Database)
			joined := joinErrs(validateBekup(&c, tc.dev))
			switch {
			case tc.wantErr == "" && joined != "":
				t.Fatalf("unexpected errors:\n%s", joined)
			case tc.wantErr != "" && !strings.Contains(joined, tc.wantErr):
				t.Fatalf("missing %q in:\n%s", tc.wantErr, joined)
			}
		})
	}
}

func TestDatabaseTLSWarnings(t *testing.T) {
	tests := []struct {
		mode, ca string
		want     string // empty: no database TLS warning
		weak     bool
	}{
		{DBTLSVerifyFull, "/run/secrets/ca.crt", "", false},
		{DBTLSRequire, "/run/secrets/ca.crt", DBTLSRequireCAWarning, true},
		{DBTLSRequire, "", DBTLSRequireWarning, true},
		{DBTLSDisable, "", DBTLSDisableWarning, true},
	}
	for _, tc := range tests {
		for _, dev := range []bool{false, true} {
			c := baseBekup()
			c.Database.TLSMode, c.Database.TLSCAFile = tc.mode, tc.ca
			ws := c.warnings(dev)
			got := hasWarning(ws, "app database TLS")
			if got != (tc.want != "") || (tc.want != "" && !hasWarning(ws, tc.want)) {
				t.Errorf("mode %s ca %q dev %t: warnings %v, want %q", tc.mode, tc.ca, dev, ws, tc.want)
			}
			if c.Database.TLSWeakened() != tc.weak {
				t.Errorf("mode %s: TLSWeakened() = %t", tc.mode, !tc.weak)
			}
		}
	}
}

func TestDatabaseTLSLoadWarningAndKey(t *testing.T) {
	dir := safeDir(t)
	key := writeFile(t, dir, "db.key", "  key-material\n", 0o600)
	p := writeFile(t, dir, BekupFileName, validBekupTOML+`
[database]
tls_mode = "require"
tls_ca_file = ""
`, 0o600)
	res, err := LoadBekup(opts([]string{p}, map[string]string{
		"BEKUP_DATABASE_TLS_CLIENT_CERT_FILE": dir + "/db.crt",
		"BEKUP_DATABASE_TLS_CLIENT_KEY_FILE":  key,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(res.Warnings, DBTLSRequireWarning) {
		t.Fatalf("warnings: %v", res.Warnings)
	}
	b, err := res.Config.DatabaseTLSClientKey()
	if err != nil || string(b) != "key-material" {
		t.Fatalf("client key = %q, %v", b, err)
	}

	c := baseBekup()
	if b, err := c.DatabaseTLSClientKey(); b != nil || err != nil {
		t.Fatalf("no key configured: %q, %v", b, err)
	}
	c.Database.TLSClientKeyFile = writeFile(t, dir, "open.key", "k", 0o644)
	if _, err := c.DatabaseTLSClientKey(); err == nil {
		t.Fatal("world-readable key accepted")
	}
}
