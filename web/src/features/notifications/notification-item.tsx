import { AlertOctagon, AlertTriangle, CircleAlert, Info, type LucideIcon } from "lucide-react";

import type { NotificationFieldsFragment, NotificationSeverity } from "@/graphql/gen/graphql";
import { formatDateTime, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

import { notificationsMessages as M } from "./messages";

const SEVERITY_ICON: Record<NotificationSeverity, LucideIcon> = {
  INFO: Info,
  WARNING: AlertTriangle,
  ERROR: CircleAlert,
  CRITICAL: AlertOctagon,
};

const SEVERITY_TEXT: Record<NotificationSeverity, string> = {
  INFO: "text-status-neutral-fg",
  WARNING: "text-status-partial-fg",
  ERROR: "text-status-failed-fg",
  CRITICAL: "text-status-insecure-fg",
};

const SEVERITY_RULE: Record<NotificationSeverity, string> = {
  INFO: "bg-status-neutral/60",
  WARNING: "bg-status-partial",
  ERROR: "bg-status-failed",
  CRITICAL: "bg-status-insecure",
};

/** The ledger margin of an entry: a severity rule plus icon (shape + color + name). */
export function SeverityMark({ severity }: { severity: NotificationSeverity }) {
  const Icon = SEVERITY_ICON[severity];
  return (
    <span className="flex items-stretch gap-2 self-stretch">
      <span aria-hidden="true" className={cn("w-0.5 rounded-full", SEVERITY_RULE[severity])} />
      <Icon
        role="img"
        aria-label={M.severity[severity]}
        className={cn("mt-0.5 size-3.5 shrink-0", SEVERITY_TEXT[severity])}
      />
    </span>
  );
}

/** Title, body and time of one entry; unread entries are set in weight and marked. */
export function NotificationText({
  n,
  clamp = false,
}: {
  n: NotificationFieldsFragment;
  clamp?: boolean;
}) {
  const unread = n.readAt === null;
  return (
    <span className="flex min-w-0 flex-1 flex-col gap-0.5">
      <span className="flex items-baseline gap-2">
        <span
          className={cn(
            "min-w-0 flex-1 text-[0.8125rem] leading-snug",
            unread ? "font-semibold text-foreground" : "font-medium text-muted-foreground",
            clamp && "truncate",
          )}
        >
          {n.title}
        </span>
        {unread ? (
          <span className="inline-flex size-1.5 shrink-0 translate-y-[-1px] rounded-full bg-primary">
            <span className="sr-only">{M.inbox.unreadMark}</span>
          </span>
        ) : null}
      </span>
      <span
        className={cn("text-xs leading-relaxed text-muted-foreground", clamp && "line-clamp-2")}
      >
        {n.body}
      </span>
      <time
        dateTime={n.createdAt}
        title={formatDateTime(n.createdAt)}
        className="font-mono text-2xs tracking-wide text-muted-foreground/80"
      >
        {formatRelative(n.createdAt)}
      </time>
    </span>
  );
}
