package offline

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/pipeline"
)

// Import errors. Compare with errors.Is.
var (
	// ErrAlreadyIndexed: the run (or one of its objects on the
	// destination) is already in the database; nothing was imported.
	ErrAlreadyIndexed = errors.New("offline: run is already in the database")
	// ErrJobMismatch: the manifest's job is not the database job with that
	// slug (a recreated job has a new id; restores bind to the id).
	ErrJobMismatch = errors.New("offline: manifest job id differs from the database job with that slug")
	// ErrNotImportable: the run did not verify (no manifest, bad signature,
	// wrong location) and is never imported.
	ErrNotImportable = errors.New("offline: run did not verify and cannot be imported")
	// ErrJobNotFound: no job with the manifest's slug.
	ErrJobNotFound = errors.New("offline: no job with that slug in the database")
	// ErrDestinationNotFound: the destination id is not in the database.
	ErrDestinationNotFound = errors.New("offline: destination not found in the database")
)

// DBPublicKeys returns the instance public keys stored in the database
// (active and retired; never the sealed private keys).
func DBPublicKeys(ctx context.Context, q *sqlc.Queries) ([]PublicKey, error) {
	rows, err := q.ListInstancePublicKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list instance keys: %w", err)
	}
	out := make([]PublicKey, 0, len(rows))
	for _, r := range rows {
		pub := ed25519.PublicKey(r.PublicKey)
		if len(pub) != ed25519.PublicKeySize || crypto.SigningKeyID(pub) != r.ID {
			return nil, fmt.Errorf("instance key %s: %w: id does not match public key", r.ID, crypto.ErrSigningKey)
		}
		out = append(out, PublicKey{ID: r.ID, Key: pub, CreatedAt: r.CreatedAt.UTC(), RetiredAt: r.RetiredAt})
	}
	return out, nil
}

// Importer imports verified runs of a destination into the database.
type Importer struct {
	DB      db.TxBeginner
	Auditor *audit.Auditor
	// DestinationID is the database destination the objects live on;
	// DestinationType must equal its type ("disk", "s3", "sftp").
	DestinationID   uuid.UUID
	DestinationType string
	Now             func() time.Time
}

// ImportResult describes one imported run.
type ImportResult struct {
	RunID     uuid.UUID
	JobID     uuid.UUID
	Artifacts int
}

const importNoticeCode = "imported"

type importWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	DB      string `json:"db,omitempty"`
}

// boundedWarnings encodes warnings as a JSON array of at most limit bytes
// (later warnings are dropped and counted in a final entry).
func boundedWarnings(ws []importWarning, limit int) (json.RawMessage, error) {
	for n := len(ws); n >= 0; n-- {
		list := ws[:n]
		if n < len(ws) {
			list = append(append([]importWarning(nil), list...), importWarning{
				Code: "warnings_truncated", Message: fmt.Sprintf("%d more warning(s) not recorded", len(ws)-n),
			})
		}
		b, err := json.Marshal(list)
		if err != nil {
			return nil, err
		}
		if len(b) <= limit {
			return b, nil
		}
	}
	return json.RawMessage("[]"), nil
}

// Byte bounds below the runs CHECKs (warnings 256 KiB, notices 64 KiB).
const (
	importWarningsBytes = 250 << 10
	importNoticesBytes  = 60 << 10
	maxDBNameBytes      = 255
)

