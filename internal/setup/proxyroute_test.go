package setup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

// The bundled proxies' /api/v1/ route of the opt-in external API (D-220
// amendment 4): the entrypoints render it only with
// BEKUP_EXTERNAL_API=enabled. The tests run the real entrypoint scripts
// with their fixed paths moved into a temporary directory and the final
// exec of the proxy replaced, so only the rendering runs.

// sandboxScript copies script with the replacements applied (each must
// match) and returns its path.
func sandboxScript(t *testing.T, script string, repl ...string) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Clean(filepath.Join(repoRoot(t), filepath.FromSlash(script))))
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if len(repl)%2 != 0 {
		t.Fatal("replacements come in pairs")
	}
	for i := 0; i+1 < len(repl); i += 2 {
		if !strings.Contains(s, repl[i]) {
			t.Fatalf("%s lacks %q", script, repl[i])
		}
		s = strings.ReplaceAll(s, repl[i], repl[i+1])
	}
	p := filepath.Join(t.TempDir(), filepath.Base(script))
	if err := os.WriteFile(filepath.Clean(p), []byte(s), 0o600); err != nil { //nolint:gosec // G703: a copy of a repository script below t.TempDir()
		t.Fatal(err)
	}
	return p
}

// runScript runs a sandboxed script with sh and only env.
func runScript(t *testing.T, script string, env ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", script) //nolint:gosec // G204: a copy of a repository script below t.TempDir()
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestTraefikExternalAPIRoute(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, value string
		route, fail bool
	}{
		{name: "off"},
		{name: "enabled", value: "enabled", route: true},
		{name: "malformed", value: "yes", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := t.TempDir()
			script := sandboxScript(t, "deploy/traefik/entrypoint.sh",
				"src=/etc/traefik/bekup", "src="+filepath.Join(repoRoot(t), "deploy", "traefik"),
				"out=/tmp/traefik", "out="+out,
				`exec traefik --configFile="$out/traefik.yml"`, "exit 0")
			log, err := runScript(t, script, "BEKUP_DOMAIN=backup.example.com", "ACME_EMAIL=ops@example.com", "BEKUP_EXTERNAL_API="+tc.value)
			if tc.fail {
				if err == nil || !strings.Contains(log, "BEKUP_EXTERNAL_API must be 'enabled' or empty") {
					t.Fatalf("malformed value accepted: %v\n%s", err, log)
				}
				return
			}
			if err != nil {
				t.Fatalf("entrypoint: %v\n%s", err, log)
			}
			api, err := os.ReadFile(filepath.Clean(filepath.Join(out, "dynamic", "external-api.yml")))
			if (err == nil) != tc.route {
				t.Fatalf("external-api.yml rendered %v (err %v)", err == nil, err)
			}
			if !tc.route {
				return
			}
			var cfg struct {
				HTTP struct {
					Routers map[string]struct {
						Rule        string   `yaml:"rule"`
						Priority    int      `yaml:"priority"`
						Service     string   `yaml:"service"`
						Middlewares []string `yaml:"middlewares"`
					} `yaml:"routers"`
					Middlewares map[string]map[string]map[string]int `yaml:"middlewares"`
					Services    map[string]struct {
						LoadBalancer struct {
							Servers []struct {
								URL string `yaml:"url"`
							} `yaml:"servers"`
						} `yaml:"loadBalancer"`
					} `yaml:"services"`
				} `yaml:"http"`
			}
			if err := yaml.Unmarshal(api, &cfg); err != nil {
				t.Fatal(err)
			}
			r := cfg.HTTP.Routers["bekup-api"]
			if r.Rule != "Host(`backup.example.com`) && PathPrefix(`/api/v1/`)" || r.Service != "bekup-api" ||
				cfg.HTTP.Services["bekup-api"].LoadBalancer.Servers[0].URL != "http://external-api:8081" {
				t.Fatalf("router %+v, services %+v", r, cfg.HTTP.Services)
			}
			if cfg.HTTP.Middlewares["bekup-api-body-limit"]["buffering"]["maxRequestBodyBytes"] != 16384 {
				t.Fatalf("body limit %v", cfg.HTTP.Middlewares)
			}
			// Above every router of the UI (the stream router has 300), so
			// /api/v1/ never reaches serve; /api/download/ stays serve's.
			ui, err := os.ReadFile(filepath.Clean(filepath.Join(out, "dynamic", "bekup.yml")))
			if err != nil {
				t.Fatal(err)
			}
			var uiCfg struct {
				HTTP struct {
					Routers map[string]struct {
						Priority int `yaml:"priority"`
					} `yaml:"routers"`
				} `yaml:"http"`
			}
			if err := yaml.Unmarshal(ui, &uiCfg); err != nil {
				t.Fatal(err)
			}
			for name, ur := range uiCfg.HTTP.Routers {
				if ur.Priority >= r.Priority {
					t.Errorf("UI router %s has priority %d >= %d", name, ur.Priority, r.Priority)
				}
			}
			for _, m := range r.Middlewares {
				if _, local := cfg.HTTP.Middlewares[m]; !local && !strings.Contains(string(ui), "\n    "+m+":\n") {
					t.Errorf("middleware %s is defined nowhere", m)
				}
			}
			if strings.Contains(r.Rule, "/api/download") {
				t.Error("the external API's router takes /api/download")
			}
		})
	}
}

