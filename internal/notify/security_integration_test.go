//go:build integration

package notify

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
)

// TestLoginBurstPerAccount: failed sign-ins on one existing account from
// changing addresses raise one security.system alert per account and
// window (subject: the account; no address), and none below the threshold.
func TestLoginBurstPerAccount(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.user("admin", true)
	victim := e.user("user", true)
	a := audit.New(e.pool, audit.Options{Observer: ObserveAudit})
	fail := func(i byte) {
		t.Helper()
		ip := netip.AddrFrom4([4]byte{192, 0, 2, i + 1})
		if _, err := a.Record(t.Context(), audit.Event{
			Actor: audit.SystemActor(), Action: audit.ActionAuthLoginFailed, IP: ip,
			TargetType: "user", TargetID: victim.String(), Details: audit.Details{"reason": "bad_password"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	burst := func() int {
		return e.count(`SELECT count(*) FROM notification_events WHERE type = 'security.system'
			AND detail->>'kind' = $1 AND user_id = $2`, KindLoginBurstAccount, victim)
	}
	for i := range byte(BurstPerAccount - 1) {
		fail(i)
	}
	if burst() != 0 {
		t.Fatal("alert below the threshold")
	}
	for i := range byte(5) {
		fail(BurstPerAccount + i)
	}
	if n := burst(); n < 1 || n > 2 {
		t.Fatalf("%d per-account burst alerts", n)
	}
	if e.count(`SELECT count(*) FROM notification_events WHERE detail->>'kind' = $1`, KindLoginBurstAddress) != 0 {
		t.Fatal("per-address alert from changing addresses")
	}
	// The alert reaches the admins only, also with every preference off.
	e.pref(admin, SecuritySystem, nil, ChannelInbox, false)
	e.pref(admin, SecuritySystem, nil, ChannelEmail, false)
	var id uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `SELECT id FROM notification_events WHERE detail->>'kind' = $1 LIMIT 1`, KindLoginBurstAccount).Scan(&id); err != nil {
		t.Fatal(err)
	}
	e.fanOut(id)
	if e.inbox(admin) != 1 || e.deliveries(admin, ChannelEmail, "queued") != 1 || e.inbox(victim) != 0 {
		t.Fatalf("audience: admin inbox %d, victim inbox %d", e.inbox(admin), e.inbox(victim))
	}
}

// TestLoginFailureCountUsesPartialIndex (M5 review L7): the burst count
// runs for every failed sign-in; it must read the partial index of the
// auth.login.failed entries, not every audit row of the window.
func TestLoginFailureCountUsesPartialIndex(t *testing.T) {
	e := newEnv(t, nil)
	// Many other audit rows in the window, a few failures.
	// (as the superuser, bypassing the append-only chain triggers: the
	// rows only feed the planner's statistics).
	if err := e.d.SuperExec(t.Context(),
		`SET session_replication_role = replica`,
		`INSERT INTO audit_log (id, ts, actor_kind, action, prev_hash, hash)
		 SELECT 1000000 + g, now() - (g * interval '100 milliseconds'), 'system', 'job.updated', sha256(int8send(g)), sha256(int8send(-g))
		 FROM generate_series(1, 5000) g`,
		`ANALYZE audit_log`,
	); err != nil {
		t.Fatal(err)
	}
	var plan []string
	rows, err := e.pool.Query(t.Context(), `EXPLAIN (FORMAT TEXT)
		SELECT count(*) FILTER (WHERE ip = $1::inet)::bigint, count(*) FILTER (WHERE target_type = 'user' AND target_id = $2::text)::bigint, count(*)::bigint
		FROM audit_log WHERE action = 'auth.login.failed' AND ts > $3::timestamptz`,
		"192.0.2.1", uuid.NewString(), time.Now().Add(-BurstWindow))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, l)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan, "\n"), "audit_log_login_failed_ts_idx") {
		t.Fatalf("the failed-login count does not use the partial index:\n%s", strings.Join(plan, "\n"))
	}
}
