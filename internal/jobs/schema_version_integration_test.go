//go:build integration

package jobs

import (
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/authz/authztest"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/schemaversion"
)

// TestSetSchemaVersion: staff set the capture (D-210), a custom query
// needs a fresh step-up, owners are refused, every change is audited with
// a digest of the query (never the query) and the worker's job view
// carries the settings.
func TestSetSchemaVersion(t *testing.T) {
	e := newEnv(t)
	j := e.create("nightly")
	if j.SchemaVersion.Mode != schemaversion.ModeNone || j.SchemaVersion.Schema != "public" {
		t.Fatalf("defaults %+v", j.SchemaVersion)
	}

	got, err := e.svc.SetSchemaVersion(e.maint.ctx, e.maint.actor, j.ID, SchemaVersionInput{Mode: schemaversion.ModePreset, Preset: "flyway", Schema: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if sv := got.SchemaVersion; sv.Mode != schemaversion.ModePreset || sv.Preset != "flyway" || sv.Schema != "app" || sv.CustomSQL != "" {
		t.Fatalf("preset %+v", sv)
	}
	a := e.audits(audit.ActionJobVersionQueryChanged)
	if len(a) != 1 || a[0]["mode"] != "preset" || a[0]["preset"] != "flyway" || a[0]["schema"] != "app" || a[0]["previous_mode"] != "none" {
		t.Fatalf("audit %v", a)
	}
	rj, err := runstate.NewStore(e.pool, nil, nil).Job(t.Context(), j.ID)
	if err != nil || rj.SchemaVersion.Mode != "preset" || rj.SchemaVersion.Preset != "flyway" || rj.SchemaVersion.Schema != "app" {
		t.Fatalf("worker view %+v %v", rj.SchemaVersion, err)
	}

	// A custom query needs a fresh step-up.
	const q = "SELECT max(version)::pg_catalog.text FROM public.schema_migrations"
	stale := authztest.Context(t.Context(), authztest.Stale(e.maint.pr))
	if _, err := e.svc.SetSchemaVersion(stale, e.maint.actor, j.ID, SchemaVersionInput{Mode: schemaversion.ModeCustom, CustomSQL: q}); apiCode(err) != apierror.StepUpRequired {
		t.Fatalf("stale custom: %v", err)
	}
	// Off and presets do not.
	if _, err := e.svc.SetSchemaVersion(stale, e.maint.actor, j.ID, SchemaVersionInput{Mode: schemaversion.ModePreset, Preset: "goose"}); err != nil {
		t.Fatalf("stale preset: %v", err)
	}
	got, err = e.svc.SetSchemaVersion(e.admin.ctx, e.admin.actor, j.ID, SchemaVersionInput{Mode: schemaversion.ModeCustom, CustomSQL: q})
	if err != nil {
		t.Fatal(err)
	}
	if sv := got.SchemaVersion; sv.Mode != schemaversion.ModeCustom || sv.CustomSQL != q || sv.Preset != "" {
		t.Fatalf("custom %+v", sv)
	}
	a = e.audits(audit.ActionJobVersionQueryChanged)
	var last map[string]any
	for _, entry := range a {
		if entry["mode"] == "custom" {
			last = entry
		}
	}
	if last == nil || last["custom_sql_sha256"] != sqlDigest(q) || last["custom_sql_changed"] != true {
		t.Fatalf("audit %v", last)
	}
	for _, entry := range a {
		for k, v := range entry {
			if s, ok := v.(string); ok && strings.Contains(s, "schema_migrations") {
				t.Fatalf("the audit holds the query (%s)", k)
			}
		}
	}

	// Owners cannot change it; invalid input is a field error.
	if _, err := e.svc.SetSchemaVersion(e.owner.ctx, e.owner.actor, j.ID, SchemaVersionInput{Mode: schemaversion.ModeNone}); apiCode(err) != apierror.Forbidden {
		t.Fatalf("owner: %v", err)
	}
	for field, in := range map[string]SchemaVersionInput{
		"preset":    {Mode: schemaversion.ModePreset, Preset: "nope"},
		"schema":    {Mode: schemaversion.ModePreset, Preset: "goose", Schema: `a"b`},
		"customSql": {Mode: schemaversion.ModeCustom, CustomSQL: "SELECT 1; DROP TABLE x"},
		"mode":      {Mode: "everything"},
	} {
		_, err := e.svc.SetSchemaVersion(e.admin.ctx, e.admin.actor, j.ID, in)
		if fe := fieldOf(t, err); fe != field {
			t.Fatalf("%s: field %q (%v)", field, fe, err)
		}
	}

	// Off clears the stored query.
	got, err = e.svc.SetSchemaVersion(e.maint.ctx, e.maint.actor, j.ID, SchemaVersionInput{Mode: schemaversion.ModeNone})
	if err != nil || got.SchemaVersion.Mode != schemaversion.ModeNone || got.SchemaVersion.CustomSQL != "" || got.SchemaVersion.Preset != "" {
		t.Fatalf("off %+v %v", got.SchemaVersion, err)
	}
}

// TestCustomVersionQueryRetarget: moving a job with a custom query to
// another connection needs the same fresh step-up as saving one and is
// audited with the retarget flag (the security alert's trigger); other
// edits and preset jobs are unaffected (D-210).
func TestCustomVersionQueryRetarget(t *testing.T) {
	e := newEnv(t)
	j := e.create("nightly")
	other := e.insertConnection("pg-2", "postgres", false)
	stale := authztest.Context(t.Context(), authztest.Stale(e.maint.pr))

	// A preset job moves without a step-up.
	if _, err := e.svc.SetSchemaVersion(e.maint.ctx, e.maint.actor, j.ID, SchemaVersionInput{Mode: schemaversion.ModePreset, Preset: "goose"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Update(stale, e.maint.actor, j.ID, UpdateInput{ConnectionID: &other}); err != nil {
		t.Fatalf("preset move: %v", err)
	}

	const q = "SELECT max(version)::pg_catalog.text FROM public.schema_migrations"
	if _, err := e.svc.SetSchemaVersion(e.maint.ctx, e.maint.actor, j.ID, SchemaVersionInput{Mode: schemaversion.ModeCustom, CustomSQL: q}); err != nil {
		t.Fatal(err)
	}
	back := e.pg
	if _, err := e.svc.Update(stale, e.maint.actor, j.ID, UpdateInput{ConnectionID: &back}); apiCode(err) != apierror.StepUpRequired {
		t.Fatalf("stale custom move: %v", err)
	}
	// Other fields need no step-up.
	name := "renamed"
	if _, err := e.svc.Update(stale, e.maint.actor, j.ID, UpdateInput{Name: &name}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	got, err := e.svc.Update(e.maint.ctx, e.maint.actor, j.ID, UpdateInput{ConnectionID: &back})
	if err != nil || got.Connection.ID != e.pg {
		t.Fatalf("fresh custom move: %+v %v", got.Connection, err)
	}
	flagged := 0
	for _, a := range e.audits(audit.ActionJobUpdated) {
		if a["custom_version_query_retargeted"] == true {
			flagged++
		}
	}
	if flagged != 1 {
		t.Fatalf("%d retarget audits, want 1", flagged)
	}
}
