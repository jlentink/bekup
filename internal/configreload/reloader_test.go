package configreload

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/logging"
)

// baseTOML validates outside dev mode.
const baseTOML = `
[server]
listen = "10.0.0.10:8080"
base_url = "https://backup.example.com"
trusted_proxies = ["172.18.0.0/16"]
`

// fakeApplier records prepares and commits.
type fakeApplier struct {
	mu       sync.Mutex
	prepared int
	commits  int
	fail     bool
	last     *config.Bekup
}

func (f *fakeApplier) prepare(next *config.Bekup) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prepared++
	if f.fail {
		return nil, errors.New("refused")
	}
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.commits++
		f.last = next
	}, nil
}

func (f *fakeApplier) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prepared, f.commits
}

type recorded struct {
	mu      sync.Mutex
	results []Result
	events  []audit.Event
	fail    error
}

type env struct {
	t      *testing.T
	path   string
	r      *Reloader
	rec    *recorded
	logs   *fakeApplier
	others *fakeApplier
}

// isolated returns load options that never touch the real environment.
func isolated() config.Options {
	empty := func(string) (string, bool) { return "", false }
	return config.Options{LookupEnv: empty, Environ: func() []string { return nil }, SearchPaths: []string{}}
}

func writeConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

// newEnv starts a "process" on baseTOML: log.level has its own applier,
// every other live key a second one.
func newEnv(t *testing.T) *env {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bekup.toml")
	writeConfig(t, path, baseTOML)
	opts := isolated()
	opts.ConfigPath = path
	res, err := config.LoadBekup(opts)
	if err != nil {
		t.Fatal(err)
	}
	return newEnvFrom(t, opts, path, res)
}

func newEnvFrom(t *testing.T, opts config.Options, path string, res *config.Result[config.Bekup]) *env {
	t.Helper()
	e := &env{t: t, path: path, rec: &recorded{}, logs: &fakeApplier{}, others: &fakeApplier{}}
	var rest []string
	for _, k := range config.LiveKeys() {
		if k != "log.level" {
			rest = append(rest, k)
		}
	}
	r, err := build(Options{
		Load: opts, ConfigPath: path, Dev: res.Dev, Running: res.Config, Warnings: res.Warnings,
		Appliers: []Applier{
			{Name: "logging", Keys: []string{"log.level"}, Prepare: e.logs.prepare},
			{Name: "others", Keys: rest, Prepare: e.others.prepare},
		},
		InstanceID: uuid.New(), Kind: KindServe, Hostname: "host-1", Version: "1.2.3",
	})
	if err != nil {
		t.Fatal(err)
	}
	r.recordFn = func(_ context.Context, req Request, res Result) error {
		e.rec.mu.Lock()
		defer e.rec.mu.Unlock()
		if e.rec.fail != nil {
			return e.rec.fail
		}
		e.rec.events = append(e.rec.events, r.auditEvent(req, res))
		e.rec.results = append(e.rec.results, res)
		return nil
	}
	e.r = r
	return e
}

func (e *env) reload() Result {
	e.t.Helper()
	res, err := e.r.Reload(e.t.Context(), Request{ID: uuid.New(), Trigger: TriggerUI})
	if err != nil {
		e.t.Fatalf("Reload: %v", err)
	}
	return res
}

func (e *env) write(content string) { writeConfig(e.t, e.path, content) }

