import { skipToken, useQuery } from "@apollo/client/react";
import { DatabaseBackup, Plus, Search } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { Link } from "react-router";

import { PageHeader } from "@/components/page-header";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { RunStatusBadge } from "@/features/runs/run-status";
import type { JobFilter, JobsListQuery as JobsListResult } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatDateTime, formatRelative } from "@/lib/format";

import { EncryptionBadge } from "./encryption-badge";
import { NativeSelect } from "./fields";
import { JobEnabledToggle } from "./job-toggle";
import { JobFilterConnectionsQuery, JobsListQuery } from "./operations";
import { Pager } from "./pager";
import { useCursorPager } from "./use-cursor-pager";
import { useRoleFlags } from "./roles";

type JobRow = JobsListResult["jobs"]["edges"][number]["node"];

const PAGE_SIZE = 25;

const T = {
  eyebrow: "Backups",
  title: "Jobs",
  leadStaff: "Every backup job, its schedule, last result and encryption.",
  leadOwner: "The backup jobs you own.",
  newJob: "New job",
  search: "Search jobs",
  searchPlaceholder: "Name or slug",
  state: "State",
  stateAll: "All jobs",
  stateEnabled: "Enabled",
  stateDisabled: "Disabled",
  connection: "Connection",
  connectionAll: "All connections",
  empty: "No jobs match.",
  emptyFirst: "No backup jobs yet.",
  emptyOwner: "You do not own any backup jobs yet. An administrator can add you as an owner.",
  cols: {
    name: "Job",
    connection: "Connection",
    schedule: "Schedule",
    next: "Next run",
    last: "Last run",
    encryption: "Encryption",
    owners: "Owners",
    enabled: "Enabled",
  },
  never: "Never run",
  noNext: "Not planned",
  paused: "Disabled",
  pager: "Job pages",
  deletedConnection: "deleted",
} as const;

function useDebouncedValue(value: string, ms: number): string {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => {
      setV(value);
    }, ms);
    return () => {
      clearTimeout(t);
    };
  }, [value, ms]);
  return v;
}

