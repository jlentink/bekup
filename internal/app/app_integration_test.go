//go:build integration

package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jlentink/bekup/internal/app"
	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/instancekeys"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// newKeyring returns a fresh master keyring in its file format.
func newKeyring(t *testing.T) string {
	t.Helper()
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer kr.Destroy()
	b, err := kr.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// keyrings holds one master keyring per test database: the database
// stores its key-check values at the first start (D-119), so every start
// against it must use the same keyring.
var keyrings sync.Map

func keyringFor(t *testing.T, url string) string {
	t.Helper()
	v, _ := keyrings.LoadOrStore(url, newKeyring(t))
	s, ok := v.(string)
	if !ok {
		t.Fatal("keyring cache holds a non-string")
	}
	return s
}

func options(t *testing.T, url string, logs *syncBuffer) app.Options {
	t.Helper()
	cfg := config.DefaultBekup()
	cfg.Server.Listen = "127.0.0.1:0"
	cfg.Server.BaseURL = "http://127.0.0.1:8080"
	cfg.Server.ShutdownTimeout = config.Duration(5 * time.Second)
	cfg.Database.URL = url
	cfg.Database.TLSMode = config.DBTLSVerifyFull
	cfg.Database.TLSCAFile = dbtest.CAFile()
	cfg.Security.MasterKey = keyringFor(t, url)
	// serve builds its runner client at startup (task 2.11); the runner
	// itself is not needed (the client connects lazily).
	pkiDir := filepath.Join(t.TempDir(), "pki")
	if _, err := certs.Init(certs.InitOptions{Dir: pkiDir, Components: []string{certs.ComponentServe}, AllowUnencrypted: true}); err != nil {
		t.Fatal(err)
	}
	l := certs.Layout{Dir: pkiDir}
	cfg.Runner.CertFile, cfg.Runner.KeyFile, cfg.Runner.CAFile = l.CertFile(certs.ComponentServe), l.KeyFile(certs.ComponentServe), l.CACert()
	return app.Options{
		Config:  &cfg,
		Dev:     true,
		Logger:  slog.New(slog.NewJSONHandler(logs, nil)),
		UI:      fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}},
		Version: "9.9.9",
	}
}

