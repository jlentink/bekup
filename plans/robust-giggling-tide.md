# Plan: external PostgreSQL + Bubble Tea setup wizard

## Context

What the user asked for (2026-10-30):
- **Existing Postgres:** connect to your own Postgres with plain connection fields (host, port, name, user, password file). Passwords come only from files, never inline or from the environment.
- **Wizard:** an interactive setup that runs inside the bekup image, built on Bubble Tea. It asks whether you use an external proxy and an existing database, then writes everything, `compose.yaml` included. The same wizard guides upgrades.
- **The admin account the user gives** (host, user, password) supports two modes ("both"):
  - **Bootstrap (default):** a one-time `bekup db bootstrap` uses the admin account to create the database and the two least-privilege roles (`bekup_owner`, `bekup_app`) with generated passwords. The admin password is not kept.
  - **Single account (opt-in):** bekup runs migrations and the app as that one user. This is an explicit weakening: startup warning, audit entry, security alert and a UI badge, because the app could then change its own schema and audit log.
- **Accepted defaults from the external-DB design:**
  - fixed role names (one bekup per cluster);
  - SCRAM channel binding stays required, so poolers are unsupported;
  - `tls_ca_file` may name a provider bundle or the system bundle file;
  - optional `tls_server_name`;
  - the roles SQL uses SCRAM verifiers, never plaintext;
  - 6 compose files: 3 proxy variants × bundled or external DB.

## Part E: external database backend (D-207, proposed)

The design agent's proposal (sections 1–3 and 5), plus the user's additions.

- **Config.** `[database]` fields mode: `host`, `port`, `name`, `user`, `password_file`, `owner_user`, `owner_password_file` and `tls_server_name`.
  - Fields mode and URL mode are mutually exclusive, enforced in a normalize step that knows each key's source.
  - New `DBCredentials` replaces `DatabaseURL()`.
- **`internal/db` connection builder:**
  - builds the pgx config from the fields, not from a DSN string, and ignores the PG* environment;
  - the TLS policy is shared by both modes;
  - the startup parameter `search_path=public`;
  - one builder for the pool, the connector and the migrator.
- **Commands and checks:**
  - `internal/db/scram.go` and `rolesql.go`, plus `bekup db roles-sql`;
  - a `bekup migrate check` preflight that `migrate up` also runs.
- **`bekup db bootstrap`** (new): takes the admin credentials (host, port, user, admin password from a file or a TTY prompt, admin database).
  - It connects with the same TLS policy, then creates the roles with SCRAM verifiers of passwords that are generated or supplied, the database, the grants and, if possible, citext. It runs the D-099 checks afterwards.
  - It is idempotent and never logs or stores the admin password.
  - It works with managed providers' admin roles (`rds_superuser`, `cloudsqlsuperuser`, `azure_pg_admin`, or CREATEROLE+CREATEDB).
- **Single-account mode:** `database.single_account = true` plus `user`/`password_file`, with no owner credentials.
  - Feasibility must be checked: released migrations GRANT to `bekup_app`/`bekup_owner`, so the roles may have to exist as NOLOGIN roles, or the mode is limited to accounts that can create roles.
  - The weakening treatment: startup WARN, an audit entry (`security.insecure_db_roles` or similar, fail closed), a daily alert, an admin `ServerInfo` badge, documentation; the D-099 checks adjusted for this mode only.
  - If it isn't feasible without editing released migrations, stop and report with options.
- **Startup:** log the TLS facts. Optionally add an admin `ServerInfo` badge for weakened DB transport or roles.
- **Tests:**
  - config table and fuzz tests;
  - the builder ignores hostile PG* environment variables (reflection test);
  - special-character passwords, with no secret leaks;
  - the TLS matrix;
  - the SCRAM test vector, roles-sql golden files, bootstrap;
  - integration against plain `postgres:15/16/17` containers with a test CA and a non-superuser CREATEROLE+CREATEDB admin: bootstrap → migrate → serve checks; single-account mode; negative cases.
- **Docs:** `docs/installation/external-database.md` with provider sections; updates to `configuration.md`, `app-db-tls.md`, `db-roles.md`, SECURITY.md, AGENTS.md; D-207.

## Part S: setup DB dimension + Bubble Tea wizard + guided upgrade (D-208, proposed)

Depends on Part E.

- **Setup DB dimension** (design section 4):
  - `DBMode bundled|external` overlays and 6 committed and released compose files;
  - manifest, state (`database_mode`), check, upgrade (no mode switch);
  - the non-internal `backend` network for external mode, the CA copy, password files.
