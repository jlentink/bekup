/** Names that can appear in a shell command without quoting. */
const SAFE_NAME_RE = /^[A-Za-z0-9_][A-Za-z0-9_.-]{0,62}$/;

/** Shell-quotes a value unless it is plainly safe. */
function shellWord(s: string): string {
  return /^[A-Za-z0-9_./-]+$/.test(s) ? s : `'${s.replaceAll("'", `'"'"'`)}'`;
}

/**
 * Fills the server's restore command template: <dump-file> becomes the
 * (server-sanitized) file name, <database> the backed-up database when it
 * is a plain identifier; anything else keeps its placeholder.
 */
export function restoreCommandFor(
  template: string,
  fileName: string,
  database?: string | null,
): string {
  let cmd = template.replaceAll("<dump-file>", shellWord(fileName));
  if (database && SAFE_NAME_RE.test(database)) cmd = cmd.replaceAll("<database>", database);
  return cmd;
}
