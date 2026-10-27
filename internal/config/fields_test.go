package config

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sampleOverride returns an env value that differs from the field's current
// value, and the value it must produce.
func sampleOverride(t *testing.T, f *Field) (string, any) {
	t.Helper()
	switch v := f.Value().(type) {
	case string:
		return v + "-override", v + "-override"
	case bool:
		return strconv.FormatBool(!v), !v
	case int:
		return strconv.Itoa(v + 7), v + 7
	case int64:
		return strconv.FormatInt(v+7, 10), v + 7
	case float64:
		return fmt.Sprint(v + 0.5), v + 0.5
	case Duration:
		d := v.D() + 90*time.Second
		return d.String(), Duration(d)
	case []string:
		return " alpha, beta ,,", []string{"alpha", "beta"}
	default:
		t.Fatalf("%s: no sample for %T", f.Path, v)
		return "", nil
	}
}

func checkEnvMapping(t *testing.T, newCfg func() any) {
	t.Helper()
	seen := map[string]string{}
	all := fieldsOf(newCfg())
	for i := range all {
		f := &all[i]
		want := "BEKUP_" + strings.ToUpper(strings.ReplaceAll(f.Path, ".", "_"))
		if f.Env != want {
			t.Errorf("%s: env %s, want %s", f.Path, f.Env, want)
		}
		if f.Env == EnvConfig || f.Env == EnvDev {
			t.Errorf("%s: env %s collides with a reserved variable", f.Path, f.Env)
		}
		if other, dup := seen[f.Env]; dup {
			t.Errorf("%s and %s share env %s", f.Path, other, f.Env)
		}
		seen[f.Env] = f.Path
		if f.Doc == "" {
			t.Errorf("%s: missing doc tag", f.Path)
		}

		t.Run(f.Env, func(t *testing.T) {
			cfg := newCfg()
			fields := fieldsOf(cfg)
			byPath := map[string]Field{}
			sources := map[string]Source{}
			var target *Field
			for i := range fields {
				byPath[fields[i].Path] = fields[i]
				sources[fields[i].Path] = SourceDefault
				if fields[i].Path == f.Path {
					target = &fields[i]
				}
			}
			raw, want := sampleOverride(t, target)
			errs := applyOverrides(fields, byPath, sources, envMap(map[string]string{f.Env: raw}), nil)
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			if got := target.Value(); !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v, want %#v", got, want)
			}
			for p, s := range sources {
				if p == f.Path && s != SourceEnv {
					t.Fatalf("source %s, want env", s)
				}
				if p != f.Path && s != SourceDefault {
					t.Fatalf("%s changed source to %s", p, s)
				}
			}
		})
	}
}

func TestEnvMappingEveryBekupKey(t *testing.T) {
	checkEnvMapping(t, func() any { c := DefaultBekup(); return &c })
}

func TestEnvMappingEveryRunnerKey(t *testing.T) {
	checkEnvMapping(t, func() any { c := DefaultRunner(); return &c })
}

func TestSecretTags(t *testing.T) {
	b := DefaultBekup()
	r := DefaultRunner()
	all := append(fieldsOf(&b), fieldsOf(&r)...)
	inline := map[string]bool{}
	for _, f := range all {
		if f.InlineSecret {
			inline[f.Path] = true
			if f.Type != "string" || !f.IsZero() {
				t.Errorf("%s: inline secrets must be strings with an empty default", f.Path)
			}
		}
		if f.SecretFile && !strings.HasSuffix(f.Key, "_file") {
			t.Errorf("%s: secret file keys must end in _file", f.Path)
		}
		// Keys that look like they hold secret material must be tagged.
		if strings.HasSuffix(f.Key, "_length") || strings.HasSuffix(f.Key, "_score") {
			continue
		}
		if strings.Contains(f.Key, "key") || strings.Contains(f.Key, "password") || strings.Contains(f.Key, "secret") {
			if !f.InlineSecret && !f.SecretFile {
				t.Errorf("%s looks secret but has no secret tag", f.Path)
			}
		}
	}
	for _, want := range []string{"database.url", "security.master_key"} {
		if !inline[want] {
			t.Errorf("%s must be an inline (dev-only) secret", want)
		}
	}
}

func TestDuration(t *testing.T) {
	var d Duration
	if err := d.UnmarshalText([]byte("1h30m")); err != nil || d.D() != 90*time.Minute {
		t.Fatalf("got %v %v", d, err)
	}
	if err := d.UnmarshalText([]byte("90")); err == nil {
		t.Fatal("unitless duration accepted")
	}
	if b, err := d.MarshalText(); err != nil || string(b) != "1h30m0s" {
		t.Fatalf("marshal %s", b)
	}
}
