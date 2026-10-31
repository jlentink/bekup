import { skipToken, useMutation, useQuery } from "@apollo/client/react";
import { ArrowLeft, CalendarDays, FlaskConical, Pencil, Trash2 } from "lucide-react";
import { useState, type ReactNode } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { reportActionError } from "@/features/auth/use-step-up";
import { RunHistory } from "@/features/runs/run-history";
import { RunStatusBadge } from "@/features/runs/run-status";
import { isActive } from "@/features/runs/run-status-model";
import { SchemaVersionValue } from "@/features/runs/schema-version";
import type { JobDetailQuery as JobDetailResult } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatDateTime, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

import { EncryptionBadge } from "./encryption-badge";
import { OwnerChips } from "./jobs-list-page";
import { JobEnabledToggle } from "./job-toggle";
import {
  DeleteJobMutation,
  JobDetailQuery,
  JobsListQuery,
  LatestTestRestoreQuery,
  TestRestoreDetailQuery,
} from "./operations";
import { RetentionHoldsAlert } from "./retention-holds";
import { useRoleFlags } from "./roles";
import { RunActions } from "./run-actions";
import { DiffSummaryLine } from "./test-restore-findings";
import { diffSummary } from "./test-restore-model";
import { TestRestoreVerdict } from "./test-restore-verdict";
import { RunTestRestoreNowButton } from "./test-restores-page";
import { formatInZone } from "./schedule";

type Job = NonNullable<JobDetailResult["job"]>;

const T = {
  back: "All jobs",
  eyebrow: "Backup job",
  disabledBanner:
    "This job is disabled: no scheduled or manual runs start until it is enabled again.",
  edit: "Edit",
  delete: "Delete",
  deleteTitle: (n: string) => `Delete ${n}?`,
  deleteLead:
    "The schedule stops at once and a run in progress finishes. Stored backups and keys stay and can still be restored.",
  deleted: "Job deleted.",
  facts: {
    next: "Next run",
    schedule: "Schedule",
    last: "Last run",
    connection: "Connection",
    databases: "Databases",
    retention: "Retention",
    destinations: "Destinations",
    encryption: "Encryption",
    verification: "Evidence",
    schemaVersion: "Schema version",
    owners: "Owners",
  },
  schemaVersionMode: (mode: string, label: string | null | undefined) =>
    mode === "CUSTOM" ? "Custom query" : (label ?? "Preset"),
  schemaVersionLast: "Last run",
  schemaVersionNoRun: "Captured from the next run on.",
  schemaVersionNoneRecorded:
    "The last run recorded no version: only complete database dumps record one.",
  noNext: "Not planned",
  disabled: "Disabled",
  never: "Never run",
  allDbs: "All databases",
  except: (list: string) => `except ${list}`,
  globals: "+ cluster globals",
  keepLast: (n: number) => `newest ${String(n)}`,
  keepDays: (n: number) => `${String(n)} days`,
  verification: { STANDARD: "Standard (estimates)", DEEP: "Deep (exact for PostgreSQL)" },
  history: "Run history",
  testRestore: "Test restores",
  testRestoreNone: "No test restore has run for this job yet.",
  testRestoreOff: "Scheduled test restores are off.",
  testRestoreTarget: (n: string) => `Scratch target: ${n}`,
  testRestoreNext: (at: string) => `next ${at}`,
  testRestoreLatest: "Latest verdict",
  testRestoreCounts: (p: number, w: number, f: number, total: number) =>
    `${String(p)} passed · ${String(w)} with warnings · ${String(f)} failed of ${String(total)}`,
  testRestoreRun: "Open the tested run",
  testRestoreHistory: "All test restores",
  testRestoreDetails: "Show the differences",
  testRestoreAcked: (at: string) =>
    `The scratch target's role is privileged or unknown; an admin acknowledged it ${at}.`,
  testRestoreUnacked:
    "The scratch target's role is privileged or unknown and not acknowledged: test restores refuse to run until an admin designates it again.",
  calendar: "Open in the calendar",
  notFound: "This job does not exist or was deleted.",
} as const;

