import type { ComponentProps } from "react";

import { cn } from "@/lib/utils";

import { STATUS_META, type RunStatus } from "./status";

interface StatusBadgeProps extends Omit<ComponentProps<"span">, "children"> {
  status: RunStatus;
  /** Override the default label, e.g. "Failed · 2 of 5". */
  label?: string;
}

/** Shape + color + text status label for runs and jobs. */
export function StatusBadge({ status, label, className, ...props }: StatusBadgeProps) {
  const meta = STATUS_META[status];
  const Icon = meta.icon;
  return (
    <span
      data-slot="status-badge"
      data-status={status}
      className={cn(
        "inline-flex h-6 items-center gap-1.5 rounded-sm border px-2 text-xs font-medium whitespace-nowrap",
        meta.badge,
        className,
      )}
      {...props}
    >
      <Icon
        aria-hidden="true"
        className={cn("size-3.5 shrink-0", status === "running" && "animate-spin")}
      />
      {label ?? meta.label}
    </span>
  );
}

interface StatusDotProps extends Omit<ComponentProps<"span">, "children"> {
  status: RunStatus;
}

/** Compact marker for dense tables; always carries an accessible name. */
export function StatusDot({ status, className, ...props }: StatusDotProps) {
  const meta = STATUS_META[status];
  return (
    <span
      role="img"
      aria-label={meta.label}
      title={meta.label}
      data-slot="status-dot"
      data-status={status}
      className={cn("inline-block size-2 shrink-0 rounded-full", meta.dot, className)}
      {...props}
    />
  );
}
