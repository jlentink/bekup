# Phase 3 roadmap: copy jobs, scoped dumps, SSO, more engines, remote runners, Kubernetes, i18n

## Context
A gap analysis of bekup against the best database backup tools produced a list
of missing features. The user selected these (numbers from the analysis):
3 copy jobs, 4 scoped dumps, 6 SSO, 7 anomaly detection, 8 remote runners,
10 engines (SQLite, MongoDB, MS SQL), 12 Azure Blob + GCS, 13 self-backup of
bekup, 14 recovery metrics + compliance report, 15 audit export + SIEM,
16 storage forecast, 17 bandwidth limits, 19 typed pre/post actions,
20 masked dev copies, 21 Kubernetes (+ Helm), plus **i18n: English and Dutch**.
Not selected (stay out): PITR, GFS retention, granular restore, native chat
channels, dedup/incremental, config as code.

User decisions (to record as D-NNN, status `accepted` once confirmed in the
decision log):
- **SQLite**: both access paths: pinned SSH to the host running a fixed
  `sqlite3 .backup`, and runner-allowlisted local directories mounted read-only.
- **MS SQL**: `sqlpackage` bacpac export/import (client side, fits the pipeline).
- **Remote runners**: the runner dials out to bekup over mTLS (no inbound port
  at remote sites).
- **Kubernetes**: a native Kubernetes Jobs executor in the runner (no Docker
  socket) plus a Helm chart.

Everything below this line that is not a user decision is the agent's most
secure sensible choice and gets a `proposed` entry.

## Preconditions
- Finish Phase 1 M7 (7.1–7.6) and Phase 2 (P2.x) in
  `plans/backup-databases-abstract-corbato.md` first, incl. SECURITY.md OI-4
  (per-job maximum artifact size). Phase 3 starts on a released, reviewed base.
- This file becomes "Phase 3" in `plans/backup-databases-abstract-corbato.md`
  (tasks appended there, same working rules: one task per agent, ∥ only where
  marked, ≥95 % confidence before checking off, dual review per milestone,
  global AC: unit tests, `just lint`/`sec`/`test`, authz matrix rows for every
  new GraphQL operation, D-NNN + AGENTS.md summary, docs, SECURITY.md on any
  boundary change, the user commits per milestone).

## Branching: one branch per change
Every task below (8.1, 8.2, ..., 14.2) is one change on its own branch.
- **Name:** `feature/p3-<task-id>-<slug>`, e.g. `feature/p3-9.1-anomaly-detection`,
  `feature/p3-12.2-mongodb`. Fixes from reviews stay on the task's branch.
- **Start:** always from an up-to-date `main` (`git switch main && git pull`
  then `git switch -c ...`), never from another feature branch and never from
  a branch with uncommitted work (e.g. the current `feature/accept-risks`
  stays untouched; Phase 3 starts only after the user has merged it).
- **Dependencies:** a task whose `deps:` are not merged into `main` yet waits.
  Stacking a branch on an unmerged one only with the user's explicit consent
  (then rebase onto `main` once the base is merged).
- **Parallel tasks (∥):** each in its own branch, ideally its own git worktree,
  so agents never share a working tree.
- **Per-branch numbers that collide:** goose migration numbers
  (`internal/db/migrations/000NN_*.sql`) and decision ids `D-NNN` are taken
  from `main` at branch start; before handing over, rebase on `main` and
  renumber if another branch took the number (never renumber something already
  merged/released). Same for generated files: re-run `just gen` after a rebase.
- **Hand-over:** the agent never commits or merges (the user does). A branch is
  handed over when its task's AC are met, checks are green, the plan checkbox
  is updated on that branch, and the branch's own **security review + code
  review** found nothing open.
- **Milestone close:** after all branches of a milestone are merged into
  `main`, one more pair of reviews (security + code) runs over the milestone
  as a whole on `main` (cross-branch consistency, AGENTS.md/SECURITY.md/docs
  in sync). Fixes from that go into a branch `feature/p3-m<N>-review-fixes`.

