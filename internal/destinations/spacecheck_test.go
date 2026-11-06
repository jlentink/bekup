package destinations

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/storage"
)

// fakeChecker is the worker's view of the service.
type fakeChecker struct {
	ids     []uuid.UUID
	listErr error
	// check answers one destination (nil: available).
	check func(ctx context.Context, id uuid.UUID) (SpaceCheckOutcome, error)

	running, peak atomic.Int32
	mu            sync.Mutex
	asked         []uuid.UUID
}

func (f *fakeChecker) SpaceCheckTargets(context.Context) ([]uuid.UUID, error) {
	return f.ids, f.listErr
}

func (f *fakeChecker) CheckSpace(ctx context.Context, id uuid.UUID) (SpaceCheckOutcome, error) {
	n := f.running.Add(1)
	defer f.running.Add(-1)
	for {
		p := f.peak.Load()
		if n <= p || f.peak.CompareAndSwap(p, n) {
			break
		}
	}
	f.mu.Lock()
	f.asked = append(f.asked, id)
	f.mu.Unlock()
	if f.check == nil {
		time.Sleep(5 * time.Millisecond)
		return SpaceCheckOutcome{Status: SpaceAvailable, Stored: true}, nil
	}
	return f.check(ctx, id)
}

func testWorker(f *fakeChecker, concurrency int, timeout time.Duration) *SpaceCheckWorker {
	return &SpaceCheckWorker{checker: f, concurrency: concurrency, timeout: timeout, budget: time.Minute, logger: slog.New(slog.DiscardHandler)}
}

// TestSpaceCheckWorkerOutcomes: every outcome is counted; a destination
// that hangs is cut off by its own deadline and does not stop the others;
// errors are logged, not returned (the next hour tries again).
func TestSpaceCheckWorkerOutcomes(t *testing.T) {
	t.Parallel()
	ids := make([]uuid.UUID, 8)
	for i := range ids {
		ids[i] = uuid.New()
	}
	outcomes := map[uuid.UUID]func(ctx context.Context) (SpaceCheckOutcome, error){
		ids[0]: func(context.Context) (SpaceCheckOutcome, error) {
			return SpaceCheckOutcome{Status: SpaceAvailable, Stored: true}, nil
		},
		ids[1]: func(context.Context) (SpaceCheckOutcome, error) {
			return SpaceCheckOutcome{Status: SpaceAvailable, Low: LowFraction, Stored: true, Notified: true}, nil
		},
		ids[2]: func(context.Context) (SpaceCheckOutcome, error) {
			return SpaceCheckOutcome{Status: SpaceUnsupported, Code: SpaceCodeUnsupported, Stored: true}, nil
		},
		ids[3]: func(context.Context) (SpaceCheckOutcome, error) {
			return SpaceCheckOutcome{Status: SpaceUnavailable, Code: string(storage.CodeNetwork), Stored: true}, nil
		},
		ids[4]: func(ctx context.Context) (SpaceCheckOutcome, error) { // hangs: timeout
			<-ctx.Done()
			return SpaceCheckOutcome{}, ctx.Err()
		},
		ids[5]: func(context.Context) (SpaceCheckOutcome, error) {
			return SpaceCheckOutcome{Status: SpaceUnavailable, Code: SpaceCodeNotReady, Stored: true}, nil
		},
		ids[6]: func(context.Context) (SpaceCheckOutcome, error) { return SpaceCheckOutcome{}, ErrNotFound },
		ids[7]: func(context.Context) (SpaceCheckOutcome, error) {
			return SpaceCheckOutcome{}, errors.New("audit failed")
		},
	}
	f := &fakeChecker{ids: ids, check: func(ctx context.Context, id uuid.UUID) (SpaceCheckOutcome, error) {
		return outcomes[id](ctx)
	}}
	start := time.Now()
	tot, err := testWorker(f, 2, 100*time.Millisecond).run(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := SpaceCheckTotals{Destinations: 8, Available: 2, Low: 1, Unsupported: 1, Unavailable: 2, Skipped: 1, Failed: 2, Notified: 1}
	if tot != want {
		t.Fatalf("totals = %+v, want %+v", tot, want)
	}
	if len(f.asked) != len(ids) {
		t.Fatalf("asked %d of %d destinations", len(f.asked), len(ids))
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("the hanging destination held the run for %s", d)
	}
}

// TestSpaceCheckWorkerConcurrency: never more than the bound at once.
func TestSpaceCheckWorkerConcurrency(t *testing.T) {
	t.Parallel()
	f := &fakeChecker{}
	for range 20 {
		f.ids = append(f.ids, uuid.New())
	}
	tot, err := testWorker(f, SpaceCheckConcurrency, time.Second).run(t.Context())
	if err != nil || tot.Available != 20 {
		t.Fatalf("totals %+v, %v", tot, err)
	}
	if p := f.peak.Load(); p > SpaceCheckConcurrency || p < 1 {
		t.Fatalf("peak concurrency %d, bound %d", p, SpaceCheckConcurrency)
	}
}

// TestSpaceCheckWorkerStops: a canceled run starts no further checks and
// ends without an error (no retry: the ones it did not reach come first
// next hour); a failed listing fails the job (River retries it).
func TestSpaceCheckWorkerStops(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	f := &fakeChecker{}
	for range 10 {
		f.ids = append(f.ids, uuid.New())
	}
	f.check = func(context.Context, uuid.UUID) (SpaceCheckOutcome, error) {
		cancel()
		return SpaceCheckOutcome{Status: SpaceAvailable}, nil
	}
	tot, err := testWorker(f, 1, time.Second).run(ctx)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.asked); n != 1 || tot.NotReached != 9 {
		t.Fatalf("asked %d destinations after the cancellation, not reached %d", n, tot.NotReached)
	}
	bad := &fakeChecker{listErr: errors.New("db down")}
	if _, err := testWorker(bad, 1, time.Second).run(t.Context()); err == nil {
		t.Fatal("a failed listing passed")
	}
}

