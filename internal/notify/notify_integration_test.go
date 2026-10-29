//go:build integration

package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/notify/email"
	"github.com/jlentink/bekup/internal/notify/webhook"
	"github.com/jlentink/bekup/internal/queue"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

// ownership answers job_owners questions (as jobs.Ownership does; that
// package imports this one).
type ownership struct{ pool *pgxpool.Pool }

func (o ownership) IsJobOwner(ctx context.Context, user, job uuid.UUID) (bool, error) {
	var ok bool
	err := o.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM job_owners WHERE user_id = $1 AND job_id = $2)`, user, job).Scan(&ok)
	return ok, err
}

func (ownership) BacksUpDatabase(context.Context, uuid.UUID, uuid.UUID, string) (bool, error) {
	return false, nil
}

func (ownership) UsesConnection(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return false, nil
}

// fakeQueue records inserted jobs.
type fakeQueue struct {
	mu   sync.Mutex
	args []river.JobArgs
}

func (f *fakeQueue) InsertTx(_ context.Context, _ pgx.Tx, args river.JobArgs, _ queue.InsertOptions) (queue.InsertResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.args = append(f.args, args)
	return queue.InsertResult{JobID: int64(len(f.args))}, nil
}

func (f *fakeQueue) deliveries() []uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []uuid.UUID
	for _, a := range f.args {
		if d, ok := a.(queue.NotifyDeliverArgs); ok {
			out = append(out, d.DeliveryID)
		}
	}
	return out
}

// fakeMailer records messages.
type fakeMailer struct {
	mu   sync.Mutex
	sent []*email.Message
	err  error
}

func (f *fakeMailer) Send(_ context.Context, m *email.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, m)
	return nil
}

func (f *fakeMailer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

type env struct {
	t      *testing.T
	d      dbtest.DB
	pool   *pgxpool.Pool
	kr     *crypto.Keyring
	q      *fakeQueue
	mail   *fakeMailer
	svc    *Service
	guard  *netguard.Guard
	hook   *httptest.Server
	hookMu sync.Mutex
	// received webhook requests (body + headers).
	got    []*http.Request
	bodies [][]byte
	status int
}

func newEnv(t *testing.T, mod func(*Options)) *env {
	t.Helper()
	d := dbtest.Migrated(t)
	e := &env{t: t, d: d, pool: d.AppPool(t), q: &fakeQueue{}, mail: &fakeMailer{}, status: http.StatusOK}
	var err error
	if e.kr, err = crypto.GenerateKeyring(time.Now()); err != nil {
		t.Fatal(err)
	}
	e.hook = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		e.hookMu.Lock()
		e.got = append(e.got, r)
		e.bodies = append(e.bodies, b)
		st := e.status
		e.hookMu.Unlock()
		w.WriteHeader(st)
	}))
	t.Cleanup(e.hook.Close)
	if e.guard, err = netguard.New(netguard.Options{LoopbackAllowable: true, AllowCIDRs: []string{"127.0.0.1/32"}}); err != nil {
		t.Fatal(err)
	}
	tr, ok := e.hook.Client().Transport.(*http.Transport)
	if !ok {
		t.Fatal("unexpected transport")
	}
	sender, err := webhook.NewSender(e.guard, webhook.SenderOptions{
		Version: "1.0.0", Timeout: 5 * time.Second, TLSConfig: tr.TLSClientConfig,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sender.CloseIdleConnections)
	o := Options{
		DB: e.pool, Policy: authz.New(authz.Options{Ownership: ownership{e.pool}}), Keyring: e.kr,
		Mailer: e.mail, Webhooks: sender, PersonalWebhooks: sender, Queue: func() Inserter { return e.q },
		BaseURL: "https://bekup.test", Logger: slog.New(slog.DiscardHandler),
	}
	if mod != nil {
		mod(&o)
	}
	if e.svc, err = New(o); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.t.Context(), sql, args...); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(e.t.Context(), sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) user(role string, verified bool) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	var at *time.Time
	if verified {
		now := time.Now()
		at = &now
	}
	e.exec(`INSERT INTO users (id, email, display_name, role, email_verified_at) VALUES ($1, $2, 'U', $3, $4)`,
		id, id.String()[:8]+"@example.com", role, at)
	return id
}

func (e *env) job(owners ...uuid.UUID) uuid.UUID {
	e.t.Helper()
	id, err := dbtest.InsertJob(e.t.Context(), e.pool, dbtest.JobRow{Enabled: true})
	if err != nil {
		e.t.Fatal(err)
	}
	for _, o := range owners {
		e.exec(`INSERT INTO job_owners (job_id, user_id) VALUES ($1, $2)`, id, o)
	}
	return id
}

// userWebhook adds a personal webhook to the test server and returns its
// id and signing key.
func (e *env) userWebhook(user uuid.UUID) (uuid.UUID, []byte) {
	e.t.Helper()
	id := uuid.New()
	key, err := webhook.GenerateHMACKey()
	if err != nil {
		e.t.Fatal(err)
	}
	urlEnc, err := e.kr.Seal([]byte(e.hook.URL+"/hook"), ChannelAAD("user_channels", id, FieldURL))
	if err != nil {
		e.t.Fatal(err)
	}
	keyEnc, err := e.kr.Seal(key, ChannelAAD("user_channels", id, FieldHMACKey))
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := sqlc.New(e.pool).InsertUserChannel(e.t.Context(), sqlc.InsertUserChannelParams{
		ID: id, UserID: user, Name: "hook", DisplayUrl: webhook.DisplayURL(e.hook.URL), UrlEnc: urlEnc, HmacKeyEnc: keyEnc,
	}); err != nil {
		e.t.Fatal(err)
	}
	return id, key
}

func (e *env) pref(user uuid.UUID, ev EventType, job *uuid.UUID, ch Channel, on bool) {
	e.t.Helper()
	if err := sqlc.New(e.pool).UpsertNotificationPref(e.t.Context(), sqlc.UpsertNotificationPrefParams{
		UserID: user, Event: string(ev), JobID: job, Channel: sqlc.NotificationChannel(ch), Enabled: on,
	}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) emit(ev Event) uuid.UUID {
	e.t.Helper()
	id, err := Emit(e.t.Context(), sqlc.New(e.pool), ev)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) fanOut(id uuid.UUID) {
	e.t.Helper()
	if err := e.svc.FanOut(e.t.Context(), id); err != nil {
		e.t.Fatal(err)
	}
}

// deliverAll works every queued delivery once.
func (e *env) deliverAll() {
	e.t.Helper()
	for _, id := range e.q.deliveries() {
		if _, err := e.svc.Deliver(e.t.Context(), id, false); err != nil && !errors.Is(err, errRetry) {
			e.t.Fatal(err)
		}
	}
}

func (e *env) inbox(user uuid.UUID) int {
	return e.count(`SELECT count(*) FROM notifications WHERE user_id = $1`, user)
}

func (e *env) deliveries(user uuid.UUID, ch Channel, status string) int {
	return e.count(`SELECT count(*) FROM notification_deliveries WHERE user_id = $1 AND channel = $2 AND status = $3`, user, string(ch), status)
}

// TestBackupFailureAudience: a failed backup reaches the admin, the
// maintainer and the job's owner in the inbox; e-mail only to verified
// addresses; nothing for the owner of another job.
func TestBackupFailureAudience(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.user("admin", true)
	maint := e.user("maintainer", false)
	owner := e.user("user", true)
	stranger := e.user("user", true)
	job := e.job(owner)
	e.job(stranger)

	id := e.emit(Event{Type: BackupFailed, Subject: Subject{JobID: job, RunID: uuid.New()}, Detail: Detail{"error_code": "dump_failed"}})
	if n, err := e.svc.Dispatch(t.Context()); err != nil || n != 1 {
		t.Fatalf("dispatch = %d, %v", n, err)
	}
	e.fanOut(id)
	for _, u := range []uuid.UUID{admin, maint, owner} {
		if e.inbox(u) != 1 {
			t.Fatalf("inbox of %s = %d", u, e.inbox(u))
		}
	}
	if e.inbox(stranger) != 0 || e.count(`SELECT count(*) FROM notification_deliveries WHERE user_id = $1`, stranger) != 0 {
		t.Fatal("the owner of another job was notified")
	}
	if e.deliveries(admin, ChannelEmail, "queued") != 1 || e.deliveries(owner, ChannelEmail, "queued") != 1 {
		t.Fatal("verified recipients lack their e-mail")
	}
	if e.deliveries(maint, ChannelEmail, "skipped") != 1 {
		t.Fatal("unverified address must be skipped")
	}
	e.deliverAll()
	if e.mail.count() != 2 {
		t.Fatalf("mails = %d", e.mail.count())
	}
	if e.deliveries(admin, ChannelEmail, "sent") != 1 {
		t.Fatal("delivery not marked sent")
	}
	var link *string
	if err := e.pool.QueryRow(t.Context(), `SELECT link FROM notifications WHERE user_id = $1`, owner).Scan(&link); err != nil || link == nil ||
		len(*link) < len("https://bekup.test/runs/") || (*link)[:len("https://bekup.test/runs/")] != "https://bekup.test/runs/" {
		t.Fatalf("link = %v, %v", link, err)
	}

	// A success: inbox only, no e-mail by default.
	ok := e.emit(Event{Type: BackupSucceeded, Subject: Subject{JobID: job}})
	e.fanOut(ok)
	if e.inbox(owner) != 2 || e.count(`SELECT count(*) FROM notification_deliveries WHERE event_id = $1`, ok) != 0 {
		t.Fatal("a success must go to the inbox only")
	}
}

// TestIdempotentFanOut: re-running a fan-out (also after a crash before
// fanned_out_at was set) never duplicates inbox rows or deliveries.
func TestIdempotentFanOut(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.user("admin", true)
	e.userWebhook(admin)
	e.pref(admin, BackupFailed, nil, ChannelWebhook, true)
	job := e.job()
	id := e.emit(Event{Type: BackupFailed, Subject: Subject{JobID: job}})
	e.fanOut(id)
	e.fanOut(id)
	if err := e.d.SuperExec(t.Context(), fmt.Sprintf(`UPDATE notification_events SET fanned_out_at = NULL WHERE id = '%s'`, id)); err != nil {
		t.Fatal(err)
	}
	e.fanOut(id)
	if e.inbox(admin) != 1 || e.count(`SELECT count(*) FROM notification_deliveries WHERE event_id = $1`, id) != 2 {
		t.Fatal("re-running the fan-out duplicated rows")
	}
	if len(e.q.deliveries()) != 2 {
		t.Fatalf("delivery jobs = %d", len(e.q.deliveries()))
	}
	// Deduplicated emits.
	a := e.emit(Event{Type: RetentionSummary, Subject: Subject{JobID: job}, DedupKey: "retention.summary:x"})
	b := e.emit(Event{Type: RetentionSummary, Subject: Subject{JobID: job}, DedupKey: "retention.summary:x"})
	if a == uuid.Nil || b != uuid.Nil {
		t.Fatalf("dedup: %s %s", a, b)
	}
}

// TestWebhookSignedDelivery: a personal webhook receives the signed
// envelope with a stable delivery id; the payload is minimal.
func TestWebhookSignedDelivery(t *testing.T) {
	e := newEnv(t, nil)
	owner := e.user("user", false)
	_, key := e.userWebhook(owner)
	job := e.job(owner)
	e.pref(owner, BackupFailed, &job, ChannelWebhook, true)
	id := e.emit(Event{Type: BackupFailed, Subject: Subject{JobID: job}, Detail: Detail{"error_code": "dump_failed"}})
	e.fanOut(id)
	e.deliverAll()
	if len(e.got) != 1 {
		t.Fatalf("webhook requests = %d", len(e.got))
	}
	r, body := e.got[0], e.bodies[0]
	if err := webhook.Verify(key, r.Header.Get(webhook.HeaderSignature), r.Header.Get(webhook.HeaderTimestamp),
		r.Header.Get(webhook.HeaderDelivery), r.Header.Get(webhook.HeaderEvent), body, time.Now(), 5*time.Minute); err != nil {
		t.Fatalf("signature: %v", err)
	}
	if r.Header.Get(webhook.HeaderEvent) != string(BackupFailed) || r.Header.Get(webhook.HeaderDelivery) == "" {
		t.Fatalf("headers %v", r.Header)
	}
	var env webhook.Envelope
	if err := json.Unmarshal(body, &env); err != nil || env.ID != id.String() || env.Type != string(BackupFailed) {
		t.Fatalf("envelope %s: %v", body, err)
	}
	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("data %T", env.Data)
	}
	jobData, ok := data["job"].(map[string]any)
	if !ok || jobData["id"] != job.String() || data["link"] == nil {
		t.Fatalf("data %v", data)
	}
	if e.deliveries(owner, ChannelWebhook, "sent") != 1 {
		t.Fatal("webhook delivery not sent")
	}
}

// TestMandatorySecurityEvent: own-account security events reach the inbox
// and e-mail even with every preference off, and only their subject.
func TestMandatorySecurityEvent(t *testing.T) {
	e := newEnv(t, nil)
	user := e.user("user", true)
	admin := e.user("admin", true)
	e.pref(user, SecurityAccount, nil, ChannelInbox, false)
	e.pref(user, SecurityAccount, nil, ChannelEmail, false)
	id := e.emit(Event{Type: SecurityAccount, Subject: Subject{UserID: user}, Detail: Detail{"kind": KindPasswordChanged}})
	e.fanOut(id)
	if e.inbox(user) != 1 || e.deliveries(user, ChannelEmail, "queued") != 1 {
		t.Fatal("a mandatory security event was suppressed")
	}
	if e.inbox(admin) != 0 {
		t.Fatal("an own-account event reached someone else")
	}
	// System events: every admin, nobody else.
	sys := e.emit(Event{Type: SecuritySystem, Detail: Detail{"kind": KindKeysRotated}})
	e.fanOut(sys)
	if e.inbox(admin) != 1 || e.inbox(user) != 1 {
		t.Fatal("system event audience")
	}
	// Via the audit observer: a password change is a security.account
	// event in the audit entry's transaction.
	a := audit.New(e.pool, audit.Options{Observer: ObserveAudit})
	if _, err := a.Record(t.Context(), audit.Event{
		Actor: audit.UserActor(user), Action: audit.ActionAuthPasswordChanged, TargetType: "user", TargetID: user.String(),
	}); err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT count(*) FROM notification_events WHERE type = 'security.account' AND user_id = $1`, user) != 2 {
		t.Fatal("audit observer did not emit")
	}
}

