//go:build integration

package graph_test

import (
	"slices"
	"testing"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/restore"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// Privileged restore targets over GraphQL (task 4.8, D-068): the preview
// shows the verdict and its reasons; a maintainer's request into a
// privileged target is FORBIDDEN, an admin's without the acknowledgement
// VALIDATION, and with it the restore is queued and audited.
func TestRestorePrivilegedTargetGraphQL(t *testing.T) {
	e := newRestoreEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	maint := e.user("maint@example.com", sqlc.UserRoleMaintainer)
	for _, u := range []sqlc.User{admin, maint} {
		e.factors.set(u.ID, true)
	}
	adminC, maintC := e.mustLogin(admin.Email), e.mustLogin(maint.Email)
	e.priv.set(&runnerpb.TargetPrivileges{
		Status: runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED, Reasons: []string{"superuser", "createrole"},
	})

	const previewQ = `query($in: RestorePreviewInput!) { restorePreview(input: $in) {
		crossSource warnings { code } targetPrivileges { status reasons acknowledgementRequired } } }`
	var pv struct {
		CrossSource      bool
		Warnings         []struct{ Code string }
		TargetPrivileges struct {
			Status                  string
			Reasons                 []string
			AcknowledgementRequired bool
		}
	}
	in := map[string]any{"artifactId": e.art, "targetConnectionId": e.conn, "mode": "NEW_DB", "targetDatabase": "copy"}
	e.mustGQL(maintC, previewQ, map[string]any{"in": in}).field(t, "restorePreview", &pv)
	if pv.TargetPrivileges.Status != "PRIVILEGED" || !pv.TargetPrivileges.AcknowledgementRequired ||
		!slices.Equal(pv.TargetPrivileges.Reasons, []string{"superuser", "createrole"}) || pv.CrossSource ||
		!slices.ContainsFunc(pv.Warnings, func(w struct{ Code string }) bool { return w.Code == restore.WarnPrivilegedTarget }) {
		t.Fatalf("preview %+v", pv)
	}
	// One runner call per request: the preview is a single-root field.
	if r := e.gql(maintC, `query($in: RestorePreviewInput!) { a: restorePreview(input: $in) { crossSource } b: restorePreview(input: $in) { crossSource } }`,
		map[string]any{"in": in}); r.code() != "VALIDATION" {
		t.Fatalf("aliased previews: %+v", r.Errors)
	}
	var dbs struct {
		TargetPrivileges struct{ Status string }
	}
	e.mustGQL(maintC, `query($a: UUID!, $c: UUID!) { restoreTargetDatabases(artifactId: $a, targetConnectionId: $c) { targetPrivileges { status } } }`,
		map[string]any{"a": e.art, "c": e.conn}).field(t, "restoreTargetDatabases", &dbs)
	if dbs.TargetPrivileges.Status != "PRIVILEGED" {
		t.Fatalf("database list verdict %+v", dbs)
	}

	e.fresh2FA(maint.ID)
	e.fresh2FA(admin.ID)
	if r := e.gql(maintC, restoreArtMut, map[string]any{"in": in}); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer into a privileged target: %+v", r.Errors)
	}
	acked := map[string]any{"artifactId": e.art, "targetConnectionId": e.conn, "mode": "NEW_DB", "targetDatabase": "copy", "acknowledgePrivilegedTarget": true}
	if r := e.gql(maintC, restoreArtMut, map[string]any{"in": acked}); r.code() != "FORBIDDEN" {
		t.Fatalf("maintainer with the acknowledgement: %+v", r.Errors)
	}
	if r := e.gql(adminC, restoreArtMut, map[string]any{"in": in}); r.code() != "VALIDATION" {
		t.Fatalf("admin without the acknowledgement: %+v", r.Errors)
	}
	var got struct{ ID, Status string }
	e.mustGQL(adminC, restoreArtMut, map[string]any{"in": acked}).field(t, "restoreArtifact", &got)
	if got.Status != "QUEUED" {
		t.Fatalf("admin restore %+v", got)
	}
	var n int
	if err := e.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_log WHERE action = 'restore.requested' AND target_id = $1
		AND details->>'target_privileges' = 'privileged' AND details->>'privileged_target_acknowledged' = 'true'
		AND details->'target_privilege_reasons' ? 'createrole'`, got.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit entry %d %v", n, err)
	}

	// Globals into the privileged role: the acknowledgement as well.
	gIn := map[string]any{"artifactId": e.globals, "targetConnectionId": e.conn, "confirmPhrase": restore.GlobalsConfirmPhrase(e.connName())}
	if r := e.gql(adminC, restoreGlobalsMut, map[string]any{"in": gIn}); r.code() != "VALIDATION" {
		t.Fatalf("globals without the acknowledgement: %+v", r.Errors)
	}
	gIn["acknowledgePrivilegedTarget"] = true
	var g struct{ Kind string }
	e.mustGQL(adminC, restoreGlobalsMut, map[string]any{"in": gIn}).field(t, "restoreGlobals", &g)
	if g.Kind != "GLOBALS" {
		t.Fatalf("globals restore %+v", g)
	}
}