// TestSpaceCheckWorkerBudget: with many slow destinations the run starts
// no further one once its budget is spent, or once the job's deadline is
// closer than one destination's timeout, and ends without an error (no
// retry storm); every destination is either asked or counted as not
// reached, in the order the targets came (the oldest answers first).
func TestSpaceCheckWorkerBudget(t *testing.T) {
	t.Parallel()
	slow := func(ctx context.Context, _ uuid.UUID) (SpaceCheckOutcome, error) {
		select {
		case <-time.After(20 * time.Millisecond):
		case <-ctx.Done():
		}
		return SpaceCheckOutcome{Status: SpaceUnavailable, Code: string(storage.CodeTimeout), Stored: true}, nil
	}
	ids := make([]uuid.UUID, 100)
	for i := range ids {
		ids[i] = uuid.New()
	}
	check := func(t *testing.T, f *fakeChecker, tot SpaceCheckTotals, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("a partial run failed: %v", err)
		}
		asked := len(f.asked)
		if asked == 0 || asked >= len(ids) || tot.NotReached != len(ids)-asked || tot.Unavailable != asked {
			t.Fatalf("asked %d, totals %+v", asked, tot)
		}
		got := map[uuid.UUID]bool{}
		for _, id := range f.asked {
			got[id] = true
		}
		for i := range asked {
			if !got[ids[i]] {
				t.Fatalf("destination %d of the target order was skipped", i)
			}
		}
	}
	t.Run("budget", func(t *testing.T) {
		t.Parallel()
		f := &fakeChecker{ids: ids, check: slow}
		w := testWorker(f, 2, time.Second)
		w.budget = 100 * time.Millisecond
		start := time.Now()
		tot, err := w.run(t.Context())
		check(t, f, tot, err)
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("run took %s", d)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		t.Parallel()
		f := &fakeChecker{ids: ids, check: slow}
		w := testWorker(f, 2, 300*time.Millisecond)
		ctx, cancel := context.WithTimeout(t.Context(), 400*time.Millisecond)
		defer cancel()
		tot, err := w.run(ctx)
		check(t, f, tot, err)
	})
}

