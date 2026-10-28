// Package scheduler computes schedules (interval + anchor in an IANA zone,
// or cron) and runs the multi-instance-safe scheduler loop that enqueues
// River jobs.
//
// Task 3.3 implements the pure next-run math (schedule.go, next.go,
// cron.go, wall.go, describe.go: no DB or River dependency). Task 3.4 adds
// the loop (loop.go, D-145): every tick locks due jobs FOR UPDATE SKIP
// LOCKED, enqueues at most one unique backup_run per job through
// internal/queue, records skipped occurrences in schedule_skips and
// advances next_run_at, one transaction per batch, safe on several serve
// instances; cleanup.go deletes old skip records. The semantics follow
// D-009, D-044 and the proposed D-143:
//
//   - A Schedule is "manual" (never due), "interval" (every N minutes,
//     hours, days, weeks or months from a local anchor date-time in an IANA
//     zone) or "cron" (a standard 5-field expression in an IANA zone).
//   - Minute and hour intervals are absolute durations from the anchor
//     instant; DST never stretches or shrinks them.
//   - Day, week and month intervals and cron are wall-clock schedules in the
//     zone. A local time that does not exist (spring forward) is shifted
//     forward by the size of the gap (02:30 becomes 03:30); an ambiguous
//     local time (fall back) resolves to its first, earlier, instant only.
//   - Monthly schedules always target the configured day of month, clamped
//     to the last day of shorter months (31 → Feb 28/29 → Mar 31).
//   - Cron uses robfig/cron/v3 only to parse the expression; occurrences are
//     computed here on the wall clock with the same DST rules, because
//     robfig's own Next skips nonexistent times.
//   - Missed occurrences collapse into one catch-up run (Due).
//
// The time/tzdata package is embedded so that zone names resolve the same
// way on every host, including minimal container images.
package scheduler
