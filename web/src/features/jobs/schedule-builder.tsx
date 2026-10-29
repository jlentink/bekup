import { skipToken, useQuery } from "@apollo/client/react";
import { CalendarClock, Hand, Repeat, SquareAsterisk, type LucideIcon } from "lucide-react";
import { useEffect, useId, useMemo, useState } from "react";

import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import type { ScheduleKind } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

import { Field, NativeSelect } from "./fields";
import { PreviewScheduleQuery } from "./operations";
import {
  draftProblem,
  draftToInput,
  formatInZone,
  SCHEDULE_UNITS,
  timezones,
  WEEKDAYS,
  type ScheduleDraft,
} from "./schedule";

const T = {
  legend: "Schedule",
  kinds: {
    MANUAL: { label: "Manual", hint: "Only when started by hand" },
    INTERVAL: { label: "Interval", hint: "Every N units from a start" },
    CRON: { label: "Cron", hint: "A 5-field expression" },
  } satisfies Record<ScheduleKind, { label: string; hint: string }>,
  every: "Every",
  unit: "Unit",
  units: { MINUTES: "minutes", HOURS: "hours", DAYS: "days", WEEKS: "weeks", MONTHS: "months" },
  anchorDate: "Starting on",
  anchorTime: "At",
  anchorHint: "Local time in the schedule's zone; occurrences count from here.",
  weekday: "On weekday",
  weekdayAuto: "Same as the start date",
  dayOfMonth: "On day of month",
  dayOfMonthHint: "Shorter months use their last day. Empty: the start date's day.",
  cron: "Cron expression",
  cronHint: "minute hour day-of-month month day-of-week, e.g. 15 2 * * 1-5",
  timezone: "Time zone",
  timezoneHint: "IANA name, e.g. Europe/Amsterdam. Daylight saving is handled.",
  preview: "Next 5 runs",
  manualPreview: "No planned runs: this job only runs when someone starts it.",
  checking: "Checking the schedule",
} as const;

const KIND_ICONS: Record<ScheduleKind, LucideIcon> = {
  MANUAL: Hand,
  INTERVAL: Repeat,
  CRON: SquareAsterisk,
};

/** Waits until value stopped changing for ms. */
function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => {
      setV(value);
    }, ms);
    return () => {
      clearTimeout(t);
    };
  }, [value, ms]);
  return v;
}

export interface ScheduleBuilderProps {
  value: ScheduleDraft;
  onChange: (next: ScheduleDraft) => void;
  /** A server VALIDATION message about the schedule (from save). */
  error?: string | undefined;
  /** Reports whether the server accepted the current draft (preview loaded without error). */
  onValidity?: (problem: string | null) => void;
}

/**
 * The schedule builder (D-143): manual, interval (every N units from an
 * anchor in a zone, optional weekday / day of month) or cron, with a live
 * preview of the next five occurrences and the server's description.
 */