/** /jobs/:jobId */
export function JobDetailPage() {
  const { jobId = "" } = useParams();
  const { data, loading, error, refetch } = useQuery(JobDetailQuery, {
    variables: { id: jobId },
    fetchPolicy: "cache-and-network",
  });
  if (loading && !data) {
    return (
      <div className="flex flex-col gap-4">
        <Skeleton className="h-12 w-80" />
        <Skeleton className="h-40 w-full" />
        <Skeleton className="h-64 w-full" />
      </div>
    );
  }
  if (error && !data) {
    return (
      <Alert variant="destructive">
        <AlertDescription>{userMessage(error)}</AlertDescription>
      </Alert>
    );
  }
  if (!data?.job) {
    return (
      <Alert>
        <AlertDescription>{T.notFound}</AlertDescription>
      </Alert>
    );
  }
  return (
    <JobDetail
      job={data.job}
      onRunFinished={() => {
        void refetch();
      }}
    />
  );
}

function Fact({
  label,
  children,
  className,
}: {
  label: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex min-w-0 flex-col gap-1 border-t border-rule pt-3", className)}>
      <dt className="eyebrow">{label}</dt>
      <dd className="min-w-0 text-sm">{children}</dd>
    </div>
  );
}

function JobDetail({ job, onRunFinished }: { job: Job; onRunFinished: () => void }) {
  const flags = useRoleFlags();
  const navigate = useNavigate();
  const [deleting, setDeleting] = useState(false);
  const [deleteJob] = useMutation(DeleteJobMutation, { refetchQueries: [JobsListQuery] });
  const zone = job.schedule.timezone;
  const destinations = [...job.destinations].sort((a, b) => a.position - b.position);

  return (
    <div className="flex flex-col gap-8">
      <Link
        to="/jobs"
        className="inline-flex w-fit items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft aria-hidden="true" className="size-3.5" /> {T.back}
      </Link>

      <header className="flex flex-col gap-5 border-b border-rule pb-6">
        <div className="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
          <div className="flex min-w-0 flex-col gap-1.5">
            <p className="eyebrow">
              {T.eyebrow} · <span className="normal-case">{job.slug}</span>
            </p>
            <h1 className="font-display text-display leading-none tracking-tight">{job.name}</h1>
            {job.description ? (
              <p className="max-w-prose text-sm text-muted-foreground">{job.description}</p>
            ) : null}
            <div className="flex flex-wrap items-center gap-2 pt-1">
              {job.lastRun ? <RunStatusBadge status={job.lastRun.status} /> : null}
              <EncryptionBadge encryption={job.encryption} />
              {flags.staff ? (
                <JobEnabledToggle job={job} showLabel />
              ) : (
                <span className="text-xs text-muted-foreground">
                  {job.enabled ? "Enabled" : T.disabled}
                </span>
              )}
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <RunActions job={job} canPreview={flags.staff} />
            {flags.staff ? (
              <>
                <Button variant="outline" asChild>
                  <Link to={`/jobs/${job.id}/edit`}>
                    <Pencil aria-hidden="true" /> {T.edit}
                  </Link>
                </Button>
                <Button
                  variant="ghost"
                  className="text-destructive hover:bg-status-failed-soft hover:text-destructive"
                  onClick={() => {
                    setDeleting(true);
                  }}
                >
                  <Trash2 aria-hidden="true" /> {T.delete}
                </Button>
              </>
            ) : null}
          </div>
        </div>
        {!job.enabled ? (
          <Alert>
            <AlertDescription>{T.disabledBanner}</AlertDescription>
          </Alert>
        ) : null}
        <RetentionHoldsAlert holds={job.retentionHolds} historyAnchor="job-history" />

        <dl className="grid gap-x-8 gap-y-4 sm:grid-cols-2 xl:grid-cols-4">
          <Fact label={T.facts.next}>
            {!job.enabled ? (
              <span className="text-muted-foreground">{T.disabled}</span>
            ) : job.nextRunAt ? (
              <span className="flex flex-col">
                <time dateTime={job.nextRunAt} className="font-mono text-sm">
                  {formatInZone(job.nextRunAt, zone)}
                </time>
                <span className="text-xs text-muted-foreground">
                  {formatRelative(job.nextRunAt)}
                </span>
              </span>
            ) : (
              <span className="text-muted-foreground">{T.noNext}</span>
            )}
          </Fact>
          <Fact label={T.facts.schedule}>{job.scheduleDescription}</Fact>
          <Fact label={T.facts.last}>
            {job.lastRun ? (
              <Link to={`/runs/${job.lastRun.id}`} className="flex flex-col hover:underline">
                <span>{formatDateTime(job.lastRun.finishedAt ?? job.lastRun.queuedAt)}</span>
                <span className="text-xs text-muted-foreground">
                  {isActive(job.lastRun.status)
                    ? "in progress"
                    : formatRelative(job.lastRun.finishedAt ?? job.lastRun.queuedAt)}
                </span>
              </Link>
            ) : (
              <span className="text-muted-foreground">{T.never}</span>
            )}
          </Fact>
          <Fact label={T.facts.connection}>
            <span className={job.connection.deleted ? "line-through" : ""}>
              {job.connection.name}
            </span>
            <span className="ml-2 font-mono text-2xs text-muted-foreground uppercase">
              {job.connection.engine} {job.connection.versionKey}
            </span>
          </Fact>
          <Fact label={T.facts.databases}>
            {job.dbMode === "ALL" ? (
              <>
                {T.allDbs}
                {job.exclude.length ? (
                  <span className="text-muted-foreground"> {T.except(job.exclude.join(", "))}</span>
                ) : null}
              </>
            ) : (
              <span className="font-mono text-xs">{job.databases.join(", ")}</span>
            )}
            {job.includeGlobals && job.connection.engine === "POSTGRES" ? (
              <span className="block text-xs text-muted-foreground">{T.globals}</span>
            ) : null}
          </Fact>
          <Fact label={T.facts.retention}>
            {[
              job.retention.keepLast != null ? T.keepLast(job.retention.keepLast) : null,
              job.retention.keepDays != null ? T.keepDays(job.retention.keepDays) : null,
            ]
              .filter(Boolean)
              .join(" · ")}
          </Fact>
          <Fact label={T.facts.destinations}>
            <ol className="flex flex-col">
              {destinations.map((d) => (
                <li key={d.id} className={d.deleted ? "text-muted-foreground line-through" : ""}>
                  <span className="mr-1.5 font-mono text-2xs text-muted-foreground">
                    {d.position + 1}.
                  </span>
                  {d.name}
                  <span className="ml-1.5 font-mono text-2xs text-muted-foreground uppercase">
                    {d.type}
                  </span>
                </li>
              ))}
            </ol>
          </Fact>
          <Fact label={T.facts.verification}>{T.verification[job.verification]}</Fact>
          {job.schemaVersion.mode !== "NONE" ? (
            <Fact label={T.facts.schemaVersion} className="sm:col-span-2">
              <SchemaVersionFact job={job} />
            </Fact>
          ) : null}
          {flags.staff ? (
            <Fact label={T.facts.owners} className="sm:col-span-2">
              <OwnerChips owners={job.owners ?? []} max={8} />
            </Fact>
          ) : null}
        </dl>
      </header>

      <section aria-labelledby="job-history" className="flex flex-col gap-4">
        <div className="flex items-end justify-between gap-4">
          <h2 id="job-history" className="scroll-mt-20 font-display text-2xl">
            {T.history}
          </h2>
          <Link
            to={`/calendar?jobId=${encodeURIComponent(job.id)}`}
            className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
          >
            <CalendarDays aria-hidden="true" className="size-3.5" /> {T.calendar}
          </Link>
        </div>
        <RunHistory
          jobId={job.id}
          onRunEvent={(run) => {
            if (!isActive(run.status)) onRunFinished();
          }}
        />
      </section>

      <TestRestoreSection job={job} />

      <ConfirmDialog
        open={deleting}
        onOpenChange={setDeleting}
        title={T.deleteTitle(job.name)}
        description={T.deleteLead}
        confirmLabel={T.delete}
        destructive
        onConfirm={async () => {
          try {
            await deleteJob({ variables: { id: job.id } });
            toast.success(T.deleted);
            void navigate("/jobs");
          } catch (err) {
            reportActionError(err);
            throw err;
          }
        }}
      />
    </div>
  );
}

