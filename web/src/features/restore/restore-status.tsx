import { Ban } from "lucide-react";

import { StatusBadge } from "@/components/status/status-badge";
import type { RunStatus } from "@/components/status/status";
import type { RestoreStatus } from "@/graphql/gen/graphql";
import { cn } from "@/lib/utils";

import { STATUS_LABELS } from "./model";

const TO_RUN_STATUS: Partial<Record<RestoreStatus, RunStatus>> = {
  QUEUED: "queued",
  STAGING: "running",
  VERIFYING: "running",
  RESTORING: "running",
  SUCCEEDED: "success",
  FAILED: "failed",
};

/** Shape + color + text status of a restore (canceled has its own neutral glyph). */
export function RestoreStatusBadge({
  status,
  className,
}: {
  status: RestoreStatus;
  className?: string;
}) {
  const mapped = TO_RUN_STATUS[status];
  if (mapped) {
    return <StatusBadge status={mapped} label={STATUS_LABELS[status]} className={className} />;
  }
  return (
    <span
      data-slot="status-badge"
      data-status="canceled"
      className={cn(
        "inline-flex h-6 items-center gap-1.5 rounded-sm border border-status-neutral/35 bg-status-neutral-soft px-2 text-xs font-medium whitespace-nowrap text-status-neutral-fg",
        className,
      )}
    >
      <Ban aria-hidden="true" className="size-3.5 shrink-0" />
      {STATUS_LABELS[status]}
    </span>
  );
}
