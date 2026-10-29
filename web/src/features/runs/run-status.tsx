import { StatusBadge, StatusDot } from "@/components/status/status-badge";
import type { RunStatus } from "@/graphql/gen/graphql";

import { RUN_STATUS_MAP as MAP, runStatusLabel } from "./run-status-model";

/** The run's status as a badge; canceled and skipped reuse the neutral style with their own label. */
export function RunStatusBadge({ status, className }: { status: RunStatus; className?: string }) {
  const m = MAP[status];
  return (
    <StatusBadge
      status={m.ui}
      {...(m.label ? { label: m.label } : {})}
      {...(className ? { className } : {})}
    />
  );
}

export function RunStatusDot({ status }: { status: RunStatus }) {
  return <StatusDot status={MAP[status].ui} aria-label={runStatusLabel(status)} />;
}