// serverInfo runs the serverInfo query as the web UI does.
func serverInfo(t *testing.T, base string) map[string]any {
	t.Helper()
	body := strings.NewReader(`{"query":"query ServerInfo { serverInfo { version insecureTransport devMode } }","operationName":"ServerInfo"}`)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, base+"/graphql", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	var out struct {
		Data struct {
			ServerInfo map[string]any `json:"serverInfo"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /graphql: %d %v", resp.StatusCode, err)
	}
	return out.Data.ServerInfo
}

type response struct {
	StatusCode int
	Header     http.Header
}

func get(t *testing.T, url string) response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/html")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	return response{StatusCode: resp.StatusCode, Header: resp.Header}
}

func TestServeStartsAndShutsDown(t *testing.T) {
	d := dbtest.Migrated(t)
	logs := &syncBuffer{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	a, err := app.New(ctx, options(t, d.AppURL, logs))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	base := "http://" + a.Addr().String()

	for _, p := range []string{"/healthz", "/readyz", "/"} {
		resp := get(t, base+p)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d", p, resp.StatusCode)
		}
		if resp.Header.Get("Content-Security-Policy") != httpserver.ContentSecurityPolicy {
			t.Errorf("%s: CSP %q", p, resp.Header.Get("Content-Security-Policy"))
		}
		if resp.Header.Get("Strict-Transport-Security") != "" {
			t.Errorf("%s: HSTS over http", p)
		}
	}
	if resp := get(t, base+"/graphql"); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /graphql: %d", resp.StatusCode)
	}
	if info := serverInfo(t, base); info["insecureTransport"] != true || info["devMode"] != true || info["version"] != "9.9.9" {
		t.Errorf("serverInfo = %v", info)
	}

	// A newer migration applied behind serve's back makes it not ready.
	if err := d.SuperExec(ctx, "INSERT INTO goose_db_version (version_id, is_applied) VALUES (99999, true)"); err != nil {
		t.Fatal(err)
	}
	if resp := get(t, base+"/readyz"); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("readyz with a newer schema: %d", resp.StatusCode)
	}
	if resp := get(t, base+"/healthz"); resp.StatusCode != http.StatusOK {
		t.Errorf("healthz: %d", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	if !strings.Contains(logs.String(), "bekup serve listening") {
		t.Errorf("no startup log: %s", logs.String())
	}
}

// Without an active admin serve logs the `bekup admin create` command at
// WARN and keeps running (D-113); with one it stays quiet.
func TestServeWarnsWithoutAdmin(t *testing.T) {
	d := dbtest.Migrated(t)
	start := func() string {
		t.Helper()
		logs := &syncBuffer{}
		a, err := app.New(t.Context(), options(t, d.AppURL, logs))
		if err != nil {
			t.Fatal(err)
		}
		a.Close()
		return logs.String()
	}
	out := start()
	if !strings.Contains(out, `"level":"WARN","msg":"NO ADMIN ACCOUNT`) || !strings.Contains(out, "bekup admin create --email") ||
		!strings.Contains(out, "docker compose run --rm admin create") {
		t.Fatalf("no admin warning: %s", out)
	}
	// A disabled admin does not count; an active one does.
	if err := d.SuperExec(t.Context(), "INSERT INTO users (email, display_name, role, disabled) VALUES ('off@example.com', 'Off', 'admin', true)"); err != nil {
		t.Fatal(err)
	}
	if out := start(); !strings.Contains(out, "NO ADMIN ACCOUNT") {
		t.Fatalf("disabled admin counted: %s", out)
	}
	if err := d.SuperExec(t.Context(), "INSERT INTO users (email, display_name, role) VALUES ('on@example.com', 'On', 'admin')"); err != nil {
		t.Fatal(err)
	}
	if out := start(); strings.Contains(out, "NO ADMIN ACCOUNT") {
		t.Fatalf("warning with an admin: %s", out)
	}
}

func TestServeRefusesOutdatedSchema(t *testing.T) {
	d := dbtest.Migrated(t)
	latest, err := db.LatestMigrationVersion()
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a database that has not seen the newest migration yet.
	if err := d.SuperExec(t.Context(), "DELETE FROM goose_db_version WHERE version_id = (SELECT max(version_id) FROM goose_db_version)"); err != nil {
		t.Fatal(err)
	}
	_, err = app.New(t.Context(), options(t, d.AppURL, &syncBuffer{}))
	var sv *db.SchemaVersionError
	if !errors.Is(err, db.ErrSchemaBehind) || !errors.As(err, &sv) || sv.Expected != latest || sv.Applied != latest-1 {
		t.Fatalf("want ErrSchemaBehind, got %v", err)
	}
	if !strings.Contains(err.Error(), "bekup migrate up") {
		t.Errorf("error lacks guidance: %v", err)
	}
}

func TestServeRefusesNewerSchema(t *testing.T) {
	d := dbtest.Migrated(t)
	if err := d.SuperExec(t.Context(), "INSERT INTO goose_db_version (version_id, is_applied) VALUES (99999, true)"); err != nil {
		t.Fatal(err)
	}
	_, err := app.New(t.Context(), options(t, d.AppURL, &syncBuffer{}))
	if !errors.Is(err, db.ErrSchemaAhead) || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("want ErrSchemaAhead, got %v", err)
	}
}

func TestServeRefusesPrivilegedRole(t *testing.T) {
	d := dbtest.Migrated(t)
	for name, url := range map[string]string{"superuser": d.SuperURL, "owner": d.OwnerURL} {
		_, err := app.New(t.Context(), options(t, url, &syncBuffer{}))
		if err == nil || !strings.Contains(err.Error(), "role check failed") {
			t.Errorf("%s: want a role check failure, got %v", name, err)
		}
	}
	// bekup_app with a dangerous membership is refused as well (D-099).
	if err := d.SuperExec(t.Context(), "GRANT pg_read_all_data TO bekup_app"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.SuperExec(context.Background(), "REVOKE pg_read_all_data FROM bekup_app"); err != nil {
			t.Error(err)
		}
	})
	_, err := app.New(t.Context(), options(t, d.AppURL, &syncBuffer{}))
	if err == nil || !strings.Contains(err.Error(), "pg_read_all_data") {
		t.Errorf("app with pg_read_all_data: %v", err)
	}
}

