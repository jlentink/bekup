//go:build integration

package connections

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/notify/email"
	"github.com/jlentink/bekup/internal/queue"
)

type alertQueue struct {
	mu   sync.Mutex
	args []river.JobArgs
}

func (f *alertQueue) InsertTx(_ context.Context, _ pgx.Tx, args river.JobArgs, _ queue.InsertOptions) (queue.InsertResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.args = append(f.args, args)
	return queue.InsertResult{JobID: int64(len(f.args))}, nil
}

type alertMailer struct {
	mu sync.Mutex
	to []string
}

func (m *alertMailer) Send(_ context.Context, msg *email.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.to = append(m.to, msg.To...)
	return nil
}

// TestTLSDowngradeAlertsAdmins: a TLS downgrade (an opt-out) reaches every
// admin's inbox and e-mail as a mandatory security.system alert, even with
// the admin's preferences for it switched off, and no other role (task
// 5.5, end to end from the service call to the sent e-mail).
func TestTLSDowngradeAlertsAdmins(t *testing.T) {
	e := newSvcEnv(t)
	ctx := t.Context()
	auditor := audit.New(e.pool, audit.Options{Registry: logging.NewRegistry(), Observer: notify.ObserveAudit})
	svc, err := New(Options{
		DB: e.pool, Keyring: testKeyring(t), Auditor: auditor, Runner: e.runner,
		Limiter: func(string) (bool, time.Duration) { return true, 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	maint, err := e.q.InsertUser(ctx, sqlc.InsertUserParams{Email: "maint@example.com", DisplayName: "M", Role: sqlc.UserRoleMaintainer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE users SET email_verified_at = now()`); err != nil {
		t.Fatal(err)
	}
	for _, ch := range []sqlc.NotificationChannel{sqlc.NotificationChannelInbox, sqlc.NotificationChannelEmail} {
		if err := e.q.UpsertNotificationPref(ctx, sqlc.UpsertNotificationPrefParams{
			UserID: e.actor.UserID, Event: string(notify.SecuritySystem), Channel: ch, Enabled: false,
		}); err != nil {
			t.Fatal(err)
		}
	}

	c, err := svc.Create(ctx, e.actor, CreateInput{
		Name: "Legacy", Engine: engine.KindMariaDB, VersionKey: "10.11", Host: "10.1.2.3", Username: "root", TLSMode: TLSDisabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	var id uuid.UUID
	if err := e.pool.QueryRow(ctx, `SELECT id FROM notification_events
		WHERE type = 'security.system' AND detail->>'kind' = 'opt_out' AND connection_id = $1`, c.ID).Scan(&id); err != nil {
		t.Fatalf("no security.system event: %v", err)
	}

	q := &alertQueue{}
	mail := &alertMailer{}
	ns, err := notify.New(notify.Options{
		DB: e.pool, Policy: authz.New(authz.Options{}), Keyring: testKeyring(t), Mailer: mail,
		Queue: func() notify.Inserter { return q }, BaseURL: "https://bekup.test", Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ns.FanOut(ctx, id); err != nil {
		t.Fatal(err)
	}
	for _, a := range q.args {
		if d, ok := a.(queue.NotifyDeliverArgs); ok {
			if _, err := ns.Deliver(ctx, d.DeliveryID, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	inbox := func(u uuid.UUID) int {
		var n int
		if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id = $1 AND event_id = $2`, u, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if inbox(e.actor.UserID) != 1 || inbox(maint.ID) != 0 {
		t.Fatalf("inbox admin=%d maintainer=%d", inbox(e.actor.UserID), inbox(maint.ID))
	}
	if !slices.Equal(mail.to, []string{"admin@example.com"}) {
		t.Fatalf("e-mail went to %v", mail.to)
	}
}
