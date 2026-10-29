//go:build integration

package egress

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/authz/authztest"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/pubsub"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

type env struct {
	t     *testing.T
	pool  *pgxpool.Pool
	bus   *pubsub.Bus
	guard *netguard.Set
	svc   *Service
	admin auth.Principal
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := dbtest.Migrated(t)
	e := &env{t: t, pool: d.AppPool(t), bus: pubsub.NewBus()}
	var err error
	if e.guard, err = netguard.NewSet(netguard.Options{AllowCIDRs: []string{"10.9.0.0/16"}}); err != nil {
		t.Fatal(err)
	}
	e.svc = e.service(e.guard)
	e.admin = authztest.Principal(auth.RoleAdmin)
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO users (id, email, display_name, role) VALUES ($1, 'a@example.com', 'A', 'admin')`, e.admin.UserID); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) service(g *netguard.Set) *Service {
	e.t.Helper()
	s, err := New(Options{
		DB: e.pool, Auditor: audit.New(e.pool, audit.Options{Observer: notify.ObserveAudit}), Policy: authz.New(authz.Options{}),
		Guards: g, Publisher: &pubsub.Publisher{Bus: e.bus}, ConfigCIDRs: []string{"10.9.0.0/16"},
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *env) as(p auth.Principal) context.Context { return authztest.Context(e.t.Context(), p) }

// allowed checks the destinations guard of g.
func allowed(g *netguard.Set, ip string) bool {
	return allowedIn(g, netguard.ScopeDestinations, ip)
}

func allowedIn(g *netguard.Set, sc netguard.Scope, ip string) bool {
	return g.Guard(sc).Check(netip.MustParseAddr(ip), "") == nil
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(e.t.Context(), sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestUpdateAndApply(t *testing.T) {
	e := newEnv(t)
	ctx := e.as(e.admin)
	sub := e.bus.Subscribe(pubsub.SettingsEvents.Name, 4)
	defer sub.Close()

	v, err := e.svc.Get(ctx)
	if err != nil || len(v.CIDRs) != 0 || v.UpdatedAt != nil || !slices.Equal(v.ConfigCIDRs, []string{"10.9.0.0/16"}) {
		t.Fatalf("initial view %+v %v", v, err)
	}
	if allowed(e.guard, "10.20.0.5") || !allowed(e.guard, "10.9.1.1") {
		t.Fatal("unexpected initial guard state")
	}

	// Widening: stored, applied, audited, alerted and announced.
	v, err = e.svc.Update(ctx, Update{CIDRs: Values([]string{"10.20.0.0/16"}), Hosts: Values([]string{"MinIO.Lan"})})
	if err != nil || !slices.Equal(vals(v.CIDRs), []string{"10.20.0.0/16"}) || !slices.Equal(vals(v.Hosts), []string{"minio.lan"}) ||
		v.UpdatedAt == nil || v.UpdatedBy == nil || *v.UpdatedBy != e.admin.UserID {
		t.Fatalf("update: %+v %v", v, err)
	}
	if !allowed(e.guard, "10.20.0.5") {
		t.Fatal("the new CIDR was not applied")
	}
	// Entries without scopes never reach personal webhooks.
	if allowedIn(e.guard, netguard.ScopePersonalWebhooks, "10.20.0.5") || v.PersonalWebhooks {
		t.Fatal("a default entry widened personal webhooks")
	}
	select {
	case m := <-sub.C():
		if m.Payload != SettingKey {
			t.Fatalf("payload %q", m.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("no settings event")
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'setting.changed' AND target_id = $1
		AND details->'added_cidrs' = '["10.20.0.0/16"]'::jsonb AND (details->>'widened')::boolean`, SettingKey); n != 1 {
		t.Fatalf("audit entries: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM notification_events WHERE type = 'security.system' AND detail->>'kind' = $1`,
		notify.KindSSRFAllowlistWidened); n != 1 {
		t.Fatalf("security alerts: %d", n)
	}

	// A stale expectedUpdatedAt is a conflict; nothing changes.
	old := v.UpdatedAt.Add(-time.Minute)
	if _, err := e.svc.Update(ctx, Update{ExpectedUpdatedAt: &old}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update: %v", err)
	}
	if _, err := e.svc.Update(ctx, Update{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("update without expectedUpdatedAt over a stored list: %v", err)
	}

	// Narrowing: audited, no new alert.
	v, err = e.svc.Update(ctx, Update{Hosts: Values([]string{"minio.lan"}), ExpectedUpdatedAt: v.UpdatedAt})
	if err != nil || len(v.CIDRs) != 0 {
		t.Fatalf("narrowing: %+v %v", v, err)
	}
	if allowed(e.guard, "10.20.0.5") || !allowed(e.guard, "10.9.1.1") {
		t.Fatal("the removed CIDR is still allowed or the bootstrap list was lost")
	}
	if n := e.count(`SELECT count(*) FROM notification_events WHERE type = 'security.system' AND detail->>'kind' = $1`,
		notify.KindSSRFAllowlistWidened); n != 1 {
		t.Fatalf("security alerts after narrowing: %d", n)
	}

	// Invalid input names the entry; nothing is stored.
	_, err = e.svc.Update(ctx, Update{CIDRs: Values([]string{"10.0.0.0/8", "169.254.0.0/16"}), ExpectedUpdatedAt: v.UpdatedAt})
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Field != "cidrs.1" {
		t.Fatalf("invalid update: %v", err)
	}
}

func TestUpdateAuthorization(t *testing.T) {
	e := newEnv(t)
	for name, p := range map[string]auth.Principal{
		"maintainer":  authztest.Principal(auth.RoleMaintainer),
		"stale admin": authztest.Stale(e.admin),
	} {
		_, err := e.svc.Update(e.as(p), Update{CIDRs: Values([]string{"10.20.0.0/16"})})
		if !apierror.IsCode(err, apierror.Forbidden) && !apierror.IsCode(err, apierror.StepUpRequired) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if allowed(e.guard, "10.20.0.5") {
		t.Fatal("an unauthorized update was applied")
	}
}

// TestSyncAcrossInstances: a second process (its own guard) applies the
// change when the settings event arrives, and ignores entries it rejects.
func TestSyncAcrossInstances(t *testing.T) {
	e := newEnv(t)
	other, err := netguard.NewSet(netguard.Options{})
	if err != nil {
		t.Fatal(err)
	}
	otherSvc := e.service(other)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- NewSync(otherSvc, e.bus, time.Hour).Run(ctx) }()
	// Wait until the sync subscribed.
	for e.bus.Subscribers(pubsub.SettingsEvents.Name) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := e.svc.Update(e.as(e.admin), Update{CIDRs: Values([]string{"10.30.0.0/16"})}); err != nil {
		t.Fatal(err)
	}
	// The bus message may arrive before the commit: the sync re-reads on
	// every message, so publish the post-commit notification again as the
	// listener would.
	e.bus.Publish(pubsub.SettingsEvents.Name, SettingKey)
	deadline := time.Now().Add(5 * time.Second)
	for !allowed(other, "10.30.0.5") {
		if time.Now().After(deadline) {
			t.Fatal("the other instance did not apply the change")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	// A stored value this process cannot decode applies nothing (fail
	// closed) and is reported as ignored.
	if _, err := e.pool.Exec(t.Context(), `UPDATE settings SET value = '{"version": 9}' WHERE key = $1`, SettingKey); err != nil {
		t.Fatal(err)
	}
	if err := otherSvc.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if allowed(other, "10.30.0.5") {
		t.Fatal("an undecodable stored list kept the old entries")
	}
	if v, err := otherSvc.Get(t.Context()); err != nil || v.Ignored != 1 || len(v.CIDRs) != 0 {
		t.Fatalf("view of an undecodable list: %+v %v", v, err)
	}
}

// TestPersonalWebhookScope (M5 review): only an entry an admin scopes to
// personal webhooks widens them; it is audited as such and the alert says
// so; saving plain values keeps stored scopes; a stored version-1 list
// (written before scopes existed) never reaches personal webhooks.
func TestPersonalWebhookScope(t *testing.T) {
	e := newEnv(t)
	ctx := e.as(e.admin)
	personal := []netguard.Scope{netguard.ScopePersonalWebhooks}
	v, err := e.svc.Update(ctx, Update{CIDRs: []netguard.Entry{
		{Value: "10.40.0.0/16", Scopes: personal},
		{Value: "10.41.0.0/16"},
	}})
	if err != nil || !v.PersonalWebhooks {
		t.Fatalf("update: %+v %v", v, err)
	}
	if !allowedIn(e.guard, netguard.ScopePersonalWebhooks, "10.40.0.5") || allowed(e.guard, "10.40.0.5") ||
		allowedIn(e.guard, netguard.ScopePersonalWebhooks, "10.41.0.5") || !allowed(e.guard, "10.41.0.5") {
		t.Fatal("scopes not applied per guard")
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'setting.changed'
		AND (details->>'personal_webhooks_widened')::boolean`); n != 1 {
		t.Fatalf("personal widening audited %d times", n)
	}
	if n := e.count(`SELECT count(*) FROM notification_events WHERE detail->>'kind' = $1
		AND (detail->>'personal_webhooks')::boolean`, notify.KindSSRFAllowlistWidened); n != 1 {
		t.Fatalf("personal widening alerts: %d", n)
	}
	// Plain values keep their stored scopes (no silent change).
	v, err = e.svc.Update(ctx, Update{CIDRs: Values([]string{"10.40.0.0/16", "10.41.0.0/16"}), ExpectedUpdatedAt: v.UpdatedAt})
	if err != nil || !slices.Equal(v.CIDRs[0].Scopes, personal) || !slices.Equal(v.CIDRs[1].Scopes, netguard.DefaultScopes()) {
		t.Fatalf("plain re-save: %+v %v", v, err)
	}
	// Adding a scope to a kept entry is a widening.
	_, err = e.svc.Update(ctx, Update{CIDRs: []netguard.Entry{
		{Value: "10.40.0.0/16", Scopes: personal},
		{Value: "10.41.0.0/16", Scopes: netguard.AllScopes()},
	}, ExpectedUpdatedAt: v.UpdatedAt})
	if err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'setting.changed' AND (details->>'widened')::boolean
		AND details->'added_scopes' @> '[{"value":"10.41.0.0/16"}]'::jsonb`); n != 1 {
		t.Fatalf("scope widening audited %d times", n)
	}
	// A version-1 list (pre-scopes) is read with the default scopes.
	if _, err := e.pool.Exec(t.Context(), `UPDATE settings SET value = '{"version":1,"cidrs":["10.42.0.0/16"],"hosts":[]}' WHERE key = $1`, SettingKey); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !allowed(e.guard, "10.42.0.5") || allowedIn(e.guard, netguard.ScopePersonalWebhooks, "10.42.0.5") || allowedIn(e.guard, netguard.ScopePersonalWebhooks, "10.40.0.5") {
		t.Fatal("version-1 list scopes")
	}
}

// TestSetConfigList: a config reload replaces the bootstrap list the
// admin page shows (D-200); the returned view never aliases it.
func TestSetConfigList(t *testing.T) {
	e := newEnv(t)
	ctx := e.as(e.admin)
	e.svc.SetConfigList([]string{"10.11.0.0/16"}, []string{"boot.internal"})
	v, err := e.svc.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(v.ConfigCIDRs, []string{"10.11.0.0/16"}) || !slices.Equal(v.ConfigHosts, []string{"boot.internal"}) {
		t.Fatalf("view after SetConfigList: %+v", v)
	}
	v.ConfigCIDRs[0] = "mutated"
	if v2, err := e.svc.Get(ctx); err != nil || v2.ConfigCIDRs[0] != "10.11.0.0/16" {
		t.Fatalf("view aliases the stored list: %+v %v", v2, err)
	}
}
