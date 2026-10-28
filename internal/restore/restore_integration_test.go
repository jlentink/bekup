//go:build integration

package restore_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/instancekeys"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/jobs"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/restore"
	"github.com/jlentink/bekup/internal/retention"
	"github.com/jlentink/bekup/internal/serviceutil"
	"github.com/jlentink/bekup/internal/storage"
	"github.com/jlentink/bekup/internal/storage/s3store"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

// getHook wraps the reader of a Get (nil: unchanged).
type getHook func(key storage.Key, rc io.ReadCloser) io.ReadCloser

// hookDest counts Gets and lets a test wrap their readers.
type hookDest struct {
	storage.Destination
	o *opener
}

func (h hookDest) Get(ctx context.Context, key storage.Key) (io.ReadCloser, storage.ObjectInfo, error) {
	h.o.gets.Add(1)
	rc, info, err := h.Destination.Get(ctx, key)
	if err != nil {
		return rc, info, err
	}
	h.o.mu.Lock()
	hook := h.o.hook
	h.o.mu.Unlock()
	if hook != nil {
		rc = hook(key, rc)
	}
	return rc, info, err
}

// opener opens test backends by destination id (a fresh backend per Open,
// like destinations.Service).
type opener struct {
	mu   sync.Mutex
	open map[uuid.UUID]func() (storage.Destination, error)
	hook getHook
	gets atomic.Int32
}

func (o *opener) Open(_ context.Context, id uuid.UUID) (storage.Destination, error) {
	o.mu.Lock()
	f := o.open[id]
	o.mu.Unlock()
	if f == nil {
		return nil, errors.New("unknown destination")
	}
	d, err := f()
	if err != nil {
		return nil, err
	}
	return hookDest{Destination: d, o: o}, nil
}

func (o *opener) setHook(h getHook) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.hook = h
}

type env struct {
	t       *testing.T
	d       dbtest.DB
	pool    *pgxpool.Pool
	kr      *crypto.Keyring
	keys    *jobkeys.Service
	auditor *audit.Auditor
	op      *opener
	root    string
	diskID  uuid.UUID
	disk    storage.Destination
	scratch string
	svc     *restore.Service
	admin   uuid.UUID
	mu      sync.Mutex
	alerts  []restore.Alert
}

