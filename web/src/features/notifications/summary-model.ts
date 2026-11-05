import type { SummaryFrequency, SummarySettingsFieldsFragment } from "@/graphql/gen/graphql";
import { localTimezone } from "@/features/jobs/schedule";

/** The periodic summary settings being edited (D-226). */
export interface SummaryDraft {
  frequency: SummaryFrequency;
  hour: number;
  /** ISO weekday, 1 = Monday. */
  weekday: number;
  /** 1..28 */
  monthDay: number;
  timezone: string;
}

export type SummaryView = SummarySettingsFieldsFragment;

export const FREQUENCIES: readonly SummaryFrequency[] = [
  "DAILY",
  "EVERY_OTHER_DAY",
  "WEEKLY",
  "MONTHLY",
  "NEVER",
];

export const HOURS: readonly number[] = Array.from({ length: 24 }, (_, i) => i);
export const MONTH_DAYS: readonly number[] = Array.from({ length: 28 }, (_, i) => i + 1);

/** "08:00" */
export function hourLabel(h: number): string {
  return `${String(h).padStart(2, "0")}:00`;
}

/**
 * The draft for the stored settings. Without own settings (the server's
 * defaults apply) the zone is prefilled from the browser, so the first
 * save stores the user's own zone.
 */
export function draftFrom(view: SummaryView, browserZone: string = localTimezone()): SummaryDraft {
  const s = view.settings;
  return {
    frequency: s.frequency,
    hour: s.hour,
    weekday: s.weekday,
    monthDay: s.monthDay,
    timezone: view.custom ? s.timezone : browserZone,
  };
}

/** Whether the draft differs from what is stored (always true while on the defaults). */
export function isDirty(view: SummaryView, draft: SummaryDraft): boolean {
  if (!view.custom) return true;
  const s = view.settings;
  return (
    s.frequency !== draft.frequency ||
    s.hour !== draft.hour ||
    s.weekday !== draft.weekday ||
    s.monthDay !== draft.monthDay ||
    s.timezone !== draft.timezone.trim()
  );
}

export function toInput(draft: SummaryDraft): SummaryDraft {
  return { ...draft, timezone: draft.timezone.trim() };
}
