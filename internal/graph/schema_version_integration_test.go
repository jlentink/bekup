//go:build integration

package graph_test

import (
	"testing"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/runstate"
)

const (
	schemaVersionFields = `schemaVersion { mode preset presetLabel schema customSql }`
	setSchemaVersionMut = `mutation($id: UUID!, $in: JobSchemaVersionInput!) { setJobSchemaVersion(jobId: $id, input: $in) { id ` + schemaVersionFields + ` } }`
	jobSchemaVersionQ   = `query($id: UUID!) { job(id: $id) { id ` + schemaVersionFields + ` } }`
	presetsQ            = `{ schemaVersionPresets { name label description postgresTable mysqlTable engines } }`
)

type schemaVersionGQL struct {
	Mode        string
	Preset      *string
	PresetLabel *string
	Schema      string
	CustomSQL   *string `json:"customSql"`
}

// TestJobSchemaVersionGraphQL (D-210): staff set the capture, a custom
// query needs a fresh step-up, the stored query is staff only, owners can
// neither change it nor list the presets.
func TestJobSchemaVersionGraphQL(t *testing.T) {
	e := newJobEnv(t)
	admin := e.user("admin@example.com", sqlc.UserRoleAdmin)
	maint := e.user("maint@example.com", sqlc.UserRoleMaintainer)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	adminC, maintC, ownerC := e.mustLogin("admin@example.com"), e.mustLogin("maint@example.com"), e.mustLogin("owner@example.com")

	e.freshStepUp(admin.ID)
	var created struct{ Job jobGQL }
	e.mustGQL(adminC, createJobMut, map[string]any{"in": e.createInput("Alpha")}).field(t, "createJob", &created)
	id := created.Job.ID
	e.mustGQL(adminC, assignMut, map[string]any{"id": id, "u": []string{owner.ID.String()}})

	var j struct {
		ID            string
		SchemaVersion schemaVersionGQL
	}
	e.mustGQL(maintC, jobSchemaVersionQ, map[string]any{"id": id}).field(t, "job", &j)
	if j.SchemaVersion.Mode != "NONE" || j.SchemaVersion.Schema != "public" || j.SchemaVersion.Preset != nil {
		t.Fatalf("default: %+v", j.SchemaVersion)
	}

	// Presets: the server's list, staff only.
	var presets []struct {
		Name, Label, Description, PostgresTable, MysqlTable string
		Engines                                             []string
	}
	e.mustGQL(maintC, presetsQ, nil).field(t, "schemaVersionPresets", &presets)
	if len(presets) != 11 || presets[0].Name != "golang-migrate" || presets[0].PostgresTable != "schema_migrations" || len(presets[0].Engines) != 3 {
		t.Fatalf("presets: %+v", presets)
	}
	if r := e.gql(ownerC, presetsQ, nil); r.code() != "FORBIDDEN" {
		t.Fatalf("owner presets: %+v", r.Errors)
	}

	// A preset without a step-up.
	e.staleStepUp(maint.ID)
	e.mustGQL(maintC, setSchemaVersionMut, map[string]any{"id": id, "in": map[string]any{"mode": "PRESET", "preset": "flyway", "schema": "app"}}).
		field(t, "setJobSchemaVersion", &j)
	if j.SchemaVersion.Mode != "PRESET" || *j.SchemaVersion.Preset != "flyway" || *j.SchemaVersion.PresetLabel != "Flyway" || j.SchemaVersion.Schema != "app" {
		t.Fatalf("preset: %+v", j.SchemaVersion)
	}

	// A custom query needs a fresh step-up.
	custom := map[string]any{"id": id, "in": map[string]any{"mode": "CUSTOM", "customSql": "SELECT max(version) FROM public.schema_migrations"}}
	if r := e.gql(maintC, setSchemaVersionMut, custom); r.code() != "STEP_UP_REQUIRED" {
		t.Fatalf("stale custom: %+v", r.Errors)
	}
	e.freshStepUp(maint.ID)
	e.mustGQL(maintC, setSchemaVersionMut, custom).field(t, "setJobSchemaVersion", &j)
	if j.SchemaVersion.Mode != "CUSTOM" || j.SchemaVersion.CustomSQL == nil || *j.SchemaVersion.CustomSQL != "SELECT max(version) FROM public.schema_migrations" {
		t.Fatalf("custom: %+v", j.SchemaVersion)
	}
	bad := map[string]any{"id": id, "in": map[string]any{"mode": "CUSTOM", "customSql": "SELECT 1; DELETE FROM t"}}
	if r := e.gql(maintC, setSchemaVersionMut, bad); r.code() != "VALIDATION" {
		t.Fatalf("invalid custom: %+v", r.Errors)
	}

	// The owner sees the mode, never the query, and cannot change it.
	e.mustGQL(ownerC, jobSchemaVersionQ, map[string]any{"id": id}).field(t, "job", &j)
	if j.SchemaVersion.Mode != "CUSTOM" || j.SchemaVersion.CustomSQL != nil {
		t.Fatalf("owner view: %+v", j.SchemaVersion)
	}
	if r := e.gql(ownerC, setSchemaVersionMut, map[string]any{"id": id, "in": map[string]any{"mode": "NONE"}}); r.code() != "FORBIDDEN" {
		t.Fatalf("owner change: %+v", r.Errors)
	}
	var n int
	if err := e.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_log WHERE action = 'job.version_query_changed'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("audit entries %d %v", n, err)
	}
}

