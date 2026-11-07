import type { RunStatus, RunTrigger } from "@/graphql/gen/graphql";
import { defineMessages } from "@/lib/i18n";

export const STATUSES: readonly RunStatus[] = [
  "QUEUED",
  "RUNNING",
  "SUCCESS",
  "PARTIAL",
  "FAILED",
  "CANCELED",
  "SKIPPED",
];
export const TRIGGERS: Record<RunTrigger, string> = defineMessages<Record<RunTrigger, string>>({
  en: {
    SCHEDULE: "Scheduled",
    CATCH_UP: "Catch-up",
    MANUAL: "Manual (extra run)",
    EARLY: "Started early",
    API: "API",
  },
  nl: {
    SCHEDULE: "Gepland",
    CATCH_UP: "Inhaalrun",
    MANUAL: "Handmatig (extra run)",
    EARLY: "Eerder gestart",
    API: "API",
  },
});
