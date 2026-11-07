/**
 * Retention visibility (D-209): the editor's estimate input and the
 * sentences for retention holds (job page and dashboard). Pure helpers;
 * the server's retention sweep decides what is kept.
 */
import type { JobRetentionInput, RetentionHoldReason } from "@/graphql/gen/graphql";
import { formatDateTime } from "@/lib/format";
import { defineMessages, plural } from "@/lib/i18n";

const W = defineMessages({
  en: {
    backups: (n: number) => plural(n, { one: "1 backup", other: `${String(n)} backups` }),
  },
  nl: {
    backups: (n: number) => plural(n, { one: "1 back-up", other: `${String(n)} back-ups` }),
  },
});

/** "1 backup", "3 backups". */
export function backups(n: number): string {
  return W.backups(n);
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

export const ESTIMATE_T = defineMessages({
  en: {
    eyebrow: "Backups kept per destination",
    kept: (n: number, atLeast: boolean) =>
      `With these settings ${atLeast ? "at least" : "about"} ${backups(n)} per destination ${plural(n, { one: "is", other: "are" })} kept.`,
    unknown:
      "Unknown with a manual schedule and a day limit: how many backups fall within the days depends on how often someone starts the job.",
    unknownFloor: (n: number) => `The newest-backups rule alone keeps ${backups(n)}.`,
    figurePrefix: (atLeast: boolean) => (atLeast ? "≥" : "≈"),
    figureLabel: "per destination",
    ruleLast: (n: number | null) =>
      n === null ? "Keep the newest" : `Keep the newest ${String(n)}`,
    ruleDays: (n: number | null) =>
      n === null
        ? "Day limit"
        : `Younger than ${String(n)} ${plural(n, { one: "day", other: "days" })}`,
    ruleTotal: "Kept: either rule",
    ruleUnknown: "unknown",
    ruleOff: "off",
    rulesLabel: "Backups each rule keeps",
    assumption:
      "Assumes every planned run succeeds. Each destination keeps its own copies; the rules add up, so the total is the larger count.",
    invalid: "Enter valid limits to see how many backups are kept.",
    scheduleInvalid: "Fix the schedule to see how many backups are kept.",
    checking: "Estimating",
  },
  nl: {
    eyebrow: "Bewaarde back-ups per bestemming",
    kept: (n: number, atLeast: boolean) =>
      `Met deze instellingen ${plural(n, { one: "blijft", other: "blijven" })} er ${
        atLeast ? "minstens" : "ongeveer"
      } ${backups(n)} per bestemming bewaard.`,
    unknown:
      "Onbekend bij een handmatige planning met een dagenlimiet: hoeveel back-ups binnen die dagen vallen, hangt af van hoe vaak iemand de taak start.",
    unknownFloor: (n: number) =>
      `Alleen de regel voor de nieuwste back-ups bewaart al ${backups(n)}.`,
    figurePrefix: (atLeast: boolean) => (atLeast ? "≥" : "≈"),
    figureLabel: "per bestemming",
    ruleLast: (n: number | null) =>
      n === null ? "Nieuwste bewaren" : `Laatste ${String(n)} bewaren`,
    ruleDays: (n: number | null) =>
      n === null
        ? "Dagenlimiet"
        : `Jonger dan ${plural(n, { one: "1 dag", other: `${String(n)} dagen` })}`,
    ruleTotal: "Bewaard: een van beide regels",
    ruleUnknown: "onbekend",
    ruleOff: "uit",
    rulesLabel: "Back-ups die elke regel bewaart",
    assumption:
      "Gaat ervan uit dat elke geplande run slaagt. Elke bestemming bewaart haar eigen kopieën; de regels vullen elkaar aan, dus het totaal is het grootste aantal.",
    invalid: "Vul geldige limieten in om te zien hoeveel back-ups bewaard blijven.",
    scheduleInvalid: "Corrigeer de planning om te zien hoeveel back-ups bewaard blijven.",
    checking: "Bezig met schatten",
  },
});

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

export const HOLD_T = defineMessages({
  en: {
    title: "Retention is keeping incomplete backups",
    deleted: "(deleted)",
    explain:
      "Retention never deletes the newest complete backup, and it keeps incomplete backups newer than that because they may hold the only copy of a database. They stay, and use space, until a complete backup reaches the destination again. Check the failed and partial runs.",
    runsLink: "Show the run history",
    listLabel: "Destinations where retention keeps incomplete backups",
    reasonSince: (at: string) => `no complete backup there since ${at}`,
    reason: "no complete backup there",
    sentence: (n: number, dest: string, since: string, reason: string) =>
      `Retention is holding ${String(n)} incomplete ${plural(n, { one: "backup", other: "backups" })} on ${dest} since ${since}: ${reason}.`,
  },
  nl: {
    title: "Het bewaarbeleid houdt onvolledige back-ups vast",
    deleted: "(verwijderd)",
    explain:
      "Het bewaarbeleid verwijdert nooit de nieuwste volledige back-up, en bewaart onvolledige back-ups die nieuwer zijn, omdat die de enige kopie van een database kunnen bevatten. Ze blijven staan, en nemen ruimte in, tot er weer een volledige back-up op de bestemming staat. Controleer de mislukte en gedeeltelijke runs.",
    runsLink: "Rungeschiedenis tonen",
    listLabel: "Bestemmingen waar het bewaarbeleid onvolledige back-ups vasthoudt",
    reasonSince: (at: string) => `daar geen volledige back-up sinds ${at}`,
    reason: "daar geen volledige back-up",
    sentence: (n: number, dest: string, since: string, reason: string) =>
      `Het bewaarbeleid houdt sinds ${since} ${plural(n, {
        one: "1 onvolledige back-up",
        other: `${String(n)} onvolledige back-ups`,
      })} vast op ${dest}: ${reason}.`,
  },
});

/** The reason part of a hold sentence. */
export function holdReason(h: HoldInput): string {
  if (h.reason === "NEWER_THAN_NEWEST_COMPLETE" && h.newestCompleteAt) {
    return HOLD_T.reasonSince(formatDateTime(h.newestCompleteAt));
  }
  return HOLD_T.reason;
}

/**
 * "Retention is holding 3 incomplete backups on vault-s3 since …: no
 * complete backup there (since …)."
 */
export function holdSentence(h: HoldInput): string {
  const dest = h.destinationDeleted ? `${h.destinationName} ${HOLD_T.deleted}` : h.destinationName;
  return HOLD_T.sentence(h.heldRuns, dest, formatDateTime(h.since), holdReason(h));
}