export function ScheduleBuilder({ value, onChange, error, onValidity }: ScheduleBuilderProps) {
  const groupId = useId();
  const zones = useMemo(() => timezones(), []);
  const set = (patch: Partial<ScheduleDraft>) => {
    onChange({ ...value, ...patch });
  };

  const localProblem = draftProblem(value);
  const input = useMemo(() => draftToInput(value), [value]);
  const debounced = useDebounced(JSON.stringify(input), 300);
  const skip = localProblem !== null || value.kind === "MANUAL";
  const preview = useQuery(
    PreviewScheduleQuery,
    skip
      ? skipToken
      : {
          variables: { input: JSON.parse(debounced) as typeof input, count: 5 },
          fetchPolicy: "cache-first",
        },
  );
  const settling = debounced !== JSON.stringify(input);
  const serverError = preview.error ? userMessage(preview.error) : null;
  const problem = localProblem ?? (skip ? null : serverError);

  useEffect(() => {
    onValidity?.(problem);
  }, [problem, onValidity]);

  return (
    <fieldset className="flex flex-col gap-5" aria-describedby={`${groupId}-preview`}>
      <legend className="sr-only">{T.legend}</legend>
      <div role="radiogroup" aria-label={T.legend} className="grid gap-2 sm:grid-cols-3">
        {(Object.keys(T.kinds) as ScheduleKind[]).map((kind) => {
          const Icon = KIND_ICONS[kind];
          const checked = value.kind === kind;
          return (
            <label
              key={kind}
              className={cn(
                "group relative flex cursor-pointer items-start gap-3 rounded-md border bg-surface px-3.5 py-3 transition-colors",
                "has-[:focus-visible]:ring-[3px] has-[:focus-visible]:ring-ring/50",
                checked
                  ? "border-primary/60 bg-accent text-accent-foreground"
                  : "hover:border-border-strong",
              )}
            >
              <input
                type="radio"
                name={`${groupId}-kind`}
                value={kind}
                checked={checked}
                onChange={() => {
                  set({ kind });
                }}
                className="sr-only"
              />
              <Icon
                aria-hidden="true"
                className={cn("mt-0.5 size-4", checked ? "text-primary" : "text-muted-foreground")}
              />
              <span className="flex flex-col">
                <span className="text-sm font-medium">{T.kinds[kind].label}</span>
                <span className="text-xs text-muted-foreground">{T.kinds[kind].hint}</span>
              </span>
            </label>
          );
        })}
      </div>

      {value.kind === "INTERVAL" ? (
        <div className="grid gap-4 sm:grid-cols-[6rem_minmax(0,10rem)_minmax(0,1fr)_7rem]">
          <Field label={T.every}>
            {({ id, describedBy }) => (
              <Input
                id={id}
                aria-describedby={describedBy}
                inputMode="numeric"
                value={value.every}
                onChange={(e) => {
                  set({ every: e.target.value });
                }}
                className="font-mono"
              />
            )}
          </Field>
          <Field label={T.unit}>
            {({ id }) => (
              <NativeSelect
                id={id}
                value={value.unit}
                onChange={(e) => {
                  set({ unit: e.target.value as ScheduleDraft["unit"] });
                }}
              >
                {SCHEDULE_UNITS.map((u) => (
                  <option key={u} value={u}>
                    {T.units[u]}
                  </option>
                ))}
              </NativeSelect>
            )}
          </Field>
          <Field label={T.anchorDate}>
            {({ id }) => (
              <Input
                id={id}
                type="date"
                value={value.anchorDate}
                onChange={(e) => {
                  set({ anchorDate: e.target.value });
                }}
                className="font-mono"
              />
            )}
          </Field>
          <Field label={T.anchorTime}>
            {({ id }) => (
              <Input
                id={id}
                type="time"
                value={value.anchorTime}
                onChange={(e) => {
                  set({ anchorTime: e.target.value });
                }}
                className="font-mono"
              />
            )}
          </Field>
          <p className="-mt-2 text-xs text-muted-foreground sm:col-span-4">{T.anchorHint}</p>
          {value.unit === "WEEKS" ? (
            <Field label={T.weekday} className="sm:col-span-2">
              {({ id }) => (
                <NativeSelect
                  id={id}
                  value={value.weekday}
                  onChange={(e) => {
                    set({ weekday: e.target.value });
                  }}
                >
                  <option value="">{T.weekdayAuto}</option>
                  {WEEKDAYS.map((d) => (
                    <option key={d} value={d}>
                      {d.charAt(0).toUpperCase() + d.slice(1)}
                    </option>
                  ))}
                </NativeSelect>
              )}
            </Field>
          ) : null}
          {value.unit === "MONTHS" ? (
            <Field label={T.dayOfMonth} hint={T.dayOfMonthHint} className="sm:col-span-2">
              {({ id, describedBy }) => (
                <Input
                  id={id}
                  aria-describedby={describedBy}
                  inputMode="numeric"
                  value={value.dayOfMonth}
                  onChange={(e) => {
                    set({ dayOfMonth: e.target.value });
                  }}
                  className="font-mono"
                />
              )}
            </Field>
          ) : null}
        </div>
      ) : null}

      {value.kind === "CRON" ? (
        <Field label={T.cron} hint={T.cronHint}>
          {({ id, describedBy }) => (
            <Input
              id={id}
              aria-describedby={describedBy}
              value={value.cron}
              spellCheck={false}
              autoComplete="off"
              onChange={(e) => {
                set({ cron: e.target.value });
              }}
              className="font-mono tracking-wide"
            />
          )}
        </Field>
      ) : null}

      {value.kind !== "MANUAL" ? (
        <Field label={T.timezone} hint={T.timezoneHint} className="sm:max-w-sm">
          {({ id, describedBy }) => (
            <>
              <Input
                id={id}
                aria-describedby={describedBy}
                list={`${groupId}-zones`}
                value={value.timezone}
                spellCheck={false}
                autoComplete="off"
                onChange={(e) => {
                  set({ timezone: e.target.value });
                }}
                className="font-mono"
              />
              <datalist id={`${groupId}-zones`}>
                {zones.map((z) => (
                  <option key={z} value={z} />
                ))}
              </datalist>
            </>
          )}
        </Field>
      ) : null}

      <SchedulePreviewPanel
        id={`${groupId}-preview`}
        manual={value.kind === "MANUAL"}
        problem={error ?? problem}
        loading={!skip && (settling || preview.loading)}
        description={skip ? null : (preview.data?.previewSchedule.description ?? null)}
        next={skip ? [] : (preview.data?.previewSchedule.next ?? [])}
        zone={preview.data?.previewSchedule.schedule.timezone ?? value.timezone}
      />
    </fieldset>
  );
}

