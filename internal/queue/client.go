package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Options configure New.
type Options struct {
	// Settings come from SettingsFromConfig.
	Settings Settings
	// Logger is the redacting process logger.
	Logger *slog.Logger
	// Workers makes this a worker client (Run executes jobs). Nil builds an
	// insert-only client (serve --no-worker): Run then just waits.
	Workers *Workers
	// FetchPollInterval overrides River's fallback poll interval (tests);
	// zero keeps River's default.
	FetchPollInterval time.Duration
}

// Client wraps a River client on the app pool. All inserts go through
// InsertTx, which applies the catalog's options.
type Client struct {
	river    *river.Client[pgx.Tx]
	config   river.Config
	settings Settings
	logger   *slog.Logger
	working  bool
}

// New builds the River client. It refuses a catalog whose args fail
// CheckArgs (D-043) and settings that break the River invariants.
func New(pool *pgxpool.Pool, opts Options) (*Client, error) {
	if pool == nil || opts.Logger == nil {
		return nil, errors.New("queue: pool and logger are required")
	}
	if err := opts.Settings.validate(); err != nil {
		return nil, fmt.Errorf("queue settings: %w", err)
	}
	if err := ValidateCatalog(); err != nil {
		return nil, fmt.Errorf("queue catalog: %w", err)
	}
	s := opts.Settings
	cfg := river.Config{
		Logger:                      slog.New(argsFilter{opts.Logger.Handler()}),
		RescueStuckJobsAfter:        s.RescueStuckJobsAfter,
		JobTimeout:                  s.Queues[QueueMaintenance].Timeout,
		MaxAttempts:                 MaxAttemptsBackup,
		CompletedJobRetentionPeriod: CompletedJobRetention,
		CancelledJobRetentionPeriod: CanceledJobRetention,
		DiscardedJobRetentionPeriod: DiscardedJobRetention,
		// bekup_app does not own River's indexes; REINDEX is an operator
		// task (docs/operations.md).
		ReindexerSchedule: river.NeverSchedule(),
		SoftStopTimeout:   0, // Run escalates itself (stop → cancel)
		// InsertTx accepts only catalog kinds, and a serve instance may
		// insert kinds whose worker runs elsewhere (bekup worker).
		SkipUnknownJobCheck: true,
	}
	working := opts.Workers != nil
	if working {
		if len(opts.Workers.kinds) == 0 {
			return nil, errors.New("queue: a worker client needs at least one registered worker")
		}
		if opts.Workers.settings.RescueStuckJobsAfter != s.RescueStuckJobsAfter {
			return nil, errors.New("queue: workers were built for other settings")
		}
		opts.Workers.sealed = true
		cfg.Workers = opts.Workers.river
		cfg.PeriodicJobs = opts.Workers.periodic
		cfg.Queues = map[string]river.QueueConfig{}
		for q, qs := range s.Queues {
			cfg.Queues[string(q)] = river.QueueConfig{MaxWorkers: qs.MaxWorkers}
		}
		cfg.FetchPollInterval = opts.FetchPollInterval
		// Fetch only kinds with a registered worker: a kind whose worker
		// is not deployed yet (or not in an older binary during a rolling
		// upgrade) waits instead of failing as an unknown kind.
		cfg.FetchOnlyKnownKinds = true
	}
	rc, err := river.NewClient(riverpgxv5.New(pool), &cfg)
	if err != nil {
		return nil, fmt.Errorf("queue: river client: %w", err)
	}
	return &Client{river: rc, config: cfg, settings: s, logger: opts.Logger, working: working}, nil
}

// RiverConfig returns the configuration the River client was built with.
func (c *Client) RiverConfig() river.Config { return c.config }

// Settings returns the settings of the client.
func (c *Client) Settings() Settings { return c.settings }

// Working reports whether the client executes jobs.
func (c *Client) Working() bool { return c.working }

// River exposes the underlying client for job management (cancel, list) by
// later tasks. Never insert through it: use InsertTx.
func (c *Client) River() *river.Client[pgx.Tx] { return c.river }

// InsertOptions are the per-insert choices left to callers; queue, attempts
// and uniqueness always come from the catalog.
type InsertOptions struct {
	// ScheduledAt delays the job; zero = available now.
	ScheduledAt time.Time
}

// InsertResult describes an insert.
type InsertResult struct {
	// JobID is River's job ID (the existing job's on a duplicate).
	JobID int64
	// Duplicate reports that a unique job with the same key is still active
	// (UniqueStates); nothing was inserted.
	Duplicate bool
}

// ErrUnknownKind is returned for args whose kind is not in the catalog.
var ErrUnknownKind = errors.New("queue: unknown job kind")

