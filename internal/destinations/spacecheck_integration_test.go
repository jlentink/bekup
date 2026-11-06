//go:build integration

package destinations

import (
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/storage"
	"github.com/jlentink/bekup/internal/storage/webdav"
)

func (e *svcEnv) lowSpaceEvents(t *testing.T, id uuid.UUID) []string {
	t.Helper()
	rows, err := e.pool.Query(t.Context(), `SELECT dedup_key FROM notification_events
		WHERE type = 'destination.low_space' AND destination_id = $1 ORDER BY created_at, id`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return keys
}

func (e *svcEnv) record(t *testing.T, id uuid.UUID) *record {
	t.Helper()
	r, err := e.svc.load(t.Context(), e.q, id, false)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestSpaceCheckWorkerStores runs the scheduled check against a real disk
// destination in a temp dir and an S3 destination: the answers are stored
// without bumping updated_at or writing audit entries, S3 is recorded on
// every run without a network call, a settings change hides the stored
// answer and puts the destination first in the target order, and the
// conditional record refuses changed, superseded and deleted rows.
func TestSpaceCheckWorkerStores(t *testing.T) {
	e := newSvcEnv(t)
	ctx := t.Context()
	disk, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "Local", Type: TypeDisk, Disk: &DiskInput{Path: "space"}})
	if err != nil {
		t.Fatal(err)
	}
	in := validS3()
	in.SecretAccessKey = []byte("first-secret-value")
	s3, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "Offsite", Type: TypeS3, S3: in})
	if err != nil {
		t.Fatal(err)
	}
	audited := len(e.auditEntries(t, ""))
	w := &SpaceCheckWorker{
		checker: e.svc, concurrency: SpaceCheckConcurrency, timeout: e.svc.spaceCheckTimeout(), budget: SpaceCheckBudget,
		logger: slog.New(slog.DiscardHandler),
	}

	tot, err := w.run(ctx)
	if err != nil || tot.Destinations != 2 || tot.Available != 1 || tot.Unsupported != 1 || tot.Failed != 0 || tot.Skipped != 0 {
		t.Fatalf("first run: %+v, %v", tot, err)
	}
	got := mustGet(t, e, disk.ID)
	sp := got.Space
	if !sp.Checked() || sp.Status != SpaceAvailable || !sp.Space.FreeKnown || !sp.Space.TotalKnown || sp.Code != "" || sp.CheckedAt.IsZero() {
		t.Fatalf("disk space: %+v", sp)
	}
	if !got.UpdatedAt.Equal(disk.UpdatedAt) {
		t.Fatalf("storing the space check bumped updated_at: %v -> %v", disk.UpdatedAt, got.UpdatedAt)
	}
	if n := len(e.auditEntries(t, "")); n != audited {
		t.Fatalf("the space check wrote audit entries: %d -> %d", audited, n)
	}
	s3got := mustGet(t, e, s3.ID)
	if s3got.Space.Status != SpaceUnsupported || s3got.Space.Code != SpaceCodeUnsupported || s3got.Space.Reason != spaceReasonS3 ||
		!s3got.UpdatedAt.Equal(s3.UpdatedAt) {
		t.Fatalf("s3 space: %+v", s3got.Space)
	}

	// The second run asks the disk again and refreshes S3.
	tot, err = w.run(ctx)
	if err != nil || tot.Available != 1 || tot.Skipped != 0 || tot.Unsupported != 1 || tot.NotReached != 0 {
		t.Fatalf("second run: %+v, %v", tot, err)
	}
	if again := mustGet(t, e, disk.ID).Space; !again.CheckedAt.After(sp.CheckedAt) {
		t.Fatalf("checked_at did not advance: %v -> %v", sp.CheckedAt, again.CheckedAt)
	}
	s3again := mustGet(t, e, s3.ID).Space
	if !s3again.CheckedAt.After(s3got.Space.CheckedAt) {
		t.Fatal("s3 was not refreshed")
	}
	// The target order: the oldest answer first.
	if ids, err := e.svc.SpaceCheckTargets(ctx); err != nil || len(ids) != 2 ||
		(ids[0] == s3.ID) != s3again.CheckedAt.Before(mustGet(t, e, disk.ID).Space.CheckedAt) {
		t.Fatalf("targets %v, %v", ids, err)
	}
	all, err := StoredSpaces(ctx, e.q)
	if err != nil || len(all) != 2 || all[disk.ID].Status != SpaceAvailable || all[s3.ID].Status != SpaceUnsupported {
		t.Fatalf("stored spaces: %+v, %v", all, err)
	}

	// An older answer never replaces a newer one; a record loaded before a
	// settings change is refused.
	before := e.record(t, disk.ID)
	old := spaceResult{status: SpaceUnavailable, code: string(storage.CodeNetwork), checkedAt: sp.CheckedAt.Add(-time.Hour)}
	if out, err := e.svc.recordSpace(ctx, before, old); err != nil || out.Stored {
		t.Fatalf("older answer: %+v, %v", out, err)
	}
	if _, err := e.svc.Update(ctx, e.actor, disk.ID, UpdateInput{Disk: &DiskInput{Path: "moved"}}); err != nil {
		t.Fatal(err)
	}
	moved := mustGet(t, e, disk.ID).Space
	if moved.Checked() || !moved.SettingsChanged {
		t.Fatalf("after a settings change: %+v", moved)
	}
	if all, err := StoredSpaces(ctx, e.q); err != nil || all[disk.ID].Checked() || !all[disk.ID].SettingsChanged {
		t.Fatalf("stored spaces after a settings change: %+v, %v", all[disk.ID], err)
	}
	// Checked under older settings: first in the target order, and so is
	// a destination never checked.
	fresh, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "Another", Type: TypeDisk, Disk: &DiskInput{Path: "another"}})
	if err != nil {
		t.Fatal(err)
	}
	if ids, err := e.svc.SpaceCheckTargets(ctx); err != nil || len(ids) != 3 || ids[2] != s3.ID ||
		(ids[0] != disk.ID && ids[0] != fresh.ID) || (ids[1] != disk.ID && ids[1] != fresh.ID) {
		t.Fatalf("targets after a settings change: %v, %v (disk %s, new %s)", ids, err, disk.ID, fresh.ID)
	}
	if err := e.svc.Delete(ctx, e.actor, fresh.ID); err != nil {
		t.Fatal(err)
	}
	stale := spaceResult{status: SpaceAvailable, space: storage.Space{Free: 1, FreeKnown: true}, checkedAt: time.Now()}
	if out, err := e.svc.recordSpace(ctx, before, stale); err != nil || out.Stored {
		t.Fatalf("record of the old settings: %+v, %v", out, err)
	}
	if out, err := e.svc.CheckSpace(ctx, disk.ID); err != nil || !out.Stored || out.Status != SpaceAvailable {
		t.Fatalf("check after the change: %+v, %v", out, err)
	}
	if !mustGet(t, e, disk.ID).Space.Checked() {
		t.Fatal("the new settings were not checked")
	}

	// A deleted destination is neither checked nor recorded.
	r := e.record(t, s3.ID)
	if err := e.svc.Delete(ctx, e.actor, s3.ID); err != nil {
		t.Fatal(err)
	}
	if out, err := e.svc.recordSpace(ctx, r, unsupportedSpaceAt(time.Now())); err != nil || out.Stored {
		t.Fatalf("record of a deleted destination: %+v, %v", out, err)
	}
	tot, err = w.run(ctx)
	if err != nil || tot.Destinations != 1 {
		t.Fatalf("run after the delete: %+v, %v", tot, err)
	}
}

