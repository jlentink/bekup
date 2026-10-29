// Package webhook is the webhook sender of the notification engine
// (D-023, D-051, D-177): URL validation for personal and shared webhooks,
// HMAC-SHA256 signing with a timestamp, one guarded POST per attempt with
// a result classification (success, retryable, permanent, aborted), the
// storage-agnostic auto-disable policy and signing-key generation.
//
// The package keeps no state and never retries: the notify worker (task
// 5.3) owns channel storage, the River retries (MaxAttempts 10, D-145) and
// the persisted failure counters it feeds into FailurePolicy.
//
// Every request goes through a netguard HTTP client (D-120): dial-time SSRF
// check, no proxy, no redirects (a 3xx is a permanent failure), TLS 1.2 or
// later with system roots, bounded timeouts, and the response body is
// discarded after at most a few KiB. Webhook URLs often embed a bearer
// credential in the path or query (Slack, Teams, ...), so they are never
// logged; use DisplayURL to show one.
package webhook
