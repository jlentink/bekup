//go:build integration

package dbtest

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Execer runs statements (*pgxpool.Pool, pgx.Tx).
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// JobRow is a minimal job for tests of the packages below internal/jobs
// (scheduler, queue, jobkeys) that need a jobs row for the foreign keys.
type JobRow struct {
	Enabled   bool
	Schedule  string // canonical schedule JSON; "" = manual
	NextRunAt *time.Time
	Deleted   bool
	// ConnectionID is the connection backed up; uuid.Nil inserts a new
	// postgres connection.
	ConnectionID uuid.UUID
	// DestinationIDs are linked in job_destinations.
	DestinationIDs []uuid.UUID
}

// InsertJob inserts a job (retention: keep the last 7), with a new
// postgres connection unless j.ConnectionID is set, and returns its id.
func InsertJob(ctx context.Context, db Execer, j JobRow) (uuid.UUID, error) {
	conn, id := j.ConnectionID, uuid.New()
	if conn == uuid.Nil {
		conn = uuid.New()
		if _, err := db.Exec(ctx, `INSERT INTO connections (id, name, engine, version_key, image_key, host, port, username)
			VALUES ($1, $2, 'postgres', '17', '17', 'db.example.test', 5432, 'backup')`, conn, "conn-"+conn.String()[:8]); err != nil {
			return uuid.Nil, fmt.Errorf("insert test connection: %w", err)
		}
	}
	sched := j.Schedule
	if sched == "" {
		sched = `{"kind":"manual"}`
	}
	var deleted *time.Time
	if j.Deleted {
		now := time.Now()
		deleted = &now
	}
	if _, err := db.Exec(ctx, `INSERT INTO jobs (id, name, slug, connection_id, retention_keep_last, enabled, schedule, next_run_at, deleted_at)
		VALUES ($1, $2, $3, $4, 7, $5, $6::jsonb, $7, $8)`,
		id, "job-"+id.String()[:8], "job-"+id.String()[:8], conn, j.Enabled, sched, j.NextRunAt, deleted); err != nil {
		return uuid.Nil, fmt.Errorf("insert test job: %w", err)
	}
	for i, d := range j.DestinationIDs {
		if _, err := db.Exec(ctx, `INSERT INTO job_destinations (job_id, destination_id, position) VALUES ($1, $2, $3)`, id, d, i); err != nil {
			return uuid.Nil, fmt.Errorf("insert test job destination: %w", err)
		}
	}
	return id, nil
}
