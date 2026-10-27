//go:build integration

package passkey

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/auth/passkey/passkeytest"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/logging"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

const testOrigin = "https://backup.example.com"

type recNotifier struct {
	mu     sync.Mutex
	events []Event
}

func (r *recNotifier) PasskeyEvent(_ context.Context, ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recNotifier) kinds() []EventKind {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]EventKind, 0, len(r.events))
	for _, e := range r.events {
		out = append(out, e.Kind)
	}
	return out
}

type guardFunc func(remaining []auth.Factor) error

func (g guardFunc) CheckFactorRemoval(_ context.Context, _ uuid.UUID, _ auth.Factor, remaining []auth.Factor) error {
	return g(remaining)
}

type env struct {
	t        *testing.T
	d        dbtest.DB
	pool     *pgxpool.Pool
	auditor  *audit.Auditor
	sessions *auth.Sessions
	factors  *auth.SecondFactors
	notifier *recNotifier
	guard    guardFunc
	svc      *Service
}

var testMeta = auth.RequestMeta{IP: netip.MustParseAddr("192.0.2.20"), UserAgent: "passkey-test"}

func newEnv(t *testing.T, base string) *env {
	t.Helper()
	d := dbtest.Migrated(t)
	e := &env{t: t, d: d, pool: d.AppPool(t), notifier: &recNotifier{}, factors: auth.NewSecondFactors()}
	e.auditor = audit.New(e.pool, audit.Options{Registry: logging.NewRegistry()})
	e.sessions = auth.NewSessions(e.pool, auth.SessionOptions{})
	svc, err := New(Options{
		DB: e.pool, Auditor: e.auditor, BaseURL: base, Factors: e.factors, Notifier: e.notifier,
		RemovalGuard: guardFunc(func(r []auth.Factor) error {
			if e.guard != nil {
				return e.guard(r)
			}
			return nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	e.factors.Register(svc)
	e.svc = svc
	return e
}

func (e *env) ctx() context.Context { return auth.WithRequestMeta(e.t.Context(), testMeta) }

func (e *env) user(email string) sqlc.User {
	e.t.Helper()
	u, err := sqlc.New(e.pool).InsertUser(e.t.Context(), sqlc.InsertUserParams{Email: email, DisplayName: "Pat", Role: sqlc.UserRoleUser})
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

// session creates a session for u with a fresh step-up and returns its
// principal.
func (e *env) session(u sqlc.User) auth.Principal {
	e.t.Helper()
	var issued auth.Issued
	if err := db.Tx(e.t.Context(), e.pool, func(q *sqlc.Queries) error {
		var err error
		issued, err = e.sessions.CreateTx(e.t.Context(), q, u.ID, auth.AAL1, true, testMeta)
		return err
	}); err != nil {
		e.t.Fatal(err)
	}
	return auth.Principal{UserID: u.ID, Role: auth.RoleUser, SessionID: issued.Session.ID, AAL: 1, StepUpAt: *issued.Session.StepupAt}
}

func (e *env) authenticator(origin string) *passkeytest.Authenticator {
	e.t.Helper()
	a, err := passkeytest.New(origin)
	if err != nil {
		e.t.Fatal(err)
	}
	return a
}

func (e *env) register(p auth.Principal, a *passkeytest.Authenticator, name string) (Passkey, error) {
	e.t.Helper()
	opts, err := e.svc.BeginRegistration(e.ctx(), p)
	if err != nil {
		e.t.Fatalf("begin registration: %v", err)
	}
	resp, err := a.Register(opts)
	if err != nil {
		e.t.Fatal(err)
	}
	return e.svc.FinishRegistration(e.ctx(), p, name, resp)
}

func (e *env) assertion(p auth.Principal, a *passkeytest.Authenticator) ([]byte, error) {
	e.t.Helper()
	opts, err := e.svc.BeginAssertion(e.ctx(), p.SessionID, p.UserID)
	if err != nil {
		e.t.Fatalf("begin assertion: %v", err)
	}
	return a.Assert(opts)
}

func (e *env) auditCount(action audit.Action) int {
	e.t.Helper()
	page, err := e.auditor.List(e.t.Context(), audit.Filter{Action: action}, 0, 200)
	if err != nil {
		e.t.Fatal(err)
	}
	return len(page.Entries)
}

func (e *env) auditDetails(action audit.Action) map[string]any {
	e.t.Helper()
	page, err := e.auditor.List(e.t.Context(), audit.Filter{Action: action}, 0, 1)
	if err != nil || len(page.Entries) == 0 {
		e.t.Fatalf("no %s entry: %v", action, err)
	}
	var d map[string]any
	if err := json.Unmarshal(page.Entries[0].Details, &d); err != nil {
		e.t.Fatal(err)
	}
	return d
}

func TestRegisterAndAssert(t *testing.T) {
	e := newEnv(t, testOrigin)
	u := e.user("pat@example.com")
	p := e.session(u)
	if has, err := e.factors.HasSecondFactor(t.Context(), u.ID); err != nil || has {
		t.Fatalf("factor before enrollment: %v %v", has, err)
	}
	if _, err := e.svc.BeginAssertion(e.ctx(), p.SessionID, u.ID); !errors.Is(err, ErrNoPasskeys) {
		t.Fatalf("assertion without passkeys: %v", err)
	}

	a := e.authenticator(testOrigin)
	pk, err := e.register(p, a, "  YubiKey  ")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if pk.Name != "YubiKey" || !pk.UserVerified || pk.LastUsedAt != nil || len(pk.Transports) != 1 || pk.AAGUID == nil {
		t.Fatalf("passkey %+v", pk)
	}
	if has, err := e.factors.HasSecondFactor(t.Context(), u.ID); err != nil || !has {
		t.Fatalf("factor after enrollment: %v %v", has, err)
	}
	if f, err := e.factors.Enrolled(t.Context(), u.ID); err != nil || len(f) != 1 || f[0] != auth.FactorPasskey {
		t.Fatalf("enrolled %v %v", f, err)
	}
	// Registering the same authenticator again is refused (excluded and
	// unique credential id).
	if _, err := e.register(p, a, "again"); !errors.Is(err, ErrVerification) {
		t.Fatalf("duplicate registration: %v", err)
	}

	resp, err := e.assertion(p, a)
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.FinishAssertion(e.ctx(), p.SessionID, u.ID, resp)
	if err != nil {
		t.Fatalf("finish assertion: %v", err)
	}
	if got.ID != pk.ID || got.LastUsedAt == nil {
		t.Fatalf("asserted passkey %+v", got)
	}
	// Replaying the same response: the challenge was single use.
	if _, err := e.svc.FinishAssertion(e.ctx(), p.SessionID, u.ID, resp); !errors.Is(err, ErrNoCeremony) {
		t.Fatalf("replayed assertion: %v", err)
	}

	list, err := e.svc.List(t.Context(), u.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list %v %v", list, err)
	}
	for action, want := range map[audit.Action]int{
		audit.ActionAuthPasskeyRegistrationBegun:  2,
		audit.ActionAuthMFAEnrolled:               1,
		audit.ActionAuthPasskeyRegistrationFailed: 1,
		audit.ActionAuthPasskeyAssertionSucceeded: 1,
		audit.ActionAuthPasskeyAssertionFailed:    1,
	} {
		if n := e.auditCount(action); n != want {
			t.Errorf("%s: %d entries, want %d", action, n, want)
		}
	}
	if d := e.auditDetails(audit.ActionAuthMFAEnrolled); d["factor"] != "passkey" || d["passkey_id"] != pk.ID.String() {
		t.Fatalf("enrolled details %v", d)
	}
	if d := e.auditDetails(audit.ActionAuthPasskeyAssertionFailed); d["reason"] != "no_ceremony" {
		t.Fatalf("assertion failure details %v", d)
	}
	if k := e.notifier.kinds(); len(k) != 1 || k[0] != EventRegistered {
		t.Fatalf("events %v", k)
	}
}

func TestRegistrationReplayAndSessionBinding(t *testing.T) {
	e := newEnv(t, testOrigin)
	u := e.user("pat@example.com")
	p1, p2 := e.session(u), e.session(u)
	a := e.authenticator(testOrigin)

	opts, err := e.svc.BeginRegistration(e.ctx(), p1)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := a.Register(opts)
	if err != nil {
		t.Fatal(err)
	}
	// Another session of the same user cannot finish it.
	if _, err := e.svc.FinishRegistration(e.ctx(), p2, "k", resp); !errors.Is(err, ErrNoCeremony) {
		t.Fatalf("finish from another session: %v", err)
	}
	if _, err := e.svc.FinishRegistration(e.ctx(), p1, "k", resp); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if _, err := e.svc.FinishRegistration(e.ctx(), p1, "k", resp); !errors.Is(err, ErrNoCeremony) {
		t.Fatalf("replayed registration: %v", err)
	}

	// Assertion begun in p1 cannot be finished in p2 (nor by another user).
	resp, err = e.assertion(p1, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.FinishAssertion(e.ctx(), p2.SessionID, u.ID, resp); !errors.Is(err, ErrNoCeremony) {
		t.Fatalf("assertion from another session: %v", err)
	}
	other := e.user("other@example.com")
	if _, err := e.svc.FinishAssertion(e.ctx(), p1.SessionID, other.ID, resp); !errors.Is(err, ErrNoCeremony) {
		t.Fatalf("assertion for another user: %v", err)
	}
	if _, err := e.svc.FinishAssertion(e.ctx(), p1.SessionID, u.ID, resp); err != nil {
		t.Fatalf("assertion in its own session: %v", err)
	}

	// An expired ceremony is refused.
	resp, err = e.assertion(p1, a)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.d.SuperExec(t.Context(), "UPDATE webauthn_ceremonies SET expires_at = now() - interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.FinishAssertion(e.ctx(), p1.SessionID, u.ID, resp); !errors.Is(err, ErrNoCeremony) {
		t.Fatalf("expired ceremony: %v", err)
	}
}

func TestWrongOriginRPIDAndForgery(t *testing.T) {
	e := newEnv(t, testOrigin)
	u := e.user("pat@example.com")
	p := e.session(u)
	if _, err := e.register(p, e.authenticator("https://evil.example.net"), "evil"); !errors.Is(err, ErrVerification) {
		t.Fatalf("wrong origin registration: %v", err)
	}
	wrongRP := e.authenticator(testOrigin)
	wrongRP.RPID = "example.com"
	if _, err := e.register(p, wrongRP, "rp"); !errors.Is(err, ErrVerification) {
		t.Fatalf("wrong RP ID registration: %v", err)
	}
	if n := e.auditCount(audit.ActionAuthPasskeyRegistrationFailed); n != 2 {
		t.Fatalf("%d registration failures audited", n)
	}

	a := e.authenticator(testOrigin)
	if _, err := e.register(p, a, "good"); err != nil {
		t.Fatal(err)
	}
	a.Origin = "https://evil.example.net"
	resp, err := e.assertion(p, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.FinishAssertion(e.ctx(), p.SessionID, u.ID, resp); !errors.Is(err, ErrVerification) {
		t.Fatalf("wrong origin assertion: %v", err)
	}
	a.Origin = testOrigin
	a.RPID = "evil.example.net"
	if resp, err = e.assertion(p, a); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.FinishAssertion(e.ctx(), p.SessionID, u.ID, resp); !errors.Is(err, ErrVerification) {
		t.Fatalf("wrong RP ID assertion: %v", err)
	}
	// A different authenticator (not registered) is refused.
	stranger := e.authenticator(testOrigin)
	if resp, err = e.assertion(p, stranger); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.FinishAssertion(e.ctx(), p.SessionID, u.ID, resp); !errors.Is(err, ErrVerification) {
		t.Fatalf("unregistered authenticator: %v", err)
	}
	if _, err := e.svc.FinishAssertion(e.ctx(), p.SessionID, u.ID, []byte(`not json`)); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("garbage response: %v", err)
	}
	if n := e.auditCount(audit.ActionAuthPasskeyAssertionFailed); n != 4 {
		t.Fatalf("%d assertion failures audited", n)
	}
}

func TestSignCountRegression(t *testing.T) {
	e := newEnv(t, testOrigin)
	u := e.user("pat@example.com")
	p := e.session(u)
	a := e.authenticator(testOrigin)
	pk, err := e.register(p, a, "key")
	if err != nil {
		t.Fatal(err)
	}
	assert := func(x *passkeytest.Authenticator) error {
		resp, err := e.assertion(p, x)
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.svc.FinishAssertion(e.ctx(), p.SessionID, u.ID, resp)
		return err
	}
	if err := assert(a); err != nil { // counter 1
		t.Fatal(err)
	}
	clone := a.Clone()
	if err := assert(a); err != nil { // counter 2
		t.Fatal(err)
	}
	if err := assert(clone); !errors.Is(err, ErrSignCountRegression) { // counter 2 again
		t.Fatalf("cloned authenticator: %v", err)
	}
	list, err := e.svc.List(t.Context(), u.ID)
	if err != nil || len(list) != 1 || list[0].SignCountAnomalyAt == nil {
		t.Fatalf("anomaly not recorded: %+v %v", list, err)
	}
	d := e.auditDetails(audit.ActionAuthPasskeySignCountAnomaly)
	if d["passkey_id"] != pk.ID.String() || d["stored_sign_count"] != float64(2) || d["received_sign_count"] != float64(2) {
		t.Fatalf("anomaly details %v", d)
	}
	if k := e.notifier.kinds(); len(k) != 2 || k[1] != EventSignCountAnomaly {
		t.Fatalf("events %v", k)
	}
	// The genuine authenticator keeps working.
	if err := assert(a); err != nil {
		t.Fatalf("genuine authenticator after anomaly: %v", err)
	}

	// Authenticators without a counter (always 0) are fine.
	zero := e.authenticator(testOrigin)
	zero.CounterStep = 0
	if _, err := e.register(p, zero, "no counter"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := assert(zero); err != nil {
			t.Fatalf("zero counter: %v", err)
		}
	}
}

func TestStepUpManagementAndRemovalGuard(t *testing.T) {
	e := newEnv(t, testOrigin)
	u := e.user("pat@example.com")
	p := e.session(u)
	a := e.authenticator(testOrigin)
	pk, err := e.register(p, a, "key")
	if err != nil {
		t.Fatal(err)
	}

	stale := p
	stale.StepUpAt = time.Now().Add(-time.Hour)
	if _, err := e.svc.BeginRegistration(e.ctx(), stale); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("begin without step-up: %v", err)
	}
	if _, err := e.svc.FinishRegistration(e.ctx(), stale, "x", []byte(`{}`)); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("finish without step-up: %v", err)
	}
	if _, err := e.svc.Rename(e.ctx(), stale, pk.ID, "x"); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("rename without step-up: %v", err)
	}
	if err := e.svc.Delete(e.ctx(), stale, pk.ID); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("delete without step-up: %v", err)
	}

	renamed, err := e.svc.Rename(e.ctx(), p, pk.ID, "Laptop")
	if err != nil || renamed.Name != "Laptop" {
		t.Fatalf("rename: %+v %v", renamed, err)
	}
	var ne *NameError
	if _, err := e.svc.Rename(e.ctx(), p, pk.ID, ""); !errors.As(err, &ne) {
		t.Fatalf("empty name: %v", err)
	}
	other := e.session(e.user("other@example.com"))
	if _, err := e.svc.Rename(e.ctx(), other, pk.ID, "mine"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rename foreign passkey: %v", err)
	}
	if err := e.svc.Delete(e.ctx(), other, pk.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete foreign passkey: %v", err)
	}

	// The removal guard sees no remaining factors and can refuse.
	var seen []auth.Factor
	e.guard = func(r []auth.Factor) error { seen = r; return auth.ErrLastFactor }
	if err := e.svc.Delete(e.ctx(), p, pk.ID); !errors.Is(err, auth.ErrLastFactor) || len(seen) != 0 {
		t.Fatalf("guarded delete: %v remaining %v", err, seen)
	}
	if list, err := e.svc.List(t.Context(), u.ID); err != nil || len(list) != 1 {
		t.Fatalf("refused delete removed the passkey: %v", err)
	}
	e.guard = nil
	q := sqlc.New(e.pool)
	if err := q.InsertRecoveryCode(t.Context(), sqlc.InsertRecoveryCodeParams{UserID: u.ID, KeyID: "0123abcd", CodeMac: make([]byte, 32)}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Delete(e.ctx(), p, pk.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n, err := q.CountUnusedRecoveryCodes(t.Context(), u.ID); err != nil || n != 0 {
		t.Fatalf("recovery codes after removing the last factor: %d %v", n, err)
	}
	if list, err := e.svc.List(t.Context(), u.ID); err != nil || len(list) != 0 {
		t.Fatalf("passkey not deleted: %v", err)
	}
	if err := e.svc.Delete(e.ctx(), p, pk.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
	if d := e.auditDetails(audit.ActionAuthMFARemoved); d["factor"] != "passkey" || d["remaining_passkeys"] != float64(0) || d["recovery_codes_deleted"] != float64(1) {
		t.Fatalf("removed details %v", d)
	}
	if n := e.auditCount(audit.ActionAuthPasskeyRenamed); n != 1 {
		t.Fatalf("%d rename entries", n)
	}
	if k := e.notifier.kinds(); len(k) != 2 || k[1] != EventRemoved {
		t.Fatalf("events %v", k)
	}
}

func TestUnavailableOverPlainHTTP(t *testing.T) {
	e := newEnv(t, "http://backup.example.com")
	st := e.svc.Status()
	if st.Available || st.Reason != ReasonInsecureContext {
		t.Fatalf("status %+v", st)
	}
	u := e.user("pat@example.com")
	p := e.session(u)
	if _, err := e.svc.BeginRegistration(e.ctx(), p); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("begin registration: %v", err)
	}
	if _, err := e.svc.FinishRegistration(e.ctx(), p, "x", []byte(`{}`)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("finish registration: %v", err)
	}
	if _, err := e.svc.BeginAssertion(e.ctx(), p.SessionID, u.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("begin assertion: %v", err)
	}
	if list, err := e.svc.List(t.Context(), u.ID); err != nil || len(list) != 0 {
		t.Fatalf("list: %v %v", list, err)
	}
}

func TestMaxPasskeys(t *testing.T) {
	e := newEnv(t, testOrigin)
	u := e.user("pat@example.com")
	p := e.session(u)
	for i := range MaxPasskeys {
		if _, err := e.register(p, e.authenticator(testOrigin), "k"); err != nil {
			t.Fatalf("passkey %d: %v", i, err)
		}
	}
	if _, err := e.svc.BeginRegistration(e.ctx(), p); !errors.Is(err, ErrTooMany) {
		t.Fatalf("over the limit: %v", err)
	}
}
