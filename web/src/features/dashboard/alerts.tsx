import { BellOff, CircleAlert, Info, OctagonX, TriangleAlert, type LucideIcon } from "lucide-react";
import { Link } from "react-router";

import type { NotificationSeverity } from "@/graphql/gen/graphql";
import { formatDateTime, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

import { internalLink } from "./model";
import { Tile, TileEmpty, TileError, TileSkeleton } from "./tile";

export interface AlertItem {
  id: string;
  type: string;
  severity: NotificationSeverity;
  title: string;
  link?: string | null;
  createdAt: string;
}

const SEVERITY: Record<NotificationSeverity, { icon: LucideIcon; text: string; label: string }> = {
  CRITICAL: { icon: OctagonX, text: "text-status-failed-fg", label: "Critical" },
  ERROR: { icon: CircleAlert, text: "text-status-failed-fg", label: "Error" },
  WARNING: { icon: TriangleAlert, text: "text-status-partial-fg", label: "Warning" },
  INFO: { icon: Info, text: "text-muted-foreground", label: "Info" },
};

/** Unread notifications of the caller's inbox (task 5.3b); the full center is task 6.10. */
export function AlertsTile({
  unread,
  items,
  loading,
  error,
  now,
}: {
  unread: number | undefined;
  items: AlertItem[];
  loading: boolean;
  error: unknown;
  now: number;
}) {
  return (
    <Tile
      folio="07"
      title="Alerts"
      aside={
        unread ? (
          <span
            data-testid="unread-count"
            className="inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-primary px-1.5 font-mono text-2xs text-primary-foreground"
          >
            {unread}
            <span className="sr-only"> unread</span>
          </span>
        ) : null
      }
      testId="tile-alerts"
    >
      {loading && unread === undefined ? (
        <TileSkeleton rows={2} label="Loading alerts" />
      ) : error && unread === undefined ? (
        <TileError error={error} />
      ) : items.length === 0 ? (
        <TileEmpty icon={<BellOff aria-hidden="true" className="size-4" />}>
          No unread alerts.
        </TileEmpty>
      ) : (
        <ul aria-label="Unread alerts" className="flex flex-col divide-y divide-rule">
          {items.map((n) => {
            const meta = SEVERITY[n.severity];
            const Icon = meta.icon;
            const to = internalLink(n.link);
            return (
              <li key={n.id} className="grid grid-cols-[auto_1fr_auto] items-start gap-x-2.5 py-2">
                <Icon
                  aria-label={meta.label}
                  role="img"
                  className={cn("mt-0.5 size-4", meta.text)}
                />
                {to ? (
                  <Link to={to} className="min-w-0 truncate underline-offset-4 hover:underline">
                    {n.title}
                  </Link>
                ) : (
                  <span className="min-w-0 truncate">{n.title}</span>
                )}
                <time
                  dateTime={n.createdAt}
                  title={formatDateTime(n.createdAt)}
                  className="font-mono text-2xs whitespace-nowrap text-muted-foreground"
                >
                  {formatRelative(n.createdAt, now)}
                </time>
              </li>
            );
          })}
        </ul>
      )}
    </Tile>
  );
}
