//go:build integration

package summary

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
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
	if title != "Weekly summary: no problems (1 OK)" || link == nil || *link != "https://bekup.test/" {
		t.Fatalf("inbox = %q %v", title, link)
	}
	if strings.Contains(body, "Billing") || strings.Contains(body, "CRM") {
		t.Fatalf("inbox body names jobs: %q", body)
	}
	mails := e.mail.to(adminMail)
	if len(mails) != 1 || mails[0].Subject != "[bekup] Weekly summary: no problems" {
		t.Fatalf("admin mails = %+v", mails)
	}
	if !strings.Contains(mails[0].Text, "Period: Mon 2 Nov 2026, 08:00 – Mon 9 Nov 2026, 08:00 (UTC).") ||
		!strings.Contains(mails[0].Text, "No security alerts in this period") ||
		!strings.Contains(mails[0].Text, "All OK: No problems in the last 7 days (1 OK, 1 without a run).") ||
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
	if e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND title = 'Weekly summary: your backup job is OK'`, owner) != 1 {
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
	var from, until, catchUp string
	if err := e.pool.QueryRow(t.Context(), `SELECT detail->>'period_start', detail->>'period_end', detail->>'catch_up' FROM notification_events WHERE id = $1`, ev[1]).Scan(&from, &until, &catchUp); err != nil {
		t.Fatal(err)
	}
	if from != "2026-11-09T08:00:00Z" || until != "2026-11-30T08:00:00Z" || catchUp != "true" {
		t.Fatalf("catch-up period = %s .. %s (catch_up %s)", from, until, catchUp)
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
	if len(om) != 1 || om[0].Subject != "[bekup] Weekly summary: your backup job is OK" {
		t.Fatalf("owner mail = %+v", om)
	}
	if strings.Contains(om[0].Text, "Secret payroll") || strings.Contains(om[0].Text, ">>") ||
		strings.Contains(om[0].Text, "\nStorage\n") || strings.Contains(om[0].Text, "Security alerts") {
		t.Fatalf("owner mail shows what the owner may not see:\n%s", om[0].Text)
	}
	am := e.mail.to(adminMail)
	if len(am) != 1 || am[0].Subject != "[bekup] Weekly summary: 1 FAILED" ||
		!regexp.MustCompile(`(?m)^>> Failed +Secret payroll +0/1 `).MatchString(am[0].Text) {
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
	if err != nil || !p.EmailVerified || !p.PeriodEnd.Equal(e.clock()) || !p.PeriodStart.Equal(e.clock().Add(-24*time.Hour)) {
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

// TestSummaryDayContent: a daily summary of a seeded day (successes with
// sizes, a failure with an error code, a missed job) has the right rows,
// sizes and level; SummaryJobActivity respects the owner scope; and a
// change from weekly 08:00 to daily 12:00 still covers 24 hours.
func TestSummaryDayContent(t *testing.T) {
	e := newEnv(t, nil)
	admin, adminMail := e.user("admin", true)
	owner, _ := e.user("user", true)
	good := e.job("Billing", owner)
	bad := e.job("CRM")
	missed := e.job("Docs")
	e.exec(`INSERT INTO runs (id, job_id, trigger, status, queued_at, started_at, finished_at, bytes_stored)
		VALUES ($1, $2, 'manual', 'success', '2026-11-08T23:00:00Z', '2026-11-08T23:00:00Z', '2026-11-08T23:10:00Z', $3)`,
		uuid.New(), good, int64(3)<<30)
	e.exec(`INSERT INTO runs (id, job_id, trigger, status, queued_at, started_at, finished_at, bytes_stored)
		VALUES ($1, $2, 'manual', 'success', '2026-11-09T03:00:00Z', '2026-11-09T03:00:00Z', '2026-11-09T03:20:00Z', $3)`,
		uuid.New(), good, int64(512)<<20)
	e.exec(`INSERT INTO runs (id, job_id, trigger, status, queued_at, started_at, finished_at, error_code, error_message)
		VALUES ($1, $2, 'manual', 'failed', '2026-11-09T04:00:00Z', '2026-11-09T04:00:00Z', '2026-11-09T04:01:00Z',
		        'target_unreachable', 'the database server could not be reached')`, uuid.New(), bad)
	// Outside the period: not counted.
	e.run(good, "failed", "2026-11-08T11:00:00Z")
	e.exec(`INSERT INTO job_missed_state (job_id, missed_since, last_missed_at, reason, checked_at)
		VALUES ($1, '2026-11-08T03:00:00Z', '2026-11-09T03:00:00Z', 'no_run', now())`, missed)

	// Weekly 08:00 (default) planned, then daily 12:00 in UTC.
	e.setNow("2026-11-09T08:00:30Z")
	e.tick()
	e.exec(`UPDATE user_summary_settings SET last_period_end = '2026-11-09T08:00:00Z' WHERE user_id = $1`, admin)
	if _, err := e.sum.SetSettings(e.as(admin, auth.RoleAdmin), Settings{Frequency: Daily, Hour: 12, Weekday: 1, MonthDay: 1, Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	e.setNow("2026-11-09T12:01:00Z")
	e.tick()
	ev := e.events(admin)
	if len(ev) != 1 {
		t.Fatalf("events = %d", len(ev))
	}
	var from, until, catchUp string
	var hasLevel bool
	if err := e.pool.QueryRow(t.Context(), `SELECT detail->>'period_start', detail->>'period_end', detail->>'catch_up', detail ? 'level'
		FROM notification_events WHERE id = $1`, ev[0]).Scan(&from, &until, &catchUp, &hasLevel); err != nil {
		t.Fatal(err)
	}
	// The switch from weekly to daily is no catch-up.
	if from != "2026-11-08T12:00:00Z" || until != "2026-11-09T12:00:00Z" || catchUp != "false" || hasLevel {
		t.Fatalf("period %s .. %s, catch_up %s, level %v", from, until, catchUp, hasLevel)
	}
	e.deliver(ev[0])
	m := e.mail.to(adminMail)
	if len(m) != 1 || m[0].Subject != "[bekup] Daily summary: 1 FAILED, 1 missed" {
		t.Fatalf("mail = %+v", m)
	}
	for _, re := range []string{
		`(?m)^!! 1 failed: 1 of 3 jobs failed and 1 job missed its backups in the last 24 hours`,
		`(?m)^>> Failed +CRM +0/1 +9 Nov 04:01 +0 B +–$`,
		`(?m)^ +target unreachable$`,
		`(?m)^>> Missed +Docs +0/0 +– +– +–$`,
		`(?m)^ +missed since Sun 8 Nov 2026, 03:00$`,
		`(?m)^   OK +Billing +2/0 +9 Nov 03:20 +3\.5 GiB +–$`,
		`Written: 3\.5 GiB`,
	} {
		if !regexp.MustCompile(re).MatchString(m[0].Text) {
			t.Errorf("mail lacks %s:\n%s", re, m[0].Text)
		}
	}
	if !strings.Contains(m[0].HTML, ">target unreachable</td>") || strings.Contains(m[0].Text, "could not be reached") {
		t.Error("the failure shows the fixed code text, never the stored message")
	}

	// The activity query is scoped: the owner sees only their job.
	q := sqlc.New(e.pool)
	rows, err := q.SummaryJobActivity(t.Context(), sqlc.SummaryJobActivityParams{
		PeriodFrom: at(t, from), PeriodUntil: at(t, until), ScopeAll: false, UserID: owner,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].JobID != good || rows[0].Succeeded != 2 || rows[0].BytesStored != int64(3)<<30+int64(512)<<20 ||
		!rows[0].LastFinishedAt.Equal(at(t, "2026-11-09T03:20:00Z")) || rows[0].LastFailureCode != "" {
		t.Fatalf("owner activity = %+v", rows)
	}
	all, err := q.SummaryJobActivity(t.Context(), sqlc.SummaryJobActivityParams{PeriodFrom: at(t, from), PeriodUntil: at(t, until), ScopeAll: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("all activity = %+v", all)
	}
	for _, r := range all {
		if r.JobID == bad && (r.Failed != 1 || r.LastFailureCode != "target_unreachable") {
			t.Fatalf("failed job activity = %+v", r)
		}
	}
	restores, err := q.SummaryRestoreCounts(t.Context(), sqlc.SummaryRestoreCountsParams{PeriodFrom: at(t, from), PeriodUntil: at(t, until), ScopeAll: true})
	if err != nil || len(restores) != 0 {
		t.Fatalf("restores = %+v, %v", restores, err)
	}
}

// TestSummaryQueryBounds: the content queries count what finished in
// (period_from, period_until] (a finish at period_from belongs to the
// previous summary, one at period_until to this one), follow the owner
// scope, take the newest failure's code inside the period only, and
// SummaryRestoreCounts leaves out the internal restores of test restores.
func TestSummaryQueryBounds(t *testing.T) {
	e := newEnv(t, nil)
	owner, _ := e.user("user", true)
	mine := e.job("Billing", owner)
	other := e.job("CRM")
	const from, until = "2026-11-08T12:00:00Z", "2026-11-09T12:00:00Z"
	failed := func(job uuid.UUID, finished, code string) {
		f := at(t, finished)
		e.exec(`INSERT INTO runs (id, job_id, trigger, status, queued_at, started_at, finished_at, error_code)
			VALUES ($1, $2, 'manual', 'failed', $3, $3, $4, $5)`, uuid.New(), job, f.Add(-time.Minute), f, code)
	}
	e.run(mine, "success", from)  // excluded: the previous period's
	e.run(mine, "success", until) // included
	e.run(mine, "success", "2026-11-09T12:00:01Z")
	failed(mine, "2026-11-08T13:00:00Z", "dump_failed")
	failed(mine, "2026-11-08T14:00:00Z", "target_unreachable") // the newest failure in the period
	failed(mine, from, "timeout")                              // excluded
	failed(mine, "2026-11-09T12:00:01Z", "verify_failed")      // after the period
	e.run(other, "success", "2026-11-08T15:00:00Z")

	// Restores and test restores (foreign keys off: artifacts, run items
	// and connections are not needed for the counts).
	conn := uuid.New()
	restore := func(job uuid.UUID, status, finished string) uuid.UUID {
		id := uuid.New()
		if err := e.d.SuperExec(t.Context(), "SET session_replication_role = replica", fmt.Sprintf(
			`INSERT INTO restores (id, artifact_id, run_item_id, job_id, target_connection_id, target_db_name, mode, status, started_at, finished_at)
			VALUES ('%s', '%s', '%s', '%s', '%s', 'app', 'new_db', '%s', '%s', '%s')`,
			id, uuid.New(), uuid.New(), job, conn, status, finished, finished)); err != nil {
			t.Fatal(err)
		}
		return id
	}
	testRestore := func(job uuid.UUID, status, finished string) uuid.UUID {
		id := uuid.New()
		if err := e.d.SuperExec(t.Context(), "SET session_replication_role = replica", fmt.Sprintf(
			`INSERT INTO test_restores (id, job_id, target_connection_id, trigger, status, started_at, finished_at)
			VALUES ('%s', '%s', '%s', 'schedule', '%s', '%s', '%s')`, id, job, conn, status, finished, finished)); err != nil {
			t.Fatal(err)
		}
		return id
	}
	restore(mine, "succeeded", until)
	restore(mine, "succeeded", "2026-11-09T01:00:00Z")
	restore(mine, "failed", from)                   // excluded
	restore(mine, "failed", "2026-11-09T12:00:01Z") // after the period
	restore(other, "canceled", "2026-11-09T02:00:00Z")
	// The internal restore of a test restore: counted as a test restore only.
	internal := restore(mine, "failed", "2026-11-09T03:00:00Z")
	tr := testRestore(mine, "failed", "2026-11-09T03:05:00Z")
	if err := e.d.SuperExec(t.Context(), "SET session_replication_role = replica", fmt.Sprintf(
		`INSERT INTO test_restore_items (test_restore_id, run_item_id, restore_id, db_name, scratch_id, status, finished_at)
		VALUES ('%s', '%s', '%s', 'app', 'abcdef123456', 'failed', '%s')`, tr, uuid.New(), internal, "2026-11-09T03:05:00Z")); err != nil {
		t.Fatal(err)
	}
	testRestore(mine, "passed", until)
	testRestore(mine, "passed", from) // excluded
	testRestore(other, "error", "2026-11-09T04:00:00Z")

	q := sqlc.New(e.pool)
	pf, pu := at(t, from), at(t, until)
	type scope struct {
		name string
		all  bool
	}
	for _, sc := range []scope{{"owner", false}, {"all", true}} {
		act, err := q.SummaryJobActivity(t.Context(), sqlc.SummaryJobActivityParams{PeriodFrom: pf, PeriodUntil: pu, ScopeAll: sc.all, UserID: owner})
		if err != nil {
			t.Fatal(err)
		}
		if want := map[bool]int{false: 1, true: 2}[sc.all]; len(act) != want {
			t.Fatalf("%s: activity rows = %+v", sc.name, act)
		}
		for _, r := range act {
			if r.JobID == mine && (r.Succeeded != 1 || r.Failed != 2 || r.LastFailureCode != "target_unreachable" || !r.LastFinishedAt.Equal(pu)) {
				t.Fatalf("%s: activity = %+v", sc.name, r)
			}
			if r.JobID == other && (r.Succeeded != 1 || r.Failed != 0 || r.LastFailureCode != "") {
				t.Fatalf("%s: other activity = %+v", sc.name, r)
			}
		}
		rs, err := q.SummaryRestoreCounts(t.Context(), sqlc.SummaryRestoreCountsParams{PeriodFrom: pf, PeriodUntil: pu, ScopeAll: sc.all, UserID: owner})
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]int64{}
		for _, r := range rs {
			got[r.Status] = r.Restores
		}
		want := map[string]int64{"succeeded": 2}
		if sc.all {
			want["canceled"] = 1
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("%s: restores = %v, want %v", sc.name, got, want)
		}
		ts, err := q.SummaryTestRestoreCounts(t.Context(), sqlc.SummaryTestRestoreCountsParams{PeriodFrom: pf, PeriodUntil: pu, ScopeAll: sc.all, UserID: owner})
		if err != nil {
			t.Fatal(err)
		}
		gotT := map[string]int64{}
		for _, r := range ts {
			gotT[r.Status] += r.TestRestores
		}
		wantT := map[string]int64{"failed": 1, "passed": 1}
		if sc.all {
			wantT["error"] = 1
		}
		if fmt.Sprint(gotT) != fmt.Sprint(wantT) {
			t.Fatalf("%s: test restores = %v, want %v", sc.name, gotT, wantT)
		}
	}
}

// TestSummaryStoredSpace: the storage table shows the stored answers of
// the space check (D-227) and never asks a backend: free space with the
// check time, low space, a stale answer, a failed check with its fixed
// reason, S3 without a limit, an old answer without a limit (stale), an
// answer hidden by a settings change and a destination never checked; the
// banner counts the low one.
func TestSummaryStoredSpace(t *testing.T) {
	e := newEnv(t, nil)
	admin, adminMail := e.user("admin", true)
	e.job("Billing")
	now := e.clock() // 2026-11-04T10:00:00Z
	dest := func(name, typ string) uuid.UUID {
		id := uuid.New()
		e.exec(`INSERT INTO destinations (id, name, type, config) VALUES ($1, $2, $3, '{}')`, id, name, typ)
		return id
	}
	check := func(id uuid.UUID, status string, code *string, free, total *int64, low *string, ago time.Duration) {
		e.exec(`UPDATE destinations SET space_checked_at = $2, space_status = $3, space_code = $4, space_free_bytes = $5,
			space_total_bytes = $6, space_low_cause = $7, space_settings_at = updated_at WHERE id = $1`,
			id, now.Add(-ago), status, code, free, total, low)
	}
	i64 := func(v int64) *int64 { return &v }
	s := func(v string) *string { return &v }
	check(dest("a-nas", "disk"), "available", nil, i64(500<<30), i64(1<<40), nil, 20*time.Minute)
	check(dest("b-full", "sftp"), "available", nil, i64(1<<30), i64(1<<40), s("fraction"), 30*time.Minute)
	check(dest("c-old", "webdav"), "available", nil, i64(200<<30), nil, nil, 5*time.Hour)
	check(dest("d-down", "sftp"), "unavailable", s("network"), nil, nil, nil, 10*time.Minute)
	check(dest("e-bucket", "s3"), "unsupported", s("unsupported"), nil, nil, nil, 15*time.Minute)
	check(dest("h-quota", "webdav"), "unsupported", s("unsupported"), nil, nil, nil, 48*time.Hour)
	moved := dest("f-moved", "disk")
	check(moved, "available", nil, i64(9<<30), i64(10<<30), nil, time.Hour)
	e.exec(`UPDATE destinations SET config = '{"path":"elsewhere"}' WHERE id = $1`, moved)
	dest("g-new", "disk")

	if _, err := e.sum.SendPreview(e.as(admin, auth.RoleAdmin)); err != nil {
		t.Fatal(err)
	}
	ev := e.events(admin)
	if len(ev) != 1 {
		t.Fatalf("events = %d", len(ev))
	}
	e.deliver(ev[0])
	m := e.mail.to(adminMail)
	if len(m) != 1 {
		t.Fatalf("mail = %+v", m)
	}
	for _, re := range []string{
		`(?m)^   OK +a-nas +0 B +500\.0 GiB +0$`,
		`(?m)^ +checked 09:40$`,
		`(?m)^>> Low space +b-full +0 B +1\.0 GiB +0$`,
		`(?m)^ +less than 10% free; checked 09:30$`,
		`(?m)^>> Stale +c-old +0 B +200\.0 GiB +0$`,
		`(?m)^ +last checked 4 Nov 05:00, more than 3 hours ago$`,
		`(?m)^>> Check failed +d-down +0 B +– +0$`,
		`(?m)^ +the destination could not be reached; checked 09:50$`,
		`(?m)^   No limit +e-bucket +0 B +– +0$`,
		`(?m)^ +no limit reported: S3 buckets have no fixed size; checked 09:45$`,
		`(?m)^   Not checked +f-moved +0 B +– +0$`,
		`(?m)^ +not checked since the settings changed$`,
		`(?m)^   Not checked +g-new +0 B +– +0$`,
		`(?m)^ +the hourly space check has not run yet$`,
		`(?m)^>> Stale +h-quota +0 B +– +0$`,
		`(?m)^ +no limit reported: the server reports no free space or quota; last checked 2 Nov 10:00, more than 3 hours ago$`,
		`1 destination is low on space`,
	} {
		if !regexp.MustCompile(re).MatchString(m[0].Text) {
			t.Errorf("mail lacks %s:\n%s", re, m[0].Text)
		}
	}
}
