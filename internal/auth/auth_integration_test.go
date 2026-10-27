//go:build integration

package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/logging"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

// clock is a settable time source shared by the services under test. It
// starts at the real time so that the database's now() filters agree.
type clock struct {
	mu  sync.Mutex
	off time.Duration
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.off)
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.off += d
}

type recNotifier struct {
	mu     sync.Mutex
	events []LockoutEvent
}

func (r *recNotifier) AccountLocked(_ context.Context, ev LockoutEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

type env struct {
	t        *testing.T
	d        dbtest.DB
	pool     *pgxpool.Pool
	clock    *clock
	hasher   *Hasher
	sessions *Sessions
	auditor  *audit.Auditor
	notifier *recNotifier
	svc      *Service
	limited  atomic.Bool
}

var testMeta = RequestMeta{IP: netip.MustParseAddr("192.0.2.10"), UserAgent: "test-agent/1.0"}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := dbtest.Migrated(t)
	e := &env{t: t, d: d, pool: d.AppPool(t), clock: &clock{}, notifier: &recNotifier{}}
	e.hasher = weakHasher(t, weak, 4)
	e.sessions = NewSessions(e.pool, SessionOptions{
		IdleTimeout: 30 * time.Minute, AbsoluteTimeout: 12 * time.Hour, TouchInterval: time.Minute, Now: e.clock.Now,
	})
	e.auditor = audit.New(e.pool, audit.Options{Registry: logging.NewRegistry()})
	var err error
	e.svc, err = NewService(ServiceOptions{
		DB: e.pool, Hasher: e.hasher, Sessions: e.sessions, Auditor: e.auditor, Factors: NewSecondFactors(),
		Policy:   NewPasswordPolicy(12, 3),
		Lockout:  LockoutPolicy{Threshold: 3, Base: time.Minute, Max: 10 * time.Minute},
		Notifier: e.notifier, Now: e.clock.Now,
		LoginLimiter: func(netip.Addr) (bool, time.Duration) {
			if e.limited.Load() {
				return false, 30 * time.Second
			}
			return true, 0
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

const goodPassword = "blue-mountain-river-42"

func (e *env) user(email string, role sqlc.UserRole) sqlc.User {
	e.t.Helper()
	h, err := e.hasher.Hash(e.t.Context(), goodPassword)
	if err != nil {
		e.t.Fatal(err)
	}
	u, err := sqlc.New(e.pool).InsertUser(e.t.Context(), sqlc.InsertUserParams{Email: email, DisplayName: "Test User", PasswordHash: &h, Role: role})
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

func (e *env) login(email, password string) (LoginResult, error) {
	return e.svc.Login(e.t.Context(), email, password, testMeta, uuid.Nil)
}

func (e *env) mustLogin(email string) LoginResult {
	e.t.Helper()
	res, err := e.login(email, goodPassword)
	if err != nil {
		e.t.Fatalf("login %s: %v", email, err)
	}
	return res
}

func (e *env) resolve(token string) (Principal, error) {
	p, _, err := e.sessions.Resolve(e.t.Context(), token, testMeta)
	return p, err
}

func (e *env) entries(action audit.Action) []audit.ListEntry {
	e.t.Helper()
	page, err := e.auditor.List(e.t.Context(), audit.Filter{Action: action}, 0, 200)
	if err != nil {
		e.t.Fatal(err)
	}
	return page.Entries
}

func (e *env) dbUser(id uuid.UUID) sqlc.User {
	e.t.Helper()
	u, err := sqlc.New(e.pool).GetUserByID(e.t.Context(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

func TestSessionTokenOnlyHashStored(t *testing.T) {
	e := newEnv(t)
	u := e.user("hash@example.com", sqlc.UserRoleUser)
	res := e.mustLogin(u.Email)
	raw, err := base64.RawURLEncoding.DecodeString(res.Issued.Token)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	row, err := sqlc.New(e.pool).GetSessionByTokenHash(t.Context(), sum[:])
	if err != nil {
		t.Fatalf("session not found by sha256(token): %v", err)
	}
	if row.ID != res.Issued.Session.ID || bytes.Equal(row.TokenHash, raw) {
		t.Fatal("stored hash is not sha256 of the token")
	}
	// No column of any session row holds the token (raw or encoded).
	var leaked int
	err = e.pool.QueryRow(t.Context(), "SELECT count(*) FROM sessions s WHERE row_to_json(s)::text LIKE '%' || $1 || '%' OR position($2::bytea IN token_hash) > 0",
		res.Issued.Token, raw).Scan(&leaked)
	if err != nil || leaked != 0 {
		t.Fatalf("token found in sessions table: %d %v", leaked, err)
	}
	if row.Aal != AAL1 || row.StepupAt == nil || row.Aal2At != nil || row.UserAgent == nil || *row.UserAgent != testMeta.UserAgent || row.Ip == nil {
		t.Fatalf("unexpected session row %+v", row)
	}
}

func TestResolveSession(t *testing.T) {
	e := newEnv(t)
	u := e.user("resolve@example.com", sqlc.UserRoleMaintainer)
	res := e.mustLogin(u.Email)
	p, err := e.resolve(res.Issued.Token)
	if err != nil {
		t.Fatal(err)
	}
	if p.UserID != u.ID || p.Role != RoleMaintainer || p.SessionID != res.Issued.Session.ID || p.AAL != AAL1 ||
		!p.HasRecentStepUp(5*time.Minute) || !p.AAL2At.IsZero() {
		t.Fatalf("principal %+v", p)
	}
	for _, bad := range []string{"", "garbage", strings.Repeat("A", 43)} {
		if _, err := e.resolve(bad); !errors.Is(err, ErrNoSession) {
			t.Errorf("Resolve(%q) = %v, want ErrNoSession", bad, err)
		}
	}
	// Disabled users have no principal; a lockout keeps existing sessions.
	ctx := t.Context()
	if err := e.d.SuperExec(ctx, "UPDATE users SET disabled = true WHERE id = '"+u.ID.String()+"'"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.resolve(res.Issued.Token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("disabled user: %v", err)
	}
	if err := e.d.SuperExec(ctx, "UPDATE users SET disabled = false, locked_until = now() + interval '5 minutes' WHERE id = '"+u.ID.String()+"'"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.resolve(res.Issued.Token); err != nil {
		t.Fatalf("a lockout must not end existing sessions: %v", err)
	}
	if err := e.d.SuperExec(ctx, "UPDATE users SET locked_until = now() - interval '1 second' WHERE id = '"+u.ID.String()+"'"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.resolve(res.Issued.Token); err != nil {
		t.Fatalf("expired lock: %v", err)
	}
}

func TestSessionExpiry(t *testing.T) {
	e := newEnv(t)
	u := e.user("expiry@example.com", sqlc.UserRoleUser)
	ctx := t.Context()

	idle := e.mustLogin(u.Email)
	if err := e.d.SuperExec(ctx, "UPDATE sessions SET idle_expires_at = now() - interval '1 second' WHERE id = '"+idle.Issued.Session.ID.String()+"'"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.resolve(idle.Issued.Token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("idle-expired session resolved: %v", err)
	}

	abs := e.mustLogin(u.Email)
	if err := e.d.SuperExec(ctx, "UPDATE sessions SET idle_expires_at = now() - interval '2 seconds', absolute_expires_at = now() - interval '1 second' WHERE id = '"+abs.Issued.Session.ID.String()+"'"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.resolve(abs.Issued.Token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("absolute-expired session resolved: %v", err)
	}

	// The absolute expiry is now + AbsoluteTimeout and caps the idle one.
	s := e.mustLogin(u.Email).Issued.Session
	if d := s.AbsoluteExpiresAt.Sub(s.CreatedAt); d < 11*time.Hour+59*time.Minute || d > 12*time.Hour+time.Minute {
		t.Fatalf("absolute lifetime %s", d)
	}
	if d := s.IdleExpiresAt.Sub(s.CreatedAt); d < 29*time.Minute || d > 31*time.Minute {
		t.Fatalf("idle lifetime %s", d)
	}
}

func TestSlidingIdleRefreshIsThrottled(t *testing.T) {
	e := newEnv(t)
	u := e.user("touch@example.com", sqlc.UserRoleUser)
	res := e.mustLogin(u.Email)
	q := sqlc.New(e.pool)
	get := func() sqlc.Session {
		t.Helper()
		s, err := q.GetSessionByTokenHash(t.Context(), res.Issued.Session.TokenHash)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := get()
	for range 5 {
		if _, err := e.resolve(res.Issued.Token); err != nil {
			t.Fatal(err)
		}
	}
	if after := get(); !after.LastSeenAt.Equal(before.LastSeenAt) || !after.IdleExpiresAt.Equal(before.IdleExpiresAt) {
		t.Fatal("session written within the touch interval")
	}
	e.clock.Advance(2 * time.Minute)
	other := RequestMeta{IP: netip.MustParseAddr("198.51.100.7"), UserAgent: "other-agent"}
	if _, _, err := e.sessions.Resolve(t.Context(), res.Issued.Token, other); err != nil {
		t.Fatal(err)
	}
	after := get()
	if !after.IdleExpiresAt.After(before.IdleExpiresAt.Add(time.Minute)) {
		t.Fatalf("idle expiry not extended: %s -> %s", before.IdleExpiresAt, after.IdleExpiresAt)
	}
	if after.Ip == nil || *after.Ip != other.IP || after.UserAgent == nil || *after.UserAgent != "other-agent" {
		t.Fatalf("client facts not recorded: %+v", after)
	}
	// The idle expiry never passes the absolute expiry.
	if err := e.d.SuperExec(t.Context(), "UPDATE sessions SET idle_expires_at = now() + interval '5 minutes', absolute_expires_at = now() + interval '10 minutes', last_seen_at = now() - interval '1 hour' WHERE id = '"+after.ID.String()+"'"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.resolve(res.Issued.Token); err != nil {
		t.Fatal(err)
	}
	if s := get(); s.IdleExpiresAt.After(s.AbsoluteExpiresAt) {
		t.Fatal("idle expiry beyond absolute expiry")
	}
}

func TestRotationAndAAL(t *testing.T) {
	e := newEnv(t)
	u := e.user("rotate@example.com", sqlc.UserRoleUser)
	ctx := t.Context()
	first := e.mustLogin(u.Email)

	rotated, err := e.sessions.Rotate(ctx, first.Issued.Session.ID, testMeta)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Token == first.Issued.Token || rotated.Session.ID == first.Issued.Session.ID {
		t.Fatal("rotation kept the token or id")
	}
	if !rotated.Session.AbsoluteExpiresAt.Equal(first.Issued.Session.AbsoluteExpiresAt) {
		t.Fatal("rotation changed the absolute expiry")
	}
	if _, err := e.resolve(first.Issued.Token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("old token after rotation: %v", err)
	}
	if _, err := e.sessions.Rotate(ctx, first.Issued.Session.ID, testMeta); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("rotating a revoked session: %v", err)
	}

	elevated, err := e.sessions.ElevateAAL(ctx, rotated.Session.ID, AAL2)
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.resolve(elevated.Token)
	if err != nil {
		t.Fatal(err)
	}
	if p.AAL != AAL2 || !p.HasRecentAAL2(time.Minute) || !p.HasRecentStepUp(time.Minute) {
		t.Fatalf("elevated principal %+v", p)
	}
	if _, err := e.resolve(rotated.Token); !errors.Is(err, ErrNoSession) {
		t.Fatal("token before AAL elevation still valid")
	}
	if _, err := e.sessions.ElevateAAL(ctx, elevated.Session.ID, 3); err == nil {
		t.Fatal("AAL 3 accepted")
	}

	if err := e.d.SuperExec(ctx, "UPDATE sessions SET stepup_at = NULL WHERE id = '"+elevated.Session.ID.String()+"'"); err != nil {
		t.Fatal(err)
	}
	if err := e.sessions.MarkStepUp(ctx, elevated.Session.ID); err != nil {
		t.Fatal(err)
	}
	if p, err := e.resolve(elevated.Token); err != nil || !p.HasRecentStepUp(time.Minute) {
		t.Fatal("MarkStepUp not recorded")
	}
	if err := e.sessions.MarkStepUp(ctx, uuid.New()); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("MarkStepUp unknown: %v", err)
	}
}

func TestLoginSuccessAuditedAndRotates(t *testing.T) {
	e := newEnv(t)
	u := e.user("Login@Example.com", sqlc.UserRoleAdmin)
	prev := e.mustLogin(u.Email)
	// E-mail is case-insensitive (citext); the browser's previous session
	// is revoked by the next login.
	res, err := e.svc.Login(t.Context(), "  login@example.COM ", goodPassword, testMeta, prev.Issued.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.User.ID != u.ID || res.SecondFactorRequired || len(res.AvailableFactors) != 0 {
		t.Fatalf("result %+v", res)
	}
	if _, err := e.resolve(prev.Issued.Token); !errors.Is(err, ErrNoSession) {
		t.Fatal("previous session survived the login")
	}
	ok := e.entries(audit.ActionAuthLoginSucceeded)
	if len(ok) != 2 || ok[0].ActorUserID == nil || *ok[0].ActorUserID != u.ID || ok[0].IP == nil || *ok[0].IP != testMeta.IP {
		t.Fatalf("login audit entries %+v", ok)
	}
}

func TestLoginFailuresAreGenericAndAudited(t *testing.T) {
	e := newEnv(t)
	u := e.user("fail@example.com", sqlc.UserRoleUser)
	dis := e.user("disabled@example.com", sqlc.UserRoleUser)
	if err := e.d.SuperExec(t.Context(), "UPDATE users SET disabled = true WHERE id = '"+dis.ID.String()+"'"); err != nil {
		t.Fatal(err)
	}
	nopw, err := sqlc.New(e.pool).InsertUser(t.Context(), sqlc.InsertUserParams{Email: "nopw@example.com", Role: sqlc.UserRoleUser})
	if err != nil {
		t.Fatal(err)
	}

	before := e.hasher.DummyVerifications()
	cases := []struct {
		email, pw, reason string
		dummy             bool
	}{
		{u.Email, "wrong-password-123", reasonBadPassword, false},
		{"nobody@example.com", goodPassword, reasonUnknownUser, true},
		{dis.Email, goodPassword, reasonDisabled, true},
		{nopw.Email, goodPassword, reasonNoPassword, true},
		{strings.Repeat("x", 300) + "@example.com", goodPassword, reasonInvalidInput, true},
	}
	for _, c := range cases {
		d0 := e.hasher.DummyVerifications()
		if _, err := e.login(c.email, c.pw); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("%s: %v, want ErrInvalidCredentials", c.reason, err)
		}
		if got := e.hasher.DummyVerifications() - d0; (got == 1) != c.dummy {
			t.Errorf("%s: dummy verifications %d, want dummy=%v", c.reason, got, c.dummy)
		}
	}
	if e.hasher.DummyVerifications()-before != 4 {
		t.Fatal("unexpected dummy verification count")
	}
	failed := e.entries(audit.ActionAuthLoginFailed)
	if len(failed) != len(cases) {
		t.Fatalf("%d failure entries, want %d", len(failed), len(cases))
	}
	reasons := map[string]bool{}
	for _, f := range failed {
		d := string(f.Details)
		if strings.Contains(d, goodPassword) || strings.Contains(d, "wrong-password") || strings.Contains(d, "nobody@") {
			t.Fatalf("audit details leak the attempt: %s", d)
		}
		for _, c := range cases {
			if strings.Contains(d, `"`+c.reason+`"`) {
				reasons[c.reason] = true
			}
		}
	}
	if len(reasons) != len(cases) {
		t.Fatalf("reasons recorded: %v", reasons)
	}
	if n := e.dbUser(u.ID).FailedLoginCount; n != 1 {
		t.Fatalf("failed_login_count = %d", n)
	}
	// Only the wrong password counts; a success resets it.
	e.mustLogin(u.Email)
	if got := e.dbUser(u.ID); got.FailedLoginCount != 0 || got.LockedUntil != nil {
		t.Fatalf("lockout state not reset: %+v", got)
	}

	e.limited.Store(true)
	var rl *RateLimitedError
	if _, err := e.login(u.Email, goodPassword); !errors.As(err, &rl) || rl.RetryAfter != 30*time.Second {
		t.Fatalf("rate limited login: %v", err)
	}
}

func TestLoginLockout(t *testing.T) {
	e := newEnv(t)
	u := e.user("lock@example.com", sqlc.UserRoleUser)
	for i := range 3 {
		if _, err := e.login(u.Email, "wrong-password-123"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	got := e.dbUser(u.ID)
	if got.FailedLoginCount != 3 || got.LockedUntil == nil {
		t.Fatalf("not locked after threshold: %+v", got)
	}
	if d := time.Until(*got.LockedUntil); d < 50*time.Second || d > 70*time.Second {
		t.Fatalf("first lock %s, want ~1m", d)
	}
	if len(e.entries(audit.ActionAuthLockout)) != 1 || len(e.notifier.events) != 1 || e.notifier.events[0].UserID != u.ID {
		t.Fatal("lockout not audited/notified")
	}
	// While locked even the right password fails generically, runs the
	// dummy verification and does not extend the lock.
	d0 := e.hasher.DummyVerifications()
	if _, err := e.login(u.Email, goodPassword); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("locked login: %v", err)
	}
	if e.hasher.DummyVerifications() != d0+1 {
		t.Fatal("locked path skipped the dummy verification")
	}
	if again := e.dbUser(u.ID); again.FailedLoginCount != 3 || !again.LockedUntil.Equal(*got.LockedUntil) {
		t.Fatal("attempt while locked changed the lockout state")
	}
	// After the lock, another failure doubles it.
	e.clock.Advance(2 * time.Minute)
	if _, err := e.login(u.Email, "wrong-password-123"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal(err)
	}
	got = e.dbUser(u.ID)
	if d := got.LockedUntil.Sub(e.clock.Now()); d < 110*time.Second || d > 130*time.Second {
		t.Fatalf("second lock %s, want ~2m", d)
	}
	e.clock.Advance(3 * time.Minute)
	e.mustLogin(u.Email)
	if got := e.dbUser(u.ID); got.FailedLoginCount != 0 || got.LockedUntil != nil {
		t.Fatal("success did not reset the lockout")
	}
}

func TestRehashOnLogin(t *testing.T) {
	e := newEnv(t)
	u := e.user("rehash@example.com", sqlc.UserRoleUser)
	stronger := weakHasher(t, Argon2Params{MemoryKiB: 128, Iterations: 2, Parallelism: 1}, 2)
	svc, err := NewService(ServiceOptions{DB: e.pool, Hasher: stronger, Sessions: e.sessions, Auditor: e.auditor, Factors: NewSecondFactors()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(t.Context(), u.Email, goodPassword, testMeta, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	h := *e.dbUser(u.ID).PasswordHash
	if !strings.Contains(h, "$m=128,t=2,p=1$") {
		t.Fatalf("hash not upgraded: %s", h)
	}
	if ok, rehash, err := stronger.Verify(t.Context(), goodPassword, h); !ok || rehash || err != nil {
		t.Fatal("upgraded hash does not verify")
	}
}

func TestChangePassword(t *testing.T) {
	e := newEnv(t)
	u := e.user("change@example.com", sqlc.UserRoleUser)
	other := e.user("other@example.com", sqlc.UserRoleUser)
	ctx := t.Context()
	a := e.mustLogin(u.Email)
	b := e.mustLogin(u.Email)
	o := e.mustLogin(other.Email)
	pa, err := e.resolve(a.Issued.Token)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.ChangePassword(ctx, pa, "wrong-password-123", "new-granite-harbor-77", testMeta); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("wrong current: %v", err)
	}
	if e.dbUser(u.ID).FailedLoginCount != 1 || len(e.entries(audit.ActionAuthPasswordChangeFailed)) != 1 {
		t.Fatal("wrong current password not counted/audited")
	}
	var pe *PolicyError
	if _, err := e.svc.ChangePassword(ctx, pa, goodPassword, goodPassword, testMeta); !errors.As(err, &pe) || pe.Reason != ReasonUnchanged {
		t.Fatalf("unchanged: %v", err)
	}
	if _, err := e.svc.ChangePassword(ctx, pa, goodPassword, "change@example.com-xyz", testMeta); !errors.As(err, &pe) || pe.Reason != ReasonContainsAccount {
		t.Fatalf("contains e-mail: %v", err)
	}
	if _, err := e.svc.ChangePassword(ctx, pa, goodPassword, "password1234", testMeta); !errors.As(err, &pe) || pe.Reason != ReasonCommon {
		t.Fatalf("common: %v", err)
	}

	issued, err := e.svc.ChangePassword(ctx, pa, goodPassword, "new-granite-harbor-77", testMeta)
	if err != nil {
		t.Fatal(err)
	}
	for name, tok := range map[string]string{"current": a.Issued.Token, "other": b.Issued.Token} {
		if _, err := e.resolve(tok); !errors.Is(err, ErrNoSession) {
			t.Errorf("%s session survived the password change: %v", name, err)
		}
	}
	np, err := e.resolve(issued.Token)
	if err != nil || np.UserID != u.ID || np.AAL != pa.AAL {
		t.Fatalf("new session: %+v %v", np, err)
	}
	if !issued.Session.AbsoluteExpiresAt.Equal(a.Issued.Session.AbsoluteExpiresAt) {
		t.Fatal("password change extended the session lifetime")
	}
	if _, err := e.resolve(o.Issued.Token); err != nil {
		t.Fatal("another user's session was revoked")
	}
	if _, err := e.login(u.Email, goodPassword); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("old password still works")
	}
	if _, err := e.login(u.Email, "new-granite-harbor-77"); err != nil {
		t.Fatalf("new password: %v", err)
	}
	changed := e.entries(audit.ActionAuthPasswordChanged)
	if len(changed) != 1 || !strings.Contains(string(changed[0].Details), `"revoked_sessions": 2`) && !strings.Contains(string(changed[0].Details), `"revoked_sessions":2`) {
		t.Fatalf("password change audit %+v", changed)
	}
}

func TestLogoutAndRevokeSession(t *testing.T) {
	e := newEnv(t)
	u := e.user("logout@example.com", sqlc.UserRoleUser)
	other := e.user("other2@example.com", sqlc.UserRoleUser)
	ctx := t.Context()
	a := e.mustLogin(u.Email)
	b := e.mustLogin(u.Email)
	o := e.mustLogin(other.Email)
	pa, err := e.resolve(a.Issued.Token)
	if err != nil {
		t.Fatal(err)
	}

	list, err := e.svc.ListSessions(ctx, pa)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListSessions = %d %v", len(list), err)
	}
	if _, err := e.svc.RevokeSession(ctx, pa, o.Issued.Session.ID, testMeta); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("revoking another user's session: %v", err)
	}
	if _, err := e.resolve(o.Issued.Token); err != nil {
		t.Fatal("other user's session revoked")
	}
	cur, err := e.svc.RevokeSession(ctx, pa, b.Issued.Session.ID, testMeta)
	if err != nil || cur {
		t.Fatalf("RevokeSession(b) = %v %v", cur, err)
	}
	if _, err := e.resolve(b.Issued.Token); !errors.Is(err, ErrNoSession) {
		t.Fatal("revoked session still resolves")
	}
	if len(e.entries(audit.ActionSessionRevoked)) != 1 {
		t.Fatal("revoke not audited")
	}
	if err := e.svc.Logout(ctx, pa, testMeta); err != nil {
		t.Fatal(err)
	}
	if _, err := e.resolve(a.Issued.Token); !errors.Is(err, ErrNoSession) {
		t.Fatal("session survived logout")
	}
	if len(e.entries(audit.ActionAuthLogout)) != 1 {
		t.Fatal("logout not audited")
	}
	if err := e.svc.Logout(ctx, pa, testMeta); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("double logout: %v", err)
	}
}

func TestDeleteStaleSessions(t *testing.T) {
	e := newEnv(t)
	u := e.user("stale@example.com", sqlc.UserRoleUser)
	a := e.mustLogin(u.Email)
	e.mustLogin(u.Email)
	if err := e.d.SuperExec(t.Context(), "UPDATE sessions SET revoked_at = now() - interval '8 days' WHERE id = '"+a.Issued.Session.ID.String()+"'"); err != nil {
		t.Fatal(err)
	}
	n, err := e.sessions.DeleteStale(t.Context(), 7*24*time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("DeleteStale = %d %v", n, err)
	}
}
