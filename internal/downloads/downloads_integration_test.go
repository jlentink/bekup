//go:build integration

package downloads_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/destinations"
	"github.com/jlentink/bekup/internal/downloads"
	"github.com/jlentink/bekup/internal/graph"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/instancekeys"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/retention"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/storage"
	"github.com/jlentink/bekup/internal/testutil/dockertest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

const (
	testBaseURL  = "https://bekup.example.test"
	testPassword = "correct horse battery staple 42"
	ticketMut    = `mutation($a: UUID!, $m: DownloadMode!, $i: String) { createDownloadTicket(artifactId: $a, mode: $m, ageIdentity: $i) { url expiresAt mode fileName format restoreCommand } }`
	loginMut     = `mutation($e: String!, $p: String!) { login(email: $e, password: $p) { user { id } } }`
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type env struct {
	t       *testing.T
	d       dbtest.DB
	pool    *pgxpool.Pool
	kr      *crypto.Keyring
	hasher  *auth.Hasher
	keys    *jobkeys.Service
	svc     *downloads.Service
	h       http.Handler
	logs    *lockedBuffer
	disk    storage.Destination
	dests   *destinations.Service
	auditor *audit.Auditor
	dest    uuid.UUID
	admin   sqlc.User
	maint   sqlc.User
	owner   sqlc.User
	other   sqlc.User
	cookies map[uuid.UUID]string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := dbtest.Migrated(t)
	e := &env{t: t, d: d, pool: d.AppPool(t), logs: &lockedBuffer{}, cookies: map[uuid.UUID]string{}}
	auditor := audit.New(e.pool, audit.Options{Registry: logging.NewRegistry()})
	var err error
	e.hasher, err = auth.NewHasher(auth.HasherOptions{
		Params: auth.Argon2Params{MemoryKiB: 64, Iterations: 1, Parallelism: 1}, MaxConcurrent: 4, WeakForTests: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessions(e.pool, auth.SessionOptions{})
	authSvc, err := auth.NewService(auth.ServiceOptions{DB: e.pool, Hasher: e.hasher, Sessions: sessions, Auditor: auditor, Factors: auth.NewSecondFactors()})
	if err != nil {
		t.Fatal(err)
	}
	if e.kr, err = crypto.GenerateKeyring(time.Now()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.kr.Destroy)
	logger := slog.New(slog.NewJSONHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pol := authz.New(authz.Options{Ownership: jobs.Ownership{DB: e.pool}})
	if e.keys, err = jobkeys.New(jobkeys.Options{DB: e.pool, Keyring: e.kr, Auditor: auditor, Authorizer: pol, Logger: logger}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "local"), 0o700); err != nil {
		t.Fatal(err)
	}
	guard, err := netguard.New(netguard.Options{})
	if err != nil {
		t.Fatal(err)
	}
	dests, err := destinations.New(destinations.Options{DB: e.pool, Keyring: e.kr, Auditor: auditor, Guard: guard, DiskRoot: root, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	e.dests, e.auditor = dests, auditor
	e.svc, err = downloads.New(downloads.Options{
		DB: e.pool, Keyring: e.kr, Auditor: auditor, Authorizer: pol, Destinations: dests, Keys: e.keys, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	gql, err := graph.New(graph.Options{
		Logger: logger, MaxComplexity: 2000, MaxDepth: 12, MaxBodyBytes: 1 << 16,
		Access: graph.NewPolicyAccess(pol), Policy: pol, AuditLog: auditor, Auth: authSvc, Downloads: e.svc,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := httpserver.New(httpserver.Options{
		BaseURL: testBaseURL, MaxBodyBytes: 1 << 20, Logger: logger,
		Ready:     func(context.Context) error { return nil },
		RateLimit: httpserver.RateLimitOptions{PerSecond: 1000, Burst: 1000, MaxClients: 100},
		GraphQL:   gql,
		Download:  e.svc,
		Sessions: &httpserver.SessionOptions{
			Resolver: httpserver.SessionsResolver{Sessions: sessions},
			Cookies:  auth.CookiePolicyFor(testBaseURL),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.h = srv.Handler()
	e.dest = uuid.New()
	e.exec(`INSERT INTO destinations (id, name, type, config) VALUES ($1, 'Local disk', 'disk', '{"path":"local"}')`, e.dest)
	if e.disk, err = storage.OpenDisk(storage.DiskConfig{Root: root, Dir: "local"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := e.disk.Close(); err != nil {
			t.Error(err)
		}
	})
	e.admin = e.user("admin@example.com", sqlc.UserRoleAdmin)
	e.maint = e.user("maint@example.com", sqlc.UserRoleMaintainer)
	e.owner = e.user("owner@example.com", sqlc.UserRoleUser)
	e.other = e.user("other@example.com", sqlc.UserRoleUser)
	return e
}

func (e *env) exec(stmt string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.t.Context(), stmt, args...); err != nil {
		e.t.Fatalf("%s: %v", stmt, err)
	}
}

func (e *env) user(email string, role sqlc.UserRole) sqlc.User {
	e.t.Helper()
	h, err := e.hasher.Hash(e.t.Context(), testPassword)
	if err != nil {
		e.t.Fatal(err)
	}
	u, err := sqlc.New(e.pool).InsertUser(e.t.Context(), sqlc.InsertUserParams{Email: email, DisplayName: "Pat", PasswordHash: &h, Role: role})
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

type gqlResp struct {
	Data   map[string]json.RawMessage `json:"data"`
	Errors []struct {
		Message    string         `json:"message"`
		Extensions map[string]any `json:"extensions"`
	} `json:"errors"`
}

func (r gqlResp) code() string {
	if len(r.Errors) == 0 {
		return ""
	}
	c, ok := r.Errors[0].Extensions["code"].(string)
	if !ok {
		return ""
	}
	return c
}

func (e *env) gql(cookie, query string, vars map[string]any) (gqlResp, *httptest.ResponseRecorder) {
	e.t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		e.t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(e.t.Context(), http.MethodPost, "/graphql", bytes.NewReader(body))
	req.RemoteAddr = "192.0.2.50:4000"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", testBaseURL)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: cookie, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	var resp gqlResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		e.t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return resp, w
}

// login returns a new session cookie of u.
func (e *env) login(u sqlc.User) string {
	e.t.Helper()
	resp, w := e.gql("", loginMut, map[string]any{"e": u.Email, "p": testPassword})
	if len(resp.Errors) > 0 {
		e.t.Fatalf("login: %+v", resp.Errors)
	}
	for _, c := range (&http.Response{Header: w.Header()}).Cookies() {
		if c.Name == auth.SessionCookieName && c.Value != "" {
			return c.Value
		}
	}
	e.t.Fatal("no session cookie")
	return ""
}

func (e *env) stepUp(u sqlc.User) {
	e.exec("UPDATE sessions SET stepup_at = now() WHERE user_id = $1", u.ID)
}

type ticketGQL struct {
	URL            string `json:"url"`
	ExpiresAt      string `json:"expiresAt"`
	Mode           string `json:"mode"`
	FileName       string `json:"fileName"`
	Format         string `json:"format"`
	RestoreCommand string `json:"restoreCommand"`
}

func (e *env) ticket(cookie string, artifact uuid.UUID, mode string, identity *string) (ticketGQL, gqlResp) {
	e.t.Helper()
	vars := map[string]any{"a": artifact, "m": mode}
	if identity != nil {
		vars["i"] = *identity
	}
	resp, _ := e.gql(cookie, ticketMut, vars)
	var t ticketGQL
	if raw, ok := resp.Data["createDownloadTicket"]; ok && len(resp.Errors) == 0 {
		if err := json.Unmarshal(raw, &t); err != nil {
			e.t.Fatal(err)
		}
	}
	return t, resp
}

func (e *env) mustTicket(cookie string, artifact uuid.UUID, mode string, identity *string) ticketGQL {
	e.t.Helper()
	t, resp := e.ticket(cookie, artifact, mode, identity)
	if len(resp.Errors) > 0 || t.URL == "" {
		e.t.Fatalf("createDownloadTicket: %+v", resp.Errors)
	}
	return t
}

// get requests url with the session cookie; aborted reports an aborted
// response (http.ErrAbortHandler).
func (e *env) get(cookie, url string, hdr ...string) (w *httptest.ResponseRecorder, aborted bool) {
	e.t.Helper()
	req := httptest.NewRequestWithContext(e.t.Context(), http.MethodGet, url, http.NoBody)
	req.RemoteAddr = "192.0.2.50:4000"
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: cookie, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	w = httptest.NewRecorder()
	func() {
		defer func() {
			if p := recover(); p != nil {
				if err, ok := p.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					aborted = true
					return
				}
				panic(p)
			}
		}()
		e.h.ServeHTTP(w, req)
	}()
	return w, aborted
}

// job creates a job on the disk destination with the given owners and
// encryption mode; it returns the job id, slug and (recipient mode) the
// generated identity.
func (e *env) job(mode jobkeys.Mode, owners ...uuid.UUID) (uuid.UUID, string, string) {
	e.t.Helper()
	ctx := e.t.Context()
	id, err := dbtest.InsertJob(ctx, e.pool, dbtest.JobRow{Enabled: true, DestinationIDs: []uuid.UUID{e.dest}})
	if err != nil {
		e.t.Fatal(err)
	}
	for _, o := range owners {
		e.exec(`INSERT INTO job_owners (job_id, user_id, created_by) VALUES ($1, $2, $3)`, id, o, e.admin.ID)
	}
	actx := auth.WithPrincipal(ctx, auth.Principal{UserID: e.admin.ID, Role: auth.RoleAdmin, SessionID: uuid.New(), AAL: 1, StepUpAt: time.Now()})
	var identity string
	err = db.Tx(actx, e.pool, func(q *sqlc.Queries) error {
		res, err := e.keys.InitTx(actx, q, jobkeys.Actor{UserID: e.admin.ID}, id, jobkeys.Init{Mode: mode})
		if err != nil {
			return err
		}
		if res.Keypair != nil {
			identity = string(res.Keypair.Identity.Bytes())
		}
		res.Destroy()
		return nil
	})
	if err != nil {
		e.t.Fatal(err)
	}
	var slug string
	if err := e.pool.QueryRow(ctx, "SELECT slug FROM jobs WHERE id = $1", id).Scan(&slug); err != nil {
		e.t.Fatal(err)
	}
	return id, slug, identity
}

// seeded is an artifact produced through the real pipeline, with its run,
// item and signed manifest recorded like the backup worker does.
type seeded struct {
	artifact, run uuid.UUID
	key           storage.Key
	manifestKey   storage.Key
	stored        []byte
}

func (e *env) seed(job uuid.UUID, slug, dbName string, format pipeline.DumpFormat, dump []byte) seeded {
	e.t.Helper()
	return e.seedAt(job, slug, dbName, format, dump, time.Now())
}

// seedAt seeds an artifact of a run with the directory timestamp ts.
func (e *env) seedAt(job uuid.UUID, slug, dbName string, format pipeline.DumpFormat, dump []byte, at time.Time) seeded {
	e.t.Helper()
	ctx := e.t.Context()
	km, err := e.keys.KeyMaterialFor(ctx, job)
	if err != nil {
		e.t.Fatal(err)
	}
	enc, err := km.Pipeline()
	if err != nil {
		e.t.Fatal(err)
	}
	defer enc.Passphrase.Destroy()
	ts := at.UTC().Truncate(time.Second)
	var run uuid.UUID
	if err := e.pool.QueryRow(ctx, `INSERT INTO runs (job_id, trigger, status, started_at, finished_at, storage_timestamp)
		VALUES ($1, 'manual', 'success', now(), now(), $2) RETURNING id`, job, ts).Scan(&run); err != nil {
		e.t.Fatal(err)
	}
	name, err := pipeline.ArtifactFileName(pipeline.KindDatabase, dbName, format, enc.Mode)
	if err != nil {
		e.t.Fatal(err)
	}
	key, err := pipeline.ArtifactKey("", slug, ts, name)
	if err != nil {
		e.t.Fatal(err)
	}
	res, err := pipeline.Run(ctx, bytes.NewReader(dump), pipeline.Spec{
		Encryption: enc, Targets: []pipeline.Target{{Name: e.dest.String(), Dest: e.disk, Key: key}},
	})
	if err != nil {
		e.t.Fatal(err)
	}
	var item uuid.UUID
	if err := e.pool.QueryRow(ctx, `INSERT INTO run_items (run_id, kind, db_name, position, status, finished_at, raw_size, stored_size, raw_sha256, sha256, verified)
		VALUES ($1, 'database', $2, 0, 'success', now(), $3, $4, $5, $6, true) RETURNING id`,
		run, dbName, res.RawSize, res.StoredSize, res.RawSHA256, res.SHA256).Scan(&item); err != nil {
		e.t.Fatal(err)
	}
	var pv, rv *int
	if enc.PassphraseVersion > 0 {
		pv = &enc.PassphraseVersion
	}
	if enc.RecipientVersion > 0 {
		rv = &enc.RecipientVersion
	}
	var art uuid.UUID
	if err := e.pool.QueryRow(ctx, `INSERT INTO artifacts (run_id, run_item_id, job_id, destination_id, kind, object_key, size, sha256,
		encryption_mode, passphrase_version, recipient_version, verified)
		VALUES ($1, $2, $3, $4, 'database', $5, $6, $7, $8, $9, $10, true) RETURNING id`,
		run, item, job, e.dest, key.String(), res.StoredSize, res.SHA256, string(enc.Mode), pv, rv).Scan(&art); err != nil {
		e.t.Fatal(err)
	}
	sk, _, err := instancekeys.Active(ctx, sqlc.New(e.pool), e.kr)
	if err != nil {
		e.t.Fatal(err)
	}
	defer sk.Destroy()
	mkey, err := pipeline.ManifestKey("", slug, ts)
	if err != nil {
		e.t.Fatal(err)
	}
	m := &pipeline.Manifest{
		FormatVersion: pipeline.ManifestFormatVersion, BekupVersion: "0.0.0-test", JobID: job.String(), JobSlug: slug,
		RunID: run.String(), RunTimestamp: ts, CreatedAt: time.Now().UTC(),
		Engine:  pipeline.EngineInfo{Kind: "postgres", VersionKey: "17", ServerVersion: "17.0"},
		Entries: []pipeline.Entry{res.Entry(pipeline.KindDatabase, dbName, name)},
	}
	mres, err := pipeline.WriteManifest(ctx, []pipeline.Target{{Name: e.dest.String(), Dest: e.disk, Key: mkey}}, sk, m)
	if err != nil {
		e.t.Fatal(err)
	}
	e.exec(`INSERT INTO artifacts (run_id, job_id, destination_id, kind, object_key, size, sha256, signing_key_id, verified)
		VALUES ($1, $2, $3, 'manifest', $4, $5, $6, $7, true)`, run, job, e.dest, mkey.String(), len(mres.File), mres.SHA256, sk.ID)
	e.exec(`UPDATE artifacts SET signing_key_id = $1 WHERE run_id = $2 AND kind <> 'manifest'`, sk.ID, run)
	rc, _, err := e.disk.Get(ctx, key)
	if err != nil {
		e.t.Fatal(err)
	}
	stored, err := io.ReadAll(rc)
	if cerr := rc.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return seeded{artifact: art, run: run, key: key, manifestKey: mkey, stored: stored}
}

func (e *env) auditCount(action audit.Action, artifact uuid.UUID) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(e.t.Context(), `SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2`,
		string(action), artifact.String()).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) auditReasons(artifact uuid.UUID) []string {
	e.t.Helper()
	rows, err := e.pool.Query(e.t.Context(), `SELECT action || ':' || coalesce(details->>'reason', '') FROM audit_log
		WHERE target_id = $1 AND action IN ('download.ticket_rejected', 'download.failed') ORDER BY id`, artifact.String())
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func randomDump(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return append([]byte("-- MySQL dump\n"), b...)
}

func sha(b []byte) []byte {
	s := sha256.Sum256(b)
	return s[:]
}

// TestAuthorizationAndTicketRules covers the role rules (D-067), step-up,
// session binding, single use, expiry, revoked ownership, anonymous and
// cross-site requests and the ticket limit.
func TestAuthorizationAndTicketRules(t *testing.T) {
	e := newEnv(t)
	job, slug, _ := e.job(jobkeys.ModePassphrase, e.owner.ID, e.maint.ID)
	dump := randomDump(1000)
	s := e.seed(job, slug, "shop", pipeline.FormatSQL, dump)
	adminC, maintC, ownerC, otherC := e.login(e.admin), e.login(e.maint), e.login(e.owner), e.login(e.other)

	// Step-up is required for everybody allowed (a fresh login counts as
	// one, so age it first).
	e.exec("UPDATE sessions SET stepup_at = now() - interval '1 hour'")
	if _, r := e.ticket(adminC, s.artifact, "RAW", nil); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("admin without step-up: %+v", r.Errors)
	}
	if _, r := e.ticket(ownerC, s.artifact, "RAW", nil); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("owner without step-up: %+v", r.Errors)
	}
	for _, u := range []sqlc.User{e.admin, e.maint, e.owner, e.other} {
		e.stepUp(u)
	}
	// Maintainers never download, not even as owners; non-owners neither.
	if _, r := e.ticket(maintC, s.artifact, "RAW", nil); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer-owner: %+v", r.Errors)
	}
	if _, r := e.ticket(otherC, s.artifact, "RAW", nil); r.code() != "FORBIDDEN" {
		t.Fatalf("non-owner: %+v", r.Errors)
	}
	// A missing artifact: NOT_FOUND for the admin, FORBIDDEN for an owner.
	if _, r := e.ticket(adminC, uuid.New(), "RAW", nil); r.code() != "NOT_FOUND" {
		t.Fatalf("admin missing: %+v", r.Errors)
	}
	if _, r := e.ticket(ownerC, uuid.New(), "RAW", nil); r.code() != "FORBIDDEN" {
		t.Fatalf("owner missing: %+v", r.Errors)
	}
	// An identity is refused where it is not needed.
	junk := "AGE-SECRET-KEY-1JUNK"
	if _, r := e.ticket(ownerC, s.artifact, "PLAIN", &junk); r.code() != "VALIDATION" {
		t.Fatalf("identity for a passphrase artifact: %+v", r.Errors)
	}

	// The owner downloads plain; the file is the dump.
	tk := e.mustTicket(ownerC, s.artifact, "PLAIN", nil)
	if !strings.HasPrefix(tk.URL, downloads.URLPrefix) || tk.Format != "sql" || !strings.HasSuffix(tk.FileName, ".sql") ||
		!strings.HasPrefix(tk.FileName, slug+"_shop_") || tk.Mode != "PLAIN" || !strings.Contains(tk.RestoreCommand, "psql --set ON_ERROR_STOP=1 --dbname <database> --file "+tk.FileName) {
		t.Fatalf("ticket: %+v", tk)
	}
	exp, err := time.Parse(time.RFC3339Nano, tk.ExpiresAt)
	if err != nil || time.Until(exp) > 61*time.Second || time.Until(exp) < 30*time.Second {
		t.Fatalf("expiry %q", tk.ExpiresAt)
	}
	// Another session of the same user cannot use it, and burns it.
	ownerC2 := e.login(e.owner)
	if w, _ := e.get(ownerC2, tk.URL); w.Code != http.StatusNotFound {
		t.Fatalf("other session: %d", w.Code)
	}
	if w, _ := e.get(ownerC, tk.URL); w.Code != http.StatusGone {
		t.Fatalf("burned ticket: %d", w.Code)
	}

	e.stepUp(e.owner)
	tk = e.mustTicket(ownerC, s.artifact, "PLAIN", nil)
	// A cross-site request is refused without burning the ticket.
	if w, _ := e.get(ownerC, tk.URL, "Sec-Fetch-Site", "cross-site"); w.Code != http.StatusNotFound {
		t.Fatalf("cross-site: %d", w.Code)
	}
	w, aborted := e.get(ownerC, tk.URL, "Sec-Fetch-Site", "same-origin")
	if aborted || w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), dump) {
		t.Fatalf("owner plain download: %d aborted=%v len=%d", w.Code, aborted, w.Body.Len())
	}
	// Reuse is refused.
	if w, _ := e.get(ownerC, tk.URL); w.Code != http.StatusGone {
		t.Fatalf("reuse: %d", w.Code)
	}

	// Expiry.
	tk = e.mustTicket(ownerC, s.artifact, "RAW", nil)
	if err := e.d.SuperExec(t.Context(), `UPDATE download_tickets SET created_at = now() - interval '3 minutes', expires_at = now() - interval '2 minutes' WHERE used_at IS NULL`); err != nil {
		t.Fatal(err)
	}
	if w, _ := e.get(ownerC, tk.URL); w.Code != http.StatusGone {
		t.Fatalf("expired: %d", w.Code)
	}
	if n, err := e.svc.DeleteExpired(t.Context()); err != nil || n == 0 {
		t.Fatalf("cleanup: %d %v", n, err)
	}

	// Ownership removed between creation and use: re-checked, burned.
	tk = e.mustTicket(ownerC, s.artifact, "RAW", nil)
	e.exec(`DELETE FROM job_owners WHERE job_id = $1 AND user_id = $2`, job, e.owner.ID)
	if w, _ := e.get(ownerC, tk.URL); w.Code != http.StatusNotFound {
		t.Fatalf("lost ownership: %d", w.Code)
	}
	e.exec(`INSERT INTO job_owners (job_id, user_id) VALUES ($1, $2)`, job, e.owner.ID)

	// Anonymous use burns the ticket.
	tk = e.mustTicket(ownerC, s.artifact, "RAW", nil)
	if w, _ := e.get("", tk.URL); w.Code != http.StatusNotFound {
		t.Fatalf("anonymous: %d", w.Code)
	}
	if w, _ := e.get(ownerC, tk.URL); w.Code != http.StatusGone {
		t.Fatalf("after anonymous: %d", w.Code)
	}
	// Garbage tickets never reach the database.
	if w, _ := e.get(ownerC, downloads.URLPrefix+"not-a-ticket"); w.Code != http.StatusNotFound {
		t.Fatalf("garbage: %d", w.Code)
	}

	want := []string{
		"download.ticket_rejected:session_mismatch", "download.ticket_rejected:reused",
		"download.ticket_rejected:reused", "download.ticket_rejected:expired",
		"download.ticket_rejected:forbidden", "download.ticket_rejected:session_mismatch", "download.ticket_rejected:reused",
	}
	if got := e.auditReasons(s.artifact); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("audit:\n got %v\nwant %v", got, want)
	}
	if e.auditCount(audit.ActionDownloadStarted, s.artifact) != 1 || e.auditCount(audit.ActionDownloadCompleted, s.artifact) != 1 {
		t.Fatal("download start/completion not audited")
	}

	// At most five open tickets per user.
	for i := range 6 {
		_, r := e.ticket(adminC, s.artifact, "RAW", nil)
		if i < 5 && len(r.Errors) > 0 {
			t.Fatalf("ticket %d: %+v", i, r.Errors)
		}
		if i == 5 && r.code() != "RATE_LIMITED" {
			t.Fatalf("sixth ticket: %+v", r.Errors)
		}
	}

	// Unverified or unsigned artifacts are refused.
	e2 := e.seed(job, slug, "other", pipeline.FormatSQL, dump)
	if err := e.d.SuperExec(t.Context(), "SET session_replication_role = replica",
		"UPDATE artifacts SET signing_key_id = NULL WHERE id = '"+e2.artifact.String()+"'"); err != nil {
		t.Fatal(err)
	}
	e.exec("DELETE FROM download_tickets")
	if _, r := e.ticket(adminC, e2.artifact, "RAW", nil); r.code() != "CONFLICT" {
		t.Fatalf("unsigned: %+v", r.Errors)
	}
	if err := e.d.SuperExec(t.Context(), "UPDATE artifacts SET verified = false WHERE id = '"+s.artifact.String()+"'"); err != nil {
		t.Fatal(err)
	}
	if _, r := e.ticket(adminC, s.artifact, "RAW", nil); r.code() != "CONFLICT" {
		t.Fatalf("unverified: %+v", r.Errors)
	}

	// No ticket value ever reached the logs.
	logs := e.logs.String()
	if strings.Contains(logs, tk.URL) || strings.Contains(logs, strings.TrimPrefix(tk.URL, downloads.URLPrefix)) ||
		!strings.Contains(logs, `"path":"/api/download/[redacted]"`) {
		t.Fatalf("access log: %s", logs)
	}
}

// TestRecipientIdentity: plain downloads of recipient-encrypted artifacts
// need the matching identity, which is stored only sealed and removed
// once the ticket is redeemed.
func TestRecipientIdentity(t *testing.T) {
	e := newEnv(t)
	job, slug, identity := e.job(jobkeys.ModeRecipient)
	if !strings.HasPrefix(identity, "AGE-SECRET-KEY-1") {
		t.Fatalf("identity %q", identity)
	}
	dump := randomDump(200_000)
	s := e.seed(job, slug, "app", pipeline.FormatSQL, dump)
	adminC := e.login(e.admin)
	e.stepUp(e.admin)

	if _, r := e.ticket(adminC, s.artifact, "PLAIN", nil); r.code() != "VALIDATION" {
		t.Fatalf("missing identity: %+v", r.Errors)
	}
	otherKP, err := crypto.GenerateAgeKeypair()
	if err != nil {
		t.Fatal(err)
	}
	wrong := string(otherKP.Identity.Bytes())
	otherKP.Identity.Destroy()
	if _, r := e.ticket(adminC, s.artifact, "PLAIN", &wrong); r.code() != "VALIDATION" || strings.Contains(r.Errors[0].Message, wrong) {
		t.Fatalf("wrong identity: %+v", r.Errors)
	}
	if _, r := e.ticket(adminC, s.artifact, "RAW", &identity); r.code() != "VALIDATION" {
		t.Fatalf("identity with raw: %+v", r.Errors)
	}
	garbage := "not a key"
	if _, r := e.ticket(adminC, s.artifact, "PLAIN", &garbage); r.code() != "VALIDATION" || strings.Contains(r.Errors[0].Message, garbage) {
		t.Fatalf("garbage identity: %+v", r.Errors)
	}
	tk := e.mustTicket(adminC, s.artifact, "PLAIN", &identity)

	// The row never holds the identity in plaintext.
	var row string
	if err := e.pool.QueryRow(t.Context(), `SELECT t::text FROM download_tickets t`).Scan(&row); err != nil {
		t.Fatal(err)
	}
	var sealed []byte
	if err := e.pool.QueryRow(t.Context(), `SELECT sealed_identity FROM download_tickets`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if len(sealed) == 0 || strings.Contains(row, "AGE-SECRET-KEY") || bytes.Contains(sealed, []byte(identity)) ||
		strings.Contains(strings.ToUpper(row), strings.ToUpper(strings.TrimPrefix(identity, "AGE-SECRET-KEY-1"))) {
		t.Fatalf("identity stored in plaintext: %s", row)
	}
	// The audit log never holds it either.
	var auditRows string
	if err := e.pool.QueryRow(t.Context(), `SELECT coalesce(string_agg(details::text, ' '), '') FROM audit_log`).Scan(&auditRows); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auditRows, "AGE-SECRET-KEY") || strings.Contains(auditRows, tk.URL) {
		t.Fatalf("audit log leaks: %s", auditRows)
	}

	w, aborted := e.get(adminC, tk.URL)
	if aborted || w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), dump) {
		t.Fatalf("plain download: %d aborted=%v len=%d", w.Code, aborted, w.Body.Len())
	}
	var left []byte
	if err := e.pool.QueryRow(t.Context(), `SELECT sealed_identity FROM download_tickets`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != nil {
		t.Fatal("identity not deleted after use")
	}
	// The table refuses to put an identity back.
	if _, err := e.pool.Exec(t.Context(), `UPDATE download_tickets SET sealed_identity = '\x01'`); err == nil {
		t.Fatal("identity re-set")
	}
	// Raw: the stored bytes.
	tk = e.mustTicket(adminC, s.artifact, "RAW", nil)
	w, aborted = e.get(adminC, tk.URL)
	if aborted || w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), s.stored) || !strings.HasSuffix(tk.FileName, ".sql.zst.age") {
		t.Fatalf("raw download: %d aborted=%v", w.Code, aborted)
	}
}

// TestIntegrityAbort: a modified stored object or manifest never yields a
// complete download.
func TestIntegrityAbort(t *testing.T) {
	e := newEnv(t)
	job, slug, _ := e.job(jobkeys.ModePassphrase)
	dump := randomDump(300_000)
	s := e.seed(job, slug, "big", pipeline.FormatSQL, dump)
	adminC := e.login(e.admin)
	e.stepUp(e.admin)
	ctx := t.Context()

	// Flip one byte near the end of the stored object (inside the
	// held-back tail and the last age chunk).
	tampered := bytes.Clone(s.stored)
	tampered[len(tampered)-100] ^= 0xff
	e.replace(s.key, tampered)
	for _, mode := range []string{"RAW", "PLAIN"} {
		tk := e.mustTicket(adminC, s.artifact, mode, nil)
		w, aborted := e.get(adminC, tk.URL)
		if !aborted {
			t.Fatalf("%s: not aborted (%d, %d bytes)", mode, w.Code, w.Body.Len())
		}
		if mode == "RAW" && w.Body.Len() >= len(s.stored) {
			t.Fatalf("raw: complete body sent")
		}
		if mode == "PLAIN" && w.Body.Len() >= len(dump) {
			t.Fatalf("plain: complete body sent")
		}
	}
	// A tampered manifest is caught before any byte is sent.
	e.replace(s.key, s.stored)
	rc, _, err := e.disk.Get(ctx, s.manifestKey)
	if err != nil {
		t.Fatal(err)
	}
	mf, err := io.ReadAll(rc)
	if cerr := rc.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	e.replace(s.manifestKey, bytes.Replace(mf, []byte(`"bekup_version":"0.0.0-test"`), []byte(`"bekup_version":"0.0.0-evil"`), 1))
	tk := e.mustTicket(adminC, s.artifact, "RAW", nil)
	if w, aborted := e.get(adminC, tk.URL); aborted || w.Code != http.StatusBadGateway || w.Header().Get("Content-Disposition") != "" {
		t.Fatalf("tampered manifest: %d aborted=%v", w.Code, aborted)
	}
	e.replace(s.manifestKey, mf)
	tk = e.mustTicket(adminC, s.artifact, "RAW", nil)
	if w, aborted := e.get(adminC, tk.URL); aborted || w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), s.stored) {
		t.Fatalf("restored: %d aborted=%v", w.Code, aborted)
	}
	got := e.auditReasons(s.artifact)
	want := "download.failed:integrity_mismatch,download.failed:integrity_mismatch,download.failed:manifest_invalid"
	if strings.Join(got, ",") != want {
		t.Fatalf("audit %v", got)
	}
}

