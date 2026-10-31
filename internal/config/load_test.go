package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestSearchOrder(t *testing.T) {
	local := safeDir(t)
	etc := safeDir(t)
	localFile := writeFile(t, local, BekupFileName, validBekupTOML+"[log]\nlevel = \"debug\"\n", 0o600)
	etcFile := writeFile(t, etc, BekupFileName, validBekupTOML+"[log]\nlevel = \"warn\"\n", 0o600)
	missingLocal := filepath.Join(local, "missing.toml")

	t.Run("local beats etc", func(t *testing.T) {
		res, err := LoadBekup(opts([]string{localFile, etcFile}, nil))
		if err != nil {
			t.Fatal(err)
		}
		if res.Path != localFile || res.Config.Log.Level != "debug" {
			t.Fatalf("loaded %s (level %s), want %s", res.Path, res.Config.Log.Level, localFile)
		}
	})

	t.Run("etc used when no local file", func(t *testing.T) {
		res, err := LoadBekup(opts([]string{missingLocal, etcFile}, nil))
		if err != nil {
			t.Fatal(err)
		}
		if res.Path != etcFile || res.Config.Log.Level != "warn" {
			t.Fatalf("loaded %s, want %s", res.Path, etcFile)
		}
	})

	t.Run("explicit --config missing never falls back", func(t *testing.T) {
		o := opts([]string{localFile, etcFile}, nil)
		o.ConfigPath = filepath.Join(local, "nope.toml")
		_, err := LoadBekup(o)
		requireErrContains(t, err, "nope.toml", "--config", "never falls back")
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("want fs.ErrNotExist, got %v", err)
		}
	})

	t.Run("explicit missing fails even in dev mode", func(t *testing.T) {
		o := opts([]string{localFile}, nil)
		o.ConfigPath = filepath.Join(local, "nope.toml")
		o.Dev = true
		_, err := LoadBekup(o)
		requireErrContains(t, err, "never falls back")
	})

	t.Run("BEKUP_CONFIG missing never falls back", func(t *testing.T) {
		_, err := LoadBekup(opts([]string{localFile, etcFile}, map[string]string{EnvConfig: filepath.Join(local, "nope.toml")}))
		requireErrContains(t, err, EnvConfig, "never falls back")
	})

	t.Run("--config beats BEKUP_CONFIG and the search path", func(t *testing.T) {
		o := opts([]string{localFile}, map[string]string{EnvConfig: localFile})
		o.ConfigPath = etcFile
		res, err := LoadBekup(o)
		if err != nil {
			t.Fatal(err)
		}
		if res.Path != etcFile {
			t.Fatalf("loaded %s, want %s", res.Path, etcFile)
		}
	})

	t.Run("BEKUP_CONFIG beats the search path", func(t *testing.T) {
		res, err := LoadBekup(opts([]string{localFile}, map[string]string{EnvConfig: etcFile}))
		if err != nil {
			t.Fatal(err)
		}
		if res.Path != etcFile {
			t.Fatalf("loaded %s, want %s", res.Path, etcFile)
		}
	})

	t.Run("no file is an error outside dev mode", func(t *testing.T) {
		_, err := LoadBekup(opts([]string{missingLocal}, nil))
		if !errors.Is(err, ErrNoConfig) {
			t.Fatalf("want ErrNoConfig, got %v", err)
		}
	})

	t.Run("no file runs on defaults in dev mode", func(t *testing.T) {
		for name, o := range map[string]Options{
			"flag":              {Dev: true, SearchPaths: []string{missingLocal}, LookupEnv: envMap(nil)},
			"BEKUP_DEV":         opts([]string{missingLocal}, map[string]string{EnvDev: "1"}),
			"BEKUP_DEV_ENABLED": opts([]string{missingLocal}, map[string]string{"BEKUP_DEV_ENABLED": "true"}),
		} {
			res, err := LoadBekup(o)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if res.Path != "" || !res.Dev || !res.Config.Dev.Enabled || res.Config.Server.BaseURL != DevBaseURL {
				t.Fatalf("%s: unexpected result %+v", name, res)
			}
			if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "DEV MODE") {
				t.Fatalf("%s: missing dev warning: %v", name, res.Warnings)
			}
		}
	})

	t.Run("runner uses runner.toml defaults", func(t *testing.T) {
		got := DefaultSearchPaths(RunnerFileName)
		want := []string{"./runner.toml", "/etc/bekup/runner.toml"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("got %v want %v", got, want)
		}
	})

	t.Run("unsafe local file does not fall back to etc", func(t *testing.T) {
		bad := writeFile(t, safeDir(t), BekupFileName, validBekupTOML, 0o666)
		_, err := LoadBekup(opts([]string{bad, etcFile}, nil))
		if !errors.Is(err, ErrUnsafeFile) {
			t.Fatalf("want ErrUnsafeFile, got %v", err)
		}
	})
}

