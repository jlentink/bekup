import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

/** Tones of a Tag; the status-* tokens of the design system (theme.css). */
export type Tone = "success" | "warning" | "danger" | "neutral" | "info" | "insecure" | "primary";

const TONE: Record<Tone, { pill: string; dot: string }> = {
  success: {
    pill: "border-status-success/40 bg-status-success-soft text-status-success-fg",
    dot: "bg-status-success",
  },
  warning: {
    pill: "border-status-partial/45 bg-status-partial-soft text-status-partial-fg",
    dot: "bg-status-partial",
  },
  danger: {
    pill: "border-status-failed/45 bg-status-failed-soft text-status-failed-fg",
    dot: "bg-status-failed",
  },
  info: {
    pill: "border-status-running/40 bg-status-running-soft text-status-running-fg",
    dot: "bg-status-running",
  },
  insecure: {
    pill: "hatch-insecure border-status-insecure/60 text-status-insecure-fg",
    dot: "bg-status-insecure",
  },
  primary: {
    pill: "border-primary/30 bg-accent text-foreground",
    dot: "bg-primary",
  },
  neutral: {
    pill: "border-border bg-surface-sunken text-muted-foreground",
    dot: "bg-status-neutral",
  },
};

/**
 * A compact state label used across the app: a dot plus words (colour is
 * never the only cue). `compact` fits dense rows such as picker options.
 */
export function Tag({
  tone,
  children,
  title,
  compact = false,
  className,
}: {
  tone: Tone;
  children: ReactNode;
  title?: string | undefined;
  compact?: boolean;
  className?: string | undefined;
}) {
  const t = TONE[tone];
  return (
    <span
      data-tone={tone}
      title={title}
      className={cn(
        "inline-flex items-center rounded-sm border font-medium whitespace-nowrap",
        compact ? "h-5 gap-1 px-1.5 text-2xs" : "h-6 gap-1.5 px-2 text-xs",
        t.pill,
        className,
      )}
    >
      <span aria-hidden="true" className={cn("size-1.5 shrink-0 rounded-full", t.dot)} />
      {children}
    </span>
  );
}
