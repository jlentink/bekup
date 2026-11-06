// Package summary is the periodic summary e-mail (D-226): every user gets
// a summary of the backups they may see, daily, every other day, weekly
// (default), monthly or never, at a local hour (and weekday or day of
// month) of their own time zone.
//
// A periodic River job (notify_summary_tick, every TickInterval, leader
// only) adds state rows to user_summary_settings for accounts without one
// (they follow notifications.summary_default_*), computes due times with
// the scheduler's wall-clock and DST rules (scheduler.Plan) and claims
// due rows FOR UPDATE SKIP LOCKED, one transaction each. A due summary is
// a summary.periodic event (notify.Emit, dedup key summary:<user>:<period
// end>); missed due times (downtime) collapse into one summary covering
// the whole gap. Accounts that can see no job get none (recorded as
// no_jobs).
//
// The content is built by Render (notify.SummaryRenderer) at fan-out and
// again at e-mail delivery, scoped to the recipient's current access:
// authz.JobScope for the jobs, destination.usage_view for the destination
// usage and monitor.alert_list for the security alert count. It reads
// only bekup's own tables (free space only from the stored answer of the
// hourly space check, D-227); the inbox row carries counts only, the e-mail
// (email.Render) a status banner, key figures and one row per job with its
// status (worst first, at most 100 rows), the restores, the storage
// (staff) and the security alerts (admins). A period is at least one
// calendar period in the user's zone (periodBefore, D-226 amendment 1).
//
// The settings API (MySettings, SetSettings, SendPreview) backs
// mySummarySettings, setSummarySettings and sendSummaryPreview.
package summary
