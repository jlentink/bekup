import { useMutation, useQuery, useSubscription } from "@apollo/client/react";
import {
  ArrowLeft,
  Ban,
  CheckCircle2,
  CircleDashed,
  Download,
  FileQuestion,
  Info,
  RotateCcw,
  ShieldCheck,
  Trash,
  TriangleAlert,
  XCircle,
} from "lucide-react";
import { useState, type ReactNode } from "react";
import { Link, useParams } from "react-router";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { reportActionError } from "@/features/auth/use-step-up";
import { useRoleFlags } from "@/features/jobs/roles";
import type { ArtifactState, RunDetailQuery as RunDetailResult } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import {
  formatBytes,
  formatClock,
  formatDateTime,
  formatDuration,
  formatRelative,
} from "@/lib/format";
import { cn } from "@/lib/utils";

import { codeTitle } from "./codes";
import { CompletenessPanel } from "./completeness-panel";
import { DownloadDialog, type DownloadableArtifact } from "./download-dialog";

import { LogViewer } from "./log-viewer";
import { useRunLogs } from "./use-run-logs";
import { CancelRunMutation, RunActivitySubscription, RunDetailQuery } from "./operations";
import { TRIGGERS } from "./run-history-model";
import { RunStatusBadge } from "./run-status";
import { SchemaVersionValue } from "./schema-version";
import { isActive } from "./run-status-model";

type Run = NonNullable<RunDetailResult["run"]>;
type Item = Run["items"][number];
type ItemArtifact = Item["artifacts"][number];

const T = {
  back: "Back to the job",
  eyebrow: "Backup run",
  notFound: "This run does not exist.",
  cancel: "Cancel run",
  cancelTitle: "Cancel this run?",
  cancelLead:
    "The dump container is stopped and no partial file is kept. Databases already stored stay. Audited.",
  cancelRequested: "Cancellation requested.",
  cancelRetry: "The pending retry was canceled.",
  cancelPending: "Cancellation requested; the worker is stopping the run.",
  timeline: "Timeline",
  queued: "Queued",
  planned: "Planned for",
  started: "Started",
  finished: "Finished",
  cancelAsked: "Cancel requested",
  notYet: "pending",
  stats: {
    raw: "Dumped",
    stored: "Stored",
    duration: "Duration",
    trigger: "Trigger",
    by: "Started by",
  },
  attempt: (n: number) => `attempt ${String(n)}`,
  error: "Error",
  warnings: "Warnings",
  warningsLead: "Problems worth attention.",
  notices: "Notices",
  noticesLead: "Informational; nothing went wrong.",
  items: "Databases",
  globals: "Cluster globals",
  verified: "verified",
  unverified: "not verified",
  signed: "signed",
  states: { PRESENT: "Present", DELETED: "Deleted", MISSING: "Missing" } satisfies Record<
    ArtifactState,
    string
  >,
  download: "Download",
  restore: "Restore…",
  downloadUnavailable:
    "Only present, verified files covered by a signed manifest can be downloaded.",
  noArtifacts: "No file stored.",
  lockUntil: (at: string) => `locked until ${at}`,
} as const;