func TestNginxExternalAPIRoute(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("envsubst"); err != nil {
		t.Skip("envsubst (gettext) not installed: the nginx image has it")
	}
	for _, tc := range []struct {
		name, value string
		route, fail bool
	}{
		{name: "off"},
		{name: "enabled", value: "enabled", route: true},
		{name: "malformed", value: "true", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tmp := t.TempDir()
			certs := filepath.Join(tmp, "certs")
			if err := os.MkdirAll(certs, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, f := range []string{"tls.crt", "tls.key"} {
				if err := os.WriteFile(filepath.Join(certs, f), []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			script := sandboxScript(t, "deploy/nginx/entrypoint.sh",
				"src=/etc/nginx/bekup", "src="+filepath.Join(repoRoot(t), "deploy", "nginx"),
				"/tmp/nginx", filepath.Join(tmp, "nginx"),
				"/certs/", certs+"/",
				"nginx -t -q\n\nnginx -g", "exit 0\n\nnginx -g")
			log, err := runScript(t, script, "BEKUP_DOMAIN=backup.example.com", "PROXY_TLS_MODE=static", "BEKUP_EXTERNAL_API="+tc.value)
			if tc.fail {
				if err == nil || !strings.Contains(log, "BEKUP_EXTERNAL_API must be 'enabled' or empty") {
					t.Fatalf("malformed value accepted: %v\n%s", err, log)
				}
				return
			}
			if err != nil {
				t.Fatalf("entrypoint: %v\n%s", err, log)
			}
			site, err := os.ReadFile(filepath.Clean(filepath.Join(tmp, "nginx", "conf.d", "bekup.conf")))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(site), "include /tmp/nginx/bekup-api.d/*.conf;") {
				t.Fatal("the HTTPS server block does not include the API locations")
			}
			loc, err := os.ReadFile(filepath.Clean(filepath.Join(tmp, "nginx", "bekup-api.d", "external-api.conf")))
			if (err == nil) != tc.route {
				t.Fatalf("external-api.conf written %v (err %v)", err == nil, err)
			}
			if tc.route && (!strings.Contains(string(loc), "location ^~ /api/v1/ {") || !strings.Contains(string(loc), "client_max_body_size 16k;") ||
				!strings.Contains(string(loc), "set $bekup_api_upstream external-api:8081;") || strings.Count(string(loc), "location ") != 1) {
				t.Fatalf("location:\n%s", loc)
			}
		})
	}
}

// TestNginxLogPathRedaction: the access log's $bekup_log_path map logs an
// external API path only in a known route shape and everything else below
// /api/v1 as /api/v1/[redacted] (a token put into the path by mistake),
// the download ticket redacted (L1 of the D-220 review). The map is
// evaluated like nginx does: exact strings first, then the regular
// expressions in order (all of them are RE2-compatible), then default.
func TestNginxLogPathRedaction(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "nginx", "nginx.conf"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	start := strings.Index(body, "map $uri $bekup_log_path {")
	if start < 0 {
		t.Fatal("no $bekup_log_path map")
	}
	body = body[start:]
	body = body[strings.Index(body, "{")+1 : strings.Index(body, "}\n")]
	type entry struct {
		re    *regexp.Regexp
		exact string
		value string
	}
	var entries []entry
	def := ""
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSuffix(line, ";")
		var key, val string
		if strings.HasPrefix(line, `"`) {
			end := strings.Index(line[1:], `"`) + 1
			key, val = line[1:end], strings.TrimSpace(line[end+1:])
		} else {
			f := strings.Fields(line)
			key, val = f[0], f[1]
		}
		val = strings.Trim(val, `"`)
		switch {
		case key == "default":
			def = val
		case strings.HasPrefix(key, "~"):
			entries = append(entries, entry{re: regexp.MustCompile(key[1:]), value: val})
		default:
			entries = append(entries, entry{exact: key, value: val})
		}
	}
	logged := func(uri string) string {
		for _, e := range entries {
			if e.re == nil && e.exact == uri {
				return e.value
			}
		}
		for _, e := range entries {
			if e.re != nil && e.re.MatchString(uri) {
				if e.value == "$uri" {
					return uri
				}
				return e.value
			}
		}
		if def == "$uri" {
			return uri
		}
		return def
	}
	id := "0f8fad5b-d9cb-469f-a165-70867728950e"
	for uri, want := range map[string]string{
		"/api/v1/self":                      "/api/v1/self",
		"/api/v1/jobs/" + id + "/runs":      "/api/v1/jobs/" + id + "/runs",
		"/api/v1/requests/" + id:            "/api/v1/requests/" + id,
		"/api/v1/bkp_aaaa_bbbb":             "/api/v1/[redacted]",
		"/api/v1/jobs/bkp_aaaa_bbbb/runs":   "/api/v1/[redacted]",
		"/api/v1/requests/" + id + "/bkp_a": "/api/v1/[redacted]",
		"/api/v1/self/bkp_a":                "/api/v1/[redacted]",
		"/api/v1bkp_a":                      "/api/v1/[redacted]",
		"/api/download/ticket":              "/api/download/[redacted]",
		"/jobs/" + id:                       "/jobs/" + id,
		"/api/graphql":                      "/api/graphql",
	} {
		if got := logged(uri); got != want {
			t.Errorf("%s logged as %q, want %q", uri, got, want)
		}
	}
}
