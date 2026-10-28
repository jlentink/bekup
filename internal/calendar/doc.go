// Package calendar builds the job calendar (task 3.14, D-156): planned
// occurrences of the visible jobs (paused for disabled jobs, marked when
// pulled forward by "start early"), skipped occurrences, runs by status and
// retention expiries (exact for keep-days and Object Lock, projected where
// they depend on future runs under keep-last or the newest-complete rule).
//
// The package is read only. Service loads everything for the whole set of
// visible jobs in a fixed number of batched queries (no per-job queries)
// and compute turns the rows into events; compute is pure and holds every
// rule. Every input and output is bounded: the range (MaxRange), the jobs
// (MaxJobs), the loaded rows, the planned occurrences per job, the schedule
// computations of the projection and the events (MaxEvents); hitting a
// bound sets Calendar.Truncated. Authorization is the caller's: it passes
// the caller's job scope (authz.JobScope) and requested job ids that are
// not visible are dropped silently.
package calendar
