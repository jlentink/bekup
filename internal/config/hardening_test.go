package config

import (
	"slices"
	"strings"
	"testing"
)

// baseBekup returns defaults that validate outside dev mode except for the
// fields a test sets.
func baseBekup() Bekup {
	c := DefaultBekup()
	c.Server.Listen = "10.213.250.10:8080"
	c.Server.BaseURL = "https://backup.example.com"
	c.Server.TrustedProxies = []string{"10.213.250.5/32"}
	return c
}

func hasWarning(ws []string, sub string) bool {
	return slices.ContainsFunc(ws, func(w string) bool { return strings.Contains(w, sub) })
}

func TestInsecureHTTPCombinations(t *testing.T) {
	tests := []struct {
		name          string
		baseURL       string
		dev           bool
		allowInsecure bool
		proxies       []string
		wantErr       string // empty: valid
		wantInsecure  bool
	}{
		{"https prod with proxies", "https://b.example.com", false, false, []string{"10.0.0.1/32"}, "", false},
		{"https prod without proxies", "https://b.example.com", false, false, nil, "server.trusted_proxies: is required", false},
		{"https prod with flag still needs proxies", "https://b.example.com", false, true, nil, "server.trusted_proxies: is required", false},
		{"https prod with flag and proxies", "https://b.example.com", false, true, []string{"10.0.0.1/32"}, "", false},
		{"https dev without proxies", "https://b.example.com", true, false, nil, "", false},
		{"http prod refused", "http://b.example.com", false, false, []string{"10.0.0.1/32"}, "server.base_url: must use https", true},
		{"http prod refused without proxies", "http://b.example.com", false, false, nil, "server.base_url: must use https", true},
		{"http prod with flag", "http://b.example.com", false, true, []string{"10.0.0.1/32"}, "", true},
		{"http prod with flag, no proxies", "http://b.example.com", false, true, nil, "", true},
		{"http dev", "http://localhost:8080", true, false, nil, "", true},
		{"http dev with flag", "http://localhost:8080", true, true, nil, "", true},
		{"ftp refused even with flag", "ftp://b.example.com", false, true, nil, "server.base_url: must be an https URL", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := baseBekup()
			c.Server.BaseURL = tc.baseURL
			c.Server.AllowInsecureHTTP = tc.allowInsecure
			c.Server.TrustedProxies = tc.proxies
			c.Dev.Enabled = tc.dev
			joined := joinErrs(validateBekup(&c, tc.dev))
			switch {
			case tc.wantErr == "" && joined != "":
				t.Fatalf("unexpected errors:\n%s", joined)
			case tc.wantErr != "" && !strings.Contains(joined, tc.wantErr):
				t.Fatalf("missing %q in:\n%s", tc.wantErr, joined)
			}
			if got := c.InsecureTransport(); got != tc.wantInsecure {
				t.Fatalf("InsecureTransport() = %t, want %t", got, tc.wantInsecure)
			}
			if got := hasWarning(c.Warnings(), "plain HTTP: traffic not encrypted"); got != tc.wantInsecure {
				t.Fatalf("plain HTTP warning = %t, want %t (%v)", got, tc.wantInsecure, c.Warnings())
			}
		})
	}
}

