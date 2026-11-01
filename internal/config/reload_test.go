package config

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLiveKeys(t *testing.T) {
	want := []string{
		"server.trusted_proxies", "server.trusted_proxy_hosts", "server.max_body_bytes",
		"server.rate_limit_per_second", "server.rate_limit_burst",
		"server.user_rate_limit_per_second", "server.user_rate_limit_burst",
		"graphql.max_complexity", "graphql.max_depth", "graphql.max_body_bytes",
		"security.password_min_length", "security.password_min_score",
		"security.login_rate_limit_per_minute", "security.login_rate_limit_burst",
		"security.lockout_threshold", "security.lockout_base_duration", "security.lockout_max_duration",
		"retention.dry_run", "retention.max_deletions_per_sweep", "retention.partial_cleanup_age",
		"notifications.max_per_user_per_hour", "notifications.collapse_window", "notifications.webhook_failure_threshold",
		"netguard.allow_cidrs", "netguard.allow_hosts",
		"log.level",
	}
	got := LiveKeys()
	if !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))) {
		t.Fatalf("LiveKeys() = %v\nwant %v", got, want)
	}
	for _, k := range want {
		if !IsLiveKey(k) {
			t.Errorf("IsLiveKey(%q) = false", k)
		}
	}
	// Posture keys must stay restart-only (D-200).
	for _, k := range []string{"server.base_url", "server.allow_insecure_http", "server.listen", "database.tls_mode",
		"dev.enabled", "security.master_key_file", "security.argon2_memory_kib", "metrics.listen", "runner.address"} {
		if IsLiveKey(k) {
			t.Errorf("%s must be restart-only", k)
		}
	}
	// The runner config has no live keys.
	var r Runner
	for _, f := range fieldsOf(&r) {
		if f.Live {
			t.Errorf("runner.toml key %s is tagged live", f.Path)
		}
	}
}

