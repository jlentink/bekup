package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validBekupTOML is the smallest file that validates outside dev mode.
const validBekupTOML = `
[server]
listen = "10.0.0.10:8080"
base_url = "https://backup.example.com"
trusted_proxies = ["172.18.0.0/16"]
`

// writeFile writes content with an explicit mode (independent of umask).
func writeFile(t *testing.T, dir, name, content string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// safeDir returns a private temp dir (t.TempDir creates it with mode 0700).
func safeDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// envMap returns a LookupEnv backed by a map, isolating tests from the real
// environment.
func envMap(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

// environ returns an Environ backed by a map.
func environ(m map[string]string) func() []string {
	return func() []string {
		out := make([]string, 0, len(m))
		for k, v := range m {
			out = append(out, k+"="+v)
		}
		return out
	}
}

// opts returns Options that never touch the real search path or env.
func opts(paths []string, env map[string]string) Options {
	if paths == nil {
		paths = []string{}
	}
	return Options{SearchPaths: paths, LookupEnv: envMap(env), Environ: environ(env)}
}

func requireErrContains(t *testing.T, err error, subs ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q, got nil", subs)
	}
	for _, s := range subs {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error does not contain %q:\n%v", s, err)
		}
	}
}
