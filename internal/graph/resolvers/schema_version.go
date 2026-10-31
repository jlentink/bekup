package resolvers

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/schemaversion"
)

// Schema version (D-210): the job setting, the presets and the per-item
// result. internal/jobs validates, authorizes (a custom query needs
// job.version_query_custom_edit) and audits.

func schemaVersionModeModel(m schemaversion.Mode) model.SchemaVersionMode {
	switch m {
	case schemaversion.ModePreset:
		return model.SchemaVersionModePreset
	case schemaversion.ModeCustom:
		return model.SchemaVersionModeCustom
	default:
		return model.SchemaVersionModeNone
	}
}

func schemaVersionModeFromModel(m model.SchemaVersionMode) schemaversion.Mode {
	switch m {
	case model.SchemaVersionModePreset:
		return schemaversion.ModePreset
	case model.SchemaVersionModeCustom:
		return schemaversion.ModeCustom
	case model.SchemaVersionModeNone:
		return schemaversion.ModeNone
	default:
		return ""
	}
}

// schemaVersionStatusModel maps a stored item status (nil: none captured).
func schemaVersionStatusModel(s *string) *model.SchemaVersionStatus {
	if s == nil {
		return nil
	}
	st := model.SchemaVersionStatus(strings.ToUpper(*s))
	if !st.IsValid() {
		st = model.SchemaVersionStatusUnavailable
	}
	return &st
}

// jobSchemaVersionModel is Job.schemaVersion; the custom query is only
// reachable through the customSql field resolver.
func jobSchemaVersionModel(sv *jobs.SchemaVersionSettings) *model.JobSchemaVersion {
	var preset, label *string
	if sv.Mode == schemaversion.ModePreset && sv.Preset != "" {
		p := sv.Preset
		preset = &p
		if ps, ok := schemaversion.Lookup(sv.Preset); ok {
			l := ps.Label
			label = &l
		}
	}
	custom := ""
	if sv.Mode == schemaversion.ModeCustom {
		custom = sv.CustomSQL
	}
	schema := sv.Schema
	if schema == "" {
		schema = schemaversion.DefaultSchema
	}
	return model.NewJobSchemaVersion(schemaVersionModeModel(sv.Mode), preset, label, schema, custom)
}

// jobSchemaVersionCustomSQL is JobSchemaVersion.customSql: admins and
// maintainers only, null otherwise.
func (r *Resolver) jobSchemaVersionCustomSQL(ctx context.Context, obj *model.JobSchemaVersion) (*string, error) {
	if obj == nil || obj.StoredCustomSQL() == "" {
		return nil, nil
	}
	if err := r.Authz.Authorize(ctx, authz.OpJobVersionQueryEdit); err != nil {
		return nil, nil //nolint:nilerr // not visible to this role: null, not an error
	}
	s := obj.StoredCustomSQL()
	return &s, nil
}

func schemaVersionPresetsModel() []model.SchemaVersionPreset {
	ps := schemaversion.Presets()
	out := make([]model.SchemaVersionPreset, 0, len(ps))
	for _, p := range ps {
		m := model.SchemaVersionPreset{
			Name: p.Name, Label: p.Label, Description: p.Description,
			PostgresTable: p.QueryFor(engine.KindPostgres).Table, MysqlTable: p.QueryFor(engine.KindMySQL).Table,
		}
		for _, k := range p.Engines() {
			m.Engines = append(m.Engines, engineModel(k))
		}
		out = append(out, m)
	}
	return out
}

func (r *Resolver) setJobSchemaVersion(ctx context.Context, jobID uuid.UUID, in model.JobSchemaVersionInput) (*model.Job, error) {
	svc, actor, err := r.jobCaller(ctx)
	if err != nil {
		return nil, err
	}
	si := jobs.SchemaVersionInput{Mode: schemaVersionModeFromModel(in.Mode)}
	if in.Preset != nil {
		si.Preset = *in.Preset
	}
	if in.Schema != nil {
		si.Schema = *in.Schema
	}
	if in.CustomSQL != nil {
		si.CustomSQL = *in.CustomSQL
	}
	j, err := svc.SetSchemaVersion(ctx, actor, jobID, si)
	if err != nil {
		return nil, jobError(err)
	}
	return jobModel(&j), nil
}