- **Wizard** (`bekup setup`, no subcommand, on a TTY), built with charmbracelet `bubbletea` + `huh` (forms) + `lipgloss`. Pin the versions, check the licenses (MIT), and add a doctor/sec check.
  - **Steps:** welcome → proxy (own Traefik / existing Traefik / Nginx Proxy Manager / none) → domain (+ ACME e-mail for own Traefik) → database (bundled / existing) → for an existing database: host, port, name, CA file, and how to authenticate (bootstrap with admin credentials / roles already exist / print SQL for my DBA / single account (weakening, explained)) → Docker GID → CA passphrase (generated or entered) → summary → write.
  - **Writes** everything including `compose.yaml` (setup-first `--write-compose` path) through the same library code as the flag-based init.
  - **Runs the bootstrap** for an existing database, if chosen. The setup container has `network_mode: none`, so it prints `docker compose run --rm migrate db bootstrap …` (or runs bootstrap from the migrate service) instead of connecting from the setup container.
  - **Ends** with the exact next commands.
  - Non-TTY or `--non-interactive` falls back to the flag mode. Every screen is accessible (no color-only cues) and supports Esc/back.
- **Key and certificate generation in the wizard** (user request): one consent screen per item.
  - **Master encryption key:** generate a new keyring (default) or import an existing keyring file (reinstall), validated with the key-check values. Then show the key ID and the escrow guidance (back it up now).
  - **Internal CA + mTLS certificates (runner, serve):**
    - generate a new private CA, passphrase-protected (generated and shown once, or entered twice), or use an existing CA mounted at `secrets/certs/ca` (offline media) with its passphrase;
    - shows the fingerprints;
    - recommends moving the CA directory offline.
  - **Bundled Postgres TLS certificate and DB passwords** (bundled DB only): generate them; for an external DB this is replaced by the DB screens of Part E.
  - **Not offered:** proxy TLS (own Traefik uses Let's Encrypt; external proxies manage their own).
  - Each "no" is explained: what the operator must provide instead and where.
  - Secret files are written with the manifest owners and modes, never shown again.
- **Certificate renewal through the CLI** (user request: guided, simple): `bekup setup renew`, the same wizard UI, with `--non-interactive` flags.
  - It lists all internal certificates (runner, serve, postgres, CA) with their expiry. It warns from 30 days before expiry; that threshold is shared with the existing hourly security check (`monitor.SecurityChecker`) and its notification.
  - It asks for the CA (mount or path) and the passphrase, then renews the expiring leaf certificates (or all of them, if chosen) through `internal/certs` (`certs rotate` for leaves). It sets the owners and modes through the ownership code.
  - It prints exactly which services to restart (`docker compose up -d --force-recreate runner serve worker postgres`, only those affected).
  - **CA renewal:** offers the existing stage → promote → finish CA rotation (D-090) as a guided multi-step flow, resumable with a state hint.
  - The CA key stays offline: no automatic renewal.
  - Renewals are not audited (setup has no network or database access); they print the new fingerprints as evidence; `bekup certs rotate` where the app DB is reachable stays the audited path.
  - `setup check` and the dashboard point to `setup renew` when a certificate is expiring.
- **Guided upgrade** (`bekup setup upgrade` on a TTY): current vs new version, compose diff summary, new secret groups, restart-required config, a backup reminder, confirm, write; then prints `docker compose pull && docker compose up -d`.
- **README:** the main path becomes one `docker run … setup` command (hardened flags), then `docker compose up -d`. The download-a-compose-file path stays as the advanced option.
- **Tests:**
  - wizard models driven programmatically: the `teatest` / huh model tests per path, so every combination of answers produces the same files as the flag mode (golden comparison);
  - a no-TTY fallback test;
  - the upgrade wizard;
  - the key/certificate consent paths (generate vs import vs mount existing CA) and `setup renew` (expiry detection, leaf renewal with a mounted test CA, owner/mode results, the restart list, the CA rotation steps), each in TTY and non-interactive form;
  - the CI end-to-end flow for the external DB (a TLS Postgres container, bootstrap, up, admin create).

## Part R: retention / disk cleanup bug (D-209, proposed)

The investigation found that the deletion path works (dumps, then `manifest.json`, then empty run/job directories). Files stay because of these causes and decisions:

- **A. Keep rules.** "Keep last N" and "keep N days" keep a backup if *either* rule keeps it (unchanged; user decision).
  - The job editor shows a live preview: "with these settings about X backups per destination are kept", computed from the schedule.
  - Its hints say the two rules add up.
  - New jobs default to keep last 7 with **no** day limit.
  - Existing jobs are unchanged. Editing retention triggers a retention sweep for that job (new: today the new values wait for the next post-run or daily sweep).
- **B. Partial runs.** The current behaviour stays (incomplete runs newer than the newest complete run are kept). New: when retention keeps runs only for that reason, or a destination has no complete run, show it:
  - a job-level warning on the job page and dashboard ("retention is holding N incomplete runs on <destination> since <date>: no complete backup");
  - a notification event `retention.blocked` (problem group, deduplicated per job/destination per day; catalog plus prefs).
- **C. Unrecorded files and folders:**
  - **Empty folders:** the daily cleanup prunes empty run and job directories (disk and SFTP), only inside bekup's layout and only directories older than 24 h. It goes through `os.Root` / Lstat (no symlinks) and is never applied to the destination root.
  - **Orphan files:** a daily scan compares files in bekup's layout (`{slug}/{timestamp}/…`) with artifact rows and records orphans older than 24 h in a new table (path, size, first seen; key names only).
    - The UI: an admin page per destination ("Unrecorded files"), plus a destination warning.
    - An admin delete action with step-up, audited, re-checked at delete time, in no-delete mode refused.
    - Orphans are never deleted automatically. Covers disk and SFTP; S3 too, by listing the prefix, if cheap.
  - **Runs stuck in `running`** after the final River attempt: the stuck sweep marks them `interrupted` (like restores), so retention sees their files.
- **D. Path changes.** The disk path, SFTP base path and S3 prefix of a destination can only change when no `present` artifacts remain there. VALIDATION error with an explanation, enforced in `destinations.Service`.
- **Tests:**
  - the policy and preview math;
  - the sweep after a retention edit;
  - `retention.blocked` emission and dedup;
  - directory pruning (disk with `os.Root`, symlink refusal, age, never the root);
  - orphan scan and delete (re-check, no-delete, step-up, audit);
  - stuck-run marking;
  - the path-change refusal;
  - integration on disk and SFTP;
  - web tests for the preview and the warnings.
- **Docs:** `docs/guides/retention.md` (rules, preview, partial runs, orphans), `destinations.md` (path changes), the notifications table, D-209.

## Part V: schema version per database (D-210, proposed)

User decisions:
- an opt-in per job;
- selectable presets for the common frameworks plus "Custom query";
- editable by admins and maintainers (a custom query needs step-up and every change is audited);
- the version is captured per backed-up database and shown on the run;
- failure means the backup still succeeds, with a `version_unavailable` warning.

- **Presets** (fixed, reviewed SQL per engine; the job stores only the preset name):
  golang-migrate (`schema_migrations`: version + dirty), goose, Flyway, Liquibase, Rails, Django, Laravel, Alembic, Prisma, Knex, Sequelize. Each preset defines its PG and MySQL/MariaDB query and the formatting of the result (e.g. `42 (dirty)`).
- **Custom query:** stored on the job (bounded length, at most 2 KiB), single statement, validated as a SELECT/WITH and refused when it contains `;` mid-statement. It is never trusted as safe: safety comes from how it runs.
- **Execution in the runner** (the target DB is untrusted, D-152):
  - captured during the stats phase on the same `dbprobe` session, in a READ ONLY transaction. For PostgreSQL that is the exported snapshot, so the version matches the dump.
  - `statement_timeout` / `max_execution_time` of a few seconds; the hardened session (search_path, row_security);
  - only the first row and first column(s) are read; the result is a string of at most 64 chars, ASCII printable, anything else rejected;
  - a missing table or permission error gives "no version", not an error.
  - New proto fields in the Dump request (`VersionQuery {preset | custom sql}`) and in the DumpStats message (`version`, `version_status`); runner validation (length, preset allowlist); secret-free logs (never log the query result).
- **Storage and API:**
  - a migration adds job columns (`version_query_mode` none|preset|custom, `version_preset`, `version_custom_sql`) and `run_items.schema_version` / `schema_version_status`;
  - the manifest `stats` include the version, a canonical string;
  - GraphQL: the job fields (custom SQL readable by staff only) and `RunItem.schemaVersion`;
  - authz: `job.version_query_edit` (staff; a custom query needs `FreshStepUp`) plus matrix rows; audit `job.version_query_changed` (preset name or "custom", never the SQL in the details beyond a hash).
- **UI:**
  - the job editor: a "Schema version" section (off / preset dropdown / custom textarea with a warning text);
  - the run detail: version per database; the job page: the version of the last run;
  - the calendar/run list optionally show it.
- **Tests:**
  - the preset SQL against real PG/MySQL/MariaDB with each framework's table layout (integration);
  - custom query bounds (timeout, read-only refusal of writes, long or binary results refused, multiple statements refused);
  - snapshot consistency;
  - authz matrix rows; web tests.
- **Docs:** `docs/guides/jobs-and-schedules.md` (schema version section with the preset list), the runner API notes, D-210.

## Order

1. Part E (one agent; running).
2. After E: Part S and Part R in parallel (separate packages; R owns retention, storage, destinations and the job/destination web pages; S owns setup, cmd, compose and the wizard).
3. Part V after R: it shares the job editor and run detail pages, the migrations and the runner proto.
4. Full checks, then the security and code reviews, the fixes and the handover.

Migrations: every agent takes the next free number at write time.

No commits: the user commits.
