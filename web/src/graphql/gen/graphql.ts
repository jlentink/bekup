// @ts-nocheck
/* eslint-disable */
/** Internal type. DO NOT USE DIRECTLY. */
type Exact<T extends { [key: string]: unknown }> = { [K in keyof T]: T[K] };
/** Internal type. DO NOT USE DIRECTLY. */
export type Incremental<T> = T | { [P in keyof T]?: P extends ' $fragmentName' | '__typename' ? T[P] : never };
import type { TypedDocumentNode as DocumentNode } from '@graphql-typed-document-node/core';
/** What an artifact holds. */
export type ArtifactKind =
  | 'DATABASE'
  | 'GLOBALS'
  /** The signed manifest.json of the run on that destination. */
  | 'MANIFEST';

/** Whether an artifact still exists on its destination. */
export type ArtifactState =
  /** Removed by retention (the record stays). */
  | 'DELETED'
  /** A verification did not find it. */
  | 'MISSING'
  | 'PRESENT';

/** Who performed an audited action. */
export type AuditActorKind =
  /** A host operator running a bekup subcommand. */
  | 'CLI'
  /** bekup itself (scheduler, workers, startup checks). */
  | 'SYSTEM'
  /** A signed-in user. */
  | 'USER';

/** Filters for auditLog; unset fields do not filter. */
export type AuditLogFilter = {
  /** Exact action name, e.g. `user.created`. */
  action?: string | null | undefined;
  /** Action prefix, e.g. `auth.` or `cli.admin.`. */
  actionPrefix?: string | null | undefined;
  actorKind?: AuditActorKind | null | undefined;
  actorUserId?: string | null | undefined;
  /** Entries at or after this time. */
  from?: string | null | undefined;
  targetId?: string | null | undefined;
  targetType?: string | null | undefined;
  /** Entries before this time. */
  to?: string | null | undefined;
};

/** The kind of bekup process. */
export type BekupProcessKind =
  | 'SERVE'
  | 'WORKER';

/** What a calendar event shows. */
export type CalendarEventKind =
  /**
   * The earliest time retention may remove a run's copy on one destination
   * (the next retention sweep at or after it removes it).
   */
  | 'EXPIRY'
  /** A planned occurrence (paused for a disabled job, or started early). */
  | 'PLANNED'
  /** A run, by status. */
  | 'RUN'
  /** A skipped occurrence (the reason is in `detail`). */
  | 'SKIPPED';

export type CertificatePinInput = {
  /** CA pins only: accept any leaf the CA issues for the host (no leaf binding). */
  allowLeafRotation?: boolean | null | undefined;
  /** Index into the chain fetchCertificate returned (re-captured and checked against leafSpkiSha256). */
  certIndex?: number | null | undefined;
  /** The anchor as PEM, supplied out of band (e.g. a root CA the server does not send). */
  certificatePem?: string | null | undefined;
  /** The leaf's SPKI SHA-256 (base64 or hex) you compared; required unless allowLeafRotation. */
  leafSpkiSha256?: string | null | undefined;
};

/**
 * Why a reload was rejected or failed. (An outcome that could not be
 * recorded, database unavailable, is never stored: it is only in the
 * process's log and metrics.)
 */
export type ConfigReloadErrorKind =
  /** A component refused the new values. */
  | 'APPLY'
  /** The file is missing, unreadable or fails the ownership/mode checks. */
  | 'FILE'
  /** A key is unknown or a value is invalid (see errorKeys). */
  | 'INVALID'
  /** The file is not valid TOML. */
  | 'SYNTAX';

/** The outcome of a reload in one process. */
export type ConfigReloadStatus =
  /** Live keys changed and were applied. */
  | 'APPLIED'
  /** A change could not be applied or recorded; nothing changed. */
  | 'FAILED'
  /** The process started with this configuration (no reload yet). */
  | 'LOADED'
  /** The file could not be read or is invalid; nothing changed. */
  | 'REJECTED'
  /** Keys changed that need a restart (live keys, if any, were applied). */
  | 'RESTART_REQUIRED'
  /** The file matches the running configuration. */
  | 'UNCHANGED';

/** What started a reload. */
export type ConfigReloadTrigger =
  /** SIGHUP to one process. */
  | 'SIGNAL'
  /** An admin's reloadConfig (every process). */
  | 'UI';

/** The credential the SSH tunnel authenticates with. */
export type ConnectionSshAuthMethod =
  | 'NONE'
  | 'PASSWORD'
  | 'PRIVATE_KEY';

export type ConnectionSshInput = {
  host: string;
  /**
   * Write-only; replaces a stored private key. Omit to keep the stored
   * credential, except when host, port or user changes: a stored SSH
   * password must then be entered again (or replaced by a key).
   */
  password?: string | null | undefined;
  /** Default 22. */
  port?: number | null | undefined;
  /** Write-only PEM private key; replaces a stored password. Prefer generateConnectionSshKeypair. */
  privateKey?: string | null | undefined;
  /** Write-only; only together with privateKey. */
  privateKeyPassphrase?: string | null | undefined;
  user: string;
};

/** Transport security towards the database server. */
export type ConnectionTlsMode =
  /** No TLS (insecure: audited opt-out). */
  | 'DISABLED'
  /** TLS verified against the pinned certificate only (the default). */
  | 'PINNED'
  /** TLS without verification (insecure: audited opt-out). */
  | 'UNVERIFIED';

export type CreateConnectionInput = {
  clientCertificatePem?: string | null | undefined;
  /** Write-only; required together with clientCertificatePem. */
  clientKeyPem?: string | null | undefined;
  engine: DatabaseEngine;
  host: string;
  /** Default: versionKey. */
  imageKey?: string | null | undefined;
  /** Concurrent run limit, 1..64 and at most workers.max_concurrent_runs; omit for the global default. */
  maxConcurrentRuns?: number | null | undefined;
  name: string;
  /** Write-only. */
  password?: string | null | undefined;
  /** Default: 5432 (PostgreSQL) or 3306. */
  port?: number | null | undefined;
  ssh?: ConnectionSshInput | null | undefined;
  /** Default PINNED; anything else is an audited opt-out. */
  tlsMode?: ConnectionTlsMode | null | undefined;
  username: string;
  versionKey: string;
};

export type CreateDestinationInput = {
  disk?: DiskDestinationInput | null | undefined;
  name: string;
  /** Never delete from this destination (default false). */
  noDelete?: boolean | null | undefined;
  s3?: S3DestinationInput | null | undefined;
  sftp?: SftpDestinationInput | null | undefined;
  type: DestinationType;
};

export type CreateJobInput = {
  connectionId: string;
  databases?: Array<string> | null | undefined;
  /** Default ALL. */
  dbMode?: JobDbMode | null | undefined;
  description?: string | null | undefined;
  /** 1-16 destinations in fan-out order. */
  destinationIds: Array<string>;
  /** Default true. */
  enabled?: boolean | null | undefined;
  encryption?: JobEncryptionInput | null | undefined;
  exclude?: Array<string> | null | undefined;
  expectedWindowSeconds?: number | null | undefined;
  /** Default true for PostgreSQL; MySQL/MariaDB have no globals. */
  includeGlobals?: boolean | null | undefined;
  name: string;
  retention: JobRetentionInput;
  schedule: ScheduleInput;
  /** Optional; derived from the name. Lower-case letters, digits and inner hyphens; never changes. */
  slug?: string | null | undefined;
  /** 60 seconds up to workers.backup_timeout; omit for the default. */
  timeoutSeconds?: number | null | undefined;
  /** Default STANDARD. */
  verification?: JobVerification | null | undefined;
};

/** Creates a shared channel: an e-mail list (addresses) or a webhook (url). */
export type CreateSharedChannelInput = {
  /** E-mail list: 1..50 addresses. */
  addresses?: Array<string> | null | undefined;
  /** 1..100 characters, unique. */
  name: string;
  type: SharedChannelType;
  /** Webhook: https only, must pass the SSRF guard; never shown again. */
  url?: string | null | undefined;
};

/**
 * A temporary restore connection: the fields of CreateConnectionInput without
 * the run limit (the global default applies).
 */
export type CreateTemporaryConnectionInput = {
  clientCertificatePem?: string | null | undefined;
  /** Write-only; required together with clientCertificatePem. */
  clientKeyPem?: string | null | undefined;
  engine: DatabaseEngine;
  host: string;
  /** Default: versionKey. */
  imageKey?: string | null | undefined;
  /** Default: "Temporary: <host>". Names of temporary connections may repeat. */
  name?: string | null | undefined;
  /** Write-only. */
  password?: string | null | undefined;
  /** Default: 5432 (PostgreSQL) or 3306. */
  port?: number | null | undefined;
  ssh?: ConnectionSshInput | null | undefined;
  /** Default PINNED; anything else is an audited opt-out. */
  tlsMode?: ConnectionTlsMode | null | undefined;
  username: string;
  versionKey: string;
};

export type CreateUserInput = {
  displayName: string;
  email: string;
  role: Role;
};

/** Database engines. MySQL and MariaDB are separate engines. */
export type DatabaseEngine =
  | 'MARIADB'
  | 'MYSQL'
  | 'POSTGRES';

/** Destination types. */
export type DestinationType =
  | 'DISK'
  | 'S3'
  | 'SFTP';

export type DiskDestinationInput = {
  /** Directory below storage.disk_root (relative; no '..'). */
  path: string;
};

/** What a download delivers. */
export type DownloadMode =
  /**
   * The decrypted and decompressed dump, ready for `pg_restore` (`.dump`) or
   * `psql`/`mysql` (`.sql`).
   */
  | 'PLAIN'
  /** The stored artifact unchanged (zstd-compressed, normally age-encrypted). */
  | 'RAW';

/** Which databases of the connection a job backs up. */
export type JobDbMode =
  /** Every database on the server except `exclude` (one file per database). */
  | 'ALL'
  /** Exactly the listed `databases`. */
  | 'SELECTED';

/**
 * The initial encryption of a new job (always on; an admin may opt out
 * afterwards). Without a passphrase or recipient bekup generates the key and
 * returns it once in CreateJobResult. The default (a generated passphrase) is
 * available to everyone who may create jobs; choosing the key (a passphrase,
 * RECIPIENT mode or a recipient) is a key change and needs an admin with a
 * fresh step-up (FORBIDDEN / STEP_UP_REQUIRED otherwise).
 */
export type JobEncryptionInput = {
  label?: string | null | undefined;
  /** Default PASSPHRASE. */
  mode?: JobKeyMode | null | undefined;
  /** Write-only; at least 20 characters and very strong. Prefer a generated one. */
  passphrase?: string | null | undefined;
  /** An age1... X25519 recipient (RECIPIENT mode). */
  recipient?: string | null | undefined;
};

/** Encryption modes of backup files (D-008, D-142). */
export type JobEncryptionMode =
  /** Opted out by an admin: backups are not encrypted (badge). */
  | 'NONE'
  /** age with the job's versioned passphrase (stored encrypted). */
  | 'PASSPHRASE'
  /** age to a public X25519 recipient; the private key is supplied at restore time. */
  | 'RECIPIENT';

/** Filters for jobs; unset fields do not filter. */
export type JobFilter = {
  connectionId?: string | null | undefined;
  enabled?: boolean | null | undefined;
  /**
   * Only jobs this user owns (listed in the job's owners). Admins and
   * maintainers only (FORBIDDEN for backup owners, who see only their own
   * jobs anyway).
   */
  ownerId?: string | null | undefined;
  /** Substring of the name or slug (case-insensitive). */
  search?: string | null | undefined;
};

/** The key modes a job can encrypt with. */
export type JobKeyMode =
  | 'PASSPHRASE'
  | 'RECIPIENT';

export type JobRetentionInput = {
  keepDays?: number | null | undefined;
  keepLast?: number | null | undefined;
};

/** New test-restore settings of a job. */
export type JobTestRestoreInput = {
  /**
   * Required (admins only) when the target's role is privileged or its
   * privileges are unknown: SQL in the tested backups runs with its rights
   * (D-068).
   */
  acknowledgePrivilegedTarget?: boolean | null | undefined;
  enabled: boolean;
  /** Default: the current schedule (weekly, Sunday 04:00 UTC, when never set). */
  schedule?: ScheduleInput | null | undefined;
  /**
   * A permanent connection with the job's engine; required while enabled.
   * bekup only ever creates and drops bekup_test_<id> databases on it.
   */
  targetConnectionId?: string | null | undefined;
};

/** Completeness evidence level of a job (task 3.10). */
export type JobVerification =
  /** Exact per-table row counts. */
  | 'DEEP'
  /** Object counts and row estimates (cheap for huge databases). */
  | 'STANDARD';

/** A channel column of the preference matrix. */
export type NotificationChannelType =
  | 'EMAIL'
  | 'INBOX'
  | 'WEBHOOK';

/** The events a shared channel receives from one job; both empty: every non-security event. */
export type NotificationFilterInput = {
  /** Event types of the catalog (job events only). */
  events?: Array<string> | null | undefined;
  groups?: Array<NotificationGroup> | null | undefined;
};

/** A group of the event catalog (D-161). */
export type NotificationGroup =
  | 'BACKUP'
  | 'CONFIG'
  | 'RESTORE'
  | 'RETENTION'
  | 'SECURITY';

/** Filters of your inbox; unset fields do not filter. */
export type NotificationInboxFilter = {
  /** Only these event types (catalog types or notice.webhook_disabled; at most 64). With `group`, both must match. */
  events?: Array<string> | null | undefined;
  /** Only events of this group (notices have no group). */
  group?: NotificationGroup | null | undefined;
  /** Only unread notifications. */
  unreadOnly?: boolean | null | undefined;
};

/** Where an effective preference comes from. */
export type NotificationPreferenceSource =
  /** The D-161 default (inbox for every event, e-mail for problems). */
  | 'DEFAULT'
  /** Mandatory (security alerts always reach the inbox and e-mail); locked. */
  | 'MANDATORY'
  /** Your own per-event setting. */
  | 'OVERRIDE';

export type NotificationPriority =
  /** Collapsed per job within a short window (*.started events). */
  | 'LOW'
  | 'NORMAL';

export type NotificationSeverity =
  | 'CRITICAL'
  | 'ERROR'
  | 'INFO'
  | 'WARNING';

/** S3 Object Lock retention modes. */
export type ObjectLockMode =
  | 'COMPLIANCE'
  | 'GOVERNANCE';

/** A database restore request. */
export type RestoreArtifactInput = {
  /**
   * Required (admins only) when the target connection's role is privileged
   * or its privileges are unknown (D-068): SQL in the backup runs with that
   * role's rights. Audited and raised as a security alert.
   */
  acknowledgePrivilegedTarget?: boolean | null | undefined;
  /**
   * The job's age identity (AGE-SECRET-KEY-1...) for recipient-encrypted
   * backups only. It is stored sealed until the restore starts and never
   * logged, audited or returned.
   */
  ageIdentity?: string | null | undefined;
  /** A DATABASE artifact (present, verified, covered by a signed manifest). */
  artifactId: string;
  /** OVERWRITE: must equal targetDatabase exactly. */
  confirmTargetName?: string | null | undefined;
  /** PostgreSQL only: restore the object owners (default: objects belong to the target connection's role). */
  keepOwners?: boolean | null | undefined;
  mode: RestoreMode;
  targetConnectionId: string;
  /** The database to create (NEW_DB) or replace (OVERWRITE). */
  targetDatabase: string;
};

/** Filters for restores; unset fields do not filter. */
export type RestoreFilter = {
  artifactId?: string | null | undefined;
  jobId?: string | null | undefined;
  status?: RestoreStatus | null | undefined;
  targetConnectionId?: string | null | undefined;
};

/** A PostgreSQL globals restore request (admins only). */
export type RestoreGlobalsInput = {
  /** Required when the target connection's role is privileged or unknown (D-068; usually the case for globals). */
  acknowledgePrivilegedTarget?: boolean | null | undefined;
  /** A GLOBALS artifact. */
  artifactId: string;
  /** Must equal the preview's confirmText: `restore globals into <connection name>`. */
  confirmPhrase: string;
  /** A PostgreSQL connection. */
  targetConnectionId: string;
};

/** How a restore writes its target database. */
export type RestoreMode =
  /** Create a new database and restore into it (fails when it exists). */
  | 'NEW_DB'
  /**
   * Replace an existing database. PostgreSQL: restored into a temporary
   * database and swapped in by renaming; MySQL/MariaDB: dropped and restored
   * in place (see the preview warnings).
   */
  | 'OVERWRITE';

/** A restore request to check (restorePreview). */
export type RestorePreviewInput = {
  artifactId: string;
  keepOwners?: boolean | null | undefined;
  /** Required for DATABASE artifacts, not allowed for GLOBALS. */
  mode?: RestoreMode | null | undefined;
  targetConnectionId: string;
  /** Required for DATABASE artifacts, not allowed for GLOBALS. */
  targetDatabase?: string | null | undefined;
};

/** Status of a restore. */
export type RestoreStatus =
  | 'CANCELED'
  | 'FAILED'
  /** Waiting for a worker or a concurrency slot. */
  | 'QUEUED'
  /** Streaming the verified dump into the target. */
  | 'RESTORING'
  /** Downloading the backup to the scratch volume. */
  | 'STAGING'
  | 'SUCCEEDED'
  /** Checking the staged backup against its record and signed manifest. */
  | 'VERIFYING';

/** Roles of bekup users (D-011). */
export type Role =
  | 'ADMIN'
  | 'MAINTAINER'
  | 'USER';

/** How a table's row count was obtained. */
export type RowCountKind =
  | 'ESTIMATE'
  | 'EXACT'
  /** Not counted (time limit, budget or failure); rows is null. */
  | 'UNKNOWN';

/** Filters for runs; unset fields do not filter. */
export type RunFilter = {
  jobId?: string | null | undefined;
  /** Runs queued at or after this time. */
  since?: string | null | undefined;
  status?: RunStatus | null | undefined;
  trigger?: RunTrigger | null | undefined;
  /** Runs queued before this time. */
  until?: string | null | undefined;
};

/** What a run item backs up. */
export type RunItemKind =
  | 'DATABASE'
  /** The PostgreSQL cluster globals (roles, tablespaces). */
  | 'GLOBALS';

/** Status of one database of a run. */
export type RunItemStatus =
  | 'CANCELED'
  | 'FAILED'
  | 'PARTIAL'
  | 'RUNNING'
  | 'SUCCESS';

export type RunLogLevel =
  | 'ERROR'
  | 'INFO'
  | 'WARN';

/** Where a log line came from. */
export type RunLogStream =
  | 'RUNNER'
  | 'STDERR'
  | 'STDOUT'
  | 'WORKER';

/** Status of a backup run. */
export type RunStatus =
  | 'CANCELED'
  /** Nothing usable was stored. */
  | 'FAILED'
  /** At least one complete, verified artifact, but something is missing (a destination, a database, the manifest). */
  | 'PARTIAL'
  /** Enqueued; waiting for a worker or a concurrency slot. */
  | 'QUEUED'
  | 'RUNNING'
  /** Not run: the job was deleted or disabled before the run started. */
  | 'SKIPPED'
  /** Every database stored and verified on every destination, with the signed manifest. */
  | 'SUCCESS';

/** What started a run. */
export type RunTrigger =
  /** A missed scheduled run caught up after downtime. */
  | 'CATCH_UP'
  /** startNextRunEarly: the next planned occurrence (scheduledFor) pulled forward. */
  | 'EARLY'
  /** runJobNow. */
  | 'MANUAL'
  | 'SCHEDULE';

/** How integrity checksums are sent to S3. */
export type S3ChecksumMode =
  /** Only where the protocol requires one (stores without flexible checksums). */
  | 'WHEN_REQUIRED'
  /** A SHA-256 checksum with every upload (AWS S3 and compatible stores). */
  | 'WHEN_SUPPORTED';

export type S3DestinationInput = {
  accessKeyId: string;
  bucket: string;
  caBundlePem?: string | null | undefined;
  checksumMode?: S3ChecksumMode | null | undefined;
  /** Default true. */
  conditionalWrites?: boolean | null | undefined;
  encryption?: S3Encryption | null | undefined;
  /** https URL of an S3-compatible service (http only in dev mode); omit for AWS S3. */
  endpoint?: string | null | undefined;
  kmsKeyId?: string | null | undefined;
  /** Default Object Lock retention (requires a bucket with Object Lock). */
  objectLockMode?: ObjectLockMode | null | undefined;
  objectLockRetentionDays?: number | null | undefined;
  pathStyle?: boolean | null | undefined;
  prefix?: string | null | undefined;
  region: string;
  /** Write-only. Required when creating; omit to keep the stored key. */
  secretAccessKey?: string | null | undefined;
  storageClass?: string | null | undefined;
};

/** S3 server-side encryption. */
export type S3Encryption =
  | 'NONE'
  /** SSE-KMS (kmsKeyId optional: the account's default key). */
  | 'SSE_KMS'
  /** SSE-S3 (provider-managed keys, AES256). */
  | 'SSE_S3';

export type ScheduleInput = {
  anchor?: string | null | undefined;
  cron?: string | null | undefined;
  dayOfMonth?: number | null | undefined;
  every?: number | null | undefined;
  kind: ScheduleKind;
  timezone?: string | null | undefined;
  unit?: ScheduleUnit | null | undefined;
  weekday?: string | null | undefined;
};

/** Schedule kinds (D-143). */
export type ScheduleKind =
  /** A 5-field cron expression. */
  | 'CRON'
  /** Every N minutes/hours/days/weeks/months from an anchor. */
  | 'INTERVAL'
  /** Never runs on its own; only runJobNow. */
  | 'MANUAL';

/** Units of interval schedules. */
export type ScheduleUnit =
  | 'DAYS'
  | 'HOURS'
  | 'MINUTES'
  | 'MONTHS'
  | 'WEEKS';

/** Kinds of second factors (tasks 1.7-1.9). */
export type SecondFactorKind =
  | 'PASSKEY'
  | 'RECOVERY_CODE'
  | 'TOTP';

/** What happened to a session. */
export type SessionEventKind =
  /** All of your sessions ended (password, role or status change). */
  | 'ALL_REVOKED'
  /** The session ended (logout, revoked, replaced by a new login). */
  | 'REVOKED'
  /** The session was replaced by a new one (privilege change). */
  | 'ROTATED';

/** The credential an SFTP destination authenticates with. */
export type SftpAuthMethod =
  | 'NONE'
  | 'PASSWORD'
  | 'PRIVATE_KEY';

export type SftpDestinationInput = {
  basePath: string;
  dir?: string | null | undefined;
  host: string;
  /**
   * Write-only; replaces a stored private key. Omit to keep the stored
   * credential, except when host, port, user or basePath changes: a stored
   * password must then be entered again (or replaced by a key).
   */
  password?: string | null | undefined;
  /** Default 22. */
  port?: number | null | undefined;
  /** Write-only PEM private key; replaces a stored password. Prefer generateDestinationSshKeypair. */
  privateKey?: string | null | undefined;
  /** Write-only; only together with privateKey. */
  privateKeyPassphrase?: string | null | undefined;
  user: string;
};

export type SharedChannelType =
  | 'EMAIL_LIST'
  | 'WEBHOOK';

/** Transport security of the SMTP connection (D-180). */
export type SmtpMode =
  /**
   * The default: STARTTLS when the server offers it, otherwise unencrypted
   * (AUTH included). A failed STARTTLS handshake is an error, not a fallback.
   * An attacker on the path can suppress the STARTTLS offer.
   */
  | 'AUTO'
  /** TLS from the first byte (SMTPS). Usually port 465. */
  | 'IMPLICIT_TLS'
  /** Never encrypt, even when the server offers STARTTLS: mail and AUTH travel in clear text. */
  | 'NONE'
  /** Plain connection upgraded with STARTTLS (refused when the server does not offer it). Usually port 587. */
  | 'STARTTLS';

/** One admin allowlist entry to store. */
export type SsrfAllowlistEntryInput = {
  /**
   * The uses it widens (at least one). Null: the scopes stored for the same
   * value, or every use except PERSONAL_WEBHOOKS for a new value.
   */
  scopes?: Array<SsrfScope> | null | undefined;
  /** A CIDR (e.g. 10.20.0.0/16) or a host name (exact or `*.suffix`). */
  value: string;
};

export type SsrfAllowlistInput = {
  /** The complete new list of CIDRs with their scopes (replaces the stored one). */
  cidrEntries?: Array<SsrfAllowlistEntryInput> | null | undefined;
  /**
   * The complete new list of CIDRs as plain values (each keeps its stored
   * scopes; new values get the default scopes). Give either cidrs or
   * cidrEntries.
   */
  cidrs?: Array<string> | null | undefined;
  /**
   * The `updatedAt` of the list you edited (null when none was stored):
   * CONFLICT when someone changed it in the meantime.
   */
  expectedUpdatedAt?: string | null | undefined;
  /** The complete new list of host names with their scopes (replaces the stored one). */
  hostEntries?: Array<SsrfAllowlistEntryInput> | null | undefined;
  /**
   * The complete new list of host names as plain values. Give either hosts
   * or hostEntries.
   */
  hosts?: Array<string> | null | undefined;
};

/** An outbound use an allowlist entry widens. */
export type SsrfScope =
  /** S3 and SFTP destinations. */
  | 'DESTINATIONS'
  /**
   * Users' personal webhooks. Any user can enter a webhook URL, so an entry
   * with this scope lets every user make bekup send requests to that private
   * target. Never granted by default.
   */
  | 'PERSONAL_WEBHOOKS'
  /** Shared notification webhooks (configured by admins). */
  | 'SHARED_WEBHOOKS'
  /** The SMTP server. */
  | 'SMTP';

/** Whether the statistics describe exactly what was dumped. */
export type StatsConsistency =
  /** Taken right before the dump (MySQL/MariaDB, globals); rows may have changed since. */
  | 'APPROXIMATE'
  /** PostgreSQL: taken inside the snapshot the dump used; exact counts match the dump. */
  | 'SNAPSHOT';

/** How the statistics of a dump were taken (D-151). */
export type StatsLevel =
  /** Exact row counts where the engine allows them (PostgreSQL). */
  | 'DEEP'
  /** Planner estimates (PostgreSQL) or information_schema (MySQL/MariaDB). */
  | 'STANDARD';

/** How a step-up is performed. */
export type StepUpMethod =
  | 'PASSKEY'
  /** Re-enter the password (only for accounts without a second factor). */
  | 'PASSWORD'
  /** A recovery code: sets the step-up time only (not enough for restores) and raises an alert. */
  | 'RECOVERY_CODE'
  | 'TOTP';

/** How privileged a connection's role is (D-068): SQL in a restored dump runs with its rights. */
export type TargetPrivilegeStatus =
  /** No privilege enabling host access or privilege escalation was found. */
  | 'NOT_PRIVILEGED'
  /**
   * Superuser or similar (PostgreSQL: SUPERUSER, CREATEROLE, REPLICATION,
   * BYPASSRLS, server file/program roles; MySQL/MariaDB: SUPER, FILE,
   * PROCESS, CREATE USER, global GRANT OPTION, admin privileges, writes to
   * the mysql schema).
   */
  | 'PRIVILEGED'
  /** Could not be determined completely (e.g. MySQL/MariaDB roles granted); treated as privileged. */
  | 'UNKNOWN';

/** Status of a test restore (or of one of its databases). */
export type TestRestoreStatus =
  | 'CANCELED'
  /** A database could not be restored or checked (see errorCode). */
  | 'ERROR'
  /** At least one database misses objects, tables or rows. */
  | 'FAILED'
  /** Every database matched its source statistics. */
  | 'PASSED'
  /** Waiting for a worker or a concurrency slot. */
  | 'QUEUED'
  | 'RUNNING'
  /** No mismatch that proves data loss, but differences worth a look (estimates, unknown counts, source warnings). */
  | 'WARNINGS';

/** Why a test restore ran. */
export type TestRestoreTrigger =
  | 'MANUAL'
  | 'SCHEDULE';

/** State of a connection's certificate pin. */
export type TlsPinStatus =
  | 'EXPIRED'
  | 'EXPIRES_WITHIN_7_DAYS'
  | 'EXPIRES_WITHIN_30_DAYS'
  /** PINNED without a confirmed pin: the connection cannot be used. */
  | 'MISSING'
  /** The TLS mode is not PINNED. */
  | 'NOT_REQUIRED'
  | 'VALID';

export type TwoFactorPolicyInput = {
  /** 0 to 90. */
  gracePeriodDays: number;
  requireForAll: boolean;
  requiredRoles: Array<Role>;
};

/** Fields to change; unset fields stay unchanged. The engine and TLS mode cannot change here. */
export type UpdateConnectionInput = {
  clearClientCertificate?: boolean | null | undefined;
  /** Returns to the global workers.max_runs_per_connection. */
  clearMaxConcurrentRuns?: boolean | null | undefined;
  clearPassword?: boolean | null | undefined;
  clientCertificatePem?: string | null | undefined;
  /** Write-only; required together with clientCertificatePem. */
  clientKeyPem?: string | null | undefined;
  /** Removes the SSH tunnel with its credentials and host-key pin. */
  disableSsh?: boolean | null | undefined;
  host?: string | null | undefined;
  imageKey?: string | null | undefined;
  /**
   * Concurrent run limit, 1..64 and at most workers.max_concurrent_runs.
   * Lowering it never stops active runs; new runs wait until fewer run.
   */
  maxConcurrentRuns?: number | null | undefined;
  name?: string | null | undefined;
  /**
   * Write-only; omit to keep the stored password. When host or port (or the
   * tunnel route) changes, a stored password must be entered again or
   * cleared in the same update (VALIDATION otherwise): bekup never sends a
   * stored password to a new endpoint.
   */
  password?: string | null | undefined;
  port?: number | null | undefined;
  /** Sets (and enables) the SSH tunnel. */
  ssh?: ConnectionSshInput | null | undefined;
  username?: string | null | undefined;
  versionKey?: string | null | undefined;
};

/** Fields to change; unset fields stay unchanged. */
export type UpdateDestinationInput = {
  disk?: DiskDestinationInput | null | undefined;
  name?: string | null | undefined;
  noDelete?: boolean | null | undefined;
  s3?: S3DestinationInput | null | undefined;
  sftp?: SftpDestinationInput | null | undefined;
};

/** Fields to change; unset fields stay unchanged. The slug cannot change. */
export type UpdateJobInput = {
  clearExpectedWindow?: boolean | null | undefined;
  /** Reset the timeout to workers.backup_timeout. */
  clearTimeout?: boolean | null | undefined;
  connectionId?: string | null | undefined;
  databases?: Array<string> | null | undefined;
  dbMode?: JobDbMode | null | undefined;
  description?: string | null | undefined;
  /** Replaces the destinations (1-16, fan-out order). */
  destinationIds?: Array<string> | null | undefined;
  exclude?: Array<string> | null | undefined;
  expectedWindowSeconds?: number | null | undefined;
  includeGlobals?: boolean | null | undefined;
  name?: string | null | undefined;
  /** Replaces both limits. */
  retention?: JobRetentionInput | null | undefined;
  /** A changed schedule is re-planned from now. */
  schedule?: ScheduleInput | null | undefined;
  timeoutSeconds?: number | null | undefined;
  verification?: JobVerification | null | undefined;
};

/** Changes a shared channel; omitted fields stay. */
export type UpdateSharedChannelInput = {
  addresses?: Array<string> | null | undefined;
  name?: string | null | undefined;
  /** A new webhook URL (the signing key stays; rotate it separately). */
  url?: string | null | undefined;
};

/**
 * Replaces the SMTP settings. `password` omitted keeps the stored password;
 * `clearPassword` removes it. When host, port or username change, the mode
 * is weakened (STARTTLS or IMPLICIT_TLS to AUTO or NONE) or the CA bundle
 * changes, a stored password must be entered again in the same update
 * (VALIDATION on `password` otherwise; it is never sent to a new or less
 * protected endpoint). The host must pass the SSRF guard (private relays
 * must be allowlisted for the SMTP scope).
 */
export type UpdateSmtpSettingsInput = {
  caBundlePem?: string | null | undefined;
  clearPassword?: boolean | null | undefined;
  /** 1..60 (default 10). */
  connectTimeoutSeconds?: number | null | undefined;
  enabled: boolean;
  fromAddress: string;
  fromName?: string | null | undefined;
  host: string;
  mode: SmtpMode;
  password?: string | null | undefined;
  port: number;
  /** 5..300 (default 60): bounds the whole SMTP session. */
  sendTimeoutSeconds?: number | null | undefined;
  username?: string | null | undefined;
};

