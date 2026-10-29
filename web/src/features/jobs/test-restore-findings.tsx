import { cn } from "@/lib/utils";

import { TestRestoreVerdict } from "./test-restore-verdict";
import { findingTitle, type DiffSummary, type TestRestoreDb } from "./test-restore-model";

/**
 * The comparison ("diff") of a test restore (task 4.6, D-169/D-170): per
 * database the findings between the backup's source statistics and the
 * restored scratch copy. Names and counts only, never data.
 */

function signed(n: number): string {
  return n > 0 ? `+${String(n)}` : String(n);
}

/** One-line chips of the diff: "2 FAIL · Row count differs ×1 ...". */
export function DiffSummaryLine({ summary }: { summary: DiffSummary }) {
  if (summary.fail === 0 && summary.warn === 0) {
    return <p className="text-xs text-muted-foreground">No differences found.</p>;
  }
  return (
    <ul aria-label="Differences" className="flex flex-wrap items-center gap-1.5">
      {summary.codes.map((c) => (
        <li
          key={`${c.severity}-${c.code}`}
          className={cn(
            "inline-flex h-6 items-center gap-1.5 rounded-sm border px-2 text-xs",
            c.severity === "FAIL"
              ? "border-status-failed/40 bg-status-failed-soft text-status-failed-fg"
              : "border-status-partial/40 bg-status-partial-soft text-status-partial-fg",
          )}
        >
          <span className="font-mono text-2xs font-semibold">{c.severity}</span>
          {findingTitle(c.code)}
          <span className="font-mono text-2xs opacity-80">×{c.count}</span>
        </li>
      ))}
      {summary.unlisted > 0 ? (
        <li className="text-2xs text-muted-foreground">+{summary.unlisted} not listed</li>
      ) : null}
    </ul>
  );
}

const T = {
  tables: (c: number, e: number) => `${String(c)} tables compared, ${String(e)} exactly`,
  findings: (shown: number, total: number) =>
    shown < total
      ? `${String(shown)} of ${String(total)} findings`
      : `${String(total)} finding${total === 1 ? "" : "s"}`,
  notDropped: "Scratch database not dropped yet (retried)",
  restore: (status: string, code: string | null | undefined) =>
    `Internal restore: ${status.toLowerCase()}${code ? ` (${code})` : ""}`,
  cols: { code: "Finding", object: "Object", source: "Backup", restored: "Restored", delta: "Δ" },
} as const;

/** The databases of a test restore, each with its findings table. */
export function TestRestoreDatabases({
  databases,
  openLimit = 10,
}: {
  databases: readonly TestRestoreDb[];
  /** Databases with at most this many findings start expanded. */
  openLimit?: number;
}) {
  return (
    <div className="flex flex-col gap-2">
      {databases.map((db) => (
        <details
          key={db.id}
          data-testid="test-restore-db"
          className="rounded-md border bg-surface px-3 py-2"
          open={db.findings.length > 0 && db.findings.length <= openLimit}
        >
          <summary className="flex cursor-pointer flex-wrap items-center gap-3 text-sm">
            <span className="font-mono">{db.database}</span>
            <TestRestoreVerdict status={db.status} />
            <span className="text-xs text-muted-foreground">
              {T.tables(db.tablesCompared, db.exactTables)}
            </span>
            <span className="text-xs text-muted-foreground">
              {T.findings(db.findings.length, db.findingsTotal)}
            </span>
            {db.scratchDropped === false ? (
              <span className="text-xs text-status-partial-fg">{T.notDropped}</span>
            ) : null}
          </summary>
          {db.errorMessage ? (
            <p className="mt-2 text-xs text-status-failed-fg">
              {db.errorMessage}
              {db.errorCode ? (
                <span className="ml-2 font-mono text-2xs text-muted-foreground">
                  {db.errorCode}
                </span>
              ) : null}
            </p>
          ) : null}
          {db.restoreStatus ? (
            <p className="mt-1 font-mono text-2xs text-muted-foreground">
              {T.restore(db.restoreStatus, db.restoreErrorCode)}
            </p>
          ) : null}
          {db.findings.length > 0 ? (
            <div className="mt-2 overflow-x-auto">
              <table className="w-full text-xs">
                <thead>
                  <tr className="text-left text-muted-foreground">
                    <th className="py-1 pr-3 font-medium">{T.cols.code}</th>
                    <th className="py-1 pr-3 font-medium">{T.cols.object}</th>
                    <th className="py-1 pr-3 text-right font-medium">{T.cols.source}</th>
                    <th className="py-1 pr-3 text-right font-medium">{T.cols.restored}</th>
                    <th className="py-1 text-right font-medium">{T.cols.delta}</th>
                  </tr>
                </thead>
                <tbody>
                  {db.findings.map((f, i) => (
                    <tr key={i} className="border-t border-rule">
                      <td className="py-1 pr-3">
                        <span
                          className={cn(
                            "mr-1.5 font-mono text-2xs font-semibold",
                            f.severity === "FAIL"
                              ? "text-status-failed-fg"
                              : "text-status-partial-fg",
                          )}
                        >
                          {f.severity}
                        </span>
                        {findingTitle(f.code)}
                      </td>
                      <td className="py-1 pr-3 font-mono break-all">{f.object}</td>
                      <td className="py-1 pr-3 text-right font-mono tabular-nums">
                        {f.source ?? ""}
                      </td>
                      <td className="py-1 pr-3 text-right font-mono tabular-nums">
                        {f.restored ?? ""}
                      </td>
                      <td
                        className={cn(
                          "py-1 text-right font-mono tabular-nums",
                          f.delta != null && f.delta < 0 && "text-status-failed-fg",
                        )}
                      >
                        {f.delta != null ? signed(f.delta) : ""}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : null}
        </details>
      ))}
    </div>
  );
}