/** /runs/:runId */
export function RunDetailPage() {
  const { runId = "" } = useParams();
  const { data, loading, error } = useQuery(RunDetailQuery, {
    variables: { id: runId },
    fetchPolicy: "cache-and-network",
  });
  const run = data?.run;
  if (loading && !data) {
    return (
      <div className="flex flex-col gap-4">
        <Skeleton className="h-12 w-80" />
        <Skeleton className="h-32 w-full" />
        <Skeleton className="h-96 w-full" />
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
  if (!run) {
    return (
      <Alert>
        <AlertDescription>{T.notFound}</AlertDescription>
      </Alert>
    );
  }
  return <RunDetail key={run.id} run={run} />;
}

function RunDetail({ run }: { run: Run }) {
  const flags = useRoleFlags();
  const active = isActive(run.status);
  const logs = useRunLogs(run.id, active);
  // The page's one stream (D-197): run changes (merged into the cache by
  // id) and the live log tail, while the run is followed.
  const from = logs.streamFrom;
  const { applyBatch, streamEnded } = logs;
  useSubscription(RunActivitySubscription, {
    skip: from === null,
    variables: { runId: run.id, ...(from?.after ? { afterLogId: from.after } : {}) },
    onData: ({ data }) => {
      const batch = data.data?.runActivity.logs;
      if (batch) applyBatch(batch);
    },
    onComplete: streamEnded,
  });
  const [canceling, setCanceling] = useState(false);
  const [cancelRun] = useMutation(CancelRunMutation, { refetchQueries: [RunDetailQuery] });
  const [downloading, setDownloading] = useState<DownloadableArtifact | null>(null);
  const canCancel = flags.staff && active && !run.cancelRequested;

  return (
    <div className="flex flex-col gap-8">
      <Link
        to={`/jobs/${run.job.id}`}
        className="inline-flex w-fit items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft aria-hidden="true" className="size-3.5" /> {T.back}
      </Link>

      <header className="flex flex-col gap-5 border-b border-rule pb-6">
        <div className="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
          <div className="flex min-w-0 flex-col gap-1.5">
            <p className="eyebrow">
              {T.eyebrow} · <span className="normal-case">{run.id.slice(0, 8)}</span>
            </p>
            <h1 className="font-display text-display leading-none tracking-tight">
              <Link
                to={`/jobs/${run.job.id}`}
                className={cn("hover:underline", run.job.deleted && "line-through")}
              >
                {run.job.name}
              </Link>
            </h1>
            <div className="flex flex-wrap items-center gap-2 pt-1">
              <RunStatusBadge status={run.status} />
              <span className="font-mono text-xs text-muted-foreground">
                {formatDateTime(run.queuedAt)} · {TRIGGERS[run.trigger]}
                {run.attempt > 1 ? ` · ${T.attempt(run.attempt)}` : ""}
              </span>
              {run.cancelRequested && active ? (
                <span className="text-xs text-status-partial-fg">{T.cancelPending}</span>
              ) : null}
            </div>
          </div>
          {canCancel ? (
            <Button
              variant="outline"
              className="text-destructive"
              onClick={() => {
                setCanceling(true);
              }}
            >
              <Ban aria-hidden="true" /> {T.cancel}
            </Button>
          ) : null}
        </div>

        <dl className="grid gap-x-8 gap-y-4 sm:grid-cols-2 xl:grid-cols-5">
          <Stat label={T.stats.trigger}>{TRIGGERS[run.trigger]}</Stat>
          <Stat label={T.stats.duration}>
            <span className="font-mono">{formatDuration(run.durationSeconds) || "–"}</span>
          </Stat>
          <Stat label={T.stats.raw}>
            <span className="font-mono">{formatBytes(run.bytesRaw)}</span>
          </Stat>
          <Stat label={T.stats.stored}>
            <span className="font-mono">{formatBytes(run.bytesStored)}</span>
          </Stat>
          {run.triggeredBy ? (
            <Stat label={T.stats.by}>
              <span title={run.triggeredBy.email}>
                {run.triggeredBy.displayName || run.triggeredBy.email}
              </span>
            </Stat>
          ) : null}
        </dl>
      </header>

      <div className="grid gap-8 xl:grid-cols-[minmax(0,16rem)_minmax(0,1fr)]">
        <Timeline run={run} />
        <div className="flex min-w-0 flex-col gap-6">
          {run.errorMessage ? (
            <Alert variant="destructive">
              <XCircle aria-hidden="true" />
              <AlertDescription>
                <span className="font-medium">{T.error}:</span> {run.errorMessage}
                {run.errorCode ? (
                  <span className="ml-2 font-mono text-2xs opacity-80">{run.errorCode}</span>
                ) : null}
              </AlertDescription>
            </Alert>
          ) : null}
          <FindingList
            kind="warning"
            title={T.warnings}
            lead={T.warningsLead}
            entries={run.warnings}
          />
          <FindingList kind="notice" title={T.notices} lead={T.noticesLead} entries={run.notices} />
          <ItemsSection
            run={run}
            canDownload={flags.canDownload}
            admin={flags.admin}
            onDownload={setDownloading}
          />
        </div>
      </div>

      <CompletenessPanel run={run} />

      <LogViewer
        lines={logs.lines}
        live={logs.live}
        loading={logs.loading}
        error={logs.error}
        dropped={Math.max(logs.dropped, run.logLinesDropped)}
        hasMore={logs.hasMore}
        onLoadMore={logs.loadMore}
      />

      {downloading ? (
        <DownloadDialog
          artifact={downloading}
          open
          onOpenChange={(o) => {
            if (!o) setDownloading(null);
          }}
        />
      ) : null}

      <ConfirmDialog
        open={canceling}
        onOpenChange={setCanceling}
        title={T.cancelTitle}
        description={T.cancelLead}
        confirmLabel={T.cancel}
        destructive
        onConfirm={async () => {
          try {
            const r = await cancelRun({ variables: { id: run.id } });
            toast.success(r.data?.cancelRun.retryCanceled ? T.cancelRetry : T.cancelRequested);
          } catch (err) {
            reportActionError(err);
            throw err;
          }
        }}
      />
    </div>
  );
}

function Stat({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1 border-t border-rule pt-3">
      <dt className="eyebrow">{label}</dt>
      <dd className="text-sm">{children}</dd>
    </div>
  );
}

/** Vertical ledger timeline: queued → (planned) → started → cancel → finished. */
function Timeline({ run }: { run: Run }) {
  const steps: {
    label: string;
    at: string | null | undefined;
    tone: "done" | "pending" | "warn" | "fail";
  }[] = [{ label: T.queued, at: run.queuedAt, tone: "done" }];
  if (run.scheduledFor && (run.trigger === "EARLY" || run.trigger === "CATCH_UP")) {
    steps.push({ label: T.planned, at: run.scheduledFor, tone: "done" });
  }
  steps.push({ label: T.started, at: run.startedAt, tone: run.startedAt ? "done" : "pending" });
  if (run.cancelRequestedAt)
    steps.push({ label: T.cancelAsked, at: run.cancelRequestedAt, tone: "warn" });
  const failed = run.status === "FAILED" || run.status === "CANCELED";
  steps.push({
    label: T.finished,
    at: run.finishedAt,
    tone: run.finishedAt
      ? failed
        ? "fail"
        : run.status === "PARTIAL"
          ? "warn"
          : "done"
      : "pending",
  });
  return (
    <section aria-label={T.timeline} className="flex flex-col gap-3">
      <h2 className="eyebrow">{T.timeline}</h2>
      <ol className="relative flex flex-col gap-4 border-l border-rule pl-5">
        {steps.map((s) => (
          <li key={s.label} className="relative flex flex-col">
            <span
              aria-hidden="true"
              className={cn(
                "absolute top-1 -left-[1.6875rem] size-3 rounded-full border-2 border-background",
                s.tone === "done" && "bg-status-success",
                s.tone === "pending" && "bg-border-strong",
                s.tone === "warn" && "bg-status-partial",
                s.tone === "fail" && "bg-status-failed",
                s.tone === "pending" &&
                  isActive(run.status) &&
                  s.label === T.finished &&
                  "running-pulse bg-status-running",
              )}
            />
            <span className="text-sm font-medium">{s.label}</span>
            {s.at ? (
              <time
                dateTime={s.at}
                className="font-mono text-xs text-muted-foreground"
                title={formatDateTime(s.at)}
              >
                {formatDateTime(s.at)}
              </time>
            ) : (
              <span className="text-xs text-muted-foreground">{T.notYet}</span>
            )}
          </li>
        ))}
      </ol>
    </section>
  );
}

/** Warnings (amber, triangle) and notices (neutral, info) are styled apart on purpose (D-155). */
function FindingList({
  kind,
  title,
  lead,
  entries,
}: {
  kind: "warning" | "notice";
  title: string;
  lead: string;
  entries: readonly { code: string; message: string; database?: string | null }[];
}) {
  if (entries.length === 0) return null;
  const warning = kind === "warning";
  const Icon = warning ? TriangleAlert : Info;
  return (
    <section
      aria-label={title}
      data-kind={kind}
      className={cn(
        "flex flex-col gap-2 rounded-md border px-4 py-3",
        warning
          ? "border-l-4 border-status-partial/40 border-l-status-partial bg-status-partial-soft/60"
          : "border-dashed bg-surface",
      )}
    >
      <h2
        className={cn(
          "flex items-center gap-2 text-sm font-semibold",
          warning ? "text-status-partial-fg" : "text-muted-foreground",
        )}
      >
        <Icon aria-hidden="true" className="size-4" /> {title}
        <span className="font-mono text-2xs font-normal">({entries.length})</span>
        <span className="sr-only">: {lead}</span>
      </h2>
      <ul className="flex flex-col gap-1">
        {entries.map((w, i) => (
          <li key={`${w.code}-${String(i)}`} className="text-sm">
            <span className={cn("font-medium", !warning && "font-normal text-muted-foreground")}>
              {codeTitle(w.code)}
            </span>
            {w.database ? <span className="ml-1.5 font-mono text-xs">{w.database}</span> : null}
            <span className="text-muted-foreground"> · {w.message}</span>
          </li>
        ))}
      </ul>
    </section>
  );
}

const STATE_ICON: Record<ArtifactState, typeof CheckCircle2> = {
  PRESENT: CheckCircle2,
  DELETED: Trash,
  MISSING: FileQuestion,
};
const STATE_STYLE: Record<ArtifactState, string> = {
  PRESENT: "text-status-success-fg",
  DELETED: "text-muted-foreground",
  MISSING: "text-status-failed-fg",
};

function downloadable(a: ItemArtifact): boolean {
  return a.state === "PRESENT" && a.verified && a.signed;
}

function ItemsSection({
  run,
  canDownload,
  admin,
  onDownload,
}: {
  run: Run;
  canDownload: boolean;
  admin: boolean;
  onDownload: (a: DownloadableArtifact) => void;
}) {
  const items = [...run.items].sort((a, b) => a.position - b.position);
  return (
    <section aria-labelledby="run-items" className="flex flex-col gap-3">
      <h2 id="run-items" className="eyebrow">
        {T.items} ({items.length})
      </h2>
      <ul className="flex flex-col gap-3">
        {items.map((item) => (
          <li key={item.id} className="rounded-lg border bg-card" data-testid="run-item">
            <div className="flex flex-wrap items-center gap-x-4 gap-y-1 border-b border-rule px-4 py-2.5">
              <span className="font-mono text-sm font-medium">
                {item.kind === "GLOBALS" ? T.globals : item.database}
              </span>
              <RunStatusBadge status={item.status} />
              <span
                className={cn(
                  "inline-flex items-center gap-1 text-xs",
                  item.verified ? "text-status-success-fg" : "text-muted-foreground",
                )}
              >
                {item.verified ? (
                  <ShieldCheck aria-hidden="true" className="size-3.5" />
                ) : (
                  <CircleDashed aria-hidden="true" className="size-3.5" />
                )}
                {item.verified ? T.verified : T.unverified}
              </span>
              <SchemaVersionValue version={item.schemaVersion} status={item.schemaVersionStatus} />
              <span className="ml-auto font-mono text-xs text-muted-foreground">
                {formatBytes(item.rawSize)} → {formatBytes(item.storedSize)} ·{" "}
                {formatDuration(item.durationSeconds)}
              </span>
            </div>
            {item.errorMessage ? (
              <p className="px-4 pt-2 text-xs text-status-failed-fg">
                {item.errorMessage}
                {item.errorCode ? (
                  <span className="ml-2 font-mono text-2xs">{item.errorCode}</span>
                ) : null}
              </p>
            ) : null}
            {item.warnings.length > 0 ? (
              <ul
                className="flex flex-col gap-0.5 px-4 pt-2 text-xs text-status-partial-fg"
                data-kind="warning"
              >
                {item.warnings.map((w, i) => (
                  <li key={i} className="flex items-start gap-1.5">
                    <TriangleAlert aria-hidden="true" className="mt-0.5 size-3 shrink-0" />
                    <span>
                      <span className="font-medium">{codeTitle(w.code)}</span> · {w.message}
                    </span>
                  </li>
                ))}
              </ul>
            ) : null}
            {item.artifacts.length === 0 ? (
              <p className="px-4 py-2.5 text-xs text-muted-foreground">{T.noArtifacts}</p>
            ) : (
              <ul className="flex flex-col divide-y divide-rule px-4 py-1">
                {item.artifacts.map((a) => {
                  const Icon = STATE_ICON[a.state];
                  const canRestore =
                    a.state === "PRESENT" && a.verified && (item.kind === "DATABASE" || admin);
                  return (
                    <li
                      key={a.id}
                      className="flex flex-wrap items-center gap-x-4 gap-y-1 py-2"
                      data-testid="artifact"
                    >
                      <span className="min-w-32 text-sm">
                        <span className={a.destination.deleted ? "line-through" : ""}>
                          {a.destination.name}
                        </span>
                        <span className="ml-1.5 font-mono text-2xs text-muted-foreground uppercase">
                          {a.destination.type}
                        </span>
                      </span>
                      <span
                        className={cn(
                          "inline-flex items-center gap-1 text-xs",
                          STATE_STYLE[a.state],
                        )}
                      >
                        <Icon aria-hidden="true" className="size-3.5" /> {T.states[a.state]}
                      </span>
                      <span className="font-mono text-2xs text-muted-foreground">
                        {a.verified ? T.verified : T.unverified}
                        {a.signed ? ` · ${T.signed}` : ""}
                        {a.encryption.mode ? ` · ${a.encryption.mode.toLowerCase()}` : ""}
                        {a.lockUntil ? ` · ${T.lockUntil(formatDateTime(a.lockUntil))}` : ""}
                      </span>
                      <span className="font-mono text-xs text-muted-foreground" title={a.sha256}>
                        {formatBytes(a.size)}
                      </span>
                      <span className="ml-auto flex items-center gap-1.5">
                        {canDownload ? (
                          <Button
                            variant="outline"
                            size="xs"
                            disabled={!downloadable(a)}
                            title={downloadable(a) ? undefined : T.downloadUnavailable}
                            onClick={() => {
                              onDownload({
                                id: a.id,
                                kind: a.kind,
                                database: item.database ?? null,
                                encryption: { mode: a.encryption.mode ?? null },
                              });
                            }}
                          >
                            <Download aria-hidden="true" /> {T.download}
                          </Button>
                        ) : null}
                        {canRestore ? (
                          <Button variant="outline" size="xs" asChild>
                            <Link to={`/restore?artifactId=${encodeURIComponent(a.id)}`}>
                              <RotateCcw aria-hidden="true" /> {T.restore}
                            </Link>
                          </Button>
                        ) : null}
                      </span>
                    </li>
                  );
                })}
              </ul>
            )}
            <p className="px-4 pb-2 font-mono text-2xs text-muted-foreground">
              {formatClock(item.startedAt)}
              {item.finishedAt ? ` → ${formatClock(item.finishedAt)}` : ""}
              {item.finishedAt ? ` · ${formatRelative(item.finishedAt)}` : ""}
            </p>
          </li>
        ))}
      </ul>
    </section>
  );
}
