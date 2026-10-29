import { useQuery } from "@apollo/client/react";
import { ArrowRight, Loader2, RotateCcw } from "lucide-react";
import { useId, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router";

import { PageHeader } from "@/components/page-header";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { getFragmentData } from "@/graphql/gen";
import type { RestoreFilter, RestoreStatus } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatDateTime, formatDuration, formatRelative } from "@/lib/format";

import { STATUS_LABELS } from "./model";
import { RestoreFieldsFragment, RestoreHistoryQuery, RestoreJobsQuery } from "./operations";
import { RestoreProgress } from "./progress";
import { RestoreStatusBadge } from "./restore-status";

const ALL = "all";
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const STATUSES = Object.keys(STATUS_LABELS) as RestoreStatus[];

function uuidParam(v: string | null): string | undefined {
  return v && UUID_RE.test(v) ? v : undefined;
}

/** The restore history (`restores`) with filters in the URL. */
export function RestoresPage() {
  const [params, setParams] = useSearchParams();
  const statusId = useId();
  const jobId = useId();
  const status = STATUSES.find((s) => s === params.get("status"));
  const filter: RestoreFilter = {
    ...(status ? { status } : {}),
    ...(uuidParam(params.get("jobId")) ? { jobId: uuidParam(params.get("jobId")) } : {}),
    ...(uuidParam(params.get("artifactId"))
      ? { artifactId: uuidParam(params.get("artifactId")) }
      : {}),
    ...(uuidParam(params.get("targetConnectionId"))
      ? { targetConnectionId: uuidParam(params.get("targetConnectionId")) }
      : {}),
  };
  const jobs = useQuery(RestoreJobsQuery, { variables: {} });
  const { data, loading, error, fetchMore } = useQuery(RestoreHistoryQuery, {
    variables: { filter },
    fetchPolicy: "cache-and-network",
  });
  const [more, setMore] = useState(false);
  const rows = (data?.restores.edges ?? []).map((e) =>
    getFragmentData(RestoreFieldsFragment, e.node),
  );

  const setFilter = (key: string, value: string) => {
    const next = new URLSearchParams(params);
    if (value === ALL) next.delete(key);
    else next.set(key, value);
    setParams(next, { replace: true });
  };

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        eyebrow="Backups"
        title="Restores"
        description="Every restore, newest first. Backup owners see the restores of their own jobs."
        actions={
          <Button asChild>
            <Link to="/restore">
              <RotateCcw aria-hidden="true" />
              New restore
            </Link>
          </Button>
        }
      />
      <div className="flex flex-wrap items-end gap-4" role="group" aria-label="Filters">
        <div className="grid w-52 gap-1.5">
          <Label htmlFor={statusId}>Status</Label>
          <Select
            value={status ?? ALL}
            onValueChange={(v) => {
              setFilter("status", v);
            }}
          >
            <SelectTrigger id={statusId} className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>Any status</SelectItem>
              {STATUSES.map((s) => (
                <SelectItem key={s} value={s}>
                  {STATUS_LABELS[s]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="grid w-64 gap-1.5">
          <Label htmlFor={jobId}>Job</Label>
          <Select
            value={uuidParam(params.get("jobId")) ?? ALL}
            onValueChange={(v) => {
              setFilter("jobId", v);
            }}
          >
            <SelectTrigger id={jobId} className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>Any job</SelectItem>
              {(jobs.data?.jobs.edges ?? []).map(({ node }) => (
                <SelectItem key={node.id} value={node.id}>
                  {node.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        {filter.artifactId || filter.targetConnectionId ? (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              const next = new URLSearchParams(params);
              next.delete("artifactId");
              next.delete("targetConnectionId");
              setParams(next, { replace: true });
            }}
          >
            Clear backup/target filter
          </Button>
        ) : null}
      </div>

      {error ? (
        <Alert variant="destructive">
          <AlertDescription>{userMessage(error)}</AlertDescription>
        </Alert>
      ) : loading && rows.length === 0 ? (
        <Skeleton className="h-64 w-full" />
      ) : rows.length === 0 ? (
        <p className="rounded-md border border-dashed px-4 py-10 text-center text-sm text-muted-foreground">
          No restores match these filters.
        </p>
      ) : (
        <div className="overflow-x-auto rounded-md border bg-surface">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Requested</TableHead>
                <TableHead>Backup</TableHead>
                <TableHead>Target</TableHead>
                <TableHead>Mode</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Duration</TableHead>
                <TableHead>
                  <span className="sr-only">Open</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((r) => (
                <TableRow key={r.id}>
                  <TableCell>
                    <time dateTime={r.createdAt} title={formatDateTime(r.createdAt)}>
                      {formatRelative(r.createdAt)}
                    </time>
                    {r.requestedBy ? (
                      <span className="block text-xs text-muted-foreground">
                        {r.requestedBy.displayName}
                      </span>
                    ) : null}
                  </TableCell>
                  <TableCell>
                    <span className="block font-medium">{r.job.name}</span>
                    <span className="font-mono text-xs text-muted-foreground">
                      {r.kind === "GLOBALS" ? "cluster globals" : r.sourceDatabase}
                    </span>
                  </TableCell>
                  <TableCell>
                    <span className="block font-mono">{r.targetDatabase ?? "globals"}</span>
                    <span className="text-xs text-muted-foreground">
                      {r.target.name}
                      {r.target.temporary ? " (temporary)" : ""}
                    </span>
                  </TableCell>
                  <TableCell className="font-mono text-xs">{r.mode ?? "GLOBALS"}</TableCell>
                  <TableCell>
                    <RestoreStatusBadge status={r.status} />
                  </TableCell>
                  <TableCell data-numeric>{formatDuration(r.durationSeconds)}</TableCell>
                  <TableCell>
                    <Link
                      to={`/restores/${r.id}`}
                      className="inline-flex items-center gap-1 text-sm font-medium text-primary hover:underline"
                    >
                      Details
                      <span className="sr-only">
                        {" "}
                        of the restore of {r.job.name} requested {formatDateTime(r.createdAt)}
                      </span>
                      <ArrowRight aria-hidden="true" className="size-3.5" />
                    </Link>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      {data?.restores.pageInfo.hasNextPage ? (
        <div>
          <Button
            variant="outline"
            disabled={more}
            onClick={() => {
              setMore(true);
              void fetchMore({
                variables: { after: data.restores.pageInfo.endCursor },
                updateQuery: (prev, { fetchMoreResult }) => ({
                  restores: {
                    ...fetchMoreResult.restores,
                    edges: [...prev.restores.edges, ...fetchMoreResult.restores.edges],
                  },
                }),
              }).finally(() => {
                setMore(false);
              });
            }}
          >
            {more ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
            Load more
          </Button>
        </div>
      ) : null}
    </div>
  );
}

/** One restore: the live progress view. */
export function RestoreDetailPage() {
  const { restoreId } = useParams();
  const id = uuidParam(restoreId ?? null);
  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        eyebrow="Restore"
        title="Restore record"
        actions={
          <Button variant="outline" size="sm" asChild>
            <Link to="/restores">All restores</Link>
          </Button>
        }
      />
      {id ? (
        <RestoreProgress key={id} restoreId={id} />
      ) : (
        <Alert variant="destructive">
          <AlertDescription>That is not a valid restore link.</AlertDescription>
        </Alert>
      )}
    </div>
  );
}