## M8 Internationalization (first, so every later UI task ships nl too)
- **8.1 i18n foundation (web).** (User decision, D-235: typed tables
  instead of i18next.) Every message table written as `defineMessages({ en,
  nl })` (`web/src/lib/i18n.ts`, compiler parity via `Messages<typeof en>`),
  language per page load (stored choice, browser, English; switching
  reloads), `intlLocale` for `Intl`/`lib/format`, language switcher in
  profile, lint rule `bekup/no-ui-literals`, Dutch with "je" and the glossary
  `web/src/lib/i18n-glossary.md`, `pnpm csp-check` with a Dutch pass, setup
  SPA chrome too.
- **8.2 Locale per user.** `users.locale` (`en`|`nl`, migration, DML-only),
  `Accept-Language` fallback before sign-in, GraphQL `updateMyLocale`
  (authz row). Error codes stay codes; the UI maps them.
- **8.3 Server-rendered texts** (user decision: three tasks, D-237).
  **8.3a** notifications, inbox rows, e-mails and the periodic summary in
  the recipient's language; **8.3b** GraphQL messages through a central
  catalog in the error presenter (call sites keep their English text; a
  source-walking test requires a Dutch entry for every message);
  **8.3c** the setup wizard (terminal: `LC_ALL`/`LC_MESSAGES`/`LANG`,
  `--lang` wins; browser setup: Accept-Language).
- **8.4 Docs**: translation guide (`docs/development.md`), contributing a
  language. *deps: 8.1*

## M9 Visibility and limits (∥ after M8)
- **9.1 Backup anomaly detection (7).** Per item: compare artifact size and
  the 3.10 object/row counts with the median of the last N good runs;
  job-level thresholds (defaults: size −50 %/+200 %, any table rows −90 %,
  object count change). Event `backup.anomaly` (catalog, problem flag, owner
  audience), item warning code `anomaly_*`, never changes run status. Pure
  `internal/anomaly` evaluator (table tests, fuzz on stats), run in the finish
  hook. UI: badge on run + job, thresholds in the job editor.
- **9.2 Upload bandwidth limits (17).** Per destination `max_upload_bytes_per_s`
  and `max_concurrent_uploads`, global cap in `bekup.toml` (live key with
  weakening rule = raising/removing the cap is not a weakening; no rule needed
  → document). Token bucket in the pipeline fan-out writer per destination;
  the lag-drop logic must account for throttling (no false drops).
- **9.3 Storage forecast (16).** Robust linear fit over stored artifact sizes
  per job/destination and the D-227 space checks → "full in ~N days",
  destination page + dashboard + summary e-mail; event
  `destination.full_forecast` (dedup per destination/week) when < 14 days.
  Reads only bekup's tables, never a backend.
- **9.4 Recovery metrics + compliance report (14).** Per job: last verified
  restorable (test restore ok), measured restore duration (RTO evidence),
  backup age vs schedule (RPO evidence). Report page (scoped by
  `Policy.JobScope`) with CSV export and a print stylesheet (no PDF library);
  operation `report.view`, export single-root + rate limited.
- **9.5 Audit export + SIEM forwarding (15).** Admin CSV/JSONL export of the
  audit log (step-up, audited `audit.exported`). Forwarder in the workers:
  syslog over TLS (RFC 5425) and HTTPS JSON (HMAC-signed like webhooks),
  at-least-once with a cursor on the audit id, through a new netguard scope
  `siem`; config admin + step-up, audited, alerted on widening; chain hash
  included so the SIEM can check continuity; never secrets (details already
  redacted).

