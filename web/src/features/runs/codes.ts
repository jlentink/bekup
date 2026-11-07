/** Readable titles of run warning and notice codes. */

import { defineMessages } from "@/lib/i18n";

/**
 * Readable titles of the stable warning and notice codes (D-151, D-155).
 * Unknown codes are shown as they are.
 */
const CODE_TITLES: Readonly<Record<string, string>> = defineMessages({
  en: {
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
  },
  nl: {
    insufficient_privileges: "Onvoldoende rechten",
    non_transactional_tables: "Niet-transactionele tabellen",
    non_innodb_tables: "Tabellen die geen InnoDB zijn",
    row_count_timeout: "Tijdslimiet bij het tellen van rijen",
    row_count_failed: "Tellen van rijen mislukt",
    stats_budget_exceeded: "Budget voor statistieken overschreden",
    stats_partial: "Statistieken gedeeltelijk",
    stats_unavailable: "Statistieken niet beschikbaar",
    stats_invalid: "Statistieken genegeerd (ongeldig)",
    stats_not_recorded: "Statistieken niet vastgelegd",
    roles_without_passwords: "Rollen zonder wachtwoord",
    databases_excluded: "Databases uitgesloten",
    warnings_truncated: "Meer waarschuwingen weggelaten",
    incomplete: "Onvolledig",
    version_unavailable: "Schemaversie niet beschikbaar",
    version_invalid: "Schemaversie ongeldig",
  },
});

export function codeTitle(code: string): string {
  return (
    (Object.hasOwn(CODE_TITLES, code) ? CODE_TITLES[code] : undefined) ?? code.replaceAll("_", " ")
  );
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
