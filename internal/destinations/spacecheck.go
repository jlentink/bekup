package destinations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/storage"
)

// Stored space checks (D-227): every answer a backend gives to a space
// check (the hourly destination_space_check worker, or an on-demand
// destinationSpace call that asked the backend) is stored on the
// destination row (space_* columns) by recordSpace, only when the row did
// not change while the check ran and no newer answer was stored. Readers
// (the periodic summary, the storage page) use the stored answer and never
// ask a backend; a settings change (updated_at) makes it invisible to
// them. A check that finds a destination low on space (the D-225 rule)
// opens a low-space episode (space_low_since) and emits
// destination.low_space once (at most one opening event per destination
// and UTC day), then a reminder per LowSpaceReminderInterval while it
// stays low; the episode ends only after SpaceRecoveryAfter of checks that
// were not low (space_ok_since), a failed check keeps it. Check times are
// the database clock (amendment 1).

// Stable codes of stored space checks (space_code; besides these, the
// storage.ErrorCode of a failed check).
const (
	// SpaceCodeUnsupported: the backend type or server reports no space.
	SpaceCodeUnsupported = "unsupported"
	// SpaceCodeNotReady: the destination was not ready when checked.
	SpaceCodeNotReady = "not_ready"
	// SpaceCodeFailed: the check failed for an unclassified reason.
	SpaceCodeFailed = "failed"
)

// Space check limits.
const (
	// SpaceCheckInterval is the period of the scheduled space check.
	SpaceCheckInterval = time.Hour
	// SpaceStaleAfter is the age after which readers flag a stored
	// answer as stale (the hourly check has not refreshed it).
	SpaceStaleAfter = 3 * time.Hour
	// SpaceRecoveryAfter is how long a destination must have been not low
	// (consecutive checks) before its low-space episode ends: a server
	// alternating between low and not low stays in one episode.
	SpaceRecoveryAfter = 3 * time.Hour
	// SpaceCheckConcurrency bounds the destinations the scheduled check
	// asks at once.
	SpaceCheckConcurrency = 2
	// SpaceCheckBudget is the time after which the scheduled check starts
	// no further destination (well under queue.MaintenanceTimeout; the
	// ones it did not reach come first next hour).
	SpaceCheckBudget = 10 * time.Minute
	// LowSpaceReminderInterval is the period of destination.low_space
	// reminders while a destination stays low.
	LowSpaceReminderInterval = 24 * time.Hour
	// spaceRecordTimeout bounds storing one answer.
	spaceRecordTimeout = 10 * time.Second
)

// storedSpaceCode is the syntax of a stored space code (the DB checks it
// too).
var storedSpaceCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Code is the stored form of a low-space cause ("" for LowNone).
func (c LowCause) Code() string {
	switch c {
	case LowFraction:
		return "fraction"
	case LowBelowNewestSet:
		return "below_newest_set"
	default:
		return ""
	}
}

func lowCauseOf(code *string) LowCause {
	if code == nil {
		return LowNone
	}
	switch *code {
	case "fraction":
		return LowFraction
	case "below_newest_set":
		return LowBelowNewestSet
	default:
		return LowNone
	}
}

// StoredSpace is the stored answer of the newest space check (D-227).
type StoredSpace struct {
	// Status is "" when the destination was never checked or its settings
	// changed since the check (SettingsChanged).
	Status SpaceStatus
	// Code is the stable reason code when Status is not SpaceAvailable,
	// Reason its fixed text.
	Code   string
	Reason string
	// Space holds the reported values (SpaceAvailable only).
	Space storage.Space
	// NewestSetBytes is the newest full set of backups there when checked.
	NewestSetBytes int64
	// Low is the D-225 verdict when checked.
	Low LowCause
	// CheckedAt is when the backend was asked.
	CheckedAt time.Time
	// LowSince is the start of the current low-space episode (nil: none).
	LowSince *time.Time
	// SettingsChanged: an answer is stored but predates the newest
	// settings change, so it is not shown.
	SettingsChanged bool
}

