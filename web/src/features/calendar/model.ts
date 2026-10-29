/**
 * Calendar logic (task 6.11): civil dates in an IANA zone, view ranges
 * (bounded by the API's 93 days), day bucketing, filters and event
 * presentation. Times from the API are UTC; the zone only buckets and
 * labels (D-159).
 */
import type {
  CalendarEventKind,
  CalendarRunUpdatedSubscription as RunUpdatedResult,
  RunStatus,
} from "@/graphql/gen/graphql";

export type CalendarView = "month" | "week" | "day";

/** A date without a time or zone (month 1-12). */
export interface CivilDate {
  y: number;
  m: number;
  d: number;
}

/** The API's range limit (D-159): 93 × 24 h. */
export const MAX_RANGE_MS = 93 * 24 * 3600 * 1000;

const partsFormatters = new Map<string, Intl.DateTimeFormat>();

function partsFormatter(tz: string): Intl.DateTimeFormat {
  let f = partsFormatters.get(tz);
  if (!f) {
    f = new Intl.DateTimeFormat("en-US", {
      timeZone: tz,
      hourCycle: "h23",
      year: "numeric",
      month: "numeric",
      day: "numeric",
      hour: "numeric",
      minute: "numeric",
      second: "numeric",
    });
    partsFormatters.set(tz, f);
  }
  return f;
}

/** Wall-clock parts of an instant in tz. */
export function zonedParts(date: Date, tz: string) {
  const out = { y: 0, m: 0, d: 0, h: 0, min: 0, s: 0 };
  for (const p of partsFormatter(tz).formatToParts(date)) {
    const v = Number(p.value);
    if (p.type === "year") out.y = v;
    else if (p.type === "month") out.m = v;
    else if (p.type === "day") out.d = v;
    else if (p.type === "hour") out.h = v % 24;
    else if (p.type === "minute") out.min = v;
    else if (p.type === "second") out.s = v;
  }
  return out;
}

/** The zone's offset from UTC at an instant (ms; east positive). */
function offsetMs(t: number, tz: string): number {
  const p = zonedParts(new Date(t), tz);
  return Date.UTC(p.y, p.m - 1, p.d, p.h, p.min, p.s) - Math.floor(t / 1000) * 1000;
}

/**
 * The instant of a wall-clock time in tz. A time skipped by a DST change
 * resolves to the instant after the gap; an ambiguous one to the first.
 */
export function zonedToUtc(c: CivilDate, tz: string, hour = 0, minute = 0): Date {
  const guess = Date.UTC(c.y, c.m - 1, c.d, hour, minute);
  const o1 = offsetMs(guess, tz);
  let t = guess - o1;
  const o2 = offsetMs(t, tz);
  if (o2 !== o1) t = guess - o2;
  return new Date(t);
}

export function civilOf(date: Date, tz: string): CivilDate {
  const p = zonedParts(date, tz);
  return { y: p.y, m: p.m, d: p.d };
}

export function addDays(c: CivilDate, n: number): CivilDate {
  const t = new Date(Date.UTC(c.y, c.m - 1, c.d + n));
  return { y: t.getUTCFullYear(), m: t.getUTCMonth() + 1, d: t.getUTCDate() };
}

export function addMonths(c: CivilDate, n: number): CivilDate {
  const first = new Date(Date.UTC(c.y, c.m - 1 + n, 1));
  const y = first.getUTCFullYear();
  const m = first.getUTCMonth() + 1;
  const last = new Date(Date.UTC(y, m, 0)).getUTCDate();
  return { y, m, d: Math.min(c.d, last) };
}

/** 0 = Monday ... 6 = Sunday (ISO weeks). */
export function weekday(c: CivilDate): number {
  return (new Date(Date.UTC(c.y, c.m - 1, c.d)).getUTCDay() + 6) % 7;
}

export function startOfWeek(c: CivilDate): CivilDate {
  return addDays(c, -weekday(c));
}

export function sameDay(a: CivilDate, b: CivilDate): boolean {
  return a.y === b.y && a.m === b.m && a.d === b.d;
}

const pad = (n: number, w = 2) => String(n).padStart(w, "0");

/** "2026-10-28". */
export function dayKey(c: CivilDate): string {
  return `${pad(c.y, 4)}-${pad(c.m)}-${pad(c.d)}`;
}

export function parseDayKey(s: string | null | undefined): CivilDate | undefined {
  const m = s ? /^(\d{4})-(\d{2})-(\d{2})$/.exec(s) : null;
  if (!m) return undefined;
  const c = { y: Number(m[1]), m: Number(m[2]), d: Number(m[3]) };
  const check = addDays(c, 0);
  return sameDay(c, check) ? c : undefined;
}