func insertOpts(spec KindSpec, s Settings, o InsertOptions) *river.InsertOpts {
	opts := &river.InsertOpts{
		Queue:       string(spec.Queue),
		MaxAttempts: spec.maxAttempts(s),
		ScheduledAt: o.ScheduledAt,
	}
	if spec.Unique {
		opts.UniqueOpts = river.UniqueOpts{ByArgs: true, ByState: UniqueStates()}
	}
	return opts
}

// InsertTx inserts args inside tx (committed or rolled back with the
// caller's other writes).
func (c *Client) InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, o InsertOptions) (InsertResult, error) {
	spec, ok := catalog[args.Kind()]
	if !ok {
		return InsertResult{}, fmt.Errorf("%w %q", ErrUnknownKind, args.Kind())
	}
	res, err := c.river.InsertTx(ctx, tx, args, insertOpts(spec, c.settings, o))
	if err != nil {
		return InsertResult{}, fmt.Errorf("insert %s job: %w", args.Kind(), err)
	}
	return InsertResult{JobID: res.Job.ID, Duplicate: res.UniqueSkippedAsDuplicate}, nil
}

// CancelTx cancels the River job jobID inside tx (River's JobCancelTx): a
// job that is still queued is canceled at once, a running one is marked
// and its worker's context is canceled (cross-process via River's control
// notifications or polling). It returns the job's state after the call;
// ErrJobNotFound when River no longer has the row.
func (c *Client) CancelTx(ctx context.Context, tx pgx.Tx, jobID int64) (rivertype.JobState, error) {
	row, err := c.river.JobCancelTx(ctx, tx, jobID)
	if errors.Is(err, river.ErrNotFound) {
		return "", ErrJobNotFound
	}
	if err != nil {
		return "", fmt.Errorf("cancel job %d: %w", jobID, err)
	}
	return row.State, nil
}

// CancelPendingTx cancels the River job jobID inside tx only while it waits
// to be worked (available, scheduled, pending or retryable: e.g. a backup
// whose attempt failed and that waits for its retry); a running or finished
// job is left alone. It returns the job's state before the call and whether
// it was canceled; ErrJobNotFound when River no longer has the row. The row
// is locked first, so a worker cannot fetch it between the check and the
// cancel.
func (c *Client) CancelPendingTx(ctx context.Context, tx pgx.Tx, jobID int64) (rivertype.JobState, bool, error) {
	state, err := sqlc.New(tx).LockRiverJobState(ctx, jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, ErrJobNotFound
	}
	if err != nil {
		return "", false, fmt.Errorf("load job %d: %w", jobID, err)
	}
	st := rivertype.JobState(state)
	switch st {
	case rivertype.JobStateAvailable, rivertype.JobStateScheduled, rivertype.JobStatePending, rivertype.JobStateRetryable:
	default:
		return st, false, nil
	}
	if _, err := c.CancelTx(ctx, tx, jobID); err != nil {
		return st, false, err
	}
	return st, true, nil
}

// ErrJobNotFound reports that River has no job with that id (finished rows are deleted
// after their retention period).
var ErrJobNotFound = errors.New("queue: job not found")

// Name implements the app component interface.
func (c *Client) Name() string { return "river workers" }

// Run starts the workers and blocks until ctx is canceled, then shuts down
// gracefully: River stops fetching, running jobs get ShutdownTimeout to
// finish, then their contexts are canceled and Run waits at most
// HardStopTimeout more. An insert-only client just waits for ctx.
func (c *Client) Run(ctx context.Context) error {
	if !c.working {
		<-ctx.Done()
		return nil
	}
	// River must not see ctx: its cancellation would be a hard stop.
	if err := c.river.Start(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("start river: %w", err)
	}
	c.logger.InfoContext(ctx, "river workers started",
		slog.Duration("rescue_stuck_jobs_after", c.settings.RescueStuckJobsAfter))
	select {
	case <-ctx.Done():
	case <-c.river.Stopped():
		return errors.New("river client stopped unexpectedly")
	}
	return c.shutdown(context.WithoutCancel(ctx))
}

func (c *Client) shutdown(ctx context.Context) error {
	c.logger.InfoContext(ctx, "stopping river workers", slog.Duration("timeout", c.settings.ShutdownTimeout))
	sctx, cancel := context.WithTimeout(ctx, c.settings.ShutdownTimeout)
	err := c.river.Stop(sctx)
	cancel()
	if err == nil {
		c.logger.InfoContext(ctx, "river workers stopped")
		return nil
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("stop river: %w", err)
	}
	c.logger.WarnContext(ctx, "running jobs did not finish in time; canceling them",
		slog.Duration("timeout", c.settings.HardStopTimeout))
	hctx, hcancel := context.WithTimeout(ctx, c.settings.HardStopTimeout)
	defer hcancel()
	if err := c.river.StopAndCancel(hctx); err != nil {
		return fmt.Errorf("hard stop river: %w", err)
	}
	c.logger.InfoContext(ctx, "river workers stopped after canceling running jobs")
	return nil
}
