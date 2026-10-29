//go:build integration

package retention

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/storage"
	"github.com/jlentink/bekup/internal/storage/s3store"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

// opener opens test backends by destination id (a fresh backend per Open,
// like destinations.Service).
type opener struct {
	mu    sync.Mutex
	open  map[uuid.UUID]func() (storage.Destination, error)
	calls int
}

func (o *opener) Open(_ context.Context, id uuid.UUID) (storage.Destination, error) {
	o.mu.Lock()
	f := o.open[id]
	o.calls++
	o.mu.Unlock()
	if f == nil {
		return nil, errors.New("unknown destination")
	}
	return f()
}

type recIns struct {
	mu   sync.Mutex
	args []queue.RetentionSweepArgs
	fail bool
}

func (r *recIns) InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, _ queue.InsertOptions) (queue.InsertResult, error) {
	if r.fail {
		// Break the (savepoint) transaction like a failed insert would.
		_, err := tx.Exec(ctx, "SELECT 1/0")
		return queue.InsertResult{}, fmt.Errorf("insert failed: %w", err)
	}
	a, ok := args.(queue.RetentionSweepArgs)
	if !ok {
		return queue.InsertResult{}, fmt.Errorf("unexpected kind %s", args.Kind())
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.args = append(r.args, a)
	return queue.InsertResult{JobID: int64(len(r.args))}, nil
}

type env struct {
	t        *testing.T
	d        dbtest.DB
	pool     *pgxpool.Pool
	auditor  *audit.Auditor
	op       *opener
	diskRoot string
	ins      *recIns
	mu       sync.Mutex
	alerts   []Alert
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := dbtest.Migrated(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, d: d, pool: d.AppPool(t), op: &opener{open: map[uuid.UUID]func() (storage.Destination, error){}},
		diskRoot: root, ins: &recIns{}}
	e.auditor = audit.New(e.pool, audit.Options{})
	return e
}

func (e *env) worker(mod func(*Options)) *Worker {
	e.t.Helper()
	o := Options{
		DB: e.pool, Auditor: e.auditor, Destinations: e.op, Queue: func() Inserter { return e.ins },
		Logger: slog.New(slog.DiscardHandler),
		Alerts: func(_ context.Context, a Alert) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.alerts = append(e.alerts, a)
		},
	}
	if mod != nil {
		mod(&o)
	}
	w, err := New(o)
	if err != nil {
		e.t.Fatal(err)
	}
	return w
}

func (e *env) alertKinds() []AlertKind {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]AlertKind, 0, len(e.alerts))
	for _, a := range e.alerts {
		out = append(out, a.Kind)
	}
	return out
}

func (e *env) destRow(typ string, noDelete bool) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	if _, err := e.pool.Exec(e.t.Context(), `INSERT INTO destinations (id, name, type, config, no_delete) VALUES ($1, $2, $3, '{"path":"x"}', $4)`,
		id, typ+"-"+id.String()[:8], typ, noDelete); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) diskDest(dir string, noDelete bool) uuid.UUID {
	id := e.destRow("disk", noDelete)
	e.op.open[id] = func() (storage.Destination, error) {
		return storage.OpenDisk(storage.DiskConfig{Root: e.diskRoot, Dir: dir, NoDelete: noDelete})
	}
	return id
}

type jobInfo struct {
	id   uuid.UUID
	slug string
}