// TestAccessCheckedAtDelivery: an owner who loses access between emit and
// fan-out gets nothing; one who loses it between fan-out and delivery
// gets no e-mail or webhook.
func TestAccessCheckedAtDelivery(t *testing.T) {
	e := newEnv(t, nil)
	owner := e.user("user", true)
	e.userWebhook(owner)
	e.pref(owner, BackupFailed, nil, ChannelWebhook, true)
	job := e.job(owner)

	before := e.emit(Event{Type: BackupFailed, Subject: Subject{JobID: job}})
	e.exec(`DELETE FROM job_owners WHERE job_id = $1`, job)
	e.fanOut(before)
	if e.inbox(owner) != 0 {
		t.Fatal("a former owner got the inbox row")
	}

	e.exec(`INSERT INTO job_owners (job_id, user_id) VALUES ($1, $2)`, job, owner)
	after := e.emit(Event{Type: BackupFailed, Subject: Subject{JobID: job}})
	e.fanOut(after)
	if e.count(`SELECT count(*) FROM notification_deliveries WHERE event_id = $1 AND status = 'queued'`, after) != 2 {
		t.Fatal("expected an e-mail and a webhook delivery")
	}
	e.exec(`DELETE FROM job_owners WHERE job_id = $1`, job)
	e.deliverAll()
	if e.mail.count() != 0 || len(e.got) != 0 {
		t.Fatal("delivered after access was lost")
	}
	if e.count(`SELECT count(*) FROM notification_deliveries WHERE event_id = $1 AND status = 'skipped' AND last_code = $2`, after, CodeAccessRevoked) != 2 {
		t.Fatal("deliveries not skipped as access_revoked")
	}
	// A disabled account gets nothing either.
	admin := e.user("admin", true)
	ev := e.emit(Event{Type: BackupFailed, Subject: Subject{JobID: job}})
	e.exec(`UPDATE users SET disabled = true WHERE id = $1`, admin)
	e.fanOut(ev)
	if e.inbox(admin) != 0 {
		t.Fatal("a disabled admin was notified")
	}
}

