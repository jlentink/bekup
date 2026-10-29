import { skipToken, useQuery } from "@apollo/client/react";
import { FileCheck2, FlaskConical, ShieldCheck } from "lucide-react";
import { Link } from "react-router";

import { Skeleton } from "@/components/ui/skeleton";
import { TestRestoreDatabases } from "@/features/jobs/test-restore-findings";
import { TestRestoreVerdict } from "@/features/jobs/test-restore-verdict";
import type {
  DatabaseEngine,
  JobVerification,
  RunDetailQuery as RunDetailResult,
} from "@/graphql/gen/graphql";
import { formatDateTime } from "@/lib/format";

import { codeTitle } from "./codes";
import { RunItemStatsSection } from "./item-stats";
import { RunJobContextQuery, RunTestRestoresQuery, TestRestoreFindingsQuery } from "./operations";
import { isActive } from "./run-status-model";
import { groupIssues } from "./completeness-model";

type Run = NonNullable<RunDetailResult["run"]>;

const T = {
  title: "Completeness evidence",
  lead: "Whether this backup is complete, not only intact (D-141).",
  counts: "Row counts",
  exactness: {
    pgDeep: "Exact: counted per table inside the dump's own snapshot.",
    pgStandard:
      "Estimates (planner statistics); object counts are exact. Deep verification makes PostgreSQL counts exact.",
    mysql:
      "Estimates: MySQL and MariaDB row counts are always approximate (information_schema); object counts are exact.",
    unknown: "Unknown (the job is no longer visible).",
  },
  level: { STANDARD: "standard", DEEP: "deep" },
  verified: "Verified databases",
  manifest: "Signed manifest",
  manifestCovers: (c: number, t: number) => `covers ${String(c)} of ${String(t)}`,
  unsigned: "not signed",
  key: (id: string) => `key ${id}`,
  issues: "Completeness warnings",
  noIssues: "No completeness warnings: every object was readable and counted.",
  objects: (n: number) => `${String(n)} object${n === 1 ? "" : "s"}`,
  testRestore: "Test restore of this run",
  noTestRestore: "No test restore has checked this run.",
  openTestRestore: "Open the test restore",
} as const;

function exactness(engine: DatabaseEngine | undefined, level: JobVerification | undefined): string {
  if (!engine || !level) return T.exactness.unknown;
  if (engine === "POSTGRES") return level === "DEEP" ? T.exactness.pgDeep : T.exactness.pgStandard;
  return T.exactness.mysql;
}

/**
 * The per-run completeness evidence (task 6.6, D-141/D-151/D-153): the
 * exactness of the counts for the job's engine and level, verification
 * and manifest coverage, completeness warnings, the per-item statistics
 * (RunItem.stats, D-187) and the verdict and findings of a test restore
 * of this run.
 */
