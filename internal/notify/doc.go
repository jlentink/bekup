// Package notify is the notification core (task 5.3, D-161, D-183): the
// typed event catalog, the transactional outbox, recipient and preference
// resolution, and delivery through the in-app inbox, e-mail (package
// email, task 5.1) and HMAC-signed webhooks (package webhook, task 5.2).
//
// Flow:
//
//   - Producers call Emit with the *sqlc.Queries of the transaction of the
//     state change: runstate for backup runs, ObserveAudit (an
//     audit.Observer) for audited job, connection and security actions,
//     Service.EmitNow from post-commit hooks (restores, test restores,
//     retention anomalies, destination tests). Events carry identifiers and
//     a bounded, secret-free Detail only.
//   - The dispatcher (Service.Run, in every process with workers) enqueues
//     a notify_fanout job per new event.
//   - FanOut resolves the recipients at that moment (active accounts the
//     authz policy allows to see the subject), their preferences (per-job
//     override > per-event preference > D-161 default; mandatory security
//     events always inbox + e-mail) and the shared channels of the job,
//     applies the noise rules (started-event collapse, per-user flood
//     rule), writes inbox rows and one delivery per (recipient, channel)
//     with an idempotency key and enqueues notify_deliver jobs.
//   - Deliver re-checks the recipient and channel before every attempt,
//     sends, classifies the result (retries, Retry-After, attempt cap) and
//     auto-disables failing webhooks with an inbox notice.
//   - RetentionDigest emits the daily retention.summary; Cleanup prunes.
//
// API (task 5.3b, D-191) backs the GraphQL layer: shared channels
// (admin), personal webhooks, the preference matrix and the inbox; new
// inbox rows are announced on pubsub.NotificationEvents (IDs only) for
// the accountEvents stream.
package notify