// TestWebhookAutoDisable: permanent failures disable the webhook and tell
// its owner in the inbox; later events skip it.
func TestWebhookAutoDisable(t *testing.T) {
	e := newEnv(t, func(o *Options) {
		o.FailurePolicy = webhook.FailurePolicy{MaxConsecutive: 10, MinSpan: time.Hour, MaxPermanent: 2}
	})
	e.status = http.StatusGone
	owner := e.user("user", false)
	hook, _ := e.userWebhook(owner)
	job := e.job(owner)
	e.pref(owner, BackupFailed, nil, ChannelWebhook, true)
	for range 2 {
		e.fanOut(e.emit(Event{Type: BackupFailed, Subject: Subject{JobID: job}}))
	}
	e.deliverAll()
	ch, err := sqlc.New(e.pool).GetUserChannel(t.Context(), hook)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Enabled || ch.DisabledReason == nil || *ch.DisabledReason != string(webhook.DisablePermanentFailures) {
		t.Fatalf("channel %+v", ch)
	}
	if e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND type = $2`, owner, NoticeWebhookDisabled) != 1 {
		t.Fatal("no inbox notice for the owner")
	}
	if e.deliveries(owner, ChannelWebhook, "failed") != 2 {
		t.Fatal("permanent failures must fail the deliveries")
	}
	n := len(e.got)
	e.fanOut(e.emit(Event{Type: BackupFailed, Subject: Subject{JobID: job}}))
	e.deliverAll()
	if len(e.got) != n {
		t.Fatal("a disabled webhook was called")
	}
}

// TestRetryableWebhook: a 503 keeps the delivery queued (retry) until the
// attempts run out.
func TestRetryableWebhook(t *testing.T) {
	e := newEnv(t, nil)
	e.status = http.StatusServiceUnavailable
	admin := e.user("admin", false)
	e.userWebhook(admin)
	e.pref(admin, BackupFailed, nil, ChannelWebhook, true)
	job := e.job()
	e.fanOut(e.emit(Event{Type: BackupFailed, Subject: Subject{JobID: job}}))
	ids := e.q.deliveries()
	if len(ids) != 1 {
		t.Fatalf("deliveries %d", len(ids))
	}
	if _, err := e.svc.Deliver(t.Context(), ids[0], false); !errors.Is(err, errRetry) {
		t.Fatalf("err = %v", err)
	}
	if e.deliveries(admin, ChannelWebhook, "queued") != 1 {
		t.Fatal("a retryable failure must stay queued")
	}
	if _, err := e.svc.Deliver(t.Context(), ids[0], true); err != nil {
		t.Fatalf("final attempt: %v", err)
	}
	if e.deliveries(admin, ChannelWebhook, "failed") != 1 {
		t.Fatal("the final attempt must fail the delivery")
	}
}

// TestStartedCollapse: backup.started of one job within the window
// collapses into the first one.
func TestStartedCollapse(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.user("admin", false)
	job, other := e.job(), e.job()
	first := e.emit(Event{Type: BackupStarted, Subject: Subject{JobID: job}})
	e.fanOut(first)
	second := e.emit(Event{Type: BackupStarted, Subject: Subject{JobID: job}})
	e.fanOut(second)
	third := e.emit(Event{Type: BackupStarted, Subject: Subject{JobID: other}})
	e.fanOut(third)
	if e.inbox(admin) != 2 {
		t.Fatalf("inbox = %d, want 2 (one per job)", e.inbox(admin))
	}
	ev, err := sqlc.New(e.pool).GetNotificationEvent(t.Context(), second)
	if err != nil || ev.CollapsedInto == nil || *ev.CollapsedInto != first {
		t.Fatalf("second event %+v, %v", ev, err)
	}
	if e.count(`SELECT collapsed_count FROM notification_events WHERE id = $1`, first) != 1 {
		t.Fatal("collapsed count")
	}
	// Outside the window: delivered again.
	if err := e.d.SuperExec(t.Context(), fmt.Sprintf(`UPDATE notification_events SET created_at = now() - interval '1 hour' WHERE id = '%s'`, first)); err != nil {
		t.Fatal(err)
	}
	e.fanOut(e.emit(Event{Type: BackupStarted, Subject: Subject{JobID: job}}))
	if e.inbox(admin) != 3 {
		t.Fatal("an event outside the window was collapsed")
	}
}

// TestFloodRule: past the per-user limit one summary goes out, then only
// the inbox; security events are exempt.
func TestFloodRule(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.MaxPerUserPerHour = 2 })
	admin := e.user("admin", true)
	job := e.job()
	for range 4 {
		e.fanOut(e.emit(Event{Type: BackupFailed, Subject: Subject{JobID: job}}))
	}
	if e.inbox(admin) != 4 {
		t.Fatal("the inbox is never collapsed")
	}
	if got := e.count(`SELECT count(*) FROM notification_deliveries WHERE user_id = $1 AND status = 'queued' AND NOT summary`, admin); got != 2 {
		t.Fatalf("normal deliveries = %d", got)
	}
	if e.count(`SELECT count(*) FROM notification_deliveries WHERE user_id = $1 AND summary`, admin) != 1 {
		t.Fatal("no flood summary")
	}
	if e.deliveries(admin, ChannelEmail, "skipped") != 1 {
		t.Fatal("no collapsed delivery")
	}
	e.fanOut(e.emit(Event{Type: SecuritySystem, Detail: Detail{"kind": KindAuditVerifyFailed}}))
	if e.count(`SELECT count(*) FROM notification_deliveries d JOIN notification_events v ON v.id = d.event_id
		WHERE d.user_id = $1 AND v.type = 'security.system' AND d.status = 'queued'`, admin) != 1 {
		t.Fatal("security alerts must bypass the flood rule")
	}
	e.deliverAll()
	var summaries int
	for _, m := range e.mail.sent {
		if m.Subject == "[bekup] Notifications paused for an hour" {
			summaries++
		}
	}
	if summaries != 1 {
		t.Fatalf("summary mails = %d", summaries)
	}
}

// TestRetentionDigest: deletions of a day become one retention.summary per
// job; re-running adds nothing.
func TestRetentionDigest(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.user("admin", false)
	dest := uuid.New()
	e.exec(`INSERT INTO destinations (id, name, type, config) VALUES ($1, 'd', 'disk', '{"path":"x"}')`, dest)
	job := e.job()
	yesterday := time.Now().UTC().Truncate(24 * time.Hour).Add(-12 * time.Hour)
	for range 2 {
		run := uuid.New()
		e.exec(`INSERT INTO runs (id, job_id, trigger, status, queued_at, started_at, finished_at, storage_timestamp)
			VALUES ($1, $2, 'schedule', 'success', $3, $3, $3, date_trunc('second', $3::timestamptz))`, run, job, yesterday.Add(-24*time.Hour))
		art := uuid.New()
		e.exec(`INSERT INTO artifacts (id, run_id, job_id, destination_id, kind, object_key, size, sha256, verified)
			VALUES ($1, $2, $3, $4, 'manifest', $5, 100, $6, true)`, art, run, job, dest, uuid.NewString(), make([]byte, 32))
		if _, err := sqlc.New(e.pool).MarkArtifactDeleted(t.Context(), sqlc.MarkArtifactDeletedParams{ID: art, At: yesterday}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := e.svc.RetentionDigest(t.Context())
	if err != nil || n != 1 {
		t.Fatalf("digest = %d, %v", n, err)
	}
	if n, err := e.svc.RetentionDigest(t.Context()); err != nil || n != 0 {
		t.Fatalf("second digest = %d, %v", n, err)
	}
	var id uuid.UUID
	var detail []byte
	if err := e.pool.QueryRow(t.Context(), `SELECT id, detail FROM notification_events WHERE type = 'retention.summary'`).Scan(&id, &detail); err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(detail, &d); err != nil || d["runs"] != float64(2) || d["bytes"] != float64(200) {
		t.Fatalf("detail %s", detail)
	}
	e.fanOut(id)
	if e.inbox(admin) != 1 {
		t.Fatal("digest not delivered to the inbox")
	}
}

// TestSharedChannels: an e-mail list attached to a job with a group
// filter gets matching events only, never security events.
func TestSharedChannels(t *testing.T) {
	e := newEnv(t, nil)
	job := e.job()
	ch := uuid.New()
	cfg, err := json.Marshal(EmailListConfig{Addresses: []string{"team@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	q := sqlc.New(e.pool)
	if _, err := q.InsertSharedChannel(t.Context(), sqlc.InsertSharedChannelParams{
		ID: ch, Name: "team", Type: sqlc.SharedChannelTypeEmailList, Config: cfg,
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.AttachSharedChannel(t.Context(), sqlc.AttachSharedChannelParams{JobID: job, ChannelID: ch, Groups: []string{"backup"}, Events: []string{}}); err != nil {
		t.Fatal(err)
	}
	e.fanOut(e.emit(Event{Type: BackupSucceeded, Subject: Subject{JobID: job}}))
	e.fanOut(e.emit(Event{Type: JobUpdated, Subject: Subject{JobID: job}}))
	e.deliverAll()
	if e.mail.count() != 1 || e.mail.sent[0].To[0] != "team@example.com" {
		t.Fatalf("shared mails %d", e.mail.count())
	}
	// Detached before delivery: skipped.
	e.fanOut(e.emit(Event{Type: BackupFailed, Subject: Subject{JobID: job}}))
	e.exec(`DELETE FROM job_shared_channels WHERE job_id = $1`, job)
	e.deliverAll()
	if e.mail.count() != 1 || e.count(`SELECT count(*) FROM notification_deliveries WHERE last_code = $1`, CodeChannelDetached) != 1 {
		t.Fatal("a detached channel was delivered")
	}
}

// TestUserChannelLimit: at most 5 personal webhooks per user.
func TestUserChannelLimit(t *testing.T) {
	e := newEnv(t, nil)
	u := e.user("user", false)
	for range 5 {
		e.userWebhook(u)
	}
	_, err := sqlc.New(e.pool).InsertUserChannel(t.Context(), sqlc.InsertUserChannelParams{
		ID: uuid.New(), UserID: u, Name: "x", DisplayUrl: "https://x", UrlEnc: []byte{1}, HmacKeyEnc: []byte{1},
	})
	if err == nil {
		t.Fatal("a sixth webhook was accepted")
	}
}

// TestPrivileges: bekup_app cannot rewrite events or move inbox rows.
func TestPrivileges(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.user("admin", false)
	job := e.job()
	id := e.emit(Event{Type: BackupFailed, Subject: Subject{JobID: job}})
	e.fanOut(id)
	for _, stmt := range []string{
		`UPDATE notification_events SET type = 'backup.succeeded'`,
		`UPDATE notification_events SET detail = '{}'`,
		`UPDATE notifications SET user_id = gen_random_uuid()`,
		`UPDATE notifications SET title = 'x'`,
		`UPDATE user_notification_prefs SET user_id = gen_random_uuid()`,
		`TRUNCATE notification_events`,
	} {
		if _, err := e.pool.Exec(t.Context(), stmt); err == nil {
			t.Errorf("%s: allowed", stmt)
		}
	}
	e.exec(`UPDATE notifications SET read_at = now() WHERE user_id = $1`, admin)
}

// TestDispatcherAndWorkers runs the real River workers: emit → dispatcher
// → fan-out → delivery.
func TestDispatcherAndWorkers(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.user("admin", true)
	job := e.job()
	settings := queue.Settings{
		Queues: map[queue.Queue]queue.PerQueue{
			queue.QueueBackup:      {MaxWorkers: 1, Timeout: time.Minute, MaxAttempts: 2},
			queue.QueueRestore:     {MaxWorkers: 1, Timeout: time.Minute, MaxAttempts: 1},
			queue.QueueRetention:   {MaxWorkers: 1, Timeout: time.Minute, MaxAttempts: 3},
			queue.QueueNotify:      {MaxWorkers: 2, Timeout: time.Minute, MaxAttempts: 10},
			queue.QueueMaintenance: {MaxWorkers: 1, Timeout: time.Minute, MaxAttempts: 3},
		},
		RescueStuckJobsAfter: 2 * time.Hour, ShutdownTimeout: 5 * time.Second, HardStopTimeout: 5 * time.Second,
	}
	ws, err := queue.NewWorkers(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := Register(ws, e.svc); err != nil {
		t.Fatal(err)
	}
	c, err := queue.New(e.pool, queue.Options{Settings: settings, Logger: slog.New(slog.DiscardHandler), Workers: ws, FetchPollInterval: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	e.svc.o.Queue = func() Inserter { return c }
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 2)
	go func() { done <- c.Run(ctx) }()
	go func() { done <- e.svc.Run(ctx) }()
	defer func() {
		cancel()
		<-done
		<-done
	}()
	e.emit(Event{Type: BackupFailed, Subject: Subject{JobID: job}})
	deadline := time.Now().Add(30 * time.Second)
	for e.mail.count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the e-mail was never delivered")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if e.inbox(admin) != 1 {
		t.Fatal("inbox")
	}
}