func unsupportedSpaceAt(at time.Time) spaceResult {
	res := unsupportedSpace(TypeS3)
	res.checkedAt = at
	return res
}

// TestSpaceLowEpisodeStored: the low-space episode lives in the row; the
// first low answer emits destination.low_space, the next ones within a day
// nothing, a day later one reminder; a failed check keeps the episode; it
// ends only after three hours of answers that are not low (a low answer in
// between resets the streak), the next low answer opens a new one, which
// notifies only on another UTC day. Times are the database clock. None of
// it bumps updated_at.
func TestSpaceLowEpisodeStored(t *testing.T) {
	e := newSvcEnv(t)
	ctx := t.Context()
	d, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "Local", Type: TypeDisk, Disk: &DiskInput{Path: "low"}})
	if err != nil {
		t.Fatal(err)
	}
	// One hour into the UTC day four days ago: the steps stay in the past
	// and their UTC days are known.
	day0 := time.Now().UTC().Truncate(24 * time.Hour).Add(-96 * time.Hour)
	t0 := day0.Add(time.Hour)
	low := storage.Space{Total: 100 << 30, Free: 1 << 30, TotalKnown: true, FreeKnown: true}
	fine := storage.Space{Total: 100 << 30, Free: 50 << 30, TotalKnown: true, FreeKnown: true}
	lowRes := spaceResult{status: SpaceAvailable, space: low}
	fineRes := spaceResult{status: SpaceAvailable, space: fine}
	const none = -1
	steps := []struct {
		name     string
		after    time.Duration
		res      spaceResult
		notified bool
		// since is the step that opened the current episode (none: no
		// episode); recovering marks an open recovery streak.
		since      int
		recovering bool
	}{
		{"opens", 0, lowRes, true, 0, false},
		{"same day", time.Hour, lowRes, false, 0, false},
		{"reminder", 25 * time.Hour, lowRes, true, 0, false},
		{"reminder once", 26 * time.Hour, lowRes, false, 0, false},
		{"failed check keeps", 27 * time.Hour, spaceResult{status: SpaceUnavailable, code: string(storage.CodeTimeout)}, false, 0, false},
		{"not low: the streak starts", 28 * time.Hour, fineRes, false, 0, true},
		{"not low for two hours", 30 * time.Hour, fineRes, false, 0, true},
		{"low again resets the streak", 30*time.Hour + 30*time.Minute, lowRes, false, 0, false},
		{"not low again", 31 * time.Hour, fineRes, false, 0, true},
		{"recovered after three hours", 34*time.Hour + time.Minute, fineRes, false, none, false},
		{"new episode on another day", 35 * time.Hour, lowRes, true, 10, false},
		{"not low", 36 * time.Hour, fineRes, false, 10, true},
		{"recovered", 39*time.Hour + time.Minute, fineRes, false, none, false},
		{"third episode, same day: no notice", 40 * time.Hour, lowRes, false, 13, false},
	}
	at := make([]time.Time, len(steps))
	for i, st := range steps {
		res := st.res
		res.checkedAt = t0.Add(st.after)
		out, err := e.svc.recordSpace(ctx, e.record(t, d.ID), res)
		if err != nil || !out.Stored || out.Notified != st.notified {
			t.Fatalf("%s: %+v, %v", st.name, out, err)
		}
		at[i] = out.CheckedAt
		// The database clock minus the answer's age: close to the
		// simulated time.
		if diff := at[i].Sub(res.checkedAt); diff < -time.Minute || diff > time.Minute {
			t.Fatalf("%s: stored check time %v, simulated %v", st.name, at[i], res.checkedAt)
		}
		got := mustGet(t, e, d.ID)
		switch {
		case st.since == none && got.Space.LowSince != nil,
			st.since != none && (got.Space.LowSince == nil || !got.Space.LowSince.Equal(at[st.since])):
			t.Fatalf("%s: low since %v", st.name, got.Space.LowSince)
		}
		var okSince *time.Time
		if err := e.pool.QueryRow(ctx, `SELECT space_ok_since FROM destinations WHERE id = $1`, d.ID).Scan(&okSince); err != nil {
			t.Fatal(err)
		}
		if (okSince != nil) != st.recovering {
			t.Fatalf("%s: ok since %v", st.name, okSince)
		}
		if !got.UpdatedAt.Equal(d.UpdatedAt) {
			t.Fatalf("%s: updated_at bumped", st.name)
		}
		if res.status == SpaceUnavailable && (got.Space.Low != LowNone || got.Space.Space.Known()) {
			t.Fatalf("%s: values kept on a failed check: %+v", st.name, got.Space)
		}
	}
	keys := e.lowSpaceEvents(t, d.ID)
	first, second := LowSpaceDedupKey(d.ID, at[0]), LowSpaceDedupKey(d.ID, at[10])
	if first == second || len(keys) != 3 || keys[0] != first || keys[1] != first+":r1" || keys[2] != second {
		t.Fatalf("events %q", keys)
	}
	var detail map[string]any
	if err := e.pool.QueryRow(ctx, `SELECT detail FROM notification_events WHERE dedup_key = $1`, first).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if detail["cause"] != "fraction" || detail["free_bytes"] != float64(1<<30) || detail["total_bytes"] != float64(100<<30) {
		t.Fatalf("detail %+v", detail)
	}
	// The constraints: values only on available answers, the code tied to
	// the status, an unchecked row entirely empty, a recovery streak only
	// inside an episode.
	for name, stmt := range map[string]string{
		"sizes on an unavailable answer": `UPDATE destinations SET space_status = 'unavailable', space_code = 'network' WHERE id = $1`,
		"a code on an available answer":  `UPDATE destinations SET space_code = 'network' WHERE id = $1`,
		"unsupported with another code": `UPDATE destinations SET space_status = 'unsupported', space_code = 'network', space_total_bytes = NULL,
			space_free_bytes = NULL, space_used_bytes = NULL, space_newest_set_bytes = NULL, space_low_cause = NULL WHERE id = $1`,
		"unavailable without a code": `UPDATE destinations SET space_status = 'unavailable', space_code = NULL, space_total_bytes = NULL,
			space_free_bytes = NULL, space_used_bytes = NULL, space_newest_set_bytes = NULL, space_low_cause = NULL WHERE id = $1`,
		"values on an unchecked row": `UPDATE destinations SET space_checked_at = NULL, space_status = NULL, space_settings_at = NULL,
			space_low_since = NULL, space_low_cause = NULL WHERE id = $1`,
		"a streak without an episode": `UPDATE destinations SET space_low_since = NULL, space_low_cause = NULL, space_ok_since = now() WHERE id = $1`,
		"a streak while low":          `UPDATE destinations SET space_ok_since = now() WHERE id = $1`,
	} {
		if _, err := e.pool.Exec(ctx, stmt, d.ID); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := e.q.RecordDestinationSpace(ctx, sqlc.RecordDestinationSpaceParams{
		ID: d.ID, SettingsAt: d.UpdatedAt, CheckedAt: time.Now(), Status: "bogus",
	}); err == nil {
		t.Fatal("an unknown status was stored")
	}
}

