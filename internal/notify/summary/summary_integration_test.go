//go:build integration

package summary

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/notify/email"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/serviceutil"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

// ownership answers job_owners questions (as jobs.Ownership does).
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
	f.args = nil
	return out
}

// mailbox captures the rendered e-mails.
type mailbox struct {
	mu   sync.Mutex
	sent []*email.Message
}

func (m *mailbox) Send(_ context.Context, msg *email.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

func (m *mailbox) to(addr string) []*email.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*email.Message
	for _, msg := range m.sent {
		if len(msg.To) == 1 && msg.To[0] == addr {
			out = append(out, msg)
		}
	}
	return out
}

type env struct {
	t    *testing.T
	d    dbtest.DB
	pool *pgxpool.Pool
	mu   sync.Mutex
	now  time.Time
	sum  *Service
	ntf  *notify.Service
	q    *fakeQueue
	mail *mailbox
}

func newEnv(t *testing.T, mod func(*Options)) *env {
	t.Helper()
	d := dbtest.Migrated(t)
	e := &env{t: t, d: d, pool: d.AppPool(t), q: &fakeQueue{}, mail: &mailbox{}, now: at(t, "2026-11-04T10:00:00Z")}
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	policy := authz.New(authz.Options{Ownership: ownership{e.pool}})
	o := Options{
		DB: e.pool, Policy: policy, BaseURL: "https://bekup.test", Logger: slog.New(slog.DiscardHandler),
		Now: e.clock,
	}
	if mod != nil {
		mod(&o)
	}
	if e.sum, err = New(o); err != nil {
		t.Fatal(err)
	}
	if e.ntf, err = notify.New(notify.Options{
		DB: e.pool, Policy: policy, Keyring: kr, Mailer: e.mail, Queue: func() notify.Inserter { return e.q },
		BaseURL: "https://bekup.test", Summaries: e.sum, Logger: slog.New(slog.DiscardHandler),
	}); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

func (e *env) setNow(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.now = at(e.t, s)
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

func (e *env) user(role string, verified bool) (uuid.UUID, string) {
	e.t.Helper()
	id := uuid.New()
	var vat *time.Time
	if verified {
		now := time.Now()
		vat = &now
	}
	addr := id.String()[:8] + "@example.com"
	e.exec(`INSERT INTO users (id, email, display_name, role, email_verified_at) VALUES ($1, $2, 'U', $3, $4)`, id, addr, role, vat)
	return id, addr
}

func (e *env) job(name string, owners ...uuid.UUID) uuid.UUID {
	e.t.Helper()
	id, err := dbtest.InsertJob(e.t.Context(), e.pool, dbtest.JobRow{Enabled: true})
	if err != nil {
		e.t.Fatal(err)
	}
	e.exec(`UPDATE jobs SET name = $2 WHERE id = $1`, id, name)
	for _, o := range owners {
		e.exec(`INSERT INTO job_owners (job_id, user_id) VALUES ($1, $2)`, id, o)
	}
	return id
}

func (e *env) run(job uuid.UUID, status, finished string) {
	e.t.Helper()
	f := at(e.t, finished)
	e.exec(`INSERT INTO runs (id, job_id, trigger, status, queued_at, started_at, finished_at)
		VALUES ($1, $2, 'manual', $3, $4, $4, $5)`, uuid.New(), job, status, f.Add(-time.Minute), f)
}

func (e *env) tick() TickResult {
	e.t.Helper()
	r, err := e.sum.Tick(e.t.Context())
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) events(user uuid.UUID) []uuid.UUID {
	e.t.Helper()
	rows, err := e.pool.Query(e.t.Context(), `SELECT id FROM notification_events WHERE type = 'summary.periodic' AND user_id = $1 ORDER BY created_at, id`, user)
	if err != nil {
		e.t.Fatal(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		e.t.Fatal(err)
	}
	return ids
}

// deliver fans the event out and works its deliveries.
func (e *env) deliver(id uuid.UUID) {
	e.t.Helper()
	if err := e.ntf.FanOut(e.t.Context(), id); err != nil {
		e.t.Fatal(err)
	}
	for _, d := range e.q.deliveries() {
		if _, err := e.ntf.Deliver(e.t.Context(), d, false); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *env) row(user uuid.UUID) sqlc.UserSummarySetting {
	e.t.Helper()
	r, err := sqlc.New(e.pool).GetSummarySettings(e.t.Context(), user)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) as(user uuid.UUID, role auth.Role) context.Context {
	return auth.WithPrincipal(e.t.Context(), auth.Principal{UserID: user, Role: role, SessionID: uuid.New()})
}

func eq(t *testing.T, name string, got *time.Time, want string) {
	t.Helper()
	if got == nil || !got.Equal(at(t, want)) {
		t.Fatalf("%s = %v, want %s", name, got, want)
	}
}

// TestTickDefaultWeekly: without a row an account follows the default
// (weekly, Monday 08:00 UTC); the tick emits exactly one summary per
// period, fan-out gives the inbox row and the e-mail (verified addresses
// only), and a second tick in the same period emits nothing.
func TestTickDefaultWeekly(t *testing.T) {
	e := newEnv(t, nil)
	admin, adminMail := e.user("admin", true)
	owner, ownerMail := e.user("user", false)
	jobA := e.job("Billing", owner)
	e.job("CRM")
	e.run(jobA, "success", "2026-11-05T03:00:00Z")

	if r := e.tick(); r.Planned < 2 || r.Emitted != 0 {
		t.Fatalf("first tick = %+v", r)
	}
	eq(t, "next due", e.row(admin).NextDueAt, "2026-11-09T08:00:00Z")
	if e.row(admin).Frequency != nil {
		t.Fatal("the tick must not store settings")
	}

	e.setNow("2026-11-09T08:02:00Z")
	if r := e.tick(); r.Emitted != 2 {
		t.Fatalf("due tick = %+v", r)
	}
	if r := e.tick(); r.Emitted != 0 {
		t.Fatalf("second tick in the period = %+v", r)
	}
	st := e.row(admin)
	eq(t, "period end", st.LastPeriodEnd, "2026-11-09T08:00:00Z")
	eq(t, "next due", st.NextDueAt, "2026-11-16T08:00:00Z")
	if st.LastOutcome == nil || *st.LastOutcome != "sent" || st.LastSentAt == nil {
		t.Fatalf("state = %+v", st)
	}

	ev := e.events(admin)
	if len(ev) != 1 {
		t.Fatalf("admin events = %d", len(ev))
	}
	e.deliver(ev[0])
	var title, body string
	var link *string
	if err := e.pool.QueryRow(t.Context(), `SELECT title, body, link FROM notifications WHERE user_id = $1`, admin).Scan(&title, &body, &link); err != nil {
		t.Fatal(err)
	}
	if title != "Weekly summary: All 2 backup jobs ran fine" || link == nil || *link != "https://bekup.test/" {
		t.Fatalf("inbox = %q %v", title, link)
	}
	if strings.Contains(body, "Billing") || strings.Contains(body, "CRM") {
		t.Fatalf("inbox body names jobs: %q", body)
	}
	mails := e.mail.to(adminMail)
	if len(mails) != 1 || mails[0].Subject != "[bekup] Weekly summary: All 2 backup jobs ran fine" {
		t.Fatalf("admin mails = %+v", mails)
	}
	if !strings.Contains(mails[0].Text, "This summary covers Mon 2 Nov 2026, 08:00 to Mon 9 Nov 2026, 08:00 (UTC).") ||
		!strings.Contains(mails[0].Text, "Security alerts: none in this period.") ||
		!strings.Contains(mails[0].Text, "https://bekup.test/") {
		t.Fatalf("admin mail:\n%s", mails[0].Text)
	}

	// The owner's address is unverified: inbox only.
	oe := e.events(owner)
	if len(oe) != 1 {
		t.Fatalf("owner events = %d", len(oe))
	}
	e.deliver(oe[0])
	if len(e.mail.to(ownerMail)) != 0 {
		t.Fatal("mail to an unverified address")
	}
	if e.count(`SELECT count(*) FROM notification_deliveries WHERE user_id = $1 AND channel = 'email' AND last_code = 'email_unverified'`, owner) != 1 {
		t.Fatal("unverified e-mail not recorded as skipped")
	}
	if e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND title = 'Weekly summary: Your backup job ran fine'`, owner) != 1 {
		t.Fatal("owner inbox row missing")
	}
}

// TestTickDedupAndCatchUp: a period is summarized once (the dedup key
// holds even when the state is rewound), and after downtime one summary
// covers the whole gap.
func TestTickDedupAndCatchUp(t *testing.T) {
	e := newEnv(t, nil)
	admin, _ := e.user("admin", true)
	e.job("Billing")
	e.tick()
	e.setNow("2026-11-09T08:01:00Z")
	if r := e.tick(); r.Emitted != 1 {
		t.Fatalf("tick = %+v", r)
	}
	// Rewind the state: the same period is due again.
	e.exec(`UPDATE user_summary_settings SET next_due_at = '2026-11-09T08:00:00Z', last_period_end = NULL WHERE user_id = $1`, admin)
	if r := e.tick(); r.Emitted != 0 || len(e.events(admin)) != 1 {
		t.Fatalf("dedup tick = %+v, events %d", r, len(e.events(admin)))
	}
	eq(t, "next due", e.row(admin).NextDueAt, "2026-11-16T08:00:00Z")

	// Three weeks of downtime: one summary from the last period end.
	e.setNow("2026-12-02T12:00:00Z")
	if r := e.tick(); r.Emitted != 1 {
		t.Fatalf("catch-up tick = %+v", r)
	}
	ev := e.events(admin)
	if len(ev) != 2 {
		t.Fatalf("events = %d", len(ev))
	}
	var from, until string
	if err := e.pool.QueryRow(t.Context(), `SELECT detail->>'period_start', detail->>'period_end' FROM notification_events WHERE id = $1`, ev[1]).Scan(&from, &until); err != nil {
		t.Fatal(err)
	}
	if from != "2026-11-09T08:00:00Z" || until != "2026-11-30T08:00:00Z" {
		t.Fatalf("catch-up period = %s .. %s", from, until)
	}
	eq(t, "next due", e.row(admin).NextDueAt, "2026-12-07T08:00:00Z")
}

// TestTickNeverAndNoJobs: "never" sends nothing; an account that can see
// no job gets no summary (recorded as no_jobs).
func TestTickNeverAndNoJobs(t *testing.T) {
	e := newEnv(t, nil)
	admin, _ := e.user("admin", true)
	lonely, _ := e.user("user", true)
	e.job("Billing")
	if _, err := e.sum.SetSettings(e.as(admin, auth.RoleAdmin), Settings{Frequency: Never, Hour: 8, Weekday: 1, MonthDay: 1, Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	if e.row(admin).NextDueAt != nil {
		t.Fatal("never must have no due time")
	}
	e.tick()
	e.setNow("2027-03-01T00:00:00Z")
	e.tick()
	if len(e.events(admin)) != 0 || len(e.events(lonely)) != 0 {
		t.Fatal("summaries were emitted")
	}
	st := e.row(lonely)
	if st.LastOutcome == nil || *st.LastOutcome != "no_jobs" || st.LastSentAt != nil {
		t.Fatalf("no-jobs state = %+v", st)
	}
	if !st.NextDueAt.After(e.clock()) {
		t.Fatal("no-jobs summary did not advance")
	}
	if _, err := e.sum.SendPreview(e.as(lonely, auth.RoleUser)); err == nil {
		t.Fatal("a preview without jobs must be refused")
	}
}

// TestOwnerScoping: a backup owner's summary covers only their jobs; an
// admin's every job, with the attention list in the e-mail.
func TestOwnerScoping(t *testing.T) {
	e := newEnv(t, nil)
	admin, adminMail := e.user("admin", true)
	owner, ownerMail := e.user("user", true)
	mine := e.job("Billing", owner)
	other := e.job("Secret payroll")
	e.run(mine, "success", "2026-11-05T03:00:00Z")
	e.run(other, "failed", "2026-11-05T03:00:00Z")
	e.tick()
	e.setNow("2026-11-09T08:01:00Z")
	e.tick()
	e.deliver(e.events(owner)[0])
	e.deliver(e.events(admin)[0])
	om := e.mail.to(ownerMail)
	if len(om) != 1 || om[0].Subject != "[bekup] Weekly summary: Your backup job ran fine" {
		t.Fatalf("owner mail = %+v", om)
	}
	if strings.Contains(om[0].Text, "Secret payroll") || strings.Contains(om[0].Text, "failed") ||
		strings.Contains(om[0].Text, "Storage:") || strings.Contains(om[0].Text, "Security alerts") {
		t.Fatalf("owner mail shows what the owner may not see:\n%s", om[0].Text)
	}
	am := e.mail.to(adminMail)
	if len(am) != 1 || am[0].Subject != "[bekup] Weekly summary: Needs attention: 1 of 2 jobs" ||
		!strings.Contains(am[0].Text, "Secret payroll: 1 failed backup") {
		t.Fatalf("admin mail = %+v", am)
	}
}

// TestPreferencesApply: the e-mail of the summary can be turned off in the
// preference matrix (the inbox row stays).
func TestPreferencesApply(t *testing.T) {
	e := newEnv(t, nil)
	admin, adminMail := e.user("admin", true)
	e.job("Billing")
	if err := sqlc.New(e.pool).UpsertNotificationPref(t.Context(), sqlc.UpsertNotificationPrefParams{
		UserID: admin, Event: string(notify.SummaryPeriodic), Channel: sqlc.NotificationChannelEmail, Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}
	e.tick()
	e.setNow("2026-11-09T08:01:00Z")
	e.tick()
	e.deliver(e.events(admin)[0])
	if len(e.mail.to(adminMail)) != 0 || e.count(`SELECT count(*) FROM notifications WHERE user_id = $1`, admin) != 1 {
		t.Fatal("preferences not applied")
	}
}

// TestSettingsAndPreview: own settings move the due time into the user's
// zone; the preview is emitted at once (marked) and rate limited.
func TestSettingsAndPreview(t *testing.T) {
	calls := 0
	e := newEnv(t, func(o *Options) {
		o.PreviewLimiter = func(string) (bool, time.Duration) {
			calls++
			return calls == 1, time.Minute
		}
	})
	admin, adminMail := e.user("admin", true)
	e.job("Billing")
	ctx := e.as(admin, auth.RoleAdmin)
	v, err := e.sum.MySettings(ctx)
	if err != nil || v.Custom || v.Settings.Frequency != Weekly || v.Jobs != 1 || !v.EmailVerified {
		t.Fatalf("defaults view = %+v, %v", v, err)
	}
	eq(t, "default next", v.NextDue, "2026-11-09T08:00:00Z")
	v, err = e.sum.SetSettings(ctx, Settings{Frequency: Daily, Hour: 7, Weekday: 1, MonthDay: 1, Timezone: "Europe/Amsterdam"})
	if err != nil || !v.Custom {
		t.Fatalf("set = %+v, %v", v, err)
	}
	eq(t, "own next", v.NextDue, "2026-11-05T06:00:00Z")
	var fe *serviceutil.FieldError
	if _, err := e.sum.SetSettings(ctx, Settings{Frequency: Daily, Hour: 7, Weekday: 1, MonthDay: 1, Timezone: "Mars/Base"}); !errors.As(err, &fe) || fe.Field != "timezone" {
		t.Fatalf("bad zone = %v", err)
	}
	if _, err := e.sum.SetSettings(ctx, Settings{Frequency: Monthly, Hour: 7, Weekday: 1, MonthDay: 31, Timezone: "UTC"}); !errors.As(err, &fe) || fe.Field != "monthDay" {
		t.Fatalf("bad day = %v", err)
	}
	// A user without the operation's principal is refused.
	if _, err := e.sum.MySettings(t.Context()); err == nil {
		t.Fatal("anonymous access")
	}

	p, err := e.sum.SendPreview(ctx)
	if err != nil || !p.EmailVerified || !p.PeriodEnd.Equal(e.clock()) {
		t.Fatalf("preview = %+v, %v", p, err)
	}
	var rl *serviceutil.RateLimitedError
	if _, err := e.sum.SendPreview(ctx); !errors.As(err, &rl) {
		t.Fatalf("second preview = %v", err)
	}
	ev := e.events(admin)
	if len(ev) != 1 {
		t.Fatalf("preview events = %d", len(ev))
	}
	e.deliver(ev[0])
	m := e.mail.to(adminMail)
	if len(m) != 1 || !strings.HasPrefix(m[0].Subject, "[bekup] Summary preview: ") || !strings.Contains(m[0].Text, "This is a preview") ||
		!strings.Contains(m[0].Text, "(Europe/Amsterdam)") {
		t.Fatalf("preview mail = %+v", m)
	}
	// The preview does not count as the period's summary.
	if st := e.row(admin); st.LastPeriodEnd != nil {
		t.Fatal("the preview moved the schedule")
	}
}
