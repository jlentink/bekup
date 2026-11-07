import { CircleCheck, CircleSlash, OctagonX, TriangleAlert, type LucideIcon } from "lucide-react";
import type { ReactNode } from "react";

import type { DashboardSystemHealthQuery } from "@/graphql/gen/graphql";
import { formatDateTime, formatRelative } from "@/lib/format";
import { defineMessages } from "@/lib/i18n";
import { cn } from "@/lib/utils";

import { daysLeft, expiryLevel, queueSummary, tickState, type Level } from "./model";
import { Tile, TileError, TileSkeleton } from "./tile";

type Health = DashboardSystemHealthQuery["systemHealth"];

const LEVEL: Record<Level | "off", { icon: LucideIcon; text: string }> = {
  ok: { icon: CircleCheck, text: "text-status-success-fg" },
  warning: { icon: TriangleAlert, text: "text-status-partial-fg" },
  critical: { icon: OctagonX, text: "text-status-failed-fg" },
  off: { icon: CircleSlash, text: "text-muted-foreground" },
};

const T = defineMessages({
  en: {
    component: {
      runner_server: "Runner server",
      runner_client: "Runner client",
      runner_ca: "Runner CA",
      postgres_ca: "App DB CA",
      postgres_client: "App DB client",
    },
    title: "System",
    loading: "Loading system health",
    runner: "Runner",
    unreachable: "Unreachable",
    up: "Up",
    dockerUnavailable: "Docker unavailable",
    containers: " containers",
    cannotStart: "Backups and restores cannot start.",
    scheduler: "Scheduler",
    schedulerOff: "Off on this instance",
    noTick: "No tick yet",
    staleTick: "Tick is stale",
    ticking: "Ticking",
    lastTick: (rel: string) => `last tick ${rel}`,
    pins: "Pinned DB certificates",
    daysLeft: (n: number) => `${String(n)} days left`,
    nonePinned: "None pinned",
    earliest: (at: string) => `earliest ${at}`,
    oldest: "Oldest last success",
    noneYet: "None yet",
    acrossEnabled: "across enabled jobs",
    queues: "Work queues",
    dbUnreadable: "The app database could not be read.",
    noQueued: "No queued work.",
    queue: "Queue",
    waiting: "Waiting",
    running: "Running",
    retrying: "Retrying",
    certificates: "Certificates",
    noCerts: "No certificate files readable.",
    expired: "expired",
    days: (n: number) => `${String(n)} d`,
    snapshot: "Snapshot",
    cached: "(cached up to 15 seconds).",
  },
  nl: {
    component: {
      runner_server: "Runner-server",
      runner_client: "Runner-client",
      runner_ca: "Runner-CA",
      postgres_ca: "CA van de app-database",
      postgres_client: "Client van de app-database",
    },
    title: "Systeem",
    loading: "Systeemstatus laden",
    runner: "Runner",
    unreachable: "Onbereikbaar",
    up: "Actief",
    dockerUnavailable: "Docker niet beschikbaar",
    containers: " containers",
    cannotStart: "Back-ups en herstelacties kunnen niet starten.",
    scheduler: "Planner",
    schedulerOff: "Uit op deze instantie",
    noTick: "Nog geen tick",
    staleTick: "Tick is verouderd",
    ticking: "Actief",
    lastTick: (rel: string) => `laatste tick ${rel}`,
    pins: "Vastgepinde DB-certificaten",
    daysLeft: (n: number) => `nog ${String(n)} dagen`,
    nonePinned: "Geen vastgepind",
    earliest: (at: string) => `eerste ${at}`,
    oldest: "Oudste laatste succes",
    noneYet: "Nog geen",
    acrossEnabled: "over ingeschakelde taken",
    queues: "Werkwachtrijen",
    dbUnreadable: "De app-database kon niet worden gelezen.",
    noQueued: "Geen werk in de wachtrij.",
    queue: "Wachtrij",
    waiting: "Wachtend",
    running: "Bezig",
    retrying: "Opnieuw",
    certificates: "Certificaten",
    noCerts: "Geen leesbare certificaatbestanden.",
    expired: "verlopen",
    days: (n: number) => `${String(n)} d`,
    snapshot: "Momentopname",
    cached: "(tot 15 seconden in de cache).",
  },
});

const COMPONENT_LABEL: Record<string, string> = T.component;

function Fact({
  label,
  level,
  value,
  detail,
  testId,
}: {
  label: string;
  level: Level | "off";
  value: ReactNode;
  detail?: ReactNode;
  testId?: string;
}) {
  const meta = LEVEL[level];
  const Icon = meta.icon;
  return (
    <div className="flex flex-col gap-1" data-testid={testId} data-level={level}>
      <dt className="eyebrow">{label}</dt>
      <dd className={cn("flex items-center gap-1.5 font-medium", meta.text)}>
        <Icon aria-hidden="true" className="size-4 shrink-0" />
        {value}
      </dd>
      {detail ? <dd className="text-2xs text-muted-foreground">{detail}</dd> : null}
    </div>
  );
}

/** Runner, scheduler, queue and certificate health (admins; systemHealth, D-176). */
export function SystemHealthTile({
  health,
  loading,
  error,
  now,
}: {
  health: Health | undefined;
  loading: boolean;
  error: unknown;
  now: number;
}) {
  return (
    <Tile folio="08" title={T.title} testId="tile-system">
      {loading && !health ? (
        <TileSkeleton rows={3} label={T.loading} />
      ) : !health ? (
        <TileError error={error} />
      ) : (
        <SystemHealthBody health={health} now={now} />
      )}
    </Tile>
  );
}