// TestSpaceCheckBudgetFitsQueueTimeout: the budget plus one destination's
// timeout stays well inside the maintenance queue's timeout, so the run
// ends by itself instead of being canceled (and retried).
func TestSpaceCheckBudgetFitsQueueTimeout(t *testing.T) {
	t.Parallel()
	s := &Service{o: Options{SpaceTimeout: DefaultSpaceTimeout}}
	if got := SpaceCheckBudget + s.spaceCheckTimeout(); got > queue.MaintenanceTimeout-2*time.Minute {
		t.Fatalf("budget %s + timeout %s too close to the queue timeout %s", SpaceCheckBudget, s.spaceCheckTimeout(), queue.MaintenanceTimeout)
	}
}

// storeCapture records what checkRecord / askSpace store.
type storeCapture struct {
	mu   sync.Mutex
	got  []spaceResult
	fail error
}

func (c *storeCapture) store(_ context.Context, _ *record, res spaceResult) (SpaceCheckOutcome, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, res)
	return SpaceCheckOutcome{Status: res.status, Code: res.code, Stored: true}, c.fail
}

// TestCheckRecord: S3 is recorded as unsupported without a network call
// (the service has no guard: any network use would fail) on every check,
// so its answer never goes stale; a destination that is not ready is
// recorded as unavailable (not_ready) without a backend call; a disk
// destination is asked and its answer stored with a fixed code.
func TestCheckRecord(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	deny := func(string) (bool, time.Duration) {
		t.Error("the scheduled check must not take a per-user token")
		return false, time.Minute
	}
	s := spaceService(t, t.TempDir(), deny, clock(start))
	var c storeCapture
	s.storeSpace = c.store

	s3 := spaceRecord(t, sqlc.DestinationTypeS3, S3Config{Bucket: "b", Region: "eu-west-1"}, start)
	out, err := s.checkRecord(t.Context(), s3)
	if err != nil || !out.Stored || len(c.got) != 1 || c.got[0].status != SpaceUnsupported ||
		c.got[0].code != SpaceCodeUnsupported || !c.got[0].checkedAt.Equal(start) {
		t.Fatalf("s3: %+v, %v, stored %+v", out, err, c.got)
	}
	// Recorded again on the next check (refreshed, no network call).
	st, code, at, setAt := string(SpaceUnsupported), SpaceCodeUnsupported, start, s3.row.UpdatedAt
	s3.row.SpaceStatus, s3.row.SpaceCode, s3.row.SpaceCheckedAt, s3.row.SpaceSettingsAt = &st, &code, &at, &setAt
	if out, err := s.checkRecord(t.Context(), s3); err != nil || !out.Stored || len(c.got) != 2 || c.got[1].status != SpaceUnsupported {
		t.Fatalf("s3 again: %+v, %v, stored %d", out, err, len(c.got))
	}

	// Not ready (an SFTP destination without a confirmed host key): stored
	// as unavailable/not_ready without asking (no guard: a dial would fail).
	sftp := spaceRecord(t, sqlc.DestinationTypeSftp, SFTPConfig{Host: "h", Port: 22, User: "u", BasePath: "/b"}, start)
	if out, err := s.checkRecord(t.Context(), sftp); err != nil || !out.Stored || out.Code != SpaceCodeNotReady || len(c.got) != 3 ||
		c.got[2].status != SpaceUnavailable || c.got[2].code != SpaceCodeNotReady || !c.got[2].checkedAt.Equal(start) {
		t.Fatalf("not ready: %+v, %v, stored %+v", out, err, c.got)
	}

	disk := spaceRecord(t, sqlc.DestinationTypeDisk, DiskConfig{Path: "backups"}, start)
	if out, err := s.checkRecord(t.Context(), disk); err != nil || !out.Stored || len(c.got) != 4 ||
		c.got[3].status != SpaceAvailable || !c.got[3].space.FreeKnown || c.got[3].code != "" {
		t.Fatalf("disk: %+v, %v, stored %+v", out, err, c.got)
	}

	noRoot := spaceService(t, "", deny, clock(start))
	noRoot.storeSpace = c.store
	if _, err := noRoot.checkRecord(t.Context(), disk); err != nil || c.got[4].status != SpaceUnavailable || c.got[4].code != SpaceCodeNotReady {
		t.Fatalf("no disk root: %v, stored %+v", err, c.got[4])
	}
}

