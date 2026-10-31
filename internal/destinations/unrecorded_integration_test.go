//go:build integration

package destinations

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/dbtest"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/storage"
	"github.com/jlentink/bekup/internal/storage/sftp"
	"github.com/jlentink/bekup/internal/storage/sftp/sftptest"
	"github.com/jlentink/bekup/internal/testutil/dockertest"
)

const old = 48 * time.Hour

// layoutJob inserts a job using dest and returns its id and slug.
func (e *svcEnv) layoutJob(t *testing.T, dest uuid.UUID) (uuid.UUID, string) {
	t.Helper()
	id, err := dbtest.InsertJob(t.Context(), e.pool, dbtest.JobRow{Enabled: true, DestinationIDs: []uuid.UUID{dest}})
	if err != nil {
		t.Fatal(err)
	}
	var slug string
	if err := e.pool.QueryRow(t.Context(), `SELECT slug FROM jobs WHERE id = $1`, id).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	return id, slug
}

// runRow inserts a run of job with the directory timestamp ts.
func (e *svcEnv) runRow(t *testing.T, job uuid.UUID, status string, ts time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var finished *time.Time
	if status != "queued" && status != "running" {
		finished = &ts
	}
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO runs (id, job_id, trigger, status, started_at, finished_at, storage_timestamp)
		VALUES ($1, $2, 'schedule', $3::run_status, $4, $5, $4)`, id, job, status, ts, finished); err != nil {
		t.Fatal(err)
	}
	return id
}

// artifactRow records key as the manifest of run on dest.
func (e *svcEnv) artifactRow(t *testing.T, run, job, dest uuid.UUID, key string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	sum := make([]byte, 32)
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO artifacts (id, run_id, job_id, destination_id, kind, object_key, size, sha256, state)
		VALUES ($1, $2, $3, $4, 'manifest', $5, 1, $6, 'present')`, id, run, job, dest, key, sum); err != nil {
		t.Fatal(err)
	}
	return id
}

// writeAged writes a file below dir with the given age.
func writeAged(t *testing.T, dir, rel string, age time.Duration) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("stray "+rel), 0o600); err != nil {
		t.Fatal(err)
	}
	ageFile(t, p, age)
}

func ageFile(t *testing.T, p string, age time.Duration) {
	t.Helper()
	at := time.Now().Add(-age)
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
}

