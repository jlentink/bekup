//go:build integration

package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// staticFactors reports the same factors for every user.
type staticFactors []Factor

func (s staticFactors) Enrolled(_ context.Context, _ uuid.UUID) ([]Factor, error) { return s, nil }

// staticFactorsRef is a changeable staticFactors.
type staticFactorsRef struct {
	mu sync.Mutex
	fs []Factor
}

func (s *staticFactorsRef) set(fs ...Factor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fs = fs
}

func (s *staticFactorsRef) Enrolled(context.Context, uuid.UUID) ([]Factor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fs, nil
}

// The session invariants of the two-step login (task 1.9).
func TestPendingSessionInvariants(t *testing.T) {
	e := newEnv(t)
	rec := &recEvents{}
	e.sessions.opts.Events = rec
	u := e.user("pending@example.com", sqlc.UserRoleUser)
	ctx := t.Context()
	var pending Issued
	if err := db.Tx(ctx, e.pool, func(q *sqlc.Queries) error {
		var err error
		pending, err = e.sessions.CreatePendingTx(ctx, q, u.ID, testMeta)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	p, err := e.resolve(pending.Token)
	if err != nil || !p.MFAPending || p.AAL != AAL1 || !p.StepUpAt.IsZero() || !p.AAL2At.IsZero() {
		t.Fatalf("pending principal %+v %v", p, err)
	}
	if d := pending.Session.AbsoluteExpiresAt.Sub(pending.Session.CreatedAt); d > PendingSecondFactorTTL+time.Second {
		t.Fatalf("pending lifetime %v", d)
	}
	if err := e.sessions.MarkStepUp(ctx, p.SessionID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("step-up on a pending session: %v", err)
	}
	if _, err := e.sessions.ElevateAAL(ctx, p.SessionID, AAL2); !errors.Is(err, ErrNotPending) {
		t.Fatalf("elevate a pending session: %v", err)
	}
	// A rotation keeps the pending state.
	rotated, err := e.sessions.Rotate(ctx, p.SessionID, testMeta)
	if err != nil || !rotated.Session.MfaPending {
		t.Fatalf("rotate pending: %+v %v", rotated.Session, err)
	}
	rec.take()
	var full Issued
	err = db.Tx(ctx, e.pool, func(q *sqlc.Queries) error {
		var err error
		full, err = e.sessions.CompleteSecondFactorTx(ctx, q, rotated.Session.ID, true, testMeta)
		return err
	})
	if err != nil || full.Session.MfaPending || full.Session.Aal != AAL2 || full.Session.Aal2At == nil || full.Session.StepupAt == nil {
		t.Fatalf("complete: %+v %v", full.Session, err)
	}
	if full.Session.AbsoluteExpiresAt.Sub(full.Session.CreatedAt) < time.Hour {
		t.Fatal("the completed session kept the pending lifetime")
	}
	// The pending session is rotated (open sockets get 4001).
	want := SessionEvent{Kind: SessionRotated, UserID: u.ID, SessionID: rotated.Session.ID}
	if evs := rec.take(); len(evs) != 1 || evs[0] != want {
		t.Fatalf("events %+v", evs)
	}
	if _, err := e.resolve(rotated.Token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("pending token after completion: %v", err)
	}
	err = db.Tx(ctx, e.pool, func(q *sqlc.Queries) error {
		_, err := e.sessions.CompleteSecondFactorTx(ctx, q, full.Session.ID, true, testMeta)
		return err
	})
	if !errors.Is(err, ErrNotPending) {
		t.Fatalf("complete a full session: %v", err)
	}
	// The database refuses a pending session with a higher level.
	if _, err := e.pool.Exec(ctx, "UPDATE sessions SET mfa_pending = true WHERE id = $1", full.Session.ID); err == nil {
		t.Fatal("pending AAL2 session accepted")
	}
}

// A login of an account with a factor is a two-step login without a
// step-up; without a factor it keeps the fresh step-up (D-112).
func TestLoginTwoStepDecision(t *testing.T) {
	e := newEnv(t)
	u := e.user("two@example.com", sqlc.UserRoleUser)
	e.svc.o.Factors = staticFactors{FactorTOTP}
	res := e.mustLogin(u.Email)
	if !res.SecondFactorRequired || len(res.AvailableFactors) != 1 || res.AvailableFactors[0] != FactorTOTP ||
		!res.Issued.Session.MfaPending || res.Issued.Session.StepupAt != nil {
		t.Fatalf("login with a factor: %+v", res)
	}
	e.svc.o.Factors = staticFactors{}
	res = e.mustLogin(u.Email)
	if res.SecondFactorRequired || res.Issued.Session.MfaPending || res.Issued.Session.StepupAt == nil {
		t.Fatalf("login without a factor: %+v", res)
	}
	entries := e.entries(audit.ActionAuthLoginSucceeded)
	if len(entries) != 2 {
		t.Fatalf("%d login audit rows", len(entries))
	}
}

func TestTwoFactorPolicyUpdateAndStatus(t *testing.T) {
	e := newEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	maint := e.user("maint@example.com", sqlc.UserRoleMaintainer)
	factors := &staticFactorsRef{}
	pol, err := NewTwoFactorPolicies(TwoFactorPoliciesOptions{DB: e.pool, Auditor: e.auditor, Factors: factors, Now: e.clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	actor := Principal{UserID: admin.ID, SessionID: uuid.New(), Role: RoleAdmin, AAL: AAL1, StepUpAt: e.clock.Now()}

	if _, err := pol.Update(ctx, Principal{UserID: maint.ID, SessionID: uuid.New(), Role: RoleMaintainer, StepUpAt: time.Now()},
		TwoFactorPolicyUpdate{Roles: []Role{RoleAdmin}}); err == nil {
		t.Fatal("maintainer changed the policy")
	}
	stale := actor
	stale.StepUpAt = e.clock.Now().Add(-time.Hour)
	if _, err := pol.Update(ctx, stale, TwoFactorPolicyUpdate{}); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("stale step-up: %v", err)
	}
	if _, err := pol.Update(ctx, actor, TwoFactorPolicyUpdate{GraceDays: 91}); !errors.Is(err, ErrInvalidTwoFactorPolicy) {
		t.Fatalf("grace 91: %v", err)
	}

	st, err := pol.Status(ctx, maint.ID)
	if err != nil || st.Required {
		t.Fatalf("default status of a maintainer: %+v %v", st, err)
	}
	start := e.clock.Now()
	p, err := pol.Update(ctx, actor, TwoFactorPolicyUpdate{Roles: []Role{RoleAdmin, RoleMaintainer}, GraceDays: 2})
	if err != nil {
		t.Fatal(err)
	}
	since := p.Since[RoleMaintainer]
	if since.Sub(start).Abs() > time.Second {
		t.Fatalf("maintainer requirement started at %v", since)
	}
	// The default requires nothing (D-112), so the admin requirement starts
	// now as well.
	if s := p.Since[RoleAdmin]; s.Sub(start).Abs() > time.Second {
		t.Fatalf("admin requirement started at %v", s)
	}
	st, err = pol.Status(ctx, maint.ID)
	if err != nil || !st.Required || st.Restricted || st.Deadline == nil || st.Deadline.Sub(since.Add(48*time.Hour)).Abs() > time.Second {
		t.Fatalf("status within grace: %+v %v", st, err)
	}
	// Changing the grace period keeps the start.
	e.clock.Advance(time.Hour)
	if p, err = pol.Update(ctx, Principal{UserID: admin.ID, SessionID: uuid.New(), Role: RoleAdmin, StepUpAt: e.clock.Now()},
		TwoFactorPolicyUpdate{Roles: []Role{RoleMaintainer, RoleAdmin}, GraceDays: 1}); err != nil || !p.Since[RoleMaintainer].Equal(since) {
		t.Fatalf("start after a grace change: %v %v", p.Since, err)
	}
	e.clock.Advance(24 * time.Hour)
	if st, err = pol.Status(ctx, maint.ID); err != nil || !st.Restricted {
		t.Fatalf("status after grace: %+v %v", st, err)
	}
	// A factor ends the restriction.
	factors.set(FactorTOTP)
	if st, err = pol.Status(ctx, maint.ID); err != nil || st.Restricted || !st.HasFactor {
		t.Fatalf("status with a factor: %+v %v", st, err)
	}
	// The last-factor guard follows the policy per role.
	if err := pol.CheckFactorRemoval(ctx, maint.ID, FactorTOTP, nil); !errors.Is(err, ErrLastFactor) {
		t.Fatalf("last factor of a required role: %v", err)
	}
	if err := pol.CheckFactorRemoval(ctx, maint.ID, FactorTOTP, []Factor{FactorPasskey}); err != nil {
		t.Fatalf("not the last factor: %v", err)
	}
	user := e.user("user@example.com", sqlc.UserRoleUser)
	if err := pol.CheckFactorRemoval(ctx, user.ID, FactorTOTP, nil); err != nil {
		t.Fatalf("last factor of a user: %v", err)
	}
	if n := len(e.entries(audit.ActionMFAPolicyUpdated)); n != 2 {
		t.Fatalf("%d policy audit rows", n)
	}
	if n := len(e.entries(audit.ActionAuthMFARemovalRefused)); n != 1 {
		t.Fatalf("%d refusal audit rows", n)
	}
}