/** The capture mode and the versions of the last run, per database (D-210). */
function SchemaVersionFact({ job }: { job: Job }) {
  const sv = job.schemaVersion;
  const items = [...(job.lastRun?.items ?? [])]
    .filter((i) => i.kind === "DATABASE" && i.schemaVersionStatus)
    .sort((a, b) => a.position - b.position);
  return (
    <div className="flex flex-col gap-1.5">
      <span>
        {T.schemaVersionMode(sv.mode, sv.presetLabel)}
        {sv.mode === "PRESET" && job.connection.engine === "POSTGRES" ? (
          <span className="ml-1.5 font-mono text-2xs text-muted-foreground">{sv.schema}</span>
        ) : null}
      </span>
      {items.length > 0 ? (
        <ul aria-label={T.schemaVersionLast} className="flex flex-col gap-0.5">
          {items.map((i) => (
            <li key={i.id} className="flex min-w-0 items-center gap-2">
              <span className="truncate font-mono text-xs text-muted-foreground">{i.database}</span>
              <SchemaVersionValue
                version={i.schemaVersion}
                status={i.schemaVersionStatus}
                showLabel={false}
              />
            </li>
          ))}
        </ul>
      ) : (
        <span className="text-xs text-muted-foreground">
          {job.lastRun && !isActive(job.lastRun.status)
            ? T.schemaVersionNoneRecorded
            : T.schemaVersionNoRun}
        </span>
      )}
    </div>
  );
}

