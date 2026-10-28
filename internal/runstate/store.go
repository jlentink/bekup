package runstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/pubsub"
)

// Run statuses (the run_status enum).
const (
	StatusQueued   = sqlc.RunStatusQueued
	StatusRunning  = sqlc.RunStatusRunning
	StatusSuccess  = sqlc.RunStatusSuccess
	StatusPartial  = sqlc.RunStatusPartial
	StatusFailed   = sqlc.RunStatusFailed
	StatusCanceled = sqlc.RunStatusCanceled
	StatusSkipped  = sqlc.RunStatusSkipped
)

// Item statuses (the run_item_status enum).
const (
	ItemRunning  = sqlc.RunItemStatusRunning
	ItemSuccess  = sqlc.RunItemStatusSuccess
	ItemPartial  = sqlc.RunItemStatusPartial
	ItemFailed   = sqlc.RunItemStatusFailed
	ItemCanceled = sqlc.RunItemStatusCanceled
)

// Limits of the stored texts.
const (
	// MaxMessageChars bounds error messages (the column CHECK allows 1000).
	MaxMessageChars = 1000
	// MaxWarningChars bounds one warning message in characters.
	MaxWarningChars = 500
	// MaxWarningBytes bounds one warning message in bytes (multibyte
	// texts reach the byte cap before the character cap).
	MaxWarningBytes = 1024
	// MaxWarningDatabaseBytes bounds the database name of a warning (the
	// run_items.db_name limit).
	MaxWarningDatabaseBytes = 255
	// MaxWarnings bounds the warnings of one run or item; further ones are
	// replaced by a single "warnings_truncated" warning.
	MaxWarnings = 200
	// RunWarningsBytes, ItemWarningsBytes and NoticesBytes are the byte
	// budgets of the encoded lists, well below the column CHECKs (runs
	// 256 KiB, run_items 64 KiB, notices 64 KiB) so that Finish and
	// FinishItem never fail on them: Postgres prints jsonb with more
	// spaces than encoding/json, which warningOverhead covers per entry.
	RunWarningsBytes  = 200 << 10
	ItemWarningsBytes = 60 << 10
	NoticesBytes      = 60 << 10
)

// warningOverhead is the per-entry allowance for jsonb's text form (a
// space after every ':' and ',') and the list separator.
const warningOverhead = 16

// truncatedReserve keeps room for the closing warnings_truncated entry.
const truncatedReserve = 256

// Errors of the store.
var (
	// ErrJobNotFound: the job of a run does not exist.
	ErrJobNotFound = errors.New("runstate: job not found")
	// ErrNotActive: the run is not queued or running (already finished).
	ErrNotActive = errors.New("runstate: the run is not active")
	// ErrRunNotFound: no run with that id.
	ErrRunNotFound = errors.New("runstate: run not found")
)

// Codes of the failures the store records itself.
const (
	// CodeInterrupted: an attempt stopped without finishing its run (worker
	// crash, rescued River job).
	CodeInterrupted = "interrupted"
	// CodeCanceled: the run was canceled (cancelRun).
	CodeCanceled = "canceled"
)

// Warning is one non-fatal finding of a run or item (fixed, sanitized
// texts and identifiers only).
type Warning struct {
	Code          string `json:"code"`
	Message       string `json:"message"`
	Database      string `json:"db,omitempty"`
	DestinationID string `json:"destination_id,omitempty"`
}

// Failure is the sanitized reason of a failed or skipped run or item.
type Failure struct {
	Code    string
	Message string
}

var codeRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// clamp cuts s to at most n characters (valid UTF-8 kept).
func clamp(s string, n int) string {
	s = SanitizeLine(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// clampBytes cuts s to at most n bytes on a rune boundary, marking a cut
// with "…".
func clampBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := max(n-len("…"), 0)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func failureCols(f *Failure) (code, msg *string) {
	if f == nil {
		return nil, nil
	}
	c := f.Code
	if !codeRe.MatchString(c) {
		c = "internal"
	}
	m := clamp(f.Message, MaxMessageChars)
	return &c, &m
}

// normalizeWarning applies the code format and the per-field caps.
func normalizeWarning(w Warning) Warning {
	if !codeRe.MatchString(w.Code) {
		w.Code = "warning"
	}
	w.Message = clampBytes(clamp(w.Message, MaxWarningChars), MaxWarningBytes)
	w.Database = clampBytes(clamp(w.Database, MaxWarningDatabaseBytes), MaxWarningDatabaseBytes)
	if len(w.DestinationID) > 64 {
		w.DestinationID = ""
	}
	return w
}

// warningsJSON normalizes and encodes warnings within maxBytes: at most
// MaxWarnings entries and maxBytes of encoded text (counted per entry,
// with the jsonb overhead); the rest is replaced by one
// "warnings_truncated" warning.
func warningsJSON(ws []Warning, maxBytes int) ([]byte, error) {
	out := make([]Warning, 0, min(len(ws), MaxWarnings+1))
	used := 2 // []
	budget := maxBytes - truncatedReserve
	for i, w := range ws {
		w = normalizeWarning(w)
		b, err := json.Marshal(w)
		if err != nil {
			return nil, fmt.Errorf("runstate: encode warning: %w", err)
		}
		cost := len(b) + warningOverhead
		if i == MaxWarnings || used+cost > budget {
			out = append(out, Warning{Code: "warnings_truncated", Message: fmt.Sprintf("%d further warnings were not recorded", len(ws)-i)})
			break
		}
		used += cost
		out = append(out, w)
	}
	return json.Marshal(out)
}

// DB is what the store needs (*pgxpool.Pool).
type DB interface {
	db.TxBeginner
	sqlc.DBTX
}

// Store writes the run tables and publishes run events.
type Store struct {
	db       DB
	pub      *pubsub.Publisher
	now      func() time.Time
	onFinish FinishHook
	log      *slog.Logger
}

// FinishHook runs inside the transaction of Finish after a run ended
// success or partial (the retention worker enqueues its post-run sweep,
// task 3.8). It runs under a savepoint: its failure is logged and rolled
// back alone and never fails the run's terminal write.
type FinishHook func(ctx context.Context, tx pgx.Tx, r Ref, status sqlc.RunStatus) error

// WithFinishHook sets the hook of Finish (log receives hook failures; nil =
// slog.Default) and returns s. Call it before the store is used.
func (s *Store) WithFinishHook(h FinishHook, log *slog.Logger) *Store {
	if log == nil {
		log = slog.Default()
	}
	s.onFinish, s.log = h, log
	return s
}

// NewStore returns a store on pool. pub may be nil (NOTIFY only, no local
// bus); now nil uses time.Now.
func NewStore(pool DB, pub *pubsub.Publisher, now func() time.Time) *Store {
	if pub == nil {
		pub = &pubsub.Publisher{}
	}
	if now == nil {
		now = time.Now
	}
	return &Store{db: pool, pub: pub, now: now}
}

// Run is the worker's view of a run row.
type Run struct {
	Ref
	Status          sqlc.RunStatus
	Trigger         sqlc.RunTrigger
	ScheduledFor    *time.Time
	TriggeredBy     *uuid.UUID
	Attempt         int
	CancelRequested bool
}

func runOf(r *sqlc.Run) Run {
	return Run{
		Ref: Ref{RunID: r.ID, JobID: r.JobID}, Status: r.Status, Trigger: r.Trigger,
		ScheduledFor: r.ScheduledFor, TriggeredBy: r.TriggeredBy, Attempt: int(r.Attempt),
		CancelRequested: r.CancelRequestedAt != nil,
	}
}

// BeginParams identify the River attempt that starts working a run.
type BeginParams struct {
	RiverJobID   int64
	Attempt      int
	JobID        uuid.UUID
	Trigger      sqlc.RunTrigger
	ScheduledFor *time.Time
	TriggeredBy  *uuid.UUID
}

// Begin returns the run a River attempt works on: the queued run created
// with the River job (EnqueueTx), or a new queued run for a retry. A run
// of an earlier attempt that is still running was interrupted (worker
// crash, rescued job): it is finished as failed first.
func (s *Store) Begin(ctx context.Context, p BeginParams) (Run, error) {
	var out Run
	err := db.Tx(ctx, s.db, func(q *sqlc.Queries) error {
		cur, err := q.LatestRunForRiverJob(ctx, p.RiverJobID)
		switch {
		case err == nil && cur.Status == StatusQueued:
			out = runOf(&cur)
			return nil
		case err == nil && cur.Status == StatusRunning:
			if _, err := s.finishTx(ctx, q, Ref{RunID: cur.ID, JobID: cur.JobID}, Result{
				Status:  StatusFailed,
				Failure: &Failure{Code: CodeInterrupted, Message: "the worker stopped before the run finished; a new attempt took over"},
			}); err != nil {
				return err
			}
		case err != nil && !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("runstate: load run: %w", err)
		}
		attempt := min(max(p.Attempt, 1), 1000)
		row, err := q.InsertRun(ctx, sqlc.InsertRunParams{
			JobID: p.JobID, Trigger: p.Trigger, TriggeredBy: p.TriggeredBy, ScheduledFor: p.ScheduledFor,
			RiverJobID: &p.RiverJobID, Attempt: int32(attempt),
		})
		if err != nil {
			return fmt.Errorf("runstate: insert run: %w", err)
		}
		out = runOf(&row)
		return publish(ctx, s.pub, q, EventRun, out.Ref)
	})
	return out, err
}

// Job is the definition of a job as a run needs it.
type Job struct {
	ID                uuid.UUID
	Name              string
	Slug              string
	Enabled           bool
	Deleted           bool
	ConnectionID      uuid.UUID
	ConnectionDeleted bool
	Engine            engine.Kind
	// VersionKey is the connection's dump client series (image key).
	VersionKey string
	// AllDatabases: db_mode all (Databases empty, Exclude applies).
	AllDatabases   bool
	Databases      []string
	Exclude        []string
	IncludeGlobals bool
	// Timeout is the job's own timeout (0: the queue cap).
	Timeout time.Duration
	// Destinations in fan-out order (soft-deleted ones included, flagged).
	Destinations []JobDestination
	// Verification is the completeness evidence level (task 3.10):
	// standard (estimates) or deep (exact row counts).
	Verification sqlc.JobVerification
}

// JobDestination is one destination of a job.
type JobDestination struct {
	ID      uuid.UUID
	Name    string
	Deleted bool
}

// Job loads a job (also a deleted one) with its connection and destinations.
func (s *Store) Job(ctx context.Context, id uuid.UUID) (Job, error) {
	q := sqlc.New(s.db)
	row, err := q.GetJobForRun(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrJobNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("runstate: load job: %w", err)
	}
	j := row.Job
	out := Job{
		ID: j.ID, Name: j.Name, Slug: j.Slug, Enabled: j.Enabled, Deleted: j.DeletedAt != nil, Verification: j.Verification,
		ConnectionID: j.ConnectionID, ConnectionDeleted: row.ConnectionDeletedAt != nil,
		Engine: engine.Kind(row.ConnectionEngine), VersionKey: row.ConnectionImageKey,
		AllDatabases: j.DbMode == sqlc.JobDbModeAll, Databases: j.Databases, Exclude: j.Exclude,
		IncludeGlobals: j.IncludeGlobals,
	}
	if j.TimeoutSeconds != nil {
		out.Timeout = time.Duration(*j.TimeoutSeconds) * time.Second
	}
	dests, err := q.ListJobDestinations(ctx, id)
	if err != nil {
		return Job{}, fmt.Errorf("runstate: load job destinations: %w", err)
	}
	for _, d := range dests {
		out.Destinations = append(out.Destinations, JobDestination{ID: d.DestinationID, Name: d.Name, Deleted: d.DeletedAt != nil})
	}
	return out, nil
}

// Start moves a queued run to running with its directory timestamp.
func (s *Store) Start(ctx context.Context, r Ref, attempt int, startedAt, storageTimestamp time.Time) error {
	return db.Tx(ctx, s.db, func(q *sqlc.Queries) error {
		_, err := q.StartRun(ctx, sqlc.StartRunParams{
			ID: r.RunID, StartedAt: startedAt, StorageTimestamp: storageTimestamp.UTC().Truncate(time.Second),
			Attempt: int32(min(max(attempt, 1), 1000)),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotActive
		}
		if err != nil {
			return fmt.Errorf("runstate: start run: %w", err)
		}
		return publish(ctx, s.pub, q, EventRun, r)
	})
}

// CancelRequested reports whether cancelRun marked the run.
func (s *Store) CancelRequested(ctx context.Context, runID uuid.UUID) (bool, error) {
	ok, err := sqlc.New(s.db).RunCancelRequested(ctx, runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrRunNotFound
	}
	return ok, err
}

// Result is the terminal state of a run.
type Result struct {
	Status   sqlc.RunStatus
	Failure  *Failure
	Warnings []Warning
	// Notices are informational findings that are not warnings (they do
	// not set has_warnings), e.g. databases_excluded.
	Notices         []Warning
	BytesRaw        int64
	BytesStored     int64
	LogLines        int
	LogLinesDropped int
}

// Finish records the terminal status of an active run (ErrNotActive when it
// already finished).
func (s *Store) Finish(ctx context.Context, r Ref, res Result) error {
	return db.TxWith(ctx, s.db, func(tx pgx.Tx, q *sqlc.Queries) error {
		if _, err := s.finishTx(ctx, q, r, res); err != nil {
			return err
		}
		s.afterFinish(ctx, tx, r, res.Status)
		return nil
	})
}

// afterFinish runs the finish hook under a savepoint (success and partial
// runs only).
func (s *Store) afterFinish(ctx context.Context, tx pgx.Tx, r Ref, status sqlc.RunStatus) {
	if s.onFinish == nil || (status != StatusSuccess && status != StatusPartial) {
		return
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		s.log.WarnContext(ctx, "run finish hook: savepoint failed", slog.String("run_id", r.RunID.String()), slog.Any("error", err))
		return
	}
	if err := s.onFinish(ctx, sp, r, status); err != nil {
		s.log.WarnContext(ctx, "run finish hook failed", slog.String("run_id", r.RunID.String()), slog.Any("error", err))
		if rbErr := sp.Rollback(ctx); rbErr != nil {
			s.log.WarnContext(ctx, "run finish hook: rollback to savepoint failed", slog.Any("error", rbErr))
		}
		return
	}
	if err := sp.Commit(ctx); err != nil {
		s.log.WarnContext(ctx, "run finish hook: release savepoint failed", slog.Any("error", err))
	}
}

func (s *Store) finishTx(ctx context.Context, q *sqlc.Queries, r Ref, res Result) (sqlc.Run, error) {
	return finishTx(ctx, q, s.pub, r, res, s.now())
}

func finishTx(ctx context.Context, q *sqlc.Queries, pub *pubsub.Publisher, r Ref, res Result, now time.Time) (sqlc.Run, error) {
	switch res.Status {
	case StatusSuccess, StatusPartial, StatusFailed, StatusCanceled, StatusSkipped:
	default:
		return sqlc.Run{}, fmt.Errorf("runstate: %q is not a terminal status", res.Status)
	}
	ws, err := warningsJSON(res.Warnings, RunWarningsBytes)
	if err != nil {
		return sqlc.Run{}, err
	}
	ns, err := warningsJSON(res.Notices, NoticesBytes)
	if err != nil {
		return sqlc.Run{}, err
	}
	code, msg := failureCols(res.Failure)
	row, err := q.FinishRun(ctx, sqlc.FinishRunParams{
		ID: r.RunID, Status: res.Status, FinishedAt: now, ErrorCode: code, ErrorMessage: msg, Warnings: ws, Notices: ns,
		BytesRaw: max(res.BytesRaw, 0), BytesStored: max(res.BytesStored, 0),
		LogLines:        int32(min(max(res.LogLines, 0), 1<<30)),
		LogLinesDropped: int32(min(max(res.LogLinesDropped, 0), 1<<30)),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Run{}, ErrNotActive
	}
	if err != nil {
		return sqlc.Run{}, fmt.Errorf("runstate: finish run: %w", err)
	}
	return row, publish(ctx, pub, q, EventRun, r)
}

// Item is a started run item.
type Item struct {
	ID       uuid.UUID
	Kind     sqlc.RunItemKind
	Database string
}

// AddItem records the start of one database (or the globals) of a run.
func (s *Store) AddItem(ctx context.Context, r Ref, kind sqlc.RunItemKind, database string, position int) (Item, error) {
	var out Item
	err := db.Tx(ctx, s.db, func(q *sqlc.Queries) error {
		var name *string
		if kind == sqlc.RunItemKindDatabase {
			name = &database
		}
		row, err := q.InsertRunItem(ctx, sqlc.InsertRunItemParams{
			RunID: r.RunID, Kind: kind, DbName: name, Position: int32(min(max(position, 0), 100000)),
		})
		if err != nil {
			return fmt.Errorf("runstate: insert item: %w", err)
		}
		out = Item{ID: row.ID, Kind: kind, Database: database}
		return publish(ctx, s.pub, q, EventRun, r)
	})
	return out, err
}

// ItemResult is the outcome of an item.
type ItemResult struct {
	Status     sqlc.RunItemStatus
	RawSize    int64
	StoredSize int64
	RawSHA256  []byte
	SHA256     []byte
	Verified   bool
	Failure    *Failure
	Warnings   []Warning
}

// FinishItem records the outcome of a running item.
func (s *Store) FinishItem(ctx context.Context, r Ref, itemID uuid.UUID, res ItemResult) error {
	ws, err := warningsJSON(res.Warnings, ItemWarningsBytes)
	if err != nil {
		return err
	}
	code, msg := failureCols(res.Failure)
	return db.Tx(ctx, s.db, func(q *sqlc.Queries) error {
		_, err := q.FinishRunItem(ctx, sqlc.FinishRunItemParams{
			ID: itemID, Status: res.Status, FinishedAt: s.now(), RawSize: max(res.RawSize, 0), StoredSize: max(res.StoredSize, 0),
			RawSha256: nilIfEmpty(res.RawSHA256), Sha256: nilIfEmpty(res.SHA256), Verified: res.Verified,
			ErrorCode: code, ErrorMessage: msg, Warnings: ws,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotActive
		}
		if err != nil {
			return fmt.Errorf("runstate: finish item: %w", err)
		}
		return publish(ctx, s.pub, q, EventRun, r)
	})
}

func nilIfEmpty(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}

// Artifact is one object finalized on a destination.
type Artifact struct {
	// ItemID is nil for the manifest.
	ItemID        *uuid.UUID
	DestinationID uuid.UUID
	Kind          sqlc.ArtifactKind
	Key           string
	Size          int64
	SHA256        []byte
	VersionID     string
	// EncryptionMode is "passphrase", "recipients" or "none" (empty for
	// the manifest) with the matching key version.
	EncryptionMode    string
	PassphraseVersion int
	RecipientVersion  int
	SigningKeyID      string
	Verified          bool
	LockUntil         time.Time
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func optInt(v int) *int32 {
	if v <= 0 {
		return nil
	}
	n := int32(min(v, 1<<30))
	return &n
}

// AddArtifact records a finalized object and returns its id.
func (s *Store) AddArtifact(ctx context.Context, r Ref, a Artifact) (uuid.UUID, error) {
	var lock *time.Time
	if !a.LockUntil.IsZero() {
		t := a.LockUntil.UTC()
		lock = &t
	}
	var id uuid.UUID
	err := db.Tx(ctx, s.db, func(q *sqlc.Queries) error {
		row, err := q.InsertArtifact(ctx, sqlc.InsertArtifactParams{
			RunID: r.RunID, RunItemID: a.ItemID, JobID: r.JobID, DestinationID: a.DestinationID, Kind: a.Kind,
			ObjectKey: a.Key, Size: a.Size, Sha256: a.SHA256, VersionID: optString(a.VersionID),
			EncryptionMode: optString(a.EncryptionMode), PassphraseVersion: optInt(a.PassphraseVersion),
			RecipientVersion: optInt(a.RecipientVersion), SigningKeyID: optString(a.SigningKeyID),
			Verified: a.Verified, LockUntil: lock,
		})
		if err != nil {
			return fmt.Errorf("runstate: insert artifact: %w", err)
		}
		id = row.ID
		return publish(ctx, s.pub, q, EventRun, r)
	})
	return id, err
}

// ErrSignedMismatch reports that MarkSigned could not sign exactly the
// listed artifacts (one is not a verified, unsigned object of the run on
// that destination); nothing was recorded.
var ErrSignedMismatch = errors.New("runstate: the manifest entries do not match the recorded artifacts")

// MarkSigned records the manifest signing key on exactly the artifacts the
// manifest on one destination lists (entries: their ids), together with the
// manifest itself, in one transaction. Other objects of the run on that
// destination (an item that failed after a destination committed, an
// unverified dump) stay unsigned.
func (s *Store) MarkSigned(ctx context.Context, r Ref, manifest Artifact, entries []uuid.UUID) error {
	if manifest.Kind != sqlc.ArtifactKindManifest || manifest.SigningKeyID == "" {
		return errors.New("runstate: MarkSigned needs the signed manifest")
	}
	if len(entries) == 0 {
		return errors.New("runstate: MarkSigned needs the manifest's entries")
	}
	ids := slices.Clone(entries)
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	ids = slices.Compact(ids)
	return db.Tx(ctx, s.db, func(q *sqlc.Queries) error {
		n, err := q.SetArtifactsSigned(ctx, sqlc.SetArtifactsSignedParams{
			RunID: r.RunID, DestinationID: manifest.DestinationID, SigningKeyID: &manifest.SigningKeyID, Ids: ids,
		})
		if err != nil {
			return fmt.Errorf("runstate: mark artifacts signed: %w", err)
		}
		if n != int64(len(ids)) {
			return ErrSignedMismatch
		}
		_, err = q.InsertArtifact(ctx, sqlc.InsertArtifactParams{
			RunID: r.RunID, JobID: r.JobID, DestinationID: manifest.DestinationID, Kind: sqlc.ArtifactKindManifest,
			ObjectKey: manifest.Key, Size: manifest.Size, Sha256: manifest.SHA256, VersionID: optString(manifest.VersionID),
			SigningKeyID: &manifest.SigningKeyID, Verified: true, LockUntil: lockPtr(manifest.LockUntil),
		})
		if err != nil {
			return fmt.Errorf("runstate: insert manifest artifact: %w", err)
		}
		return publish(ctx, s.pub, q, EventRun, r)
	})
}

func lockPtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

// RequestCancelTx marks an active run for cancellation in the caller's
// transaction (cancelRun) and publishes the change. ErrRunNotFound /
// ErrNotActive when there is nothing to cancel.
func RequestCancelTx(ctx context.Context, q *sqlc.Queries, pub *pubsub.Publisher, runID uuid.UUID, by *uuid.UUID, at time.Time) (sqlc.Run, error) {
	cur, err := q.GetRunForUpdate(ctx, runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Run{}, ErrRunNotFound
	}
	if err != nil {
		return sqlc.Run{}, fmt.Errorf("runstate: load run: %w", err)
	}
	if cur.Status != StatusQueued && cur.Status != StatusRunning {
		return cur, ErrNotActive
	}
	row, err := q.RequestRunCancel(ctx, sqlc.RequestRunCancelParams{ID: runID, RequestedAt: at, RequestedBy: by})
	if err != nil {
		return sqlc.Run{}, fmt.Errorf("runstate: request cancel: %w", err)
	}
	return row, publish(ctx, pub, q, EventRun, Ref{RunID: row.ID, JobID: row.JobID})
}

// CancelQueuedTx finishes a run that was canceled before a worker picked
// it up (its River job was canceled in the same transaction).
func CancelQueuedTx(ctx context.Context, q *sqlc.Queries, pub *pubsub.Publisher, r Ref, at time.Time) (sqlc.Run, error) {
	return finishTx(ctx, q, pub, r, Result{
		Status: StatusCanceled, Failure: &Failure{Code: CodeCanceled, Message: "the run was canceled before it started"},
	}, at)
}
