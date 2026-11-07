import { STATUS_META, type RunStatus as UiStatus } from "@/components/status/status";
import type { RunStatus } from "@/graphql/gen/graphql";
import { defineMessages } from "@/lib/i18n";

const T = defineMessages({
  en: { canceled: "Canceled", skipped: "Skipped" },
  nl: { canceled: "Geannuleerd", skipped: "Overgeslagen" },
});

export const RUN_STATUS_MAP: Record<RunStatus, { ui: UiStatus; label?: string }> = {
  SUCCESS: { ui: "success" },
  PARTIAL: { ui: "partial" },
  FAILED: { ui: "failed" },
  RUNNING: { ui: "running" },
  QUEUED: { ui: "queued" },
  CANCELED: { ui: "queued", label: T.canceled },
  SKIPPED: { ui: "queued", label: T.skipped },
};

export function uiStatus(status: RunStatus): UiStatus {
  return RUN_STATUS_MAP[status].ui;
}

export function runStatusLabel(status: RunStatus): string {
  const m = RUN_STATUS_MAP[status];
  return m.label ?? STATUS_META[m.ui].label;
}

/** QUEUED or RUNNING. */
export function isActive(status: RunStatus): boolean {
  return status === "QUEUED" || status === "RUNNING";
}
