//go:build integration

package graph_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/restore"
)

// Combined streams (D-197): accountEvents, activityUpdated, runActivity.

const (
	runActivitySub     = `subscription($run: UUID!, $after: ID) { runActivity(runId: $run, afterLogId: $after) { run { id status } logs { runId lines { id line } endId done } } }`
	activityUpdatedSub = `subscription { activityUpdated { run { id status job { id } } restore { id status job { id } } } }`
	accountEventsSub   = `subscription { accountEvents { session { kind current } notification { id } } }`
)

type runActivityGQL struct {
	Run  *struct{ ID, Status string }
	Logs *struct {
		RunID string
		Lines []struct{ ID, Line string }
		EndID *string
		Done  bool
	}
}

type activityGQL struct {
	Run *struct {
		ID, Status string
		Job        struct{ ID string }
	}
	Restore *struct {
		ID, Status string
		Job        struct{ ID string }
	}
}

// TestRunActivityStream: one stream carries the run's changes and its log
// batches, re-authorized per event; after the done batch it sends the
// final run state and completes. Callers without access are refused.
func TestRunActivityStream(t *testing.T) {
	e := newRunEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	e.user("other@example.com", sqlc.UserRoleUser)
	e.own(e.jobA, owner.ID)
	ownerC, otherC := e.mustLogin("owner@example.com"), e.mustLogin("other@example.com")

	ref := e.newRun(e.jobA, &admin.ID)
	for name, vars := range map[string]map[string]any{
		"non-owner":   {"run": ref.RunID},
		"unknown run": {"run": uuid.New()},
	} {
		ch := e.openStream(otherC, runActivitySub, vars)
		ev, ok := nextEv(ch, 5*time.Second)
		if !ok || !strings.Contains(ev.data, "FORBIDDEN") {
			t.Fatalf("%s: %+v", name, ev)
		}
	}

	ch := e.openStream(ownerC, runActivitySub, map[string]any{"run": ref.RunID})
	// The log source and the run source both listen.
	waitSubscribers(t, e, 2)
	e.logs(ref, "starting", "dumping app")
	var lines []string
	sawRun := false
	for len(lines) < 2 {
		var ev runActivityGQL
		nextData(t, ch, "runActivity", &ev)
		if (ev.Run == nil) == (ev.Logs == nil) {
			t.Fatalf("exactly one field must be set: %+v", ev)
		}
		if ev.Logs != nil {
			for _, l := range ev.Logs.Lines {
				lines = append(lines, l.Line)
			}
		}
	}
	if lines[0] != "starting" || lines[1] != "dumping app" {
		t.Fatalf("log lines %v", lines)
	}
	e.completeRun(ref)
	done := false
	final := ""
	for {
		ev, ok := nextEv(ch, 10*time.Second)
		if !ok {
			t.Fatal("stream did not complete")
		}
		if ev.event == "complete" {
			break
		}
		var r gqlResp
		if err := json.Unmarshal([]byte(ev.data), &r); err != nil {
			t.Fatal(err)
		}
		if len(r.Errors) > 0 {
			t.Fatalf("stream error %+v", r.Errors)
		}
		var a runActivityGQL
		r.field(t, "runActivity", &a)
		switch {
		case a.Logs != nil && a.Logs.Done:
			done = true
		case a.Run != nil:
			sawRun = true
			if a.Run.ID != ref.RunID.String() {
				t.Fatalf("foreign run %+v", a.Run)
			}
			final = a.Run.Status
		}
	}
	if !done || !sawRun || final != "SUCCESS" {
		t.Fatalf("done %v, run events %v, final status %q", done, sawRun, final)
	}
}