function SystemHealthBody({ health, now }: { health: Health; now: number }) {
  const { runner } = health;
  const runnerLevel: Level = !runner.up ? "critical" : !runner.dockerOk ? "critical" : "ok";
  const tick = tickState(health.schedulerEnabled, health.lastSchedulerTick, now);
  const queues = health.queue ? queueSummary(health.queue) : null;
  const pinLevel = expiryLevel(health.earliestPinNotAfter, now);
  return (
    <div className="flex flex-col gap-5">
      <dl className="grid grid-cols-2 gap-x-6 gap-y-4 lg:grid-cols-4">
        <Fact
          label={T.runner}
          level={runnerLevel}
          testId="fact-runner"
          value={!runner.up ? T.unreachable : runner.dockerOk ? T.up : T.dockerUnavailable}
          detail={
            runner.up ? (
              <>
                {runner.version ? <span className="font-mono">{runner.version}</span> : null}
                {runner.activeRuns != null ? (
                  <>
                    {" · "}
                    {runner.activeRuns}
                    {runner.maxConcurrentRuns != null
                      ? ` / ${String(runner.maxConcurrentRuns)}`
                      : ""}
                    {T.containers}
                  </>
                ) : null}
              </>
            ) : (
              T.cannotStart
            )
          }
        />
        <Fact
          label={T.scheduler}
          testId="fact-scheduler"
          level={tick === "ok" ? "ok" : tick === "off" ? "off" : "warning"}
          value={
            tick === "off"
              ? T.schedulerOff
              : tick === "none"
                ? T.noTick
                : tick === "stale"
                  ? T.staleTick
                  : T.ticking
          }
          detail={
            health.lastSchedulerTick ? (
              <time
                dateTime={health.lastSchedulerTick}
                title={formatDateTime(health.lastSchedulerTick)}
              >
                {T.lastTick(formatRelative(health.lastSchedulerTick, now))}
              </time>
            ) : null
          }
        />
        <Fact
          label={T.pins}
          testId="fact-pins"
          level={health.earliestPinNotAfter ? pinLevel : "off"}
          value={
            health.earliestPinNotAfter
              ? T.daysLeft(daysLeft(health.earliestPinNotAfter, now))
              : T.nonePinned
          }
          detail={
            health.earliestPinNotAfter
              ? T.earliest(formatDateTime(health.earliestPinNotAfter))
              : null
          }
        />
        <Fact
          label={T.oldest}
          testId="fact-oldest"
          level={
            !health.oldestLastSuccess
              ? "off"
              : now - new Date(health.oldestLastSuccess).getTime() > 7 * 86_400_000
                ? "warning"
                : "ok"
          }
          value={
            health.oldestLastSuccess ? formatRelative(health.oldestLastSuccess, now) : T.noneYet
          }
          detail={T.acrossEnabled}
        />
      </dl>
      <div className="grid gap-5 md:grid-cols-2">
        <div className="flex flex-col gap-2">
          <h3 className="eyebrow">{T.queues}</h3>
          {queues === null ? (
            <p className="text-xs text-status-partial-fg">{T.dbUnreadable}</p>
          ) : queues.length === 0 ? (
            <p className="text-xs text-muted-foreground">{T.noQueued}</p>
          ) : (
            <table className="w-full text-xs">
              <thead>
                <tr className="text-left text-2xs text-muted-foreground">
                  <th scope="col" className="py-1 font-medium">
                    {T.queue}
                  </th>
                  <th scope="col" className="py-1 text-right font-medium">
                    {T.waiting}
                  </th>
                  <th scope="col" className="py-1 text-right font-medium">
                    {T.running}
                  </th>
                  <th scope="col" className="py-1 text-right font-medium">
                    {T.retrying}
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-rule">
                {queues.map((q) => (
                  <tr key={q.queue} data-testid="queue-row">
                    <th scope="row" className="py-1 text-left font-mono font-normal">
                      {q.queue}
                    </th>
                    <td className="py-1 text-right font-mono">{q.waiting}</td>
                    <td className="py-1 text-right font-mono">{q.running}</td>
                    <td
                      className={cn(
                        "py-1 text-right font-mono",
                        q.retrying > 0 && "text-status-partial-fg",
                      )}
                    >
                      {q.retrying}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
        <div className="flex flex-col gap-2">
          <h3 className="eyebrow">{T.certificates}</h3>
          {health.certificates.length === 0 ? (
            <p className="text-xs text-muted-foreground">{T.noCerts}</p>
          ) : (
            <ul className="flex flex-col divide-y divide-rule text-xs">
              {health.certificates.map((c) => {
                const level = expiryLevel(c.notAfter, now);
                const meta = LEVEL[level];
                const Icon = meta.icon;
                const days = daysLeft(c.notAfter, now);
                return (
                  <li
                    key={c.component}
                    data-testid="cert-row"
                    data-level={level}
                    className="flex items-center gap-2 py-1"
                  >
                    <Icon aria-hidden="true" className={cn("size-3.5 shrink-0", meta.text)} />
                    <span>{COMPONENT_LABEL[c.component] ?? c.component}</span>
                    <time
                      dateTime={c.notAfter}
                      title={formatDateTime(c.notAfter)}
                      className={cn("ml-auto font-mono", meta.text)}
                    >
                      {days < 0 ? T.expired : T.days(days)}
                    </time>
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      </div>
      <p className="text-2xs text-muted-foreground">
        {T.snapshot}{" "}
        <time dateTime={health.checkedAt}>{formatRelative(health.checkedAt, now)}</time> {T.cached}
      </p>
    </div>
  );
}
