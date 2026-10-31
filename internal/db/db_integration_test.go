//go:build integration

package db_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db"
	"github.com/jlentink/bekup/internal/db/auditchain"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/logging"
)

func pgCode(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

func ptr[T any](v T) *T { return &v }

func TestMigrationsUpDownUp(t *testing.T) {
	ctx := withTimeout(t, 2*time.Minute)
	d := newTestDB(t)
	m := d.migrator(t)

	up, err := m.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(up) < 4 {
		t.Fatalf("applied %d migrations, want >= 4", len(up))
	}
	st, err := m.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range st {
		if !s.Applied {
			t.Fatalf("migration %d not applied", s.Version)
		}
	}
	if again, err := m.Up(ctx); err != nil || len(again) != 0 {
		t.Fatalf("second up: %v, %v", again, err)
	}
	// Leave some data behind so down really has to drop it.
	if _, err := sqlc.New(d.appPool(t)).InsertAuditLog(ctx, sqlc.InsertAuditLogParams{ActorKind: "system", Action: "test.down", Details: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DownOne(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DownTo(ctx, 0); err != nil {
		t.Fatal(err)
	}
	st, err = m.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range st {
		if s.Applied {
			t.Fatalf("migration %d still applied after down", s.Version)
		}
	}
	if _, err := m.Up(ctx); err != nil {
		t.Fatalf("up after down: %v", err)
	}
}

func TestMigratorRejectsWrongRole(t *testing.T) {
	ctx := withTimeout(t, time.Minute)
	d := newTestDB(t)
	for name, url := range map[string]string{
		"superuser": urlFor(superUser, superPassword, d.name),
		"app":       d.appURL,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := db.NewMigrator(ctx, config.DBCredentials{URL: []byte(url)}, tlsVerify, logging.NewRegistry())
			if err == nil || !strings.Contains(err.Error(), "bekup_owner") {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestMigratorConcurrent(t *testing.T) {
	ctx := withTimeout(t, 2*time.Minute)
	d := newTestDB(t)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Go(func() {
			m, err := db.NewMigrator(ctx, config.DBCredentials{URL: []byte(d.ownerURL)}, tlsVerify, logging.NewRegistry())
			if err != nil {
				errs[i] = err
				return
			}
			_, errs[i] = m.Up(ctx)
			errs[i] = errors.Join(errs[i], m.Close(context.WithoutCancel(ctx)))
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestNewPoolRuntimeParams(t *testing.T) {
	ctx := withTimeout(t, time.Minute)
	d := migratedDB(t)
	p, err := db.NewPool(ctx, config.DBCredentials{URL: []byte(d.appURL)}, db.PoolOptions{StatementTimeout: 1500 * time.Millisecond, ApplicationName: "bekup-x", Registry: logging.NewRegistry(), TLS: tlsVerify})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var st, app, iso string
	if err := p.QueryRow(ctx, "SELECT current_setting('statement_timeout'), current_setting('application_name'), current_setting('default_transaction_isolation')").Scan(&st, &app, &iso); err != nil {
		t.Fatal(err)
	}
	if st != "1500ms" || app != "bekup-x" || iso != "read committed" {
		t.Fatalf("got %q %q %q", st, app, iso)
	}
}

func TestAppPrivileges(t *testing.T) {
	ctx := withTimeout(t, time.Minute)
	d := migratedDB(t)
	app := d.appPool(t)
	q := sqlc.New(app)

	// DML on the core tables through the generated queries.
	u, err := q.InsertUser(ctx, sqlc.InsertUserParams{Email: "Admin@Example.com", DisplayName: "Admin", PasswordHash: ptr("x"), Role: sqlc.UserRoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	got, err := q.GetUserByEmail(ctx, "admin@example.COM") // citext
	if err != nil || got.ID != u.ID {
		t.Fatalf("GetUserByEmail: %v %v", got.ID, err)
	}
	if _, err := q.GetUserByID(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.InsertUser(ctx, sqlc.InsertUserParams{Email: "ADMIN@example.com", Role: sqlc.UserRoleUser}); pgCode(err) != "23505" {
		t.Fatalf("duplicate email: %v", err)
	}

	now := time.Now()
	hash := bytes.Repeat([]byte{7}, 32)
	ip := netip.MustParseAddr("192.0.2.10")
	s, err := q.InsertSession(ctx, sqlc.InsertSessionParams{
		TokenHash: hash, UserID: u.ID, Aal: 1,
		IdleExpiresAt: now.Add(30 * time.Minute), AbsoluteExpiresAt: now.Add(12 * time.Hour),
		Ip: &ip, UserAgent: ptr("test"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if gs, err := q.GetSessionByTokenHash(ctx, hash); err != nil || gs.ID != s.ID || *gs.Ip != ip {
		t.Fatalf("GetSessionByTokenHash: %+v %v", gs, err)
	}
	if n, err := q.TouchSession(ctx, sqlc.TouchSessionParams{ID: s.ID, IdleExpiresAt: now.Add(24 * time.Hour)}); err != nil || n != 1 {
		t.Fatalf("TouchSession: %d %v", n, err)
	}
	if gs, err := q.GetSessionByTokenHash(ctx, hash); err != nil || !gs.IdleExpiresAt.Equal(gs.AbsoluteExpiresAt) {
		t.Fatalf("idle expiry not capped at absolute expiry: %v vs %v", gs.IdleExpiresAt, gs.AbsoluteExpiresAt)
	}
	if n, err := q.RevokeSession(ctx, s.ID); err != nil || n != 1 {
		t.Fatalf("RevokeSession: %d %v", n, err)
	}
	if _, err := q.GetSessionByTokenHash(ctx, hash); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("revoked session returned: %v", err)
	}

	set, err := q.PutSetting(ctx, sqlc.PutSettingParams{Key: "smtp.host", Value: json.RawMessage(`{"host":"mail"}`), UpdatedBy: &u.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.PutSetting(ctx, sqlc.PutSettingParams{Key: "smtp.host", Value: json.RawMessage(`"x"`), SecretEnc: []byte{1}}); err != nil {
		t.Fatal(err)
	}
	gset, err := q.GetSetting(ctx, "smtp.host")
	if err != nil || string(gset.Value) != `"x"` || gset.UpdatedAt.Before(set.UpdatedAt) {
		t.Fatalf("GetSetting: %+v %v", gset, err)
	}

	// Things bekup_app must not be able to do.
	for _, stmt := range []string{
		"CREATE TABLE evil (id int)",
		"CREATE TEMP TABLE evil (id int)",
		"CREATE FUNCTION public.audit_log_chain2() RETURNS int LANGUAGE sql AS 'SELECT 1'",
		"INSERT INTO goose_db_version (version_id, is_applied) VALUES (999, true)",
		"DELETE FROM goose_db_version",
		"SELECT nextval('audit_log_id_seq')",
		"TRUNCATE users",
		"ALTER TABLE audit_log DISABLE TRIGGER USER",
		"DROP TABLE audit_log",
	} {
		if _, err := app.Exec(ctx, stmt); err == nil {
			t.Errorf("bekup_app could run %q", stmt)
		}
	}
}

func TestAuditLogAppendOnly(t *testing.T) {
	ctx := withTimeout(t, time.Minute)
	d := migratedDB(t)
	app := d.appPool(t)
	q := sqlc.New(app)

	row, err := q.InsertAuditLog(ctx, sqlc.InsertAuditLogParams{ActorKind: "system", Action: "test.insert", Details: json.RawMessage(`{"a":1}`)})
	if err != nil {
		t.Fatalf("bekup_app INSERT: %v", err)
	}
	if !bytes.Equal(row.PrevHash, auditchain.GenesisPrevHash) || len(row.Hash) != 32 {
		t.Fatalf("first row: prev=%x hash=%x", row.PrevHash, row.Hash)
	}

	// As bekup_app: privilege errors (42501).
	for _, stmt := range []string{
		"UPDATE audit_log SET action = 'forged'",
		"DELETE FROM audit_log",
		"TRUNCATE audit_log",
	} {
		_, err := app.Exec(ctx, stmt)
		if pgCode(err) != "42501" {
			t.Errorf("bekup_app %q: want insufficient_privilege, got %v", stmt, err)
		}
	}
	// Even the owner is stopped by the raising triggers.
	owner := d.ownerPool(t)
	for _, stmt := range []string{
		"UPDATE audit_log SET action = 'forged'",
		"DELETE FROM audit_log",
		"TRUNCATE audit_log",
	} {
		_, err := owner.Exec(ctx, stmt)
		if err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("owner %q: want append-only error, got %v", stmt, err)
		}
	}

	// Client-supplied id, ts, prev_hash and hash are ignored.
	forged := bytes.Repeat([]byte{0xAA}, 32)
	var id int64
	var ts time.Time
	var prev, h []byte
	err = app.QueryRow(ctx, `INSERT INTO audit_log (id, ts, actor_kind, action, prev_hash, hash)
		VALUES (424242, '2000-01-01T00:00:00Z', 'cli', 'test.forged', $1, $1)
		RETURNING id, ts, prev_hash, hash`, forged).Scan(&id, &ts, &prev, &h)
	if err != nil {
		t.Fatal(err)
	}
	if id == 424242 || ts.Year() == 2000 || bytes.Equal(prev, forged) || bytes.Equal(h, forged) || !bytes.Equal(prev, row.Hash) {
		t.Fatalf("client-supplied values were kept: id=%d ts=%v prev=%x hash=%x", id, ts, prev, h)
	}

	// Inserts outside READ COMMITTED are refused (they could fork the chain).
	tx, err := app.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sqlc.New(tx).InsertAuditLog(ctx, sqlc.InsertAuditLogParams{ActorKind: "system", Action: "test.rr", Details: json.RawMessage(`{}`)})
	if rbErr := tx.Rollback(ctx); rbErr != nil {
		t.Fatal(rbErr)
	}
	if err == nil || !strings.Contains(err.Error(), "READ COMMITTED") {
		t.Fatalf("repeatable read insert: %v", err)
	}

	verifyChain(ctx, t, app, 2)
}

func TestAuditChainConcurrent(t *testing.T) {
	ctx := withTimeout(t, 2*time.Minute)
	d := migratedDB(t)
	app := d.appPool(t)

	const workers, perWorker = 16, 15
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := range workers {
		wg.Go(func() {
			for i := range perWorker {
				err := db.Tx(ctx, app, func(q *sqlc.Queries) error {
					uid := uuid.New()
					ip := netip.MustParseAddr(fmt.Sprintf("2001:db8::%x", w*100+i))
					if i%3 == 0 {
						ip = netip.MustParseAddr(fmt.Sprintf("10.0.%d.%d", w, i))
					}
					p := sqlc.InsertAuditLogParams{
						ActorKind: "user", Action: "test.concurrent", ActorUserID: &uid,
						TargetType: ptr("job"), TargetID: ptr(fmt.Sprintf("w%d-%d", w, i)),
						Details: json.RawMessage(fmt.Sprintf(`{"z":[1,2.50,"é\"x\\"],"a":{"n":null,"b":true},"w":%d}`, w)),
					}
					if i%2 == 0 {
						p.Ip = &ip
					}
					if i%5 == 0 {
						p.ActorUserID, p.TargetType, p.TargetID = nil, nil, nil
						p.Details = json.RawMessage(`{}`)
					}
					_, err := q.InsertAuditLog(ctx, p)
					return err
				})
				if err != nil {
					errs <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	// Several rows in one statement and in one transaction chain correctly too.
	if _, err := app.Exec(ctx, `INSERT INTO audit_log (actor_kind, action)
		SELECT 'system', 'test.multi' FROM generate_series(1, 5)`); err != nil {
		t.Fatal(err)
	}
	verifyChain(ctx, t, app, workers*perWorker+5)
}

// verifyChain recomputes every hash in Go, compares the canonical bytes with
// the SQL audit_log_canonical() function and checks the links.
func verifyChain(ctx context.Context, t *testing.T, p *pgxpool.Pool, want int) {
	t.Helper()
	q := sqlc.New(p)
	prev := auditchain.GenesisPrevHash
	var after int64
	n := 0
	for {
		rows, err := q.ListAuditLogCanonical(ctx, sqlc.ListAuditLogCanonicalParams{AfterID: after, MaxRows: 50})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			break
		}
		for i := range rows {
			r := auditchain.FromCanonical(&rows[i])
			if err := auditchain.Verify(&r, prev, rows[i].Hash); err != nil {
				t.Fatal(err)
			}
			var sqlCanon []byte
			if err := p.QueryRow(ctx, `SELECT audit_log_canonical(id, ts, actor_user_id, actor_kind, action, target_type, target_id, ip, details, prev_hash)
				FROM audit_log WHERE id = $1`, r.ID).Scan(&sqlCanon); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(sqlCanon, auditchain.Canonical(&r)) {
				t.Fatalf("id %d: canonical mismatch\n sql=%q\n  go=%q", r.ID, sqlCanon, auditchain.Canonical(&r))
			}
			prev = rows[i].Hash
			after = rows[i].ID
			n++
		}
	}
	if n != want {
		t.Fatalf("verified %d rows, want %d", n, want)
	}
	latest, err := q.GetLatestAuditLog(ctx)
	if err != nil || !bytes.Equal(latest.Hash, prev) {
		t.Fatalf("latest row is not the chain head: %v", err)
	}
}

func TestAuditTamperDetected(t *testing.T) {
	ctx := withTimeout(t, time.Minute)
	d := migratedDB(t)
	app := d.appPool(t)
	q := sqlc.New(app)
	for i := range 3 {
		if _, err := q.InsertAuditLog(ctx, sqlc.InsertAuditLogParams{ActorKind: "system", Action: fmt.Sprintf("test.%d", i), Details: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := superExec(ctx, d.name,
		"ALTER TABLE audit_log DISABLE TRIGGER USER",
		"UPDATE audit_log SET action = 'forged' WHERE action = 'test.1'",
	); err != nil {
		t.Fatal(err)
	}
	rows, err := q.ListAuditLogCanonical(ctx, sqlc.ListAuditLogCanonicalParams{MaxRows: 10})
	if err != nil {
		t.Fatal(err)
	}
	prev := auditchain.GenesisPrevHash
	var verr error
	for i := range rows {
		r := auditchain.FromCanonical(&rows[i])
		if verr = auditchain.Verify(&r, prev, rows[i].Hash); verr != nil {
			break
		}
		prev = rows[i].Hash
	}
	if !errors.Is(verr, auditchain.ErrChainBroken) {
		t.Fatalf("tampering not detected: %v", verr)
	}
}

func TestTxHelper(t *testing.T) {
	ctx := withTimeout(t, time.Minute)
	d := migratedDB(t)
	app := d.appPool(t)
	q := sqlc.New(app)

	errBoom := errors.New("boom")
	err := db.Tx(ctx, app, func(q *sqlc.Queries) error {
		if _, err := q.InsertUser(ctx, sqlc.InsertUserParams{Email: "rollback@example.com", Role: sqlc.UserRoleUser}); err != nil {
			return err
		}
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("got %v", err)
	}
	if _, err := q.GetUserByEmail(ctx, "rollback@example.com"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("rolled-back row visible: %v", err)
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic not propagated")
			}
		}()
		err := db.Tx(ctx, app, func(q *sqlc.Queries) error {
			if _, err := q.InsertUser(ctx, sqlc.InsertUserParams{Email: "panic@example.com", Role: sqlc.UserRoleUser}); err != nil {
				return err
			}
			panic("boom")
		})
		t.Fatalf("Tx returned %v instead of panicking", err)
	}()
	if _, err := q.GetUserByEmail(ctx, "panic@example.com"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("row of panicked tx visible: %v", err)
	}

	if err := db.Tx(ctx, app, func(q *sqlc.Queries) error {
		_, err := q.InsertUser(ctx, sqlc.InsertUserParams{Email: "commit@example.com", Role: sqlc.UserRoleUser})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetUserByEmail(ctx, "commit@example.com"); err != nil {
		t.Fatal(err)
	}
}

type noopArgs struct {
	N int `json:"n"`
}

func (noopArgs) Kind() string { return "test_noop" }

type noopWorker struct {
	river.WorkerDefaults[noopArgs]
	done chan int
}

func (w *noopWorker) Work(_ context.Context, j *river.Job[noopArgs]) error {
	w.done <- j.Args.N
	return nil
}

// TestRiverAsApp proves bekup_app has every privilege River needs on the
// vendored schema.
func TestRiverAsApp(t *testing.T) {
	ctx := withTimeout(t, 2*time.Minute)
	d := migratedDB(t)
	app := d.appPool(t)

	w := &noopWorker{done: make(chan int, 1)}
	workers := river.NewWorkers()
	river.AddWorker(workers, w)
	client, err := river.NewClient(riverpgxv5.New(app), &river.Config{
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 2}},
		Workers: workers,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	if _, err := client.Insert(ctx, noopArgs{N: 42}, &river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}}); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-w.done:
		if n != 42 {
			t.Fatalf("got %d", n)
		}
	case <-ctx.Done():
		t.Fatal("job did not run")
	}
}

var (
	dumpNoise    = regexp.MustCompile(`(?m)^(-- Dumped (from|by) .*|\\(un)?restrict .*)\n`)
	blankRuns    = regexp.MustCompile(`\n{3,}`)
	schemaPath   = "schema.sql"
	updateEnvVar = "BEKUP_UPDATE_SCHEMA"
)

// TestSchemaDump checks that the migrations produce internal/db/schema.sql
// (pg_dump --schema-only, kept for review). Regenerate with `just db-schema`.
func TestSchemaDump(t *testing.T) {
	ctx := withTimeout(t, 2*time.Minute)
	d := migratedDB(t)
	out, err := containerExec(ctx, "pg_dump", "--username=postgres", "--schema-only", "--no-comments", "--dbname="+d.name)
	if err != nil {
		t.Fatal(err)
	}
	got := blankRuns.ReplaceAllString(dumpNoise.ReplaceAllString(out, ""), "\n\n")
	got = "-- Code generated by `just db-schema` (pg_dump --schema-only of all migrations). DO NOT EDIT.\n" + got
	if os.Getenv(updateEnvVar) == "1" {
		if err := os.WriteFile(schemaPath, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("%v (run `just db-schema`)", err)
	}
	if string(want) != got {
		t.Fatalf("internal/db/schema.sql is out of date with the migrations; run `just db-schema` and review the diff")
	}
}

// TestCanonicalGolden checks that Postgres hashes the golden row of the
// auditchain unit tests to the same value as Go.
func TestCanonicalGolden(t *testing.T) {
	ctx := withTimeout(t, time.Minute)
	d := migratedDB(t)
	var got string
	err := d.appPool(t).QueryRow(ctx, `SELECT encode(sha256(audit_log_canonical(
		42, '2026-10-26 12:34:56.789012+00', '0b9f6f3e-6d2a-4c1e-9a53-3e0d2c1b4a59', 'user',
		'job.update', 'job', NULL, '2001:db8::1', '{"name": "é", "a": 1}', decode(repeat('11', 32), 'hex'))), 'hex')`).Scan(&got)
	if err != nil {
		t.Fatal(err)
	}
	const goldenHash = "1772526146dd540a91fc512df6c44de8c15f23ea4cef084dc98137a97039aaeb" // auditchain_test.go
	if got != goldenHash {
		t.Fatalf("SQL hash %s != Go golden hash %s", got, goldenHash)
	}
}
