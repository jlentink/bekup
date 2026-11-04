package setup

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/config"
)

// The opt-in external API in setup (D-220 amendment 4).

func TestComposeProfiles(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		v    Variant
		p    Proxy
		ext  bool
		want string
	}{
		{VariantTraefik, ProxyTraefik, false, ""},
		{VariantTraefik, ProxyTraefik, true, "external-api"},
		{VariantOwnProxy, ProxyExternal, true, "external-api"},
		{VariantFull, ProxyNginx, false, "nginx"},
		{VariantFull, ProxyNginx, true, "nginx,external-api"},
		{VariantFull, ProxyNone, true, "external-api"},
		{VariantFull, ProxyNone, false, ""},
	} {
		if got := composeProfiles(tc.v, tc.p, tc.ext); got != tc.want {
			t.Errorf("%s/%s/%v: %q, want %q", tc.v, tc.p, tc.ext, got, tc.want)
		}
	}
	if got := profileList(" nginx, ,external-api,nginx"); !slices.Equal(got, []string{"external-api", "nginx"}) {
		t.Fatalf("profileList %v", got)
	}
	for cur, want := range map[string]string{"": "external-api", "nginx": "nginx,external-api", "nginx,": "nginx,external-api", "external-api,nginx": "external-api,nginx"} {
		if got := withProfile(cur, ExternalAPIProfile); got != want {
			t.Errorf("withProfile(%q) = %q, want %q", cur, got, want)
		}
	}
}

// externalAPICase is one deployment shape with the external API enabled.
type externalAPICase struct {
	name string
	// variant: compose-first release file ("" = setup-first with answers).
	variant Variant
	answers func(t *testing.T) Answers
	mut     func(*Options)
	db      bool // external database
	// want: external-api.toml listen address and trusted proxies/hosts.
	listen       string
	wildcard     bool
	trusted      []string
	trustedHosts []string
	profiles     string
	// route: the printed /api/v1/ target ("" = the bundled proxy routes it).
	route string
}

func externalAPICases() []externalAPICase {
	ext := func(o *Options) { o.Answers.ExternalAPI = true }
	return []externalAPICase{
		{name: "traefik", variant: VariantTraefik, mut: ext, listen: "10.213.250.11:8081", trusted: []string{"10.213.250.5/32"}, profiles: "external-api"},
		{name: "traefik-external-db", variant: VariantTraefik, mut: ext, db: true, listen: "10.213.250.11:8081", trusted: []string{"10.213.250.5/32"}, profiles: "external-api"},
		{name: "own-proxy-join", variant: VariantOwnProxy, mut: ext, listen: config.ExternalAPIJoinListen, wildcard: true, trusted: []string{}, trustedHosts: []string{"npm"}, profiles: "external-api", route: "http://bekup-external-api:8081"},
		{name: "own-proxy-link", variant: VariantOwnProxy, mut: func(o *Options) {
			ext(o)
			o.Answers.ProxyNetwork, o.Answers.ProxyContainer, o.Answers.ProxyMode = "", "", ProxyLink
		}, listen: config.ExternalAPIJoinListen, wildcard: true, trusted: []string{"10.213.253.5/32"}, profiles: "external-api", route: "http://bekup-external-api:8081"},
		{name: "own-proxy-labels-db-container", variant: VariantOwnProxy, db: true, mut: func(o *Options) {
			ext(o)
			o.Answers.ProxyLabels = true
			o.Answers.DB.Location, o.Answers.DB.Network, o.Answers.DB.Host, o.Answers.DB.TLS, o.Answers.DB.CAFile = DBDocker, "dbnet", "postgres", DBTLSRequire, ""
		}, listen: config.ExternalAPIJoinListen, wildcard: true, trusted: []string{}, trustedHosts: []string{"npm"}, profiles: "external-api", route: "http://bekup-external-api:8081"},
		{name: "full-nginx-custom-ip", answers: func(t *testing.T) Answers {
			a := testAnswers(t, ProxyNginx)
			a.Variant, a.ExternalAPI = VariantFull, true
			a.Network.ExternalAPIFrontendIP = "10.213.250.20"
			return a
		}, listen: "10.213.250.20:8081", trusted: []string{"10.213.250.5/32"}, profiles: "nginx,external-api"},
		{name: "full-none", answers: func(t *testing.T) Answers {
			a := testAnswers(t, ProxyNone)
			a.Variant, a.ExternalAPI = VariantFull, true
			return a
		}, listen: "10.213.250.11:8081", trusted: []string{"10.213.250.6/32"}, profiles: "external-api", route: "http://10.213.250.11:8081"},
	}
}

