//go:build integration

package restore_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/restore"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// alertLog records the restore security alerts.
type alertLog struct {
	mu     sync.Mutex
	alerts []restore.Alert
}

func (l *alertLog) add(_ context.Context, a restore.Alert) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.alerts = append(l.alerts, a)
}

func (l *alertLog) kinds() []restore.AlertKind {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]restore.AlertKind, 0, len(l.alerts))
	for _, a := range l.alerts {
		out = append(out, a.Kind)
	}
	return out
}

// Privileged restore targets (task 4.8, D-068): the preview shows the
// verdict, a request into a privileged or unknown target role needs an
// admin (FORBIDDEN otherwise) and the acknowledgement (VALIDATION
// otherwise), is audited with the reasons and raises a security alert; a
// request probes the target afresh (no cached verdict).
func TestAPIPrivilegedTarget(t *testing.T) {
	alerts := &alertLog{}
	e := newAPIEnv(t, func(o *restore.Options) { o.Alert = alerts.add })
	j, s := e.pgJob()
	pg2 := e.insertConn("postgres", false)
	admin, maint, owner := e.as(e.admin, auth.RoleAdmin), e.as(e.maint, auth.RoleMaintainer), e.as(e.owner, auth.RoleUser)
	privileged := &runnerpb.TargetPrivileges{
		Status:  runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED,
		Reasons: []string{"superuser", "member_of:pg_execute_server_program"},
	}

	// Not privileged: no warning, no acknowledgement needed.
	p, err := e.api.Preview(maint, newDB(s.artifact, pg2, "plain_copy"))
	if err != nil || p.Privileges.Status != connections.PrivilegeNotPrivileged || hasWarning(p.Warnings, restore.WarnPrivilegedTarget) ||
		!p.CrossSource || !hasWarning(p.Warnings, restore.WarnCrossSource) {
		t.Fatalf("plain preview %+v %v", p, err)
	}
	if _, _, err := e.api.Request(maint, newDB(s.artifact, pg2, "plain_copy")); err != nil {
		t.Fatalf("maintainer into a plain target: %v", err)
	}

	// Privileged: the preview (any role allowed to restore) shows the
	// verdict and the reasons.
	e.runner.setPrivileges(privileged)
	calls := e.runner.calls
	if _, _, err := e.api.Request(maint, newDB(s.artifact, pg2, "copy1")); !isCode(err, apierror.Forbidden) {
		t.Fatalf("maintainer into a privileged target: %v", err)
	}
	if e.runner.calls != calls+1 {
		t.Fatalf("the request did not probe afresh (%d -> %d)", calls, e.runner.calls)
	}
	p, err = e.api.Preview(maint, newDB(s.artifact, pg2, "copy1"))
	if err != nil || p.Privileges.Status != connections.PrivilegePrivileged || !p.Privileges.Gated() ||
		!slices.Equal(p.Privileges.Reasons, privileged.Reasons) || !hasWarning(p.Warnings, restore.WarnPrivilegedTarget) {
		t.Fatalf("privileged preview %+v %v", p, err)
	}
	if _, _, err := e.api.Request(owner, newDB(s.artifact, j.conn, "owner_copy")); !isCode(err, apierror.Forbidden) {
		t.Fatalf("owner into a privileged target: %v", err)
	}
	// An acknowledgement does not help a non-admin.
	in := newDB(s.artifact, pg2, "copy1")
	in.AcknowledgePrivilegedTarget = true
	if _, _, err := e.api.Request(maint, in); !isCode(err, apierror.Forbidden) {
		t.Fatalf("maintainer with acknowledgement: %v", err)
	}
	if _, _, err := e.api.Request(admin, newDB(s.artifact, pg2, "copy1")); fieldOf(err) != "acknowledgePrivilegedTarget" {
		t.Fatalf("admin without acknowledgement: %v", err)
	}
	if _, _, err := e.api.Request(e.stepUpOnly(e.admin, auth.RoleAdmin), in); !isCode(err, apierror.StepUpRequired) {
		t.Fatalf("admin without a fresh factor: %v", err)
	}
	if n := e.count(`SELECT count(*) FROM restores`); n != 1 {
		t.Fatalf("refused requests created restores: %d", n)
	}
	r, _, err := e.api.Request(admin, in)
	if err != nil {
		t.Fatalf("admin with acknowledgement: %v", err)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'restore.requested' AND target_id = $1
		AND details->>'target_privileges' = 'privileged' AND details->>'privileged_target_acknowledged' = 'true'
		AND details->>'cross_source' = 'true' AND details->'target_privilege_reasons' ? 'superuser'
		AND details->'warnings' ? 'privileged_target'`, r.ID.String()); n != 1 {
		t.Fatalf("audit entry of the privileged restore: %d", n)
	}
	if got := alerts.kinds(); !slices.Equal(got, []restore.AlertKind{restore.AlertPrivilegedTarget}) {
		t.Fatalf("alerts %v", got)
	}
	// The verdict and the acknowledgement are stored for the worker's
	// re-check.
	if r.TargetPrivileges == nil || *r.TargetPrivileges != "privileged" || !r.PrivilegedTargetAcknowledged ||
		!slices.Contains(r.TargetPrivilegeReasons, "superuser") {
		t.Fatalf("stored verdict %v %v %v", r.TargetPrivileges, r.TargetPrivilegeReasons, r.PrivilegedTargetAcknowledged)
	}
	if _, err := e.pool.Exec(t.Context(), "UPDATE restores SET privileged_target_acknowledged = false WHERE id = $1", r.ID); err == nil {
		t.Fatal("the acknowledgement of a restore could be changed")
	}

	// Unknown (and a runner without a verdict) is treated as privileged.
	for _, v := range []*runnerpb.TargetPrivileges{
		{Status: runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_UNKNOWN, Reasons: []string{"roles_granted"}},
		{},
		{Status: runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_NOT_PRIVILEGED, Reasons: []string{"odd"}},
	} {
		e.runner.setPrivileges(v)
		if _, _, err := e.api.Request(maint, newDB(s.artifact, pg2, "copy2")); !isCode(err, apierror.Forbidden) {
			t.Fatalf("maintainer into an unknown target (%v): %v", v, err)
		}
		p, err := e.api.Preview(admin, newDB(s.artifact, pg2, "copy2"))
		if err != nil || p.Privileges.Status != connections.PrivilegeUnknown || len(p.Privileges.Reasons) == 0 {
			t.Fatalf("unknown preview (%v) %+v %v", v, p.Privileges, err)
		}
	}

	// A plain role on the original connection: no gate, not cross-source.
	e.runner.setPrivileges(nil)
	r, p, err = e.api.Request(owner, newDB(s.artifact, j.conn, "owner_copy"))
	if err != nil || p.CrossSource || hasWarning(p.Warnings, restore.WarnCrossSource) {
		t.Fatalf("owner into a plain original target: %v %+v", err, p)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'restore.requested' AND target_id = $1
		AND details->>'target_privileges' = 'not_privileged' AND details->>'privileged_target_acknowledged' = 'false'
		AND details->>'cross_source' = 'false'`, r.ID.String()); n != 1 || r.PrivilegedTargetAcknowledged || r.TargetPrivileges == nil || *r.TargetPrivileges != "not_privileged" {
		t.Fatalf("audit entry of the plain restore: %d", n)
	}
	if len(alerts.kinds()) != 1 {
		t.Fatalf("alerts %v", alerts.kinds())
	}
}

