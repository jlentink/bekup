import { useMutation, useQuery } from "@apollo/client/react";
import { ArrowLeft, FlaskConical, Loader2, Play } from "lucide-react";
import type { ReactNode } from "react";
import { Link, useParams } from "react-router";
import { toast } from "sonner";

import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatDuration } from "@/lib/format";
import { userMessage } from "@/lib/api-error";
import { formatDateTime, formatRelative } from "@/lib/format";

import {
  JobDetailQuery,
  LatestTestRestoreQuery,
  RunTestRestoreNowMutation,
  TestRestoreDetailQuery,
  TestRestoresQuery,
} from "./operations";
import { Pager } from "./pager";
import { useCursorPager } from "./use-cursor-pager";
import { useRoleFlags } from "./roles";
import { DiffSummaryLine, TestRestoreDatabases } from "./test-restore-findings";
import { diffSummary } from "./test-restore-model";
import { TestRestoreVerdict } from "./test-restore-verdict";

const PAGE_SIZE = 20;

const T = {
  eyebrow: "Test restores",
  title: (job: string) => `Test restores of ${job}`,
  titleFallback: "Test restores",
  lead: "Each test restore copies the newest verified backup into a scratch database, compares it with the backup's source statistics and drops it again.",
  back: "Back to the job",
  history: "All test restores",
  empty: "No test restore has run for this job yet.",
  emptyHint:
    "Designate a scratch target in the job settings, then run one now or wait for the schedule.",
  runNow: "Run test restore now",
  queued: "Test restore queued.",
  cols: {
    verdict: "Verdict",
    when: "When",
    trigger: "Trigger",
    target: "Scratch target",
    databases: "Databases",
    duration: "Duration",
  },
  trigger: { SCHEDULE: "Scheduled", MANUAL: "Manual" },
  counts: (p: number, w: number, f: number, total: number) =>
    `${String(p)} passed · ${String(w)} warned · ${String(f)} failed of ${String(total)}`,
  open: "Details",
  notFound: "This test restore does not exist or you cannot see it.",
  facts: {
    run: "Tested run",
    target: "Scratch target",
    trigger: "Trigger",
    requestedBy: "Requested by",
    started: "Started",
    finished: "Finished",
    duration: "Duration",
  },
  openRun: "Open the run",
  noRun: "No backup qualified",
  deleted: "deleted",
  diff: "Differences",
  databases: "Databases",
  noDatabases: "No database was restored.",
} as const;

/** "Run test restore now" (staff, needs a scratch target; audited server-side). */
export function RunTestRestoreNowButton({ jobId }: { jobId: string }) {
  const [run, { loading }] = useMutation(RunTestRestoreNowMutation, {
    refetchQueries: [TestRestoresQuery, LatestTestRestoreQuery],
  });
  return (
    <Button
      variant="outline"
      size="sm"
      disabled={loading}
      onClick={() => {
        run({ variables: { jobId } }).then(
          () => toast.success(T.queued),
          (err: unknown) => toast.error(userMessage(err)),
        );
      }}
    >
      {loading ? (
        <Loader2 aria-hidden="true" className="animate-spin" />
      ) : (
        <Play aria-hidden="true" />
      )}
      {T.runNow}
    </Button>
  );
}

