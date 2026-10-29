/**
 * The jobs named in an in-use CONFLICT ("... still used by these jobs: a,
 * b"); empty when the message names none.
 */
export function jobsInUse(message: string): string[] {
  const m = /used by these jobs:\s*(.+)$/i.exec(message);
  if (!m?.[1]) return [];
  return m[1]
    .split(",")
    .map((s) => s.trim())
    .filter((s) => s !== "");
}