func TestUnsafeConfigFiles(t *testing.T) {
	tests := []struct {
		name string
		mode os.FileMode
		ok   bool
	}{
		{"0600", 0o600, true},
		{"0644", 0o644, true},
		{"0640", 0o640, true},
		{"group writable", 0o660, false},
		{"world writable", 0o646, false},
		{"0666", 0o666, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := writeFile(t, safeDir(t), BekupFileName, validBekupTOML, tc.mode)
			_, err := LoadBekup(opts([]string{p}, nil))
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok && !errors.Is(err, ErrUnsafeFile) {
				t.Fatalf("want ErrUnsafeFile, got %v", err)
			}
		})
	}

	t.Run("owned by another user", func(t *testing.T) {
		p := writeFile(t, safeDir(t), BekupFileName, validBekupTOML, 0o600)
		orig := getuid
		getuid = func() int { return orig() + 4242 }
		t.Cleanup(func() { getuid = orig })
		if os.Getuid() == 0 {
			t.Skip("running as root: root-owned files are always accepted")
		}
		_, err := LoadBekup(opts([]string{p}, nil))
		requireErrContains(t, err, "owned by uid")
	})

	t.Run("directory", func(t *testing.T) {
		d := safeDir(t)
		p := filepath.Join(d, BekupFileName)
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := LoadBekup(opts([]string{p}, nil))
		requireErrContains(t, err, "not a regular file")
	})

	t.Run("fifo", func(t *testing.T) {
		p := filepath.Join(safeDir(t), BekupFileName)
		if err := syscall.Mkfifo(p, 0o600); err != nil {
			t.Skip("mkfifo not supported:", err)
		}
		_, err := LoadBekup(opts([]string{p}, nil))
		requireErrContains(t, err, "not a regular file")
	})

	t.Run("symlink into a safe directory is followed", func(t *testing.T) {
		target := writeFile(t, safeDir(t), "real.toml", validBekupTOML, 0o600)
		link := filepath.Join(safeDir(t), BekupFileName)
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadBekup(opts([]string{link}, nil)); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("symlink into a writable directory is rejected", func(t *testing.T) {
		dir := safeDir(t)
		target := writeFile(t, dir, "real.toml", validBekupTOML, 0o600)
		if err := os.Chmod(dir, 0o777); err != nil { //nolint:gosec // G302: deliberately unsafe directory under test
			t.Fatal(err)
		}
		link := filepath.Join(safeDir(t), BekupFileName)
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		_, err := LoadBekup(opts([]string{link}, nil))
		requireErrContains(t, err, "group- or world-writable")
	})

	t.Run("symlink to an unsafe file is rejected", func(t *testing.T) {
		target := writeFile(t, safeDir(t), "real.toml", validBekupTOML, 0o666)
		link := filepath.Join(safeDir(t), BekupFileName)
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		_, err := LoadBekup(opts([]string{link}, nil))
		if !errors.Is(err, ErrUnsafeFile) {
			t.Fatalf("want ErrUnsafeFile, got %v", err)
		}
	})

	t.Run("too large", func(t *testing.T) {
		p := writeFile(t, safeDir(t), BekupFileName, validBekupTOML+"#"+strings.Repeat("x", maxConfigBytes)+"\n", 0o600)
		_, err := LoadBekup(opts([]string{p}, nil))
		requireErrContains(t, err, "larger than")
	})
}

func TestStrictParsing(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{"unknown key", validBekupTOML + "\n[log]\nlevel = \"info\"\nbogus = 1\n", []string{":9:1:", `unknown key "log.bogus"`}},
		{"unknown section", "[nope]\nx = 1\n", []string{":1:", `unknown key "nope"`}},
		{"top-level key", "stray = 1\n" + validBekupTOML, []string{":1:1:", `unknown key "stray"`}},
		{"several unknown keys", "[server]\na = 1\nb = 2\n", []string{`"server.a"`, `"server.b"`, ":2:1:", ":3:1:"}},
		{"wrong type", "[server]\nmax_body_bytes = \"big\"\n", []string{":2:", "max_body_bytes"}},
		{"bad duration", "[server]\nshutdown_timeout = \"soon\"\n", []string{"invalid duration"}},
		{"syntax error", "[server\n", []string{":1:"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := writeFile(t, safeDir(t), BekupFileName, tc.content, 0o600)
			_, err := LoadBekup(opts([]string{p}, nil))
			requireErrContains(t, err, append([]string{p}, tc.want...)...)
		})
	}

	t.Run("runner unknown key", func(t *testing.T) {
		p := writeFile(t, safeDir(t), RunnerFileName, "[docker]\nprivileged = true\n", 0o600)
		_, err := LoadRunner(opts([]string{p}, nil))
		requireErrContains(t, err, ":2:1:", `unknown key "docker.privileged"`)
	})
}

