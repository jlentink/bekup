import { useQuery } from "@apollo/client/react";
import { Archive, BadgeCheck, Database, FileKey2, KeyRound, Users } from "lucide-react";
import { useId, useState } from "react";

import { StatusBadge } from "@/components/status/status-badge";
import type { RunStatus as UiRunStatus } from "@/components/status/status";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
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
import type {
  RestoreRunsQuery as RestoreRunsResult,
  RestoreTargetsQuery as RestoreTargetsResult,
  Role,
  RunStatus,
} from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatBytes, formatDateTime } from "@/lib/format";
import { cn } from "@/lib/utils";

import { ENGINE_LABELS } from "./model";
import { RestoreJobsQuery, RestoreRunsQuery } from "./operations";

type Run = RestoreRunsResult["runs"]["edges"][number]["node"];
type Artifact = Run["items"][number]["artifacts"][number];
export type RestoreSource = RestoreTargetsResult["restoreTargets"]["source"];

/** What the browse list knew about the picked artifact (for the summary). */
export interface PickedArtifact {
  id: string;
  jobName: string;
  database: string | null;
  kind: string;
  size: number;
  createdAt: string;
  destination: string;
  encryption: string | null;
}

const RUN_BADGE: Partial<Record<RunStatus, UiRunStatus>> = {
  SUCCESS: "success",
  PARTIAL: "partial",
  FAILED: "failed",
  RUNNING: "running",
  QUEUED: "queued",
};

/**
 * Only verified copies covered by a signed manifest can be restored
 * (D-167); globals backups are for admins only.
 */
function restorable(a: Artifact, role: Role): boolean {
  if (a.kind === "MANIFEST") return false;
  if (a.kind === "GLOBALS" && role !== "ADMIN") return false;
  return a.verified && a.signed && a.state === "PRESENT" && !a.destination.deleted;
}

export function SourceStep({
  role,
  artifactId,
  picked,
  source,
  sourceLoading,
  sourceError,
  onPick,
  onClear,
}: {
  role: Role;
  artifactId: string | undefined;
  picked: PickedArtifact | undefined;
  source: RestoreSource | undefined;
  sourceLoading: boolean;
  sourceError: unknown;
  onPick: (artifact: PickedArtifact) => void;
  onClear: () => void;
}) {
  if (artifactId) {
    return (
      <div className="flex flex-col gap-4">
        {sourceError ? (
          <Alert variant="destructive">
            <AlertTitle>This backup cannot be restored</AlertTitle>
            <AlertDescription>{userMessage(sourceError)}</AlertDescription>
          </Alert>
        ) : sourceLoading || !source ? (
          <Skeleton className="h-28 w-full" />
        ) : (
          <SourceSummary source={source} picked={picked} />
        )}
        <div>
          <Button variant="outline" size="sm" onClick={onClear}>
            Choose a different backup
          </Button>
        </div>
      </div>
    );
  }
  return <BackupBrowser role={role} onPick={onPick} />;
}

/** The backup about to be restored, as verified by the server. */
export function SourceSummary({
  source,
  picked,
}: {
  source: RestoreSource;
  picked?: PickedArtifact | undefined;
}) {
  const globals = source.kind === "GLOBALS";
  return (
    <div className="grid gap-px overflow-hidden rounded-md border bg-rule sm:grid-cols-[minmax(0,1.4fr)_repeat(3,minmax(0,1fr))]">
      <Fact label={globals ? "Cluster globals" : "Database"}>
        <span className="flex items-center gap-2 font-mono text-base">
          {globals ? (
            <Users aria-hidden="true" className="size-4 text-muted-foreground" />
          ) : (
            <Database aria-hidden="true" className="size-4 text-muted-foreground" />
          )}
          <span className="truncate">{globals ? "roles and tablespaces" : source.database}</span>
        </span>
        {picked ? <span className="text-xs text-muted-foreground">{picked.jobName}</span> : null}
      </Fact>
      <Fact label="Engine">
        {ENGINE_LABELS[source.engine] ?? source.engine} {source.versionKey}
        <span className="font-mono text-xs text-muted-foreground">
          server {source.serverVersion}
        </span>
      </Fact>
      <Fact label={picked ? "Taken" : "Integrity"}>
        {picked ? (
          <time dateTime={picked.createdAt}>{formatDateTime(picked.createdAt)}</time>
        ) : null}
        <span className="inline-flex items-center gap-1 text-xs text-status-success-fg">
          <BadgeCheck aria-hidden="true" className="size-3.5" />
          Verified, signed
        </span>
      </Fact>
      <Fact label={picked ? "Size" : "Key"}>
        {picked ? <span data-numeric>{formatBytes(picked.size)}</span> : null}
        <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
          {source.needsIdentity ? (
            <>
              <FileKey2 aria-hidden="true" className="size-3.5" /> Needs the age identity
            </>
          ) : (
            <>
              <KeyRound aria-hidden="true" className="size-3.5" /> Key held by bekup
            </>
          )}
        </span>
      </Fact>
    </div>
  );
}

function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1 bg-surface px-4 py-3">
      <span className="eyebrow">{label}</span>
      <div className="flex min-w-0 flex-col gap-0.5 text-sm">{children}</div>
    </div>
  );
}

