import type { ScheduleInput, ScheduleKind, ScheduleUnit } from "@/graphql/gen/graphql";

/** A stored schedule as the API returns it (Schedule, D-143). */
export interface Schedule {
  kind: ScheduleKind;
  every?: number | null;
  unit?: ScheduleUnit | null;
  anchor?: string | null;
  weekday?: string | null;
  dayOfMonth?: number | null;
  cron?: string | null;
  timezone?: string | null;
}

/** The schedule builder's editable state (D-143 schedules, split for inputs). */
export interface ScheduleDraft {
  kind: ScheduleKind;
  every: string;
  unit: ScheduleUnit;
  /** YYYY-MM-DD in the schedule's zone. */
  anchorDate: string;
  /** HH:MM in the schedule's zone. */
  anchorTime: string;
  /** "" = derived from the anchor (weeks only). */
  weekday: string;
  /** "" = derived from the anchor (months only). */
  dayOfMonth: string;
  cron: string;
  timezone: string;
}

export const SCHEDULE_UNITS: readonly ScheduleUnit[] = [
  "MINUTES",
  "HOURS",
  "DAYS",
  "WEEKS",
  "MONTHS",
];

export const WEEKDAYS = [
  "monday",
  "tuesday",
  "wednesday",
  "thursday",
  "friday",
  "saturday",
  "sunday",
] as const;

/** The browser's zone, or UTC. */
export function localTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

const FALLBACK_ZONES = [
  "UTC",
  "Europe/Amsterdam",
  "Europe/Berlin",
  "Europe/London",
  "America/New_York",
  "America/Chicago",
  "America/Los_Angeles",
  "Asia/Tokyo",
  "Australia/Sydney",
];

/** IANA zone names the browser knows (the server validates the choice). */
export function timezones(): string[] {
  const intl = Intl as unknown as { supportedValuesOf?: (key: string) => string[] };
  try {
    const zones = intl.supportedValuesOf?.("timeZone");
    if (zones && zones.length > 0) return zones.includes("UTC") ? zones : ["UTC", ...zones];
  } catch {
    /* fall back below */
  }
  return FALLBACK_ZONES;
}

function today(): string {
  const d = new Date();
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

/** A sensible new schedule: every day at 03:00 in the browser's zone. */
export function defaultDraft(): ScheduleDraft {
  return {
    kind: "INTERVAL",
    every: "1",
    unit: "DAYS",
    anchorDate: today(),
    anchorTime: "03:00",
    weekday: "",
    dayOfMonth: "",
    cron: "0 3 * * *",
    timezone: localTimezone(),
  };
}

const KINDS: ReadonlySet<string> = new Set(["MANUAL", "INTERVAL", "CRON"]);
const UNITS: ReadonlySet<string> = new Set(SCHEDULE_UNITS);

/**
 * The draft of a stored schedule. Defensive for every kind: fields the kind
 * does not use (null for MANUAL and CRON) and missing or unknown values
 * keep the defaults; a missing schedule yields the default draft.
 */
export function draftFromSchedule(s: Partial<Schedule> | null | undefined): ScheduleDraft {
  const d = defaultDraft();
  if (!s) return d;
  const anchor = typeof s.anchor === "string" ? s.anchor : "";
  const [date, time] = anchor.split("T");
  return {
    kind: s.kind && KINDS.has(s.kind) ? s.kind : d.kind,
    every: s.every != null ? String(s.every) : d.every,
    unit: s.unit && UNITS.has(s.unit) ? s.unit : d.unit,
    anchorDate: date !== undefined && DATE_PREFIX_RE.test(date) ? date : d.anchorDate,
    anchorTime: time ? time.slice(0, 5) : d.anchorTime,
    weekday: s.weekday ?? "",
    dayOfMonth: s.dayOfMonth != null ? String(s.dayOfMonth) : "",
    cron: s.cron ?? d.cron,
    timezone: s.timezone ?? d.timezone,
  };
}

const DATE_PREFIX_RE = /^\d{4}-\d{2}-\d{2}$/;

const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;
const TIME_RE = /^([01]\d|2[0-3]):[0-5]\d$/;

/**
 * Local checks before the server sees the schedule. Returns the problem as
 * a sentence, or null. The server (previewSchedule) stays authoritative.
 */
export function draftProblem(d: ScheduleDraft): string | null {
  if (d.kind === "MANUAL") return null;
  if (d.timezone.trim() === "") return "Choose a time zone.";
  if (d.kind === "CRON") {
    const fields = d.cron.trim().split(/\s+/);
    return fields.length === 5 ? null : "A cron expression has exactly 5 fields.";
  }
  const every = Number(d.every);
  if (!Number.isInteger(every) || every < 1) return "Enter a whole number of 1 or more.";
  if (!DATE_RE.test(d.anchorDate)) return "Enter the start date.";
  if (!TIME_RE.test(d.anchorTime)) return "Enter the start time as HH:MM.";
  if (d.unit === "MONTHS" && d.dayOfMonth !== "") {
    const dom = Number(d.dayOfMonth);
    if (!Number.isInteger(dom) || dom < 1 || dom > 31) return "Day of month is 1 to 31.";
  }
  return null;
}

/** The API input of a draft; only the fields of its kind are sent. */
export function draftToInput(d: ScheduleDraft): ScheduleInput {
  switch (d.kind) {
    case "MANUAL":
      return { kind: "MANUAL" };
    case "CRON":
      return { kind: "CRON", cron: d.cron.trim().replace(/\s+/g, " "), timezone: d.timezone };
    case "INTERVAL": {
      const input: ScheduleInput = {
        kind: "INTERVAL",
        every: Number(d.every),
        unit: d.unit,
        anchor: `${d.anchorDate}T${d.anchorTime}`,
        timezone: d.timezone,
      };
      if (d.unit === "WEEKS" && d.weekday !== "") input.weekday = d.weekday;
      if (d.unit === "MONTHS" && d.dayOfMonth !== "") input.dayOfMonth = Number(d.dayOfMonth);
      return input;
    }
  }
}

/** The ScheduleInput of a stored schedule (for previews of an existing job). */
export function scheduleToInput(s: Schedule): ScheduleInput {
  const input: ScheduleInput = { kind: s.kind };
  if (s.every != null) input.every = s.every;
  if (s.unit != null) input.unit = s.unit;
  if (s.anchor != null) input.anchor = s.anchor;
  if (s.weekday != null) input.weekday = s.weekday;
  if (s.dayOfMonth != null) input.dayOfMonth = s.dayOfMonth;
  if (s.cron != null) input.cron = s.cron;
  if (s.timezone != null) input.timezone = s.timezone;
  return input;
}

/** "Wed 28 Oct 2026, 03:00" in zone (falls back to the browser's zone for an unknown name). */
export function formatInZone(iso: string, zone: string | null | undefined): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const opts: Intl.DateTimeFormatOptions = {
    weekday: "short",
    day: "numeric",
    month: "short",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  };
  try {
    return new Intl.DateTimeFormat(undefined, { ...opts, timeZone: zone ?? undefined }).format(d);
  } catch {
    return new Intl.DateTimeFormat(undefined, opts).format(d);
  }
}
