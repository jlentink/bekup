/**
 * Pure dashboard logic (task 6.2): job health, live run and restore
 * merging, security findings, storage per destination, calendar slices and
 * system health.
 * Everything here shapes the UI only; the server's authz decides what the
 * caller sees (D-053).
 */
import type { RunStatus as UiStatus } from "@/components/status/status";
import type {
  CalendarEventKind,
  ConnectionTlsMode,
  DestinationType,
  JobEncryptionMode,
  RestoreStatus,
  RunStatus,
  TargetPrivilegeStatus,
  TlsPinStatus,
} from "@/graphql/gen/graphql";

export const DAY_MS = 86_400_000;
export const HOUR_MS = 3_600_000;
/** A planned run this late without a run is shown as overdue. */
export const OVERDUE_GRACE_MS = 10 * 60_000;
/** A scheduler tick older than this is stale (the scheduler ticks every minute). */
export const TICK_STALE_MS = 5 * 60_000;

/* ------------------------------------------------------------------ */
/* Job health                                                          */
/* ------------------------------------------------------------------ */

export interface RunRef {
  id: string;
  status: RunStatus;
  queuedAt: string;
  finishedAt?: string | null;
  warnings?: readonly { code: string }[];
}

export interface JobInput {
  id: string;
  name: string;
  enabled: boolean;
  nextRunAt?: string | null;
  scheduleDescription: string;
  encryption: { configured: boolean; mode?: JobEncryptionMode | null; optedOut: boolean };
  lastRun?: RunRef | null;
  /** The missed-backup check's ongoing outage (Job.missed, D-196). */
  missed: boolean;
  missedSince?: string | null;
}

export type HealthState =
  "failing" | "missed" | "overdue" | "warning" | "running" | "never" | "healthy" | "paused";

/** Most urgent first: the grid and the counts use this order. */
export const HEALTH_ORDER: readonly HealthState[] = [
  "failing",
  "missed",
  "overdue",
  "warning",
  "running",
  "never",
  "healthy",
  "paused",
];

export interface JobHealth {
  id: string;
  name: string;
  state: HealthState;
  last: RunRef | null;
  nextRunAt: string | null;
  schedule: string;
  overdue: boolean;
  missed: boolean;
  /** First missed occurrence of the ongoing outage. */
  missedSince: string | null;
  warnings: number;
  unencrypted: boolean;
}

export function isActiveRun(status: RunStatus): boolean {
  return status === "QUEUED" || status === "RUNNING";
}

function time(iso: string | null | undefined): number {
  if (!iso) return Number.NaN;
  return new Date(iso).getTime();
}

/** The newer of two runs by queue time (a live event may beat the loaded lastRun). */
export function newerRun(
  a: RunRef | null | undefined,
  b: RunRef | null | undefined,
): RunRef | null {
  if (!a) return b ?? null;
  if (!b) return a;
  if (a.id === b.id) return b;
  return time(b.queuedAt) >= time(a.queuedAt) ? b : a;
}

export interface HealthContext {
  now: number;
  /** Latest live run event per job id. */
  live?: ReadonlyMap<string, RunRef>;
  /** Jobs with a calendar PLANNED event flagged overdue. */
  overdueJobs?: ReadonlySet<string>;
}

function succeeded(run: RunRef): boolean {
  return run.status === "SUCCESS" || run.status === "PARTIAL";
}

export function jobHealth(job: JobInput, ctx: HealthContext): JobHealth {
  const last = newerRun(job.lastRun, ctx.live?.get(job.id));
  const next = job.nextRunAt ?? null;
  const overdue =
    job.enabled &&
    ((next !== null && time(next) < ctx.now - OVERDUE_GRACE_MS) ||
      (ctx.overdueJobs?.has(job.id) ?? false));
  // Missed: the server's missed-backup check reports an ongoing outage
  // (D-188/D-196); a successful run seen live since then ends it here too.
  const since = job.missedSince ?? null;
  const recovered =
    last !== null &&
    succeeded(last) &&
    (since === null || time(last.finishedAt ?? last.queuedAt) > time(since));
  const missed = job.enabled && job.missed && !recovered;
  const warnings = last?.warnings?.length ?? 0;
  let state: HealthState;
  if (!job.enabled) state = "paused";
  else if (last && isActiveRun(last.status)) state = "running";
  else if (last?.status === "FAILED") state = "failing";
  else if (missed) state = "missed";
  else if (overdue) state = "overdue";
  else if (
    last &&
    (last.status === "PARTIAL" ||
      last.status === "CANCELED" ||
      last.status === "SKIPPED" ||
      warnings > 0)
  )
    state = "warning";
  else if (last?.status === "SUCCESS") state = "healthy";
  else state = "never";
  return {
    id: job.id,
    name: job.name,
    state,
    last,
    nextRunAt: next,
    schedule: job.scheduleDescription,
    overdue,
    missed,
    missedSince: missed ? since : null,
    warnings,
    unencrypted: isUnencrypted(job.encryption),
  };
}

