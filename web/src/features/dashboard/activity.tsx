import { Activity, RotateCcw } from "lucide-react";
import { Link } from "react-router";

import { RunStatusBadge } from "@/features/runs/run-status";
import type { RestoreStatus } from "@/graphql/gen/graphql";
import { formatDateTime, formatRelative } from "@/lib/format";
import { defineMessages } from "@/lib/i18n";

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

const T = defineMessages({
  en: {
    title: "Running now",
    link: "Runs",
    loading: "Loading active runs",
    empty: "Nothing is running. Quiet ledger.",
    listLabel: "Active runs and restores",
    started: "started ",
    queued: "queued ",
    into: "into ",
    restore: {
      QUEUED: "Queued",
      STAGING: "Staging",
      VERIFYING: "Verifying",
      RESTORING: "Restoring",
      SUCCEEDED: "Succeeded",
      FAILED: "Failed",
      CANCELED: "Canceled",
    },
  },
  nl: {
    title: "Nu bezig",
    link: "Runs",
    loading: "Actieve runs laden",
    empty: "Er loopt niets. Alles is rustig.",
    listLabel: "Actieve runs en herstelacties",
    started: "gestart ",
    queued: "in de wachtrij ",
    into: "naar ",
    restore: {
      QUEUED: "In de wachtrij",
      STAGING: "Klaarzetten",
      VERIFYING: "Controleren",
      RESTORING: "Herstellen",
      SUCCEEDED: "Geslaagd",
      FAILED: "Mislukt",
      CANCELED: "Geannuleerd",
    },
  },
});

const RESTORE_LABEL: Record<RestoreStatus, string> = T.restore;

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
      title={T.title}
      to="/runs"
      linkLabel={T.link}
      aside={<LiveMarker on={live} />}
      testId="tile-activity"
    >
      {loading && empty ? (
        <TileSkeleton rows={2} label={T.loading} />
      ) : error && empty ? (
        <TileError error={error} />
      ) : empty ? (
        <TileEmpty icon={<Activity aria-hidden="true" className="size-4" />}>{T.empty}</TileEmpty>
      ) : (
        <ul aria-label={T.listLabel} className="flex flex-col divide-y divide-rule">
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
                  {run.status === "RUNNING" ? T.started : T.queued}
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
                  {T.into}
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
