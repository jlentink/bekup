import { useQuery, useSubscription } from "@apollo/client/react";
import { History, Radio } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link } from "react-router";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { NativeSelect } from "@/features/jobs/fields";
import { Pager } from "@/features/jobs/pager";
import { useCursorPager } from "@/features/jobs/use-cursor-pager";
import type {
  RunFilter,
  RunStatus,
  RunTrigger,
  RunUpdatedSubscription as RunEvent,
} from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatDateTime, formatRelative } from "@/lib/format";

import { formatBytes, formatDuration } from "@/lib/format";
import { RunsListQuery, RunUpdatedSubscription } from "./operations";
import { RunStatusBadge } from "./run-status";
import { STATUSES, TRIGGERS } from "./run-history-model";

const PAGE_SIZE = 20;

const T = {
  status: "Status",
  statusAll: "Any status",
  trigger: "Trigger",
  triggerAll: "Any trigger",
  cols: {
    status: "Status",
    job: "Job",
    trigger: "Trigger",
    started: "Queued",
    duration: "Duration",
    items: "Databases",
    size: "Stored",
  },
  empty: "No runs yet.",
  emptyFiltered: "No runs match.",
  pager: "Run pages",
  live: "Live",
  warnings: (n: number) => `${String(n)} warning${n === 1 ? "" : "s"}`,
  attempt: (n: number) => `attempt ${String(n)}`,
  planned: (at: string) => `planned for ${at}`,
} as const;

/**
 * Paginated run history with status/trigger filters. With `live` it opens
 * the page's one runUpdated stream (D-149: filter by jobId on the server,
 * everything else on the client); events for runs on the page update the
 * cache, a new run refetches the page.
 */
