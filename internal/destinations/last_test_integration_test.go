//go:build integration

package destinations

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

// TestLastTestPersisted covers the stored test outcome (D-187): stored
// without bumping updated_at or writing audit entries, the normalized
// class of the first failed step, and stale after a settings change.
func TestLastTestPersisted(t *testing.T) {
	e := newSvcEnv(t)
	ctx := t.Context()
	d, err := e.svc.Create(ctx, e.actor, CreateInput{Name: "Local", Type: TypeDisk, Disk: &DiskInput{Path: "ok"}})
	if err != nil {
		t.Fatal(err)
	}
	if d.LastTest != nil {
		t.Fatalf("new destination has a last test: %+v", d.LastTest)
	}
	audited := len(e.auditEntries(t, ""))
	if audited == 0 {
		t.Fatal("no audit entries listed")
	}
	if res, err := e.svc.Test(ctx, e.actor, d.ID); err != nil || !res.OK {
		t.Fatalf("test: %v %+v", err, res)
	}
	got, err := e.svc.Get(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lt := got.LastTest; lt == nil || !lt.OK || lt.Code != "" || lt.Stale {
		t.Fatalf("last test after success: %+v", got.LastTest)
	}
	if !got.UpdatedAt.Equal(d.UpdatedAt) {
		t.Fatalf("storing the result bumped updated_at: %v -> %v", d.UpdatedAt, got.UpdatedAt)
	}
	if n := len(e.auditEntries(t, "")); n != audited {
		t.Fatalf("storing a test result wrote audit entries: %d -> %d", audited, n)
	}

	// A read-only directory: the write step fails with a normalized class.
	ro := filepath.Join(e.root, "ro")
	if err := os.MkdirAll(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(ro, 0o700); err != nil { //nolint:gosec // G302: test cleanup of a temp dir
			t.Error(err)
		}
	})
	if _, err := e.svc.Update(ctx, e.actor, d.ID, UpdateInput{Disk: &DiskInput{Path: "ro"}}); err != nil {
		t.Fatal(err)
	}
	got = mustGet(t, e, d.ID)
	if got.LastTest == nil || !got.LastTest.Stale {
		t.Fatalf("not stale after an update: %+v", got.LastTest)
	}
	res, err := e.svc.Test(ctx, e.actor, d.ID)
	if err != nil || res.OK {
		t.Fatalf("failing test: %v %+v", err, res)
	}
	got = mustGet(t, e, d.ID)
	if lt := got.LastTest; lt == nil || lt.OK || lt.Code == "" || lt.Code == "ok" || lt.Stale {
		t.Fatalf("last test after failure: %+v", got.LastTest)
	}
	for _, st := range res.Steps {
		if !st.OK {
			if string(st.Code) != got.LastTest.Code {
				t.Fatalf("stored code %q, first failed step %q", got.LastTest.Code, st.Code)
			}
			break
		}
	}
}

func mustGet(t *testing.T, e *svcEnv, id uuid.UUID) Destination {
	t.Helper()
	v, err := e.svc.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
