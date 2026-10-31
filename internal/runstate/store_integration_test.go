//go:build integration

package runstate

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/queue"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

type fakeInserter struct {
	next      int64
	duplicate bool
}

func (f *fakeInserter) InsertTx(context.Context, pgx.Tx, river.JobArgs, queue.InsertOptions) (queue.InsertResult, error) {
	f.next++
	return queue.InsertResult{JobID: f.next, Duplicate: f.duplicate}, nil
}

type stEnv struct {
	t     *testing.T
	d     dbtest.DB
	pool  *pgxpool.Pool
	q     *sqlc.Queries
	store *Store
	job   uuid.UUID
	dest  uuid.UUID
	ins   *fakeInserter
}

func newStEnv(t *testing.T) *stEnv {
	t.Helper()
	d := dbtest.Migrated(t)
	e := &stEnv{t: t, d: d, pool: d.AppPool(t), ins: &fakeInserter{next: 100}}
	e.q = sqlc.New(e.pool)
	e.store = NewStore(e.pool, &pubsub.Publisher{Bus: pubsub.NewBus()}, nil)
	e.dest = uuid.New()
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO destinations (id, name, type, config) VALUES ($1, 'disk', 'disk', '{"path":"x"}')`, e.dest); err != nil {
		t.Fatal(err)
	}
	var err error
	e.job, err = dbtest.InsertJob(t.Context(), e.pool, dbtest.JobRow{Enabled: true, DestinationIDs: []uuid.UUID{e.dest}})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *stEnv) enqueue(dup bool) Enqueued {
	e.t.Helper()
	e.ins.duplicate = dup
	var out Enqueued
	err := db.TxWith(e.t.Context(), e.pool, func(tx pgx.Tx, _ *sqlc.Queries) error {
		var err error
		out, err = EnqueueTx(e.t.Context(), tx, e.ins, nil, queue.BackupRunArgs{JobID: e.job, Trigger: queue.TriggerSchedule, ScheduledFor: time.Now()})
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

func TestEnqueueBeginAndLifecycle(t *testing.T) {
	e := newStEnv(t)
	ctx := t.Context()

	// Notifications arrive on bekup_run_events.
	conn, err := e.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if err := pubsub.RunEvents.Listen(ctx, sqlc.New(conn)); err != nil {
		t.Fatal(err)
	}

	if dup := e.enqueue(true); dup.RunID != uuid.Nil || !dup.Duplicate {
		t.Fatalf("duplicate created a run: %+v", dup)
	}
	enq := e.enqueue(false)
	if enq.RunID == uuid.Nil {
		t.Fatal("no queued run")
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	n, err := conn.Conn().WaitForNotification(wctx)
	cancel()
	if err != nil || n.Payload != Payload(EventRun, Ref{RunID: enq.RunID, JobID: e.job}) {
		t.Fatalf("notification %+v %v", n, err)
	}

	job, err := e.store.Job(ctx, e.job)
	if err != nil || !job.Enabled || job.Deleted || len(job.Destinations) != 1 || job.Destinations[0].ID != e.dest || job.Slug == "" {
		t.Fatalf("job %+v %v", job, err)
	}
	if _, err := e.store.Job(ctx, uuid.New()); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("missing job: %v", err)
	}

	// Begin picks up the queued run of the River job.
	bp := BeginParams{RiverJobID: enq.JobID, Attempt: 1, JobID: e.job, Trigger: sqlc.RunTriggerSchedule}
	run, err := e.store.Begin(ctx, bp)
	if err != nil || run.RunID != enq.RunID || run.Status != StatusQueued {
		t.Fatalf("begin %+v %v", run, err)
	}
	now := time.Now()
	if err := e.store.Start(ctx, run.Ref, 1, now, now); err != nil {
		t.Fatal(err)
	}
	if err := e.store.Start(ctx, run.Ref, 1, now, now); !errors.Is(err, ErrNotActive) {
		t.Fatalf("second start: %v", err)
	}
	item, err := e.store.AddItem(ctx, run.Ref, sqlc.RunItemKindDatabase, "shop", 0)
	if err != nil {
		t.Fatal(err)
	}
	g, err := e.store.AddItem(ctx, run.Ref, sqlc.RunItemKindGlobals, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("x"))
	if err := e.store.FinishItem(ctx, run.Ref, item.ID, ItemResult{Status: ItemSuccess, RawSize: 10, StoredSize: 5, RawSHA256: sum[:], SHA256: sum[:], Verified: true,
		Warnings: []Warning{{Code: "non_innodb_tables", Message: "m", Database: "shop"}}, SchemaVersionStatus: "ok", SchemaVersion: "42 (dirty)"}); err != nil {
		t.Fatal(err)
	}
	// A version without status ok is never stored (D-210).
	if err := e.store.FinishItem(ctx, run.Ref, g.ID, ItemResult{Status: ItemFailed, Failure: &Failure{Code: "dump_failed", Message: "pg_dumpall exited with code 1"},
		SchemaVersionStatus: "unavailable", SchemaVersion: "leak"}); err != nil {
		t.Fatal(err)
	}
	items, err := e.q.ListRunItems(ctx, run.RunID)
	if err != nil || len(items) != 2 {
		t.Fatalf("items %v", err)
	}
	if v, st := items[0].SchemaVersion, items[0].SchemaVersionStatus; v == nil || *v != "42 (dirty)" || st == nil || *st != "ok" {
		t.Fatalf("item version %v %v", v, st)
	}
	if v, st := items[1].SchemaVersion, items[1].SchemaVersionStatus; v != nil || st == nil || *st != "unavailable" {
		t.Fatalf("globals version %v %v", v, st)
	}
	if err := e.store.FinishItem(ctx, run.Ref, g.ID, ItemResult{Status: ItemSuccess}); !errors.Is(err, ErrNotActive) {
		t.Fatalf("second item finish: %v", err)
	}
	id := item.ID
	art := Artifact{ItemID: &id, DestinationID: e.dest, Kind: sqlc.ArtifactKindDatabase, Key: "shop/20260101T000000Z/shop.dump.zst.age",
		Size: 5, SHA256: sum[:], EncryptionMode: "recipients", RecipientVersion: 1, Verified: true, LockUntil: now.Add(time.Hour)}
	artID, err := e.store.AddArtifact(ctx, run.Ref, art)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.AddArtifact(ctx, run.Ref, art); err == nil {
		t.Fatal("the same object key was recorded twice")
	}
	if err := e.store.MarkSigned(ctx, run.Ref, Artifact{DestinationID: e.dest, Kind: sqlc.ArtifactKindManifest,
		Key: "shop/20260101T000000Z/manifest.json", Size: 100, SHA256: sum[:], SigningKeyID: "0123456789abcdef"}, []uuid.UUID{artID}); err != nil {
		t.Fatal(err)
	}
	arts, err := e.q.ListRunArtifacts(ctx, run.RunID)
	if err != nil || len(arts) != 2 {
		t.Fatalf("artifacts %d %v", len(arts), err)
	}
	for _, a := range arts {
		if a.SigningKeyID == nil || *a.SigningKeyID != "0123456789abcdef" || a.JobID != e.job {
			t.Fatalf("artifact %+v", a)
		}
	}
	if err := e.store.AppendLogs(ctx, run.Ref, []LogLine{{Time: now, Level: LevelInfo, Stream: StreamRunner, Text: "a"}, {Time: now, Level: LevelWarn, Stream: StreamStderr, Text: "b"}}); err != nil {
		t.Fatal(err)
	}
	logs, err := e.q.ListRunLogs(ctx, sqlc.ListRunLogsParams{RunID: run.RunID, AfterID: 0, MaxRows: 10})
	if err != nil || len(logs) != 2 || logs[1].Stream != StreamStderr || logs[1].Line != "b" {
		t.Fatalf("logs %+v %v", logs, err)
	}
	if err := e.store.Finish(ctx, run.Ref, Result{Status: StatusPartial, Failure: &Failure{Code: "incomplete", Message: "1 of 2"},
		Warnings: []Warning{{Code: "w", Message: "x"}}, BytesRaw: 10, BytesStored: 5, LogLines: 2}); err != nil {
		t.Fatal(err)
	}
	if err := e.store.Finish(ctx, run.Ref, Result{Status: StatusSuccess}); !errors.Is(err, ErrNotActive) {
		t.Fatalf("second finish: %v", err)
	}
	row, err := e.q.GetRun(ctx, run.RunID)
	if err != nil || row.Status != StatusPartial || row.FinishedAt == nil || row.StorageTimestamp == nil ||
		!row.StorageTimestamp.Equal(now.UTC().Truncate(time.Second)) || *row.ErrorCode != "incomplete" || row.LogLines != 2 {
		t.Fatalf("run %+v %v", row, err)
	}

	// A retry of the same River job gets a new run.
	run2, err := e.store.Begin(ctx, BeginParams{RiverJobID: enq.JobID, Attempt: 2, JobID: e.job, Trigger: sqlc.RunTriggerSchedule})
	if err != nil || run2.RunID == run.RunID || run2.Attempt != 2 {
		t.Fatalf("retry run %+v %v", run2, err)
	}
	if err := e.store.Start(ctx, run2.Ref, 2, now, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	// A rescued attempt finds its predecessor running: interrupted.
	run3, err := e.store.Begin(ctx, BeginParams{RiverJobID: enq.JobID, Attempt: 3, JobID: e.job, Trigger: sqlc.RunTriggerSchedule})
	if err != nil || run3.RunID == run2.RunID {
		t.Fatalf("rescue run %+v %v", run3, err)
	}
	if prev, err := e.q.GetRun(ctx, run2.RunID); err != nil || prev.Status != StatusFailed || *prev.ErrorCode != CodeInterrupted {
		t.Fatalf("interrupted run %+v", prev)
	}
}

// TestPrivileges checks the bekup_app grants and the guard triggers: no
// DELETE of history, artifacts keep their identity, finished runs stay
// finished.
func TestPrivileges(t *testing.T) {
	e := newStEnv(t)
	ctx := t.Context()
	enq := e.enqueue(false)
	run := Ref{RunID: enq.RunID, JobID: e.job}
	sum := sha256.Sum256([]byte("y"))
	item, err := e.store.AddItem(ctx, run, sqlc.RunItemKindDatabase, "shop", 0)
	if err != nil {
		t.Fatal(err)
	}
	artID, err := e.store.AddArtifact(ctx, run, Artifact{ItemID: &item.ID, DestinationID: e.dest, Kind: sqlc.ArtifactKindDatabase,
		Key: "k/shop.dump.zst", Size: 1, SHA256: sum[:], EncryptionMode: "none", Verified: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkSigned(ctx, run, Artifact{DestinationID: e.dest, Kind: sqlc.ArtifactKindManifest, Key: "k/m.json", Size: 1, SHA256: sum[:], SigningKeyID: "0123456789abcdef"}, []uuid.UUID{artID}); err != nil {
		t.Fatal(err)
	}
	if err := e.store.AppendLogs(ctx, run, []LogLine{{Time: time.Now(), Level: LevelInfo, Stream: StreamWorker, Text: "x"}}); err != nil {
		t.Fatal(err)
	}
	denied := []string{
		"DELETE FROM runs",
		"DELETE FROM run_items",
		"DELETE FROM artifacts",
		"DELETE FROM run_logs",
		"UPDATE run_logs SET line = 'y'",
		"UPDATE artifacts SET object_key = 'other'",
		"UPDATE artifacts SET sha256 = '\\x00'",
		"TRUNCATE run_slots",
	}
	for _, stmt := range denied {
		if _, err := e.pool.Exec(ctx, stmt); err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("%s: %v", stmt, err)
		}
	}
	// Deleted is final; the signing key is set once.
	if _, err := e.pool.Exec(ctx, "UPDATE artifacts SET state = 'deleted', deleted_at = now()"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, "UPDATE artifacts SET state = 'present', deleted_at = NULL"); err == nil {
		t.Fatal("a deleted artifact was revived")
	}
	if _, err := e.pool.Exec(ctx, "UPDATE artifacts SET signing_key_id = 'fedcba9876543210'"); err == nil {
		t.Fatal("the signing key changed")
	}
	if err := e.store.Finish(ctx, run, Result{Status: StatusSkipped, Failure: &Failure{Code: "job_deleted", Message: "x"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, "UPDATE runs SET status = 'running', finished_at = NULL WHERE id = $1", run.RunID); err == nil {
		t.Fatal("a finished run became active again")
	}
}

// TestMarkSignedExactEntries: MarkSigned signs exactly the manifest's
// entries. A verified object of the run on the same destination that the
// manifest does not list (an item that failed after one destination
// committed) stays unsigned, and a list naming an object that is not a
// verified, unsigned artifact of the run there records nothing.
func TestMarkSignedExactEntries(t *testing.T) {
	e := newStEnv(t)
	ctx := t.Context()
	enq := e.enqueue(false)
	run := Ref{RunID: enq.RunID, JobID: e.job}
	sum := sha256.Sum256([]byte("z"))
	add := func(db string, verified bool) uuid.UUID {
		t.Helper()
		it, err := e.store.AddItem(ctx, run, sqlc.RunItemKindDatabase, db, 0)
		if err != nil {
			t.Fatal(err)
		}
		id, err := e.store.AddArtifact(ctx, run, Artifact{ItemID: &it.ID, DestinationID: e.dest, Kind: sqlc.ArtifactKindDatabase,
			Key: "k/" + db + ".dump.zst", Size: 1, SHA256: sum[:], EncryptionMode: "none", Verified: verified})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	good := add("good", true)
	failedLater := add("failed", true) // stored and verified here, but the item failed elsewhere
	unverified := add("bad", false)
	manifest := Artifact{DestinationID: e.dest, Kind: sqlc.ArtifactKindManifest, Key: "k/manifest.json", Size: 1, SHA256: sum[:], SigningKeyID: "0123456789abcdef"}
	for _, bad := range [][]uuid.UUID{{good, unverified}, {good, uuid.New()}, nil} {
		if err := e.store.MarkSigned(ctx, run, manifest, bad); err == nil {
			t.Fatalf("entries %v accepted", bad)
		}
	}
	arts, err := e.q.ListRunArtifacts(ctx, run.RunID)
	if err != nil || len(arts) != 3 {
		t.Fatalf("a rejected MarkSigned recorded something: %d %v", len(arts), err)
	}
	if err := e.store.MarkSigned(ctx, run, manifest, []uuid.UUID{good}); err != nil {
		t.Fatal(err)
	}
	arts, err = e.q.ListRunArtifacts(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range arts {
		signed := a.SigningKeyID != nil
		want := a.ID == good || a.Kind == sqlc.ArtifactKindManifest
		if signed != want {
			t.Fatalf("artifact %s (failed-later %v) signed=%v", a.ObjectKey, a.ID == failedLater, signed)
		}
	}
}

// TestFinishWithHugeMultibyteWarnings: Finish and FinishItem always succeed
// against the column CHECKs, whatever the warnings (long 4-byte names and
// messages, many of them).
func TestFinishWithHugeMultibyteWarnings(t *testing.T) {
	e := newStEnv(t)
	ctx := t.Context()
	enq := e.enqueue(false)
	run, err := e.store.Begin(ctx, BeginParams{RiverJobID: enq.JobID, Attempt: 1, JobID: e.job, Trigger: sqlc.RunTriggerSchedule})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := e.store.Start(ctx, run.Ref, 1, now, now); err != nil {
		t.Fatal(err)
	}
	name := strings.Repeat("𝔡", 63) // 252 bytes
	ws := make([]Warning, 3000)
	for i := range ws {
		ws[i] = Warning{Code: "insufficient_privileges", Message: strings.Repeat(`𝔪\"\`, 400) + name, Database: name,
			DestinationID: e.dest.String()}
	}
	item, err := e.store.AddItem(ctx, run.Ref, sqlc.RunItemKindDatabase, name, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.FinishItem(ctx, run.Ref, item.ID, ItemResult{Status: ItemFailed, Failure: &Failure{Code: "dump_failed", Message: strings.Repeat("𝔪", 5000)}, Warnings: ws}); err != nil {
		t.Fatalf("finish item: %v", err)
	}
	if err := e.store.Finish(ctx, run.Ref, Result{Status: StatusFailed, Failure: &Failure{Code: "dump_failed", Message: "x"}, Warnings: ws, Notices: ws}); err != nil {
		t.Fatalf("finish: %v", err)
	}
	row, err := e.q.GetRun(ctx, run.RunID)
	if err != nil || !row.HasWarnings || !strings.Contains(string(row.Warnings), "warnings_truncated") || !strings.Contains(string(row.Notices), "warnings_truncated") {
		t.Fatalf("run %v: %d bytes of warnings", err, len(row.Warnings))
	}
}

