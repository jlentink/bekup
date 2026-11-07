# Accepted security risks + API tokens that never expire

## Context

bekup flags every weakened setting (D-001): plain-HTTP base URL, app-DB TLS
`require`/`disable`, single-account DB mode, a followed (`:latest`) image,
connections in UNVERIFIED/DISABLED TLS, privileged target roles, SMTP
`auto`/`none`, job encryption opt-out. Operators can't always fix their
environment, so the flags become permanent noise: daily posture e-mails,
open items on the dashboard Security tile, and counts in the summary e-mail.

Goal: admins (and backup owners for their own jobs) can **accept** a risk for
**1 month, 1 year or forever**. An accepted risk is no longer an open issue
(dashboard, e-mails, summary). All acceptances are listed on a separate
**Accepted risks** page. In addition, external **API tokens get a "never
expires" option**.

User decisions (2026-11-07):
- Acceptable kinds: the 4 instance posture kinds, connection TLS, privileged role, SMTP unencrypted, job encryption opt-out.
- Only admins accept connection, SMTP and instance risks. Owners (role `user`) may accept only risks on jobs they own, i.e. the encryption opt-out. Maintainers can't accept or revoke.
- The restore / test-restore privileged-target acknowledgement gate stays as it is.
- Badges and the HTTP banner stay but switch to a muted "Accepted until … / Accepted (no end date)" variant, so D-001's permanent badge is kept.
- Page access: admins and maintainers see all acceptances (maintainers read-only); owners see their own jobs'.
- An alert goes out 7 days before a month/year acceptance expires, and another when it ends.
- The admin summary e-mail gets one count line.
- Admins and owners (the current `api_token.create` set) may create never-expiring tokens; they raise an alert and get a permanent badge.

## Design

### 1. Migration `00044_risk_acceptances.sql` (the token migration became 00043)
- Enums:
  - `risk_kind`: `insecure_transport`, `insecure_db_tls`, `insecure_db_roles`, `unpinned_image`, `connection_tls`, `privileged_role`, `smtp_unencrypted`, `encryption_opt_out`.
  - `risk_duration`: `month`, `year`, `forever`.
  - `risk_end_reason`: `revoked`, `superseded`, `expired`, `condition_changed`, `resolved`, `subject_deleted`, `acceptor_lost_rights`.
- Table `risk_acceptances`:
  - `id`, `kind`
  - `connection_id` / `job_id`: nullable FKs. The subject CHECK is per kind. Instance and SMTP risks have both NULL.
  - `condition_hash bytea(32)` and `condition text ≤300` (a display form).
  - `duration`, `reason text 1–500`, `created_by`, `created_at`
  - `expires_at` (NULL = forever). A CHECK ties it to the duration on the DB clock: `created_at + 1 month` or `+ 1 year`.
  - `warned_at` (when the pre-expiry alert went out)
  - `ended_at`, `ended_by`, `end_reason`, with a consistency CHECK.
- A partial unique index allows one live acceptance per (kind, subject).
- Guard trigger modelled on `api_tokens_guard` (`00037_api_access.sql:142`): every column except `warned_at` and the end columns is immutable, and ending is final.
- Grants:
  - Revoke ALL from PUBLIC.
  - Revoke UPDATE/DELETE/TRUNCATE from `bekup_app`, then `GRANT UPDATE (warned_at, ended_at, ended_by, end_reason)`. Rows are never deleted.
  - The guard function is revoked from PUBLIC and `bekup_app`.
  - Template for this: `00040_user_summary_settings.sql`.
- Down migration: drop the table and the types.

### 2. Package `internal/riskaccept` (the only reader/writer)
- **Queries:** `internal/db/queries/riskaccept.sql` holds the insert, lock-live, end-live, warn, list (all / owner via `job_owners`) and covered-lookup statements. It also holds one read-only subjects query: connections with non-pinned TLS or a gated privilege verdict, and opted-out jobs.
- **Conditions:** `condition.go` has pure builders. The canonical encoding is `"bekup-risk-v1\0kind\0k=v…"`, sorted, then SHA-256. Fields that reopen the risk when they change:

  | Kind | Fields |
  |---|---|
  | `connection_tls` | tls_mode, host, port |
  | `privileged_role` | status, sorted reasons, host, port, username |
  | `encryption_opt_out` | opted_out_at |
  | `smtp_unencrypted` | mode, host, port |
  | `insecure_db_tls` | tls_mode, ca set |
  | `insecure_db_roles` | – |
  | `unpinned_image` | – |
  | `insecure_transport` | – |

