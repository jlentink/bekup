/** Readable titles of run warning and notice codes. */

/**
 * Readable titles of the stable warning and notice codes (D-151, D-155).
 * Unknown codes are shown as they are.
 */
const CODE_TITLES: Record<string, string> = {
  insufficient_privileges: "Insufficient privileges",
  non_transactional_tables: "Non-transactional tables",
  non_innodb_tables: "Non-InnoDB tables",
  row_count_timeout: "Row count timed out",
  row_count_failed: "Row count failed",
  stats_budget_exceeded: "Statistics budget exceeded",
  stats_partial: "Statistics partial",
  stats_unavailable: "Statistics unavailable",
  stats_invalid: "Statistics discarded (invalid)",
  stats_not_recorded: "Statistics not recorded",
  roles_without_passwords: "Roles without passwords",
  databases_excluded: "Databases excluded",
  warnings_truncated: "More warnings truncated",
  incomplete: "Incomplete",
  version_unavailable: "Schema version unavailable",
  version_invalid: "Schema version invalid",
};

export function codeTitle(code: string): string {
  return CODE_TITLES[code] ?? code.replaceAll("_", " ");
}

/** Completeness-related warning codes (task 3.10), as opposed to transport/destination ones. */
export const COMPLETENESS_CODES: ReadonlySet<string> = new Set([
  "insufficient_privileges",
  "non_transactional_tables",
  "non_innodb_tables",
  "row_count_timeout",
  "row_count_failed",
  "stats_budget_exceeded",
  "stats_partial",
  "stats_unavailable",
  "stats_invalid",
  "stats_not_recorded",
  "roles_without_passwords",
]);