func TestReloadOutcomes(t *testing.T) {
	tests := []struct {
		name        string
		file        string
		status      Status
		applied     []string
		restart     []string
		weakened    []string
		logCommits  int
		restCommits int
	}{
		{"unchanged", baseTOML, StatusUnchanged, nil, nil, nil, 0, 0},
		{"live key", baseTOML + "[log]\nlevel = \"debug\"\n", StatusApplied, []string{"log.level"}, nil, nil, 1, 0},
		{"restart key only", strings.Replace(baseTOML, "10.0.0.10:8080", "10.0.0.11:8080", 1), StatusRestartRequired,
			nil, []string{"server.listen"}, nil, 0, 0},
		{"live and restart", strings.Replace(baseTOML, "10.0.0.10:8080", "10.0.0.11:8080", 1) + "[retention]\ndry_run = true\n",
			StatusRestartRequired, []string{"retention.dry_run"}, []string{"server.listen"}, nil, 0, 1},
		{"weakening", strings.Replace(baseTOML, `["172.18.0.0/16"]`, `["172.18.0.0/16", "10.9.0.0/16"]`, 1),
			StatusApplied, []string{"server.trusted_proxies"}, nil, []string{"server.trusted_proxies"}, 0, 1},
		{"narrowing is not weakening", strings.Replace(baseTOML, `["172.18.0.0/16"]`, `["172.18.1.0/24"]`, 1),
			StatusApplied, []string{"server.trusted_proxies"}, nil, nil, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.write(tt.file)
			res := e.reload()
			if res.Status != tt.status || !slices.Equal(res.Applied, nonNil(tt.applied)) ||
				!slices.Equal(res.Restart, nonNil(tt.restart)) || !slices.Equal(res.Weakened, nonNil(tt.weakened)) {
				t.Fatalf("result %+v", res)
			}
			if _, c := e.logs.counts(); c != tt.logCommits {
				t.Errorf("logging commits = %d, want %d", c, tt.logCommits)
			}
			if _, c := e.others.counts(); c != tt.restCommits {
				t.Errorf("other commits = %d, want %d", c, tt.restCommits)
			}
			if len(e.rec.results) != 1 || e.rec.results[0].Status != tt.status {
				t.Fatalf("recorded %+v", e.rec.results)
			}
			// Restart keys never change the running config.
			if e.r.Running().Server.Listen != "10.0.0.10:8080" {
				t.Error("a restart-only key was applied")
			}
		})
	}
}

func TestReloadAppliesToRunning(t *testing.T) {
	e := newEnv(t)
	e.write(baseTOML + "[log]\nlevel = \"warn\"\n")
	e.reload()
	if e.r.Running().Log.Level != "warn" || e.logs.last == nil || e.logs.last.Log.Level != "warn" {
		t.Fatalf("running %q", e.r.Running().Log.Level)
	}
	// The same file again is unchanged (the running config moved on).
	if res := e.reload(); res.Status != StatusUnchanged {
		t.Fatalf("second reload %+v", res)
	}
}

func TestReloadRejectsAndKeeps(t *testing.T) {
	tests := []struct {
		name string
		do   func(e *env)
		kind string
		keys []string
	}{
		{"invalid value", func(e *env) { e.write(baseTOML + "[log]\nlevel = \"loudest-secret-value\"\n") }, config.ErrorKindInvalid, []string{"log.level"}},
		{"unknown key", func(e *env) { e.write(baseTOML + "[log]\nverbosity = \"x\"\n") }, config.ErrorKindInvalid, []string{"log.unknown_key"}},
		{"syntax", func(e *env) { e.write("[server\n") }, config.ErrorKindSyntax, []string{}},
		{"group-writable", func(e *env) {
			e.write(baseTOML + "[log]\nlevel = \"debug\"\n")
			if err := os.Chmod(e.path, 0o620); err != nil { //nolint:gosec // G302: the test needs a group-writable file

				e.t.Fatal(err)
			}
		}, config.ErrorKindFile, []string{}},
		{"deleted", func(e *env) {
			if err := os.Remove(e.path); err != nil {
				e.t.Fatal(err)
			}
		}, config.ErrorKindFile, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			tt.do(e)
			res := e.reload()
			if res.Status != StatusRejected || res.ErrorKind != tt.kind || !slices.Equal(res.ErrorKeys, tt.keys) {
				t.Fatalf("result %+v", res)
			}
			if p, _ := e.logs.counts(); p != 0 {
				t.Error("an applier was prepared for a rejected file")
			}
			if e.r.Running().Log.Level != "info" {
				t.Error("running config changed")
			}
			ev := fmt.Sprintf("%s %v", e.rec.events[0].Action, e.rec.events[0].Details)
			if !strings.Contains(ev, "config.reload_failed") || strings.Contains(ev, "loudest-secret-value") || strings.Contains(ev, e.path) {
				t.Fatalf("audit event leaks or is wrong: %s", ev)
			}
		})
	}
}