## M10 Backup scope, actions, copies, storage
- **10.1 Scoped dumps (4).** Per job and per-database overrides: include/exclude
  schemas and tables, exclude table data, schema-only. Typed fields in the job
  model, validated identifiers, builders in `internal/engine` only, typed
  runner fields (no free-form args; extend the SQL/args field test), stats and
  completeness warnings aware of exclusions (excluded = notice, not warning),
  manifest records the scope, restore preview says "filtered backup".
- **10.2 Typed pre/post actions (19).** Actions per job: `before_dump`,
  `after_success`, `after_failure`; kind `webhook` only (signed v1 like
  notification webhooks, through a netguard scope `job_actions`, timeout,
  bounded response ignored); `before_dump` failure policy `abort|continue`.
  No shell, no SQL. Staff + step-up to edit, audited, run log entries with
  fixed codes.
- **10.3 Copy jobs (3).** New entity: copy policy per job (source destination
  set → target destination, trigger `after_run` | schedule | manual, optional
  "only verified complete runs"). River kind `copy_run`: stage via
  `pipeline.Verify` with a DB binding, stream artifacts byte-identical to the
  target (`Put` never overwrites), re-verify digest on the target, write the
  manifest last, `MarkSigned` on the target, artifact rows on the new
  destination; "repair partial run" action fills destinations a run missed.
  Holds on source artifacts during the copy (`AcquireHoldTx`). Retention per
  destination applies unchanged. Bandwidth limits from 9.2 apply. Audit,
  events, notifications (`copy.failed`), metrics, UI on job + run pages.
- **10.4 Azure Blob + Google Cloud Storage (12).** `storage/azblob` (shared
  key or SAS only; never managed identity/env credentials; immutability
  policies + legal hold mapped to lock options) and `storage/gcs` (service
  account key from the sealed blob or HMAC; never ADC/metadata server;
  retention policy / object hold). Both through `Guard.NewHTTPClient`, pass
  `storagetest.Run` (emulators Azurite + fake-gcs-server), offline `reindex`
  sections, destinations service + UI + docs. Evaluate SDK dependency weight;
  if the SDK reads env/metadata credentials implicitly, use a minimal own
  client (like WebDAV).
- **10.5 Self-backup of bekup (13).** A built-in system job: dump of the app
  database through the runner with a new read-only role `bekup_backup`
  (bootstrap + role checks + init script), encrypted to an **escrowed age
  recipient** configured by the admin (never the master key: master-key loss
  is the scenario), to any destination; plus `bekup keys verify-escrow`
  (checks an escrowed master key file against the key-check values) and a
  monthly reminder alert until verified. Recovery doc: rebuild bekup from the
  self-backup + escrowed master key. *deps: 10.3 not required*

## M11 Single sign-on (6)
- **11.1 OIDC login.** Authorization code + PKCE + nonce + state (server-side,
  session-bound, single use), discovery and JWKS fetched through a netguard
  scope `oidc`, issuer pinned, `id_token` verified (alg allowlist, aud, exp,
  iat skew); one or more providers configured by admins (client secret
  sealed, write-only, step-up, audited).
- **11.2 Accounts and roles.** Link by verified `sub` (never by e-mail alone);
  just-in-time provisioning **off by default**; group-claim → role mapping;
  granting `admin` via SSO needs an explicit mapping and raises an alert per
  new admin; a local break-glass admin always remains; disabling the provider
  revokes its sessions.
- **11.3 Assurance.** SSO sign-in counts as the first factor; bekup's second
  factor is still required by the 2FA policy, and restores/step-up keep
  needing TOTP or a UV passkey (FreshAAL2 unaffected by `amr` claims).
  Restrictions, rate limits, audit (`auth.sso_*`), security alerts, authz
  rows, UI (login buttons, admin provider page), docs (Keycloak, Entra ID,
  Authentik, Google examples), SECURITY.md (new boundary: IdP).

