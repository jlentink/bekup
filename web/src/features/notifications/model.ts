import { getFragmentData, type FragmentType } from "@/graphql/gen";
import type {
  NotificationChannelType,
  NotificationEventPreferenceFieldsFragment,
  NotificationGroup,
} from "@/graphql/gen/graphql";

import { NotificationEventPreferenceFields, NotificationPreferencesFields } from "./operations";

/* ---- Inbox links ---- */

const UUID = "[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}";
const SUBJECT_PATHS: readonly RegExp[] = [
  new RegExp(`^/(runs|jobs|restores|connections|destinations)(/${UUID})?$`),
  /^\/profile(\/[a-z-]+)?$/,
  /^\/admin\/[a-z-]+$/,
];

export type SubjectKind =
  "run" | "job" | "restore" | "connection" | "destination" | "account" | "admin";

/**
 * The in-app path of a notification's link (the server sends an absolute
 * URL under server.base_url). Only the path is used, and only when it
 * names a known page of the app: a link can never navigate elsewhere, even
 * when base_url differs from the origin the UI is served on.
 */
export function subjectPath(link: string | null | undefined): string | null {
  if (!link || link.length > 2048) return null;
  let url: URL;
  try {
    url = new URL(link, "https://bekup.invalid");
  } catch {
    return null;
  }
  if (url.protocol !== "https:" && url.protocol !== "http:") return null;
  const path = url.pathname.toLowerCase();
  return SUBJECT_PATHS.some((re) => re.test(path)) ? path : null;
}

export function subjectKind(path: string): SubjectKind {
  const first = path.split("/")[1] ?? "";
  switch (first) {
    case "runs":
      return "run";
    case "jobs":
      return "job";
    case "restores":
      return "restore";
    case "connections":
      return "connection";
    case "destinations":
      return "destination";
    case "profile":
      return "account";
    default:
      return "admin";
  }
}

/* ---- Preference matrix ---- */

export type PreferenceRow = NotificationEventPreferenceFieldsFragment;
export type PreferenceCell = PreferenceRow["channels"][number];

/** The preference matrix with its fragments unmasked. */
export interface Prefs {
  emailVerified: boolean;
  activeWebhooks: number;
  groups: { group: NotificationGroup; label: string; events: PreferenceRow[] }[];
}

export function unmaskPrefs(ref: FragmentType<typeof NotificationPreferencesFields>): Prefs {
  const p = getFragmentData(NotificationPreferencesFields, ref);
  return {
    emailVerified: p.emailVerified,
    activeWebhooks: p.activeWebhooks,
    groups: p.groups.map((g) => ({
      group: g.group,
      label: g.label,
      events: g.events.map((e) => unmaskRow(e)),
    })),
  };
}

export function unmaskRow(
  ref: FragmentType<typeof NotificationEventPreferenceFields>,
): PreferenceRow {
  return getFragmentData(NotificationEventPreferenceFields, ref);
}

/** The matrix columns. */
export const CHANNEL_COLUMNS: readonly NotificationChannelType[] = ["INBOX", "EMAIL", "WEBHOOK"];

export const GROUP_ORDER: readonly NotificationGroup[] = [
  "BACKUP",
  "RESTORE",
  "RETENTION",
  "CONFIG",
  "SECURITY",
];

export function cellOf(row: PreferenceRow, channel: NotificationChannelType): PreferenceCell {
  return (
    row.channels.find((c) => c.channel === channel) ?? {
      channel,
      enabled: false,
      source: "DEFAULT",
      locked: false,
    }
  );
}

export function overrideOf(
  row: PreferenceRow,
  jobId: string,
  channel: NotificationChannelType,
): boolean | null {
  const o = row.jobOverrides.find((x) => x.jobId === jobId && x.channel === channel);
  return o ? o.enabled : null;
}