/** Fields to change; unset fields stay unchanged. */
export type UpdateUserInput = {
  disabled?: boolean | null | undefined;
  displayName?: string | null | undefined;
  role?: Role | null | undefined;
};

/** Filters for users; unset fields do not filter. */
export type UserFilter = {
  disabled?: boolean | null | undefined;
  /** Also list deleted accounts (default false). */
  includeDeleted?: boolean | null | undefined;
  role?: Role | null | undefined;
  /** Case-insensitive substring of the e-mail address or display name. */
  search?: string | null | undefined;
};

export type AccountEventsSubscriptionVariables = Exact<{ [key: string]: never; }>;


export type AccountEventsSubscription = { accountEvents: { session: { kind: SessionEventKind, sessionId: string | null, current: boolean } | null, notification: { ' $fragmentRefs'?: { 'NotificationFieldsFragment': NotificationFieldsFragment } } | null } };

export type ChangePasswordMutationVariables = Exact<{
  currentPassword: string;
  newPassword: string;
}>;


export type ChangePasswordMutation = { changePassword: boolean };

export type MySessionsQueryVariables = Exact<{ [key: string]: never; }>;


export type MySessionsQuery = { mySessions: Array<{ id: string, createdAt: string, lastSeenAt: string, idleExpiresAt: string, absoluteExpiresAt: string, ip: string | null, userAgent: string | null, aal: number, current: boolean }> };

export type RevokeSessionMutationVariables = Exact<{
  id: string;
}>;


export type RevokeSessionMutation = { revokeSession: boolean };

export type TwoFactorOverviewQueryVariables = Exact<{ [key: string]: never; }>;


export type TwoFactorOverviewQuery = { myTwoFactorStatus: { totpEnabled: boolean, totpEnabledAt: string | null, totpEnrollmentPending: boolean, recoveryCodesRemaining: number }, webauthnStatus: { webauthnAvailable: boolean, reason: string | null, rpId: string | null }, myPasskeys: Array<{ id: string, name: string, createdAt: string, lastUsedAt: string | null, transports: Array<string>, backupEligible: boolean, backedUp: boolean, userVerified: boolean, signCountAnomalyAt: string | null }> };

export type BeginTotpEnrollmentMutationVariables = Exact<{ [key: string]: never; }>;


export type BeginTotpEnrollmentMutation = { beginTotpEnrollment: { qrCodeSvg: string, manualEntryKey: string, expiresAt: string } };

export type ConfirmTotpEnrollmentMutationVariables = Exact<{
  code: string;
}>;


export type ConfirmTotpEnrollmentMutation = { confirmTotpEnrollment: { codes: Array<string> } };

export type DisableTotpMutationVariables = Exact<{ [key: string]: never; }>;


export type DisableTotpMutation = { disableTotp: boolean };

export type RegenerateRecoveryCodesMutationVariables = Exact<{ [key: string]: never; }>;


export type RegenerateRecoveryCodesMutation = { regenerateRecoveryCodes: { codes: Array<string> } };

export type BeginPasskeyRegistrationMutationVariables = Exact<{ [key: string]: never; }>;


export type BeginPasskeyRegistrationMutation = { beginPasskeyRegistration: Record<string, unknown> };

export type FinishPasskeyRegistrationMutationVariables = Exact<{
  name: string;
  response: Record<string, unknown>;
}>;


export type FinishPasskeyRegistrationMutation = { finishPasskeyRegistration: { id: string, name: string } };

export type RenamePasskeyMutationVariables = Exact<{
  id: string;
  name: string;
}>;


export type RenamePasskeyMutation = { renamePasskey: { id: string, name: string } };

export type DeletePasskeyMutationVariables = Exact<{
  id: string;
}>;


export type DeletePasskeyMutation = { deletePasskey: boolean };

export type SmtpSettingsFieldsFragment = { configured: boolean, enabled: boolean, host: string, port: number, mode: SmtpMode, username: string | null, hasPassword: boolean, fromAddress: string, fromName: string | null, caBundlePem: string | null, connectTimeoutSeconds: number, sendTimeoutSeconds: number, mayBeUnencrypted: boolean, updatedAt: string | null } & { ' $fragmentName'?: 'SmtpSettingsFieldsFragment' };

export type AdminSmtpSettingsQueryVariables = Exact<{ [key: string]: never; }>;


export type AdminSmtpSettingsQuery = { smtpSettings: { ' $fragmentRefs'?: { 'SmtpSettingsFieldsFragment': SmtpSettingsFieldsFragment } } };

export type AdminUpdateSmtpSettingsMutationVariables = Exact<{
  input: UpdateSmtpSettingsInput;
}>;


export type AdminUpdateSmtpSettingsMutation = { updateSmtpSettings: { ' $fragmentRefs'?: { 'SmtpSettingsFieldsFragment': SmtpSettingsFieldsFragment } } };

export type AdminSendTestEmailMutationVariables = Exact<{
  to: string;
}>;


export type AdminSendTestEmailMutation = { sendTestEmail: { ok: boolean, code: string | null, message: string | null, detail: string | null, encrypted: boolean, tlsVersion: string | null } };

export type SharedChannelFieldsFragment = { id: string, name: string, type: SharedChannelType, addresses: Array<string>, displayUrl: string | null, createdAt: string, updatedAt: string, state: { enabled: boolean, disabledAt: string | null, disabledReason: string | null, disabledMessage: string | null, consecutiveFailures: number, permanentFailures: number, lastFailureAt: string | null, lastFailureCode: string | null }, attachments: Array<{ jobId: string, jobName: string, groups: Array<NotificationGroup>, events: Array<string> }> } & { ' $fragmentName'?: 'SharedChannelFieldsFragment' };

export type AdminSharedChannelsQueryVariables = Exact<{ [key: string]: never; }>;


export type AdminSharedChannelsQuery = { sharedChannels: Array<{ ' $fragmentRefs'?: { 'SharedChannelFieldsFragment': SharedChannelFieldsFragment } }> };

export type AdminNotificationCatalogQueryVariables = Exact<{ [key: string]: never; }>;


export type AdminNotificationCatalogQuery = { notificationCatalog: Array<{ group: NotificationGroup, label: string, events: Array<{ type: string, label: string, group: NotificationGroup, mandatory: boolean, perJob: boolean, severity: NotificationSeverity }> }> };

export type AdminCreateSharedChannelMutationVariables = Exact<{
  input: CreateSharedChannelInput;
}>;


export type AdminCreateSharedChannelMutation = { createSharedChannel: { signingKey: string | null, channel: { ' $fragmentRefs'?: { 'SharedChannelFieldsFragment': SharedChannelFieldsFragment } } } };

export type AdminUpdateSharedChannelMutationVariables = Exact<{
  id: string;
  input: UpdateSharedChannelInput;
}>;


export type AdminUpdateSharedChannelMutation = { updateSharedChannel: { ' $fragmentRefs'?: { 'SharedChannelFieldsFragment': SharedChannelFieldsFragment } } };

export type AdminRotateSharedChannelKeyMutationVariables = Exact<{
  id: string;
}>;


export type AdminRotateSharedChannelKeyMutation = { rotateSharedChannelKey: { signingKey: string | null, channel: { ' $fragmentRefs'?: { 'SharedChannelFieldsFragment': SharedChannelFieldsFragment } } } };

export type AdminDeleteSharedChannelMutationVariables = Exact<{
  id: string;
}>;


export type AdminDeleteSharedChannelMutation = { deleteSharedChannel: boolean };

export type AdminEnableSharedChannelMutationVariables = Exact<{
  id: string;
}>;


export type AdminEnableSharedChannelMutation = { enableSharedChannel: { ' $fragmentRefs'?: { 'SharedChannelFieldsFragment': SharedChannelFieldsFragment } } };

export type AdminDisableSharedChannelMutationVariables = Exact<{
  id: string;
}>;


export type AdminDisableSharedChannelMutation = { disableSharedChannel: { ' $fragmentRefs'?: { 'SharedChannelFieldsFragment': SharedChannelFieldsFragment } } };

export type AdminTestSharedChannelMutationVariables = Exact<{
  id: string;
}>;


export type AdminTestSharedChannelMutation = { testSharedChannel: { ok: boolean, code: string | null, message: string | null, statusCode: number | null } };

export type AdminAttachSharedChannelMutationVariables = Exact<{
  jobId: string;
  channelId: string;
  filter?: NotificationFilterInput | null | undefined;
}>;


export type AdminAttachSharedChannelMutation = { attachSharedChannel: { ' $fragmentRefs'?: { 'SharedChannelFieldsFragment': SharedChannelFieldsFragment } } };

export type AdminDetachSharedChannelMutationVariables = Exact<{
  jobId: string;
  channelId: string;
}>;


export type AdminDetachSharedChannelMutation = { detachSharedChannel: { ' $fragmentRefs'?: { 'SharedChannelFieldsFragment': SharedChannelFieldsFragment } } };

export type AdminJobsQueryVariables = Exact<{
  filter?: JobFilter | null | undefined;
  first?: number | null | undefined;
  after?: string | null | undefined;
}>;


export type AdminJobsQuery = { jobs: { edges: Array<{ cursor: string, node: { id: string, name: string, slug: string, enabled: boolean, encryption: { configured: boolean, mode: JobEncryptionMode | null, optedOut: boolean, passphrase: { version: number, createdAt: string } | null } } }>, pageInfo: { hasNextPage: boolean, endCursor: string | null } } };

export type AdminAuditLogQueryVariables = Exact<{
  filter?: AuditLogFilter | null | undefined;
  first?: number | null | undefined;
  after?: string | null | undefined;
}>;


export type AdminAuditLogQuery = { auditLog: { edges: Array<{ cursor: string, node: { id: string, ts: string, actorKind: AuditActorKind, actorUserId: string | null, action: string, targetType: string | null, targetId: string | null, ip: string | null, details: string, hash: string } }>, pageInfo: { hasNextPage: boolean, endCursor: string | null } } };

export type AdminAuditChainQueryVariables = Exact<{ [key: string]: never; }>;


export type AdminAuditChainQuery = { auditChainStatus: { headId: string | null, headHash: string | null, headAt: string | null, lastVerification: { checkedAt: string, ok: boolean, full: boolean, finding: string | null, verifiedThroughId: string | null } | null, latestSignedHead: { chainId: string, signedAt: string, keyId: string } | null } };

export type AdminSecurityAlertTypesQueryVariables = Exact<{ [key: string]: never; }>;


export type AdminSecurityAlertTypesQuery = { securityAlertTypes: Array<{ kind: string, severity: NotificationSeverity, description: string, repeat: string }> };

export type AdminSecurityInboxQueryVariables = Exact<{
  filter?: NotificationInboxFilter | null | undefined;
  first?: number | null | undefined;
  after?: string | null | undefined;
}>;


export type AdminSecurityInboxQuery = { notifications: { edges: Array<{ cursor: string, node: { id: string, type: string, group: NotificationGroup | null, severity: NotificationSeverity, title: string, body: string, link: string | null, createdAt: string, readAt: string | null } }>, pageInfo: { hasNextPage: boolean, endCursor: string | null } } };

export type SsrfAllowlistFieldsFragment = { configScopes: Array<SsrfScope>, defaultScopes: Array<SsrfScope>, personalWebhooksWidened: boolean, configCidrs: Array<string>, configHosts: Array<string>, loopbackAllowed: boolean, ignoredEntries: number, maxEntries: number, updatedAt: string | null, cidrEntries: Array<{ value: string, scopes: Array<SsrfScope> }>, hostEntries: Array<{ value: string, scopes: Array<SsrfScope> }> } & { ' $fragmentName'?: 'SsrfAllowlistFieldsFragment' };

export type AdminSsrfAllowlistQueryVariables = Exact<{ [key: string]: never; }>;


export type AdminSsrfAllowlistQuery = { ssrfAllowlist: { ' $fragmentRefs'?: { 'SsrfAllowlistFieldsFragment': SsrfAllowlistFieldsFragment } } };

export type AdminUpdateSsrfAllowlistMutationVariables = Exact<{
  input: SsrfAllowlistInput;
}>;


export type AdminUpdateSsrfAllowlistMutation = { updateSsrfAllowlist: { ' $fragmentRefs'?: { 'SsrfAllowlistFieldsFragment': SsrfAllowlistFieldsFragment } } };

export type ConfigProcessResultFieldsFragment = { instanceId: string, kind: BekupProcessKind, hostname: string, version: string, processStartedAt: string, finishedAt: string, requestId: string | null, trigger: ConfigReloadTrigger | null, listening: boolean, status: ConfigReloadStatus, errorKind: ConfigReloadErrorKind | null, appliedKeys: Array<string>, restartKeys: Array<string>, errorKeys: Array<string>, weakenedKeys: Array<string> } & { ' $fragmentName'?: 'ConfigProcessResultFieldsFragment' };

export type ConfigReloadRequestFieldsFragment = { id: string, requestedAt: string, trigger: ConfigReloadTrigger, listeningProcesses: number, expectedProcesses: number, results: Array<{ ' $fragmentRefs'?: { 'ConfigProcessResultFieldsFragment': ConfigProcessResultFieldsFragment } }> } & { ' $fragmentName'?: 'ConfigReloadRequestFieldsFragment' };

export type AdminConfigStatusQueryVariables = Exact<{ [key: string]: never; }>;


export type AdminConfigStatusQuery = { configStatus: { liveKeys: Array<string>, listeningProcesses: number, processes: Array<{ ' $fragmentRefs'?: { 'ConfigProcessResultFieldsFragment': ConfigProcessResultFieldsFragment } }>, lastRequest: { ' $fragmentRefs'?: { 'ConfigReloadRequestFieldsFragment': ConfigReloadRequestFieldsFragment } } | null } };

export type AdminConfigReloadRequestQueryVariables = Exact<{
  id: string;
}>;


export type AdminConfigReloadRequestQuery = { configReloadRequest: { ' $fragmentRefs'?: { 'ConfigReloadRequestFieldsFragment': ConfigReloadRequestFieldsFragment } } };

export type AdminReloadConfigMutationVariables = Exact<{ [key: string]: never; }>;


export type AdminReloadConfigMutation = { reloadConfig: { ' $fragmentRefs'?: { 'ConfigReloadRequestFieldsFragment': ConfigReloadRequestFieldsFragment } } };

export type AdminTwoFactorPolicyQueryVariables = Exact<{ [key: string]: never; }>;


export type AdminTwoFactorPolicyQuery = { twoFactorPolicy: { requireForAll: boolean, requiredRoles: Array<Role>, gracePeriodDays: number, updatedAt: string | null, passkeysAvailable: boolean } };

export type AdminUpdateTwoFactorPolicyMutationVariables = Exact<{
  input: TwoFactorPolicyInput;
}>;


export type AdminUpdateTwoFactorPolicyMutation = { updateTwoFactorPolicy: { requireForAll: boolean, requiredRoles: Array<Role>, gracePeriodDays: number, updatedAt: string | null, passkeysAvailable: boolean } };

export type MeQueryVariables = Exact<{ [key: string]: never; }>;


export type MeQuery = { me: { id: string, email: string, displayName: string, role: Role, passwordChangeRequired: boolean, twoFactorRequired: boolean, twoFactorEnrollmentDeadline: string | null } | null };

export type LoginMutationVariables = Exact<{
  email: string;
  password: string;
}>;


export type LoginMutation = { login: { secondFactorRequired: boolean, availableFactors: Array<SecondFactorKind>, passwordChangeRequired: boolean, twoFactorEnrollmentRequired: boolean, user: { id: string, email: string, displayName: string, role: Role } | null } };

export type LogoutMutationVariables = Exact<{ [key: string]: never; }>;


export type LogoutMutation = { logout: boolean };

export type BeginPasskeyLoginMutationVariables = Exact<{ [key: string]: never; }>;


export type BeginPasskeyLoginMutation = { beginPasskeyLogin: Record<string, unknown> };

export type CompleteSecondFactorMutationVariables = Exact<{
  method: SecondFactorKind;
  code?: string | null | undefined;
  assertion?: Record<string, unknown> | null | undefined;
}>;


export type CompleteSecondFactorMutation = { completeSecondFactor: { secondFactorRequired: boolean, passwordChangeRequired: boolean, twoFactorEnrollmentRequired: boolean, user: { id: string } | null } };

export type BeginStepUpMutationVariables = Exact<{ [key: string]: never; }>;


export type BeginStepUpMutation = { beginStepUp: { methods: Array<StepUpMethod>, passkeyOptions: Record<string, unknown> | null } };

export type CompleteStepUpMutationVariables = Exact<{
  method: StepUpMethod;
  code?: string | null | undefined;
  password?: string | null | undefined;
  assertion?: Record<string, unknown> | null | undefined;
}>;


export type CompleteStepUpMutation = { completeStepUp: { stepUpAt: string, aal2At: string | null, aal: number } };

export type CalendarQueryVariables = Exact<{
  from: string;
  to: string;
  timezone: string;
  jobIds?: Array<string> | string | null | undefined;
}>;


export type CalendarQuery = { calendar: { truncated: boolean, from: string, to: string, timezone: string, events: Array<{ jobId: string, jobName: string, kind: CalendarEventKind, start: string, end: string | null, status: RunStatus | null, trigger: RunTrigger | null, runId: string | null, destinationId: string | null, projected: boolean, paused: boolean, startedEarly: boolean, overdue: boolean, detail: string | null }> } };

export type CalendarJobsQueryVariables = Exact<{
  after?: string | null | undefined;
}>;


export type CalendarJobsQuery = { jobs: { edges: Array<{ node: { id: string, name: string, connection: { id: string, name: string }, destinations: Array<{ id: string, name: string }> } }>, pageInfo: { hasNextPage: boolean, endCursor: string | null } } };

export type CalendarRunUpdatedSubscriptionVariables = Exact<{ [key: string]: never; }>;


export type CalendarRunUpdatedSubscription = { runUpdated: { id: string, status: RunStatus, trigger: RunTrigger, scheduledFor: string | null, queuedAt: string, startedAt: string | null, finishedAt: string | null, errorCode: string | null, job: { id: string, name: string } } };

export type ConnectionsListQueryVariables = Exact<{ [key: string]: never; }>;


export type ConnectionsListQuery = { connections: Array<{ id: string, name: string, engine: DatabaseEngine, versionKey: string, legacyVersion: boolean, host: string, port: number, tlsMode: ConnectionTlsMode, insecureTransport: boolean, pinStatus: TlsPinStatus, pinExpiresAt: string | null, sshEnabled: boolean, ready: boolean, notReadyReason: string | null, maxConcurrentRuns: number | null, effectiveMaxConcurrentRuns: number, lastTest: { at: string, ok: boolean, code: string | null, stale: boolean, privileges: { status: TargetPrivilegeStatus, reasons: Array<string>, acknowledgementRequired: boolean } | null } | null }> };

export type ConnectionDetailQueryVariables = Exact<{
  id: string;
}>;


export type ConnectionDetailQuery = { connection: { id: string, name: string, engine: DatabaseEngine, versionKey: string, legacyVersion: boolean, host: string, port: number, tlsMode: ConnectionTlsMode, insecureTransport: boolean, pinStatus: TlsPinStatus, pinExpiresAt: string | null, sshEnabled: boolean, ready: boolean, notReadyReason: string | null, maxConcurrentRuns: number | null, effectiveMaxConcurrentRuns: number, createdAt: string, updatedAt: string, lastTest: { at: string, ok: boolean, code: string | null, stale: boolean, privileges: { status: TargetPrivilegeStatus, reasons: Array<string>, acknowledgementRequired: boolean } | null } | null, settings: { imageKey: string, username: string, hasPassword: boolean, clientCertificatePem: string | null, tlsPin: { certificatePem: string, subject: string, issuer: string, isCa: boolean, leafSpkiSha256: string | null, notAfter: string, pinnedAt: string } | null, backupPin: { certificatePem: string, subject: string, issuer: string, isCa: boolean, leafSpkiSha256: string | null, notAfter: string, pinnedAt: string } | null, ssh: { host: string, port: number, user: string, authMethod: ConnectionSshAuthMethod, hasPassword: boolean, hasPrivateKey: boolean, hasPassphrase: boolean, publicKey: string | null, hostKey: { type: string, fingerprintSha256: string, authorizedKey: string, confirmedAt: string | null } | null } | null } | null } | null };

export type ConnectionEngineVersionsQueryVariables = Exact<{
  engine?: DatabaseEngine | null | undefined;
}>;


export type ConnectionEngineVersionsQuery = { engineVersions: Array<{ engine: DatabaseEngine, key: string, clientVersion: string, legacy: boolean }> };

export type CreateConnectionMutationVariables = Exact<{
  input: CreateConnectionInput;
}>;


export type CreateConnectionMutation = { createConnection: { id: string, name: string } };

export type UpdateConnectionMutationVariables = Exact<{
  id: string;
  input: UpdateConnectionInput;
}>;


export type UpdateConnectionMutation = { updateConnection: { id: string, name: string } };

export type DeleteConnectionMutationVariables = Exact<{
  id: string;
}>;


export type DeleteConnectionMutation = { deleteConnection: boolean };

export type TestConnectionMutationVariables = Exact<{
  id: string;
}>;


export type TestConnectionMutation = { testConnection: { ok: boolean, code: string | null, message: string | null, serverVersion: string | null, detectedVersionKey: string | null, compatible: boolean, legacy: boolean, warnings: Array<string>, databaseCount: number, privileges: { status: TargetPrivilegeStatus, reasons: Array<string>, acknowledgementRequired: boolean } | null } };

export type DetectConnectionVersionMutationVariables = Exact<{
  id: string;
}>;


export type DetectConnectionVersionMutation = { detectVersion: { serverVersion: string, suggestedVersionKey: string | null, legacy: boolean, currentCompatible: boolean, warnings: Array<string> } };

export type PrePullConnectionImageMutationVariables = Exact<{
  id: string;
}>;


export type PrePullConnectionImageMutation = { prePullImage: { imageRef: string, alreadyPresent: boolean } };

export type FetchConnectionCertificateMutationVariables = Exact<{
  id: string;
}>;


export type FetchConnectionCertificateMutation = { fetchCertificate: { suggestedAnchorIndex: number | null, leafSpkiSha256: string, certificates: Array<{ pem: string, subject: string, issuer: string, dnsNames: Array<string>, ipAddresses: Array<string>, notBefore: string, notAfter: string, isCa: boolean, selfSigned: boolean, spkiSha256: string, spkiSha256Hex: string, fingerprintSha256: string }> } };

export type ConfirmConnectionCertificatePinMutationVariables = Exact<{
  id: string;
  input: CertificatePinInput;
}>;


export type ConfirmConnectionCertificatePinMutation = { confirmCertificatePin: { id: string, pinStatus: TlsPinStatus, pinExpiresAt: string | null } };

export type StageConnectionBackupPinMutationVariables = Exact<{
  id: string;
  input: CertificatePinInput;
}>;


export type StageConnectionBackupPinMutation = { stageBackupPin: { id: string, pinStatus: TlsPinStatus } };

export type FetchConnectionSshHostKeyMutationVariables = Exact<{
  id: string;
}>;


export type FetchConnectionSshHostKeyMutation = { fetchSshHostKey: { type: string, fingerprintSha256: string, authorizedKey: string, confirmedAt: string | null, host: string | null, port: number | null } };

export type ConfirmConnectionSshHostKeyMutationVariables = Exact<{
  id: string;
  authorizedKey: string;
  expectedHost: string;
  expectedPort: number;
}>;


export type ConfirmConnectionSshHostKeyMutation = { confirmSshHostKey: { id: string, ready: boolean } };

export type SetConnectionTlsModeMutationVariables = Exact<{
  id: string;
  mode: ConnectionTlsMode;
}>;


export type SetConnectionTlsModeMutation = { setTlsMode: { id: string, tlsMode: ConnectionTlsMode, insecureTransport: boolean, pinStatus: TlsPinStatus } };

export type GenerateConnectionSshKeypairMutationVariables = Exact<{
  id: string;
}>;


export type GenerateConnectionSshKeypairMutation = { generateConnectionSshKeypair: { publicKey: string, fingerprintSha256: string, connection: { id: string } } };

export type DashboardJobsQueryVariables = Exact<{
  first?: number | null | undefined;
}>;


export type DashboardJobsQuery = { jobs: { edges: Array<{ node: { id: string, name: string, enabled: boolean, nextRunAt: string | null, scheduleDescription: string, missed: boolean, missedSince: string | null, connection: { id: string, name: string, deleted: boolean }, encryption: { configured: boolean, mode: JobEncryptionMode | null, optedOut: boolean }, lastRun: { id: string, status: RunStatus, queuedAt: string, finishedAt: string | null, warnings: Array<{ code: string }> } | null } }>, pageInfo: { hasNextPage: boolean } } };

export type DashboardRunsQueryVariables = Exact<{
  since: string;
}>;


export type DashboardRunsQuery = { running: { edges: Array<{ node: { id: string, status: RunStatus, trigger: RunTrigger, queuedAt: string, startedAt: string | null, finishedAt: string | null, errorCode: string | null, errorMessage: string | null, job: { id: string, name: string } } }> }, queued: { edges: Array<{ node: { id: string, status: RunStatus, trigger: RunTrigger, queuedAt: string, startedAt: string | null, finishedAt: string | null, errorCode: string | null, errorMessage: string | null, job: { id: string, name: string } } }> }, failed: { edges: Array<{ node: { id: string, status: RunStatus, trigger: RunTrigger, queuedAt: string, startedAt: string | null, finishedAt: string | null, errorCode: string | null, errorMessage: string | null, job: { id: string, name: string } } }>, pageInfo: { hasNextPage: boolean } } };

export type DashboardRestoresQueryVariables = Exact<{ [key: string]: never; }>;


export type DashboardRestoresQuery = { restores: { edges: Array<{ node: { id: string, status: RestoreStatus, createdAt: string, startedAt: string | null, sourceDatabase: string | null, targetDatabase: string | null, kind: RunItemKind, job: { id: string, name: string }, target: { id: string, name: string } } }> } };

export type DashboardActivitySubscriptionVariables = Exact<{ [key: string]: never; }>;


export type DashboardActivitySubscription = { activityUpdated: { run: { id: string, status: RunStatus, trigger: RunTrigger, queuedAt: string, startedAt: string | null, finishedAt: string | null, errorCode: string | null, errorMessage: string | null, warnings: Array<{ code: string }>, job: { id: string, name: string } } | null, restore: { id: string, status: RestoreStatus, createdAt: string, startedAt: string | null, sourceDatabase: string | null, targetDatabase: string | null, kind: RunItemKind, job: { id: string, name: string }, target: { id: string, name: string } } | null } };

export type DashboardCalendarQueryVariables = Exact<{
  from: string;
  to: string;
  timezone: string;
}>;


export type DashboardCalendarQuery = { calendar: { truncated: boolean, events: Array<{ jobId: string, jobName: string, kind: CalendarEventKind, start: string, status: RunStatus | null, runId: string | null, paused: boolean, startedEarly: boolean, overdue: boolean, detail: string | null }> } };

export type DashboardInfraQueryVariables = Exact<{ [key: string]: never; }>;


export type DashboardInfraQuery = { connections: Array<{ id: string, name: string, tlsMode: ConnectionTlsMode, insecureTransport: boolean, pinStatus: TlsPinStatus, pinExpiresAt: string | null, lastTest: { at: string, ok: boolean, code: string | null, stale: boolean, privileges: { status: TargetPrivilegeStatus } | null } | null }>, destinations: Array<{ id: string, name: string, type: DestinationType, ready: boolean, lastTest: { at: string, ok: boolean, code: string | null } | null }> };

export type DashboardStorageQueryVariables = Exact<{ [key: string]: never; }>;


export type DashboardStorageQuery = { destinationUsage: Array<{ destinationId: string, name: string, type: DestinationType, storedBytes: number, artifactCount: number }> };

export type DashboardSystemHealthQueryVariables = Exact<{ [key: string]: never; }>;


export type DashboardSystemHealthQuery = { systemHealth: { checkedAt: string, earliestPinNotAfter: string | null, oldestLastSuccess: string | null, schedulerEnabled: boolean, lastSchedulerTick: string | null, runner: { up: boolean, dockerOk: boolean, version: string | null, activeRuns: number | null, maxConcurrentRuns: number | null }, certificates: Array<{ component: string, notAfter: string }>, queue: Array<{ queue: string, state: string, jobs: number }> | null } };

export type DashboardAlertsQueryVariables = Exact<{ [key: string]: never; }>;


export type DashboardAlertsQuery = { unreadNotificationCount: number, notifications: { edges: Array<{ node: { id: string, type: string, severity: NotificationSeverity, title: string, link: string | null, createdAt: string } }> } };

export type DestinationsListQueryVariables = Exact<{ [key: string]: never; }>;


export type DestinationsListQuery = { destinations: Array<{ id: string, name: string, type: DestinationType, noDelete: boolean, objectLockMode: ObjectLockMode | null, objectLockRetentionDays: number | null, ready: boolean, notReadyReason: string | null, updatedAt: string, lastTest: { at: string, ok: boolean, code: string | null, stale: boolean } | null }> };

export type DestinationDetailQueryVariables = Exact<{
  id: string;
}>;


export type DestinationDetailQuery = { destination: { id: string, name: string, type: DestinationType, noDelete: boolean, objectLockMode: ObjectLockMode | null, objectLockRetentionDays: number | null, ready: boolean, notReadyReason: string | null, createdAt: string, updatedAt: string, lastTest: { at: string, ok: boolean, code: string | null, stale: boolean } | null, settings: { disk: { path: string } | null, s3: { endpoint: string | null, region: string, bucket: string, prefix: string, pathStyle: boolean, encryption: S3Encryption, kmsKeyId: string | null, storageClass: string | null, checksumMode: S3ChecksumMode, conditionalWrites: boolean, accessKeyId: string, hasSecretAccessKey: boolean, caBundlePem: string | null } | null, sftp: { host: string, port: number, user: string, basePath: string, dir: string, authMethod: SftpAuthMethod, hasPassword: boolean, hasPrivateKey: boolean, hasPassphrase: boolean, publicKey: string | null, hostKey: { type: string, fingerprintSha256: string, authorizedKey: string, confirmedAt: string | null } | null } | null } | null } | null };

export type CreateDestinationMutationVariables = Exact<{
  input: CreateDestinationInput;
}>;


export type CreateDestinationMutation = { createDestination: { id: string, name: string } };

export type UpdateDestinationMutationVariables = Exact<{
  id: string;
  input: UpdateDestinationInput;
}>;


export type UpdateDestinationMutation = { updateDestination: { id: string, name: string } };

export type DeleteDestinationMutationVariables = Exact<{
  id: string;
}>;


export type DeleteDestinationMutation = { deleteDestination: boolean };

export type TestDestinationMutationVariables = Exact<{
  id: string;
}>;


export type TestDestinationMutation = { testDestination: { ok: boolean, probeKey: string | null, probeRemains: boolean, note: string | null, steps: Array<{ name: string, ok: boolean, code: string, message: string }> } };

export type FetchDestinationHostKeyMutationVariables = Exact<{
  id: string;
}>;


export type FetchDestinationHostKeyMutation = { fetchDestinationHostKey: { type: string, fingerprintSha256: string, authorizedKey: string, confirmedAt: string | null, host: string | null, port: number | null } };

export type ConfirmDestinationHostKeyMutationVariables = Exact<{
  id: string;
  authorizedKey: string;
  expectedHost: string;
  expectedPort: number;
}>;


export type ConfirmDestinationHostKeyMutation = { confirmDestinationHostKey: { id: string, ready: boolean } };

export type GenerateDestinationSshKeypairMutationVariables = Exact<{
  id: string;
}>;


export type GenerateDestinationSshKeypairMutation = { generateDestinationSshKeypair: { publicKey: string, fingerprintSha256: string, destination: { id: string } } };

export type JobsListQueryVariables = Exact<{
  filter?: JobFilter | null | undefined;
  first?: number | null | undefined;
  after?: string | null | undefined;
}>;