func (e *svcEnv) scan(t *testing.T, id uuid.UUID) {
	t.Helper()
	d, err := e.svc.Open(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := e.svc.ScanUnrecorded(t.Context(), id, d); err != nil {
		t.Fatalf("scan: %v", err)
	}
}

func (e *svcEnv) unrecordedKeys(t *testing.T, id uuid.UUID) ([]string, UnrecordedPage) {
	t.Helper()
	page, err := e.svc.UnrecordedFiles(t.Context(), id, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(page.Files))
	for _, f := range page.Files {
		keys = append(keys, f.Key)
	}
	return keys, page
}

func TestUnrecordedFilesDisk(t *testing.T) {
	e := newSvcEnv(t)
	ctx := t.Context()
	d, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "Local", Type: TypeDisk, Disk: &DiskInput{Path: "local/b"}})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(e.root, "local", "b")
	job, slug := e.layoutJob(t, d.ID)
	ts := func(h int) time.Time { return time.Date(2026, 10, 1, h, 0, 0, 0, time.UTC) }
	seg := func(h int) string { return slug + "/" + storage.TimestampSegment(ts(h)) }

	run1 := e.runRow(t, job, "success", ts(1))
	recorded := seg(1) + "/manifest.json"
	writeAged(t, dir, recorded, old)
	e.artifactRow(t, run1, job, d.ID, recorded)
	orphan := seg(1) + "/orphan.dump"
	writeAged(t, dir, orphan, old)
	orphan2 := seg(1) + "/orphan2.dump"
	writeAged(t, dir, orphan2, old)
	// A key recorded on another destination (soft-deleted, too) is never
	// reported (D-209: locations might once have been shared).
	other, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "Other", Type: TypeDisk, Disk: &DiskInput{Path: "other"}})
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := seg(1) + "/elsewhere.dump"
	writeAged(t, dir, elsewhere, old)
	e.artifactRow(t, run1, job, other.ID, elsewhere)
	if _, err := e.pool.Exec(ctx, `UPDATE destinations SET deleted_at = now() WHERE id = $1`, other.ID); err != nil {
		t.Fatal(err)
	}
	writeAged(t, dir, seg(2)+"/fresh.dump", time.Minute)
	e.runRow(t, job, "running", ts(3))
	writeAged(t, dir, seg(3)+"/inflight.dump", old)
	writeAged(t, dir, "foreign/file.txt", old)
	writeAged(t, dir, "unknown-job/"+storage.TimestampSegment(ts(4))+"/x.dump", old)
	emptyRun := filepath.Join(dir, filepath.FromSlash(seg(5)))
	if err := os.MkdirAll(emptyRun, 0o700); err != nil {
		t.Fatal(err)
	}
	ageFile(t, emptyRun, old)

	e.scan(t, d.ID)
	keys, page := e.unrecordedKeys(t, d.ID)
	// Only directories of known job slugs are listed: unknown-job/... and
	// foreign/... are not bekup's to judge.
	want := []string{orphan, orphan2}
	if fmt.Sprint(keys) != fmt.Sprint(want) {
		t.Fatalf("unrecorded = %v, want %v", keys, want)
	}
	if page.ScannedAt == nil || page.TotalCount != 2 || page.PrunedDirs != 1 || page.Truncated {
		t.Fatalf("page = %+v", page)
	}
	if _, err := os.Stat(emptyRun); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty run directory kept: %v", err)
	}
	// Paging by key.
	p1, err := e.svc.UnrecordedFiles(ctx, d.ID, 1, "")
	if err != nil || len(p1.Files) != 1 || p1.Next != orphan {
		t.Fatalf("page 1 = %+v, %v", p1, err)
	}
	if p2, err := e.svc.UnrecordedFiles(ctx, d.ID, 1, p1.Next); err != nil || len(p2.Files) != 1 || p2.Next != "" {
		t.Fatalf("page 2 = %+v, %v", p2, err)
	}
	if _, err := e.svc.UnrecordedFiles(ctx, d.ID, 1, "../x"); err == nil {
		t.Fatal("invalid cursor accepted")
	}
	if _, err := e.svc.UnrecordedFiles(ctx, uuid.New(), 1, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown destination: %v", err)
	}

	// Delete: re-checked, audited, the file and the row go.
	id := page.Files[0].ID
	code, err := e.svc.DeleteUnrecordedFile(ctx, e.actor, d.ID, id)
	if err != nil || code != storage.CodeOK {
		t.Fatalf("delete = %s, %v", code, err)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(orphan))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("file kept")
	}
	for _, k := range []string{recorded, elsewhere, orphan2} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(k))); err != nil {
			t.Fatalf("%s removed", k)
		}
	}
	if a := e.auditEntries(t, audit.ActionDestinationUnrecordedFileDeleted); len(a) != 1 || a[0]["object"] != orphan {
		t.Fatalf("audit = %v", a)
	}
	if _, err := e.svc.DeleteUnrecordedFile(ctx, e.actor, d.ID, id); !errors.Is(err, ErrUnrecordedNotFound) {
		t.Fatalf("second delete: %v", err)
	}

	// A key recorded after the scan is refused (and kept).
	late := seg(1) + "/late.dump"
	writeAged(t, dir, late, old)
	tooNew := seg(1) + "/touched.dump"
	writeAged(t, dir, tooNew, old)
	e.scan(t, d.ID)
	byKey := map[string]uuid.UUID{}
	_, page = e.unrecordedKeys(t, d.ID)
	for _, f := range page.Files {
		byKey[f.Key] = f.ID
	}
	e.artifactRow(t, run1, job, d.ID, late)
	if _, err := e.svc.DeleteUnrecordedFile(ctx, e.actor, d.ID, byKey[late]); !errors.Is(err, ErrUnrecordedNowRecorded) {
		t.Fatalf("recorded meanwhile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(late))); err != nil {
		t.Fatal("recorded file deleted")
	}
	// A file changed within 24 hours is refused.
	ageFile(t, filepath.Join(dir, filepath.FromSlash(tooNew)), time.Minute)
	if _, err := e.svc.DeleteUnrecordedFile(ctx, e.actor, d.ID, byKey[tooNew]); !errors.Is(err, ErrUnrecordedTooNew) {
		t.Fatalf("changed recently: %v", err)
	}
	// The next scan drops the recorded and the young file.
	e.scan(t, d.ID)
	if keys, _ := e.unrecordedKeys(t, d.ID); len(keys) != 1 || keys[0] != orphan2 {
		t.Fatalf("after rescan = %v", keys)
	}
	if n := len(e.auditEntries(t, audit.ActionDestinationUnrecordedFileDeleted)); n != 1 {
		t.Fatalf("refusals audited: %d", n)
	}

	// No-delete mode refuses.
	if _, err := e.svc.Update(ctx, e.actor, d.ID, UpdateInput{NoDelete: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	_, page = e.unrecordedKeys(t, d.ID)
	if _, err := e.svc.DeleteUnrecordedFile(ctx, e.actor, d.ID, page.Files[0].ID); !errors.Is(err, ErrNoDeleteMode) {
		t.Fatalf("no-delete: %v", err)
	}
	// In no-delete mode empty directories are not pruned either.
	if err := os.MkdirAll(emptyRun, 0o700); err != nil {
		t.Fatal(err)
	}
	ageFile(t, emptyRun, old)
	e.scan(t, d.ID)
	if _, err := os.Stat(emptyRun); err != nil {
		t.Fatalf("pruned in no-delete mode: %v", err)
	}
}

// TestLocationChangeRefused: D-209 (D). The disk path, the S3 endpoint,
// region, bucket and prefix and the SFTP host, port, user, base path and
// directory cannot change while present artifacts exist there or a run of
// one of its jobs is queued or running.
func TestLocationChangeRefused(t *testing.T) {
	e := newSvcEnv(t)
	ctx := t.Context()
	d, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "Local", Type: TypeDisk, Disk: &DiskInput{Path: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	job, slug := e.layoutJob(t, d.ID)
	run := e.runRow(t, job, "success", time.Now().Add(-old).Truncate(time.Second))
	art := e.artifactRow(t, run, job, d.ID, slug+"/2026-01-01T000000Z/manifest.json")
	// Seed a scan result: it is forgotten when the location changes.
	if _, err := e.pool.Exec(ctx, `INSERT INTO unrecorded_files (destination_id, object_key, size, first_seen_at, last_seen_at)
		VALUES ($1, 'j/2026-01-01T000000Z/x', 1, now(), now())`, d.ID); err != nil {
		t.Fatal(err)
	}

	_, err = e.svc.Update(ctx, e.actor, d.ID, UpdateInput{Disk: &DiskInput{Path: "b"}})
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Field != "disk.path" {
		t.Fatalf("path change with present artifacts: %v", err)
	}
	// A queued or running run of a job using it refuses a move too.
	active := e.runRow(t, job, "running", time.Now().Truncate(time.Second))
	// The same path (normalized) and other fields still change.
	if _, err := e.svc.Update(ctx, e.actor, d.ID, UpdateInput{Disk: &DiskInput{Path: "a/"}, Name: ptr("Renamed")}); err != nil {
		t.Fatalf("unchanged path: %v", err)
	}
	// Once nothing is present there, the path may change.
	if _, err := e.pool.Exec(ctx, `UPDATE artifacts SET state = 'deleted', deleted_at = now(), state_changed_at = now() WHERE id = $1`, art); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Update(ctx, e.actor, d.ID, UpdateInput{Disk: &DiskInput{Path: "b"}}); !errors.As(err, &fe) || fe.Field != "disk.path" ||
		!strings.Contains(fe.Message, "run") {
		t.Fatalf("path change during a run: %v", err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE runs SET status = 'failed', finished_at = now() WHERE id = $1`, active); err != nil {
		t.Fatal(err)
	}
	if got, err := e.svc.Update(ctx, e.actor, d.ID, UpdateInput{Disk: &DiskInput{Path: "b"}}); err != nil || got.Disk.Path != "b" {
		t.Fatalf("path change without present artifacts: %+v %v", got, err)
	}
	var n int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM unrecorded_files WHERE destination_id = $1`, d.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("unrecorded files kept after a move: %d %v", n, err)
	}

	// S3 bucket and prefix.
	in := validS3()
	in.SecretAccessKey = []byte("s3-secret-value-1")
	s3d, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "Offsite", Type: TypeS3, S3: in})
	if err != nil {
		t.Fatal(err)
	}
	job2, slug2 := e.layoutJob(t, s3d.ID)
	run2 := e.runRow(t, job2, "success", time.Now().Add(-old).Truncate(time.Second))
	e.artifactRow(t, run2, job2, s3d.ID, slug2+"/2026-01-01T000000Z/manifest.json")
	for field, mod := range map[string]func(*S3Input){
		"s3.endpoint": func(i *S3Input) { i.Endpoint = "https://127.0.0.2:9000" },
		"s3.region":   func(i *S3Input) { i.Region = "eu-west-1" },
		"s3.bucket":   func(i *S3Input) { i.Bucket = "elsewhere" },
		"s3.prefix":   func(i *S3Input) { i.Prefix = "moved" },
	} {
		up := validS3()
		up.SecretAccessKey = nil
		mod(up)
		_, err := e.svc.Update(ctx, e.actor, s3d.ID, UpdateInput{S3: up})
		if !errors.As(err, &fe) || fe.Field != field {
			t.Fatalf("%s change: %v", field, err)
		}
	}
	// A non-location setting still changes.
	up := validS3()
	up.SecretAccessKey = nil
	up.StorageClass = "STANDARD_IA"
	if _, err := e.svc.Update(ctx, e.actor, s3d.ID, UpdateInput{S3: up}); err != nil {
		t.Fatalf("storage class change: %v", err)
	}

	// SFTP host, port, user, base path and directory.
	sftpIn := func() *SFTPInput {
		return &SFTPInput{Host: "127.0.0.1", Port: 2222, User: "bekup", BasePath: "/upload", Dir: "nightly"}
	}
	sd, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "NAS", Type: TypeSFTP, SFTP: sftpIn()})
	if err != nil {
		t.Fatal(err)
	}
	job3, slug3 := e.layoutJob(t, sd.ID)
	run3 := e.runRow(t, job3, "success", time.Now().Add(-old).Truncate(time.Second))
	e.artifactRow(t, run3, job3, sd.ID, slug3+"/2026-01-01T000000Z/manifest.json")
	for field, mod := range map[string]func(*SFTPInput){
		"sftp.host":     func(i *SFTPInput) { i.Host = "127.0.0.2" },
		"sftp.port":     func(i *SFTPInput) { i.Port = 2200 },
		"sftp.user":     func(i *SFTPInput) { i.User = "other" },
		"sftp.basePath": func(i *SFTPInput) { i.BasePath = "/elsewhere" },
		"sftp.dir":      func(i *SFTPInput) { i.Dir = "weekly" },
	} {
		up := sftpIn()
		mod(up)
		_, err := e.svc.Update(ctx, e.actor, sd.ID, UpdateInput{SFTP: up})
		if !errors.As(err, &fe) || fe.Field != field {
			t.Fatalf("%s change: %v", field, err)
		}
	}
	// The same location spelled differently is no change.
	same := sftpIn()
	same.BasePath, same.Dir = "/upload/", "/nightly/"
	if _, err := e.svc.Update(ctx, e.actor, sd.ID, UpdateInput{SFTP: same, Name: ptr("NAS 2")}); err != nil {
		t.Fatalf("same sftp location: %v", err)
	}
}

