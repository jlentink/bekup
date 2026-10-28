package restore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/artifactverify"
	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/serviceutil"
	"github.com/jlentink/bekup/internal/storage"
)

// Mode is how a restore writes its target.
type Mode string

// Restore modes.
const (
	// ModeNewDB restores into a database that is created for it.
	ModeNewDB Mode = "new_db"
	// ModeOverwrite replaces an existing database (PostgreSQL: temp DB
	// and swap; MySQL/MariaDB: in place, task 4.3).
	ModeOverwrite Mode = "overwrite"
)

// Status is a restore's status (restores.status).
type Status = sqlc.RestoreStatus

// Statuses.
const (
	StatusQueued    = sqlc.RestoreStatusQueued
	StatusStaging   = sqlc.RestoreStatusStaging
	StatusVerifying = sqlc.RestoreStatusVerifying
	StatusRestoring = sqlc.RestoreStatusRestoring
	StatusSucceeded = sqlc.RestoreStatusSucceeded
	StatusFailed    = sqlc.RestoreStatusFailed
	StatusCanceled  = sqlc.RestoreStatusCanceled
)

// Limits (D-162).
const (
	// HandoffTTL is the lifetime of an identity handoff.
	HandoffTTL = 15 * time.Minute
	// MaxIdentityBytes bounds the age identity input.
	MaxIdentityBytes = 4096
	// DefaultScratchReserve is kept free on the scratch file system.
	DefaultScratchReserve = 1 << 30
	// DefaultStaleAfter is the age after which the sweeper removes a
	// staging directory even of a running restore.
	DefaultStaleAfter = 24 * time.Hour
	// housekeepingInterval is the period of RunHousekeeping (expired
	// handoffs); the scratch sweep runs every sweepEvery ticks.
	housekeepingInterval = time.Minute
	sweepEvery           = 10
)

// Encryption modes stored on artifacts.
const (
	encPassphrase = "passphrase"
	encRecipients = "recipients"
	encNone       = "none"
)

// DB is the app database.
type DB interface {
	db.TxBeginner
	sqlc.DBTX
}

// DestinationOpener opens a destination's storage backend, soft-deleted
// ones included (*destinations.Service).
type DestinationOpener interface {
	Open(ctx context.Context, id uuid.UUID) (storage.Destination, error)
}

// KeySource reads job keys by version (*jobkeys.Service).
type KeySource interface {
	PassphraseForVersion(ctx context.Context, job uuid.UUID, version int) (*crypto.Secret, error)
	RecipientForVersion(ctx context.Context, job uuid.UUID, version int) (jobkeys.RecipientInfo, error)
}

// Options configure New.
type Options struct {
	DB           DB
	Keyring      *crypto.Keyring
	Auditor      *audit.Auditor
	Destinations DestinationOpener
	Keys         KeySource
	// ScratchDir is storage.scratch_dir (absolute; created 0700 when
	// missing; a restore refuses one that is group- or world-writable).
	ScratchDir string
	// ScratchReserve is kept free on the scratch file system (bytes;
	// negative = DefaultScratchReserve, 0 = no reserve).
	ScratchReserve int64
	// StaleAfter: see DefaultStaleAfter (0 = the default).
	StaleAfter time.Duration
	// HoldTTL is the lifetime of the artifact hold between renewals
	// (0 = runstate.DefaultHoldTTL; renewed every third of it).
	HoldTTL time.Duration
	// Alert receives security events (nil = log them).
	Alert AlertFunc
	// Publisher sends the restore events (bekup_restore_events; nil =
	// NOTIFY only, no local bus).
	Publisher *pubsub.Publisher
	Now       func() time.Time
	Logger    *slog.Logger
}

// Service stages and verifies restores.
type Service struct {
	o Options
	// live are the restores staged by this process (the sweeper never
	// removes their directories).
	mu   sync.Mutex
	live map[uuid.UUID]struct{}
}

