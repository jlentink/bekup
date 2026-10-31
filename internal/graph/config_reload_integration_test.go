//go:build integration

package graph_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/configreload"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/graph"
	"github.com/jlentink/bekup/internal/notify"
)

const (
	configStatusQuery = `{ configStatus { liveKeys listeningProcesses
		processes { instanceId kind hostname version status errorKind appliedKeys restartKeys errorKeys weakenedKeys requestId trigger listening }
		lastRequest { id trigger listeningProcesses results { status } } } }`
	reloadConfigMutation = `mutation { reloadConfig { id trigger requestedBy listeningProcesses expectedProcesses
		results { kind status errorKind appliedKeys restartKeys errorKeys weakenedKeys trigger listening } } }`
	reloadRequestQuery = `query($id: UUID!) { configReloadRequest(id: $id) { id results { status } } }`
)

const reloadBaseTOML = `
[server]
listen = "10.0.0.10:8080"
base_url = "https://backup.example.com"
trusted_proxies = ["172.18.0.0/16"]
`

type reloadResultGQL struct {
	Kind, Status                                      string
	ErrorKind, Trigger                                *string
	Listening                                         bool
	AppliedKeys, RestartKeys, ErrorKeys, WeakenedKeys []string
}

type reloadRequestGQL struct {
	ID, Trigger        string
	RequestedBy        *string
	ListeningProcesses int
	ExpectedProcesses  int
	Results            []reloadResultGQL
}

