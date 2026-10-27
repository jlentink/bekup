//go:build integration

package auth

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// recEvents records published session events.
type recEvents struct {
	mu     sync.Mutex
	events []SessionEvent
	err    error
}

func (r *recEvents) PublishSessionEventTx(_ context.Context, _ *sqlc.Queries, ev SessionEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
	return r.err
}

func (r *recEvents) take() []SessionEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.events
	r.events = nil
	return out
}

func TestSessionEventsArePublished(t *testing.T) {
	e := newEnv(t)
	rec := &recEvents{}
	e.sessions.opts.Events = rec
	u := e.user("events@example.com", sqlc.UserRoleUser)

	first := e.mustLogin("events@example.com")
	if evs := rec.take(); len(evs) != 0 {
		t.Fatalf("plain login published %+v", evs)
	}
	p, err := e.resolve(first.Issued.Token)
	if err != nil {
		t.Fatal(err)
	}

	// A login in a browser that already has a session revokes it.
	second, err := e.svc.Login(t.Context(), "events@example.com", goodPassword, testMeta, p.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	want := SessionEvent{Kind: SessionRevoked, SessionID: p.SessionID}
	if evs := rec.take(); len(evs) != 1 || evs[0] != want {
		t.Fatalf("login replacing a session: %+v, want %+v", evs, want)
	}

	// Rotation.
	p2, err := e.resolve(second.Issued.Token)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := e.sessions.Rotate(t.Context(), p2.SessionID, testMeta)
	if err != nil {
		t.Fatal(err)
	}
	want = SessionEvent{Kind: SessionRotated, UserID: u.ID, SessionID: p2.SessionID}
	if evs := rec.take(); len(evs) != 1 || evs[0] != want {
		t.Fatalf("rotation: %+v, want %+v", evs, want)
	}

	// Logout.
	p3, err := e.resolve(rotated.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Logout(t.Context(), p3, testMeta); err != nil {
		t.Fatal(err)
	}
	want = SessionEvent{Kind: SessionRevoked, UserID: u.ID, SessionID: p3.SessionID}
	if evs := rec.take(); len(evs) != 1 || evs[0] != want {
		t.Fatalf("logout: %+v, want %+v", evs, want)
	}

	// Revoke all (password, role or status changes).
	e.mustLogin("events@example.com")
	if err := db.Tx(t.Context(), e.pool, func(q *sqlc.Queries) error {
		_, err := e.sessions.RevokeAllTx(t.Context(), q, u.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	want = SessionEvent{Kind: SessionsRevokedAll, UserID: u.ID}
	if evs := rec.take(); len(evs) != 1 || evs[0] != want {
		t.Fatalf("revoke all: %+v, want %+v", evs, want)
	}
	// Nothing left to revoke: no event.
	if err := db.Tx(t.Context(), e.pool, func(q *sqlc.Queries) error {
		_, err := e.sessions.RevokeAllTx(t.Context(), q, u.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if evs := rec.take(); len(evs) != 0 {
		t.Fatalf("empty revoke all published %+v", evs)
	}
}

func TestSessionEventFailureRollsBack(t *testing.T) {
	e := newEnv(t)
	rec := &recEvents{err: errors.New("notify failed")}
	e.sessions.opts.Events = rec
	e.user("rollback@example.com", sqlc.UserRoleUser)
	res := e.mustLogin("rollback@example.com")
	p, err := e.resolve(res.Issued.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Logout(t.Context(), p, testMeta); err == nil {
		t.Fatal("logout succeeded although the event could not be published")
	}
	if _, err := e.resolve(res.Issued.Token); err != nil {
		t.Fatalf("the revocation must roll back with its event: %v", err)
	}
}

func TestSessionCheck(t *testing.T) {
	e := newEnv(t)
	u := e.user("check@example.com", sqlc.UserRoleMaintainer)
	res := e.mustLogin("check@example.com")
	p, err := e.resolve(res.Issued.Token)
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := e.sessions.Check(t.Context(), p.SessionID)
	if err != nil || st != p {
		t.Fatalf("Check = %+v, %v; want the resolved principal %+v", st, err, p)
	}
	// Check never touches the session (an open socket is not activity).
	before := e.dbSession(p.SessionID)
	e.clock.Advance(2 * e.sessions.opts.TouchInterval)
	if _, _, err := e.sessions.Check(t.Context(), p.SessionID); err != nil {
		t.Fatal(err)
	}
	if after := e.dbSession(p.SessionID); !after.LastSeenAt.Equal(before.LastSeenAt) {
		t.Fatal("Check touched the session")
	}
	// A role change shows up at once.
	if err := e.d.SuperExec(t.Context(), "UPDATE users SET role = 'admin' WHERE id = '"+u.ID.String()+"'"); err != nil {
		t.Fatal(err)
	}
	if st, _, err := e.sessions.Check(t.Context(), p.SessionID); err != nil || st.Role != RoleAdmin {
		t.Fatalf("after role change: %+v, %v", st, err)
	}
	// So does a temporary password (M1 review L2: the full principal).
	if err := e.d.SuperExec(t.Context(), "UPDATE users SET password_temporary_until = now() + interval '1 hour' WHERE id = '"+u.ID.String()+"'"); err != nil {
		t.Fatal(err)
	}
	if st, _, err := e.sessions.Check(t.Context(), p.SessionID); err != nil || !st.PasswordChangeRequired {
		t.Fatalf("after temporary password: %+v, %v", st, err)
	}
	if err := e.d.SuperExec(t.Context(), "UPDATE users SET disabled = true WHERE id = '"+u.ID.String()+"'"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.sessions.Check(t.Context(), p.SessionID); !errors.Is(err, ErrNoSession) {
		t.Fatalf("disabled user: %v", err)
	}
	if _, _, err := e.sessions.Check(t.Context(), uuid.New()); !errors.Is(err, ErrNoSession) {
		t.Fatalf("unknown session: %v", err)
	}
}

func (e *env) dbSession(id uuid.UUID) sqlc.Session {
	e.t.Helper()
	rows, err := sqlc.New(e.pool).ListActiveUserSessions(e.t.Context(), e.sessionUser(id))
	if err != nil {
		e.t.Fatal(err)
	}
	for i := range rows {
		if rows[i].ID == id {
			return rows[i]
		}
	}
	e.t.Fatalf("session %s not found", id)
	return sqlc.Session{}
}

func (e *env) sessionUser(id uuid.UUID) uuid.UUID {
	e.t.Helper()
	st, _, err := e.sessions.Check(e.t.Context(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return st.UserID
}
