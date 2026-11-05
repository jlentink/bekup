/** Strings of the job's storage section (D-225). */
export const STORAGE_T = {
  title: "Storage",
  lead: "Backup files bekup tracks as present on the destinations. Each destination holds its own copy, so the total counts every copy.",
  loading: "Loading storage",
  total: "Stored in total",
  backups: (n: number) => (n === 1 ? "1 backup" : `${String(n)} backups`),
  files: (n: number) => (n === 1 ? "1 file" : `${String(n)} files`),
  newest: "Newest backup",
  newestNone: "No successful backup is stored.",
  newestHint: "one copy",
  empty: "No backup files are stored yet.",
  perDestination: "Per destination",
  cols: { destination: "Destination", stored: "Stored", backups: "Backups", files: "Files" },
  deleted: "deleted",
  trend: "Backup size",
  trendLead: (n: number) =>
    `The last ${String(n)} successful or partial backups, oldest first (one copy each).`,
  trendLabel: (n: number, min: string, max: string) =>
    `Bar chart of the size of the last ${String(n)} backups, from ${min} to ${max}.`,
  trendRange: (min: string, max: string) => `Smallest ${min} · largest ${max}`,
  trendBar: (at: string, size: string, partial: boolean) =>
    `${at}: ${size}${partial ? " (partial)" : ""}`,
} as const;
