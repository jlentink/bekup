//go:build integration

package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/logging"
)

type adminEnv struct {
	*env
	logs   *bytes.Buffer
	users  *UserAdmin
	resets *recFactorResets
}

// recFactorResets records FactorResetNotifier events.
type recFactorResets struct {
	mu  sync.Mutex
	evs []FactorResetEvent
}

func (r *recFactorResets) SecondFactorsReset(_ context.Context, ev FactorResetEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evs = append(r.evs, ev)
}

func (r *recFactorResets) take() []FactorResetEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	evs := r.evs
	r.evs = nil
	return evs
}

func newAdminEnv(t *testing.T) *adminEnv {
	t.Helper()
	e := &adminEnv{env: newEnv(t), logs: &bytes.Buffer{}, resets: &recFactorResets{}}
	logger, err := logging.New(e.logs, logging.Options{Level: "info", Format: "json"})
	if err != nil {
		t.Fatal(err)
	}
	e.users, err = NewUserAdmin(UserAdminOptions{
		DB: e.pool, Hasher: e.hasher, Sessions: e.sessions, Auditor: e.auditor, Policy: NewPasswordPolicy(12, 3),
		Logger: logger, Now: e.clock.Now, FactorReset: e.resets,
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// serve warns when no active admin exists (D-113): disabled and deleted
// admins do not count.
func TestActiveAdminExists(t *testing.T) {
	e := newAdminEnv(t)
	ctx := t.Context()
	check := func(want bool) {
		t.Helper()
		if got, err := e.users.ActiveAdminExists(ctx); err != nil || got != want {
			t.Fatalf("ActiveAdminExists = %v %v, want %v", got, err, want)
		}
	}
	check(false)
	e.user("plain@example.com", sqlc.UserRoleUser)
	check(false)
	a, _, err := e.users.Create(ctx, OperatorActor(), NewUser{Email: "ops@example.com", DisplayName: "Ops", Role: RoleAdmin, Password: goodPassword})
	if err != nil {
		t.Fatal(err)
	}
	check(true)
	if ents := e.entries(audit.ActionCLIAdminCreated); len(ents) != 1 || ents[0].ActorKind != audit.ActorCLI {
		t.Fatalf("cli entries: %+v", ents)
	}
	if err := e.d.SuperExec(ctx, "UPDATE users SET disabled = true WHERE id = '"+a.ID.String()+"'"); err != nil {
		t.Fatal(err)
	}
	check(false)
}

// Lost-factor recovery (task 1.9): every second factor goes, sessions are
// revoked, a new grace period starts at mfa_reset_at.
func TestResetSecondFactors(t *testing.T) {
	e := newAdminEnv(t)
	ctx := t.Context()
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	u := e.user("lost@example.com", sqlc.UserRoleUser)
	login := e.mustLogin(u.Email)
	sid := login.Issued.Session.ID
	id := "'" + u.ID.String() + "'"
	for _, q := range []string{
		"UPDATE users SET totp_secret_enc = '\\x01', totp_enabled_at = now(), totp_last_step = 7, mfa_failed_count = 2, mfa_locked_until = now() + interval '1 hour' WHERE id = " + id,
		"INSERT INTO recovery_codes (user_id, key_id, code_mac) VALUES (" + id + ", '0000000a', decode(repeat('aa', 32), 'hex')), (" + id + ", '0000000a', decode(repeat('bb', 32), 'hex'))",
		"INSERT INTO webauthn_credentials (user_id, credential_id, public_key, name) VALUES (" + id + ", decode(repeat('cc', 16), 'hex'), '\\x01', 'key')",
		"INSERT INTO webauthn_ceremonies (session_id, user_id, kind, data, expires_at) VALUES ('" + sid.String() + "', " + id + ", 'registration', '{}', now() + interval '5 minutes')",
	} {
		if err := e.d.SuperExec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	self := Actor{UserID: u.ID}
	if _, err := e.users.ResetSecondFactors(ctx, self, u.ID); !errors.Is(err, ErrSelfAction) {
		t.Fatalf("self reset: %v", err)
	}
	if _, err := e.users.ResetSecondFactors(ctx, Actor{UserID: admin.ID}, uuid.New()); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
	res, err := e.users.ResetSecondFactors(ctx, Actor{UserID: admin.ID, IP: testMeta.IP}, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !res.HadTOTP || res.Passkeys != 1 || res.RecoveryCodes != 2 || res.RevokedSessions != 1 {
		t.Fatalf("result %+v", res)
	}
	got := e.dbUser(u.ID)
	if got.TotpSecretEnc != nil || got.TotpEnabledAt != nil || got.TotpLastStep != nil || got.MfaFailedCount != 0 ||
		got.MfaLockedUntil != nil || got.MfaResetAt == nil || time.Since(*got.MfaResetAt).Abs() > time.Minute {
		t.Fatalf("user after reset %+v", got)
	}
	var left int
	if err := e.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM recovery_codes WHERE user_id = $1)
		+ (SELECT count(*) FROM webauthn_credentials WHERE user_id = $1)
		+ (SELECT count(*) FROM webauthn_ceremonies WHERE user_id = $1)`, u.ID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("rows left %d %v", left, err)
	}
	if _, err := e.resolve(login.Issued.Token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("old session: %v", err)
	}
	ents := e.entries(audit.ActionUserSecondFactorsReset)
	if len(ents) != 1 || ents[0].ActorKind != audit.ActorUser || !strings.Contains(string(ents[0].Details), `"recovery_codes": 2`) {
		t.Fatalf("audit %+v", ents)
	}
	if evs := e.resets.take(); len(evs) != 1 || evs[0].UserID != u.ID || evs[0].ActorID != admin.ID || evs[0].CLI {
		t.Fatalf("notifier %+v", evs)
	}
	if !strings.Contains(e.logs.String(), "all second factors of an account were reset") {
		t.Fatal("no warning logged")
	}

	// The grace period restarts at a reset when the policy requires a
	// factor for the role (the account and the requirement are older than
	// the grace period). A CLI reset is audited as a CLI event.
	pol, err := NewTwoFactorPolicies(TwoFactorPoliciesOptions{DB: e.pool, Auditor: e.auditor, Factors: staticFactors{}, Now: e.clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	actor := Principal{UserID: admin.ID, SessionID: uuid.New(), Role: RoleAdmin, AAL: AAL2, StepUpAt: e.clock.Now()}
	if _, err := pol.Update(ctx, actor, TwoFactorPolicyUpdate{Roles: []Role{RoleUser}, GraceDays: 2}); err != nil {
		t.Fatal(err)
	}
	if err := e.d.SuperExec(ctx, "UPDATE users SET created_at = now() - interval '30 days' WHERE id = "+id); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(72 * time.Hour)
	if st, err := pol.Status(ctx, u.ID); err != nil || !st.Restricted {
		t.Fatalf("status before the second reset %+v %v", st, err)
	}
	if _, err := e.users.ResetSecondFactors(ctx, OperatorActor(), u.ID); err != nil {
		t.Fatal(err)
	}
	if ents := e.entries(audit.ActionCLIAdminMFAReset); len(ents) != 1 || ents[0].ActorKind != audit.ActorCLI {
		t.Fatalf("cli audit %+v", ents)
	}
	if evs := e.resets.take(); len(evs) != 1 || !evs[0].CLI {
		t.Fatalf("cli notifier %+v", evs)
	}
	e.clock.Advance(-72 * time.Hour)
	reset := e.dbUser(u.ID).MfaResetAt
	st, err := pol.Status(ctx, u.ID)
	if err != nil || reset == nil || !st.Required || st.Restricted || st.Deadline == nil || st.Deadline.Sub(reset.Add(48*time.Hour)).Abs() > 2*time.Second {
		t.Fatalf("status after reset %+v %v (reset %v)", st, err, reset)
	}
	// With a grace period of 0 the account is restricted at once.
	actor.StepUpAt = e.clock.Now()
	if _, err := pol.Update(ctx, actor, TwoFactorPolicyUpdate{Roles: []Role{RoleUser}, GraceDays: 0}); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(2 * time.Second)
	if st, err := pol.Status(ctx, u.ID); err != nil || !st.Restricted {
		t.Fatalf("status with zero grace %+v %v", st, err)
	}
}

func TestTemporaryPasswordFlow(t *testing.T) {
	e := newAdminEnv(t)
	ctx := t.Context()
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	actor := Actor{UserID: admin.ID, IP: testMeta.IP}
	u, temp, err := e.users.Create(ctx, actor, NewUser{Email: "new@example.com", DisplayName: "New User", Role: RoleUser})
	if err != nil || temp == nil || len(temp.Password) != 39 {
		t.Fatalf("create: %+v %v", temp, err)
	}
	if u.PasswordTemporaryUntil == nil || time.Until(*u.PasswordTemporaryUntil) < TemporaryPasswordTTL-time.Minute {
		t.Fatalf("temporary until: %v", u.PasswordTemporaryUntil)
	}
	if strings.Contains(*u.PasswordHash, temp.Password) {
		t.Fatal("plain password stored")
	}
	ents := e.entries(audit.ActionUserCreated)
	if len(ents) != 1 || strings.Contains(string(ents[0].Details), temp.Password) {
		t.Fatalf("created entries: %+v", ents)
	}
	if _, _, err := e.users.Create(ctx, actor, NewUser{Email: "NEW@example.com", DisplayName: "Dup", Role: RoleUser}); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("duplicate: %v", err)
	}

	res, err := e.login("new@example.com", temp.Password)
	if err != nil || !res.PasswordChangeRequired {
		t.Fatalf("temp login: %+v %v", res, err)
	}
	p, err := e.resolve(res.Issued.Token)
	if err != nil || !p.PasswordChangeRequired {
		t.Fatalf("principal: %+v %v", p, err)
	}
	issued, err := e.svc.ChangePassword(ctx, p, temp.Password, "copper-lantern-orbit-73", testMeta)
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if p, err = e.resolve(issued.Token); err != nil || p.PasswordChangeRequired {
		t.Fatalf("after change: %+v %v", p, err)
	}

	// An expired temporary password cannot sign in.
	temp2, _, err := e.users.ResetPassword(ctx, actor, u.ID, "")
	if err != nil || temp2 == nil {
		t.Fatal(err)
	}
	e.clock.Advance(TemporaryPasswordTTL + time.Minute)
	if _, err := e.login("new@example.com", temp2.Password); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expired temp: %v", err)
	}
	last := e.entries(audit.ActionAuthLoginFailed)
	if len(last) == 0 || !strings.Contains(string(last[0].Details), reasonTemporaryExpired) {
		t.Fatalf("failure reason: %+v", last)
	}
}

func TestLastAdminInvariant(t *testing.T) {
	e := newAdminEnv(t)
	ctx := t.Context()
	a1 := e.user("a1@example.com", sqlc.UserRoleAdmin)
	self := Actor{UserID: a1.ID}
	user := RoleUser
	if _, err := e.users.Update(ctx, self, a1.ID, UserChanges{Role: &user}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demote last: %v", err)
	}
	if _, err := e.users.SetDisabled(ctx, OperatorActor(), a1.ID, true); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("disable last: %v", err)
	}
	if err := e.users.Delete(ctx, OperatorActor(), a1.ID); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("delete last: %v", err)
	}
	if err := e.users.Delete(ctx, self, a1.ID); !errors.Is(err, ErrSelfAction) {
		t.Fatalf("delete self: %v", err)
	}
	// A disabled admin does not count.
	a2 := e.user("a2@example.com", sqlc.UserRoleAdmin)
	if _, err := e.users.SetDisabled(ctx, self, a2.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.users.Update(ctx, self, a1.ID, UserChanges{Role: &user}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demote with disabled peer: %v", err)
	}
	if _, err := e.users.SetDisabled(ctx, self, a2.ID, false); err != nil {
		t.Fatal(err)
	}

	// Two admins demoting each other concurrently: exactly one wins.
	for round := range 5 {
		var wg sync.WaitGroup
		var ok, last atomic.Int32
		for _, pair := range [][2]uuid.UUID{{a1.ID, a2.ID}, {a2.ID, a1.ID}} {
			wg.Go(func() {
				_, err := e.users.Update(ctx, Actor{UserID: pair[0]}, pair[1], UserChanges{Role: &user})
				switch {
				case err == nil:
					ok.Add(1)
				case errors.Is(err, ErrLastAdmin):
					last.Add(1)
				default:
					t.Errorf("round %d: %v", round, err)
				}
			})
		}
		wg.Wait()
		if ok.Load() != 1 || last.Load() != 1 {
			t.Fatalf("round %d: ok %d last %d", round, ok.Load(), last.Load())
		}
		var active int
		if err := e.pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE role = 'admin' AND NOT disabled").Scan(&active); err != nil || active != 1 {
			t.Fatalf("active admins %d %v", active, err)
		}
		if err := e.d.SuperExec(ctx, "UPDATE users SET role = 'admin'"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUpdateRevokesSessionsAndAudits(t *testing.T) {
	e := newAdminEnv(t)
	ctx := t.Context()
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	target := e.user("pat@example.com", sqlc.UserRoleUser)
	tok := e.mustLogin("pat@example.com").Issued.Token
	actor := Actor{UserID: admin.ID, IP: testMeta.IP}

	name := "Pat Renamed"
	res, err := e.users.Update(ctx, actor, target.ID, UserChanges{DisplayName: &name})
	if err != nil || res.RevokedSessions != 0 || res.User.DisplayName != name {
		t.Fatalf("rename: %+v %v", res, err)
	}
	if _, err := e.resolve(tok); err != nil {
		t.Fatalf("rename revoked the session: %v", err)
	}
	maint := RoleMaintainer
	res, err = e.users.Update(ctx, actor, target.ID, UserChanges{Role: &maint})
	if err != nil || res.RevokedSessions != 1 || res.User.Role != sqlc.UserRoleMaintainer {
		t.Fatalf("role change: %+v %v", res, err)
	}
	if _, err := e.resolve(tok); !errors.Is(err, ErrNoSession) {
		t.Fatalf("session survived role change: %v", err)
	}
	rc := e.entries(audit.ActionUserRoleChanged)
	if len(rc) != 1 || !strings.Contains(string(rc[0].Details), `"previous_role": "user"`) && !strings.Contains(string(rc[0].Details), `"previous_role":"user"`) {
		t.Fatalf("role entries: %+v", rc)
	}

	// Lockout, unlock, reset.
	for range 3 {
		if _, err := e.login("pat@example.com", "wrong-password-123"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatal(err)
		}
	}
	if u, err := e.users.Get(ctx, target.ID); err != nil || u.LockedUntil == nil {
		t.Fatal("not locked")
	}
	if u, err := e.users.Unlock(ctx, OperatorActor(), target.ID); err != nil || u.LockedUntil != nil || u.FailedLoginCount != 0 {
		t.Fatalf("unlock: %+v %v", u, err)
	}
	tok = e.mustLogin("pat@example.com").Issued.Token
	if _, _, err := e.users.ResetPassword(ctx, actor, admin.ID, ""); !errors.Is(err, ErrSelfAction) {
		t.Fatalf("self reset: %v", err)
	}
	if _, _, err := e.users.ResetPassword(ctx, OperatorActor(), target.ID, "short"); err == nil {
		t.Fatal("weak CLI password accepted")
	}
	temp, revoked, err := e.users.ResetPassword(ctx, actor, target.ID, "")
	if err != nil || revoked != 1 || temp == nil {
		t.Fatalf("reset: %v %d", err, revoked)
	}
	if _, err := e.resolve(tok); !errors.Is(err, ErrNoSession) {
		t.Fatal("session survived reset")
	}

	// CLI events carry actor kind cli, OS user and hostname.
	ul := e.entries(audit.ActionCLIAdminUnlocked)
	if len(ul) != 1 || ul[0].ActorKind != audit.ActorCLI || !strings.Contains(string(ul[0].Details), audit.DetailHostname) {
		t.Fatalf("cli entries: %+v", ul)
	}
	if n := len(e.entries(audit.ActionUserPasswordReset)); n != 1 {
		t.Fatalf("reset entries: %d", n)
	}
}

func TestSoftDelete(t *testing.T) {
	e := newAdminEnv(t)
	ctx := t.Context()
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	target := e.user("gone@example.com", sqlc.UserRoleMaintainer)
	tok := e.mustLogin("gone@example.com").Issued.Token
	actor := Actor{UserID: admin.ID}
	if err := e.users.Delete(ctx, actor, target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.resolve(tok); !errors.Is(err, ErrNoSession) {
		t.Fatal("session survived delete")
	}
	u, err := e.users.Get(ctx, target.ID)
	if err != nil || u.DeletedAt == nil || !u.Disabled || u.PasswordHash != nil {
		t.Fatalf("deleted row: %+v %v", u, err)
	}
	if _, err := e.login("gone@example.com", goodPassword); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("login deleted: %v", err)
	}
	if err := e.users.Delete(ctx, actor, target.ID); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
	if _, err := e.users.SetDisabled(ctx, actor, target.ID, false); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("enable deleted: %v", err)
	}
	if _, err := e.users.ByEmail(ctx, "gone@example.com"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("by email: %v", err)
	}
	// The address can be reused.
	again, _, err := e.users.Create(ctx, actor, NewUser{Email: "gone@example.com", DisplayName: "Again", Role: RoleUser})
	if err != nil || again.ID == target.ID {
		t.Fatalf("reuse: %v", err)
	}
	if n := len(e.entries(audit.ActionUserDeleted)); n != 1 {
		t.Fatalf("deleted entries: %d", n)
	}
}

func TestListUsers(t *testing.T) {
	e := newAdminEnv(t)
	ctx := t.Context()
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	for i := range 7 {
		e.user(fmt.Sprintf("u%02d@example.com", i), sqlc.UserRoleUser)
	}
	gone := e.user("zz_gone@example.com", sqlc.UserRoleUser)
	if err := e.users.Delete(ctx, Actor{UserID: admin.ID}, gone.ID); err != nil {
		t.Fatal(err)
	}
	var all []string
	var cur *UserCursor
	for {
		rows, more, err := e.users.List(ctx, UserFilter{}, cur, 3)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			all = append(all, r.Email)
		}
		if !more {
			break
		}
		last := rows[len(rows)-1]
		cur = &UserCursor{Email: last.Email, ID: last.ID}
	}
	if len(all) != 8 || all[0] != "admin@example.com" || all[7] != "u06@example.com" {
		t.Fatalf("pages: %v", all)
	}
	rows, _, err := e.users.List(ctx, UserFilter{IncludeDeleted: true, Search: "gone"}, nil, 10)
	if err != nil || len(rows) != 1 || rows[0].ID != gone.ID {
		t.Fatalf("deleted search: %v %v", rows, err)
	}
	rows, _, err = e.users.List(ctx, UserFilter{Role: RoleAdmin}, nil, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("role filter: %v %v", rows, err)
	}
	// LIKE wildcards are literal.
	rows, _, err = e.users.List(ctx, UserFilter{Search: "%"}, nil, 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("wildcard: %v %v", rows, err)
	}
	if _, _, err := e.users.List(ctx, UserFilter{}, nil, MaxUserPage+1); err == nil {
		t.Fatal("page size")
	}
}