// TestLocationOverlapRefused: D-209 (R-1). A destination may not be
// created on, or moved onto, the location of another one (equal, inside
// or containing it), including a soft-deleted one that still holds
// present backups; a soft-deleted one without backups frees its location.
func TestLocationOverlapRefused(t *testing.T) {
	e := newSvcEnv(t)
	ctx := t.Context()
	a, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "A", Type: TypeDisk, Disk: &DiskInput{Path: "backups"}})
	if err != nil {
		t.Fatal(err)
	}
	var fe *FieldError
	for _, p := range []string{"backups", "backups/inner"} {
		_, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "B " + p, Type: TypeDisk, Disk: &DiskInput{Path: p}})
		if !errors.As(err, &fe) || fe.Field != "disk.path" || !strings.Contains(fe.Message, `"A"`) || !strings.Contains(fe.Message, "bekup reindex --import") {
			t.Fatalf("create on %q: %v", p, err)
		}
	}
	b, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "B", Type: TypeDisk, Disk: &DiskInput{Path: "backups-2"}})
	if err != nil {
		t.Fatalf("sibling path: %v", err)
	}
	// Moving onto A's location (or above it) is refused as well.
	for _, p := range []string{"backups", "backups/x"} {
		if _, err := e.svc.Update(ctx, e.actor, b.ID, UpdateInput{Disk: &DiskInput{Path: p}}); !errors.As(err, &fe) || fe.Field != "disk.path" {
			t.Fatalf("move onto %q: %v", p, err)
		}
	}
	// A soft-deleted destination with present backups keeps its location;
	// without them it frees it.
	job, slug := e.layoutJob(t, a.ID)
	run := e.runRow(t, job, "success", time.Now().Add(-old).Truncate(time.Second))
	art := e.artifactRow(t, run, job, a.ID, slug+"/2026-01-01T000000Z/manifest.json")
	if _, err := e.pool.Exec(ctx, `UPDATE destinations SET deleted_at = now() WHERE id = $1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "C", Type: TypeDisk, Disk: &DiskInput{Path: "backups"}}); !errors.As(err, &fe) {
		t.Fatalf("create on a deleted destination with backups: %v", err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE artifacts SET state = 'deleted', deleted_at = now(), state_changed_at = now() WHERE id = $1`, art); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "C", Type: TypeDisk, Disk: &DiskInput{Path: "backups"}}); err != nil {
		t.Fatalf("create on a freed location: %v", err)
	}

	// S3: endpoint + bucket, then the prefix.
	s3 := func(prefix string) *S3Input {
		in := validS3()
		in.Prefix = prefix
		return in
	}
	if _, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "S3 a", Type: TypeS3, S3: s3("team/a")}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "S3 b", Type: TypeS3, S3: s3("team/b")}); err != nil {
		t.Fatalf("sibling prefix: %v", err)
	}
	for _, p := range []string{"team", "", "team/a/x"} {
		if _, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "S3 " + p, Type: TypeS3, S3: s3(p)}); !errors.As(err, &fe) || fe.Field != "s3.prefix" {
			t.Fatalf("s3 prefix %q: %v", p, err)
		}
	}
	other := s3("team/a")
	other.Bucket = "another-bucket"
	if _, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "S3 other bucket", Type: TypeS3, S3: other}); err != nil {
		t.Fatalf("other bucket: %v", err)
	}

	// SFTP: host + port, then base path + directory.
	sftpIn := func(base, dir string) *SFTPInput {
		return &SFTPInput{Host: "127.0.0.1", Port: 2222, User: "bekup", BasePath: base, Dir: dir}
	}
	if _, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "NAS a", Type: TypeSFTP, SFTP: sftpIn("/upload", "a")}); err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string]*SFTPInput{
		"same":          sftpIn("/upload", "a"),
		"base contains": sftpIn("/upload", ""),
		"dir inside":    sftpIn("/upload/a", "b"),
	} {
		if _, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "NAS " + name, Type: TypeSFTP, SFTP: in}); !errors.As(err, &fe) || fe.Field != "sftp.basePath" {
			t.Fatalf("sftp %s: %v", name, err)
		}
	}
	if _, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "NAS b", Type: TypeSFTP, SFTP: sftpIn("/upload", "b")}); err != nil {
		t.Fatalf("sftp sibling: %v", err)
	}
}

