//go:build integration

package connections

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// TestLastTestPersisted covers the stored test outcome (D-187): results
// and the privilege verdict are stored without bumping updated_at or
// writing audit entries, settings changes mark them stale, calls without
// a verdict on the target store nothing, and a connection that changed
// during its test keeps its previous result.
func TestLastTestPersisted(t *testing.T) {
	e := newSvcEnv(t)
	ctx := t.Context()
	c, err := e.svc.Create(ctx, e.actor, CreateInput{
		Name: "Legacy", Engine: engine.KindMariaDB, VersionKey: "10.11", ImageKey: "10.11", Host: "10.1.2.3", Username: "root", TLSMode: TLSDisabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.LastTest != nil {
		t.Fatalf("new connection has a last test: %+v", c.LastTest)
	}
	auditBefore := e.allAudit(t)

	e.runner.probe = &runnerpb.ProbeResponse{
		ServerVersion: "10.11.6", DetectedVersionKey: "10.11", VersionKeyCompatible: true, Databases: []string{"app"},
		Privileges: &runnerpb.TargetPrivileges{Status: runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED, Reasons: []string{"global:FILE", "global:SUPER"}},
	}
	if res, err := e.svc.Test(ctx, e.actor, c.ID); err != nil || !res.OK {
		t.Fatalf("test: %v %+v", err, res)
	}
	got, err := e.svc.Get(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	lt := got.LastTest
	if lt == nil || !lt.OK || lt.Code != "" || lt.Stale || lt.Privileges == nil || lt.Privileges.Status != PrivilegePrivileged ||
		len(lt.Privileges.Reasons) != 2 || lt.Privileges.Reasons[0] != "global:FILE" {
		t.Fatalf("last test after success: %+v", lt)
	}
	if !got.UpdatedAt.Equal(c.UpdatedAt) {
		t.Fatalf("storing the result bumped updated_at: %v -> %v", c.UpdatedAt, got.UpdatedAt)
	}
	if e.allAudit(t) != auditBefore {
		t.Fatal("storing a test result wrote audit entries")
	}

	// A target failure: the normalized code, no verdict.
	e.runner.probeErr = runnerpb.ReasonAuthFailed.Status()
	if res, err := e.svc.Test(ctx, e.actor, c.ID); err != nil || res.OK {
		t.Fatalf("failing test: %v %+v", err, res)
	}
	got = mustGet(t, e, c.ID)
	if lt := got.LastTest; lt == nil || lt.OK || lt.Code != string(runnerpb.ReasonAuthFailed) || lt.Privileges != nil || !lt.At.After(c.CreatedAt) {
		t.Fatalf("last test after failure: %+v", got.LastTest)
	}
	failedAt := got.LastTest.At

	// No verdict on the target (runner unavailable): nothing stored.
	e.runner.probeErr = status.Error(codes.Unavailable, "down")
	if _, err := e.svc.Test(ctx, e.actor, c.ID); !errors.Is(err, ErrRunnerUnavailable) {
		t.Fatalf("unavailable: %v", err)
	}
	got = mustGet(t, e, c.ID)
	if !got.LastTest.At.Equal(failedAt) || got.LastTest.Code != string(runnerpb.ReasonAuthFailed) {
		t.Fatalf("runner unavailable changed the last test: %+v", got.LastTest)
	}
	e.runner.probeErr = nil

	// A settings change marks the result stale; a new test refreshes it.
	if _, err := e.svc.Update(ctx, e.actor, c.ID, UpdateInput{Name: ptr("Legacy 2")}); err != nil {
		t.Fatal(err)
	}
	got = mustGet(t, e, c.ID)
	if got.LastTest == nil || !got.LastTest.Stale {
		t.Fatalf("not stale after an update: %+v", got.LastTest)
	}
	if _, err := e.svc.Test(ctx, e.actor, c.ID); err != nil {
		t.Fatal(err)
	}
	got = mustGet(t, e, c.ID)
	if got.LastTest == nil || got.LastTest.Stale || !got.LastTest.OK {
		t.Fatalf("after a fresh test: %+v", got.LastTest)
	}
	okAt := got.LastTest.At

	// Changed while the test ran: the result is not stored.
	e.runner.probeErr = runnerpb.ReasonUnreachable.Status()
	e.runner.onProbe = func() {
		if _, err := e.svc.Update(ctx, e.actor, c.ID, UpdateInput{Name: ptr("Legacy 3")}); err != nil {
			t.Error(err)
		}
	}
	if res, err := e.svc.Test(ctx, e.actor, c.ID); err != nil || res.OK {
		t.Fatalf("racing test: %v %+v", err, res)
	}
	e.runner.onProbe, e.runner.probeErr = nil, nil
	got = mustGet(t, e, c.ID)
	if !got.LastTest.At.Equal(okAt) || !got.LastTest.OK || !got.LastTest.Stale {
		t.Fatalf("a test racing an update was stored: %+v", got.LastTest)
	}
}

func mustGet(t *testing.T, e *svcEnv, id uuid.UUID) Connection {
	t.Helper()
	v, err := e.svc.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
