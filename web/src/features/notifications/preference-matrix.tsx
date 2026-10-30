import { Lock, ShieldAlert } from "lucide-react";
import type { ReactNode } from "react";

import { Switch } from "@/components/ui/switch";
import type { NotificationChannelType } from "@/graphql/gen/graphql";
import { cn } from "@/lib/utils";

import { notificationsMessages as M } from "./messages";
import { CHANNEL_COLUMNS, cellOf, effectiveFor, overrideOf, type PreferenceRow } from "./model";

export type ToggleHandler = (
  row: PreferenceRow,
  channel: NotificationChannelType,
  enabled: boolean,
) => void;

function EventLabel({ row }: { row: PreferenceRow }) {
  const e = row.event;
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <span className="text-[0.8125rem] font-medium">{e.label}</span>
      <span className="flex flex-wrap items-center gap-1.5">
        <code className="font-mono text-2xs text-muted-foreground">{e.type}</code>
        {e.problem ? (
          <span className="rounded-xs border border-status-failed/30 px-1 font-mono text-[0.625rem] tracking-wider text-status-failed-fg uppercase">
            {M.prefs.problem}
          </span>
        ) : null}
        {e.priority === "LOW" ? (
          <span
            title={M.prefs.lowPriorityHint}
            className="rounded-xs border px-1 font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase"
          >
            {M.prefs.lowPriority}
          </span>
        ) : null}
      </span>
    </div>
  );
}

function HeaderRow({ first }: { first: ReactNode }) {
  return (
    <thead>
      <tr className="border-b border-rule">
        <th scope="col" className="py-2 pr-4 text-left">
          {first}
        </th>
        {CHANNEL_COLUMNS.map((c) => (
          <th key={c} scope="col" className="eyebrow w-20 px-2 py-2 text-center">
            {M.channels[c]}
          </th>
        ))}
      </tr>
    </thead>
  );
}

/**
 * One group of the event × channel matrix. Locked cells (mandatory
 * security channels) render as a checked, disabled switch with a lock.
 */
export function GroupMatrix({
  label,
  rows,
  security,
  busy,
  onToggle,
}: {
  label: string;
  rows: readonly PreferenceRow[];
  security: boolean;
  busy: ReadonlySet<string>;
  onToggle: ToggleHandler;
}) {
  return (
    <div className="flex flex-col gap-3" data-testid="pref-group">
      {security ? (
        <p
          role="note"
          className="flex gap-2.5 rounded-md border border-status-insecure/30 bg-status-insecure-soft px-3.5 py-2.5 text-sm text-status-insecure-fg"
        >
          <ShieldAlert aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
          <span>
            <span className="font-medium">{M.prefs.lockedTitle}. </span>
            {M.prefs.lockedNote}
          </span>
        </p>
      ) : null}
      <div className="overflow-x-auto">
        <table className="w-full min-w-[29rem] border-collapse text-sm">
          <caption className="sr-only">{label}</caption>
          <HeaderRow first={<span className="eyebrow">{M.prefs.eventColumn}</span>} />
          <tbody>
            {rows.map((row) => (
              <tr key={row.event.type} className="border-b border-rule/70 last:border-b-0">
                <th scope="row" className="py-2.5 pr-4 text-left font-normal">
                  <EventLabel row={row} />
                </th>
                {CHANNEL_COLUMNS.map((channel) => {
                  const cell = cellOf(row, channel);
                  const key = `${row.event.type}|${channel}|`;
                  const name = M.prefs.cell(row.event.label, M.channels[channel]);
                  return (
                    <td key={channel} className="px-2 py-2.5 text-center">
                      <span className="relative inline-flex items-center justify-center gap-1">
                        <Switch
                          aria-label={name}
                          checked={cell.enabled}
                          disabled={cell.locked || busy.has(key)}
                          data-source={cell.source}
                          onCheckedChange={(v) => {
                            onToggle(row, channel, v);
                          }}
                        />
                        {cell.locked ? (
                          <Lock
                            role="img"
                            aria-label={M.prefs.locked}
                            className="absolute -right-4 size-3 text-muted-foreground"
                          />
                        ) : cell.source === "OVERRIDE" ? (
                          <span
                            title={M.prefs.custom}
                            className="absolute -right-3 size-1.5 rounded-full bg-primary"
                          >
                            <span className="sr-only">{M.prefs.custom}</span>
                          </span>
                        ) : null}
                      </span>
                    </td>
                  );
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

/**
 * The override rows of one job: the effective value per cell, marked when
 * an override (rather than the per-event setting) decides it.
 */
export function JobOverrideMatrix({
  jobId,
  jobName,
  rows,
  busy,
  onToggle,
}: {
  jobId: string;
  jobName: string;
  rows: readonly PreferenceRow[];
  busy: ReadonlySet<string>;
  onToggle: ToggleHandler;
}) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[29rem] border-collapse text-sm" data-testid="override-matrix">
        <caption className="sr-only">{M.overrides.editing(jobName)}</caption>
        <HeaderRow first={<span className="eyebrow">{M.prefs.eventColumn}</span>} />
        <tbody>
          {rows.map((row) => (
            <tr key={row.event.type} className="border-b border-rule/70 last:border-b-0">
              <th scope="row" className="py-2.5 pr-4 text-left font-normal">
                <EventLabel row={row} />
              </th>
              {CHANNEL_COLUMNS.map((channel) => {
                const cell = cellOf(row, channel);
                const own = overrideOf(row, jobId, channel);
                const key = `${row.event.type}|${channel}|${jobId}`;
                return (
                  <td key={channel} className="px-2 py-2.5 text-center">
                    <span className="inline-flex flex-col items-center gap-1">
                      <Switch
                        aria-label={M.prefs.cellForJob(
                          row.event.label,
                          M.channels[channel],
                          jobName,
                        )}
                        checked={effectiveFor(row, channel, jobId)}
                        disabled={cell.locked || busy.has(key)}
                        data-override={own !== null}
                        onCheckedChange={(v) => {
                          onToggle(row, channel, v);
                        }}
                      />
                      <span
                        className={cn(
                          "font-mono text-[0.625rem] tracking-wide",
                          own !== null ? "text-primary" : "text-muted-foreground/70",
                        )}
                      >
                        {own !== null ? M.overrides.overridden : M.overrides.inherited}
                      </span>
                    </span>
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
