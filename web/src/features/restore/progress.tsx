import { useApolloClient, useQuery, useSubscription } from "@apollo/client/react";
import { Ban, CheckCircle2, Lock, OctagonX, ScrollText, TriangleAlert } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useSessionUser } from "@/features/auth/use-session-user";
import { getFragmentData } from "@/graphql/gen";
import type {
  RestoreFieldsFragment,
  RestoreLogsQuery as RestoreLogsResult,
  RestoreStatus,
} from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatBytes, formatDateTime, formatDuration } from "@/lib/format";
import { cn } from "@/lib/utils";

import { ENGINE_LABELS, isActive, neutralizedFunctions, PHASES, STATUS_LABELS } from "./model";
import {
  RestoreCancelMutation,
  RestoreDetailQuery,
  RestoreFieldsFragment as RestoreFields,
  RestoreLogsQuery,
  RestoreUpdatedSubscription,
} from "./operations";
import { RestoreStatusBadge } from "./restore-status";

type LogLine = NonNullable<RestoreLogsResult["restoreLogs"]>["lines"][number];

/** The newer of two snapshots of the same restore. */
function newer(
  a: RestoreFieldsFragment | undefined,
  b: RestoreFieldsFragment | undefined,
): RestoreFieldsFragment | undefined {
  if (!a) return b;
  if (!b) return a;
  return Date.parse(b.updatedAt) >= Date.parse(a.updatedAt) ? b : a;
}

/**
 * Live view of one restore: the phase strip, the log, cancel (staff) and
 * the result with the neutralized SECURITY DEFINER routines (D-171).
 * Live state comes from restoreUpdated (one stream, closed once finished).
 */
export function RestoreProgress({ restoreId }: { restoreId: string }) {
  const user = useSessionUser();
  const detail = useQuery(RestoreDetailQuery, { variables: { id: restoreId } });
  const loaded = getFragmentData(RestoreFields, detail.data?.restore ?? undefined);
  const live = useSubscription(RestoreUpdatedSubscription, {
    variables: { restoreId },
    skip: !loaded || !isActive(loaded.status),
  });
  const restore = newer(loaded, getFragmentData(RestoreFields, live.data?.restoreUpdated));
  const [lines, setLines] = useState<LogLine[]>([]);

  if (detail.loading && !restore) return <Skeleton className="h-64 w-full" />;
  if (detail.error) {
    return (
      <Alert variant="destructive">
        <AlertDescription>{userMessage(detail.error)}</AlertDescription>
      </Alert>
    );
  }
  if (!restore) {
    return (
      <Alert>
        <AlertDescription>That restore does not exist.</AlertDescription>
      </Alert>
    );
  }
  const staff = user.role === "ADMIN" || user.role === "MAINTAINER";

  return (
    <div className="flex flex-col gap-8">
      <Overview restore={restore} />
      <PhaseStrip status={restore.status} />
      {isActive(restore.status) && staff ? <CancelButton restoreId={restore.id} /> : null}
      {!isActive(restore.status) ? <Outcome restore={restore} lines={lines} /> : null}
      <LogViewer
        restoreId={restore.id}
        logLines={restore.logLines}
        dropped={restore.logLinesDropped}
        status={restore.status}
        lines={lines}
        onLines={setLines}
      />
    </div>
  );
}

function Overview({ restore: r }: { restore: RestoreFieldsFragment }) {
  return (
    <dl className="grid gap-px overflow-hidden rounded-md border bg-rule sm:grid-cols-2 lg:grid-cols-4">
      <Cell label="Status">
        <RestoreStatusBadge status={r.status} />
      </Cell>
      <Cell label="Backup">
        <Link to={`/jobs/${r.job.id}`} className="font-medium hover:underline">
          {r.job.name}
        </Link>
        <span className="font-mono text-xs text-muted-foreground">
          {r.kind === "GLOBALS" ? "cluster globals" : r.sourceDatabase}
          {" · "}
          <Link to={`/runs/${r.runId}`} className="hover:underline">
            run
          </Link>
        </span>
      </Cell>
      <Cell label={r.mode === "OVERWRITE" ? "Replaces" : r.kind === "GLOBALS" ? "Into" : "Creates"}>
        <span className="font-mono">{r.targetDatabase ?? r.target.name}</span>
        <span className="text-xs text-muted-foreground">
          on {r.target.name} ({ENGINE_LABELS[r.target.engine] ?? r.target.engine})
          {r.target.temporary ? ", temporary" : ""}
          {r.target.deleted ? ", deleted" : ""}
        </span>
      </Cell>
      <Cell label="Timing">
        <time dateTime={r.createdAt}>{formatDateTime(r.createdAt)}</time>
        <span className="text-xs text-muted-foreground" data-numeric>
          {r.durationSeconds != null ? formatDuration(r.durationSeconds) : "not finished"}
          {r.stagedBytes > 0 ? ` · ${formatBytes(r.stagedBytes)} staged` : ""}
          {r.requestedBy ? ` · by ${r.requestedBy.displayName}` : ""}
        </span>
      </Cell>
    </dl>
  );
}