// New returns the service. The scratch directory is created (0700) and
// checked when a restore stages (checkScratch), so a process that never
// restores does not need it.
func New(o Options) (*Service, error) {
	if o.DB == nil || o.Keyring == nil || o.Auditor == nil || o.Destinations == nil || o.Keys == nil || o.Logger == nil {
		return nil, errors.New("restore: DB, Keyring, Auditor, Destinations, Keys and Logger are required")
	}
	if o.ScratchDir == "" || !filepath.IsAbs(o.ScratchDir) || filepath.Clean(o.ScratchDir) != o.ScratchDir {
		return nil, errors.New("restore: the scratch directory must be a clean absolute path")
	}
	if o.ScratchReserve < 0 {
		o.ScratchReserve = DefaultScratchReserve
	}
	o.ScratchReserve = min(o.ScratchReserve, math.MaxInt64/2)
	if o.StaleAfter <= 0 {
		o.StaleAfter = DefaultStaleAfter
	}
	if o.HoldTTL <= 0 {
		o.HoldTTL = runstate.DefaultHoldTTL
	}
	o.HoldTTL = min(max(o.HoldTTL, 3*time.Second), runstate.MaxHoldTTL)
	if o.Alert == nil {
		o.Alert = logAlert(o.Logger)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{o: o, live: map[uuid.UUID]struct{}{}}, nil
}

// CreateInput is a restore request (validated and authorized by the
// caller, task 4.5). Identity (age identity-file content) is borrowed: the
// caller zeroes it.
type CreateInput struct {
	ArtifactID         uuid.UUID
	TargetConnectionID uuid.UUID
	// TargetDBName is nil only for a globals restore.
	TargetDBName *string
	Mode         Mode
	RequestedBy  uuid.UUID
	Identity     []byte
	// KeepOwners restores PostgreSQL object ownership (pg_restore without
	// --no-owner). Off by default: the objects belong to the target
	// connection's role (restores.no_owner, D-166).
	KeepOwners bool
	// Privileges is the target role's verdict probed for the request and
	// PrivilegedAck the admin's acknowledgement of a gated one; the
	// worker re-probes before it starts and compares (D-173).
	Privileges    *connections.Privileges
	PrivilegedAck bool
}

// identityAAD binds a sealed identity to its restore.
func identityAAD(id uuid.UUID) crypto.AAD {
	return crypto.AAD{Table: "restore_identity_handoff", RowID: id.String(), Field: "identity"}
}

// CreateTx inserts a queued restore in the caller's transaction (which
// also enqueues its River job with the restore id only and audits the
// request). It checks that the artifact can be restored (a present,
// verified database or globals object covered by a signed manifest, whose
// key version still exists) and, for a recipient-encrypted artifact, that
// Identity matches the job's recipient; the identity is then sealed into
// restore_identity_handoff (15-minute TTL). It does not authorize: the
// caller has done that (target rules, fresh 2FA).
func (s *Service) CreateTx(ctx context.Context, q *sqlc.Queries, in CreateInput) (sqlc.Restore, error) {
	if in.Mode != ModeNewDB && in.Mode != ModeOverwrite {
		return sqlc.Restore{}, serviceutil.FieldErr("mode", "unknown restore mode")
	}
	row, err := q.GetDownloadArtifact(ctx, in.ArtifactID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Restore{}, ErrNotFound
	}
	if err != nil {
		return sqlc.Restore{}, fmt.Errorf("restore: load artifact: %w", err)
	}
	a := &row.Artifact
	if err := checkArtifact(&row); err != nil {
		return sqlc.Restore{}, err
	}
	if (a.Kind == sqlc.ArtifactKindGlobals) != (in.TargetDBName == nil) {
		return sqlc.Restore{}, serviceutil.FieldErr("targetDatabase", "a database name is required for database backups and not allowed for globals")
	}
	if in.TargetDBName != nil && (*in.TargetDBName == "" || len(*in.TargetDBName) > 255) {
		return sqlc.Restore{}, serviceutil.FieldErr("targetDatabase", "must be 1 to 255 bytes")
	}
	seal, err := s.checkKeys(ctx, &row, in.Identity)
	if err != nil {
		return sqlc.Restore{}, err
	}
	id := uuid.New()
	var by *uuid.UUID
	if in.RequestedBy != uuid.Nil {
		by = &in.RequestedBy
	}
	p := sqlc.InsertRestoreParams{
		ID: id, ArtifactID: a.ID, RunItemID: *a.RunItemID, JobID: a.JobID, TargetConnectionID: in.TargetConnectionID,
		TargetDbName: in.TargetDBName, Mode: sqlc.RestoreMode(in.Mode), RequestedBy: by, NoOwner: !in.KeepOwners,
	}
	if in.Privileges != nil {
		st := string(in.Privileges.Status)
		p.TargetPrivileges, p.TargetPrivilegeReasons = &st, slices.Clone(in.Privileges.Reasons)
		p.PrivilegedTargetAcknowledged = in.PrivilegedAck && in.Privileges.Gated() && by != nil
	}
	r, err := q.InsertRestore(ctx, p)
	if err != nil {
		return sqlc.Restore{}, fmt.Errorf("restore: insert: %w", err)
	}
	if seal {
		sealed, err := s.o.Keyring.Seal(in.Identity, identityAAD(id))
		if err != nil {
			return sqlc.Restore{}, fmt.Errorf("restore: seal identity: %w", err)
		}
		err = q.InsertRestoreIdentityHandoff(ctx, sqlc.InsertRestoreIdentityHandoffParams{
			RestoreID: id, SealedIdentity: sealed, TtlSeconds: HandoffTTL.Seconds(),
		})
		crypto.Zero(sealed)
		if err != nil {
			return sqlc.Restore{}, fmt.Errorf("restore: store identity handoff: %w", err)
		}
	}
	return r, nil
}

// checkArtifact applies the restore preconditions to an artifact row.
func checkArtifact(row *sqlc.GetDownloadArtifactRow) error {
	a := &row.Artifact
	switch {
	case a.Kind == sqlc.ArtifactKindManifest || a.RunItemID == nil:
		return serviceutil.FieldErr("artifactId", "manifest files cannot be restored")
	case a.State != sqlc.ArtifactStatePresent, !a.Verified, a.SigningKeyID == nil, row.RunStorageTimestamp == nil:
		return ErrUnavailable
	}
	enc := ""
	if a.EncryptionMode != nil {
		enc = *a.EncryptionMode
	}
	switch enc {
	case encPassphrase, encRecipients, encNone:
	default:
		return fmt.Errorf("restore: artifact %s has an unknown encryption mode", a.ID)
	}
	if _, _, err := artifactverify.StoredFormat(a.ObjectKey, enc != encNone); err != nil {
		return fmt.Errorf("restore: artifact %s: %w", a.ID, err)
	}
	return nil
}

func encryptionOf(a *sqlc.Artifact) string {
	if a.EncryptionMode == nil {
		return ""
	}
	return *a.EncryptionMode
}

// checkKeys validates the key input of a request; it reports whether the
// identity must be sealed into the handoff.
func (s *Service) checkKeys(ctx context.Context, row *sqlc.GetDownloadArtifactRow, identity []byte) (bool, error) {
	a := &row.Artifact
	switch encryptionOf(a) {
	case encRecipients:
		if len(identity) == 0 {
			return false, serviceutil.FieldErr("ageIdentity", "this backup is encrypted to an age recipient: paste its private key (AGE-SECRET-KEY-1...)")
		}
		if len(identity) > MaxIdentityBytes {
			return false, serviceutil.FieldErr("ageIdentity", "the identity is too long")
		}
		if err := s.matchIdentity(ctx, a, identity); err != nil {
			if errors.Is(err, artifactverify.ErrIdentityInvalid) {
				return false, serviceutil.FieldErr("ageIdentity", "not an age X25519 identity (AGE-SECRET-KEY-1...)")
			}
			if errors.Is(err, ErrIdentityMismatch) {
				return false, serviceutil.FieldErr("ageIdentity", "this key does not match the recipient of the backup")
			}
			return false, err
		}
		return true, nil
	case encPassphrase:
		if len(identity) > 0 {
			return false, serviceutil.FieldErr("ageIdentity", "an identity is only needed for recipient-encrypted backups")
		}
		p, err := s.passphrase(ctx, a)
		if err != nil {
			return false, err
		}
		p.Destroy()
		return false, nil
	default:
		if len(identity) > 0 {
			return false, serviceutil.FieldErr("ageIdentity", "an identity is only needed for recipient-encrypted backups")
		}
		return false, nil
	}
}

// matchIdentity checks identity against the recipient version of a.
func (s *Service) matchIdentity(ctx context.Context, a *sqlc.Artifact, identity []byte) error {
	if a.RecipientVersion == nil {
		return ErrKeyUnavailable
	}
	info, err := s.o.Keys.RecipientForVersion(ctx, a.JobID, int(*a.RecipientVersion))
	if errors.Is(err, jobkeys.ErrVersionNotFound) {
		return ErrKeyUnavailable
	}
	if err != nil {
		return fmt.Errorf("restore: recipient: %w", err)
	}
	switch err := artifactverify.MatchIdentity(identity, info.Recipient); {
	case err == nil:
		return nil
	case errors.Is(err, artifactverify.ErrIdentityMismatch):
		return ErrIdentityMismatch
	default:
		return err
	}
}

// passphrase reads the job passphrase of a's version (destroy it).
func (s *Service) passphrase(ctx context.Context, a *sqlc.Artifact) (*crypto.Secret, error) {
	if a.PassphraseVersion == nil {
		return nil, ErrKeyUnavailable
	}
	p, err := s.o.Keys.PassphraseForVersion(ctx, a.JobID, int(*a.PassphraseVersion))
	if errors.Is(err, jobkeys.ErrVersionNotFound) {
		return nil, ErrKeyUnavailable
	}
	if err != nil {
		return nil, fmt.Errorf("restore: passphrase: %w", err)
	}
	return p, nil
}

// SetRiverJob records the River job of a restore (once).
func (s *Service) SetRiverJob(ctx context.Context, q *sqlc.Queries, id uuid.UUID, jobID int64) error {
	n, err := q.SetRestoreRiverJob(ctx, sqlc.SetRestoreRiverJobParams{ID: id, RiverJobID: &jobID})
	if err != nil {
		return fmt.Errorf("restore: set River job: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("restore: restore %s not found or its River job is already set", id)
	}
	return nil
}

// Inserter inserts River jobs in a transaction (*queue.Client).
type Inserter interface {
	InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, o queue.InsertOptions) (queue.InsertResult, error)
}

// EnqueueTx inserts the restore's River job (kind restore, the restore id
// only, D-145) in the caller's transaction, right after CreateTx, and
// records its id on the restore.
func (s *Service) EnqueueTx(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, ins Inserter, id uuid.UUID) error {
	res, err := ins.InsertTx(ctx, tx, queue.RestoreArgs{RestoreID: id}, queue.InsertOptions{})
	if err != nil {
		return fmt.Errorf("restore: enqueue: %w", err)
	}
	if res.Duplicate {
		return fmt.Errorf("restore: restore %s is already enqueued", id)
	}
	return s.SetRiverJob(ctx, q, id, res.JobID)
}

// Get returns a restore.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (sqlc.Restore, error) {
	r, err := sqlc.New(s.o.DB).GetRestore(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Restore{}, ErrNotFound
	}
	if err != nil {
		return sqlc.Restore{}, fmt.Errorf("restore: get: %w", err)
	}
	return r, nil
}

