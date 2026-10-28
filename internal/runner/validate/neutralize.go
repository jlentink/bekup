package validate

import (
	"context"

	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runnerpb"
)

// pgMaintenanceDB is the maintenance database of PostgreSQL targets (the
// engine.Target default; the runner never sets another).
const pgMaintenanceDB = "postgres"

// NeutralizeRestoredDatabase validates a NeutralizeRestoredDatabaseRequest
// (PostgreSQL only) and derives the database to harden: the overwrite's
// staging database <database>__bekup_restore_<temp_id>, the scratch
// database bekup_test_<temp_id>, or a new database that is neither a bekup
// temporary nor a scratch name. The operation additionally refuses a
// database the connection's role does not own (checked in the session).
func (v *Validator) NeutralizeRestoredDatabase(ctx context.Context, req *runnerpb.NeutralizeRestoredDatabaseRequest) (*DatabaseOp, error) {
	if err := checkEnvelope(req, MaxRequestBytes); err != nil {
		return nil, err
	}
	if err := checkRunContext(req.GetCtx()); err != nil {
		return nil, err
	}
	t, err := v.target(ctx, "target.", req.GetTarget())
	if err != nil {
		return nil, err
	}
	if t.Kind != engine.KindPostgres {
		return nil, fieldError("target.engine", "neutralizing a restored database is PostgreSQL only")
	}
	var name string
	switch req.GetKind() {
	case runnerpb.NeutralizeKind_NEUTRALIZE_KIND_RESTORE_STAGING:
		if err := database(t, req.GetDatabase(), true); err != nil {
			return nil, err
		}
		if engine.IsBekupTempDatabase(req.GetDatabase()) || engine.IsReservedScratchName(req.GetDatabase()) {
			return nil, fieldError("database", "must not be a bekup temporary or scratch database")
		}
		if err := engine.ValidateRestoreID(req.GetTempId()); err != nil {
			return nil, fieldError("temp_id", "must be [a-z0-9]{1,26}")
		}
		name, err = engine.TempDatabaseName(req.GetDatabase(), req.GetTempId())
		if err != nil || !engine.IsBekupTempDatabase(name) || t.Engine.IsSystemDatabase(name) {
			return nil, fieldError("temp_id", "cannot derive the temporary name")
		}
	case runnerpb.NeutralizeKind_NEUTRALIZE_KIND_NEW_DATABASE:
		if err := database(t, req.GetDatabase(), true); err != nil {
			return nil, err
		}
		if engine.IsBekupTempDatabase(req.GetDatabase()) || engine.IsReservedScratchName(req.GetDatabase()) {
			return nil, fieldError("database", "must not be a bekup temporary or scratch database")
		}
		// Nor the maintenance database the runner's tools connect to
		// (restores never create it).
		if req.GetDatabase() == pgMaintenanceDB {
			return nil, fieldError("database", "must not be the maintenance database")
		}
		if req.GetTempId() != "" {
			return nil, fieldError("temp_id", "must be empty for a new database")
		}
		name = req.GetDatabase()
	case runnerpb.NeutralizeKind_NEUTRALIZE_KIND_SCRATCH:
		if req.GetDatabase() != "" {
			return nil, fieldError("database", "must be empty for a scratch database")
		}
		if name, err = scratchName(t, req.GetTempId()); err != nil {
			return nil, err
		}
	default:
		return nil, fieldError("kind", "unknown or unspecified kind")
	}
	return &DatabaseOp{Target: t, Database: name}, nil
}