// Checked reports whether a stored answer for the current settings exists.
func (s StoredSpace) Checked() bool { return s.Status != "" }

// Stale reports an answer older than SpaceStaleAfter at now, whatever its
// status: the hourly check refreshes every answer (also S3's unsupported
// and not-ready ones), so an old answer means it stopped.
func (s StoredSpace) Stale(now time.Time) bool {
	return s.Checked() && now.Sub(s.CheckedAt) > SpaceStaleAfter
}

// FreeKnown reports an available answer with a known free space.
func (s StoredSpace) FreeKnown() bool { return s.Status == SpaceAvailable && s.Space.FreeKnown }

// spaceColumns are the space_* columns of a destination row.
type spaceColumns struct {
	updatedAt                  time.Time
	checkedAt, lowSince, setAt *time.Time
	status, code, lowCause     *string
	total, free, used, newest  *int64
}

func spaceColumnsOf(row *sqlc.Destination) spaceColumns {
	return spaceColumns{
		updatedAt: row.UpdatedAt, checkedAt: row.SpaceCheckedAt, lowSince: row.SpaceLowSince, setAt: row.SpaceSettingsAt,
		status: row.SpaceStatus, code: row.SpaceCode, lowCause: row.SpaceLowCause,
		total: row.SpaceTotalBytes, free: row.SpaceFreeBytes, used: row.SpaceUsedBytes, newest: row.SpaceNewestSetBytes,
	}
}

// stored decodes the columns (the reader rule: an answer that predates the
// newest settings change is not shown).
func (c spaceColumns) stored(t Type) StoredSpace {
	if c.checkedAt == nil || c.status == nil || c.setAt == nil {
		return StoredSpace{}
	}
	if !c.setAt.Equal(c.updatedAt) {
		return StoredSpace{SettingsChanged: true}
	}
	s := StoredSpace{Status: SpaceStatus(*c.status), CheckedAt: *c.checkedAt, LowSince: c.lowSince, Low: lowCauseOf(c.lowCause)}
	switch s.Status {
	case SpaceAvailable, SpaceUnsupported, SpaceUnavailable:
	default:
		return StoredSpace{}
	}
	if c.code != nil {
		s.Code = *c.code
	}
	if s.Status == SpaceAvailable {
		if c.total != nil {
			s.Space.Total, s.Space.TotalKnown = *c.total, true
		}
		if c.free != nil {
			s.Space.Free, s.Space.FreeKnown = *c.free, true
		}
		if c.used != nil {
			s.Space.Used, s.Space.UsedKnown = *c.used, true
		}
	} else {
		s.Low = LowNone
	}
	if c.newest != nil {
		s.NewestSetBytes = *c.newest
	}
	s.Reason = storedSpaceReason(s.Status, s.Code, t)
	return s
}

// storedSpaceReason is the fixed text of a stored answer's code.
func storedSpaceReason(status SpaceStatus, code string, t Type) string {
	switch status {
	case SpaceUnsupported:
		return unsupportedSpaceReason(t)
	case SpaceUnavailable:
		switch code {
		case SpaceCodeNotReady:
			return "The destination was not ready when it was checked."
		case SpaceCodeFailed, "":
			return spaceReasonUnchecked
		}
		if m := storage.ErrorCode(code).Message(); m != "" {
			return m
		}
		return spaceReasonUnchecked
	default:
		return ""
	}
}

// StoredSpaces returns the stored space answers of the destinations that
// are not deleted (readers that never ask a backend, such as the periodic
// summary; D-227).
func StoredSpaces(ctx context.Context, q *sqlc.Queries) (map[uuid.UUID]StoredSpace, error) {
	rows, err := q.ListDestinationSpace(ctx)
	if err != nil {
		return nil, fmt.Errorf("destinations: stored space: %w", err)
	}
	out := make(map[uuid.UUID]StoredSpace, len(rows))
	for i := range rows {
		r := &rows[i]
		out[r.ID] = spaceColumns{
			updatedAt: r.UpdatedAt, checkedAt: r.SpaceCheckedAt, lowSince: r.SpaceLowSince, setAt: r.SpaceSettingsAt,
			status: r.SpaceStatus, code: r.SpaceCode, lowCause: r.SpaceLowCause,
			total: r.SpaceTotalBytes, free: r.SpaceFreeBytes, used: r.SpaceUsedBytes, newest: r.SpaceNewestSetBytes,
		}.stored(Type(r.Type))
	}
	return out, nil
}

