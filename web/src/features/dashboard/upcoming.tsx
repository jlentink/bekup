import { CalendarClock } from "lucide-react";
import { Link } from "react-router";

import { formatDateTime, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

import { HOUR_MS, type UpcomingRun } from "./model";
import { Tile, TileEmpty, TileError, TileSkeleton } from "./tile";

const clock = new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit" });
const LIST_MAX = 6;

/**
 * The next 24 hours as one ruled strip (SVG attributes only: CSP-safe),
 * one tick per planned run, with the soonest runs listed below.
 */
function Strip({ runs, now }: { runs: UpcomingRun[]; now: number }) {
  const W = 240;
  const H = 34;
  const ticks = [0, 6, 12, 18, 24];
  const labelled = new Set([0, 6, 12, 18]);
  return (
    <svg viewBox={`0 0 ${String(W)} ${String(H)}`} className="h-auto w-full" aria-hidden="true">
      <line x1={0} x2={W} y1={14} y2={14} className="stroke-border-strong" strokeWidth={1} />
      {ticks.map((h) => {
        const x = (h / 24) * W;
        return (
          <g key={h}>
            <line x1={x} x2={x} y1={10} y2={18} className="stroke-border-strong" strokeWidth={1} />
            {labelled.has(h) ? (
              <text
                x={x}
                y={30}
                textAnchor={h === 0 ? "start" : "middle"}
                className="dash-strip-label"
              >
                {h === 0 ? "now" : clock.format(new Date(now + h * HOUR_MS))}
              </text>
            ) : null}
          </g>
        );
      })}
      {runs.map((r, i) => (
        <circle
          key={`${r.jobId}-${r.at}-${String(i)}`}
          cx={Math.max(4, Math.min(W - 4, r.offset * W))}
          cy={14}
          r={4}
          strokeWidth={2}
          className={cn("stroke-card", r.overdue ? "fill-status-missed" : "fill-status-running")}
        >
          <title>{`${r.jobName} · ${formatDateTime(r.at)}`}</title>
        </circle>
      ))}
    </svg>
  );
}

/** Planned runs of the next 24 hours (calendar query, D-156). */
export function UpcomingTile({
  runs,
  loading,
  error,
  now,
}: {
  runs: UpcomingRun[];
  loading: boolean;
  error: unknown;
  now: number;
}) {
  return (
    <Tile
      folio="04"
      title="Next 24 hours"
      to="/calendar?view=day"
      linkLabel="Calendar"
      testId="tile-upcoming"
    >
      {loading && runs.length === 0 ? (
        <TileSkeleton rows={3} label="Loading planned runs" />
      ) : error && runs.length === 0 ? (
        <TileError error={error} />
      ) : runs.length === 0 ? (
        <TileEmpty icon={<CalendarClock aria-hidden="true" className="size-4" />}>
          No runs planned in the next 24 hours.
        </TileEmpty>
      ) : (
        <div className="flex flex-col gap-3">
          <Strip runs={runs} now={now} />
          <ol aria-label="Planned runs" className="flex flex-col gap-1.5">
            {runs.slice(0, LIST_MAX).map((r, i) => (
              <li
                key={`${r.jobId}-${r.at}-${String(i)}`}
                data-testid="upcoming-run"
                className="grid grid-cols-[4.75rem_1fr_auto] items-baseline gap-2"
              >
                <time dateTime={r.at} className="font-mono text-xs whitespace-nowrap">
                  {clock.format(new Date(r.at))}
                </time>
                <Link
                  to={`/jobs/${r.jobId}`}
                  className="truncate underline-offset-4 hover:underline"
                >
                  {r.jobName}
                </Link>
                {r.overdue ? (
                  <span className="text-2xs font-medium text-status-missed-fg">Overdue</span>
                ) : (
                  <span className="text-2xs text-muted-foreground">
                    {formatRelative(r.at, now)}
                  </span>
                )}
              </li>
            ))}
          </ol>
          {runs.length > LIST_MAX ? (
            <p className="text-2xs text-muted-foreground">
              and {runs.length - LIST_MAX} more today
            </p>
          ) : null}
        </div>
      )}
    </Tile>
  );
}
