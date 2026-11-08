import { defineMessages, plural } from "@/lib/i18n";

/** Strings of the destination storage views (D-225). */
export const ST = defineMessages({
  en: {
    column: "Stored",
    none: "Nothing yet",
    backups: (n: number) => plural(n, { one: "1 backup", other: `${String(n)} backups` }),
    files: (n: number) => plural(n, { one: "1 file", other: `${String(n)} files` }),
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
    checkedStored: (when: string) => `Last checked ${when}; bekup checks every hour.`,
    stale: (when: string) =>
      `Last checked ${when}: the hourly space check has not refreshed this for more than 3 hours, so it may be out of date.`,
    checkNow: "Check now",
    liveFailed: (reason: string) =>
      `Check now failed: ${reason.replace(/\.$/, "")}. Shown is the last stored check.`,
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
  },
  nl: {
    column: "Opgeslagen",
    none: "Nog niets",
    backups: (n: number) => plural(n, { one: "1 back-up", other: `${String(n)} back-ups` }),
    files: (n: number) => plural(n, { one: "1 bestand", other: `${String(n)} bestanden` }),
    section: "Opslag",
    lead: "Hoeveel ruimte de back-ups van bekup hier innemen en, als de server het meldt, hoeveel ruimte er over is.",
    storedLine: (bytes: string, backups: string, files: string) =>
      `${bytes} gebruikt door bekup · ${backups} · ${files}`,
    storedUnknown: "De opgeslagen grootte kon niet worden geladen.",
    spaceLine: (stored: string, free: string, total: string) =>
      `${stored} gebruikt door bekup · ${free} vrij van ${total}`,
    spaceFreeOnly: (stored: string, free: string) => `${stored} gebruikt door bekup · ${free} vrij`,
    spaceUsedOnly: (stored: string, used: string) =>
      `${stored} gebruikt door bekup · ${used} in totaal in gebruik; de vrije ruimte wordt niet gemeld (onbeperkt quotum?)`,
    checking: "Vrije ruimte controleren",
    checked: (when: string) => `Gecontroleerd ${when}; tot 5 minuten in de cache.`,
    checkedStored: (when: string) => `Laatst gecontroleerd ${when}; bekup controleert elk uur.`,
    stale: (when: string) =>
      `Laatst gecontroleerd ${when}: de controle per uur heeft dit al meer dan 3 uur niet bijgewerkt, dus het kan verouderd zijn.`,
    checkNow: "Nu controleren",
    liveFailed: (reason: string) =>
      `Nu controleren is mislukt: ${reason.replace(/\.$/, "")}. Je ziet de laatst opgeslagen controle.`,
    barLabel: (stored: string, others: string, free: string, total: string) =>
      `Van ${total}: ${stored} back-ups van bekup, ${others} andere bestanden, ${free} vrij.`,
    legendBekup: "Back-ups van bekup",
    legendOther: "Andere bestanden",
    legendFree: "Vrij",
    low: "Weinig vrije ruimte",
    lowLead: (newest: string) =>
      `Minder dan een tiende van de ruimte is vrij, of minder dan de nieuwste volledige set back-ups (${newest}): de volgende back-ups passen misschien niet.`,
    unsupported: "Vrije ruimte wordt niet gemeld",
    unavailable: "Vrije ruimte niet beschikbaar",
    checkAgain: "Opnieuw controleren",
  },
});