- **`Covered(ctx, conds)`:** one batched query. A condition counts as covered only when the acceptance is live, not expired (DB clock), the hash matches, and the acceptor still holds the right: active, and admin (or a `user` who still owns the job). A lookup error means "not covered" (fail closed).
- **`Findings(ctx, scope)`:** the current acceptable risks plus any acceptance. It merges the subjects query, `email.Settings` and a `Posture func() []Condition` from the app. Owners are filtered by `authz.JobScope`. SMTP and instance risks are shown to staff only.
- **`Accept(ctx, {Kind, SubjectID, ConditionID, Duration, Reason})`:**
  - The server rebuilds the condition from the DB or config. A changed `ConditionID` is a CONFLICT; a condition that is no longer a risk is a VALIDATION error.
  - Authorizes `risk.accept`. Job kinds use `authz.Job{ID}` loaded from the DB. Other kinds pass no job, so owners fail; the service also refuses non-admins explicitly.
  - One transaction: lock the live row, mark it `superseded`, insert the new row, `RecordTx risk.accepted` (fail closed).
- **`Revoke(ctx, id)`** checks `risk.revoke` with the row's job, ends the row as `revoked`, and audits `risk.acceptance_revoked`.
- **`List(ctx, status, first)`** checks `risk.list` and filters by OwnerList + `JobScope`.
- **`Sweep(ctx, now)`** runs inside the hourly `monitor.SecurityChecker.Run`, as the system actor, using conditional UPDATE … RETURNING so only one process audits each change:
  - Within 7 days of expiry, it sets `warned_at` and audits `risk.acceptance_expiring`.
  - It ends rows as `expired` or `acceptor_lost_rights`.
  - For connection and job subjects only, it also ends rows as `condition_changed`, `resolved` or `subject_deleted`.
  - Instance kinds are never ended on a mismatch: processes may differ, so each process checks `Covered` against its own config.
- **End at once:** `connections.Service` (SetTLSMode / Update when host, port or user changes) and `jobkeys` opt-in end the live acceptances in the same transaction, through a small `RiskEnder` hook. Changing a condition and quickly changing it back can't silently re-cover it.

### 3. Authz, audit, alerts
- **Operations** in `internal/authz/operations.go`, with rows in `matrix_test.go` and `graph/policy_access.go`:

  | Operation | Rule | Matrix |
  |---|---|---|
  | `risk.list` | staff + OwnerList | `YYYYYU` |
  | `risk.accept` | adminOnly + OwnerJob + FreshStepUp | `YFFYFU`, S |
  | `risk.revoke` | adminOnly + OwnerJob + FreshStepUp | `YFFYFU`, S |

  Add policy tests for an owner refused on a foreign job and on non-job kinds. `OpAlertAcknowledge` stays reserved.
- **Audit:** `risk.accepted`, `risk.acceptance_revoked`, `risk.acceptance_expiring` and `risk.acceptance_ended` go in the `internal/audit/actions.go` catalog, target type `risk_acceptance`. Detail names must pass `TestDetailNamesNotRedacted`. The reason is recorded as `note`.
- **Alerts** in `internal/notify/security.go`, `content.go` and `alert_types.go`, each with a test row and a row in the `docs/guides/notifications.md` table:

  | Alert | Raised when | Severity |
  |---|---|---|
  | `risk_accepted` | a month or year acceptance is made | warning |
  | `risk_accepted_permanently` | a forever acceptance is made | error |
  | `risk_acceptance_expiring` | 7 days before expiry | warning |
  | `risk_acceptance_ended` | expired, condition_changed or acceptor_lost_rights | warning |

  `risk_acceptance_expiring` and `risk_acceptance_ended` go to admins plus a `security.account` alert to the acceptor. Revoking raises no alert. Alert details never carry the reason.