/** /jobs/:jobId/test-restores: the history of a job's test restores. */
export function TestRestoreHistoryPage() {
  const { jobId = "" } = useParams();
  const flags = useRoleFlags();
  const job = useQuery(JobDetailQuery, { variables: { id: jobId } });
  const pager = useCursorPager(jobId);
  const { data, loading, error, refetch } = useQuery(TestRestoresQuery, {
    variables: { jobId, first: PAGE_SIZE, after: pager.after },
    fetchPolicy: "cache-and-network",
  });
  const rows = data?.testRestores.edges.map((e) => e.node) ?? [];
  const name = job.data?.job?.name;
  const canRun = flags.staff && job.data?.job?.testRestore.target != null;

  return (
    <div className="flex flex-col gap-6">
      <Link
        to={`/jobs/${jobId}`}
        className="inline-flex items-center gap-1.5 self-start text-xs text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft aria-hidden="true" className="size-3.5" /> {T.back}
      </Link>
      <PageHeader
        eyebrow={T.eyebrow}
        title={name ? T.title(name) : T.titleFallback}
        description={T.lead}
        actions={canRun ? <RunTestRestoreNowButton jobId={jobId} /> : null}
      />
      {error && !data ? (
        <Alert variant="destructive">
          <AlertDescription className="flex flex-wrap items-center gap-3">
            {userMessage(error)}
            <Button variant="outline" size="sm" onClick={() => void refetch()}>
              Try again
            </Button>
          </AlertDescription>
        </Alert>
      ) : loading && !data ? (
        <Skeleton className="h-48 w-full" />
      ) : rows.length === 0 ? (
        <EmptyState icon={FlaskConical} title={T.empty}>
          {T.emptyHint}
        </EmptyState>
      ) : (
        <div className="rounded-lg border bg-card">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{T.cols.verdict}</TableHead>
                <TableHead>{T.cols.when}</TableHead>
                <TableHead>{T.cols.trigger}</TableHead>
                <TableHead>{T.cols.target}</TableHead>
                <TableHead>{T.cols.databases}</TableHead>
                <TableHead className="text-right">{T.cols.duration}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((tr) => (
                <TableRow key={tr.id}>
                  <TableCell className="py-3">
                    <Link
                      to={`/jobs/${jobId}/test-restores/${tr.id}`}
                      aria-label={`${T.open}: ${formatDateTime(tr.createdAt)}`}
                      className="inline-flex rounded-sm focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none"
                    >
                      <TestRestoreVerdict status={tr.status} />
                    </Link>
                  </TableCell>
                  <TableCell>
                    <time
                      dateTime={tr.finishedAt ?? tr.createdAt}
                      title={formatDateTime(tr.finishedAt ?? tr.createdAt)}
                      className="font-mono text-xs"
                    >
                      {formatRelative(tr.finishedAt ?? tr.createdAt)}
                    </time>
                  </TableCell>
                  <TableCell className="text-sm">
                    {T.trigger[tr.trigger]}
                    {tr.requestedBy ? (
                      <span className="block text-xs text-muted-foreground">
                        {tr.requestedBy.displayName}
                      </span>
                    ) : null}
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    {tr.target.name}
                    {tr.target.deleted ? ` (${T.deleted})` : ""}
                  </TableCell>
                  <TableCell className="text-xs">
                    {T.counts(tr.passed, tr.warned, tr.failed, tr.databaseCount)}
                    {tr.errorMessage ? (
                      <span className="block text-status-failed-fg">{tr.errorMessage}</span>
                    ) : null}
                  </TableCell>
                  <TableCell className="text-right font-mono text-xs">
                    {formatDuration(tr.durationSeconds)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      <Pager
        label={T.history}
        page={pager.page}
        hasNext={data?.testRestores.pageInfo.hasNextPage ?? false}
        onPrev={pager.prev}
        onNext={() => {
          pager.next(data?.testRestores.pageInfo.endCursor);
        }}
      />
    </div>
  );
}

function Fact({ term, children }: { term: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1 border-t border-rule pt-3">
      <dt className="eyebrow">{term}</dt>
      <dd className="text-sm">{children}</dd>
    </div>
  );
}

/** /jobs/:jobId/test-restores/:testRestoreId: one test restore with its diff. */
export function TestRestoreDetailPage() {
  const { jobId = "", testRestoreId = "" } = useParams();
  const { data, loading, error } = useQuery(TestRestoreDetailQuery, {
    variables: { id: testRestoreId },
    fetchPolicy: "cache-and-network",
  });
  const tr = data?.testRestore;
  const back = (
    <Link
      to={`/jobs/${jobId}/test-restores`}
      className="inline-flex items-center gap-1.5 self-start text-xs text-muted-foreground hover:text-foreground"
    >
      <ArrowLeft aria-hidden="true" className="size-3.5" /> {T.history}
    </Link>
  );
  if (loading && !data) {
    return (
      <div className="flex flex-col gap-4">
        {back}
        <Skeleton className="h-12 w-80" />
        <Skeleton className="h-48 w-full" />
      </div>
    );
  }
  if (!tr) {
    return (
      <div className="flex flex-col gap-4">
        {back}
        <Alert variant={error ? "destructive" : "default"}>
          <AlertDescription>{error ? userMessage(error) : T.notFound}</AlertDescription>
        </Alert>
      </div>
    );
  }
  const summary = diffSummary(tr.databases);
  return (
    <div className="flex flex-col gap-6">
      {back}
      <PageHeader
        eyebrow={T.eyebrow}
        title={`${tr.job.name} · ${formatDateTime(tr.createdAt)}`}
        actions={<TestRestoreVerdict status={tr.status} />}
      />
      {tr.errorMessage ? (
        <Alert variant="destructive">
          <AlertDescription>
            {tr.errorMessage}
            {tr.errorCode ? <span className="ml-2 font-mono text-2xs">{tr.errorCode}</span> : null}
          </AlertDescription>
        </Alert>
      ) : null}
      <dl className="grid gap-x-8 gap-y-4 sm:grid-cols-2 lg:grid-cols-4">
        <Fact term={T.facts.run}>
          {tr.runId ? (
            <Link
              to={`/runs/${tr.runId}`}
              className="text-primary underline-offset-4 hover:underline"
            >
              {T.openRun}
            </Link>
          ) : (
            <span className="text-muted-foreground">{T.noRun}</span>
          )}
        </Fact>
        <Fact term={T.facts.target}>
          <span className="font-mono text-xs">
            {tr.target.name}
            {tr.target.deleted ? ` (${T.deleted})` : ""}
          </span>
        </Fact>
        <Fact term={T.facts.trigger}>
          {T.trigger[tr.trigger]}
          {tr.requestedBy ? (
            <span className="block text-xs text-muted-foreground">
              {T.facts.requestedBy}: {tr.requestedBy.displayName}
            </span>
          ) : null}
        </Fact>
        <Fact term={T.facts.duration}>
          <span className="font-mono text-xs">
            {formatDuration(tr.durationSeconds) || "—"}
            {tr.finishedAt ? (
              <span className="block text-muted-foreground">
                {T.facts.finished} {formatDateTime(tr.finishedAt)}
              </span>
            ) : null}
          </span>
        </Fact>
      </dl>
      <section
        aria-labelledby="tr-diff"
        className="flex flex-col gap-3 rounded-lg border bg-card px-5 py-4"
      >
        <h2 id="tr-diff" className="text-[0.9375rem] font-semibold">
          {T.diff}
          <span className="ml-3 font-mono text-xs font-normal text-muted-foreground">
            {T.counts(tr.passed, tr.warned, tr.failed, tr.databaseCount)}
          </span>
        </h2>
        <DiffSummaryLine summary={summary} />
        <h3 className="eyebrow pt-2">{T.databases}</h3>
        {tr.databases.length === 0 ? (
          <p className="text-sm text-muted-foreground">{T.noDatabases}</p>
        ) : (
          <TestRestoreDatabases databases={tr.databases} openLimit={25} />
        )}
      </section>
    </div>
  );
}