## M12 Engines: MongoDB, SQLite, MS SQL (10)
Common definition of done per engine: catalog entries (digest-pinned images,
version floors), dump + restore builders with hardening flags, runner
validation + pre-checks (Go driver probe: version, database list, privilege
verdict), TLS pinning, SSH tunnel, stats (approximate allowed), restore
(new DB + overwrite with temp + swap where the engine allows), test restores
with `CompareStats`, `failureTexts`, docs, integration round trips, SECURITY.md
rows for restore-time risks. Schema-version presets optional per engine.
- **12.1 Engine abstraction pass.** Find PG/MySQL assumptions in jobs,
  restore, stats, privileges, UI (engine switches) and make them engine
  capabilities (`engine.Capabilities`: globals, overwrite mode, stats
  precision, scoped dumps support). *deps: M10.1*
- **12.2 MongoDB.** `mongodump --archive` (`--oplog` on replica sets for
  consistency), `mongorestore --archive` with `--nsInclude` scoping and
  `--drop` only for overwrite into a temp database + rename where possible;
  privilege verdict from `rolesInfo` (gated: `root`, `__system`,
  `clusterAdmin`, ...).
- **12.3 MS SQL Server.** `sqlpackage /Action:Export` (bacpac) through the
  official image; import only into a new database, overwrite via temp DB +
  `ALTER DATABASE ... MODIFY NAME` swap; consistency warning (bacpac is not
  transactional) with an option to export from a database snapshot when
  available; privilege verdict (`sysadmin`, `CONTROL SERVER`, ...); TLS with
  pinned certificate (`TrustServerCertificate` never).
- **12.4 SQLite.** New connection kind `file`:
  (a) **SSH**: pinned host key, fixed remote command `sqlite3 <path>
  ".backup /dev/stdout"`-equivalent (implemented as a fixed argv with a
  validated absolute path, no shell interpolation; streaming via a temp file
  removed after), runner-side SSH policy from `internal/sshpolicy`, a new
  runner allowlist `[targets] ssh_exec_hosts`;
  (b) **local**: `runner.toml` `[sqlite] allowed_dirs` mounted read-only into
  the dump container, path must resolve inside an allowed dir (no symlinks,
  `os.Root`). Integrity check (`PRAGMA integrity_check`) on the copy; restore
  = write a new file via the same path rules (overwrite = temp file + atomic
  rename), Kubernetes executor: PVC allowlist instead of dirs.

## M13 Remote runners and Kubernetes (8, 21)
- **13.1 Runner registry.** `runners` table (id, name, site, last seen,
  version, status), connections bound to a runner, runner-scoped target
  allowlists stay in each runner's own `runner.toml`; SPIFFE ID
  `spiffe://bekup/runner/<id>`; certificates issued offline with
  `bekup certs issue runner --id` (CA key stays offline); admin UI to
  register/disable (step-up, audited, alerted).
- **13.2 Dial-out transport.** New component `bekup runner-gateway` (or a
  serve/worker listener, decided in the D-NNN) on its own port: remote runners
  connect out with mTLS (TLS 1.3, SAN = registered id), the gateway
  multiplexes the existing typed gRPC API back over that connection (runner
  remains the gRPC server; streams carried over a yamux-style session),
  keepalive, reconnect with backoff, revoked/disabled runner refused at
  handshake. Workers reach any runner through the gateway; the local runner
  keeps today's direct mode. `methodClasses`, deadlines, `ZeroSecrets`
  unchanged. Proxies: document the gateway port (TCP passthrough, not HTTP).
- **13.3 Scheduling across runners.** Run slots per runner, health per runner
  on the dashboard, missed-backup reason `runner_offline`, metrics per runner
  (bounded label = runner id behind a flag like per-job labels).