// Finish sets a restore's terminal status (succeeded, failed or canceled;
// a no-op when it already finished). For failed and canceled, cause is
// mapped to the fixed error_code / error_message (ErrorCode); it is never
// stored itself. It reports whether the restore changed.
func (s *Service) Finish(ctx context.Context, id uuid.UUID, status Status, cause error) (bool, error) {
	return s.finish(ctx, id, status, cause, Outcome{})
}

// Outcome is what the worker records with the terminal status.
type Outcome struct {
	LogLines, LogLinesDropped int
	// Warnings are stable codes (at most maxWarnings are stored).
	Warnings []string
}

// maxWarnings bounds restores.warnings.
const maxWarnings = 32

// finish is Finish with the outcome; the restore event is published in
// the same transaction.
func (s *Service) finish(ctx context.Context, id uuid.UUID, status Status, cause error, out Outcome) (bool, error) {
	// The outcome is recorded even when the worker's context ended.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var changed bool
	err := db.Tx(wctx, s.o.DB, func(q *sqlc.Queries) error {
		var err error
		changed, err = s.finishTx(wctx, q, id, status, cause, out)
		return err
	})
	if err != nil {
		return false, err
	}
	return changed, nil
}

// finishTx records the terminal status in q's transaction and publishes
// the restore event there (a no-op on a finished restore).
func (s *Service) finishTx(ctx context.Context, q *sqlc.Queries, id uuid.UUID, status Status, cause error, out Outcome) (bool, error) {
	warnings := make([]string, 0, min(len(out.Warnings), maxWarnings))
	for _, w := range out.Warnings {
		if len(warnings) < maxWarnings && codeShape.MatchString(w) {
			warnings = append(warnings, w)
		}
	}
	p := sqlc.FinishRestoreParams{
		ID: id, Status: status, Warnings: warnings,
		LogLines: clampInt32(out.LogLines), LogLinesDropped: clampInt32(out.LogLinesDropped),
	}
	switch status {
	case StatusSucceeded:
	case StatusFailed, StatusCanceled:
		code := ErrorCode(cause)
		if code == "" {
			code = CodeInternal
		}
		if status == StatusCanceled {
			code = CodeCanceled
		}
		msg := ErrorMessage(code)
		p.ErrorCode, p.ErrorMessage = &code, &msg
	default:
		return false, fmt.Errorf("restore: %q is not a terminal status", status)
	}
	n, err := q.FinishRestore(ctx, p)
	if err != nil {
		return false, fmt.Errorf("restore: finish: %w", err)
	}
	if n == 0 {
		return false, nil
	}
	return true, s.publish(ctx, q, EventRestore, id)
}