// SpaceCheckOutcome is what storing one space check did.
type SpaceCheckOutcome struct {
	Status SpaceStatus
	Code   string
	Low    LowCause
	// Stored: the answer was stored (false: the row changed or was
	// deleted meanwhile, or a newer answer was stored).
	Stored bool
	// CheckedAt is the stored check time (database clock; zero when not
	// stored).
	CheckedAt time.Time
	// Notified: a destination.low_space event (or reminder) was emitted.
	Notified bool
}

// lowEpisode is a destination's low-space episode: lowSince its start
// (nil: none), okSince the first of the consecutive checks since then that
// were not low (nil: none, or the newest check was low).
type lowEpisode struct {
	lowSince, okSince *time.Time
}

// nextEpisode is the low-space episode after an answer at at: a failed
// check (unavailable, also not ready) keeps it as it is; a low one opens
// it or continues it (and resets the recovery streak); one that is not
// low (or cannot tell: unsupported, no free value) starts or continues the
// recovery streak and ends the episode once the streak is
// SpaceRecoveryAfter long (hysteresis against a flapping server).
func nextEpisode(prev lowEpisode, status SpaceStatus, cause LowCause, at time.Time) lowEpisode {
	switch {
	case status == SpaceUnavailable:
		return prev
	case cause != LowNone && status == SpaceAvailable:
		if prev.lowSince != nil {
			return lowEpisode{lowSince: prev.lowSince}
		}
		return lowEpisode{lowSince: &at}
	case prev.lowSince == nil:
		return lowEpisode{}
	}
	ok := prev.okSince
	if ok == nil || ok.After(at) {
		ok = &at
	}
	if at.Sub(*ok) >= SpaceRecoveryAfter {
		return lowEpisode{}
	}
	return lowEpisode{lowSince: prev.lowSince, okSince: ok}
}

// LowSpaceDedupKey is the dedup key of an episode's first
// destination.low_space event: per destination and UTC day of the episode
// start, so at most one episode a day notifies; reminder n appends
// ":r<n>".
func LowSpaceDedupKey(id uuid.UUID, since time.Time) string {
	return "destination.low_space:" + id.String() + ":" + since.UTC().Format("20060102")
}

// lowSpaceEvent returns the event of a low answer: the first notice when
// the answer opened the episode (prev nil), else the reminder of the
// LowSpaceReminderInterval periods since the episode started (none in the
// first one). Details are bounded scalars only.
func lowSpaceEvent(id uuid.UUID, prev, since *time.Time, cause LowCause, sp storage.Space, newest int64, at time.Time) (notify.Event, bool) {
	if cause == LowNone || since == nil {
		return notify.Event{}, false
	}
	key, reminder := LowSpaceDedupKey(id, *since), 0
	if prev != nil {
		reminder = int(at.Sub(*since) / LowSpaceReminderInterval)
		if reminder < 1 {
			return notify.Event{}, false
		}
		key += ":r" + strconv.Itoa(reminder)
	}
	detail := notify.Detail{
		"cause": cause.Code(), "free_bytes": sp.Free, "newest_set_bytes": newest,
		"low_since": since.UTC().Format(time.RFC3339), "reminder": reminder,
	}
	if sp.TotalKnown {
		detail["total_bytes"] = sp.Total
	}
	return notify.Event{
		Type: notify.DestinationLowSpace, Subject: notify.Subject{DestinationID: id}, Detail: detail, DedupKey: key,
	}, true
}

