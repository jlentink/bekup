package setup

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/jlentink/bekup"
)

// The embedded assets are byte-equal to the repository files, and every
// file below the embedded deploy directories is embedded.
func TestEmbeddedAssetsEqualRepo(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	repo := os.DirFS(root)
	fsys := bekup.Assets()
	n := 0
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		n++
		got, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		want, err := fs.ReadFile(repo, p)
		if err != nil {
			return err
		}
		if !bytes.Equal(got, want) {
			t.Errorf("embedded %s differs from the repository file (rebuild)", p)
		}
		if strings.HasPrefix(p, "deploy/dev") {
			t.Errorf("deploy/dev must never be embedded: %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range assetDirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel := mustRel(t, root, p)
			if _, err := fs.Stat(fsys, rel); err != nil {
				t.Errorf("%s is not embedded (hidden files are not embedded; rename it)", rel)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if n < 10 {
		t.Fatalf("only %d embedded files", n)
	}
}

type composeDoc struct {
	Services map[string]struct {
		User    string `yaml:"user"`
		Volumes []any  `yaml:"volumes"`
		Secrets []any  `yaml:"secrets"`
	} `yaml:"services"`
	Secrets map[string]struct {
		File string `yaml:"file"`
	} `yaml:"secrets"`
	Configs map[string]struct {
		File string `yaml:"file"`
	} `yaml:"configs"`
}

func loadCompose(t *testing.T) composeDoc {
	t.Helper()
	var doc composeDoc
	if err := yaml.Unmarshal(readFile(t, repoRoot(t), composeSource), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// Every ./deploy/... bind source of each variant is embedded in the
// variant's asset set, and every asset of the set is mounted.
func TestDeploySourcesEmbedded(t *testing.T) {
	t.Parallel()
	for _, v := range AllVariants {
		t.Run(string(v), func(t *testing.T) {
			out, err := RenderRelease(v, DBBundled, nil)
			if err != nil {
				t.Fatal(err)
			}
			var doc composeDoc
			if err := yaml.Unmarshal(out, &doc); err != nil {
				t.Fatal(err)
			}
			assets, err := assetFiles(bekup.Assets(), v.assetDirs(DBBundled))
			if err != nil {
				t.Fatal(err)
			}
			var sources []string
			for name, svc := range doc.Services {
				for _, vol := range svc.Volumes {
					var src string
					switch vol := vol.(type) {
					case string:
						src = strings.SplitN(vol, ":", 2)[0]
					case map[string]any:
						if s, ok := vol["source"].(string); ok {
							src = s
						}
					}
					if !strings.HasPrefix(src, "./deploy/") {
						continue
					}
					p := strings.TrimPrefix(src, "./")
					sources = append(sources, p)
					found := false
					for _, a := range assets {
						if a == p || strings.HasPrefix(a, p+"/") {
							found = true
						}
					}
					if !found {
						t.Errorf("service %s mounts %s, which is not in the variant's assets", name, src)
					}
				}
			}
			for _, a := range assets {
				used := false
				for _, s := range sources {
					if a == s || strings.HasPrefix(a, s+"/") {
						used = true
					}
				}
				if !used {
					t.Errorf("asset %s is not mounted by any service", a)
				}
			}
		})
	}
}

// The manifest owner of every secret file equals the user: of each service
// that consumes it; public files are world-readable; the compose secret
// paths and configs are exactly the manifest's.
func TestManifestMatchesComposeUsers(t *testing.T) {
	t.Parallel()
	doc := loadCompose(t)
	l := Layout{KeysDir: DefaultKeysDir}
	byPath := map[string]Entry{}
	for _, e := range Manifest(l, nil, ManifestOptions{}) {
		byPath[e.Path] = e
	}
	fileOf := func(name string) string {
		f := doc.Secrets[name].File
		return strings.Replace(f, "${BEKUP_KEYS_DIR:-./secrets}", DefaultKeysDir, 1)
	}
	for name := range doc.Secrets {
		if _, ok := byPath[fileOf(name)]; !ok {
			t.Errorf("compose secret %s (%s) has no manifest entry", name, fileOf(name))
		}
	}
	checked := 0
	for svcName, svc := range doc.Services {
		for _, s := range svc.Secrets {
			name, ok := s.(string)
			if !ok {
				if m, isMap := s.(map[string]any); isMap {
					name, _ = m["source"].(string) //nolint:errcheck // a missing source fails the lookup below
				}
			}
			e, ok := byPath[fileOf(name)]
			if !ok {
				t.Errorf("%s: secret %s not in the manifest", svcName, name)
				continue
			}
			if !e.Secret {
				if e.Mode&0o004 == 0 {
					t.Errorf("%s: public file %s is not world-readable", svcName, e.Path)
				}
				continue
			}
			uid := strings.SplitN(svc.User, ":", 2)[0]
			if svc.User == "" || uid != strconv.Itoa(e.UID) {
				t.Errorf("%s (user %q) reads %s, but the manifest owner is %d", svcName, svc.User, e.Path, e.UID)
			}
			if e.Mode != 0o400 {
				t.Errorf("%s: mode %04o, want 0400", e.Path, e.Mode)
			}
			checked++
		}
	}
	if checked < 8 {
		t.Fatalf("only %d secret uses checked", checked)
	}
	for name, c := range doc.Configs {
		if _, ok := byPath[strings.TrimPrefix(c.File, "./")]; !ok {
			t.Errorf("config %s (%s) has no manifest entry", name, c.File)
		}
	}
}

// The rendered TOML files pass the bekup validators for every proxy choice.
func TestRenderedConfigsValidate(t *testing.T) {
	t.Parallel()
	variants := map[string]func() Answers{
		"traefik": func() Answers { return testAnswers(t, ProxyTraefik) },
		"nginx-static": func() Answers {
			a := testAnswers(t, ProxyNginx)
			a.TLSMode, a.ACMEEmail = TLSStatic, ""
			return a
		},
		"none": func() Answers { return testAnswers(t, ProxyNone) },
		"custom-network": func() Answers {
			a := testAnswers(t, ProxyTraefik)
			a.Network = Network{RunnerCtlSubnet: "172.30.2.0/24", RunnerCtlIP: "172.30.2.10", MetricsSubnet: "172.30.3.0/24", ServeMetricsIP: "172.30.3.10"}
			a.TargetCIDRs, a.TargetHosts = nil, nil
			return a
		},
	}
	for name, mk := range variants {
		t.Run(name, func(t *testing.T) {
			a := mk()
			if err := a.Validate(); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			for f, data := range operatorFiles(&a, "1.2.3") {
				if err := os.WriteFile(pathIn(dir, f), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := validateConfigs(dir); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEnvFile(t *testing.T) {
	t.Parallel()
	a := testAnswers(t, ProxyTraefik)
	a.ACMEStaging = true
	a.Network.RunnerCtlSubnet, a.Network.RunnerCtlIP = "172.30.2.0/24", "172.30.2.10"
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	env := string(renderEnv(&a, "1.2.3"))
	for _, l := range []string{
		"COMPOSE_PROFILES=traefik", "BEKUP_DOMAIN=backup.example.com", "PROXY_TLS_MODE=acme",
		"ACME_EMAIL=ops@example.com", "ACME_CA_SERVER=" + ACMEStagingURL, "DOCKER_GID=999",
		"BEKUP_KEYS_DIR=./secrets", "RUNNER_CTL_SUBNET=172.30.2.0/24", "RUNNER_CTL_IP=172.30.2.10",
		"BEKUP_SETUP_VERSION=1.2.3",
	} {
		if !hasLine(env, l) {
			t.Errorf(".env lacks %q:\n%s", l, env)
		}
	}
	if strings.Contains(env, "FRONTEND_SUBNET") || strings.Contains(env, "\nBEKUP_IMAGE=") || strings.Contains(env, "\nBEKUP_IMAGE_MODE=") {
		t.Errorf("unexpected variables:\n%s", env)
	}
	// D-212: following a tag records the image mode.
	f := a
	f.Image = mustRef(t, "ghcr.io/jlentink/bekup:latest")
	if env := string(renderEnv(&f, "v1.2.3")); !hasLine(env, "BEKUP_IMAGE_MODE=following") || !hasLine(env, "BEKUP_SETUP_VERSION=1.2.3") {
		t.Errorf("following .env:\n%s", env)
	}
	n := testAnswers(t, ProxyNone)
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	env = string(renderEnv(&n, "1.2.3"))
	if !hasLine(env, "COMPOSE_PROFILES=") || strings.Contains(env, "ACME") || strings.Contains(env, "PROXY_TLS_MODE") {
		t.Errorf("proxy none .env:\n%s", env)
	}
}

func TestOverrideOnlyForStaticTLS(t *testing.T) {
	t.Parallel()
	a := testAnswers(t, ProxyNginx)
	a.TLSMode, a.ACMEEmail = TLSStatic, ""
	files := operatorFiles(&a, "1.2.3")
	ov, ok := files[OverrideFile]
	if !ok {
		t.Fatal("no override for static TLS")
	}
	var doc map[string]any
	if err := yaml.Unmarshal(ov, &doc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ov), "target: /certs") || !strings.Contains(string(ov), "certbot:") {
		t.Fatalf("override:\n%s", ov)
	}
	if _, ok := operatorFiles(&Answers{Proxy: ProxyTraefik, TLSMode: TLSACME}, "1.2.3")[OverrideFile]; ok {
		t.Fatal("override for ACME")
	}
}

func TestManifestTableInDocs(t *testing.T) {
	t.Parallel()
	doc := string(readFile(t, repoRoot(t), "docs/installation/compose.md"))
	const begin, end = "<!-- setup-manifest:begin -->\n", "<!-- setup-manifest:end -->"
	i, j := strings.Index(doc, begin), strings.Index(doc, end)
	if i < 0 || j < i {
		t.Fatal("docs/installation/compose.md lacks the setup-manifest markers")
	}
	if got, want := doc[i+len(begin):j], ManifestTable(Layout{KeysDir: DefaultKeysDir}); got != want {
		t.Fatalf("manifest table in docs/installation/compose.md is stale; replace it with:\n%s", want)
	}
}

func TestIsAssetPath(t *testing.T) {
	t.Parallel()
	for p, want := range map[string]bool{
		ComposeFile: true, "deploy/traefik/x.yml": true, "reference/bekup.example.toml": true,
		"deploy/dev/x": false, "deploy/traefik/../../x": false, ".env": false, "secrets/x": false,
		"reference/other.toml": false, "/etc/passwd": false,
	} {
		if got := isAssetPath(p); got != want {
			t.Errorf("isAssetPath(%q) = %v", p, got)
		}
	}
}

// .dockerignore must not exclude an embedded asset (the image build would
// fail or, worse, embed nothing): a simplified dockerignore matcher.
func TestDockerignoreKeepsEmbeddedAssets(t *testing.T) {
	t.Parallel()
	data := string(readFile(t, repoRoot(t), ".dockerignore"))
	var patterns []string
	for _, l := range strings.Split(data, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "!") {
			continue
		}
		patterns = append(patterns, strings.TrimSuffix(strings.TrimPrefix(l, "/"), "/"))
	}
	matches := func(pat, p string) bool {
		// Match the path and every parent directory; "**/" matches any
		// number of leading directories.
		parts := strings.Split(p, "/")
		for i := len(parts); i > 0; i-- {
			sub := strings.Join(parts[:i], "/")
			if ok, err := filepath.Match(pat, sub); err == nil && ok {
				return true
			}
			if rest, ok := strings.CutPrefix(pat, "**/"); ok {
				for j := 0; j < i; j++ {
					if ok, err := filepath.Match(rest, strings.Join(parts[j:i], "/")); err == nil && ok {
						return true
					}
				}
			}
		}
		return false
	}
	err := fs.WalkDir(bekup.Assets(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		for _, pat := range patterns {
			if matches(pat, p) {
				t.Errorf(".dockerignore pattern %q excludes the embedded asset %s", pat, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !matches("deploy/dev", "deploy/dev/x") || !matches("**/*.key", "a/b/c.key") {
		t.Fatal("matcher self-test")
	}
}