func TestCancelHelpers(t *testing.T) {
	e := newStEnv(t)
	ctx := t.Context()
	enq := e.enqueue(false)
	err := db.Tx(ctx, e.pool, func(q *sqlc.Queries) error {
		r, err := RequestCancelTx(ctx, q, nil, enq.RunID, nil, time.Now())
		if err != nil || r.CancelRequestedAt == nil {
			t.Fatalf("request %+v %v", r, err)
		}
		_, err = CancelQueuedTx(ctx, q, nil, Ref{RunID: r.ID, JobID: r.JobID}, time.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := e.store.CancelRequested(ctx, enq.RunID); err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	err = db.Tx(ctx, e.pool, func(q *sqlc.Queries) error {
		_, err := RequestCancelTx(ctx, q, nil, enq.RunID, nil, time.Now())
		return err
	})
	if !errors.Is(err, ErrNotActive) {
		t.Fatalf("cancel of a finished run: %v", err)
	}
	if _, err := e.store.CancelRequested(ctx, uuid.New()); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("unknown run: %v", err)
	}
}

func TestSlotsLimits(t *testing.T) {
	e := newStEnv(t)
	ctx := t.Context()
	s := NewSlots(e.pool)
	connA, connB := uuid.New(), uuid.New()
	l := SlotLimits{Global: 2, PerConnection: 1}
	r1, r2, r3, r4 := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	take := func(holder, conn uuid.UUID) bool {
		t.Helper()
		ok, err := s.TryAcquire(ctx, holder, conn, SlotBackup, l)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !take(r1, connA) {
		t.Fatal("first slot refused")
	}
	if take(r2, connA) {
		t.Fatal("per-connection limit not enforced")
	}
	if !take(r2, connB) {
		t.Fatal("second connection refused")
	}
	if take(r3, uuid.New()) {
		t.Fatal("global limit not enforced")
	}
	if err := s.Heartbeat(ctx, r1); err != nil {
		t.Fatal(err)
	}
	if err := s.Release(ctx, r1); err != nil {
		t.Fatal(err)
	}
	if err := s.Heartbeat(ctx, r1); !errors.Is(err, ErrSlotLost) {
		t.Fatalf("heartbeat of a released slot: %v", err)
	}
	if !take(r3, connA) {
		t.Fatal("released slot not reusable")
	}
	// A slot without heartbeat for longer than the lease is taken over.
	if err := e.d.SuperExec(ctx, "UPDATE run_slots SET heartbeat_at = now() - interval '10 minutes' WHERE holder_id = '"+r2.String()+"'"); err != nil {
		t.Fatal(err)
	}
	if !take(r4, connB) {
		t.Fatal("stale slot not expired")
	}
	if err := s.Heartbeat(ctx, r2); !errors.Is(err, ErrSlotLost) {
		t.Fatalf("expired slot heartbeat: %v", err)
	}
	if _, err := s.TryAcquire(ctx, uuid.New(), connA, SlotBackup, SlotLimits{}); err == nil {
		t.Fatal("zero limits accepted")
	}
	// Concurrent acquirers never exceed the limits.
	conn := uuid.New()
	results := make(chan bool, 8)
	for range 8 {
		go func() {
			ok, err := s.TryAcquire(ctx, uuid.New(), conn, SlotRestore, SlotLimits{Global: 100, PerConnection: 3})
			results <- ok && err == nil
		}()
	}
	got := 0
	for range 8 {
		if <-results {
			got++
		}
	}
	if got != 3 {
		t.Fatalf("%d concurrent slots for a per-connection limit of 3", got)
	}
}

// TestSlotsConnectionLimit covers the per-connection run limit (task 3.13,
// D-156): the connection's max_concurrent_runs replaces the global default,
// and lowering it never touches active slots.
func TestSlotsConnectionLimit(t *testing.T) {
	e := newStEnv(t)
	ctx := t.Context()
	s := NewSlots(e.pool)
	newConn := func(name string, limit *int) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		err := e.pool.QueryRow(ctx, `INSERT INTO connections (name, engine, version_key, image_key, host, port, username, max_concurrent_runs)
			VALUES ($1, 'postgres', '17', '17', 'db.example.com', 5432, 'u', $2) RETURNING id`, name, limit).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	two := 2
	limited, dflt := newConn("limited", &two), newConn("default", nil)
	l := SlotLimits{Global: 10, PerConnection: 1}
	take := func(conn uuid.UUID) (uuid.UUID, bool) {
		t.Helper()
		holder := uuid.New()
		ok, err := s.TryAcquire(ctx, holder, conn, SlotBackup, l)
		if err != nil {
			t.Fatal(err)
		}
		return holder, ok
	}
	// Limit 2: two runs acquire, the third waits.
	h1, ok1 := take(limited)
	h2, ok2 := take(limited)
	if !ok1 || !ok2 {
		t.Fatal("limit 2: the first two runs must get a slot")
	}
	if _, ok := take(limited); ok {
		t.Fatal("limit 2: the third run must wait")
	}
	// NULL: the global default (1) applies.
	if _, ok := take(dflt); !ok {
		t.Fatal("default limit: the first run must get a slot")
	}
	if _, ok := take(dflt); ok {
		t.Fatal("default limit 1: the second run must wait")
	}
	// Lowering 2 -> 1 keeps both active runs; a new one waits until the
	// count is below the new limit.
	if _, err := e.pool.Exec(ctx, `UPDATE connections SET max_concurrent_runs = 1 WHERE id = $1`, limited); err != nil {
		t.Fatal(err)
	}
	for _, h := range []uuid.UUID{h1, h2} {
		if err := s.Heartbeat(ctx, h); err != nil {
			t.Fatalf("active run lost its slot after lowering the limit: %v", err)
		}
	}
	if _, ok := take(limited); ok {
		t.Fatal("lowered limit: a new run must wait")
	}
	if err := s.Release(ctx, h1); err != nil {
		t.Fatal(err)
	}
	if _, ok := take(limited); ok {
		t.Fatal("lowered limit 1 with one active run: a new run must still wait")
	}
	if err := s.Release(ctx, h2); err != nil {
		t.Fatal(err)
	}
	if _, ok := take(limited); !ok {
		t.Fatal("lowered limit: a run must start once the count is below the limit")
	}
	// The global cap still applies above a connection's own limit.
	big := 64
	wide := newConn("wide", &big)
	l = SlotLimits{Global: 4, PerConnection: 1}
	got := 0
	for range 6 {
		if _, ok := take(wide); ok {
			got++
		}
	}
	if got != 2 { // 2 slots are held already (limited, default)
		t.Fatalf("global cap: %d slots taken, want 2", got)
	}
	// The column's range is enforced by the database too.
	for _, bad := range []int{0, 65} {
		if _, err := e.pool.Exec(ctx, `UPDATE connections SET max_concurrent_runs = $1 WHERE id = $2`, bad, limited); err == nil {
			t.Fatalf("max_concurrent_runs %d accepted", bad)
		}
	}
}