function Cell({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1 bg-surface px-4 py-3">
      <dt className="eyebrow">{label}</dt>
      <dd className="flex min-w-0 flex-col gap-0.5 text-sm">{children}</dd>
    </div>
  );
}

/** Queued → staging → verifying → restoring, then the final state. */
function PhaseStrip({ status }: { status: RestoreStatus }) {
  const finished = !isActive(status);
  const current = PHASES.indexOf(status);
  return (
    <ol aria-label="Restore phases" className="grid grid-cols-5 gap-1">
      {[...PHASES, "DONE" as const].map((phase, i) => {
        const isFinal = phase === "DONE";
        const done = finished || i < current;
        const now = !finished && i === current;
        const finalLabel = finished ? STATUS_LABELS[status] : "Done";
        return (
          <li key={phase} aria-current={now ? "step" : undefined} className="flex flex-col gap-1.5">
            <span
              aria-hidden="true"
              className={cn(
                "h-1.5 rounded-full bg-muted",
                done && !isFinal && "bg-primary",
                now && "running-pulse bg-status-running",
                isFinal && status === "SUCCEEDED" && "bg-status-success",
                isFinal && status === "FAILED" && "bg-status-failed",
                isFinal && status === "CANCELED" && "bg-status-neutral",
              )}
            />
            <span
              className={cn(
                "font-mono text-2xs tracking-[0.08em] uppercase",
                now || (isFinal && finished) ? "text-foreground" : "text-muted-foreground",
              )}
            >
              {isFinal ? finalLabel : STATUS_LABELS[phase]}
              <span className="sr-only">{done ? " (done)" : now ? " (current)" : ""}</span>
            </span>
          </li>
        );
      })}
    </ol>
  );
}

function CancelButton({ restoreId }: { restoreId: string }) {
  const client = useApolloClient();
  const [open, setOpen] = useState(false);
  return (
    <div>
      <Button
        variant="outline"
        onClick={() => {
          setOpen(true);
        }}
      >
        <Ban aria-hidden="true" />
        Cancel restore
      </Button>
      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title="Cancel this restore?"
        description="A restore that has not started is canceled at once; a running one stops as soon as its worker notices. A PostgreSQL overwrite leaves the target unchanged until the swap."
        confirmLabel="Cancel restore"
        destructive
        onConfirm={async () => {
          try {
            await client.mutate({ mutation: RestoreCancelMutation, variables: { id: restoreId } });
            toast.success("Cancellation requested.");
          } catch (err) {
            toast.error(userMessage(err));
            throw err;
          }
        }}
      />
    </div>
  );
}

function Outcome({ restore: r, lines }: { restore: RestoreFieldsFragment; lines: LogLine[] }) {
  const neutralized = neutralizedFunctions(lines);
  const hasSecdef = r.warnings.some((w) => w.code === "secdef_neutralized");
  return (
    <section aria-label="Result" className="flex flex-col gap-4">
      {r.status === "SUCCEEDED" ? (
        <div className="flex items-start gap-3 rounded-md border border-status-success/40 bg-status-success-soft p-4">
          <CheckCircle2 aria-hidden="true" className="mt-0.5 size-5 text-status-success-fg" />
          <div className="flex flex-col gap-1">
            <h3 className="font-display text-2xl leading-tight">Restore complete</h3>
            <p className="text-sm">
              {r.kind === "GLOBALS"
                ? `The roles and tablespaces were applied to ${r.target.name}.`
                : `${r.targetDatabase ?? ""} on ${r.target.name} holds the restored backup.`}
            </p>
            {r.kind === "DATABASE" && r.target.engine === "POSTGRES" ? (
              <p className="text-xs text-muted-foreground">
                PUBLIC has no CONNECT on the restored database: grant CONNECT to the roles your
                applications use.
              </p>
            ) : null}
          </div>
        </div>
      ) : r.status === "FAILED" ? (
        <Alert variant="destructive">
          <OctagonX aria-hidden="true" />
          <AlertTitle>The restore failed</AlertTitle>
          <AlertDescription>
            <p>{r.errorMessage ?? "No details were recorded."}</p>
            {r.errorCode ? <p className="font-mono text-xs">{r.errorCode}</p> : null}
          </AlertDescription>
        </Alert>
      ) : (
        <Alert>
          <Ban aria-hidden="true" />
          <AlertTitle>The restore was canceled</AlertTitle>
          <AlertDescription>{r.errorMessage ?? "It stopped before it finished."}</AlertDescription>
        </Alert>
      )}

      {r.warnings.length > 0 ? (
        <ul className="flex flex-col gap-2">
          {r.warnings.map((w) => (
            <li
              key={w.code}
              className="flex items-start gap-3 rounded-sm border border-l-4 border-l-status-partial bg-status-partial-soft px-4 py-3 text-sm"
            >
              <TriangleAlert aria-hidden="true" className="mt-0.5 size-4 text-status-partial-fg" />
              <div className="flex flex-col gap-0.5">
                <span className="font-mono text-2xs text-muted-foreground">{w.code}</span>
                <p>{w.message}</p>
              </div>
            </li>
          ))}
        </ul>
      ) : null}

      {hasSecdef || neutralized.functions.length > 0 ? (
        <section
          aria-labelledby="restore-secdef"
          className="flex flex-col gap-2 rounded-md border bg-surface p-4"
        >
          <h3 id="restore-secdef" className="flex items-center gap-2 font-semibold">
            <Lock aria-hidden="true" className="size-4 text-primary" />
            Switched to SECURITY INVOKER
          </h3>
          <p className="text-sm text-muted-foreground">
            These routines would have run with the restore role's rights for every caller. Re-enable
            SECURITY DEFINER only after reviewing each one.
          </p>
          {neutralized.functions.length > 0 ? (
            <ul className="flex flex-col gap-1 font-mono text-xs">
              {neutralized.functions.map((f) => (
                <li key={f} className="rounded-xs bg-surface-sunken px-2 py-1 break-all">
                  {f}
                </li>
              ))}
              {neutralized.more > 0 ? (
                <li className="px-2 text-muted-foreground">... and {neutralized.more} more</li>
              ) : null}
            </ul>
          ) : (
            <p className="text-xs text-muted-foreground">The list is in the restore log below.</p>
          )}
        </section>
      ) : null}
    </section>
  );
}

