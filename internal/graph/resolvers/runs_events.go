package resolvers

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/graph/streams"
	"github.com/jlentink/bekup/internal/pubsub"
	"github.com/jlentink/bekup/internal/runstate"
)

// Live run subscriptions (task 3.7). Run events on bekup_run_events carry
// "<kind>:<run id>:<job id>" only; they are hints. Every event is checked
// by streams.Deliver (session re-check) and authorized again with the job
// loaded from the run's row (never the payload's job id), and what is
// sent is re-loaded from the database at delivery time, so a backup owner
// who loses a job stops receiving its runs at once.

const (
	// runEventBuffer is the bus buffer of one stream; on overflow the
	// stream resyncs from the database instead of blocking publishers.
	runEventBuffer = 256
	// logBatchLines caps the lines of one runLogAppended event.
	logBatchLines = 200
	// resyncMax caps the runs re-sent after possibly lost events.
	resyncMax = 100
	// resyncMargin covers clock skew between serve and the database when
	// looking for runs changed since the last resync.
	resyncMargin = 30 * time.Second
)

var errRunEventsNotConfigured = errors.New("resolvers: run events not configured")

// runEvent is one run to deliver; check fills view with the row it
// authorized, so conv sends exactly what was authorized.
type runEvent struct {
	runID uuid.UUID
	view  *runstate.RunView
}

func (r *Resolver) runEventSources() (*runstate.Reader, *pubsub.Bus, error) {
	if r.Runs == nil || r.Bus == nil {
		return nil, nil, errRunEventsNotConfigured
	}
	return r.Runs, r.Bus, nil
}

// runUpdated streams changes of the runs the caller may view, optionally
// of one job and/or one run.
func (r *Resolver) runUpdated(ctx context.Context, jobID, runID *uuid.UUID) (<-chan *model.Run, error) {
	if streams.FromContext(ctx) == nil {
		return nil, errNotStream
	}
	rd, bus, err := r.runEventSources()
	if err != nil {
		return nil, err
	}
	if runID != nil {
		v, err := rd.Run(ctx, *runID)
		switch {
		case errors.Is(err, runstate.ErrRunNotFound):
			if err := r.authorizeRun(ctx, authz.OpRunView, uuid.Nil, false); err != nil {
				return nil, err
			}
			return nil, apierror.New(apierror.NotFound, "run not found")
		case err != nil:
			return nil, err
		}
		if err := r.authorizeRun(ctx, authz.OpRunView, v.JobID, true); err != nil {
			return nil, err
		}
		if jobID != nil && *jobID != v.JobID {
			return nil, apierror.Invalid("jobId", "the run belongs to another job")
		}
	}
	if jobID != nil {
		if err := r.Authz.Authorize(ctx, authz.OpRunView, authz.Job{ID: *jobID}); err != nil {
			return nil, err
		}
	}
	sub := bus.Subscribe(pubsub.RunEvents.Name, runEventBuffer)
	src := make(chan *runEvent)
	go r.pumpRunEvents(ctx, sub, src, jobID, runID)
	check := streams.Authorize(r.Authz, authz.OpRunView, func(ctx context.Context, ev *runEvent) ([]authz.Resource, error) {
		v, err := rd.Run(ctx, ev.runID)
		if err != nil {
			return nil, err
		}
		ev.view = &v
		return []authz.Resource{authz.Job{ID: v.JobID}}, nil
	})
	return streams.Deliver(ctx, src, check, func(ctx context.Context, ev *runEvent) (*model.Run, bool) {
		if ev.view == nil || (jobID != nil && ev.view.JobID != *jobID) {
			return nil, false
		}
		return runModel(ev.view, r.showActor(ctx)), true
	}), nil
}

// runIDSource lists the runs changed since a time (resync after possibly
// lost events; *runstate.Reader).
type runIDSource interface {
	RunIDsUpdatedSince(ctx context.Context, since time.Time, jobID, runID *uuid.UUID, limit int) ([]uuid.UUID, error)
}

// pumpRunEvents turns the bus messages matching the filter into run
// events until ctx ends. Events are coalesced per stream (task 3.11): a run
// that is already pending is not queued again (delivery re-loads its
// current state anyway), at most resyncMax runs are pending (beyond that
// the stream falls back to a resync), and runs are delivered at most every
// runEventMinGap, so an unfiltered stream costs at most one load and
// authorization per distinct run and gap, however busy the instance. After
// possibly lost messages (bus overflow, listener reconnect, a full pending
// set) it re-sends the runs changed since the last resync.
func (r *Resolver) pumpRunEvents(ctx context.Context, sub *pubsub.Subscription, src chan<- *runEvent, jobID, runID *uuid.UUID) {
	pumpCoalescedRunEvents(ctx, sub, src, r.Runs, jobID, runID, runEventMinGap)
}

// runEventMinGap is the minimum gap between two deliveries of one stream
// (at most 20 runs per second).
const runEventMinGap = 50 * time.Millisecond

