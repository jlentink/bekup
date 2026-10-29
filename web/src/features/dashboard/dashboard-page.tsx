import "./dashboard.css";

import { skipToken, useQuery, useSubscription } from "@apollo/client/react";
import { useEffect, useMemo, useState } from "react";

import { PageHeader } from "@/components/page-header";
import { useSessionUser } from "@/features/auth/use-session-user";
import { browserZone } from "@/features/calendar/model";
import { roleFlags } from "@/features/jobs/roles";
import type {
  DashboardActivitySubscription as ActivityResult,
  DashboardRestoresQuery as RestoresResult,
} from "@/graphql/gen/graphql";
import { sharedStream } from "@/lib/apollo";
import { formatRelative } from "@/lib/format";

import { ActivityTile } from "./activity";
import { AlertsTile } from "./alerts";
import { AttentionTile } from "./attention";
import { GettingStarted, NoOwnedJobs } from "./getting-started";
import { JobHealthTile } from "./job-health";
import {
  ACTIVE_RESTORE,
  applyRunEvent,
  attentionItems,
  countHealth,
  DAY_MS,
  HOUR_MS,
  jobHealth,
  mergeRestores,
  newerRun,
  overdueJobIds,
  securityFindings,
  sortActive,
  sortHealth,
  storageByDestination,
  upcomingRuns,
  type LiveRun,
} from "./model";
import {
  DashboardAlertsQuery,
  DashboardCalendarQuery,
  DashboardInfraQuery,
  DashboardJobsQuery,
  DashboardActivitySubscription,
  DashboardRestoresQuery,
  DashboardRunsQuery,
  DashboardStorageQuery,
  DashboardSystemHealthQuery,
} from "./operations";
import { SecurityTile } from "./security";
import { StorageTile } from "./storage";
import { SystemHealthTile } from "./system-health";
import { UpcomingTile } from "./upcoming";

const JOBS_MAX = 200;

type RunEvent = NonNullable<ActivityResult["activityUpdated"]["run"]>;
type RestoreRow = RestoresResult["restores"]["edges"][number]["node"];

/** "now" for relative times, refreshed every 30 seconds. */
function useNow(intervalMs = 30_000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => {
      setNow(Date.now());
    }, intervalMs);
    return () => {
      clearInterval(id);
    };
  }, [intervalMs]);
  return now;
}

/**
 * The dashboard (task 6.2): a role-aware overview. Everyone sees job
 * health, live activity, recent failures and missed backups, the next 24
 * hours and their alerts (backup owners only their own jobs, as filtered by
 * the server); staff also see security findings and storage, admins the
 * system health. Two streams per page: runUpdated (D-149) and
 * restoresUpdated (D-196).
 */