func TestSchemaVersionFunctionGrants(t *testing.T) {
	d := dbtest.Migrated(t)
	pool := d.AppPool(t)
	latest, err := db.LatestMigrationVersion()
	if err != nil {
		t.Fatal(err)
	}
	v, err := db.SchemaVersion(t.Context(), pool)
	if err != nil || v != latest {
		t.Fatalf("bekup_app SchemaVersion = %d, %v; want %d", v, err, latest)
	}
	// bekup_app still cannot read or write goose_db_version directly, and
	// cannot replace the function.
	for _, stmt := range []string{
		"SELECT * FROM goose_db_version",
		"CREATE OR REPLACE FUNCTION public.bekup_schema_version() RETURNS bigint LANGUAGE sql AS 'SELECT 1::bigint'",
	} {
		if _, err := pool.Exec(t.Context(), stmt); err == nil {
			t.Errorf("bekup_app could run %q", stmt)
		}
	}
}

func TestServeRefusesWrongKeyring(t *testing.T) {
	d := dbtest.Migrated(t)
	// An instance key sealed with another keyring already exists.
	other, err := crypto.ParseKeyring([]byte(newKeyring(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Destroy()
	key, _, err := instancekeys.Active(t.Context(), sqlc.New(d.AppPool(t)), other)
	if err != nil {
		t.Fatal(err)
	}
	key.Destroy()
	_, err = app.New(t.Context(), options(t, d.AppURL, &syncBuffer{}))
	if err == nil || !strings.Contains(err.Error(), "cannot open the instance signing key") {
		t.Fatalf("want a keyring mismatch refusal, got %v", err)
	}

	// No keyring at all: refused as well (fail closed).
	o := options(t, d.AppURL, &syncBuffer{})
	o.Config.Security.MasterKey = "not a keyring"
	if _, err := app.New(t.Context(), o); err == nil || !strings.Contains(err.Error(), "master keyring") {
		t.Fatalf("want a keyring refusal, got %v", err)
	}
}

func TestServeSignsAuditChainHead(t *testing.T) {
	d := dbtest.Migrated(t)
	pool := d.AppPool(t)
	if _, err := audit.New(pool, audit.Options{}).Record(t.Context(), audit.Event{
		Actor: audit.SystemActor(), Action: audit.ActionSettingChanged,
	}); err != nil {
		t.Fatal(err)
	}
	o := options(t, d.AppURL, &syncBuffer{})
	o.Config.Security.AuditHeadSignInterval = config.Duration(50 * time.Millisecond)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	a, err := app.New(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	q := sqlc.New(pool)
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := q.GetLatestAuditChainHead(t.Context()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("serve did not sign the audit chain head")
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// insecureEntries returns the system.insecure_transport_started entries.
func insecureEntries(t *testing.T, d dbtest.DB) []audit.ListEntry {
	t.Helper()
	page, err := audit.New(d.AppPool(t), audit.Options{}).List(t.Context(),
		audit.Filter{Action: audit.ActionSystemInsecureTransportStarted}, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	return page.Entries
}

// D-095: plain HTTP outside dev mode is audited at every startup; dev mode
// and https are not.
func TestServeAuditsInsecureTransport(t *testing.T) {
	d := dbtest.Migrated(t)
	start := func(mutate func(*app.Options)) {
		t.Helper()
		o := options(t, d.AppURL, &syncBuffer{})
		mutate(&o)
		a, err := app.New(t.Context(), o)
		if err != nil {
			t.Fatal(err)
		}
		a.Close()
	}
	start(func(*app.Options) {}) // dev mode, http
	start(func(o *app.Options) { // production, https
		o.Dev = false
		o.Config.Server.BaseURL = "https://backup.example.com"
		o.Config.Server.TrustedProxies = []string{"10.0.0.1/32"}
	})
	if n := len(insecureEntries(t, d)); n != 0 {
		t.Fatalf("%d entries without insecure production transport", n)
	}
	start(func(o *app.Options) { // production, http with the opt-in
		o.Dev = false
		o.Config.Server.AllowInsecureHTTP = true
	})
	entries := insecureEntries(t, d)
	if len(entries) != 1 {
		t.Fatalf("%d entries, want 1", len(entries))
	}
	e := entries[0]
	var details map[string]any
	if err := json.Unmarshal(e.Details, &details); err != nil {
		t.Fatal(err)
	}
	if e.ActorKind != audit.ActorSystem || e.TargetType == nil || *e.TargetType != "server" ||
		details["base_url"] != "http://127.0.0.1:8080" || details["allow_insecure_http"] != true {
		t.Fatalf("entry %+v details %s", e, e.Details)
	}
}

// A weakened database.tls_mode outside dev mode is audited at every
// startup (security.insecure_db_tls, fail closed); verify-full and dev
// mode are not (M1 code review L3).
func TestServeAuditsInsecureDBTLS(t *testing.T) {
	d := dbtest.Migrated(t)
	entries := func() []audit.ListEntry {
		t.Helper()
		page, err := audit.New(d.AppPool(t), audit.Options{}).List(t.Context(),
			audit.Filter{Action: audit.ActionSecurityInsecureDBTLS}, 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		return page.Entries
	}
	start := func(mutate func(*app.Options)) {
		t.Helper()
		o := options(t, d.AppURL, &syncBuffer{})
		o.Config.Server.BaseURL = "https://backup.example.com"
		o.Config.Server.TrustedProxies = []string{"10.0.0.1/32"}
		mutate(&o)
		a, err := app.New(t.Context(), o)
		if err != nil {
			t.Fatal(err)
		}
		a.Close()
	}
	start(func(o *app.Options) { o.Dev = false })                                   // verify-full
	start(func(o *app.Options) { o.Config.Database.TLSMode = config.DBTLSRequire }) // dev mode
	if n := len(entries()); n != 0 {
		t.Fatalf("%d entries without a weakened production setting", n)
	}
	start(func(o *app.Options) {
		o.Dev = false
		o.Config.Database.TLSMode = config.DBTLSRequire
	})
	got := entries()
	if len(got) != 1 {
		t.Fatalf("%d entries, want 1", len(got))
	}
	var details map[string]any
	if err := json.Unmarshal(got[0].Details, &details); err != nil {
		t.Fatal(err)
	}
	if got[0].ActorKind != audit.ActorSystem || details["tls_mode"] != "require" || details["chain_verified"] != true {
		t.Fatalf("entry %+v details %s", got[0], got[0].Details)
	}
}

// D-119: the first start stores the key-check values; a restart with the
// same keyring starts, a rotated keyring (new primary, old key kept) is
// recorded as keys.rotated, and a different keyring is refused before
// anything is decrypted.
func TestServeChecksKeyring(t *testing.T) {
	d := dbtest.Migrated(t)
	start := func(keyring string) error {
		t.Helper()
		o := options(t, d.AppURL, &syncBuffer{})
		if keyring != "" {
			o.Config.Security.MasterKey = keyring
		}
		a, err := app.New(t.Context(), o)
		if err != nil {
			return err
		}
		a.Close()
		return nil
	}
	entries := func(action audit.Action) []audit.ListEntry {
		t.Helper()
		page, err := audit.New(d.AppPool(t), audit.Options{}).List(t.Context(), audit.Filter{Action: action}, 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		return page.Entries
	}
	if err := start(""); err != nil {
		t.Fatal(err)
	}
	row, err := sqlc.New(d.AppPool(t)).GetSetting(t.Context(), app.KeyCheckSetting)
	if err != nil || !strings.Contains(string(row.Value), "kcv1:") {
		t.Fatalf("key-check values not stored: %s %v", row.Value, err)
	}
	if n := len(entries(audit.ActionKeysCheckValueStored)); n != 1 {
		t.Fatalf("%d keys.check_value_stored entries, want 1", n)
	}
	if err := start(""); err != nil {
		t.Fatalf("restart with the same keyring: %v", err)
	}
	if n := len(entries(audit.ActionKeysCheckValueStored)); n != 1 {
		t.Fatalf("restart stored the values again (%d entries)", n)
	}

	// bekup keys rotate: a new primary, the old key stays.
	kr, err := crypto.ParseKeyring([]byte(keyringFor(t, d.AppURL)))
	if err != nil {
		t.Fatal(err)
	}
	newID, err := kr.AddPrimary(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := kr.Marshal()
	kr.Destroy()
	if err != nil {
		t.Fatal(err)
	}
	if err := start(string(rotated)); err != nil {
		t.Fatalf("start with a rotated keyring: %v", err)
	}
	got := entries(audit.ActionKeysRotated)
	if len(got) != 1 || !strings.Contains(string(got[0].Details), newID.String()) {
		t.Fatalf("keys.rotated entries: %+v", got)
	}
	if err := start(string(rotated)); err != nil {
		t.Fatalf("restart with the rotated keyring: %v", err)
	}
	if n := len(entries(audit.ActionKeysRotated)); n != 1 {
		t.Fatalf("rotation recorded again (%d entries)", n)
	}

	// A different keyring is refused.
	err = start(newKeyring(t))
	if err == nil || !errors.Is(err, app.ErrKeyringMismatch) || !strings.Contains(err.Error(), "does not match this database") {
		t.Fatalf("want a keyring mismatch refusal, got %v", err)
	}
}