// TestRunItemSchemaVersion: the captured version and its status on the
// run's items, with the run's visibility.
func TestRunItemSchemaVersion(t *testing.T) {
	e := newRunEnv(t)
	e.user("admin@example.com", sqlc.UserRoleAdmin)
	owner := e.user("owner@example.com", sqlc.UserRoleUser)
	e.own(e.jobA, owner.ID)
	ownerC := e.mustLogin("owner@example.com")

	ok, unavailable, none := e.newRun(e.jobA, nil), e.newRun(e.jobA, nil), e.newRun(e.jobA, nil)
	for _, r := range []runstate.Ref{ok, unavailable, none} {
		e.completeRun(r)
	}
	set := func(ref runstate.Ref, v, st any) {
		t.Helper()
		if _, err := e.pool.Exec(t.Context(), `UPDATE run_items SET schema_version = $2, schema_version_status = $3 WHERE run_id = $1`, ref.RunID, v, st); err != nil {
			t.Fatal(err)
		}
	}
	set(ok, "42 (dirty)", "ok")
	set(unavailable, nil, "unavailable")
	const q = `query($id: UUID!) { run(id: $id) { items { schemaVersion schemaVersionStatus } } }`
	for _, tc := range []struct {
		ref   runstate.Ref
		v, st *string
	}{
		{ok, strp("42 (dirty)"), strp("OK")},
		{unavailable, nil, strp("UNAVAILABLE")},
		{none, nil, nil},
	} {
		var run struct {
			Items []struct{ SchemaVersion, SchemaVersionStatus *string }
		}
		e.mustGQL(ownerC, q, map[string]any{"id": tc.ref.RunID}).field(t, "run", &run)
		if len(run.Items) != 1 || !eqStr(run.Items[0].SchemaVersion, tc.v) || !eqStr(run.Items[0].SchemaVersionStatus, tc.st) {
			t.Fatalf("%v: %+v", tc.ref.RunID, run.Items)
		}
	}
	// The database refuses a version without status ok.
	if _, err := e.pool.Exec(t.Context(), `UPDATE run_items SET schema_version = 'x', schema_version_status = 'none' WHERE run_id = $1`, none.RunID); err == nil {
		t.Fatal("a version with status none was stored")
	}
}

func strp(s string) *string { return &s }

func eqStr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
