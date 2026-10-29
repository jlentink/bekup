import type { LucideIcon } from "lucide-react";
import { useId, type ReactNode } from "react";

import { cn } from "@/lib/utils";

/**
 * One entry of the account ledger: the heading column (icon, title, lead,
 * status) on the left and the controls on the right; stacked on narrow
 * screens. Sections are separated by hairline rules.
 */
export function Section({
  icon: Icon,
  title,
  lead,
  status,
  children,
  className,
}: {
  icon: LucideIcon;
  title: string;
  lead?: ReactNode;
  status?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  const headingId = useId();
  return (
    <section
      aria-labelledby={headingId}
      className={cn("@container border-b border-rule py-8 first:pt-2 last:border-b-0", className)}
    >
      <div className="grid gap-x-10 gap-y-4 @3xl:grid-cols-[minmax(0,18rem)_minmax(0,1fr)]">
        <div className="flex flex-col gap-2">
          <div className="flex items-center gap-2.5">
            <span
              aria-hidden="true"
              className="inline-flex size-7 items-center justify-center rounded-sm border bg-surface text-muted-foreground"
            >
              <Icon className="size-3.5" />
            </span>
            <h2 id={headingId} className="text-[0.9375rem] font-semibold tracking-tight">
              {title}
            </h2>
          </div>
          {lead ? <p className="text-sm text-muted-foreground">{lead}</p> : null}
          {status ? <div className="pt-1">{status}</div> : null}
        </div>
        <div className="flex min-w-0 flex-col gap-4">{children}</div>
      </div>
    </section>
  );
}

/** Small status pill used in section headers (enabled / not set up). */
export function StatePill({ on, children }: { on: boolean; children: ReactNode }) {
  return (
    <span
      className={cn(
        "inline-flex h-6 items-center gap-1.5 rounded-sm border px-2 font-mono text-2xs font-medium tracking-[0.08em] uppercase",
        on
          ? "border-status-success/40 bg-status-success-soft text-status-success-fg"
          : "border-border bg-surface-sunken text-muted-foreground",
      )}
    >
      <span
        aria-hidden="true"
        className={cn("size-1.5 rounded-full", on ? "bg-status-success" : "bg-status-neutral")}
      />
      {children}
    </span>
  );
}
