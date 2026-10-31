package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/schemaversion"
)

// Schema version (D-210). A job may capture the schema (migration) version
// of every database it backs up: off (default), a preset of
// internal/schemaversion (the job stores only its name; the SQL lives in
// Go) or a custom query (a single SELECT/WITH statement of at most 2 KiB).
// The runner reads it in the dump's stats transaction; a failed capture is
// an item warning that never changes the run status.
//
// Changing it is job.version_query_edit (admins and maintainers); saving a
// custom query also needs job.version_query_custom_edit (a fresh step-up),
// because the query runs on the target with the backup role's rights.
// Every change is audited as job.version_query_changed with the mode, the
// preset, the schema and the SHA-256 of a custom query (never the query).

// SchemaVersionSettings are a job's schema-version capture. CustomSQL is
// for staff only (the API shows it with job.version_query_edit).
type SchemaVersionSettings struct {
	Mode schemaversion.Mode
	// Preset is set in ModePreset.
	Preset string
	// Schema is the PostgreSQL schema of the preset's table.
	Schema string
	// CustomSQL is set in ModeCustom.
	CustomSQL string
}

// SchemaVersionInput replaces a job's schema-version capture. Preset is
// required for ModePreset, CustomSQL for ModeCustom; Schema (preset only)
// defaults to public.
type SchemaVersionInput struct {
	Mode      schemaversion.Mode
	Preset    string
	Schema    string
	CustomSQL string
}

// schemaVersionView reads the settings of a row.
func schemaVersionView(row *sqlc.Job) SchemaVersionSettings {
	out := SchemaVersionSettings{Mode: schemaversion.Mode(row.VersionQueryMode), Schema: row.VersionSchema}
	if row.VersionPreset != nil {
		out.Preset = *row.VersionPreset
	}
	if row.VersionCustomSql != nil {
		out.CustomSQL = *row.VersionCustomSql
	}
	if !out.Mode.Valid() {
		out.Mode = schemaversion.ModeNone
	}
	return out
}

// checkSchemaVersion validates the input and returns the stored values.
func checkSchemaVersion(in SchemaVersionInput) (SchemaVersionInput, error) {
	out := SchemaVersionInput{Mode: in.Mode, Schema: schemaversion.DefaultSchema}
	switch in.Mode {
	case schemaversion.ModeNone:
	case schemaversion.ModePreset:
		if schemaversion.ValidatePreset(in.Preset) != nil {
			return out, fieldErr("preset", "choose one of the presets")
		}
		out.Preset = in.Preset
		if in.Schema != "" {
			if err := schemaversion.ValidateSchema(in.Schema); err != nil {
				return out, fieldErr("schema", "%s", err.Error())
			}
			out.Schema = in.Schema
		}
	case schemaversion.ModeCustom:
		if err := schemaversion.ValidateCustomSQL(in.CustomSQL); err != nil {
			var se *schemaversion.SQLError
			if errors.As(err, &se) {
				return out, fieldErr("customSql", "%s", se.Message)
			}
			return out, fieldErr("customSql", "the query is not allowed")
		}
		out.CustomSQL = in.CustomSQL
	default:
		return out, fieldErr("mode", "unknown mode")
	}
	return out, nil
}

// sqlDigest is the audit form of a custom query.
func sqlDigest(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// SetSchemaVersion replaces a job's schema-version capture
// (job.version_query_edit; a custom query also job.version_query_custom_edit
// with a fresh step-up). Audited as job.version_query_changed.
func (s *Service) SetSchemaVersion(ctx context.Context, actor Actor, id uuid.UUID, in SchemaVersionInput) (Job, error) {
	if err := s.authorize(ctx, actor, authz.OpJobVersionQueryEdit, authz.Job{ID: id}); err != nil {
		return Job{}, err
	}
	if in.Mode == schemaversion.ModeCustom {
		if err := s.authorize(ctx, actor, authz.OpJobVersionQueryCustomEdit, authz.Job{ID: id}); err != nil {
			return Job{}, err
		}
	}
	clean, err := checkSchemaVersion(in)
	if err != nil {
		return Job{}, err
	}
	var out Job
	err = db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		cur, err := q.GetJobForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("jobs: load: %w", err)
		}
		prev := schemaVersionView(&cur)
		params := sqlc.SetJobVersionQueryParams{
			ID: id, Mode: sqlc.JobVersionQueryMode(clean.Mode), SchemaName: clean.Schema, UpdatedBy: &actor.UserID,
		}
		if clean.Preset != "" {
			params.Preset = &clean.Preset
		}
		if clean.CustomSQL != "" {
			params.CustomSql = &clean.CustomSQL
		}
		row, err := q.SetJobVersionQuery(ctx, params)
		if err != nil {
			return fmt.Errorf("jobs: set the schema-version query: %w", err)
		}
		details := audit.Details{
			"name": cur.Name, "mode": string(clean.Mode), "previous_mode": string(prev.Mode),
			"custom_sql_changed": clean.CustomSQL != prev.CustomSQL,
		}
		if clean.Mode == schemaversion.ModePreset {
			details["preset"], details["schema"] = clean.Preset, clean.Schema
		}
		if clean.Mode == schemaversion.ModeCustom {
			details["custom_sql_sha256"], details["custom_sql_bytes"] = sqlDigest(clean.CustomSQL), len(clean.CustomSQL)
		}
		if err := s.record(ctx, q, actor, audit.ActionJobVersionQueryChanged, id, details); err != nil {
			return err
		}
		out, err = s.view(ctx, q, &row)
		return err
	})
	if err != nil {
		return Job{}, err
	}
	return out, nil
}