// initExternalAPI writes the deployment of c into a new directory.
func initExternalAPI(t *testing.T, c externalAPICase) (string, string) {
	t.Helper()
	dir := t.TempDir()
	var dbAnswers Database
	if c.db {
		testDBCA(t, dir, "db-ca.pem", time.Now(), 0)
		dbAnswers = Database{Mode: DBExternal, Host: "db.example.com", CAFile: "db-ca.pem"}
	}
	if c.variant == "" {
		a := c.answers(t)
		a.DB = dbAnswers
		return dir, mustInit(t, dir, a).String()
	}
	writeReleaseDB(t, dir, c.variant, map[bool]DBMode{false: DBBundled, true: DBExternal}[c.db], variantImage)
	out, err := composeFirstInit(t, dir, c.variant, func(o *Options) {
		o.Answers.DB = dbAnswers
		c.mut(o)
	})
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	return dir, out.String()
}

// TestInitExternalAPI: --external-api writes external-api.toml trusting the
// proxy serve trusts, the .env lines of the profile and the proxy route,
// the role's secrets (always) and the state; setup check passes; the
// report names your proxy's route and the bootstrap flag.
func TestInitExternalAPI(t *testing.T) {
	t.Parallel()
	for _, c := range externalAPICases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir, out := initExternalAPI(t, c)
			env, bad := parseDotEnv(readFile(t, dir, EnvFile))
			if len(bad) > 0 || env[ExternalAPIEnvVar] != ExternalAPIEnabled || env["COMPOSE_PROFILES"] != c.profiles {
				t.Fatalf(".env %v", env)
			}
			res, err := config.LoadExternalAPI(config.Options{LookupEnv: noEnv, Environ: func() []string { return nil }, SearchPaths: []string{}, ConfigPath: filepath.Join(dir, ExternalAPITOML)})
			if err != nil {
				t.Fatalf("external-api.toml: %v\n%s", err, readFile(t, dir, ExternalAPITOML))
			}
			l := res.Config.Listen
			if l.Address != c.listen || l.AllowWildcard != c.wildcard || !slices.Equal(l.TrustedProxies, c.trusted) || !slices.Equal(nonNil(l.TrustedProxyHosts), nonNil(c.trustedHosts)) {
				t.Fatalf("listen %+v", l)
			}
			d := res.Config.Database
			if c.db {
				if !d.FieldsMode() || d.User != config.DBExternalRole || d.PasswordFile != ExternalDBPasswordTarget || d.Name != "bekup" {
					t.Fatalf("database %+v", d)
				}
				if _, err := os.Stat(pathIn(dir, "secrets/"+extDBPasswordName)); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(out, "db bootstrap --admin-user") || !strings.Contains(out, externalRoleFlag) {
					t.Fatalf("report lacks the bootstrap flag:\n%s", out)
				}
			} else {
				if d.FieldsMode() || d.URLFile != extURLTarget {
					t.Fatalf("database %+v", d)
				}
				for _, f := range []string{extPassName, extURLName} {
					if _, err := os.Stat(pathIn(dir, "secrets/"+f)); err != nil {
						t.Fatal(err)
					}
				}
			}
			st := mustState(t, dir)
			if !st.ExternalAPI || !slices.Contains(st.SecretGroups, groupExternalAPI) {
				t.Fatalf("state %+v", st)
			}
			if !strings.Contains(out, "external API: enabled, https://backup.example.com/api/v1/") {
				t.Fatalf("report:\n%s", out)
			}
			if c.route != "" && (!strings.Contains(out, "Route https://backup.example.com/api/v1/ to the external API at "+c.route) || !strings.Contains(out, "location ^~ /api/v1/")) {
				t.Fatalf("report lacks the route to %s:\n%s", c.route, out)
			}
			if c.route == "" && strings.Contains(out, "Route https://") {
				t.Fatalf("a bundled proxy routes /api/v1/ itself:\n%s", out)
			}
			if fs, report, err := runCheck(t, dir, nil); err != nil || hasErrors(fs) {
				t.Fatalf("check: %v\n%s", err, report)
			}
		})
	}
}