/** Job → run → database copy, newest runs first. */
function BackupBrowser({ role, onPick }: { role: Role; onPick: (a: PickedArtifact) => void }) {
  const jobSelectId = useId();
  const jobs = useQuery(RestoreJobsQuery, { variables: {} });
  const [jobId, setJobId] = useState<string>();
  const jobList = jobs.data?.jobs.edges.map((e) => e.node) ?? [];
  const job = jobList.find((j) => j.id === jobId);

  return (
    <div className="flex flex-col gap-5">
      <div className="grid max-w-md gap-2">
        <Label htmlFor={jobSelectId}>Job</Label>
        {jobs.loading ? (
          <Skeleton className="h-9 w-full" />
        ) : jobs.error ? (
          <Alert variant="destructive">
            <AlertDescription>{userMessage(jobs.error)}</AlertDescription>
          </Alert>
        ) : jobList.length === 0 ? (
          <p className="text-sm text-muted-foreground">No jobs are visible to you.</p>
        ) : (
          <Select value={jobId ?? ""} onValueChange={setJobId}>
            <SelectTrigger id={jobSelectId} className="w-full">
              <SelectValue placeholder="Choose the job whose backup you restore" />
            </SelectTrigger>
            <SelectContent>
              {jobList.map((j) => (
                <SelectItem key={j.id} value={j.id}>
                  {j.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
      </div>
      {job ? (
        <RunList
          key={job.id}
          jobId={job.id}
          jobName={job.name}
          engine={`${ENGINE_LABELS[job.connection.engine] ?? job.connection.engine} ${job.connection.versionKey}`}
          role={role}
          onPick={onPick}
        />
      ) : null}
    </div>
  );
}

function RunList({
  jobId,
  jobName,
  engine,
  role,
  onPick,
}: {
  jobId: string;
  jobName: string;
  engine: string;
  role: Role;
  onPick: (a: PickedArtifact) => void;
}) {
  const { data, loading, error, fetchMore } = useQuery(RestoreRunsQuery, {
    variables: { jobId },
    fetchPolicy: "network-only",
  });
  const [more, setMore] = useState(false);
  if (loading && !data) return <Skeleton className="h-40 w-full" />;
  if (error) {
    return (
      <Alert variant="destructive">
        <AlertDescription>{userMessage(error)}</AlertDescription>
      </Alert>
    );
  }
  const runs = (data?.runs.edges ?? [])
    .map((e) => e.node)
    .map((run) => ({
      run,
      copies: run.items.flatMap((item) =>
        item.artifacts.filter((a) => restorable(a, role)).map((a) => ({ item, a })),
      ),
    }))
    .filter((r) => r.copies.length > 0);
  const page = data?.runs.pageInfo;

  return (
    <div className="flex flex-col gap-4">
      {runs.length === 0 ? (
        <p className="rounded-md border border-dashed px-4 py-6 text-center text-sm text-muted-foreground">
          No verified, signed backups of this job in the loaded runs.
        </p>
      ) : (
        <ol className="flex flex-col gap-3" aria-label={`Runs of ${jobName}`}>
          {runs.map(({ run, copies }) => {
            const when = run.storageTimestamp ?? run.startedAt ?? run.queuedAt;
            const badge = RUN_BADGE[run.status];
            return (
              <li key={run.id}>
                <fieldset className="rounded-md border bg-surface">
                  <legend className="sr-only">Run of {formatDateTime(when)}</legend>
                  <div
                    aria-hidden="true"
                    className="flex flex-wrap items-center gap-3 border-b border-rule px-4 py-2.5"
                  >
                    <Archive className="size-3.5 text-muted-foreground" />
                    <time dateTime={when} className="text-sm font-medium">
                      {formatDateTime(when)}
                    </time>
                    {badge ? <StatusBadge status={badge} /> : null}
                    <span className="eyebrow ml-auto">{engine}</span>
                  </div>
                  <ul className="divide-y divide-rule">
                    {copies.map(({ item, a }) => (
                      <li key={a.id}>
                        <label
                          className={cn(
                            "flex cursor-pointer flex-wrap items-center gap-x-4 gap-y-1 px-4 py-2.5 transition-colors hover:bg-accent/60",
                            "has-[:checked]:bg-accent has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-ring",
                          )}
                        >
                          <input
                            type="radio"
                            name="restore-artifact"
                            className="size-4 accent-primary"
                            onChange={() => {
                              onPick({
                                id: a.id,
                                jobName,
                                database: item.database,
                                kind: a.kind,
                                size: a.size,
                                createdAt: a.createdAt,
                                destination: a.destination.name,
                                encryption: a.encryption.mode ?? null,
                              });
                            }}
                          />
                          <span className="min-w-40 font-mono text-sm">
                            {a.kind === "GLOBALS" ? "cluster globals" : item.database}
                          </span>
                          <span className="text-xs text-muted-foreground">
                            on {a.destination.name}{" "}
                            <span className="font-mono">({a.destination.type})</span>
                          </span>
                          <span className="ml-auto text-xs text-muted-foreground" data-numeric>
                            {formatBytes(a.size)}
                          </span>
                        </label>
                      </li>
                    ))}
                  </ul>
                </fieldset>
              </li>
            );
          })}
        </ol>
      )}
      {page?.hasNextPage ? (
        <div>
          <Button
            variant="outline"
            size="sm"
            disabled={more}
            onClick={() => {
              setMore(true);
              void fetchMore({
                variables: { after: page.endCursor },
                updateQuery: (prev, { fetchMoreResult }) => ({
                  runs: {
                    ...fetchMoreResult.runs,
                    edges: [...prev.runs.edges, ...fetchMoreResult.runs.edges],
                  },
                }),
              }).finally(() => {
                setMore(false);
              });
            }}
          >
            Load older runs
          </Button>
        </div>
      ) : null}
    </div>
  );
}
