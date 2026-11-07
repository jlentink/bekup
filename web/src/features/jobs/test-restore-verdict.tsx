import type { TestRestoreStatus } from "@/graphql/gen/graphql";
import { defineMessages } from "@/lib/i18n";
import { cn } from "@/lib/utils";

const LABELS: Record<TestRestoreStatus, string> = defineMessages({
  en: {
    PASSED: "passed",
    WARNINGS: "warnings",
    FAILED: "failed",
    ERROR: "error",
    QUEUED: "queued",
    RUNNING: "running",
    CANCELED: "canceled",
  },
  nl: {
    PASSED: "geslaagd",
    WARNINGS: "waarschuwingen",
    FAILED: "mislukt",
    ERROR: "fout",
    QUEUED: "in de wachtrij",
    RUNNING: "bezig",
    CANCELED: "geannuleerd",
  },
});

const VERDICT_STYLE: Record<TestRestoreStatus, string> = {
  PASSED: "border-status-success/40 bg-status-success-soft text-status-success-fg",
  WARNINGS: "border-status-partial/40 bg-status-partial-soft text-status-partial-fg",
  FAILED: "border-status-failed/40 bg-status-failed-soft text-status-failed-fg",
  ERROR: "border-status-failed/40 bg-status-failed-soft text-status-failed-fg",
  QUEUED: "border-status-neutral/35 bg-status-neutral-soft text-status-neutral-fg",
  RUNNING: "border-status-running/40 bg-status-running-soft text-status-running-fg",
  CANCELED: "border-status-neutral/35 bg-status-neutral-soft text-status-neutral-fg",
};

/** The verdict of a test restore (task 4.6) as a mono pill. */
export function TestRestoreVerdict({ status }: { status: TestRestoreStatus }) {
  return (
    <span
      className={cn(
        "inline-flex h-6 items-center rounded-sm border px-2 font-mono text-2xs font-semibold tracking-[0.08em] uppercase",
        VERDICT_STYLE[status],
      )}
    >
      {LABELS[status]}
    </span>
  );
}
