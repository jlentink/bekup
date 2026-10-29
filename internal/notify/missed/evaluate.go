package missed

import (
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/queue"
)

// Source says where an occurrence comes from.
type Source string

// Occurrence sources.
const (
	// SourceRun: a scheduled, catch-up or early run stands for it.
	SourceRun Source = "run"
	// SourceSkip: a schedule_skips row (still_running, unschedulable).
	SourceSkip Source = "skip"
	// SourcePending: the stored next_run_at, not enqueued although its
	// window has passed (the scheduler is not running or is stuck).
	SourcePending Source = "pending"
)

// sourceRank orders occurrences at the same instant: a run is the best
// evidence, then a skip record, then the pending next_run_at.
var sourceRank = map[Source]int{SourceRun: 0, SourceSkip: 1, SourcePending: 2}

// Occurrence is one planned occurrence of a job's schedule that the
// scheduler handled (or should have).
type Occurrence struct {
	At     time.Time
	Source Source
	// Status is the run status (SourceRun) or the skip reason (SourceSkip).
	Status string
	// Finished is the run's finish time (SourceRun, terminal runs).
	Finished *time.Time
}

// Success is a run of the job that ended success or partial.
type Success struct {
	RunID    uuid.UUID
	Finished time.Time
}

// Outage is a series of consecutive missed occurrences of one job.
type Outage struct {
	// After is the last successful run before the outage (uuid.Nil: the
	// job never succeeded); it identifies the outage.
	After uuid.UUID
	// First and Last are the first and the last missed occurrence.
	First, Last time.Time
	// Missed is the number of missed occurrences seen.
	Missed int
	// Reason classifies the last missed occurrence (see reasonOf).
	Reason string
	// Ongoing reports that no run succeeded since the last one.
	Ongoing bool
}

// Defaults of the derived window (D-188).
const (
	// Grace is added to the run time of a derived window (queue wait,
	// retry backoff, clock skew).
	Grace = time.Hour
)

// Window is the expected window of a job: expected_window_seconds when
// set; otherwise derived from the run time a backup may take, every
// attempt up to the job's timeout (timeout_seconds, else
// workers.backup_timeout) plus Grace.
func Window(expectedSeconds, timeoutSeconds *int32, defaultTimeout time.Duration) time.Duration {
	if expectedSeconds != nil && *expectedSeconds > 0 {
		return time.Duration(*expectedSeconds) * time.Second
	}
	timeout := defaultTimeout
	if timeoutSeconds != nil && *timeoutSeconds > 0 {
		timeout = time.Duration(*timeoutSeconds) * time.Second
	}
	return time.Duration(queue.MaxAttemptsBackup)*timeout + Grace
}

// normalize sorts occurrences by time and keeps one per instant (the best
// source).
func normalize(occs []Occurrence) []Occurrence {
	out := append([]Occurrence(nil), occs...)
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.Before(out[j].At)
		}
		if out[i].Source != out[j].Source {
			return sourceRank[out[i].Source] < sourceRank[out[j].Source]
		}
		return succeeded(out[i].Status) && !succeeded(out[j].Status)
	})
	n := 0
	for i := range out {
		if n > 0 && out[n-1].At.Equal(out[i].At) {
			// Several records of one occurrence: the first is the best
			// (a successful run before other runs, skips and next_run_at).
			continue
		}
		out[n] = out[i]
		n++
	}
	return out[:n]
}

func succeeded(status string) bool { return status == "success" || status == "partial" }

// Evaluate returns the outages of one job. occs are the occurrences whose
// window has passed (any order); succ the job's successful and partial
// runs sorted by finish time, including the latest one that finished at
// or before the first occurrence.
//
// An occurrence T is covered when its own run succeeded by T+window
// (also an early run that finished before T) or any run of the job
// succeeded in (T, T+window] (a later run, a manual run, or the run that
// was still running when T was skipped). Partial counts as covered: the
// job ran and the failed databases have their own event (D-188).
func Evaluate(occs []Occurrence, succ []Success, window time.Duration) []Outage {
	var out []Outage
	for _, o := range normalize(occs) {
		deadline := o.At.Add(window)
		if o.Source == SourceRun && succeeded(o.Status) && o.Finished != nil && !o.Finished.After(deadline) {
			continue
		}
		// First success that finished strictly after T.
		i := sort.Search(len(succ), func(k int) bool { return succ[k].Finished.After(o.At) })
		if i < len(succ) && !succ[i].Finished.After(deadline) {
			continue
		}
		after := uuid.Nil
		if i > 0 {
			after = succ[i-1].RunID
		}
		if len(out) > 0 && out[len(out)-1].After == after {
			cur := &out[len(out)-1]
			cur.Last, cur.Missed, cur.Reason = o.At, cur.Missed+1, reasonOf(o)
			cur.Ongoing = i == len(succ)
			continue
		}
		out = append(out, Outage{After: after, First: o.At, Last: o.At, Missed: 1, Reason: reasonOf(o), Ongoing: i == len(succ)})
	}
	return out
}

// reasonOf is a fixed classification of a missed occurrence (no free
// text: it goes into the event detail).
func reasonOf(o Occurrence) string {
	switch o.Source {
	case SourcePending:
		return "not_enqueued"
	case SourceSkip:
		switch o.Status {
		case "still_running":
			return "skipped_still_running"
		case "unschedulable":
			return "unschedulable"
		}
		return "skipped"
	case SourceRun:
		switch o.Status {
		case "success", "partial":
			return "finished_late"
		case "queued", "running", "failed", "canceled", "skipped":
			return "run_" + o.Status
		}
	}
	return "unknown"
}