func pumpCoalescedRunEvents(ctx context.Context, sub *pubsub.Subscription, src chan<- *runEvent, ids runIDSource,
	jobID, runID *uuid.UUID, gap time.Duration,
) {
	defer close(src)
	defer sub.Close()
	since := time.Now()
	var pending []uuid.UUID
	queued := map[uuid.UUID]bool{}
	needResync := false
	add := func(id uuid.UUID) {
		if queued[id] {
			return
		}
		if len(pending) >= resyncMax {
			needResync = true
			return
		}
		queued[id] = true
		pending = append(pending, id)
	}
	ready := true
	timer := time.NewTimer(gap)
	timer.Stop()
	defer timer.Stop()
	for {
		if needResync && len(pending) == 0 {
			now := time.Now()
			list, err := ids.RunIDsUpdatedSince(ctx, since.Add(-resyncMargin), jobID, runID, resyncMax)
			if ctx.Err() != nil {
				return
			}
			// On an error since stays, so the next resync signal covers
			// the gap.
			needResync = false
			if err == nil {
				since = now
				for _, id := range list {
					add(id)
				}
			}
		}
		var out chan<- *runEvent
		var next *runEvent
		if ready && len(pending) > 0 {
			out, next = src, &runEvent{runID: pending[0]}
		}
		select {
		case <-ctx.Done():
			return
		case out <- next:
			delete(queued, pending[0])
			pending = pending[1:]
			ready = false
			timer.Reset(gap)
		case <-timer.C:
			ready = true
		case m := <-sub.C():
			kind, ref, err := runstate.ParsePayload(m.Payload)
			if err != nil || kind != runstate.EventRun {
				continue
			}
			// The payload's ids only pre-filter; delivery re-loads the
			// run and authorizes it with its stored job.
			if (runID != nil && ref.RunID != *runID) || (jobID != nil && ref.JobID != *jobID) {
				continue
			}
			add(ref.RunID)
		case <-sub.Resync():
			needResync = true
		}
	}
}

// runLogAppended streams new log lines of one run in bounded batches and
// completes after the run finished and every line was delivered.
func (r *Resolver) runLogAppended(ctx context.Context, runID uuid.UUID, afterID *string) (<-chan *model.RunLogBatch, error) {
	if streams.FromContext(ctx) == nil {
		return nil, errNotStream
	}
	rd, bus, err := r.runEventSources()
	if err != nil {
		return nil, err
	}
	after, err := parseLogID(afterID)
	if err != nil {
		return nil, err
	}
	st, err := rd.LogState(ctx, runID)
	switch {
	case errors.Is(err, runstate.ErrRunNotFound):
		if err := r.authorizeRun(ctx, authz.OpRunLogsView, uuid.Nil, false); err != nil {
			return nil, err
		}
		return nil, apierror.New(apierror.NotFound, "run not found")
	case err != nil:
		return nil, err
	}
	if err := r.authorizeRun(ctx, authz.OpRunLogsView, st.JobID, true); err != nil {
		return nil, err
	}
	// The run's job never changes (runs_terminal_guard), so the job loaded
	// above is the one every batch is authorized against; ownership itself
	// is looked up again for every batch.
	job := authz.Job{ID: st.JobID}

	sub := bus.Subscribe(pubsub.RunEvents.Name, runEventBuffer)
	kick := make(chan struct{}, 1)
	done := make(chan struct{})
	src := make(chan struct{})
	go pumpLogTicks(ctx, sub, src, kick, done, runID)

	check := streams.Authorize(r.Authz, authz.OpRunLogsView, func(context.Context, struct{}) ([]authz.Resource, error) {
		return []authz.Resource{job}, nil
	})
	// conv runs on Deliver's goroutine only, so last and finished need no
	// lock.
	last, finished := after, false
	conv := func(ctx context.Context, _ struct{}) (*model.RunLogBatch, bool) {
		if finished {
			return nil, false
		}
		// Read the status before the lines: a finished run's writer
		// flushed every line before the run was finished.
		st, err := rd.LogState(ctx, runID)
		if err != nil {
			return nil, false
		}
		lines, more, err := rd.Logs(ctx, runID, last, logBatchLines)
		if err != nil {
			return nil, false
		}
		var prev *string
		if last > 0 || afterID != nil {
			p := idString(last)
			prev = &p
		}
		if n := len(lines); n > 0 {
			last = lines[n-1].ID
		}
		if more {
			select {
			case kick <- struct{}{}:
			default:
			}
		}
		end := !more && st.Finished()
		if len(lines) == 0 && !end {
			return nil, false
		}
		if end {
			finished = true
			close(done)
		}
		return &model.RunLogBatch{RunID: runID, Lines: logLinesModel(lines), EndID: endID(lines, prev), Done: end}, true
	}
	return streams.Deliver(ctx, src, check, conv), nil
}

// pumpLogTicks sends a tick (coalesced) at the start, for every run event
// of runID, after possibly lost events and when the last batch was full,
// until ctx ends or done closes.
func pumpLogTicks(ctx context.Context, sub *pubsub.Subscription, src chan<- struct{}, kick, done <-chan struct{}, runID uuid.UUID) {
	defer close(src)
	defer sub.Close()
	pending := true
	for {
		var out chan<- struct{}
		if pending {
			out = src
		}
		select {
		case out <- struct{}{}:
			pending = false
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-kick:
			pending = true
		case m := <-sub.C():
			if _, ref, err := runstate.ParsePayload(m.Payload); err == nil && ref.RunID == runID {
				pending = true
			}
		case <-sub.Resync():
			pending = true
		}
	}
}