function TestRestoreSection({ job }: { job: Job }) {
  const flags = useRoleFlags();
  const { data, loading } = useQuery(LatestTestRestoreQuery, { variables: { jobId: job.id } });
  const latest = data?.testRestores.edges[0]?.node;
  // The diff of the latest test restore (its databases and findings).
  const detail = useQuery(
    TestRestoreDetailQuery,
    latest ? { variables: { id: latest.id } } : skipToken,
  );
  const dbs = detail.data?.testRestore?.databases;
  const tr = job.testRestore;
  return (
    <section
      aria-labelledby="job-test-restores"
      className="flex flex-col gap-3 rounded-lg border bg-card px-5 py-4"
    >
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2
          id="job-test-restores"
          className="flex items-center gap-2 text-[0.9375rem] font-semibold"
        >
          <FlaskConical aria-hidden="true" className="size-4 text-muted-foreground" />{" "}
          {T.testRestore}
        </h2>
        <div className="flex flex-wrap items-center gap-3">
          {flags.staff && tr.target ? <RunTestRestoreNowButton jobId={job.id} /> : null}
          <Link
            to={`/jobs/${job.id}/test-restores`}
            className="text-xs text-primary underline-offset-4 hover:underline"
          >
            {T.testRestoreHistory}
          </Link>
        </div>
      </div>
      <p className="text-sm text-muted-foreground">
        {tr.enabled ? tr.scheduleDescription : T.testRestoreOff}
        {tr.target ? ` · ${T.testRestoreTarget(tr.target.name)}` : ""}
        {tr.enabled && tr.nextAt ? ` · ${T.testRestoreNext(formatDateTime(tr.nextAt))}` : ""}
      </p>
      {tr.targetPrivileges?.acknowledgementRequired ? (
        <p className="text-xs text-status-partial-fg">
          {tr.privilegedTargetAcknowledgedAt
            ? T.testRestoreAcked(formatDateTime(tr.privilegedTargetAcknowledgedAt))
            : T.testRestoreUnacked}
        </p>
      ) : null}
      {loading && !data ? (
        <Skeleton className="h-6 w-64" />
      ) : latest ? (
        <div className="flex flex-col gap-2">
          <div className="flex flex-wrap items-center gap-3">
            <span className="eyebrow">{T.testRestoreLatest}</span>
            <TestRestoreVerdict status={latest.status} />
            <span className="font-mono text-xs text-muted-foreground">
              {formatDateTime(latest.finishedAt ?? latest.createdAt)}
            </span>
            <span className="text-xs text-muted-foreground">
              {T.testRestoreCounts(
                latest.passed,
                latest.warned,
                latest.failed,
                latest.databaseCount,
              )}
            </span>
            <Link
              to={`/jobs/${job.id}/test-restores/${latest.id}`}
              className="text-xs text-primary underline-offset-4 hover:underline"
            >
              {T.testRestoreDetails}
            </Link>
            {latest.runId ? (
              <Link
                to={`/runs/${latest.runId}`}
                className="text-xs text-primary underline-offset-4 hover:underline"
              >
                {T.testRestoreRun}
              </Link>
            ) : null}
          </div>
          {latest.errorMessage ? (
            <p className="w-full text-xs text-status-failed-fg">{latest.errorMessage}</p>
          ) : null}
          {dbs ? (
            <DiffSummaryLine summary={diffSummary(dbs)} />
          ) : detail.loading ? (
            <Skeleton className="h-6 w-80" />
          ) : null}
        </div>
      ) : (
        <p className="text-sm text-muted-foreground">{T.testRestoreNone}</p>
      )}
    </section>
  );
}
