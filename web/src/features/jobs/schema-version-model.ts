/**
 * Client-side checks of the schema-version settings (D-210). The server
 * (internal/schemaversion/custom.go) is authoritative; these only give
 * early feedback on the simple character rules. Denied words and
 * functions are checked by the server, which names the offending one.
 */
import { defineMessages } from "@/lib/i18n";

/** The longest custom query the server accepts. */
export const MAX_CUSTOM_SQL_BYTES = 2048;

/** A preset table's PostgreSQL schema: a plain identifier. */
export const SCHEMA_RE = /^[A-Za-z_][A-Za-z0-9_]{0,62}$/;

export function utf8Bytes(s: string): number {
  return new TextEncoder().encode(s).length;
}

const T = defineMessages({
  en: {
    sequences: {
      semicolon: "a semicolon (;)",
      dashComment: "a comment (--)",
      blockOpen: "a comment (/*)",
      blockClose: "a comment (*/)",
      hashComment: "a comment (#)",
      backslash: "a backslash (\\)",
      dollar: "a dollar sign ($)",
    },
    refused: {
      characters:
        "; (also at the end), comments (--, /* */, #), backslashes, $, U& escapes, carriage returns, other control characters (tabs and newlines are fine) and non-ASCII characters",
      functions:
        "file, network, lock, setting, backend and delay functions such as pg_read_file, pg_ls_*, pg_sleep*, pg_terminate_backend, set_config, lo_*, dblink*, query_to_xml*, *_to_xmlschema, ts_stat, ts_rewrite, pg_create_*, pg_stat_reset*, LOAD_FILE, GET_LOCK, xp_*, sys_*",
    },
    empty: "Enter a query.",
    tooLong: (max: number) => `The query is longer than ${String(max)} bytes.`,
    carriageReturn: "The query may not contain a carriage return (use plain newlines).",
    control: "The query may not contain control characters.",
    ascii: "The query may contain ASCII characters only.",
    denied: (what: string) => `The query may not contain ${what}.`,
    unicodeEscape: "The query may not contain a U& escape.",
    start: "The query must start with SELECT or WITH.",
  },
  nl: {
    sequences: {
      semicolon: "een puntkomma (;)",
      dashComment: "commentaar (--)",
      blockOpen: "commentaar (/*)",
      blockClose: "commentaar (*/)",
      hashComment: "commentaar (#)",
      backslash: "een backslash (\\)",
      dollar: "een dollarteken ($)",
    },
    refused: {
      characters:
        "; (ook aan het einde), commentaar (--, /* */, #), backslashes, $, U&-escapes, carriage returns, andere stuurtekens (tabs en nieuwe regels mogen wel) en niet-ASCII-tekens",
      functions:
        "bestands-, netwerk-, lock-, instellings-, backend- en vertragingsfuncties zoals pg_read_file, pg_ls_*, pg_sleep*, pg_terminate_backend, set_config, lo_*, dblink*, query_to_xml*, *_to_xmlschema, ts_stat, ts_rewrite, pg_create_*, pg_stat_reset*, LOAD_FILE, GET_LOCK, xp_*, sys_*",
    },
    empty: "Vul een query in.",
    tooLong: (max: number) => `De query is langer dan ${String(max)} bytes.`,
    carriageReturn: "De query mag geen carriage return bevatten (gebruik gewone nieuwe regels).",
    control: "De query mag geen stuurtekens bevatten.",
    ascii: "De query mag alleen ASCII-tekens bevatten.",
    denied: (what: string) => `De query mag ${what} niet bevatten.`,
    unicodeEscape: "De query mag geen U&-escape bevatten.",
    start: "De query moet beginnen met SELECT of WITH.",
  },
});

/** Sequences refused anywhere in the text, with their description. */
const DENIED_SEQUENCES: readonly (readonly [string, string])[] = [
  [";", T.sequences.semicolon],
  ["--", T.sequences.dashComment],
  ["/*", T.sequences.blockOpen],
  ["*/", T.sequences.blockClose],
  ["#", T.sequences.hashComment],
  ["\\", T.sequences.backslash],
  ["$", T.sequences.dollar],
];

/**
 * What the server refuses, for the editor's help text (keep in sync with
 * internal/schemaversion/custom.go).
 */
export const REFUSED_SUMMARY = {
  characters: T.refused.characters,
  words:
    "UNION, INTERSECT, EXCEPT, INSERT, UPDATE, DELETE, MERGE, REPLACE, INTO, COPY, LOAD, OUTFILE, DUMPFILE, CREATE, ALTER, DROP, TRUNCATE, RENAME, COMMENT, GRANT, REVOKE, SET, RESET, DO, CALL, EXECUTE, EXEC, PREPARE, DEALLOCATE, DECLARE, BEGIN, COMMIT, ROLLBACK, SAVEPOINT, RELEASE, START, TRANSACTION, LOCK, UNLOCK, SHARE, LISTEN, NOTIFY, UNLISTEN, DISCARD, VACUUM, ANALYZE, REINDEX, CLUSTER, REFRESH, IMPORT, ATTACH, DETACH, INSTALL, UNINSTALL, KILL, FLUSH, PURGE, SHUTDOWN, HANDLER, SECURITY, SLEEP, BENCHMARK, UESCAPE",
  functions: T.refused.functions,
};

/** The first problem of a custom query, or null. */
export function customSqlProblem(sql: string): string | null {
  if (sql.trim() === "") return T.empty;
  if (utf8Bytes(sql) > MAX_CUSTOM_SQL_BYTES) return T.tooLong(MAX_CUSTOM_SQL_BYTES);
  for (const ch of sql) {
    const c = ch.codePointAt(0) ?? 0;
    if (c === 0x0a || c === 0x09) continue;
    if (c === 0x0d) return T.carriageReturn;
    if (c < 0x20 || c === 0x7f) return T.control;
    if (c > 0x7f) return T.ascii;
  }
  for (const [seq, what] of DENIED_SEQUENCES) {
    if (sql.includes(seq)) return T.denied(what);
  }
  if (/u&['"]/i.test(sql)) return T.unicodeEscape;
  if (!/^[ \t\n]*(select|with)(?![A-Za-z0-9_])/i.test(sql)) return T.start;
  return null;
}
