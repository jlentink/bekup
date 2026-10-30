//go:build integration

package db_test

import (
	"testing"
	"time"
)

// TestMigration00033KeepsData migrates a database with notification data to
// 00032, applies 00033 (D-206) and checks that every inbox/e-mail/webhook
// row survives while the stored push preference is gone; then down and up
// again.
func TestMigration00033KeepsData(t *testing.T) {
	ctx := withTimeout(t, 2*time.Minute)
	d := newTestDB(t)
	m := d.migrator(t)
	if _, err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DownTo(ctx, 32); err != nil {
		t.Fatal(err)
	}
	p := d.ownerPool(t)
	const user = "6f1f5b52-8c3a-4b0e-9a3e-2d6c0f3b9a01"
	const event = "6f1f5b52-8c3a-4b0e-9a3e-2d6c0f3b9a02"
	const channel = "6f1f5b52-8c3a-4b0e-9a3e-2d6c0f3b9a03"
	for _, q := range []string{
		`INSERT INTO public.users (id, email, display_name) VALUES ('` + user + `', 'push@example.com', 'Push')`,
		`INSERT INTO public.notification_events (id, type, severity) VALUES ('` + event + `', 'backup.failed', 'error')`,
		`INSERT INTO public.user_channels (id, user_id, type, name, display_url, url_enc, hmac_key_enc)
		 VALUES ('` + channel + `', '` + user + `', 'webhook', 'hook', 'https://hooks.example.com/…', '\x01', '\x02')`,
		`INSERT INTO public.user_notification_prefs (user_id, event, channel, enabled) VALUES
		 ('` + user + `', 'backup.failed', 'inbox', true),
		 ('` + user + `', 'backup.failed', 'email', false),
		 ('` + user + `', 'backup.failed', 'webhook', true),
		 ('` + user + `', 'backup.failed', 'push', true)`,
		`INSERT INTO public.notification_deliveries (event_id, user_id, channel, idempotency_key) VALUES
		 ('` + event + `', '` + user + `', 'email', 'k-email')`,
		`INSERT INTO public.notification_deliveries (event_id, user_id, user_channel_id, channel, idempotency_key) VALUES
		 ('` + event + `', '` + user + `', '` + channel + `', 'webhook', 'k-webhook')`,
	} {
		if _, err := p.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}

	var prefs string
	if err := p.QueryRow(ctx, `SELECT string_agg(channel::text || '=' || enabled::text, ',' ORDER BY channel::text)
		FROM public.user_notification_prefs WHERE user_id = $1`, user).Scan(&prefs); err != nil {
		t.Fatal(err)
	}
	if prefs != "email=false,inbox=true,webhook=true" {
		t.Fatalf("preferences after 00033: %s", prefs)
	}
	var deliveries string
	if err := p.QueryRow(ctx, `SELECT string_agg(channel::text || ':' || idempotency_key, ',' ORDER BY idempotency_key)
		FROM public.notification_deliveries WHERE event_id = $1`, event).Scan(&deliveries); err != nil {
		t.Fatal(err)
	}
	if deliveries != "email:k-email,webhook:k-webhook" {
		t.Fatalf("deliveries after 00033: %s", deliveries)
	}
	var channels int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM public.user_channels WHERE id = $1 AND type = 'webhook'`, channel).Scan(&channels); err != nil || channels != 1 {
		t.Fatalf("user channel after 00033: %d %v", channels, err)
	}
	for _, typ := range []string{"notification_channel", "user_channel_type"} {
		var labels string
		if err := p.QueryRow(ctx, `SELECT string_agg(e.enumlabel, ',' ORDER BY e.enumsortorder)
			FROM pg_catalog.pg_enum e JOIN pg_catalog.pg_type t ON t.oid = e.enumtypid
			JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace
			WHERE n.nspname = 'public' AND t.typname = $1`, typ).Scan(&labels); err != nil {
			t.Fatal(err)
		}
		if want := map[string]string{"notification_channel": "inbox,email,webhook", "user_channel_type": "webhook"}[typ]; labels != want {
			t.Fatalf("%s = %s, want %s", typ, labels, want)
		}
	}
	// The re-added CHECK constraints still hold.
	if _, err := p.Exec(ctx, `INSERT INTO public.notification_deliveries (event_id, user_id, channel, idempotency_key)
		VALUES ($1, $2, 'inbox', 'k-inbox')`, event, user); pgCode(err) != "23514" {
		t.Fatalf("inbox delivery: %v", err)
	}
	if _, err := p.Exec(ctx, `INSERT INTO public.user_notification_prefs (user_id, event, channel, enabled)
		VALUES ($1, 'backup.succeeded', 'push', true)`, user); pgCode(err) != "22P02" {
		t.Fatalf("push preference after 00033: %v", err)
	}

	// Down appends push again (data kept), up removes it.
	if _, err := m.DownTo(ctx, 32); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `INSERT INTO public.user_notification_prefs (user_id, event, channel, enabled)
		VALUES ($1, 'backup.succeeded', 'push', true)`, user); err != nil {
		t.Fatalf("push preference after down: %v", err)
	}
	if _, err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM public.user_notification_prefs WHERE user_id = $1`, user).Scan(&n); err != nil || n != 3 {
		t.Fatalf("preferences after down/up: %d %v", n, err)
	}
}
