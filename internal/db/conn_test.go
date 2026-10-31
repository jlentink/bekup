package db

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/logging"
)

const specialPassword = `p@ss'w"o\rd $1;--%:/?#&= x`

func fieldsCreds() config.DBCredentials {
	return config.DBCredentials{Host: "db.example.internal", Port: 6432, Database: "bekup_prod", User: "bekup_app", Password: []byte(specialPassword)}
}

func verifyFull() TLSOptions {
	return TLSOptions{Mode: config.DBTLSVerifyFull, RootCAs: x509.NewCertPool()}
}

func TestConnConfigFields(t *testing.T) {
	reg := logging.NewRegistry()
	cc, err := connConfig(fieldsCreds(), verifyFull(), reg)
	if err != nil {
		t.Fatal(err)
	}
	if cc.Host != "db.example.internal" || cc.Port != 6432 || cc.Database != "bekup_prod" || cc.User != "bekup_app" {
		t.Fatalf("unexpected target %s:%d/%s as %s", cc.Host, cc.Port, cc.Database, cc.User)
	}
	if cc.Password != specialPassword {
		t.Fatal("password changed on the way (no escaping must apply in fields mode)")
	}
	if cc.TLSConfig == nil || cc.TLSConfig.ServerName != "db.example.internal" || cc.TLSConfig.InsecureSkipVerify {
		t.Fatalf("TLS %+v", cc.TLSConfig)
	}
	if len(cc.Fallbacks) != 0 || cc.ChannelBinding != "require" || cc.RequireAuth != requireAuth {
		t.Fatalf("fallbacks %d, channel binding %q, require_auth %q", len(cc.Fallbacks), cc.ChannelBinding, cc.RequireAuth)
	}
	if got := cc.RuntimeParams; len(got) != 1 || got["search_path"] != "public" {
		t.Fatalf("runtime params %v", got)
	}
	if cc.AfterConnect != nil || cc.ValidateConnect != nil {
		t.Fatal("unexpected hooks")
	}
	if strings.Contains(reg.Scrub("x "+specialPassword+" y"), "p@ss") {
		t.Error("password not registered for redaction")
	}
}

func TestConnConfigServerName(t *testing.T) {
	creds := fieldsCreds()
	creds.Host = "10.0.0.5"
	o := verifyFull()
	o.ServerName = "db.example.internal"
	cc, err := connConfig(creds, o, logging.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if cc.Host != "10.0.0.5" || cc.TLSConfig.ServerName != "db.example.internal" {
		t.Fatalf("host %s, server name %s", cc.Host, cc.TLSConfig.ServerName)
	}
	// The override only exists for verify-full.
	for _, mode := range []string{config.DBTLSRequire, config.DBTLSDisable} {
		if _, err := connConfig(creds, TLSOptions{Mode: mode, ServerName: "db"}, logging.NewRegistry()); err == nil {
			t.Errorf("%s with server name accepted", mode)
		}
	}
	// URL mode too.
	cc, err = connConfig(config.DBCredentials{URL: []byte("postgres://bekup_app:pw-0123456789@10.0.0.5/bekup")}, o, logging.NewRegistry())
	if err != nil || cc.TLSConfig.ServerName != "db.example.internal" {
		t.Fatalf("URL mode server name: %v", err)
	}
}

func TestConnConfigTLSMatrix(t *testing.T) {
	tests := []struct {
		name               string
		o                  TLSOptions
		tls, skip, hasHook bool
		channelBinding     string
	}{
		{"verify-full", verifyFull(), true, false, false, "require"},
		{"require with CA", TLSOptions{Mode: config.DBTLSRequire, RootCAs: x509.NewCertPool()}, true, true, true, "require"},
		{"require without CA", TLSOptions{Mode: config.DBTLSRequire}, true, true, false, "require"},
		{"disable", TLSOptions{Mode: config.DBTLSDisable}, false, false, false, "disable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cc, err := connConfig(fieldsCreds(), tc.o, logging.NewRegistry())
			if err != nil {
				t.Fatal(err)
			}
			if (cc.TLSConfig != nil) != tc.tls || cc.ChannelBinding != tc.channelBinding || cc.RequireAuth != requireAuth {
				t.Fatalf("tls %v, channel binding %q", cc.TLSConfig != nil, cc.ChannelBinding)
			}
			if tc.tls && (cc.TLSConfig.InsecureSkipVerify != tc.skip || (cc.TLSConfig.VerifyConnection != nil) != tc.hasHook) {
				t.Fatalf("skip %v hook %v", cc.TLSConfig.InsecureSkipVerify, cc.TLSConfig.VerifyConnection != nil)
			}
		})
	}
	if _, err := connConfig(fieldsCreds(), TLSOptions{}, logging.NewRegistry()); err == nil {
		t.Fatal("zero TLS options accepted")
	}
	if _, err := connConfig(fieldsCreds(), TLSOptions{Mode: config.DBTLSVerifyFull}, logging.NewRegistry()); err == nil {
		t.Fatal("verify-full without CA accepted")
	}
}

