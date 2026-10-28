package queue

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/riverqueue/river"
)

// Workers collects the workers and periodic jobs of a worker process. Build
// it with NewWorkers, Register every worker, then pass it to New.
type Workers struct {
	settings Settings
	river    *river.Workers
	kinds    map[string]time.Duration
	periodic []*river.PeriodicJob
	sealed   bool
}

// NewWorkers returns an empty registry for s.
func NewWorkers(s Settings) (*Workers, error) {
	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("queue settings: %w", err)
	}
	return &Workers{settings: s, river: river.NewWorkers(), kinds: map[string]time.Duration{}}, nil
}

// ErrSealed is returned when registering after the client was built.
var ErrSealed = errors.New("queue: workers are already in use by a client")

// boundWorker binds River's per-attempt Timeout to the queue timeout, so
// no worker can run unbounded or longer than RescueStuckJobsAfter assumes.
type boundWorker[T river.JobArgs] struct {
	river.Worker[T]
	timeout time.Duration
}

func (w boundWorker[T]) Timeout(*river.Job[T]) time.Duration { return w.timeout }

// Register adds the worker of a catalog kind. Its attempts are bounded by the
// kind's queue timeout (the worker's own Timeout is ignored; enforce shorter
// per-job limits inside Work). Each kind is registered once.
func Register[T river.JobArgs](ws *Workers, w river.Worker[T]) error {
	if ws.sealed {
		return ErrSealed
	}
	var zero T
	kind := zero.Kind()
	spec, ok := catalog[kind]
	if !ok {
		return fmt.Errorf("queue: kind %q is not in the catalog (internal/queue/kinds.go)", kind)
	}
	if err := CheckArgs(zero); err != nil {
		return fmt.Errorf("queue: kind %q: %w", kind, err)
	}
	if _, dup := ws.kinds[kind]; dup {
		return fmt.Errorf("queue: kind %q registered twice", kind)
	}
	timeout := ws.settings.Queues[spec.Queue].Timeout
	if err := river.AddWorkerSafely[T](ws.river, boundWorker[T]{Worker: w, timeout: timeout}); err != nil {
		return fmt.Errorf("queue: register %q: %w", kind, err)
	}
	ws.kinds[kind] = timeout
	return nil
}

// AddPeriodic enqueues args every interval (River periodic job, enqueued by
// the elected leader; also once at leader start). The kind must be
// registered first and should be unique so leader changes do not pile up
// duplicates.
func (ws *Workers) AddPeriodic(interval time.Duration, args river.JobArgs) error {
	if ws.sealed {
		return ErrSealed
	}
	if interval < time.Minute {
		return fmt.Errorf("queue: periodic interval %s below 1m", interval)
	}
	spec, ok := catalog[args.Kind()]
	if !ok {
		return fmt.Errorf("queue: kind %q is not in the catalog", args.Kind())
	}
	if _, ok := ws.kinds[args.Kind()]; !ok {
		return fmt.Errorf("queue: periodic kind %q has no registered worker", args.Kind())
	}
	opts := insertOpts(spec, ws.settings, InsertOptions{})
	ws.periodic = append(ws.periodic, river.NewPeriodicJob(river.PeriodicInterval(interval),
		func() (river.JobArgs, *river.InsertOpts) { return args, opts },
		&river.PeriodicJobOpts{ID: args.Kind(), RunOnStart: true}))
	return nil
}

// Timeout returns the attempt timeout bound to a registered kind.
func (ws *Workers) Timeout(kind string) (time.Duration, bool) {
	d, ok := ws.kinds[kind]
	return d, ok
}

// Kinds returns the registered kinds in sorted order.
func (ws *Workers) Kinds() []string {
	out := make([]string, 0, len(ws.kinds))
	for k := range ws.kinds {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
