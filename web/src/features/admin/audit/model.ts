import type { AuditActorKind, AuditLogFilter } from "@/graphql/gen/graphql";

/** Entries per page (the API allows 1..200). */
export const AUDIT_PAGE_SIZE = 50;

export interface AuditFilterDraft {
  /** An exact action, or a prefix when it ends with "." or "*". */
  action: string;
  actorKind: AuditActorKind | "";
  actorUserId: string;
  targetType: string;
  targetId: string;
  /** datetime-local values (local time). */
  from: string;
  to: string;
}

export const emptyDraft: AuditFilterDraft = {
  action: "",
  actorKind: "",
  actorUserId: "",
  targetType: "",
  targetId: "",
  from: "",
  to: "",
};

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export type DraftErrors = Partial<Record<keyof AuditFilterDraft, string>>;

function localToIso(v: string): string | null {
  if (v === "") return null;
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? null : d.toISOString();
}

export function validateDraft(d: AuditFilterDraft): DraftErrors {
  const e: DraftErrors = {};
  if (d.actorUserId.trim() !== "" && !UUID_RE.test(d.actorUserId.trim()))
    e.actorUserId = "Enter a user ID (UUID).";
  if (d.from !== "" && localToIso(d.from) === null) e.from = "Enter a valid time.";
  if (d.to !== "" && localToIso(d.to) === null) e.to = "Enter a valid time.";
  const from = localToIso(d.from);
  const to = localToIso(d.to);
  if (from && to && from >= to) e.to = "Must be after the start.";
  if (d.action.length > 200) e.action = "Too long.";
  return e;
}

/** The GraphQL filter for a (valid) draft; unset fields do not filter. */
export function toAuditFilter(d: AuditFilterDraft): AuditLogFilter {
  const f: AuditLogFilter = {};
  const action = d.action.trim();
  if (action !== "") {
    if (action.endsWith("*")) f.actionPrefix = action.slice(0, -1);
    else if (action.endsWith(".")) f.actionPrefix = action;
    else f.action = action;
  }
  if (d.actorKind !== "") f.actorKind = d.actorKind;
  if (d.actorUserId.trim() !== "") f.actorUserId = d.actorUserId.trim().toLowerCase();
  if (d.targetType.trim() !== "") f.targetType = d.targetType.trim();
  if (d.targetId.trim() !== "") f.targetId = d.targetId.trim();
  const from = localToIso(d.from);
  const to = localToIso(d.to);
  if (from) f.from = from;
  if (to) f.to = to;
  return f;
}

/** Number of active filters (for the toolbar badge). */
export function activeFilters(d: AuditFilterDraft): number {
  return (Object.values(d) as string[]).filter((v) => v.trim() !== "").length;
}

/** The details JSON pretty-printed (the raw text when it is not JSON). */
export function prettyDetails(details: string): string {
  try {
    const v: unknown = JSON.parse(details);
    if (v && typeof v === "object" && Object.keys(v).length === 0) return "{}";
    return JSON.stringify(v, null, 2);
  } catch {
    return details;
  }
}

/** Common action families for the quick filters. */
export const ACTION_PREFIXES: readonly string[] = [
  "auth.",
  "user.",
  "security.",
  "job.",
  "restore.",
  "connection.",
  "destination.",
  "notification.",
  "download.",
  "retention.",
  "audit.",
  "cli.",
];

/** The first characters of a chain hash, for the ledger column. */
export function shortHash(hash: string): string {
  return hash.slice(0, 10);
}
