//go:build integration

package runstate

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

type recordingObserver struct {
	mu   sync.Mutex
	runs []sqlc.Run
}

func (r *recordingObserver) RunFinished(_ context.Context, run sqlc.Run) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs = append(r.runs, run)
}

func (r *recordingObserver) all() []sqlc.Run {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]sqlc.Run(nil), r.runs...)
}

// TestFinishObserver: committed terminal runs reach the observer (Finish
// and the interrupted attempt that Begin fails), a rejected Finish does
// not.
func TestFinishObserver(t *testing.T) {
	e := newStEnv(t)
	ctx := t.Context()
	obs := &recordingObserver{}
	e.store.WithObserver(obs)

	enq := e.enqueue(false)
	bp := BeginParams{RiverJobID: enq.JobID, Attempt: 1, JobID: e.job, Trigger: sqlc.RunTriggerSchedule}
	run, err := e.store.Begin(ctx, bp)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := e.store.Start(ctx, run.Ref, 1, now, now); err != nil {
		t.Fatal(err)
	}
	if len(obs.all()) != 0 {
		t.Fatal("observer called before a terminal status")
	}
	// A retry of the same River job finds the run still running: Begin
	// fails it as interrupted and that is observed.
	bp.Attempt = 2
	run2, err := e.store.Begin(ctx, bp)
	if err != nil {
		t.Fatal(err)
	}
	got := obs.all()
	if len(got) != 1 || got[0].ID != run.RunID || got[0].Status != StatusFailed || got[0].Trigger != sqlc.RunTriggerSchedule {
		t.Fatalf("after Begin: %+v", got)
	}
	if err := e.store.Finish(ctx, run2.Ref, Result{Status: StatusSkipped, Failure: &Failure{Code: "job_disabled", Message: "x"}}); err != nil {
		t.Fatal(err)
	}
	got = obs.all()
	if len(got) != 2 || got[1].ID != run2.RunID || got[1].Status != StatusSkipped || got[1].FinishedAt == nil {
		t.Fatalf("after Finish: %+v", got)
	}
	// Finishing again is rejected and not observed.
	if err := e.store.Finish(ctx, run2.Ref, Result{Status: StatusSuccess}); !errors.Is(err, ErrNotActive) {
		t.Fatalf("second finish: %v", err)
	}
	if len(obs.all()) != 2 {
		t.Fatal("a rejected finish was observed")
	}
}