/** The days a view shows for an anchor date (month: whole weeks, Monday first). */
export function viewDays(view: CalendarView, anchor: CivilDate): CivilDate[] {
  if (view === "day") return [anchor];
  if (view === "week") {
    const start = startOfWeek(anchor);
    return Array.from({ length: 7 }, (_, i) => addDays(start, i));
  }
  const first = { y: anchor.y, m: anchor.m, d: 1 };
  const start = startOfWeek(first);
  const lastOfMonth = addDays(addMonths(first, 1), -1);
  const end = addDays(startOfWeek(lastOfMonth), 7);
  const days: CivilDate[] = [];
  for (let c = start; !sameDay(c, end); c = addDays(c, 1)) days.push(c);
  return days;
}

/** The [from, to) instants of a view, never longer than the API allows. */
export function viewRange(
  view: CalendarView,
  anchor: CivilDate,
  tz: string,
): { from: Date; to: Date } {
  const days = viewDays(view, anchor);
  const first = days[0] ?? anchor;
  const last = days[days.length - 1] ?? anchor;
  const from = zonedToUtc(first, tz);
  let to = zonedToUtc(addDays(last, 1), tz);
  if (to.getTime() - from.getTime() > MAX_RANGE_MS) to = new Date(from.getTime() + MAX_RANGE_MS);
  return { from, to };
}

/** The anchor after moving a view one page (month, week or day) by dir. */
export function shiftAnchor(view: CalendarView, anchor: CivilDate, dir: number): CivilDate {
  if (view === "month") return addMonths(anchor, dir);
  return addDays(anchor, view === "week" ? 7 * dir : dir);
}

/** Moves the keyboard focus by a key (undefined: not a navigation key). */
export function moveFocus(view: CalendarView, c: CivilDate, key: string): CivilDate | undefined {
  switch (key) {
    case "ArrowLeft":
      return addDays(c, -1);
    case "ArrowRight":
      return addDays(c, 1);
    case "ArrowUp":
      return view === "day" ? undefined : addDays(c, -7);
    case "ArrowDown":
      return view === "day" ? undefined : addDays(c, 7);
    case "Home":
      return startOfWeek(c);
    case "End":
      return addDays(startOfWeek(c), 6);
    case "PageUp":
      return view === "month" ? addMonths(c, -1) : addDays(c, view === "week" ? -7 : -1);
    case "PageDown":
      return view === "month" ? addMonths(c, 1) : addDays(c, view === "week" ? 7 : 1);
    default:
      return undefined;
  }
}

/** Whether a day belongs to the view of anchor (focus moves beyond it shift the view). */
export function inView(view: CalendarView, anchor: CivilDate, c: CivilDate): boolean {
  if (view === "month") return c.y === anchor.y && c.m === anchor.m;
  return viewDays(view, anchor).some((d) => sameDay(d, c));
}

export interface CalEvent {
  jobId: string;
  jobName: string;
  kind: CalendarEventKind;
  start: string;
  end: string | null;
  status: RunStatus | null;
  runId: string | null;
  destinationId: string | null;
  projected: boolean;
  paused: boolean;
  startedEarly: boolean;
  overdue: boolean;
  detail: string | null;
}

/** Events bucketed by the day (in tz) they start on, in start order. */
export function bucketByDay<T extends { start: string }>(
  events: readonly T[],
  tz: string,
): Map<string, T[]> {
  const out = new Map<string, T[]>();
  for (const e of events) {
    const key = dayKey(civilOf(new Date(e.start), tz));
    const list = out.get(key);
    if (list) list.push(e);
    else out.set(key, [e]);
  }
  for (const list of out.values()) list.sort((a, b) => Date.parse(a.start) - Date.parse(b.start));
  return out;
}

export interface FilterJob {
  id: string;
  connection: { id: string };
  destinations: readonly { id: string }[];
}

export interface Filters {
  jobId?: string | undefined;
  connectionId?: string | undefined;
  destinationId?: string | undefined;
}

/**
 * The job ids to ask for: null when no filter is set (every visible job),
 * otherwise the jobs matching every set filter (possibly none).
 */
export function jobIdsFor(jobs: readonly FilterJob[], f: Filters): string[] | null {
  if (!f.jobId && !f.connectionId && !f.destinationId) return null;
  return jobs
    .filter((j) => !f.jobId || j.id === f.jobId)
    .filter((j) => !f.connectionId || j.connection.id === f.connectionId)
    .filter((j) => !f.destinationId || j.destinations.some((d) => d.id === f.destinationId))
    .map((j) => j.id);
}

/** With a destination filter, expiries of copies on other destinations are hidden. */
export function eventVisible(e: CalEvent, f: Filters): boolean {
  if (f.destinationId && e.kind === "EXPIRY" && e.destinationId !== f.destinationId) return false;
  return true;
}

export type Variant =
  | "planned"
  | "paused"
  | "early"
  | "overdue"
  | "skipped"
  | "run-success"
  | "run-partial"
  | "run-failed"
  | "run-running"
  | "run-queued"
  | "run-neutral"
  | "expiry-exact"
  | "expiry-projected";