// TestSpaceOnDemandStores: an on-demand check stores the answer of a real
// backend call, never a busy answer or a cached one.
func TestSpaceOnDemandStores(t *testing.T) {
	t.Parallel()
	start := time.Now()
	var allow atomic.Bool
	s := spaceService(t, t.TempDir(), func(string) (bool, time.Duration) { return allow.Load(), time.Minute }, clock(start))
	c := storeCapture{fail: errors.New("db down")}
	s.storeSpace = c.store
	r := spaceRecord(t, sqlc.DestinationTypeDisk, DiskConfig{Path: "x"}, start)
	if res, err := s.spaceResult(t.Context(), Actor{}, r); err != nil || res.status != SpaceUnavailable || len(c.got) != 0 {
		t.Fatalf("busy: %+v, %v, stored %d", res, err, len(c.got))
	}
	allow.Store(true)
	// A storing failure is logged; the caller still gets the answer.
	if res, err := s.spaceResult(t.Context(), Actor{}, r); err != nil || res.status != SpaceAvailable || len(c.got) != 1 {
		t.Fatalf("asked: %+v, %v, stored %d", res, err, len(c.got))
	}
	if _, err := s.spaceResult(t.Context(), Actor{}, r); err != nil || len(c.got) != 1 {
		t.Fatalf("cached answer stored again: %v, %d", err, len(c.got))
	}
}

func refOf[T any](v T) *T { return &v }

// TestStoredSpace: the reader rule (never checked, settings changed,
// values only when available, fixed reasons by code and type, staleness).
func TestStoredSpace(t *testing.T) {
	t.Parallel()
	upd := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	at := upd.Add(time.Hour)
	cols := func(status, code string, setAt time.Time) spaceColumns {
		c := spaceColumns{updatedAt: upd, checkedAt: &at, setAt: &setAt, status: &status,
			total: refOf(int64(100)), free: refOf(int64(5)), newest: refOf(int64(3)), lowCause: refOf("fraction"), lowSince: &at}
		if code != "" {
			c.code = &code
		}
		return c
	}
	if s := (spaceColumns{updatedAt: upd}).stored(TypeDisk); s.Checked() || s.SettingsChanged {
		t.Fatalf("never checked: %+v", s)
	}
	if s := cols("available", "", upd.Add(-time.Second)).stored(TypeDisk); s.Checked() || !s.SettingsChanged {
		t.Fatalf("settings changed: %+v", s)
	}
	if s := cols("bogus", "", upd).stored(TypeDisk); s.Checked() {
		t.Fatalf("unknown status: %+v", s)
	}
	s := cols("available", "", upd).stored(TypeDisk)
	if !s.Checked() || !s.FreeKnown() || s.Space.Free != 5 || !s.Space.TotalKnown || s.Space.UsedKnown ||
		s.Low != LowFraction || s.NewestSetBytes != 3 || s.Reason != "" || !s.CheckedAt.Equal(at) || s.LowSince == nil {
		t.Fatalf("available: %+v", s)
	}
	if s.Stale(at.Add(SpaceStaleAfter)) || !s.Stale(at.Add(SpaceStaleAfter+time.Second)) {
		t.Fatal("staleness boundary")
	}
	// Values and the verdict belong to available answers only.
	f := cols("unavailable", "network", upd).stored(TypeSFTP)
	if f.Space.Known() || f.Low != LowNone || f.Reason != storage.CodeNetwork.Message() || f.FreeKnown() {
		t.Fatalf("unavailable: %+v", f)
	}
	for code, want := range map[string]string{
		SpaceCodeNotReady: "The destination was not ready when it was checked.",
		SpaceCodeFailed:   spaceReasonUnchecked,
		"something_new":   storage.ErrorCode("something_new").Message(),
		"timeout":         storage.CodeTimeout.Message(),
	} {
		if got := cols("unavailable", code, upd).stored(TypeSFTP).Reason; got != want {
			t.Errorf("reason of %s = %q, want %q", code, got, want)
		}
	}
	u := cols("unsupported", SpaceCodeUnsupported, upd).stored(TypeWebDAV)
	if u.Reason != spaceReasonWebDAV || u.Stale(at.Add(SpaceStaleAfter)) || !u.Stale(at.Add(SpaceStaleAfter+time.Second)) {
		t.Fatalf("unsupported (refreshed hourly, so it goes stale too): %+v", u)
	}
	if got := cols("unsupported", SpaceCodeUnsupported, upd).stored(TypeS3).Reason; got != spaceReasonS3 {
		t.Fatalf("s3 reason %q", got)
	}
}