export type JobsListQuery = { jobs: { edges: Array<{ cursor: string, node: { id: string, name: string, slug: string, enabled: boolean, nextRunAt: string | null, scheduleDescription: string, connection: { id: string, name: string, engine: DatabaseEngine, deleted: boolean }, encryption: { configured: boolean, mode: JobEncryptionMode | null, optedOut: boolean }, owners: Array<{ userId: string, displayName: string, email: string, role: Role, disabled: boolean }> | null, lastRun: { id: string, status: RunStatus, finishedAt: string | null, queuedAt: string, warnings: Array<{ code: string }> } | null } }>, pageInfo: { hasNextPage: boolean, endCursor: string | null } } };

export type JobFilterConnectionsQueryVariables = Exact<{ [key: string]: never; }>;


export type JobFilterConnectionsQuery = { connections: Array<{ id: string, name: string, engine: DatabaseEngine, temporary: boolean }> };

export type JobDetailQueryVariables = Exact<{
  id: string;
}>;


export type JobDetailQuery = { job: { id: string, name: string, slug: string, description: string, enabled: boolean, nextRunAt: string | null, lastScheduledAt: string | null, scheduleDescription: string, dbMode: JobDbMode, databases: Array<string>, exclude: Array<string>, includeGlobals: boolean, timeoutSeconds: number | null, verification: JobVerification, createdAt: string, updatedAt: string, schedule: { kind: ScheduleKind, every: number | null, unit: ScheduleUnit | null, anchor: string | null, weekday: string | null, dayOfMonth: number | null, cron: string | null, timezone: string | null }, connection: { id: string, name: string, engine: DatabaseEngine, versionKey: string, deleted: boolean }, retention: { keepLast: number | null, keepDays: number | null }, destinations: Array<{ id: string, name: string, type: DestinationType, position: number, deleted: boolean }>, encryption: { configured: boolean, mode: JobEncryptionMode | null, optedOut: boolean, optedOutAt: string | null, optOutReason: string | null, updatedAt: string | null, passphrase: { version: number, createdAt: string } | null, recipient: { version: number, recipient: string, fingerprint: string, label: string, generated: boolean, createdAt: string } | null }, owners: Array<{ userId: string, email: string, displayName: string, role: Role, disabled: boolean, addedAt: string }> | null, lastRun: { id: string, status: RunStatus, queuedAt: string, finishedAt: string | null } | null, testRestore: { enabled: boolean, scheduleDescription: string, nextAt: string | null, privilegedTargetAcknowledgedAt: string | null, schedule: { kind: ScheduleKind, every: number | null, unit: ScheduleUnit | null, anchor: string | null, weekday: string | null, dayOfMonth: number | null, cron: string | null, timezone: string | null }, target: { id: string, name: string, engine: DatabaseEngine, deleted: boolean } | null, targetPrivileges: { status: TargetPrivilegeStatus, reasons: Array<string>, acknowledgementRequired: boolean } | null } } | null };

export type LatestTestRestoreQueryVariables = Exact<{
  jobId: string;
}>;


export type LatestTestRestoreQuery = { testRestores: { edges: Array<{ node: { id: string, runId: string | null, status: TestRestoreStatus, finishedAt: string | null, createdAt: string, databaseCount: number, passed: number, warned: number, failed: number, errorMessage: string | null } }> } };

export type JobTestRestoresQueryVariables = Exact<{
  jobId: string;
  first?: number | null | undefined;
  after?: string | null | undefined;
}>;


export type JobTestRestoresQuery = { testRestores: { edges: Array<{ cursor: string, node: { id: string, runId: string | null, status: TestRestoreStatus, trigger: TestRestoreTrigger, scheduledFor: string | null, createdAt: string, startedAt: string | null, finishedAt: string | null, durationSeconds: number | null, errorCode: string | null, errorMessage: string | null, databaseCount: number, passed: number, warned: number, failed: number, requestedBy: { userId: string, displayName: string, email: string } | null, target: { id: string, name: string, deleted: boolean } } }>, pageInfo: { hasNextPage: boolean, endCursor: string | null } } };

export type TestRestoreDetailQueryVariables = Exact<{
  id: string;
}>;


export type TestRestoreDetailQuery = { testRestore: { id: string, runId: string | null, status: TestRestoreStatus, trigger: TestRestoreTrigger, scheduledFor: string | null, createdAt: string, startedAt: string | null, finishedAt: string | null, durationSeconds: number | null, errorCode: string | null, errorMessage: string | null, databaseCount: number, passed: number, warned: number, failed: number, job: { id: string, name: string, slug: string, deleted: boolean }, requestedBy: { userId: string, displayName: string, email: string } | null, target: { id: string, name: string, engine: DatabaseEngine, temporary: boolean, deleted: boolean }, databases: Array<{ id: string, runItemId: string, database: string, restoreId: string | null, status: TestRestoreStatus, errorCode: string | null, errorMessage: string | null, scratchDropped: boolean, findingsTotal: number, tablesCompared: number, exactTables: number, restoreStatus: string | null, restoreErrorCode: string | null, startedAt: string, finishedAt: string | null, findings: Array<{ severity: string, code: string, object: string | null, source: number | null, restored: number | null, delta: number | null }> }> } | null };

export type TestRestoreTargetsQueryVariables = Exact<{ [key: string]: never; }>;


export type TestRestoreTargetsQuery = { connections: Array<{ id: string, name: string, engine: DatabaseEngine, temporary: boolean, ready: boolean, lastTest: { at: string, ok: boolean, stale: boolean, privileges: { status: TargetPrivilegeStatus, reasons: Array<string>, acknowledgementRequired: boolean } | null } | null }> };

export type SetJobTestRestoreMutationVariables = Exact<{
  jobId: string;
  input: JobTestRestoreInput;
}>;


export type SetJobTestRestoreMutation = { setJobTestRestore: { id: string, testRestore: { enabled: boolean, scheduleDescription: string, nextAt: string | null, privilegedTargetAcknowledgedAt: string | null, schedule: { kind: ScheduleKind, every: number | null, unit: ScheduleUnit | null, anchor: string | null, weekday: string | null, dayOfMonth: number | null, cron: string | null, timezone: string | null }, target: { id: string, name: string, engine: DatabaseEngine, deleted: boolean } | null, targetPrivileges: { status: TargetPrivilegeStatus, reasons: Array<string>, acknowledgementRequired: boolean } | null } } };

export type RunTestRestoreNowMutationVariables = Exact<{
  jobId: string;
}>;


export type RunTestRestoreNowMutation = { runTestRestoreNow: { id: string, status: TestRestoreStatus } };

export type JobEditorOptionsQueryVariables = Exact<{ [key: string]: never; }>;


export type JobEditorOptionsQuery = { connections: Array<{ id: string, name: string, engine: DatabaseEngine, versionKey: string, temporary: boolean }>, destinations: Array<{ id: string, name: string, type: DestinationType, ready: boolean, notReadyReason: string | null }> };

export type PreviewScheduleQueryVariables = Exact<{
  input: ScheduleInput;
  count?: number | null | undefined;
}>;


export type PreviewScheduleQuery = { previewSchedule: { description: string, next: Array<string>, schedule: { kind: ScheduleKind, timezone: string | null } } };

export type CreateJobMutationVariables = Exact<{
  input: CreateJobInput;
}>;


export type CreateJobMutation = { createJob: { generatedPassphrase: string | null, job: { id: string, name: string, slug: string }, generatedKeypair: { version: number, recipient: string, fingerprint: string, identity: string, identityFile: string, fileName: string } | null } };

export type UpdateJobMutationVariables = Exact<{
  id: string;
  input: UpdateJobInput;
}>;


export type UpdateJobMutation = { updateJob: { id: string, name: string, updatedAt: string } };

export type DeleteJobMutationVariables = Exact<{
  id: string;
}>;


export type DeleteJobMutation = { deleteJob: boolean };

export type EnableJobMutationVariables = Exact<{
  id: string;
}>;


export type EnableJobMutation = { enableJob: { id: string, enabled: boolean, nextRunAt: string | null } };

export type DisableJobMutationVariables = Exact<{
  id: string;
}>;


export type DisableJobMutation = { disableJob: { id: string, enabled: boolean, nextRunAt: string | null } };

export type RunJobNowMutationVariables = Exact<{
  id: string;
}>;


export type RunJobNowMutation = { runJobNow: { runId: string, queuedAt: string, job: { id: string, nextRunAt: string | null } } };

export type StartNextRunEarlyMutationVariables = Exact<{
  id: string;
}>;


export type StartNextRunEarlyMutation = { startNextRunEarly: { runId: string, queuedAt: string, job: { id: string, nextRunAt: string | null } } };

export type JobNextOccurrencesQueryVariables = Exact<{
  input: ScheduleInput;
}>;


export type JobNextOccurrencesQuery = { previewSchedule: { next: Array<string> } };

export type GenerateJobPassphraseMutationVariables = Exact<{
  jobId: string;
}>;


export type GenerateJobPassphraseMutation = { generateJobPassphrase: { version: number, passphrase: string } };

export type SetJobPassphraseMutationVariables = Exact<{
  jobId: string;
  passphrase: string;
}>;


export type SetJobPassphraseMutation = { setJobPassphrase: { mode: JobEncryptionMode | null } };

export type GenerateAgeKeypairMutationVariables = Exact<{
  jobId: string;
  label?: string | null | undefined;
}>;


export type GenerateAgeKeypairMutation = { generateAgeKeypair: { version: number, recipient: string, fingerprint: string, identity: string, identityFile: string, fileName: string } };

export type SetJobRecipientMutationVariables = Exact<{
  jobId: string;
  recipient: string;
  label?: string | null | undefined;
}>;


export type SetJobRecipientMutation = { setJobRecipient: { mode: JobEncryptionMode | null } };

export type EnableJobEncryptionMutationVariables = Exact<{
  jobId: string;
  mode: JobKeyMode;
}>;


export type EnableJobEncryptionMutation = { enableJobEncryption: { mode: JobEncryptionMode | null } };

export type DisableJobEncryptionMutationVariables = Exact<{
  jobId: string;
  reason: string;
}>;


export type DisableJobEncryptionMutation = { disableJobEncryption: { mode: JobEncryptionMode | null, optedOut: boolean } };

export type JobSharedChannelAttachmentsQueryVariables = Exact<{
  id: string;
}>;


export type JobSharedChannelAttachmentsQuery = { job: { id: string, sharedChannelAttachments: Array<{ channelId: string, name: string, type: SharedChannelType, displayUrl: string | null, enabled: boolean, groups: Array<NotificationGroup>, events: Array<string> }> | null } | null };

export type NotificationFieldsFragment = { id: string, type: string, group: NotificationGroup | null, severity: NotificationSeverity, title: string, body: string, link: string | null, createdAt: string, readAt: string | null } & { ' $fragmentName'?: 'NotificationFieldsFragment' };

export type NotificationBellQueryVariables = Exact<{ [key: string]: never; }>;


export type NotificationBellQuery = { unreadNotificationCount: number, notifications: { edges: Array<{ cursor: string, node: { ' $fragmentRefs'?: { 'NotificationFieldsFragment': NotificationFieldsFragment } } }>, pageInfo: { hasNextPage: boolean, endCursor: string | null } } };

export type NotificationsInboxQueryVariables = Exact<{
  unreadOnly?: boolean | null | undefined;
  first?: number | null | undefined;
  after?: string | null | undefined;
}>;


export type NotificationsInboxQuery = { unreadNotificationCount: number, notifications: { edges: Array<{ cursor: string, node: { ' $fragmentRefs'?: { 'NotificationFieldsFragment': NotificationFieldsFragment } } }>, pageInfo: { hasNextPage: boolean, endCursor: string | null } } };

export type MarkNotificationsReadMutationVariables = Exact<{
  ids?: Array<string> | string | null | undefined;
  all?: boolean | null | undefined;
}>;


export type MarkNotificationsReadMutation = { markNotificationsRead: { marked: number, unreadCount: number } };

export type DeleteNotificationMutationVariables = Exact<{
  id: string;
}>;


export type DeleteNotificationMutation = { deleteNotification: boolean };

export type NotificationEventPreferenceFieldsFragment = { event: { type: string, label: string, group: NotificationGroup, problem: boolean, mandatory: boolean, priority: NotificationPriority, severity: NotificationSeverity, perJob: boolean }, channels: Array<{ channel: NotificationChannelType, enabled: boolean, source: NotificationPreferenceSource, locked: boolean }>, jobOverrides: Array<{ jobId: string, jobName: string, channel: NotificationChannelType, enabled: boolean }> } & { ' $fragmentName'?: 'NotificationEventPreferenceFieldsFragment' };

export type NotificationPreferencesFieldsFragment = { emailVerified: boolean, activeWebhooks: number, groups: Array<{ group: NotificationGroup, label: string, events: Array<{ ' $fragmentRefs'?: { 'NotificationEventPreferenceFieldsFragment': NotificationEventPreferenceFieldsFragment } }> }> } & { ' $fragmentName'?: 'NotificationPreferencesFieldsFragment' };

export type MyNotificationPreferencesQueryVariables = Exact<{ [key: string]: never; }>;


export type MyNotificationPreferencesQuery = { myNotificationPreferences: { ' $fragmentRefs'?: { 'NotificationPreferencesFieldsFragment': NotificationPreferencesFieldsFragment } } };

export type SetNotificationPreferenceMutationVariables = Exact<{
  event: string;
  channel: NotificationChannelType;
  enabled: boolean;
  jobId?: string | null | undefined;
}>;


export type SetNotificationPreferenceMutation = { setNotificationPreference: { ' $fragmentRefs'?: { 'NotificationEventPreferenceFieldsFragment': NotificationEventPreferenceFieldsFragment } } };

export type ResetNotificationPreferencesMutationVariables = Exact<{
  jobId?: string | null | undefined;
}>;


export type ResetNotificationPreferencesMutation = { resetNotificationPreferences: { ' $fragmentRefs'?: { 'NotificationPreferencesFieldsFragment': NotificationPreferencesFieldsFragment } } };

export type NotificationJobOptionsQueryVariables = Exact<{
  search?: string | null | undefined;
}>;


export type NotificationJobOptionsQuery = { jobs: { edges: Array<{ node: { id: string, name: string } }> } };

export type PersonalWebhookFieldsFragment = { id: string, label: string, displayUrl: string, createdAt: string, state: { enabled: boolean, disabledAt: string | null, disabledReason: string | null, disabledMessage: string | null, consecutiveFailures: number, permanentFailures: number, lastFailureAt: string | null, lastFailureCode: string | null } } & { ' $fragmentName'?: 'PersonalWebhookFieldsFragment' };

export type MyNotificationChannelsQueryVariables = Exact<{ [key: string]: never; }>;


export type MyNotificationChannelsQuery = { myNotificationChannels: { email: string, emailVerified: boolean, emailVerifiedAt: string | null, maxWebhooks: number, webhooks: Array<{ ' $fragmentRefs'?: { 'PersonalWebhookFieldsFragment': PersonalWebhookFieldsFragment } }> } };

export type AddMyWebhookMutationVariables = Exact<{
  url: string;
  label: string;
}>;


export type AddMyWebhookMutation = { addMyWebhook: { signingKey: string, webhook: { id: string, label: string, displayUrl: string } } };

export type RotateMyWebhookKeyMutationVariables = Exact<{
  id: string;
}>;


export type RotateMyWebhookKeyMutation = { rotateMyWebhookKey: { signingKey: string, webhook: { id: string, label: string, displayUrl: string } } };

export type DeleteMyWebhookMutationVariables = Exact<{
  id: string;
}>;


export type DeleteMyWebhookMutation = { deleteMyWebhook: boolean };

export type TestMyWebhookMutationVariables = Exact<{
  id: string;
}>;


export type TestMyWebhookMutation = { testMyWebhook: { ok: boolean, code: string | null } };

export type ReenableMyWebhookMutationVariables = Exact<{
  id: string;
}>;


export type ReenableMyWebhookMutation = { reenableMyWebhook: { ' $fragmentRefs'?: { 'PersonalWebhookFieldsFragment': PersonalWebhookFieldsFragment } } };

export type RequestEmailVerificationMutationVariables = Exact<{ [key: string]: never; }>;


export type RequestEmailVerificationMutation = { requestEmailVerification: { sentTo: string, expiresAt: string } };

export type VerifyEmailMutationVariables = Exact<{
  code: string;
}>;


export type VerifyEmailMutation = { verifyEmail: { id: string, emailVerified: boolean, emailVerifiedAt: string | null } };

export type RestoreFieldsFragment = { id: string, artifactId: string, runId: string, kind: RunItemKind, sourceDatabase: string | null, targetDatabase: string | null, mode: RestoreMode | null, keepOwners: boolean, status: RestoreStatus, createdAt: string, startedAt: string | null, finishedAt: string | null, durationSeconds: number | null, stagedBytes: number, errorCode: string | null, errorMessage: string | null, logLines: number, logLinesDropped: number, updatedAt: string, job: { id: string, name: string, slug: string, deleted: boolean }, target: { id: string, name: string, engine: DatabaseEngine, temporary: boolean, deleted: boolean }, requestedBy: { userId: string, displayName: string, email: string } | null, warnings: Array<{ code: string, message: string }> } & { ' $fragmentName'?: 'RestoreFieldsFragment' };

export type RestoreJobsQueryVariables = Exact<{
  after?: string | null | undefined;
}>;


export type RestoreJobsQuery = { jobs: { edges: Array<{ node: { id: string, name: string, slug: string, connection: { id: string, name: string, engine: DatabaseEngine, versionKey: string } } }>, pageInfo: { hasNextPage: boolean, endCursor: string | null } } };

export type RestoreRunsQueryVariables = Exact<{
  jobId: string;
  after?: string | null | undefined;
}>;


export type RestoreRunsQuery = { runs: { edges: Array<{ node: { id: string, status: RunStatus, trigger: RunTrigger, startedAt: string | null, finishedAt: string | null, queuedAt: string, storageTimestamp: string | null, items: Array<{ id: string, kind: RunItemKind, database: string | null, status: RunItemStatus, artifacts: Array<{ id: string, kind: ArtifactKind, database: string | null, size: number, verified: boolean, signed: boolean, state: ArtifactState, createdAt: string, destination: { id: string, name: string, type: DestinationType, deleted: boolean }, encryption: { mode: JobEncryptionMode | null } }> }> } }>, pageInfo: { hasNextPage: boolean, endCursor: string | null } } };

export type RestoreTargetsQueryVariables = Exact<{
  artifactId: string;
}>;


export type RestoreTargetsQuery = { restoreTargets: { source: { artifactId: string, jobId: string, runId: string, kind: RunItemKind, database: string | null, engine: DatabaseEngine, versionKey: string, serverVersion: string, needsIdentity: boolean }, targets: Array<{ id: string, name: string, engine: DatabaseEngine, versionKey: string, original: boolean, temporary: boolean, expiresAt: string | null, ready: boolean, notReadyReason: string | null, insecureTransport: boolean, newDatabaseAllowed: boolean, existingRestricted: boolean }> } };

export type RestoreTargetDatabasesQueryVariables = Exact<{
  artifactId: string;
  targetConnectionId: string;
}>;


export type RestoreTargetDatabasesQuery = { restoreTargetDatabases: { databases: Array<string>, warnings: Array<string>, listedAt: string, targetPrivileges: { status: TargetPrivilegeStatus, reasons: Array<string>, acknowledgementRequired: boolean } } };

export type RestoreTempConnectionFieldsFragment = { id: string, name: string, engine: DatabaseEngine, versionKey: string, host: string, port: number, tlsMode: ConnectionTlsMode, insecureTransport: boolean, pinStatus: TlsPinStatus, sshEnabled: boolean, ready: boolean, notReadyReason: string | null, temporary: boolean, expiresAt: string | null, settings: { tlsPin: { subject: string, notAfter: string } | null, ssh: { host: string, port: number, hostKey: { fingerprintSha256: string, confirmedAt: string | null } | null } | null } | null } & { ' $fragmentName'?: 'RestoreTempConnectionFieldsFragment' };

export type RestoreEngineVersionsQueryVariables = Exact<{
  engine?: DatabaseEngine | null | undefined;
}>;


export type RestoreEngineVersionsQuery = { engineVersions: Array<{ engine: DatabaseEngine, key: string, clientVersion: string, legacy: boolean }> };

export type RestoreCreateTemporaryConnectionMutationVariables = Exact<{
  input: CreateTemporaryConnectionInput;
}>;


export type RestoreCreateTemporaryConnectionMutation = { createTemporaryConnection: { ' $fragmentRefs'?: { 'RestoreTempConnectionFieldsFragment': RestoreTempConnectionFieldsFragment } } };

export type RestoreDeleteTemporaryConnectionMutationVariables = Exact<{
  id: string;
}>;


export type RestoreDeleteTemporaryConnectionMutation = { deleteTemporaryConnection: boolean };

export type RestoreFetchCertificateMutationVariables = Exact<{
  id: string;
}>;


export type RestoreFetchCertificateMutation = { fetchCertificate: { suggestedAnchorIndex: number | null, leafSpkiSha256: string, certificates: Array<{ subject: string, issuer: string, dnsNames: Array<string>, ipAddresses: Array<string>, notBefore: string, notAfter: string, isCa: boolean, selfSigned: boolean, spkiSha256: string, spkiSha256Hex: string, fingerprintSha256: string }> } };

export type RestoreConfirmCertificatePinMutationVariables = Exact<{
  id: string;
  input: CertificatePinInput;
}>;


export type RestoreConfirmCertificatePinMutation = { confirmCertificatePin: { ' $fragmentRefs'?: { 'RestoreTempConnectionFieldsFragment': RestoreTempConnectionFieldsFragment } } };

export type RestoreFetchSshHostKeyMutationVariables = Exact<{
  id: string;
}>;


export type RestoreFetchSshHostKeyMutation = { fetchSshHostKey: { type: string, fingerprintSha256: string, authorizedKey: string, host: string | null, port: number | null } };

export type RestoreConfirmSshHostKeyMutationVariables = Exact<{
  id: string;
  authorizedKey: string;
  expectedHost: string;
  expectedPort: number;
}>;


export type RestoreConfirmSshHostKeyMutation = { confirmSshHostKey: { ' $fragmentRefs'?: { 'RestoreTempConnectionFieldsFragment': RestoreTempConnectionFieldsFragment } } };

export type RestoreTestConnectionMutationVariables = Exact<{
  id: string;
}>;


export type RestoreTestConnectionMutation = { testConnection: { ok: boolean, code: string | null, message: string | null, serverVersion: string | null, compatible: boolean, warnings: Array<string>, privileges: { status: TargetPrivilegeStatus, reasons: Array<string>, acknowledgementRequired: boolean } | null } };

export type RestorePreviewQueryVariables = Exact<{
  input: RestorePreviewInput;
}>;


export type RestorePreviewQuery = { restorePreview: { mode: RestoreMode | null, targetDatabase: string | null, confirmText: string | null, crossSource: boolean, warnings: Array<{ code: string, message: string }>, targetPrivileges: { status: TargetPrivilegeStatus, reasons: Array<string>, acknowledgementRequired: boolean }, target: { id: string, name: string, engine: DatabaseEngine, temporary: boolean, insecureTransport: boolean }, source: { artifactId: string, kind: RunItemKind, database: string | null, engine: DatabaseEngine, versionKey: string, serverVersion: string, needsIdentity: boolean } } };

export type RestoreFactorStatusQueryVariables = Exact<{ [key: string]: never; }>;


export type RestoreFactorStatusQuery = { myTwoFactorStatus: { totpEnabled: boolean }, myPasskeys: Array<{ id: string }> };

export type RestoreArtifactMutationVariables = Exact<{
  input: RestoreArtifactInput;
}>;


export type RestoreArtifactMutation = { restoreArtifact: { ' $fragmentRefs'?: { 'RestoreFieldsFragment': RestoreFieldsFragment } } };

export type RestoreGlobalsMutationVariables = Exact<{
  input: RestoreGlobalsInput;
}>;


export type RestoreGlobalsMutation = { restoreGlobals: { ' $fragmentRefs'?: { 'RestoreFieldsFragment': RestoreFieldsFragment } } };

export type RestoreDetailQueryVariables = Exact<{
  id: string;
}>;


export type RestoreDetailQuery = { restore: { ' $fragmentRefs'?: { 'RestoreFieldsFragment': RestoreFieldsFragment } } | null };

export type RestoreUpdatedSubscriptionVariables = Exact<{
  restoreId: string;
}>;


export type RestoreUpdatedSubscription = { restoreUpdated: { ' $fragmentRefs'?: { 'RestoreFieldsFragment': RestoreFieldsFragment } } };

export type RestoreLogsQueryVariables = Exact<{
  restoreId: string;
  afterId?: string | number | null | undefined;
}>;


export type RestoreLogsQuery = { restoreLogs: { restoreId: string, endId: string | null, hasMore: boolean, dropped: number, lines: Array<{ id: string, time: string, level: RunLogLevel, stream: RunLogStream, line: string }> } | null };

export type RestoreCancelMutationVariables = Exact<{
  id: string;
}>;


export type RestoreCancelMutation = { cancelRestore: { restoreId: string, status: RestoreStatus, requestedAt: string } };

export type RestoreHistoryQueryVariables = Exact<{
  filter?: RestoreFilter | null | undefined;
  after?: string | null | undefined;
}>;


export type RestoreHistoryQuery = { restores: { edges: Array<{ node: { ' $fragmentRefs'?: { 'RestoreFieldsFragment': RestoreFieldsFragment } } }>, pageInfo: { hasNextPage: boolean, endCursor: string | null } } };

export type RunsListQueryVariables = Exact<{
  filter?: RunFilter | null | undefined;
  first?: number | null | undefined;
  after?: string | null | undefined;
}>;


export type RunsListQuery = { runs: { edges: Array<{ cursor: string, node: { id: string, status: RunStatus, trigger: RunTrigger, scheduledFor: string | null, queuedAt: string, startedAt: string | null, finishedAt: string | null, durationSeconds: number | null, bytesStored: number, attempt: number, job: { id: string, name: string, deleted: boolean }, warnings: Array<{ code: string }>, items: Array<{ id: string }> } }>, pageInfo: { hasNextPage: boolean, endCursor: string | null } } };

export type RunDetailQueryVariables = Exact<{
  id: string;
}>;


export type RunDetailQuery = { run: { id: string, status: RunStatus, trigger: RunTrigger, scheduledFor: string | null, attempt: number, queuedAt: string, startedAt: string | null, finishedAt: string | null, durationSeconds: number | null, storageTimestamp: string | null, errorCode: string | null, errorMessage: string | null, bytesRaw: number, bytesStored: number, cancelRequested: boolean, cancelRequestedAt: string | null, logLines: number, logLinesDropped: number, updatedAt: string, triggeredBy: { userId: string, displayName: string, email: string } | null, warnings: Array<{ code: string, message: string, database: string | null, destinationId: string | null }>, notices: Array<{ code: string, message: string, database: string | null, destinationId: string | null }>, job: { id: string, name: string, slug: string, deleted: boolean }, items: Array<{ id: string, kind: RunItemKind, database: string | null, position: number, status: RunItemStatus, rawSize: number, storedSize: number, sha256: string | null, verified: boolean, startedAt: string, finishedAt: string | null, durationSeconds: number | null, errorCode: string | null, errorMessage: string | null, warnings: Array<{ code: string, message: string, database: string | null, destinationId: string | null }>, artifacts: Array<{ id: string, kind: ArtifactKind, state: ArtifactState, verified: boolean, signed: boolean, size: number, sha256: string, objectKey: string, lockUntil: string | null, stateChangedAt: string | null, encryption: { mode: JobEncryptionMode | null, passphraseVersion: number | null, recipientVersion: number | null }, destination: { id: string, name: string, type: DestinationType, deleted: boolean } }> }>, manifest: { signed: boolean, signingKeyId: string | null, coveredItems: number, totalItems: number, copies: Array<{ id: string, state: ArtifactState, destination: { id: string, name: string } }> } } | null };

export type RunActivitySubscriptionVariables = Exact<{
  runId: string;
  afterLogId?: string | number | null | undefined;
}>;


export type RunActivitySubscription = { runActivity: { run: { id: string, status: RunStatus, trigger: RunTrigger, scheduledFor: string | null, attempt: number, queuedAt: string, startedAt: string | null, finishedAt: string | null, durationSeconds: number | null, storageTimestamp: string | null, errorCode: string | null, errorMessage: string | null, bytesRaw: number, bytesStored: number, cancelRequested: boolean, cancelRequestedAt: string | null, logLines: number, logLinesDropped: number, updatedAt: string, triggeredBy: { userId: string, displayName: string, email: string } | null, warnings: Array<{ code: string, message: string, database: string | null, destinationId: string | null }>, notices: Array<{ code: string, message: string, database: string | null, destinationId: string | null }>, job: { id: string, name: string, slug: string, deleted: boolean }, items: Array<{ id: string, kind: RunItemKind, database: string | null, position: number, status: RunItemStatus, rawSize: number, storedSize: number, sha256: string | null, verified: boolean, startedAt: string, finishedAt: string | null, durationSeconds: number | null, errorCode: string | null, errorMessage: string | null, warnings: Array<{ code: string, message: string, database: string | null, destinationId: string | null }>, artifacts: Array<{ id: string, kind: ArtifactKind, state: ArtifactState, verified: boolean, signed: boolean, size: number, sha256: string, objectKey: string, lockUntil: string | null, stateChangedAt: string | null, encryption: { mode: JobEncryptionMode | null, passphraseVersion: number | null, recipientVersion: number | null }, destination: { id: string, name: string, type: DestinationType, deleted: boolean } }> }>, manifest: { signed: boolean, signingKeyId: string | null, coveredItems: number, totalItems: number, copies: Array<{ id: string, state: ArtifactState, destination: { id: string, name: string } }> } } | null, logs: { runId: string, endId: string | null, done: boolean, lines: Array<{ id: string, time: string, level: RunLogLevel, stream: RunLogStream, line: string }> } | null } };

export type RunJobContextQueryVariables = Exact<{
  id: string;
}>;


export type RunJobContextQuery = { job: { id: string, verification: JobVerification, connection: { id: string, engine: DatabaseEngine } } | null };

export type RunItemStatsQueryVariables = Exact<{
  id: string;
}>;


export type RunItemStatsQuery = { run: { id: string, items: Array<{ id: string, kind: RunItemKind, database: string | null, stats: { level: StatsLevel, consistency: StatsConsistency, tablesTotal: number, tablesTruncated: boolean, recordedAt: string, objects: Array<{ kind: string, count: number }>, tables: Array<{ name: string, rows: number | null, count: RowCountKind, exact: boolean, estimate: boolean, unknown: boolean, approximate: boolean }>, warnings: Array<{ code: string, count: number, objects: Array<string>, truncated: boolean }>, notices: Array<{ code: string, count: number, objects: Array<string>, truncated: boolean }> } | null }> } | null };

export type RunTestRestoresQueryVariables = Exact<{
  jobId: string;
}>;


export type RunTestRestoresQuery = { testRestores: { edges: Array<{ node: { id: string, runId: string | null, status: TestRestoreStatus, finishedAt: string | null } }> } };

export type TestRestoreFindingsQueryVariables = Exact<{
  id: string;
}>;


export type TestRestoreFindingsQuery = { testRestore: { id: string, status: TestRestoreStatus, finishedAt: string | null, errorMessage: string | null, databaseCount: number, passed: number, warned: number, failed: number, databases: Array<{ id: string, database: string, status: TestRestoreStatus, errorMessage: string | null, tablesCompared: number, exactTables: number, findingsTotal: number, findings: Array<{ severity: string, code: string, object: string | null, source: number | null, restored: number | null, delta: number | null }> }> } | null };

export type RunLogsQueryVariables = Exact<{
  runId: string;
  afterId?: string | number | null | undefined;
  first?: number | null | undefined;
}>;


export type RunLogsQuery = { runLogs: { runId: string, endId: string | null, hasMore: boolean, dropped: number, lines: Array<{ id: string, time: string, level: RunLogLevel, stream: RunLogStream, line: string }> } | null };

export type RunUpdatedSubscriptionVariables = Exact<{
  jobId?: string | null | undefined;
  runId?: string | null | undefined;
}>;


export type RunUpdatedSubscription = { runUpdated: { id: string, status: RunStatus, startedAt: string | null, finishedAt: string | null, durationSeconds: number | null, bytesRaw: number, bytesStored: number, cancelRequested: boolean, logLines: number, updatedAt: string, job: { id: string } } };

export type CancelRunMutationVariables = Exact<{
  id: string;
}>;


export type CancelRunMutation = { cancelRun: { runId: string, status: RunStatus, retryCanceled: boolean } };