func TestInlineSecrets(t *testing.T) {
	dir := safeDir(t)
	withURL := writeFile(t, dir, "url.toml", validBekupTOML+"\n[database]\nurl = \"postgres://u:hunter22@db/x\"\n", 0o600)
	plain := writeFile(t, dir, "plain.toml", validBekupTOML, 0o600)

	t.Run("file rejected outside dev", func(t *testing.T) {
		_, err := LoadBekup(opts([]string{withURL}, nil))
		requireErrContains(t, err, "database.url", "inline secrets are rejected", "url_file", "(set via file)")
		if strings.Contains(err.Error(), "hunter22") {
			t.Fatal("error leaks the secret")
		}
	})

	t.Run("env rejected outside dev", func(t *testing.T) {
		_, err := LoadBekup(opts([]string{plain}, map[string]string{"BEKUP_SECURITY_MASTER_KEY": "abcdefgh"}))
		requireErrContains(t, err, "security.master_key", "(set via env)", "master_key_file")
	})

	t.Run("flag rejected even in dev", func(t *testing.T) {
		o := opts([]string{plain}, nil)
		o.Dev = true
		o.Flags = map[string]string{"database.url": "postgres://x"}
		_, err := LoadBekup(o)
		requireErrContains(t, err, "command-line flags")
	})

	t.Run("allowed in dev with a warning", func(t *testing.T) {
		o := opts([]string{withURL}, map[string]string{"BEKUP_SECURITY_MASTER_KEY": "abcdefgh"})
		o.Dev = true
		res, err := LoadBekup(o)
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(res.Warnings, "\n")
		for _, want := range []string{"DEV MODE", "database.url", "security.master_key"} {
			if !strings.Contains(joined, want) {
				t.Errorf("warnings miss %q: %v", want, res.Warnings)
			}
		}
		creds, err := res.Config.AppDBCredentials()
		if err != nil || string(creds.URL) != "postgres://u:hunter22@db/x" || !strings.Contains(creds.String(), "url") {
			t.Fatalf("AppDBCredentials = %v, %v", creds, err)
		}
	})

	t.Run("dev.enabled in the file enables dev mode", func(t *testing.T) {
		p := writeFile(t, dir, "dev.toml", "[dev]\nenabled = true\n[database]\nurl = \"postgres://x\"\n", 0o600)
		res, err := LoadBekup(opts([]string{p}, nil))
		if err != nil {
			t.Fatal(err)
		}
		if !res.Dev || res.Config.Server.BaseURL != DevBaseURL {
			t.Fatalf("dev not applied: %+v", res)
		}
	})
}

