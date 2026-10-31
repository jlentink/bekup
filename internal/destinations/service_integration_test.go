//go:build integration

package destinations

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/netguard"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

type svcEnv struct {
	svc     *Service
	auditor *audit.Auditor
	q       *sqlc.Queries
	d       dbtest.DB
	pool    *pgxpool.Pool
	actor   Actor
	root    string
}

func newSvcEnv(t *testing.T, opts ...func(*Options)) *svcEnv {
	t.Helper()
	d := dbtest.Migrated(t)
	pool := d.AppPool(t)
	g, err := netguard.New(netguard.Options{LoopbackAllowable: true, AllowCIDRs: []string{"127.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	e := &svcEnv{auditor: audit.New(pool, audit.Options{Registry: logging.NewRegistry()}), q: sqlc.New(pool), pool: pool, d: d, root: t.TempDir()}
	o := Options{DB: pool, Keyring: testKeyring(t), Auditor: e.auditor, Guard: g, DiskRoot: e.root}
	for _, f := range opts {
		f(&o)
	}
	e.svc, err = New(o)
	if err != nil {
		t.Fatal(err)
	}
	u, err := e.q.InsertUser(t.Context(), sqlc.InsertUserParams{Email: "admin@example.com", DisplayName: "A", Role: sqlc.UserRoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	e.actor = Actor{UserID: u.ID}
	return e
}

func (e *svcEnv) auditEntries(t *testing.T, action audit.Action) []map[string]any {
	t.Helper()
	page, err := e.auditor.List(t.Context(), audit.Filter{Action: action}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]map[string]any, 0, len(page.Entries))
	for i := range page.Entries {
		en := &page.Entries[i]
		m := map[string]any{}
		if err := json.Unmarshal(en.Details, &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func TestServiceCRUD(t *testing.T) {
	e := newSvcEnv(t)
	ctx := t.Context()

	in := validS3()
	in.SecretAccessKey = []byte("first-secret-value")
	secretInput := in.SecretAccessKey
	d, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "Offsite", Type: TypeS3, S3: in})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(secretInput, make([]byte, len(secretInput))) {
		t.Fatal("the input secret was not zeroed")
	}
	if !d.Ready || !d.HasSecretAccessKey || d.S3.Bucket != "backups" {
		t.Fatalf("created: %+v", d)
	}
	// The secret is sealed under destinations:<id>:secret and nowhere else.
	row, err := e.q.GetDestination(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(row.SecretEnc, []byte("first-secret-value")) || bytes.Contains(row.Config, []byte("first-secret-value")) {
		t.Fatal("secret stored in clear")
	}
	creds, err := openCredentials(e.svc.o.Keyring, d.ID, row.SecretEnc)
	if err != nil || string(creds.SecretAccessKey) != "first-secret-value" {
		t.Fatalf("open: %v", err)
	}

	// Duplicate name (case-insensitive; another prefix: the location of
	// "Offsite" is taken, D-209).
	in2 := validS3()
	in2.Prefix = "dup"
	if _, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "offsite", Type: TypeS3, S3: in2}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("duplicate: %v", err)
	}

	// Update without a secret keeps the stored one.
	in3 := validS3()
	in3.SecretAccessKey = nil
	in3.Bucket = "other-bucket"
	d, err = e.svc.Update(ctx, e.actor, d.ID, UpdateInput{S3: in3})
	if err != nil || d.S3.Bucket != "other-bucket" {
		t.Fatalf("update: %v %+v", err, d)
	}
	row2, err := e.q.GetDestination(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(row.SecretEnc, row2.SecretEnc) {
		t.Fatal("secret changed without a new value")
	}
	// Update with a new secret.
	in4 := validS3()
	in4.SecretAccessKey = []byte("second-secret-value")
	in4.Bucket = "other-bucket"
	if _, err := e.svc.Update(ctx, e.actor, d.ID, UpdateInput{S3: in4}); err != nil {
		t.Fatal(err)
	}
	row3, err := e.q.GetDestination(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	creds, err = openCredentials(e.svc.o.Keyring, d.ID, row3.SecretEnc)
	if err != nil || string(creds.SecretAccessKey) != "second-secret-value" {
		t.Fatalf("new secret: %v", err)
	}
	// No-op update: no audit entry.
	before := len(e.auditEntries(t, audit.ActionDestinationUpdated))
	if _, err := e.svc.Update(ctx, e.actor, d.ID, UpdateInput{Name: ptr("Offsite")}); err != nil {
		t.Fatal(err)
	}
	if got := len(e.auditEntries(t, audit.ActionDestinationUpdated)); got != before {
		t.Fatalf("no-op update audited: %d -> %d", before, got)
	}
	// Wrong type input.
	if _, err := e.svc.Update(ctx, e.actor, d.ID, UpdateInput{Disk: &DiskInput{Path: "x"}}); err == nil {
		t.Fatal("disk input on an S3 destination accepted")
	}

	// Audit: created + updated, secrets only by name, never by value.
	for _, a := range []audit.Action{audit.ActionDestinationCreated, audit.ActionDestinationUpdated, audit.ActionSecretChanged} {
		entries := e.auditEntries(t, a)
		if len(entries) == 0 {
			t.Fatalf("no %s entry", a)
		}
		raw := mustJSON(t, entries)
		if strings.Contains(string(raw), "secret-value") {
			t.Fatalf("%s leaks a secret: %s", a, raw)
		}
	}
	upd := e.auditEntries(t, audit.ActionDestinationUpdated)
	found := false
	for _, u := range upd {
		raw := mustJSON(t, u["changed"])
		if strings.Contains(string(raw), `"secret_access_key"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("secret change not listed: %v", upd)
	}

	// A sealed blob moved to another row does not open (AAD).
	other, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "Other", Type: TypeS3, S3: validS3()})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.d.SuperExec(ctx, "UPDATE destinations SET secret_enc = (SELECT secret_enc FROM destinations WHERE id = '"+d.ID.String()+"') WHERE id = '"+other.ID.String()+"'"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Open(ctx, other.ID); err == nil || !strings.Contains(err.Error(), "open credentials") {
		t.Fatalf("swapped blob opened: %v", err)
	}

	// A destination used by a (non-deleted) job cannot be deleted (task
	// 3.5); once the job is deleted it can.
	jobID, err := dbtest.InsertJob(ctx, e.pool, dbtest.JobRow{DestinationIDs: []uuid.UUID{d.ID}})
	if err != nil {
		t.Fatal(err)
	}
	var inUse *InUseError
	if err := e.svc.Delete(ctx, e.actor, d.ID); !errors.Is(err, ErrInUse) || !errors.As(err, &inUse) || len(inUse.Jobs) != 1 {
		t.Fatalf("delete while in use: %v", err)
	}
	if _, err := e.pool.Exec(ctx, "UPDATE jobs SET deleted_at = now() WHERE id = $1", jobID); err != nil {
		t.Fatal(err)
	}

	// Delete: soft, then gone; the name is reusable.
	if err := e.svc.Delete(ctx, e.actor, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Get(ctx, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
	if err := e.svc.Delete(ctx, e.actor, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	reuse := validS3()
	reuse.Prefix = "reuse" // "Other" owns the bucket root (D-209)
	if _, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "Offsite", Type: TypeS3, S3: reuse}); err != nil {
		t.Fatalf("name reuse: %v", err)
	}
	if len(e.auditEntries(t, audit.ActionDestinationDeleted)) != 1 {
		t.Fatal("delete not audited")
	}
}

func TestServiceDiskTest(t *testing.T) {
	e := newSvcEnv(t)
	ctx := t.Context()
	d, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "Local", Type: TypeDisk, NoDelete: false, Disk: &DiskInput{Path: "local/backups"}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.Test(ctx, e.actor, d.ID)
	if err != nil || !res.OK || len(res.Steps) != 4 || res.ProbeRemains {
		t.Fatalf("test: %v %+v", err, res)
	}
	// No-delete mode leaves the probe.
	if _, err := e.svc.Update(ctx, e.actor, d.ID, UpdateInput{NoDelete: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	res, err = e.svc.Test(ctx, e.actor, d.ID)
	if err != nil || !res.OK || !res.ProbeRemains {
		t.Fatalf("no-delete test: %v %+v", err, res)
	}
	dest, err := e.svc.Open(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	if !dest.Capabilities().NoDeleteMode {
		t.Fatal("no-delete not applied")
	}
	if _, err := dest.Stat(ctx, res.ProbeKey); err != nil {
		t.Fatalf("probe below the disk path: %v", err)
	}
	if _, err := os.Stat(e.root + "/local/backups/" + res.ProbeKey.String()); err != nil {
		t.Fatalf("probe location: %v", err)
	}

	// Code L17: a soft-deleted destination is hidden from the API but
	// still opens for the workers (artifacts keep referencing it).
	if err := e.svc.Delete(ctx, e.actor, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Get(ctx, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted destination visible: %v", err)
	}
	if _, err := e.svc.Test(ctx, e.actor, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted destination tested: %v", err)
	}
	deleted, err := e.svc.Open(ctx, d.ID)
	if err != nil {
		t.Fatalf("open after delete: %v", err)
	}
	defer deleted.Close()
	if _, err := deleted.Stat(ctx, res.ProbeKey); err != nil {
		t.Fatalf("artifact of a deleted destination: %v", err)
	}
	if _, err := e.svc.Open(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("open unknown: %v", err)
	}
}

func TestServiceNotReadyAndRateLimit(t *testing.T) {
	// A deterministic limiter (5 operations, no refill) so the test doesn't
	// depend on how long each disk test takes on a loaded machine.
	var mu sync.Mutex
	used := map[string]int{}
	e := newSvcEnv(t, func(o *Options) {
		o.Limiter = func(key string) (bool, time.Duration) {
			mu.Lock()
			defer mu.Unlock()
			if used[key] >= 5 {
				return false, time.Minute
			}
			used[key]++
			return true, 0
		}
	})
	ctx := t.Context()
	d, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "SFTP", Type: TypeSFTP, SFTP: &SFTPInput{Host: "127.0.0.1", User: "u", BasePath: "/up"}})
	if err != nil {
		t.Fatal(err)
	}
	var nr *NotReadyError
	if _, err := e.svc.Test(ctx, e.actor, d.ID); !errors.As(err, &nr) {
		t.Fatalf("not ready: %v", err)
	}
	if _, err := e.svc.Open(ctx, d.ID); !errors.As(err, &nr) {
		t.Fatalf("open not ready: %v", err)
	}
	disk, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "D", Type: TypeDisk, Disk: &DiskInput{Path: "d"}})
	if err != nil {
		t.Fatal(err)
	}
	var limited *RateLimitedError
	for i := range 10 {
		_, err := e.svc.Test(ctx, Actor{UserID: e.actor.UserID}, disk.ID)
		if errors.As(err, &limited) {
			if i < 5 {
				t.Fatalf("limited after %d tests", i)
			}
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if limited == nil {
		t.Fatal("never rate limited")
	}
	// ConfirmHostKey and GenerateSSHKeypair refuse other types.
	if _, _, err := e.svc.GenerateSSHKeypair(ctx, e.actor, disk.ID); !errors.Is(err, ErrWrongType) {
		t.Fatalf("keypair on disk: %v", err)
	}
	if _, err := e.svc.Get(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func ptr[T any](v T) *T { return &v }

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
