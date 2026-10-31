//go:build integration

package app_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/app"
	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/externaltest"
	"github.com/jlentink/bekup/internal/logging"
)

// TestServeSingleAccountExternal: serve on an external PostgreSQL in
// single-account mode (D-207) starts through SET ROLE, logs the TLS facts
// and the weakening, and records security.insecure_db_roles outside dev mode
// (fail closed), with the login role's findings.
func TestServeSingleAccountExternal(t *testing.T) {
	ctx := t.Context()
	s := externaltest.Start(t, externaltest.PG17, externaltest.Options{})
	const acct, acctPW = "acct_single", "acct-password-0123456789"
	if err := s.CreateAdmin(ctx, acct, acctPW); err != nil {
		t.Fatal(err)
	}
	cfgDB := config.DefaultBekup()
	cfgDB.Database.TLSCAFile = s.CAFile
	tlsOpts, err := db.LoadTLSOptions(&cfgDB)
	if err != nil {
		t.Fatal(err)
	}
	creds := func(role string) config.DBCredentials {
		return config.DBCredentials{Host: s.Host, Port: s.Port, Database: "bekup", User: acct, Password: []byte(acctPW), SetRole: role}
	}
	admin := creds("")
	admin.Database = "postgres"
	if _, err := db.Bootstrap(ctx, db.BootstrapOptions{Admin: admin, TLS: tlsOpts, Database: "bekup", SingleAccount: true, Registry: logging.NewRegistry()}); err != nil {
		t.Fatal(err)
	}
	m, err := db.NewMigrator(ctx, creds(db.OwnerRole), tlsOpts, logging.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(ctx); err != nil {
		t.Fatal(err)
	}

	pwFile := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(pwFile, []byte(acctPW), 0o600); err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	o := options(t, "external-single-account", logs)
	d := &o.Config.Database
	d.URL, d.URLFile, d.OwnerURLFile = "", "", ""
	d.Host, d.Port, d.Name, d.User, d.PasswordFile, d.SingleAccount = s.Host, int(s.Port), "bekup", acct, pwFile, true
	d.TLSCAFile = s.CAFile
	o.Dev = false
	o.Config.Server.BaseURL = "https://backup.example.com"
	o.Config.Server.TrustedProxies = []string{"10.0.0.1/32"}
	a, err := app.New(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	a.Close()
	out := logs.String()
	for _, want := range []string{`"msg":"app database connected"`, `"config_mode":"single_account"`, `"host_verified":true`, "single-account mode"} {
		if !strings.Contains(out, want) {
			t.Errorf("log misses %s", want)
		}
	}
	if strings.Contains(out, acctPW) {
		t.Fatal("password in the log")
	}

	app2 := creds(db.AppRole)
	pool, err := db.NewPool(ctx, app2, db.PoolOptions{TLS: tlsOpts, Registry: logging.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	page, err := audit.New(pool, audit.Options{}).List(ctx, audit.Filter{Action: audit.ActionSecurityInsecureDBRoles}, 0, 10)
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("audit: %v %+v", err, page)
	}
	var details map[string]any
	if err := json.Unmarshal(page.Entries[0].Details, &details); err != nil {
		t.Fatal(err)
	}
	if details["single_account"] != true || !strings.Contains(string(page.Entries[0].Details), "CREATEROLE") || details["process"] != "serve" {
		t.Fatalf("details %s", page.Entries[0].Details)
	}
}
