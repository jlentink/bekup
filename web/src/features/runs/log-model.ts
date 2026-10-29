import type { RunLogLevel, RunLogStream } from "@/graphql/gen/graphql";

export interface LogLine {
  id: string;
  time: string;
  level: RunLogLevel;
  stream: RunLogStream;
  line: string;
}

/** Lines kept in memory; the server caps logs anyway (logLinesDropped). */
const MAX_LINES = 100_000;
export const PAGE = 1000;

/** Compares increasing numeric ids given as strings, without precision loss. */
export function compareIds(a: string, b: string): number {
  if (a.length !== b.length) return a.length - b.length;
  return a < b ? -1 : a > b ? 1 : 0;
}

/**
 * Appends a batch, dropping lines already present (a reopened stream may
 * replay from an older afterId). Lines arrive in id order.
 */
export function appendLines(prev: readonly LogLine[], batch: readonly LogLine[]): LogLine[] {
  const last = prev[prev.length - 1]?.id;
  const fresh = last ? batch.filter((l) => compareIds(l.id, last) > 0) : [...batch];
  if (fresh.length === 0) return prev as LogLine[];
  const next = [...prev, ...fresh];
  return next.length > MAX_LINES ? next.slice(next.length - MAX_LINES) : next;
}

/** One live log batch (the `logs` part of the run page's runActivity stream). */
export interface LogBatch {
  lines: readonly LogLine[];
  endId?: string | null;
  done: boolean;
}