// TestUnrecordedFilesSFTP runs the scan with pruning on a real (chrooted
// OpenSSH) SFTP server.
func TestUnrecordedFilesSFTP(t *testing.T) {
	srv, err := sftptest.Start(context.Background())
	if err != nil {
		dockertest.Skipf(t, "start atmoz/sftp: %v", err)
	}
	t.Cleanup(func() {
		if err := srv.Terminate(context.Background()); err != nil {
			t.Logf("terminate atmoz/sftp: %v", err)
		}
	})
	e := newSvcEnv(t)
	ctx := t.Context()
	id := uuid.New()
	if _, err := e.pool.Exec(ctx, `INSERT INTO destinations (id, name, type, config) VALUES ($1, 'sftp', 'sftp', '{}')`, id); err != nil {
		t.Fatal(err)
	}
	job, slug := e.layoutJob(t, id)
	g, err := netguard.New(netguard.Options{LoopbackAllowable: true, AllowCIDRs: []string{"127.0.0.1/32"}})
	if err != nil {
		t.Fatal(err)
	}
	d, err := sftp.Open(sftp.Config{
		Host: srv.Host, Port: srv.Port, User: sftptest.User,
		Auth:       sftp.Auth{PrivateKey: crypto.NewSecret(bytes.Clone(srv.ClientKeyPEM))},
		HostKeyPin: sftp.HostKeyPin{AuthorizedKey: srv.HostKeyAuthorized()},
		BasePath:   sftptest.BasePath, Dir: "scan", Guard: g,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	}()
	run := e.runRow(t, job, "success", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	e.artifactRow(t, run, job, id, slug+"/2026-01-01T000000Z/manifest.json")
	fs := sftptest.FSUpload + "/scan"
	script := "set -e; mkdir -p " + fs + "/" + slug + "/2026-01-01T000000Z " + fs + "/" + slug + "/2026-01-02T000000Z" +
		" && echo m > " + fs + "/" + slug + "/2026-01-01T000000Z/manifest.json" +
		" && echo o > " + fs + "/" + slug + "/2026-01-01T000000Z/orphan.dump" +
		" && touch -d '2020-01-01' " + fs + "/" + slug + "/2026-01-01T000000Z/manifest.json " + fs + "/" + slug + "/2026-01-01T000000Z/orphan.dump " +
		fs + "/" + slug + "/2026-01-02T000000Z && chown -R 1001:1001 " + fs
	code, r, err := srv.Container.Exec(ctx, []string{"sh", "-c", script})
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil || code != 0 {
		t.Fatalf("seed: exit %d (%v): %s", code, err, out)
	}
	if err := e.svc.ScanUnrecorded(ctx, id, d); err != nil {
		t.Fatalf("scan: %v", err)
	}
	keys, page := e.unrecordedKeys(t, id)
	if len(keys) != 1 || keys[0] != slug+"/2026-01-01T000000Z/orphan.dump" || page.PrunedDirs != 1 {
		t.Fatalf("sftp scan: %v %+v", keys, page)
	}
	code, r, err = srv.Container.Exec(ctx, []string{"sh", "-c", "ls " + fs + "/" + slug})
	if err != nil {
		t.Fatal(err)
	}
	out, err = io.ReadAll(r)
	if err != nil || code != 0 || bytes.Contains(out, []byte("2026-01-02T000000Z")) || !bytes.Contains(out, []byte("2026-01-01T000000Z")) {
		t.Fatalf("pruning on SFTP: exit %d: %s", code, out)
	}
}