func TestConnConfigFieldsIncomplete(t *testing.T) {
	for name, mutate := range map[string]func(*config.DBCredentials){
		"no host":     func(c *config.DBCredentials) { c.Host = "" },
		"no port":     func(c *config.DBCredentials) { c.Port = 0 },
		"no database": func(c *config.DBCredentials) { c.Database = "" },
		"no user":     func(c *config.DBCredentials) { c.User = "" },
		"no password": func(c *config.DBCredentials) { c.Password = []byte{} },
		"socket":      func(c *config.DBCredentials) { c.Host = "/var/run/postgresql" },
	} {
		c := fieldsCreds()
		mutate(&c)
		_, err := connConfig(c, verifyFull(), logging.NewRegistry())
		if err == nil {
			t.Errorf("%s accepted", name)
		} else if strings.Contains(err.Error(), "p@ss") {
			t.Errorf("%s: error leaks the password: %v", name, err)
		}
	}
}

func TestConnConfigSetRole(t *testing.T) {
	c := fieldsCreds()
	c.User, c.SetRole = "acct", AppRole
	cc, err := connConfig(c, verifyFull(), logging.NewRegistry())
	if err != nil || cc.AfterConnect == nil {
		t.Fatalf("SET ROLE hook missing: %v", err)
	}
	c.SetRole = "postgres"
	if _, err := connConfig(c, verifyFull(), logging.NewRegistry()); err == nil {
		t.Fatal("SET ROLE to an arbitrary role accepted")
	}
	if s, err := setRoleStatement(OwnerRole); err != nil || s != `SET ROLE "bekup_owner"` {
		t.Fatalf("%q %v", s, err)
	}
}

func TestConnConfigURLParams(t *testing.T) {
	for _, q := range []string{
		"options=-csearch_path%3Devil", "search_path=evil", "role=postgres", "service=x", "servicefile=/tmp/x",
		"passfile=/tmp/p", "pool_max_conns=5", "require_auth=password", "require_auth=scram-sha-256,password",
		"require_auth=!password", "sslsni=0", "krbsrvname=x", "session_authorization=postgres",
		"default_query_exec_mode=simple_protocol", "statement_cache_capacity=0", "timezone=UTC",
		"default_transaction_read_only=off", "statement_timeout=0", "gssencmode=disable", "min_protocol_version=3.0",
		"application_name=a&application_name=b", "X-Odd%20Name=1", "host=evil.example", "port=1", "dbname=x", "user=x",
	} {
		u := "postgres://bekup_app:hunter2-pw-012345@db/bekup?" + q
		_, err := connConfig(config.DBCredentials{URL: []byte(u)}, verifyFull(), logging.NewRegistry())
		if err == nil {
			t.Errorf("%s accepted", q)
		} else if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%s: error leaks the password: %v", q, err)
		}
	}
	cc, err := connConfig(config.DBCredentials{URL: []byte("postgres://bekup_app:hunter2-pw-012345@db/bekup?require_auth=scram-sha-256&application_name=x" +
		"&sslmode=require&channel_binding=require&connect_timeout=5&target_session_attrs=read-write")}, verifyFull(), logging.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if cc.RuntimeParams["search_path"] != "public" || cc.RuntimeParams["application_name"] != "x" || len(cc.RuntimeParams) != 2 || cc.RequireAuth != requireAuth {
		t.Fatalf("params %v require_auth %q", cc.RuntimeParams, cc.RequireAuth)
	}
}

