# Notifications

> **Status:** implemented: the notification core, e-mail and webhooks, missed-backup and security alerts and the GraphQL API (M5), and the notification center of the web UI: the bell, the inbox, **Profile → Notifications**, `/verify-email` and the admin pages for SMTP, shared channels and security alerts (M6).

How notifications work: the in-app inbox, email and HMAC-signed webhooks, admin-managed shared channels, per-user preferences per event and job, verifying webhook signatures, and the security alerts that cannot be disabled.

## How notifications work

Every notification starts as an **event** from a fixed catalog. bekup
records the event in the same database transaction as the change it reports
(a backup finishing, a job being edited, a password change), so an event is
never lost and never reports something that did not happen. A few seconds
later bekup works out who receives it:

1. **Who may see it now.** Recipients are the active accounts that are
   allowed to see the event's subject *at that moment*: admins and
   maintainers see every job, a backup owner only the jobs they own. The
   check is repeated right before every e-mail or webhook is sent, so
   someone who loses access in the meantime gets nothing more.
2. **Their preferences.** For each recipient and channel bekup takes, in
   this order: the recipient's override for this job, their setting for
   this event, the default below.
3. **Shared channels.** Admin-managed team channels (an e-mail list or a
   webhook) attached to the job receive the job's events that match the
   channel's filter (event groups or single events; no filter = all).
   Security events never go to shared channels.

Notifications carry minimal data: the event, the job (or connection,
destination) name, a status or fixed error code, the time and a link into
bekup built from `server.base_url`. They never contain passwords, keys,
connection strings, server output or database content.

### Channels

