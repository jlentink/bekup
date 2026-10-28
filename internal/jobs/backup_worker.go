package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/runnerpb"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/storage"
)

// The backup run worker (task 3.6, D-147): River kind backup_run. See
// docs/architecture.md "Backup flow".

var (
	errCanceledRemotely = rivertype.ErrJobCancelledRemotely
	codeShape           = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	versionShape        = regexp.MustCompile(`^[0-9A-Za-z.+_-]{1,64}$`)
)

// RunStore is the run state the worker writes (*runstate.Store).
type RunStore interface {
	Begin(ctx context.Context, p runstate.BeginParams) (runstate.Run, error)
	Job(ctx context.Context, id uuid.UUID) (runstate.Job, error)
	Start(ctx context.Context, r runstate.Ref, attempt int, startedAt, storageTimestamp time.Time) error
	CancelRequested(ctx context.Context, runID uuid.UUID) (bool, error)
	AddItem(ctx context.Context, r runstate.Ref, kind sqlc.RunItemKind, database string, position int) (runstate.Item, error)
	FinishItem(ctx context.Context, r runstate.Ref, itemID uuid.UUID, res runstate.ItemResult) error
	AddArtifact(ctx context.Context, r runstate.Ref, a runstate.Artifact) (uuid.UUID, error)
	MarkSigned(ctx context.Context, r runstate.Ref, manifest runstate.Artifact, entries []uuid.UUID) error
	Finish(ctx context.Context, r runstate.Ref, res runstate.Result) error
	AppendLogs(ctx context.Context, r runstate.Ref, lines []runstate.LogLine) error
}

// RunSlots is the global / per-connection run semaphore (*runstate.Slots).
type RunSlots interface {
	TryAcquire(ctx context.Context, holder, connection uuid.UUID, kind runstate.SlotKind, l runstate.SlotLimits) (bool, error)
	Heartbeat(ctx context.Context, holder uuid.UUID) error
	Release(ctx context.Context, holder uuid.UUID) error
}

// TargetSource builds runner targets with unsealed credentials and records
// runner security events (*connections.Service).
type TargetSource interface {
	// Target returns the request target; release zeroes its credentials.
	Target(ctx context.Context, id uuid.UUID) (*runnerpb.Target, func(), error)
	RecordRunFailure(ctx context.Context, id uuid.UUID, op string, err error) error
}

// DestinationOpener opens destination backends, soft-deleted ones included
// (*destinations.Service).
type DestinationOpener interface {
	Open(ctx context.Context, id uuid.UUID) (storage.Destination, error)
	RecordRunFailure(ctx context.Context, id uuid.UUID, code storage.ErrorCode) error
}

// EncryptorSource yields a job's encryption for a run (*jobkeys.Service).
type EncryptorSource interface {
	Encryptor(job uuid.UUID) pipeline.Encryptor
}

// ManifestSigner provides the instance signing key (purpose "manifest").
// The caller destroys the key.
type ManifestSigner interface {
	SigningKey(ctx context.Context) (*crypto.SigningKey, error)
}

// PipelineTuning overrides pipeline defaults (zero values keep them).
type PipelineTuning struct {
	CompressionLevel int
	ChunkSize        int
	BufferChunks     int
	LagTimeout       time.Duration
}

// Defaults of the worker.
const (
	DefaultCancelPoll    = 2 * time.Second
	DefaultSlotRetry     = 15 * time.Second
	DefaultProbeTimeout  = time.Minute
	finalizeTimeout      = 2 * time.Minute
	defaultBekupVersion  = "dev"
	maxServerVersionLen  = 128
	runOpProbe           = "backup_probe"
	runOpDump            = "backup_dump"
	runOpDumpGlobals     = "backup_dump_globals"
	slotRetryJitterRatio = 4
)

// BackupWorkerOptions configure NewBackupWorker.
type BackupWorkerOptions struct {
	Store        RunStore
	Slots        RunSlots
	Targets      TargetSource
	Runner       runnerpb.RunnerServiceClient
	Destinations DestinationOpener
	Keys         EncryptorSource
	Signer       ManifestSigner
	// Stats is the completeness evidence hook (nil: NoStats).
	Stats StatsCollector
	// Limits are workers.max_concurrent_runs / max_runs_per_connection.
	Limits runstate.SlotLimits
	// QueueTimeout is workers.backup_timeout (the cap of every run).
	QueueTimeout time.Duration
	// LogMaxLines / LogMaxLineBytes are the run-log caps.
	LogMaxLines      int
	LogMaxLineBytes  int
	LogFlushInterval time.Duration
	// BekupVersion goes into the manifest.
	BekupVersion string
	Pipeline     PipelineTuning
	// Registry holds known secret values for redaction (nil:
	// logging.DefaultRegistry). The run registers its connection's
	// credentials and the job passphrase for its duration.
	Registry *logging.Registry
	Logger   *slog.Logger
	Now      func() time.Time
	// CancelPoll is how often the run checks cancel_requested_at (besides
	// River's own cancel signal).
	CancelPoll time.Duration
	// SlotRetry is the snooze while no slot is free.
	SlotRetry time.Duration
	// SlotHeartbeat overrides runstate.SlotHeartbeat (tests).
	SlotHeartbeat time.Duration
	ProbeTimeout  time.Duration
}

// BackupWorker works backup_run jobs.
type BackupWorker struct {
	river.WorkerDefaults[queue.BackupRunArgs]
	o BackupWorkerOptions
}