export type CreateDownloadTicketMutationVariables = Exact<{
  artifactId: string;
  mode: DownloadMode;
  ageIdentity?: string | null | undefined;
}>;


export type CreateDownloadTicketMutation = { createDownloadTicket: { url: string, expiresAt: string, mode: DownloadMode, fileName: string, format: string, restoreCommand: string } };

export type AdminUserFieldsFragment = { id: string, email: string, displayName: string, role: Role, disabled: boolean, lockedUntil: string | null, passwordChangeRequired: boolean, temporaryPasswordExpiresAt: string | null, twoFactorRequired: boolean, twoFactorEnrollmentDeadline: string | null, twoFactorEnrolled: boolean, lastSeenAt: string | null, emailVerified: boolean, createdAt: string, updatedAt: string, deletedAt: string | null } & { ' $fragmentName'?: 'AdminUserFieldsFragment' };

export type AdminUsersQueryVariables = Exact<{
  filter?: UserFilter | null | undefined;
  first?: number | null | undefined;
  after?: string | null | undefined;
}>;


export type AdminUsersQuery = { users: { edges: Array<{ cursor: string, node: { ' $fragmentRefs'?: { 'AdminUserFieldsFragment': AdminUserFieldsFragment } } }>, pageInfo: { hasNextPage: boolean, endCursor: string | null } } };

export type AdminUserQueryVariables = Exact<{
  id: string;
}>;


export type AdminUserQuery = { user: { ' $fragmentRefs'?: { 'AdminUserFieldsFragment': AdminUserFieldsFragment } } | null };

export type AdminUserSessionsQueryVariables = Exact<{
  userId: string;
}>;


export type AdminUserSessionsQuery = { userSessions: Array<{ id: string, createdAt: string, lastSeenAt: string, absoluteExpiresAt: string, ip: string | null, userAgent: string | null, aal: number, current: boolean }> };

export type AdminCreateUserMutationVariables = Exact<{
  input: CreateUserInput;
}>;


export type AdminCreateUserMutation = { createUser: { user: { id: string, email: string, displayName: string, role: Role }, temporaryPassword: { password: string, expiresAt: string } } };

export type AdminResetUserPasswordMutationVariables = Exact<{
  id: string;
}>;


export type AdminResetUserPasswordMutation = { resetUserPassword: { password: string, expiresAt: string } };

export type AdminUpdateUserMutationVariables = Exact<{
  id: string;
  input: UpdateUserInput;
}>;


export type AdminUpdateUserMutation = { updateUser: { ' $fragmentRefs'?: { 'AdminUserFieldsFragment': AdminUserFieldsFragment } } };

export type AdminDisableUserMutationVariables = Exact<{
  id: string;
}>;


export type AdminDisableUserMutation = { disableUser: { ' $fragmentRefs'?: { 'AdminUserFieldsFragment': AdminUserFieldsFragment } } };

export type AdminEnableUserMutationVariables = Exact<{
  id: string;
}>;


export type AdminEnableUserMutation = { enableUser: { ' $fragmentRefs'?: { 'AdminUserFieldsFragment': AdminUserFieldsFragment } } };

export type AdminUnlockUserMutationVariables = Exact<{
  id: string;
}>;


export type AdminUnlockUserMutation = { unlockUser: { ' $fragmentRefs'?: { 'AdminUserFieldsFragment': AdminUserFieldsFragment } } };

export type AdminDeleteUserMutationVariables = Exact<{
  id: string;
}>;


export type AdminDeleteUserMutation = { deleteUser: boolean };

export type AdminRevokeUserSessionsMutationVariables = Exact<{
  userId: string;
}>;


export type AdminRevokeUserSessionsMutation = { revokeUserSessions: number };

export type AdminResetUserSecondFactorsMutationVariables = Exact<{
  userId: string;
}>;


export type AdminResetUserSecondFactorsMutation = { resetUserSecondFactors: { removedTotp: boolean, removedPasskeys: number, removedRecoveryCodes: number, revokedSessions: number, user: { ' $fragmentRefs'?: { 'AdminUserFieldsFragment': AdminUserFieldsFragment } } } };

export type AdminOwnedJobsQueryVariables = Exact<{
  ownerId: string;
  first?: number | null | undefined;
  after?: string | null | undefined;
}>;


export type AdminOwnedJobsQuery = { jobs: { edges: Array<{ node: { id: string, name: string, slug: string, enabled: boolean, owners: Array<{ userId: string, addedAt: string }> | null } }>, pageInfo: { hasNextPage: boolean, endCursor: string | null } } };

export type AdminAssignableJobsQueryVariables = Exact<{
  search?: string | null | undefined;
  first?: number | null | undefined;
}>;


export type AdminAssignableJobsQuery = { jobs: { edges: Array<{ node: { id: string, name: string, slug: string, enabled: boolean } }>, pageInfo: { hasNextPage: boolean } } };

export type UserPickerCandidatesQueryVariables = Exact<{
  search?: string | null | undefined;
}>;


export type UserPickerCandidatesQuery = { userDirectory: { edges: Array<{ node: { id: string, email: string, displayName: string, role: Role, disabled: boolean } }> } };

export type AccountSubscriptionVariables = Exact<{ [key: string]: never; }>;


export type AccountSubscription = { accountEvents: { session: { kind: SessionEventKind } | null } };

export type ListDatabasesMutationVariables = Exact<{
  id: string;
}>;


export type ListDatabasesMutation = { listDatabases: { databases: Array<string>, warnings: Array<string> } };

export type AssignJobOwnersMutationVariables = Exact<{
  jobId: string;
  users: Array<string> | string;
}>;


export type AssignJobOwnersMutation = { assignJobOwners: Array<{ userId: string, email: string, displayName: string, role: Role, disabled: boolean, addedAt: string }> };

export type RemoveJobOwnerMutationVariables = Exact<{
  jobId: string;
  userId: string;
}>;


export type RemoveJobOwnerMutation = { removeJobOwner: Array<{ userId: string }> };

export type ExportJobPassphrasesMutationVariables = Exact<{
  jobId: string;
}>;


export type ExportJobPassphrasesMutation = { exportJobPassphrases: { fileName: string, content: string, versions: Array<number> } };

export type ServerInfoQueryVariables = Exact<{ [key: string]: never; }>;


export type ServerInfoQuery = { serverInfo: { version: string, insecureTransport: boolean, devMode: boolean } };