// TestLowSpaceEpisode: the episode opens on the first low answer, a
// failed check keeps it (and its recovery streak), answers that are not
// low (or cannot tell) end it only after SpaceRecoveryAfter of them, a low
// answer resets the streak; the first notice comes once per destination
// and UTC day, reminders once per day after it.
func TestLowSpaceEpisode(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	prev, ok1 := t0.Add(-5*time.Hour), t0.Add(-time.Hour)
	okOld := t0.Add(-SpaceRecoveryAfter)
	ep := func(low, ok *time.Time) lowEpisode { return lowEpisode{lowSince: low, okSince: ok} }
	for _, c := range []struct {
		name   string
		prev   lowEpisode
		status SpaceStatus
		cause  LowCause
		want   lowEpisode
	}{
		{"opens", ep(nil, nil), SpaceAvailable, LowFraction, ep(&t0, nil)},
		{"continues", ep(&prev, nil), SpaceAvailable, LowBelowNewestSet, ep(&prev, nil)},
		{"low again resets the streak", ep(&prev, &ok1), SpaceAvailable, LowFraction, ep(&prev, nil)},
		{"not low starts the streak", ep(&prev, nil), SpaceAvailable, LowNone, ep(&prev, &t0)},
		{"not low continues the streak", ep(&prev, &ok1), SpaceAvailable, LowNone, ep(&prev, &ok1)},
		{"recovered after the streak", ep(&prev, &okOld), SpaceAvailable, LowNone, ep(nil, nil)},
		{"unsupported counts as not low", ep(&prev, &okOld), SpaceUnsupported, LowNone, ep(nil, nil)},
		{"unsupported starts the streak", ep(&prev, nil), SpaceUnsupported, LowNone, ep(&prev, &t0)},
		{"failed check keeps", ep(&prev, &ok1), SpaceUnavailable, LowNone, ep(&prev, &ok1)},
		{"failed check without episode", ep(nil, nil), SpaceUnavailable, LowNone, ep(nil, nil)},
		{"not low without episode", ep(nil, nil), SpaceAvailable, LowNone, ep(nil, nil)},
	} {
		got := nextEpisode(c.prev, c.status, c.cause, t0)
		if !sameTime(got.lowSince, c.want.lowSince) || !sameTime(got.okSince, c.want.okSince) {
			t.Errorf("%s: got %v/%v, want %v/%v", c.name, got.lowSince, got.okSince, c.want.lowSince, c.want.okSince)
		}
	}

	// A hostile server alternating every hour between low and not low:
	// one episode, one notice.
	var cur lowEpisode
	opened := 0
	for h := range 48 {
		at := t0.Add(time.Duration(h) * time.Hour)
		cause := LowNone
		if h%2 == 0 {
			cause = LowFraction
		}
		next := nextEpisode(cur, SpaceAvailable, cause, at)
		if cur.lowSince == nil && next.lowSince != nil {
			opened++
		}
		cur = next
	}
	if opened != 1 {
		t.Fatalf("flapping opened %d episodes", opened)
	}
	// Low for an hour, then not low for three hours: four hours per
	// episode, six a day, but one notice a day (the dedup key is per UTC
	// day of the start).
	cur, keys := lowEpisode{}, map[string]bool{}
	id0 := uuid.New()
	for h := range 24 {
		at := time.Date(2026, 11, 5, 0, 30, 0, 0, time.UTC).Add(time.Duration(h) * time.Hour)
		cause := LowNone
		if h%4 == 0 {
			cause = LowFraction
		}
		next := nextEpisode(cur, SpaceAvailable, cause, at)
		if ev, ok := lowSpaceEvent(id0, cur.lowSince, next.lowSince, cause, storage.Space{Free: 1, FreeKnown: true}, 0, at); ok {
			keys[ev.DedupKey] = true
		}
		cur = next
	}
	if len(keys) != 1 {
		t.Fatalf("dedup keys of a day of short episodes: %v", keys)
	}

	id := uuid.MustParse("6a3c1f6e-1d2b-4c3a-9e8f-0123456789ab")
	sp := storage.Space{Total: 100 << 30, Free: 5 << 30, TotalKnown: true, FreeKnown: true}
	dedup := regexp.MustCompile(`^[a-z][a-z0-9_.:-]*$`) // notify's dedup key syntax
	ev, ok := lowSpaceEvent(id, nil, &t0, LowFraction, sp, 7, t0)
	if !ok || ev.Type != notify.DestinationLowSpace || ev.Subject.DestinationID != id ||
		ev.DedupKey != "destination.low_space:"+id.String()+":20261105" || !dedup.MatchString(ev.DedupKey) {
		t.Fatalf("first notice: %+v, %v", ev, ok)
	}
	if ev.Detail["cause"] != "fraction" || ev.Detail["free_bytes"] != int64(5<<30) || ev.Detail["total_bytes"] != int64(100<<30) ||
		ev.Detail["newest_set_bytes"] != int64(7) || ev.Detail["reminder"] != 0 || ev.Detail["low_since"] != "2026-11-05T10:00:00Z" {
		t.Fatalf("detail %+v", ev.Detail)
	}
	if _, err := notify.ValidateDetail(ev.Detail); err != nil {
		t.Fatalf("detail rejected: %v", err)
	}
	for _, c := range []struct {
		after    time.Duration
		reminder int
	}{{time.Hour, 0}, {23 * time.Hour, 0}, {25 * time.Hour, 1}, {49 * time.Hour, 2}} {
		ev, ok := lowSpaceEvent(id, &t0, &t0, LowBelowNewestSet, storage.Space{Free: 1, FreeKnown: true}, 9, t0.Add(c.after))
		if ok != (c.reminder > 0) {
			t.Fatalf("after %s: event %v", c.after, ok)
		}
		if !ok {
			continue
		}
		if !strings.HasSuffix(ev.DedupKey, ":r"+strconv.Itoa(c.reminder)) || ev.Detail["reminder"] != c.reminder ||
			ev.Detail["cause"] != "below_newest_set" || ev.Detail["total_bytes"] != nil {
			t.Fatalf("after %s: %+v", c.after, ev)
		}
	}
	if _, ok := lowSpaceEvent(id, nil, nil, LowNone, sp, 0, t0); ok {
		t.Fatal("an event without low space")
	}
}

func sameTime(a, b *time.Time) bool {
	return (a == nil) == (b == nil) && (a == nil || a.Equal(*b))
}