// hostileEnv sets every PG* variable pgx reads (and a service file that
// would redirect the connection) to attacker-chosen values.
func hostileEnv(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	svc := filepath.Join(dir, "pg_service.conf")
	if err := os.WriteFile(svc, []byte("[evil]\nhost=evil.example\nport=1\nuser=evil\ndbname=evil\noptions=-csearch_path=evil\nsslmode=disable\nkrbsrvname=evil\n"+
		"statement_cache_capacity=1\ndescription_cache_capacity=1\ndefault_query_exec_mode=simple_protocol\n"+
		"session_authorization=postgres\nrole=postgres\nsearch_path=evil\napplication_name=evil\ntarget_session_attrs=read-only\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pass := filepath.Join(dir, "pgpass")
	if err := os.WriteFile(pass, []byte("*:*:*:*:evil-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"PGHOST": "evil.example", "PGPORT": "1", "PGDATABASE": "evil", "PGUSER": "evil", "PGPASSWORD": "evil-password",
		"PGPASSFILE": pass, "PGAPPNAME": "evil", "PGCONNECT_TIMEOUT": "1", "PGSSLMODE": "disable",
		"PGSSLKEY": filepath.Join(dir, "k"), "PGSSLCERT": filepath.Join(dir, "c"), "PGSSLSNI": "0",
		"PGSSLROOTCERT": filepath.Join(dir, "r"), "PGSSLPASSWORD": "evil", "PGSSLNEGOTIATION": "direct",
		"PGTARGETSESSIONATTRS": "read-only", "PGSERVICE": "evil", "PGSERVICEFILE": svc, "PGTZ": "Antarctica/Troll",
		"PGOPTIONS": "-c search_path=evil", "PGMINPROTOCOLVERSION": "3.0", "PGMAXPROTOCOLVERSION": "latest",
		"PGCHANNELBINDING": "disable", "PGREQUIREAUTH": "password",
	} {
		t.Setenv(k, v)
	}
}

// TestConnConfigIgnoresPGEnvironment: a fields-mode config built under a
// hostile PG* environment equals the one built under a clean environment,
// field by field (reflection over pgx.ConnConfig and pgconn.Config, so a new
// pgx field is covered automatically; function fields are compared by
// presence).
func TestConnConfigIgnoresPGEnvironment(t *testing.T) {
	clean := func() *pgx.ConnConfig {
		cc, err := connConfig(fieldsCreds(), verifyFull(), logging.NewRegistry())
		if err != nil {
			t.Fatal(err)
		}
		return cc
	}
	for _, k := range os.Environ() {
		if name, _, _ := strings.Cut(k, "="); strings.HasPrefix(name, "PG") {
			t.Setenv(name, "")
			_ = os.Unsetenv(name)
		}
	}
	want := clean()
	hostileEnv(t)
	got := clean()
	compareStruct(t, "ConnConfig", reflect.ValueOf(*want), reflect.ValueOf(*got))
	if got.Host != "db.example.internal" || got.User != "bekup_app" || got.Password != specialPassword ||
		got.RuntimeParams["search_path"] != "public" || got.ValidateConnect != nil || got.SSLNegotiation != "" {
		t.Fatal("hostile environment leaked into the config")
	}

	if got.DefaultQueryExecMode != pgx.QueryExecModeCacheStatement || got.StatementCacheCapacity != defaultStatementCacheCapacity ||
		got.DescriptionCacheCapacity != defaultDescriptionCacheCapacity || got.Tracer != nil {
		t.Fatalf("service file settings leaked: exec mode %v, caches %d/%d", got.DefaultQueryExecMode, got.StatementCacheCapacity, got.DescriptionCacheCapacity)
	}

	// URL mode under the same environment: no run-time parameter and none
	// of pgx's own settings from the service file. (pgx reads TLS files
	// named by PG* variables while parsing a URL, so those go.)
	for _, k := range []string{"PGSSLKEY", "PGSSLCERT", "PGSSLROOTCERT", "PGSSLNEGOTIATION", "PGSSLMODE", "PGSSLSNI"} {
		t.Setenv(k, "")
	}
	uc, err := connConfig(config.DBCredentials{URL: []byte("postgres://bekup_app:hunter2-pw-012345@db.example.internal/bekup")}, verifyFull(), logging.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if len(uc.RuntimeParams) != 1 || uc.RuntimeParams["search_path"] != "public" ||
		uc.DefaultQueryExecMode != pgx.QueryExecModeCacheStatement || uc.StatementCacheCapacity != defaultStatementCacheCapacity ||
		uc.DescriptionCacheCapacity != defaultDescriptionCacheCapacity || uc.KerberosSrvName != "" || uc.ValidateConnect != nil {
		t.Fatalf("URL mode leaked: params %v, exec mode %v, caches %d/%d", uc.RuntimeParams, uc.DefaultQueryExecMode, uc.StatementCacheCapacity, uc.DescriptionCacheCapacity)
	}

	// The pool and connector configs come from the same builder.
	pc, err := poolConfig(fieldsCreds(), PoolOptions{TLS: verifyFull(), Registry: logging.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	if pc.ConnConfig.Host != "db.example.internal" || pc.ConnConfig.RuntimeParams["application_name"] != DefaultApplicationName ||
		pc.ConnConfig.RuntimeParams["search_path"] != "public" {
		t.Fatalf("pool config %v", pc.ConnConfig.RuntimeParams)
	}
}

func compareStruct(t *testing.T, path string, a, b reflect.Value) {
	t.Helper()
	for i := range a.NumField() {
		f := a.Type().Field(i)
		name := path + "." + f.Name
		av, bv := a.Field(i), b.Field(i)
		switch {
		case f.Anonymous && f.Type == reflect.TypeFor[pgconn.Config]():
			compareStruct(t, name, av, bv)
		case av.Kind() == reflect.Func:
			if av.IsNil() != bv.IsNil() {
				t.Errorf("%s: presence differs", name)
			}
		case f.Name == "TLSConfig":
			if av.IsNil() != bv.IsNil() || (!av.IsNil() && av.Elem().FieldByName("ServerName").String() != bv.Elem().FieldByName("ServerName").String()) {
				t.Errorf("%s differs", name)
			}
		case !f.IsExported():
			// unexported bookkeeping (createdByParseConfig, caches)
		default:
			if !reflect.DeepEqual(av.Interface(), bv.Interface()) {
				t.Errorf("%s differs: %v vs %v", name, av.Interface(), bv.Interface())
			}
		}
	}
}
