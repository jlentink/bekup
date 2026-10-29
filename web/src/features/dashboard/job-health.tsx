import { ClockAlert, DatabaseBackup, TriangleAlert } from "lucide-react";
import { Link } from "react-router";

import { StatusDot } from "@/components/status/status-badge";
import { runStatusLabel } from "@/features/runs/run-status-model";
import { formatDateTime, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

import { HEALTH_META, HEALTH_ORDER, type HealthState, type JobHealth } from "./model";
import { Tile, TileEmpty, TileError, TileSkeleton } from "./tile";

/**
 * Distribution of job states as one stacked bar (SVG geometry, no inline
 * styles) with a text legend; status is never conveyed by color alone.
 */
function HealthBar({ counts, total }: { counts: Record<HealthState, number>; total: number }) {
  const GAP = 0.5;
  const states = HEALTH_ORDER.filter((s) => counts[s] > 0);
  const usable = 100 - GAP * Math.max(0, states.length - 1);
  const segments = states.map((s, i) => {
    const before = states.slice(0, i).reduce((sum, p) => sum + (counts[p] / total) * usable, 0);
    return { s, x: before + i * GAP, w: (counts[s] / total) * usable };
  });
  return (
    <div className="flex flex-col gap-2.5">
      <svg
        viewBox="0 0 100 6"
        preserveAspectRatio="none"
        className="h-2 w-full overflow-visible"
        aria-hidden="true"
      >
        {segments.map(({ s, x: sx, w }) => (
          <rect key={s} x={sx} y={0} width={w} height={6} rx={0.6} className={HEALTH_META[s].fill}>
            <title>{`${HEALTH_META[s].label}: ${String(counts[s])}`}</title>
          </rect>
        ))}
      </svg>
      <ul aria-label="Jobs by state" className="flex flex-wrap gap-x-4 gap-y-1">
        {states.map((s) => (
          <li key={s} className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
            <StatusDot status={HEALTH_META[s].ui} aria-hidden="true" role="presentation" />
            <span>
              {HEALTH_META[s].label}{" "}
              <span className="font-mono text-foreground" data-testid={`health-count-${s}`}>
                {counts[s]}
              </span>
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

function HealthCard({ job, now }: { job: JobHealth; now: number }) {
  const meta = HEALTH_META[job.state];
  return (
    <li>
      <Link
        to={`/jobs/${job.id}`}
        data-testid="health-card"
        data-state={job.state}
        className={cn(
          "dash-health-card group flex h-full flex-col gap-2 rounded-md border bg-surface px-3 py-2.5",
          "transition-colors hover:border-border-strong hover:bg-accent/40",
        )}
      >
        <span className="flex items-start justify-between gap-2">
          <span className="truncate font-medium group-hover:underline" title={job.name}>
            {job.name}
          </span>
          <span className="inline-flex shrink-0 items-center gap-1.5 text-2xs font-medium text-muted-foreground">
            <StatusDot status={meta.ui} aria-hidden="true" role="presentation" />
            {meta.label}
          </span>
        </span>
        <span className="grid grid-cols-[auto_1fr] gap-x-2 gap-y-0.5 text-2xs">
          <span className="text-muted-foreground">Last</span>
          <span className="truncate">
            {job.last ? (
              <>
                {runStatusLabel(job.last.status)}{" "}
                <time
                  dateTime={job.last.finishedAt ?? job.last.queuedAt}
                  title={formatDateTime(job.last.finishedAt ?? job.last.queuedAt)}
                  className="text-muted-foreground"
                >
                  {formatRelative(job.last.finishedAt ?? job.last.queuedAt, now)}
                </time>
              </>
            ) : (
              <span className="text-muted-foreground">No runs yet</span>
            )}
          </span>
          <span className="text-muted-foreground">Next</span>
          <span className="truncate">
            {job.state === "paused" ? (
              <span className="text-muted-foreground">Disabled</span>
            ) : job.nextRunAt ? (
              <time dateTime={job.nextRunAt} title={formatDateTime(job.nextRunAt)}>
                {formatRelative(job.nextRunAt, now)}
              </time>
            ) : (
              <span className="text-muted-foreground" title={job.schedule}>
                Manual only
              </span>
            )}
          </span>
        </span>
        {job.overdue || job.missed || job.warnings > 0 || job.unencrypted ? (
          <span className="flex flex-wrap gap-1">
            {job.missed ? (
              <Marker className="border-status-missed/40 bg-status-missed-soft text-status-missed-fg">
                <ClockAlert aria-hidden="true" className="size-3" /> Missed occurrence
              </Marker>
            ) : null}
            {job.overdue ? (
              <Marker className="border-status-missed/40 bg-status-missed-soft text-status-missed-fg">
                <ClockAlert aria-hidden="true" className="size-3" /> Overdue
              </Marker>
            ) : null}
            {job.warnings > 0 ? (
              <Marker className="border-status-partial/40 bg-status-partial-soft text-status-partial-fg">
                <TriangleAlert aria-hidden="true" className="size-3" />
                {job.warnings === 1 ? "1 warning" : `${String(job.warnings)} warnings`}
              </Marker>
            ) : null}
            {job.unencrypted ? (
              <Marker className="hatch-insecure border-status-insecure/60 font-mono tracking-[0.06em] text-status-insecure-fg uppercase">
                Unencrypted
              </Marker>
            ) : null}
          </span>
        ) : null}
      </Link>
    </li>
  );
}

function Marker({ className, children }: { className: string; children: React.ReactNode }) {
  return (
    <span
      className={cn(
        "inline-flex h-5 items-center gap-1 rounded-xs border px-1.5 text-2xs font-medium",
        className,
      )}
    >
      {children}
    </span>
  );
}

/** Status per job: last result, next run, missed/overdue markers and warnings. */
export function JobHealthTile({
  rows,
  counts,
  loading,
  error,
  more,
  owner,
  now,
}: {
  rows: JobHealth[];
  counts: Record<HealthState, number>;
  loading: boolean;
  error: unknown;
  more: boolean;
  owner: boolean;
  now: number;
}) {
  return (
    <Tile
      folio="01"
      title={owner ? "Your jobs" : "Job health"}
      to="/jobs"
      linkLabel="All jobs"
      testId="tile-job-health"
    >
      {loading && rows.length === 0 ? (
        <TileSkeleton rows={4} label="Loading jobs" />
      ) : error && rows.length === 0 ? (
        <TileError error={error} />
      ) : rows.length === 0 ? (
        <TileEmpty icon={<DatabaseBackup aria-hidden="true" className="size-4" />}>
          No jobs yet.
        </TileEmpty>
      ) : (
        <div className="flex flex-col gap-4">
          <HealthBar counts={counts} total={rows.length} />
          <ul
            aria-label="Jobs"
            className="grid grid-cols-[repeat(auto-fill,minmax(14rem,1fr))] gap-2"
          >
            {rows.map((job) => (
              <HealthCard key={job.id} job={job} now={now} />
            ))}
          </ul>
          {more ? (
            <p className="text-2xs text-muted-foreground">
              Showing the first 200 jobs.{" "}
              <Link to="/jobs" className="underline underline-offset-4">
                See every job
              </Link>
            </p>
          ) : null}
        </div>
      )}
    </Tile>
  );
}
