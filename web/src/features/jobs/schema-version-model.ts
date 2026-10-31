/**
 * Client-side checks of the schema-version settings (D-210). The server
 * (internal/schemaversion/custom.go) is authoritative; these only give
 * early feedback on the simple character rules. Denied words and
 * functions are checked by the server, which names the offending one.
 */

/** The longest custom query the server accepts. */
export const MAX_CUSTOM_SQL_BYTES = 2048;

/** A preset table's PostgreSQL schema: a plain identifier. */
export const SCHEMA_RE = /^[A-Za-z_][A-Za-z0-9_]{0,62}$/;

export function utf8Bytes(s: string): number {
  return new TextEncoder().encode(s).length;
}

/** Sequences refused anywhere in the text, with their description. */
const DENIED_SEQUENCES: readonly (readonly [string, string])[] = [
  [";", "a semicolon (;)"],
  ["--", "a comment (--)"],
  ["/*", "a comment (/*)"],
  ["*/", "a comment (*/)"],
  ["#", "a comment (#)"],
  ["\\", "a backslash (\\)"],
  ["$", "a dollar sign ($)"],
];

/**
 * What the server refuses, for the editor's help text (keep in sync with
 * internal/schemaversion/custom.go).
 */
export const REFUSED_SUMMARY = {
  characters:
    "; (also at the end), comments (--, /* */, #), backslashes, $, U& escapes, carriage returns, other control characters (tabs and newlines are fine) and non-ASCII characters",
  words:
    "UNION, INTERSECT, EXCEPT, INSERT, UPDATE, DELETE, MERGE, REPLACE, INTO, COPY, LOAD, OUTFILE, DUMPFILE, CREATE, ALTER, DROP, TRUNCATE, RENAME, COMMENT, GRANT, REVOKE, SET, RESET, DO, CALL, EXECUTE, EXEC, PREPARE, DEALLOCATE, DECLARE, BEGIN, COMMIT, ROLLBACK, SAVEPOINT, RELEASE, START, TRANSACTION, LOCK, UNLOCK, SHARE, LISTEN, NOTIFY, UNLISTEN, DISCARD, VACUUM, ANALYZE, REINDEX, CLUSTER, REFRESH, IMPORT, ATTACH, DETACH, INSTALL, UNINSTALL, KILL, FLUSH, PURGE, SHUTDOWN, HANDLER, SECURITY, SLEEP, BENCHMARK, UESCAPE",
  functions:
    "file, network, lock, setting, backend and delay functions such as pg_read_file, pg_ls_*, pg_sleep*, pg_terminate_backend, set_config, lo_*, dblink*, query_to_xml*, *_to_xmlschema, ts_stat, ts_rewrite, pg_create_*, pg_stat_reset*, LOAD_FILE, GET_LOCK, xp_*, sys_*",
} as const;

/** The first problem of a custom query, or null. */
export function customSqlProblem(sql: string): string | null {
  if (sql.trim() === "") return "Enter a query.";
  if (utf8Bytes(sql) > MAX_CUSTOM_SQL_BYTES)
    return `The query is longer than ${String(MAX_CUSTOM_SQL_BYTES)} bytes.`;
  for (const ch of sql) {
    const c = ch.codePointAt(0) ?? 0;
    if (c === 0x0a || c === 0x09) continue;
    if (c === 0x0d) return "The query may not contain a carriage return (use plain newlines).";
    if (c < 0x20 || c === 0x7f) return "The query may not contain control characters.";
    if (c > 0x7f) return "The query may contain ASCII characters only.";
  }
  for (const [seq, what] of DENIED_SEQUENCES) {
    if (sql.includes(seq)) return `The query may not contain ${what}.`;
  }
  if (/u&['"]/i.test(sql)) return "The query may not contain a U& escape.";
  if (!/^[ \t\n]*(select|with)(?![A-Za-z0-9_])/i.test(sql))
    return "The query must start with SELECT or WITH.";
  return null;
}
