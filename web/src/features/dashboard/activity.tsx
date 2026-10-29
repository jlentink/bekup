import { Activity, RotateCcw } from "lucide-react";
import { Link } from "react-router";

import { RunStatusBadge } from "@/features/runs/run-status";
import type { RestoreStatus } from "@/graphql/gen/graphql";
import { formatDateTime, formatRelative } from "@/lib/format";

import type { LiveRun } from "./model";
import { LiveMarker, Tile, TileEmpty, TileError, TileSkeleton } from "./tile";

export interface ActiveRestore {
  id: string;
  status: RestoreStatus;
  createdAt: string;
  startedAt?: string | null;
  sourceDatabase?: string | null;
  targetDatabase?: string | null;
  job: { id: string; name: string };
  target: { id: string; name: string };
}

const RESTORE_LABEL: Record<RestoreStatus, string> = {
  QUEUED: "Queued",
  STAGING: "Staging",
  VERIFYING: "Verifying",
  RESTORING: "Restoring",
  SUCCEEDED: "Succeeded",
  FAILED: "Failed",
  CANCELED: "Canceled",
};

/** Running and queued backups and restores in progress (live via the page stream, D-197). */
export function ActivityTile({
  runs,
  restores,
  loading,
  error,
  live,
  now,
}: {
  runs: LiveRun[];
  restores: ActiveRestore[];
  loading: boolean;
  error: unknown;
  live: boolean;
  now: number;
}) {
  const empty = runs.length === 0 && restores.length === 0;
  return (
    <Tile
      folio="02"
      title="Running now"
      to="/runs"
      linkLabel="Runs"
      aside={<LiveMarker on={live} />}
      testId="tile-activity"
    >
      {loading && empty ? (
        <TileSkeleton rows={2} label="Loading active runs" />
      ) : error && empty ? (
        <TileError error={error} />
      ) : empty ? (
        <TileEmpty icon={<Activity aria-hidden="true" className="size-4" />}>
          Nothing is running. Quiet ledger.
        </TileEmpty>
      ) : (
        <ul aria-label="Active runs and restores" className="flex flex-col divide-y divide-rule">
          {runs.map((run) => (
            <li key={run.id} data-testid="active-run" className="flex items-start gap-3 py-2">
              <RunStatusBadge status={run.status} className="w-24 shrink-0" />
              <span className="flex min-w-0 flex-1 flex-col">
                <Link
                  to={`/runs/${run.id}`}
                  className="truncate font-medium underline-offset-4 hover:underline"
                >
                  {run.job.name}
                </Link>
                <time
                  dateTime={run.startedAt ?? run.queuedAt}
                  title={formatDateTime(run.startedAt ?? run.queuedAt)}
                  className="font-mono text-2xs text-muted-foreground"
                >
                  {run.status === "RUNNING" ? "started " : "queued "}
                  {formatRelative(run.startedAt ?? run.queuedAt, now)}
                </time>
              </span>
            </li>
          ))}
          {restores.map((r) => (
            <li key={r.id} data-testid="active-restore" className="flex items-start gap-3 py-2">
              <span className="inline-flex h-6 w-24 shrink-0 items-center gap-1.5 rounded-sm border border-status-running/40 bg-status-running-soft px-2 text-xs font-medium text-status-running-fg">
                <RotateCcw aria-hidden="true" className="size-3.5" />
                {RESTORE_LABEL[r.status]}
              </span>
              <span className="flex min-w-0 flex-1 flex-col">
                <Link
                  to={`/restores/${r.id}`}
                  className="truncate font-medium underline-offset-4 hover:underline"
                >
                  {r.job.name}
                </Link>
                <span className="truncate text-2xs text-muted-foreground">
                  {"into "}
                  {r.target.name}
                  {r.targetDatabase ? ` / ${r.targetDatabase}` : ""}
                  {" · "}
                  <time dateTime={r.startedAt ?? r.createdAt} className="font-mono">
                    {formatRelative(r.startedAt ?? r.createdAt, now)}
                  </time>
                </span>
              </span>
            </li>
          ))}
        </ul>
      )}
    </Tile>
  );
}