export function sortHealth(rows: JobHealth[]): JobHealth[] {
  return [...rows].sort(
    (a, b) =>
      HEALTH_ORDER.indexOf(a.state) - HEALTH_ORDER.indexOf(b.state) || a.name.localeCompare(b.name),
  );
}

export function countHealth(rows: readonly JobHealth[]): Record<HealthState, number> {
  const out = Object.fromEntries(HEALTH_ORDER.map((s) => [s, 0])) as Record<HealthState, number>;
  for (const r of rows) out[r.state]++;
  return out;
}

/* ------------------------------------------------------------------ */
/* Live runs                                                           */
/* ------------------------------------------------------------------ */

export interface LiveRun extends RunRef {
  trigger?: string;
  startedAt?: string | null;
  errorCode?: string | null;
  errorMessage?: string | null;
  job: { id: string; name: string };
}

/** Applies one runUpdated event to the active set: active runs upsert, finished ones leave. */
export function applyRunEvent<T extends LiveRun>(
  active: ReadonlyMap<string, T>,
  run: T,
): Map<string, T> {
  const next = new Map(active);
  if (isActiveRun(run.status)) next.set(run.id, run);
  else next.delete(run.id);
  return next;
}

/** Running first, then queued; oldest first within each. */
export function sortActive<T extends LiveRun>(runs: Iterable<T>): T[] {
  return [...runs].sort(
    (a, b) =>
      (a.status === "RUNNING" ? 0 : 1) - (b.status === "RUNNING" ? 0 : 1) ||
      time(a.startedAt ?? a.queuedAt) - time(b.startedAt ?? b.queuedAt),
  );
}

/* ------------------------------------------------------------------ */
/* Needs attention: failures and ongoing missed backups                */
/* ------------------------------------------------------------------ */

export interface CalendarEventInput {
  jobId: string;
  jobName: string;
  kind: CalendarEventKind;
  start: string;
  status?: RunStatus | null;
  runId?: string | null;
  paused: boolean;
  startedEarly: boolean;
  overdue: boolean;
  detail?: string | null;
}

export type AttentionItem =
  | { kind: "failed"; at: string; run: LiveRun }
  | { kind: "missed"; at: string | null; job: { id: string; name: string } };

/**
 * Failures of the last 7 days merged with the jobs in an ongoing
 * missed-backup outage (any age), newest first; at most `limit`.
 */
export function attentionItems(
  failures: readonly LiveRun[],
  health: readonly JobHealth[],
  now: number,
  limit = 8,
): AttentionItem[] {
  const since = now - 7 * DAY_MS;
  const seen = new Set<string>();
  const items: AttentionItem[] = [];
  for (const run of failures) {
    if (seen.has(run.id)) continue;
    seen.add(run.id);
    const at = run.finishedAt ?? run.queuedAt;
    if (time(at) >= since) items.push({ kind: "failed", at, run });
  }
  for (const h of health) {
    if (h.missed)
      items.push({ kind: "missed", at: h.missedSince, job: { id: h.id, name: h.name } });
  }
  const t = (i: AttentionItem) => (i.at ? time(i.at) : Number.POSITIVE_INFINITY);
  return items.sort((a, b) => t(b) - t(a)).slice(0, limit);
}

/* ------------------------------------------------------------------ */
/* Live restores                                                       */
/* ------------------------------------------------------------------ */

/** Applies restore events (newest state per id) to the loaded list: events win. */
export function mergeRestores<T extends { id: string }>(
  loaded: readonly T[],
  live: ReadonlyMap<string, T>,
): T[] {
  const out = new Map<string, T>();
  for (const r of loaded) out.set(r.id, r);
  for (const r of live.values()) out.set(r.id, r);
  return [...out.values()];
}

/* ------------------------------------------------------------------ */
/* Upcoming runs (next 24 h)                                           */
/* ------------------------------------------------------------------ */

export interface UpcomingRun {
  jobId: string;
  jobName: string;
  at: string;
  overdue: boolean;
  /** Position on the 24 h strip, 0..1. */
  offset: number;
}

export function upcomingRuns(
  events: readonly CalendarEventInput[],
  now: number,
  hours = 24,
): UpcomingRun[] {
  const end = now + hours * HOUR_MS;
  return events
    .filter((e) => e.kind === "PLANNED" && !e.paused && !e.startedEarly)
    .filter((e) => e.overdue || (time(e.start) >= now - OVERDUE_GRACE_MS && time(e.start) < end))
    .map((e) => ({
      jobId: e.jobId,
      jobName: e.jobName,
      at: e.start,
      overdue: e.overdue,
      offset: Math.min(1, Math.max(0, (time(e.start) - now) / (end - now))),
    }))
    .sort((a, b) => time(a.at) - time(b.at));
}