// TestInitWithoutExternalAPI: the secrets exist (compose mounts them), the
// rest does not.
func TestInitWithoutExternalAPI(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustInit(t, dir, testAnswers(t, ProxyTraefik))
	for _, f := range []string{extPassName, extURLName} {
		if _, err := os.Stat(pathIn(dir, "secrets/"+f)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(pathIn(dir, ExternalAPITOML)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("external-api.toml: %v", err)
	}
	env, _ := parseDotEnv(readFile(t, dir, EnvFile))
	if _, ok := env[ExternalAPIEnvVar]; ok || strings.Contains(env["COMPOSE_PROFILES"], ExternalAPIProfile) {
		t.Fatalf(".env %v", env)
	}
	if st := mustState(t, dir); st.ExternalAPI {
		t.Fatal("state enables the external API")
	}
}

func TestInitExternalAPIRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		mut  func(*Answers)
		want string
	}{
		{"single account", func(a *Answers) {
			a.ExternalAPI = true
			a.DB = Database{Mode: DBExternal, Host: "db.example.com", CAFile: "db-ca.pem", Auth: DBAuthSingleAccount, User: "acct", PasswordFile: "acct.pw"}
		}, "single-account mode does not support the external API"},
		{"ip without the API", func(a *Answers) { a.Network.ExternalAPIFrontendIP = "10.213.250.20" }, "applies to --external-api only"},
		{"ip outside the subnet", func(a *Answers) {
			a.ExternalAPI = true
			a.Network.ExternalAPIFrontendIP = "10.9.9.9"
		}, "not inside 10.213.250.0/24"},
		{"ip of serve", func(a *Answers) {
			a.ExternalAPI = true
			a.Network.ExternalAPIFrontendIP = "10.213.250.10"
		}, "must differ from serve-ip and proxy-ip"},
		{"ip with own proxy", func(a *Answers) {
			a.ExternalAPI = true
			a.Variant, a.Proxy, a.TLSMode, a.ACMEEmail = VariantOwnProxy, "", "", ""
			a.ProxyNetwork, a.ProxyContainer = "web", "npm"
			a.Network.ExternalAPIFrontendIP = "10.213.250.20"
		}, "has no frontend network"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := testAnswers(t, ProxyTraefik)
			a.Variant = VariantFull
			tc.mut(&a)
			err := a.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate: %v, want %q", err, tc.want)
			}
		})
	}
}