export function RunHistory({
  jobId,
  live = true,
  onRunEvent,
  showJob = !jobId,
}: {
  jobId?: string;
  live?: boolean;
  onRunEvent?: (run: RunEvent["runUpdated"]) => void;
  showJob?: boolean;
}) {
  const [status, setStatus] = useState<RunStatus | "">("");
  const [trigger, setTrigger] = useState<RunTrigger | "">("");
  const filter: RunFilter = {};
  if (jobId) filter.jobId = jobId;
  if (status) filter.status = status;
  if (trigger) filter.trigger = trigger;
  const pager = useCursorPager(JSON.stringify(filter));
  const { data, loading, error, refetch } = useQuery(RunsListQuery, {
    variables: { filter, first: PAGE_SIZE, after: pager.after },
    fetchPolicy: "cache-and-network",
  });
  const rows = data?.runs.edges.map((e) => e.node) ?? [];
  const ids = useRef(new Set<string>());
  const onEvent = useRef(onRunEvent);
  const page = useRef(pager.page);
  useEffect(() => {
    ids.current = new Set(data?.runs.edges.map((e) => e.node.id) ?? []);
    onEvent.current = onRunEvent;
    page.current = pager.page;
  });

  const sub = useSubscription(RunUpdatedSubscription, {
    skip: !live,
    variables: jobId ? { jobId } : {},
    onData: ({ data: d }) => {
      const run = d.data?.runUpdated;
      if (!run) return;
      if (!ids.current.has(run.id) && page.current === 1) void refetch();
      onEvent.current?.(run);
    },
  });
  const filtered = Boolean(status || trigger);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <NativeSelect
          aria-label={T.status}
          value={status}
          onChange={(e) => {
            setStatus(e.target.value as RunStatus | "");
          }}
          className="w-44"
        >
          <option value="">{T.statusAll}</option>
          {STATUSES.map((s) => (
            <option key={s} value={s}>
              {s.charAt(0) + s.slice(1).toLowerCase()}
            </option>
          ))}
        </NativeSelect>
        <NativeSelect
          aria-label={T.trigger}
          value={trigger}
          onChange={(e) => {
            setTrigger(e.target.value as RunTrigger | "");
          }}
          className="w-52"
        >
          <option value="">{T.triggerAll}</option>
          {(Object.keys(TRIGGERS) as RunTrigger[]).map((t) => (
            <option key={t} value={t}>
              {TRIGGERS[t]}
            </option>
          ))}
        </NativeSelect>
        {live && !sub.error ? (
          <span className="ml-auto inline-flex items-center gap-1.5 font-mono text-2xs tracking-[0.12em] text-muted-foreground uppercase">
            <Radio aria-hidden="true" className="size-3 text-status-running" /> {T.live}
          </span>
        ) : null}
      </div>
      {error ? (
        <Alert variant="destructive">
          <AlertDescription>{userMessage(error)}</AlertDescription>
        </Alert>
      ) : null}
      <div className="overflow-x-auto rounded-lg border bg-card">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{T.cols.status}</TableHead>
              {showJob ? <TableHead>{T.cols.job}</TableHead> : null}
              <TableHead>{T.cols.trigger}</TableHead>
              <TableHead>{T.cols.started}</TableHead>
              <TableHead className="text-right">{T.cols.duration}</TableHead>
              <TableHead className="text-right">{T.cols.items}</TableHead>
              <TableHead className="text-right">{T.cols.size}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && rows.length === 0
              ? Array.from({ length: 3 }, (_, i) => (
                  <TableRow key={i}>
                    <TableCell colSpan={showJob ? 7 : 6}>
                      <Skeleton className="h-5 w-full" />
                    </TableCell>
                  </TableRow>
                ))
              : rows.map((run) => (
                  <TableRow key={run.id} data-testid="run-row">
                    <TableCell>
                      <Link
                        to={`/runs/${run.id}`}
                        className="inline-flex flex-col gap-0.5 rounded-sm"
                      >
                        <RunStatusBadge status={run.status} />
                        {run.warnings.length > 0 ? (
                          <span className="text-2xs text-status-partial-fg">
                            {T.warnings(run.warnings.length)}
                          </span>
                        ) : null}
                      </Link>
                    </TableCell>
                    {showJob ? (
                      <TableCell>
                        <Link
                          to={`/jobs/${run.job.id}`}
                          className={
                            "underline-offset-4 hover:underline " +
                            (run.job.deleted ? "text-muted-foreground line-through" : "")
                          }
                        >
                          {run.job.name}
                        </Link>
                      </TableCell>
                    ) : null}
                    <TableCell className="text-xs">
                      {TRIGGERS[run.trigger]}
                      {run.attempt > 1 ? (
                        <span className="ml-1 text-muted-foreground">
                          ({T.attempt(run.attempt)})
                        </span>
                      ) : null}
                      {run.trigger === "EARLY" && run.scheduledFor ? (
                        <div className="text-2xs text-muted-foreground">
                          {T.planned(formatDateTime(run.scheduledFor))}
                        </div>
                      ) : null}
                    </TableCell>
                    <TableCell className="whitespace-nowrap">
                      <Link to={`/runs/${run.id}`} className="hover:underline">
                        <time
                          dateTime={run.queuedAt}
                          title={formatDateTime(run.queuedAt)}
                          className="font-mono text-xs"
                        >
                          {formatDateTime(run.queuedAt)}
                        </time>
                      </Link>
                      <div className="text-2xs text-muted-foreground">
                        {formatRelative(run.queuedAt)}
                      </div>
                    </TableCell>
                    <TableCell className="text-right font-mono text-xs">
                      {formatDuration(run.durationSeconds)}
                    </TableCell>
                    <TableCell className="text-right font-mono text-xs">
                      {run.items.length}
                    </TableCell>
                    <TableCell className="text-right font-mono text-xs">
                      {run.bytesStored > 0 ? formatBytes(run.bytesStored) : ""}
                    </TableCell>
                  </TableRow>
                ))}
          </TableBody>
        </Table>
        {!loading && rows.length === 0 && !error ? (
          <div className="flex flex-col items-center gap-2 px-6 py-10 text-center">
            <History aria-hidden="true" className="size-5 text-muted-foreground" />
            <p className="text-sm font-medium">{filtered ? T.emptyFiltered : T.empty}</p>
          </div>
        ) : null}
      </div>
      <Pager
        label={T.pager}
        page={pager.page}
        hasNext={data?.runs.pageInfo.hasNextPage ?? false}
        onPrev={pager.prev}
        onNext={() => {
          pager.next(data?.runs.pageInfo.endCursor);
        }}
      />
    </div>
  );
}
