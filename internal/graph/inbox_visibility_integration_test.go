//go:build integration

package graph_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/notify"
)

// M5 review L2: security notifications can be marked read but not
// deleted (VALIDATION); other rows still can.
func TestInboxSecurityRowsNotDeletable(t *testing.T) {
	e := newNotifEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	ac := e.mustLogin("admin@example.com")
	e.emit(notify.Event{Type: notify.SecuritySystem, Detail: notify.Detail{"kind": notify.KindKeysRotated}})
	e.emit(notify.Event{Type: notify.SecurityAccount, Subject: notify.Subject{UserID: admin.ID}, Detail: notify.Detail{"kind": notify.KindPasswordChanged}})
	e.emit(notify.Event{Type: notify.BackupFailed, Subject: notify.Subject{JobID: e.job, RunID: uuid.New()}})
	var page inboxPage
	e.mustGQL(ac, inboxQuery, nil).field(t, "notifications", &page)
	if len(page.Edges) != 3 {
		t.Fatalf("inbox %+v", page)
	}
	for _, edge := range page.Edges {
		id := edge.Node.ID
		if edge.Node.Type == "backup.failed" {
			e.mustGQL(ac, deleteNMut, map[string]any{"id": id})
			continue
		}
		r := e.gql(ac, deleteNMut, map[string]any{"id": id})
		if r.code() != "VALIDATION" {
			t.Fatalf("delete %s: %+v", edge.Node.Type, r.Errors)
		}
		var mr struct{ Marked, UnreadCount int }
		e.mustGQL(ac, markReadMut, map[string]any{"ids": []string{id}}).field(t, "markNotificationsRead", &mr)
		if mr.Marked != 1 {
			t.Fatalf("mark %s read: %+v", edge.Node.Type, mr)
		}
	}
	var n int
	if err := e.pool.QueryRow(t.Context(), `SELECT count(*) FROM notifications WHERE user_id = $1 AND type LIKE 'security.%'`, admin.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("security rows left: %d", n)
	}
}

// M5 review L5: the inbox hides, at read time, rows about jobs the reader
// no longer owns and security.system rows after the reader stopped being
// an admin (list, unread count and the single-row load of the stream).
func TestInboxReadTimeVisibility(t *testing.T) {
	e := newNotifEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	e.own(e.job, owner.ID)
	ac, oc := e.mustLogin("admin@example.com"), e.mustLogin("owner@example.com")
	e.emit(notify.Event{Type: notify.SecuritySystem, Detail: notify.Detail{"kind": notify.KindKeysRotated}})
	e.emit(notify.Event{Type: notify.BackupFailed, Subject: notify.Subject{JobID: e.job, RunID: uuid.New()}})

	count := func(c string) int {
		t.Helper()
		var n int
		e.mustGQL(c, unreadQuery, nil).field(t, "unreadNotificationCount", &n)
		var page inboxPage
		e.mustGQL(c, inboxQuery, nil).field(t, "notifications", &page)
		if len(page.Edges) != n {
			t.Fatalf("list %d, unread %d", len(page.Edges), n)
		}
		return n
	}
	if a, o := count(ac), count(oc); a != 2 || o != 1 {
		t.Fatalf("before: admin %d, owner %d", a, o)
	}
	// The owner loses the job: its rows disappear (also for the stream's
	// single-row load).
	var ownerRow uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `SELECT id FROM notifications WHERE user_id = $1`, owner.ID).Scan(&ownerRow); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), `DELETE FROM job_owners WHERE job_id = $1`, e.job); err != nil {
		t.Fatal(err)
	}
	if o := count(oc); o != 0 {
		t.Fatalf("owner after losing the job: %d", o)
	}
	// The admin is demoted to maintainer: security.system rows are hidden,
	// the job row (maintainers see every job) stays.
	if _, err := e.pool.Exec(t.Context(), `UPDATE users SET role = 'maintainer' WHERE id = $1`, admin.ID); err != nil {
		t.Fatal(err)
	}
	mc := e.mustLogin("admin@example.com")
	var page inboxPage
	e.mustGQL(mc, inboxQuery, nil).field(t, "notifications", &page)
	if len(page.Edges) != 1 || page.Edges[0].Node.Type != "backup.failed" {
		t.Fatalf("maintainer inbox %+v", page)
	}
	// Stored rows are kept (only hidden).
	var n int
	if err := e.pool.QueryRow(t.Context(), `SELECT count(*) FROM notifications`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("stored rows %d", n)
	}
}
