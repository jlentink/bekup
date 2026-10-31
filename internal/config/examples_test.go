package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// repoFile reads a file relative to the repository root.
func repoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel)) //nolint:gosec // G304: fixed repository paths in tests
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func checkExample(t *testing.T, name string, newCfg func() any, load func(Options) ([]Field, error)) {
	t.Helper()
	content := repoFile(t, name)
	// Copy to a private file so repository checkout permissions don't matter.
	p := writeFile(t, safeDir(t), filepath.Base(name), content, 0o600)

	fields, err := load(opts([]string{p}, nil))
	if err != nil {
		t.Fatalf("%s does not load and validate: %v", name, err)
	}

	var doc map[string]map[string]any
	if err := toml.Unmarshal([]byte(content), &doc); err != nil {
		t.Fatal(err)
	}
	all := fieldsOf(newCfg())
	for i := range all {
		f := &all[i]
		if f.InlineSecret || commentedExampleKeys[f.Path] {
			// Inline secrets are dev-only: they must be documented as a
			// commented-out line in their section, never set.
			if _, set := doc[f.Section][f.Key]; set {
				t.Errorf("%s sets the inline secret or alternative-mode key %s", name, f.Path)
			}
			re := regexp.MustCompile(`(?m)^#\s*` + regexp.QuoteMeta(f.Key) + `\s*=`)
			if !re.MatchString(sectionText(content, f.Section)) {
				t.Errorf("%s does not document the dev-only or alternative-mode key %s as a comment", name, f.Path)
			}
			continue
		}
		if _, ok := doc[f.Section][f.Key]; !ok {
			t.Errorf("%s does not cover %s", name, f.Path)
		}
	}
	if len(fields) == 0 {
		t.Fatal("no fields")
	}
}

// commentedExampleKeys are keys of the alternative [database] mode (fields
// mode, D-207: they cannot be combined with the URL files the example
// sets) and optional keys without a sensible example value. The example
// documents them as commented-out lines.
var commentedExampleKeys = map[string]bool{
	"database.host": true, "database.port": true, "database.name": true, "database.user": true,
	"database.password_file": true, "database.owner_user": true, "database.owner_password_file": true,
	"database.single_account": true, "database.tls_server_name": true,
}

// sectionText returns the text of [section] up to the next section header.
func sectionText(content, section string) string {
	_, rest, ok := strings.Cut(content, "\n["+section+"]\n")
	if !ok {
		return ""
	}
	if i := strings.Index(rest, "\n["); i >= 0 {
		return rest[:i]
	}
	return rest
}

func TestBekupExampleCoversEveryKey(t *testing.T) {
	checkExample(t, "bekup.example.toml", func() any { c := DefaultBekup(); return &c }, func(o Options) ([]Field, error) {
		res, err := LoadBekup(o)
		if err != nil {
			return nil, err
		}
		if res.Dev {
			t.Error("example must not enable dev mode")
		}
		return res.Fields(), nil
	})
}

func TestRunnerExampleCoversEveryKey(t *testing.T) {
	checkExample(t, "runner.example.toml", func() any { c := DefaultRunner(); return &c }, func(o Options) ([]Field, error) {
		res, err := LoadRunner(o)
		if err != nil {
			return nil, err
		}
		return res.Fields(), nil
	})
}

func TestDocsReferenceUpToDate(t *testing.T) {
	doc := repoFile(t, "docs/configuration.md")
	_, rest, ok := strings.Cut(doc, ReferenceBegin)
	if !ok {
		t.Fatal("begin marker missing in docs/configuration.md")
	}
	got, _, ok := strings.Cut(rest, ReferenceEnd)
	if !ok {
		t.Fatal("end marker missing in docs/configuration.md")
	}
	var want strings.Builder
	if err := WriteReference(&want); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got) != strings.TrimSpace(want.String()) {
		t.Fatal("docs/configuration.md key reference is out of date; run: go run ./internal/config/gendocs")
	}
	b := DefaultBekup()
	r := DefaultRunner()
	for _, f := range append(fieldsOf(&b), fieldsOf(&r)...) {
		if !strings.Contains(doc, "`"+f.Env+"`") || !strings.Contains(doc, "`["+f.Section+"]`") {
			t.Errorf("docs do not mention %s (%s)", f.Path, f.Env)
		}
	}
}