function SchedulePreviewPanel({
  id,
  manual,
  problem,
  loading,
  description,
  next,
  zone,
}: {
  id: string;
  manual: boolean;
  problem: string | null;
  loading: boolean;
  description: string | null;
  next: readonly string[];
  zone: string;
}) {
  return (
    <section
      id={id}
      aria-label={T.preview}
      aria-live="polite"
      aria-busy={loading}
      className="rounded-md border border-dashed border-border-strong/60 bg-surface-sunken px-4 py-3.5"
    >
      <div className="flex items-center gap-2">
        <CalendarClock aria-hidden="true" className="size-3.5 text-muted-foreground" />
        <p className="eyebrow">{T.preview}</p>
      </div>
      {manual ? (
        <p className="mt-2 text-sm text-muted-foreground">{T.manualPreview}</p>
      ) : problem ? (
        <p role="alert" className="mt-2 text-sm text-destructive">
          {problem}
        </p>
      ) : loading && next.length === 0 ? (
        <div className="mt-3 flex flex-col gap-2">
          <span className="sr-only">{T.checking}</span>
          <Skeleton className="h-4 w-56" />
          <Skeleton className="h-4 w-40" />
          <Skeleton className="h-4 w-40" />
        </div>
      ) : (
        <>
          {description ? (
            <p
              className="mt-2 font-display text-lg leading-snug"
              data-testid="schedule-description"
            >
              {description}
            </p>
          ) : null}
          <ol className={cn("mt-2 flex flex-col", loading && "opacity-60")}>
            {next.map((iso, i) => (
              <li
                key={iso}
                className="flex items-baseline gap-3 border-b border-rule py-1 font-mono text-xs last:border-b-0"
              >
                <span aria-hidden="true" className="w-4 text-right text-muted-foreground">
                  {i + 1}
                </span>
                <time dateTime={iso} className="text-foreground">
                  {formatInZone(iso, zone)}
                </time>
                <span className="ml-auto text-muted-foreground">{formatRelative(iso)}</span>
              </li>
            ))}
          </ol>
        </>
      )}
    </section>
  );
}