// Import records one fully verified run (signature, location and every
// artifact's size and SHA-256): the run (with the manifest's run id, status
// success), one verified run item and one verified, signed artifact per
// entry, and the manifest object, plus the audit event cli.run.imported, in
// one transaction. A run with a missing or mismatching artifact is refused
// (ErrNotImportable): restores bind to exactly the signed entries, so it
// could not be used anyway; its intact artifacts stay available to `bekup
// decrypt`. Runs already in the database are refused (ErrAlreadyIndexed).
func (im *Importer) Import(ctx context.Context, r *ScannedRun) (ImportResult, error) {
	var res ImportResult
	if !r.Verified() || r.Report.Partial {
		return res, ErrNotImportable
	}
	m := r.Report.Manifest
	runID, err := uuid.Parse(m.RunID)
	if err != nil {
		return res, fmt.Errorf("%w: run id is not a UUID", ErrNotImportable)
	}
	jobID, err := uuid.Parse(m.JobID)
	if err != nil {
		return res, fmt.Errorf("%w: job id is not a UUID", ErrNotImportable)
	}
	now := time.Now
	if im.Now != nil {
		now = im.Now
	}
	res.RunID, res.JobID = runID, jobID
	err = db.Tx(ctx, im.DB, func(q *sqlc.Queries) error {
		job, err := q.GetJobBySlugForImport(ctx, m.JobSlug)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrJobNotFound, m.JobSlug)
		}
		if err != nil {
			return fmt.Errorf("load job: %w", err)
		}
		if job.DeletedAt != nil {
			return fmt.Errorf("%w: job %s is deleted", ErrJobNotFound, m.JobSlug)
		}
		if job.ID != jobID {
			return fmt.Errorf("%w (manifest %s, database %s)", ErrJobMismatch, jobID, job.ID)
		}
		dest, err := q.GetDestinationIncludingDeleted(ctx, im.DestinationID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDestinationNotFound
		}
		if err != nil {
			return fmt.Errorf("load destination: %w", err)
		}
		if dest.DeletedAt != nil {
			return fmt.Errorf("%w: destination %s is deleted", ErrDestinationNotFound, im.DestinationID)
		}
		if string(dest.Type) != im.DestinationType {
			return fmt.Errorf("offline: destination %s is of type %s, the destination config describes %s",
				im.DestinationID, dest.Type, im.DestinationType)
		}
		if exists, err := q.RunExists(ctx, runID); err != nil {
			return fmt.Errorf("check run: %w", err)
		} else if exists {
			return ErrAlreadyIndexed
		}
		keys := []string{r.ManifestKey.String()}
		for i := range m.Entries {
			k, err := r.Dir.Join(m.Entries[i].Path)
			if err != nil {
				return err
			}
			keys = append(keys, k.String())
		}
		for _, k := range keys {
			exists, err := q.ArtifactKeyExists(ctx, sqlc.ArtifactKeyExistsParams{DestinationID: im.DestinationID, ObjectKey: k})
			if err != nil {
				return fmt.Errorf("check artifact: %w", err)
			}
			if exists {
				return fmt.Errorf("%w (object %s)", ErrAlreadyIndexed, k)
			}
		}
		return im.insert(ctx, q, r, runID, jobID, keys[1:], now().UTC(), &res)
	})
	if err != nil {
		return ImportResult{}, err
	}
	return res, nil
}