export const SmtpSettingsFieldsFragmentDoc = {"kind":"Document","definitions":[{"kind":"FragmentDefinition","name":{"kind":"Name","value":"SmtpSettingsFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"SmtpSettings"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"configured"}},{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"host"}},{"kind":"Field","name":{"kind":"Name","value":"port"}},{"kind":"Field","name":{"kind":"Name","value":"mode"}},{"kind":"Field","name":{"kind":"Name","value":"username"}},{"kind":"Field","name":{"kind":"Name","value":"hasPassword"}},{"kind":"Field","name":{"kind":"Name","value":"fromAddress"}},{"kind":"Field","name":{"kind":"Name","value":"fromName"}},{"kind":"Field","name":{"kind":"Name","value":"caBundlePem"}},{"kind":"Field","name":{"kind":"Name","value":"connectTimeoutSeconds"}},{"kind":"Field","name":{"kind":"Name","value":"sendTimeoutSeconds"}},{"kind":"Field","name":{"kind":"Name","value":"mayBeUnencrypted"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]} as unknown as DocumentNode<SmtpSettingsFieldsFragment, unknown>;
export const SharedChannelFieldsFragmentDoc = {"kind":"Document","definitions":[{"kind":"FragmentDefinition","name":{"kind":"Name","value":"SharedChannelFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"SharedChannel"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"addresses"}},{"kind":"Field","name":{"kind":"Name","value":"displayUrl"}},{"kind":"Field","name":{"kind":"Name","value":"state"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"disabledAt"}},{"kind":"Field","name":{"kind":"Name","value":"disabledReason"}},{"kind":"Field","name":{"kind":"Name","value":"disabledMessage"}},{"kind":"Field","name":{"kind":"Name","value":"consecutiveFailures"}},{"kind":"Field","name":{"kind":"Name","value":"permanentFailures"}},{"kind":"Field","name":{"kind":"Name","value":"lastFailureAt"}},{"kind":"Field","name":{"kind":"Name","value":"lastFailureCode"}}]}},{"kind":"Field","name":{"kind":"Name","value":"attachments"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"jobId"}},{"kind":"Field","name":{"kind":"Name","value":"jobName"}},{"kind":"Field","name":{"kind":"Name","value":"groups"}},{"kind":"Field","name":{"kind":"Name","value":"events"}}]}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]} as unknown as DocumentNode<SharedChannelFieldsFragment, unknown>;
export const SsrfAllowlistFieldsFragmentDoc = {"kind":"Document","definitions":[{"kind":"FragmentDefinition","name":{"kind":"Name","value":"SsrfAllowlistFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"SsrfAllowlist"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"cidrEntries"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"value"}},{"kind":"Field","name":{"kind":"Name","value":"scopes"}}]}},{"kind":"Field","name":{"kind":"Name","value":"hostEntries"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"value"}},{"kind":"Field","name":{"kind":"Name","value":"scopes"}}]}},{"kind":"Field","name":{"kind":"Name","value":"configScopes"}},{"kind":"Field","name":{"kind":"Name","value":"defaultScopes"}},{"kind":"Field","name":{"kind":"Name","value":"personalWebhooksWidened"}},{"kind":"Field","name":{"kind":"Name","value":"configCidrs"}},{"kind":"Field","name":{"kind":"Name","value":"configHosts"}},{"kind":"Field","name":{"kind":"Name","value":"loopbackAllowed"}},{"kind":"Field","name":{"kind":"Name","value":"ignoredEntries"}},{"kind":"Field","name":{"kind":"Name","value":"maxEntries"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]} as unknown as DocumentNode<SsrfAllowlistFieldsFragment, unknown>;
export const ConfigProcessResultFieldsFragmentDoc = {"kind":"Document","definitions":[{"kind":"FragmentDefinition","name":{"kind":"Name","value":"ConfigProcessResultFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"ConfigProcessResult"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"instanceId"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"hostname"}},{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"processStartedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"requestId"}},{"kind":"Field","name":{"kind":"Name","value":"trigger"}},{"kind":"Field","name":{"kind":"Name","value":"listening"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"errorKind"}},{"kind":"Field","name":{"kind":"Name","value":"appliedKeys"}},{"kind":"Field","name":{"kind":"Name","value":"restartKeys"}},{"kind":"Field","name":{"kind":"Name","value":"errorKeys"}},{"kind":"Field","name":{"kind":"Name","value":"weakenedKeys"}}]}}]} as unknown as DocumentNode<ConfigProcessResultFieldsFragment, unknown>;
export const ConfigReloadRequestFieldsFragmentDoc = {"kind":"Document","definitions":[{"kind":"FragmentDefinition","name":{"kind":"Name","value":"ConfigReloadRequestFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"ConfigReloadRequest"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"requestedAt"}},{"kind":"Field","name":{"kind":"Name","value":"trigger"}},{"kind":"Field","name":{"kind":"Name","value":"listeningProcesses"}},{"kind":"Field","name":{"kind":"Name","value":"expectedProcesses"}},{"kind":"Field","name":{"kind":"Name","value":"results"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"ConfigProcessResultFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"ConfigProcessResultFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"ConfigProcessResult"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"instanceId"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"hostname"}},{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"processStartedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"requestId"}},{"kind":"Field","name":{"kind":"Name","value":"trigger"}},{"kind":"Field","name":{"kind":"Name","value":"listening"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"errorKind"}},{"kind":"Field","name":{"kind":"Name","value":"appliedKeys"}},{"kind":"Field","name":{"kind":"Name","value":"restartKeys"}},{"kind":"Field","name":{"kind":"Name","value":"errorKeys"}},{"kind":"Field","name":{"kind":"Name","value":"weakenedKeys"}}]}}]} as unknown as DocumentNode<ConfigReloadRequestFieldsFragment, unknown>;
export const NotificationFieldsFragmentDoc = {"kind":"Document","definitions":[{"kind":"FragmentDefinition","name":{"kind":"Name","value":"NotificationFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Notification"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"group"}},{"kind":"Field","name":{"kind":"Name","value":"severity"}},{"kind":"Field","name":{"kind":"Name","value":"title"}},{"kind":"Field","name":{"kind":"Name","value":"body"}},{"kind":"Field","name":{"kind":"Name","value":"link"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"readAt"}}]}}]} as unknown as DocumentNode<NotificationFieldsFragment, unknown>;
export const NotificationEventPreferenceFieldsFragmentDoc = {"kind":"Document","definitions":[{"kind":"FragmentDefinition","name":{"kind":"Name","value":"NotificationEventPreferenceFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"NotificationEventPreference"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"event"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"label"}},{"kind":"Field","name":{"kind":"Name","value":"group"}},{"kind":"Field","name":{"kind":"Name","value":"problem"}},{"kind":"Field","name":{"kind":"Name","value":"mandatory"}},{"kind":"Field","name":{"kind":"Name","value":"priority"}},{"kind":"Field","name":{"kind":"Name","value":"severity"}},{"kind":"Field","name":{"kind":"Name","value":"perJob"}}]}},{"kind":"Field","name":{"kind":"Name","value":"channels"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"channel"}},{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"source"}},{"kind":"Field","name":{"kind":"Name","value":"locked"}}]}},{"kind":"Field","name":{"kind":"Name","value":"jobOverrides"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"jobId"}},{"kind":"Field","name":{"kind":"Name","value":"jobName"}},{"kind":"Field","name":{"kind":"Name","value":"channel"}},{"kind":"Field","name":{"kind":"Name","value":"enabled"}}]}}]}}]} as unknown as DocumentNode<NotificationEventPreferenceFieldsFragment, unknown>;
export const NotificationPreferencesFieldsFragmentDoc = {"kind":"Document","definitions":[{"kind":"FragmentDefinition","name":{"kind":"Name","value":"NotificationPreferencesFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"NotificationPreferences"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"emailVerified"}},{"kind":"Field","name":{"kind":"Name","value":"activeWebhooks"}},{"kind":"Field","name":{"kind":"Name","value":"groups"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"group"}},{"kind":"Field","name":{"kind":"Name","value":"label"}},{"kind":"Field","name":{"kind":"Name","value":"events"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"NotificationEventPreferenceFields"}}]}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"NotificationEventPreferenceFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"NotificationEventPreference"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"event"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"label"}},{"kind":"Field","name":{"kind":"Name","value":"group"}},{"kind":"Field","name":{"kind":"Name","value":"problem"}},{"kind":"Field","name":{"kind":"Name","value":"mandatory"}},{"kind":"Field","name":{"kind":"Name","value":"priority"}},{"kind":"Field","name":{"kind":"Name","value":"severity"}},{"kind":"Field","name":{"kind":"Name","value":"perJob"}}]}},{"kind":"Field","name":{"kind":"Name","value":"channels"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"channel"}},{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"source"}},{"kind":"Field","name":{"kind":"Name","value":"locked"}}]}},{"kind":"Field","name":{"kind":"Name","value":"jobOverrides"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"jobId"}},{"kind":"Field","name":{"kind":"Name","value":"jobName"}},{"kind":"Field","name":{"kind":"Name","value":"channel"}},{"kind":"Field","name":{"kind":"Name","value":"enabled"}}]}}]}}]} as unknown as DocumentNode<NotificationPreferencesFieldsFragment, unknown>;
export const PersonalWebhookFieldsFragmentDoc = {"kind":"Document","definitions":[{"kind":"FragmentDefinition","name":{"kind":"Name","value":"PersonalWebhookFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"PersonalWebhook"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"label"}},{"kind":"Field","name":{"kind":"Name","value":"displayUrl"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"state"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"disabledAt"}},{"kind":"Field","name":{"kind":"Name","value":"disabledReason"}},{"kind":"Field","name":{"kind":"Name","value":"disabledMessage"}},{"kind":"Field","name":{"kind":"Name","value":"consecutiveFailures"}},{"kind":"Field","name":{"kind":"Name","value":"permanentFailures"}},{"kind":"Field","name":{"kind":"Name","value":"lastFailureAt"}},{"kind":"Field","name":{"kind":"Name","value":"lastFailureCode"}}]}}]}}]} as unknown as DocumentNode<PersonalWebhookFieldsFragment, unknown>;
export const RestoreFieldsFragmentDoc = {"kind":"Document","definitions":[{"kind":"FragmentDefinition","name":{"kind":"Name","value":"RestoreFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Restore"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"job"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"slug"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"artifactId"}},{"kind":"Field","name":{"kind":"Name","value":"runId"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"sourceDatabase"}},{"kind":"Field","name":{"kind":"Name","value":"target"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"temporary"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"targetDatabase"}},{"kind":"Field","name":{"kind":"Name","value":"mode"}},{"kind":"Field","name":{"kind":"Name","value":"keepOwners"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"requestedBy"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"userId"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"email"}}]}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"startedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"durationSeconds"}},{"kind":"Field","name":{"kind":"Name","value":"stagedBytes"}},{"kind":"Field","name":{"kind":"Name","value":"errorCode"}},{"kind":"Field","name":{"kind":"Name","value":"errorMessage"}},{"kind":"Field","name":{"kind":"Name","value":"warnings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"message"}}]}},{"kind":"Field","name":{"kind":"Name","value":"logLines"}},{"kind":"Field","name":{"kind":"Name","value":"logLinesDropped"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]} as unknown as DocumentNode<RestoreFieldsFragment, unknown>;
export const RestoreTempConnectionFieldsFragmentDoc = {"kind":"Document","definitions":[{"kind":"FragmentDefinition","name":{"kind":"Name","value":"RestoreTempConnectionFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Connection"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"versionKey"}},{"kind":"Field","name":{"kind":"Name","value":"host"}},{"kind":"Field","name":{"kind":"Name","value":"port"}},{"kind":"Field","name":{"kind":"Name","value":"tlsMode"}},{"kind":"Field","name":{"kind":"Name","value":"insecureTransport"}},{"kind":"Field","name":{"kind":"Name","value":"pinStatus"}},{"kind":"Field","name":{"kind":"Name","value":"sshEnabled"}},{"kind":"Field","name":{"kind":"Name","value":"ready"}},{"kind":"Field","name":{"kind":"Name","value":"notReadyReason"}},{"kind":"Field","name":{"kind":"Name","value":"temporary"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"settings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"tlsPin"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"subject"}},{"kind":"Field","name":{"kind":"Name","value":"notAfter"}}]}},{"kind":"Field","name":{"kind":"Name","value":"ssh"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"host"}},{"kind":"Field","name":{"kind":"Name","value":"port"}},{"kind":"Field","name":{"kind":"Name","value":"hostKey"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"fingerprintSha256"}},{"kind":"Field","name":{"kind":"Name","value":"confirmedAt"}}]}}]}}]}}]}}]} as unknown as DocumentNode<RestoreTempConnectionFieldsFragment, unknown>;
export const AdminUserFieldsFragmentDoc = {"kind":"Document","definitions":[{"kind":"FragmentDefinition","name":{"kind":"Name","value":"AdminUserFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"User"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"email"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"role"}},{"kind":"Field","name":{"kind":"Name","value":"disabled"}},{"kind":"Field","name":{"kind":"Name","value":"lockedUntil"}},{"kind":"Field","name":{"kind":"Name","value":"passwordChangeRequired"}},{"kind":"Field","name":{"kind":"Name","value":"temporaryPasswordExpiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorRequired"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorEnrollmentDeadline"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorEnrolled"}},{"kind":"Field","name":{"kind":"Name","value":"lastSeenAt"}},{"kind":"Field","name":{"kind":"Name","value":"emailVerified"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}},{"kind":"Field","name":{"kind":"Name","value":"deletedAt"}}]}}]} as unknown as DocumentNode<AdminUserFieldsFragment, unknown>;
export const AccountEventsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"AccountEvents"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"accountEvents"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"session"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"sessionId"}},{"kind":"Field","name":{"kind":"Name","value":"current"}}]}},{"kind":"Field","name":{"kind":"Name","value":"notification"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"NotificationFields"}}]}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"NotificationFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Notification"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"group"}},{"kind":"Field","name":{"kind":"Name","value":"severity"}},{"kind":"Field","name":{"kind":"Name","value":"title"}},{"kind":"Field","name":{"kind":"Name","value":"body"}},{"kind":"Field","name":{"kind":"Name","value":"link"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"readAt"}}]}}]} as unknown as DocumentNode<AccountEventsSubscription, AccountEventsSubscriptionVariables>;
export const ChangePasswordDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ChangePassword"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"currentPassword"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"newPassword"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"changePassword"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"currentPassword"},"value":{"kind":"Variable","name":{"kind":"Name","value":"currentPassword"}}},{"kind":"Argument","name":{"kind":"Name","value":"newPassword"},"value":{"kind":"Variable","name":{"kind":"Name","value":"newPassword"}}}]}]}}]} as unknown as DocumentNode<ChangePasswordMutation, ChangePasswordMutationVariables>;
export const MySessionsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"MySessions"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"mySessions"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"lastSeenAt"}},{"kind":"Field","name":{"kind":"Name","value":"idleExpiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"absoluteExpiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"ip"}},{"kind":"Field","name":{"kind":"Name","value":"userAgent"}},{"kind":"Field","name":{"kind":"Name","value":"aal"}},{"kind":"Field","name":{"kind":"Name","value":"current"}}]}}]}}]} as unknown as DocumentNode<MySessionsQuery, MySessionsQueryVariables>;
export const RevokeSessionDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RevokeSession"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"revokeSession"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}]}]}}]} as unknown as DocumentNode<RevokeSessionMutation, RevokeSessionMutationVariables>;
export const TwoFactorOverviewDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"TwoFactorOverview"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"myTwoFactorStatus"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"totpEnabled"}},{"kind":"Field","name":{"kind":"Name","value":"totpEnabledAt"}},{"kind":"Field","name":{"kind":"Name","value":"totpEnrollmentPending"}},{"kind":"Field","name":{"kind":"Name","value":"recoveryCodesRemaining"}}]}},{"kind":"Field","name":{"kind":"Name","value":"webauthnStatus"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"webauthnAvailable"}},{"kind":"Field","name":{"kind":"Name","value":"reason"}},{"kind":"Field","name":{"kind":"Name","value":"rpId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"myPasskeys"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"lastUsedAt"}},{"kind":"Field","name":{"kind":"Name","value":"transports"}},{"kind":"Field","name":{"kind":"Name","value":"backupEligible"}},{"kind":"Field","name":{"kind":"Name","value":"backedUp"}},{"kind":"Field","name":{"kind":"Name","value":"userVerified"}},{"kind":"Field","name":{"kind":"Name","value":"signCountAnomalyAt"}}]}}]}}]} as unknown as DocumentNode<TwoFactorOverviewQuery, TwoFactorOverviewQueryVariables>;
export const BeginTotpEnrollmentDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"BeginTotpEnrollment"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"beginTotpEnrollment"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"qrCodeSvg"}},{"kind":"Field","name":{"kind":"Name","value":"manualEntryKey"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}}]}}]}}]} as unknown as DocumentNode<BeginTotpEnrollmentMutation, BeginTotpEnrollmentMutationVariables>;
export const ConfirmTotpEnrollmentDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ConfirmTotpEnrollment"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"code"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"confirmTotpEnrollment"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"code"},"value":{"kind":"Variable","name":{"kind":"Name","value":"code"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"codes"}}]}}]}}]} as unknown as DocumentNode<ConfirmTotpEnrollmentMutation, ConfirmTotpEnrollmentMutationVariables>;
export const DisableTotpDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"DisableTotp"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"disableTotp"}}]}}]} as unknown as DocumentNode<DisableTotpMutation, DisableTotpMutationVariables>;
export const RegenerateRecoveryCodesDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RegenerateRecoveryCodes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"regenerateRecoveryCodes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"codes"}}]}}]}}]} as unknown as DocumentNode<RegenerateRecoveryCodesMutation, RegenerateRecoveryCodesMutationVariables>;
export const BeginPasskeyRegistrationDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"BeginPasskeyRegistration"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"beginPasskeyRegistration"}}]}}]} as unknown as DocumentNode<BeginPasskeyRegistrationMutation, BeginPasskeyRegistrationMutationVariables>;
export const FinishPasskeyRegistrationDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"FinishPasskeyRegistration"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"name"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"response"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"JSON"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"finishPasskeyRegistration"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"name"},"value":{"kind":"Variable","name":{"kind":"Name","value":"name"}}},{"kind":"Argument","name":{"kind":"Name","value":"response"},"value":{"kind":"Variable","name":{"kind":"Name","value":"response"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}}]} as unknown as DocumentNode<FinishPasskeyRegistrationMutation, FinishPasskeyRegistrationMutationVariables>;
export const RenamePasskeyDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RenamePasskey"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"name"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"renamePasskey"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"name"},"value":{"kind":"Variable","name":{"kind":"Name","value":"name"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}}]} as unknown as DocumentNode<RenamePasskeyMutation, RenamePasskeyMutationVariables>;
export const DeletePasskeyDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"DeletePasskey"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"deletePasskey"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}]}]}}]} as unknown as DocumentNode<DeletePasskeyMutation, DeletePasskeyMutationVariables>;
export const AdminSmtpSettingsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminSmtpSettings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"smtpSettings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"SmtpSettingsFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"SmtpSettingsFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"SmtpSettings"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"configured"}},{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"host"}},{"kind":"Field","name":{"kind":"Name","value":"port"}},{"kind":"Field","name":{"kind":"Name","value":"mode"}},{"kind":"Field","name":{"kind":"Name","value":"username"}},{"kind":"Field","name":{"kind":"Name","value":"hasPassword"}},{"kind":"Field","name":{"kind":"Name","value":"fromAddress"}},{"kind":"Field","name":{"kind":"Name","value":"fromName"}},{"kind":"Field","name":{"kind":"Name","value":"caBundlePem"}},{"kind":"Field","name":{"kind":"Name","value":"connectTimeoutSeconds"}},{"kind":"Field","name":{"kind":"Name","value":"sendTimeoutSeconds"}},{"kind":"Field","name":{"kind":"Name","value":"mayBeUnencrypted"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]} as unknown as DocumentNode<AdminSmtpSettingsQuery, AdminSmtpSettingsQueryVariables>;
export const AdminUpdateSmtpSettingsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminUpdateSmtpSettings"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UpdateSmtpSettingsInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"updateSmtpSettings"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"SmtpSettingsFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"SmtpSettingsFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"SmtpSettings"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"configured"}},{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"host"}},{"kind":"Field","name":{"kind":"Name","value":"port"}},{"kind":"Field","name":{"kind":"Name","value":"mode"}},{"kind":"Field","name":{"kind":"Name","value":"username"}},{"kind":"Field","name":{"kind":"Name","value":"hasPassword"}},{"kind":"Field","name":{"kind":"Name","value":"fromAddress"}},{"kind":"Field","name":{"kind":"Name","value":"fromName"}},{"kind":"Field","name":{"kind":"Name","value":"caBundlePem"}},{"kind":"Field","name":{"kind":"Name","value":"connectTimeoutSeconds"}},{"kind":"Field","name":{"kind":"Name","value":"sendTimeoutSeconds"}},{"kind":"Field","name":{"kind":"Name","value":"mayBeUnencrypted"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]} as unknown as DocumentNode<AdminUpdateSmtpSettingsMutation, AdminUpdateSmtpSettingsMutationVariables>;
export const AdminSendTestEmailDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminSendTestEmail"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"to"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"sendTestEmail"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"to"},"value":{"kind":"Variable","name":{"kind":"Name","value":"to"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"ok"}},{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"detail"}},{"kind":"Field","name":{"kind":"Name","value":"encrypted"}},{"kind":"Field","name":{"kind":"Name","value":"tlsVersion"}}]}}]}}]} as unknown as DocumentNode<AdminSendTestEmailMutation, AdminSendTestEmailMutationVariables>;
export const AdminSharedChannelsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminSharedChannels"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"sharedChannels"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"SharedChannelFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"SharedChannelFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"SharedChannel"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"addresses"}},{"kind":"Field","name":{"kind":"Name","value":"displayUrl"}},{"kind":"Field","name":{"kind":"Name","value":"state"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"disabledAt"}},{"kind":"Field","name":{"kind":"Name","value":"disabledReason"}},{"kind":"Field","name":{"kind":"Name","value":"disabledMessage"}},{"kind":"Field","name":{"kind":"Name","value":"consecutiveFailures"}},{"kind":"Field","name":{"kind":"Name","value":"permanentFailures"}},{"kind":"Field","name":{"kind":"Name","value":"lastFailureAt"}},{"kind":"Field","name":{"kind":"Name","value":"lastFailureCode"}}]}},{"kind":"Field","name":{"kind":"Name","value":"attachments"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"jobId"}},{"kind":"Field","name":{"kind":"Name","value":"jobName"}},{"kind":"Field","name":{"kind":"Name","value":"groups"}},{"kind":"Field","name":{"kind":"Name","value":"events"}}]}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]} as unknown as DocumentNode<AdminSharedChannelsQuery, AdminSharedChannelsQueryVariables>;
export const AdminNotificationCatalogDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminNotificationCatalog"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"notificationCatalog"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"group"}},{"kind":"Field","name":{"kind":"Name","value":"label"}},{"kind":"Field","name":{"kind":"Name","value":"events"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"label"}},{"kind":"Field","name":{"kind":"Name","value":"group"}},{"kind":"Field","name":{"kind":"Name","value":"mandatory"}},{"kind":"Field","name":{"kind":"Name","value":"perJob"}},{"kind":"Field","name":{"kind":"Name","value":"severity"}}]}}]}}]}}]} as unknown as DocumentNode<AdminNotificationCatalogQuery, AdminNotificationCatalogQueryVariables>;
export const AdminCreateSharedChannelDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminCreateSharedChannel"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"CreateSharedChannelInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"createSharedChannel"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"channel"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"SharedChannelFields"}}]}},{"kind":"Field","name":{"kind":"Name","value":"signingKey"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"SharedChannelFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"SharedChannel"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"addresses"}},{"kind":"Field","name":{"kind":"Name","value":"displayUrl"}},{"kind":"Field","name":{"kind":"Name","value":"state"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"disabledAt"}},{"kind":"Field","name":{"kind":"Name","value":"disabledReason"}},{"kind":"Field","name":{"kind":"Name","value":"disabledMessage"}},{"kind":"Field","name":{"kind":"Name","value":"consecutiveFailures"}},{"kind":"Field","name":{"kind":"Name","value":"permanentFailures"}},{"kind":"Field","name":{"kind":"Name","value":"lastFailureAt"}},{"kind":"Field","name":{"kind":"Name","value":"lastFailureCode"}}]}},{"kind":"Field","name":{"kind":"Name","value":"attachments"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"jobId"}},{"kind":"Field","name":{"kind":"Name","value":"jobName"}},{"kind":"Field","name":{"kind":"Name","value":"groups"}},{"kind":"Field","name":{"kind":"Name","value":"events"}}]}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]} as unknown as DocumentNode<AdminCreateSharedChannelMutation, AdminCreateSharedChannelMutationVariables>;
export const AdminUpdateSharedChannelDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminUpdateSharedChannel"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UpdateSharedChannelInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"updateSharedChannel"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"SharedChannelFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"SharedChannelFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"SharedChannel"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"addresses"}},{"kind":"Field","name":{"kind":"Name","value":"displayUrl"}},{"kind":"Field","name":{"kind":"Name","value":"state"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"disabledAt"}},{"kind":"Field","name":{"kind":"Name","value":"disabledReason"}},{"kind":"Field","name":{"kind":"Name","value":"disabledMessage"}},{"kind":"Field","name":{"kind":"Name","value":"consecutiveFailures"}},{"kind":"Field","name":{"kind":"Name","value":"permanentFailures"}},{"kind":"Field","name":{"kind":"Name","value":"lastFailureAt"}},{"kind":"Field","name":{"kind":"Name","value":"lastFailureCode"}}]}},{"kind":"Field","name":{"kind":"Name","value":"attachments"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"jobId"}},{"kind":"Field","name":{"kind":"Name","value":"jobName"}},{"kind":"Field","name":{"kind":"Name","value":"groups"}},{"kind":"Field","name":{"kind":"Name","value":"events"}}]}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]} as unknown as DocumentNode<AdminUpdateSharedChannelMutation, AdminUpdateSharedChannelMutationVariables>;
export const AdminRotateSharedChannelKeyDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminRotateSharedChannelKey"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"rotateSharedChannelKey"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"channel"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"SharedChannelFields"}}]}},{"kind":"Field","name":{"kind":"Name","value":"signingKey"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"SharedChannelFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"SharedChannel"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"addresses"}},{"kind":"Field","name":{"kind":"Name","value":"displayUrl"}},{"kind":"Field","name":{"kind":"Name","value":"state"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"disabledAt"}},{"kind":"Field","name":{"kind":"Name","value":"disabledReason"}},{"kind":"Field","name":{"kind":"Name","value":"disabledMessage"}},{"kind":"Field","name":{"kind":"Name","value":"consecutiveFailures"}},{"kind":"Field","name":{"kind":"Name","value":"permanentFailures"}},{"kind":"Field","name":{"kind":"Name","value":"lastFailureAt"}},{"kind":"Field","name":{"kind":"Name","value":"lastFailureCode"}}]}},{"kind":"Field","name":{"kind":"Name","value":"attachments"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"jobId"}},{"kind":"Field","name":{"kind":"Name","value":"jobName"}},{"kind":"Field","name":{"kind":"Name","value":"groups"}},{"kind":"Field","name":{"kind":"Name","value":"events"}}]}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]} as unknown as DocumentNode<AdminRotateSharedChannelKeyMutation, AdminRotateSharedChannelKeyMutationVariables>;
export const AdminDeleteSharedChannelDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminDeleteSharedChannel"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"deleteSharedChannel"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}]}]}}]} as unknown as DocumentNode<AdminDeleteSharedChannelMutation, AdminDeleteSharedChannelMutationVariables>;
export const AdminEnableSharedChannelDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminEnableSharedChannel"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"enableSharedChannel"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"SharedChannelFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"SharedChannelFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"SharedChannel"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"addresses"}},{"kind":"Field","name":{"kind":"Name","value":"displayUrl"}},{"kind":"Field","name":{"kind":"Name","value":"state"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"disabledAt"}},{"kind":"Field","name":{"kind":"Name","value":"disabledReason"}},{"kind":"Field","name":{"kind":"Name","value":"disabledMessage"}},{"kind":"Field","name":{"kind":"Name","value":"consecutiveFailures"}},{"kind":"Field","name":{"kind":"Name","value":"permanentFailures"}},{"kind":"Field","name":{"kind":"Name","value":"lastFailureAt"}},{"kind":"Field","name":{"kind":"Name","value":"lastFailureCode"}}]}},{"kind":"Field","name":{"kind":"Name","value":"attachments"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"jobId"}},{"kind":"Field","name":{"kind":"Name","value":"jobName"}},{"kind":"Field","name":{"kind":"Name","value":"groups"}},{"kind":"Field","name":{"kind":"Name","value":"events"}}]}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]} as unknown as DocumentNode<AdminEnableSharedChannelMutation, AdminEnableSharedChannelMutationVariables>;
export const AdminDisableSharedChannelDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminDisableSharedChannel"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"disableSharedChannel"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"SharedChannelFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"SharedChannelFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"SharedChannel"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"addresses"}},{"kind":"Field","name":{"kind":"Name","value":"displayUrl"}},{"kind":"Field","name":{"kind":"Name","value":"state"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"disabledAt"}},{"kind":"Field","name":{"kind":"Name","value":"disabledReason"}},{"kind":"Field","name":{"kind":"Name","value":"disabledMessage"}},{"kind":"Field","name":{"kind":"Name","value":"consecutiveFailures"}},{"kind":"Field","name":{"kind":"Name","value":"permanentFailures"}},{"kind":"Field","name":{"kind":"Name","value":"lastFailureAt"}},{"kind":"Field","name":{"kind":"Name","value":"lastFailureCode"}}]}},{"kind":"Field","name":{"kind":"Name","value":"attachments"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"jobId"}},{"kind":"Field","name":{"kind":"Name","value":"jobName"}},{"kind":"Field","name":{"kind":"Name","value":"groups"}},{"kind":"Field","name":{"kind":"Name","value":"events"}}]}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]} as unknown as DocumentNode<AdminDisableSharedChannelMutation, AdminDisableSharedChannelMutationVariables>;
export const AdminTestSharedChannelDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminTestSharedChannel"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"testSharedChannel"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"ok"}},{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"statusCode"}}]}}]}}]} as unknown as DocumentNode<AdminTestSharedChannelMutation, AdminTestSharedChannelMutationVariables>;
export const AdminAttachSharedChannelDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminAttachSharedChannel"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"channelId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"filter"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"NotificationFilterInput"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"attachSharedChannel"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"jobId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}}},{"kind":"Argument","name":{"kind":"Name","value":"channelId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"channelId"}}},{"kind":"Argument","name":{"kind":"Name","value":"filter"},"value":{"kind":"Variable","name":{"kind":"Name","value":"filter"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"SharedChannelFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"SharedChannelFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"SharedChannel"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"addresses"}},{"kind":"Field","name":{"kind":"Name","value":"displayUrl"}},{"kind":"Field","name":{"kind":"Name","value":"state"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"disabledAt"}},{"kind":"Field","name":{"kind":"Name","value":"disabledReason"}},{"kind":"Field","name":{"kind":"Name","value":"disabledMessage"}},{"kind":"Field","name":{"kind":"Name","value":"consecutiveFailures"}},{"kind":"Field","name":{"kind":"Name","value":"permanentFailures"}},{"kind":"Field","name":{"kind":"Name","value":"lastFailureAt"}},{"kind":"Field","name":{"kind":"Name","value":"lastFailureCode"}}]}},{"kind":"Field","name":{"kind":"Name","value":"attachments"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"jobId"}},{"kind":"Field","name":{"kind":"Name","value":"jobName"}},{"kind":"Field","name":{"kind":"Name","value":"groups"}},{"kind":"Field","name":{"kind":"Name","value":"events"}}]}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]} as unknown as DocumentNode<AdminAttachSharedChannelMutation, AdminAttachSharedChannelMutationVariables>;
export const AdminDetachSharedChannelDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminDetachSharedChannel"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"channelId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"detachSharedChannel"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"jobId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}}},{"kind":"Argument","name":{"kind":"Name","value":"channelId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"channelId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"SharedChannelFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"SharedChannelFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"SharedChannel"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"addresses"}},{"kind":"Field","name":{"kind":"Name","value":"displayUrl"}},{"kind":"Field","name":{"kind":"Name","value":"state"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"disabledAt"}},{"kind":"Field","name":{"kind":"Name","value":"disabledReason"}},{"kind":"Field","name":{"kind":"Name","value":"disabledMessage"}},{"kind":"Field","name":{"kind":"Name","value":"consecutiveFailures"}},{"kind":"Field","name":{"kind":"Name","value":"permanentFailures"}},{"kind":"Field","name":{"kind":"Name","value":"lastFailureAt"}},{"kind":"Field","name":{"kind":"Name","value":"lastFailureCode"}}]}},{"kind":"Field","name":{"kind":"Name","value":"attachments"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"jobId"}},{"kind":"Field","name":{"kind":"Name","value":"jobName"}},{"kind":"Field","name":{"kind":"Name","value":"groups"}},{"kind":"Field","name":{"kind":"Name","value":"events"}}]}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]} as unknown as DocumentNode<AdminDetachSharedChannelMutation, AdminDetachSharedChannelMutationVariables>;
export const AdminJobsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminJobs"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"filter"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"JobFilter"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"first"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"jobs"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"filter"},"value":{"kind":"Variable","name":{"kind":"Name","value":"filter"}}},{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"Variable","name":{"kind":"Name","value":"first"}}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"cursor"}},{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"slug"}},{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"encryption"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"configured"}},{"kind":"Field","name":{"kind":"Name","value":"mode"}},{"kind":"Field","name":{"kind":"Name","value":"optedOut"}},{"kind":"Field","name":{"kind":"Name","value":"passphrase"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}}]}}]}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}},{"kind":"Field","name":{"kind":"Name","value":"endCursor"}}]}}]}}]}}]} as unknown as DocumentNode<AdminJobsQuery, AdminJobsQueryVariables>;
export const AdminAuditLogDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminAuditLog"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"filter"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"AuditLogFilter"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"first"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"auditLog"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"filter"},"value":{"kind":"Variable","name":{"kind":"Name","value":"filter"}}},{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"Variable","name":{"kind":"Name","value":"first"}}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"cursor"}},{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"ts"}},{"kind":"Field","name":{"kind":"Name","value":"actorKind"}},{"kind":"Field","name":{"kind":"Name","value":"actorUserId"}},{"kind":"Field","name":{"kind":"Name","value":"action"}},{"kind":"Field","name":{"kind":"Name","value":"targetType"}},{"kind":"Field","name":{"kind":"Name","value":"targetId"}},{"kind":"Field","name":{"kind":"Name","value":"ip"}},{"kind":"Field","name":{"kind":"Name","value":"details"}},{"kind":"Field","name":{"kind":"Name","value":"hash"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}},{"kind":"Field","name":{"kind":"Name","value":"endCursor"}}]}}]}}]}}]} as unknown as DocumentNode<AdminAuditLogQuery, AdminAuditLogQueryVariables>;
export const AdminAuditChainDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminAuditChain"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"auditChainStatus"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"lastVerification"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"checkedAt"}},{"kind":"Field","name":{"kind":"Name","value":"ok"}},{"kind":"Field","name":{"kind":"Name","value":"full"}},{"kind":"Field","name":{"kind":"Name","value":"finding"}},{"kind":"Field","name":{"kind":"Name","value":"verifiedThroughId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"headId"}},{"kind":"Field","name":{"kind":"Name","value":"headHash"}},{"kind":"Field","name":{"kind":"Name","value":"headAt"}},{"kind":"Field","name":{"kind":"Name","value":"latestSignedHead"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"chainId"}},{"kind":"Field","name":{"kind":"Name","value":"signedAt"}},{"kind":"Field","name":{"kind":"Name","value":"keyId"}}]}}]}}]}}]} as unknown as DocumentNode<AdminAuditChainQuery, AdminAuditChainQueryVariables>;
export const AdminSecurityAlertTypesDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminSecurityAlertTypes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"securityAlertTypes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"severity"}},{"kind":"Field","name":{"kind":"Name","value":"description"}},{"kind":"Field","name":{"kind":"Name","value":"repeat"}}]}}]}}]} as unknown as DocumentNode<AdminSecurityAlertTypesQuery, AdminSecurityAlertTypesQueryVariables>;
export const AdminSecurityInboxDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminSecurityInbox"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"filter"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"NotificationInboxFilter"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"first"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"notifications"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"filter"},"value":{"kind":"Variable","name":{"kind":"Name","value":"filter"}}},{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"Variable","name":{"kind":"Name","value":"first"}}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"cursor"}},{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"group"}},{"kind":"Field","name":{"kind":"Name","value":"severity"}},{"kind":"Field","name":{"kind":"Name","value":"title"}},{"kind":"Field","name":{"kind":"Name","value":"body"}},{"kind":"Field","name":{"kind":"Name","value":"link"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"readAt"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}},{"kind":"Field","name":{"kind":"Name","value":"endCursor"}}]}}]}}]}}]} as unknown as DocumentNode<AdminSecurityInboxQuery, AdminSecurityInboxQueryVariables>;
export const AdminSsrfAllowlistDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminSsrfAllowlist"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"ssrfAllowlist"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"SsrfAllowlistFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"SsrfAllowlistFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"SsrfAllowlist"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"cidrEntries"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"value"}},{"kind":"Field","name":{"kind":"Name","value":"scopes"}}]}},{"kind":"Field","name":{"kind":"Name","value":"hostEntries"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"value"}},{"kind":"Field","name":{"kind":"Name","value":"scopes"}}]}},{"kind":"Field","name":{"kind":"Name","value":"configScopes"}},{"kind":"Field","name":{"kind":"Name","value":"defaultScopes"}},{"kind":"Field","name":{"kind":"Name","value":"personalWebhooksWidened"}},{"kind":"Field","name":{"kind":"Name","value":"configCidrs"}},{"kind":"Field","name":{"kind":"Name","value":"configHosts"}},{"kind":"Field","name":{"kind":"Name","value":"loopbackAllowed"}},{"kind":"Field","name":{"kind":"Name","value":"ignoredEntries"}},{"kind":"Field","name":{"kind":"Name","value":"maxEntries"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]} as unknown as DocumentNode<AdminSsrfAllowlistQuery, AdminSsrfAllowlistQueryVariables>;
export const AdminUpdateSsrfAllowlistDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminUpdateSsrfAllowlist"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"SsrfAllowlistInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"updateSsrfAllowlist"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"SsrfAllowlistFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"SsrfAllowlistFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"SsrfAllowlist"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"cidrEntries"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"value"}},{"kind":"Field","name":{"kind":"Name","value":"scopes"}}]}},{"kind":"Field","name":{"kind":"Name","value":"hostEntries"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"value"}},{"kind":"Field","name":{"kind":"Name","value":"scopes"}}]}},{"kind":"Field","name":{"kind":"Name","value":"configScopes"}},{"kind":"Field","name":{"kind":"Name","value":"defaultScopes"}},{"kind":"Field","name":{"kind":"Name","value":"personalWebhooksWidened"}},{"kind":"Field","name":{"kind":"Name","value":"configCidrs"}},{"kind":"Field","name":{"kind":"Name","value":"configHosts"}},{"kind":"Field","name":{"kind":"Name","value":"loopbackAllowed"}},{"kind":"Field","name":{"kind":"Name","value":"ignoredEntries"}},{"kind":"Field","name":{"kind":"Name","value":"maxEntries"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]} as unknown as DocumentNode<AdminUpdateSsrfAllowlistMutation, AdminUpdateSsrfAllowlistMutationVariables>;
export const AdminConfigStatusDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminConfigStatus"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"configStatus"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"liveKeys"}},{"kind":"Field","name":{"kind":"Name","value":"listeningProcesses"}},{"kind":"Field","name":{"kind":"Name","value":"processes"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"ConfigProcessResultFields"}}]}},{"kind":"Field","name":{"kind":"Name","value":"lastRequest"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"ConfigReloadRequestFields"}}]}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"ConfigProcessResultFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"ConfigProcessResult"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"instanceId"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"hostname"}},{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"processStartedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"requestId"}},{"kind":"Field","name":{"kind":"Name","value":"trigger"}},{"kind":"Field","name":{"kind":"Name","value":"listening"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"errorKind"}},{"kind":"Field","name":{"kind":"Name","value":"appliedKeys"}},{"kind":"Field","name":{"kind":"Name","value":"restartKeys"}},{"kind":"Field","name":{"kind":"Name","value":"errorKeys"}},{"kind":"Field","name":{"kind":"Name","value":"weakenedKeys"}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"ConfigReloadRequestFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"ConfigReloadRequest"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"requestedAt"}},{"kind":"Field","name":{"kind":"Name","value":"trigger"}},{"kind":"Field","name":{"kind":"Name","value":"listeningProcesses"}},{"kind":"Field","name":{"kind":"Name","value":"expectedProcesses"}},{"kind":"Field","name":{"kind":"Name","value":"results"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"ConfigProcessResultFields"}}]}}]}}]} as unknown as DocumentNode<AdminConfigStatusQuery, AdminConfigStatusQueryVariables>;
export const AdminConfigReloadRequestDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminConfigReloadRequest"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"configReloadRequest"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"ConfigReloadRequestFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"ConfigProcessResultFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"ConfigProcessResult"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"instanceId"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"hostname"}},{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"processStartedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"requestId"}},{"kind":"Field","name":{"kind":"Name","value":"trigger"}},{"kind":"Field","name":{"kind":"Name","value":"listening"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"errorKind"}},{"kind":"Field","name":{"kind":"Name","value":"appliedKeys"}},{"kind":"Field","name":{"kind":"Name","value":"restartKeys"}},{"kind":"Field","name":{"kind":"Name","value":"errorKeys"}},{"kind":"Field","name":{"kind":"Name","value":"weakenedKeys"}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"ConfigReloadRequestFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"ConfigReloadRequest"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"requestedAt"}},{"kind":"Field","name":{"kind":"Name","value":"trigger"}},{"kind":"Field","name":{"kind":"Name","value":"listeningProcesses"}},{"kind":"Field","name":{"kind":"Name","value":"expectedProcesses"}},{"kind":"Field","name":{"kind":"Name","value":"results"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"ConfigProcessResultFields"}}]}}]}}]} as unknown as DocumentNode<AdminConfigReloadRequestQuery, AdminConfigReloadRequestQueryVariables>;
export const AdminReloadConfigDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminReloadConfig"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"reloadConfig"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"ConfigReloadRequestFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"ConfigProcessResultFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"ConfigProcessResult"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"instanceId"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"hostname"}},{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"processStartedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"requestId"}},{"kind":"Field","name":{"kind":"Name","value":"trigger"}},{"kind":"Field","name":{"kind":"Name","value":"listening"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"errorKind"}},{"kind":"Field","name":{"kind":"Name","value":"appliedKeys"}},{"kind":"Field","name":{"kind":"Name","value":"restartKeys"}},{"kind":"Field","name":{"kind":"Name","value":"errorKeys"}},{"kind":"Field","name":{"kind":"Name","value":"weakenedKeys"}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"ConfigReloadRequestFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"ConfigReloadRequest"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"requestedAt"}},{"kind":"Field","name":{"kind":"Name","value":"trigger"}},{"kind":"Field","name":{"kind":"Name","value":"listeningProcesses"}},{"kind":"Field","name":{"kind":"Name","value":"expectedProcesses"}},{"kind":"Field","name":{"kind":"Name","value":"results"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"ConfigProcessResultFields"}}]}}]}}]} as unknown as DocumentNode<AdminReloadConfigMutation, AdminReloadConfigMutationVariables>;
export const AdminTwoFactorPolicyDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminTwoFactorPolicy"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"twoFactorPolicy"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"requireForAll"}},{"kind":"Field","name":{"kind":"Name","value":"requiredRoles"}},{"kind":"Field","name":{"kind":"Name","value":"gracePeriodDays"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}},{"kind":"Field","name":{"kind":"Name","value":"passkeysAvailable"}}]}}]}}]} as unknown as DocumentNode<AdminTwoFactorPolicyQuery, AdminTwoFactorPolicyQueryVariables>;
export const AdminUpdateTwoFactorPolicyDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminUpdateTwoFactorPolicy"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"TwoFactorPolicyInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"updateTwoFactorPolicy"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"requireForAll"}},{"kind":"Field","name":{"kind":"Name","value":"requiredRoles"}},{"kind":"Field","name":{"kind":"Name","value":"gracePeriodDays"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}},{"kind":"Field","name":{"kind":"Name","value":"passkeysAvailable"}}]}}]}}]} as unknown as DocumentNode<AdminUpdateTwoFactorPolicyMutation, AdminUpdateTwoFactorPolicyMutationVariables>;
export const MeDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"Me"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"me"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"email"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"role"}},{"kind":"Field","name":{"kind":"Name","value":"passwordChangeRequired"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorRequired"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorEnrollmentDeadline"}}]}}]}}]} as unknown as DocumentNode<MeQuery, MeQueryVariables>;
export const LoginDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"Login"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"email"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"password"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"login"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"email"},"value":{"kind":"Variable","name":{"kind":"Name","value":"email"}}},{"kind":"Argument","name":{"kind":"Name","value":"password"},"value":{"kind":"Variable","name":{"kind":"Name","value":"password"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"secondFactorRequired"}},{"kind":"Field","name":{"kind":"Name","value":"availableFactors"}},{"kind":"Field","name":{"kind":"Name","value":"passwordChangeRequired"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorEnrollmentRequired"}},{"kind":"Field","name":{"kind":"Name","value":"user"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"email"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"role"}}]}}]}}]}}]} as unknown as DocumentNode<LoginMutation, LoginMutationVariables>;
export const LogoutDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"Logout"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"logout"}}]}}]} as unknown as DocumentNode<LogoutMutation, LogoutMutationVariables>;
export const BeginPasskeyLoginDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"BeginPasskeyLogin"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"beginPasskeyLogin"}}]}}]} as unknown as DocumentNode<BeginPasskeyLoginMutation, BeginPasskeyLoginMutationVariables>;
export const CompleteSecondFactorDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"CompleteSecondFactor"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"method"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"SecondFactorKind"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"code"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"assertion"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"JSON"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"completeSecondFactor"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"method"},"value":{"kind":"Variable","name":{"kind":"Name","value":"method"}}},{"kind":"Argument","name":{"kind":"Name","value":"code"},"value":{"kind":"Variable","name":{"kind":"Name","value":"code"}}},{"kind":"Argument","name":{"kind":"Name","value":"assertion"},"value":{"kind":"Variable","name":{"kind":"Name","value":"assertion"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"secondFactorRequired"}},{"kind":"Field","name":{"kind":"Name","value":"passwordChangeRequired"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorEnrollmentRequired"}},{"kind":"Field","name":{"kind":"Name","value":"user"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}}]}}]}}]}}]} as unknown as DocumentNode<CompleteSecondFactorMutation, CompleteSecondFactorMutationVariables>;
export const BeginStepUpDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"BeginStepUp"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"beginStepUp"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"methods"}},{"kind":"Field","name":{"kind":"Name","value":"passkeyOptions"}}]}}]}}]} as unknown as DocumentNode<BeginStepUpMutation, BeginStepUpMutationVariables>;
export const CompleteStepUpDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"CompleteStepUp"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"method"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"StepUpMethod"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"code"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"password"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"assertion"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"JSON"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"completeStepUp"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"method"},"value":{"kind":"Variable","name":{"kind":"Name","value":"method"}}},{"kind":"Argument","name":{"kind":"Name","value":"code"},"value":{"kind":"Variable","name":{"kind":"Name","value":"code"}}},{"kind":"Argument","name":{"kind":"Name","value":"password"},"value":{"kind":"Variable","name":{"kind":"Name","value":"password"}}},{"kind":"Argument","name":{"kind":"Name","value":"assertion"},"value":{"kind":"Variable","name":{"kind":"Name","value":"assertion"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"stepUpAt"}},{"kind":"Field","name":{"kind":"Name","value":"aal2At"}},{"kind":"Field","name":{"kind":"Name","value":"aal"}}]}}]}}]} as unknown as DocumentNode<CompleteStepUpMutation, CompleteStepUpMutationVariables>;
export const CalendarDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"Calendar"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"from"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Time"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"to"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Time"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"timezone"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"jobIds"}},"type":{"kind":"ListType","type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"calendar"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"from"},"value":{"kind":"Variable","name":{"kind":"Name","value":"from"}}},{"kind":"Argument","name":{"kind":"Name","value":"to"},"value":{"kind":"Variable","name":{"kind":"Name","value":"to"}}},{"kind":"Argument","name":{"kind":"Name","value":"timezone"},"value":{"kind":"Variable","name":{"kind":"Name","value":"timezone"}}},{"kind":"Argument","name":{"kind":"Name","value":"jobIds"},"value":{"kind":"Variable","name":{"kind":"Name","value":"jobIds"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"events"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"jobId"}},{"kind":"Field","name":{"kind":"Name","value":"jobName"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"start"}},{"kind":"Field","name":{"kind":"Name","value":"end"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"trigger"}},{"kind":"Field","name":{"kind":"Name","value":"runId"}},{"kind":"Field","name":{"kind":"Name","value":"destinationId"}},{"kind":"Field","name":{"kind":"Name","value":"projected"}},{"kind":"Field","name":{"kind":"Name","value":"paused"}},{"kind":"Field","name":{"kind":"Name","value":"startedEarly"}},{"kind":"Field","name":{"kind":"Name","value":"overdue"}},{"kind":"Field","name":{"kind":"Name","value":"detail"}}]}},{"kind":"Field","name":{"kind":"Name","value":"truncated"}},{"kind":"Field","name":{"kind":"Name","value":"from"}},{"kind":"Field","name":{"kind":"Name","value":"to"}},{"kind":"Field","name":{"kind":"Name","value":"timezone"}}]}}]}}]} as unknown as DocumentNode<CalendarQuery, CalendarQueryVariables>;
export const CalendarJobsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"CalendarJobs"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"jobs"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"200"}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"connection"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}},{"kind":"Field","name":{"kind":"Name","value":"destinations"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}},{"kind":"Field","name":{"kind":"Name","value":"endCursor"}}]}}]}}]}}]} as unknown as DocumentNode<CalendarJobsQuery, CalendarJobsQueryVariables>;
export const CalendarRunUpdatedDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"CalendarRunUpdated"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"runUpdated"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"trigger"}},{"kind":"Field","name":{"kind":"Name","value":"scheduledFor"}},{"kind":"Field","name":{"kind":"Name","value":"queuedAt"}},{"kind":"Field","name":{"kind":"Name","value":"startedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"errorCode"}},{"kind":"Field","name":{"kind":"Name","value":"job"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}}]}}]} as unknown as DocumentNode<CalendarRunUpdatedSubscription, CalendarRunUpdatedSubscriptionVariables>;
export const ConnectionsListDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"ConnectionsList"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"connections"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"versionKey"}},{"kind":"Field","name":{"kind":"Name","value":"legacyVersion"}},{"kind":"Field","name":{"kind":"Name","value":"host"}},{"kind":"Field","name":{"kind":"Name","value":"port"}},{"kind":"Field","name":{"kind":"Name","value":"tlsMode"}},{"kind":"Field","name":{"kind":"Name","value":"insecureTransport"}},{"kind":"Field","name":{"kind":"Name","value":"pinStatus"}},{"kind":"Field","name":{"kind":"Name","value":"pinExpiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"sshEnabled"}},{"kind":"Field","name":{"kind":"Name","value":"ready"}},{"kind":"Field","name":{"kind":"Name","value":"notReadyReason"}},{"kind":"Field","name":{"kind":"Name","value":"maxConcurrentRuns"}},{"kind":"Field","name":{"kind":"Name","value":"effectiveMaxConcurrentRuns"}},{"kind":"Field","name":{"kind":"Name","value":"lastTest"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"at"}},{"kind":"Field","name":{"kind":"Name","value":"ok"}},{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"stale"}},{"kind":"Field","name":{"kind":"Name","value":"privileges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"reasons"}},{"kind":"Field","name":{"kind":"Name","value":"acknowledgementRequired"}}]}}]}}]}}]}}]} as unknown as DocumentNode<ConnectionsListQuery, ConnectionsListQueryVariables>;
export const ConnectionDetailDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"ConnectionDetail"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"connection"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"versionKey"}},{"kind":"Field","name":{"kind":"Name","value":"legacyVersion"}},{"kind":"Field","name":{"kind":"Name","value":"host"}},{"kind":"Field","name":{"kind":"Name","value":"port"}},{"kind":"Field","name":{"kind":"Name","value":"tlsMode"}},{"kind":"Field","name":{"kind":"Name","value":"insecureTransport"}},{"kind":"Field","name":{"kind":"Name","value":"pinStatus"}},{"kind":"Field","name":{"kind":"Name","value":"pinExpiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"sshEnabled"}},{"kind":"Field","name":{"kind":"Name","value":"ready"}},{"kind":"Field","name":{"kind":"Name","value":"notReadyReason"}},{"kind":"Field","name":{"kind":"Name","value":"maxConcurrentRuns"}},{"kind":"Field","name":{"kind":"Name","value":"effectiveMaxConcurrentRuns"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}},{"kind":"Field","name":{"kind":"Name","value":"lastTest"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"at"}},{"kind":"Field","name":{"kind":"Name","value":"ok"}},{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"stale"}},{"kind":"Field","name":{"kind":"Name","value":"privileges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"reasons"}},{"kind":"Field","name":{"kind":"Name","value":"acknowledgementRequired"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"settings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"imageKey"}},{"kind":"Field","name":{"kind":"Name","value":"username"}},{"kind":"Field","name":{"kind":"Name","value":"hasPassword"}},{"kind":"Field","name":{"kind":"Name","value":"clientCertificatePem"}},{"kind":"Field","name":{"kind":"Name","value":"tlsPin"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"certificatePem"}},{"kind":"Field","name":{"kind":"Name","value":"subject"}},{"kind":"Field","name":{"kind":"Name","value":"issuer"}},{"kind":"Field","name":{"kind":"Name","value":"isCa"}},{"kind":"Field","name":{"kind":"Name","value":"leafSpkiSha256"}},{"kind":"Field","name":{"kind":"Name","value":"notAfter"}},{"kind":"Field","name":{"kind":"Name","value":"pinnedAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"backupPin"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"certificatePem"}},{"kind":"Field","name":{"kind":"Name","value":"subject"}},{"kind":"Field","name":{"kind":"Name","value":"issuer"}},{"kind":"Field","name":{"kind":"Name","value":"isCa"}},{"kind":"Field","name":{"kind":"Name","value":"leafSpkiSha256"}},{"kind":"Field","name":{"kind":"Name","value":"notAfter"}},{"kind":"Field","name":{"kind":"Name","value":"pinnedAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"ssh"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"host"}},{"kind":"Field","name":{"kind":"Name","value":"port"}},{"kind":"Field","name":{"kind":"Name","value":"user"}},{"kind":"Field","name":{"kind":"Name","value":"authMethod"}},{"kind":"Field","name":{"kind":"Name","value":"hasPassword"}},{"kind":"Field","name":{"kind":"Name","value":"hasPrivateKey"}},{"kind":"Field","name":{"kind":"Name","value":"hasPassphrase"}},{"kind":"Field","name":{"kind":"Name","value":"publicKey"}},{"kind":"Field","name":{"kind":"Name","value":"hostKey"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"fingerprintSha256"}},{"kind":"Field","name":{"kind":"Name","value":"authorizedKey"}},{"kind":"Field","name":{"kind":"Name","value":"confirmedAt"}}]}}]}}]}}]}}]}}]} as unknown as DocumentNode<ConnectionDetailQuery, ConnectionDetailQueryVariables>;
export const ConnectionEngineVersionsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"ConnectionEngineVersions"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"engine"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"DatabaseEngine"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"engineVersions"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"engine"},"value":{"kind":"Variable","name":{"kind":"Name","value":"engine"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"key"}},{"kind":"Field","name":{"kind":"Name","value":"clientVersion"}},{"kind":"Field","name":{"kind":"Name","value":"legacy"}}]}}]}}]} as unknown as DocumentNode<ConnectionEngineVersionsQuery, ConnectionEngineVersionsQueryVariables>;
export const CreateConnectionDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"CreateConnection"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"CreateConnectionInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"createConnection"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}}]} as unknown as DocumentNode<CreateConnectionMutation, CreateConnectionMutationVariables>;
export const UpdateConnectionDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"UpdateConnection"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UpdateConnectionInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"updateConnection"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}}]} as unknown as DocumentNode<UpdateConnectionMutation, UpdateConnectionMutationVariables>;
export const DeleteConnectionDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"DeleteConnection"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"deleteConnection"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}]}]}}]} as unknown as DocumentNode<DeleteConnectionMutation, DeleteConnectionMutationVariables>;
export const TestConnectionDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"TestConnection"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"testConnection"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"ok"}},{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"serverVersion"}},{"kind":"Field","name":{"kind":"Name","value":"detectedVersionKey"}},{"kind":"Field","name":{"kind":"Name","value":"compatible"}},{"kind":"Field","name":{"kind":"Name","value":"legacy"}},{"kind":"Field","name":{"kind":"Name","value":"warnings"}},{"kind":"Field","name":{"kind":"Name","value":"databaseCount"}},{"kind":"Field","name":{"kind":"Name","value":"privileges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"reasons"}},{"kind":"Field","name":{"kind":"Name","value":"acknowledgementRequired"}}]}}]}}]}}]} as unknown as DocumentNode<TestConnectionMutation, TestConnectionMutationVariables>;
export const DetectConnectionVersionDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"DetectConnectionVersion"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"detectVersion"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"serverVersion"}},{"kind":"Field","name":{"kind":"Name","value":"suggestedVersionKey"}},{"kind":"Field","name":{"kind":"Name","value":"legacy"}},{"kind":"Field","name":{"kind":"Name","value":"currentCompatible"}},{"kind":"Field","name":{"kind":"Name","value":"warnings"}}]}}]}}]} as unknown as DocumentNode<DetectConnectionVersionMutation, DetectConnectionVersionMutationVariables>;
export const PrePullConnectionImageDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"PrePullConnectionImage"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"prePullImage"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"imageRef"}},{"kind":"Field","name":{"kind":"Name","value":"alreadyPresent"}}]}}]}}]} as unknown as DocumentNode<PrePullConnectionImageMutation, PrePullConnectionImageMutationVariables>;
export const FetchConnectionCertificateDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"FetchConnectionCertificate"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"fetchCertificate"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"suggestedAnchorIndex"}},{"kind":"Field","name":{"kind":"Name","value":"leafSpkiSha256"}},{"kind":"Field","name":{"kind":"Name","value":"certificates"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"pem"}},{"kind":"Field","name":{"kind":"Name","value":"subject"}},{"kind":"Field","name":{"kind":"Name","value":"issuer"}},{"kind":"Field","name":{"kind":"Name","value":"dnsNames"}},{"kind":"Field","name":{"kind":"Name","value":"ipAddresses"}},{"kind":"Field","name":{"kind":"Name","value":"notBefore"}},{"kind":"Field","name":{"kind":"Name","value":"notAfter"}},{"kind":"Field","name":{"kind":"Name","value":"isCa"}},{"kind":"Field","name":{"kind":"Name","value":"selfSigned"}},{"kind":"Field","name":{"kind":"Name","value":"spkiSha256"}},{"kind":"Field","name":{"kind":"Name","value":"spkiSha256Hex"}},{"kind":"Field","name":{"kind":"Name","value":"fingerprintSha256"}}]}}]}}]}}]} as unknown as DocumentNode<FetchConnectionCertificateMutation, FetchConnectionCertificateMutationVariables>;
export const ConfirmConnectionCertificatePinDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ConfirmConnectionCertificatePin"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"CertificatePinInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"confirmCertificatePin"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"pinStatus"}},{"kind":"Field","name":{"kind":"Name","value":"pinExpiresAt"}}]}}]}}]} as unknown as DocumentNode<ConfirmConnectionCertificatePinMutation, ConfirmConnectionCertificatePinMutationVariables>;
export const StageConnectionBackupPinDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"StageConnectionBackupPin"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"CertificatePinInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"stageBackupPin"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"pinStatus"}}]}}]}}]} as unknown as DocumentNode<StageConnectionBackupPinMutation, StageConnectionBackupPinMutationVariables>;
export const FetchConnectionSshHostKeyDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"FetchConnectionSshHostKey"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"fetchSshHostKey"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"fingerprintSha256"}},{"kind":"Field","name":{"kind":"Name","value":"authorizedKey"}},{"kind":"Field","name":{"kind":"Name","value":"confirmedAt"}},{"kind":"Field","name":{"kind":"Name","value":"host"}},{"kind":"Field","name":{"kind":"Name","value":"port"}}]}}]}}]} as unknown as DocumentNode<FetchConnectionSshHostKeyMutation, FetchConnectionSshHostKeyMutationVariables>;
export const ConfirmConnectionSshHostKeyDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ConfirmConnectionSshHostKey"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"authorizedKey"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"expectedHost"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"expectedPort"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"confirmSshHostKey"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"authorizedKey"},"value":{"kind":"Variable","name":{"kind":"Name","value":"authorizedKey"}}},{"kind":"Argument","name":{"kind":"Name","value":"expectedHost"},"value":{"kind":"Variable","name":{"kind":"Name","value":"expectedHost"}}},{"kind":"Argument","name":{"kind":"Name","value":"expectedPort"},"value":{"kind":"Variable","name":{"kind":"Name","value":"expectedPort"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"ready"}}]}}]}}]} as unknown as DocumentNode<ConfirmConnectionSshHostKeyMutation, ConfirmConnectionSshHostKeyMutationVariables>;
export const SetConnectionTlsModeDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"SetConnectionTlsMode"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"mode"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ConnectionTlsMode"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"setTlsMode"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"mode"},"value":{"kind":"Variable","name":{"kind":"Name","value":"mode"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"tlsMode"}},{"kind":"Field","name":{"kind":"Name","value":"insecureTransport"}},{"kind":"Field","name":{"kind":"Name","value":"pinStatus"}}]}}]}}]} as unknown as DocumentNode<SetConnectionTlsModeMutation, SetConnectionTlsModeMutationVariables>;
export const GenerateConnectionSshKeypairDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"GenerateConnectionSshKeypair"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"generateConnectionSshKeypair"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"publicKey"}},{"kind":"Field","name":{"kind":"Name","value":"fingerprintSha256"}},{"kind":"Field","name":{"kind":"Name","value":"connection"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}}]}}]}}]}}]} as unknown as DocumentNode<GenerateConnectionSshKeypairMutation, GenerateConnectionSshKeypairMutationVariables>;
export const DashboardJobsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"DashboardJobs"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"first"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"jobs"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"Variable","name":{"kind":"Name","value":"first"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"nextRunAt"}},{"kind":"Field","name":{"kind":"Name","value":"scheduleDescription"}},{"kind":"Field","name":{"kind":"Name","value":"missed"}},{"kind":"Field","name":{"kind":"Name","value":"missedSince"}},{"kind":"Field","name":{"kind":"Name","value":"connection"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"encryption"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"configured"}},{"kind":"Field","name":{"kind":"Name","value":"mode"}},{"kind":"Field","name":{"kind":"Name","value":"optedOut"}}]}},{"kind":"Field","name":{"kind":"Name","value":"lastRun"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"queuedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"warnings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}}]}}]}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}}]}}]} as unknown as DocumentNode<DashboardJobsQuery, DashboardJobsQueryVariables>;
export const DashboardRunsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"DashboardRuns"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"since"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Time"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","alias":{"kind":"Name","value":"running"},"name":{"kind":"Name","value":"runs"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"filter"},"value":{"kind":"ObjectValue","fields":[{"kind":"ObjectField","name":{"kind":"Name","value":"status"},"value":{"kind":"EnumValue","value":"RUNNING"}}]}},{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"20"}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"trigger"}},{"kind":"Field","name":{"kind":"Name","value":"queuedAt"}},{"kind":"Field","name":{"kind":"Name","value":"startedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"errorCode"}},{"kind":"Field","name":{"kind":"Name","value":"errorMessage"}},{"kind":"Field","name":{"kind":"Name","value":"job"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}}]}}]}},{"kind":"Field","alias":{"kind":"Name","value":"queued"},"name":{"kind":"Name","value":"runs"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"filter"},"value":{"kind":"ObjectValue","fields":[{"kind":"ObjectField","name":{"kind":"Name","value":"status"},"value":{"kind":"EnumValue","value":"QUEUED"}}]}},{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"20"}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"trigger"}},{"kind":"Field","name":{"kind":"Name","value":"queuedAt"}},{"kind":"Field","name":{"kind":"Name","value":"startedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"errorCode"}},{"kind":"Field","name":{"kind":"Name","value":"errorMessage"}},{"kind":"Field","name":{"kind":"Name","value":"job"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}}]}}]}},{"kind":"Field","alias":{"kind":"Name","value":"failed"},"name":{"kind":"Name","value":"runs"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"filter"},"value":{"kind":"ObjectValue","fields":[{"kind":"ObjectField","name":{"kind":"Name","value":"status"},"value":{"kind":"EnumValue","value":"FAILED"}},{"kind":"ObjectField","name":{"kind":"Name","value":"since"},"value":{"kind":"Variable","name":{"kind":"Name","value":"since"}}}]}},{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"10"}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"trigger"}},{"kind":"Field","name":{"kind":"Name","value":"queuedAt"}},{"kind":"Field","name":{"kind":"Name","value":"startedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"errorCode"}},{"kind":"Field","name":{"kind":"Name","value":"errorMessage"}},{"kind":"Field","name":{"kind":"Name","value":"job"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}}]}}]} as unknown as DocumentNode<DashboardRunsQuery, DashboardRunsQueryVariables>;
export const DashboardRestoresDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"DashboardRestores"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"restores"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"20"}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"startedAt"}},{"kind":"Field","name":{"kind":"Name","value":"sourceDatabase"}},{"kind":"Field","name":{"kind":"Name","value":"targetDatabase"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"job"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}},{"kind":"Field","name":{"kind":"Name","value":"target"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}}]}}]}}]}}]} as unknown as DocumentNode<DashboardRestoresQuery, DashboardRestoresQueryVariables>;
export const DashboardActivityDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"DashboardActivity"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"activityUpdated"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"run"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"warnings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}}]}},{"kind":"Field","name":{"kind":"Name","value":"trigger"}},{"kind":"Field","name":{"kind":"Name","value":"queuedAt"}},{"kind":"Field","name":{"kind":"Name","value":"startedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"errorCode"}},{"kind":"Field","name":{"kind":"Name","value":"errorMessage"}},{"kind":"Field","name":{"kind":"Name","value":"job"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"restore"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"startedAt"}},{"kind":"Field","name":{"kind":"Name","value":"sourceDatabase"}},{"kind":"Field","name":{"kind":"Name","value":"targetDatabase"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"job"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}},{"kind":"Field","name":{"kind":"Name","value":"target"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}}]}}]}}]} as unknown as DocumentNode<DashboardActivitySubscription, DashboardActivitySubscriptionVariables>;
export const DashboardCalendarDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"DashboardCalendar"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"from"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Time"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"to"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Time"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"timezone"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"calendar"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"from"},"value":{"kind":"Variable","name":{"kind":"Name","value":"from"}}},{"kind":"Argument","name":{"kind":"Name","value":"to"},"value":{"kind":"Variable","name":{"kind":"Name","value":"to"}}},{"kind":"Argument","name":{"kind":"Name","value":"timezone"},"value":{"kind":"Variable","name":{"kind":"Name","value":"timezone"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"events"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"jobId"}},{"kind":"Field","name":{"kind":"Name","value":"jobName"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"start"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"runId"}},{"kind":"Field","name":{"kind":"Name","value":"paused"}},{"kind":"Field","name":{"kind":"Name","value":"startedEarly"}},{"kind":"Field","name":{"kind":"Name","value":"overdue"}},{"kind":"Field","name":{"kind":"Name","value":"detail"}}]}},{"kind":"Field","name":{"kind":"Name","value":"truncated"}}]}}]}}]} as unknown as DocumentNode<DashboardCalendarQuery, DashboardCalendarQueryVariables>;
export const DashboardInfraDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"DashboardInfra"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"connections"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"tlsMode"}},{"kind":"Field","name":{"kind":"Name","value":"insecureTransport"}},{"kind":"Field","name":{"kind":"Name","value":"pinStatus"}},{"kind":"Field","name":{"kind":"Name","value":"pinExpiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"lastTest"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"at"}},{"kind":"Field","name":{"kind":"Name","value":"ok"}},{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"stale"}},{"kind":"Field","name":{"kind":"Name","value":"privileges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"status"}}]}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"destinations"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"ready"}},{"kind":"Field","name":{"kind":"Name","value":"lastTest"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"at"}},{"kind":"Field","name":{"kind":"Name","value":"ok"}},{"kind":"Field","name":{"kind":"Name","value":"code"}}]}}]}}]}}]} as unknown as DocumentNode<DashboardInfraQuery, DashboardInfraQueryVariables>;
export const DashboardStorageDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"DashboardStorage"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"destinationUsage"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"destinationId"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"storedBytes"}},{"kind":"Field","name":{"kind":"Name","value":"artifactCount"}}]}}]}}]} as unknown as DocumentNode<DashboardStorageQuery, DashboardStorageQueryVariables>;
export const DashboardSystemHealthDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"DashboardSystemHealth"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"systemHealth"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"checkedAt"}},{"kind":"Field","name":{"kind":"Name","value":"runner"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"up"}},{"kind":"Field","name":{"kind":"Name","value":"dockerOk"}},{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"activeRuns"}},{"kind":"Field","name":{"kind":"Name","value":"maxConcurrentRuns"}}]}},{"kind":"Field","name":{"kind":"Name","value":"certificates"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"component"}},{"kind":"Field","name":{"kind":"Name","value":"notAfter"}}]}},{"kind":"Field","name":{"kind":"Name","value":"queue"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"queue"}},{"kind":"Field","name":{"kind":"Name","value":"state"}},{"kind":"Field","name":{"kind":"Name","value":"jobs"}}]}},{"kind":"Field","name":{"kind":"Name","value":"earliestPinNotAfter"}},{"kind":"Field","name":{"kind":"Name","value":"oldestLastSuccess"}},{"kind":"Field","name":{"kind":"Name","value":"schedulerEnabled"}},{"kind":"Field","name":{"kind":"Name","value":"lastSchedulerTick"}}]}}]}}]} as unknown as DocumentNode<DashboardSystemHealthQuery, DashboardSystemHealthQueryVariables>;
export const DashboardAlertsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"DashboardAlerts"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"unreadNotificationCount"}},{"kind":"Field","name":{"kind":"Name","value":"notifications"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"unreadOnly"},"value":{"kind":"BooleanValue","value":true}},{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"5"}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"severity"}},{"kind":"Field","name":{"kind":"Name","value":"title"}},{"kind":"Field","name":{"kind":"Name","value":"link"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}}]}}]}}]}}]}}]} as unknown as DocumentNode<DashboardAlertsQuery, DashboardAlertsQueryVariables>;
export const DestinationsListDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"DestinationsList"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"destinations"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"noDelete"}},{"kind":"Field","name":{"kind":"Name","value":"objectLockMode"}},{"kind":"Field","name":{"kind":"Name","value":"objectLockRetentionDays"}},{"kind":"Field","name":{"kind":"Name","value":"ready"}},{"kind":"Field","name":{"kind":"Name","value":"notReadyReason"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}},{"kind":"Field","name":{"kind":"Name","value":"lastTest"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"at"}},{"kind":"Field","name":{"kind":"Name","value":"ok"}},{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"stale"}}]}}]}}]}}]} as unknown as DocumentNode<DestinationsListQuery, DestinationsListQueryVariables>;
export const DestinationDetailDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"DestinationDetail"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"destination"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"noDelete"}},{"kind":"Field","name":{"kind":"Name","value":"objectLockMode"}},{"kind":"Field","name":{"kind":"Name","value":"objectLockRetentionDays"}},{"kind":"Field","name":{"kind":"Name","value":"ready"}},{"kind":"Field","name":{"kind":"Name","value":"notReadyReason"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}},{"kind":"Field","name":{"kind":"Name","value":"lastTest"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"at"}},{"kind":"Field","name":{"kind":"Name","value":"ok"}},{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"stale"}}]}},{"kind":"Field","name":{"kind":"Name","value":"settings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"disk"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"path"}}]}},{"kind":"Field","name":{"kind":"Name","value":"s3"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"endpoint"}},{"kind":"Field","name":{"kind":"Name","value":"region"}},{"kind":"Field","name":{"kind":"Name","value":"bucket"}},{"kind":"Field","name":{"kind":"Name","value":"prefix"}},{"kind":"Field","name":{"kind":"Name","value":"pathStyle"}},{"kind":"Field","name":{"kind":"Name","value":"encryption"}},{"kind":"Field","name":{"kind":"Name","value":"kmsKeyId"}},{"kind":"Field","name":{"kind":"Name","value":"storageClass"}},{"kind":"Field","name":{"kind":"Name","value":"checksumMode"}},{"kind":"Field","name":{"kind":"Name","value":"conditionalWrites"}},{"kind":"Field","name":{"kind":"Name","value":"accessKeyId"}},{"kind":"Field","name":{"kind":"Name","value":"hasSecretAccessKey"}},{"kind":"Field","name":{"kind":"Name","value":"caBundlePem"}}]}},{"kind":"Field","name":{"kind":"Name","value":"sftp"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"host"}},{"kind":"Field","name":{"kind":"Name","value":"port"}},{"kind":"Field","name":{"kind":"Name","value":"user"}},{"kind":"Field","name":{"kind":"Name","value":"basePath"}},{"kind":"Field","name":{"kind":"Name","value":"dir"}},{"kind":"Field","name":{"kind":"Name","value":"authMethod"}},{"kind":"Field","name":{"kind":"Name","value":"hasPassword"}},{"kind":"Field","name":{"kind":"Name","value":"hasPrivateKey"}},{"kind":"Field","name":{"kind":"Name","value":"hasPassphrase"}},{"kind":"Field","name":{"kind":"Name","value":"publicKey"}},{"kind":"Field","name":{"kind":"Name","value":"hostKey"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"fingerprintSha256"}},{"kind":"Field","name":{"kind":"Name","value":"authorizedKey"}},{"kind":"Field","name":{"kind":"Name","value":"confirmedAt"}}]}}]}}]}}]}}]}}]} as unknown as DocumentNode<DestinationDetailQuery, DestinationDetailQueryVariables>;
export const CreateDestinationDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"CreateDestination"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"CreateDestinationInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"createDestination"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}}]} as unknown as DocumentNode<CreateDestinationMutation, CreateDestinationMutationVariables>;
export const UpdateDestinationDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"UpdateDestination"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UpdateDestinationInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"updateDestination"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}}]} as unknown as DocumentNode<UpdateDestinationMutation, UpdateDestinationMutationVariables>;
export const DeleteDestinationDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"DeleteDestination"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"deleteDestination"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}]}]}}]} as unknown as DocumentNode<DeleteDestinationMutation, DeleteDestinationMutationVariables>;
export const TestDestinationDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"TestDestination"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"testDestination"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"ok"}},{"kind":"Field","name":{"kind":"Name","value":"probeKey"}},{"kind":"Field","name":{"kind":"Name","value":"probeRemains"}},{"kind":"Field","name":{"kind":"Name","value":"note"}},{"kind":"Field","name":{"kind":"Name","value":"steps"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"ok"}},{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"message"}}]}}]}}]}}]} as unknown as DocumentNode<TestDestinationMutation, TestDestinationMutationVariables>;
export const FetchDestinationHostKeyDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"FetchDestinationHostKey"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"fetchDestinationHostKey"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"fingerprintSha256"}},{"kind":"Field","name":{"kind":"Name","value":"authorizedKey"}},{"kind":"Field","name":{"kind":"Name","value":"confirmedAt"}},{"kind":"Field","name":{"kind":"Name","value":"host"}},{"kind":"Field","name":{"kind":"Name","value":"port"}}]}}]}}]} as unknown as DocumentNode<FetchDestinationHostKeyMutation, FetchDestinationHostKeyMutationVariables>;
export const ConfirmDestinationHostKeyDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ConfirmDestinationHostKey"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"authorizedKey"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"expectedHost"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"expectedPort"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"confirmDestinationHostKey"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"authorizedKey"},"value":{"kind":"Variable","name":{"kind":"Name","value":"authorizedKey"}}},{"kind":"Argument","name":{"kind":"Name","value":"expectedHost"},"value":{"kind":"Variable","name":{"kind":"Name","value":"expectedHost"}}},{"kind":"Argument","name":{"kind":"Name","value":"expectedPort"},"value":{"kind":"Variable","name":{"kind":"Name","value":"expectedPort"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"ready"}}]}}]}}]} as unknown as DocumentNode<ConfirmDestinationHostKeyMutation, ConfirmDestinationHostKeyMutationVariables>;
export const GenerateDestinationSshKeypairDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"GenerateDestinationSshKeypair"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"generateDestinationSshKeypair"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"publicKey"}},{"kind":"Field","name":{"kind":"Name","value":"fingerprintSha256"}},{"kind":"Field","name":{"kind":"Name","value":"destination"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}}]}}]}}]}}]} as unknown as DocumentNode<GenerateDestinationSshKeypairMutation, GenerateDestinationSshKeypairMutationVariables>;
export const JobsListDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"JobsList"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"filter"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"JobFilter"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"first"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"jobs"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"filter"},"value":{"kind":"Variable","name":{"kind":"Name","value":"filter"}}},{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"Variable","name":{"kind":"Name","value":"first"}}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"cursor"}},{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"slug"}},{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"nextRunAt"}},{"kind":"Field","name":{"kind":"Name","value":"scheduleDescription"}},{"kind":"Field","name":{"kind":"Name","value":"connection"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"encryption"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"configured"}},{"kind":"Field","name":{"kind":"Name","value":"mode"}},{"kind":"Field","name":{"kind":"Name","value":"optedOut"}}]}},{"kind":"Field","name":{"kind":"Name","value":"owners"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"userId"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"email"}},{"kind":"Field","name":{"kind":"Name","value":"role"}},{"kind":"Field","name":{"kind":"Name","value":"disabled"}}]}},{"kind":"Field","name":{"kind":"Name","value":"lastRun"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"queuedAt"}},{"kind":"Field","name":{"kind":"Name","value":"warnings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}}]}}]}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}},{"kind":"Field","name":{"kind":"Name","value":"endCursor"}}]}}]}}]}}]} as unknown as DocumentNode<JobsListQuery, JobsListQueryVariables>;
export const JobFilterConnectionsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"JobFilterConnections"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"connections"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"temporary"}}]}}]}}]} as unknown as DocumentNode<JobFilterConnectionsQuery, JobFilterConnectionsQueryVariables>;
export const JobDetailDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"JobDetail"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"job"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"slug"}},{"kind":"Field","name":{"kind":"Name","value":"description"}},{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"nextRunAt"}},{"kind":"Field","name":{"kind":"Name","value":"lastScheduledAt"}},{"kind":"Field","name":{"kind":"Name","value":"scheduleDescription"}},{"kind":"Field","name":{"kind":"Name","value":"schedule"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"every"}},{"kind":"Field","name":{"kind":"Name","value":"unit"}},{"kind":"Field","name":{"kind":"Name","value":"anchor"}},{"kind":"Field","name":{"kind":"Name","value":"weekday"}},{"kind":"Field","name":{"kind":"Name","value":"dayOfMonth"}},{"kind":"Field","name":{"kind":"Name","value":"cron"}},{"kind":"Field","name":{"kind":"Name","value":"timezone"}}]}},{"kind":"Field","name":{"kind":"Name","value":"connection"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"versionKey"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"dbMode"}},{"kind":"Field","name":{"kind":"Name","value":"databases"}},{"kind":"Field","name":{"kind":"Name","value":"exclude"}},{"kind":"Field","name":{"kind":"Name","value":"includeGlobals"}},{"kind":"Field","name":{"kind":"Name","value":"retention"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"keepLast"}},{"kind":"Field","name":{"kind":"Name","value":"keepDays"}}]}},{"kind":"Field","name":{"kind":"Name","value":"timeoutSeconds"}},{"kind":"Field","name":{"kind":"Name","value":"verification"}},{"kind":"Field","name":{"kind":"Name","value":"destinations"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"position"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"encryption"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"configured"}},{"kind":"Field","name":{"kind":"Name","value":"mode"}},{"kind":"Field","name":{"kind":"Name","value":"optedOut"}},{"kind":"Field","name":{"kind":"Name","value":"optedOutAt"}},{"kind":"Field","name":{"kind":"Name","value":"optOutReason"}},{"kind":"Field","name":{"kind":"Name","value":"passphrase"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"recipient"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"recipient"}},{"kind":"Field","name":{"kind":"Name","value":"fingerprint"}},{"kind":"Field","name":{"kind":"Name","value":"label"}},{"kind":"Field","name":{"kind":"Name","value":"generated"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"owners"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"userId"}},{"kind":"Field","name":{"kind":"Name","value":"email"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"role"}},{"kind":"Field","name":{"kind":"Name","value":"disabled"}},{"kind":"Field","name":{"kind":"Name","value":"addedAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"lastRun"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"queuedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"testRestore"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"scheduleDescription"}},{"kind":"Field","name":{"kind":"Name","value":"schedule"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"every"}},{"kind":"Field","name":{"kind":"Name","value":"unit"}},{"kind":"Field","name":{"kind":"Name","value":"anchor"}},{"kind":"Field","name":{"kind":"Name","value":"weekday"}},{"kind":"Field","name":{"kind":"Name","value":"dayOfMonth"}},{"kind":"Field","name":{"kind":"Name","value":"cron"}},{"kind":"Field","name":{"kind":"Name","value":"timezone"}}]}},{"kind":"Field","name":{"kind":"Name","value":"nextAt"}},{"kind":"Field","name":{"kind":"Name","value":"target"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"targetPrivileges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"reasons"}},{"kind":"Field","name":{"kind":"Name","value":"acknowledgementRequired"}}]}},{"kind":"Field","name":{"kind":"Name","value":"privilegedTargetAcknowledgedAt"}}]}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]}}]} as unknown as DocumentNode<JobDetailQuery, JobDetailQueryVariables>;
export const LatestTestRestoreDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"LatestTestRestore"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"testRestores"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"jobId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}}},{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"1"}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"runId"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"databaseCount"}},{"kind":"Field","name":{"kind":"Name","value":"passed"}},{"kind":"Field","name":{"kind":"Name","value":"warned"}},{"kind":"Field","name":{"kind":"Name","value":"failed"}},{"kind":"Field","name":{"kind":"Name","value":"errorMessage"}}]}}]}}]}}]}}]} as unknown as DocumentNode<LatestTestRestoreQuery, LatestTestRestoreQueryVariables>;
export const JobTestRestoresDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"JobTestRestores"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"first"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"testRestores"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"jobId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}}},{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"Variable","name":{"kind":"Name","value":"first"}}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"cursor"}},{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"runId"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"trigger"}},{"kind":"Field","name":{"kind":"Name","value":"requestedBy"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"userId"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"email"}}]}},{"kind":"Field","name":{"kind":"Name","value":"target"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"scheduledFor"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"startedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"durationSeconds"}},{"kind":"Field","name":{"kind":"Name","value":"errorCode"}},{"kind":"Field","name":{"kind":"Name","value":"errorMessage"}},{"kind":"Field","name":{"kind":"Name","value":"databaseCount"}},{"kind":"Field","name":{"kind":"Name","value":"passed"}},{"kind":"Field","name":{"kind":"Name","value":"warned"}},{"kind":"Field","name":{"kind":"Name","value":"failed"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}},{"kind":"Field","name":{"kind":"Name","value":"endCursor"}}]}}]}}]}}]} as unknown as DocumentNode<JobTestRestoresQuery, JobTestRestoresQueryVariables>;
export const TestRestoreDetailDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"TestRestoreDetail"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"testRestore"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"job"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"slug"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"runId"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"trigger"}},{"kind":"Field","name":{"kind":"Name","value":"requestedBy"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"userId"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"email"}}]}},{"kind":"Field","name":{"kind":"Name","value":"target"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"temporary"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"scheduledFor"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"startedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"durationSeconds"}},{"kind":"Field","name":{"kind":"Name","value":"errorCode"}},{"kind":"Field","name":{"kind":"Name","value":"errorMessage"}},{"kind":"Field","name":{"kind":"Name","value":"databaseCount"}},{"kind":"Field","name":{"kind":"Name","value":"passed"}},{"kind":"Field","name":{"kind":"Name","value":"warned"}},{"kind":"Field","name":{"kind":"Name","value":"failed"}},{"kind":"Field","name":{"kind":"Name","value":"databases"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"runItemId"}},{"kind":"Field","name":{"kind":"Name","value":"database"}},{"kind":"Field","name":{"kind":"Name","value":"restoreId"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"errorCode"}},{"kind":"Field","name":{"kind":"Name","value":"errorMessage"}},{"kind":"Field","name":{"kind":"Name","value":"scratchDropped"}},{"kind":"Field","name":{"kind":"Name","value":"findingsTotal"}},{"kind":"Field","name":{"kind":"Name","value":"tablesCompared"}},{"kind":"Field","name":{"kind":"Name","value":"exactTables"}},{"kind":"Field","name":{"kind":"Name","value":"restoreStatus"}},{"kind":"Field","name":{"kind":"Name","value":"restoreErrorCode"}},{"kind":"Field","name":{"kind":"Name","value":"startedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"findings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"severity"}},{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"object"}},{"kind":"Field","name":{"kind":"Name","value":"source"}},{"kind":"Field","name":{"kind":"Name","value":"restored"}},{"kind":"Field","name":{"kind":"Name","value":"delta"}}]}}]}}]}}]}}]} as unknown as DocumentNode<TestRestoreDetailQuery, TestRestoreDetailQueryVariables>;
export const TestRestoreTargetsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"TestRestoreTargets"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"connections"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"temporary"}},{"kind":"Field","name":{"kind":"Name","value":"ready"}},{"kind":"Field","name":{"kind":"Name","value":"lastTest"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"at"}},{"kind":"Field","name":{"kind":"Name","value":"ok"}},{"kind":"Field","name":{"kind":"Name","value":"stale"}},{"kind":"Field","name":{"kind":"Name","value":"privileges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"reasons"}},{"kind":"Field","name":{"kind":"Name","value":"acknowledgementRequired"}}]}}]}}]}}]}}]} as unknown as DocumentNode<TestRestoreTargetsQuery, TestRestoreTargetsQueryVariables>;
export const SetJobTestRestoreDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"SetJobTestRestore"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"JobTestRestoreInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"setJobTestRestore"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"jobId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}}},{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"testRestore"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"scheduleDescription"}},{"kind":"Field","name":{"kind":"Name","value":"schedule"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"every"}},{"kind":"Field","name":{"kind":"Name","value":"unit"}},{"kind":"Field","name":{"kind":"Name","value":"anchor"}},{"kind":"Field","name":{"kind":"Name","value":"weekday"}},{"kind":"Field","name":{"kind":"Name","value":"dayOfMonth"}},{"kind":"Field","name":{"kind":"Name","value":"cron"}},{"kind":"Field","name":{"kind":"Name","value":"timezone"}}]}},{"kind":"Field","name":{"kind":"Name","value":"nextAt"}},{"kind":"Field","name":{"kind":"Name","value":"target"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"targetPrivileges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"reasons"}},{"kind":"Field","name":{"kind":"Name","value":"acknowledgementRequired"}}]}},{"kind":"Field","name":{"kind":"Name","value":"privilegedTargetAcknowledgedAt"}}]}}]}}]}}]} as unknown as DocumentNode<SetJobTestRestoreMutation, SetJobTestRestoreMutationVariables>;
export const RunTestRestoreNowDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RunTestRestoreNow"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"runTestRestoreNow"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"jobId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}}]}}]}}]} as unknown as DocumentNode<RunTestRestoreNowMutation, RunTestRestoreNowMutationVariables>;
export const JobEditorOptionsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"JobEditorOptions"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"connections"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"versionKey"}},{"kind":"Field","name":{"kind":"Name","value":"temporary"}}]}},{"kind":"Field","name":{"kind":"Name","value":"destinations"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"ready"}},{"kind":"Field","name":{"kind":"Name","value":"notReadyReason"}}]}}]}}]} as unknown as DocumentNode<JobEditorOptionsQuery, JobEditorOptionsQueryVariables>;
export const PreviewScheduleDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"PreviewSchedule"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ScheduleInput"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"count"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"previewSchedule"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}},{"kind":"Argument","name":{"kind":"Name","value":"count"},"value":{"kind":"Variable","name":{"kind":"Name","value":"count"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"description"}},{"kind":"Field","name":{"kind":"Name","value":"next"}},{"kind":"Field","name":{"kind":"Name","value":"schedule"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"timezone"}}]}}]}}]}}]} as unknown as DocumentNode<PreviewScheduleQuery, PreviewScheduleQueryVariables>;
export const CreateJobDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"CreateJob"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"CreateJobInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"createJob"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"job"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"slug"}}]}},{"kind":"Field","name":{"kind":"Name","value":"generatedPassphrase"}},{"kind":"Field","name":{"kind":"Name","value":"generatedKeypair"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"recipient"}},{"kind":"Field","name":{"kind":"Name","value":"fingerprint"}},{"kind":"Field","name":{"kind":"Name","value":"identity"}},{"kind":"Field","name":{"kind":"Name","value":"identityFile"}},{"kind":"Field","name":{"kind":"Name","value":"fileName"}}]}}]}}]}}]} as unknown as DocumentNode<CreateJobMutation, CreateJobMutationVariables>;
export const UpdateJobDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"UpdateJob"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UpdateJobInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"updateJob"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]}}]} as unknown as DocumentNode<UpdateJobMutation, UpdateJobMutationVariables>;
export const DeleteJobDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"DeleteJob"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"deleteJob"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}]}]}}]} as unknown as DocumentNode<DeleteJobMutation, DeleteJobMutationVariables>;
export const EnableJobDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"EnableJob"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"enableJob"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"nextRunAt"}}]}}]}}]} as unknown as DocumentNode<EnableJobMutation, EnableJobMutationVariables>;
export const DisableJobDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"DisableJob"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"disableJob"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"nextRunAt"}}]}}]}}]} as unknown as DocumentNode<DisableJobMutation, DisableJobMutationVariables>;
export const RunJobNowDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RunJobNow"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"runJobNow"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"runId"}},{"kind":"Field","name":{"kind":"Name","value":"queuedAt"}},{"kind":"Field","name":{"kind":"Name","value":"job"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"nextRunAt"}}]}}]}}]}}]} as unknown as DocumentNode<RunJobNowMutation, RunJobNowMutationVariables>;
export const StartNextRunEarlyDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"StartNextRunEarly"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"startNextRunEarly"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"runId"}},{"kind":"Field","name":{"kind":"Name","value":"queuedAt"}},{"kind":"Field","name":{"kind":"Name","value":"job"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"nextRunAt"}}]}}]}}]}}]} as unknown as DocumentNode<StartNextRunEarlyMutation, StartNextRunEarlyMutationVariables>;
export const JobNextOccurrencesDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"JobNextOccurrences"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ScheduleInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"previewSchedule"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}},{"kind":"Argument","name":{"kind":"Name","value":"count"},"value":{"kind":"IntValue","value":"3"}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"next"}}]}}]}}]} as unknown as DocumentNode<JobNextOccurrencesQuery, JobNextOccurrencesQueryVariables>;
export const GenerateJobPassphraseDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"GenerateJobPassphrase"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"generateJobPassphrase"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"jobId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"passphrase"}}]}}]}}]} as unknown as DocumentNode<GenerateJobPassphraseMutation, GenerateJobPassphraseMutationVariables>;
export const SetJobPassphraseDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"SetJobPassphrase"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"passphrase"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"setJobPassphrase"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"jobId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}}},{"kind":"Argument","name":{"kind":"Name","value":"passphrase"},"value":{"kind":"Variable","name":{"kind":"Name","value":"passphrase"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"mode"}}]}}]}}]} as unknown as DocumentNode<SetJobPassphraseMutation, SetJobPassphraseMutationVariables>;
export const GenerateAgeKeypairDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"GenerateAgeKeypair"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"label"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"generateAgeKeypair"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"jobId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}}},{"kind":"Argument","name":{"kind":"Name","value":"label"},"value":{"kind":"Variable","name":{"kind":"Name","value":"label"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"recipient"}},{"kind":"Field","name":{"kind":"Name","value":"fingerprint"}},{"kind":"Field","name":{"kind":"Name","value":"identity"}},{"kind":"Field","name":{"kind":"Name","value":"identityFile"}},{"kind":"Field","name":{"kind":"Name","value":"fileName"}}]}}]}}]} as unknown as DocumentNode<GenerateAgeKeypairMutation, GenerateAgeKeypairMutationVariables>;
export const SetJobRecipientDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"SetJobRecipient"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"recipient"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"label"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"setJobRecipient"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"jobId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}}},{"kind":"Argument","name":{"kind":"Name","value":"recipient"},"value":{"kind":"Variable","name":{"kind":"Name","value":"recipient"}}},{"kind":"Argument","name":{"kind":"Name","value":"label"},"value":{"kind":"Variable","name":{"kind":"Name","value":"label"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"mode"}}]}}]}}]} as unknown as DocumentNode<SetJobRecipientMutation, SetJobRecipientMutationVariables>;
export const EnableJobEncryptionDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"EnableJobEncryption"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"mode"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"JobKeyMode"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"enableJobEncryption"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"jobId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}}},{"kind":"Argument","name":{"kind":"Name","value":"mode"},"value":{"kind":"Variable","name":{"kind":"Name","value":"mode"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"mode"}}]}}]}}]} as unknown as DocumentNode<EnableJobEncryptionMutation, EnableJobEncryptionMutationVariables>;
export const DisableJobEncryptionDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"DisableJobEncryption"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"reason"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"disableJobEncryption"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"jobId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}}},{"kind":"Argument","name":{"kind":"Name","value":"reason"},"value":{"kind":"Variable","name":{"kind":"Name","value":"reason"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"mode"}},{"kind":"Field","name":{"kind":"Name","value":"optedOut"}}]}}]}}]} as unknown as DocumentNode<DisableJobEncryptionMutation, DisableJobEncryptionMutationVariables>;
export const JobSharedChannelAttachmentsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"JobSharedChannelAttachments"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"job"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"sharedChannelAttachments"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"channelId"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"displayUrl"}},{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"groups"}},{"kind":"Field","name":{"kind":"Name","value":"events"}}]}}]}}]}}]} as unknown as DocumentNode<JobSharedChannelAttachmentsQuery, JobSharedChannelAttachmentsQueryVariables>;
export const NotificationBellDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"NotificationBell"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"unreadNotificationCount"}},{"kind":"Field","name":{"kind":"Name","value":"notifications"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"8"}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"cursor"}},{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"NotificationFields"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}},{"kind":"Field","name":{"kind":"Name","value":"endCursor"}}]}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"NotificationFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Notification"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"group"}},{"kind":"Field","name":{"kind":"Name","value":"severity"}},{"kind":"Field","name":{"kind":"Name","value":"title"}},{"kind":"Field","name":{"kind":"Name","value":"body"}},{"kind":"Field","name":{"kind":"Name","value":"link"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"readAt"}}]}}]} as unknown as DocumentNode<NotificationBellQuery, NotificationBellQueryVariables>;
export const NotificationsInboxDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"NotificationsInbox"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"unreadOnly"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"Boolean"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"first"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"notifications"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"unreadOnly"},"value":{"kind":"Variable","name":{"kind":"Name","value":"unreadOnly"}}},{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"Variable","name":{"kind":"Name","value":"first"}}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"cursor"}},{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"NotificationFields"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}},{"kind":"Field","name":{"kind":"Name","value":"endCursor"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"unreadNotificationCount"}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"NotificationFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Notification"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"group"}},{"kind":"Field","name":{"kind":"Name","value":"severity"}},{"kind":"Field","name":{"kind":"Name","value":"title"}},{"kind":"Field","name":{"kind":"Name","value":"body"}},{"kind":"Field","name":{"kind":"Name","value":"link"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"readAt"}}]}}]} as unknown as DocumentNode<NotificationsInboxQuery, NotificationsInboxQueryVariables>;
export const MarkNotificationsReadDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"MarkNotificationsRead"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"ids"}},"type":{"kind":"ListType","type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"all"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"Boolean"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"markNotificationsRead"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"ids"},"value":{"kind":"Variable","name":{"kind":"Name","value":"ids"}}},{"kind":"Argument","name":{"kind":"Name","value":"all"},"value":{"kind":"Variable","name":{"kind":"Name","value":"all"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"marked"}},{"kind":"Field","name":{"kind":"Name","value":"unreadCount"}}]}}]}}]} as unknown as DocumentNode<MarkNotificationsReadMutation, MarkNotificationsReadMutationVariables>;
export const DeleteNotificationDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"DeleteNotification"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"deleteNotification"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}]}]}}]} as unknown as DocumentNode<DeleteNotificationMutation, DeleteNotificationMutationVariables>;
export const MyNotificationPreferencesDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"MyNotificationPreferences"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"myNotificationPreferences"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"NotificationPreferencesFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"NotificationEventPreferenceFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"NotificationEventPreference"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"event"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"label"}},{"kind":"Field","name":{"kind":"Name","value":"group"}},{"kind":"Field","name":{"kind":"Name","value":"problem"}},{"kind":"Field","name":{"kind":"Name","value":"mandatory"}},{"kind":"Field","name":{"kind":"Name","value":"priority"}},{"kind":"Field","name":{"kind":"Name","value":"severity"}},{"kind":"Field","name":{"kind":"Name","value":"perJob"}}]}},{"kind":"Field","name":{"kind":"Name","value":"channels"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"channel"}},{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"source"}},{"kind":"Field","name":{"kind":"Name","value":"locked"}}]}},{"kind":"Field","name":{"kind":"Name","value":"jobOverrides"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"jobId"}},{"kind":"Field","name":{"kind":"Name","value":"jobName"}},{"kind":"Field","name":{"kind":"Name","value":"channel"}},{"kind":"Field","name":{"kind":"Name","value":"enabled"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"NotificationPreferencesFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"NotificationPreferences"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"emailVerified"}},{"kind":"Field","name":{"kind":"Name","value":"activeWebhooks"}},{"kind":"Field","name":{"kind":"Name","value":"groups"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"group"}},{"kind":"Field","name":{"kind":"Name","value":"label"}},{"kind":"Field","name":{"kind":"Name","value":"events"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"NotificationEventPreferenceFields"}}]}}]}}]}}]} as unknown as DocumentNode<MyNotificationPreferencesQuery, MyNotificationPreferencesQueryVariables>;
export const SetNotificationPreferenceDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"SetNotificationPreference"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"event"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"channel"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"NotificationChannelType"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"enabled"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Boolean"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"setNotificationPreference"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"event"},"value":{"kind":"Variable","name":{"kind":"Name","value":"event"}}},{"kind":"Argument","name":{"kind":"Name","value":"channel"},"value":{"kind":"Variable","name":{"kind":"Name","value":"channel"}}},{"kind":"Argument","name":{"kind":"Name","value":"enabled"},"value":{"kind":"Variable","name":{"kind":"Name","value":"enabled"}}},{"kind":"Argument","name":{"kind":"Name","value":"jobId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"NotificationEventPreferenceFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"NotificationEventPreferenceFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"NotificationEventPreference"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"event"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"label"}},{"kind":"Field","name":{"kind":"Name","value":"group"}},{"kind":"Field","name":{"kind":"Name","value":"problem"}},{"kind":"Field","name":{"kind":"Name","value":"mandatory"}},{"kind":"Field","name":{"kind":"Name","value":"priority"}},{"kind":"Field","name":{"kind":"Name","value":"severity"}},{"kind":"Field","name":{"kind":"Name","value":"perJob"}}]}},{"kind":"Field","name":{"kind":"Name","value":"channels"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"channel"}},{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"source"}},{"kind":"Field","name":{"kind":"Name","value":"locked"}}]}},{"kind":"Field","name":{"kind":"Name","value":"jobOverrides"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"jobId"}},{"kind":"Field","name":{"kind":"Name","value":"jobName"}},{"kind":"Field","name":{"kind":"Name","value":"channel"}},{"kind":"Field","name":{"kind":"Name","value":"enabled"}}]}}]}}]} as unknown as DocumentNode<SetNotificationPreferenceMutation, SetNotificationPreferenceMutationVariables>;
export const ResetNotificationPreferencesDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ResetNotificationPreferences"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"resetNotificationPreferences"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"jobId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"NotificationPreferencesFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"NotificationEventPreferenceFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"NotificationEventPreference"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"event"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"label"}},{"kind":"Field","name":{"kind":"Name","value":"group"}},{"kind":"Field","name":{"kind":"Name","value":"problem"}},{"kind":"Field","name":{"kind":"Name","value":"mandatory"}},{"kind":"Field","name":{"kind":"Name","value":"priority"}},{"kind":"Field","name":{"kind":"Name","value":"severity"}},{"kind":"Field","name":{"kind":"Name","value":"perJob"}}]}},{"kind":"Field","name":{"kind":"Name","value":"channels"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"channel"}},{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"source"}},{"kind":"Field","name":{"kind":"Name","value":"locked"}}]}},{"kind":"Field","name":{"kind":"Name","value":"jobOverrides"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"jobId"}},{"kind":"Field","name":{"kind":"Name","value":"jobName"}},{"kind":"Field","name":{"kind":"Name","value":"channel"}},{"kind":"Field","name":{"kind":"Name","value":"enabled"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"NotificationPreferencesFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"NotificationPreferences"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"emailVerified"}},{"kind":"Field","name":{"kind":"Name","value":"activeWebhooks"}},{"kind":"Field","name":{"kind":"Name","value":"groups"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"group"}},{"kind":"Field","name":{"kind":"Name","value":"label"}},{"kind":"Field","name":{"kind":"Name","value":"events"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"NotificationEventPreferenceFields"}}]}}]}}]}}]} as unknown as DocumentNode<ResetNotificationPreferencesMutation, ResetNotificationPreferencesMutationVariables>;
export const NotificationJobOptionsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"NotificationJobOptions"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"search"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"jobs"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"filter"},"value":{"kind":"ObjectValue","fields":[{"kind":"ObjectField","name":{"kind":"Name","value":"search"},"value":{"kind":"Variable","name":{"kind":"Name","value":"search"}}}]}},{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"50"}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}}]}}]}}]} as unknown as DocumentNode<NotificationJobOptionsQuery, NotificationJobOptionsQueryVariables>;
export const MyNotificationChannelsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"MyNotificationChannels"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"myNotificationChannels"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"email"}},{"kind":"Field","name":{"kind":"Name","value":"emailVerified"}},{"kind":"Field","name":{"kind":"Name","value":"emailVerifiedAt"}},{"kind":"Field","name":{"kind":"Name","value":"maxWebhooks"}},{"kind":"Field","name":{"kind":"Name","value":"webhooks"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"PersonalWebhookFields"}}]}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"PersonalWebhookFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"PersonalWebhook"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"label"}},{"kind":"Field","name":{"kind":"Name","value":"displayUrl"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"state"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"disabledAt"}},{"kind":"Field","name":{"kind":"Name","value":"disabledReason"}},{"kind":"Field","name":{"kind":"Name","value":"disabledMessage"}},{"kind":"Field","name":{"kind":"Name","value":"consecutiveFailures"}},{"kind":"Field","name":{"kind":"Name","value":"permanentFailures"}},{"kind":"Field","name":{"kind":"Name","value":"lastFailureAt"}},{"kind":"Field","name":{"kind":"Name","value":"lastFailureCode"}}]}}]}}]} as unknown as DocumentNode<MyNotificationChannelsQuery, MyNotificationChannelsQueryVariables>;
export const AddMyWebhookDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AddMyWebhook"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"url"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"label"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"addMyWebhook"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"url"},"value":{"kind":"Variable","name":{"kind":"Name","value":"url"}}},{"kind":"Argument","name":{"kind":"Name","value":"label"},"value":{"kind":"Variable","name":{"kind":"Name","value":"label"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"webhook"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"label"}},{"kind":"Field","name":{"kind":"Name","value":"displayUrl"}}]}},{"kind":"Field","name":{"kind":"Name","value":"signingKey"}}]}}]}}]} as unknown as DocumentNode<AddMyWebhookMutation, AddMyWebhookMutationVariables>;
export const RotateMyWebhookKeyDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RotateMyWebhookKey"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"rotateMyWebhookKey"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"webhook"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"label"}},{"kind":"Field","name":{"kind":"Name","value":"displayUrl"}}]}},{"kind":"Field","name":{"kind":"Name","value":"signingKey"}}]}}]}}]} as unknown as DocumentNode<RotateMyWebhookKeyMutation, RotateMyWebhookKeyMutationVariables>;
export const DeleteMyWebhookDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"DeleteMyWebhook"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"deleteMyWebhook"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}]}]}}]} as unknown as DocumentNode<DeleteMyWebhookMutation, DeleteMyWebhookMutationVariables>;
export const TestMyWebhookDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"TestMyWebhook"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"testMyWebhook"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"ok"}},{"kind":"Field","name":{"kind":"Name","value":"code"}}]}}]}}]} as unknown as DocumentNode<TestMyWebhookMutation, TestMyWebhookMutationVariables>;
export const ReenableMyWebhookDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ReenableMyWebhook"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"reenableMyWebhook"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"PersonalWebhookFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"PersonalWebhookFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"PersonalWebhook"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"label"}},{"kind":"Field","name":{"kind":"Name","value":"displayUrl"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"state"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"disabledAt"}},{"kind":"Field","name":{"kind":"Name","value":"disabledReason"}},{"kind":"Field","name":{"kind":"Name","value":"disabledMessage"}},{"kind":"Field","name":{"kind":"Name","value":"consecutiveFailures"}},{"kind":"Field","name":{"kind":"Name","value":"permanentFailures"}},{"kind":"Field","name":{"kind":"Name","value":"lastFailureAt"}},{"kind":"Field","name":{"kind":"Name","value":"lastFailureCode"}}]}}]}}]} as unknown as DocumentNode<ReenableMyWebhookMutation, ReenableMyWebhookMutationVariables>;
export const RequestEmailVerificationDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RequestEmailVerification"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"requestEmailVerification"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"sentTo"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}}]}}]}}]} as unknown as DocumentNode<RequestEmailVerificationMutation, RequestEmailVerificationMutationVariables>;
export const VerifyEmailDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"VerifyEmail"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"code"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"verifyEmail"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"code"},"value":{"kind":"Variable","name":{"kind":"Name","value":"code"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"emailVerified"}},{"kind":"Field","name":{"kind":"Name","value":"emailVerifiedAt"}}]}}]}}]} as unknown as DocumentNode<VerifyEmailMutation, VerifyEmailMutationVariables>;
export const RestoreJobsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"RestoreJobs"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"jobs"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"200"}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"slug"}},{"kind":"Field","name":{"kind":"Name","value":"connection"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"versionKey"}}]}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}},{"kind":"Field","name":{"kind":"Name","value":"endCursor"}}]}}]}}]}}]} as unknown as DocumentNode<RestoreJobsQuery, RestoreJobsQueryVariables>;
export const RestoreRunsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"RestoreRuns"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"runs"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"filter"},"value":{"kind":"ObjectValue","fields":[{"kind":"ObjectField","name":{"kind":"Name","value":"jobId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}}}]}},{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"25"}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"trigger"}},{"kind":"Field","name":{"kind":"Name","value":"startedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"queuedAt"}},{"kind":"Field","name":{"kind":"Name","value":"storageTimestamp"}},{"kind":"Field","name":{"kind":"Name","value":"items"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"database"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"artifacts"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"database"}},{"kind":"Field","name":{"kind":"Name","value":"size"}},{"kind":"Field","name":{"kind":"Name","value":"verified"}},{"kind":"Field","name":{"kind":"Name","value":"signed"}},{"kind":"Field","name":{"kind":"Name","value":"state"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"destination"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"encryption"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"mode"}}]}}]}}]}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}},{"kind":"Field","name":{"kind":"Name","value":"endCursor"}}]}}]}}]}}]} as unknown as DocumentNode<RestoreRunsQuery, RestoreRunsQueryVariables>;
export const RestoreTargetsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"RestoreTargets"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"artifactId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"restoreTargets"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"artifactId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"artifactId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"source"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"artifactId"}},{"kind":"Field","name":{"kind":"Name","value":"jobId"}},{"kind":"Field","name":{"kind":"Name","value":"runId"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"database"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"versionKey"}},{"kind":"Field","name":{"kind":"Name","value":"serverVersion"}},{"kind":"Field","name":{"kind":"Name","value":"needsIdentity"}}]}},{"kind":"Field","name":{"kind":"Name","value":"targets"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"versionKey"}},{"kind":"Field","name":{"kind":"Name","value":"original"}},{"kind":"Field","name":{"kind":"Name","value":"temporary"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"ready"}},{"kind":"Field","name":{"kind":"Name","value":"notReadyReason"}},{"kind":"Field","name":{"kind":"Name","value":"insecureTransport"}},{"kind":"Field","name":{"kind":"Name","value":"newDatabaseAllowed"}},{"kind":"Field","name":{"kind":"Name","value":"existingRestricted"}}]}}]}}]}}]} as unknown as DocumentNode<RestoreTargetsQuery, RestoreTargetsQueryVariables>;
export const RestoreTargetDatabasesDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"RestoreTargetDatabases"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"artifactId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"targetConnectionId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"restoreTargetDatabases"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"artifactId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"artifactId"}}},{"kind":"Argument","name":{"kind":"Name","value":"targetConnectionId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"targetConnectionId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"databases"}},{"kind":"Field","name":{"kind":"Name","value":"warnings"}},{"kind":"Field","name":{"kind":"Name","value":"listedAt"}},{"kind":"Field","name":{"kind":"Name","value":"targetPrivileges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"reasons"}},{"kind":"Field","name":{"kind":"Name","value":"acknowledgementRequired"}}]}}]}}]}}]} as unknown as DocumentNode<RestoreTargetDatabasesQuery, RestoreTargetDatabasesQueryVariables>;
export const RestoreEngineVersionsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"RestoreEngineVersions"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"engine"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"DatabaseEngine"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"engineVersions"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"engine"},"value":{"kind":"Variable","name":{"kind":"Name","value":"engine"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"key"}},{"kind":"Field","name":{"kind":"Name","value":"clientVersion"}},{"kind":"Field","name":{"kind":"Name","value":"legacy"}}]}}]}}]} as unknown as DocumentNode<RestoreEngineVersionsQuery, RestoreEngineVersionsQueryVariables>;
export const RestoreCreateTemporaryConnectionDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RestoreCreateTemporaryConnection"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"CreateTemporaryConnectionInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"createTemporaryConnection"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"RestoreTempConnectionFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"RestoreTempConnectionFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Connection"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"versionKey"}},{"kind":"Field","name":{"kind":"Name","value":"host"}},{"kind":"Field","name":{"kind":"Name","value":"port"}},{"kind":"Field","name":{"kind":"Name","value":"tlsMode"}},{"kind":"Field","name":{"kind":"Name","value":"insecureTransport"}},{"kind":"Field","name":{"kind":"Name","value":"pinStatus"}},{"kind":"Field","name":{"kind":"Name","value":"sshEnabled"}},{"kind":"Field","name":{"kind":"Name","value":"ready"}},{"kind":"Field","name":{"kind":"Name","value":"notReadyReason"}},{"kind":"Field","name":{"kind":"Name","value":"temporary"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"settings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"tlsPin"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"subject"}},{"kind":"Field","name":{"kind":"Name","value":"notAfter"}}]}},{"kind":"Field","name":{"kind":"Name","value":"ssh"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"host"}},{"kind":"Field","name":{"kind":"Name","value":"port"}},{"kind":"Field","name":{"kind":"Name","value":"hostKey"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"fingerprintSha256"}},{"kind":"Field","name":{"kind":"Name","value":"confirmedAt"}}]}}]}}]}}]}}]} as unknown as DocumentNode<RestoreCreateTemporaryConnectionMutation, RestoreCreateTemporaryConnectionMutationVariables>;
export const RestoreDeleteTemporaryConnectionDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RestoreDeleteTemporaryConnection"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"deleteTemporaryConnection"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}]}]}}]} as unknown as DocumentNode<RestoreDeleteTemporaryConnectionMutation, RestoreDeleteTemporaryConnectionMutationVariables>;
export const RestoreFetchCertificateDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RestoreFetchCertificate"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"fetchCertificate"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"certificates"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"subject"}},{"kind":"Field","name":{"kind":"Name","value":"issuer"}},{"kind":"Field","name":{"kind":"Name","value":"dnsNames"}},{"kind":"Field","name":{"kind":"Name","value":"ipAddresses"}},{"kind":"Field","name":{"kind":"Name","value":"notBefore"}},{"kind":"Field","name":{"kind":"Name","value":"notAfter"}},{"kind":"Field","name":{"kind":"Name","value":"isCa"}},{"kind":"Field","name":{"kind":"Name","value":"selfSigned"}},{"kind":"Field","name":{"kind":"Name","value":"spkiSha256"}},{"kind":"Field","name":{"kind":"Name","value":"spkiSha256Hex"}},{"kind":"Field","name":{"kind":"Name","value":"fingerprintSha256"}}]}},{"kind":"Field","name":{"kind":"Name","value":"suggestedAnchorIndex"}},{"kind":"Field","name":{"kind":"Name","value":"leafSpkiSha256"}}]}}]}}]} as unknown as DocumentNode<RestoreFetchCertificateMutation, RestoreFetchCertificateMutationVariables>;
export const RestoreConfirmCertificatePinDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RestoreConfirmCertificatePin"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"CertificatePinInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"confirmCertificatePin"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"RestoreTempConnectionFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"RestoreTempConnectionFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Connection"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"versionKey"}},{"kind":"Field","name":{"kind":"Name","value":"host"}},{"kind":"Field","name":{"kind":"Name","value":"port"}},{"kind":"Field","name":{"kind":"Name","value":"tlsMode"}},{"kind":"Field","name":{"kind":"Name","value":"insecureTransport"}},{"kind":"Field","name":{"kind":"Name","value":"pinStatus"}},{"kind":"Field","name":{"kind":"Name","value":"sshEnabled"}},{"kind":"Field","name":{"kind":"Name","value":"ready"}},{"kind":"Field","name":{"kind":"Name","value":"notReadyReason"}},{"kind":"Field","name":{"kind":"Name","value":"temporary"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"settings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"tlsPin"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"subject"}},{"kind":"Field","name":{"kind":"Name","value":"notAfter"}}]}},{"kind":"Field","name":{"kind":"Name","value":"ssh"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"host"}},{"kind":"Field","name":{"kind":"Name","value":"port"}},{"kind":"Field","name":{"kind":"Name","value":"hostKey"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"fingerprintSha256"}},{"kind":"Field","name":{"kind":"Name","value":"confirmedAt"}}]}}]}}]}}]}}]} as unknown as DocumentNode<RestoreConfirmCertificatePinMutation, RestoreConfirmCertificatePinMutationVariables>;
export const RestoreFetchSshHostKeyDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RestoreFetchSshHostKey"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"fetchSshHostKey"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"fingerprintSha256"}},{"kind":"Field","name":{"kind":"Name","value":"authorizedKey"}},{"kind":"Field","name":{"kind":"Name","value":"host"}},{"kind":"Field","name":{"kind":"Name","value":"port"}}]}}]}}]} as unknown as DocumentNode<RestoreFetchSshHostKeyMutation, RestoreFetchSshHostKeyMutationVariables>;
export const RestoreConfirmSshHostKeyDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RestoreConfirmSshHostKey"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"authorizedKey"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"expectedHost"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"expectedPort"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"confirmSshHostKey"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"authorizedKey"},"value":{"kind":"Variable","name":{"kind":"Name","value":"authorizedKey"}}},{"kind":"Argument","name":{"kind":"Name","value":"expectedHost"},"value":{"kind":"Variable","name":{"kind":"Name","value":"expectedHost"}}},{"kind":"Argument","name":{"kind":"Name","value":"expectedPort"},"value":{"kind":"Variable","name":{"kind":"Name","value":"expectedPort"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"RestoreTempConnectionFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"RestoreTempConnectionFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Connection"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"versionKey"}},{"kind":"Field","name":{"kind":"Name","value":"host"}},{"kind":"Field","name":{"kind":"Name","value":"port"}},{"kind":"Field","name":{"kind":"Name","value":"tlsMode"}},{"kind":"Field","name":{"kind":"Name","value":"insecureTransport"}},{"kind":"Field","name":{"kind":"Name","value":"pinStatus"}},{"kind":"Field","name":{"kind":"Name","value":"sshEnabled"}},{"kind":"Field","name":{"kind":"Name","value":"ready"}},{"kind":"Field","name":{"kind":"Name","value":"notReadyReason"}},{"kind":"Field","name":{"kind":"Name","value":"temporary"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"settings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"tlsPin"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"subject"}},{"kind":"Field","name":{"kind":"Name","value":"notAfter"}}]}},{"kind":"Field","name":{"kind":"Name","value":"ssh"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"host"}},{"kind":"Field","name":{"kind":"Name","value":"port"}},{"kind":"Field","name":{"kind":"Name","value":"hostKey"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"fingerprintSha256"}},{"kind":"Field","name":{"kind":"Name","value":"confirmedAt"}}]}}]}}]}}]}}]} as unknown as DocumentNode<RestoreConfirmSshHostKeyMutation, RestoreConfirmSshHostKeyMutationVariables>;
export const RestoreTestConnectionDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RestoreTestConnection"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"testConnection"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"ok"}},{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"serverVersion"}},{"kind":"Field","name":{"kind":"Name","value":"compatible"}},{"kind":"Field","name":{"kind":"Name","value":"warnings"}},{"kind":"Field","name":{"kind":"Name","value":"privileges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"reasons"}},{"kind":"Field","name":{"kind":"Name","value":"acknowledgementRequired"}}]}}]}}]}}]} as unknown as DocumentNode<RestoreTestConnectionMutation, RestoreTestConnectionMutationVariables>;
export const RestorePreviewDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"RestorePreview"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"RestorePreviewInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"restorePreview"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"mode"}},{"kind":"Field","name":{"kind":"Name","value":"targetDatabase"}},{"kind":"Field","name":{"kind":"Name","value":"warnings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"message"}}]}},{"kind":"Field","name":{"kind":"Name","value":"confirmText"}},{"kind":"Field","name":{"kind":"Name","value":"crossSource"}},{"kind":"Field","name":{"kind":"Name","value":"targetPrivileges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"reasons"}},{"kind":"Field","name":{"kind":"Name","value":"acknowledgementRequired"}}]}},{"kind":"Field","name":{"kind":"Name","value":"target"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"temporary"}},{"kind":"Field","name":{"kind":"Name","value":"insecureTransport"}}]}},{"kind":"Field","name":{"kind":"Name","value":"source"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"artifactId"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"database"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"versionKey"}},{"kind":"Field","name":{"kind":"Name","value":"serverVersion"}},{"kind":"Field","name":{"kind":"Name","value":"needsIdentity"}}]}}]}}]}}]} as unknown as DocumentNode<RestorePreviewQuery, RestorePreviewQueryVariables>;
export const RestoreFactorStatusDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"RestoreFactorStatus"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"myTwoFactorStatus"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"totpEnabled"}}]}},{"kind":"Field","name":{"kind":"Name","value":"myPasskeys"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}}]}}]}}]} as unknown as DocumentNode<RestoreFactorStatusQuery, RestoreFactorStatusQueryVariables>;
export const RestoreArtifactDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RestoreArtifact"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"RestoreArtifactInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"restoreArtifact"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"RestoreFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"RestoreFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Restore"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"job"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"slug"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"artifactId"}},{"kind":"Field","name":{"kind":"Name","value":"runId"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"sourceDatabase"}},{"kind":"Field","name":{"kind":"Name","value":"target"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"temporary"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"targetDatabase"}},{"kind":"Field","name":{"kind":"Name","value":"mode"}},{"kind":"Field","name":{"kind":"Name","value":"keepOwners"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"requestedBy"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"userId"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"email"}}]}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"startedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"durationSeconds"}},{"kind":"Field","name":{"kind":"Name","value":"stagedBytes"}},{"kind":"Field","name":{"kind":"Name","value":"errorCode"}},{"kind":"Field","name":{"kind":"Name","value":"errorMessage"}},{"kind":"Field","name":{"kind":"Name","value":"warnings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"message"}}]}},{"kind":"Field","name":{"kind":"Name","value":"logLines"}},{"kind":"Field","name":{"kind":"Name","value":"logLinesDropped"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]} as unknown as DocumentNode<RestoreArtifactMutation, RestoreArtifactMutationVariables>;
export const RestoreGlobalsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RestoreGlobals"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"RestoreGlobalsInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"restoreGlobals"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"RestoreFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"RestoreFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Restore"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"job"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"slug"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"artifactId"}},{"kind":"Field","name":{"kind":"Name","value":"runId"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"sourceDatabase"}},{"kind":"Field","name":{"kind":"Name","value":"target"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"temporary"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"targetDatabase"}},{"kind":"Field","name":{"kind":"Name","value":"mode"}},{"kind":"Field","name":{"kind":"Name","value":"keepOwners"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"requestedBy"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"userId"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"email"}}]}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"startedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"durationSeconds"}},{"kind":"Field","name":{"kind":"Name","value":"stagedBytes"}},{"kind":"Field","name":{"kind":"Name","value":"errorCode"}},{"kind":"Field","name":{"kind":"Name","value":"errorMessage"}},{"kind":"Field","name":{"kind":"Name","value":"warnings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"message"}}]}},{"kind":"Field","name":{"kind":"Name","value":"logLines"}},{"kind":"Field","name":{"kind":"Name","value":"logLinesDropped"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]} as unknown as DocumentNode<RestoreGlobalsMutation, RestoreGlobalsMutationVariables>;
export const RestoreDetailDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"RestoreDetail"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"restore"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"RestoreFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"RestoreFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Restore"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"job"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"slug"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"artifactId"}},{"kind":"Field","name":{"kind":"Name","value":"runId"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"sourceDatabase"}},{"kind":"Field","name":{"kind":"Name","value":"target"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"temporary"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"targetDatabase"}},{"kind":"Field","name":{"kind":"Name","value":"mode"}},{"kind":"Field","name":{"kind":"Name","value":"keepOwners"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"requestedBy"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"userId"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"email"}}]}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"startedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"durationSeconds"}},{"kind":"Field","name":{"kind":"Name","value":"stagedBytes"}},{"kind":"Field","name":{"kind":"Name","value":"errorCode"}},{"kind":"Field","name":{"kind":"Name","value":"errorMessage"}},{"kind":"Field","name":{"kind":"Name","value":"warnings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"message"}}]}},{"kind":"Field","name":{"kind":"Name","value":"logLines"}},{"kind":"Field","name":{"kind":"Name","value":"logLinesDropped"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]} as unknown as DocumentNode<RestoreDetailQuery, RestoreDetailQueryVariables>;
export const RestoreUpdatedDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"RestoreUpdated"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"restoreId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"restoreUpdated"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"restoreId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"restoreId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"RestoreFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"RestoreFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Restore"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"job"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"slug"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"artifactId"}},{"kind":"Field","name":{"kind":"Name","value":"runId"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"sourceDatabase"}},{"kind":"Field","name":{"kind":"Name","value":"target"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"temporary"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"targetDatabase"}},{"kind":"Field","name":{"kind":"Name","value":"mode"}},{"kind":"Field","name":{"kind":"Name","value":"keepOwners"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"requestedBy"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"userId"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"email"}}]}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"startedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"durationSeconds"}},{"kind":"Field","name":{"kind":"Name","value":"stagedBytes"}},{"kind":"Field","name":{"kind":"Name","value":"errorCode"}},{"kind":"Field","name":{"kind":"Name","value":"errorMessage"}},{"kind":"Field","name":{"kind":"Name","value":"warnings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"message"}}]}},{"kind":"Field","name":{"kind":"Name","value":"logLines"}},{"kind":"Field","name":{"kind":"Name","value":"logLinesDropped"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]} as unknown as DocumentNode<RestoreUpdatedSubscription, RestoreUpdatedSubscriptionVariables>;
export const RestoreLogsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"RestoreLogs"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"restoreId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"afterId"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"restoreLogs"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"restoreId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"restoreId"}}},{"kind":"Argument","name":{"kind":"Name","value":"afterId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"afterId"}}},{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"500"}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"restoreId"}},{"kind":"Field","name":{"kind":"Name","value":"lines"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"time"}},{"kind":"Field","name":{"kind":"Name","value":"level"}},{"kind":"Field","name":{"kind":"Name","value":"stream"}},{"kind":"Field","name":{"kind":"Name","value":"line"}}]}},{"kind":"Field","name":{"kind":"Name","value":"endId"}},{"kind":"Field","name":{"kind":"Name","value":"hasMore"}},{"kind":"Field","name":{"kind":"Name","value":"dropped"}}]}}]}}]} as unknown as DocumentNode<RestoreLogsQuery, RestoreLogsQueryVariables>;
export const RestoreCancelDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RestoreCancel"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"cancelRestore"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"restoreId"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"requestedAt"}}]}}]}}]} as unknown as DocumentNode<RestoreCancelMutation, RestoreCancelMutationVariables>;
export const RestoreHistoryDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"RestoreHistory"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"filter"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"RestoreFilter"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"restores"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"filter"},"value":{"kind":"Variable","name":{"kind":"Name","value":"filter"}}},{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"50"}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"RestoreFields"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}},{"kind":"Field","name":{"kind":"Name","value":"endCursor"}}]}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"RestoreFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"Restore"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"job"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"slug"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"artifactId"}},{"kind":"Field","name":{"kind":"Name","value":"runId"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"sourceDatabase"}},{"kind":"Field","name":{"kind":"Name","value":"target"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}},{"kind":"Field","name":{"kind":"Name","value":"temporary"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"targetDatabase"}},{"kind":"Field","name":{"kind":"Name","value":"mode"}},{"kind":"Field","name":{"kind":"Name","value":"keepOwners"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"requestedBy"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"userId"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"email"}}]}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"startedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"durationSeconds"}},{"kind":"Field","name":{"kind":"Name","value":"stagedBytes"}},{"kind":"Field","name":{"kind":"Name","value":"errorCode"}},{"kind":"Field","name":{"kind":"Name","value":"errorMessage"}},{"kind":"Field","name":{"kind":"Name","value":"warnings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"message"}}]}},{"kind":"Field","name":{"kind":"Name","value":"logLines"}},{"kind":"Field","name":{"kind":"Name","value":"logLinesDropped"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]} as unknown as DocumentNode<RestoreHistoryQuery, RestoreHistoryQueryVariables>;
export const RunsListDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"RunsList"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"filter"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"RunFilter"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"first"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"runs"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"filter"},"value":{"kind":"Variable","name":{"kind":"Name","value":"filter"}}},{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"Variable","name":{"kind":"Name","value":"first"}}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"cursor"}},{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"trigger"}},{"kind":"Field","name":{"kind":"Name","value":"scheduledFor"}},{"kind":"Field","name":{"kind":"Name","value":"queuedAt"}},{"kind":"Field","name":{"kind":"Name","value":"startedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"durationSeconds"}},{"kind":"Field","name":{"kind":"Name","value":"bytesStored"}},{"kind":"Field","name":{"kind":"Name","value":"attempt"}},{"kind":"Field","name":{"kind":"Name","value":"job"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"warnings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}}]}},{"kind":"Field","name":{"kind":"Name","value":"items"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}}]}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}},{"kind":"Field","name":{"kind":"Name","value":"endCursor"}}]}}]}}]}}]} as unknown as DocumentNode<RunsListQuery, RunsListQueryVariables>;
export const RunDetailDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"RunDetail"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"run"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"trigger"}},{"kind":"Field","name":{"kind":"Name","value":"triggeredBy"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"userId"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"email"}}]}},{"kind":"Field","name":{"kind":"Name","value":"scheduledFor"}},{"kind":"Field","name":{"kind":"Name","value":"attempt"}},{"kind":"Field","name":{"kind":"Name","value":"queuedAt"}},{"kind":"Field","name":{"kind":"Name","value":"startedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"durationSeconds"}},{"kind":"Field","name":{"kind":"Name","value":"storageTimestamp"}},{"kind":"Field","name":{"kind":"Name","value":"errorCode"}},{"kind":"Field","name":{"kind":"Name","value":"errorMessage"}},{"kind":"Field","name":{"kind":"Name","value":"warnings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"database"}},{"kind":"Field","name":{"kind":"Name","value":"destinationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"notices"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"database"}},{"kind":"Field","name":{"kind":"Name","value":"destinationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"bytesRaw"}},{"kind":"Field","name":{"kind":"Name","value":"bytesStored"}},{"kind":"Field","name":{"kind":"Name","value":"cancelRequested"}},{"kind":"Field","name":{"kind":"Name","value":"cancelRequestedAt"}},{"kind":"Field","name":{"kind":"Name","value":"logLines"}},{"kind":"Field","name":{"kind":"Name","value":"logLinesDropped"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}},{"kind":"Field","name":{"kind":"Name","value":"job"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"slug"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"items"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"database"}},{"kind":"Field","name":{"kind":"Name","value":"position"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"rawSize"}},{"kind":"Field","name":{"kind":"Name","value":"storedSize"}},{"kind":"Field","name":{"kind":"Name","value":"sha256"}},{"kind":"Field","name":{"kind":"Name","value":"verified"}},{"kind":"Field","name":{"kind":"Name","value":"startedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"durationSeconds"}},{"kind":"Field","name":{"kind":"Name","value":"errorCode"}},{"kind":"Field","name":{"kind":"Name","value":"errorMessage"}},{"kind":"Field","name":{"kind":"Name","value":"warnings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"database"}},{"kind":"Field","name":{"kind":"Name","value":"destinationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"artifacts"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"state"}},{"kind":"Field","name":{"kind":"Name","value":"verified"}},{"kind":"Field","name":{"kind":"Name","value":"signed"}},{"kind":"Field","name":{"kind":"Name","value":"size"}},{"kind":"Field","name":{"kind":"Name","value":"sha256"}},{"kind":"Field","name":{"kind":"Name","value":"objectKey"}},{"kind":"Field","name":{"kind":"Name","value":"lockUntil"}},{"kind":"Field","name":{"kind":"Name","value":"stateChangedAt"}},{"kind":"Field","name":{"kind":"Name","value":"encryption"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"mode"}},{"kind":"Field","name":{"kind":"Name","value":"passphraseVersion"}},{"kind":"Field","name":{"kind":"Name","value":"recipientVersion"}}]}},{"kind":"Field","name":{"kind":"Name","value":"destination"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"manifest"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"signed"}},{"kind":"Field","name":{"kind":"Name","value":"signingKeyId"}},{"kind":"Field","name":{"kind":"Name","value":"coveredItems"}},{"kind":"Field","name":{"kind":"Name","value":"totalItems"}},{"kind":"Field","name":{"kind":"Name","value":"copies"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"state"}},{"kind":"Field","name":{"kind":"Name","value":"destination"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}}]}}]}}]}}]} as unknown as DocumentNode<RunDetailQuery, RunDetailQueryVariables>;
export const RunActivityDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"RunActivity"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"runId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"afterLogId"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"runActivity"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"runId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"runId"}}},{"kind":"Argument","name":{"kind":"Name","value":"afterLogId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"afterLogId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"run"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"trigger"}},{"kind":"Field","name":{"kind":"Name","value":"triggeredBy"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"userId"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"email"}}]}},{"kind":"Field","name":{"kind":"Name","value":"scheduledFor"}},{"kind":"Field","name":{"kind":"Name","value":"attempt"}},{"kind":"Field","name":{"kind":"Name","value":"queuedAt"}},{"kind":"Field","name":{"kind":"Name","value":"startedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"durationSeconds"}},{"kind":"Field","name":{"kind":"Name","value":"storageTimestamp"}},{"kind":"Field","name":{"kind":"Name","value":"errorCode"}},{"kind":"Field","name":{"kind":"Name","value":"errorMessage"}},{"kind":"Field","name":{"kind":"Name","value":"warnings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"database"}},{"kind":"Field","name":{"kind":"Name","value":"destinationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"notices"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"database"}},{"kind":"Field","name":{"kind":"Name","value":"destinationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"bytesRaw"}},{"kind":"Field","name":{"kind":"Name","value":"bytesStored"}},{"kind":"Field","name":{"kind":"Name","value":"cancelRequested"}},{"kind":"Field","name":{"kind":"Name","value":"cancelRequestedAt"}},{"kind":"Field","name":{"kind":"Name","value":"logLines"}},{"kind":"Field","name":{"kind":"Name","value":"logLinesDropped"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}},{"kind":"Field","name":{"kind":"Name","value":"job"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"slug"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}},{"kind":"Field","name":{"kind":"Name","value":"items"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"database"}},{"kind":"Field","name":{"kind":"Name","value":"position"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"rawSize"}},{"kind":"Field","name":{"kind":"Name","value":"storedSize"}},{"kind":"Field","name":{"kind":"Name","value":"sha256"}},{"kind":"Field","name":{"kind":"Name","value":"verified"}},{"kind":"Field","name":{"kind":"Name","value":"startedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"durationSeconds"}},{"kind":"Field","name":{"kind":"Name","value":"errorCode"}},{"kind":"Field","name":{"kind":"Name","value":"errorMessage"}},{"kind":"Field","name":{"kind":"Name","value":"warnings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"database"}},{"kind":"Field","name":{"kind":"Name","value":"destinationId"}}]}},{"kind":"Field","name":{"kind":"Name","value":"artifacts"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"state"}},{"kind":"Field","name":{"kind":"Name","value":"verified"}},{"kind":"Field","name":{"kind":"Name","value":"signed"}},{"kind":"Field","name":{"kind":"Name","value":"size"}},{"kind":"Field","name":{"kind":"Name","value":"sha256"}},{"kind":"Field","name":{"kind":"Name","value":"objectKey"}},{"kind":"Field","name":{"kind":"Name","value":"lockUntil"}},{"kind":"Field","name":{"kind":"Name","value":"stateChangedAt"}},{"kind":"Field","name":{"kind":"Name","value":"encryption"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"mode"}},{"kind":"Field","name":{"kind":"Name","value":"passphraseVersion"}},{"kind":"Field","name":{"kind":"Name","value":"recipientVersion"}}]}},{"kind":"Field","name":{"kind":"Name","value":"destination"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"deleted"}}]}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"manifest"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"signed"}},{"kind":"Field","name":{"kind":"Name","value":"signingKeyId"}},{"kind":"Field","name":{"kind":"Name","value":"coveredItems"}},{"kind":"Field","name":{"kind":"Name","value":"totalItems"}},{"kind":"Field","name":{"kind":"Name","value":"copies"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"state"}},{"kind":"Field","name":{"kind":"Name","value":"destination"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"logs"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"runId"}},{"kind":"Field","name":{"kind":"Name","value":"endId"}},{"kind":"Field","name":{"kind":"Name","value":"done"}},{"kind":"Field","name":{"kind":"Name","value":"lines"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"time"}},{"kind":"Field","name":{"kind":"Name","value":"level"}},{"kind":"Field","name":{"kind":"Name","value":"stream"}},{"kind":"Field","name":{"kind":"Name","value":"line"}}]}}]}}]}}]}}]} as unknown as DocumentNode<RunActivitySubscription, RunActivitySubscriptionVariables>;
export const RunJobContextDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"RunJobContext"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"job"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"verification"}},{"kind":"Field","name":{"kind":"Name","value":"connection"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"engine"}}]}}]}}]}}]} as unknown as DocumentNode<RunJobContextQuery, RunJobContextQueryVariables>;
export const RunItemStatsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"RunItemStats"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"run"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"items"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"database"}},{"kind":"Field","name":{"kind":"Name","value":"stats"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"level"}},{"kind":"Field","name":{"kind":"Name","value":"consistency"}},{"kind":"Field","name":{"kind":"Name","value":"objects"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"count"}}]}},{"kind":"Field","name":{"kind":"Name","value":"tables"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"rows"}},{"kind":"Field","name":{"kind":"Name","value":"count"}},{"kind":"Field","name":{"kind":"Name","value":"exact"}},{"kind":"Field","name":{"kind":"Name","value":"estimate"}},{"kind":"Field","name":{"kind":"Name","value":"unknown"}},{"kind":"Field","name":{"kind":"Name","value":"approximate"}}]}},{"kind":"Field","name":{"kind":"Name","value":"tablesTotal"}},{"kind":"Field","name":{"kind":"Name","value":"tablesTruncated"}},{"kind":"Field","name":{"kind":"Name","value":"warnings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"count"}},{"kind":"Field","name":{"kind":"Name","value":"objects"}},{"kind":"Field","name":{"kind":"Name","value":"truncated"}}]}},{"kind":"Field","name":{"kind":"Name","value":"notices"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"count"}},{"kind":"Field","name":{"kind":"Name","value":"objects"}},{"kind":"Field","name":{"kind":"Name","value":"truncated"}}]}},{"kind":"Field","name":{"kind":"Name","value":"recordedAt"}}]}}]}}]}}]}}]} as unknown as DocumentNode<RunItemStatsQuery, RunItemStatsQueryVariables>;
export const RunTestRestoresDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"RunTestRestores"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"testRestores"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"jobId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}}},{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"50"}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"runId"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}}]}}]}}]}}]}}]} as unknown as DocumentNode<RunTestRestoresQuery, RunTestRestoresQueryVariables>;
export const TestRestoreFindingsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"TestRestoreFindings"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"testRestore"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"errorMessage"}},{"kind":"Field","name":{"kind":"Name","value":"databaseCount"}},{"kind":"Field","name":{"kind":"Name","value":"passed"}},{"kind":"Field","name":{"kind":"Name","value":"warned"}},{"kind":"Field","name":{"kind":"Name","value":"failed"}},{"kind":"Field","name":{"kind":"Name","value":"databases"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"database"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"errorMessage"}},{"kind":"Field","name":{"kind":"Name","value":"tablesCompared"}},{"kind":"Field","name":{"kind":"Name","value":"exactTables"}},{"kind":"Field","name":{"kind":"Name","value":"findingsTotal"}},{"kind":"Field","name":{"kind":"Name","value":"findings"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"severity"}},{"kind":"Field","name":{"kind":"Name","value":"code"}},{"kind":"Field","name":{"kind":"Name","value":"object"}},{"kind":"Field","name":{"kind":"Name","value":"source"}},{"kind":"Field","name":{"kind":"Name","value":"restored"}},{"kind":"Field","name":{"kind":"Name","value":"delta"}}]}}]}}]}}]}}]} as unknown as DocumentNode<TestRestoreFindingsQuery, TestRestoreFindingsQueryVariables>;
export const RunLogsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"RunLogs"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"runId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"afterId"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"ID"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"first"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"runLogs"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"runId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"runId"}}},{"kind":"Argument","name":{"kind":"Name","value":"afterId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"afterId"}}},{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"Variable","name":{"kind":"Name","value":"first"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"runId"}},{"kind":"Field","name":{"kind":"Name","value":"endId"}},{"kind":"Field","name":{"kind":"Name","value":"hasMore"}},{"kind":"Field","name":{"kind":"Name","value":"dropped"}},{"kind":"Field","name":{"kind":"Name","value":"lines"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"time"}},{"kind":"Field","name":{"kind":"Name","value":"level"}},{"kind":"Field","name":{"kind":"Name","value":"stream"}},{"kind":"Field","name":{"kind":"Name","value":"line"}}]}}]}}]}}]} as unknown as DocumentNode<RunLogsQuery, RunLogsQueryVariables>;
export const RunUpdatedDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"RunUpdated"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"runId"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"runUpdated"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"jobId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}}},{"kind":"Argument","name":{"kind":"Name","value":"runId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"runId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"startedAt"}},{"kind":"Field","name":{"kind":"Name","value":"finishedAt"}},{"kind":"Field","name":{"kind":"Name","value":"durationSeconds"}},{"kind":"Field","name":{"kind":"Name","value":"bytesRaw"}},{"kind":"Field","name":{"kind":"Name","value":"bytesStored"}},{"kind":"Field","name":{"kind":"Name","value":"cancelRequested"}},{"kind":"Field","name":{"kind":"Name","value":"logLines"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}},{"kind":"Field","name":{"kind":"Name","value":"job"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}}]}}]}}]}}]} as unknown as DocumentNode<RunUpdatedSubscription, RunUpdatedSubscriptionVariables>;
export const CancelRunDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"CancelRun"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"cancelRun"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"runId"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"retryCanceled"}}]}}]}}]} as unknown as DocumentNode<CancelRunMutation, CancelRunMutationVariables>;
export const CreateDownloadTicketDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"CreateDownloadTicket"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"artifactId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"mode"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"DownloadMode"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"ageIdentity"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"createDownloadTicket"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"artifactId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"artifactId"}}},{"kind":"Argument","name":{"kind":"Name","value":"mode"},"value":{"kind":"Variable","name":{"kind":"Name","value":"mode"}}},{"kind":"Argument","name":{"kind":"Name","value":"ageIdentity"},"value":{"kind":"Variable","name":{"kind":"Name","value":"ageIdentity"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"url"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"mode"}},{"kind":"Field","name":{"kind":"Name","value":"fileName"}},{"kind":"Field","name":{"kind":"Name","value":"format"}},{"kind":"Field","name":{"kind":"Name","value":"restoreCommand"}}]}}]}}]} as unknown as DocumentNode<CreateDownloadTicketMutation, CreateDownloadTicketMutationVariables>;
export const AdminUsersDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminUsers"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"filter"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"UserFilter"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"first"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"users"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"filter"},"value":{"kind":"Variable","name":{"kind":"Name","value":"filter"}}},{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"Variable","name":{"kind":"Name","value":"first"}}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"cursor"}},{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"AdminUserFields"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}},{"kind":"Field","name":{"kind":"Name","value":"endCursor"}}]}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"AdminUserFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"User"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"email"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"role"}},{"kind":"Field","name":{"kind":"Name","value":"disabled"}},{"kind":"Field","name":{"kind":"Name","value":"lockedUntil"}},{"kind":"Field","name":{"kind":"Name","value":"passwordChangeRequired"}},{"kind":"Field","name":{"kind":"Name","value":"temporaryPasswordExpiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorRequired"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorEnrollmentDeadline"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorEnrolled"}},{"kind":"Field","name":{"kind":"Name","value":"lastSeenAt"}},{"kind":"Field","name":{"kind":"Name","value":"emailVerified"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}},{"kind":"Field","name":{"kind":"Name","value":"deletedAt"}}]}}]} as unknown as DocumentNode<AdminUsersQuery, AdminUsersQueryVariables>;
export const AdminUserDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminUser"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"user"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"AdminUserFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"AdminUserFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"User"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"email"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"role"}},{"kind":"Field","name":{"kind":"Name","value":"disabled"}},{"kind":"Field","name":{"kind":"Name","value":"lockedUntil"}},{"kind":"Field","name":{"kind":"Name","value":"passwordChangeRequired"}},{"kind":"Field","name":{"kind":"Name","value":"temporaryPasswordExpiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorRequired"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorEnrollmentDeadline"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorEnrolled"}},{"kind":"Field","name":{"kind":"Name","value":"lastSeenAt"}},{"kind":"Field","name":{"kind":"Name","value":"emailVerified"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}},{"kind":"Field","name":{"kind":"Name","value":"deletedAt"}}]}}]} as unknown as DocumentNode<AdminUserQuery, AdminUserQueryVariables>;
export const AdminUserSessionsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminUserSessions"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"userId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"userSessions"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"userId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"userId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"lastSeenAt"}},{"kind":"Field","name":{"kind":"Name","value":"absoluteExpiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"ip"}},{"kind":"Field","name":{"kind":"Name","value":"userAgent"}},{"kind":"Field","name":{"kind":"Name","value":"aal"}},{"kind":"Field","name":{"kind":"Name","value":"current"}}]}}]}}]} as unknown as DocumentNode<AdminUserSessionsQuery, AdminUserSessionsQueryVariables>;
export const AdminCreateUserDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminCreateUser"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"CreateUserInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"createUser"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"user"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"email"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"role"}}]}},{"kind":"Field","name":{"kind":"Name","value":"temporaryPassword"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"password"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}}]}}]}}]}}]} as unknown as DocumentNode<AdminCreateUserMutation, AdminCreateUserMutationVariables>;
export const AdminResetUserPasswordDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminResetUserPassword"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"resetUserPassword"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"password"}},{"kind":"Field","name":{"kind":"Name","value":"expiresAt"}}]}}]}}]} as unknown as DocumentNode<AdminResetUserPasswordMutation, AdminResetUserPasswordMutationVariables>;
export const AdminUpdateUserDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminUpdateUser"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UpdateUserInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"updateUser"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"AdminUserFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"AdminUserFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"User"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"email"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"role"}},{"kind":"Field","name":{"kind":"Name","value":"disabled"}},{"kind":"Field","name":{"kind":"Name","value":"lockedUntil"}},{"kind":"Field","name":{"kind":"Name","value":"passwordChangeRequired"}},{"kind":"Field","name":{"kind":"Name","value":"temporaryPasswordExpiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorRequired"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorEnrollmentDeadline"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorEnrolled"}},{"kind":"Field","name":{"kind":"Name","value":"lastSeenAt"}},{"kind":"Field","name":{"kind":"Name","value":"emailVerified"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}},{"kind":"Field","name":{"kind":"Name","value":"deletedAt"}}]}}]} as unknown as DocumentNode<AdminUpdateUserMutation, AdminUpdateUserMutationVariables>;
export const AdminDisableUserDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminDisableUser"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"disableUser"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"AdminUserFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"AdminUserFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"User"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"email"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"role"}},{"kind":"Field","name":{"kind":"Name","value":"disabled"}},{"kind":"Field","name":{"kind":"Name","value":"lockedUntil"}},{"kind":"Field","name":{"kind":"Name","value":"passwordChangeRequired"}},{"kind":"Field","name":{"kind":"Name","value":"temporaryPasswordExpiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorRequired"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorEnrollmentDeadline"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorEnrolled"}},{"kind":"Field","name":{"kind":"Name","value":"lastSeenAt"}},{"kind":"Field","name":{"kind":"Name","value":"emailVerified"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}},{"kind":"Field","name":{"kind":"Name","value":"deletedAt"}}]}}]} as unknown as DocumentNode<AdminDisableUserMutation, AdminDisableUserMutationVariables>;
export const AdminEnableUserDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminEnableUser"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"enableUser"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"AdminUserFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"AdminUserFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"User"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"email"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"role"}},{"kind":"Field","name":{"kind":"Name","value":"disabled"}},{"kind":"Field","name":{"kind":"Name","value":"lockedUntil"}},{"kind":"Field","name":{"kind":"Name","value":"passwordChangeRequired"}},{"kind":"Field","name":{"kind":"Name","value":"temporaryPasswordExpiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorRequired"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorEnrollmentDeadline"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorEnrolled"}},{"kind":"Field","name":{"kind":"Name","value":"lastSeenAt"}},{"kind":"Field","name":{"kind":"Name","value":"emailVerified"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}},{"kind":"Field","name":{"kind":"Name","value":"deletedAt"}}]}}]} as unknown as DocumentNode<AdminEnableUserMutation, AdminEnableUserMutationVariables>;
export const AdminUnlockUserDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminUnlockUser"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"unlockUser"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"AdminUserFields"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"AdminUserFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"User"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"email"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"role"}},{"kind":"Field","name":{"kind":"Name","value":"disabled"}},{"kind":"Field","name":{"kind":"Name","value":"lockedUntil"}},{"kind":"Field","name":{"kind":"Name","value":"passwordChangeRequired"}},{"kind":"Field","name":{"kind":"Name","value":"temporaryPasswordExpiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorRequired"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorEnrollmentDeadline"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorEnrolled"}},{"kind":"Field","name":{"kind":"Name","value":"lastSeenAt"}},{"kind":"Field","name":{"kind":"Name","value":"emailVerified"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}},{"kind":"Field","name":{"kind":"Name","value":"deletedAt"}}]}}]} as unknown as DocumentNode<AdminUnlockUserMutation, AdminUnlockUserMutationVariables>;
export const AdminDeleteUserDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminDeleteUser"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"deleteUser"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}]}]}}]} as unknown as DocumentNode<AdminDeleteUserMutation, AdminDeleteUserMutationVariables>;
export const AdminRevokeUserSessionsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminRevokeUserSessions"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"userId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"revokeUserSessions"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"userId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"userId"}}}]}]}}]} as unknown as DocumentNode<AdminRevokeUserSessionsMutation, AdminRevokeUserSessionsMutationVariables>;
export const AdminResetUserSecondFactorsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AdminResetUserSecondFactors"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"userId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"resetUserSecondFactors"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"userId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"userId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"user"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"FragmentSpread","name":{"kind":"Name","value":"AdminUserFields"}}]}},{"kind":"Field","name":{"kind":"Name","value":"removedTotp"}},{"kind":"Field","name":{"kind":"Name","value":"removedPasskeys"}},{"kind":"Field","name":{"kind":"Name","value":"removedRecoveryCodes"}},{"kind":"Field","name":{"kind":"Name","value":"revokedSessions"}}]}}]}},{"kind":"FragmentDefinition","name":{"kind":"Name","value":"AdminUserFields"},"typeCondition":{"kind":"NamedType","name":{"kind":"Name","value":"User"}},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"email"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"role"}},{"kind":"Field","name":{"kind":"Name","value":"disabled"}},{"kind":"Field","name":{"kind":"Name","value":"lockedUntil"}},{"kind":"Field","name":{"kind":"Name","value":"passwordChangeRequired"}},{"kind":"Field","name":{"kind":"Name","value":"temporaryPasswordExpiresAt"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorRequired"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorEnrollmentDeadline"}},{"kind":"Field","name":{"kind":"Name","value":"twoFactorEnrolled"}},{"kind":"Field","name":{"kind":"Name","value":"lastSeenAt"}},{"kind":"Field","name":{"kind":"Name","value":"emailVerified"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}},{"kind":"Field","name":{"kind":"Name","value":"deletedAt"}}]}}]} as unknown as DocumentNode<AdminResetUserSecondFactorsMutation, AdminResetUserSecondFactorsMutationVariables>;
export const AdminOwnedJobsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminOwnedJobs"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"ownerId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"first"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"after"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"jobs"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"Variable","name":{"kind":"Name","value":"first"}}},{"kind":"Argument","name":{"kind":"Name","value":"after"},"value":{"kind":"Variable","name":{"kind":"Name","value":"after"}}},{"kind":"Argument","name":{"kind":"Name","value":"filter"},"value":{"kind":"ObjectValue","fields":[{"kind":"ObjectField","name":{"kind":"Name","value":"ownerId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"ownerId"}}}]}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"slug"}},{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"owners"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"userId"}},{"kind":"Field","name":{"kind":"Name","value":"addedAt"}}]}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}},{"kind":"Field","name":{"kind":"Name","value":"endCursor"}}]}}]}}]}}]} as unknown as DocumentNode<AdminOwnedJobsQuery, AdminOwnedJobsQueryVariables>;
export const AdminAssignableJobsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"AdminAssignableJobs"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"search"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"first"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"Int"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"jobs"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"Variable","name":{"kind":"Name","value":"first"}}},{"kind":"Argument","name":{"kind":"Name","value":"filter"},"value":{"kind":"ObjectValue","fields":[{"kind":"ObjectField","name":{"kind":"Name","value":"search"},"value":{"kind":"Variable","name":{"kind":"Name","value":"search"}}}]}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"slug"}},{"kind":"Field","name":{"kind":"Name","value":"enabled"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"pageInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"hasNextPage"}}]}}]}}]}}]} as unknown as DocumentNode<AdminAssignableJobsQuery, AdminAssignableJobsQueryVariables>;
export const UserPickerCandidatesDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"UserPickerCandidates"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"search"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"userDirectory"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"search"},"value":{"kind":"Variable","name":{"kind":"Name","value":"search"}}},{"kind":"Argument","name":{"kind":"Name","value":"first"},"value":{"kind":"IntValue","value":"50"}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"edges"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"node"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"email"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"role"}},{"kind":"Field","name":{"kind":"Name","value":"disabled"}}]}}]}}]}}]}}]} as unknown as DocumentNode<UserPickerCandidatesQuery, UserPickerCandidatesQueryVariables>;
export const AccountDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"Account"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"accountEvents"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"session"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"kind"}}]}}]}}]}}]} as unknown as DocumentNode<AccountSubscription, AccountSubscriptionVariables>;
export const ListDatabasesDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ListDatabases"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"listDatabases"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"databases"}},{"kind":"Field","name":{"kind":"Name","value":"warnings"}}]}}]}}]} as unknown as DocumentNode<ListDatabasesMutation, ListDatabasesMutationVariables>;
export const AssignJobOwnersDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AssignJobOwners"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"users"}},"type":{"kind":"NonNullType","type":{"kind":"ListType","type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"assignJobOwners"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"jobId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}}},{"kind":"Argument","name":{"kind":"Name","value":"users"},"value":{"kind":"Variable","name":{"kind":"Name","value":"users"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"userId"}},{"kind":"Field","name":{"kind":"Name","value":"email"}},{"kind":"Field","name":{"kind":"Name","value":"displayName"}},{"kind":"Field","name":{"kind":"Name","value":"role"}},{"kind":"Field","name":{"kind":"Name","value":"disabled"}},{"kind":"Field","name":{"kind":"Name","value":"addedAt"}}]}}]}}]} as unknown as DocumentNode<AssignJobOwnersMutation, AssignJobOwnersMutationVariables>;
export const RemoveJobOwnerDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"RemoveJobOwner"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"userId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"removeJobOwner"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"jobId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}}},{"kind":"Argument","name":{"kind":"Name","value":"userId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"userId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"userId"}}]}}]}}]} as unknown as DocumentNode<RemoveJobOwnerMutation, RemoveJobOwnerMutationVariables>;
export const ExportJobPassphrasesDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ExportJobPassphrases"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"UUID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"exportJobPassphrases"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"jobId"},"value":{"kind":"Variable","name":{"kind":"Name","value":"jobId"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"fileName"}},{"kind":"Field","name":{"kind":"Name","value":"content"}},{"kind":"Field","name":{"kind":"Name","value":"versions"}}]}}]}}]} as unknown as DocumentNode<ExportJobPassphrasesMutation, ExportJobPassphrasesMutationVariables>;
export const ServerInfoDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"ServerInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"serverInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"insecureTransport"}},{"kind":"Field","name":{"kind":"Name","value":"devMode"}}]}}]}}]} as unknown as DocumentNode<ServerInfoQuery, ServerInfoQueryVariables>;