export function overdueJobIds(events: readonly CalendarEventInput[]): Set<string> {
  return new Set(events.filter((e) => e.kind === "PLANNED" && e.overdue).map((e) => e.jobId));
}

/* ------------------------------------------------------------------ */
/* Security findings (staff)                                           */
/* ------------------------------------------------------------------ */

export function isUnencrypted(enc: JobInput["encryption"]): boolean {
  return enc.optedOut || enc.mode === "NONE";
}

export interface ConnectionInput {
  id: string;
  name: string;
  tlsMode: ConnectionTlsMode;
  insecureTransport: boolean;
  pinStatus: TlsPinStatus;
  pinExpiresAt?: string | null;
  lastTest?: {
    at: string;
    ok: boolean;
    code?: string | null;
    stale: boolean;
    privileges?: { status: TargetPrivilegeStatus } | null;
  } | null;
}

export type FindingKind =
  | "unencrypted"
  | "insecure-transport"
  | "identity-mismatch"
  | "privileged-target"
  | "pin-expired"
  | "pin-expiring";

export interface FindingItem {
  id: string;
  name: string;
  to: string;
  detail: string;
}

export interface SecurityFinding {
  kind: FindingKind;
  title: string;
  /** critical: act now; warning: weakened or soon. */
  severity: "critical" | "warning";
  items: FindingItem[];
}

const TLS_DETAIL: Record<ConnectionTlsMode, string> = {
  PINNED: "TLS pinned",
  UNVERIFIED: "TLS without verification",
  DISABLED: "TLS disabled",
};

/** Weakened or failing security settings, most severe first; empty kinds are left out. */
export function securityFindings(
  jobs: readonly JobInput[],
  connections: readonly ConnectionInput[],
): SecurityFinding[] {
  const job = (j: JobInput, detail: string): FindingItem => ({
    id: j.id,
    name: j.name,
    to: `/jobs/${j.id}`,
    detail,
  });
  const conn = (c: ConnectionInput, detail: string): FindingItem => ({
    id: c.id,
    name: c.name,
    to: `/connections/${c.id}`,
    detail,
  });
  const byName = (a: FindingItem, b: FindingItem) => a.name.localeCompare(b.name);
  const findings: SecurityFinding[] = [
    {
      kind: "identity-mismatch",
      title: "Server identity mismatch",
      severity: "critical",
      items: connections
        .filter(
          (c) =>
            c.lastTest &&
            !c.lastTest.ok &&
            !c.lastTest.stale &&
            (c.lastTest.code === "TLS_PIN_MISMATCH" || c.lastTest.code === "SSH_HOST_KEY_MISMATCH"),
        )
        .map((c) =>
          conn(
            c,
            c.lastTest?.code === "TLS_PIN_MISMATCH"
              ? "Certificate does not match the pin"
              : "SSH host key does not match",
          ),
        ),
    },
    {
      kind: "pin-expired",
      title: "Pinned certificate expired",
      severity: "critical",
      items: connections.filter((c) => c.pinStatus === "EXPIRED").map((c) => conn(c, "Expired")),
    },
    {
      kind: "unencrypted",
      title: "Unencrypted jobs",
      severity: "warning",
      items: jobs
        .filter((j) => isUnencrypted(j.encryption))
        .map((j) => job(j, "Encryption opted out")),
    },
    {
      kind: "insecure-transport",
      title: "Insecure transport",
      severity: "warning",
      items: connections
        .filter((c) => c.insecureTransport || c.tlsMode !== "PINNED")
        .map((c) => conn(c, TLS_DETAIL[c.tlsMode])),
    },
    {
      kind: "privileged-target",
      title: "Privileged database roles",
      severity: "warning",
      items: connections
        .filter((c) => {
          const s = c.lastTest?.privileges?.status;
          return s === "PRIVILEGED" || s === "UNKNOWN";
        })
        .map((c) =>
          conn(
            c,
            (c.lastTest?.privileges?.status === "PRIVILEGED"
              ? "Privileged role"
              : "Privileges unknown (treated as privileged)") +
              (c.lastTest?.stale ? " · last test is stale" : ""),
          ),
        ),
    },
    {
      kind: "pin-expiring",
      title: "Pinned certificates expiring",
      severity: "warning",
      items: connections
        .filter(
          (c) =>
            c.pinStatus === "EXPIRES_WITHIN_7_DAYS" || c.pinStatus === "EXPIRES_WITHIN_30_DAYS",
        )
        .map((c) =>
          conn(c, c.pinStatus === "EXPIRES_WITHIN_7_DAYS" ? "Within 7 days" : "Within 30 days"),
        ),
    },
  ];
  return findings
    .filter((f) => f.items.length > 0)
    .map((f) => ({ ...f, items: [...f.items].sort(byName) }));
}