func newEnv(t *testing.T, mods ...func(*restore.Options)) *env {
	t.Helper()
	d := dbtest.Migrated(t)
	e := &env{t: t, d: d, pool: d.AppPool(t), op: &opener{open: map[uuid.UUID]func() (storage.Destination, error){}}}
	e.auditor = audit.New(e.pool, audit.Options{Registry: logging.NewRegistry()})
	var err error
	if e.kr, err = crypto.GenerateKeyring(time.Now()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.kr.Destroy)
	logger := slog.New(slog.DiscardHandler)
	pol := authz.New(authz.Options{Ownership: jobs.Ownership{DB: e.pool}})
	if e.keys, err = jobkeys.New(jobkeys.Options{DB: e.pool, Keyring: e.kr, Auditor: e.auditor, Authorizer: pol, Logger: logger}); err != nil {
		t.Fatal(err)
	}
	if e.root, err = filepath.EvalSymlinks(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(e.root, "local"), 0o700); err != nil {
		t.Fatal(err)
	}
	e.diskID = e.destRow("disk")
	e.op.open[e.diskID] = func() (storage.Destination, error) {
		return storage.OpenDisk(storage.DiskConfig{Root: e.root, Dir: "local"})
	}
	if e.disk, err = storage.OpenDisk(storage.DiskConfig{Root: e.root, Dir: "local"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := e.disk.Close(); err != nil {
			t.Error(err)
		}
	})
	e.scratch = filepath.Join(t.TempDir(), "scratch")
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO users (email, display_name, role) VALUES ('admin@example.com', 'Admin', 'admin') RETURNING id`).Scan(&e.admin); err != nil {
		t.Fatal(err)
	}
	e.svc = e.service(mods...)
	return e
}

func (e *env) service(mods ...func(*restore.Options)) *restore.Service {
	e.t.Helper()
	o := restore.Options{
		DB: e.pool, Keyring: e.kr, Auditor: e.auditor, Destinations: e.op, Keys: e.keys,
		ScratchDir: e.scratch, ScratchReserve: 0, Logger: slog.New(slog.DiscardHandler),
		Alert: func(_ context.Context, a restore.Alert) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.alerts = append(e.alerts, a)
		},
	}
	for _, m := range mods {
		m(&o)
	}
	svc, err := restore.New(o)
	if err != nil {
		e.t.Fatal(err)
	}
	return svc
}

func (e *env) destRow(typ string) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	e.exec(`INSERT INTO destinations (id, name, type, config) VALUES ($1, $2, $3, '{"path":"x"}')`, id, typ+"-"+id.String()[:8], typ)
	return id
}

func (e *env) exec(stmt string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.t.Context(), stmt, args...); err != nil {
		e.t.Fatalf("%s: %v", stmt, err)
	}
}

func (e *env) count(query string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(e.t.Context(), query, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", query, err)
	}
	return n
}

func (e *env) adminCtx() context.Context {
	return auth.WithPrincipal(e.t.Context(), auth.Principal{
		UserID: e.admin, Role: auth.RoleAdmin, SessionID: uuid.New(), AAL: 2, StepUpAt: time.Now(), AAL2At: time.Now(),
	})
}

type jobInfo struct {
	id, conn uuid.UUID
	slug     string
	identity string
}

// job creates a job with the given encryption (ModeNone: created with a
// passphrase, then opted out).
func (e *env) job(mode jobkeys.Mode, dests ...uuid.UUID) jobInfo {
	e.t.Helper()
	ctx := e.adminCtx()
	id, err := dbtest.InsertJob(ctx, e.pool, dbtest.JobRow{Enabled: true, DestinationIDs: dests})
	if err != nil {
		e.t.Fatal(err)
	}
	initMode := mode
	if mode == jobkeys.ModeNone {
		initMode = jobkeys.ModePassphrase
	}
	var j jobInfo
	j.id = id
	err = db.Tx(ctx, e.pool, func(q *sqlc.Queries) error {
		res, err := e.keys.InitTx(ctx, q, jobkeys.Actor{UserID: e.admin}, id, jobkeys.Init{Mode: initMode})
		if err != nil {
			return err
		}
		if res.Keypair != nil {
			j.identity = string(res.Keypair.Identity.Bytes())
		}
		res.Destroy()
		return nil
	})
	if err != nil {
		e.t.Fatal(err)
	}
	if mode == jobkeys.ModeNone {
		if _, err := e.keys.OptOut(ctx, jobkeys.Actor{UserID: e.admin}, id, "test opt-out"); err != nil {
			e.t.Fatal(err)
		}
	}
	if err := e.pool.QueryRow(ctx, "SELECT slug, connection_id FROM jobs WHERE id = $1", id).Scan(&j.slug, &j.conn); err != nil {
		e.t.Fatal(err)
	}
	return j
}

// seeded is an artifact produced through the real pipeline, with its run,
// item and signed manifest recorded like the backup worker does.
type seeded struct {
	artifact, run uuid.UUID
	key, mkey     storage.Key
	stored        []byte
	manifest      []byte
}

// seed backs dump up for job j onto (destID, dest) as a run at ts.
func (e *env) seed(j jobInfo, destID uuid.UUID, dest storage.Destination, dump []byte, at time.Time) seeded {
	e.t.Helper()
	return e.seedAs(j, destID, dest, dump, at, seedSpec{})
}

// seedSpec describes the seeded artifact (zero: a PostgreSQL 17 database
// "app" as a SQL dump).
type seedSpec struct {
	kind       pipeline.EntryKind
	format     pipeline.DumpFormat
	engine     string
	versionKey string
}

// seedAs is seed with an explicit artifact kind, format and engine.
func (e *env) seedAs(j jobInfo, destID uuid.UUID, dest storage.Destination, dump []byte, at time.Time, sp seedSpec) seeded {
	e.t.Helper()
	if sp.kind == "" {
		sp.kind = pipeline.KindDatabase
	}
	if sp.format == "" {
		sp.format = pipeline.FormatSQL
	}
	if sp.engine == "" {
		sp.engine, sp.versionKey = "postgres", "17"
	}
	dbName, itemKind := "app", "database"
	var dbArg *string
	if sp.kind == pipeline.KindGlobals {
		dbName, itemKind = "", "globals"
	} else {
		dbArg = &dbName
	}
	ctx := e.t.Context()
	km, err := e.keys.KeyMaterialFor(ctx, j.id)
	if err != nil {
		e.t.Fatal(err)
	}
	enc, err := km.Pipeline()
	if err != nil {
		e.t.Fatal(err)
	}
	defer enc.Passphrase.Destroy()
	ts := at.UTC().Truncate(time.Second)
	var run uuid.UUID
	if err := e.pool.QueryRow(ctx, `INSERT INTO runs (job_id, trigger, status, started_at, finished_at, storage_timestamp)
		VALUES ($1, 'manual', 'success', now(), now(), $2) RETURNING id`, j.id, ts).Scan(&run); err != nil {
		e.t.Fatal(err)
	}
	name, err := pipeline.ArtifactFileName(sp.kind, dbName, sp.format, enc.Mode)
	if err != nil {
		e.t.Fatal(err)
	}
	key, err := pipeline.ArtifactKey("", j.slug, ts, name)
	if err != nil {
		e.t.Fatal(err)
	}
	res, err := pipeline.Run(ctx, bytes.NewReader(dump), pipeline.Spec{
		Encryption: enc, Targets: []pipeline.Target{{Name: destID.String(), Dest: dest, Key: key}},
	})
	if err != nil {
		e.t.Fatal(err)
	}
	var item uuid.UUID
	if err := e.pool.QueryRow(ctx, `INSERT INTO run_items (run_id, kind, db_name, position, status, finished_at, raw_size, stored_size, raw_sha256, sha256, verified)
		VALUES ($1, $6::run_item_kind, $7, 0, 'success', now(), $2, $3, $4, $5, true) RETURNING id`,
		run, res.RawSize, res.StoredSize, res.RawSHA256, res.SHA256, itemKind, dbArg).Scan(&item); err != nil {
		e.t.Fatal(err)
	}
	var pv, rv *int
	if enc.PassphraseVersion > 0 {
		pv = &enc.PassphraseVersion
	}
	if enc.RecipientVersion > 0 {
		rv = &enc.RecipientVersion
	}
	var art uuid.UUID
	if err := e.pool.QueryRow(ctx, `INSERT INTO artifacts (run_id, run_item_id, job_id, destination_id, kind, object_key, size, sha256,
		encryption_mode, passphrase_version, recipient_version, verified)
		VALUES ($1, $2, $3, $4, $11::artifact_kind, $5, $6, $7, $8, $9, $10, true) RETURNING id`,
		run, item, j.id, destID, key.String(), res.StoredSize, res.SHA256, string(enc.Mode), pv, rv, itemKind).Scan(&art); err != nil {
		e.t.Fatal(err)
	}
	sk, _, err := instancekeys.Active(ctx, sqlc.New(e.pool), e.kr)
	if err != nil {
		e.t.Fatal(err)
	}
	defer sk.Destroy()
	mkey, err := pipeline.ManifestKey("", j.slug, ts)
	if err != nil {
		e.t.Fatal(err)
	}
	m := &pipeline.Manifest{
		FormatVersion: pipeline.ManifestFormatVersion, BekupVersion: "0.0.0-test", JobID: j.id.String(), JobSlug: j.slug,
		RunID: run.String(), RunTimestamp: ts, CreatedAt: time.Now().UTC(),
		Engine:  pipeline.EngineInfo{Kind: sp.engine, VersionKey: sp.versionKey, ServerVersion: sp.versionKey + ".0"},
		Entries: []pipeline.Entry{res.Entry(sp.kind, dbName, name)},
	}
	mres, err := pipeline.WriteManifest(ctx, []pipeline.Target{{Name: destID.String(), Dest: dest, Key: mkey}}, sk, m)
	if err != nil {
		e.t.Fatal(err)
	}
	e.exec(`INSERT INTO artifacts (run_id, job_id, destination_id, kind, object_key, size, sha256, signing_key_id, verified)
		VALUES ($1, $2, $3, 'manifest', $4, $5, $6, $7, true)`, run, j.id, destID, mkey.String(), len(mres.File), mres.SHA256, sk.ID)
	e.exec(`UPDATE artifacts SET signing_key_id = $1 WHERE run_id = $2 AND kind <> 'manifest'`, sk.ID, run)
	return seeded{artifact: art, run: run, key: key, mkey: mkey, stored: read(e.t, dest, key), manifest: mres.File}
}

func read(t *testing.T, d storage.Destination, k storage.Key) []byte {
	t.Helper()
	rc, _, err := d.Get(t.Context(), k)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(rc)
	if cerr := rc.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// replace overwrites a stored object on the disk destination (tamper
// simulation; the disk backend never overwrites).
func (e *env) replace(k storage.Key, data []byte) {
	e.t.Helper()
	if err := e.disk.Delete(e.t.Context(), k); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.disk.Put(e.t.Context(), k, bytes.NewReader(data), storage.PutOptions{}); err != nil {
		e.t.Fatal(err)
	}
}

// create inserts a queued restore of artifact into a new database.
func (e *env) create(j jobInfo, artifact uuid.UUID, identity string) (uuid.UUID, error) {
	e.t.Helper()
	var id uuid.UUID
	name := "restored"
	err := db.Tx(e.t.Context(), e.pool, func(q *sqlc.Queries) error {
		r, err := e.svc.CreateTx(e.t.Context(), q, restore.CreateInput{
			ArtifactID: artifact, TargetConnectionID: j.conn, TargetDBName: &name, Mode: restore.ModeNewDB,
			RequestedBy: e.admin, Identity: []byte(identity),
		})
		id = r.ID
		return err
	})
	return id, err
}

func (e *env) mustCreate(j jobInfo, artifact uuid.UUID, identity string) uuid.UUID {
	e.t.Helper()
	id, err := e.create(j, artifact, identity)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) status(id uuid.UUID) (string, string) {
	e.t.Helper()
	var st string
	var code *string
	if err := e.pool.QueryRow(e.t.Context(), "SELECT status, error_code FROM restores WHERE id = $1", id).Scan(&st, &code); err != nil {
		e.t.Fatal(err)
	}
	if code == nil {
		return st, ""
	}
	return st, *code
}

// scratchEntries lists the scratch directory (empty when it is missing).
func (e *env) scratchEntries() []string {
	e.t.Helper()
	des, err := os.ReadDir(e.scratch)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, d := range des {
		out = append(out, d.Name())
	}
	return out
}

func (e *env) holds(artifact uuid.UUID) int {
	return e.count("SELECT count(*) FROM artifact_holds WHERE artifact_id = $1", artifact)
}

func (e *env) auditReasons(restoreID uuid.UUID) []string {
	e.t.Helper()
	rows, err := e.pool.Query(e.t.Context(), `SELECT coalesce(details->>'reason', '') FROM audit_log
		WHERE action = 'restore.verification_failed' AND target_id = $1 ORDER BY id`, restoreID.String())
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func (e *env) alertCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.alerts)
}

func randomDump(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return append([]byte("-- MySQL dump\n"), b...)
}

func readAllOpen(t *testing.T, st *restore.Staged) []byte {
	t.Helper()
	rc, err := st.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc)
	if cerr := rc.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// TestStageVerifyOpen: a good artifact stages (private dir and file,
// wrapped at rest), verifies, opens once to the original dump, and the
// cleanup removes the staging and releases the hold.
func TestStageVerifyOpen(t *testing.T) {
	for _, mode := range []jobkeys.Mode{jobkeys.ModePassphrase, jobkeys.ModeRecipient} {
		t.Run(string(mode), func(t *testing.T) {
			e := newEnv(t)
			j := e.job(mode, e.diskID)
			dump := randomDump(300_000)
			s := e.seed(j, e.diskID, e.disk, dump, time.Now())
			id := e.mustCreate(j, s.artifact, j.identity)
			if mode == jobkeys.ModeRecipient {
				if n := e.count("SELECT count(*) FROM restore_identity_handoff WHERE restore_id = $1", id); n != 1 {
					t.Fatalf("handoff rows %d", n)
				}
				var sealed []byte
				if err := e.pool.QueryRow(t.Context(), "SELECT sealed_identity FROM restore_identity_handoff WHERE restore_id = $1", id).Scan(&sealed); err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(sealed, []byte("AGE-SECRET-KEY")) {
					t.Fatal("the identity is stored in plain text")
				}
			}
			st, err := e.svc.Stage(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Cleanup()
			if got, _ := e.status(id); got != "verifying" {
				t.Fatalf("status %s", got)
			}
			if n := e.count("SELECT count(*) FROM restore_identity_handoff WHERE restore_id = $1", id); n != 0 {
				t.Fatalf("the handoff was not deleted: %d", n)
			}
			if e.holds(s.artifact) != 1 {
				t.Fatal("no hold during the restore")
			}
			var staged int64
			if err := e.pool.QueryRow(t.Context(), "SELECT staged_bytes FROM restores WHERE id = $1", id).Scan(&staged); err != nil || staged != int64(len(s.stored)) {
				t.Fatalf("staged bytes %d %v", staged, err)
			}
			dir := filepath.Join(e.scratch, "restore-"+id.String())
			fi, err := os.Stat(dir)
			if err != nil || fi.Mode().Perm() != 0o700 {
				t.Fatalf("staging dir %v %v", fi, err)
			}
			ff, err := os.Stat(filepath.Join(dir, "artifact.age"))
			if err != nil || ff.Mode().Perm() != 0o600 {
				t.Fatalf("staging file %v %v", ff, err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "artifact.age")) //nolint:gosec // G304: the test scratch dir
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(raw, s.stored[100:200]) {
				t.Fatal("the staged file holds the stored bytes unwrapped")
			}
			if st.Manifest() == nil || st.Manifest().RunID != s.run.String() || st.Format() != pipeline.FormatSQL {
				t.Fatalf("manifest %+v format %s", st.Manifest(), st.Format())
			}
			if err := st.BeginRestore(t.Context()); err != nil {
				t.Fatal(err)
			}
			if got := readAllOpen(t, st); !bytes.Equal(got, dump) {
				t.Fatalf("opened %d bytes, want the %d-byte dump", len(got), len(dump))
			}
			if _, err := st.Open(t.Context()); !errors.Is(err, restore.ErrAlreadyOpened) {
				t.Fatalf("second open: %v", err)
			}
			st.Cleanup()
			st.Cleanup() // idempotent
			if ents := e.scratchEntries(); len(ents) != 0 {
				t.Fatalf("scratch not cleaned: %v", ents)
			}
			if e.holds(s.artifact) != 0 {
				t.Fatal("hold not released")
			}
			if _, err := st.Open(t.Context()); !errors.Is(err, restore.ErrCleanedUp) {
				t.Fatalf("open after cleanup: %v", err)
			}
			if ok, err := e.svc.Finish(t.Context(), id, restore.StatusSucceeded, nil); err != nil || !ok {
				t.Fatalf("finish %v %v", ok, err)
			}
			if got, _ := e.status(id); got != "succeeded" {
				t.Fatalf("status %s", got)
			}
			// A second worker attempt finds it no longer queued.
			if _, err := e.svc.Stage(t.Context(), id); !errors.Is(err, restore.ErrNotQueued) {
				t.Fatalf("restage: %v", err)
			}
			if len(e.auditReasons(id)) != 0 || e.alertCount() != 0 {
				t.Fatal("a good restore raised a verification failure")
			}
		})
	}
}

// TestOptOutStagedEncryptedAtRest: the staged copy of an unencrypted
// (opted-out) artifact is never plaintext on the scratch volume.
func TestOptOutStagedEncryptedAtRest(t *testing.T) {
	e := newEnv(t)
	j := e.job(jobkeys.ModeNone, e.diskID)
	dump := randomDump(200_000) // incompressible: zstd stores it verbatim
	s := e.seed(j, e.diskID, e.disk, dump, time.Now())
	if !bytes.Contains(s.stored, dump[1000:1064]) {
		t.Fatal("test premise: the opt-out artifact should hold the dump in clear")
	}
	id := e.mustCreate(j, s.artifact, "")
	st, err := e.svc.Stage(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Cleanup()
	raw, err := os.ReadFile(filepath.Join(e.scratch, "restore-"+id.String(), "artifact.age"))
	if err != nil {
		t.Fatal(err)
	}
	for off := 0; off+64 <= len(dump); off += 4096 {
		if bytes.Contains(raw, dump[off:off+64]) {
			t.Fatalf("plaintext of the dump at offset %d is on the scratch volume", off)
		}
	}
	if !bytes.HasPrefix(raw, []byte("age-encryption.org/v1\n")) {
		t.Fatal("the staged file is not age-encrypted")
	}
	if got := readAllOpen(t, st); !bytes.Equal(got, dump) {
		t.Fatal("opt-out dump differs")
	}
}

func checkFailed(t *testing.T, e *env, id, artifact uuid.UUID, wantCode string) {
	t.Helper()
	if st, code := e.status(id); st != "failed" || code != wantCode {
		t.Fatalf("restore %s / %s, want failed / %s", st, code, wantCode)
	}
	if ents := e.scratchEntries(); len(ents) != 0 {
		t.Fatalf("staging left behind: %v", ents)
	}
	if e.holds(artifact) != 0 {
		t.Fatal("hold not released")
	}
}

// TestTampering: a tampered stored byte, a tampered manifest and a
// replayed manifest (of another run) are refused, audited and alerted.
func TestTampering(t *testing.T) {
	t.Run("stored byte", func(t *testing.T) {
		e := newEnv(t)
		j := e.job(jobkeys.ModePassphrase, e.diskID)
		s := e.seed(j, e.diskID, e.disk, randomDump(100_000), time.Now())
		bad := bytes.Clone(s.stored)
		bad[len(bad)/2] ^= 0x01
		e.replace(s.key, bad)
		id := e.mustCreate(j, s.artifact, "")
		_, err := e.svc.Stage(t.Context(), id)
		var ve *restore.VerificationError
		if !errors.Is(err, restore.ErrVerification) || !errors.As(err, &ve) || ve.Code != restore.VerifyStoredDigest {
			t.Fatalf("stage: %v", err)
		}
		checkFailed(t, e, id, s.artifact, restore.CodeVerification)
		if r := e.auditReasons(id); len(r) != 1 || r[0] != restore.VerifyStoredDigest {
			t.Fatalf("audit %v", r)
		}
		if e.alertCount() != 1 {
			t.Fatal("no security alert")
		}
	})
	t.Run("manifest signature", func(t *testing.T) {
		e := newEnv(t)
		j := e.job(jobkeys.ModePassphrase, e.diskID)
		s := e.seed(j, e.diskID, e.disk, randomDump(10_000), time.Now())
		forged := bytes.Replace(s.manifest, []byte("0.0.0-test"), []byte("6.6.6-test"), 1)
		if bytes.Equal(forged, s.manifest) {
			t.Fatal("test premise: version string not found")
		}
		e.replace(s.mkey, forged)
		id := e.mustCreate(j, s.artifact, "")
		_, err := e.svc.Stage(t.Context(), id)
		if !errors.Is(err, pipeline.ErrManifestBadSignature) || !errors.Is(err, restore.ErrVerification) {
			t.Fatalf("stage: %v", err)
		}
		checkFailed(t, e, id, s.artifact, restore.CodeVerification)
		if r := e.auditReasons(id); len(r) != 1 || r[0] != restore.VerifyManifestSignature {
			t.Fatalf("audit %v", r)
		}
	})
	t.Run("replayed manifest", func(t *testing.T) {
		e := newEnv(t)
		j := e.job(jobkeys.ModePassphrase, e.diskID)
		a := e.seed(j, e.diskID, e.disk, randomDump(10_000), time.Now().Add(-time.Hour))
		b := e.seed(j, e.diskID, e.disk, randomDump(10_000), time.Now())
		e.replace(a.mkey, b.manifest)
		id := e.mustCreate(j, a.artifact, "")
		_, err := e.svc.Stage(t.Context(), id)
		if !errors.Is(err, pipeline.ErrReplay) || !errors.Is(err, pipeline.ErrBindingMismatch) {
			t.Fatalf("stage: %v", err)
		}
		checkFailed(t, e, id, a.artifact, restore.CodeVerification)
		if r := e.auditReasons(id); len(r) != 1 || r[0] != restore.VerifyManifestReplayed {
			t.Fatalf("audit %v", r)
		}
		if e.alertCount() != 1 {
			t.Fatal("no security alert")
		}
	})
}

// TestInsufficientScratchSpace: refused before anything is downloaded.
func TestInsufficientScratchSpace(t *testing.T) {
	e := newEnv(t, func(o *restore.Options) { o.ScratchReserve = math.MaxInt64 / 2 })
	j := e.job(jobkeys.ModePassphrase, e.diskID)
	s := e.seed(j, e.diskID, e.disk, randomDump(10_000), time.Now())
	id := e.mustCreate(j, s.artifact, "")
	e.op.gets.Store(0)
	_, err := e.svc.Stage(t.Context(), id)
	if !errors.Is(err, restore.ErrInsufficientSpace) || !errors.Is(err, storage.ErrInsufficientSpace) {
		t.Fatalf("stage: %v", err)
	}
	if n := e.op.gets.Load(); n != 0 {
		t.Fatalf("%d downloads before the space check", n)
	}
	checkFailed(t, e, id, s.artifact, restore.CodeInsufficientSpace)
}

// TestIdentityHandoff: required and matched at creation, used once and
// deleted, refused when expired; bekup_app cannot rewrite it or add one to
// a running restore.
func TestIdentityHandoff(t *testing.T) {
	e := newEnv(t)
	j := e.job(jobkeys.ModeRecipient, e.diskID)
	s := e.seed(j, e.diskID, e.disk, randomDump(10_000), time.Now())

	var fe *serviceutil.FieldError
	if _, err := e.create(j, s.artifact, ""); !errors.As(err, &fe) || fe.Field != "ageIdentity" {
		t.Fatalf("missing identity: %v", err)
	}
	other, err := crypto.GenerateAgeKeypair()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.create(j, s.artifact, string(other.Identity.Bytes())); !errors.As(err, &fe) {
		t.Fatalf("wrong identity: %v", err)
	}
	if _, err := e.create(j, s.artifact, "not a key"); !errors.As(err, &fe) || bytes.Contains([]byte(err.Error()), []byte("not a key")) {
		t.Fatalf("garbage identity: %v", err)
	}
	if n := e.count("SELECT count(*) FROM restores"); n != 0 {
		t.Fatalf("rejected requests left %d restores", n)
	}

	// Expired: refused and deleted.
	id := e.mustCreate(j, s.artifact, j.identity)
	if _, err := e.pool.Exec(t.Context(), "UPDATE restore_identity_handoff SET expires_at = now() WHERE restore_id = $1", id); err == nil {
		t.Fatal("bekup_app rewrote a handoff")
	}
	if err := e.d.SuperExec(t.Context(), fmt.Sprintf(
		"UPDATE restore_identity_handoff SET created_at = now() - interval '20 minutes', expires_at = now() - interval '6 minutes' WHERE restore_id = '%s'", id)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Stage(t.Context(), id); !errors.Is(err, restore.ErrIdentityExpired) {
		t.Fatalf("expired handoff: %v", err)
	}
	checkFailed(t, e, id, s.artifact, restore.CodeIdentityExpired)
	if n := e.count("SELECT count(*) FROM restore_identity_handoff"); n != 0 {
		t.Fatalf("expired handoff kept: %d", n)
	}
	// No handoff can be added to a restore that is not queued.
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO restore_identity_handoff (restore_id, sealed_identity, expires_at)
		VALUES ($1, '\x01', now() + interval '1 minute')`, id); err == nil {
		t.Fatal("a handoff was added to a finished restore")
	}

	// Housekeeping deletes expired handoffs of restores that never start.
	id2 := e.mustCreate(j, s.artifact, j.identity)
	if err := e.d.SuperExec(t.Context(), fmt.Sprintf(
		"UPDATE restore_identity_handoff SET created_at = now() - interval '20 minutes', expires_at = now() - interval '6 minutes' WHERE restore_id = '%s'", id2)); err != nil {
		t.Fatal(err)
	}
	if n, err := e.svc.DeleteExpiredHandoffs(t.Context()); err != nil || n != 1 {
		t.Fatalf("housekeeping %d %v", n, err)
	}

	// Used once: the row is gone after staging and the identity is
	// destroyed after Open.
	id3 := e.mustCreate(j, s.artifact, j.identity)
	st, err := e.svc.Stage(t.Context(), id3)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Cleanup()
	if n := e.count("SELECT count(*) FROM restore_identity_handoff"); n != 0 {
		t.Fatalf("handoff kept after use: %d", n)
	}
	_ = readAllOpen(t, st)

	// bekup_app cannot delete restores or change what they restore.
	if _, err := e.pool.Exec(t.Context(), "DELETE FROM restores WHERE id = $1", id); err == nil {
		t.Fatal("bekup_app deleted a restore")
	}
	if _, err := e.pool.Exec(t.Context(), "UPDATE restores SET status = 'queued' WHERE id = $1", id); err == nil {
		t.Fatal("a finished restore was re-queued")
	}
}

