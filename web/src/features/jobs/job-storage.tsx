import { useQuery } from "@apollo/client/react";
import { HardDrive } from "lucide-react";

import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { JobStorageQuery as JobStorageResult } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatBytes, formatDateTime } from "@/lib/format";

import { STORAGE_T } from "./job-storage-text";
import { JobStorageQuery } from "./operations";

type Storage = NonNullable<JobStorageResult["job"]>["storage"];
type TrendRun = Storage["trend"][number];

const TYPE_LABEL = { DISK: "Disk", S3: "S3", SFTP: "SFTP", WEBDAV: "WebDAV" } as const;

const T = STORAGE_T;

/**
 * The size trend: one bar per backup on a shared zero baseline (one
 * series, one hue; partial runs are drawn in the partial status color and
 * named in their tooltip). SVG geometry only (no inline styles, CSP).
 */
function Trend({ runs }: { runs: TrendRun[] }) {
  if (runs.length === 0) return null;
  const sizes = runs.map((r) => r.bytes);
  const max = Math.max(...sizes);
  const min = Math.min(...sizes);
  const H = 32;
  const W = 6;
  const GAP = 2;
  const width = runs.length * W + (runs.length - 1) * GAP;
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-baseline justify-between gap-3">
        <h3 className="eyebrow">{T.trend}</h3>
        <span
          className="font-mono text-2xs text-muted-foreground"
          data-testid="storage-trend-range"
        >
          {T.trendRange(formatBytes(min), formatBytes(max))}
        </span>
      </div>
      <svg
        role="img"
        aria-label={T.trendLabel(runs.length, formatBytes(min), formatBytes(max))}
        viewBox={`0 0 ${String(width)} ${String(H)}`}
        preserveAspectRatio="none"
        className="h-12 w-full max-w-xl"
        data-testid="storage-trend"
      >
        {runs.map((r, i) => {
          const h = max > 0 ? Math.max(1, (r.bytes / max) * H) : 1;
          return (
            <rect
              key={r.runId}
              x={i * (W + GAP)}
              y={H - h}
              width={W}
              height={h}
              rx={0.8}
              className={r.status === "PARTIAL" ? "fill-status-partial" : "fill-primary"}
            >
              <title>
                {T.trendBar(formatDateTime(r.at), formatBytes(r.bytes), r.status === "PARTIAL")}
              </title>
            </rect>
          );
        })}
      </svg>
      <p className="text-xs text-muted-foreground">{T.trendLead(runs.length)}</p>
    </div>
  );
}

function StorageBody({ storage }: { storage: Storage }) {
  if (storage.totalBytes === 0 && storage.perDestination.length === 0) {
    return (
      <p className="flex items-center gap-2 text-sm text-muted-foreground">
        <HardDrive aria-hidden="true" className="size-4" /> {T.empty}
      </p>
    );
  }
  return (
    <div className="flex flex-col gap-6">
      <dl className="grid gap-x-8 gap-y-4 sm:grid-cols-2">
        <div className="flex flex-col gap-1 border-t border-rule pt-3">
          <dt className="eyebrow">{T.total}</dt>
          <dd className="flex flex-col">
            <span className="font-mono text-lg" data-testid="storage-total">
              {formatBytes(storage.totalBytes)}
            </span>
            <span className="text-xs text-muted-foreground">
              {T.backups(storage.backupCount)} · {T.files(storage.artifactCount)}
            </span>
          </dd>
        </div>
        <div className="flex flex-col gap-1 border-t border-rule pt-3">
          <dt className="eyebrow">{T.newest}</dt>
          <dd className="flex flex-col">
            {storage.newestRun ? (
              <>
                <span className="font-mono text-lg" data-testid="storage-newest">
                  {formatBytes(storage.newestRun.bytes)}
                </span>
                <span className="text-xs text-muted-foreground">
                  {formatDateTime(storage.newestRun.at)} · {T.newestHint}
                </span>
              </>
            ) : (
              <span className="text-sm text-muted-foreground">{T.newestNone}</span>
            )}
          </dd>
        </div>
      </dl>
      {storage.perDestination.length > 0 ? (
        <div className="flex flex-col gap-2">
          <h3 className="eyebrow">{T.perDestination}</h3>
          <div className="rounded-lg border bg-card">
            <Table aria-label={T.perDestination}>
              <TableHeader>
                <TableRow>
                  <TableHead>{T.cols.destination}</TableHead>
                  <TableHead className="text-right">{T.cols.stored}</TableHead>
                  <TableHead className="text-right">{T.cols.backups}</TableHead>
                  <TableHead className="text-right">{T.cols.files}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {storage.perDestination.map((d) => (
                  <TableRow key={d.destinationId} data-testid="storage-destination">
                    <TableCell>
                      <span className={d.deleted ? "text-muted-foreground line-through" : ""}>
                        {d.name}
                      </span>
                      <span className="ml-1.5 font-mono text-2xs text-muted-foreground uppercase">
                        {TYPE_LABEL[d.type]}
                        {d.deleted ? ` · ${T.deleted}` : ""}
                      </span>
                    </TableCell>
                    <TableCell className="text-right font-mono text-xs">
                      {formatBytes(d.storedBytes)}
                    </TableCell>
                    <TableCell className="text-right font-mono text-xs">{d.backupCount}</TableCell>
                    <TableCell className="text-right font-mono text-xs">
                      {d.artifactCount}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </div>
      ) : null}
      <Trend runs={storage.trend} />
    </div>
  );
}

/** The job page's storage section (D-225); same access as the job. */
export function JobStorageSection({ jobId }: { jobId: string }) {
  const { data, loading, error } = useQuery(JobStorageQuery, {
    variables: { id: jobId },
    fetchPolicy: "cache-and-network",
  });
  const storage = data?.job?.storage;
  return (
    <section aria-labelledby="job-storage" className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 id="job-storage" className="scroll-mt-20 font-display text-2xl">
          {T.title}
        </h2>
        <p className="max-w-prose text-sm text-muted-foreground">{T.lead}</p>
      </div>
      {storage ? (
        <StorageBody storage={storage} />
      ) : loading ? (
        <div className="flex flex-col gap-2">
          <span className="sr-only">{T.loading}</span>
          <Skeleton className="h-12 w-72" />
          <Skeleton className="h-24 w-full" />
        </div>
      ) : error ? (
        <p role="alert" className="text-sm text-destructive">
          {userMessage(error)}
        </p>
      ) : null}
    </section>
  );
}
