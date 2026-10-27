package db

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jlentink/bekup/internal/db/migrations"
	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Schema version errors returned (wrapped in a *SchemaVersionError) by
// CheckSchemaVersion.
var (
	// ErrSchemaBehind: migrations embedded in this binary are not applied yet.
	ErrSchemaBehind = errors.New("database schema is behind this bekup version")
	// ErrSchemaAhead: the database was migrated by a newer bekup version.
	ErrSchemaAhead = errors.New("database schema is newer than this bekup version")
)

// SchemaVersionError describes a mismatch between the applied schema version
// and the latest migration embedded in this binary.
type SchemaVersionError struct {
	// Applied is the highest applied migration version (0: none or unknown).
	Applied int64
	// Expected is the latest embedded migration version.
	Expected int64
	kind     error
}

func (e *SchemaVersionError) Error() string {
	if errors.Is(e.kind, ErrSchemaAhead) {
		return fmt.Sprintf("%s (database at version %d, this binary knows up to %d): "+
			"run the matching or a newer bekup release; downgrades are not supported (migrations are forward-only)",
			e.kind, e.Applied, e.Expected)
	}
	return fmt.Sprintf("%s (database at version %d, this binary needs %d): run `bekup migrate up` as %s first",
		e.kind, e.Applied, e.Expected, OwnerRole)
}

// Unwrap returns ErrSchemaBehind or ErrSchemaAhead.
func (e *SchemaVersionError) Unwrap() error { return e.kind }

// LatestMigrationVersion returns the highest version of the migrations
// embedded in this binary (the NNNNN prefix of internal/db/migrations/*.sql).
func LatestMigrationVersion() (int64, error) {
	return latestVersion(migrations.FS)
}

func latestVersion(fsys fs.FS) (int64, error) {
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return 0, fmt.Errorf("list migrations: %w", err)
	}
	var latest int64
	for _, name := range names {
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			return 0, fmt.Errorf("migration %q has no NNNNN_ version prefix", name)
		}
		v, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil || v <= 0 {
			return 0, fmt.Errorf("migration %q has an invalid version prefix", name)
		}
		latest = max(latest, v)
	}
	if latest == 0 {
		return 0, errors.New("no embedded migrations found")
	}
	return latest, nil
}

// SchemaVersion returns the applied schema version as seen by the current
// role, through the SECURITY DEFINER function bekup_schema_version() (the app
// role cannot read goose_db_version). A database migrated to a version before
// the function existed reports 0.
func SchemaVersion(ctx context.Context, db sqlc.DBTX) (int64, error) {
	v, err := sqlc.New(db).SchemaVersion(ctx)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42883" { // undefined_function
			return 0, nil
		}
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return v, nil
}

// CheckSchemaVersion compares the applied schema version with the latest
// embedded migration. `bekup serve` refuses to start unless they are equal
// (D-078): serve never holds the owner credentials, so it cannot migrate
// itself. The error is a *SchemaVersionError wrapping ErrSchemaBehind or
// ErrSchemaAhead, or a plain error when the version cannot be read.
func CheckSchemaVersion(ctx context.Context, db sqlc.DBTX) error {
	want, err := LatestMigrationVersion()
	if err != nil {
		return err
	}
	got, err := SchemaVersion(ctx, db)
	if err != nil {
		return err
	}
	return compareSchemaVersion(got, want)
}

func compareSchemaVersion(applied, expected int64) error {
	switch {
	case applied < expected:
		return &SchemaVersionError{Applied: applied, Expected: expected, kind: ErrSchemaBehind}
	case applied > expected:
		return &SchemaVersionError{Applied: applied, Expected: expected, kind: ErrSchemaAhead}
	default:
		return nil
	}
}
