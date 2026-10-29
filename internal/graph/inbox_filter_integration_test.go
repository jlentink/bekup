//go:build integration

package graph_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/notify"
)

const filteredInboxQuery = `query($u: Boolean, $f: NotificationInboxFilter) {
  notifications(unreadOnly: $u, filter: $f, first: 50) { edges { node { id type group readAt } } }
}`

// TestInboxFilterGraphQL covers notifications(filter: ...) (D-196).
func TestInboxFilterGraphQL(t *testing.T) {
	e := newNotifEnv(t)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	e.own(e.job, owner.ID)
	oc := e.mustLogin("owner@example.com")
	for range 2 {
		e.emit(notify.Event{Type: notify.BackupFailed, Subject: notify.Subject{JobID: e.job, RunID: uuid.New()}})
	}
	e.emit(notify.Event{Type: notify.BackupSucceeded, Subject: notify.Subject{JobID: e.job, RunID: uuid.New()}})
	e.emit(notify.Event{Type: notify.RestoreFailed, Subject: notify.Subject{JobID: e.job, RestoreID: uuid.New()}})

	count := func(vars map[string]any) (int, map[string]int) {
		t.Helper()
		var p inboxPage
		e.mustGQL(oc, filteredInboxQuery, vars).field(t, "notifications", &p)
		types := map[string]int{}
		for _, ed := range p.Edges {
			types[ed.Node.Type]++
		}
		return len(p.Edges), types
	}
	if n, _ := count(nil); n != 4 {
		t.Fatalf("unfiltered: %d", n)
	}
	if n, ty := count(map[string]any{"f": map[string]any{"group": "BACKUP"}}); n != 3 || ty["restore.failed"] != 0 {
		t.Fatalf("group BACKUP: %d %v", n, ty)
	}
	if n, ty := count(map[string]any{"f": map[string]any{"events": []string{"backup.failed", "restore.failed"}}}); n != 3 || ty["backup.succeeded"] != 0 {
		t.Fatalf("events: %d %v", n, ty)
	}
	// group and events must both match.
	if n, ty := count(map[string]any{"f": map[string]any{"group": "BACKUP", "events": []string{"backup.failed", "restore.failed"}}}); n != 2 || ty["backup.failed"] != 2 {
		t.Fatalf("group and events: %d %v", n, ty)
	}
	if n, _ := count(map[string]any{"f": map[string]any{"group": "SECURITY"}}); n != 0 {
		t.Fatalf("group SECURITY: %d", n)
	}
	// unreadOnly in the filter or as the argument.
	var mr struct{ Marked, UnreadCount int }
	var all inboxPage
	e.mustGQL(oc, filteredInboxQuery, map[string]any{"f": map[string]any{"group": "RESTORE"}}).field(t, "notifications", &all)
	e.mustGQL(oc, markReadMut, map[string]any{"ids": []string{all.Edges[0].Node.ID}}).field(t, "markNotificationsRead", &mr)
	if n, _ := count(map[string]any{"f": map[string]any{"unreadOnly": true}}); n != 3 {
		t.Fatalf("filter unreadOnly: %d", n)
	}
	if n, _ := count(map[string]any{"u": true, "f": map[string]any{"group": "RESTORE"}}); n != 0 {
		t.Fatalf("argument unreadOnly with group: %d", n)
	}
	// Unknown event types are rejected with the field.
	r := e.gql(oc, filteredInboxQuery, map[string]any{"f": map[string]any{"events": []string{"backup.failed", "nope.nope"}}})
	if r.code() != "VALIDATION" {
		t.Fatalf("unknown event: %+v", r.Errors)
	}
}