### 4. Where flagging consults acceptances
1. **Posture source.** `internal/app/security.go`: `weakenedPosture()` becomes `postureConditions() []riskaccept.Condition`. `monitor.SecurityOptions` gets `Posture` (new type) and `Risks`.
2. **Hourly check.** `monitor.SecurityChecker.checkPosture` (`internal/monitor/security.go:228`) runs `Sweep` first, then skips covered kinds.
3. **Startup audits.** `internal/app/audit.go` (`auditInsecure*`, `recordUnpinnedImage`, the runner image observe path) still record fail closed, now with the details `risk_accepted`, `risk_acceptance_id` and `accepted_until`. `securityFromAudit` drops the alert when `risk_accepted` is true. The WARN log lines stay.
4. **Summary e-mail.** The count query is unchanged: accepted posture produces no events. For admins, `internal/notify/summary/content.go` and `build.go` add one line: "Accepted risks: N (M without end date) · API tokens without expiry: K". Re-run the goldens and stay within `maxSummaryHTML`.
5. **Dashboard.** The new server-side `Query.securityRisks` replaces the client-side derivation of the `unencrypted`, `insecure-transport` and `privileged-target` findings in `web/src/features/dashboard/model.ts:387`. Instance-posture and SMTP items are added for staff. Accepted items leave the open count and show as "N accepted → view". Identity mismatch and pin expiry are unchanged.
6. **Badges.**
   - New batch-loaded fields:
     - `Connection.acceptedRisks`
     - `JobEncryption.acceptance`
     - `SmtpSettings.acceptance`
     - `DatabasePosture.tlsAcceptance` and `singleAccountAcceptance`
     - `ImagePosture.acceptance`
     - `ConfigStatus.insecureTransportAcceptance`
   - Never on the public `ServerInfo`.
   - The HTTP banner gets its "accepted" text from a signed-in-only field.
7. **Unchanged:** the restore and test-restore privileged acknowledgement, the one-time alerts sent at the moment of a change (e.g. `security.opt_out`), and the audit entries.

### 5. GraphQL and UI
- **Schema.** New `internal/graph/schema/riskaccept.graphqls`:
  - Enums `RiskKind`, `RiskDuration { ONE_MONTH ONE_YEAR FOREVER }`, `RiskAcceptanceStatus`.
  - `RiskAcceptance`: kind, condition, reason, duration, createdAt, expiresAt, createdBy, status, endedAt, endReason, connection, job.
  - `SecurityRisk`: kind, conditionId, condition, severity, connection, job, acceptance, canAccept.
  - Queries `securityRisks` and `riskAcceptances(status, first ≤ 500)`, for ADMIN, MAINTAINER and USER.
  - Mutations `acceptRisk(input)` and `revokeRiskAcceptance(id)`, for ADMIN and USER with `@stepUp`.
  - Resolvers in `internal/graph/resolvers/riskaccept.go` only translate. Run `just gen`.
- **Web** (`web/src/features/accepted-risks/`):
  - Files: `routes.tsx`, `accepted-risks-page.tsx`, `accept-dialog.tsx`, `operations.ts`, `model.ts`, `messages.ts` and tests.
  - Nav entry in `components/shell/nav.ts` (ALL), with `handle: access.nav`.
  - The page lists kind, subject link, condition, reason, accepted by, the end date (or a "No end date" badge) and status. It filters by Active / Ended and has a Revoke button behind `withStepUp`.
  - The accept dialog offers 1 month / 1 year / forever (forever shows a warning) and a reason (zod, 1–500). It runs through `withStepUp`. A CONFLICT shows "the condition changed".
  - The dialog opens from the dashboard tile, connection detail, the job encryption panel, the configuration page and the SMTP page.
  - `components/status/security-badge.tsx` gets an `accepted` variant: the hazard look stays, plus a check and the end date. Use frontend-design; keep `pnpm csp-check` green.

### 6. API tokens that never expire: migration `00043_api_access_never_expires.sql`
- **Database:**
  - `api_tokens.expires_at` drops NOT NULL; add `never_expires boolean NOT NULL DEFAULT false`.
  - Replace the CHECK with: `never_expires = (expires_at IS NULL)`, and when an expiry is set it stays within 366 days.
  - `CREATE OR REPLACE api_tokens_guard()` (`never_expires` is immutable) and `public.api_token_valid` (`expires_at IS NULL OR expires_at > now()`). The signatures don't change, so the grants, `bekup_sync_external_grants` and `db.externalFunctions` stay as they are.
