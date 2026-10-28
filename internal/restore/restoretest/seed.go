// Package restoretest seeds restorable artifacts for integration tests of
// the restore API outside internal/restore (task 4.5): a dump backed up
// through the real pipeline onto a destination, with its run, item,
// artifact and signed manifest recorded like the backup worker does. Test
// code only.
package restoretest

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/instancekeys"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/storage"
)

// Spec describes the seeded artifact (zero: a PostgreSQL 17 database
// "app" as a SQL dump).
type Spec struct {
	Kind       pipeline.EntryKind
	Format     pipeline.DumpFormat
	Engine     string
	VersionKey string
	// Database is the item's database (default "app"; ignored for
	// globals).
	Database string
}

// Seeded is a seeded artifact.
type Seeded struct {
	Artifact uuid.UUID
	Run      uuid.UUID
}

// Seed backs dump up for job (with slug) onto (destID, dest) as a run at
// at, using the job's current key material.
func Seed(ctx context.Context, pool *pgxpool.Pool, kr *crypto.Keyring, keys *jobkeys.Service, job uuid.UUID, slug string,
	destID uuid.UUID, dest storage.Destination, dump []byte, at time.Time, sp Spec,
) (Seeded, error) {
	if sp.Kind == "" {
		sp.Kind = pipeline.KindDatabase
	}
	if sp.Format == "" {
		sp.Format = pipeline.FormatSQL
	}
	if sp.Engine == "" {
		sp.Engine, sp.VersionKey = "postgres", "17"
	}
	if sp.Database == "" {
		sp.Database = "app"
	}
	dbName, itemKind := sp.Database, "database"
	var dbArg *string
	if sp.Kind == pipeline.KindGlobals {
		dbName, itemKind = "", "globals"
	} else {
		dbArg = &dbName
	}
	km, err := keys.KeyMaterialFor(ctx, job)
	if err != nil {
		return Seeded{}, err
	}
	enc, err := km.Pipeline()
	if err != nil {
		return Seeded{}, err
	}
	defer enc.Passphrase.Destroy()
	ts := at.UTC().Truncate(time.Second)
	var run uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO runs (job_id, trigger, status, started_at, finished_at, storage_timestamp)
		VALUES ($1, 'manual', 'success', now(), now(), $2) RETURNING id`, job, ts).Scan(&run); err != nil {
		return Seeded{}, fmt.Errorf("insert run: %w", err)
	}
	name, err := pipeline.ArtifactFileName(sp.Kind, dbName, sp.Format, enc.Mode)
	if err != nil {
		return Seeded{}, err
	}
	key, err := pipeline.ArtifactKey("", slug, ts, name)
	if err != nil {
		return Seeded{}, err
	}
	res, err := pipeline.Run(ctx, bytes.NewReader(dump), pipeline.Spec{
		Encryption: enc, Targets: []pipeline.Target{{Name: destID.String(), Dest: dest, Key: key}},
	})
	if err != nil {
		return Seeded{}, err
	}
	var item uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO run_items (run_id, kind, db_name, position, status, finished_at, raw_size, stored_size, raw_sha256, sha256, verified)
		VALUES ($1, $6::run_item_kind, $7, 0, 'success', now(), $2, $3, $4, $5, true) RETURNING id`,
		run, res.RawSize, res.StoredSize, res.RawSHA256, res.SHA256, itemKind, dbArg).Scan(&item); err != nil {
		return Seeded{}, fmt.Errorf("insert run item: %w", err)
	}
	var pv, rv *int
	if enc.PassphraseVersion > 0 {
		pv = &enc.PassphraseVersion
	}
	if enc.RecipientVersion > 0 {
		rv = &enc.RecipientVersion
	}
	var art uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO artifacts (run_id, run_item_id, job_id, destination_id, kind, object_key, size, sha256,
		encryption_mode, passphrase_version, recipient_version, verified)
		VALUES ($1, $2, $3, $4, $11::artifact_kind, $5, $6, $7, $8, $9, $10, true) RETURNING id`,
		run, item, job, destID, key.String(), res.StoredSize, res.SHA256, string(enc.Mode), pv, rv, itemKind).Scan(&art); err != nil {
		return Seeded{}, fmt.Errorf("insert artifact: %w", err)
	}
	sk, _, err := instancekeys.Active(ctx, sqlc.New(pool), kr)
	if err != nil {
		return Seeded{}, err
	}
	defer sk.Destroy()
	mkey, err := pipeline.ManifestKey("", slug, ts)
	if err != nil {
		return Seeded{}, err
	}
	m := &pipeline.Manifest{
		FormatVersion: pipeline.ManifestFormatVersion, BekupVersion: "0.0.0-test", JobID: job.String(), JobSlug: slug,
		RunID: run.String(), RunTimestamp: ts, CreatedAt: time.Now().UTC(),
		Engine:  pipeline.EngineInfo{Kind: sp.Engine, VersionKey: sp.VersionKey, ServerVersion: sp.VersionKey + ".0"},
		Entries: []pipeline.Entry{res.Entry(sp.Kind, dbName, name)},
	}
	mres, err := pipeline.WriteManifest(ctx, []pipeline.Target{{Name: destID.String(), Dest: dest, Key: mkey}}, sk, m)
	if err != nil {
		return Seeded{}, err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO artifacts (run_id, job_id, destination_id, kind, object_key, size, sha256, signing_key_id, verified)
		VALUES ($1, $2, $3, 'manifest', $4, $5, $6, $7, true)`, run, job, destID, mkey.String(), len(mres.File), mres.SHA256, sk.ID); err != nil {
		return Seeded{}, fmt.Errorf("insert manifest: %w", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE artifacts SET signing_key_id = $1 WHERE run_id = $2 AND kind <> 'manifest'`, sk.ID, run); err != nil {
		return Seeded{}, fmt.Errorf("mark signed: %w", err)
	}
	return Seeded{Artifact: art, Run: run}, nil
}
