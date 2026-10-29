import type { RunDetailQuery as RunDetailResult } from "@/graphql/gen/graphql";

import { COMPLETENESS_CODES } from "./codes";

type Run = NonNullable<RunDetailResult["run"]>;

export interface Issue {
  code: string;
  entries: { database: string | null; message: string }[];
}

/** Completeness warnings of all items, grouped by code. */
export function groupIssues(run: Pick<Run, "items" | "warnings">): Issue[] {
  const byCode = new Map<string, Issue>();
  const seen = new Set<string>();
  const add = (code: string, database: string | null | undefined, message: string) => {
    if (!COMPLETENESS_CODES.has(code)) return;
    const key = `${code}\u0000${database ?? ""}\u0000${message}`;
    if (seen.has(key)) return;
    seen.add(key);
    const issue = byCode.get(code) ?? { code, entries: [] };
    issue.entries.push({ database: database ?? null, message });
    byCode.set(code, issue);
  };
  for (const item of run.items) {
    for (const w of item.warnings) add(w.code, w.database ?? item.database, w.message);
  }
  for (const w of run.warnings) add(w.code, w.database, w.message);
  return [...byCode.values()];
}