/** The value that applies to events of jobId: its override, else the event's cell. */
export function effectiveFor(
  row: PreferenceRow,
  channel: NotificationChannelType,
  jobId?: string | null,
): boolean {
  const cell = cellOf(row, channel);
  if (!jobId || cell.locked) return cell.enabled;
  return overrideOf(row, jobId, channel) ?? cell.enabled;
}

/** Every job that has at least one stored override, by name. */
export function jobsWithOverrides(
  rows: readonly PreferenceRow[],
): { jobId: string; jobName: string }[] {
  const seen = new Map<string, string>();
  for (const row of rows) for (const o of row.jobOverrides) seen.set(o.jobId, o.jobName);
  return [...seen.entries()]
    .map(([jobId, jobName]) => ({ jobId, jobName }))
    .sort((a, b) => a.jobName.localeCompare(b.jobName));
}

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/**
 * The job to open in the per-job override editor from a deep link
 * (`?job=<uuid>`, e.g. from the job editor). The name comes from the
 * existing overrides, else from the navigation state, else a neutral
 * fallback; a malformed id opens nothing.
 */
export function linkedJob(
  search: string,
  state: unknown,
  existing: readonly { jobId: string; jobName: string }[],
  fallbackName: string,
): { jobId: string; jobName: string } | null {
  const id = new URLSearchParams(search).get("job");
  if (!id || !UUID_RE.test(id)) return null;
  const known = existing.find((j) => j.jobId.toLowerCase() === id.toLowerCase());
  if (known) return known;
  const raw = (state as { jobName?: unknown } | null)?.jobName;
  const name = typeof raw === "string" && raw.trim() !== "" ? raw.trim().slice(0, 200) : "";
  return { jobId: id, jobName: name || fallbackName };
}

/** Replaces one event row (the answer of setNotificationPreference). */
export function replaceRow(prefs: Prefs, row: PreferenceRow): Prefs {
  return {
    ...prefs,
    groups: prefs.groups.map((g) => ({
      ...g,
      events: g.events.map((e) => (e.event.type === row.event.type ? row : e)),
    })),
  };
}

/* ---- E-mail verification link ---- */

/** The code of a /verify-email#code=… link (base64url), or null. */
export function codeFromFragment(hash: string): string | null {
  const raw = hash.startsWith("#") ? hash.slice(1) : hash;
  if (raw === "") return null;
  const code = new URLSearchParams(raw).get("code");
  return code && /^[A-Za-z0-9_-]{16,128}$/.test(code) ? code : null;
}

/* ---- Webhook URL (early feedback; the server decides) ---- */

export type WebhookUrlProblem = "empty" | "invalid" | "https" | "userinfo" | "fragment" | "length";

/**
 * Mirrors the D-177 URL rules the browser can check: https (http only when
 * the server runs in dev mode), no user name or password, no fragment,
 * printable ASCII, at most 2048 characters. Addresses are checked by the
 * server's SSRF guard, never here.
 */
export function webhookUrlProblem(raw: string, allowHttp = false): WebhookUrlProblem | null {
  const value = raw.trim();
  if (value === "") return "empty";
  if (value.length > 2048) return "length";
  if (!/^[\x21-\x7e]+$/.test(value)) return "invalid";
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return "invalid";
  }
  if (url.protocol !== "https:" && !(allowHttp && url.protocol === "http:")) return "https";
  if (url.hostname === "") return "invalid";
  if (url.username !== "" || url.password !== "") return "userinfo";
  if (value.includes("#")) return "fragment";
  return null;
}

/* ---- Personal webhook tests ---- */

/**
 * The three outcomes a personal webhook test can have (D-198): the server
 * buckets every failure into `not_allowed` (the SSRF guard refused the
 * address for personal webhooks) or `failed`, and never tells the receiver's
 * status. Anything unexpected counts as `failed`.
 */
export type PersonalTestOutcome = "ok" | "not_allowed" | "failed";

export function personalTestOutcome(result: {
  ok: boolean;
  code?: string | null;
}): PersonalTestOutcome {
  if (result.ok) return "ok";
  return result.code === "not_allowed" ? "not_allowed" : "failed";
}