// TestSpaceCheckDatabaseClock: the stored check time comes from the
// database clock, not from the (here six hours fast) process clock.
func TestSpaceCheckDatabaseClock(t *testing.T) {
	e := newSvcEnv(t, func(o *Options) { o.Now = func() time.Time { return time.Now().Add(6 * time.Hour) } })
	ctx := t.Context()
	d, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "Local", Type: TypeDisk, Disk: &DiskInput{Path: "clock"}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := e.svc.CheckSpace(ctx, d.ID)
	if err != nil || !out.Stored {
		t.Fatalf("check: %+v, %v", out, err)
	}
	var dbNow time.Time
	if err := e.pool.QueryRow(ctx, `SELECT now()`).Scan(&dbNow); err != nil {
		t.Fatal(err)
	}
	if diff := dbNow.Sub(out.CheckedAt); diff < -time.Minute || diff > time.Minute {
		t.Fatalf("stored %v, database now %v", out.CheckedAt, dbNow)
	}
	if got := mustGet(t, e, d.ID).Space; !got.CheckedAt.Equal(out.CheckedAt) {
		t.Fatalf("stored %v, returned %v", got.CheckedAt, out.CheckedAt)
	}
}

// TestSpaceCheckNotReadyStored: a destination that is not ready is stored
// as unavailable/not_ready by the scheduled check (no backend call), so its
// old answer does not linger as a stale one.
func TestSpaceCheckNotReadyStored(t *testing.T) {
	e := newSvcEnv(t)
	ctx := t.Context()
	srv, ignore := davServer(t)
	in := WebDAVInput{
		URL: srv.URL + "/dav/backups", Username: "bekup", Path: "nr", Password: []byte("dav-password-9a8b"),
		CABundlePEM: string(webdav.CertificateOf(srv.Certificate()).PEM()),
	}
	d, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "dav", Type: TypeWebDAV, WebDAV: &in})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := e.svc.CheckSpace(ctx, d.ID); err != nil || !out.Stored || out.Status == SpaceUnavailable {
		t.Fatalf("ready: %+v, %v", out, err)
	}
	// The server starts replacing files: a test makes it not ready.
	ignore.Store(true)
	if res, err := e.svc.Test(ctx, e.actor, d.ID); err != nil || res.OK {
		t.Fatalf("test: %+v, %v", res, err)
	}
	if got := mustGet(t, e, d.ID); got.Ready {
		t.Fatal("still ready")
	}
	out, err := e.svc.CheckSpace(ctx, d.ID)
	if err != nil || !out.Stored || out.Status != SpaceUnavailable || out.Code != SpaceCodeNotReady {
		t.Fatalf("not ready: %+v, %v", out, err)
	}
	got := mustGet(t, e, d.ID).Space
	if got.Status != SpaceUnavailable || got.Code != SpaceCodeNotReady || got.Stale(time.Now()) ||
		got.Reason != "The destination was not ready when it was checked." {
		t.Fatalf("stored: %+v", got)
	}
}

