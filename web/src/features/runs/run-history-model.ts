import type { RunStatus, RunTrigger } from "@/graphql/gen/graphql";

export const STATUSES: readonly RunStatus[] = [
  "QUEUED",
  "RUNNING",
  "SUCCESS",
  "PARTIAL",
  "FAILED",
  "CANCELED",
  "SKIPPED",
];
export const TRIGGERS: Record<RunTrigger, string> = {
  SCHEDULE: "Scheduled",
  CATCH_UP: "Catch-up",
  MANUAL: "Manual (extra run)",
  EARLY: "Started early",
};
