package missed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/scheduler"
)

// Detector bounds and periods (D-188).
const (
	// Interval is the period of the missed_backup_check job.
	Interval = 5 * time.Minute
	// Lookback bounds how far back (from now, before the window) the
	// detector looks for occurrences whose deadline it has not judged
	// yet; the pending next_run_at is checked whatever its age.
	Lookback = 7 * 24 * time.Hour
	// ReminderInterval is the period of reminders while an outage lasts.
	ReminderInterval = 24 * time.Hour
	// BatchSize is the number of jobs per batch.
	BatchSize = 200
	// MaxOccurrencesPerJob caps the occurrences read per job (the newest;
	// a week of a 5-minute schedule fits).
	MaxOccurrencesPerJob = 2500
	// maxBatches bounds one check (BatchSize * maxBatches jobs).
	maxBatches = 500
)

// DB is what the detector needs (*pgxpool.Pool).
type DB interface {
	db.TxBeginner
	sqlc.DBTX
}

// Options configure New.
type Options struct {
	DB DB
	// DefaultTimeout is workers.backup_timeout: the timeout of jobs
	// without timeout_seconds and the cap of every job's timeout.
	DefaultTimeout time.Duration
	Logger         *slog.Logger
	Now            func() time.Time
}

// Detector emits backup.missed events.
type Detector struct{ o Options }

