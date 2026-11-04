package config

import (
	"strings"
	"testing"
)

// validExternalTOML is the smallest external-api.toml that validates
// outside dev mode.
const validExternalTOML = `
[listen]
address = "10.0.0.11:8081"
trusted_proxies = ["10.0.0.5/32"]
`

func loadExternal(t *testing.T, content string, env map[string]string) (*Result[ExternalAPI], error) {
	t.Helper()
	p := writeFile(t, safeDir(t), ExternalAPIFileName, content, 0o600)
	return LoadExternalAPI(opts([]string{p}, env))
}

func TestExternalAPIDefaults(t *testing.T) {
	res, err := loadExternal(t, validExternalTOML, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := res.Config
	if c.Database.URLFile != "/run/secrets/bekup_external_database_url" || c.Database.OwnerURLFile != "" ||
		c.Database.TLSMode != DBTLSVerifyFull || c.Limits.FailedAuthLimit != 10 || c.Limits.FailedAuthWindow.D().Minutes() != 5 ||
		c.Limits.MaxBodyBytes != 16<<10 || c.Database.FieldsMode() {
		t.Fatalf("defaults: %+v", c)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("warnings: %v", res.Warnings)
	}
}

func TestExternalAPIValidation(t *testing.T) {
	for _, tc := range []struct {
		name, toml string
		env        map[string]string
		want       []string
	}{
		{"no proxy outside dev", "[listen]\naddress = \"10.0.0.11:8081\"\n", nil, []string{"listen.trusted_proxies: is required outside dev mode"}},
		{"wildcard", "[listen]\naddress = \":8081\"\ntrusted_proxies = [\"10.0.0.5/32\"]\n", nil, []string{"listens on every interface", "listen.allow_wildcard"}},
		{"proxy host ip", validExternalTOML + "trusted_proxy_hosts = [\"10.0.0.7\"]\n", nil, []string{"put it into listen.trusted_proxies"}},
		{"proxy host reserved", validExternalTOML + "trusted_proxy_hosts = [\"bekup-external-api\"]\n", nil, []string{"one of bekup's own services"}},
		{"proxy any", "[listen]\naddress = \"10.0.0.11:8081\"\ntrusted_proxies = [\"0.0.0.0/0\"]\n", nil, []string{"matches every address"}},
		{"owner url file", validExternalTOML + "[database]\nowner_url_file = \"/run/secrets/owner\"\n", nil, []string{"database.owner_url_file: is not used by the external API"}},
		{"owner password env", validExternalTOML, map[string]string{"BEKUP_DATABASE_OWNER_PASSWORD_FILE": "/run/secrets/o"}, []string{"database.owner_password_file: is not used"}},
		{"single account", validExternalTOML + "[database]\nhost = \"db.example.com\"\npassword_file = \"/run/secrets/p\"\nsingle_account = true\n", nil, []string{"single-account mode does not support the external API"}},
		{"fields app user", validExternalTOML + "[database]\nhost = \"db.example.com\"\npassword_file = \"/run/secrets/p\"\nuser = \"bekup_app\"\n", nil, []string{"database.user: must be bekup_external"}},
		{"fields with url", validExternalTOML + "[database]\nhost = \"db.example.com\"\npassword_file = \"/run/secrets/p\"\nurl_file = \"/run/secrets/u\"\n", nil, []string{"cannot be combined with database.host"}},
		{"fields key in url mode", validExternalTOML + "[database]\nuser = \"bekup_external\"\n", nil, []string{"database.user: is a fields-mode key"}},
		{"fields no password", validExternalTOML + "[database]\nhost = \"db.example.com\"\n", nil, []string{"database.password_file: is required"}},
		{"max conns", validExternalTOML + "[database]\nmax_conns = 21\n", nil, []string{"the connection limit of the bekup_external role"}},
		{"verify-full needs ca", validExternalTOML + "[database]\ntls_ca_file = \"\"\n", nil, []string{"database.tls_ca_file: is required"}},
		{"body", validExternalTOML + "[limits]\nmax_body_bytes = 100000\n", nil, []string{"limits.max_body_bytes"}},
		{"failed auth", validExternalTOML + "[limits]\nfailed_auth_limit = 0\nfailed_auth_window = \"10s\"\n", nil, []string{"limits.failed_auth_limit", "limits.failed_auth_window"}},
		{"rates", validExternalTOML + "[limits]\nrate_limit_per_second = 0.0\ntoken_rate_limit_burst = 0\n", nil, []string{"limits.rate_limit_per_second", "limits.token_rate_limit_burst"}},
		{"logging", validExternalTOML + "[logging]\nlevel = \"loud\"\n", nil, []string{"logging.level"}},
		{"unknown key", validExternalTOML + "[security]\nmaster_key_file = \"/run/secrets/k\"\n", nil, []string{"unknown key"}},
		{"inline url outside dev", validExternalTOML + "[database]\nurl = \"postgres://x\"\n", nil, []string{"inline secrets are rejected outside dev mode"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadExternal(t, tc.toml, tc.env)
			requireErrContains(t, err, tc.want...)
		})
	}
}

func TestExternalAPIFieldsMode(t *testing.T) {
	dir := safeDir(t)
	pw := writeFile(t, dir, "pw", "s3cr3t-external\n", 0o600)
	res, err := loadExternal(t, validExternalTOML+"[database]\nhost = \"db.example.com\"\npassword_file = \""+pw+"\"\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	d := res.Config.Database
	if !d.FieldsMode() || d.User != DBExternalRole || d.URLFile != "" {
		t.Fatalf("fields mode: %+v", d)
	}
	creds, err := res.Config.DBCredentials()
	if err != nil {
		t.Fatal(err)
	}
	defer creds.Zero()
	if creds.User != DBExternalRole || string(creds.Password) != "s3cr3t-external" || creds.SetRole != "" || creds.Host != "db.example.com" {
		t.Fatalf("credentials: %v", creds)
	}
}

func TestExternalAPIDevAndWarnings(t *testing.T) {
	o := opts(nil, map[string]string{"BEKUP_DEV": "1"})
	res, err := LoadExternalAPI(o)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Dev || !strings.Contains(strings.Join(res.Warnings, "\n"), "DEV MODE") {
		t.Fatalf("dev: %v %v", res.Dev, res.Warnings)
	}
	res, err = loadExternal(t, "[listen]\naddress = \"0.0.0.0:8081\"\nallow_wildcard = true\ntrusted_proxies = [\"10.0.0.5/32\"]\n[database]\ntls_mode = \"disable\"\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	w := strings.Join(res.Warnings, "\n")
	if !strings.Contains(w, "listens on every interface") || !strings.Contains(w, "TLS disabled") {
		t.Fatalf("warnings: %s", w)
	}
}

func TestExternalAPIEnvOverridesAndOtherFiles(t *testing.T) {
	res, err := loadExternal(t, validExternalTOML, map[string]string{
		"BEKUP_LIMITS_MAX_BODY_BYTES":      "4096",
		"BEKUP_LISTEN_TRUSTED_PROXY_HOSTS": "npm",
		// A bekup.toml key: warned, never applied.
		"BEKUP_SECURITY_MASTER_KEY_FILE": "/run/secrets/bekup_master_key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Limits.MaxBodyBytes != 4096 || res.Sources["limits.max_body_bytes"] != SourceEnv ||
		len(res.Config.Listen.TrustedProxyHosts) != 1 {
		t.Fatalf("overrides: %+v", res.Config)
	}
	if w := strings.Join(res.Warnings, "\n"); !strings.Contains(w, "BEKUP_SECURITY_MASTER_KEY_FILE configures another config file") {
		t.Fatalf("warnings: %s", w)
	}
}

func TestExternalAPIReference(t *testing.T) {
	var b strings.Builder
	if err := WriteReference(&b); err != nil {
		t.Fatal(err)
	}
	ref := b.String()
	_, ext, ok := strings.Cut(ref, "### `external-api.toml`")
	if !ok {
		t.Fatal("no external-api.toml section")
	}
	for _, want := range []string{"`BEKUP_LIMITS_FAILED_AUTH_LIMIT`", "must be bekup_external", "Not used: the external API never migrates"} {
		if !strings.Contains(ext, want) {
			t.Errorf("reference lacks %q", want)
		}
	}
	if strings.Contains(ext, "login role of serve and worker") {
		t.Error("reference shows bekup.toml's description of database.user")
	}
}
