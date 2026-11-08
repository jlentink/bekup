import {
  CircleCheck,
  Clock,
  ClockAlert,
  Contrast,
  LoaderCircle,
  OctagonX,
  type LucideIcon,
} from "lucide-react";

import { defineMessages } from "@/lib/i18n";

const LABEL = defineMessages({
  en: {
    success: "Succeeded",
    partial: "Partial",
    failed: "Failed",
    running: "Running",
    missed: "Missed",
    queued: "Queued",
  },
  nl: {
    success: "Geslaagd",
    partial: "Gedeeltelijk",
    failed: "Mislukt",
    running: "Bezig",
    missed: "Gemist",
    queued: "In de wachtrij",
  },
});

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
    label: LABEL.success,
    icon: CircleCheck,
    badge: "bg-status-success-soft text-status-success-fg border-status-success/35",
    dot: "bg-status-success",
  },
  partial: {
    label: LABEL.partial,
    icon: Contrast,
    badge: "bg-status-partial-soft text-status-partial-fg border-status-partial/40",
    dot: "bg-status-partial",
  },
  failed: {
    label: LABEL.failed,
    icon: OctagonX,
    badge: "bg-status-failed-soft text-status-failed-fg border-status-failed/40",
    dot: "bg-status-failed",
  },
  running: {
    label: LABEL.running,
    icon: LoaderCircle,
    badge: "bg-status-running-soft text-status-running-fg border-status-running/40",
    dot: "bg-status-running running-pulse",
  },
  missed: {
    label: LABEL.missed,
    icon: ClockAlert,
    badge: "bg-status-missed-soft text-status-missed-fg border-status-missed/40",
    dot: "bg-status-missed",
  },
  queued: {
    label: LABEL.queued,
    icon: Clock,
    badge: "bg-status-neutral-soft text-status-neutral-fg border-status-neutral/35",
    dot: "bg-status-neutral",
  },
};

/** Weakened-security markers (D-001: permanent, never hidden). */
export const SECURITY_FLAGS = [
  "unencrypted",
  "insecure-transport",
  "personal-webhooks-widened",
  "any-address",
  "never-expires",
] as const;
export type SecurityFlag = (typeof SECURITY_FLAGS)[number];
