//go:build integration

package externalapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/apiaccess"
	"github.com/jlentink/bekup/internal/apikey"
	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/externalapi"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/runstate"
)

// The external API end to end against a real database (D-220): the real
// `bekup external-api` process (externalapi.Run) connected as
// bekup_external, a token created with the app-side queries, the workers'
// consumer (internal/apiaccess) deciding the request, a River worker
// running the backup_run job (a fake backup that records a successful
// run through runstate, as the real worker does) and the status polled
// until it is terminal.

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

// fakeBackup works backup_run jobs like the real worker records them:
// Begin (the queued run of the attempt), Start, Finish with SUCCESS.
type fakeBackup struct {
	river.WorkerDefaults[queue.BackupRunArgs]
	store *runstate.Store
}

func (w fakeBackup) Work(ctx context.Context, job *river.Job[queue.BackupRunArgs]) error {
	trigger, err := runstate.TriggerOf(job.Args.Trigger)
	if err != nil {
		return river.JobCancel(err)
	}
	run, err := w.store.Begin(ctx, runstate.BeginParams{
		RiverJobID: job.ID, Attempt: job.Attempt, JobID: job.Args.JobID, Trigger: trigger, TriggeredBy: job.Args.TriggeredBy,
	})
	if err != nil {
		return err
	}
	now := time.Now()
	if err := w.store.Start(ctx, run.Ref, job.Attempt, now, now); err != nil {
		return err
	}
	return w.store.Finish(ctx, run.Ref, runstate.Result{Status: runstate.StatusSuccess})
}

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