// TestCheckExternalAPIConsistency: profile on <=> BEKUP_EXTERNAL_API=enabled
// <=> external-api.toml, which trusts exactly the proxy serve trusts.
func TestCheckExternalAPIConsistency(t *testing.T) {
	t.Parallel()
	enabled := externalAPICases()[0] // traefik
	editEnv := func(t *testing.T, dir, key, value string) {
		t.Helper()
		next, err := setEnvLine(readFile(t, dir, EnvFile), key, value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pathIn(dir, EnvFile), next, 0o644); err != nil { //nolint:gosec // G306: .env is public (manifest mode 0644)
			t.Fatal(err)
		}
	}
	editTOML := func(t *testing.T, dir, old, repl string) {
		t.Helper()
		s := string(readFile(t, dir, ExternalAPITOML))
		if !strings.Contains(s, old) {
			t.Fatalf("external-api.toml lacks %q", old)
		}
		if err := os.WriteFile(pathIn(dir, ExternalAPITOML), []byte(strings.Replace(s, old, repl, 1)), 0o644); err != nil { //nolint:gosec // G306: manifest mode 0644
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name    string
		enabled bool
		mut     func(t *testing.T, dir string)
		sev     Severity
		want    string
	}{
		{"profile missing", true, func(t *testing.T, dir string) { editEnv(t, dir, "COMPOSE_PROFILES", "") }, SeverityError, "lacks external-api"},
		{"variable missing", true, func(t *testing.T, dir string) { editEnv(t, dir, ExternalAPIEnvVar, "") }, SeverityError, `BEKUP_EXTERNAL_API="", but the external API is enabled`},
		{"variable malformed", true, func(t *testing.T, dir string) { editEnv(t, dir, ExternalAPIEnvVar, "yes") }, SeverityError, `BEKUP_EXTERNAL_API="yes"`},
		{"toml missing", true, func(t *testing.T, dir string) {
			if err := os.Remove(pathIn(dir, ExternalAPITOML)); err != nil {
				t.Fatal(err)
			}
		}, SeverityError, "external-api.toml: missing"},
		{"other trusted proxy", true, func(t *testing.T, dir string) {
			editTOML(t, dir, `trusted_proxies = ["10.213.250.5/32"]`, `trusted_proxies = ["10.213.250.0/24"]`)
		}, SeverityError, "listen.trusted_proxies"},
		{"trusted host added", true, func(t *testing.T, dir string) {
			editTOML(t, dir, `trusted_proxies = ["10.213.250.5/32"]`, "trusted_proxies = [\"10.213.250.5/32\"]\ntrusted_proxy_hosts = [\"evil\"]")
		}, SeverityError, "trusts its proxy by address only"},
		{"other listen address", true, func(t *testing.T, dir string) {
			editTOML(t, dir, `address = "10.213.250.11:8081"`, `address = "10.213.250.12:8081"`)
		}, SeverityError, `listen.address = "10.213.250.12:8081", want "10.213.250.11:8081"`},
		{"other database URL", true, func(t *testing.T, dir string) {
			editTOML(t, dir, `url_file = "/run/secrets/bekup_external_database_url"`, `url_file = "/run/secrets/bekup_database_url"`)
		}, SeverityError, "url_file"},
		{"profile without the API", false, func(t *testing.T, dir string) { editEnv(t, dir, "COMPOSE_PROFILES", "external-api") }, SeverityError, "which setup has not enabled"},
		{"variable without the API", false, func(t *testing.T, dir string) { editEnv(t, dir, ExternalAPIEnvVar, ExternalAPIEnabled) }, SeverityError, "setup has not enabled the external API"},
		{"toml without the API", false, func(t *testing.T, dir string) {
			s := renderExternalAPITOML(externalAPIShape{listen: "10.213.250.11:8081", trustedProxies: []string{"10.213.250.5/32"}})
			if err := os.WriteFile(pathIn(dir, ExternalAPITOML), s, 0o644); err != nil { //nolint:gosec // G306: manifest mode 0644
				t.Fatal(err)
			}
		}, SeverityWarning, "has no effect: the external API is not enabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var dir string
			if tc.enabled {
				dir, _ = initExternalAPI(t, enabled)
			} else {
				dir = t.TempDir()
				writeRelease(t, dir, VariantTraefik, variantImage)
				if _, err := composeFirstInit(t, dir, VariantTraefik, nil); err != nil {
					t.Fatal(err)
				}
			}
			tc.mut(t, dir)
			fs, report, err := runCheck(t, dir, nil)
			if err != nil && !errors.Is(err, ErrCheckFailed) {
				t.Fatal(err)
			}
			if !hasFinding(fs, tc.sev, tc.want) {
				t.Fatalf("no %s %q:\n%s", tc.sev, tc.want, report)
			}
		})
	}
}

// upgradeComposeFirst runs `setup upgrade --external-api` (compose-first,
// same image).
func upgradeComposeFirst(t *testing.T, dir string) (string, error) {
	t.Helper()
	o := upgradeOptions(t, dir, variantImage)
	o.WriteCompose, o.ExternalAPI = false, true
	err := Upgrade(o)
	return bufOf(t, o.Out).String(), err
}

