package setup

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jlentink/bekup"
)

// writeRelease saves the release file of v for image as compose.yaml.
func writeRelease(t *testing.T, dir string, v Variant, image string) []byte {
	t.Helper()
	return writeReleaseDB(t, dir, v, DBBundled, image)
}

// writeReleaseDB saves the release file of v with db for image as
// compose.yaml.
func writeReleaseDB(t *testing.T, dir string, v Variant, db DBMode, image string) []byte {
	t.Helper()
	img := mustRef(t, image)
	data, err := RenderRelease(v, db, &img)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathIn(dir, ComposeFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	// The release file as root saves it: world-readable, like the manifest.
	if err := os.Chmod(pathIn(dir, ComposeFile), 0o644); err != nil { //nolint:gosec // G302: compose.yaml is public (manifest mode 0644)
		t.Fatal(err)
	}
	return data
}

// variantAnswers are the answers of a compose-first init (variant and
// image come from compose.yaml).
func variantAnswers(v Variant) Answers {
	a := Answers{Domain: "backup.example.com", DockerGID: 999}
	if v == VariantTraefik {
		a.TLSMode, a.ACMEEmail = TLSACME, "ops@example.com"
	}
	return a
}

func composeFirstInit(t *testing.T, dir string, v Variant, mut func(*Options)) (*bytes.Buffer, error) {
	t.Helper()
	o := testOptions(t, dir, variantAnswers(v))
	o.WriteCompose = false
	if mut != nil {
		mut(&o)
	}
	err := Init(o)
	return bufOf(t, o.Out), err
}

func TestInspectComposeRecognition(t *testing.T) {
	t.Parallel()
	img := mustRef(t, variantImage)
	src, err := RenderRelease(VariantNPM, DBBundled, &img)
	if err != nil {
		t.Fatal(err)
	}
	fsys := bekup.Assets()
	info, err := inspectComposeData(fsys, composeInput{compose: src}, "", "")
	if err != nil || info.Variant != VariantNPM || info.Level != RecognizedExact || len(info.Findings) > 0 {
		t.Fatalf("exact: %+v %v", info, err)
	}
	if info.Image.String() != variantImage || info.Hash != hashOf(src) {
		t.Fatalf("image/hash %+v", info)
	}
	// Comments and formatting only: same YAML model.
	reformatted := strings.ReplaceAll(string(src), "\n  # serve/migrate <-> postgres.\n", "\n")
	reformatted = strings.Replace(reformatted, "cap_drop: [ALL]", "cap_drop:\n      - ALL", 1)
	if info, err := inspectComposeData(fsys, composeInput{compose: []byte(reformatted)}, "", ""); err != nil || info.Variant != VariantNPM || info.Level != RecognizedModel {
		t.Fatalf("model: %+v %v", info, err)
	}
	// Customized: needs --variant, then the lint decides.
	custom := strings.Replace(string(src), "memory: 2G, pids: 512", "memory: 4G, pids: 512", 1)
	if custom == string(src) {
		t.Fatal("test edit did not apply")
	}
	if _, err := inspectComposeData(fsys, composeInput{compose: []byte(custom)}, "", ""); err == nil || !strings.Contains(err.Error(), "customized") {
		t.Fatalf("customized without variant: %v", err)
	}
	info, err = inspectComposeData(fsys, composeInput{compose: []byte(custom)}, VariantNPM, "")
	if err != nil || info.Level != RecognizedCustom || info.Variant != VariantNPM || info.HasErrors() {
		t.Fatalf("customized: %+v %v", info, err)
	}
	// A recognized file must match a given variant; full is setup-first.
	if _, err := inspectComposeData(fsys, composeInput{compose: src}, VariantTraefik, ""); err == nil || !strings.Contains(err.Error(), "is the npm variant") {
		t.Fatalf("mismatch: %v", err)
	}
	if _, err := inspectComposeData(fsys, composeInput{compose: src}, VariantFull, ""); err == nil || !strings.Contains(err.Error(), "setup-first") {
		t.Fatalf("full: %v", err)
	}
	// The image line.
	for name, bad := range map[string]string{
		"example":   strings.Replace(string(src), variantImage, ComposeImageVar, 1),
		"unpinned":  strings.Replace(string(src), variantImage, "registry.example.com/bekup:1.2.3", 1),
		"two lines": strings.Replace(string(src), "name: bekup\n", "name: bekup\nx-bekup-image: &other x\n", 1),
		"no anchor": strings.Replace(string(src), "x-bekup-image: &bekup-image ", "x-bekup-image: ", 1),
		"indented":  strings.Replace(string(src), "x-bekup-image: &bekup-image ", "x-bekup-image:  &bekup-image ", 1),
		"not yaml":  "x-bekup-image: &bekup-image " + variantImage + "\nservices: [\n",
	} {
		if _, err := inspectComposeData(fsys, composeInput{compose: []byte(bad)}, VariantNPM, ""); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func FuzzInspectCompose(f *testing.F) {
	img := mustRef(f, variantImage)
	src, err := RenderRelease(VariantNPM, DBBundled, &img)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(string(src))
	f.Add("x-bekup-image: &bekup-image " + variantImage + "\nservices:\n  setup: {}\n")
	fsys := bekup.Assets()
	f.Fuzz(func(t *testing.T, s string) {
		info, err := inspectComposeData(fsys, composeInput{compose: []byte(s)}, VariantNPM, "")
		if err == nil && info.Level == RecognizedExact && s != string(src) {
			t.Fatal("a different file was recognized as exact")
		}
	})
}

func TestLintCompose(t *testing.T) {
	t.Parallel()
	img := mustRef(t, variantImage)
	src, err := RenderRelease(VariantTraefik, DBBundled, &img)
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	edit := func(old, repl string) string {
		t.Helper()
		if !strings.Contains(s, old) {
			t.Fatalf("no %q", old)
		}
		return strings.Replace(s, old, repl, 1)
	}
	const serveCmd = "    command: [serve]\n"
	for _, tc := range []struct{ name, yaml, want string }{
		{"privileged", edit(serveCmd, serveCmd+"    privileged: true\n"), "`privileged`"},
		{"host pid", edit(serveCmd, serveCmd+"    pid: host\n"), "`pid: host`"},
		{"cap sys_admin", edit(serveCmd, serveCmd+"    cap_add: [SYS_ADMIN]\n"), "capability SYS_ADMIN"},
		{"seccomp off", edit(serveCmd, serveCmd+"    security_opt: [\"seccomp=unconfined\"]\n"), "is not allowed (only no-new-privileges"},
		{"docker socket in serve", edit("      - bekup-scratch:/var/lib/bekup/scratch\n", "      - bekup-scratch:/var/lib/bekup/scratch\n      - /var/run/docker.sock:/var/run/docker.sock\n"), "container engine socket"},
		{"ports on serve", edit(serveCmd, serveCmd+"    ports: [\"8080:8080\"]\n"), "publishes ports"},
		{"other image on serve", edit(serveCmd, serveCmd+"    image: ghcr.io/evil/bekup:1.2.3@"+testDigest2+"\n"), "is not the x-bekup-image"},
		{"unpinned third party", edit("traefik:v3.7.16@sha256:c8a121e4bb1347e8fd52657fab12f2cb70b68f8a018895e2068f92c9e519b9c2", "traefik:v3"), "is not digest-pinned"},
		{"setup changed", edit("    cap_add: [CHOWN]\n", "    cap_add: [CHOWN, FOWNER]\n"), "the setup service differs"},
		{"setup network", edit("    network_mode: none\n    logging:\n      driver: none\n", "    logging:\n      driver: none\n"), "the setup service differs"},
		{"include", "include: [x.yml]\n" + s, "`include` and `extends.file`"},
		{"build", edit(serveCmd, serveCmd+"    build: .\n"), "`build`"},
		{"extends file", edit(serveCmd, serveCmd+"    extends:\n      file: x.yml\n      service: s\n"), "`include` and `extends.file`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fs := lintWith(t, []byte(tc.yaml), nil, VariantTraefik, nil)
			if !hasFinding(fs, SeverityError, tc.want) {
				t.Fatalf("no error %q in %v", tc.want, fs)
			}
		})
	}
	// The override: no setup service, same rules.
	for _, tc := range []struct{ yaml, want string }{
		{"services:\n  setup:\n    environment: {X: 1}\n", "changes the setup service"},
		{"services:\n  serve:\n    ports: [\"80:8080\"]\n", "publishes ports"},
		{"services:\n  postgres:\n    privileged: true\n", "`privileged`"},
		{"services:\n  traefik:\n    volumes: [\"/var/run/docker.sock:/var/run/docker.sock\"]\n", "container engine socket"},
		{"services:\n  runner:\n    image: ghcr.io/jlentink/bekup:1.2.4@" + testDigest2 + "\n", "is not the x-bekup-image"},
	} {
		fs := lintWith(t, nil, []byte(tc.yaml), VariantTraefik, nil)
		if !hasFinding(fs, SeverityError, tc.want) {
			t.Errorf("override %q: no error %q in %v", tc.yaml, tc.want, fs)
		}
	}
	ok := "services:\n  traefik:\n    ports: !override [\"127.0.0.1:443:8443\"]\n  runner:\n    volumes: [\"/var/run/docker.sock:/var/run/docker.sock:ro\"]\n"
	if fs := lintWith(t, nil, []byte(ok), VariantTraefik, nil); len(fs) > 0 {
		t.Fatalf("allowed override: %v", fs)
	}
	if fs := lintWith(t, nil, []byte("services:\n  traefik:\n    image: traefik:v3@"+testDigest2+"\n    ports: [\"80:80\"]\n"), VariantNPM, nil); !hasFinding(fs, SeverityError, "publishes ports") {
		t.Fatal("ports in an external variant")
	}
}

func TestInitComposeFirst(t *testing.T) {
	t.Parallel()
	for _, v := range ReleaseVariants {
		t.Run(string(v), func(t *testing.T) {
			dir := t.TempDir()
			compose := writeRelease(t, dir, v, variantImage)
			out, err := composeFirstInit(t, dir, v, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, dir, ComposeFile); !bytes.Equal(got, compose) {
				t.Fatal("compose.yaml was changed")
			}
			if fi := mustStat(t, pathIn(dir, ComposeFile)); fi.Mode().Perm() != 0o644 {
				t.Fatalf("compose.yaml mode %v", fi.Mode())
			}
			st := mustState(t, dir)
			if st.ComposeMode != ComposeOperator || st.Variant != v || st.ComposeSHA256 != hashOf(compose) || st.Image != variantImage {
				t.Fatalf("state %+v", st)
			}
			if _, ok := st.Assets[ComposeFile]; ok {
				t.Fatal("compose.yaml recorded as generated")
			}
			bt, env := string(readFile(t, dir, BekupTOML)), string(readFile(t, dir, EnvFile))
			if v.External() {
				for _, l := range []string{`listen = "10.213.253.10:8080"`, `trusted_proxies = ["10.213.253.5/32"]`} {
					if !hasLine(bt, l) {
						t.Errorf("bekup.toml lacks %s:\n%s", l, bt)
					}
				}
				for _, l := range []string{"BEKUP_PROXY_NETWORK=bekup-proxy", "SERVE_PROXY_IP=10.213.253.10", "BEKUP_DOMAIN=backup.example.com", "DOCKER_GID=999"} {
					if !hasLine(env, l) {
						t.Errorf(".env lacks %s:\n%s", l, env)
					}
				}
				if st.ProxyNetwork != "bekup-proxy" || st.ProxySubnet != "10.213.253.0/28" || st.ProxyIP != "10.213.253.5" {
					t.Fatalf("state link %+v", st)
				}
				if _, err := os.Stat(pathIn(dir, "deploy/traefik")); !errors.Is(err, os.ErrNotExist) {
					t.Error("deploy/traefik written for an external variant")
				}
				if !strings.Contains(out.String(), "docker network create --internal --subnet 10.213.253.0/28 --gateway 10.213.253.1 --ip-range 10.213.253.12/30 bekup-proxy") {
					t.Errorf("report lacks the network step:\n%s", out)
				}
			} else if !hasLine(bt, `trusted_proxies = ["10.213.250.5/32"]`) || !hasLine(env, "PROXY_TLS_MODE=acme") {
				t.Errorf("bundled config:\n%s\n%s", bt, env)
			}
			if strings.Contains(env, "COMPOSE_PROFILES") {
				t.Errorf("COMPOSE_PROFILES in a variant .env:\n%s", env)
			}
			if (v == VariantTraefikExternal) != strings.Contains(env, "TRAEFIK_ENTRYPOINT=websecure") {
				t.Errorf("traefik label variables:\n%s", env)
			}
			if _, err := os.Stat(pathIn(dir, "deploy/nginx")); !errors.Is(err, os.ErrNotExist) {
				t.Error("deploy/nginx written for a variant without nginx")
			}
			fs, report, err := runCheck(t, dir, nil)
			if err != nil {
				t.Fatalf("check: %v\n%s", err, report)
			}
			for _, f := range fs {
				if !strings.Contains(f.Msg, "CA private key") {
					t.Errorf("unexpected finding %s", f)
				}
			}
		})
	}
}

func TestInitComposeFirstRefusals(t *testing.T) {
	t.Parallel()
	// No compose.yaml.
	dir := t.TempDir()
	if _, err := composeFirstInit(t, dir, VariantTraefik, nil); !errors.Is(err, ErrNoCompose) || !strings.Contains(err.Error(), ReleaseURL) {
		t.Fatalf("no compose: %v", err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("refused init wrote %d entries (%v)", len(entries), err)
	}
	// Image mismatch (BEKUP_IMAGE of another release).
	dir = t.TempDir()
	writeRelease(t, dir, VariantNPM, variantImage)
	if _, err := composeFirstInit(t, dir, VariantNPM, func(o *Options) {
		o.Answers.Image = mustRef(t, "ghcr.io/jlentink/bekup:1.2.3@"+testDigest2)
	}); err == nil || !strings.Contains(err.Error(), "does not match the x-bekup-image") {
		t.Fatalf("image mismatch: %v", err)
	}
	// Variant mismatch.
	if _, err := composeFirstInit(t, dir, VariantNPM, func(o *Options) { o.Answers.Variant = VariantTraefikExternal }); err == nil || !strings.Contains(err.Error(), "is the npm variant") {
		t.Fatalf("variant mismatch: %v", err)
	}
	// A shadow compose file next to compose.yaml.
	if err := os.WriteFile(pathIn(dir, "docker-compose.yml"), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := composeFirstInit(t, dir, VariantNPM, nil); err == nil || !strings.Contains(err.Error(), "docker-compose.yml exists") {
		t.Fatalf("shadow file: %v", err)
	}
	// A customized file that fails the lint.
	dir = t.TempDir()
	data := writeRelease(t, dir, VariantNPM, variantImage)
	bad := strings.Replace(string(data), "    command: [serve]\n", "    command: [serve]\n    privileged: true\n", 1)
	if err := os.WriteFile(pathIn(dir, ComposeFile), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := composeFirstInit(t, dir, VariantNPM, func(o *Options) { o.Answers.Variant = VariantNPM }); err == nil || !strings.Contains(err.Error(), "hardening lint") {
		t.Fatalf("lint: %v", err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Fatalf("refused init wrote files: %d entries (%v)", len(entries), err)
	}
	// A customized file that passes: init warns.
	ok := strings.Replace(string(data), "memory: 2G, pids: 512", "memory: 3G, pids: 512", 1)
	if err := os.WriteFile(pathIn(dir, ComposeFile), []byte(ok), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := composeFirstInit(t, dir, VariantNPM, func(o *Options) { o.Answers.Variant = VariantNPM })
	if err != nil || !strings.Contains(out.String(), "is customized") {
		t.Fatalf("customized: %v\n%s", err, out)
	}
	findings, report, err := runCheck(t, dir, nil)
	if err != nil || !hasFinding(findings, SeverityWarning, "customized (based on the npm variant)") {
		t.Fatalf("check:\n%s", report)
	}
}

func TestInitWriteComposeVariant(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := variantAnswers(VariantTraefikExternal)
	a.Image, a.Variant = mustRef(t, variantImage), VariantTraefikExternal
	a.Link = LinkNetwork{Name: "edge-link", Subnet: "172.31.240.0/28", ProxyIP: "172.31.240.2", ServeIP: "172.31.240.3"}
	a.TraefikEntrypoint, a.TraefikCertResolver = "https", "le"
	mustInit(t, dir, a)
	compose := string(readFile(t, dir, ComposeFile))
	if !strings.HasPrefix(compose, composeHeader) || !strings.Contains(compose, "traefik.enable") {
		t.Fatal("compose.yaml is not the generated traefik-external variant")
	}
	st := mustState(t, dir)
	if st.ComposeMode != ComposeGenerated || st.Variant != VariantTraefikExternal || st.Assets[ComposeFile] == "" {
		t.Fatalf("state %+v", st)
	}
	env := string(readFile(t, dir, EnvFile))
	for _, l := range []string{"BEKUP_PROXY_NETWORK=edge-link", "SERVE_PROXY_IP=172.31.240.3", "TRAEFIK_ENTRYPOINT=https", "TRAEFIK_CERTRESOLVER=le"} {
		if !hasLine(env, l) {
			t.Errorf(".env lacks %s", l)
		}
	}
	if !hasLine(string(readFile(t, dir, BekupTOML)), `trusted_proxies = ["172.31.240.2/32"]`) {
		t.Error("trusted_proxies")
	}
	if _, report, err := runCheck(t, dir, nil); err != nil {
		t.Fatalf("check: %v\n%s", err, report)
	}
}

func TestUpgradeComposeFirst(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRelease(t, dir, VariantTraefik, variantImage)
	if _, err := composeFirstInit(t, dir, VariantTraefik, nil); err != nil {
		t.Fatal(err)
	}
	opts := func() UpgradeOptions {
		o := upgradeOptions(t, dir, variantImage)
		o.WriteCompose, o.Image = false, mustRef(t, variantImage)
		return o
	}
	// No-op: the same file.
	before := snapshot(t, dir)
	o := opts()
	if err := Upgrade(o); err != nil {
		t.Fatal(err)
	}
	if d := diffSnapshots(before, snapshot(t, dir)); len(d) > 0 || !strings.Contains(bufOf(t, o.Out).String(), "no changes") {
		t.Fatalf("no-op upgrade changed %v\n%s", d, o.Out)
	}
	// --write-compose is refused compose-first.
	o = opts()
	o.WriteCompose = true
	if err := Upgrade(o); err == nil || !strings.Contains(err.Error(), "compose-first") {
		t.Fatalf("write-compose: %v", err)
	}
	// The operator replaced compose.yaml with a new release file.
	next := "ghcr.io/jlentink/bekup:1.2.3@" + testDigest2
	data := writeRelease(t, dir, VariantTraefik, next)
	before = snapshot(t, dir)
	o = opts()
	o.Image = mustRef(t, next) // BEKUP_IMAGE of the new setup service
	if err := Upgrade(o); err != nil {
		t.Fatal(err)
	}
	if bad := onlyChanged(before, snapshot(t, dir), StateFileName); len(bad) > 0 {
		t.Fatalf("unexpected changes: %v", bad)
	}
	if !bytes.Equal(readFile(t, dir, ComposeFile), data) {
		t.Fatal("compose.yaml written")
	}
	if st := mustState(t, dir); st.Image != next || st.ComposeSHA256 != hashOf(data) {
		t.Fatalf("state %+v", st)
	}
	// BEKUP_IMAGE must match the file.
	o = opts()
	if err := Upgrade(o); err == nil || !strings.Contains(err.Error(), "does not match the x-bekup-image") {
		t.Fatalf("image mismatch: %v", err)
	}
	// A variant switch is refused.
	writeRelease(t, dir, VariantNPM, next)
	before = snapshot(t, dir)
	o = opts()
	o.Image = mustRef(t, next)
	if err := Upgrade(o); err == nil || !strings.Contains(err.Error(), "variant") {
		t.Fatalf("variant switch: %v", err)
	}
	if d := diffSnapshots(before, snapshot(t, dir)); len(d) > 0 {
		t.Fatalf("refused upgrade changed %v", d)
	}
	// A setup-first deployment needs --write-compose.
	dir2 := t.TempDir()
	mustInit(t, dir2, testAnswers(t, ProxyTraefik))
	o = upgradeOptions(t, dir2, variantImage)
	o.WriteCompose = false
	if err := Upgrade(o); err == nil || !strings.Contains(err.Error(), "--write-compose") {
		t.Fatalf("setup-first without --write-compose: %v", err)
	}
}

func TestCheckComposeFirst(t *testing.T) {
	t.Parallel()
	setupDir := func(t *testing.T, v Variant) string {
		dir := t.TempDir()
		writeRelease(t, dir, v, variantImage)
		if _, err := composeFirstInit(t, dir, v, nil); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	write := func(t *testing.T, dir, rel, s string) {
		if err := os.WriteFile(pathIn(dir, rel), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name    string
		variant Variant
		mut     func(t *testing.T, dir string)
		sev     Severity
		want    string
	}{
		{"/32 rule", VariantNPM, replaceIn(BekupTOML, `["10.213.253.5/32"]`, `["10.213.253.0/28"]`), SeverityError, `want exactly ["10.213.253.5/32"]`},
		{"listen", VariantNPM, replaceIn(BekupTOML, `"10.213.253.10:8080"`, `"0.0.0.0:8080"`), SeverityError, "server.listen"},
		{"serve ip outside subnet", VariantTraefikExternal, func(t *testing.T, dir string) {
			write(t, dir, EnvFile, strings.Replace(string(readFile(t, dir, EnvFile)), "SERVE_PROXY_IP=10.213.253.10", "SERVE_PROXY_IP=10.213.254.10", 1))
		}, SeverityError, "is not inside 10.213.253.0/28"},
		{"network renamed", VariantNPM, replaceIn(EnvFile, "BEKUP_PROXY_NETWORK=bekup-proxy", "BEKUP_PROXY_NETWORK=other"), SeverityError, "BEKUP_PROXY_NETWORK"},
		{"bad entrypoint", VariantTraefikExternal, replaceIn(EnvFile, "TRAEFIK_ENTRYPOINT=websecure", "TRAEFIK_ENTRYPOINT=web`x"), SeverityError, ".env"},
		{"profiles", VariantTraefik, func(t *testing.T, dir string) {
			write(t, dir, EnvFile, string(readFile(t, dir, EnvFile))+"COMPOSE_PROFILES=setup\n")
		}, SeverityWarning, "uses no profiles"},
		{"override privileged", VariantTraefik, func(t *testing.T, dir string) {
			write(t, dir, OverrideFile, "services:\n  runner:\n    privileged: true\n")
		}, SeverityError, "compose.override.yaml: service \"runner\": `privileged`"},
		{"override setup", VariantNPM, func(t *testing.T, dir string) {
			write(t, dir, OverrideFile, "services:\n  setup:\n    network_mode: bridge\n")
		}, SeverityError, "changes the setup service"},
		{"override multi-document", VariantTraefik, func(t *testing.T, dir string) {
			write(t, dir, OverrideFile, "services: {}\n---\nservices:\n  runner:\n    privileged: true\n")
		}, SeverityError, "more than one YAML document"},
		{"override host bind from .env", VariantNPM, func(t *testing.T, dir string) {
			write(t, dir, EnvFile, string(readFile(t, dir, EnvFile))+"EXTRA_SRC=/\n")
			write(t, dir, OverrideFile, "services:\n  runner:\n    volumes: [\"${EXTRA_SRC:-./extra}:/extra:ro\"]\n")
		}, SeverityError, `service "runner" bind-mounts "/"`},
		{"customized compose", VariantNPM, func(t *testing.T, dir string) {
			write(t, dir, ComposeFile, strings.Replace(string(readFile(t, dir, ComposeFile)), "    command: [serve]\n", "    command: [serve]\n    ports: [\"8080:8080\"]\n", 1))
		}, SeverityError, "publishes ports"},
		{"variant switch", VariantNPM, func(t *testing.T, dir string) {
			writeRelease(t, dir, VariantTraefikExternal, variantImage)
		}, SeverityError, "is the traefik-external variant"},
		{"newer image", VariantNPM, func(t *testing.T, dir string) {
			writeRelease(t, dir, VariantNPM, "ghcr.io/jlentink/bekup:1.2.4@"+testDigest2)
		}, SeverityWarning, "setup upgrade"},
		{"older image", VariantNPM, func(t *testing.T, dir string) {
			writeRelease(t, dir, VariantNPM, "ghcr.io/jlentink/bekup:1.2.2@"+testDigest2)
		}, SeverityError, "older than the one setup last ran with"},
		{"compose missing", VariantNPM, func(t *testing.T, dir string) {
			if err := os.Remove(pathIn(dir, ComposeFile)); err != nil {
				t.Fatal(err)
			}
		}, SeverityError, "no compose.yaml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := setupDir(t, tc.variant)
			tc.mut(t, dir)
			fs, out, err := runCheck(t, dir, nil)
			if !hasFinding(fs, tc.sev, tc.want) {
				t.Fatalf("no %s %q:\n%s", tc.sev, tc.want, out)
			}
			if tc.sev == SeverityError && !errors.Is(err, ErrCheckFailed) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

// replaceIn edits a file of the deployment (the edit must apply).
func replaceIn(rel, from, to string) func(*testing.T, string) {
	return func(t *testing.T, dir string) {
		t.Helper()
		old := string(readFile(t, dir, rel))
		if !strings.Contains(old, from) {
			t.Fatalf("%s lacks %q", rel, from)
		}
		if err := os.WriteFile(pathIn(dir, rel), []byte(strings.Replace(old, from, to, 1)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
