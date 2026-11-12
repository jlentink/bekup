//go:build integration

package connections

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/authz/authztest"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/riskaccept"
)

// TestRiskEnderOnConnectionChanges: with the real risk acceptance service
// as RiskEnder (D-228), a TLS mode change ends the connection's TLS
// acceptance in the change's transaction, so changing it back is flagged
// again; a rename keeps it; an endpoint change ends the TLS and the
// privileged-role acceptance alike.
func TestRiskEnderOnConnectionChanges(t *testing.T) {
	e := newSvcEnv(t)
	ctx := t.Context()
	auditor := audit.New(e.pool, audit.Options{Registry: logging.NewRegistry()})
	risks, err := riskaccept.New(riskaccept.Options{
		DB: e.pool, Auditor: auditor, Authorizer: authz.New(authz.Options{Ownership: &authztest.Ownership{}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(Options{
		DB: e.pool, Keyring: testKeyring(t), Auditor: e.auditor, Runner: e.runner,
		Limiter:   func(string) (bool, time.Duration) { return true, 0 },
		Alerts:    func(context.Context, Alert) {},
		RiskEnder: risks.EndForConnectionTx,
	})
	if err != nil {
		t.Fatal(err)
	}
	admin := auth.WithPrincipal(ctx, auth.Principal{
		UserID: e.actor.UserID, SessionID: uuid.New(), Role: auth.RoleAdmin, AAL: auth.AAL1, StepUpAt: time.Now(),
	})
	c, err := svc.Create(ctx, e.actor, CreateInput{
		Name: "Legacy", Engine: engine.KindMariaDB, VersionKey: "10.11", Host: "db.example.com", Username: "backup", TLSMode: TLSDisabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	finding := func(kind riskaccept.Kind) *riskaccept.Finding {
		t.Helper()
		fs, err := risks.Findings(admin)
		if err != nil {
			t.Fatal(err)
		}
		for i := range fs {
			if fs[i].Condition.Kind == kind && fs[i].Condition.ConnectionID == c.ID {
				return &fs[i]
			}
		}
		return nil
	}
	accept := func(kind riskaccept.Kind) riskaccept.Acceptance {
		t.Helper()
		f := finding(kind)
		if f == nil {
			t.Fatalf("no %s finding", kind)
		}
		a, err := risks.Accept(admin, riskaccept.AcceptInput{
			Kind: kind, ConnectionID: c.ID, ConditionID: f.Condition.ID(), Duration: riskaccept.DurationYear, Reason: "legacy server",
		})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	endReason := func(id uuid.UUID) string {
		t.Helper()
		var r *string
		if err := e.pool.QueryRow(ctx, "SELECT end_reason::text FROM risk_acceptances WHERE id = $1", id).Scan(&r); err != nil {
			t.Fatal(err)
		}
		if r == nil {
			return ""
		}
		return *r
	}

	// A TLS mode change and back: the old acceptance does not cover again.
	tls := accept(riskaccept.KindConnectionTLS)
	if _, err := svc.SetTLSMode(ctx, e.actor, c.ID, TLSUnverified); err != nil {
		t.Fatal(err)
	}
	if got := endReason(tls.ID); got != "condition_changed" {
		t.Fatalf("after the TLS change: end reason %q", got)
	}
	if _, err := svc.SetTLSMode(ctx, e.actor, c.ID, TLSDisabled); err != nil {
		t.Fatal(err)
	}
	if f := finding(riskaccept.KindConnectionTLS); f == nil || f.Acceptance != nil {
		t.Fatalf("changed back: %+v", f)
	}

	// A rename keeps the acceptance; an endpoint change ends both kinds.
	tls = accept(riskaccept.KindConnectionTLS)
	if _, err := e.pool.Exec(ctx, `UPDATE connections SET last_tested_at = now(), last_test_ok = true,
		last_test_settings_at = updated_at, last_test_privileges = 'privileged', last_test_privilege_reasons = '{superuser}'
		WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}
	priv := accept(riskaccept.KindPrivilegedRole)
	if _, err := svc.Update(ctx, e.actor, c.ID, UpdateInput{Name: ptr("Legacy 2")}); err != nil {
		t.Fatal(err)
	}
	if endReason(tls.ID) != "" || endReason(priv.ID) != "" {
		t.Fatal("a rename ended an acceptance")
	}
	if _, err := svc.Update(ctx, e.actor, c.ID, UpdateInput{Port: ptr(3307)}); err != nil {
		t.Fatal(err)
	}
	if endReason(tls.ID) != "condition_changed" || endReason(priv.ID) != "condition_changed" {
		t.Fatalf("after the port change: %q %q", endReason(tls.ID), endReason(priv.ID))
	}
	var audited int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'risk.acceptance_ended'
		AND details->>'end_reason' = 'condition_changed'`).Scan(&audited); err != nil || audited != 3 {
		t.Fatalf("%d ended audits (%v)", audited, err)
	}
}
