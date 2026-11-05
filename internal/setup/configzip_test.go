package setup

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestConfigFilesHoldNoSecrets: no file of the config zip (D-217) is key
// material in any manifest form, nor below the keys directory; and the
// zip's content holds no value of any file below the keys directory, no
// private key, and of .env only the variables setup manages
// (TestConfigZipContents covers the operator's own changes).
func TestConfigFilesHoldNoSecrets(t *testing.T) {
	t.Parallel()
	l := Layout{KeysDir: "secrets"}
	for _, o := range []ManifestOptions{{}, {StaticTLS: true, Overlays: true}, {ExternalDB: true}, {ComposeOperator: true, ExternalDB: true, Overlays: true}} {
		for _, e := range Manifest(l, assetDirs, o) {
			if !slices.Contains(configFiles, e.Path) {
				continue
			}
			if e.Secret || e.Class == ClassSecret || e.Group != "" {
				t.Errorf("%+v: config file %s is key material", o, e.Path)
			}
		}
	}
	for _, f := range configFiles {
		if strings.Contains(f, "/") || path.Base(f) != f {
			t.Errorf("%s is not a top-level file", f)
		}
	}
	for name, mk := range zipDeployments() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			a := mk(t)
			mustInit(t, dir, a)
			entries, notes, err := configZipEntries(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(notes) > 0 {
				t.Errorf("notes for an untouched deployment: %v", notes)
			}
			secrets := keysDirValues(t, dir, mustState(t, dir).KeysDir)
			if len(secrets) == 0 {
				t.Fatal("no secret values found below the keys directory")
			}
			keys := managedEnvKeys()
			for _, e := range entries {
				for _, s := range secrets {
					if bytes.Contains(e.data, s) {
						t.Errorf("%s holds the content of a file below the keys directory", e.name)
					}
				}
				for _, marker := range []string{"PRIVATE KEY", "AGE-SECRET-KEY"} {
					if bytes.Contains(e.data, []byte(marker)) {
						t.Errorf("%s holds %q", e.name, marker)
					}
				}
				for _, m := range dbURLRe.FindAllSubmatch(e.data, -1) {
					if string(m[1]) != "..." {
						t.Errorf("%s holds a database URL with a password", e.name)
					}
				}
				if e.name == EnvFile {
					for l := range strings.SplitSeq(string(e.data), "\n") {
						if k, _, ok := strings.Cut(l, "="); ok && !strings.HasPrefix(l, "#") && !keys[k] {
							t.Errorf(".env in the zip carries %s", k)
						}
					}
				}
			}
			if a.ExternalAPI && !slices.ContainsFunc(entries, func(e zipEntry) bool { return e.name == ExternalAPITOML }) {
				t.Error("the zip lacks external-api.toml")
			}
		})
	}
}

// dbURLRe matches a PostgreSQL URL with a password (the compose comments
// show the form with "...").
var dbURLRe = regexp.MustCompile(`postgres(?:ql)?://[^:/@\s]+:([^@\s]+)@`)

// zipDeployments are deployment shapes for the zip's content checks.
func zipDeployments() map[string]func(t *testing.T) Answers {
	return map[string]func(t *testing.T) Answers{
		"traefik bundled external api": func(t *testing.T) Answers {
			a := testAnswers(t, ProxyTraefik)
			a.ExternalAPI = true
			return a
		},
		"own proxy labels db container": func(t *testing.T) Answers {
			a := testAnswers(t, ProxyNone)
			ownProxyJoin(&a)
			a.ProxyLabels, a.TraefikEntrypoint, a.TraefikCertResolver = true, "websecure", "le"
			a.DB = Database{Mode: DBExternal, Location: DBDocker, Network: "dbnet", Host: "postgres", TLS: DBTLSDisable, Auth: DBAuthBootstrap, AdminUser: "postgres"}
			return a
		},
	}
}

