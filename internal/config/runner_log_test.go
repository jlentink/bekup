package config

import (
	"testing"
)

// TestRunnerLog: runner.toml [log] level and format, their defaults,
// the BEKUP_LOG_* env overrides, the --log-level flag (log.level) on top
// and the validation of both keys.
func TestRunnerLog(t *testing.T) {
	t.Parallel()
	const base = "[listen]\naddress = \"10.0.0.11:7443\"\n"
	load := func(t *testing.T, content string, env, flags map[string]string) (*Result[Runner], error) {
		t.Helper()
		p := writeFile(t, safeDir(t), RunnerFileName, content, 0o600)
		o := opts([]string{p}, env)
		o.Flags = flags
		return LoadRunner(o)
	}
	tests := []struct {
		name         string
		content      string
		env, flags   map[string]string
		level, form  string
		levelSource  Source
		formatSource Source
	}{
		{"default", base, nil, nil, "info", "json", SourceDefault, SourceDefault},
		{"file", base + "[log]\nlevel = \"debug\"\nformat = \"text\"\n", nil, nil, "debug", "text", SourceFile, SourceFile},
		{"env over file", base + "[log]\nlevel = \"debug\"\n", map[string]string{"BEKUP_LOG_LEVEL": "warn", "BEKUP_LOG_FORMAT": "text"}, nil, "warn", "text", SourceEnv, SourceEnv},
		{"flag over env and file", base + "[log]\nlevel = \"warn\"\n", map[string]string{"BEKUP_LOG_LEVEL": "error"}, map[string]string{"log.level": "debug"}, "debug", "json", SourceFlag, SourceDefault},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := load(t, tc.content, tc.env, tc.flags)
			if err != nil {
				t.Fatal(err)
			}
			if got := res.Config.Log; got.Level != tc.level || got.Format != tc.form {
				t.Fatalf("log = %+v, want level %q format %q", got, tc.level, tc.form)
			}
			if res.Sources["log.level"] != tc.levelSource || res.Sources["log.format"] != tc.formatSource {
				t.Fatalf("sources = %s/%s", res.Sources["log.level"], res.Sources["log.format"])
			}
		})
	}

	invalid := []struct {
		name       string
		content    string
		env, flags map[string]string
		want       string
	}{
		{"file level", base + "[log]\nlevel = \"verbose\"\n", nil, nil, "log.level"},
		{"file format", base + "[log]\nformat = \"xml\"\n", nil, nil, "log.format"},
		{"env level", base, map[string]string{"BEKUP_LOG_LEVEL": "DEBUG"}, nil, "log.level"},
		{"flag level", base, nil, map[string]string{"log.level": "trace"}, "log.level"},
		{"unknown key", base + "[log]\ncolor = true\n", nil, nil, `unknown key "log.color"`},
	}
	for _, tc := range invalid {
		t.Run("invalid "+tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := load(t, tc.content, tc.env, tc.flags)
			requireErrContains(t, err, tc.want)
		})
	}
}