func clampInt32(n int) int32 {
	return int32(min(max(n, 0), math.MaxInt32))
}

// DeleteExpiredHandoffs removes identity handoffs that expired (their
// restore never started in time).
func (s *Service) DeleteExpiredHandoffs(ctx context.Context) (int64, error) {
	n, err := sqlc.New(s.o.DB).DeleteExpiredRestoreIdentityHandoffs(ctx)
	if err != nil {
		return 0, fmt.Errorf("restore: delete expired identity handoffs: %w", err)
	}
	return n, nil
}

// RunHousekeeping sweeps the scratch directory at once and every 10
// minutes and deletes expired identity handoffs every minute, until ctx
// ends. Failures are logged and retried at the next tick.
func (s *Service) RunHousekeeping(ctx context.Context) error {
	s.housekeep(ctx, true)
	t := time.NewTicker(housekeepingInterval)
	defer t.Stop()
	for i := 1; ; i++ {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			s.housekeep(ctx, i%sweepEvery == 0)
		}
	}
}

func (s *Service) housekeep(ctx context.Context, sweep bool) {
	if n, err := s.DeleteExpiredHandoffs(ctx); err != nil {
		s.o.Logger.WarnContext(ctx, "deleting expired restore identity handoffs failed", slog.Any("error", err))
	} else if n > 0 {
		s.o.Logger.InfoContext(ctx, "expired restore identity handoffs deleted", slog.Int64("count", n))
	}
	if !sweep {
		return
	}
	if n, err := s.SweepScratch(ctx); err != nil {
		s.o.Logger.WarnContext(ctx, "sweeping the restore scratch directory failed", slog.Any("error", err))
	} else if n > 0 {
		s.o.Logger.InfoContext(ctx, "stale restore staging directories removed", slog.Int("count", n))
	}
}