// NewBackupWorker validates o.
func NewBackupWorker(o BackupWorkerOptions) (*BackupWorker, error) {
	if o.Store == nil || o.Slots == nil || o.Targets == nil || o.Runner == nil || o.Destinations == nil ||
		o.Keys == nil || o.Signer == nil {
		return nil, errors.New("jobs: the backup worker needs Store, Slots, Targets, Runner, Destinations, Keys and Signer")
	}
	if o.Limits.Global < 1 || o.Limits.PerConnection < 1 {
		return nil, errors.New("jobs: backup worker slot limits must be positive")
	}
	if o.QueueTimeout <= 0 {
		return nil, errors.New("jobs: the backup worker needs the queue timeout")
	}
	if o.Stats == nil {
		o.Stats = NoStats{}
	}
	if !versionShape.MatchString(o.BekupVersion) {
		// The manifest accepts printable ASCII without spaces only.
		o.BekupVersion = defaultBekupVersion
	}
	if o.Registry == nil {
		o.Registry = logging.DefaultRegistry
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.CancelPoll <= 0 {
		o.CancelPoll = DefaultCancelPoll
	}
	if o.SlotRetry <= 0 {
		o.SlotRetry = DefaultSlotRetry
	}
	if o.SlotHeartbeat <= 0 {
		o.SlotHeartbeat = runstate.SlotHeartbeat
	}
	if o.ProbeTimeout <= 0 {
		o.ProbeTimeout = DefaultProbeTimeout
	}
	return &BackupWorker{o: o}, nil
}

// finalCtx is a context for the final writes of a run: it survives the
// cancellation of the run and of River's attempt context, bounded in time.
func finalCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
}

// Work implements river.Worker. It returns nil for every recorded outcome
// (the runs table is the history) except: a canceled run cancels the River
// job (no retry); a run interrupted by shutdown, or one that stored nothing
// because the runner was unavailable, returns an error so River retries it
// once (D-043); a slot shortage snoozes the job.
func (w *BackupWorker) Work(ctx context.Context, job *river.Job[queue.BackupRunArgs]) error {
	args := job.Args
	trigger, err := runstate.TriggerOf(args.Trigger)
	if err != nil {
		return river.JobCancel(err)
	}
	run, err := w.o.Store.Begin(ctx, runstate.BeginParams{
		RiverJobID: job.ID, Attempt: job.Attempt, JobID: args.JobID, Trigger: trigger,
		ScheduledFor: timePtr(args.ScheduledFor), TriggeredBy: args.TriggeredBy,
	})
	if err != nil {
		return fmt.Errorf("jobs: begin backup run: %w", err)
	}
	log := w.o.Logger.With(slog.String("run_id", run.RunID.String()), slog.String("job_id", run.JobID.String()))
	if run.CancelRequested {
		return w.finishEarly(ctx, log, run, runstate.StatusCanceled, fail(CodeCanceled, "the run was canceled before it started"))
	}
	def, err := w.o.Store.Job(ctx, args.JobID)
	switch {
	case errors.Is(err, runstate.ErrJobNotFound):
		return w.finishEarly(ctx, log, run, runstate.StatusSkipped, fail(CodeJobMissing, "the job no longer exists"))
	case err != nil:
		return fmt.Errorf("jobs: load job for backup run: %w", err)
	case def.Deleted:
		return w.finishEarly(ctx, log, run, runstate.StatusSkipped, fail(CodeJobDeleted, "the job was deleted before the run started"))
	case !def.Enabled:
		return w.finishEarly(ctx, log, run, runstate.StatusSkipped, fail(CodeJobDisabled, "the job was disabled before the run started"))
	case def.ConnectionDeleted:
		return w.finishEarly(ctx, log, run, runstate.StatusFailed, fail(CodeConnectionDeleted, "the job's connection was deleted"))
	}
	ok, err := w.o.Slots.TryAcquire(ctx, run.RunID, def.ConnectionID, runstate.SlotBackup, w.o.Limits)
	if err != nil {
		return fmt.Errorf("jobs: acquire run slot: %w", err)
	}
	if !ok {
		log.DebugContext(ctx, "backup run waits for a concurrency slot")
		return river.JobSnooze(w.o.SlotRetry + time.Duration(time.Now().UnixNano()%int64(w.o.SlotRetry/slotRetryJitterRatio+1)))
	}
	defer func() {
		fctx, cancel := finalCtx(ctx)
		defer cancel()
		if err := w.o.Slots.Release(fctx, run.RunID); err != nil {
			log.WarnContext(fctx, "releasing the run slot failed", slog.Any("error", err))
		}
	}()
	e := newRunExec(w, run, def, job.Attempt, log)
	res := e.execute(ctx)
	return e.riverResult(res)
}

// finishEarly records a run that ends before it starts (skipped, canceled
// or failed) and maps the outcome to River.
func (w *BackupWorker) finishEarly(ctx context.Context, log *slog.Logger, run runstate.Run, st sqlc.RunStatus, f *runstate.Failure) error {
	fctx, cancel := finalCtx(ctx)
	defer cancel()
	err := w.o.Store.Finish(fctx, run.Ref, runstate.Result{Status: st, Failure: f})
	if err != nil && !errors.Is(err, runstate.ErrNotActive) {
		return fmt.Errorf("jobs: finish backup run: %w", err)
	}
	log.InfoContext(fctx, "backup run ended before it started", slog.String("status", string(st)), slog.String("code", f.Code))
	if st == runstate.StatusCanceled {
		return river.JobCancel(errRunCanceled)
	}
	return nil
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}
