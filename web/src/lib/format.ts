/**
 * The one place for user-visible formatting of API values: timestamps
 * (RFC 3339, locale-aware), sizes (always binary IEC units: KiB, MiB, ...)
 * and durations. Features never format these themselves.
 */

const dateTime = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });
const relative = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });

function parse(iso: string | null | undefined): Date | null {
  if (!iso) return null;
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? null : d;
}

/** "28 Oct 2026, 14:05" (empty for a missing or invalid value). */
export function formatDateTime(iso: string | null | undefined): string {
  const d = parse(iso);
  return d ? dateTime.format(d) : "";
}

const UNITS: readonly [Intl.RelativeTimeFormatUnit, number][] = [
  ["year", 365 * 24 * 3600],
  ["month", 30 * 24 * 3600],
  ["week", 7 * 24 * 3600],
  ["day", 24 * 3600],
  ["hour", 3600],
  ["minute", 60],
];

/** "3 hours ago", "in 2 days", "now" (empty for a missing or invalid value). */
export function formatRelative(iso: string | null | undefined, now: number = Date.now()): string {
  const d = parse(iso);
  if (!d) return "";
  const seconds = Math.round((d.getTime() - now) / 1000);
  for (const [unit, size] of UNITS) {
    if (Math.abs(seconds) >= size) return relative.format(Math.round(seconds / size), unit);
  }
  return relative.format(0, "second");
}

const BYTE_UNITS = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"] as const;

/** "1.40 GiB": binary IEC units (sizes arrive as Float bytes); empty for invalid input. */
export function formatBytes(bytes: number | null | undefined): string {
  if (bytes == null || !Number.isFinite(bytes) || bytes < 0) return "";
  let v = bytes;
  let i = 0;
  while (v >= 1024 && i < BYTE_UNITS.length - 1) {
    v /= 1024;
    i++;
  }
  const digits = i === 0 || v >= 100 ? 0 : v >= 10 ? 1 : 2;
  return `${v.toFixed(digits)} ${BYTE_UNITS[i] ?? "B"}`;
}

/** "2h 05m", "3m 07s", "42s", "0.4s"; empty for invalid input. */
export function formatDuration(seconds: number | null | undefined): string {
  if (seconds == null || !Number.isFinite(seconds) || seconds < 0) return "";
  if (seconds < 1) return `${seconds.toFixed(1)}s`;
  const s = Math.round(seconds);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  if (h > 0) return `${h}h ${String(m).padStart(2, "0")}m`;
  if (m > 0) return `${m}m ${String(sec).padStart(2, "0")}s`;
  return `${sec}s`;
}

/** "14:05:09" in local time, for log lines and evidence; empty for invalid input. */
export function formatClock(iso: string | null | undefined): string {
  const d = parse(iso);
  if (!d) return "";
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}