// blockingWriter is a ResponseWriter whose first body write waits until
// release is closed (a slow client in the middle of a download).
type blockingWriter struct {
	*httptest.ResponseRecorder
	once    sync.Once
	writing chan struct{}
	release chan struct{}
}

func (b *blockingWriter) Write(p []byte) (int, error) {
	b.once.Do(func() {
		close(b.writing)
		<-b.release
	})
	return b.ResponseRecorder.Write(p)
}

// TestRetentionSkipsArtifactBeingDownloaded: a download holds its artifact
// from ticket redemption to the end of the stream (task 3.11). A retention
// sweep that runs meanwhile and would delete the artifact skips it (held)
// and keeps the run's manifest; the download completes intact; once the
// download released its hold the next sweep deletes the run.
func TestRetentionSkipsArtifactBeingDownloaded(t *testing.T) {
	e := newEnv(t)
	job, slug, _ := e.job(jobkeys.ModePassphrase)
	e.exec("UPDATE jobs SET retention_keep_last = 1, retention_keep_days = NULL WHERE id = $1", job)
	dump := randomDump(400_000)
	old := e.seedAt(job, slug, "app", pipeline.FormatSQL, dump, time.Now().Add(-2*time.Hour))
	e.seedAt(job, slug, "app", pipeline.FormatSQL, randomDump(1000), time.Now().Add(-time.Hour))
	ret, err := retention.New(retention.Options{DB: e.pool, Auditor: e.auditor, Destinations: e.dests, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	adminC := e.login(e.admin)
	e.stepUp(e.admin)
	tk := e.mustTicket(adminC, old.artifact, "RAW", nil)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tk.URL, http.NoBody)
	req.RemoteAddr = "192.0.2.50:4000"
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: adminC, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	bw := &blockingWriter{ResponseRecorder: httptest.NewRecorder(), writing: make(chan struct{}), release: make(chan struct{})}
	served := make(chan struct{})
	go func() {
		defer close(served)
		e.h.ServeHTTP(bw, req)
	}()
	select {
	case <-bw.writing:
	case <-served:
		t.Fatalf("the download ended early: %d", bw.Code)
	case <-time.After(30 * time.Second):
		t.Fatal("the download did not start")
	}
	var holds int
	if err := e.pool.QueryRow(t.Context(), "SELECT count(*) FROM artifact_holds WHERE artifact_id = $1 AND expires_at > now()", old.artifact).Scan(&holds); err != nil || holds != 1 {
		t.Fatalf("holds %d %v", holds, err)
	}

	// Retention runs while the download streams.
	res, err := ret.Sweep(t.Context(), job, queue.RetentionPostRun, false)
	if err != nil || len(res.Destinations) != 1 {
		t.Fatalf("sweep %+v %v", res, err)
	}
	if dr := res.Destinations[0]; dr.Planned != 2 || dr.Held != 1 || dr.Deleted != 0 || dr.Aborted != "" {
		t.Fatalf("sweep during the download: %+v", dr)
	}
	for _, k := range []storage.Key{old.key, old.manifestKey} {
		if _, err := e.disk.Stat(t.Context(), k); err != nil {
			t.Fatalf("%s deleted while being downloaded: %v", k, err)
		}
	}
	var state string
	if err := e.pool.QueryRow(t.Context(), "SELECT state FROM artifacts WHERE id = $1", old.artifact).Scan(&state); err != nil || state != "present" {
		t.Fatalf("state %q %v", state, err)
	}

	close(bw.release)
	<-served
	if bw.Code != http.StatusOK || !bytes.Equal(bw.Body.Bytes(), old.stored) {
		t.Fatalf("download %d, %d of %d bytes", bw.Code, bw.Body.Len(), len(old.stored))
	}
	if err := e.pool.QueryRow(t.Context(), "SELECT count(*) FROM artifact_holds WHERE artifact_id = $1", old.artifact).Scan(&holds); err != nil || holds != 0 {
		t.Fatalf("hold not released: %d %v", holds, err)
	}

	// Released: the next sweep deletes the old run (dump, then manifest).
	res, err = ret.Sweep(t.Context(), job, queue.RetentionDaily, false)
	if err != nil || len(res.Destinations) != 1 || res.Destinations[0].Deleted != 2 || res.Destinations[0].Held != 0 {
		t.Fatalf("sweep after the download: %+v %v", res, err)
	}
	if _, err := e.disk.Stat(t.Context(), old.key); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("old dump still there: %v", err)
	}
	// A ticket for the deleted artifact is refused.
	if _, resp := e.ticket(adminC, old.artifact, "RAW", nil); len(resp.Errors) == 0 {
		t.Fatal("a ticket for a deleted artifact was issued")
	}
}

