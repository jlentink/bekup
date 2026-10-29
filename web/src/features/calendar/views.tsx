import { useEffect, useRef, type KeyboardEvent, type RefObject } from "react";

import { cn } from "@/lib/utils";

import { EventChip } from "./event-chip";
import {
  dayKey,
  dayLabel,
  hourOf,
  moveFocus,
  sameDay,
  type CalEvent,
  type CalendarView,
  type CivilDate,
} from "./model";

const WEEKDAYS = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"] as const;
const WEEKDAYS_LONG = [
  "Monday",
  "Tuesday",
  "Wednesday",
  "Thursday",
  "Friday",
  "Saturday",
  "Sunday",
] as const;

/** Events shown per month cell before "+N more". */
const MONTH_CELL_LIMIT = 3;

interface GridProps {
  view: CalendarView;
  days: CivilDate[];
  anchor: CivilDate;
  today: CivilDate;
  buckets: Map<string, CalEvent[]>;
  tz: string;
  labelledBy: string;
  /** Set by the page when the keyboard moved the focus: the grid focuses the new day. */
  keyboardMoveRef: RefObject<boolean>;
  onFocusDay: (c: CivilDate, viaKeyboard: boolean) => void;
  onOpenDay: (c: CivilDate) => void;
}

function useFocusFollow(
  ref: RefObject<HTMLElement | null>,
  key: string,
  keyboardMoveRef: RefObject<boolean>,
) {
  useEffect(() => {
    if (!keyboardMoveRef.current) return;
    const cell = ref.current?.querySelector<HTMLElement>(`[data-day="${key}"]`);
    if (cell) {
      cell.focus();
      keyboardMoveRef.current = false;
    }
  });
}

/**
 * Month and week grids (WAI-ARIA grid pattern): one tab stop (the focused
 * day), arrow keys move between days (beyond the view, the view follows),
 * Home/End to the week's ends, PageUp/PageDown by a page, Enter opens the
 * day. Events are links; only the focused day's events are in the tab order.
 */
export function DayGrid({
  view,
  days,
  anchor,
  today,
  buckets,
  tz,
  labelledBy,
  keyboardMoveRef,
  onFocusDay,
  onOpenDay,
}: GridProps) {
  const ref = useRef<HTMLDivElement>(null);
  useFocusFollow(ref, dayKey(anchor), keyboardMoveRef);
  const weeks: CivilDate[][] = [];
  for (let i = 0; i < days.length; i += 7) weeks.push(days.slice(i, i + 7));

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const next = moveFocus(view, anchor, e.key);
    if (next) {
      e.preventDefault();
      onFocusDay(next, true);
      return;
    }
    const onCell = (e.target as HTMLElement).getAttribute("role") === "gridcell";
    if (onCell && (e.key === "Enter" || e.key === " ")) {
      e.preventDefault();
      onOpenDay(anchor);
    }
  };

  return (
    <div
      ref={ref}
      role="grid"
      tabIndex={-1}
      aria-labelledby={labelledBy}
      className="flex min-w-[42rem] flex-col overflow-hidden rounded-md border bg-rule"
      onKeyDown={onKeyDown}
    >
      <div role="row" className="grid grid-cols-7 gap-px border-b border-rule">
        {WEEKDAYS.map((d, i) => (
          <div
            key={d}
            role="columnheader"
            aria-label={WEEKDAYS_LONG[i]}
            className="eyebrow bg-surface-sunken px-2 py-1.5"
          >
            {d}
          </div>
        ))}
      </div>
      {weeks.map((week) => (
        <div key={dayKey(week[0] ?? anchor)} role="row" className="grid flex-1 grid-cols-7 gap-px">
          {week.map((c) => {
            const key = dayKey(c);
            const events = buckets.get(key) ?? [];
            const focused = sameDay(c, anchor);
            const isToday = sameDay(c, today);
            const outside = view === "month" && c.m !== anchor.m;
            const shown = view === "month" ? events.slice(0, MONTH_CELL_LIMIT) : events;
            const hidden = events.length - shown.length;
            return (
              <div
                key={key}
                role="gridcell"
                data-day={key}
                tabIndex={focused ? 0 : -1}
                aria-selected={focused}
                aria-current={isToday ? "date" : undefined}
                className={cn(
                  "flex min-w-0 flex-col gap-1 bg-surface p-1.5 outline-none",
                  view === "month" ? "min-h-28" : "min-h-80",
                  outside && "bg-surface-sunken/70",
                  "focus-visible:relative focus-visible:z-10 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset",
                  focused && "bg-accent/40",
                )}
                onFocus={(e) => {
                  if (e.target === e.currentTarget && !focused) onFocusDay(c, false);
                }}
              >
                <span className="flex items-center justify-between gap-1">
                  <span
                    className={cn(
                      "inline-flex size-6 items-center justify-center rounded-full font-mono text-xs",
                      isToday && "bg-primary font-semibold text-primary-foreground",
                      !isToday && outside && "text-muted-foreground",
                    )}
                  >
                    <span aria-hidden="true">{c.d}</span>
                    <span className="sr-only">
                      {dayLabel(c)}
                      {isToday ? ", today" : ""}
                      {`, ${events.length === 0 ? "no events" : `${events.length} event${events.length === 1 ? "" : "s"}`}`}
                    </span>
                  </span>
                  {view === "week" ? (
                    <span aria-hidden="true" className="eyebrow">
                      {events.length > 0 ? events.length : ""}
                    </span>
                  ) : null}
                </span>
                <ul className="flex min-w-0 flex-col gap-0.5">
                  {shown.map((e, i) => (
                    <li
                      key={`${e.kind}-${e.runId ?? ""}-${e.jobId}-${e.start}-${e.destinationId ?? ""}-${String(i)}`}
                    >
                      <EventChip event={e} tz={tz} tabbable={focused} />
                    </li>
                  ))}
                </ul>
                {hidden > 0 ? (
                  <button
                    type="button"
                    tabIndex={focused ? 0 : -1}
                    className="self-start rounded-xs px-1 text-2xs font-medium text-primary hover:underline"
                    onClick={() => {
                      onOpenDay(c);
                    }}
                  >
                    +{hidden} more<span className="sr-only"> on {dayLabel(c)}</span>
                  </button>
                ) : null}
              </div>
            );
          })}
        </div>
      ))}
    </div>
  );
}

