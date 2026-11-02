package setup

import (
	"archive/zip"
	"bytes"
	"path"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestConfigFilesHoldNoSecrets: no file of the config zip (D-217) is key
// material in any manifest form, nor below the keys directory.
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
		if want := readFile(t, dir, strings.TrimPrefix(f.Name, "bekup/")); !bytes.Equal(got.Bytes(), want) {
			t.Errorf("%s differs", f.Name)
		}
		if bytes.Contains(got.Bytes(), []byte("PRIVATE KEY")) || bytes.Contains(got.Bytes(), []byte("AGE-SECRET-KEY")) {
			t.Errorf("%s holds key material", f.Name)
		}
	}
	for _, want := range []string{"bekup/" + EnvFile, "bekup/" + BekupTOML, "bekup/" + RunnerTOML, "bekup/" + StateFileName} {
		if !slices.Contains(names, want) {
			t.Errorf("zip lacks %s: %v", want, names)
		}
	}
}
