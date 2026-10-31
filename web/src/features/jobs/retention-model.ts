/**
 * Retention visibility (D-209): the editor's estimate input and the
 * sentences for retention holds (job page and dashboard). Pure helpers;
 * the server's retention sweep decides what is kept.
 */
import type { JobRetentionInput, RetentionHoldReason } from "@/graphql/gen/graphql";
import { formatDateTime } from "@/lib/format";

/** "1 backup", "3 backups". */
export function backups(n: number): string {
  return n === 1 ? "1 backup" : `${String(n)} backups`;
}

function limit(v: string): number | null | undefined {
  const t = v.trim();
  if (t === "") return null;
  if (!/^\d+$/.test(t)) return undefined;
  const n = Number(t);
  return n >= 1 && Number.isSafeInteger(n) ? n : undefined;
}

/**
 * The retention input of the editor's two fields, or null while they are
 * invalid (not whole numbers of 1 or more, or both empty).
 */
export function retentionInput(keepLast: string, keepDays: string): JobRetentionInput | null {
  const last = limit(keepLast);
  const days = limit(keepDays);
  if (last === undefined || days === undefined) return null;
  if (last === null && days === null) return null;
  return {
    ...(last !== null ? { keepLast: last } : {}),
    ...(days !== null ? { keepDays: days } : {}),
  };
}

export const ESTIMATE_T = {
  eyebrow: "Backups kept per destination",
  kept: (n: number, atLeast: boolean) =>
    `With these settings ${atLeast ? "at least" : "about"} ${backups(n)} per destination ${
      n === 1 ? "is" : "are"
    } kept.`,
  unknown:
    "Unknown with a manual schedule and a day limit: how many backups fall within the days depends on how often someone starts the job.",
  unknownFloor: (n: number) => `The newest-backups rule alone keeps ${backups(n)}.`,
  figurePrefix: (atLeast: boolean) => (atLeast ? "≥" : "≈"),
  figureLabel: "per destination",
  ruleLast: (n: number | null) => (n === null ? "Keep the newest" : `Keep the newest ${String(n)}`),
  ruleDays: (n: number | null) =>
    n === null ? "Day limit" : `Younger than ${String(n)} ${n === 1 ? "day" : "days"}`,
  ruleTotal: "Kept: either rule",
  ruleUnknown: "unknown",
  ruleOff: "off",
  rulesLabel: "Backups each rule keeps",
  assumption:
    "Assumes every planned run succeeds. Each destination keeps its own copies; the rules add up, so the total is the larger count.",
  invalid: "Enter valid limits to see how many backups are kept.",
  scheduleInvalid: "Fix the schedule to see how many backups are kept.",
  checking: "Estimating",
} as const;

/** SchedulePreview.retention (RetentionEstimate, D-209). */
export interface EstimateInput {
  /** null: unknown (a manual schedule with a day limit). */
  kept?: number | null;
  byKeepLast: number;
  byKeepDays?: number | null;
  atLeast: boolean;
}

export interface EstimateRow {
  key: "last" | "days" | "total";
  label: string;
  /** null: unknown. */
  count: number | null;
  /** The rule is not set. */
  off: boolean;
  /** Bar length 0..1 of the largest known count. */
  share: number;
}

/** The three rows of the estimate panel: each rule on its own and the union. */
export function estimateRows(est: EstimateInput, input: JobRetentionInput): EstimateRow[] {
  const last = input.keepLast ?? null;
  const days = input.keepDays ?? null;
  const rows: Omit<EstimateRow, "share">[] = [
    {
      key: "last",
      label: ESTIMATE_T.ruleLast(last),
      count: last === null ? 0 : est.byKeepLast,
      off: last === null,
    },
    {
      key: "days",
      label: ESTIMATE_T.ruleDays(days),
      count: days === null ? 0 : (est.byKeepDays ?? null),
      off: days === null,
    },
    { key: "total", label: ESTIMATE_T.ruleTotal, count: est.kept ?? null, off: false },
  ];
  const max = Math.max(0, ...rows.map((r) => r.count ?? 0));
  return rows.map((r) => ({ ...r, share: max > 0 && r.count !== null ? r.count / max : 0 }));
}

export interface HoldInput {
  destinationName: string;
  destinationDeleted: boolean;
  reason: RetentionHoldReason;
  heldRuns: number;
  since: string;
  newestCompleteAt?: string | null;
}

export const HOLD_T = {
  title: "Retention is keeping incomplete backups",
  deleted: "(deleted)",
  explain:
    "Retention never deletes the newest complete backup, and it keeps incomplete backups newer than that because they may hold the only copy of a database. They stay, and use space, until a complete backup reaches the destination again. Check the failed and partial runs.",
  runsLink: "Show the run history",
  listLabel: "Destinations where retention keeps incomplete backups",
} as const;

/** The reason part of a hold sentence. */
export function holdReason(h: HoldInput): string {
  if (h.reason === "NEWER_THAN_NEWEST_COMPLETE" && h.newestCompleteAt) {
    return `no complete backup there since ${formatDateTime(h.newestCompleteAt)}`;
  }
  return "no complete backup there";
}

/**
 * "Retention is holding 3 incomplete backups on vault-s3 since …: no
 * complete backup there (since …)."
 */
export function holdSentence(h: HoldInput): string {
  const dest = h.destinationDeleted ? `${h.destinationName} ${HOLD_T.deleted}` : h.destinationName;
  return `Retention is holding ${String(h.heldRuns)} incomplete ${
    h.heldRuns === 1 ? "backup" : "backups"
  } on ${dest} since ${formatDateTime(h.since)}: ${holdReason(h)}.`;
}