func optInt64(ok bool, v int64) *int64 {
	if !ok {
		return nil
	}
	return &v
}

// storedCodeOf is the stored code of an answer (the DB checks the same
// rule): none when available, exactly "unsupported" when unsupported, a
// stable code other than that when unavailable.
func storedCodeOf(status SpaceStatus, code string) *string {
	switch status {
	case SpaceAvailable:
		return nil
	case SpaceUnsupported:
		code = SpaceCodeUnsupported
	default:
		if !storedSpaceCode.MatchString(code) || code == SpaceCodeUnsupported {
			code = SpaceCodeFailed
		}
	}
	return &code
}

// recordSpace stores a backend's answer (the only writer of the space_*
// columns) in one transaction with its low-space event: under the row
// lock, only when the row is the one the check ran against and no newer
// answer is stored. The stored time is the database clock minus the age
// of the answer (measured with this process's monotonic clock), so
// answers of different processes compare on one clock. A busy answer (no
// checkedAt) is never stored.
func (s *Service) recordSpace(ctx context.Context, r *record, res spaceResult) (SpaceCheckOutcome, error) {
	out := SpaceCheckOutcome{Status: res.status, Code: res.code}
	if res.status == "" || res.checkedAt.IsZero() {
		return out, nil
	}
	age := max(s.now().Sub(res.checkedAt), 0)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), spaceRecordTimeout)
	defer cancel()
	id := r.row.ID
	err := db.Tx(ctx, s.o.DB, func(q *sqlc.Queries) error {
		out.Stored, out.Notified, out.Low, out.CheckedAt = false, false, LowNone, time.Time{}
		prev, err := q.LockDestinationSpace(ctx, sqlc.LockDestinationSpaceParams{AgeSeconds: age.Seconds(), ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("destinations: lock space: %w", err)
		}
		at := prev.CheckedAt
		if !prev.UpdatedAt.Equal(r.row.UpdatedAt) || (prev.SpaceCheckedAt != nil && !prev.SpaceCheckedAt.Before(at)) {
			return nil
		}
		arg := sqlc.RecordDestinationSpaceParams{
			ID: id, SettingsAt: r.row.UpdatedAt, CheckedAt: at, Status: string(res.status), Code: storedCodeOf(res.status, res.code),
		}
		cause, newest := LowNone, int64(0)
		if res.status == SpaceAvailable {
			if newest, err = q.DestinationNewestSetBytes(ctx, id); err != nil {
				return fmt.Errorf("destinations: newest backup set: %w", err)
			}
			cause = LowSpaceCause(res.space, newest)
			sp := res.space
			arg.TotalBytes, arg.FreeBytes, arg.UsedBytes = optInt64(sp.TotalKnown, sp.Total), optInt64(sp.FreeKnown, sp.Free), optInt64(sp.UsedKnown, sp.Used)
			arg.NewestSetBytes = &newest
			if c := cause.Code(); c != "" {
				arg.LowCause = &c
			}
		}
		ep := nextEpisode(lowEpisode{lowSince: prev.SpaceLowSince, okSince: prev.SpaceOkSince}, res.status, cause, at)
		arg.LowSince, arg.OkSince = ep.lowSince, ep.okSince
		n, err := q.RecordDestinationSpace(ctx, arg)
		if err != nil {
			return fmt.Errorf("destinations: record space: %w", err)
		}
		if n == 0 {
			return nil
		}
		out.Stored, out.Low, out.CheckedAt = true, cause, at
		if ev, ok := lowSpaceEvent(id, prev.SpaceLowSince, ep.lowSince, cause, res.space, newest, at); ok {
			evID, err := notify.Emit(ctx, q, ev)
			if err != nil {
				return fmt.Errorf("destinations: emit destination.low_space: %w", err)
			}
			out.Notified = evID != uuid.Nil
		}
		return nil
	})
	if err != nil {
		return SpaceCheckOutcome{Status: res.status, Code: res.code}, err
	}
	if !out.Stored {
		s.o.Logger.InfoContext(ctx, "space check result not stored: the destination changed or a newer result exists",
			slog.String("destination_id", id.String()))
	}
	return out, nil
}