// TestReloadMergedRevalidation: a file that turns on dev mode validates
// by itself, but its live keys are validated against the running
// (non-dev) config and refused.
func TestReloadMergedRevalidation(t *testing.T) {
	e := newEnv(t)
	e.write(strings.Replace(baseTOML, `trusted_proxies = ["172.18.0.0/16"]`, "trusted_proxies = []", 1) + "[dev]\nenabled = true\n")
	res := e.reload()
	if res.Status != StatusRejected || res.ErrorKind != config.ErrorKindInvalid || !slices.Equal(res.ErrorKeys, []string{"server.trusted_proxies"}) {
		t.Fatalf("result %+v", res)
	}
	if p, _ := e.others.counts(); p != 0 {
		t.Error("an applier was prepared for an inconsistent merge")
	}
	if len(e.r.Running().Server.TrustedProxies) != 1 {
		t.Error("trusted proxies changed")
	}
}

func TestReloadApplierFailureCommitsNothing(t *testing.T) {
	e := newEnv(t)
	e.others.fail = true
	e.write(baseTOML + "[log]\nlevel = \"debug\"\n[retention]\ndry_run = true\n")
	res := e.reload()
	if res.Status != StatusFailed || res.ErrorKind != ErrorKindApply || len(res.Applied) != 0 {
		t.Fatalf("result %+v", res)
	}
	if p, c := e.logs.counts(); p != 1 || c != 0 {
		t.Fatalf("logging prepared %d committed %d; want 1/0", p, c)
	}
	if e.r.Running().Log.Level != "info" || e.r.Running().Retention.DryRun {
		t.Fatal("running config changed after a failed prepare")
	}
	if len(e.rec.results) != 1 || e.rec.results[0].Status != StatusFailed {
		t.Fatalf("recorded %+v", e.rec.results)
	}
}

func TestReloadRecordFailureAppliesNothing(t *testing.T) {
	e := newEnv(t)
	e.rec.fail = errors.New("database down")
	e.write(baseTOML + "[log]\nlevel = \"debug\"\n")
	id := uuid.New()
	res, err := e.r.Reload(t.Context(), Request{ID: id, Trigger: TriggerUI})
	if err == nil || res.Status != StatusFailed || res.ErrorKind != ErrorKindRecord {
		t.Fatalf("result %+v err %v", res, err)
	}
	if _, c := e.logs.counts(); c != 0 || e.r.Running().Log.Level != "info" {
		t.Fatal("applied although the audit entry failed")
	}
	// The unrecorded request may be retried (catch-up) and then applies.
	e.rec.fail = nil
	res, err = e.r.Reload(t.Context(), Request{ID: id, Trigger: TriggerUI})
	if err != nil || res.Status != StatusApplied {
		t.Fatalf("retry %+v %v", res, err)
	}
}

// TestReloadAlreadyAnsweredAppliesNothing: a request whose answer the
// database already holds (the insert affected no row) commits nothing and
// is remembered, so it is not reloaded again.
func TestReloadAlreadyAnsweredAppliesNothing(t *testing.T) {
	e := newEnv(t)
	e.rec.fail = errAlreadyAnswered
	e.write(baseTOML + "[log]\nlevel = \"debug\"\n")
	id := uuid.New()
	res, err := e.r.Reload(t.Context(), Request{ID: id, Trigger: TriggerUI})
	if err != nil || !res.AlreadyAnswered || res.Status != "" || res.RequestID != id {
		t.Fatalf("result %+v err %v", res, err)
	}
	if p, c := e.logs.counts(); p != 1 || c != 0 || e.r.Running().Log.Level != "info" {
		t.Fatalf("prepared %d committed %d level %s; want nothing applied", p, c, e.r.Running().Log.Level)
	}
	e.rec.fail = nil
	res, err = e.r.Reload(t.Context(), Request{ID: id, Trigger: TriggerUI})
	if err != nil || !res.AlreadyAnswered || len(e.rec.results) != 0 {
		t.Fatalf("second call %+v err %v recorded %d", res, err, len(e.rec.results))
	}
}