func TestInsecureHTTPLoadWarning(t *testing.T) {
	p := writeFile(t, safeDir(t), BekupFileName,
		"[server]\nlisten = \"10.0.0.10:8080\"\nbase_url = \"http://bekup.lan:8080\"\nallow_insecure_http = true\n", 0o600)
	res, err := LoadBekup(opts([]string{p}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Config.InsecureTransport() || !hasWarning(res.Warnings, InsecureHTTPWarning) {
		t.Fatalf("warnings: %v", res.Warnings)
	}
	if res.Dev {
		t.Fatal("allow_insecure_http must not enable dev mode")
	}
}

func TestInsecureTransportUnparsable(t *testing.T) {
	c := DefaultBekup()
	c.Server.BaseURL = "https://[::1"
	if !c.InsecureTransport() {
		t.Fatal("an unparsable base URL must count as insecure")
	}
	c.Server.BaseURL = "HTTPS://b.example.com"
	if c.InsecureTransport() {
		t.Fatal("scheme comparison must be case-insensitive")
	}
}

func TestWildcardListen(t *testing.T) {
	tests := []struct {
		listen   string
		wildcard bool
	}{
		{":8080", true},
		{"0.0.0.0:8080", true},
		{"[::]:8080", true},
		{"[::ffff:0.0.0.0]:8080", true},
		{"0:8080", true},
		{"10.213.250.10:8080", false},
		{"127.0.0.1:8080", false},
		{"[::1]:8080", false},
		{"serve:8080", false},
		{"nope", false},
	}
	for _, tc := range tests {
		if got := isWildcardListen(tc.listen); got != tc.wildcard {
			t.Errorf("isWildcardListen(%q) = %t, want %t", tc.listen, got, tc.wildcard)
		}
	}

	for _, tc := range []struct {
		name    string
		listen  string
		dev     bool
		allow   bool
		wantErr bool
		warn    bool
	}{
		{"wildcard prod refused", ":8080", false, false, true, false},
		{"0.0.0.0 prod refused", "0.0.0.0:8080", false, false, true, false},
		{"[::] prod refused", "[::]:8080", false, false, true, false},
		{"wildcard prod with override warns", ":8080", false, true, false, true},
		{"wildcard dev allowed silently", "0.0.0.0:8080", true, false, false, false},
		{"fixed address prod", "10.213.250.10:8080", false, false, false, false},
		{"fixed address with override no warning", "10.213.250.10:8080", false, true, false, false},
	} {
		t.Run("server "+tc.name, func(t *testing.T) {
			c := baseBekup()
			c.Server.Listen = tc.listen
			c.Server.AllowWildcardListen = tc.allow
			c.Dev.Enabled = tc.dev
			joined := joinErrs(validateBekup(&c, tc.dev))
			if got := strings.Contains(joined, "server.listen:"); got != tc.wantErr {
				t.Fatalf("error = %t, want %t:\n%s", got, tc.wantErr, joined)
			}
			if got := hasWarning(c.Warnings(), "listens on every interface"); got != tc.warn {
				t.Fatalf("warning = %t, want %t (%v)", got, tc.warn, c.Warnings())
			}
		})
		t.Run("metrics "+tc.name, func(t *testing.T) {
			c := baseBekup()
			c.Server.Listen = "10.213.250.10:8080"
			c.Metrics.Enabled = true
			c.Metrics.Listen = strings.Replace(tc.listen, "8080", "9464", 1)
			c.Metrics.AllowWildcardListen = tc.allow
			c.Dev.Enabled = tc.dev
			joined := joinErrs(validateBekup(&c, tc.dev))
			if got := strings.Contains(joined, "metrics.listen:"); got != tc.wantErr {
				t.Fatalf("error = %t, want %t:\n%s", got, tc.wantErr, joined)
			}
			if got := hasWarning(c.Warnings(), "metrics.listen"); got != tc.warn {
				t.Fatalf("warning = %t, want %t (%v)", got, tc.warn, c.Warnings())
			}
		})
		t.Run("runner "+tc.name, func(t *testing.T) {
			r := DefaultRunner()
			r.Listen.Address = strings.Replace(tc.listen, "8080", "7443", 1)
			r.Listen.AllowWildcard = tc.allow
			joined := joinErrs(validateRunner(&r, tc.dev))
			if got := strings.Contains(joined, "listen.address:"); got != tc.wantErr {
				t.Fatalf("error = %t, want %t:\n%s", got, tc.wantErr, joined)
			}
			if got := hasWarning(r.warnings(tc.dev), "listens on every interface"); got != tc.warn {
				t.Fatalf("warning = %t, want %t", got, tc.warn)
			}
		})
	}
}

func TestRunnerWildcardLoadWarning(t *testing.T) {
	p := writeFile(t, safeDir(t), RunnerFileName, "[listen]\naddress = \"0.0.0.0:7443\"\nallow_wildcard = true\n", 0o600)
	res, err := LoadRunner(opts([]string{p}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(res.Warnings, "listen.allow_wildcard = true") {
		t.Fatalf("warnings: %v", res.Warnings)
	}
}

func TestMetricsListenCollision(t *testing.T) {
	tests := []struct {
		server, metrics string
		collide         bool
	}{
		{":9464", ":9464", true},
		{":9464", "0.0.0.0:9464", true},
		{"0.0.0.0:9464", "[::]:9464", true},
		{"10.0.0.1:9464", "0.0.0.0:9464", true},
		{"10.0.0.1:9464", ":9464", true},
		{"127.0.0.1:9464", "localhost:9464", true},
		{"[::1]:9464", "localhost:9464", true},
		{"[::ffff:10.0.0.1]:9464", "10.0.0.1:9464", true},
		{"10.0.0.1:9464", "10.0.0.1:09464", true},
		{"Serve:9464", "serve:9464", true},
		{"10.0.0.1:8080", "127.0.0.1:9464", false},
		{"10.0.0.1:9464", "127.0.0.1:9464", false},
		{":8080", ":9464", false},
	}
	for _, tc := range tests {
		if got := listenersCollide(tc.server, tc.metrics); got != tc.collide {
			t.Errorf("listenersCollide(%q, %q) = %t, want %t", tc.server, tc.metrics, got, tc.collide)
		}
		c := baseBekup()
		c.Server.Listen = tc.server
		c.Server.AllowWildcardListen = true
		c.Metrics.Listen = tc.metrics
		got := strings.Contains(joinErrs(validateBekup(&c, false)), "metrics.listen: must differ")
		if got != tc.collide {
			t.Errorf("validate %q vs %q: collision error = %t, want %t", tc.server, tc.metrics, got, tc.collide)
		}
	}
}

func TestRunnerAllowCIDRsAlwaysDenied(t *testing.T) {
	tests := []struct {
		cidr string
		ok   bool
	}{
		// Overlapping (not only contained) prefixes are rejected.
		{"0.0.0.0/1", false},
		{"126.0.0.0/7", false},
		{"112.0.0.0/4", false},
		{"169.0.0.0/8", false},
		{"128.0.0.0/1", false},
		{"::/1", false},
		{"fe80::/16", false},
		{"fe00::/7", false},
		{"127.0.0.1/32", false},
		{"169.254.169.254/32", false},
		// IPv4-mapped IPv6 forms are unmapped before the check.
		{"::ffff:127.0.0.1/128", false},
		{"::ffff:127.0.0.0/104", false},
		{"::ffff:169.254.169.254/128", false},
		{"::ffff:0:0/96", false},
		{"::ffff:0.0.0.0/97", false},
		{"::/0", false},
		{"0.0.0.0/0", false},
		// Legitimate private ranges still pass.
		{"10.0.0.0/8", true},
		{"172.16.0.0/12", true},
		{"192.168.0.0/16", true},
		{"192.168.1.10/32", true},
		{"100.64.0.0/10", true},
		{"fd12:3456:789a::/48", true},
		{"::ffff:10.0.0.0/104", true},
		{"::ffff:192.168.1.10/128", true},
	}
	for _, tc := range tests {
		r := DefaultRunner()
		r.Listen.Address = "10.213.251.10:7443"
		r.Targets.AllowCIDRs = []string{tc.cidr}
		errs := validateRunner(&r, false)
		if got := len(errs) == 0; got != tc.ok {
			t.Errorf("%s: valid=%t, want %t (%v)", tc.cidr, got, tc.ok, errs)
		}
	}
}

func TestUnknownEnvWarnings(t *testing.T) {
	dir := safeDir(t)
	p := writeFile(t, dir, BekupFileName, validBekupTOML, 0o600)
	env := map[string]string{
		"BEKUP_LOG_LEVEL":             "warn",       // key of bekup.toml
		"BEKUP_CONFIG":                p,            // documented non-key var
		"BEKUP_DOMAIN":                "b.example",  // documented non-key var (compose)
		"BEKUP_SERVER_BASEURL":        "x",          // typo
		"BEKUP_BASE_URL":              "x",          // legacy shorthand
		"BEKUP_RUNNER_CONTAINER":      "x",          // legacy shorthand
		"BEKUP_DOCKER_SELF_CONTAINER": "c",          // runner.toml key
		"BEKUP_SECRET_SAUCE":          "hunter2222", // value must never appear
		"OTHER_VAR":                   "x",
	}
	o := opts(nil, env)
	o.SearchPaths = []string{p}
	res, err := LoadBekup(o)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(res.Warnings, "\n")
	for _, want := range []string{
		"BEKUP_SERVER_BASEURL does not match any configuration key",
		"BEKUP_SECRET_SAUCE does not match any configuration key",
		"BEKUP_BASE_URL is not a configuration key and is ignored; did you mean BEKUP_SERVER_BASE_URL?",
		"BEKUP_RUNNER_CONTAINER is not a configuration key and is ignored; did you mean BEKUP_DOCKER_SELF_CONTAINER?",
		"BEKUP_DOCKER_SELF_CONTAINER configures the other config file",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing warning %q in:\n%s", want, joined)
		}
	}
	for _, unwanted := range []string{"BEKUP_LOG_LEVEL", "BEKUP_CONFIG", "BEKUP_DOMAIN", "OTHER_VAR", "hunter2222"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("unexpected %q in warnings:\n%s", unwanted, joined)
		}
	}

	// The runner accepts its own keys and flags bekup.toml keys.
	rp := writeFile(t, dir, RunnerFileName, "[listen]\naddress = \"10.0.0.11:7443\"\n", 0o600)
	rres, err := LoadRunner(opts([]string{rp}, map[string]string{"BEKUP_DOCKER_SELF_CONTAINER": "c", "BEKUP_LOG_LEVEL": "x"}))
	if err != nil {
		t.Fatal(err)
	}
	rj := strings.Join(rres.Warnings, "\n")
	if strings.Contains(rj, "BEKUP_DOCKER_SELF_CONTAINER") || !strings.Contains(rj, "BEKUP_LOG_LEVEL configures the other config file") {
		t.Errorf("runner warnings:\n%s", rj)
	}
}

func TestLegacyEnvNamesMapToRealKeys(t *testing.T) {
	b, r := DefaultBekup(), DefaultRunner()
	known := map[string]bool{}
	for _, f := range append(fieldsOf(&b), fieldsOf(&r)...) {
		known[f.Env] = true
	}
	for legacy, canonical := range LegacyEnvNames {
		if !known[canonical] {
			t.Errorf("%s maps to %s, which is not a configuration key", legacy, canonical)
		}
		if known[legacy] {
			t.Errorf("%s is a real key and must not be listed as legacy", legacy)
		}
	}
	for _, name := range NonKeyEnvVars {
		if known[name] {
			t.Errorf("%s is a real key and must not be listed as a non-key variable", name)
		}
	}
}

// TestDevModeEnvDisable documents that BEKUP_DEV_ENABLED=false overrides
// [dev] enabled = true from the file, while BEKUP_DEV=0 only declines to
// enable dev mode (it is an enable-only alias).
func TestDevModeEnvDisable(t *testing.T) {
	dir := safeDir(t)
	p := writeFile(t, dir, BekupFileName, validBekupTOML+"[dev]\nenabled = true\n", 0o600)

	res, err := LoadBekup(opts([]string{p}, map[string]string{"BEKUP_DEV_ENABLED": "false"}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Dev || res.Config.Dev.Enabled || res.Sources["dev.enabled"] != SourceEnv {
		t.Fatalf("BEKUP_DEV_ENABLED=false must disable dev mode: dev=%t source=%s", res.Dev, res.Sources["dev.enabled"])
	}

	res, err = LoadBekup(opts([]string{p}, map[string]string{"BEKUP_DEV": "0"}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Dev {
		t.Fatal("BEKUP_DEV=0 is enable-only and must not override the file")
	}

	// Any enabling source wins over a disabling one.
	res, err = LoadBekup(opts([]string{p}, map[string]string{"BEKUP_DEV": "1", "BEKUP_DEV_ENABLED": "false"}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Dev {
		t.Fatal("BEKUP_DEV=1 must enable dev mode")
	}
}