func (e *env) job(keepLast, keepDays int, deleted bool, dests ...uuid.UUID) jobInfo {
	e.t.Helper()
	ctx := e.t.Context()
	id, err := dbtest.InsertJob(ctx, e.pool, dbtest.JobRow{Enabled: !deleted, Deleted: deleted, DestinationIDs: dests})
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE jobs SET retention_keep_last = NULLIF($2::int, 0), retention_keep_days = NULLIF($3::int, 0) WHERE id = $1`,
		id, keepLast, keepDays); err != nil {
		e.t.Fatal(err)
	}
	var slug string
	if err := e.pool.QueryRow(ctx, `SELECT slug FROM jobs WHERE id = $1`, id).Scan(&slug); err != nil {
		e.t.Fatal(err)
	}
	return jobInfo{id: id, slug: slug}
}

type obj struct {
	id   uuid.UUID
	dest uuid.UUID
	key  storage.Key
}

type runInfo struct {
	id      uuid.UUID
	objects []obj
}

// on places a run on a destination: which items and whether the manifest.
type on struct {
	dest     uuid.UUID
	items    []int // nil = all
	noMan    bool
	putOpts  storage.PutOptions
	lockUtil time.Time // recorded lock_until
}

// run inserts a finished run aged `age` with n database items and stores its
// objects.
func (e *env) run(j jobInfo, age time.Duration, n int, places ...on) runInfo {
	e.t.Helper()
	ctx := e.t.Context()
	ts := time.Now().Add(-age).UTC().Truncate(time.Second)
	ri := runInfo{id: uuid.New()}
	if _, err := e.pool.Exec(ctx, `INSERT INTO runs (id, job_id, trigger, status, started_at, finished_at, storage_timestamp)
		VALUES ($1, $2, 'schedule', 'success', $3, $3, $3)`, ri.id, j.id, ts); err != nil {
		e.t.Fatal(err)
	}
	items := make([]uuid.UUID, n)
	for i := range n {
		items[i] = uuid.New()
		if _, err := e.pool.Exec(ctx, `INSERT INTO run_items (id, run_id, kind, db_name, position, status, finished_at, verified)
			VALUES ($1, $2, 'database', $3, $4, 'success', now(), true)`, items[i], ri.id, fmt.Sprintf("db%d", i), i); err != nil {
			e.t.Fatal(err)
		}
	}
	for pi := range places {
		p := &places[pi]
		idx := p.items
		if idx == nil {
			for i := range n {
				idx = append(idx, i)
			}
		}
		for _, i := range idx {
			key := storage.MustParseKey(fmt.Sprintf("%s/%s/db%d.dump", j.slug, storage.TimestampSegment(ts), i))
			ri.objects = append(ri.objects, e.store(ri.id, j.id, &items[i], p, key))
		}
		if !p.noMan {
			key := storage.MustParseKey(fmt.Sprintf("%s/%s/manifest.json", j.slug, storage.TimestampSegment(ts)))
			ri.objects = append(ri.objects, e.store(ri.id, j.id, nil, p, key))
		}
	}
	return ri
}

func (e *env) store(runID, jobID uuid.UUID, item *uuid.UUID, p *on, key storage.Key) obj {
	e.t.Helper()
	ctx := e.t.Context()
	data := []byte("backup bytes of " + key.String())
	sum := sha256.Sum256(data)
	d, err := e.op.Open(ctx, p.dest)
	if err != nil {
		e.t.Fatal(err)
	}
	opts := p.putOpts
	opts.ExpectedSHA256 = sum[:]
	if _, err := d.Put(ctx, key, bytes.NewReader(data), opts); err != nil {
		e.t.Fatalf("put %s: %v", key, err)
	}
	if err := d.Close(); err != nil {
		e.t.Fatal(err)
	}
	kind, mode := "database", any("none")
	if item == nil {
		kind, mode = "manifest", nil
	}
	var lock *time.Time
	if !p.lockUtil.IsZero() {
		lock = &p.lockUtil
	}
	id := uuid.New()
	if _, err := e.pool.Exec(ctx, `INSERT INTO artifacts (id, run_id, run_item_id, job_id, destination_id, kind, object_key, size, sha256,
		encryption_mode, verified, lock_until) VALUES ($1, $2, $3, $4, $5, $6::artifact_kind, $7, $8, $9, $10, true, $11)`,
		id, runID, item, jobID, p.dest, kind, key.String(), len(data), sum[:], mode, lock); err != nil {
		e.t.Fatal(err)
	}
	return obj{id: id, dest: p.dest, key: key}
}

func (e *env) state(id uuid.UUID) (string, bool) {
	e.t.Helper()
	var st string
	var deletedAt *time.Time
	if err := e.pool.QueryRow(e.t.Context(), `SELECT state::text, deleted_at FROM artifacts WHERE id = $1`, id).Scan(&st, &deletedAt); err != nil {
		e.t.Fatal(err)
	}
	return st, deletedAt != nil
}

func (e *env) exists(o obj) bool {
	e.t.Helper()
	d, err := e.op.Open(e.t.Context(), o.dest)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() {
		if err := d.Close(); err != nil {
			e.t.Error(err)
		}
	}()
	_, err = d.Stat(e.t.Context(), o.key)
	if errors.Is(err, storage.ErrNotFound) {
		return false
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return true
}

// removeBehindBack deletes an object outside bekup (bypassing no-delete and
// the artifact state).
func (e *env) removeBehindBack(o obj, dir string) {
	e.t.Helper()
	if err := os.Remove(filepath.Join(e.diskRoot, dir, filepath.FromSlash(o.key.String()))); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) assertRun(r runInfo, wantState string, wantExists bool) {
	e.t.Helper()
	for _, o := range r.objects {
		st, del := e.state(o.id)
		if st != wantState || del != (wantState == "deleted") {
			e.t.Errorf("artifact %s: state %s (deleted_at %v), want %s", o.key, st, del, wantState)
		}
		if got := e.exists(o); got != wantExists {
			e.t.Errorf("object %s: exists=%v, want %v", o.key, got, wantExists)
		}
	}
}

type auditRow struct {
	Action  string
	Target  string
	Details map[string]any
}

func (e *env) audits(prefix string) []auditRow {
	e.t.Helper()
	rows, err := e.pool.Query(e.t.Context(), `SELECT action, coalesce(target_id, ''), details FROM audit_log WHERE action LIKE $1 || '%' ORDER BY id`, prefix)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var r auditRow
		var raw []byte
		if err := rows.Scan(&r.Action, &r.Target, &raw); err != nil {
			e.t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &r.Details); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func actions(rs []auditRow) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Action)
	}
	return out
}

func TestIntegrationDiskKeepLast(t *testing.T) {
	e := newEnv(t)
	disk := e.diskDest("a", false)
	j := e.job(2, 0, false, disk)
	r := []runInfo{
		e.run(j, 0, 2, on{dest: disk}),
		e.run(j, day, 2, on{dest: disk}),
		e.run(j, 2*day, 2, on{dest: disk}),
		e.run(j, 3*day, 2, on{dest: disk}),
	}
	res, err := e.worker(nil).Sweep(t.Context(), j.id, queue.RetentionPostRun, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Destinations) != 1 || res.Destinations[0].Deleted != 6 || res.Destinations[0].Planned != 6 {
		t.Fatalf("result %+v", res.Destinations)
	}
	e.assertRun(r[0], "present", true)
	e.assertRun(r[1], "present", true)
	e.assertRun(r[2], "deleted", false)
	e.assertRun(r[3], "deleted", false)
	got := e.audits("retention.")
	if a := actions(got); len(a) != 2 || a[0] != "retention.deletion_planned" || a[1] != "retention.swept" {
		t.Fatalf("audit %v", a)
	}
	if got[1].Target != j.id.String() || got[1].Details["deleted"] != float64(6) {
		t.Errorf("swept entry %+v", got[1])
	}
	// A second sweep has nothing left to do and records nothing.
	if _, err := e.worker(nil).Sweep(t.Context(), j.id, queue.RetentionDaily, false); err != nil {
		t.Fatal(err)
	}
	if n := len(e.audits("retention.")); n != 2 {
		t.Errorf("idle sweep audited: %d entries", n)
	}
	if len(e.alertKinds()) != 0 {
		t.Errorf("alerts %v", e.alertKinds())
	}
}

func TestIntegrationMissingObjectAlerts(t *testing.T) {
	e := newEnv(t)
	disk := e.diskDest("m", false)
	j := e.job(1, 0, false, disk)
	newest := e.run(j, 0, 1, on{dest: disk})
	old := e.run(j, day, 2, on{dest: disk})
	e.removeBehindBack(old.objects[0], "m")

	res, err := e.worker(nil).Sweep(t.Context(), j.id, queue.RetentionDaily, false)
	if err != nil {
		t.Fatal(err)
	}
	dr := res.Destinations[0]
	if dr.Missing != 1 || dr.Deleted != 2 {
		t.Fatalf("result %+v", dr)
	}
	if st, _ := e.state(old.objects[0].id); st != "missing" {
		t.Errorf("vanished object state %s", st)
	}
	for _, o := range old.objects[1:] {
		if st, _ := e.state(o.id); st != "deleted" {
			t.Errorf("state %s", st)
		}
	}
	e.assertRun(newest, "present", true)
	if k := e.alertKinds(); len(k) != 1 || k[0] != AlertArtifactMissing {
		t.Errorf("alerts %v", k)
	}
	missing := e.audits("retention.artifact_missing")
	if len(missing) != 1 || missing[0].Target != old.objects[0].id.String() || missing[0].Details["newest_complete"] != false {
		t.Errorf("missing audit %+v", missing)
	}
}

func TestIntegrationNewestMissingAborts(t *testing.T) {
	e := newEnv(t)
	disk := e.diskDest("n", false)
	j := e.job(1, 0, false, disk)
	newest := e.run(j, 0, 1, on{dest: disk})
	old := e.run(j, day, 1, on{dest: disk})
	e.removeBehindBack(newest.objects[0], "n")

	res, err := e.worker(nil).Sweep(t.Context(), j.id, queue.RetentionDaily, false)
	if err != nil {
		t.Fatal(err)
	}
	if dr := res.Destinations[0]; dr.Aborted != AbortNewestMissing || dr.Deleted != 0 {
		t.Fatalf("result %+v", dr)
	}
	e.assertRun(old, "present", true)
	if st, _ := e.state(newest.objects[0].id); st != "missing" {
		t.Errorf("newest state %s", st)
	}
	if k := e.alertKinds(); len(k) != 2 || k[0] != AlertArtifactMissing || k[1] != AlertSweepAborted {
		t.Errorf("alerts %v", k)
	}
	if a := actions(e.audits("retention.")); len(a) != 2 || a[0] != "retention.artifact_missing" || a[1] != "retention.aborted" {
		t.Errorf("audit %v", a)
	}
	// Next sweep: the damaged run is no longer complete, so the old one is
	// the newest complete and stays.
	res, err = e.worker(nil).Sweep(t.Context(), j.id, queue.RetentionDaily, false)
	if err != nil {
		t.Fatal(err)
	}
	if dr := res.Destinations[0]; dr.Planned != 0 || dr.Plan.NewestComplete != old.id {
		t.Fatalf("second sweep %+v", dr)
	}
	e.assertRun(old, "present", true)
}

func TestIntegrationDeletionCapAndDryRun(t *testing.T) {
	e := newEnv(t)
	disk := e.diskDest("c", false)
	j := e.job(1, 0, false, disk)
	runs := make([]runInfo, 0, 4)
	for i := range 4 {
		runs = append(runs, e.run(j, time.Duration(i)*day, 1, on{dest: disk}))
	}
	// Dry run (config): nothing deleted, nothing audited, no backend call.
	calls := e.op.calls
	res, err := e.worker(func(o *Options) { o.DryRun = true }).Sweep(t.Context(), j.id, queue.RetentionDaily, true)
	if err != nil {
		t.Fatal(err)
	}
	if dr := res.Destinations[0]; dr.Planned != 6 || dr.Deleted != 0 || !res.DryRun {
		t.Fatalf("dry run %+v", dr)
	}
	if n := len(e.audits("retention.")); n != 0 || e.op.calls != calls {
		t.Fatalf("dry run audited %d entries / opened destinations %d times", n, e.op.calls-calls)
	}
	for _, r := range runs {
		e.assertRun(r, "present", true)
	}
	// Dry run through the args (Work).
	w := e.worker(nil)
	if err := w.Work(t.Context(), &river.Job[queue.RetentionSweepArgs]{Args: queue.RetentionSweepArgs{JobID: &j.id, Reason: queue.RetentionDaily, DryRun: true}}); err != nil {
		t.Fatal(err)
	}
	for _, r := range runs {
		e.assertRun(r, "present", true)
	}

	// A config reload turning retention.dry_run on applies to the next
	// sweep (D-200).
	commit, err := w.PrepareLimits(Limits{DryRun: true, MaxDeletions: 500, PartialCleanupAge: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	commit()
	if err := w.Work(t.Context(), &river.Job[queue.RetentionSweepArgs]{Args: queue.RetentionSweepArgs{JobID: &j.id, Reason: queue.RetentionDaily}}); err != nil {
		t.Fatal(err)
	}
	for _, r := range runs {
		e.assertRun(r, "present", true)
	}

	// Cap 3 (set by a reload): the oldest run (2 objects) and the next
	// run's dump go; its manifest waits for the next sweep.
	if commit, err = w.PrepareLimits(Limits{MaxDeletions: 3, PartialCleanupAge: time.Hour}); err != nil {
		t.Fatal(err)
	}
	commit()
	res, err = w.Sweep(t.Context(), j.id, queue.RetentionDaily, false)
	if err != nil {
		t.Fatal(err)
	}
	dr := res.Destinations[0]
	if dr.Deleted != 3 || !dr.Capped {
		t.Fatalf("capped %+v", dr)
	}
	e.assertRun(runs[3], "deleted", false)
	e.assertRun(runs[0], "present", true)
	sw := e.audits("retention.swept")
	if len(sw) != 1 || sw[0].Details["capped"] != true {
		t.Errorf("swept %+v", sw)
	}
	// The next sweep finishes the job.
	if _, err := e.worker(func(o *Options) { o.MaxDeletions = 3 }).Sweep(t.Context(), j.id, queue.RetentionDaily, false); err != nil {
		t.Fatal(err)
	}
	e.assertRun(runs[2], "deleted", false)
	e.assertRun(runs[1], "deleted", false)
	e.assertRun(runs[0], "present", true)
}

func TestIntegrationNoDeleteDiskSkips(t *testing.T) {
	e := newEnv(t)
	disk := e.diskDest("nd", true)
	j := e.job(0, 1, false, disk)
	e.run(j, 0, 1, on{dest: disk})
	old := e.run(j, day, 1, on{dest: disk})
	res, err := e.worker(nil).Sweep(t.Context(), j.id, queue.RetentionDaily, false)
	if err != nil {
		t.Fatal(err)
	}
	if dr := res.Destinations[0]; !dr.SkippedNoDelete || dr.Deleted != 0 {
		t.Fatalf("result %+v", dr)
	}
	e.assertRun(old, "present", true)
	if n := len(e.audits("retention.")); n != 0 {
		t.Errorf("no-delete skip audited %d entries", n)
	}
}

func TestIntegrationSoftDeletedJobAndDaily(t *testing.T) {
	e := newEnv(t)
	disk := e.diskDest("d", false)
	gone := e.job(1, 0, true, disk)
	e.run(gone, 0, 1, on{dest: disk})
	old := e.run(gone, day, 1, on{dest: disk})
	empty := e.job(3, 0, false, disk) // no artifacts: not enqueued

	// Leftover partial files: an old one goes, a fresh one stays.
	dir := filepath.Join(e.diskRoot, "d", gone.slug)
	oldPartial := filepath.Join(dir, "."+strings.Repeat("ab", 16)+".partial")
	newPartial := filepath.Join(dir, "."+strings.Repeat("cd", 16)+".partial")
	for _, p := range []string{oldPartial, newPartial} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldPartial, past, past); err != nil {
		t.Fatal(err)
	}

	w := e.worker(nil)
	if err := w.Work(t.Context(), &river.Job[queue.RetentionSweepArgs]{Args: queue.RetentionSweepArgs{Reason: queue.RetentionDaily}}); err != nil {
		t.Fatal(err)
	}
	if len(e.ins.args) != 1 || *e.ins.args[0].JobID != gone.id || e.ins.args[0].Reason != queue.RetentionDaily {
		t.Fatalf("enqueued %+v (empty job %s)", e.ins.args, empty.id)
	}
	if _, err := os.Stat(oldPartial); !os.IsNotExist(err) {
		t.Errorf("old partial file not removed: %v", err)
	}
	if _, err := os.Stat(newPartial); err != nil {
		t.Errorf("fresh partial file removed: %v", err)
	}
	// The enqueued sweep of the soft-deleted job applies its retention.
	if err := w.Work(t.Context(), &river.Job[queue.RetentionSweepArgs]{Args: e.ins.args[0]}); err != nil {
		t.Fatal(err)
	}
	e.assertRun(old, "deleted", false)

	// A dry-run daily enqueues dry-run sweeps and leaves partials alone.
	if err := os.Chtimes(newPartial, past, past); err != nil {
		t.Fatal(err)
	}
	if err := w.Daily(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if last := e.ins.args[len(e.ins.args)-1]; !last.DryRun {
		t.Error("dry-run daily enqueued a real sweep")
	}
	if _, err := os.Stat(newPartial); err != nil {
		t.Errorf("dry-run daily removed a partial file: %v", err)
	}
}

func TestIntegrationPostRunHook(t *testing.T) {
	e := newEnv(t)
	disk := e.diskDest("h", false)
	j := e.job(1, 0, false, disk)
	ctx := t.Context()
	newRun := func() runstate.Ref {
		id := uuid.New()
		if _, err := e.pool.Exec(ctx, `INSERT INTO runs (id, job_id, trigger, status, started_at) VALUES ($1, $2, 'manual', 'running', now())`, id, j.id); err != nil {
			t.Fatal(err)
		}
		return runstate.Ref{RunID: id, JobID: j.id}
	}
	status := func(r runstate.Ref) string {
		var s string
		if err := e.pool.QueryRow(ctx, `SELECT status::text FROM runs WHERE id = $1`, r.RunID).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	store := runstate.NewStore(e.pool, nil, nil).WithFinishHook(PostRunHook(func() Inserter { return e.ins }), slog.New(slog.DiscardHandler))

	r := newRun()
	if err := store.Finish(ctx, r, runstate.Result{Status: runstate.StatusPartial}); err != nil {
		t.Fatal(err)
	}
	if len(e.ins.args) != 1 || *e.ins.args[0].JobID != j.id || e.ins.args[0].Reason != queue.RetentionPostRun {
		t.Fatalf("enqueued %+v", e.ins.args)
	}
	// Failed runs do not trigger a sweep.
	r = newRun()
	if err := store.Finish(ctx, r, runstate.Result{Status: runstate.StatusFailed, Failure: &runstate.Failure{Code: "x", Message: "x"}}); err != nil {
		t.Fatal(err)
	}
	if len(e.ins.args) != 1 {
		t.Fatalf("failed run enqueued a sweep")
	}
	// A failing hook never fails the run's terminal write.
	e.ins.fail = true
	r = newRun()
	if err := store.Finish(ctx, r, runstate.Result{Status: runstate.StatusSuccess}); err != nil {
		t.Fatalf("hook failure failed the finish: %v", err)
	}
	if s := status(r); s != "success" {
		t.Fatalf("status %s", s)
	}
	// No queue yet: logged, the run still finishes.
	store = runstate.NewStore(e.pool, nil, nil).WithFinishHook(PostRunHook(func() Inserter { return nil }), nil)
	r = newRun()
	if err := store.Finish(ctx, r, runstate.Result{Status: runstate.StatusSuccess}); err != nil || status(r) != "success" {
		t.Fatalf("finish without queue: %v", err)
	}
}

// --- S3 (minio) -----------------------------------------------------------

// minioImage is the digest-pinned image of docker-compose.dev.yml.
const minioImage = "cgr.dev/chainguard/minio:latest@sha256:268d68a2a3b6fc65f905a61f84d7eab6345ce6bff14f551e2b028ec31e6a1341"

const (
	minioUser = "bekuptestadmin"
	minioPass = "bekup-test-password-not-used-anywhere"
)

type staticCreds struct{}

func (staticCreds) Retrieve(context.Context) (aws.Credentials, error) {
	return aws.Credentials{AccessKeyID: minioUser, SecretAccessKey: minioPass, Source: "test"}, nil
}

func startMinio(t *testing.T) (endpoint string, admin *s3.Client) {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: testcontainers.ContainerRequest{
		Image:        minioImage,
		ExposedPorts: []string{"9000/tcp"},
		Env:          map[string]string{"MINIO_ROOT_USER": minioUser, "MINIO_ROOT_PASSWORD": minioPass},
		Cmd:          []string{"server", "/tmp/data", "--address", ":9000"},
		WaitingFor:   wait.ForListeningPort("9000/tcp").WithStartupTimeout(90 * time.Second),
	}, Started: true})
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("start minio: %v", err)
	}
	port, err := c.MappedPort(ctx, "9000/tcp")
	if err != nil {
		t.Fatal(err)
	}
	endpoint = "http://127.0.0.1:" + port.Port()
	admin = s3.New(s3.Options{
		Region: "us-east-1", Credentials: staticCreds{}, BaseEndpoint: aws.String(endpoint), UsePathStyle: true,
		HTTPClient: &http.Client{Timeout: time.Minute},
	})
	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := admin.ListBuckets(ctx, &s3.ListBucketsInput{}); err == nil {
			return endpoint, admin
		} else if time.Now().After(deadline) {
			t.Fatalf("minio not ready: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (e *env) s3Dest(endpoint string, admin *s3.Client, bucket string, lock, noDelete bool) uuid.UUID {
	e.t.Helper()
	if _, err := admin.CreateBucket(e.t.Context(), &s3.CreateBucketInput{Bucket: aws.String(bucket), ObjectLockEnabledForBucket: aws.Bool(lock)}); err != nil {
		e.t.Fatal(err)
	}
	guard, err := netguard.New(netguard.Options{LoopbackAllowable: true, AllowCIDRs: []string{"127.0.0.0/8"}})
	if err != nil {
		e.t.Fatal(err)
	}
	id := e.destRow("s3", noDelete)
	e.op.open[id] = func() (storage.Destination, error) {
		return s3store.Open(context.Background(), s3store.Config{
			Endpoint: endpoint, Region: "us-east-1", Bucket: bucket, PathStyle: true,
			AccessKeyID: minioUser, SecretAccessKey: crypto.NewSecret([]byte(minioPass)),
			AllowHTTP: true, Guard: guard, NoDelete: noDelete,
		})
	}
	return id
}

func TestIntegrationS3ObjectLockAndNoDelete(t *testing.T) {
	endpoint, admin := startMinio(t)

	t.Run("object lock refuses and the artifact stays present", func(t *testing.T) {
		e := newEnv(t)
		dest := e.s3Dest(endpoint, admin, "bk-lock", true, false)
		j := e.job(1, 0, false, dest)
		newest := e.run(j, 0, 1, on{dest: dest})
		// The lock is not recorded in lock_until (e.g. a bucket default
		// retention): the policy finds the run eligible, the store refuses.
		locked := e.run(j, 2*day, 1, on{dest: dest, putOpts: storage.PutOptions{
			RetainUntil: time.Now().Add(time.Hour), LockMode: storage.LockGovernance,
		}})
		plain := e.run(j, 3*day, 1, on{dest: dest})
		res, err := e.worker(nil).Sweep(t.Context(), j.id, queue.RetentionDaily, false)
		if err != nil {
			t.Fatal(err)
		}
		dr := res.Destinations[0]
		if dr.Locked != 1 || dr.Deleted != 2 || dr.LockedUntil.IsZero() {
			t.Fatalf("result %+v", dr)
		}
		e.assertRun(newest, "present", true)
		e.assertRun(locked, "present", true) // dump locked, so its manifest stays too
		e.assertRun(plain, "deleted", false)
		// Version-exact deletes: no versions of the plain run remain.
		vs, err := admin.ListObjectVersions(t.Context(), &s3.ListObjectVersionsInput{Bucket: aws.String("bk-lock"), Prefix: aws.String(plain.objects[0].key.String())})
		if err != nil {
			t.Fatal(err)
		}
		if len(vs.Versions) != 0 || len(vs.DeleteMarkers) != 0 {
			t.Errorf("versions left: %d versions, %d markers", len(vs.Versions), len(vs.DeleteMarkers))
		}
		sw := e.audits("retention.swept")
		if len(sw) != 1 || sw[0].Details["locked"] != float64(1) || sw[0].Details["locked_until_max"] == nil {
			t.Errorf("swept %+v", sw)
		}
		// A recorded lock_until keeps the run out of the plan entirely.
		e2 := e.run(j, 4*day, 1, on{dest: dest, lockUtil: time.Now().Add(day)})
		res, err = e.worker(nil).Sweep(t.Context(), j.id, queue.RetentionDaily, false)
		if err != nil {
			t.Fatal(err)
		}
		if res.Destinations[0].Plan.Keep[e2.id] != KeepLocked {
			t.Errorf("recorded lock not kept: %v", res.Destinations[0].Plan.Keep)
		}
		e.assertRun(e2, "present", true)
	})

	t.Run("no-delete mode skips deletion", func(t *testing.T) {
		e := newEnv(t)
		dest := e.s3Dest(endpoint, admin, "bk-nodelete", false, true)
		j := e.job(1, 0, false, dest)
		e.run(j, 0, 1, on{dest: dest})
		old := e.run(j, day, 1, on{dest: dest})
		res, err := e.worker(nil).Sweep(t.Context(), j.id, queue.RetentionDaily, false)
		if err != nil {
			t.Fatal(err)
		}
		if dr := res.Destinations[0]; !dr.SkippedNoDelete || dr.Deleted != 0 || dr.Planned != 2 {
			t.Fatalf("result %+v", dr)
		}
		e.assertRun(old, "present", true)
	})

	t.Run("several destinations with different completeness", func(t *testing.T) {
		e := newEnv(t)
		disk := e.diskDest("multi", false)
		s3d := e.s3Dest(endpoint, admin, "bk-multi", false, false)
		j := e.job(1, 0, false, disk, s3d)
		// r0 reached only the disk completely (S3 got no manifest).
		r0 := e.run(j, 0, 1, on{dest: disk}, on{dest: s3d, noMan: true})
		r1 := e.run(j, day, 1, on{dest: disk}, on{dest: s3d})
		res, err := e.worker(nil).Sweep(t.Context(), j.id, queue.RetentionDaily, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Destinations) != 2 {
			t.Fatalf("destinations %+v", res.Destinations)
		}
		for _, o := range r1.objects {
			st, _ := e.state(o.id)
			want := "present" // newest complete on S3
			if o.dest == disk {
				want = "deleted"
			}
			if st != want {
				t.Errorf("r1 on %s: %s, want %s", o.dest, st, want)
			}
		}
		e.assertRun(r0, "present", true)
	})
}
