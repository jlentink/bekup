import { defineMessages, plural } from "@/lib/i18n";

/** Strings of the job's storage section (D-225). */
export const STORAGE_T = defineMessages({
  en: {
    title: "Storage",
    lead: "Backup files bekup tracks as present on the destinations. Each destination holds its own copy, so the total counts every copy.",
    loading: "Loading storage",
    total: "Stored in total",
    backups: (n: number) => plural(n, { one: "1 backup", other: `${String(n)} backups` }),
    files: (n: number) => plural(n, { one: "1 file", other: `${String(n)} files` }),
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
  },
  nl: {
    title: "Opslag",
    lead: "Back-upbestanden die volgens bekup op de bestemmingen staan. Elke bestemming heeft haar eigen kopie, dus het totaal telt elke kopie mee.",
    loading: "Opslag laden",
    total: "Totaal opgeslagen",
    backups: (n: number) => plural(n, { one: "1 back-up", other: `${String(n)} back-ups` }),
    files: (n: number) => plural(n, { one: "1 bestand", other: `${String(n)} bestanden` }),
    newest: "Nieuwste back-up",
    newestNone: "Er is geen geslaagde back-up opgeslagen.",
    newestHint: "één kopie",
    empty: "Er zijn nog geen back-upbestanden opgeslagen.",
    perDestination: "Per bestemming",
    cols: {
      destination: "Bestemming",
      stored: "Opgeslagen",
      backups: "Back-ups",
      files: "Bestanden",
    },
    deleted: "verwijderd",
    trend: "Back-upgrootte",
    trendLead: (n: number) =>
      `De laatste ${String(n)} geslaagde of gedeeltelijke back-ups, oudste eerst (één kopie per back-up).`,
    trendLabel: (n: number, min: string, max: string) =>
      `Staafdiagram van de grootte van de laatste ${String(n)} back-ups, van ${min} tot ${max}.`,
    trendRange: (min: string, max: string) => `Kleinste ${min} · grootste ${max}`,
    trendBar: (at: string, size: string, partial: boolean) =>
      `${at}: ${size}${partial ? " (gedeeltelijk)" : ""}`,
  },
});
