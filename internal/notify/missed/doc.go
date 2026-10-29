// Package missed is the missed-backup (dead-man) detector (task 5.4,
// D-188). A periodic River job (missed_backup_check, every Interval)
// scans the enabled, non-manual, non-deleted jobs in keyset batches and
// emits backup.missed through notify.Emit when a planned occurrence got no
// successful (or partial) backup within the job's expected window.
//
// Occurrences are taken from what the scheduler recorded (scheduled,
// catch-up and early runs by scheduled_for, schedule_skips rows) plus the
// stored next_run_at once its window has passed (the scheduler did not
// enqueue it), never replayed from the schedule: a replay cannot tell
// occurrences before the job was enabled from missed ones. Consecutive
// missed occurrences form one outage, keyed by the last successful run
// before it, so a broken job alerts once per outage (with a reminder every
// ReminderInterval while it lasts) instead of once per occurrence; a
// later success ends the outage and its backup.succeeded event is the
// recovery notice.
package missed