- **13.4 Kubernetes executor.** `executor.Kubernetes` behind the same
  `Executor` interface: per run a Pod with the digest-pinned image, restricted
  Pod Security (non-root, read-only root, drop ALL, seccomp RuntimeDefault, no
  service account token), a per-run NetworkPolicy allowing only the target
  (or the runner's proxy), secrets via the stdin preamble over
  `pods/attach`, cleanup + reaper by label; runner ServiceAccount limited to
  its namespace (pods, pods/attach, pods/log, networkpolicies). Integration
  tests on kind/k3d.
- **13.5 Helm chart.** serve, worker, runner (Kubernetes executor),
  external-api (optional), app Postgres optional (or external, D-207),
  secrets from Kubernetes Secrets mounted as files, ingress examples, chart
  lint + `helm template` golden tests + a CI kind install smoke test; signed
  chart (cosign) in the release workflow. Docs `docs/installation/kubernetes.md`.

## M14 Masked development copies (20)
- **14.1 Masking rules.** Per job a rule set: table.column → strategy
  (`null`, `fixed`, `hash` (keyed HMAC, per-copy key), `fake_email`,
  `fake_name`, `truncate_table`). Stored typed, validated identifiers; no
  user SQL (D-210 stays the only user SQL).
- **14.2 Masked restore.** Restore mode "masked copy" into a **new** database
  only: restore → neutralize (D-171) → runner RPC `MaskRestoredDatabase`
  builds statements with `engine.Quote*` from the typed rules inside one
  transaction → only then the database is announced. Failure drops the
  database. Staff + FreshAAL2; owners only if the job allows it; audited
  (`restore.masked`); the copy is labeled masked. PG and MySQL/MariaDB first,
  other engines per capability.

## Decisions to record
D-NNN entries (user decisions above as accepted after confirmation, the rest
proposed): i18n library + locale storage; anomaly thresholds; bandwidth model;
forecast method; report format; SIEM transports + scope; scoped dump fields;
job actions (webhook only); copy jobs model; Azure/GCS credential policy;
self-backup role + escrow recipient; OIDC design + JIT/admin rules; engine
capabilities; MongoDB/MS SQL/SQLite designs (SQLite SSH exec is a new runner
capability: SECURITY.md row); runner registry + dial-out gateway (new trust
boundary); Kubernetes executor + RBAC; masking strategies.

## Critical files (representative)
- Engines: `internal/engine/` (catalog, builders, floors), `internal/runner/`
  (`validate`, `spec`, `executor`, `dbprobe`, `ops*.go`), `proto/runner/v1/runner.proto`.
- Pipeline/storage: `internal/pipeline/`, `internal/storage/` (+ new
  `azblob`, `gcs`), `internal/destinations/`, `internal/retention/`.
- Jobs/runs/restore: `internal/jobs/`, `internal/runstate/`, `internal/restore/`.
- Auth: `internal/auth/` (new `oidc` subpackage), `internal/authz/` (operations,
  policy, `matrix_test.go`), `internal/graph/schema/*.graphqls`.
- Notify/monitor: `internal/notify/` (catalog, security.go), `internal/monitor/`.
- Web: `web/src/features/*`, `web/src/lib/messages.ts` (replaced by catalogs).
- Deploy: `docker-compose.yml`, `internal/setup/` (variants, manifest, lint),
  new `deploy/helm/`.

## Verification
Per task: unit + fuzz tests, `just lint`, `just sec`, `just test`, integration
tests (`just test-int`, testcontainers / kind) and docs. Per engine: round trip
backup → restore → data equality, test restore stats match. Copy jobs: copy a
run to a second destination, delete the original, restore from the copy.
Remote runner: runner in a separate Docker network behind NAT dials out,
backs up a target only it can reach. Kubernetes: kind cluster, Helm install,
backup + restore through Pods, NetworkPolicy blocks a non-target. SSO:
Keycloak container, login, group mapping, restore still asks for TOTP/passkey.
i18n: switch to nl, walk all pages (`pnpm csp-check`), Dutch e-mail goldens.
Every branch gets its own security review + code review before hand-over;
every milestone gets a final pair of reviews on `main` after its branches are
merged (see "Branching").