// writeSecret writes a secret file with the secret-file rules (0600).
func writeSecret(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func externalConfig(t *testing.T, url string) *config.ExternalAPI {
	t.Helper()
	cfg := config.DefaultExternalAPI()
	cfg.Database.URLFile = writeSecret(t, "url", url)
	cfg.Database.TLSCAFile = dbtest.CAFile()
	return &cfg
}

type apiClient struct {
	t    *testing.T
	base string
}

func (c apiClient) do(method, path, token string) (int, http.Header, []byte) {
	c.t.Helper()
	req, err := http.NewRequestWithContext(c.t.Context(), method, c.base+path, http.NoBody)
	if err != nil {
		c.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			c.t.Error(err)
		}
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, body
}

func TestIntegrationExternalAPIEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	d := dbtest.Migrated(t)
	pool := d.AppPool(t)
	q := sqlc.New(pool)

	admin, err := q.InsertUser(ctx, sqlc.InsertUserParams{Email: "admin@example.com", Role: sqlc.UserRoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	job, err := dbtest.InsertJob(ctx, pool, dbtest.JobRow{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	newToken := func(name string, cidr string) apikey.Generated {
		t.Helper()
		g, err := apikey.Generate()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := q.InsertAPIToken(ctx, sqlc.InsertAPITokenParams{
			ID: g.ID, Name: name, SecretHash: g.Hash[:], CreatedBy: admin.ID, ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
			AllowedCidrs: []netip.Prefix{netip.MustParsePrefix(cidr)},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := q.InsertAPITokenJobs(ctx, sqlc.InsertAPITokenJobsParams{TokenID: g.ID, JobIds: []uuid.UUID{job}}); err != nil {
			t.Fatal(err)
		}
		return g
	}
	// The test client connects from loopback.
	ci := newToken("ci pipeline", "127.0.0.0/8")
	elsewhere := newToken("other network", "192.0.2.0/24")

	// The workers: River with the fake backup, and the request consumer.
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kr.Destroy)
	auditor := audit.New(pool, audit.Options{Registry: logging.NewRegistry()})
	alerting := audit.New(pool, audit.Options{Registry: logging.NewRegistry(), Observer: notify.ObserveAudit})
	policy := authz.New(authz.Options{Ownership: jobs.Ownership{DB: pool}})
	keys, err := jobkeys.New(jobkeys.Options{DB: pool, Keyring: kr, Auditor: auditor, Authorizer: policy})
	if err != nil {
		t.Fatal(err)
	}
	settings := queue.SettingsFromConfig(config.DefaultBekup().Workers)
	ws, err := queue.NewWorkers(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Register[queue.BackupRunArgs](ws, fakeBackup{store: runstate.NewStore(pool, nil, nil)}); err != nil {
		t.Fatal(err)
	}
	qc, err := queue.New(pool, queue.Options{Settings: settings, Logger: slog.New(slog.DiscardHandler), Workers: ws, FetchPollInterval: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := jobs.New(jobs.Options{DB: pool, Auditor: auditor, Authorizer: policy, Keys: keys, Queue: qc, Canceler: qc})
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := apiaccess.NewConsumer(apiaccess.ConsumerOptions{DB: pool, Runner: svc, Auditor: alerting, SweepInterval: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}

	// The external API process itself, as bekup_external.
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	runCtx, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	wg.Go(func() {
		errs <- externalapi.Run(runCtx, externalapi.RunOptions{Config: externalConfig(t, d.ExternalURL), Logger: logger, Listener: ln})
	})
	wg.Go(func() { errs <- qc.Run(runCtx) })
	wg.Go(func() { errs <- consumer.Run(runCtx) })
	defer func() {
		stop()
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("component: %v", err)
			}
		}
	}()
	c := apiClient{t: t, base: "http://" + ln.Addr().String()}
	waitFor(ctx, t, "readiness", func() bool { s, _, _ := c.do(http.MethodGet, "/readyz", ""); return s == http.StatusOK })

	// GET /self: the token's scope and the address bekup sees.
	status, _, body := c.do(http.MethodGet, "/api/v1/self", ci.Plaintext)
	var self externalapi.Self
	if status != http.StatusOK || json.Unmarshal(body, &self) != nil || self.Name != "ci pipeline" || len(self.Jobs) != 1 ||
		self.Jobs[0].ID != job || self.ClientIP == nil || *self.ClientIP != "127.0.0.1" {
		t.Fatalf("self: %d %s", status, body)
	}

	// POST: 202 with a pending request; the consumer accepts it, River
	// runs it, the status ends in success.
	status, hdr, body := c.do(http.MethodPost, "/api/v1/jobs/"+job.String()+"/runs", ci.Plaintext)
	var accepted externalapi.RunAccepted
	if status != http.StatusAccepted || json.Unmarshal(body, &accepted) != nil || accepted.State != externalapi.StatePending ||
		hdr.Get("Location") != accepted.StatusURL {
		t.Fatalf("run request: %d %s", status, body)
	}
	var final externalapi.Status
	seen := map[string]bool{}
	waitFor(ctx, t, "a terminal state", func() bool {
		status, _, body := c.do(http.MethodGet, accepted.StatusURL, ci.Plaintext)
		if status != http.StatusOK {
			t.Fatalf("status: %d %s", status, body)
		}
		final = externalapi.Status{}
		if err := json.Unmarshal(body, &final); err != nil {
			t.Fatal(err)
		}
		seen[final.State] = true
		return final.Done
	})
	if final.State != externalapi.StateSuccess || !final.Success || final.Status == nil || *final.Status != "SUCCESS" ||
		final.RunID == nil || final.Attempt == nil || *final.Attempt != 1 || final.FinishedAt == nil || final.JobID != job {
		t.Fatalf("final status %+v (seen %v)", final, seen)
	}
	var trigger string
	if err := pool.QueryRow(ctx, `SELECT trigger::text FROM runs WHERE id = $1`, *final.RunID).Scan(&trigger); err != nil || trigger != "api" {
		t.Fatalf("run trigger %q %v", trigger, err)
	}
	// An unknown job is 404.
	if status, _, body := c.do(http.MethodPost, "/api/v1/jobs/"+uuid.NewString()+"/runs", ci.Plaintext); status != http.StatusNotFound {
		t.Fatalf("unknown job: %d %s", status, body)
	}

	// The same valid token's sibling from a refused address: the generic
	// 401, a recorded denial, and the workers audit and alert it.
	status, hdr, body = c.do(http.MethodGet, "/api/v1/self", elsewhere.Plaintext)
	_, _, unknownBody := c.do(http.MethodGet, "/api/v1/self", "bkp_aaaaaaaaaaaaaaaaaaaaaaaaaa_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if status != http.StatusUnauthorized || hdr.Get("WWW-Authenticate") != `Bearer realm="bekup"` || !bytes.Equal(body, unknownBody) {
		t.Fatalf("refused address: %d %v %s (unknown token: %s)", status, hdr, body, unknownBody)
	}
	waitFor(ctx, t, "the denial alert", func() bool {
		var audits, alerts int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'api_token.ip_denied' AND target_id = $1`, elsewhere.ID.String()).Scan(&audits); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM notification_events WHERE detail->>'kind' = 'api_token_ip_denied' AND detail->>'api_id' = $1`, elsewhere.ID.String()).Scan(&alerts); err != nil {
			t.Fatal(err)
		}
		return audits == 1 && alerts >= 1
	})

	// Nothing of a token ever reaches the log.
	out := logs.String()
	for _, leak := range []string{ci.Plaintext, elsewhere.Plaintext, ci.Plaintext[len(apikey.Prefix)+apikey.IDChars+1:], "Bearer"} {
		if strings.Contains(out, leak) {
			t.Fatalf("log shows a token:\n%s", out)
		}
	}
	if !strings.Contains(out, "app database connected as bekup_external") {
		t.Fatalf("log:\n%s", out)
	}
}

// The external API refuses to start with any other database role.
func TestIntegrationExternalAPIRefusesOtherRoles(t *testing.T) {
	d := dbtest.Migrated(t)
	for name, url := range map[string]string{"bekup_app": d.AppURL, "bekup_owner": d.OwnerURL} {
		err := externalapi.Run(t.Context(), externalapi.RunOptions{Config: externalConfig(t, url), Logger: slog.New(slog.DiscardHandler)})
		if err == nil || !strings.Contains(err.Error(), "refusing to start") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func waitFor(ctx context.Context, t *testing.T, what string, ok func() bool) {
	t.Helper()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for !ok() {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", what)
		case <-tick.C:
		}
	}
}
