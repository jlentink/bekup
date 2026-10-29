import { Link } from "react-router";

import { cn } from "@/lib/utils";

import { eventLabel, VARIANT_META } from "./event-chip-model";
import { eventHref, formatTime, variantOf, type CalEvent, type Variant } from "./model";

/** One event: a link to the run or job, styled by kind and status. */
export function EventChip({
  event: e,
  tz,
  tabbable = true,
  showTime = true,
  className,
}: {
  event: CalEvent;
  tz: string;
  tabbable?: boolean;
  showTime?: boolean;
  className?: string;
}) {
  const variant = variantOf(e);
  const meta = VARIANT_META[variant];
  const Icon = meta.icon;
  const label = eventLabel(e, tz);
  return (
    <Link
      to={eventHref(e)}
      aria-label={label}
      title={label}
      tabIndex={tabbable ? 0 : -1}
      data-variant={variant}
      data-kind={e.kind}
      className={cn(
        "group flex min-w-0 items-center gap-1.5 rounded-xs px-1.5 py-0.5 text-2xs leading-4 transition-colors hover:brightness-95 focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring dark:hover:brightness-110",
        meta.chip,
        className,
      )}
    >
      <Icon
        aria-hidden="true"
        className={cn("size-3 shrink-0", variant === "run-running" && "animate-spin")}
      />
      {showTime ? (
        <span className="shrink-0 font-mono tabular-nums opacity-80">
          {formatTime(e.start, tz)}
        </span>
      ) : null}
      <span className="truncate">
        {e.kind === "EXPIRY" && e.projected ? "≈ " : ""}
        {e.jobName}
      </span>
    </Link>
  );
}

/** The key to the event styles. */
export function Legend() {
  const items: [Variant, string][] = [
    ["planned", "Planned"],
    ["paused", "Paused"],
    ["early", "Started early"],
    ["overdue", "Overdue"],
    ["skipped", "Skipped"],
    ["run-success", "Run succeeded"],
    ["run-partial", "Partial"],
    ["run-failed", "Failed"],
    ["run-running", "Running"],
    ["expiry-exact", "Expiry, exact"],
    ["expiry-projected", "Expiry, projected"],
  ];
  return (
    <ul aria-label="Legend" className="flex flex-wrap gap-1.5">
      {items.map(([v, label]) => {
        const meta = VARIANT_META[v];
        const Icon = meta.icon;
        return (
          <li
            key={v}
            className={cn(
              "inline-flex items-center gap-1 rounded-xs px-1.5 py-0.5 text-2xs",
              meta.chip,
            )}
          >
            <Icon aria-hidden="true" className="size-3" />
            {label}
          </li>
        );
      })}
    </ul>
  );
}