// TestSpaceCheckPinMismatchAuditedOnce: the scheduled check audits a
// certificate-pin violation once per settings: the same violation found
// again is stored (refreshed, still fail closed) but not audited again; a
// settings change audits again.
func TestSpaceCheckPinMismatchAuditedOnce(t *testing.T) {
	e := newSvcEnv(t)
	ctx := t.Context()
	srv, _ := davServer(t)
	caPEM := string(webdav.CertificateOf(srv.Certificate()).PEM())
	in := WebDAVInput{URL: srv.URL + "/dav/backups", Username: "bekup", Path: "pin", Password: []byte("dav-password-9a8b"), CABundlePEM: caPEM}
	d, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "dav", Type: TypeWebDAV, WebDAV: &in})
	if err != nil {
		t.Fatal(err)
	}
	// A pin of another certificate, which the server's does not match.
	otherPEM := testCA(t)
	block, _ := pem.Decode([]byte(otherPEM))
	other, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	spki := hex.EncodeToString(webdav.CertificateOf(other).SPKISHA256)
	pin := func() {
		t.Helper()
		if _, err := e.pool.Exec(ctx, `UPDATE destinations SET tls_pin_sha256 = $2, tls_pin_certificate = $3,
			tls_pin_set_at = clock_timestamp(), tls_pin_set_by = NULL WHERE id = $1`, d.ID, spki, otherPEM); err != nil {
			t.Fatal(err)
		}
	}
	pin()
	audited := func() int { return len(e.auditEntries(t, audit.ActionDestinationTLSPinMismatch)) }
	var prev time.Time
	for i, want := range []int{1, 1, 1} {
		out, err := e.svc.CheckSpace(ctx, d.ID)
		if err != nil || !out.Stored || out.Status != SpaceUnavailable || out.Code != string(storage.CodeTLSPinMismatch) {
			t.Fatalf("check %d: %+v, %v", i, out, err)
		}
		if !out.CheckedAt.After(prev) {
			t.Fatalf("check %d: not refreshed", i)
		}
		prev = out.CheckedAt
		if n := audited(); n != want {
			t.Fatalf("check %d: audited %d times, want %d", i, n, want)
		}
	}
	// A settings change (here the pin again): audited again.
	pin()
	if out, err := e.svc.CheckSpace(ctx, d.ID); err != nil || out.Code != string(storage.CodeTLSPinMismatch) || audited() != 2 {
		t.Fatalf("after a settings change: %+v, %v, audited %d", out, err, audited())
	}
}
