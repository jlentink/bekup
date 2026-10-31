package resolvers

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/retention"
	"github.com/jlentink/bekup/internal/runstate"
)

// runLoader batches the per-run and per-job loads of one query or
// mutation (task 3.7) so a page of runs costs one query for its items and
// one for its artifacts, and a page of jobs one query for their recent
// runs, instead of one per row. List resolvers hint the ids of their page;
// the first field resolver that needs data loads it for every hinted id
// (under the mutex, so concurrent field resolvers wait for that load
// instead of issuing their own) and the rest read the cache.
//
// It lives for one operation only (graph's AroundOperations installs it;
// subscriptions get none, so every event reads fresh data). It caches
// data the caller was already authorized for by the parent field; it
// never decides access itself.
type runLoader struct {
	mu        sync.Mutex
	runHints  []uuid.UUID
	jobHints  []uuid.UUID
	items     map[uuid.UUID][]sqlc.RunItem
	artifacts map[uuid.UUID][]runstate.ArtifactView
	// stats is the completeness evidence by run id, then item id (D-187).
	stats map[uuid.UUID]map[uuid.UUID]*runstate.ItemStats
	// recent is keyed by the per-job count.
	recent map[int]map[uuid.UUID][]runstate.RunView
	// missed is the missed-backup state by job id (Job.missed, D-196).
	missed map[uuid.UUID]*runstate.MissedState
	// userHints/activity batch the admin facts of a page of users
	// (User.lastSeenAt, User.twoFactorEnrolled; D-184).
	userHints []uuid.UUID
	activity  map[uuid.UUID]auth.UserActivity
	// channels are the shared channels attached to a page of jobs
	// (Job.sharedChannelAttachments, D-198).
	channels map[uuid.UUID][]notify.JobAttachment
	// holds are the retention holds of a page of jobs
	// (Job.retentionHolds, D-209).
	holds map[uuid.UUID][]retention.HoldView
}

type runLoaderKey struct{}

// WithRunLoader returns ctx with a fresh batch loader for one operation
// (graph.New installs it for queries and mutations).
func WithRunLoader(ctx context.Context) context.Context {
	return context.WithValue(ctx, runLoaderKey{}, &runLoader{
		items:     map[uuid.UUID][]sqlc.RunItem{},
		artifacts: map[uuid.UUID][]runstate.ArtifactView{},
		stats:     map[uuid.UUID]map[uuid.UUID]*runstate.ItemStats{},
		recent:    map[int]map[uuid.UUID][]runstate.RunView{},
		missed:    map[uuid.UUID]*runstate.MissedState{},
		activity:  map[uuid.UUID]auth.UserActivity{},
		channels:  map[uuid.UUID][]notify.JobAttachment{},
		holds:     map[uuid.UUID][]retention.HoldView{},
	})
}

func loaderFrom(ctx context.Context) *runLoader {
	if l, ok := ctx.Value(runLoaderKey{}).(*runLoader); ok {
		return l
	}
	return nil
}

// hintRuns announces the runs of a page.
func hintRuns(ctx context.Context, ids []uuid.UUID) {
	if l := loaderFrom(ctx); l != nil {
		l.mu.Lock()
		l.runHints = append(l.runHints, ids...)
		l.mu.Unlock()
	}
}

// hintJobs announces the jobs of a page.
func hintJobs(ctx context.Context, ids []uuid.UUID) {
	if l := loaderFrom(ctx); l != nil {
		l.mu.Lock()
		l.jobHints = append(l.jobHints, ids...)
		l.mu.Unlock()
	}
}

// hintUsers announces the users of a page.
func hintUsers(ctx context.Context, ids []uuid.UUID) {
	if l := loaderFrom(ctx); l != nil {
		l.mu.Lock()
		l.userHints = append(l.userHints, ids...)
		l.mu.Unlock()
	}
}

