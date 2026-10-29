import type { TestRestoreStatus } from "@/graphql/gen/graphql";
import { cn } from "@/lib/utils";

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
      {status.toLowerCase()}
    </span>
  );
}
