import {
  CircleCheck,
  Clock,
  ClockAlert,
  Contrast,
  LoaderCircle,
  OctagonX,
  type LucideIcon,
} from "lucide-react";

/** Run / job states shown across the UI. */
export const RUN_STATUSES = [
  "success",
  "partial",
  "failed",
  "running",
  "missed",
  "queued",
] as const;
export type RunStatus = (typeof RUN_STATUSES)[number];

interface StatusMeta {
  label: string;
  /** Shape-coded glyph: status is never conveyed by color alone (WCAG 1.4.1). */
  icon: LucideIcon;
  /** Static Tailwind class strings (kept literal so Tailwind can see them). */
  badge: string;
  dot: string;
}

export const STATUS_META: Record<RunStatus, StatusMeta> = {
  success: {
    label: "Succeeded",
    icon: CircleCheck,
    badge: "bg-status-success-soft text-status-success-fg border-status-success/35",
    dot: "bg-status-success",
  },
  partial: {
    label: "Partial",
    icon: Contrast,
    badge: "bg-status-partial-soft text-status-partial-fg border-status-partial/40",
    dot: "bg-status-partial",
  },
  failed: {
    label: "Failed",
    icon: OctagonX,
    badge: "bg-status-failed-soft text-status-failed-fg border-status-failed/40",
    dot: "bg-status-failed",
  },
  running: {
    label: "Running",
    icon: LoaderCircle,
    badge: "bg-status-running-soft text-status-running-fg border-status-running/40",
    dot: "bg-status-running running-pulse",
  },
  missed: {
    label: "Missed",
    icon: ClockAlert,
    badge: "bg-status-missed-soft text-status-missed-fg border-status-missed/40",
    dot: "bg-status-missed",
  },
  queued: {
    label: "Queued",
    icon: Clock,
    badge: "bg-status-neutral-soft text-status-neutral-fg border-status-neutral/35",
    dot: "bg-status-neutral",
  },
};

/** Weakened-security markers (D-001: permanent, never hidden). */
export const SECURITY_FLAGS = ["unencrypted", "insecure-transport"] as const;
export type SecurityFlag = (typeof SECURITY_FLAGS)[number];
