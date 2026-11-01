package setup

import (
	"bytes"
	"io/fs"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/jlentink/bekup"
	"github.com/jlentink/bekup/internal/imageref"
)

const variantImage = "ghcr.io/jlentink/bekup:1.2.3@" + testDigest

type variantDoc struct {
	Image    string                    `yaml:"x-bekup-image"`
	Services map[string]map[string]any `yaml:"services"`
	Networks map[string]map[string]any `yaml:"networks"`
	Volumes  map[string]any            `yaml:"volumes"`
}

func renderVariantT(t *testing.T, v Variant) ([]byte, variantDoc) {
	t.Helper()
	img := mustRef(t, variantImage)
	out, err := RenderRelease(v, DBBundled, &img)
	if err != nil {
		t.Fatal(err)
	}
	var doc variantDoc
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	return out, doc
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestVariantServiceSets(t *testing.T) {
	t.Parallel()
	want := map[Variant][]string{
		VariantTraefik:  {"admin", "certs-init", "migrate", "postgres", "runner", "serve", "setup", "traefik", "traefik-init"},
		VariantOwnProxy: {"admin", "certs-init", "migrate", "postgres", "runner", "serve", "setup"},
		VariantFull:     {"admin", "certbot", "certs-init", "migrate", "nginx", "nginx-init", "postgres", "runner", "serve", "setup", "traefik", "traefik-init"},
	}
	for _, v := range AllVariants {
		t.Run(string(v), func(t *testing.T) {
			out, doc := renderVariantT(t, v)
			if got := keys(doc.Services); !slices.Equal(got, want[v]) {
				t.Fatalf("services %v, want %v", got, want[v])
			}
			if doc.Image != variantImage {
				t.Fatalf("x-bekup-image = %q", doc.Image)
			}
			for name, svc := range doc.Services {
				if _, ok := svc["build"]; ok {
					t.Errorf("%s uses build", name)
				}
				img, _ := svc["image"].(string) //nolint:errcheck // a missing image fails ParsePinned
				if _, err := imageref.ParsePinned(img); err != nil {
					t.Errorf("%s: image %q: %v", name, img, err)
				}
				if slices.Contains(bekupServices, name) && img != variantImage {
					t.Errorf("%s runs %q, not the x-bekup-image", name, img)
				}
			}
			if imageVarRefRe.MatchString(string(out)) {
				t.Error("a ${BEKUP_IMAGE reference remains")
			}
			// The proxy services of the bundled variant start with the stack.
			if v == VariantTraefik {
				for _, s := range []string{"traefik", "traefik-init"} {
					if _, ok := doc.Services[s]["profiles"]; ok {
						t.Errorf("%s keeps a profile", s)
					}
				}
			}
			// Every variant passes its own hardening lint.
			if findings := lintWith(t, out, nil, v, nil); len(findings) > 0 {
				t.Errorf("lint findings: %v", findings)
			}
		})
	}
}

func TestVariantExternalNetworks(t *testing.T) {
	t.Parallel()
	for _, v := range []Variant{VariantOwnProxy} {
		t.Run(string(v), func(t *testing.T) {
			_, doc := renderVariantT(t, v)
			for _, n := range []string{"frontend", "edge"} {
				if _, ok := doc.Networks[n]; ok {
					t.Errorf("network %s exists", n)
				}
			}
			p := doc.Networks["proxy"]
			if p["external"] != true || p["name"] != "${BEKUP_PROXY_NETWORK:-bekup-proxy}" {
				t.Fatalf("proxy network %v", p)
			}
			for name, svc := range doc.Services {
				if _, ok := svc["ports"]; ok {
					t.Errorf("%s publishes ports", name)
				}
				nets, _ := svc["networks"].(map[string]any) //nolint:errcheck // list form has no proxy
				_, onProxy := nets["proxy"]
				if onProxy != (name == "serve") {
					t.Errorf("%s on the proxy network: %v", name, onProxy)
				}
			}
			serveNets, _ := doc.Services["serve"]["networks"].(map[string]any) //nolint:errcheck // checked below
			proxy, _ := serveNets["proxy"].(map[string]any)                    //nolint:errcheck // checked below
			// Join mode (D-215): no fixed address, the alias bekup-serve, and
			// the listen shape for the wildcard address.
			if _, fixed := proxy["ipv4_address"]; fixed || !equalYAML(proxy["aliases"], []any{"bekup-serve"}) {
				t.Fatalf("serve on the proxy network %v", proxy)
			}
			envm, _ := doc.Services["serve"]["environment"].(map[string]any) //nolint:errcheck // checked below
			if envm["BEKUP_LISTEN_SHAPE"] != "own-proxy-join" {
				t.Fatalf("serve environment %v", envm)
			}
			for _, vol := range []string{"traefik-acme", "nginx-certs", "nginx-acme-webroot"} {
				if _, ok := doc.Volumes[vol]; ok {
					t.Errorf("volume %s exists", vol)
				}
			}
		})
	}
}

func TestVariantLabels(t *testing.T) {
	t.Parallel()
	want := map[string]any{
		"traefik.enable":                                                            "true",
		"traefik.docker.network":                                                    "${BEKUP_PROXY_NETWORK:-bekup-proxy}",
		"traefik.http.routers.bekup.rule":                                           "Host(`${BEKUP_DOMAIN:-bekup.invalid}`)",
		"traefik.http.routers.bekup.entrypoints":                                    "${TRAEFIK_ENTRYPOINT:-websecure}",
		"traefik.http.routers.bekup.tls":                                            "true",
		"traefik.http.routers.bekup.tls.certresolver":                               "${TRAEFIK_CERTRESOLVER:-letsencrypt}",
		"traefik.http.routers.bekup.service":                                        "bekup",
		"traefik.http.services.bekup.loadbalancer.server.port":                      "8080",
		"traefik.http.services.bekup.loadbalancer.responseforwarding.flushinterval": "-1ms",
	}
	// No release file carries labels (D-215: an overlay).
	for _, v := range AllVariants {
		out, doc := renderVariantT(t, v)
		for name, svc := range doc.Services {
			if _, ok := svc["labels"]; ok {
				t.Errorf("%s: %s has labels", v, name)
			}
		}
		if strings.Contains(string(out), "traefik.enable") {
			t.Errorf("%s mentions traefik labels", v)
		}
	}
	var doc variantDoc
	if err := yaml.Unmarshal([]byte(overlayDoc(Overlays{ProxyLabels: true}, "")), &doc); err != nil {
		t.Fatal(err)
	}
	m, isMap := doc.Services["serve"]["labels"].(map[string]any)
	if !isMap || len(m) != len(want) || len(doc.Services) != 1 {
		t.Fatalf("overlay labels %v", doc.Services)
	}
	for k, val := range want {
		if m[k] != val {
			t.Errorf("label %s = %v, want %v", k, m[k], val)
		}
	}
	for k := range m {
		if strings.Contains(k, "middlewares") {
			t.Errorf("middleware label %s", k)
		}
	}
}

// TestOverlayDocs: every combination of the setup-only overlays (D-215)
// renders the blocks it names, only those, and merges with its variant
// into a project that passes the lint of exactly that form.
func TestOverlayDocs(t *testing.T) {
	t.Parallel()
	for _, v := range AllVariants {
		for _, db := range DBModes {
			for _, ov := range overlayCombos(v, db) {
				doc := overlayDoc(ov, "")
				if !ov.Any() {
					if doc != "" {
						t.Fatalf("%s/%s: empty overlays render %q", v, db, doc)
					}
					continue
				}
				var d variantDoc
				if err := yaml.Unmarshal([]byte(doc), &d); err != nil {
					t.Fatalf("%+v: %v", ov, err)
				}
				serveNets, _ := d.Services["serve"]["networks"].(map[string]any) //nolint:errcheck // checked below
				if _, ok := serveNets["database"]; ok != ov.DBNetwork {
					t.Errorf("%+v: serve on database %v", ov, ok)
				}
				if _, ok := serveNets["proxy"]; ok != ov.ProxyLink {
					t.Errorf("%+v: serve link %v", ov, ok)
				}
				for _, svc := range []string{"migrate", "admin"} {
					if _, ok := d.Services[svc]; ok != ov.DBNetwork {
						t.Errorf("%+v: %s %v", ov, svc, ok)
					}
				}
				if ov.DBNetwork && (d.Networks["database"]["external"] != true || d.Networks["database"]["name"] != "${BEKUP_DB_NETWORK:-bekup-db}" || d.Networks["backend"]["internal"] != true) {
					t.Errorf("%+v: networks %v", ov, d.Networks)
				}
				compose := []byte(renderedVariantDB(t, v, db))
				in := composeInput{root: "/deploy", compose: compose, override: []byte(overlayHeader + doc)}
				findings := lintComposeFiles(&in, projectEnv(nil, nil, in.docs()...), v, variantImage, expectedDocs(compose, ov), false)
				if len(findings) > 0 {
					t.Errorf("%s/%s %+v: %v", v, db, ov, findings)
				}
				// Without the override the lint holds the deployment to it.
				in.override = nil
				if findings := lintComposeFiles(&in, projectEnv(nil, nil, in.docs()...), v, variantImage, expectedDocs(compose, ov), false); !hasErrors(findings) {
					t.Errorf("%s/%s %+v: a missing override passes", v, db, ov)
				}
			}
		}
	}
}

// overlayCombos are the overlay combinations a variant and database mode
// allow.
func overlayCombos(v Variant, db DBMode) []Overlays {
	var out []Overlays
	for _, labels := range []bool{false, true} {
		for _, link := range []bool{false, true} {
			for _, dbn := range []bool{false, true} {
				if (labels || link) && !v.OwnProxy() || dbn && db != DBExternal {
					continue
				}
				out = append(out, Overlays{ProxyLabels: labels, ProxyLink: link, DBNetwork: dbn})
			}
		}
	}
	return out
}

// The setup service is exactly the hardened one-shot installer in every
// variant.
func TestVariantSetupService(t *testing.T) {
	t.Parallel()
	want := map[string]any{
		"image":        variantImage,
		"profiles":     []any{"setup"},
		"user":         "0:0",
		"entrypoint":   []any{"/usr/local/bin/bekup", "setup"},
		"command":      []any{"init"},
		"environment":  map[string]any{"BEKUP_IMAGE": variantImage, "DOCKER_GID": "${DOCKER_GID:-}"},
		"read_only":    true,
		"tmpfs":        []any{"/tmp:rw,noexec,nosuid,nodev,size=64m"},
		"cap_drop":     []any{"ALL"},
		"cap_add":      []any{"CHOWN"},
		"security_opt": []any{"no-new-privileges:true"},
		"network_mode": "none",
		"logging":      map[string]any{"driver": "none"},
		"stdin_open":   true,
		"tty":          true,
		"volumes": []any{map[string]any{
			"type": "bind", "source": ".", "target": "/deploy",
			"bind": map[string]any{"create_host_path": false},
		}},
		"restart": "no",
		"deploy":  map[string]any{"resources": map[string]any{"limits": map[string]any{"cpus": "1.00", "memory": "512M", "pids": 64}}},
	}
	for _, v := range AllVariants {
		_, doc := renderVariantT(t, v)
		got := doc.Services["setup"]
		if len(got) != len(want) {
			t.Errorf("%s: setup has keys %v", v, keys(got))
		}
		for k, w := range want {
			if !equalYAML(got[k], w) {
				t.Errorf("%s: setup.%s = %#v, want %#v", v, k, got[k], w)
			}
		}
	}
}

func equalYAML(a, b any) bool {
	x, err1 := yaml.Marshal(a)
	y, err2 := yaml.Marshal(b)
	return err1 == nil && err2 == nil && bytes.Equal(x, y)
}

// Every node a variant does not edit is byte-identical to the base.
func TestVariantUntouchedBlocksByteEqual(t *testing.T) {
	t.Parallel()
	src, err := fs.ReadFile(bekup.Assets(), composeSource)
	if err != nil {
		t.Fatal(err)
	}
	base, err := newOutline(string(src))
	if err != nil {
		t.Fatal(err)
	}
	edited := map[Variant][]string{
		VariantTraefik:  {"services/traefik-init", "services/traefik"},
		VariantOwnProxy: {"services/serve"},
	}
	var doc variantDoc
	if err := yaml.Unmarshal(src, &doc); err != nil {
		t.Fatal(err)
	}
	nodes := make([]string, 0, 3+len(doc.Services)+len(doc.Networks))
	nodes = append(nodes, "x-bekup-hardening", "configs", "secrets")
	for name := range doc.Services {
		nodes = append(nodes, "services/"+name)
	}
	for name := range doc.Networks {
		nodes = append(nodes, "networks/"+name)
	}
	for _, v := range AllVariants {
		out, err := RenderRelease(v, DBBundled, nil)
		if err != nil {
			t.Fatal(err)
		}
		o, err := newOutline(string(out))
		if err != nil {
			t.Fatal(err)
		}
		checked := 0
		for _, n := range nodes {
			if slices.Contains(edited[v], n) {
				continue
			}
			got, err := o.find(n)
			if err != nil {
				continue // removed by the variant
			}
			want, err := base.find(n)
			if err != nil {
				t.Fatal(err)
			}
			// The key line and body (leading comments may be replaced).
			if g, w := strings.Join(o.lines[got.key:got.end], "\n"), strings.Join(base.lines[want.key:want.end], "\n"); g != w {
				t.Errorf("%s: %s differs from the base", v, n)
			}
			checked++
		}
		if checked < 8 {
			t.Errorf("%s: only %d nodes compared", v, checked)
		}
	}
}

// Overlays never name an image and never use build.
func TestOverlaysHaveNoImage(t *testing.T) {
	t.Parallel()
	n := 0
	err := fs.WalkDir(variantFS, "variants", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		n++
		if !strings.HasSuffix(p, ".yaml") {
			t.Errorf("%s: overlays are .yaml fragments", p)
		}
		b, err := fs.ReadFile(variantFS, p)
		if err != nil {
			return err
		}
		// BEKUP_IMAGE may appear only in comments (the headers explain it;
		// BEKUP_IMAGE_MODE is another variable, D-212).
		code := regexp.MustCompile(`(?m)^\s*#.*$`).ReplaceAllString(string(b), "")
		if regexp.MustCompile(`(?m)^\s*(-\s+)?(image|build)\s*:`).Match(b) || regexp.MustCompile(`BEKUP_IMAGE([^A-Za-z0-9_]|$)`).MatchString(code) {
			t.Errorf("%s names an image or a build", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 8 {
		t.Fatalf("only %d fragments", n)
	}
	// Every fragment is used by some variant, every referenced one exists.
	used := map[string]bool{}
	for v, edits := range variantEdits {
		for _, e := range append(slices.Clone(edits), externalDBEdits(v)...) {
			if e.fragment != "" {
				used["variants/"+e.fragment] = true
			}
		}
	}
	err = fs.WalkDir(variantFS, "variants", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !used[p] {
			t.Errorf("fragment %s is unused", p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Base style lint for the repository compose file.
func TestBaseComposeStyle(t *testing.T) {
	t.Parallel()
	src := string(readFile(t, repoRoot(t), composeSource))
	if strings.Count(src, "\n"+ComposeImageLine+"\n") != 1 || len(imageVarRefRe.FindAllStringIndex(src, -1)) != 1 {
		t.Fatal("the image must appear only in the x-bekup-image line")
	}
	if !strings.Contains(src, "\n  image: *bekup-image\n") {
		t.Fatal("x-bekup-hardening must use image: *bekup-image")
	}
	for i, l := range strings.Split(src, "\n") {
		trim := strings.TrimSpace(l)
		if strings.HasPrefix(trim, "#") {
			continue
		}
		if strings.Contains(trim, "./deploy/") && !strings.HasPrefix(trim, "source: ./deploy/") {
			t.Errorf("line %d: ./deploy binds must use the long syntax: %s", i+1, trim)
		}
		if strings.HasPrefix(trim, "build:") {
			t.Errorf("line %d: build", i+1)
		}
	}
	var doc struct {
		Services map[string]struct {
			Volumes []any `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatal(err)
	}
	binds := 0
	for name, svc := range doc.Services {
		for _, v := range svc.Volumes {
			m, ok := v.(map[string]any)
			if !ok {
				continue
			}
			s, _ := m["source"].(string) //nolint:errcheck // non-strings are not deploy binds
			if !strings.HasPrefix(s, "./deploy/") && s != "." {
				continue
			}
			binds++
			bind, _ := m["bind"].(map[string]any) //nolint:errcheck // checked below
			if bind["create_host_path"] != false {
				t.Errorf("%s: %s without create_host_path: false", name, s)
			}
			if s != "." && m["read_only"] != true {
				t.Errorf("%s: %s is not read-only", name, s)
			}
		}
	}
	if binds < 7 {
		t.Fatalf("only %d deploy binds", binds)
	}
}

func TestRenderVariantRefusesBadSources(t *testing.T) {
	t.Parallel()
	src := string(readFile(t, repoRoot(t), composeSource))
	img := mustRef(t, variantImage)
	for name, bad := range map[string]string{
		"no image line":   strings.Replace(src, ComposeImageLine, "x-bekup-image: &bekup-image bekup:dev", 1),
		"two image lines": strings.Replace(src, "name: bekup\n", "name: bekup\n"+ComposeImageLine+"\n", 1),
		"extra var":       strings.Replace(src, "name: bekup\n", "name: bekup\nx-y: ${BEKUP_IMAGE}\n", 1),
		"build":           strings.Replace(src, "    command: [serve]\n", "    command: [serve]\n    build: .\n", 1),
		"missing node":    strings.Replace(src, "  certbot:\n", "  certbot2:\n", 1),
	} {
		if _, err := RenderVariant([]byte(bad), VariantTraefik, DBBundled, &img); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := RenderVariant([]byte(src), VariantTraefik, DBBundled, &imageref.Ref{Repo: "bekup", Tag: "dev"}); err == nil {
		t.Error("unpinned image accepted")
	}
	if _, err := RenderVariant([]byte(src), "caddy", DBBundled, &img); err == nil {
		t.Error("unknown variant accepted")
	}
	// Without an image the line becomes the placeholder of the committed
	// files (deploy/compose/).
	out, err := RenderVariant([]byte(src), VariantOwnProxy, DBBundled, nil)
	if err != nil || !hasLine(string(out), ComposePlaceholderLine) || hasLine(string(out), ComposeImageLine) {
		t.Fatalf("example: %v", err)
	}
	// Setup-first files carry the GENERATED header.
	gen, err := RenderGenerated(bekup.Assets(), VariantTraefik, DBBundled, img)
	if err != nil || !strings.HasPrefix(string(gen), composeHeader) {
		t.Fatalf("generated: %v", err)
	}
}

func TestVariantNames(t *testing.T) {
	t.Parallel()
	for v, name := range map[Variant]string{VariantTraefik: "compose.yaml", VariantOwnProxy: "compose.own-proxy.yaml"} {
		if v.AssetName(DBBundled) != name {
			t.Errorf("%s: %s", v, v.AssetName(DBBundled))
		}
		if got, err := ParseVariant(string(v)); err != nil || got != v {
			t.Errorf("ParseVariant(%s)", v)
		}
	}
	if _, err := ParseVariant("traefik "); err == nil {
		t.Error("bad variant accepted")
	}
	for _, retired := range []string{"traefik-external", "npm"} {
		if _, err := ParseVariant(retired); err == nil || !strings.Contains(err.Error(), "own-proxy") {
			t.Errorf("retired %s: %v", retired, err)
		}
	}
}

// TestExternalDBComments: the external-database form keeps no comment
// about the bundled postgres service, names its own release asset and
// asks for the CA bundle in the install steps.
func TestExternalDBComments(t *testing.T) {
	t.Parallel()
	for _, v := range AllVariants {
		out, err := RenderRelease(v, DBExternal, nil)
		if err != nil {
			t.Fatal(err)
		}
		text := string(out)
		for _, stale := range []string{"postgres/{postgres.crt", "70 for postgres", "runner and postgres each", "postgres/ca.crt"} {
			if strings.Contains(text, stale) {
				t.Errorf("%s external: stale comment %q", v, stale)
			}
		}
		if v == VariantFull {
			continue
		}
		bundled := v.AssetName(DBBundled)
		if strings.Contains(text, "release asset "+bundled+":") || !strings.Contains(text, "release asset "+v.AssetName(DBExternal)+":") {
			t.Errorf("%s external: the header names the wrong release asset", v)
		}
		if !strings.Contains(text, "your PostgreSQL's CA bundle") {
			t.Errorf("%s external: no CA bundle install step", v)
		}
	}
}