func TestReloadDeduplicatesRequests(t *testing.T) {
	e := newEnv(t)
	e.write(baseTOML + "[log]\nlevel = \"debug\"\n")
	id := uuid.New()
	var wg sync.WaitGroup
	results := make([]Result, 4)
	errs := make([]error, 4)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = e.r.Reload(t.Context(), Request{ID: id, Trigger: TriggerUI})
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	if len(e.rec.results) != 1 {
		t.Fatalf("request handled %d times", len(e.rec.results))
	}
	for _, r := range results {
		if r.Status != StatusApplied {
			t.Fatalf("duplicate result %+v", r)
		}
	}
	if _, c := e.logs.counts(); c != 1 {
		t.Fatalf("committed %d times", c)
	}
}

// TestReloadPinnedPath: a reload reads the file loaded at startup, never a
// new ./bekup.toml.
func TestReloadPinnedPath(t *testing.T) {
	dir1, dir2 := t.TempDir(), t.TempDir()
	writeConfig(t, filepath.Join(dir1, "bekup.toml"), baseTOML)
	t.Chdir(dir1)
	opts := isolated()
	opts.SearchPaths = config.DefaultSearchPaths(config.BekupFileName)[:1] // ./bekup.toml
	res, err := config.LoadBekup(opts)
	if err != nil {
		t.Fatal(err)
	}
	e := newEnvFrom(t, opts, res.Path, res)
	writeConfig(t, filepath.Join(dir2, "bekup.toml"), baseTOML+"[log]\nlevel = \"debug\"\n")
	t.Chdir(dir2)
	if r := e.reload(); r.Status != StatusUnchanged {
		t.Fatalf("a ./bekup.toml in the new working directory was read: %+v", r)
	}
	writeConfig(t, filepath.Join(dir1, "bekup.toml"), baseTOML+"[log]\nlevel = \"warn\"\n")
	if r := e.reload(); r.Status != StatusApplied || e.r.Running().Log.Level != "warn" {
		t.Fatalf("the startup file was not re-read: %+v", r)
	}
}

// TestReloadDevDefaultsNeverSearch: a dev process that started without a
// file never picks one up.
func TestReloadDevDefaultsNeverSearch(t *testing.T) {
	dir := t.TempDir()
	opts := isolated()
	opts.Dev = true
	opts.SearchPaths = []string{filepath.Join(dir, "bekup.toml")}
	res, err := config.LoadBekup(opts)
	if err != nil || res.Path != "" {
		t.Fatalf("dev defaults: %v %q", err, res.Path)
	}
	e := newEnvFrom(t, opts, "", res)
	writeConfig(t, filepath.Join(dir, "bekup.toml"), "[log]\nlevel = \"debug\"\n")
	if r := e.reload(); r.Status != StatusUnchanged {
		t.Fatalf("a file that appeared later was read: %+v", r)
	}
}

func TestSignalTriggerAndAudit(t *testing.T) {
	e := newEnv(t)
	e.write(baseTOML + "[log]\nlevel = \"debug\"\n")
	res, err := e.r.Reload(t.Context(), Request{Trigger: TriggerSignal})
	if err != nil || res.RequestID == uuid.Nil || res.Trigger != TriggerSignal {
		t.Fatalf("%+v %v", res, err)
	}
	ev := e.rec.events[0]
	if ev.Action != audit.ActionConfigReloaded || ev.Actor.Kind != audit.ActorSystem || ev.Details["trigger"] != "signal" {
		t.Fatalf("audit %+v", ev)
	}
}

func TestSignalLoop(t *testing.T) {
	e := newEnv(t)
	c := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- NewSignalLoop(e.r, c, 50*time.Millisecond).Run(ctx) }()
	c <- struct{}{}
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.rec.mu.Lock()
		n := len(e.rec.results)
		e.rec.mu.Unlock()
		if n >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no reload after a signal")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if e.rec.results[0].Trigger != TriggerSignal {
		t.Fatalf("trigger %q", e.rec.results[0].Trigger)
	}
}

func TestCheckAppliers(t *testing.T) {
	all := config.LiveKeys()
	tests := []struct {
		name     string
		appliers []Applier
		ok       bool
	}{
		{"complete", []Applier{{Name: "a", Keys: all}}, true},
		{"nil prepare covers", []Applier{{Name: "a", Keys: all[1:]}, {Name: "unused", Keys: all[:1]}}, true},
		{"missing", []Applier{{Name: "a", Keys: all[1:]}}, false},
		{"duplicate", []Applier{{Name: "a", Keys: all}, {Name: "b", Keys: all[:1]}}, false},
		{"not live", []Applier{{Name: "a", Keys: append(slices.Clone(all), "server.base_url")}}, false},
	}
	for _, tt := range tests {
		if err := CheckAppliers(tt.appliers); (err == nil) != tt.ok {
			t.Errorf("%s: err = %v", tt.name, err)
		}
	}
}

