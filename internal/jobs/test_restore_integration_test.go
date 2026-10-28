//go:build integration

package jobs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/authz/authztest"
	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/scheduler"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// fakeTestProber answers the scratch target probe (D-170).
type fakeTestProber struct {
	mu    sync.Mutex
	verd  map[uuid.UUID]connections.Privileges
	err   error
	calls int
}

func (f *fakeTestProber) ProbeTarget(_ context.Context, _ connections.Actor, id uuid.UUID) (connections.TargetProbe, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return connections.TargetProbe{}, f.err
	}
	p, ok := f.verd[id]
	if !ok {
		p = connections.Privileges{Status: connections.PrivilegeNotPrivileged, Reasons: []string{}}
	}
	return connections.TargetProbe{Privileges: p}, nil
}

func (f *fakeTestProber) set(id uuid.UUID, p connections.Privileges) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verd[id] = p
}

// newTestRestoreEnv is newEnv with a scratch target prober that reports
// every connection as not privileged unless set otherwise.
func newTestRestoreEnv(t *testing.T) (*env, *fakeTestProber) {
	e := newEnv(t)
	p := &fakeTestProber{verd: map[uuid.UUID]connections.Privileges{}}
	e.svc.o.TestTargets = p
	return e, p
}

func TestSetTestRestore(t *testing.T) {
	e, _ := newTestRestoreEnv(t)
	j := e.create("nightly")
	if j.TestRestore.Enabled || j.TestRestore.Target != nil || j.TestRestore.NextAt != nil ||
		j.TestRestore.Schedule.Kind != scheduler.KindCron || j.TestRestore.Schedule.Cron != "0 4 * * 0" {
		t.Fatalf("defaults %+v", j.TestRestore)
	}
	scratch := e.insertConnection("scratch", "postgres", false)

	t.Run("enable", func(t *testing.T) {
		got, err := e.svc.SetTestRestore(e.maint.ctx, e.maint.actor, j.ID, TestRestoreInput{Enabled: true, TargetConnectionID: &scratch})
		if err != nil {
			t.Fatal(err)
		}
		tr := got.TestRestore
		// 2026-03-28 is a Saturday: the next Sunday 04:00 UTC.
		want := time.Date(2026, 3, 29, 4, 0, 0, 0, time.UTC)
		if !tr.Enabled || tr.Target == nil || tr.Target.ID != scratch || tr.NextAt == nil || !tr.NextAt.Equal(want) {
			t.Fatalf("settings %+v (next %v)", tr, tr.NextAt)
		}
		a := e.audits(audit.ActionJobTestRestoreUpdated)
		if len(a) != 1 || a[0]["target_connection_id"] != scratch.String() || a[0]["enabled"] != true {
			t.Fatalf("audit %v", a)
		}
	})
	t.Run("manual schedule is not scheduled", func(t *testing.T) {
		manual := scheduler.Schedule{Kind: scheduler.KindManual}
		got, err := e.svc.SetTestRestore(e.admin.ctx, e.admin.actor, j.ID, TestRestoreInput{Enabled: true, TargetConnectionID: &scratch, Schedule: &manual})
		if err != nil || got.TestRestore.NextAt != nil || got.TestRestore.Schedule.Kind != scheduler.KindManual {
			t.Fatalf("%v %+v", err, got.TestRestore)
		}
	})
	t.Run("validation", func(t *testing.T) {
		for name, in := range map[string]TestRestoreInput{
			"no target":       {Enabled: true},
			"other engine":    {Enabled: true, TargetConnectionID: &e.my},
			"temporary":       {Enabled: true, TargetConnectionID: &e.temp},
			"unknown":         {Enabled: true, TargetConnectionID: &e.dest1},
			"schedule policy": {Enabled: true, TargetConnectionID: &scratch, Schedule: &scheduler.Schedule{Kind: scheduler.KindInterval, Every: 1, Unit: scheduler.UnitMinutes, Anchor: "2026-01-01T00:00", Timezone: "UTC"}},
		} {
			_, err := e.svc.SetTestRestore(e.admin.ctx, e.admin.actor, j.ID, in)
			var fe *serviceutil.FieldError
			if !errors.As(err, &fe) {
				t.Errorf("%s: %v", name, err)
			}
		}
	})
	t.Run("authorization", func(t *testing.T) {
		in := TestRestoreInput{Enabled: false}
		// Backup owners never; staff need a fresh second factor.
		if _, err := e.svc.AssignOwners(e.admin.ctx, e.admin.actor, j.ID, []uuid.UUID{e.owner.actor.UserID}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.SetTestRestore(e.owner.ctx, e.owner.actor, j.ID, in); apiCode(err) != "FORBIDDEN" {
			t.Fatalf("owner: %v", err)
		}
		stale := authztest.Stale(e.maint.pr)
		// Without a fresh passkey/TOTP check (and here without a
		// registered factor) the policy refuses.
		if _, err := e.svc.SetTestRestore(authztest.Context(t.Context(), stale), e.maint.actor, j.ID, in); apiCode(err) != "STEP_UP_REQUIRED" && apiCode(err) != "REQUIRES_2FA_ENROLLMENT" {
			t.Fatalf("stale maintainer: %v", err)
		}
	})
	t.Run("the scratch target blocks the connection's deletion", func(t *testing.T) {
		if _, err := e.svc.SetTestRestore(e.admin.ctx, e.admin.actor, j.ID, TestRestoreInput{Enabled: true, TargetConnectionID: &scratch}); err != nil {
			t.Fatal(err)
		}
		rows, err := e.q.JobsUsingConnection(t.Context(), scratch)
		if err != nil || len(rows) != 1 || rows[0].ID != j.ID {
			t.Fatalf("in use: %v %v", rows, err)
		}
	})
	t.Run("disable keeps the target", func(t *testing.T) {
		got, err := e.svc.SetTestRestore(e.admin.ctx, e.admin.actor, j.ID, TestRestoreInput{Enabled: false, TargetConnectionID: &scratch})
		if err != nil || got.TestRestore.Enabled || got.TestRestore.NextAt != nil || got.TestRestore.Target == nil {
			t.Fatalf("%v %+v", err, got.TestRestore)
		}
	})
}

func TestSetTestRestorePrivilegedTarget(t *testing.T) {
	e, prober := newTestRestoreEnv(t)
	j := e.create("nightly")
	scratch := e.insertConnection("scratch", "postgres", false)
	priv := connections.Privileges{Status: connections.PrivilegePrivileged, Reasons: []string{"superuser"}}
	prober.set(scratch, priv)
	in := TestRestoreInput{Enabled: true, TargetConnectionID: &scratch}

	t.Run("maintainers are forbidden", func(t *testing.T) {
		acked := in
		acked.AcknowledgePrivilegedTarget = true
		if _, err := e.svc.SetTestRestore(e.maint.ctx, e.maint.actor, j.ID, acked); !errors.Is(err, ErrPrivilegedTargetAdminOnly) {
			t.Fatalf("maintainer: %v", err)
		}
	})
	t.Run("admins need the acknowledgement", func(t *testing.T) {
		_, err := e.svc.SetTestRestore(e.admin.ctx, e.admin.actor, j.ID, in)
		var fe *serviceutil.FieldError
		if !errors.As(err, &fe) || fe.Field != "acknowledgePrivilegedTarget" {
			t.Fatalf("admin without ack: %v", err)
		}
		if len(e.audits(audit.ActionJobTestRestoreUpdated)) != 0 {
			t.Fatal("refused changes must not be audited as updates")
		}
	})
	t.Run("an acknowledged admin designation is stored, audited and alerted", func(t *testing.T) {
		acked := in
		acked.AcknowledgePrivilegedTarget = true
		got, err := e.svc.SetTestRestore(e.admin.ctx, e.admin.actor, j.ID, acked)
		if err != nil {
			t.Fatal(err)
		}
		tr := got.TestRestore
		if tr.TargetPrivileges == nil || tr.TargetPrivileges.Status != connections.PrivilegePrivileged ||
			tr.PrivilegedAckAt == nil || tr.PrivilegedAckBy == nil || *tr.PrivilegedAckBy != e.admin.actor.UserID {
			t.Fatalf("settings %+v", tr)
		}
		a := e.audits(audit.ActionJobTestRestoreUpdated)
		if len(a) != 1 || a[0]["target_privileges"] != "privileged" || a[0]["privileged_target_acknowledged"] != true {
			t.Fatalf("audit %v", a)
		}
		e.mu.Lock()
		alerts := append([]Alert(nil), e.alerts...)
		e.mu.Unlock()
		if len(alerts) != 1 || alerts[0].Kind != AlertTestRestorePrivilegedTarget || alerts[0].JobID != j.ID {
			t.Fatalf("alerts %+v", alerts)
		}
		row, err := e.q.GetJob(t.Context(), j.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !TestTargetCovered(&row, priv) {
			t.Fatal("the stored acknowledgement must cover the verdict")
		}
		if TestTargetCovered(&row, connections.Privileges{Status: connections.PrivilegePrivileged, Reasons: []string{"superuser", "createrole"}}) {
			t.Fatal("a new reason must not be covered")
		}
	})
	t.Run("a not-privileged target clears the acknowledgement", func(t *testing.T) {
		plain := e.insertConnection("plain", "postgres", false)
		got, err := e.svc.SetTestRestore(e.maint.ctx, e.maint.actor, j.ID, TestRestoreInput{Enabled: true, TargetConnectionID: &plain})
		if err != nil {
			t.Fatal(err)
		}
		if got.TestRestore.PrivilegedAckAt != nil || got.TestRestore.TargetPrivileges.Status != connections.PrivilegeNotPrivileged {
			t.Fatalf("settings %+v", got.TestRestore)
		}
	})
	t.Run("unknown and unprobed targets are gated", func(t *testing.T) {
		unknown := e.insertConnection("unknown", "postgres", false)
		prober.set(unknown, connections.Privileges{Status: connections.PrivilegeUnknown, Reasons: []string{"roles_granted"}})
		if _, err := e.svc.SetTestRestore(e.maint.ctx, e.maint.actor, j.ID, TestRestoreInput{Enabled: true, TargetConnectionID: &unknown}); !errors.Is(err, ErrPrivilegedTargetAdminOnly) {
			t.Fatalf("unknown: %v", err)
		}
		prober.mu.Lock()
		prober.err = &connections.NotReadyError{Reason: "pin required"}
		prober.mu.Unlock()
		defer func() { prober.mu.Lock(); prober.err = nil; prober.mu.Unlock() }()
		if _, err := e.svc.SetTestRestore(e.maint.ctx, e.maint.actor, j.ID, TestRestoreInput{Enabled: true, TargetConnectionID: &unknown}); !errors.Is(err, ErrPrivilegedTargetAdminOnly) {
			t.Fatalf("not ready: %v", err)
		}
		e.svc.o.TestTargets = nil
		defer func() { e.svc.o.TestTargets = prober }()
		if _, err := e.svc.SetTestRestore(e.maint.ctx, e.maint.actor, j.ID, TestRestoreInput{Enabled: true, TargetConnectionID: &unknown}); !errors.Is(err, ErrPrivilegedTargetAdminOnly) {
			t.Fatalf("no prober: %v", err)
		}
	})
	t.Run("probe failures are returned", func(t *testing.T) {
		prober.mu.Lock()
		prober.err = connections.ErrRunnerUnavailable
		prober.mu.Unlock()
		defer func() { prober.mu.Lock(); prober.err = nil; prober.mu.Unlock() }()
		if _, err := e.svc.SetTestRestore(e.admin.ctx, e.admin.actor, j.ID, TestRestoreInput{Enabled: true, TargetConnectionID: &scratch, AcknowledgePrivilegedTarget: true}); !errors.Is(err, connections.ErrRunnerUnavailable) {
			t.Fatalf("runner down: %v", err)
		}
	})
	t.Run("removing the target needs no probe", func(t *testing.T) {
		prober.mu.Lock()
		before := prober.calls
		prober.mu.Unlock()
		got, err := e.svc.SetTestRestore(e.maint.ctx, e.maint.actor, j.ID, TestRestoreInput{Enabled: false})
		if err != nil || got.TestRestore.Target != nil || got.TestRestore.TargetPrivileges != nil {
			t.Fatalf("%v %+v", err, got.TestRestore)
		}
		prober.mu.Lock()
		defer prober.mu.Unlock()
		if prober.calls != before {
			t.Fatal("removing the target probed")
		}
	})
}