// TestActivityUpdatedStream: the dashboard stream delivers runs and
// restores, each authorized at delivery with its own operation; owners
// only receive their jobs' events and stop receiving them when the job
// is taken away.
func TestActivityUpdatedStream(t *testing.T) {
	e := newRestoreEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	other := e.user("other@example.com", sqlc.UserRoleUser)
	e.factors.set(admin.ID, true)
	e.own(e.job, owner.ID)
	adminC, ownerC, otherC := e.mustLogin(admin.Email), e.mustLogin(owner.Email), e.mustLogin(other.Email)

	// Before the streams open: raising the admin session to AAL2 later
	// ends the admin's stream at its next session check (D-118).
	e.fresh2FA(admin.ID)
	adminCh := e.openStream(adminC, activityUpdatedSub, nil)
	ownerCh := e.openStream(ownerC, activityUpdatedSub, nil)
	otherCh := e.openStream(otherC, activityUpdatedSub, nil)
	deadline := time.Now().Add(10 * time.Second)
	for e.events.Subscribers(pubsub.RestoreEvents.Name) < 3 || e.events.Subscribers(pubsub.RunEvents.Name) < 3 {
		if time.Now().After(deadline) {
			t.Fatal("the streams did not subscribe")
		}
		time.Sleep(10 * time.Millisecond)
	}

	ref := e.newRun(e.job, &admin.ID)
	for name, ch := range map[string]<-chan sseEv{"admin": adminCh, "owner": ownerCh} {
		var ev activityGQL
		nextData(t, ch, "activityUpdated", &ev)
		if ev.Run == nil || ev.Restore != nil || ev.Run.ID != ref.RunID.String() || ev.Run.Job.ID != e.job.String() {
			t.Fatalf("%s: run event %+v", name, ev)
		}
	}

	var r struct{ ID string }
	e.mustGQL(adminC, restoreArtMut, map[string]any{"in": map[string]any{
		"artifactId": e.art, "targetConnectionId": e.conn, "mode": "NEW_DB", "targetDatabase": "live",
	}}).field(t, "restoreArtifact", &r)
	for name, ch := range map[string]<-chan sseEv{"admin": adminCh, "owner": ownerCh} {
		var ev activityGQL
		for ev.Restore == nil {
			nextData(t, ch, "activityUpdated", &ev)
		}
		if ev.Run != nil || ev.Restore.ID != r.ID || ev.Restore.Status != "QUEUED" {
			t.Fatalf("%s: restore event %+v", name, ev)
		}
	}

	// The owner loses the job: the admin still receives both kinds, the
	// owner and the non-owner nothing.
	if _, err := e.pool.Exec(t.Context(), `DELETE FROM job_owners WHERE job_id = $1 AND user_id = $2`, e.job, owner.ID); err != nil {
		t.Fatal(err)
	}
	drain(ownerCh)
	if _, err := e.svc.Finish(t.Context(), uuid.MustParse(r.ID), restore.StatusFailed, restore.ErrInterrupted); err != nil {
		t.Fatal(err)
	}
	ref2 := e.newRun(e.job, nil)
	gotRestore, gotRun := false, false
	for !gotRestore || !gotRun {
		var ev activityGQL
		nextData(t, adminCh, "activityUpdated", &ev)
		if ev.Restore != nil && ev.Restore.Status == "FAILED" {
			gotRestore = true
		}
		if ev.Run != nil && ev.Run.ID == ref2.RunID.String() {
			gotRun = true
		}
	}
	for name, ch := range map[string]<-chan sseEv{"former owner": ownerCh, "non-owner": otherCh} {
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

// streamStatus opens a stream and returns the response status (the
// stream stays open until the test ends).
func streamStatus(t *testing.T, url, cookie, query string, vars map[string]any) int {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Origin", testBaseURL)
	req.Header.Set("Cookie", auth.SessionCookieName+"="+cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); closeResp(t, resp) })
	return resp.StatusCode
}

// TestStreamBudgetTwoTabs: two tabs of one browser (the same session)
// use the D-197 budget, one shared account stream plus one page stream
// per tab (dashboard, run page), and stay within
// graph.stream_max_per_session (4); only a fifth stream is refused.
func TestStreamBudgetTwoTabs(t *testing.T) {
	e := newRestoreEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	c := e.mustLogin(admin.Email)
	ref := e.newRun(e.job, &admin.ID)

	// Tab 1 (leader): the shared account stream and the dashboard stream.
	account := e.openStream(c, accountEventsSub, nil)
	dashboard := e.openStream(c, activityUpdatedSub, nil)
	// Tab 2 (follower: receives the account events relayed by tab 1):
	// the run page stream.
	runPage := e.openStream(c, runActivitySub, map[string]any{"run": ref.RunID})
	for name, ch := range map[string]<-chan sseEv{"account": account, "dashboard": dashboard, "run page": runPage} {
		if ev, ok := nextEv(ch, 300*time.Millisecond); ok && ev.event != "next" {
			t.Fatalf("%s stream: %+v", name, ev)
		}
	}
	// Headroom for one more page stream (a third tab), then the cap.
	if st := streamStatus(t, e.url, c, activityUpdatedSub, nil); st != http.StatusOK {
		t.Fatalf("fourth stream: status %d", st)
	}
	if st := streamStatus(t, e.url, c, activityUpdatedSub, nil); st != http.StatusTooManyRequests {
		t.Fatalf("fifth stream: status %d, want 429", st)
	}
}