/* ------------------------------------------------------------------ */
/* Storage per destination (staff)                                     */
/* ------------------------------------------------------------------ */

export interface UsageInput {
  destinationId: string;
  name: string;
  type: DestinationType;
  storedBytes: number;
  artifactCount: number;
}

export interface StorageRow {
  id: string;
  name: string;
  type: DestinationType;
  bytes: number;
  objects: number;
  /** Bar length, 0..1 of the largest destination. */
  share: number;
}

/** Rows of the storage tile from destinationUsage (exact server aggregate, D-196). */
export function storageByDestination(usage: readonly UsageInput[]): StorageRow[] {
  const rows = usage.map((u) => ({
    id: u.destinationId,
    name: u.name,
    type: u.type,
    bytes: Number.isFinite(u.storedBytes) && u.storedBytes > 0 ? u.storedBytes : 0,
    objects: u.artifactCount,
    share: 0,
  }));
  const max = Math.max(0, ...rows.map((r) => r.bytes));
  return rows
    .map((r) => ({ ...r, share: max > 0 ? r.bytes / max : 0 }))
    .sort((a, b) => b.bytes - a.bytes || a.name.localeCompare(b.name));
}

/* ------------------------------------------------------------------ */
/* System health (admin)                                               */
/* ------------------------------------------------------------------ */

export type Level = "ok" | "warning" | "critical";

export function expiryLevel(notAfter: string | null | undefined, now: number): Level {
  const t = time(notAfter);
  if (Number.isNaN(t)) return "ok";
  if (t - now < 7 * DAY_MS) return "critical";
  if (t - now < 30 * DAY_MS) return "warning";
  return "ok";
}

export function daysLeft(notAfter: string, now: number): number {
  return Math.floor((time(notAfter) - now) / DAY_MS);
}

export type TickState = "off" | "none" | "stale" | "ok";

export function tickState(
  enabled: boolean,
  lastTick: string | null | undefined,
  now: number,
): TickState {
  if (!enabled) return "off";
  if (!lastTick) return "none";
  return now - time(lastTick) > TICK_STALE_MS ? "stale" : "ok";
}

export interface QueueRow {
  queue: string;
  waiting: number;
  running: number;
  retrying: number;
}

const QUEUE_ORDER = ["backup", "restore", "retention", "notify", "maintenance", "other"];

/** River jobs per queue: waiting (available + scheduled), running, retrying. Finished states are ignored. */
export function queueSummary(
  rows: readonly { queue: string; state: string; jobs: number }[],
): QueueRow[] {
  const out = new Map<string, QueueRow>();
  for (const r of rows) {
    const q = out.get(r.queue) ?? { queue: r.queue, waiting: 0, running: 0, retrying: 0 };
    if (r.state === "available" || r.state === "scheduled" || r.state === "pending")
      q.waiting += r.jobs;
    else if (r.state === "running") q.running += r.jobs;
    else if (r.state === "retryable") q.retrying += r.jobs;
    else continue;
    out.set(r.queue, q);
  }
  const rank = (q: string) => {
    const i = QUEUE_ORDER.indexOf(q);
    return i < 0 ? QUEUE_ORDER.length : i;
  };
  return [...out.values()].sort((a, b) => rank(a.queue) - rank(b.queue));
}

/* ------------------------------------------------------------------ */
/* Links                                                               */
/* ------------------------------------------------------------------ */

/** An in-app path from server data, or null (never an absolute or protocol-relative URL). */
export function internalLink(link: string | null | undefined): string | null {
  if (!link || !link.startsWith("/") || link.startsWith("//") || link.startsWith("/\\"))
    return null;
  if (/\s/.test(link)) return null;
  for (let i = 0; i < link.length; i++) if (link.charCodeAt(i) < 0x20) return null;
  return link;
}

export const ACTIVE_RESTORE: ReadonlySet<RestoreStatus> = new Set([
  "QUEUED",
  "STAGING",
  "VERIFYING",
  "RESTORING",
]);

export const HEALTH_META: Record<HealthState, { label: string; ui: UiStatus; fill: string }> = {
  failing: { label: "Failing", ui: "failed", fill: "fill-status-failed" },
  missed: { label: "Missed", ui: "missed", fill: "fill-status-missed" },
  overdue: { label: "Overdue", ui: "missed", fill: "fill-status-missed" },
  warning: { label: "Warnings", ui: "partial", fill: "fill-status-partial" },
  running: { label: "Running", ui: "running", fill: "fill-status-running" },
  never: { label: "Never run", ui: "queued", fill: "fill-status-neutral" },
  healthy: { label: "Healthy", ui: "success", fill: "fill-status-success" },
  paused: { label: "Paused", ui: "queued", fill: "fill-status-neutral" },
};