const LEVEL_CLASS: Record<string, string> = {
  INFO: "text-muted-foreground",
  WARN: "text-status-partial-fg",
  ERROR: "text-status-failed-fg",
};

/**
 * The restore log, fetched in pages after the last line whenever the
 * restore reports more lines (restoreUpdated carries the count).
 */
function LogViewer({
  restoreId,
  logLines,
  dropped,
  status,
  lines,
  onLines,
}: {
  restoreId: string;
  logLines: number;
  dropped: number;
  status: RestoreStatus;
  lines: LogLine[];
  onLines: (update: (prev: LogLine[]) => LogLine[]) => void;
}) {
  const client = useApolloClient();
  const endId = useRef<string | null>(null);
  const fetched = useRef(0);
  const wanted = useRef(0);
  const fetching = useRef(false);
  const again = useRef(false);
  const mounted = useRef(true);
  const [error, setError] = useState<string>();

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  useEffect(() => {
    wanted.current = logLines;
    // A running loop picks the change up before it stops.
    if (fetching.current) {
      again.current = true;
      return;
    }
    fetching.current = true;
    void (async () => {
      try {
        for (;;) {
          const r = await client.query({
            query: RestoreLogsQuery,
            variables: { restoreId, afterId: endId.current },
            fetchPolicy: "no-cache",
          });
          const p = r.data?.restoreLogs;
          if (!p || !mounted.current) break;
          if (p.lines.length > 0) {
            fetched.current += p.lines.length;
            onLines((prev) => [...prev, ...p.lines]);
          }
          endId.current = p.endId ?? endId.current;
          if (p.hasMore) continue;
          if (again.current) {
            again.current = false;
            continue;
          }
          if (p.lines.length === 0 || fetched.current >= wanted.current) break;
        }
      } catch (err) {
        if (mounted.current) setError(userMessage(err));
      } finally {
        fetching.current = false;
      }
    })();
  }, [client, restoreId, logLines, status, onLines]);

  return (
    <section aria-labelledby="restore-log" className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        <ScrollText aria-hidden="true" className="size-4 text-muted-foreground" />
        <h3 id="restore-log" className="font-semibold">
          Restore log
        </h3>
        <span className="font-mono text-xs text-muted-foreground">
          {lines.length} line{lines.length === 1 ? "" : "s"}
          {dropped > 0 ? `, ${dropped} dropped (log cap)` : ""}
        </span>
      </div>
      {error ? (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}
      <div
        role="log"
        aria-live="polite"
        aria-label="Restore log lines"
        // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- the scrollable log must be reachable by keyboard (WCAG 2.1.1)
        tabIndex={0}
        className="max-h-96 overflow-auto rounded-md border bg-surface-sunken px-3 py-2 font-mono text-xs leading-relaxed"
      >
        {lines.length === 0 ? (
          <p className="text-muted-foreground">No log lines yet.</p>
        ) : (
          <ol>
            {lines.map((l) => (
              <li key={l.id} className="grid grid-cols-[auto_auto_1fr] gap-3">
                <time dateTime={l.time} className="text-muted-foreground">
                  {new Date(l.time).toLocaleTimeString()}
                </time>
                <span className={cn("w-10", LEVEL_CLASS[l.level])}>{l.level}</span>
                <span className="break-all whitespace-pre-wrap">{l.line}</span>
              </li>
            ))}
          </ol>
        )}
      </div>
    </section>
  );
}
