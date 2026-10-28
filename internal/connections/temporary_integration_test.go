//go:build integration

package connections

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/engine"
)

// testClock is a settable clock for the service.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTempEnv(t *testing.T) (*svcEnv, *testClock) {
	t.Helper()
	e := newSvcEnv(t)
	clock := &testClock{now: time.Now().UTC().Truncate(time.Second)}
	e.svc.o.Now = clock.Now
	e.actor.AllowTemporary = true
	return e, clock
}

func tempInput(name, host string) CreateInput {
	return CreateInput{
		Name: name, Engine: engine.KindPostgres, VersionKey: "17", Host: host, Username: "restore",
		Password: []byte(dbPassword),
	}
}

// rawRow reads the stored columns the API never returns.
type rawRow struct {
	secret    []byte
	hasPw     bool
	deletedAt *time.Time
	temporary bool
}

func (e *svcEnv) raw(t *testing.T, id uuid.UUID) rawRow {
	t.Helper()
	var r rawRow
	if err := e.pool.QueryRow(t.Context(), `SELECT secret_enc, has_password, deleted_at, temporary FROM connections WHERE id = $1`, id).
		Scan(&r.secret, &r.hasPw, &r.deletedAt, &r.temporary); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestTemporaryConnectionLifecycle(t *testing.T) {
	noPublicRoots(t)
	e, clock := newTempEnv(t)
	ctx := t.Context()
	created := clock.Now()

	// A permanent connection and a temporary one may share a name.
	if _, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "Prod", Engine: engine.KindPostgres, VersionKey: "17", Host: "db.example.com", Username: "u"}); err != nil {
		t.Fatal(err)
	}
	in := tempInput("Prod", "restore.example.com")
	pw := in.Password
	c, err := e.svc.CreateTemporary(ctx, e.actor, in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Trim(string(pw), "\x00") != "" {
		t.Fatal("CreateTemporary did not zero the password input")
	}
	if !c.Temporary || c.ExpiresAt == nil || !c.ExpiresAt.Equal(created.Add(DefaultTempTTL)) || c.TLSMode != TLSPinned || !c.HasPassword {
		t.Fatalf("created: %+v", c)
	}
	if bytes.Contains(e.raw(t, c.ID).secret, []byte(dbPassword)) {
		t.Fatal("the stored blob contains the password")
	}
	// A second one without a name gets the default name.
	anon, err := e.svc.CreateTemporary(ctx, e.actor, tempInput("", "other.example.com"))
	if err != nil || anon.Name != "Temporary: other.example.com" {
		t.Fatalf("default name: %v %+v", err, anon)
	}
	if got := e.auditEntries(t, audit.ActionConnectionTemporaryCreated); len(got) != 2 || got[0]["expires_at"] == nil {
		t.Fatalf("created audit: %v", got)
	}

	// D-132 and the other validation still apply.
	var fe *FieldError
	if _, err := e.svc.CreateTemporary(ctx, e.actor, tempInput("IP", "10.1.2.3")); !errors.As(err, &fe) || fe.Field != "host" {
		t.Fatalf("ip literal with pinned TLS: %v", err)
	}
	if _, err := e.svc.CreateTemporary(ctx, e.actor, CreateInput{Engine: engine.KindPostgres, VersionKey: "nope", Host: "x.example.com", Username: "u"}); !errors.As(err, &fe) || fe.Field != "versionKey" {
		t.Fatalf("unknown version: %v", err)
	}

	// Excluded from everything except the restore paths.
	list, err := e.svc.List(ctx)
	if err != nil || len(list) != 1 || list[0].Temporary {
		t.Fatalf("list: %v %+v", err, list)
	}
	if _, err := e.svc.Get(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get: %v", err)
	}
	if _, err := e.svc.Update(ctx, e.actor, c.ID, UpdateInput{Name: ptr("x")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update: %v", err)
	}
	if err := e.svc.Delete(ctx, e.actor, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete: %v", err)
	}
	if _, err := e.svc.SetTLSMode(ctx, e.actor, c.ID, TLSUnverified); !errors.Is(err, ErrNotFound) {
		t.Fatalf("set tls mode: %v", err)
	}
	if _, _, err := e.svc.Target(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("backup target: %v", err)
	}
	tl, err := e.svc.ListTemporary(ctx)
	if err != nil || len(tl) != 2 || tl[0].ID != anon.ID || tl[1].ID != c.ID {
		t.Fatalf("list temporary: %v %+v", err, tl)
	}
	if g, err := e.svc.GetForRestore(ctx, c.ID); err != nil || !g.Temporary {
		t.Fatalf("get for restore: %v %+v", err, g)
	}

	// Without AllowTemporary (maintainers) the runner and pin operations
	// do not see it; admins pin it like any other connection.
	maint := Actor{UserID: e.actor.UserID}
	if _, err := e.svc.Test(ctx, maint, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("test without AllowTemporary: %v", err)
	}
	if _, err := e.svc.FetchCertificate(ctx, maint, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("fetch certificate without AllowTemporary: %v", err)
	}
	leaf := newCert(t, "restore.example.com", false, nil, time.Now().Add(60*24*time.Hour))
	pinIn := PinInput{CertificatePEM: leaf.pem, LeafSPKI: hex.EncodeToString(leaf.spki)}
	if _, err := e.svc.ConfirmPin(ctx, maint, c.ID, pinIn); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pin without AllowTemporary: %v", err)
	}
	if _, _, err := e.svc.RestoreTarget(ctx, c.ID); err == nil {
		t.Fatal("restore target without a pin")
	}
	if c, err = e.svc.ConfirmPin(ctx, e.actor, c.ID, pinIn); err != nil || !c.Ready || !c.Temporary {
		t.Fatalf("pin: %v %+v", err, c)
	}
	tgt, release, err := e.svc.RestoreTarget(ctx, c.ID)
	if err != nil || string(tgt.GetPassword()) != dbPassword || len(tgt.GetPins()) != 1 {
		t.Fatalf("restore target: %v %v", err, tgt)
	}
	release()
	if len(tgt.GetPassword()) != 0 {
		t.Fatal("release did not zero the request")
	}

	// Extend: in the future and at most TempMaxTTL after the creation.
	if _, err := e.svc.ExtendTemporary(ctx, e.actor, c.ID, created.Add(DefaultTempMaxTTL+time.Second)); !errors.As(err, &fe) || fe.Field != "expiresAt" {
		t.Fatalf("extend beyond the max: %v", err)
	}
	if _, err := e.svc.ExtendTemporary(ctx, e.actor, c.ID, created.Add(-time.Minute)); !errors.As(err, &fe) {
		t.Fatalf("extend into the past: %v", err)
	}
	if c, err = e.svc.ExtendTemporary(ctx, e.actor, c.ID, created.Add(DefaultTempMaxTTL)); err != nil || !c.ExpiresAt.Equal(created.Add(DefaultTempMaxTTL)) {
		t.Fatalf("extend: %v %+v", err, c)
	}
	if got := e.auditEntries(t, audit.ActionConnectionTemporaryExtended); len(got) != 1 {
		t.Fatalf("extend audit: %v", got)
	}
	if _, err := e.svc.ExtendTemporary(ctx, e.actor, list[0].ID, created.Add(time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("extend a permanent connection: %v", err)
	}

	// Nothing is due yet.
	if res, err := e.svc.CleanupTemporary(ctx); err != nil || res != (CleanupResult{}) {
		t.Fatalf("early cleanup: %v %+v", err, res)
	}

	// After the default TTL the unextended one has expired: unusable at
	// once, then crypto-shredded and deleted by the cleanup.
	clock.Add(DefaultTempTTL)
	if _, err := e.svc.GetForRestore(ctx, anon.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired connection still usable: %v", err)
	}
	if _, err := e.svc.ExtendTemporary(ctx, e.actor, anon.ID, clock.Now().Add(time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("extend an expired connection: %v", err)
	}
	res, err := e.svc.CleanupTemporary(ctx)
	if err != nil || res.Expired != 1 || res.RestoreFinished != 0 {
		t.Fatalf("cleanup: %v %+v", err, res)
	}
	r := e.raw(t, anon.ID)
	if r.secret != nil || r.hasPw || r.deletedAt == nil || !r.temporary {
		t.Fatalf("not shredded: %+v", r)
	}
	exp := e.auditEntries(t, audit.ActionConnectionTemporaryExpired)
	if len(exp) != 1 || exp[0]["reason"] != ReasonExpired || strings.Join(anyStrings(exp[0]["destroyed_fields"]), ",") != "password" {
		t.Fatalf("expired audit: %v", exp)
	}
	page, err := e.auditor.List(ctx, audit.Filter{Action: audit.ActionConnectionTemporaryExpired}, 0, 10)
	if err != nil || len(page.Entries) != 1 || page.Entries[0].ActorKind != audit.ActorSystem || page.Entries[0].TargetID == nil || *page.Entries[0].TargetID != anon.ID.String() {
		t.Fatalf("expired audit entry: %v %+v", err, page.Entries)
	}
	if err := e.svc.DeleteTemporary(ctx, e.actor, anon.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete after cleanup: %v", err)
	}

	// Manual deletion shreds as well.
	if err := e.svc.DeleteTemporary(ctx, e.actor, list[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete a permanent connection as temporary: %v", err)
	}
	if err := e.svc.DeleteTemporary(ctx, e.actor, c.ID); err != nil {
		t.Fatal(err)
	}
	if r := e.raw(t, c.ID); r.secret != nil || r.deletedAt == nil {
		t.Fatalf("manual delete not shredded: %+v", r)
	}
	del := e.auditEntries(t, audit.ActionConnectionTemporaryDeleted)
	if len(del) != 1 || del[0]["reason"] != "manual" {
		t.Fatalf("deleted audit: %v", del)
	}
	if tl, err := e.svc.ListTemporary(ctx); err != nil || len(tl) != 0 {
		t.Fatalf("list after delete: %v %+v", err, tl)
	}
	if strings.Contains(e.allAudit(t), dbPassword) {
		t.Fatal("a password reached the audit log")
	}
}

func TestTemporaryConnectionRestoreCleanup(t *testing.T) {
	e, clock := newTempEnv(t)
	ctx := t.Context()
	c, err := e.svc.CreateTemporary(ctx, e.actor, tempInput("R", "restore.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	busy, err := e.svc.CreateTemporary(ctx, e.actor, tempInput("B", "busy.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	// A running restore on c (task 4.1's restores table; the references
	// to artifact, run item and job are not needed here).
	rid := uuid.New()
	if err := e.db.SuperExec(ctx, "SET session_replication_role = replica",
		fmt.Sprintf(`INSERT INTO restores (id, artifact_id, run_item_id, job_id, target_connection_id, target_db_name, mode, status, started_at)
			VALUES ('%s', '%s', '%s', '%s', '%s', 'app', 'overwrite', 'restoring', now())`, rid, uuid.New(), uuid.New(), uuid.New(), c.ID),
		fmt.Sprintf(`INSERT INTO run_slots (holder_id, kind, connection_id) VALUES ('%s', 'restore', '%s')`, uuid.New(), busy.ID),
	); err != nil {
		t.Fatal(err)
	}
	if res, err := e.svc.CleanupTemporary(ctx); err != nil || res != (CleanupResult{}) {
		t.Fatalf("cleanup while the restore runs: %v %+v", err, res)
	}
	// The restore finished: kept for RestoreGrace, then deleted.
	if err := e.db.SuperExec(ctx, fmt.Sprintf(`UPDATE restores SET status = 'succeeded', finished_at = '%s' WHERE id = '%s'`,
		clock.Now().Format(time.RFC3339Nano), rid)); err != nil {
		t.Fatal(err)
	}
	clock.Add(RestoreGrace - time.Minute)
	if res, err := e.svc.CleanupTemporary(ctx); err != nil || res != (CleanupResult{}) {
		t.Fatalf("cleanup within the grace: %v %+v", err, res)
	}
	clock.Add(time.Minute)
	res, err := e.svc.CleanupTemporary(ctx)
	if err != nil || res.RestoreFinished != 1 || res.Expired != 0 {
		t.Fatalf("cleanup after the restore: %v %+v", err, res)
	}
	if r := e.raw(t, c.ID); r.secret != nil || r.deletedAt == nil {
		t.Fatalf("not shredded: %+v", r)
	}
	if got := e.auditEntries(t, audit.ActionConnectionTemporaryExpired); len(got) != 1 || got[0]["reason"] != ReasonRestoreFinished {
		t.Fatalf("audit: %v", got)
	}

	// An expired connection with a held run slot waits for the run.
	clock.Add(DefaultTempTTL)
	if res, err := e.svc.CleanupTemporary(ctx); err != nil || res != (CleanupResult{}) {
		t.Fatalf("cleanup with a held slot: %v %+v", err, res)
	}
	if err := e.db.SuperExec(ctx, fmt.Sprintf(`DELETE FROM run_slots WHERE connection_id = '%s'`, busy.ID)); err != nil {
		t.Fatal(err)
	}
	if res, err := e.svc.CleanupTemporary(ctx); err != nil || res.Expired != 1 {
		t.Fatalf("cleanup after the slot was released: %v %+v", err, res)
	}
}

func TestTemporaryConnectionCap(t *testing.T) {
	e, _ := newTempEnv(t)
	ctx := t.Context()
	var first Connection
	for i := range MaxActiveTemporary {
		c, err := e.svc.CreateTemporary(ctx, e.actor, CreateInput{Engine: engine.KindPostgres, VersionKey: "17", Host: "db.example.com", Username: "u"})
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		if i == 0 {
			first = c
		}
	}
	if _, err := e.svc.CreateTemporary(ctx, e.actor, CreateInput{Engine: engine.KindPostgres, VersionKey: "17", Host: "db.example.com", Username: "u"}); !errors.Is(err, ErrTooManyTemporary) {
		t.Fatalf("above the cap: %v", err)
	}
	if err := e.svc.DeleteTemporary(ctx, e.actor, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateTemporary(ctx, e.actor, CreateInput{Engine: engine.KindPostgres, VersionKey: "17", Host: "db.example.com", Username: "u"}); err != nil {
		t.Fatalf("after a delete: %v", err)
	}
}

// TestRunningRestoreTarget: a restore holding a run slot keeps its
// temporary target after it expired (the cleanup never deletes it while
// the slot is held), never after it was deleted, and nobody else gets an
// expired target.
func TestRunningRestoreTarget(t *testing.T) {
	noPublicRoots(t)
	e, clock := newTempEnv(t)
	ctx := t.Context()
	c, err := e.svc.CreateTemporary(ctx, e.actor, tempInput("R", "restore.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	leaf := newCert(t, "restore.example.com", false, nil, time.Now().Add(60*24*time.Hour))
	if _, err := e.svc.ConfirmPin(ctx, e.actor, c.ID, PinInput{CertificatePEM: leaf.pem, LeafSPKI: hex.EncodeToString(leaf.spki)}); err != nil {
		t.Fatal(err)
	}
	holder, other := uuid.New(), uuid.New()
	use := func(h uuid.UUID) error {
		tgt, release, err := e.svc.RunningRestoreTarget(ctx, c.ID, h)
		if err != nil {
			return err
		}
		defer release()
		if string(tgt.GetPassword()) != dbPassword {
			t.Fatalf("target %v", tgt)
		}
		return nil
	}
	if err := use(holder); err != nil {
		t.Fatalf("unexpired: %v", err)
	}
	if err := e.db.SuperExec(ctx, fmt.Sprintf(`INSERT INTO run_slots (holder_id, kind, connection_id) VALUES ('%s', 'restore', '%s')`, holder, c.ID)); err != nil {
		t.Fatal(err)
	}
	clock.Add(DefaultTempTTL + time.Minute)
	if _, _, err := e.svc.RestoreTarget(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired RestoreTarget: %v", err)
	}
	if err := use(other); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired, other holder: %v", err)
	}
	if err := use(holder); err != nil {
		t.Fatalf("expired, slot holder: %v", err)
	}
	if res, err := e.svc.CleanupTemporary(ctx); err != nil || res != (CleanupResult{}) {
		t.Fatalf("cleanup with the slot held: %v %+v", err, res)
	}
	// Deleted (an admin): gone for the slot holder too.
	if err := e.svc.DeleteTemporary(ctx, e.actor, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := use(holder); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted, slot holder: %v", err)
	}
}
