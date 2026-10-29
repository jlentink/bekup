import "./calendar.css";

import { skipToken, useQuery, useSubscription } from "@apollo/client/react";
import { ChevronLeft, ChevronRight, Globe, TriangleAlert } from "lucide-react";
import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router";

import { PageHeader } from "@/components/page-header";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { userMessage } from "@/lib/api-error";
import { cn } from "@/lib/utils";

import { Legend } from "./event-chip";
import {
  applyLiveRuns,
  browserZone,
  bucketByDay,
  civilOf,
  dayKey,
  eventVisible,
  jobIdsFor,
  parseDayKey,
  shiftAnchor,
  validZone,
  viewDays,
  viewRange,
  viewTitle,
  zoneOptions,
  type CalEvent,
  type CalendarView,
  type CivilDate,
  type Filters,
  type LiveRun,
} from "./model";
import { CalendarJobsQuery, CalendarQuery, CalendarRunUpdatedSubscription } from "./operations";
import { DayGrid, DayTimeline } from "./views";

const VIEWS: { id: CalendarView; label: string }[] = [
  { id: "month", label: "Month" },
  { id: "week", label: "Week" },
  { id: "day", label: "Day" },
];

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const uuid = (v: string | null) => (v && UUID_RE.test(v) ? v : undefined);

const selectClass =
  "h-9 w-full min-w-0 rounded-md border border-input bg-surface px-2.5 text-sm shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 dark:bg-surface-sunken";

/**
 * The job calendar (task 6.11, D-156/D-159): month, week and day views of
 * planned occurrences, runs, skips and expiries in a chosen zone. State
 * lives in the URL (view, date, tz, filters) so views can be linked.
 */