export function CompletenessPanel({ run }: { run: Run }) {
  const ctx = useQuery(
    RunJobContextQuery,
    run.job.deleted ? skipToken : { variables: { id: run.job.id } },
  );
  const engine = ctx.data?.job?.connection.engine;
  const level = ctx.data?.job?.verification;
  const issues = groupIssues(run);
  const verified = run.items.filter((i) => i.verified).length;

  return (
    <section
      aria-labelledby="run-evidence"
      className="flex flex-col gap-5 rounded-lg border bg-card px-5 py-5"
    >
      <div className="flex flex-col gap-1">
        <h2 id="run-evidence" className="flex items-center gap-2 text-[0.9375rem] font-semibold">
          <ShieldCheck aria-hidden="true" className="size-4 text-muted-foreground" /> {T.title}
        </h2>
        <p className="text-sm text-muted-foreground">{T.lead}</p>
      </div>

      <dl className="grid gap-x-8 gap-y-4 sm:grid-cols-3">
        <div className="flex flex-col gap-1 border-t border-rule pt-3">
          <dt className="eyebrow">
            {T.counts}
            {level ? ` · ${T.level[level]}` : ""}
          </dt>
          <dd className="text-sm" data-testid="evidence-exactness">
            {ctx.loading ? <Skeleton className="h-4 w-48" /> : exactness(engine, level)}
          </dd>
        </div>
        <div className="flex flex-col gap-1 border-t border-rule pt-3">
          <dt className="eyebrow">{T.verified}</dt>
          <dd className="font-mono text-sm">
            {verified} / {run.items.length}
          </dd>
        </div>
        <div className="flex flex-col gap-1 border-t border-rule pt-3">
          <dt className="eyebrow">{T.manifest}</dt>
          <dd className="text-sm">
            {run.manifest.signed ? (
              <span className="inline-flex items-center gap-1.5">
                <FileCheck2 aria-hidden="true" className="size-3.5 text-status-success" />
                {T.manifestCovers(run.manifest.coveredItems, run.manifest.totalItems)}
              </span>
            ) : (
              <span className="text-status-partial-fg">{T.unsigned}</span>
            )}
            {run.manifest.signingKeyId ? (
              <span className="block font-mono text-2xs text-muted-foreground">
                {T.key(run.manifest.signingKeyId)}
              </span>
            ) : null}
          </dd>
        </div>
      </dl>

      <div className="flex flex-col gap-2">
        <h3 className="eyebrow">{T.issues}</h3>
        {issues.length === 0 ? (
          <p className="text-sm text-muted-foreground">{T.noIssues}</p>
        ) : (
          <ul className="flex flex-col gap-2">
            {issues.map((issue) => (
              <li
                key={issue.code}
                data-kind="warning"
                className="rounded-md border border-l-4 border-status-partial/40 border-l-status-partial bg-status-partial-soft/50 px-3 py-2"
              >
                <p className="text-sm font-medium text-status-partial-fg">
                  {codeTitle(issue.code)}
                  <span className="ml-2 font-mono text-2xs font-normal text-muted-foreground">
                    {issue.code}
                  </span>
                </p>
                <ul className="mt-1 flex flex-col gap-0.5 text-xs">
                  {issue.entries.map((e, i) => (
                    <li key={i}>
                      {e.database ? <span className="mr-1.5 font-mono">{e.database}:</span> : null}
                      {e.message}
                    </li>
                  ))}
                </ul>
              </li>
            ))}
          </ul>
        )}
      </div>

      <RunItemStatsSection runId={run.id} active={isActive(run.status)} />

      <RunTestRestore run={run} />
    </section>
  );
}

function RunTestRestore({ run }: { run: Run }) {
  const list = useQuery(RunTestRestoresQuery, { variables: { jobId: run.job.id } });
  const match = list.data?.testRestores.edges.map((e) => e.node).find((t) => t.runId === run.id);
  const detail = useQuery(
    TestRestoreFindingsQuery,
    match ? { variables: { id: match.id } } : skipToken,
  );
  const tr = detail.data?.testRestore;
  return (
    <div className="flex flex-col gap-2">
      <h3 className="eyebrow flex items-center gap-1.5">
        <FlaskConical aria-hidden="true" className="size-3.5" /> {T.testRestore}
      </h3>
      {list.loading ? (
        <Skeleton className="h-5 w-56" />
      ) : !match ? (
        <p className="text-sm text-muted-foreground">{T.noTestRestore}</p>
      ) : (
        <div className="flex flex-col gap-3">
          <div className="flex flex-wrap items-center gap-3">
            <TestRestoreVerdict status={match.status} />
            <span className="font-mono text-xs text-muted-foreground">
              {formatDateTime(match.finishedAt)}
            </span>
            <Link
              to={`/jobs/${run.job.id}/test-restores/${match.id}`}
              className="text-xs text-primary underline-offset-4 hover:underline"
            >
              {T.openTestRestore}
            </Link>
          </div>
          {tr?.errorMessage ? (
            <p className="text-xs text-status-failed-fg">{tr.errorMessage}</p>
          ) : null}
          {tr ? <TestRestoreDatabases databases={tr.databases} /> : null}
        </div>
      )}
    </div>
  );
}
