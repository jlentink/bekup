//go:build integration

package restore_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/jobkeys"
	"github.com/jlentink/bekup/internal/restore"
)

type recordingMetrics struct {
	mu       sync.Mutex
	restores []string
}

func (r *recordingMetrics) RestoreFinished(mode sqlc.RestoreMode, status restore.Status) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.restores = append(r.restores, string(status)+"/"+string(mode))
}

func (r *recordingMetrics) TestRestoreFinished(sqlc.TestRestoreStatus) {}

func (r *recordingMetrics) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.restores...)
}

// TestRestoreMetrics: a committed terminal status is counted once with
// the restore's mode; finishing again is a no-op and not counted.
func TestRestoreMetrics(t *testing.T) {
	rec := &recordingMetrics{}
	e := newEnv(t, func(o *restore.Options) { o.Metrics = rec })
	j := e.job(jobkeys.ModePassphrase, e.diskID)
	s := e.seed(j, e.diskID, e.disk, randomDump(1000), time.Now())
	id := e.mustCreate(j, s.artifact, "")
	if ok, err := e.svc.Finish(t.Context(), id, restore.StatusFailed, errors.New("x")); err != nil || !ok {
		t.Fatalf("finish %v %v", ok, err)
	}
	if ok, err := e.svc.Finish(t.Context(), id, restore.StatusSucceeded, nil); err != nil || ok {
		t.Fatalf("second finish %v %v", ok, err)
	}
	got := rec.all()
	if len(got) != 1 || (got[0] != "failed/new_db" && got[0] != "failed/overwrite") {
		t.Fatalf("recorded %v", got)
	}
}
