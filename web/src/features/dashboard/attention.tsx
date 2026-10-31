import { ArchiveX, CircleCheck, ClockAlert, OctagonX } from "lucide-react";
import { Link } from "react-router";

import { backups, holdReason } from "@/features/jobs/retention-model";
import { formatDateTime, formatRelative } from "@/lib/format";

import type { AttentionItem } from "./model";
import { Tile, TileEmpty, TileError, TileSkeleton } from "./tile";

const T = {
  empty: "No failures in the last 7 days, no missed backups and no stuck retention.",
  listLabel: "Failures, missed backups and retention holds",
  retention: "Retention",
  held: (n: number, dest: string, deleted: boolean) =>
    `keeps ${backups(n)} incomplete on ${dest}${deleted ? " (deleted)" : ""}`,
  heldSince: (at: string) => `Oldest held backup from ${at}`,
  since: (rel: string) => `since ${rel}`,
} as const;

/**
 * Failed runs of the last 7 days, jobs in an ongoing missed-backup outage
 * (the server's missed-backup check, D-188/D-196) and destinations where
 * retention keeps incomplete backups (D-209), newest first.
 */
export function AttentionTile({
  items,
  loading,
  error,
  now,
}: {
  items: AttentionItem[];
  loading: boolean;
  error: unknown;
  now: number;
}) {
  return (
    <Tile
      folio="03"
      title="Needs attention"
      to="/runs"
      linkLabel="Run history"
      aside={<span className="font-mono text-2xs text-muted-foreground">7 days</span>}
      testId="tile-attention"
    >
      {loading && items.length === 0 ? (
        <TileSkeleton rows={3} label="Loading failures" />
      ) : error && items.length === 0 ? (
        <TileError error={error} />
      ) : items.length === 0 ? (
        <TileEmpty icon={<CircleCheck aria-hidden="true" className="size-4 text-status-success" />}>
          {T.empty}
        </TileEmpty>
      ) : (
        <ol aria-label={T.listLabel} className="flex flex-col divide-y divide-rule">
          {items.map((item) =>
            item.kind === "failed" ? (
              <li
                key={`f-${item.run.id}`}
                data-testid="attention-item"
                data-kind="failed"
                className="grid grid-cols-[auto_1fr_auto] items-start gap-x-3 py-2"
              >
                <OctagonX aria-hidden="true" className="mt-0.5 size-4 text-status-failed" />
                <span className="min-w-0">
                  <Link
                    to={`/runs/${item.run.id}`}
                    className="font-medium underline-offset-4 hover:underline"
                  >
                    {item.run.job.name}
                  </Link>
                  <span className="block truncate text-xs text-muted-foreground">
                    <span className="text-status-failed-fg">Failed</span>
                    {item.run.errorMessage ? ` · ${item.run.errorMessage}` : ""}
                  </span>
                </span>
                <time
                  dateTime={item.at}
                  title={formatDateTime(item.at)}
                  className="font-mono text-2xs whitespace-nowrap text-muted-foreground"
                >
                  {formatRelative(item.at, now)}
                </time>
              </li>
            ) : item.kind === "retention" ? (
              <li
                key={`h-${item.job.id}-${item.hold.destinationId}`}
                data-testid="attention-item"
                data-kind="retention"
                className="grid grid-cols-[auto_1fr_auto] items-start gap-x-3 py-2"
              >
                <ArchiveX aria-hidden="true" className="mt-0.5 size-4 text-status-partial" />
                <span className="min-w-0">
                  <Link
                    to={`/jobs/${item.job.id}`}
                    className="font-medium underline-offset-4 hover:underline"
                  >
                    {item.job.name}
                  </Link>
                  <span className="block text-xs text-muted-foreground">
                    <span className="text-status-partial-fg">{T.retention}</span> ·{" "}
                    {T.held(
                      item.hold.heldRuns,
                      item.hold.destinationName,
                      item.hold.destinationDeleted,
                    )}
                    : {holdReason(item.hold)}
                  </span>
                </span>
                <time
                  dateTime={item.at}
                  title={T.heldSince(formatDateTime(item.at))}
                  className="font-mono text-2xs whitespace-nowrap text-muted-foreground"
                >
                  {T.since(formatRelative(item.at, now))}
                </time>
              </li>
            ) : (
              <li
                key={`m-${item.job.id}`}
                data-testid="attention-item"
                data-kind="missed"
                className="grid grid-cols-[auto_1fr_auto] items-start gap-x-3 py-2"
              >
                <ClockAlert aria-hidden="true" className="mt-0.5 size-4 text-status-missed" />
                <span className="min-w-0">
                  <Link
                    to={`/jobs/${item.job.id}`}
                    className="font-medium underline-offset-4 hover:underline"
                  >
                    {item.job.name}
                  </Link>
                  <span className="block truncate text-xs text-muted-foreground">
                    <span className="text-status-missed-fg">Missed</span> · no successful backup in
                    its window
                  </span>
                </span>
                {item.at ? (
                  <time
                    dateTime={item.at}
                    title={`Missed since ${formatDateTime(item.at)}`}
                    className="font-mono text-2xs whitespace-nowrap text-muted-foreground"
                  >
                    since {formatRelative(item.at, now)}
                  </time>
                ) : (
                  <span />
                )}
              </li>
            ),
          )}
        </ol>
      )}
    </Tile>
  );
}