// TestUpgradeExternalAPI: `setup upgrade --external-api` on an existing
// deployment writes external-api.toml and the .env lines and prints the
// remaining steps: the documented role command (bundled) or bootstrap
// (your PostgreSQL), `run --rm migrate` (never `migrate up`: the service's
// command is migrate up already), `up -d`; check passes afterwards; a
// second run changes nothing.
func TestUpgradeExternalAPI(t *testing.T) {
	t.Parallel()
	t.Run("bundled", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeRelease(t, dir, VariantTraefik, variantImage)
		if _, err := composeFirstInit(t, dir, VariantTraefik, nil); err != nil {
			t.Fatal(err)
		}
		before := snapshot(t, dir)
		out, err := upgradeComposeFirst(t, dir)
		if err != nil {
			t.Fatalf("upgrade: %v\n%s", err, out)
		}
		if bad := onlyChanged(before, snapshot(t, dir), StateFileName, EnvFile, ExternalAPITOML); len(bad) > 0 {
			t.Fatalf("unexpected changes: %v", bad)
		}
		for _, want := range []string{
			"docker compose up -d postgres\n",
			bundledRoleCommand + "\n",
			"docker compose run --rm migrate\n",
			"docker compose up -d\n",
			"COMPOSE_PROFILES=external-api",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "run --rm migrate up") || strings.Contains(out, "Route https://") {
			t.Fatalf("output:\n%s", out)
		}
		if i, j := strings.Index(out, bundledRoleCommand), strings.Index(out, "docker compose run --rm migrate\n"); i < 0 || j < i {
			t.Fatal("migrate must follow the role command")
		}
		if st := mustState(t, dir); !st.ExternalAPI {
			t.Fatal("state not enabled")
		}
		if fs, report, err := runCheck(t, dir, nil); err != nil || hasErrors(fs) {
			t.Fatalf("check: %v\n%s", err, report)
		}
		before = snapshot(t, dir)
		if out, err := upgradeComposeFirst(t, dir); err != nil || strings.Contains(out, "Finish it") || !strings.Contains(out, "no changes") {
			t.Fatalf("second upgrade: %v\n%s", err, out)
		}
		if d := diffSnapshots(before, snapshot(t, dir)); len(d) > 0 {
			t.Fatalf("second upgrade changed %v", d)
		}
	})
	t.Run("external database", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		testDBCA(t, dir, "db-ca.pem", time.Now(), 0)
		writeReleaseDB(t, dir, VariantTraefik, DBExternal, variantImage)
		if _, err := composeFirstInit(t, dir, VariantTraefik, func(o *Options) {
			o.Answers.DB = Database{Mode: DBExternal, Host: "db.example.com", CAFile: "db-ca.pem"}
		}); err != nil {
			t.Fatal(err)
		}
		out, err := upgradeComposeFirst(t, dir)
		if err != nil {
			t.Fatalf("upgrade: %v\n%s", err, out)
		}
		if !strings.Contains(out, "db bootstrap --admin-user <admin role> --adopt-existing-roles "+externalRoleFlag) || strings.Contains(out, "docker compose exec postgres") || !strings.Contains(out, "docker compose run --rm migrate\n") {
			t.Fatalf("output:\n%s", out)
		}
		toml := string(readFile(t, dir, ExternalAPITOML))
		if !strings.Contains(toml, `host = "db.example.com"`) || !strings.Contains(toml, `password_file = "`+ExternalDBPasswordTarget+`"`) {
			t.Fatalf("external-api.toml:\n%s", toml)
		}
		if fs, report, err := runCheck(t, dir, nil); err != nil || hasErrors(fs) {
			t.Fatalf("check: %v\n%s", err, report)
		}
	})
	t.Run("own proxy route", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeRelease(t, dir, VariantOwnProxy, variantImage)
		if _, err := composeFirstInit(t, dir, VariantOwnProxy, nil); err != nil {
			t.Fatal(err)
		}
		out, err := upgradeComposeFirst(t, dir)
		if err != nil || !strings.Contains(out, "Route https://backup.example.com/api/v1/ to the external API at http://bekup-external-api:8081") {
			t.Fatalf("upgrade: %v\n%s", err, out)
		}
		if toml := string(readFile(t, dir, ExternalAPITOML)); !strings.Contains(toml, `trusted_proxy_hosts = ["npm"]`) || !strings.Contains(toml, `address = "0.0.0.0:8081"`) {
			t.Fatalf("external-api.toml:\n%s", toml)
		}
		if fs, report, err := runCheck(t, dir, nil); err != nil || hasErrors(fs) {
			t.Fatalf("check: %v\n%s", err, report)
		}
	})
	t.Run("setup-first nginx", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		mustInit(t, dir, testAnswers(t, ProxyNginx))
		o := upgradeOptions(t, dir, variantImage)
		o.ExternalAPI = true
		if err := Upgrade(o); err != nil {
			t.Fatalf("upgrade: %v\n%s", err, o.Out)
		}
		env, _ := parseDotEnv(readFile(t, dir, EnvFile))
		if env["COMPOSE_PROFILES"] != "nginx,external-api" || env[ExternalAPIEnvVar] != ExternalAPIEnabled {
			t.Fatalf(".env %v", env)
		}
		if fs, report, err := runCheck(t, dir, nil); err != nil || hasErrors(fs) {
			t.Fatalf("check: %v\n%s", err, report)
		}
	})
	t.Run("single account", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		testDBCA(t, dir, "db-ca.pem", time.Now(), 0)
		if err := os.WriteFile(pathIn(dir, "acct.pw"), []byte(testAppPW), 0o600); err != nil {
			t.Fatal(err)
		}
		writeReleaseDB(t, dir, VariantTraefik, DBExternal, variantImage)
		if _, err := composeFirstInit(t, dir, VariantTraefik, func(o *Options) {
			o.Answers.DB = Database{Mode: DBExternal, Host: "db.example.com", CAFile: "db-ca.pem", Auth: DBAuthSingleAccount, User: "acct", PasswordFile: "acct.pw"}
		}); err != nil {
			t.Fatal(err)
		}
		before := snapshot(t, dir)
		if out, err := upgradeComposeFirst(t, dir); err == nil || !strings.Contains(err.Error(), "single-account mode does not support the external API") {
			t.Fatalf("upgrade: %v\n%s", err, out)
		}
		if d := diffSnapshots(before, snapshot(t, dir)); len(d) > 0 {
			t.Fatalf("refused upgrade changed %v", d)
		}
	})
}