func TestDiff(t *testing.T) {
	base := DefaultBekup()
	tests := []struct {
		name   string
		mutate func(c *Bekup)
		want   []string
	}{
		{"unchanged", func(*Bekup) {}, nil},
		{"one live key", func(c *Bekup) { c.Log.Level = "debug" }, []string{"log.level"}},
		{"restart key", func(c *Bekup) { c.Server.BaseURL = "https://other.example" }, []string{"server.base_url"}},
		{"list entry", func(c *Bekup) { c.Netguard.AllowCIDRs = []string{"10.0.0.0/8"} }, []string{"netguard.allow_cidrs"}},
		{"nil vs empty list", func(c *Bekup) { c.Netguard.AllowCIDRs = []string{} }, nil},
		{"duration", func(c *Bekup) { c.Retention.PartialCleanupAge = Duration(2 * time.Hour) }, []string{"retention.partial_cleanup_age"}},
		{"several in order", func(c *Bekup) {
			c.Log.Level = "warn"
			c.Server.Listen = "10.0.0.1:1"
			c.Security.LockoutThreshold = 9
		}, []string{"server.listen", "security.lockout_threshold", "log.level"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next := base.Clone()
			tt.mutate(next)
			if got := Diff(&base, next); !slices.Equal(got, tt.want) {
				t.Fatalf("Diff = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWithLive(t *testing.T) {
	running := DefaultBekup()
	running.Netguard.AllowCIDRs = []string{"10.1.0.0/16"}
	next := running.Clone()
	next.Log.Level = "debug"
	next.Server.BaseURL = "https://changed.example"
	next.Dev.Enabled = true
	next.Netguard.AllowCIDRs = []string{"10.2.0.0/16", "10.3.0.0/16"}

	merged := WithLive(&running, next)
	if merged.Log.Level != "debug" || !slices.Equal(merged.Netguard.AllowCIDRs, next.Netguard.AllowCIDRs) {
		t.Fatalf("live keys not taken from next: %+v %+v", merged.Log, merged.Netguard)
	}
	if merged.Server.BaseURL != running.Server.BaseURL || merged.Dev.Enabled {
		t.Fatalf("restart keys changed: base_url=%q dev=%v", merged.Server.BaseURL, merged.Dev.Enabled)
	}
	// Deep copy: no list is shared with either input.
	next.Netguard.AllowCIDRs[0] = "mutated"
	running.Netguard.AllowCIDRs[0] = "mutated"
	if merged.Netguard.AllowCIDRs[0] != "10.2.0.0/16" {
		t.Fatal("WithLive shares a list with next")
	}
	next.Netguard.AllowCIDRs[0] = "10.2.0.0/16"
	if got := Diff(merged, next); !slices.Equal(got, []string{"server.base_url", "dev.enabled"}) {
		t.Fatalf("Diff(merged, next) = %v, want only the restart keys", got)
	}
}

func TestCloneIsDeep(t *testing.T) {
	c := DefaultBekup()
	c.Server.TrustedProxies = []string{"10.0.0.0/8"}
	d := c.Clone()
	d.Server.TrustedProxies[0] = "x"
	if c.Server.TrustedProxies[0] != "10.0.0.0/8" {
		t.Fatal("Clone shares lists")
	}
}

func TestErrorKeysAndKind(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantKeys []string
		wantKind string
	}{
		{"nil", nil, []string{}, ""},
		{"validation", errors.Join(&KeyError{Path: "log.level", Msg: `log.level: must be one of (got "secretvalue")`},
			&KeyError{Path: "server.base_url", Msg: "x"}), []string{"log.level", "server.base_url"}, ErrorKindInvalid},
		{"duplicates", errors.Join(&KeyError{Path: "log.level"}, fmt.Errorf("wrap: %w", &KeyError{Path: "log.level"})),
			[]string{"log.level"}, ErrorKindInvalid},
		{"unknown key in known section", &KeyError{Path: "server.hunter2"}, []string{"server.unknown_key"}, ErrorKindInvalid},
		{"unknown section", &KeyError{Path: "Pass.Word"}, []string{"unknown_section.unknown_key"}, ErrorKindInvalid},
		{"top-level key", &KeyError{Path: "stray"}, []string{"unknown_section.unknown_key"}, ErrorKindInvalid},
		{"file", &FileError{Err: errors.New("config file /x: group-writable")}, []string{}, ErrorKindFile},
		{"file wrapped", fmt.Errorf("load: %w", &FileError{Err: ErrNoConfig}), []string{}, ErrorKindFile},
		{"syntax", &SyntaxError{Err: errors.New("bad")}, []string{}, ErrorKindSyntax},
		{"other", errors.New("BEKUP_X: invalid"), []string{}, ErrorKindInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ErrorKeys(tt.err); !slices.Equal(got, tt.wantKeys) {
				t.Errorf("ErrorKeys = %v, want %v", got, tt.wantKeys)
			}
			if got := ErrorKindOf(tt.err); got != tt.wantKind {
				t.Errorf("ErrorKindOf = %q, want %q", got, tt.wantKind)
			}
		})
	}
}

// TestLoadErrorClassification checks the kinds and keys of real load
// failures: values never appear in ErrorKeys.
func TestLoadErrorClassification(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		mode     os.FileMode
		wantKind string
		wantKeys []string
	}{
		{"invalid value", validBekupTOML + "\n[log]\nlevel = \"loud\"\n", 0o600, ErrorKindInvalid, []string{"log.level"}},
		{"unknown key", validBekupTOML + "\n[log]\nverbosity = 1\n", 0o600, ErrorKindInvalid, []string{"log.unknown_key"}},
		{"wrong type", validBekupTOML + "\n[graphql]\nmax_depth = \"deep\"\n", 0o600, ErrorKindInvalid, []string{"graphql.max_depth"}},
		{"syntax", "[server\nlisten = 1\n", 0o600, ErrorKindSyntax, []string{}},
		{"group-writable", validBekupTOML, 0o620, ErrorKindFile, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := writeFile(t, safeDir(t), "bekup.toml", tt.content, tt.mode)
			o := opts(nil, nil)
			o.ConfigPath = p
			_, err := LoadBekup(o)
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := ErrorKindOf(err); got != tt.wantKind {
				t.Errorf("kind = %q, want %q (%v)", got, tt.wantKind, err)
			}
			if got := ErrorKeys(err); !slices.Equal(got, tt.wantKeys) {
				t.Errorf("keys = %v, want %v (%v)", got, tt.wantKeys, err)
			}
		})
	}
	t.Run("missing explicit file", func(t *testing.T) {
		o := opts(nil, nil)
		o.ConfigPath = safeDir(t) + "/absent.toml"
		_, err := LoadBekup(o)
		if ErrorKindOf(err) != ErrorKindFile {
			t.Fatalf("kind = %q (%v)", ErrorKindOf(err), err)
		}
	})
}

func TestValidateBekupErrorsAreKeyErrors(t *testing.T) {
	c := DefaultBekup()
	c.Log.Level = "nope"
	c.GraphQL.MaxDepth = 1000
	errs := ValidateBekup(&c, false)
	if len(errs) == 0 {
		t.Fatal("expected errors")
	}
	for _, e := range errs {
		var ke *KeyError
		if !errors.As(e, &ke) || ke.Path == "" || !strings.HasPrefix(ke.Msg, ke.Path+": ") {
			t.Errorf("not a KeyError with a path: %#v", e)
		}
	}
}

func TestWriteReferenceReloadColumn(t *testing.T) {
	var b strings.Builder
	if err := WriteReference(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "| `level` | `BEKUP_LOG_LEVEL` | string | `'info'` | live |") {
		t.Error("log.level is not marked live in the reference")
	}
	if !strings.Contains(out, "| Key | Env override | Type | Default | Reload | Description |") {
		t.Error("bekup.toml tables lack the Reload column")
	}
	if !strings.Contains(out, "| `base_url` | `BEKUP_SERVER_BASE_URL` | string | `(empty)` | restart |") {
		t.Error("server.base_url is not marked restart")
	}
}

// FuzzDiffWithLive checks the reload invariants on arbitrary configs:
// WithLive changes only live keys, takes every live key from next, and
// Diff is symmetric and empty on equal inputs.
func FuzzDiffWithLive(f *testing.F) {
	f.Add("[log]\nlevel = \"debug\"\n", "[server]\nbase_url = \"https://x\"\n")
	f.Add("[netguard]\nallow_cidrs = [\"10.0.0.0/8\"]\n", "[netguard]\nallow_hosts = [\"a.example\"]\n[dev]\nenabled = true\n")
	f.Add("", "[retention]\ndry_run = true\nmax_deletions_per_sweep = 3\n")
	f.Fuzz(func(t *testing.T, a, b string) {
		ca, cb := DefaultBekup(), DefaultBekup()
		if _, err := decodeStrict("a.toml", []byte(a), &ca); err != nil {
			return
		}
		if _, err := decodeStrict("b.toml", []byte(b), &cb); err != nil {
			return
		}
		if d := Diff(&ca, ca.Clone()); len(d) != 0 {
			t.Fatalf("Diff of a clone = %v", d)
		}
		if !slices.Equal(Diff(&ca, &cb), Diff(&cb, &ca)) {
			t.Fatal("Diff is not symmetric")
		}
		before := ca.Clone()
		m := WithLive(&ca, &cb)
		if d := Diff(before, &ca); len(d) != 0 {
			t.Fatalf("WithLive modified running: %v", d)
		}
		for _, k := range Diff(&ca, m) {
			if !IsLiveKey(k) {
				t.Fatalf("WithLive changed restart key %s", k)
			}
		}
		for _, k := range Diff(m, &cb) {
			if IsLiveKey(k) {
				t.Fatalf("WithLive did not take live key %s", k)
			}
		}
	})
}
