/**
 * The runtime config reload model (task 6.13, D-200): status tones, the
 * outcome summary of a reload request and the poll rules. Key names only;
 * the server never sends values or the file path.
 */

import type { Tone } from "@/components/status/tag";
import type {
  ConfigProcessResultFieldsFragment,
  ConfigReloadStatus,
  ConfigReloadTrigger,
} from "@/graphql/gen/graphql";

export type ProcessResult = ConfigProcessResultFieldsFragment;

/** Poll cadence for a reload request (mutable for tests only). */
export const POLL = { intervalMs: 2_000, timeoutMs: 30_000 };

/** Status → Tag tone. Colour is never the only cue: the Tag carries words. */
export const STATUS_TONE: Record<ConfigReloadStatus, Tone> = {
  LOADED: "neutral",
  APPLIED: "success",
  UNCHANGED: "neutral",
  RESTART_REQUIRED: "warning",
  REJECTED: "danger",
  FAILED: "danger",
};

/**
 * Outcome buckets of a request's summary, in display order. "stopped":
 * the process answered but no longer listens (stopped or restarted since),
 * so its answer is history and needs no action.
 */
export type Outcome = "applied" | "unchanged" | "restart" | "rejected" | "failed" | "stopped";
export const OUTCOMES: readonly Outcome[] = [
  "applied",
  "restart",
  "unchanged",
  "rejected",
  "failed",
  "stopped",
];

export const OUTCOME_TONE: Record<Outcome, Tone> = {
  applied: "success",
  unchanged: "neutral",
  restart: "warning",
  rejected: "danger",
  failed: "danger",
  stopped: "neutral",
};

function outcomeOf(r: Pick<ProcessResult, "status" | "listening">): Outcome | null {
  if (r.status === "LOADED") return null;
  if (!r.listening) return "stopped";
  switch (r.status) {
    case "APPLIED":
      return "applied";
    case "UNCHANGED":
      return "unchanged";
    case "RESTART_REQUIRED":
      return "restart";
    case "REJECTED":
      return "rejected";
    case "FAILED":
      return "failed";
  }
}

export type Summary = Record<Outcome, number>;

/**
 * Counts the answers of a request per outcome (startup rows never belong to
 * a request); answers of processes that no longer listen count as "stopped".
 */
export function summarize(
  results: readonly Pick<ProcessResult, "status" | "listening">[],
): Summary {
  const out: Summary = { applied: 0, unchanged: 0, restart: 0, rejected: 0, failed: 0, stopped: 0 };
  for (const r of results) {
    const o = outcomeOf(r);
    if (o) out[o] += 1;
  }
  return out;
}

/**
 * The overall verdict of a request: the most serious outcome of a process
 * that still listens ("stopped" when only stopped processes answered).
 */
export type Verdict = "problem" | "restart" | "applied" | "unchanged" | "stopped" | "none";

export function verdict(s: Summary): Verdict {
  if (s.rejected + s.failed > 0) return "problem";
  if (s.restart > 0) return "restart";
  if (s.applied > 0) return "applied";
  if (s.unchanged > 0) return "unchanged";
  if (s.stopped > 0) return "stopped";
  return "none";
}

export const VERDICT_TONE: Record<Verdict, Tone> = {
  problem: "danger",
  restart: "warning",
  applied: "success",
  unchanged: "neutral",
  stopped: "neutral",
  none: "neutral",
};

/**
 * How many processes a request waits for: the server's expectedProcesses
 * (answered plus listening ones responsible for the request: started at
 * most a minute after it, D-200), or more when more answered.
 */
export function expected(answered: number, expectedProcesses: number): number {
  return Math.max(answered, expectedProcesses);
}

/** True when every process the request waits for has answered. */
export function complete(answered: number, expectedProcesses: number): boolean {
  return answered >= expectedProcesses;
}

/** Splits processes into listening ones and stopped/not listening ones (order kept). */
export function splitByListening<P extends Pick<ProcessResult, "listening">>(
  processes: readonly P[],
): { listening: P[]; stopped: P[] } {
  return {
    listening: processes.filter((p) => p.listening),
    stopped: processes.filter((p) => !p.listening),
  };
}

/** Keys that weaken security across results (deduplicated, sorted). */
export function weakened(results: readonly Pick<ProcessResult, "weakenedKeys">[]): string[] {
  return [...new Set(results.flatMap((r) => r.weakenedKeys))].sort();
}

/** Where a process's latest row came from. */
export type Origin = "startup" | "latest" | "ui" | "signal";

/**
 * Startup rows have no request; a row answering the newest UI request is
 * "latest"; otherwise the request's trigger says whether it was an earlier
 * reload from this page or a SIGHUP to that process.
 */
export function originOf(
  r: Pick<ProcessResult, "requestId"> & { trigger?: ConfigReloadTrigger | null },
  lastRequestId?: string,
): Origin {
  if (!r.requestId) return "startup";
  if (r.requestId === lastRequestId) return "latest";
  return r.trigger === "SIGNAL" ? "signal" : "ui";
}

export interface KeyGroup {
  section: string;
  keys: string[];
}

/** Groups `section.key` names by their section (everything before the last dot); top-level keys first. */
export function groupKeys(keys: readonly string[]): KeyGroup[] {
  const by = new Map<string, string[]>();
  for (const k of [...new Set(keys)].sort()) {
    const dot = k.lastIndexOf(".");
    const section = dot > 0 ? k.slice(0, dot) : "";
    const list = by.get(section) ?? [];
    list.push(k);
    by.set(section, list);
  }
  return [...by.entries()]
    .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
    .map(([section, list]) => ({ section, keys: list }));
}