// TestUpgradeExternalAPIExistingOverride (D-215, D-220 amendment 4): the
// external API must join the database container's network, but setup never
// rewrites an existing compose.override.yaml: upgrade prints the block,
// check reports it until the operator added it, then passes.
func TestUpgradeExternalAPIExistingOverride(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeReleaseDB(t, dir, VariantOwnProxy, DBExternal, variantImage)
	if _, err := composeFirstInit(t, dir, VariantOwnProxy, func(o *Options) {
		o.Answers.DB = Database{Mode: DBExternal, Location: DBDocker, Network: "dbnet", Host: "postgres", TLS: DBTLSRequire}
	}); err != nil {
		t.Fatal(err)
	}
	override := readFile(t, dir, OverrideFile)
	out, err := upgradeComposeFirst(t, dir)
	if err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out)
	}
	if !bytes.Equal(readFile(t, dir, OverrideFile), override) {
		t.Fatal("compose.override.yaml rewritten")
	}
	block := "  external-api:\n    networks:\n      # YOUR database container's network (BEKUP_DB_NETWORK, D-215).\n      database: {}\n"
	if !strings.Contains(out, "Add this block to compose.override.yaml") || !strings.Contains(out, indentBlock(block, "       ")) {
		t.Fatalf("output lacks the block:\n%s", out)
	}
	fs, report, err := runCheck(t, dir, nil)
	if !errors.Is(err, ErrCheckFailed) || !hasFinding(fs, SeverityError, `service "external-api"`) || !hasFinding(fs, SeverityError, "external-api:") {
		t.Fatalf("check without the block:\n%s", report)
	}
	withBlock := strings.Replace(string(override), "services:\n", "services:\n"+block, 1)
	if err := os.WriteFile(pathIn(dir, OverrideFile), []byte(withBlock), 0o644); err != nil { //nolint:gosec // G306: manifest mode 0644
		t.Fatal(err)
	}
	if fs, report, err := runCheck(t, dir, nil); err != nil || hasErrors(fs) {
		t.Fatalf("check with the block: %v\n%s", err, report)
	}
	// A second upgrade sees the block and prints nothing more.
	if out, err := upgradeComposeFirst(t, dir); err != nil || strings.Contains(out, "Add this block") {
		t.Fatalf("second upgrade: %v\n%s", err, out)
	}
}