// SpaceCheckTargets returns the destinations the scheduled check visits:
// every one that is not deleted, the longest unchecked first (never
// checked or checked under older settings, then the oldest answer), so a
// run cut short by its budget continues with the rest next hour.
func (s *Service) SpaceCheckTargets(ctx context.Context) ([]uuid.UUID, error) {
	ids, err := sqlc.New(s.o.DB).ListDestinationSpaceCheckTargets(ctx)
	if err != nil {
		return nil, fmt.Errorf("destinations: space check targets: %w", err)
	}
	return ids, nil
}

// CheckSpace is the scheduled space check of one destination (D-227, the
// system actor): it asks the backend afresh through the same path as
// Space (Open: SSRF guard, pins, host keys; SpaceTimeout; fixed codes; a
// pin violation is audited and fails closed) without the per-user rate
// limit (the worker bounds itself) and stores the answer. S3 is recorded
// as unsupported and a destination that is not ready as unavailable
// (not_ready), both without a network call, so every answer stays fresh.
// A pin violation already stored for the same settings is not audited
// again (amendment 1). ErrNotFound when the destination is gone.
func (s *Service) CheckSpace(ctx context.Context, id uuid.UUID) (SpaceCheckOutcome, error) {
	r, err := s.load(ctx, sqlc.New(s.o.DB), id, false)
	if err != nil {
		return SpaceCheckOutcome{}, err
	}
	return s.checkRecord(ctx, r)
}

func (s *Service) checkRecord(ctx context.Context, r *record) (SpaceCheckOutcome, error) {
	v := r.view()
	var res spaceResult
	switch {
	case v.Type == TypeS3:
		res = unsupportedSpace(TypeS3)
		res.checkedAt = s.now()
	case !v.Ready:
		res = spaceResult{status: SpaceUnavailable, reason: spaceReasonNotReady + v.NotReadyReason, code: SpaceCodeNotReady, checkedAt: s.now()}
	default:
		audited := ""
		if v.Space.Status == SpaceUnavailable && isMismatchCode(v.Space.Code) {
			audited = v.Space.Code
		}
		var err error
		if res, err = s.askBackend(ctx, Actor{}, r, audited); err != nil {
			return SpaceCheckOutcome{}, err
		}
	}
	if s.storeSpace == nil {
		return SpaceCheckOutcome{Status: res.status, Code: res.code}, nil
	}
	return s.storeSpace(ctx, r, res)
}

// spaceChecker is what the worker needs of the service.
type spaceChecker interface {
	SpaceCheckTargets(ctx context.Context) ([]uuid.UUID, error)
	CheckSpace(ctx context.Context, id uuid.UUID) (SpaceCheckOutcome, error)
}

// SpaceCheckWorker runs the scheduled space check (River kind
// destination_space_check, maintenance queue, every SpaceCheckInterval):
// at most SpaceCheckConcurrency destinations at once, each bounded by its
// own deadline; one failure never stops the others. It starts no further
// destination once its budget is spent (or the job's deadline is closer
// than one destination's timeout) and then ends without an error: the
// destinations it did not reach are the oldest next hour, and a retry
// would only ask the reached ones again.
type SpaceCheckWorker struct {
	river.WorkerDefaults[queue.DestinationSpaceCheckArgs]
	checker     spaceChecker
	concurrency int
	timeout     time.Duration
	budget      time.Duration
	logger      *slog.Logger
}

// SpaceCheckTotals counts the outcomes of one scheduled check.
type SpaceCheckTotals struct {
	Destinations, Available, Low, Unsupported, Unavailable, Skipped, Failed, Notified int
	// NotReached counts the destinations the run did not start (budget
	// spent, deadline near or canceled).
	NotReached int
}