const HOURS = Array.from({ length: 24 }, (_, h) => h);

/**
 * One day as an hour ledger. It is a one-cell grid so the same keys
 * work: left/right move a day, PageUp/PageDown too.
 */
export function DayTimeline({
  anchor,
  today,
  buckets,
  tz,
  labelledBy,
  keyboardMoveRef,
  onFocusDay,
}: Omit<GridProps, "view" | "days" | "onOpenDay">) {
  const ref = useRef<HTMLDivElement>(null);
  useFocusFollow(ref, dayKey(anchor), keyboardMoveRef);
  const events = buckets.get(dayKey(anchor)) ?? [];
  const byHour = new Map<number, CalEvent[]>();
  for (const e of events) {
    const h = hourOf(e.start, tz);
    const list = byHour.get(h);
    if (list) list.push(e);
    else byHour.set(h, [e]);
  }
  const isToday = sameDay(anchor, today);

  return (
    <div
      ref={ref}
      role="grid"
      tabIndex={-1}
      aria-labelledby={labelledBy}
      className="rounded-md border bg-surface"
      onKeyDown={(e) => {
        const next = moveFocus("day", anchor, e.key);
        if (next) {
          e.preventDefault();
          onFocusDay(next, true);
        }
      }}
    >
      <div role="row">
        <div
          role="gridcell"
          data-day={dayKey(anchor)}
          tabIndex={0}
          aria-selected="true"
          aria-current={isToday ? "date" : undefined}
          className="outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset"
        >
          <span className="sr-only">
            {dayLabel(anchor)}
            {`, ${events.length === 0 ? "no events" : `${events.length} event${events.length === 1 ? "" : "s"}`}`}
          </span>
          <ol className="divide-y divide-rule">
            {HOURS.map((h) => {
              const list = byHour.get(h) ?? [];
              return (
                <li key={h} className="grid grid-cols-[4rem_minmax(0,1fr)] gap-3 px-3 py-1.5">
                  <span
                    aria-hidden={list.length === 0}
                    className={cn(
                      "pt-0.5 font-mono text-xs tabular-nums",
                      list.length === 0 ? "text-muted-foreground/60" : "text-muted-foreground",
                    )}
                  >
                    {String(h).padStart(2, "0")}:00
                  </span>
                  {list.length > 0 ? (
                    <ul className="grid gap-1 sm:grid-cols-2 xl:grid-cols-3">
                      {list.map((e, i) => (
                        <li
                          key={`${e.kind}-${e.runId ?? ""}-${e.jobId}-${e.start}-${e.destinationId ?? ""}-${String(i)}`}
                        >
                          <EventChip event={e} tz={tz} className="py-1 text-xs" />
                          {e.detail ? (
                            <span className="block truncate px-1.5 text-2xs text-muted-foreground">
                              {e.detail}
                            </span>
                          ) : null}
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <span aria-hidden="true" className="min-h-4" />
                  )}
                </li>
              );
            })}
          </ol>
        </div>
      </div>
    </div>
  );
}