// blockingReader blocks after the first read until release is closed (or
// ctx ends).
type blockingReader struct {
	io.ReadCloser
	ctx     context.Context
	n       int
	reading chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingReader) Read(p []byte) (int, error) {
	if b.n > 0 {
		b.once.Do(func() { close(b.reading) })
		select {
		case <-b.release:
		case <-b.ctx.Done():
			return 0, b.ctx.Err()
		}
	}
	n, err := b.ReadCloser.Read(p[:min(len(p), 4096)])
	b.n += n
	return n, err
}

// stallArtifact makes the next Get of key block after its first bytes.
func (e *env) stallArtifact(ctx context.Context, key storage.Key) *blockingReader {
	br := &blockingReader{ctx: ctx, reading: make(chan struct{}), release: make(chan struct{})}
	e.op.setHook(func(k storage.Key, rc io.ReadCloser) io.ReadCloser {
		if k.String() != key.String() {
			return rc
		}
		br.ReadCloser = rc
		return br
	})
	return br
}

// TestCleanupOnCancelAndPanic: a canceled staging and a panicking one
// leave no files and no hold behind; the end of the worker context cleans
// up a staged restore.
func TestCleanupOnCancelAndPanic(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		e := newEnv(t)
		j := e.job(jobkeys.ModePassphrase, e.diskID)
		s := e.seed(j, e.diskID, e.disk, randomDump(500_000), time.Now())
		id := e.mustCreate(j, s.artifact, "")
		ctx, cancel := context.WithCancel(t.Context())
		br := e.stallArtifact(ctx, s.key)
		done := make(chan error, 1)
		go func() {
			_, err := e.svc.Stage(ctx, id)
			done <- err
		}()
		<-br.reading
		if len(e.scratchEntries()) != 1 {
			t.Fatal("no partial staging while downloading")
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("stage: %v", err)
		}
		if st, code := e.status(id); st != "canceled" || code != restore.CodeCanceled {
			t.Fatalf("status %s %s", st, code)
		}
		if ents := e.scratchEntries(); len(ents) != 0 {
			t.Fatalf("partial staging left behind: %v", ents)
		}
		if e.holds(s.artifact) != 0 {
			t.Fatal("hold not released")
		}
	})
	t.Run("panic", func(t *testing.T) {
		e := newEnv(t)
		j := e.job(jobkeys.ModePassphrase, e.diskID)
		s := e.seed(j, e.diskID, e.disk, randomDump(500_000), time.Now())
		id := e.mustCreate(j, s.artifact, "")
		e.op.setHook(func(k storage.Key, rc io.ReadCloser) io.ReadCloser {
			if k.String() != s.key.String() {
				return rc
			}
			return &panicReader{ReadCloser: rc}
		})
		func() {
			defer func() {
				if p := recover(); p == nil {
					t.Fatal("no panic")
				}
			}()
			if _, err := e.svc.Stage(t.Context(), id); err != nil {
				t.Errorf("a panicking staging returned %v", err)
			}
		}()
		checkFailed(t, e, id, s.artifact, restore.CodeInternal)
	})
	t.Run("context end", func(t *testing.T) {
		e := newEnv(t)
		j := e.job(jobkeys.ModePassphrase, e.diskID)
		s := e.seed(j, e.diskID, e.disk, randomDump(10_000), time.Now())
		id := e.mustCreate(j, s.artifact, "")
		ctx, cancel := context.WithCancel(t.Context())
		st, err := e.svc.Stage(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(e.scratchEntries()) != 1 {
			t.Fatal("not staged")
		}
		cancel()
		deadline := time.Now().Add(10 * time.Second)
		for len(e.scratchEntries()) != 0 || e.holds(s.artifact) != 0 {
			if time.Now().After(deadline) {
				t.Fatal("the end of the worker context did not clean up")
			}
			time.Sleep(20 * time.Millisecond)
		}
		if _, err := st.Open(t.Context()); !errors.Is(err, restore.ErrCleanedUp) {
			t.Fatalf("open: %v", err)
		}
	})
}

