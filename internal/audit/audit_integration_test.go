//go:build integration

package audit_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/instancekeys"
	"github.com/jlentink/bekup/internal/logging"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

type env struct {
	d    dbtest.DB
	pool *pgxpool.Pool
	a    *audit.Auditor
	kr   *crypto.Keyring
}

func setup(t *testing.T) (context.Context, env) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	t.Cleanup(cancel)
	d := dbtest.Migrated(t)
	p := d.AppPool(t)
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kr.Destroy)
	return ctx, env{d: d, pool: p, a: audit.New(p, audit.Options{Registry: logging.NewRegistry()}), kr: kr}
}

func (e env) record(ctx context.Context, t *testing.T, n int) {
	t.Helper()
	for i := range n {
		uid := uuid.New()
		ev := audit.Event{Actor: audit.UserActor(uid), Action: audit.ActionUserUpdated, TargetType: "user",
			TargetID: fmt.Sprintf("u%d", i), IP: netip.MustParseAddr("2001:db8::1"),
			Details: audit.Details{"field": "display_name", "password": "never-stored", "n": i}}
		if i%2 == 1 {
			ev = audit.Event{Actor: audit.SystemActor(), Action: audit.ActionSettingChanged, Details: audit.Details{"key": "x"}}
		}
		if _, err := e.a.Record(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
}

func verify(ctx context.Context, t *testing.T, e env, opts audit.VerifyOptions) *audit.Report {
	t.Helper()
	rep, err := audit.Verify(ctx, e.pool, opts)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestRecordVerifyList(t *testing.T) {
	ctx, e := setup(t)
	e.record(ctx, t, 7)
	if _, err := e.a.Record(ctx, audit.CLIEvent(audit.ActionCLIAdminCreated, "user", "admin@example.com", nil)); err != nil {
		t.Fatal(err)
	}
	rep := verify(ctx, t, e, audit.VerifyOptions{BatchSize: 3})
	if !rep.OK || rep.Rows != 8 || rep.FirstID != 1 || rep.LastID != 8 || !rep.NoSignedHead {
		t.Fatalf("report: %+v", rep)
	}
	part := verify(ctx, t, e, audit.VerifyOptions{FromID: 5})
	if !part.OK || !part.Partial || part.Rows != 4 {
		t.Fatalf("partial: %+v", part)
	}

	// Secrets in details never reach the table.
	var n int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE details::text LIKE '%never-stored%'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("secret stored: n=%d err=%v", n, err)
	}

	page, err := e.a.List(ctx, audit.Filter{Action: audit.ActionUserUpdated}, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 || page.NextCursor == 0 || page.Entries[0].ID <= page.Entries[1].ID {
		t.Fatalf("page 1: %+v", page)
	}
	page2, err := e.a.List(ctx, audit.Filter{Action: audit.ActionUserUpdated}, page.NextCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Entries) != 2 || page2.NextCursor != 0 {
		t.Fatalf("page 2: %+v", page2)
	}
	cli, err := e.a.List(ctx, audit.Filter{ActionPrefix: "cli.", ActorKind: audit.ActorCLI, TargetID: "admin@example.com",
		From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour)}, 0, 0)
	if err != nil || len(cli.Entries) != 1 || !strings.Contains(string(cli.Entries[0].Details), "hostname") {
		t.Fatalf("cli filter: %+v %v", cli, err)
	}
	none, err := e.a.List(ctx, audit.Filter{TargetType: "nothing"}, 0, 0)
	if err != nil || len(none.Entries) != 0 {
		t.Fatalf("empty filter: %+v %v", none, err)
	}
}

func TestTamperDetected(t *testing.T) {
	ctx, e := setup(t)
	e.record(ctx, t, 5)
	if err := e.d.SuperExec(ctx, "ALTER TABLE audit_log DISABLE TRIGGER USER",
		"UPDATE audit_log SET details = '{\"field\": \"role\"}' WHERE id = 3"); err != nil {
		t.Fatal(err)
	}
	rep := verify(ctx, t, e, audit.VerifyOptions{})
	if rep.OK || rep.Broken == nil || rep.Broken.ID != 3 || rep.Broken.Kind != audit.BreakHash || rep.Broken.TS.IsZero() {
		t.Fatalf("report: %+v broken=%+v", rep, rep.Broken)
	}
	if !errors.Is(rep.Err(), audit.ErrTampered) || !strings.Contains(rep.Err().Error(), "id 3") {
		t.Fatalf("err: %v", rep.Err())
	}
}

func TestDeletedRowDetected(t *testing.T) {
	ctx, e := setup(t)
	e.record(ctx, t, 5)
	if err := e.d.SuperExec(ctx, "ALTER TABLE audit_log DISABLE TRIGGER USER", "DELETE FROM audit_log WHERE id = 3"); err != nil {
		t.Fatal(err)
	}
	rep := verify(ctx, t, e, audit.VerifyOptions{})
	if rep.Broken == nil || rep.Broken.ID != 4 || rep.Broken.Kind != audit.BreakLink || !strings.Contains(rep.Broken.Reason, "missing") {
		t.Fatalf("broken: %+v", rep.Broken)
	}
	// Deleting the first row breaks the genesis rule.
	if err := e.d.SuperExec(ctx, "ALTER TABLE audit_log DISABLE TRIGGER USER", "DELETE FROM audit_log WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	rep = verify(ctx, t, e, audit.VerifyOptions{})
	if rep.Broken == nil || rep.Broken.ID != 2 || rep.Broken.Kind != audit.BreakGenesis {
		t.Fatalf("genesis: %+v", rep.Broken)
	}
}

func TestIDGapFromRollbackIsHarmless(t *testing.T) {
	ctx, e := setup(t)
	e.record(ctx, t, 2)
	errBoom := errors.New("boom")
	err := db.Tx(ctx, e.pool, func(q *sqlc.Queries) error {
		if _, err := e.a.RecordTx(ctx, q, audit.Event{Actor: audit.SystemActor(), Action: audit.ActionSettingChanged}); err != nil {
			return err
		}
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatal(err)
	}
	var n int
	if err := e.pool.QueryRow(ctx, "SELECT count(*) FROM audit_log").Scan(&n); err != nil || n != 2 {
		t.Fatalf("rolled-back RecordTx left a row: n=%d err=%v", n, err)
	}
	e.record(ctx, t, 1)
	rep := verify(ctx, t, e, audit.VerifyOptions{})
	if !rep.OK || rep.IDGaps != 1 || rep.Rows != 3 {
		t.Fatalf("report: %+v", rep)
	}
}

func TestRecordTxRejectsInvalid(t *testing.T) {
	ctx, e := setup(t)
	_, err := e.a.Record(ctx, audit.Event{Actor: audit.SystemActor(), Action: "made.up"})
	if !errors.Is(err, audit.ErrRecordFailed) || !errors.Is(err, audit.ErrInvalidEvent) {
		t.Fatalf("err: %v", err)
	}
}

func TestSignedHead(t *testing.T) {
	ctx, e := setup(t)
	e.record(ctx, t, 4)
	file := filepath.Join(t.TempDir(), "head.json")
	s := audit.NewHeadSigner(e.pool, e.kr, e.a, audit.FileExporter{Path: file})
	h, err := s.SignHead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The instance key creation is audited before signing, so the head covers it.
	if h.ChainID != 5 {
		t.Fatalf("head chain id %d", h.ChainID)
	}
	if _, signed, err := s.SignIfChanged(ctx); err != nil || signed {
		t.Fatalf("unchanged chain re-signed: %v %v", signed, err)
	}
	fh, err := audit.ReadHeadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	e.record(ctx, t, 2)
	rep := verify(ctx, t, e, audit.VerifyOptions{ExternalHead: &fh})
	if !rep.OK || len(rep.Heads) != 2 || rep.NoSignedHead {
		t.Fatalf("report: %+v", rep)
	}
	// A second head reuses the same key.
	h2, signed, err := s.SignIfChanged(ctx)
	if err != nil || !signed || h2.KeyID != h.KeyID || h2.ChainID != 7 {
		t.Fatalf("second head: %+v %v %v", h2, signed, err)
	}
	k, err := instancekeys.Load(ctx, sqlc.New(e.pool), e.kr)
	if err != nil || k.ID != h.KeyID {
		t.Fatalf("instance key: %v", err)
	}
	k.Destroy()

	// A forged signature in the database is detected.
	if err := e.d.SuperExec(ctx, "ALTER TABLE audit_chain_heads DISABLE TRIGGER USER",
		"UPDATE audit_chain_heads SET signature = set_byte(signature, 0, get_byte(signature, 0) # 1) WHERE id = (SELECT max(id) FROM audit_chain_heads)"); err != nil {
		t.Fatal(err)
	}
	rep = verify(ctx, t, e, audit.VerifyOptions{})
	if rep.OK || rep.Broken != nil || !strings.Contains(rep.Heads[0].Problem, "signature") {
		t.Fatalf("forged signature: %+v", rep.Heads)
	}
}

// A superuser rewrites the chain consistently (every later hash recomputed):
// the chain verifies on its own, but the signed head no longer matches.
func TestRewrittenChainDetectedByHead(t *testing.T) {
	ctx, e := setup(t)
	e.record(ctx, t, 5)
	file := filepath.Join(t.TempDir(), "head.json")
	if _, err := audit.NewHeadSigner(e.pool, e.kr, e.a, audit.FileExporter{Path: file}).SignHead(ctx); err != nil {
		t.Fatal(err)
	}
	fh, err := audit.ReadHeadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.d.SuperExec(ctx, "ALTER TABLE audit_log DISABLE TRIGGER USER",
		"UPDATE audit_log SET action = 'user.deleted' WHERE id = 2",
		`DO $$
		DECLARE r record; prev bytea := decode(repeat('00', 32), 'hex');
		BEGIN
			FOR r IN SELECT * FROM audit_log ORDER BY id LOOP
				UPDATE audit_log SET prev_hash = prev,
					hash = sha256(audit_log_canonical(r.id, r.ts, r.actor_user_id, r.actor_kind, r.action,
						r.target_type, r.target_id, r.ip, r.details, prev))
				WHERE id = r.id RETURNING hash INTO prev;
			END LOOP;
		END $$`); err != nil {
		t.Fatal(err)
	}
	rep := verify(ctx, t, e, audit.VerifyOptions{ExternalHead: &fh})
	if rep.Broken != nil {
		t.Fatalf("consistent rewrite should pass the chain walk: %+v", rep.Broken)
	}
	if rep.OK || len(rep.Heads) != 2 || rep.Heads[0].OK || rep.Heads[1].OK ||
		!strings.Contains(rep.Heads[0].Problem, "rewritten") {
		t.Fatalf("rewrite not detected: %+v", rep.Heads)
	}

	// Deleting the tail (including the signed row) is detected too.
	if err := e.d.SuperExec(ctx, "DELETE FROM audit_log WHERE id >= 5"); err != nil {
		t.Fatal(err)
	}
	rep = verify(ctx, t, e, audit.VerifyOptions{})
	if rep.OK || !strings.Contains(rep.Heads[0].Problem, "missing") {
		t.Fatalf("truncation not detected: %+v", rep.Heads)
	}
}

func TestConcurrentRecord(t *testing.T) {
	ctx, e := setup(t)
	const workers, per = 12, 10
	// One entry up front so the concurrent signers below never see an
	// empty chain (they would then create no instance key).
	e.record(ctx, t, 1)
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := range workers {
		wg.Go(func() {
			for i := range per {
				var err error
				if i%2 == 0 {
					_, err = e.a.Record(ctx, audit.Event{Actor: audit.SystemActor(), Action: audit.ActionSettingChanged,
						Details: audit.Details{"w": w, "i": i}})
				} else {
					err = db.Tx(ctx, e.pool, func(q *sqlc.Queries) error {
						_, err := e.a.RecordTx(ctx, q, audit.Event{Actor: audit.UserActor(uuid.New()), Action: audit.ActionAuthLogout})
						return err
					})
				}
				if err != nil {
					errs <- err
					return
				}
			}
		})
	}
	// Concurrent signers converge on one instance key.
	s := audit.NewHeadSigner(e.pool, e.kr, e.a, nil)
	for range 3 {
		wg.Go(func() {
			if _, _, err := s.SignIfChanged(ctx); err != nil && !errors.Is(err, audit.ErrEmptyChain) {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	rep := verify(ctx, t, e, audit.VerifyOptions{BatchSize: 7})
	if !rep.OK || rep.Rows < workers*per {
		t.Fatalf("report: %+v %v", rep, rep.Err())
	}
	var keys int
	if err := e.pool.QueryRow(ctx, "SELECT count(*) FROM instance_keys").Scan(&keys); err != nil || keys != 1 {
		t.Fatalf("instance keys: %d %v", keys, err)
	}
}

func TestAppCannotAlterHeadsOrKeys(t *testing.T) {
	ctx, e := setup(t)
	e.record(ctx, t, 1)
	if _, err := audit.NewHeadSigner(e.pool, e.kr, e.a, nil).SignHead(ctx); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"UPDATE audit_chain_heads SET chain_id = 1",
		"DELETE FROM audit_chain_heads",
		"DELETE FROM instance_keys",
		"UPDATE instance_keys SET public_key = public_key",
	} {
		if _, err := e.pool.Exec(ctx, stmt); err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("bekup_app %q: %v", stmt, err)
		}
	}
	if _, err := e.pool.Exec(ctx, "UPDATE instance_keys SET retired_at = now()"); err != nil {
		t.Errorf("retire key: %v", err)
	}
}