func TestSafeHostname(t *testing.T) {
	cases := map[string]string{
		"web-1.internal":        "web-1.internal",
		"a b/c":                 "a-b-c",
		"":                      "unknown",
		"-lead":                 "unknown",
		strings.Repeat("x", 80): strings.Repeat("x", 63),
		"hôst":                  "h-st",
	}
	for in, want := range cases {
		if got := SafeHostname(in); got != want {
			t.Errorf("SafeHostname(%q) = %q, want %q", in, got, want)
		}
	}
	if safeVersion("0.0.0-dev") != "0.0.0-dev" || safeVersion("bad version") != "unknown" {
		t.Error("safeVersion")
	}
	// `just publish` builds of a non-release commit carry SemVer build
	// metadata (D-202).
	for _, v := range []string{"1.2.3+sha.0123456789ab", "1.2.3+sha.0123456789ab.dirty"} {
		if safeVersion(v) != v {
			t.Errorf("safeVersion(%q) = %q", v, safeVersion(v))
		}
	}
}

func TestParsePayload(t *testing.T) {
	id := uuid.New()
	cases := map[string]bool{
		PayloadPrefix + id.String():                              true,
		"netguard.allowlist":                                     false,
		PayloadPrefix:                                            false,
		PayloadPrefix + "nope":                                   false,
		PayloadPrefix + strings.ToUpper(id.String()):             false,
		PayloadPrefix + id.String() + ":x":                       false,
		"x" + PayloadPrefix + id.String():                        false,
		PayloadPrefix + "{" + id.String() + "}":                  false,
		PayloadPrefix + strings.ReplaceAll(id.String(), "-", ""): false,
	}
	for p, want := range cases {
		got, ok := ParsePayload(p)
		if ok != want || (ok && got != id) {
			t.Errorf("ParsePayload(%q) = %v %v", p, got, ok)
		}
	}
}