// pending returns id followed by the hinted ids not in cache yet (at most
// runstate.MaxBatch, without duplicates).
func pending[V any](id uuid.UUID, hints []uuid.UUID, cache map[uuid.UUID]V) []uuid.UUID {
	out := []uuid.UUID{id}
	seen := map[uuid.UUID]bool{id: true}
	for _, h := range hints {
		if len(out) >= runstate.MaxBatch {
			break
		}
		if _, ok := cache[h]; ok || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}

// batchLoad returns cache[id], loading it together with the other hinted
// ids first when needed. Without a loader it loads id alone.
func batchLoad[V any](ctx context.Context, l *runLoader, cache func(*runLoader) map[uuid.UUID]V, hints func(*runLoader) []uuid.UUID,
	id uuid.UUID, fetch func(context.Context, []uuid.UUID) (map[uuid.UUID]V, error),
) (V, error) {
	if l == nil {
		m, err := fetch(ctx, []uuid.UUID{id})
		return m[id], err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	c := cache(l)
	if v, ok := c[id]; ok {
		return v, nil
	}
	m, err := fetch(ctx, pending(id, hints(l), c))
	if err != nil {
		var zero V
		return zero, err
	}
	for k, v := range m {
		c[k] = v
	}
	return m[id], nil
}

func runHintsOf(l *runLoader) []uuid.UUID  { return l.runHints }
func jobHintsOf(l *runLoader) []uuid.UUID  { return l.jobHints }
func userHintsOf(l *runLoader) []uuid.UUID { return l.userHints }

// loadItems returns the items of a run.
func (r *Resolver) loadItems(ctx context.Context, runID uuid.UUID) ([]sqlc.RunItem, error) {
	return batchLoad(ctx, loaderFrom(ctx), func(l *runLoader) map[uuid.UUID][]sqlc.RunItem { return l.items },
		runHintsOf, runID, r.Runs.Items)
}

// loadArtifacts returns the artifacts of a run.
func (r *Resolver) loadArtifacts(ctx context.Context, runID uuid.UUID) ([]runstate.ArtifactView, error) {
	return batchLoad(ctx, loaderFrom(ctx), func(l *runLoader) map[uuid.UUID][]runstate.ArtifactView { return l.artifacts },
		runHintsOf, runID, r.Runs.Artifacts)
}

// loadStats returns the completeness evidence of a run's items by item id.
func (r *Resolver) loadStats(ctx context.Context, runID uuid.UUID) (map[uuid.UUID]*runstate.ItemStats, error) {
	return batchLoad(ctx, loaderFrom(ctx), func(l *runLoader) map[uuid.UUID]map[uuid.UUID]*runstate.ItemStats { return l.stats },
		runHintsOf, runID, r.Runs.Stats)
}

// loadRecentRuns returns the newest n runs of a job.
func (r *Resolver) loadRecentRuns(ctx context.Context, jobID uuid.UUID, n int) ([]runstate.RunView, error) {
	fetch := func(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]runstate.RunView, error) {
		return r.Runs.RecentRunsForJobs(ctx, ids, n)
	}
	return batchLoad(ctx, loaderFrom(ctx), func(l *runLoader) map[uuid.UUID][]runstate.RunView {
		m := l.recent[n]
		if m == nil {
			m = map[uuid.UUID][]runstate.RunView{}
			l.recent[n] = m
		}
		return m
	}, jobHintsOf, jobID, fetch)
}

// loadMissed returns the ongoing missed-backup outage of a job (nil:
// none).
func (r *Resolver) loadMissed(ctx context.Context, jobID uuid.UUID) (*runstate.MissedState, error) {
	return batchLoad(ctx, loaderFrom(ctx), func(l *runLoader) map[uuid.UUID]*runstate.MissedState { return l.missed },
		jobHintsOf, jobID, r.Runs.MissedForJobs)
}

// loadUserActivity returns the admin facts of a user.
func (r *Resolver) loadUserActivity(ctx context.Context, userID uuid.UUID) (auth.UserActivity, error) {
	return batchLoad(ctx, loaderFrom(ctx), func(l *runLoader) map[uuid.UUID]auth.UserActivity { return l.activity },
		userHintsOf, userID, r.Users.Activity)
}

// loadRetentionHolds returns the retention holds of a job.
func (r *Resolver) loadRetentionHolds(ctx context.Context, jobID uuid.UUID) ([]retention.HoldView, error) {
	return batchLoad(ctx, loaderFrom(ctx), func(l *runLoader) map[uuid.UUID][]retention.HoldView { return l.holds },
		jobHintsOf, jobID, r.RetentionHolds.HoldsForJobs)
}
