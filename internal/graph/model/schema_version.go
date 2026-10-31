package model

// JobSchemaVersion is a job's schema-version capture (D-210). The custom
// query is kept unexported: JobSchemaVersion.customSql is a field resolver
// that returns it to admins and maintainers only.
type JobSchemaVersion struct {
	Mode        SchemaVersionMode `json:"mode"`
	Preset      *string           `json:"preset,omitempty"`
	PresetLabel *string           `json:"presetLabel,omitempty"`
	Schema      string            `json:"schema"`
	customSQL   string
}

// NewJobSchemaVersion builds the model with the stored custom query.
func NewJobSchemaVersion(mode SchemaVersionMode, preset, presetLabel *string, schema, customSQL string) *JobSchemaVersion {
	return &JobSchemaVersion{Mode: mode, Preset: preset, PresetLabel: presetLabel, Schema: schema, customSQL: customSQL}
}

// StoredCustomSQL returns the custom query ("" outside CUSTOM). Only the
// customSql field resolver may expose it, after its authorization check.
func (v *JobSchemaVersion) StoredCustomSQL() string { return v.customSQL }