// TestReloadUnusedKeysNotReported: a changed live key whose applier has no
// Prepare in this process is taken into the running config but neither
// applied nor weakened here (another process reports it).
func TestReloadUnusedKeysNotReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bekup.toml")
	writeConfig(t, path, baseTOML)
	opts := isolated()
	opts.ConfigPath = path
	res, err := config.LoadBekup(opts)
	if err != nil {
		t.Fatal(err)
	}
	var server, rest []string
	for _, k := range config.LiveKeys() {
		if strings.HasPrefix(k, "server.") {
			server = append(server, k)
		} else {
			rest = append(rest, k)
		}
	}
	others := &fakeApplier{}
	r, err := build(Options{
		Load: opts, ConfigPath: path, Dev: res.Dev, Running: res.Config,
		Appliers: []Applier{
			{Name: "http server (unused)", Keys: server},
			{Name: "others", Keys: rest, Prepare: others.prepare},
		},
		InstanceID: uuid.New(), Kind: KindWorker,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.recordFn = func(context.Context, Request, Result) error { return nil }
	weaker := strings.Replace(baseTOML, `["172.18.0.0/16"]`, `["172.18.0.0/16", "10.9.0.0/16"]`, 1)

	got, err := r.Reload(t.Context(), Request{ID: uuid.New(), Trigger: TriggerUI})
	if err != nil || got.Status != StatusUnchanged {
		t.Fatalf("baseline %+v %v", got, err)
	}
	writeConfig(t, path, weaker)
	got, err = r.Reload(t.Context(), Request{ID: uuid.New(), Trigger: TriggerUI})
	if err != nil || got.Status != StatusUnchanged || len(got.Applied) != 0 || len(got.Weakened) != 0 {
		t.Fatalf("only an unused key changed: %+v %v", got, err)
	}
	if len(r.Running().Server.TrustedProxies) != 2 {
		t.Fatal("the unused key was not taken into the running config")
	}
	writeConfig(t, path, strings.Replace(weaker, `"10.9.0.0/16"`, `"10.9.0.0/16", "10.8.0.0/16"`, 1)+"[retention]\nmax_deletions_per_sweep = 501\n")
	got, err = r.Reload(t.Context(), Request{ID: uuid.New(), Trigger: TriggerUI})
	if err != nil || got.Status != StatusApplied || !slices.Equal(got.Applied, []string{"retention.max_deletions_per_sweep"}) ||
		!slices.Equal(got.Weakened, []string{"retention.max_deletions_per_sweep"}) {
		t.Fatalf("mixed change: %+v %v", got, err)
	}
}

// TestReloadNamesSurviveRedaction: the key names in the audit details and
// the outcome log line are never redacted (no "*_keys" attribute names,
// which logging.IsSecretKey treats as secrets).
func TestReloadNamesSurviveRedaction(t *testing.T) {
	e := newEnv(t)
	var buf strings.Builder
	var mu sync.Mutex
	e.r.o.Logger = slog.New(logging.NewHandler(slog.NewJSONHandler(&lockedWriter{w: &buf, mu: &mu}, nil), logging.NewRegistry()))
	e.write(baseTOML + "[log]\nverbosity = \"x\"\n")
	if res := e.reload(); res.Status != StatusRejected {
		t.Fatalf("result %+v", res)
	}
	e.write(strings.Replace(baseTOML, `["172.18.0.0/16"]`, `["172.18.0.0/16", "10.9.0.0/16"]`, 1))
	if res := e.reload(); res.Status != StatusApplied {
		t.Fatalf("result %+v", res)
	}
	for i, want := range []string{"log.unknown_key", "server.trusted_proxies"} {
		raw, err := audit.Redact(e.rec.events[i].Details, logging.NewRegistry())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), want) || strings.Contains(string(raw), "REDACTED") {
			t.Errorf("audit details %d: %s", i, raw)
		}
	}
	mu.Lock()
	out := buf.String()
	mu.Unlock()
	for _, want := range []string{`"error_settings":["log.unknown_key"]`, `"applied":["server.trusted_proxies"]`, `"weakened":["server.trusted_proxies"]`} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %s: %s", want, out)
		}
	}
	if strings.Contains(out, "REDACTED") {
		t.Errorf("log redacted a key name: %s", out)
	}
}

type lockedWriter struct {
	w  *strings.Builder
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func TestListenerApplicationName(t *testing.T) {
	id := uuid.New()
	for _, kind := range []ProcessKind{KindServe, KindWorker} {
		name := ListenerApplicationName(kind, id)
		if len(name) > 63 {
			t.Fatalf("%s: %d bytes (Postgres keeps 63)", name, len(name))
		}
		if got, ok := ParseListenerApplicationName(name); !ok || got != id {
			t.Fatalf("parse %s: %v %v", name, got, ok)
		}
	}
	for _, bad := range []string{
		ServeListenerPrefix, WorkerListenerPrefix + ":", ServeListenerPrefix + ":nope",
		ServeListenerPrefix + ":" + strings.ToUpper(id.String()), "x" + ServeListenerPrefix + ":" + id.String(),
		"bekup-serve:" + id.String(), ServeListenerPrefix + id.String(),
	} {
		if _, ok := ParseListenerApplicationName(bad); ok {
			t.Errorf("parsed %q", bad)
		}
	}
}

// TestReloadWarningsFromMergedConfig: a warning of a restart-only value in
// the file is not logged after a reload (the value does not run yet).
func TestReloadWarningsFromMergedConfig(t *testing.T) {
	e := newEnv(t)
	var buf strings.Builder
	var mu sync.Mutex
	e.r.o.Logger = slog.New(slog.NewJSONHandler(&lockedWriter{w: &buf, mu: &mu}, nil))
	e.write(baseTOML + "[database]\ntls_mode = \"disable\"\n[log]\nlevel = \"debug\"\n")
	if res := e.reload(); res.Status != StatusRestartRequired || !slices.Equal(res.Restart, []string{"database.tls_mode"}) {
		t.Fatalf("result %+v", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Contains(buf.String(), "configuration warning after reload") {
		t.Fatalf("a warning of a restart-only value was logged: %s", buf.String())
	}
}