/** /jobs: the job ledger with search and filters. */
export function JobsListPage() {
  const flags = useRoleFlags();
  const searchId = useId();
  const [search, setSearch] = useState("");
  const [state, setState] = useState<"" | "on" | "off">("");
  const [connectionId, setConnectionId] = useState("");
  const debouncedSearch = useDebouncedValue(search.trim(), 250);
  const filter: JobFilter = {};
  if (debouncedSearch) filter.search = debouncedSearch;
  if (state) filter.enabled = state === "on";
  if (connectionId) filter.connectionId = connectionId;
  const pager = useCursorPager(JSON.stringify(filter));

  const { data, loading, error } = useQuery(JobsListQuery, {
    variables: { filter, first: PAGE_SIZE, after: pager.after },
    fetchPolicy: "cache-and-network",
  });
  const connections = useQuery(JobFilterConnectionsQuery, flags.staff ? {} : skipToken);

  const rows = data?.jobs.edges.map((e) => e.node) ?? [];
  const filtered = Boolean(debouncedSearch || state || connectionId);

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        eyebrow={T.eyebrow}
        title={T.title}
        description={flags.staff ? T.leadStaff : T.leadOwner}
        actions={
          flags.staff ? (
            <Button asChild>
              <Link to="/jobs/new">
                <Plus aria-hidden="true" /> {T.newJob}
              </Link>
            </Button>
          ) : null
        }
      />

      <div
        role="search"
        aria-label={T.search}
        className="flex flex-col gap-3 sm:flex-row sm:items-end"
      >
        <div className="flex min-w-0 flex-1 flex-col gap-1.5">
          <label htmlFor={searchId} className="sr-only">
            {T.search}
          </label>
          <span className="relative">
            <Search
              aria-hidden="true"
              className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
            />
            <Input
              id={searchId}
              type="search"
              placeholder={T.searchPlaceholder}
              value={search}
              onChange={(e) => {
                setSearch(e.target.value);
              }}
              className="pl-9"
            />
          </span>
        </div>
        <NativeSelect
          aria-label={T.state}
          value={state}
          onChange={(e) => {
            setState(e.target.value as "" | "on" | "off");
          }}
          className="sm:w-40"
        >
          <option value="">{T.stateAll}</option>
          <option value="on">{T.stateEnabled}</option>
          <option value="off">{T.stateDisabled}</option>
        </NativeSelect>
        {flags.staff ? (
          <NativeSelect
            aria-label={T.connection}
            value={connectionId}
            onChange={(e) => {
              setConnectionId(e.target.value);
            }}
            className="sm:w-56"
          >
            <option value="">{T.connectionAll}</option>
            {(connections.data?.connections ?? [])
              .filter((c) => !c.temporary)
              .map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
          </NativeSelect>
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
              <TableHead>{T.cols.name}</TableHead>
              <TableHead>{T.cols.connection}</TableHead>
              <TableHead>{T.cols.schedule}</TableHead>
              <TableHead>{T.cols.next}</TableHead>
              <TableHead>{T.cols.last}</TableHead>
              <TableHead>{T.cols.encryption}</TableHead>
              {flags.staff ? <TableHead>{T.cols.owners}</TableHead> : null}
              <TableHead className="text-right">{T.cols.enabled}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && rows.length === 0
              ? Array.from({ length: 4 }, (_, i) => (
                  <TableRow key={i}>
                    <TableCell colSpan={flags.staff ? 8 : 7}>
                      <Skeleton className="h-5 w-full" />
                    </TableCell>
                  </TableRow>
                ))
              : rows.map((job) => <JobListRow key={job.id} job={job} staff={flags.staff} />)}
          </TableBody>
        </Table>
        {!loading && rows.length === 0 && !error ? (
          <div className="flex flex-col items-center gap-2 px-6 py-12 text-center">
            <DatabaseBackup aria-hidden="true" className="size-6 text-muted-foreground" />
            <p className="font-medium">
              {filtered ? T.empty : flags.staff ? T.emptyFirst : T.emptyOwner}
            </p>
          </div>
        ) : null}
      </div>

      <Pager
        label={T.pager}
        page={pager.page}
        hasNext={data?.jobs.pageInfo.hasNextPage ?? false}
        onPrev={pager.prev}
        onNext={() => {
          pager.next(data?.jobs.pageInfo.endCursor);
        }}
      />
    </div>
  );
}

function JobListRow({ job, staff }: { job: JobRow; staff: boolean }) {
  const last = job.lastRun;
  return (
    <TableRow data-testid={`job-row-${job.slug}`}>
      <TableCell className="min-w-48">
        <Link
          to={`/jobs/${job.id}`}
          className="font-medium underline-offset-4 hover:text-primary hover:underline"
        >
          {job.name}
        </Link>
        <div className="font-mono text-2xs text-muted-foreground">{job.slug}</div>
      </TableCell>
      <TableCell>
        <span className={job.connection.deleted ? "text-muted-foreground line-through" : ""}>
          {job.connection.name}
        </span>
        <div className="font-mono text-2xs tracking-wide text-muted-foreground uppercase">
          {job.connection.engine}
          {job.connection.deleted ? ` · ${T.deletedConnection}` : ""}
        </div>
      </TableCell>
      <TableCell className="max-w-64 text-sm whitespace-normal">
        {job.scheduleDescription}
      </TableCell>
      <TableCell className="whitespace-nowrap">
        {!job.enabled ? (
          <span className="text-muted-foreground">{T.paused}</span>
        ) : job.nextRunAt ? (
          <time
            dateTime={job.nextRunAt}
            title={formatDateTime(job.nextRunAt)}
            className="font-mono text-xs"
          >
            {formatRelative(job.nextRunAt)}
          </time>
        ) : (
          <span className="text-muted-foreground">{T.noNext}</span>
        )}
      </TableCell>
      <TableCell>
        {last ? (
          <Link to={`/runs/${last.id}`} className="inline-flex flex-col gap-0.5 rounded-sm">
            <RunStatusBadge status={last.status} />
            <span className="font-mono text-2xs text-muted-foreground">
              {formatRelative(last.finishedAt ?? last.queuedAt)}
              {last.warnings.length > 0 ? ` · ${String(last.warnings.length)} warning(s)` : ""}
            </span>
          </Link>
        ) : (
          <span className="text-xs text-muted-foreground">{T.never}</span>
        )}
      </TableCell>
      <TableCell>
        <EncryptionBadge encryption={job.encryption} />
      </TableCell>
      {staff ? (
        <TableCell>
          <OwnerChips owners={job.owners ?? []} />
        </TableCell>
      ) : null}
      <TableCell className="text-right">
        {staff ? (
          <JobEnabledToggle job={job} />
        ) : (
          <span className="text-xs text-muted-foreground">{job.enabled ? "Yes" : "No"}</span>
        )}
      </TableCell>
    </TableRow>
  );
}

export function OwnerChips({
  owners,
  max = 3,
}: {
  owners: readonly { userId: string; displayName: string; email: string; disabled: boolean }[];
  max?: number;
}) {
  if (owners.length === 0) return <span className="text-xs text-muted-foreground">None</span>;
  const shown = owners.slice(0, max);
  return (
    <ul className="flex flex-wrap gap-1" aria-label="Owners">
      {shown.map((o) => (
        <li
          key={o.userId}
          title={o.email}
          className={
            "inline-flex h-5 items-center rounded-full border bg-surface px-2 text-2xs " +
            (o.disabled ? "text-muted-foreground line-through" : "")
          }
        >
          {o.displayName || o.email}
        </li>
      ))}
      {owners.length > max ? (
        <li className="inline-flex h-5 items-center px-1 text-2xs text-muted-foreground">
          +{owners.length - max}
        </li>
      ) : null}
    </ul>
  );
}