// keysDirValues returns the trimmed contents (8 bytes or more) of every
// regular file below the keys directory (read through an os.Root).
func keysDirValues(t *testing.T, dir, keysDir string) [][]byte {
	t.Helper()
	root, err := os.OpenRoot(filepath.Join(dir, keysDir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	}()
	var out [][]byte
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		b, err := fs.ReadFile(root.FS(), p)
		if err != nil {
			return err
		}
		if b = bytes.TrimSpace(b); len(b) >= 8 {
			out = append(out, b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestConfigZipContents: the operator's own .env lines and a changed
// compose.override.yaml stay on the server (D-217 amendment 1), with a
// note; an untouched override is included as written.
func TestConfigZipContents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustInit(t, dir, zipDeployments()["own proxy labels db container"](t))
	override := readFile(t, dir, OverrideFile)
	entries, _, err := configZipEntries(dir)
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.IndexFunc(entries, func(e zipEntry) bool { return e.name == OverrideFile }); i < 0 || !bytes.Equal(entries[i].data, override) {
		t.Fatal("the untouched override is not in the zip as written")
	}
	const own = "CF_DNS_API_KEY=cf-0123456789-abcdef"
	env := append(readFile(t, dir, EnvFile), []byte(own+"\n")...)
	if err := os.WriteFile(filepath.Join(dir, EnvFile), env, 0o600); err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(override), "services:\n", "services:\n  traefik-own:\n    environment:\n      CF_DNS_API_KEY: cf-0123456789-abcdef\n", 1)
	if err := os.WriteFile(filepath.Join(dir, OverrideFile), []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, notes, err := configZipEntries(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.name == OverrideFile || bytes.Contains(e.data, []byte("cf-0123456789")) {
			t.Errorf("%s: the operator's own value reached the zip", e.name)
		}
		if e.name == EnvFile && !bytes.Contains(e.data, []byte("BEKUP_DB_NETWORK=dbnet")) {
			t.Errorf(".env lost a managed line:\n%s", e.data)
		}
	}
	if len(notes) != 2 || !strings.Contains(notes[0]+notes[1], OverrideFile+": left out") || !strings.Contains(notes[0]+notes[1], EnvFile+": only the variables") {
		t.Fatalf("notes %v", notes)
	}
	if got, err := ConfigZipNotes(dir); err != nil || !slices.Equal(got, notes) {
		t.Fatalf("ConfigZipNotes %v %v", got, err)
	}
}

func TestWriteConfigZip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := WriteConfigZip(dir, &buf, time.Now()); err == nil {
		t.Fatal("a zip without a deployment")
	}
	writeReleaseDB(t, dir, VariantOwnProxy, DBBundled, variantImage)
	if out, err := composeFirstInit(t, dir, VariantOwnProxy, nil); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	buf.Reset()
	if err := WriteConfigZip(dir, &buf, time.Now()); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		names = append(names, f.Name)
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var got bytes.Buffer
		if _, err := got.ReadFrom(rc); err != nil {
			t.Fatal(err)
		}
		if err := rc.Close(); err != nil {
			t.Fatal(err)
		}
		want := readFile(t, dir, strings.TrimPrefix(f.Name, "bekup/"))
		if f.Name == "bekup/"+EnvFile {
			want, _ = filterEnv(want)
		}
		if !bytes.Equal(got.Bytes(), want) {
			t.Errorf("%s differs", f.Name)
		}
	}
	for _, want := range []string{"bekup/" + EnvFile, "bekup/" + BekupTOML, "bekup/" + RunnerTOML, "bekup/" + StateFileName} {
		if !slices.Contains(names, want) {
			t.Errorf("zip lacks %s: %v", want, names)
		}
	}
}

// TestManagedEnvKeys: every .env variable setup writes is one the zip
// keeps (managedEnvKeys covers envVars).
func TestManagedEnvKeys(t *testing.T) {
	t.Parallel()
	keys := managedEnvKeys()
	shapes := zipDeployments()
	for name, mk := range shapes {
		a := mk(t)
		a.ImageInEnv = true
		a.Network.FrontendSubnet = "172.30.9.0/24"
		for _, v := range envVars(&a, "1.2.3") {
			if !keys[v.Key] {
				t.Errorf("%s: setup writes %s, the zip drops it", name, v.Key)
			}
		}
	}
	link := testAnswers(t, ProxyNone)
	ownProxyLink(&link)
	full := testAnswers(t, ProxyNginx)
	full.Variant = VariantFull
	for _, a := range []Answers{link, full} {
		for _, v := range envVars(&a, "1.2.3") {
			if !keys[v.Key] {
				t.Errorf("setup writes %s, the zip drops it", v.Key)
			}
		}
	}
}
