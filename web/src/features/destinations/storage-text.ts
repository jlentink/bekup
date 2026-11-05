/** Strings of the destination storage views (D-225). */
export const ST = {
  column: "Stored",
  none: "Nothing yet",
  backups: (n: number) => (n === 1 ? "1 backup" : `${String(n)} backups`),
  files: (n: number) => (n === 1 ? "1 file" : `${String(n)} files`),
  section: "Storage",
  lead: "What bekup's backups occupy here and, where the server reports it, how much space is left.",
  storedLine: (bytes: string, backups: string, files: string) =>
    `${bytes} used by bekup · ${backups} · ${files}`,
  storedUnknown: "The stored size could not be loaded.",
  spaceLine: (stored: string, free: string, total: string) =>
    `${stored} used by bekup · ${free} free of ${total}`,
  spaceFreeOnly: (stored: string, free: string) => `${stored} used by bekup · ${free} free`,
  spaceUsedOnly: (stored: string, used: string) =>
    `${stored} used by bekup · ${used} in use in total; the free space is not reported (unlimited quota?)`,
  checking: "Checking the free space",
  checked: (when: string) => `Checked ${when}; cached for up to 5 minutes.`,
  barLabel: (stored: string, others: string, free: string, total: string) =>
    `Of ${total}: ${stored} bekup's backups, ${others} other files, ${free} free.`,
  legendBekup: "bekup's backups",
  legendOther: "Other files",
  legendFree: "Free",
  low: "Free space is low",
  lowLead: (newest: string) =>
    `Less than a tenth of the space is free, or less than the newest full set of backups (${newest}): the next backups may not fit.`,
  unsupported: "Free space is not reported",
  unavailable: "Free space not available",
  checkAgain: "Check again",
} as const;