export function CalendarPage() {
  const [params, setParams] = useSearchParams();
  const titleId = useId();
  const tzId = useId();
  const jobSel = useId();
  const connSel = useId();
  const destSel = useId();

  const view: CalendarView = VIEWS.find((v) => v.id === params.get("view"))?.id ?? "month";
  const tz = validZone(params.get("tz")) ? (params.get("tz") ?? "UTC") : browserZone();
  const [now] = useState(() => new Date());
  const today = civilOf(now, tz);
  const anchor = parseDayKey(params.get("date")) ?? today;
  const filters: Filters = {
    jobId: uuid(params.get("job")),
    connectionId: uuid(params.get("connection")),
    destinationId: uuid(params.get("destination")),
  };
  const keyboardMoveRef = useRef(false);

  const setParam = useCallback(
    (changes: Record<string, string | undefined>) => {
      setParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          for (const [k, v] of Object.entries(changes)) {
            if (v === undefined || v === "") next.delete(k);
            else next.set(k, v);
          }
          return next;
        },
        { replace: true },
      );
    },
    [setParams],
  );

  const jobsQ = useQuery(CalendarJobsQuery, { variables: {} });
  const jobs = useMemo(() => jobsQ.data?.jobs.edges.map((e) => e.node) ?? [], [jobsQ.data]);
  const connections = useMemo(
    () =>
      [...new Map(jobs.map((j) => [j.connection.id, j.connection])).values()].sort((a, b) =>
        a.name.localeCompare(b.name),
      ),
    [jobs],
  );
  const destinations = useMemo(
    () =>
      [...new Map(jobs.flatMap((j) => j.destinations).map((d) => [d.id, d])).values()].sort(
        (a, b) => a.name.localeCompare(b.name),
      ),
    [jobs],
  );
  const filtering = Boolean(filters.jobId ?? filters.connectionId ?? filters.destinationId);
  const jobIds = filtering && !jobsQ.data ? undefined : jobIdsFor(jobs, filters);
  const { from, to } = viewRange(view, anchor, tz);

  const calQ = useQuery(
    CalendarQuery,
    jobIds === undefined || (jobIds !== null && jobIds.length === 0)
      ? skipToken
      : {
          variables: { from: from.toISOString(), to: to.toISOString(), timezone: tz, jobIds },
          fetchPolicy: "cache-and-network",
        },
  );
  const cal = calQ.data?.calendar ?? calQ.previousData?.calendar;

  // Live: one runUpdated stream for the page, filtered here.
  const [live, setLive] = useState<Map<string, LiveRun>>(() => new Map());
  const refetchTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const knownRuns = useMemo(
    () =>
      new Set((cal?.events ?? []).flatMap((e) => (e.kind === "RUN" && e.runId ? [e.runId] : []))),
    [cal],
  );
  const { refetch } = calQ;
  useSubscription(CalendarRunUpdatedSubscription, {
    onData: ({ data }) => {
      const run = data.data?.runUpdated;
      if (!run) return;
      if (jobIds && !jobIds.includes(run.job.id)) return;
      if (knownRuns.has(run.id)) {
        setLive((prev) => new Map(prev).set(run.id, run));
        return;
      }
      // A run the calendar does not show yet: reload once things settle.
      clearTimeout(refetchTimer.current);
      refetchTimer.current = setTimeout(() => {
        void refetch();
      }, 1500);
    },
  });
  useEffect(
    () => () => {
      clearTimeout(refetchTimer.current);
    },
    [],
  );

  // Cheap enough per render (at most 5,000 events); the compiler memoizes.
  const events = applyLiveRuns((cal?.events ?? []) as CalEvent[], live).filter((e) =>
    eventVisible(e, filters),
  );
  const buckets = bucketByDay(events, tz);
  const days = viewDays(view, anchor);

  const focusDay = (c: CivilDate, viaKeyboard: boolean) => {
    keyboardMoveRef.current = viaKeyboard;
    setParam({ date: dayKey(c) });
  };
  const openDay = (c: CivilDate) => {
    keyboardMoveRef.current = true;
    setParam({ view: "day", date: dayKey(c) });
  };
  const empty = jobIds?.length === 0;

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        eyebrow="Overview"
        title="Calendar"
        description="Planned backups, runs, skips and retention expiries. Times are shown in the zone you choose."
      />

      <div className="flex flex-col gap-4">
        <div className="flex flex-wrap items-center gap-3">
          <div
            role="group"
            aria-label="View"
            className="inline-flex rounded-md border bg-surface-sunken p-0.5"
          >
            {VIEWS.map((v) => (
              <button
                key={v.id}
                type="button"
                aria-pressed={view === v.id}
                className={cn(
                  "h-8 rounded-sm px-3 text-sm font-medium text-muted-foreground transition-colors hover:text-foreground",
                  view === v.id && "bg-surface text-foreground shadow-raised",
                )}
                onClick={() => {
                  setParam({ view: v.id });
                }}
              >
                {v.label}
              </button>
            ))}
          </div>
          <div className="flex items-center gap-1">
            <Button
              variant="outline"
              size="icon-sm"
              aria-label={`Previous ${view}`}
              onClick={() => {
                setParam({ date: dayKey(shiftAnchor(view, anchor, -1)) });
              }}
            >
              <ChevronLeft aria-hidden="true" />
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                setParam({ date: undefined });
              }}
            >
              Today
            </Button>
            <Button
              variant="outline"
              size="icon-sm"
              aria-label={`Next ${view}`}
              onClick={() => {
                setParam({ date: dayKey(shiftAnchor(view, anchor, 1)) });
              }}
            >
              <ChevronRight aria-hidden="true" />
            </Button>
          </div>
          <h2 id={titleId} aria-live="polite" className="font-display text-2xl leading-none">
            {viewTitle(view, anchor)}
          </h2>
        </div>

        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4" role="group" aria-label="Filters">
          <FilterSelect
            id={jobSel}
            label="Job"
            value={filters.jobId}
            any="All jobs"
            options={jobs.map((j) => ({ id: j.id, name: j.name }))}
            onChange={(v) => {
              setParam({ job: v });
            }}
          />
          <FilterSelect
            id={connSel}
            label="Connection"
            value={filters.connectionId}
            any="All connections"
            options={connections}
            onChange={(v) => {
              setParam({ connection: v });
            }}
          />
          <FilterSelect
            id={destSel}
            label="Destination"
            value={filters.destinationId}
            any="All destinations"
            options={destinations}
            onChange={(v) => {
              setParam({ destination: v });
            }}
          />
          <div className="grid gap-1.5">
            <label htmlFor={tzId} className="flex items-center gap-1.5 text-sm font-medium">
              <Globe aria-hidden="true" className="size-3.5 text-muted-foreground" />
              Time zone
            </label>
            <select
              id={tzId}
              className={selectClass}
              value={tz}
              onChange={(e) => {
                setParam({ tz: e.target.value === browserZone() ? undefined : e.target.value });
              }}
            >
              {zoneOptions(tz).map((z) => (
                <option key={z} value={z}>
                  {z === browserZone() ? `${z} (this browser)` : z}
                </option>
              ))}
            </select>
          </div>
        </div>
      </div>

      {cal?.truncated ? (
        <Alert>
          <TriangleAlert aria-hidden="true" />
          <AlertTitle>Some events are not shown</AlertTitle>
          <AlertDescription>
            This range hit the calendar's limits, so only its earliest events are listed. Narrow the
            view or filter by job, connection or destination.
          </AlertDescription>
        </Alert>
      ) : null}
      {calQ.error ? (
        <Alert variant="destructive">
          <AlertDescription>{userMessage(calQ.error)}</AlertDescription>
        </Alert>
      ) : null}
      {empty ? (
        <p className="text-sm text-muted-foreground">No job matches these filters.</p>
      ) : null}

      <div
        className={cn("overflow-x-auto transition-opacity", calQ.loading && !cal && "opacity-60")}
        aria-busy={calQ.loading}
      >
        {view === "day" ? (
          <DayTimeline
            anchor={anchor}
            today={today}
            buckets={buckets}
            tz={tz}
            labelledBy={titleId}
            keyboardMoveRef={keyboardMoveRef}
            onFocusDay={focusDay}
          />
        ) : (
          <DayGrid
            view={view}
            days={days}
            anchor={anchor}
            today={today}
            buckets={buckets}
            tz={tz}
            labelledBy={titleId}
            keyboardMoveRef={keyboardMoveRef}
            onFocusDay={focusDay}
            onOpenDay={openDay}
          />
        )}
      </div>

      <div className="flex flex-col gap-2 border-t border-rule pt-4">
        <p className="eyebrow">Legend</p>
        <Legend />
        <p className="text-xs text-muted-foreground">
          Projected expiries assume every planned run succeeds; exact ones follow keep-days or
          Object Lock. Arrow keys move between days (the view follows), Enter opens a day.
        </p>
      </div>
    </div>
  );
}

function FilterSelect({
  id,
  label,
  value,
  any,
  options,
  onChange,
}: {
  id: string;
  label: string;
  value: string | undefined;
  any: string;
  options: readonly { id: string; name: string }[];
  onChange: (v: string | undefined) => void;
}) {
  return (
    <div className="grid gap-1.5">
      <label htmlFor={id} className="text-sm font-medium">
        {label}
      </label>
      <select
        id={id}
        className={selectClass}
        value={value ?? ""}
        onChange={(e) => {
          onChange(e.target.value || undefined);
        }}
      >
        <option value="">{any}</option>
        {options.map((o) => (
          <option key={o.id} value={o.id}>
            {o.name}
          </option>
        ))}
      </select>
    </div>
  );
}
