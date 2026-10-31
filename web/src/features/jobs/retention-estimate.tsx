import { skipToken, useQuery } from "@apollo/client/react";
import { Archive } from "lucide-react";
import { useMemo } from "react";

import { Skeleton } from "@/components/ui/skeleton";
import type { JobRetentionInput, ScheduleInput } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { cn } from "@/lib/utils";

import { PreviewRetentionQuery } from "./operations";
import { ESTIMATE_T as T, estimateRows, retentionInput } from "./retention-model";
import { draftProblem, draftToInput, type ScheduleDraft } from "./schedule";
import { useDebounced } from "./use-debounced";

/**
 * The editor's live retention estimate (D-209): how many backups each
 * destination keeps with the current schedule and limits, computed by the
 * server (previewSchedule with `retention`), debounced like the schedule
 * preview and skipped while the draft is invalid.
 */
export function RetentionEstimatePanel({
  draft,
  keepLast,
  keepDays,
  scheduleProblem,
}: {
  draft: ScheduleDraft;
  keepLast: string;
  keepDays: string;
  /** The schedule builder's problem (local or server), if any. */
  scheduleProblem: string | null;
}) {
  const retention = useMemo(() => retentionInput(keepLast, keepDays), [keepLast, keepDays]);
  const scheduleInvalid = draftProblem(draft) !== null || scheduleProblem !== null;
  const vars = useMemo(
    () => (retention ? JSON.stringify({ input: draftToInput(draft), retention }) : null),
    [draft, retention],
  );
  const debounced = useDebounced(vars, 300);
  const skip = scheduleInvalid || vars === null || debounced === null;
  const q = useQuery(
    PreviewRetentionQuery,
    skip
      ? skipToken
      : {
          variables: JSON.parse(debounced) as {
            input: ScheduleInput;
            retention: JobRetentionInput;
          },
          fetchPolicy: "cache-first",
        },
  );
  const settling = debounced !== vars;
  const est = skip ? null : (q.data?.previewSchedule.retention ?? null);
  const loading = !skip && (settling || q.loading);
  const rows = est && retention ? estimateRows(est, retention) : [];

  let body;
  if (retention === null) {
    body = <p className="mt-2 text-sm text-muted-foreground">{T.invalid}</p>;
  } else if (scheduleInvalid) {
    body = <p className="mt-2 text-sm text-muted-foreground">{T.scheduleInvalid}</p>;
  } else if (q.error && !settling) {
    body = (
      <p role="alert" className="mt-2 text-sm text-destructive">
        {userMessage(q.error)}
      </p>
    );
  } else if (!est) {
    body = (
      <div className="mt-3 flex flex-col gap-2">
        <span className="sr-only">{T.checking}</span>
        <Skeleton className="h-6 w-72" />
        <Skeleton className="h-4 w-56" />
      </div>
    );
  } else {
    body = (
      <div className={cn("mt-2 flex flex-col gap-3", loading && "opacity-60")}>
        <div className="flex items-start gap-4">
          {est.kept != null ? (
            <p
              aria-hidden="true"
              className="flex shrink-0 flex-col items-center border-r border-rule pr-4 leading-none"
            >
              <span className="font-display text-4xl tabular-nums">
                <span className="mr-0.5 text-2xl text-muted-foreground">
                  {T.figurePrefix(est.atLeast)}
                </span>
                {est.kept}
              </span>
              <span className="mt-1 text-2xs tracking-wide text-muted-foreground uppercase">
                {T.figureLabel}
              </span>
            </p>
          ) : null}
          <p className="font-display text-lg leading-snug" data-testid="retention-estimate">
            {est.kept != null ? (
              T.kept(est.kept, est.atLeast)
            ) : (
              <>
                {T.unknown}
                {est.byKeepLast > 0 ? ` ${T.unknownFloor(est.byKeepLast)}` : ""}
              </>
            )}
          </p>
        </div>
        <dl aria-label={T.rulesLabel} className="flex flex-col">
          {rows.map((r) => (
            <div
              key={r.key}
              data-testid={`retention-rule-${r.key}`}
              className={cn(
                "grid grid-cols-[minmax(9rem,auto)_minmax(0,1fr)_4.5rem] items-center gap-3 border-b border-rule py-1 last:border-b-0",
                r.key === "total" && "font-medium",
              )}
            >
              <dt className={cn("text-xs", r.off && "text-muted-foreground")}>{r.label}</dt>
              <svg
                viewBox="0 0 100 4"
                preserveAspectRatio="none"
                className="h-1.5 w-full"
                aria-hidden="true"
              >
                <rect x={0} y={0} width={100} height={4} className="fill-surface" />
                {r.share > 0 ? (
                  <rect
                    x={0}
                    y={0}
                    width={Math.max(0.8, r.share * 100)}
                    height={4}
                    className={r.key === "total" ? "fill-primary" : "fill-muted-foreground/60"}
                  />
                ) : null}
              </svg>
              <dd className="text-right font-mono text-xs tabular-nums">
                {r.off ? (
                  <span className="text-muted-foreground">{T.ruleOff}</span>
                ) : r.count === null ? (
                  <span className="text-muted-foreground">{T.ruleUnknown}</span>
                ) : (
                  <>
                    {r.key === "total" && est.atLeast ? T.figurePrefix(true) : ""}
                    {r.count}
                  </>
                )}
              </dd>
            </div>
          ))}
        </dl>
        <p className="text-xs text-muted-foreground">{T.assumption}</p>
      </div>
    );
  }

  return (
    <section
      aria-label={T.eyebrow}
      aria-live="polite"
      aria-busy={loading}
      className="rounded-md border border-dashed border-border-strong/60 bg-surface-sunken px-4 py-3.5"
    >
      <div className="flex items-center gap-2">
        <Archive aria-hidden="true" className="size-3.5 text-muted-foreground" />
        <p className="eyebrow">{T.eyebrow}</p>
      </div>
      {body}
    </section>
  );
}