func TestPrecedence(t *testing.T) {
	dir := safeDir(t)
	withLevel := writeFile(t, dir, "level.toml", validBekupTOML+"[log]\nlevel = \"warn\"\n", 0o600)
	plain := writeFile(t, dir, "plain.toml", validBekupTOML, 0o600)

	tests := []struct {
		name       string
		file       string
		env        map[string]string
		flags      map[string]string
		wantLevel  string
		wantSource Source
	}{
		{"default", plain, nil, nil, "info", SourceDefault},
		{"file beats default", withLevel, nil, nil, "warn", SourceFile},
		{"env beats default", plain, map[string]string{"BEKUP_LOG_LEVEL": "error"}, nil, "error", SourceEnv},
		{"env beats file", withLevel, map[string]string{"BEKUP_LOG_LEVEL": "error"}, nil, "error", SourceEnv},
		{"flag beats default", plain, nil, map[string]string{"log.level": "debug"}, "debug", SourceFlag},
		{"flag beats file", withLevel, nil, map[string]string{"log.level": "debug"}, "debug", SourceFlag},
		{"flag beats env and file", withLevel, map[string]string{"BEKUP_LOG_LEVEL": "error"}, map[string]string{"log.level": "debug"}, "debug", SourceFlag},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := opts([]string{tc.file}, tc.env)
			o.Flags = tc.flags
			res, err := LoadBekup(o)
			if err != nil {
				t.Fatal(err)
			}
			if res.Config.Log.Level != tc.wantLevel || res.Sources["log.level"] != tc.wantSource {
				t.Fatalf("level=%s source=%s, want %s/%s", res.Config.Log.Level, res.Sources["log.level"], tc.wantLevel, tc.wantSource)
			}
			// Untouched keys keep their own source.
			if res.Sources["server.base_url"] != SourceFile || res.Sources["server.read_header_timeout"] != SourceDefault {
				t.Fatalf("unexpected sources: %v", res.Sources)
			}
		})
	}

	t.Run("unknown flag key", func(t *testing.T) {
		o := opts([]string{plain}, nil)
		o.Flags = map[string]string{"log.nope": "x"}
		_, err := LoadBekup(o)
		requireErrContains(t, err, `unknown key "log.nope"`)
	})

	t.Run("invalid env value", func(t *testing.T) {
		_, err := LoadBekup(opts([]string{plain}, map[string]string{"BEKUP_WORKERS_MAX_CONCURRENT_RUNS": "many", "BEKUP_SCHEDULER_ENABLED": "maybe"}))
		requireErrContains(t, err, "BEKUP_WORKERS_MAX_CONCURRENT_RUNS", "BEKUP_SCHEDULER_ENABLED")
	})

	t.Run("dev flag source", func(t *testing.T) {
		o := opts([]string{plain}, nil)
		o.Dev = true
		res, err := LoadBekup(o)
		if err != nil {
			t.Fatal(err)
		}
		if res.Sources["dev.enabled"] != SourceFlag {
			t.Fatalf("dev.enabled source = %s", res.Sources["dev.enabled"])
		}
	})

	t.Run("env list replaces file list", func(t *testing.T) {
		res, err := LoadBekup(opts([]string{plain}, map[string]string{"BEKUP_SERVER_TRUSTED_PROXIES": "10.0.0.0/8, 192.168.1.0/24"}))
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(res.Config.Server.TrustedProxies, ","); got != "10.0.0.0/8,192.168.1.0/24" {
			t.Fatalf("got %s", got)
		}
	})
}

func TestSourcesCoverEveryKey(t *testing.T) {
	p := writeFile(t, safeDir(t), BekupFileName, validBekupTOML, 0o600)
	res, err := LoadBekup(opts([]string{p}, nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Fields() {
		if _, ok := res.Sources[f.Path]; !ok {
			t.Errorf("no source for %s", f.Path)
		}
	}
}
