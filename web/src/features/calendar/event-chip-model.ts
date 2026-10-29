import {
  AlarmClock,
  CircleCheck,
  CircleDashed,
  Clock,
  Contrast,
  FastForward,
  Hourglass,
  LoaderCircle,
  OctagonX,
  Pause,
  SkipForward,
  Ban,
  type LucideIcon,
} from "lucide-react";

import { formatTime, kindLabel, type CalEvent, type Variant } from "./model";

/** Static class strings per variant (literal so Tailwind sees them). */
export const VARIANT_META: Record<Variant, { icon: LucideIcon; chip: string }> = {
  planned: { icon: Clock, chip: "border border-primary/55 bg-surface text-foreground" },
  paused: {
    icon: Pause,
    chip: "cal-hatch-paused border border-dashed border-status-neutral/60 text-muted-foreground",
  },
  early: {
    icon: FastForward,
    chip: "border border-primary/55 bg-surface text-foreground shadow-[inset_3px_0_0_var(--primary)]",
  },
  overdue: {
    icon: AlarmClock,
    chip: "border border-status-missed/60 bg-status-missed-soft font-medium text-status-missed-fg",
  },
  skipped: {
    icon: SkipForward,
    chip: "border border-transparent text-muted-foreground line-through decoration-1",
  },
  "run-success": {
    icon: CircleCheck,
    chip: "border-l-[3px] border-status-success bg-status-success-soft text-status-success-fg",
  },
  "run-partial": {
    icon: Contrast,
    chip: "border-l-[3px] border-status-partial bg-status-partial-soft text-status-partial-fg",
  },
  "run-failed": {
    icon: OctagonX,
    chip: "border-l-[3px] border-status-failed bg-status-failed-soft text-status-failed-fg",
  },
  "run-running": {
    icon: LoaderCircle,
    chip: "border-l-[3px] border-status-running bg-status-running-soft text-status-running-fg",
  },
  "run-queued": {
    icon: Clock,
    chip: "border-l-[3px] border-status-neutral bg-status-neutral-soft text-status-neutral-fg",
  },
  "run-neutral": {
    icon: Ban,
    chip: "border-l-[3px] border-status-neutral bg-status-neutral-soft text-status-neutral-fg",
  },
  "expiry-exact": {
    icon: Hourglass,
    chip: "border border-status-neutral/45 bg-status-neutral-soft text-status-neutral-fg",
  },
  "expiry-projected": {
    icon: CircleDashed,
    chip: "border border-dotted border-status-neutral/70 text-muted-foreground italic",
  },
};

/** The accessible name of an event: kind/state, job, time, detail. */
export function eventLabel(e: CalEvent, tz: string): string {
  const parts = [kindLabel(e), e.jobName, formatTime(e.start, tz)];
  if (e.detail) parts.push(e.detail);
  return parts.join(", ");
}