// newReloadEnv is a run environment with the config reload API backed by
// a reloader of its own bekup.toml (task 6.13, D-200).
func newReloadGraphEnv(t *testing.T) (*runEnv, string) {
	t.Helper()
	// A directory name that cannot occur in an ID by chance (t.TempDir's
	// own "001" can: the leak check below looks for the directory name).
	dir := filepath.Join(t.TempDir(), "reload-cfg-dir")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "bekup.toml")
	if err := os.WriteFile(path, []byte(reloadBaseTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	load := config.Options{ConfigPath: path, LookupEnv: func(string) (string, bool) { return "", false }, Environ: func() []string { return nil }}
	res, err := config.LoadBekup(load)
	if err != nil {
		t.Fatal(err)
	}
	e := newRunEnv(t, func(a *authEnv, o *graph.Options) {
		alerting := audit.New(a.pool, audit.Options{Observer: notify.ObserveAudit})
		r, err := configreload.New(configreload.Options{
			DB: a.pool, Auditor: alerting, Load: load, ConfigPath: path, Running: res.Config,
			Appliers:   []configreload.Applier{{Name: "all (test)", Keys: config.LiveKeys()}},
			InstanceID: uuid.New(), Kind: configreload.KindServe, Hostname: "web-1", Version: "1.2.3", StartedAt: time.Now(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := r.RecordStartup(t.Context()); err != nil {
			t.Fatal(err)
		}
		api, err := configreload.NewAPI(configreload.APIOptions{DB: a.pool, Auditor: alerting, Policy: o.Policy, Reloader: r})
		if err != nil {
			t.Fatal(err)
		}
		o.ConfigReload = api
	})
	return e, path
}

func TestConfigReloadGraphQL(t *testing.T) {
	e, path := newReloadGraphEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.user("maint@example.com", sqlc.UserRoleMaintainer)
	adminC, maintC := e.mustLogin("admin@example.com"), e.mustLogin("maint@example.com")

	// Admin only; reloading needs a fresh step-up.
	if r := e.gql(maintC, configStatusQuery, nil); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer status: %+v", r.Errors)
	}
	if r := e.gql(maintC, reloadConfigMutation, nil); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer reload: %+v", r.Errors)
	}
	if r := e.gql("", configStatusQuery, nil); r.code() != "UNAUTHENTICATED" {
		t.Fatalf("anonymous status: %+v", r.Errors)
	}
	var st struct {
		LiveKeys           []string
		ListeningProcesses int
		Processes          []struct {
			Kind, Hostname, Version, Status string
			RequestID, Trigger              *string
			Listening                       bool
		}
		LastRequest *reloadRequestGQL
	}
	e.mustGQL(adminC, configStatusQuery, nil).field(t, "configStatus", &st)
	if !slices.Contains(st.LiveKeys, "log.level") || len(st.Processes) != 1 || st.Processes[0].Status != "LOADED" ||
		st.Processes[0].Kind != "SERVE" || st.Processes[0].Hostname != "web-1" || st.LastRequest != nil ||
		st.Processes[0].Trigger != nil || st.Processes[0].Listening || st.ListeningProcesses != 0 {
		t.Fatalf("status %+v", st)
	}
	e.staleStepUp(admin.ID)
	if r := e.gql(adminC, reloadConfigMutation, nil); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("stale step-up: %+v", r.Errors)
	}

	// An invalid file is rejected with the key name only.
	if err := os.WriteFile(path, []byte(reloadBaseTOML+"[log]\nlevel = \"very-secret-level\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.freshStepUp(admin.ID)
	resp := e.mustGQL(adminC, reloadConfigMutation, nil)
	var req reloadRequestGQL
	resp.field(t, "reloadConfig", &req)
	if req.Trigger != "UI" || req.RequestedBy == nil || *req.RequestedBy != admin.ID.String() || len(req.Results) != 1 {
		t.Fatalf("request %+v", req)
	}
	r0 := req.Results[0]
	if r0.Status != "REJECTED" || r0.ErrorKind == nil || *r0.ErrorKind != "INVALID" || !slices.Equal(r0.ErrorKeys, []string{"log.level"}) ||
		r0.Trigger == nil || *r0.Trigger != "UI" || r0.Listening {
		t.Fatalf("result %+v", r0)
	}
	// This env has no LISTEN connection: nobody listens, the one answer is
	// all the request waits for.
	if req.ListeningProcesses != 0 || req.ExpectedProcesses != 1 {
		t.Fatalf("request counts %+v", req)
	}
	var settings string
	if err := e.pool.QueryRow(t.Context(), `SELECT details->>'error_settings' FROM audit_log WHERE action = 'config.reload_failed' AND details->>'request_id' = $1`, req.ID).Scan(&settings); err != nil || settings != `["log.level"]` {
		t.Fatalf("config.reload_failed error_settings %q %v", settings, err)
	}
	raw := string(resp.Data["reloadConfig"])
	if strings.Contains(raw, "very-secret-level") || strings.Contains(raw, path) || strings.Contains(raw, filepath.Base(filepath.Dir(path))) {
		t.Fatalf("response leaks a value or the path: %s", raw)
	}
	// A second request within 10 s is rate limited.
	if r := e.gql(adminC, reloadConfigMutation, nil); r.code() != "RATE_LIMITED" {
		t.Fatalf("second request: %+v", r.Errors)
	}
	var one reloadRequestGQL
	e.mustGQL(adminC, reloadRequestQuery, map[string]any{"id": req.ID}).field(t, "configReloadRequest", &one)
	if one.ID != req.ID || len(one.Results) != 1 {
		t.Fatalf("by id %+v", one)
	}
	if r := e.gql(adminC, reloadRequestQuery, map[string]any{"id": uuid.NewString()}); r.code() != "NOT_FOUND" {
		t.Fatalf("unknown id: %+v", r.Errors)
	}
	e.mustGQL(adminC, configStatusQuery, nil).field(t, "configStatus", &st)
	if st.LastRequest == nil || st.LastRequest.ID != req.ID || st.Processes[0].Status != "REJECTED" {
		t.Fatalf("status after %+v", st)
	}
	var n int
	if err := e.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_log WHERE action = 'config.reload_requested' AND actor_user_id = $1`, admin.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("config.reload_requested entries %d %v", n, err)
	}
}