// New validates o.
func New(o Options) (*Detector, error) {
	if o.DB == nil || o.DefaultTimeout <= 0 {
		return nil, errors.New("missed: DB and DefaultTimeout are required")
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Detector{o: o}, nil
}

// Result summarizes one check.
type Result struct {
	// Jobs is the number of jobs checked.
	Jobs int
	// Outages is the number of outages found (new or already notified).
	Outages int
	// Emitted is the number of new events (first notices and reminders).
	Emitted int
}

// maxRun is the longest a run can take from its start to its end.
func (d *Detector) maxRun() time.Duration {
	return time.Duration(queue.MaxAttemptsBackup)*d.o.DefaultTimeout + Grace
}

type watched struct {
	id     uuid.UUID
	window time.Duration
	occs   []Occurrence
	minAt  time.Time
}

// Check runs one pass over all watched jobs.
func (d *Detector) Check(ctx context.Context) (Result, error) {
	var res Result
	now := d.o.Now().UTC()
	after := uuid.Nil
	for range maxBatches {
		rows, err := sqlc.New(d.o.DB).ListMissedCheckJobs(ctx, sqlc.ListMissedCheckJobsParams{After: after, BatchSize: BatchSize})
		if err != nil {
			return res, fmt.Errorf("missed: list jobs: %w", err)
		}
		if len(rows) == 0 {
			break
		}
		after = rows[len(rows)-1].ID
		if err := d.checkBatch(ctx, now, rows, &res); err != nil {
			return res, err
		}
		if len(rows) < BatchSize {
			break
		}
	}
	// Jobs no longer watched (disabled, deleted, manual) lose their state.
	if _, err := sqlc.New(d.o.DB).PruneJobMissedState(ctx); err != nil {
		return res, fmt.Errorf("missed: prune state: %w", err)
	}
	if res.Emitted > 0 {
		d.o.Logger.InfoContext(ctx, "missed backups notified",
			slog.Int("jobs", res.Jobs), slog.Int("outages", res.Outages), slog.Int("events", res.Emitted))
	}
	return res, nil
}

func (d *Detector) checkBatch(ctx context.Context, now time.Time, rows []sqlc.ListMissedCheckJobsRow, res *Result) error {
	jobs := make(map[uuid.UUID]*watched, len(rows))
	order := make([]uuid.UUID, 0, len(rows))
	var p sqlc.MissedCheckOccurrencesParams
	p.MaxPerJob = MaxOccurrencesPerJob
	for _, r := range rows {
		if manual(r.Schedule) {
			continue
		}
		w := &watched{id: r.ID, window: Window(r.ExpectedWindowSeconds, r.TimeoutSeconds, d.o.DefaultTimeout)}
		until := now.Add(-w.window)
		if r.NextRunAt != nil && !r.NextRunAt.After(until) {
			w.occs = append(w.occs, Occurrence{At: r.NextRunAt.UTC(), Source: SourcePending})
		}
		jobs[r.ID] = w
		order = append(order, r.ID)
		p.JobIds = append(p.JobIds, r.ID)
		p.Since = append(p.Since, until.Add(-Lookback))
		p.Until = append(p.Until, until)
	}
	res.Jobs += len(order)
	if len(order) == 0 {
		return nil
	}
	ongoing := map[uuid.UUID]Outage{}
	if err := d.evaluateBatch(ctx, now, jobs, order, p, ongoing, res); err != nil {
		return err
	}
	return d.saveState(ctx, now, order, ongoing)
}

// saveState records the batch's ongoing outages for Job.missed (D-196) and
// clears the state of the batch's other jobs, in one transaction.
func (d *Detector) saveState(ctx context.Context, now time.Time, order []uuid.UUID, ongoing map[uuid.UUID]Outage) error {
	return db.Tx(ctx, d.o.DB, func(q *sqlc.Queries) error {
		var ended []uuid.UUID
		for _, id := range order {
			o, ok := ongoing[id]
			if !ok {
				ended = append(ended, id)
				continue
			}
			if err := q.UpsertJobMissedState(ctx, sqlc.UpsertJobMissedStateParams{
				JobID: id, MissedSince: o.First, LastMissedAt: o.Last, Reason: o.Reason, CheckedAt: now,
			}); err != nil {
				return fmt.Errorf("missed: save state: %w", err)
			}
		}
		if len(ended) == 0 {
			return nil
		}
		if err := q.ClearJobMissedState(ctx, ended); err != nil {
			return fmt.Errorf("missed: clear state: %w", err)
		}
		return nil
	})
}

// evaluateBatch reads the occurrences and successes of one batch, emits
// the events and fills ongoing with the jobs' ongoing outages.
func (d *Detector) evaluateBatch(ctx context.Context, now time.Time, jobs map[uuid.UUID]*watched, order []uuid.UUID,
	p sqlc.MissedCheckOccurrencesParams, ongoing map[uuid.UUID]Outage, res *Result,
) error {
	occs, err := sqlc.New(d.o.DB).MissedCheckOccurrences(ctx, p)
	if err != nil {
		return fmt.Errorf("missed: occurrences: %w", err)
	}
	for _, o := range occs {
		w := jobs[o.JobID]
		if w == nil {
			continue
		}
		w.occs = append(w.occs, Occurrence{At: o.Occurrence.UTC(), Source: Source(o.Source), Status: o.Status, Finished: o.FinishedAt})
	}
	var sp sqlc.MissedCheckSuccessesParams
	sp.MaxRunSeconds = int64(d.maxRun() / time.Second)
	for _, id := range order {
		w := jobs[id]
		if len(w.occs) == 0 {
			continue
		}
		w.minAt = w.occs[0].At
		for _, o := range w.occs {
			if o.At.Before(w.minAt) {
				w.minAt = o.At
			}
		}
		sp.JobIds = append(sp.JobIds, id)
		sp.Since = append(sp.Since, w.minAt)
	}
	if len(sp.JobIds) == 0 {
		return nil
	}
	srows, err := sqlc.New(d.o.DB).MissedCheckSuccesses(ctx, sp)
	if err != nil {
		return fmt.Errorf("missed: successes: %w", err)
	}
	succ := make(map[uuid.UUID][]Success, len(sp.JobIds))
	for _, s := range srows {
		succ[s.JobID] = append(succ[s.JobID], Success{RunID: s.RunID, Finished: s.FinishedAt})
	}
	for _, id := range sp.JobIds {
		w := jobs[id]
		outages := Evaluate(w.occs, succ[id], w.window)
		if len(outages) == 0 {
			continue
		}
		if last := outages[len(outages)-1]; last.Ongoing {
			ongoing[id] = last
		}
		res.Outages += len(outages)
		n, err := d.emit(ctx, now, w, outages)
		if err != nil {
			return err
		}
		res.Emitted += n
	}
	return nil
}

// manual reports whether a stored schedule is manual (never missed). A
// schedule that does not compile is watched: the scheduler recorded it as
// unschedulable and that occurrence is missed.
func manual(raw []byte) bool {
	p, err := scheduler.CompileStored(raw)
	return err == nil && p.Schedule().Kind == scheduler.KindManual
}

// DedupKey is the dedup key of an outage's first notice; reminder n
// appends ":r<n>".
func DedupKey(job, after uuid.UUID) string {
	a := "never"
	if after != uuid.Nil {
		a = after.String()
	}
	return "backup.missed:" + job.String() + ":after:" + a
}

// emit writes the events of one job's outages in one transaction: the
// first notice of every outage (idempotent by DedupKey) and, for an
// ongoing outage already notified, one reminder per ReminderInterval.
func (d *Detector) emit(ctx context.Context, now time.Time, w *watched, outages []Outage) (int, error) {
	emitted := 0
	err := db.TxWith(ctx, d.o.DB, func(_ pgx.Tx, q *sqlc.Queries) error {
		emitted = 0
		for _, o := range outages {
			key := DedupKey(w.id, o.After)
			id, err := notify.Emit(ctx, q, event(w, o, key, 0))
			if err != nil {
				return fmt.Errorf("missed: emit: %w", err)
			}
			if id != uuid.Nil {
				emitted++
				continue
			}
			if !o.Ongoing {
				continue
			}
			first, err := q.MissedEventCreatedAt(ctx, key)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return fmt.Errorf("missed: first notice: %w", err)
			}
			n := int(now.Sub(first) / ReminderInterval)
			if n < 1 {
				continue
			}
			id, err = notify.Emit(ctx, q, event(w, o, key+":r"+strconv.Itoa(n), n))
			if err != nil {
				return fmt.Errorf("missed: emit reminder: %w", err)
			}
			if id != uuid.Nil {
				emitted++
			}
		}
		return nil
	})
	return emitted, err
}

func event(w *watched, o Outage, key string, reminder int) notify.Event {
	return notify.Event{
		Type:    notify.BackupMissed,
		Subject: notify.Subject{JobID: w.id},
		Detail: notify.Detail{
			"scheduled_for":      o.First.UTC().Format(time.RFC3339),
			"last_scheduled_for": o.Last.UTC().Format(time.RFC3339),
			"missed":             o.Missed,
			"window_seconds":     int64(w.window / time.Second),
			"reason":             o.Reason,
			"ongoing":            o.Ongoing,
			"reminder":           reminder,
		},
		DedupKey: key,
	}
}

// Worker works the periodic missed_backup_check job.
type Worker struct {
	river.WorkerDefaults[queue.MissedBackupCheckArgs]
	d *Detector
}

// Work implements river.Worker.
func (w *Worker) Work(ctx context.Context, _ *river.Job[queue.MissedBackupCheckArgs]) error {
	_, err := w.d.Check(ctx)
	return err
}

// Register registers the worker and its periodic job (every Interval).
func Register(ws *queue.Workers, d *Detector) error {
	if err := queue.Register(ws, &Worker{d: d}); err != nil {
		return err
	}
	return ws.AddPeriodic(Interval, queue.MissedBackupCheckArgs{})
}