// TestInitExternalAPIExistingOverride: compose-first init with an existing
// override that lacks the external API's block names it and writes nothing.
func TestInitExternalAPIExistingOverride(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeReleaseDB(t, dir, VariantOwnProxy, DBExternal, variantImage)
	db := Database{Mode: DBExternal, Location: DBDocker, Network: "dbnet", Host: "postgres", TLS: DBTLSRequire}
	// The override of the same deployment without the external API.
	old := overlayHeader + overlayDoc(Overlays{DBNetwork: true}, "")
	if err := os.WriteFile(pathIn(dir, OverrideFile), []byte(old), 0o644); err != nil { //nolint:gosec // G306: manifest mode 0644
		t.Fatal(err)
	}
	before := snapshot(t, dir)
	out, err := composeFirstInit(t, dir, VariantOwnProxy, func(o *Options) {
		o.Answers.DB, o.Answers.ExternalAPI = db, true
	})
	if err == nil || !strings.Contains(err.Error(), "add these blocks to it") || !strings.Contains(err.Error(), "  external-api:\n    networks:") {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if d := diffSnapshots(before, snapshot(t, dir)); len(d) > 0 {
		t.Fatalf("refused init changed %v", d)
	}
}

// TestLintExternalAPIService: the external-api service stays as shipped
// (opt-in profile, no port, frontend/backend or the proxy network only, no
// further secret), and an added service cannot take its address.
func TestLintExternalAPIService(t *testing.T) {
	t.Parallel()
	for _, v := range []Variant{VariantTraefik, VariantOwnProxy} {
		if fs := lintWith(t, nil, []byte("services: {}\n"), v, nil); hasErrors(fs) {
			t.Fatalf("%s shipped: %v", v, fs)
		}
	}
	changed := `the override changes bekup's service "external-api"`
	for _, tc := range []struct {
		name, override string
		v              Variant
		want           string
	}{
		{"ports", "services:\n  external-api:\n    ports: [\"8081:8081\"]\n", VariantTraefik, `service "external-api" publishes ports`},
		{"egress", "services:\n  external-api:\n    networks: [egress]\n", VariantTraefik, changed},
		{"runner-ctl", "services:\n  external-api:\n    networks:\n      runner-ctl: {}\n", VariantTraefik, changed},
		{"always on", "services:\n  external-api:\n    profiles: !reset []\n", VariantTraefik, changed},
		{"master key", "services:\n  external-api:\n    secrets: [bekup_master_key]\n", VariantTraefik, changed},
		{"bekup.toml", "services:\n  external-api:\n    configs: [bekup_toml]\n", VariantTraefik, changed},
		{"writable root", "services:\n  external-api:\n    read_only: false\n", VariantTraefik, "must be read-only"},
		{"capability", "services:\n  external-api:\n    cap_add: [NET_ADMIN]\n", VariantTraefik, "capability NET_ADMIN"},
		{"root", "services:\n  external-api:\n    user: \"0:0\"\n", VariantTraefik, `user "0:0"`},
		{"no listen shape", "services:\n  external-api:\n    environment: !override\n      BEKUP_SETUP_VERSION: ${BEKUP_SETUP_VERSION:-}\n      BEKUP_IMAGE_MODE: ${BEKUP_IMAGE_MODE:-}\n", VariantOwnProxy, changed},
		{"other alias", "services:\n  external-api:\n    networks:\n      proxy:\n        aliases: !override [bekup-serve]\n", VariantOwnProxy, changed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if fs := lintWith(t, nil, []byte(tc.override), tc.v, nil); !hasFinding(fs, SeverityError, tc.want) {
				t.Fatalf("no error %q in %v", tc.want, fs)
			}
		})
	}
	// An added service on the frontend network (full variant) cannot take
	// the external API's fixed address, also while its profile is off.
	evil := evilService + "    networks:\n      frontend:\n        ipv4_address: 10.213.250.11\n"
	if fs := lintWith(t, nil, []byte("services:\n"+evil), VariantFull, nil); !hasFinding(fs, SeverityError, "belongs to a bekup service") {
		t.Fatalf("evil address: %v", fs)
	}
}