export function DashboardPage() {
  const user = useSessionUser();
  const flags = roleFlags(user.role);
  const now = useNow();
  // Ranges are fixed at mount so polling and cache keys stay stable.
  const [range] = useState(() => {
    const t = Date.now();
    return {
      since: new Date(t - 7 * DAY_MS).toISOString(),
      now: new Date(t).toISOString(),
      until: new Date(t + 24 * HOUR_MS).toISOString(),
      zone: browserZone(),
    };
  });

  const jobsQ = useQuery(DashboardJobsQuery, {
    variables: { first: JOBS_MAX },
    fetchPolicy: "cache-and-network",
  });
  const runsQ = useQuery(DashboardRunsQuery, {
    variables: { since: range.since },
    fetchPolicy: "network-only",
  });
  const upcomingQ = useQuery(DashboardCalendarQuery, {
    variables: { from: range.now, to: range.until, timezone: range.zone },
    fetchPolicy: "network-only",
    pollInterval: 300_000,
  });
  // Restores: a snapshot at load, then the restoresUpdated stream.
  const restoresQ = useQuery(DashboardRestoresQuery, { fetchPolicy: "network-only" });
  const alertsQ = useQuery(DashboardAlertsQuery, {
    fetchPolicy: "network-only",
    pollInterval: 60_000,
  });
  const infraQ = useQuery(
    DashboardInfraQuery,
    flags.staff ? { fetchPolicy: "cache-and-network" } : skipToken,
  );
  const healthQ = useQuery(
    DashboardSystemHealthQuery,
    flags.admin ? { fetchPolicy: "network-only", pollInterval: 30_000 } : skipToken,
  );
  const storageQ = useQuery(
    DashboardStorageQuery,
    flags.staff ? { fetchPolicy: "cache-and-network", pollInterval: 300_000 } : skipToken,
  );

  // Live state from the page's one stream (D-197; shared across tabs):
  // every run and restore event since load, by id.
  const [liveRuns, setLiveRuns] = useState<ReadonlyMap<string, LiveRun>>(new Map());
  const [liveRestores, setLiveRestores] = useState<ReadonlyMap<string, RestoreRow>>(new Map());
  const sub = useSubscription(DashboardActivitySubscription, {
    context: sharedStream(`dashboard:${user.id}`),
    onData: ({ data }) => {
      const ev = data.data?.activityUpdated;
      const run: RunEvent | null | undefined = ev?.run;
      if (run) setLiveRuns((prev) => new Map(prev).set(run.id, run));
      const r = ev?.restore;
      if (r) setLiveRestores((prev) => new Map(prev).set(r.id, r));
    },
  });
  const liveByJob = useMemo(() => {
    const m = new Map<string, LiveRun>();
    for (const run of liveRuns.values()) {
      const prev = m.get(run.job.id);
      if (!prev || newerRun(prev, run) === run) m.set(run.job.id, run);
    }
    return m;
  }, [liveRuns]);

  const activeRuns = useMemo(() => {
    let m = new Map<string, LiveRun>();
    for (const e of [...(runsQ.data?.running.edges ?? []), ...(runsQ.data?.queued.edges ?? [])])
      m.set(e.node.id, e.node);
    // Events seen since load win over the loaded snapshot.
    for (const run of liveRuns.values()) m = applyRunEvent(m, run);
    return sortActive(m.values());
  }, [runsQ.data, liveRuns]);
  const activeRestores = useMemo(
    () =>
      mergeRestores(restoresQ.data?.restores.edges.map((e) => e.node) ?? [], liveRestores).filter(
        (r) => ACTIVE_RESTORE.has(r.status),
      ),
    [restoresQ.data, liveRestores],
  );

  const upcomingEvents = upcomingQ.data?.calendar.events;
  const jobNodes = useMemo(() => jobsQ.data?.jobs.edges.map((e) => e.node) ?? [], [jobsQ.data]);
  const health = useMemo(() => {
    const overdueJobs = overdueJobIds(upcomingEvents ?? []);
    return sortHealth(jobNodes.map((j) => jobHealth(j, { now, live: liveByJob, overdueJobs })));
  }, [jobNodes, upcomingEvents, liveByJob, now]);
  const counts = countHealth(health);

  const failures = useMemo(() => {
    const loaded = runsQ.data?.failed.edges.map((e) => e.node) ?? [];
    const latest = (r: LiveRun) => liveRuns.get(r.id) ?? r;
    const fresh = [...liveRuns.values()].filter((r) => r.status === "FAILED");
    return [...fresh, ...loaded.map(latest)].filter((r) => r.status === "FAILED");
  }, [runsQ.data, liveRuns]);
  const attention = attentionItems(failures, health, now);
  const upcoming = upcomingRuns(upcomingEvents ?? [], now);

  const connections = infraQ.data?.connections;
  const destinations = infraQ.data?.destinations;
  const findings = useMemo(
    () => securityFindings(jobNodes, connections ?? []),
    [jobNodes, connections],
  );
  const storageRows = useMemo(
    () => storageByDestination(storageQ.data?.destinationUsage ?? []),
    [storageQ.data],
  );

  const jobsLoaded = jobsQ.data !== undefined;
  const noJobs = jobsLoaded && jobNodes.length === 0;
  const freshInstall = flags.staff && noJobs && connections !== undefined;
  const attentionCount = counts.failing + counts.missed + counts.overdue;
  // The soonest planned run still ahead; overdue ones are counted under attention.
  const nextRun = jobNodes
    .filter((j) => j.enabled && j.nextRunAt && Date.parse(j.nextRunAt) >= now)
    .map((j) => j.nextRunAt ?? "")
    .sort()[0];

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        eyebrow={new Intl.DateTimeFormat(undefined, {
          weekday: "long",
          day: "numeric",
          month: "long",
        }).format(now)}
        title="Dashboard"
        description={
          flags.owner
            ? "The health of the backup jobs you own, live."
            : "Job health, live runs, failures, security and storage at a glance."
        }
      />

      {jobsLoaded && !noJobs ? (
        <dl
          aria-label="Summary"
          className="dash-figures grid grid-cols-2 rounded-lg border bg-card md:grid-cols-4"
        >
          <Figure label="Healthy jobs" testId="figure-healthy">
            {counts.healthy}
            <span className="text-muted-foreground"> / {health.length}</span>
          </Figure>
          <Figure label="Need attention" testId="figure-attention" alert={attentionCount > 0}>
            {attentionCount}
          </Figure>
          <Figure label="Running now" testId="figure-running">
            {activeRuns.length + activeRestores.length}
          </Figure>
          <Figure label="Next run" testId="figure-next">
            {nextRun ? formatRelative(nextRun, now) : "—"}
          </Figure>
        </dl>
      ) : null}

      {freshInstall ? (
        <GettingStarted
          admin={flags.admin}
          connections={connections.length}
          destinations={destinations?.length ?? 0}
        />
      ) : flags.owner && noJobs ? (
        <NoOwnedJobs />
      ) : (
        <div className="dash-stagger grid gap-6 xl:grid-cols-3">
          <div className="dash-stagger flex min-w-0 flex-col gap-6 xl:col-span-2">
            <JobHealthTile
              rows={health}
              counts={counts}
              loading={jobsQ.loading}
              error={jobsQ.error}
              more={jobsQ.data?.jobs.pageInfo.hasNextPage ?? false}
              owner={flags.owner}
              now={now}
            />
            <AttentionTile
              items={attention}
              loading={runsQ.loading || jobsQ.loading}
              error={runsQ.error ?? jobsQ.error}
              now={now}
            />
          </div>
          <div className="dash-stagger flex min-w-0 flex-col gap-6">
            <ActivityTile
              runs={activeRuns}
              restores={activeRestores}
              loading={runsQ.loading}
              error={runsQ.error}
              live={!sub.error}
              now={now}
            />
            <UpcomingTile
              runs={upcoming}
              loading={upcomingQ.loading}
              error={upcomingQ.error}
              now={now}
            />
            <AlertsTile
              unread={alertsQ.data?.unreadNotificationCount}
              items={alertsQ.data?.notifications.edges.map((e) => e.node) ?? []}
              loading={alertsQ.loading}
              error={alertsQ.error}
              now={now}
            />
          </div>
        </div>
      )}

      {flags.staff && !freshInstall ? (
        <div className="dash-stagger grid gap-6 xl:grid-cols-3">
          <SecurityTile
            className="xl:col-span-2"
            findings={findings}
            loading={infraQ.loading || jobsQ.loading}
            error={infraQ.error ?? jobsQ.error}
          />
          <StorageTile rows={storageRows} loading={storageQ.loading} error={storageQ.error} />
        </div>
      ) : null}

      {flags.admin ? (
        <SystemHealthTile
          health={healthQ.data?.systemHealth}
          loading={healthQ.loading}
          error={healthQ.error}
          now={now}
        />
      ) : null}
    </div>
  );
}

function Figure({
  label,
  children,
  alert = false,
  testId,
}: {
  label: string;
  children: React.ReactNode;
  alert?: boolean;
  testId: string;
}) {
  return (
    <div className="flex flex-col gap-1 px-5 py-4" data-testid={testId}>
      <dt className="eyebrow">{label}</dt>
      <dd
        className={
          "font-display text-4xl leading-none tracking-tight " +
          (alert ? "text-status-failed-fg" : "")
        }
      >
        {children}
      </dd>
    </div>
  );
}
