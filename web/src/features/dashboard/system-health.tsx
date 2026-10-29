import { CircleCheck, CircleSlash, OctagonX, TriangleAlert, type LucideIcon } from "lucide-react";
import type { ReactNode } from "react";

import type { DashboardSystemHealthQuery } from "@/graphql/gen/graphql";
import { formatDateTime, formatRelative } from "@/lib/format";
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

const COMPONENT_LABEL: Record<string, string> = {
  runner_server: "Runner server",
  runner_client: "Runner client",
  runner_ca: "Runner CA",
  postgres_ca: "App DB CA",
  postgres_client: "App DB client",
};

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
    <Tile folio="08" title="System" testId="tile-system">
      {loading && !health ? (
        <TileSkeleton rows={3} label="Loading system health" />
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
          label="Runner"
          level={runnerLevel}
          testId="fact-runner"
          value={!runner.up ? "Unreachable" : runner.dockerOk ? "Up" : "Docker unavailable"}
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
                      : ""}{" "}
                    containers
                  </>
                ) : null}
              </>
            ) : (
              "Backups and restores cannot start."
            )
          }
        />
        <Fact
          label="Scheduler"
          testId="fact-scheduler"
          level={tick === "ok" ? "ok" : tick === "off" ? "off" : "warning"}
          value={
            tick === "off"
              ? "Off on this instance"
              : tick === "none"
                ? "No tick yet"
                : tick === "stale"
                  ? "Tick is stale"
                  : "Ticking"
          }
          detail={
            health.lastSchedulerTick ? (
              <time
                dateTime={health.lastSchedulerTick}
                title={formatDateTime(health.lastSchedulerTick)}
              >
                last tick {formatRelative(health.lastSchedulerTick, now)}
              </time>
            ) : null
          }
        />
        <Fact
          label="Pinned DB certificates"
          testId="fact-pins"
          level={health.earliestPinNotAfter ? pinLevel : "off"}
          value={
            health.earliestPinNotAfter
              ? `${String(daysLeft(health.earliestPinNotAfter, now))} days left`
              : "None pinned"
          }
          detail={
            health.earliestPinNotAfter
              ? `earliest ${formatDateTime(health.earliestPinNotAfter)}`
              : null
          }
        />
        <Fact
          label="Oldest last success"
          testId="fact-oldest"
          level={
            !health.oldestLastSuccess
              ? "off"
              : now - new Date(health.oldestLastSuccess).getTime() > 7 * 86_400_000
                ? "warning"
                : "ok"
          }
          value={
            health.oldestLastSuccess ? formatRelative(health.oldestLastSuccess, now) : "None yet"
          }
          detail="across enabled jobs"
        />
      </dl>
      <div className="grid gap-5 md:grid-cols-2">
        <div className="flex flex-col gap-2">
          <h3 className="eyebrow">Work queues</h3>
          {queues === null ? (
            <p className="text-xs text-status-partial-fg">The app database could not be read.</p>
          ) : queues.length === 0 ? (
            <p className="text-xs text-muted-foreground">No queued work.</p>
          ) : (
            <table className="w-full text-xs">
              <thead>
                <tr className="text-left text-2xs text-muted-foreground">
                  <th scope="col" className="py-1 font-medium">
                    Queue
                  </th>
                  <th scope="col" className="py-1 text-right font-medium">
                    Waiting
                  </th>
                  <th scope="col" className="py-1 text-right font-medium">
                    Running
                  </th>
                  <th scope="col" className="py-1 text-right font-medium">
                    Retrying
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
          <h3 className="eyebrow">Certificates</h3>
          {health.certificates.length === 0 ? (
            <p className="text-xs text-muted-foreground">No certificate files readable.</p>
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
                      {days < 0 ? "expired" : `${String(days)} d`}
                    </time>
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      </div>
      <p className="text-2xs text-muted-foreground">
        Snapshot <time dateTime={health.checkedAt}>{formatRelative(health.checkedAt, now)}</time>{" "}
        (cached up to 15 seconds).
      </p>
    </div>
  );
}