// Globals restores (admins only) into a privileged role need the
// acknowledgement as well.
func TestAPIPrivilegedGlobals(t *testing.T) {
	alerts := &alertLog{}
	e := newAPIEnv(t, func(o *restore.Options) { o.Alert = alerts.add })
	admin := e.as(e.admin, auth.RoleAdmin)
	gj := e.job(jobkeys.ModePassphrase, e.diskID)
	gs := e.seedAs(gj, e.diskID, e.disk, []byte("CREATE ROLE reader;\n"), time.Now(),
		seedSpec{kind: pipeline.KindGlobals, format: pipeline.FormatSQL, engine: "postgres", versionKey: "17"})
	e.exec(`UPDATE connections SET tls_mode = 'unverified' WHERE id = $1`, gj.conn)
	c, err := e.conns.GetForRestore(t.Context(), gj.conn)
	if err != nil {
		t.Fatal(err)
	}
	e.runner.setPrivileges(&runnerpb.TargetPrivileges{Status: runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED, Reasons: []string{"superuser"}})
	gin := restore.GlobalsInput{ArtifactID: gs.artifact, TargetConnectionID: gj.conn, ConfirmPhrase: restore.GlobalsConfirmPhrase(c.Name)}
	p, err := e.api.PreviewGlobals(admin, gin)
	if err != nil || p.Privileges.Status != connections.PrivilegePrivileged || !hasWarning(p.Warnings, restore.WarnPrivilegedTarget) || p.CrossSource {
		t.Fatalf("globals preview %+v %v", p, err)
	}
	if _, _, err := e.api.RequestGlobals(admin, gin); fieldOf(err) != "acknowledgePrivilegedTarget" {
		t.Fatalf("globals without acknowledgement: %v", err)
	}
	gin.AcknowledgePrivilegedTarget = true
	r, _, err := e.api.RequestGlobals(admin, gin)
	if err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'restore.requested' AND target_id = $1
		AND details->>'target_privileges' = 'privileged' AND details->>'privileged_target_acknowledged' = 'true'`, r.ID.String()); n != 1 {
		t.Fatalf("audit entry: %d", n)
	}
	if got := alerts.kinds(); !slices.Equal(got, []restore.AlertKind{restore.AlertPrivilegedTarget}) {
		t.Fatalf("alerts %v", got)
	}
}