func (im *Importer) insert(ctx context.Context, q *sqlc.Queries, r *ScannedRun, runID, jobID uuid.UUID, keys []string, now time.Time, res *ImportResult) error {
	m := r.Report.Manifest
	warnings := make([]importWarning, 0, len(m.Warnings))
	for _, w := range m.Warnings {
		warnings = append(warnings, importWarning{Code: "manifest_warning", Message: w})
	}
	var bytesRaw, bytesStored int64
	for i := range m.Entries {
		bytesRaw += m.Entries[i].RawSize
		bytesStored += m.Entries[i].StoredSize
	}
	ws, err := boundedWarnings(warnings, importWarningsBytes)
	if err != nil {
		return err
	}
	ns, err := boundedWarnings([]importWarning{{
		Code: importNoticeCode,
		Message: fmt.Sprintf("imported by bekup reindex on %s from the signed manifest %s",
			now.Format(time.RFC3339), r.ManifestKey),
	}}, importNoticesBytes)
	if err != nil {
		return err
	}
	if _, err := q.ImportRun(ctx, sqlc.ImportRunParams{
		ID: runID, JobID: jobID, Status: sqlc.RunStatusSuccess, FinishedAt: m.CreatedAt, StorageTimestamp: m.RunTimestamp,
		Warnings: ws, Notices: ns, BytesRaw: bytesRaw, BytesStored: bytesStored,
	}); err != nil {
		return fmt.Errorf("insert run: %w", err)
	}
	keyID := m.InstanceKeyID
	for i := range m.Entries {
		if err := im.insertEntry(ctx, q, runID, jobID, int32(i), &m.Entries[i], keys[i], keyID, m.CreatedAt); err != nil {
			return err
		}
		res.Artifacts++
	}
	if _, err := q.ImportArtifact(ctx, sqlc.ImportArtifactParams{
		RunID: runID, JobID: jobID, DestinationID: im.DestinationID, Kind: sqlc.ArtifactKindManifest,
		ObjectKey: r.ManifestKey.String(), Size: int64(len(r.ManifestFile)), Sha256: r.ManifestSHA256(),
		SigningKeyID: &keyID, Verified: true, State: sqlc.ArtifactStatePresent,
	}); err != nil {
		return fmt.Errorf("insert manifest artifact: %w", err)
	}
	_, err = im.Auditor.RecordTx(ctx, q, audit.CLIEvent(audit.ActionCLIRunImported, "run", runID.String(), audit.Details{
		"job_id": jobID.String(), "job_slug": m.JobSlug, "destination_id": im.DestinationID.String(),
		"run_timestamp": m.RunTimestamp.Format(time.RFC3339), "manifest_key": r.ManifestKey.String(),
		"manifest_sha256": hex.EncodeToString(r.ManifestSHA256()), "signing_key_id": keyID,
		"artifacts": res.Artifacts,
	}))
	if err != nil {
		return fmt.Errorf("audit: %w", err)
	}
	return nil
}

func (im *Importer) insertEntry(ctx context.Context, q *sqlc.Queries, runID, jobID uuid.UUID, pos int32, e *pipeline.Entry,
	key, keyID string, at time.Time) error {
	kind := sqlc.RunItemKindDatabase
	akind := sqlc.ArtifactKindDatabase
	var dbName *string
	if e.Kind == pipeline.KindGlobals {
		kind, akind = sqlc.RunItemKindGlobals, sqlc.ArtifactKindGlobals
	} else {
		name, err := pipeline.DecodeName(e.DB)
		if err != nil {
			return err
		}
		if len(name) > maxDBNameBytes {
			return fmt.Errorf("offline: database name of entry %d is longer than %d bytes", pos+1, maxDBNameBytes)
		}
		dbName = &name
	}
	sum, err := hex.DecodeString(e.SHA256)
	if err != nil {
		return err
	}
	rawSum, err := hex.DecodeString(e.RawSHA256)
	if err != nil {
		return err
	}
	item, err := q.ImportRunItem(ctx, sqlc.ImportRunItemParams{
		RunID: runID, Kind: kind, DbName: dbName, Position: pos, Status: sqlc.RunItemStatusSuccess,
		FinishedAt: at, RawSize: e.RawSize, StoredSize: e.StoredSize, RawSha256: rawSum, Sha256: sum,
		Verified: true,
	})
	if err != nil {
		return fmt.Errorf("insert run item: %w", err)
	}
	p := sqlc.ImportArtifactParams{
		RunID: runID, RunItemID: &item.ID, JobID: jobID, DestinationID: im.DestinationID, Kind: akind,
		ObjectKey: key, Size: e.StoredSize, Sha256: sum, Verified: true, State: sqlc.ArtifactStatePresent,
		SigningKeyID: &keyID,
	}
	mode := string(e.Encryption.Mode)
	p.EncryptionMode = &mode
	switch e.Encryption.Mode {
	case pipeline.EncryptPassphrase:
		v := int32(e.Encryption.PassphraseVersion) //nolint:gosec // G115: validated positive manifest version
		p.PassphraseVersion = &v
	case pipeline.EncryptRecipients:
		v := int32(e.Encryption.RecipientVersion) //nolint:gosec // G115: validated positive manifest version
		p.RecipientVersion = &v
	case pipeline.EncryptNone:
	}
	if _, err := q.ImportArtifact(ctx, p); err != nil {
		return fmt.Errorf("insert artifact: %w", err)
	}
	return nil
}
