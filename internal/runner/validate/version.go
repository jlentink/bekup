package validate

import (
	"strings"

	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/schemaversion"
)

// Version is a validated schema-version query of a Dump (D-210).
type Version struct {
	// Preset is set for a preset query.
	Preset *schemaversion.Preset
	// Custom is the validated statement body of a custom query (trimmed),
	// empty for a preset.
	Custom string
	// Schema is the PostgreSQL schema of a preset's table.
	Schema string
}

// VersionQuery validates the version query of a Dump; nil means none. A
// query needs enabled stats (it runs in the stats transaction). The custom
// SQL is re-checked with schemaversion.ValidateCustomSQL (the same strict
// whole-text rules as the server: length, charset, no ";" or comment,
// SELECT/WITH, denied words and functions); the error never quotes it.
func VersionQuery(q *runnerpb.VersionQuery, stats *Stats) (*Version, error) {
	if q == nil {
		return nil, nil
	}
	if stats == nil {
		return nil, fieldError("version_query", "requires stats")
	}
	out := &Version{Schema: schemaversion.DefaultSchema}
	switch v := q.GetQuery().(type) {
	case *runnerpb.VersionQuery_Preset:
		p, ok := schemaversion.Lookup(v.Preset)
		if !ok {
			return nil, fieldError("version_query.preset", "unknown preset")
		}
		out.Preset = p
		if s := q.GetSchema(); s != "" {
			if schemaversion.ValidateSchema(s) != nil {
				return nil, fieldError("version_query.schema", "invalid schema name")
			}
			out.Schema = s
		}
	case *runnerpb.VersionQuery_CustomSql:
		if len(v.CustomSql) > schemaversion.MaxCustomSQLBytes || strings.ContainsRune(v.CustomSql, 0) {
			return nil, fieldError("version_query.custom_sql", "too long or contains NUL")
		}
		body, err := schemaversion.StatementBody(v.CustomSql)
		if err != nil {
			return nil, fieldError("version_query.custom_sql", "not a single SELECT or WITH statement within the rules")
		}
		if q.GetSchema() != "" {
			return nil, fieldError("version_query.schema", "only for presets")
		}
		out.Custom = body
	default:
		return nil, fieldError("version_query", "missing query")
	}
	return out, nil
}
