//go:build integration

package graph_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/restore"
)

const restoresUpdatedSub = `subscription { restoresUpdated { id status job { id } } }`

// TestRestoresUpdatedStream covers the unfiltered, scoped restore stream
// (D-196): staff see every restore, owners their own jobs' restores (also
// new ones), others nothing, and access is re-checked per event.
func TestRestoresUpdatedStream(t *testing.T) {
	e := newRestoreEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	other := e.user("other@example.com", sqlc.UserRoleUser)
	maint := e.user("maint@example.com", sqlc.UserRoleMaintainer)
	e.factors.set(admin.ID, true)
	e.own(e.job, owner.ID)
	adminC, ownerC, otherC, maintC := e.mustLogin(admin.Email), e.mustLogin(owner.Email), e.mustLogin(other.Email), e.mustLogin(maint.Email)

	type evGQL struct {
		ID, Status string
		Job        struct{ ID string }
	}
	// Before the streams open: raising the admin session to AAL2 later
	// would end the admin's stream at its next re-validation (D-118).
	e.fresh2FA(admin.ID)
	adminCh := e.openStream(adminC, restoresUpdatedSub, nil)
	ownerCh := e.openStream(ownerC, restoresUpdatedSub, nil)
	otherCh := e.openStream(otherC, restoresUpdatedSub, nil)
	maintCh := e.openStream(maintC, restoresUpdatedSub, nil)
	// Wait until the four streams listen on the bus.
	deadline := time.Now().Add(10 * time.Second)
	for e.events.Subscribers(pubsub.RestoreEvents.Name) < 4 {
		if time.Now().After(deadline) {
			t.Fatal("the streams did not subscribe")
		}
		time.Sleep(10 * time.Millisecond)
	}

	var r struct{ ID string }
	e.mustGQL(adminC, restoreArtMut, map[string]any{"in": map[string]any{
		"artifactId": e.art, "targetConnectionId": e.conn, "mode": "NEW_DB", "targetDatabase": "live",
	}}).field(t, "restoreArtifact", &r)
	id := uuid.MustParse(r.ID)

	for name, ch := range map[string]<-chan sseEv{"admin": adminCh, "owner": ownerCh, "maintainer": maintCh} {
		var ev evGQL
		nextData(t, ch, "restoresUpdated", &ev)
		if ev.ID != id.String() || ev.Status != "QUEUED" || ev.Job.ID != e.job.String() {
			t.Fatalf("%s: first event %+v", name, ev)
		}
	}

	// Creating a restore can announce it more than once; an announcement
	// loaded and authorized before the access change below may still be
	// in flight on a stream. Drain until the streams are quiet: the owner
	// may only see this restore QUEUED again, the non-owner nothing.
	for name, ch := range map[string]<-chan sseEv{"owner": ownerCh, "non-owner": otherCh} {
		for {
			x, ok := nextEv(ch, time.Second)
			if !ok {
				break
			}
			if x.event != "next" {
				continue
			}
			if name == "non-owner" || !strings.Contains(x.data, id.String()) || !strings.Contains(x.data, `"QUEUED"`) {
				t.Fatalf("%s received %+v before the access change", name, x)
			}
		}
	}

	// The owner loses the job: the admin still sees the terminal status,
	// the owner and the non-owner see nothing more.
	if _, err := e.pool.Exec(t.Context(), `DELETE FROM job_owners WHERE job_id = $1 AND user_id = $2`, e.job, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Finish(t.Context(), id, restore.StatusFailed, restore.ErrInterrupted); err != nil {
		t.Fatal(err)
	}
	var ev evGQL
	for ev.Status != "FAILED" {
		nextData(t, adminCh, "restoresUpdated", &ev)
	}
	for name, ch := range map[string]<-chan sseEv{"owner": ownerCh, "non-owner": otherCh} {
		for {
			x, ok := nextEv(ch, time.Second)
			if !ok {
				break
			}
			if x.event == "next" {
				t.Fatalf("%s received %+v", name, x)
			}
		}
	}
}
