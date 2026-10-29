import { ArrowDownToLine, Copy, Loader2, Pause } from "lucide-react";
import { useLayoutEffect, useRef, useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import type { RunLogLevel } from "@/graphql/gen/graphql";
import { cn } from "@/lib/utils";

import { formatClock } from "@/lib/format";
import type { LogLine } from "./log-model";

/** Fixed row height of the virtualized list, in px (matches the row classes). */
export const ROW_HEIGHT = 20;
const VIEWPORT_ROWS = 22;
const OVERSCAN = 12;

const LEVEL_STYLE: Record<RunLogLevel, string> = {
  INFO: "text-muted-foreground",
  WARN: "text-status-partial-fg",
  ERROR: "text-status-failed-fg font-semibold",
};

const T = {
  title: "Log",
  follow: "Follow",
  following: "Following the tail",
  paused: "Paused",
  copy: "Copy log",
  copied: "Log copied to the clipboard.",
  copyFailed: "Copying failed.",
  empty: "No log lines yet.",
  loadMore: "Load more",
  live: "live",
  dropped: (n: number) => `${String(n)} line${n === 1 ? "" : "s"} dropped (log cap reached).`,
  count: (n: number) => `${n.toLocaleString()} line${n === 1 ? "" : "s"}`,
} as const;

/**
 * A virtualized log viewer: fixed-height rows, only the visible window is
 * rendered. "Follow" keeps the newest line in view; scrolling up pauses it.
 */
export function LogViewer({
  lines,
  live = false,
  loading = false,
  error,
  dropped = 0,
  hasMore = false,
  onLoadMore,
}: {
  lines: readonly LogLine[];
  live?: boolean;
  loading?: boolean;
  error?: string | null;
  dropped?: number;
  hasMore?: boolean;
  onLoadMore?: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [follow, setFollow] = useState(true);
  const [scrollTop, setScrollTop] = useState(0);
  const total = lines.length;
  const visible = VIEWPORT_ROWS + OVERSCAN * 2;
  const tailStart = Math.max(0, total - visible);
  const start = follow
    ? tailStart
    : Math.min(tailStart, Math.max(0, Math.floor(scrollTop / ROW_HEIGHT) - OVERSCAN));
  const end = Math.min(total, start + visible);
  const shown = lines.slice(start, end);

  // Keep the scroll position at the bottom while following.
  useLayoutEffect(() => {
    const el = ref.current;
    if (follow && el) el.scrollTop = total * ROW_HEIGHT;
  }, [follow, total]);

  const copy = () => {
    const text = lines
      .map((l) => `${l.time} ${l.level.padEnd(5)} ${l.stream.toLowerCase().padEnd(6)} ${l.line}`)
      .join("\n");
    navigator.clipboard.writeText(text).then(
      () => toast.success(T.copied),
      () => toast.error(T.copyFailed),
    );
  };

  return (
    <section
      aria-label={T.title}
      className="flex flex-col overflow-hidden rounded-lg border bg-card"
    >
      <div className="flex flex-wrap items-center gap-3 border-b px-4 py-2">
        <h3 className="eyebrow">{T.title}</h3>
        {live ? (
          <span className="inline-flex items-center gap-1.5 font-mono text-2xs tracking-[0.12em] text-status-running-fg uppercase">
            <span
              aria-hidden="true"
              className="running-pulse size-1.5 rounded-full bg-status-running"
            />
            {T.live}
          </span>
        ) : null}
        <span className="font-mono text-2xs text-muted-foreground" aria-live="off">
          {T.count(total)}
        </span>
        {loading ? (
          <Loader2 aria-hidden="true" className="size-3.5 animate-spin text-muted-foreground" />
        ) : null}
        <div className="ml-auto flex items-center gap-1.5">
          <Button
            variant={follow ? "secondary" : "ghost"}
            size="xs"
            aria-pressed={follow}
            onClick={() => {
              setFollow((f) => !f);
            }}
          >
            {follow ? <ArrowDownToLine aria-hidden="true" /> : <Pause aria-hidden="true" />}
            {T.follow}
          </Button>
          <Button variant="ghost" size="xs" onClick={copy} disabled={total === 0}>
            <Copy aria-hidden="true" /> {T.copy}
          </Button>
        </div>
      </div>
      {error ? (
        <p role="alert" className="px-4 py-2 text-xs text-destructive">
          {error}
        </p>
      ) : null}
      {dropped > 0 ? (
        <p className="border-b px-4 py-1.5 text-2xs text-status-partial-fg">{T.dropped(dropped)}</p>
      ) : null}
      <div
        ref={ref}
        role="log"
        aria-label={follow ? T.following : T.paused}
        aria-live={live && follow ? "polite" : "off"}
        // A scrollable region must be reachable by keyboard (WCAG 2.1.1).
        // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex
        tabIndex={0}
        data-follow={follow}
        onScroll={(e) => {
          const el = e.currentTarget;
          setScrollTop(el.scrollTop);
          const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < ROW_HEIGHT * 2;
          if (follow && !atBottom && el.scrollHeight > el.clientHeight) setFollow(false);
          else if (!follow && atBottom && el.scrollHeight > el.clientHeight) setFollow(true);
        }}
        className="h-[28rem] overflow-auto bg-surface-sunken font-mono text-xs leading-5"
      >
        {total === 0 && !loading ? (
          <p className="px-4 py-3 text-muted-foreground">{T.empty}</p>
        ) : (
          <div style={{ height: total * ROW_HEIGHT, position: "relative" }}>
            <ol
              style={{ transform: `translateY(${String(start * ROW_HEIGHT)}px)` }}
              className="absolute inset-x-0 top-0"
            >
              {shown.map((l, i) => (
                <li
                  key={l.id}
                  data-testid="log-line"
                  className={cn(
                    "flex h-5 items-baseline gap-3 px-4 whitespace-pre hover:bg-accent/40",
                    l.level === "ERROR" && "bg-status-failed-soft/50",
                  )}
                >
                  <span
                    aria-hidden="true"
                    className="w-12 shrink-0 text-right text-muted-foreground/70 select-none"
                  >
                    {start + i + 1}
                  </span>
                  <time dateTime={l.time} className="shrink-0 text-muted-foreground">
                    {formatClock(l.time)}
                  </time>
                  <span className={cn("w-10 shrink-0", LEVEL_STYLE[l.level])}>{l.level}</span>
                  <span className="w-12 shrink-0 text-muted-foreground">
                    {l.stream.toLowerCase()}
                  </span>
                  <span className="min-w-0">{l.line}</span>
                </li>
              ))}
            </ol>
          </div>
        )}
      </div>
      {hasMore && onLoadMore ? (
        <div className="border-t px-4 py-2">
          <Button variant="outline" size="xs" onClick={onLoadMore} disabled={loading}>
            {T.loadMore}
          </Button>
        </div>
      ) : null}
    </section>
  );
}