- **Go:**
  - `apiaccess.CreateInput.NeverExpires`. `checkExpiry` requires exactly one of `NeverExpires` and `ExpiresAt`, mirroring `allowAnyAddress`.
  - `Token.ExpiresAt *time.Time`; update the rows and the queries in `api_access.sql`.
  - Audit `never_expires`.
- **External API:**
  - `internal/externalapi/store.go:131`: a nil expiry is no longer a 401.
  - `handler.go` `Self`: `expiresAt` becomes nullable and gains `neverExpires`.
- **GraphQL:** `CreateApiTokenInput.neverExpires: Boolean!`; `expiresAt: Time` becomes nullable in the input and the output.
- **Alert:** a new `api_token_never_expires` (error) raised from `api_token.created`. Its body says "never expires".
- **UI** (`web/src/features/api-access`):
  - A "Never" preset with a warning.
  - `tokenState` handles null.
  - The list shows "Never expires" with a `never-expires` `SecurityBadge` flag.
  - Tests.
- **Docs:** `docs/guides/external-api.md` (expiry and the `/self` example).

### 7. Docs and bookkeeping
- `docs/DECISIONS.md`:
  - **D-228** Accepted security risks (proposed; user decisions above).
  - **D-229** API tokens without expiry (proposed; it reverses D-220's "tokens without expiry rejected"). D-220's index status gets "amended by D-229".
- `SECURITY.md`: the threat (a hijacked session hides a risk) and its mitigations (step-up, audit, alerts, the badge kept, fingerprint reopening, the summary line); never-expiring tokens.
- `AGENTS.md`: the `riskaccept` layout entry and a convention bullet (a new acceptable kind needs a builder, an enum value, the subject CHECK, a Findings source and a badge); update the D-220 expiry bullets.
- New guide `docs/guides/accepted-risks.md`, the notifications alert table, and `docs/security/audit.md`.
- Plan tasks **6.22** (accepted risks) and **6.23** (never-expiring tokens) in `plans/backup-databases-abstract-corbato.md`, then the milestone's dual review (a security agent and a code agent).

## Verification
- **Go unit tests:**
  - The condition builders, table-driven, plus a fuzz test of the canonical encoding.
  - `Covered`: expired, hash mismatch, acceptor disabled / demoted / no longer owner.
  - `Sweep`: every end reason, the expiring warning sent once, two processes racing so only one audits.
  - `checkPosture` skips covered kinds.
  - `securityFromAudit` drops the alert on `risk_accepted`.
  - `security_test.go` and `notify_test.go` rows.
  - The authz matrix and policy rows; `checkExpiry` exactly-one.
- **Integration tests** (`just test-int`, `DOCKER_API_VERSION=1.47`):
  - Migrations 00043 (tokens) and 00044 (risks): the guard refuses immutable updates, DELETE is refused for `bekup_app`, the expiry CHECKs hold, one live row per subject.
  - Accept: CONFLICT on a changed condition, supersede, an owner refused for non-job kinds.
  - `RiskEnder` on TLS changes.
  - External API with a never-expiring token: authenticate, request_run, `/self` returns `expiresAt: null`.
- **Web tests:**
  - The accept dialog with step-up and CONFLICT.
  - The page per role (admin / maintainer read-only / owner).
  - The dashboard split between open and accepted.
  - The accepted badge variant.
  - The "never" token preset.
  - Summary goldens updated (`go test -update`), email width check.
- **Checks:** `just gen`, `just lint`, `just sec`, `just test`, `pnpm csp-check` all green.
- **Manual:** `just dev` + `just run`. Set a connection to UNVERIFIED and accept it for 1 month from the dashboard. The tile item moves to "accepted", the connection badge shows the end date, and the risk appears on the Accepted risks page. Switch it to DISABLED: it is flagged again. Run `security_check` with the app DB at tls_mode `require` and the risk accepted: no reminder in mailpit. Create a never-expiring API token and call `/api/v1/self`.