type panicReader struct {
	io.ReadCloser
	n int
}

func (p *panicReader) Read(b []byte) (int, error) {
	if p.n > 0 {
		panic("destination reader exploded")
	}
	n, err := p.ReadCloser.Read(b[:min(len(b), 4096)])
	p.n += n
	return n, err
}

// TestHoldBlocksRetention: retention running while a restore stages skips
// the held artifact; after the cleanup it deletes it.
func TestHoldBlocksRetention(t *testing.T) {
	e := newEnv(t)
	j := e.job(jobkeys.ModePassphrase, e.diskID)
	e.exec("UPDATE jobs SET retention_keep_last = 1, retention_keep_days = NULL WHERE id = $1", j.id)
	dump := randomDump(400_000)
	old := e.seed(j, e.diskID, e.disk, dump, time.Now().Add(-2*time.Hour))
	e.seed(j, e.diskID, e.disk, randomDump(1000), time.Now().Add(-time.Hour))
	ret, err := retention.New(retention.Options{DB: e.pool, Auditor: e.auditor, Destinations: e.op, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	id := e.mustCreate(j, old.artifact, "")
	br := e.stallArtifact(t.Context(), old.key)
	type result struct {
		st  *restore.Staged
		err error
	}
	done := make(chan result, 1)
	go func() {
		st, err := e.svc.Stage(t.Context(), id)
		done <- result{st, err}
	}()
	select {
	case <-br.reading:
	case r := <-done:
		t.Fatalf("staging ended early: %v", r.err)
	case <-time.After(30 * time.Second):
		t.Fatal("staging did not start")
	}
	// Retention races the restore: the hold wins.
	res, err := ret.Sweep(t.Context(), j.id, queue.RetentionPostRun, false)
	if err != nil || len(res.Destinations) != 1 {
		t.Fatalf("sweep %+v %v", res, err)
	}
	if dr := res.Destinations[0]; dr.Held != 1 || dr.Deleted != 0 {
		t.Fatalf("sweep during the restore: %+v", dr)
	}
	close(br.release)
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	e.op.setHook(nil)
	if got := readAllOpen(t, r.st); !bytes.Equal(got, dump) {
		t.Fatal("dump differs")
	}
	r.st.Cleanup()
	res, err = ret.Sweep(t.Context(), j.id, queue.RetentionDaily, false)
	if err != nil || len(res.Destinations) != 1 || res.Destinations[0].Deleted != 2 || res.Destinations[0].Held != 0 {
		t.Fatalf("sweep after the restore: %+v %v", res, err)
	}
	// A restore of the deleted artifact is refused.
	if _, err := e.create(j, old.artifact, ""); !errors.Is(err, restore.ErrUnavailable) {
		t.Fatalf("restore of a deleted artifact: %v", err)
	}
}

// TestSweepScratch: leftovers of restores that are not running (and of
// unknown ones) are removed; running ones only once stale; other entries
// stay.
func TestSweepScratch(t *testing.T) {
	e := newEnv(t)
	j := e.job(jobkeys.ModePassphrase, e.diskID)
	s := e.seed(j, e.diskID, e.disk, randomDump(1000), time.Now())
	finished := e.mustCreate(j, s.artifact, "")
	if _, err := e.svc.Finish(t.Context(), finished, restore.StatusFailed, errors.New("x")); err != nil {
		t.Fatal(err)
	}
	running := e.mustCreate(j, s.artifact, "")
	e.exec("UPDATE restores SET status = 'restoring', started_at = now() WHERE id = $1", running)
	if err := os.MkdirAll(e.scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"restore-" + finished.String(), "restore-" + running.String(), "restore-" + uuid.NewString(), "keep-me"} {
		if err := os.MkdirAll(filepath.Join(e.scratch, n, "sub"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := e.svc.SweepScratch(t.Context()); err != nil || n != 2 {
		t.Fatalf("sweep %d %v", n, err)
	}
	if got := e.scratchEntries(); len(got) != 2 || got[0] != "keep-me" || got[1] != "restore-"+running.String() {
		t.Fatalf("left %v", got)
	}
	later := e.service(func(o *restore.Options) {
		o.StaleAfter = time.Hour
		o.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	})
	if n, err := later.SweepScratch(t.Context()); err != nil || n != 1 {
		t.Fatalf("stale sweep %d %v", n, err)
	}
	if got := e.scratchEntries(); len(got) != 1 || got[0] != "keep-me" {
		t.Fatalf("left %v", got)
	}
}

// TestScratchPermissions: a group- or world-writable scratch directory is
// refused.
func TestScratchPermissions(t *testing.T) {
	e := newEnv(t)
	j := e.job(jobkeys.ModePassphrase, e.diskID)
	s := e.seed(j, e.diskID, e.disk, randomDump(1000), time.Now())
	if err := os.MkdirAll(e.scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(e.scratch, 0o777); err != nil { //nolint:gosec // G302: the insecure mode under test
		t.Fatal(err)
	}
	id := e.mustCreate(j, s.artifact, "")
	if _, err := e.svc.Stage(t.Context(), id); err == nil {
		t.Fatal("a world-writable scratch directory was used")
	}
	if st, code := e.status(id); st != "failed" || code != restore.CodeInternal {
		t.Fatalf("status %s %s", st, code)
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

// TestStageFromS3: the same staging and verification against an S3
// destination, including a tampered object.
func TestStageFromS3(t *testing.T) {
	endpoint, admin := startMinio(t)
	e := newEnv(t)
	if _, err := admin.CreateBucket(t.Context(), &s3.CreateBucketInput{Bucket: aws.String("restores")}); err != nil {
		t.Fatal(err)
	}
	guard, err := netguard.New(netguard.Options{LoopbackAllowable: true, AllowCIDRs: []string{"127.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	openS3 := func() (storage.Destination, error) {
		return s3store.Open(context.Background(), s3store.Config{
			Endpoint: endpoint, Region: "us-east-1", Bucket: "restores", PathStyle: true,
			AccessKeyID: minioUser, SecretAccessKey: crypto.NewSecret([]byte(minioPass)),
			AllowHTTP: true, Guard: guard,
		})
	}
	s3ID := e.destRow("s3")
	e.op.open[s3ID] = openS3
	dest, err := openS3()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := dest.Close(); err != nil {
			t.Error(err)
		}
	}()
	j := e.job(jobkeys.ModePassphrase, s3ID)
	dump := randomDump(2 << 20)
	s := e.seed(j, s3ID, dest, dump, time.Now())
	id := e.mustCreate(j, s.artifact, "")
	st, err := e.svc.Stage(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got := readAllOpen(t, st); !bytes.Equal(got, dump) {
		t.Fatal("dump differs")
	}
	st.Cleanup()
	if len(e.scratchEntries()) != 0 {
		t.Fatal("not cleaned up")
	}

	// A swapped object on the bucket is refused.
	bad := bytes.Clone(s.stored)
	bad[10] ^= 0xff
	if _, err := admin.PutObject(t.Context(), &s3.PutObjectInput{
		Bucket: aws.String("restores"), Key: aws.String(s.key.String()), Body: bytes.NewReader(bad),
	}); err != nil {
		t.Fatal(err)
	}
	id2 := e.mustCreate(j, s.artifact, "")
	if _, err := e.svc.Stage(t.Context(), id2); !errors.Is(err, restore.ErrVerification) {
		t.Fatalf("tampered S3 object: %v", err)
	}
	checkFailed(t, e, id2, s.artifact, restore.CodeVerification)
}