// Work implements river.Worker.
func (w *SpaceCheckWorker) Work(ctx context.Context, _ *river.Job[queue.DestinationSpaceCheckArgs]) error {
	_, err := w.run(ctx)
	return err
}

// stopAt is when the run starts no further destination.
func (w *SpaceCheckWorker) stopAt(ctx context.Context, start time.Time) time.Time {
	at := start.Add(w.budget)
	if dl, ok := ctx.Deadline(); ok {
		if t := dl.Add(-w.timeout); t.Before(at) {
			at = t
		}
	}
	return at
}

func (w *SpaceCheckWorker) run(ctx context.Context) (SpaceCheckTotals, error) {
	var tot SpaceCheckTotals
	ids, err := w.checker.SpaceCheckTargets(ctx)
	if err != nil {
		return tot, fmt.Errorf("destinations: space check targets: %w", err)
	}
	tot.Destinations = len(ids)
	stop := w.stopAt(ctx, time.Now())
	budget := time.NewTimer(time.Until(stop))
	defer budget.Stop()
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, max(w.concurrency, 1))
	)
	for i, id := range ids {
		acquired := false
		if ctx.Err() == nil && time.Now().Before(stop) {
			select {
			case sem <- struct{}{}:
				acquired = true
			case <-ctx.Done():
			case <-budget.C:
			}
		}
		if acquired && (ctx.Err() != nil || !time.Now().Before(stop)) {
			<-sem
			acquired = false
		}
		if !acquired {
			tot.NotReached = len(ids) - i
			break
		}
		wg.Add(1)
		go func(id uuid.UUID) {
			defer wg.Done()
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, w.timeout)
			defer cancel()
			out, err := w.checker.CheckSpace(cctx, id)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case errors.Is(err, ErrNotFound):
				tot.Skipped++
			case err != nil:
				// Internal failures only (database, audit): backend
				// failures are answers with fixed codes.
				tot.Failed++
				w.logger.WarnContext(ctx, "destination space check failed",
					slog.String("destination_id", id.String()), slog.Any("error", err))
			default:
				switch out.Status {
				case SpaceAvailable:
					tot.Available++
				case SpaceUnsupported:
					tot.Unsupported++
				case SpaceUnavailable:
					tot.Unavailable++
				}
				if out.Low != LowNone {
					tot.Low++
				}
				if out.Notified {
					tot.Notified++
				}
			}
		}(id)
	}
	wg.Wait()
	attrs := []any{
		slog.Int("destinations", tot.Destinations), slog.Int("available", tot.Available), slog.Int("low", tot.Low),
		slog.Int("unsupported", tot.Unsupported), slog.Int("unavailable", tot.Unavailable),
		slog.Int("skipped", tot.Skipped), slog.Int("failed", tot.Failed), slog.Int("notified", tot.Notified),
		slog.Int("not_reached", tot.NotReached),
	}
	if tot.NotReached > 0 {
		w.logger.WarnContext(ctx, "destination space check stopped before reaching every destination; the rest come first next time", attrs...)
	} else {
		w.logger.InfoContext(ctx, "destination space check finished", attrs...)
	}
	return tot, nil
}

// spaceCheckTimeout bounds one destination of the scheduled check: the
// backend call (SpaceTimeout), loading and storing.
func (s *Service) spaceCheckTimeout() time.Duration {
	return s.o.SpaceTimeout + 2*spaceRecordTimeout
}

// RegisterSpaceCheck registers the space check worker and its hourly
// periodic job (D-227).
func RegisterSpaceCheck(ws *queue.Workers, svc *Service) error {
	if svc == nil {
		return errors.New("destinations: the space check worker needs the service")
	}
	w := &SpaceCheckWorker{
		checker: svc, concurrency: SpaceCheckConcurrency, timeout: svc.spaceCheckTimeout(), budget: SpaceCheckBudget,
		logger: svc.o.Logger,
	}
	if err := queue.Register(ws, w); err != nil {
		return err
	}
	return ws.AddPeriodic(SpaceCheckInterval, queue.DestinationSpaceCheckArgs{})
}