// TestHoldBlocksAndExpires: a hold protects only while unexpired, and a
// hold cannot be taken on an artifact that is no longer present.
func TestHoldBlocksAndExpires(t *testing.T) {
	e := newEnv(t)
	job, slug, _ := e.job(jobkeys.ModePassphrase)
	s := e.seed(job, slug, "app", pipeline.FormatSQL, randomDump(1000))
	ctx := t.Context()
	var hold runstate.Hold
	err := db.Tx(ctx, e.pool, func(q *sqlc.Queries) error {
		h, ok, err := runstate.AcquireHoldTx(ctx, q, s.artifact, runstate.HoldDownload, uuid.New(), time.Second)
		if err != nil || !ok {
			return fmt.Errorf("acquire: ok=%v %w", ok, err)
		}
		hold = h
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	locked := func() error {
		return db.Tx(ctx, e.pool, func(q *sqlc.Queries) error { return runstate.LockForDeletionTx(ctx, q, s.artifact) })
	}
	if err := locked(); !errors.Is(err, runstate.ErrArtifactHeld) {
		t.Fatalf("held artifact: %v", err)
	}
	time.Sleep(1200 * time.Millisecond)
	if err := runstate.RenewHold(ctx, e.pool, hold.ID, time.Minute); !errors.Is(err, runstate.ErrHoldLost) {
		t.Fatalf("renewing an expired hold: %v", err)
	}
	if err := locked(); err != nil {
		t.Fatalf("expired hold still protects: %v", err)
	}
	if n, err := runstate.DeleteExpiredHolds(ctx, e.pool, time.Now()); err != nil || n != 1 {
		t.Fatalf("cleanup %d %v", n, err)
	}
	e.exec("UPDATE artifacts SET state = 'deleted', deleted_at = now() WHERE id = $1", s.artifact)
	err = db.Tx(ctx, e.pool, func(q *sqlc.Queries) error {
		_, ok, err := runstate.AcquireHoldTx(ctx, q, s.artifact, runstate.HoldDownload, uuid.New(), time.Minute)
		if err == nil && ok {
			return errors.New("a hold on a deleted artifact")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// bekup_app may only move a hold's expiry.
	if _, err := e.pool.Exec(ctx, "UPDATE artifact_holds SET artifact_id = $1", uuid.New()); err == nil {
		t.Fatal("a hold was moved to another artifact")
	}
}

// replace overwrites a stored object on the disk destination (tamper
// simulation; the disk backend never overwrites).
func (e *env) replace(k storage.Key, data []byte) {
	e.t.Helper()
	if err := e.disk.Delete(e.t.Context(), k); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.disk.Put(e.t.Context(), k, bytes.NewReader(data), storage.PutOptions{}); err != nil {
		e.t.Fatal(err)
	}
}

// TestPlainRestoresWithPgRestore is the task's acceptance criterion: a
// real pg_dump archive goes through the backup pipeline; the plain
// download restores with the stock pg_restore of the official image, and
// the raw download is the stored artifact.
func TestPlainRestoresWithPgRestore(t *testing.T) {
	ctx := t.Context()
	pg, err := postgres.Run(ctx, dbtest.PGImage, postgres.WithDatabase("src"), postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"), postgres.BasicWaitStrategies())
	if err != nil {
		dockertest.Skipf(t, "postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(pg); err != nil {
			t.Logf("terminate postgres: %v", err)
		}
	})
	run := func(cmd ...string) string {
		t.Helper()
		code, out, err := pg.Exec(ctx, cmd, tcexec.Multiplexed())
		var b []byte
		if out != nil {
			var rerr error
			if b, rerr = io.ReadAll(out); rerr != nil {
				t.Fatal(rerr)
			}
		}
		if err != nil || code != 0 {
			t.Fatalf("%v: exit %d: %v: %s", cmd, code, err, b)
		}
		return string(b)
	}
	run("psql", "-U", "postgres", "-d", "src", "-v", "ON_ERROR_STOP=1", "-c",
		"CREATE TABLE items (id int PRIMARY KEY, name text NOT NULL); INSERT INTO items SELECT g, 'item ' || g FROM generate_series(1, 2500) g;")
	run("pg_dump", "-U", "postgres", "-d", "src", "--format=custom", "--file=/tmp/src.dump")
	rc, err := pg.CopyFileFromContainer(ctx, "/tmp/src.dump")
	if err != nil {
		t.Fatal(err)
	}
	dump, err := io.ReadAll(rc)
	if cerr := rc.Close(); err == nil {
		err = cerr
	}
	if err != nil || !bytes.HasPrefix(dump, []byte("PGDMP")) {
		t.Fatalf("dump: %v", err)
	}

	e := newEnv(t)
	job, slug, _ := e.job(jobkeys.ModePassphrase, e.owner.ID)
	s := e.seed(job, slug, "src", pipeline.FormatCustom, dump)
	adminC := e.login(e.admin)
	e.stepUp(e.admin)

	tk := e.mustTicket(adminC, s.artifact, "PLAIN", nil)
	if tk.Format != "dump" || !strings.HasSuffix(tk.FileName, ".dump") || !strings.HasPrefix(tk.RestoreCommand, "pg_restore ") {
		t.Fatalf("ticket %+v", tk)
	}
	w, aborted := e.get(adminC, tk.URL)
	if aborted || w.Code != http.StatusOK {
		t.Fatalf("plain: %d aborted=%v", w.Code, aborted)
	}
	h := w.Header()
	if h.Get("Cache-Control") != "no-store" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Content-Type") != "application/octet-stream" || h.Get("Content-Security-Policy") != httpserver.ContentSecurityPolicy ||
		h.Get("Content-Disposition") != `attachment; filename="`+tk.FileName+`"; filename*=UTF-8''`+tk.FileName {
		t.Fatalf("headers %v", h)
	}
	plain := w.Body.Bytes()
	if !bytes.Equal(sha(plain), sha(dump)) {
		t.Fatal("plain download differs from the dump")
	}
	if err := pg.CopyToContainer(ctx, plain, "/tmp/"+tk.FileName, 0o644); err != nil {
		t.Fatal(err)
	}
	run("createdb", "-U", "postgres", "restored")
	run("pg_restore", "-U", "postgres", "--exit-on-error", "--no-owner", "--dbname=restored", "/tmp/"+tk.FileName)
	if got := strings.TrimSpace(run("psql", "-U", "postgres", "-d", "restored", "-At", "-c", "SELECT count(*) || ':' || max(name) FROM items")); got != "2500:item 999" {
		t.Fatalf("restored rows: %q", got)
	}

	tk = e.mustTicket(adminC, s.artifact, "RAW", nil)
	w, aborted = e.get(adminC, tk.URL)
	if aborted || w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), s.stored) ||
		w.Header().Get("Content-Length") != "" && w.Header().Get("Content-Length") != strconv.Itoa(len(s.stored)) ||
		!strings.HasSuffix(tk.FileName, ".dump.zst.age") {
		t.Fatalf("raw: %d aborted=%v len=%d", w.Code, aborted, w.Body.Len())
	}
	if strings.Contains(e.logs.String(), strings.TrimPrefix(tk.URL, downloads.URLPrefix)) {
		t.Fatal("ticket in logs")
	}
}
