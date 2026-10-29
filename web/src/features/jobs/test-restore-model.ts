import type { TestRestoreStatus } from "@/graphql/gen/graphql";

export interface Finding {
  severity: string;
  code: string;
  object?: string | null;
  source?: number | null;
  restored?: number | null;
  delta?: number | null;
}

export interface TestRestoreDb {
  id: string;
  database: string;
  status: TestRestoreStatus;
  errorCode?: string | null;
  errorMessage?: string | null;
  scratchDropped?: boolean;
  findingsTotal: number;
  tablesCompared: number;
  exactTables: number;
  restoreStatus?: string | null;
  restoreErrorCode?: string | null;
  findings: readonly Finding[];
}

const FINDING_TITLES: Record<string, string> = {
  object_count_mismatch: "Object count differs",
  table_missing: "Table missing in the restore",
  table_unexpected: "Unexpected table in the restore",
  row_count_mismatch: "Row count differs",
  row_count_delta: "Row count drift (estimate)",
  row_count_unknown: "Row count unknown",
  tables_total_mismatch: "Table total differs",
  tables_truncated: "Table list truncated",
  source_stats_missing: "Source statistics missing",
  source_stats_invalid: "Source statistics invalid",
  source_warnings: "Source had completeness warnings",
  scratch_stats_invalid: "Restored statistics invalid",
};

export function findingTitle(code: string): string {
  return FINDING_TITLES[code] ?? code.replaceAll("_", " ");
}

export interface DiffSummary {
  fail: number;
  warn: number;
  /** Findings beyond the 200 listed per database. */
  unlisted: number;
  /** Finding codes by frequency (listed findings only). */
  codes: { code: string; severity: string; count: number }[];
}

/** Counts the findings of all databases (FAIL first, then by frequency). */
export function diffSummary(dbs: readonly TestRestoreDb[]): DiffSummary {
  const byCode = new Map<string, { code: string; severity: string; count: number }>();
  let fail = 0;
  let warn = 0;
  let unlisted = 0;
  for (const db of dbs) {
    unlisted += Math.max(0, db.findingsTotal - db.findings.length);
    for (const f of db.findings) {
      if (f.severity === "FAIL") fail++;
      else warn++;
      const key = `${f.severity}\u0000${f.code}`;
      const entry = byCode.get(key) ?? { code: f.code, severity: f.severity, count: 0 };
      entry.count++;
      byCode.set(key, entry);
    }
  }
  const codes = [...byCode.values()].sort(
    (a, b) =>
      Number(b.severity === "FAIL") - Number(a.severity === "FAIL") ||
      b.count - a.count ||
      a.code.localeCompare(b.code),
  );
  return { fail, warn, unlisted, codes };
}