| Channel | Who sets it up | Notes |
|---|---|---|
| In-app inbox | always there | every event you may see, by default |
| E-mail | your account address | only after you [verified it](#verifying-your-e-mail-address); otherwise e-mails are skipped |
| Personal webhooks | you (up to 5) | HMAC-signed, opt-in per event |
| Shared e-mail list / webhook | admins, attached to jobs | job events matching the channel's filter |

Webhook URLs are stored encrypted (chat services put a bearer credential in
the URL); bekup only shows scheme, host and port.

### The event catalog

Defaults for a new account: **every event you may see goes to the inbox;
problem events (✱) also go to e-mail; webhooks are opt-in.**

| Group | Event | Who may receive it |
|---|---|---|
| Backup | `backup.started` (low priority), `backup.started_early`, `backup.succeeded`, `backup.succeeded_with_warnings` ✱, `backup.partial` ✱, `backup.failed` ✱, `backup.canceled`, `backup.missed` ✱ | admins, maintainers, the job's owners |
| Restore | `restore.started` (low priority), `restore.completed`, `restore.failed` ✱, `test_restore.passed`, `test_restore.warnings` ✱, `test_restore.failed` ✱ | admins, maintainers, the job's owners |
| Retention & storage | `retention.summary` (daily digest), `retention.blocked` ✱, `artifact.missing` ✱ | admins, maintainers, the job's owners |
| | `destination.test_failed` ✱ | admins, maintainers |
| Job & configuration | `job.created`, `job.updated`, `job.disabled`, `job.enabled`, `job.deleted`, `job.encryption_changed` ✱ | admins, maintainers, the job's owners |
| | `connection.pin_changed` ✱, `connection.tls_downgraded` ✱, `connection.host_key_changed` ✱ | admins, maintainers |
| Security (mandatory) | `security.account` ✱: a new sign-in (at most one per address and day), password changed or reset (also by `bekup admin reset-password`), a second factor added, removed or reset (also by `bekup admin reset-2fa`), a recovery code used or regenerated, the account or its second factor locked, your role changed, a passkey with a backwards signature counter (possible clone) | only you |
| | `security.system` ✱: see [Security alerts](#security-alerts) | admins |

**Security events cannot be turned off:** they always go to your inbox and
to your (verified) e-mail address. You can only add channels, for example a
personal webhook.

### Notices

Besides the catalog events, bekup writes a few **notices** straight into
an inbox. They are not events: they have no preferences, are never sent by
e-mail or webhook and never reach shared channels. The inbox filter
accepts their type in `events` (D-196).

| Type | Severity | Recipients | When |
|---|---|---|---|
| `notice.webhook_disabled` | warning | the owner of a personal webhook; every active admin for a shared webhook | a webhook was [disabled automatically](#automatic-disabling). The title names the webhook, the body the reason; the link opens **Profile → Notifications** (personal) or **Administration → Shared channels** (shared); the payload holds `channel_id`, `shared` and `reason` (`repeated_failures` or `permanent_failures`). |

### Security alerts

`security.system` alerts go to every admin (inbox and e-mail, never to
shared channels, exempt from flood protection). Each has a fixed severity;
alerts that could repeat are sent once per the period shown. Admins can read
the same list from the API (`securityAlertTypes`: kind, severity,
description and repeat rule, D-198).

| Alert | Severity | Sent |
|---|---|---|
| A security setting weakened (opt-out: connection TLS downgrade, job encryption off) | error | every time |
| Database certificate pin, SSH tunnel host key or SFTP host key mismatch (possible impersonation) | critical | once per connection/destination and day |
| A backup file failed its verification before a restore (possible tampering), while staging or when the restore is requested | critical | every time |
| Audit chain verification failed (hourly check, see below) | critical | once per finding |
| Signed audit head could not be exported | error | once a day |
| A new admin (UI or `bekup admin create`), an account given the admin role | warning | every time |
| A job owner added (warning) or removed (info) (D-067) | warning / info | every time |
| A job's encryption passphrases exported | warning | every time |
| Master keys, instance signing key or internal certificates rotated | warning / info | every time |
| A backup file disappeared from its destination without bekup deleting it | error | once per file |
| Retention stopped by a safety check (newest backup missing or changed) | error | once per job, destination and day |
| A restore into a privileged (or unverified) database role requested with the acknowledgement; a restore refused because the role became privileged; a privileged test-restore scratch target set | warning | every time |
| A restore switched SECURITY DEFINER functions to SECURITY INVOKER | warning | every time |
| A job's custom schema-version query set or changed, or a job with one moved to another connection (`job_custom_version_query`; it runs on the backed-up databases with the backup role's rights, D-210) | warning | every time |
| The 2FA policy weakened | error | every time |
| The SSRF allowlist widened (an admin added CIDRs or host names, or scopes to an entry, that outbound connections may reach; the alert says so when users' personal webhooks gained access; D-196, D-198) | error | every time |
| A configuration reload weakened security (`config_reload_weakened`: an applied `bekup.toml` change widened `trusted_proxies` or the `[netguard]` list, raised a rate, size or GraphQL limit, raised the lockout threshold or shortened a lockout, lowered a password bound, raised the retention deletion cap or the webhook failure threshold, shortened the partial cleanup age or turned `retention.dry_run` off; the alert names the keys, never values; D-200) | error | once per reload request and set of keys |
| A passkey with a backwards signature counter (possible clone) | error | every time |
| A download link presented after use, after expiry, by another session or without access | warning | once per link |
| bekup running over plain HTTP outside dev mode, or with a weakened app-database TLS mode (D-095, D-100) | error | at startup and once a day |
| bekup running in single-account database mode (`insecure_db_roles`: one database account runs migrations and the app, so a compromised server process could alter the schema and the audit log; D-207) | error | at startup and once a day |
| bekup running an image that follows a tag instead of a digest (`unpinned_image`: `ghcr.io/jlentink/bekup:latest`, so whoever moves the tag decides which code runs; set up so, or reported by the runner for its own container image; D-212) | warning | at startup (or when the runner first reports it) and once a day |
| Failed sign-in burst: ≥ 20 failures from one address, ≥ 10 on one account or ≥ 100 in total within 15 minutes | error | once per address / account / instance and 15 minutes |
| An internal certificate (runner mTLS client and CA, app-database CA and client certificate, the runner's server certificate) or a pinned database certificate expires within 30 days (warning), within 7 days (error) or expired | warning / error / critical | once per threshold and certificate |
| A break-glass command on the host (`bekup admin …`, `bekup certs rotate`, `bekup audit sign-head`, `bekup reindex --import`) | warning | every time |

Burst alerts name the client address or the targeted account, never an
e-mail address someone typed. CLI alerts carry the command, the OS user and
the host name; the command writes the alert to the database together with
its audit entry, and the running `serve` delivers it within seconds.

An hourly security check (River job `security_check`) looks for the
conditions no single action reports: certificate and pin expiry, the
audit chain (only new rows each hour, the whole chain once a day and after
every restart; a rewrite of already verified rows is also caught) and the
weakened startup settings.

### Noise rules

- **Started events are collapsed:** `backup.started` and `restore.started`
  of the same job within `notifications.collapse_window` (default 15 min)
  are shown once, with a count of the collapsed ones.
- **Flood protection:** when you received `notifications.max_per_user_per_hour`
  (default 30) e-mails (or webhook calls) within an hour, the next one is a
  single "notifications paused" summary and further ones only go to your
  inbox until the rate drops. Security events are exempt.
- **Missed backups alert once per outage:** `backup.missed` is sent when a
  planned backup did not succeed within the job's expected window, once
  for a series of misses and then at most once a day until a backup
  succeeds again (details: [Missed-backup
  alerts](jobs-and-schedules.md#missed-backup-alerts)).
- **Retention is a daily digest:** removals by retention are never reported
  per file; once a day each job with removals gets one `retention.summary`
  (backups, files and bytes removed on that day).
- **Blocked retention at most once a day:** `retention.blocked` is sent when
  retention keeps incomplete backups on a destination that no keep rule
  would keep (the destination has no complete backup, or the backups after
  the newest complete one are incomplete), at most once per job,
  destination and UTC day while that lasts (details:
  [Retention](retention.md#when-incomplete-backups-block-retention)).
- Inbox entries are kept for 180 days; delivery records for 90 days.

### Delivery status

Every e-mail and webhook call is recorded per event, recipient and channel
with a status (`queued`, `sent`, `failed`, `skipped`) and a fixed reason
code, for example `email_unverified`, `access_revoked`, `flood_collapsed`,
`channel_disabled`, `email_not_configured`. A delivery is never sent twice
for the same event and channel (webhook receivers can additionally
deduplicate on `X-Bekup-Delivery`).

## Your notification settings

Every user manages their own channels and preferences (GraphQL names in
brackets; D-191). Nobody, not even an admin, can see or change another
user's settings.

In the web UI the **bell** in the top bar shows your unread count live and
the latest entries; **Open the inbox** (`/notifications`) lists everything
with an unread filter, mark read and delete. The API filters the inbox by
group and event type as well (`notifications(filter: {group, events,
unreadOnly})`, D-196); the admin security-alerts page uses it. Your channels and the
preference matrix (with per-job overrides and **Reset to defaults**) are
under **Profile → Notifications** (D-193).

### Your channels

`myNotificationChannels` shows your e-mail address with its verification
state (e-mail notifications are skipped until you
[verify it](#verifying-your-e-mail-address)) and your personal webhooks:
label, `https://host/…`, and their health (enabled, failure counters, the
last failure code, and why a webhook was disabled).

- **Add a webhook** (`addMyWebhook(url, label)`, up to 5, needs a recent
  step-up confirmation). The URL must follow the
  [webhook URL rules](#rules-for-webhook-urls). bekup generates the
  **signing key** (`bkwh_…`) and shows it **once**, in the answer: copy it
  into your receiver right away. It is never shown again.
- **Rotate the key** (`rotateMyWebhookKey`, step-up): a new key, again
  shown once; the old one stops working at once.
- **Test** (`testMyWebhook`): sends a signed `webhook.test` delivery and
  reports only `ok`, `not_allowed` (the address is blocked by the SSRF
  guard) or `failed`, with one fixed message: never the receiver's HTTP
  status or answer, so a test cannot be used to probe other services
  (D-198). Rate limited, one per request; works on a disabled webhook
  too, so you can check a fix before re-enabling it. Tests never count
  towards automatic disabling.
- **Re-enable** (`reenableMyWebhook`) after an
  [automatic disable](#automatic-disabling); the failure counters start
  afresh.
- **Delete** (`deleteMyWebhook`, step-up).

### Preferences

`notificationCatalog` lists the events you can receive, grouped (backup,
restore, retention & storage, job & configuration, security), with their
audience, whether they are problem events, mandatory, low priority, and
their default channels. Maintainers do not see `security.system`; backup
owners also not the connection and destination events.

`myNotificationPreferences` is the matrix: one row per event, one cell per
channel (inbox, e-mail, webhook) with the effective value and where it
comes from: `DEFAULT` (the defaults above), `OVERRIDE` (your setting) or
`MANDATORY` (security alerts, locked). Each row also lists your per-job
overrides for jobs you can still see.

- `setNotificationPreference(event, channel, enabled)` changes a cell;
  with `jobId` it sets an override for one job only (job events, jobs you
  may see). The inbox and e-mail cells of security events cannot be
  turned off (VALIDATION); you may add your webhooks to them.
- `resetNotificationPreferences` goes back to the defaults;
  with `jobId` it removes only that job's overrides.

The webhook column applies to all your enabled webhooks.

### The in-app inbox

- `notifications(unreadOnly, first, after)`: newest first, 1–200 per page
  (default 50), cursor pagination (`pageInfo.endCursor`).
- `unreadNotificationCount` for the bell badge.
- `markNotificationsRead(ids: [...])` (up to 200) or
  `markNotificationsRead(all: true)`; returns the remaining unread count.
- `deleteNotification(id)`. Security notifications (`security.account`,
  `security.system`) cannot be deleted (VALIDATION), only marked read: they
  stay as a record for the account holder and the admins.
- What you see follows your **current** access: notifications about a job
  you no longer own, and `security.system` alerts after you stop being an
  admin (or staff events after you stop being staff), are hidden from the
  list, the unread count and the live stream (D-198).
- `subscription { accountEvents { notification { ... } session { ... } } }`
  streams new notifications of **your own** inbox as they arrive, together
  with changes to your sessions (Server-Sent Events, like the other
  subscriptions; each event sets one of the two fields). Each notification
  is loaded for your account and re-authorized when it is delivered;
  signing out ends the stream. The web UI keeps one such stream per
  browser and shares it across tabs (D-197).

Inbox entries are kept for 180 days.

## Shared channels (admins)

Admins manage team channels that receive the events of the jobs they are
attached to: an **e-mail list** (1–50 addresses) or a **webhook** (same URL
rules and signing as personal webhooks). Shared channels never receive
security alerts.

In the web UI they are under **Administration → Shared channels**
(`/admin/channels`): create, edit, test, enable or disable a channel,
rotate a webhook's key (shown once) and attach it to jobs with an event
filter.

| Operation | Notes |
|---|---|
| `sharedChannels` | every channel with its type, addresses or `https://host/…`, health and job attachments |
| `createSharedChannel(input)` | step-up; a webhook's signing key is returned **once** |
| `updateSharedChannel(id, input)` | step-up; name, addresses or a new URL (the key stays) |
| `rotateSharedChannelKey(id)` | step-up; the new key is returned once |
| `deleteSharedChannel(id)` | step-up; also removes its job attachments |
| `enableSharedChannel(id)` / `disableSharedChannel(id)` | enabling also clears an automatic disable |
| `testSharedChannel(id)` | a test e-mail to the list or a signed test delivery; rate limited, one per request |
| `attachSharedChannel(jobId, channelId, filter)` | `filter: { groups: [BACKUP, ...], events: ["restore.failed", ...] }`, both empty = every non-security event of the job; attaching again replaces the filter |
| `detachSharedChannel(jobId, channelId)` | |

Every change and test is audited (`notification.channel_*`, plus
`secret.changed` when a URL or key is set; never the URL path or the key).

Maintainers can see which shared channels a job notifies, without
managing them: `Job.sharedChannelAttachments` lists each attached
channel's name, type, masked webhook URL, whether it is enabled and the
group/event filter (never e-mail addresses, URLs or keys; null for backup
owners; D-198).

## E-mail (SMTP)

bekup sends e-mail through one SMTP relay that an admin configures in
**Administration → Settings** (the SMTP section; GraphQL `updateSmtpSettings`; admin only, every change
needs a fresh step-up and is recorded in the audit log field by field).
The settings live in the database, not in `bekup.toml` (D-178, D-180).

| Setting | Meaning |
|---|---|
| Enabled | Off: bekup sends no e-mail at all. |
| Host, port | The relay. Usually port 587 for STARTTLS and 465 for implicit TLS. |
| Mode | How the connection is encrypted (see below). Default `AUTO`. |
| Username, password | Optional SMTP AUTH (PLAIN or LOGIN). The password is write-only: it is stored encrypted and the UI only shows whether one is set. |
| From address, from name | The sender of every message. |
| CA bundle | Optional PEM certificates for a relay with a private CA. When set, **only** these CAs are trusted for the relay (not the system roots). |
| Timeouts | Connect 1–60 s (default 10); the whole SMTP session 5–300 s (default 60). |

### Modes

bekup follows what your SMTP server supports (D-180):

| Mode | Behaviour |
|---|---|
| `AUTO` (default) | Upgrade with STARTTLS when the server offers it, otherwise send **unencrypted**. If the server offers STARTTLS but the handshake or certificate check fails, sending fails; bekup does not fall back to plain text then. |
| `STARTTLS` | STARTTLS is required: a server that does not offer it is refused before anything is sent. Usually port 587. |
| `IMPLICIT_TLS` | TLS from the first byte (SMTPS). Usually port 465. |
| `NONE` | Never encrypt, even when the server offers STARTTLS. |

**The risk of `AUTO` and `NONE`:** when the connection ends up
unencrypted, the message (recipients, subject, body) **and the SMTP
username and password** (AUTH PLAIN/LOGIN is then sent in clear text)
can be read and changed by anyone on the network path. In `AUTO` an
attacker on the path can also hide the server's STARTTLS offer and force
plain text. Use `STARTTLS` or `IMPLICIT_TLS` whenever your server supports
TLS. Because these modes weaken security:

- the settings show `mayBeUnencrypted` (the admin UI shows an "e-mail may
  be sent unencrypted" badge);
- every change is audited with the mode (`setting.changed`,
  `mode` / `may_be_unencrypted`) and logs a warning when saved;
- a send that ends up unencrypted logs `smtp_unencrypted_session` at WARN
  (host, port and mode only; once per server and process);
- the test e-mail reports whether the connection was encrypted
  (`encrypted`, `tlsVersion`).

Settings saved by older builds with the dev-only `plaintext` mode are read
as `NONE`.

Security rules:

- **When TLS is used it is verified:** TLS 1.2 or newer, and the
  certificate must match the host name and chain to the system roots (or
  the CA bundle). The certificate is not pinned (D-018).
- **AUTH** (PLAIN preferred, LOGIN as fallback) goes only to the configured
  host; without TLS only in `AUTO`/`NONE` (see the risk above).
- **SSRF guard:** the host is resolved and checked when you save and again
  on every connection. A relay on a private network (for example
  `10.0.0.25` or `smtp.internal`) must be allowlisted in `[netguard]`
  (`allow_cidrs` / `allow_hosts`, see [Configuration](../configuration.md))
  or in the admin allowlist with the `smtp` scope (the default);
  otherwise sending fails with `not_allowed`.
- When you change the host, port or username, weaken the mode (from
  `STARTTLS` or `IMPLICIT_TLS` to `AUTO` or `NONE`) or change the CA
  bundle, enter the password again in the same save: a stored password is
  never sent to a different or less protected server (D-140, D-198).
- In dev mode `just dev` starts mailpit on `127.0.0.1:1025` (web UI on
  `http://127.0.0.1:8025`); use host `127.0.0.1`, port `1025`, mode
  `AUTO` or `NONE` and keep
  `allow_cidrs = ["127.0.0.1/32"]` (already in
  `deploy/dev/bekup.local.toml`).

**Test e-mail.** `sendTestEmail(to)` sends a short test message with the
stored settings (admin only, rate limited, audited as `smtp.test_sent`
with `encrypted`). The result says whether the connection was encrypted
(`encrypted`) and the negotiated TLS version (`tlsVersion`). A failure
returns a fixed code and message, never the server's text:

| Code | Meaning |
|---|---|
| `not_configured` | No settings stored, or e-mail is disabled. |
| `not_allowed` | The relay's address is blocked by the SSRF guard (the detail names the address; admins only). |
| `unreachable` | Connection refused, timed out or the host does not resolve. |
| `tls_failed` | Certificate, host name or TLS version problem. |
| `starttls_unavailable` | The relay does not offer STARTTLS (mode `STARTTLS`). |
| `auth_failed` / `auth_unsupported` | Wrong credentials / the relay offers no PLAIN or LOGIN. |
| `rejected` | The relay refused the sender, a recipient or the message. |

Messages are plain text plus a simple HTML part with the same content: no
images, no remote resources, no tracking pixels or rewritten links, and
only minimal data (never secrets or database contents).

## Verifying your e-mail address

bekup sends e-mail notifications to a user only after the address was
verified (D-023, D-051).

1. In your profile choose **Verify e-mail** (GraphQL
   `requestEmailVerification`). bekup mails a link to your account's
   address. At most one link per minute; a new link replaces the old one.
2. Open the link **while signed in to the same account**. The link looks
   like `https://bekup.example.com/verify-email#code=…`; the code sits in
   the URL fragment, so it never appears in server or proxy logs. The page
   calls `verifyEmail(code)`.

The link works **once**, expires after **24 hours** and only for the
account and the address it was sent for. A wrong, used, expired or foreign
code gets one generic answer; after five wrong codes the link is voided.
Whenever an account's e-mail address changes, its verification is cleared
and must be repeated. Verification needs working SMTP settings.

## Webhooks

Every user may add up to five personal webhooks; admins can also attach
shared webhooks to jobs. bekup delivers each notification as one signed
HTTPS `POST` (D-023, D-051, D-177).

### Rules for webhook URLs

- **https only.** Plain `http` is accepted only when `dev.enabled` is set,
  and then with a warning in the log.
- No user name or password in the URL, no `#fragment`, at most 2048
  characters of printable ASCII (enter international domain names in
  punycode).
- The host must resolve to public addresses only. Private, loopback,
  link-local, cloud-metadata and other special addresses (also in their
  IPv4-mapped and IPv6 transition forms) are refused when you save the URL
  **and** again on every connection, so a DNS record that changes later
  (DNS rebinding) cannot reach an internal service. An admin can allowlist
  specific private networks or hosts for **shared** webhooks (the
  `shared_webhooks` scope, the default; `[netguard]` in bekup.toml applies
  too, see [Configuration](../configuration.md)). **Personal** webhooks
  reach a private target only through an allowlist entry an admin
  explicitly scoped to `personal_webhooks` (Settings → Network; audited,
  alerted and flagged permanently), because any user can enter a
  personal webhook URL (D-198). Metadata and link-local addresses can
  never be allowlisted.
- **Redirects are never followed.** A `3xx` answer is a failed delivery;
  configure the final URL.
- No HTTP proxy is used (a proxy would bypass the address checks).
- Many services (Slack, Microsoft Teams, ...) put a credential in the
  webhook URL. bekup treats the URL as sensitive: it is stored encrypted,
  never logged, and shown as `https://host/…` after saving.

### The request

```http
POST /your/path HTTP/1.1
Content-Type: application/json
User-Agent: bekup/1.4.0
X-Bekup-Event: backup.failed
X-Bekup-Delivery: 7f9c2ba4e88f827d616045507605853e
X-Bekup-Timestamp: 1767225600
X-Bekup-Signature: v1=<64 lowercase hex characters>

{"id":"evt_01","type":"backup.failed","created_at":"2026-01-01T00:00:00Z","data":{...}}
```

| Header | Meaning |
|---|---|
| `X-Bekup-Event` | Event type from the event catalog (`backup.failed`, `restore.completed`, `webhook.test`, ...). |
| `X-Bekup-Delivery` | Delivery ID. **The same on every retry** of one notification to one webhook; use it to drop duplicates. |
| `X-Bekup-Timestamp` | Unix time (seconds) when this attempt was signed. |
| `X-Bekup-Signature` | `v1=` followed by the lowercase hex HMAC-SHA256 described below. Several comma-separated `v1=` entries may appear in the future (key rotation); accept the request if any entry matches. |

The body is a JSON object of at most 64 KiB:

| Field | Meaning |
|---|---|
| `id` | Event ID (the same event sent to several channels has the same `id`). |
| `type` | Event type, equal to `X-Bekup-Event`. |
| `created_at` | When the event happened (RFC 3339, UTC, whole seconds). |
| `data` | Event fields: only minimal data such as the job name, status, time and a link to bekup. Never credentials, keys or database contents. |

A "send test" in the UI delivers the event type `webhook.test` with
`data: {"message": "This is a test delivery from bekup."}`.

Any `2xx` status is a success; bekup ignores the response body (it reads at
most 4 KiB). Answer quickly (within 15 seconds) and do slow work
asynchronously.

### Retries

| Answer | Treated as |
|---|---|
| `2xx` | delivered |
| network error, timeout, `5xx`, `408`, `429` | temporary: retried with backoff (up to 10 attempts); `Retry-After` on `429`/`503` is honored up to 1 hour |
| `3xx`, other `4xx`, a refused address, a TLS error | permanent: not retried |

### Verifying the signature

When you create a webhook (or rotate its key), bekup shows its **signing
key** once, for example `bkwh_dGhpcyBpcyBhbiBleGFtcGxlIGtleSBvbmx5ISEhISE`.
Store it in your receiver's secret store. Use **the whole string, including
the `bkwh_` prefix, as the HMAC key** (its UTF-8 bytes; do not decode it).

The signature covers the timestamp, the delivery ID, the event type and
the body, each separated by one newline (`\n`):

```
v1=hex( HMAC-SHA256( key,
        X-Bekup-Timestamp + "\n" + X-Bekup-Delivery + "\n" + X-Bekup-Event + "\n" + raw request body ) )
```

Because the delivery ID and the event type are signed, a captured request
cannot be replayed under another delivery ID (to slip past your duplicate
check) or as another event type. None of the three headers can contain a
newline, so the signed string is unambiguous. (Before bekup 1.0 the
signed string was only `timestamp + "." + body`; that format was never
released.)

A receiver must:

1. Compute the HMAC over the **raw** body bytes, before any JSON parsing or
   re-serialization, with the header values exactly as received.
2. Compare in constant time against every `v1=` entry.
3. **Reject timestamps more than 5 minutes** away from its own clock (the
   replay window; keep the clock in sync with NTP).
4. Optionally remember the `X-Bekup-Delivery` values seen in the last 5
   minutes and drop repeats, so a captured request cannot be replayed even
   inside the window.

**Test vector.** Key `bkwh_dGhpcyBpcyBhbiBleGFtcGxlIGtleSBvbmx5ISEhISE`,
timestamp `1767225600`, delivery ID `dlv_01`, event type `webhook.test`,
body (no trailing newline)

```json
{"id":"evt_01","type":"webhook.test","created_at":"2026-01-01T00:00:00Z","data":{"message":"This is a test delivery from bekup."}}
```

gives `v1=ba73f3d1d38e7e39caef2f7db71ef92aa65c84e158df98b60642729694e3f8aa`.
A second vector: key `0123456789abcdef0123456789abcdef`, timestamp
`1700000000`, delivery ID `d1`, event type `backup.failed`, body `{}` gives
`v1=378998cd93fa9aa63e43535f70c44d01da1b7a49810db1f273d5caa4814a456d`.

**Go**

```go
import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const replayWindow = 5 * time.Minute

// verifyBekup checks a bekup webhook. body must be the raw request body.
func verifyBekup(key string, h http.Header, body []byte, now time.Time) bool {
	tsHeader := h.Get("X-Bekup-Timestamp")
	delivery, event := h.Get("X-Bekup-Delivery"), h.Get("X-Bekup-Event")
	ts, err := strconv.ParseInt(tsHeader, 10, 64)
	if err != nil || len(tsHeader) > 12 || delivery == "" || event == "" {
		return false
	}
	if d := now.Sub(time.Unix(ts, 0)); d > replayWindow || d < -replayWindow {
		return false
	}
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte(tsHeader + "\n" + delivery + "\n" + event + "\n"))
	m.Write(body)
	want := m.Sum(nil)
	for _, part := range strings.Split(h.Get("X-Bekup-Signature"), ",") {
		v, ok := strings.CutPrefix(strings.TrimSpace(part), "v1=")
		if !ok {
			continue
		}
		if got, err := hex.DecodeString(v); err == nil && hmac.Equal(got, want) {
			return true
		}
	}
	return false
}
```

**Python**

```python
import hashlib
import hmac
import time

def verify_bekup(key: str, headers, body: bytes, window: int = 300) -> bool:
    """headers: a case-insensitive mapping; body: the raw request body."""
    ts = headers.get("X-Bekup-Timestamp", "")
    delivery = headers.get("X-Bekup-Delivery", "")
    event = headers.get("X-Bekup-Event", "")
    sig = headers.get("X-Bekup-Signature", "")
    if not ts.isdigit() or len(ts) > 12 or abs(time.time() - int(ts)) > window:
        return False
    if not delivery or not event:
        return False
    signed = f"{ts}\n{delivery}\n{event}\n".encode() + body
    expected = hmac.new(key.encode(), signed, hashlib.sha256).hexdigest().encode()
    return any(
        hmac.compare_digest(part.strip()[3:].lower().encode(), expected)
        for part in sig.split(",")
        if part.strip().startswith("v1=")
    )
```

**Node.js**

```js
import crypto from "node:crypto";

// headers: lower-cased header names (as in Node's req.headers);
// rawBody: a Buffer with the unparsed request body.
export function verifyBekup(key, headers, rawBody, windowSec = 300) {
  const ts = headers["x-bekup-timestamp"] ?? "";
  const delivery = headers["x-bekup-delivery"] ?? "";
  const event = headers["x-bekup-event"] ?? "";
  const sig = headers["x-bekup-signature"] ?? "";
  if (!/^\d{1,12}$/.test(ts) || Math.abs(Date.now() / 1000 - Number(ts)) > windowSec) {
    return false;
  }
  if (!delivery || !event) {
    return false;
  }
  const expected = crypto
    .createHmac("sha256", key)
    .update(`${ts}\n${delivery}\n${event}\n`)
    .update(rawBody)
    .digest();
  return sig
    .split(",")
    .map((s) => s.trim())
    .filter((s) => s.startsWith("v1="))
    .some((s) => {
      const got = Buffer.from(s.slice(3), "hex");
      return got.length === expected.length && crypto.timingSafeEqual(got, expected);
    });
}
```

With Express, read the body with `express.raw({ type: "application/json" })`
so the bytes are not re-serialized before verification.

### Automatic disabling

A webhook that keeps failing is disabled automatically, and its owner (all
admins for a shared webhook) gets the inbox notice
[`notice.webhook_disabled`](#notices) with the reason:

- **10 failed attempts in a row spanning at least 24 hours** (a short
  outage with its retries does not disable a webhook), or
- **5 permanent failures** (a redirect, a `4xx` other than `408`/`429`, a
  refused address or a TLS error) since the last successful delivery
  (`notifications.webhook_failure_threshold`).

Any successful delivery resets the counters. Fix the receiver, send a test
and enable the webhook again in your notification settings
(`reenableMyWebhook`; admins: `enableSharedChannel`).
