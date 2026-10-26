package config

import (
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestWriteRedacted(t *testing.T) {
	p := writeFile(t, safeDir(t), BekupFileName, validBekupTOML+"[database]\nurl = \"postgres://bekup:hunter22@db:5432/bekup\"\n", 0o600)
	o := opts([]string{p}, map[string]string{"BEKUP_SECURITY_MASTER_KEY": "super-secret-master", "BEKUP_LOG_LEVEL": "warn"})
	o.Dev = true
	o.Flags = map[string]string{"log.format": "text"}
	res, err := LoadBekup(o)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := res.WriteRedacted(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, leak := range []string{"hunter22", "super-secret-master"} {
		if strings.Contains(out, leak) {
			t.Fatalf("output leaks %q:\n%s", leak, out)
		}
	}
	for _, want := range []string{
		"# loaded from: " + p,
		"# dev mode: true",
		"[server]",
		"base_url = 'https://backup.example.com'  # file",
		"listen = '10.0.0.10:8080'  # file",
		"read_header_timeout = '10s'  # default",
		"url = '[REDACTED]'  # file",
		"master_key = '[REDACTED]'  # env",
		"level = 'warn'  # env",
		"format = 'text'  # flag",
		"enabled = true  # flag",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}

	// The output is valid TOML for the same schema.
	var c Bekup
	dec := toml.NewDecoder(strings.NewReader(out))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		t.Fatalf("printed config is not valid TOML: %v", err)
	}
}

func TestWriteRedactedDevDefaults(t *testing.T) {
	o := opts([]string{}, nil)
	o.Dev = true
	res, err := LoadRunner(o)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := res.WriteRedacted(&b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "loaded from: none") || !strings.Contains(b.String(), "[targets]") {
		t.Fatalf("unexpected output:\n%s", b.String())
	}
}

func TestRedactURLCredentials(t *testing.T) {
	tests := map[string]string{
		"https://backup.example.com":    "https://backup.example.com",
		"postgres://u:pw@db/x":          "postgres://u:REDACTED@db/x",
		"postgres://u@db/x":             "postgres://u@db/x",
		"not a url @ all":               "not a url @ all",
		"s3://key:secret@bucket/prefix": "s3://key:REDACTED@bucket/prefix",
	}
	for in, want := range tests {
		if got := redactURLCredentials(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