export function variantOf(e: CalEvent): Variant {
  switch (e.kind) {
    case "PLANNED":
      if (e.overdue) return "overdue";
      if (e.startedEarly) return "early";
      if (e.paused) return "paused";
      return "planned";
    case "SKIPPED":
      return "skipped";
    case "EXPIRY":
      return e.projected ? "expiry-projected" : "expiry-exact";
    case "RUN":
      switch (e.status) {
        case "SUCCESS":
          return "run-success";
        case "PARTIAL":
          return "run-partial";
        case "FAILED":
          return "run-failed";
        case "RUNNING":
          return "run-running";
        case "QUEUED":
          return "run-queued";
        default:
          return "run-neutral";
      }
  }
}

const RUN_STATUS_LABELS: Record<RunStatus, string> = {
  QUEUED: "Run queued",
  RUNNING: "Run in progress",
  SUCCESS: "Run succeeded",
  PARTIAL: "Run partial",
  FAILED: "Run failed",
  CANCELED: "Run canceled",
  SKIPPED: "Run skipped",
};

/** The text that names an event's kind and state (also for screen readers). */
export function kindLabel(e: CalEvent): string {
  switch (variantOf(e)) {
    case "planned":
      return "Planned";
    case "paused":
      return "Paused (job disabled)";
    case "early":
      return "Started early";
    case "overdue":
      return "Overdue: catch-up run pending";
    case "skipped":
      return "Skipped";
    case "expiry-exact":
      return "Expires (exact)";
    case "expiry-projected":
      return "Expiry (projected)";
    default:
      return e.status ? RUN_STATUS_LABELS[e.status] : "Run";
  }
}

/** Where a click on the event leads: the run when there is one, else the job. */
export function eventHref(e: CalEvent): string {
  if (e.runId && (e.kind === "RUN" || e.kind === "EXPIRY" || e.startedEarly))
    return `/runs/${e.runId}`;
  return `/jobs/${e.jobId}`;
}

const timeFormatters = new Map<string, Intl.DateTimeFormat>();

/** "14:05" in tz. */
export function formatTime(iso: string, tz: string): string {
  let f = timeFormatters.get(tz);
  if (!f) {
    f = new Intl.DateTimeFormat(undefined, {
      timeZone: tz,
      hour: "2-digit",
      minute: "2-digit",
      hourCycle: "h23",
    });
    timeFormatters.set(tz, f);
  }
  return f.format(new Date(iso));
}

/** The wall-clock hour (0-23) of an instant in tz. */
export function hourOf(iso: string, tz: string): number {
  return zonedParts(new Date(iso), tz).h;
}

/** "October 2026", "26 Oct – 1 Nov 2026", "Wednesday, 28 October 2026". */
export function viewTitle(view: CalendarView, anchor: CivilDate): string {
  const utc = (c: CivilDate) => new Date(Date.UTC(c.y, c.m - 1, c.d, 12));
  if (view === "month")
    return new Intl.DateTimeFormat(undefined, {
      month: "long",
      year: "numeric",
      timeZone: "UTC",
    }).format(utc(anchor));
  if (view === "day")
    return new Intl.DateTimeFormat(undefined, { dateStyle: "full", timeZone: "UTC" }).format(
      utc(anchor),
    );
  const days = viewDays("week", anchor);
  const first = days[0] ?? anchor;
  const last = days[6] ?? anchor;
  return new Intl.DateTimeFormat(undefined, {
    day: "numeric",
    month: "short",
    year: "numeric",
    timeZone: "UTC",
  }).formatRange(utc(first), utc(last));
}

/** A full, readable date of a civil day (accessible names of day cells). */
export function dayLabel(c: CivilDate): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: "full", timeZone: "UTC" }).format(
    new Date(Date.UTC(c.y, c.m - 1, c.d, 12)),
  );
}

/** The browser's zone, or UTC. */
export function browserZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

/** IANA zones for the selector (UTC first); the given zone is always included. */
export function zoneOptions(current: string): string[] {
  let zones: string[];
  try {
    zones = Intl.supportedValuesOf("timeZone");
  } catch {
    zones = [];
  }
  const set = new Set(["UTC", ...zones]);
  set.add(current);
  return [...set];
}

/** Whether tz is a zone this browser knows (anything else falls back to the browser zone). */
export function validZone(tz: string | null | undefined): tz is string {
  if (!tz || tz.length > 64 || tz === "Local") return false;
  try {
    new Intl.DateTimeFormat("en-US", { timeZone: tz });
    return true;
  } catch {
    return false;
  }
}

export type LiveRun = RunUpdatedResult["runUpdated"];

/** Applies live run changes to the RUN events they belong to. */
export function applyLiveRuns(
  events: readonly CalEvent[],
  live: ReadonlyMap<string, LiveRun>,
): CalEvent[] {
  if (live.size === 0) return [...events];
  return events.map((e) => {
    const run = e.kind === "RUN" && e.runId ? live.get(e.runId) : undefined;
    if (!run) return e;
    return {
      ...e,
      status: run.status,
      start: run.startedAt ?? e.start,
      end: run.finishedAt ?? null,
      detail: run.errorCode ?? e.detail,
    };
  });
}
