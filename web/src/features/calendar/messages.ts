import type { RunStatus } from "@/graphql/gen/graphql";
import { defineMessages, plural, type Messages } from "@/lib/i18n";

import type { CalendarView } from "./model";

/** Strings of the job calendar (task 6.11), in English and Dutch (D-235). */

const en = {
  page: {
    eyebrow: "Overview",
    title: "Calendar",
    description:
      "Planned backups, runs, skips and retention expiries. Times are shown in the zone you choose.",
    views: { month: "Month", week: "Week", day: "Day" } satisfies Record<CalendarView, string>,
    viewGroup: "View",
    previous: {
      month: "Previous month",
      week: "Previous week",
      day: "Previous day",
    } satisfies Record<CalendarView, string>,
    next: {
      month: "Next month",
      week: "Next week",
      day: "Next day",
    } satisfies Record<CalendarView, string>,
    today: "Today",
    filters: "Filters",
    job: "Job",
    allJobs: "All jobs",
    connection: "Connection",
    allConnections: "All connections",
    destination: "Destination",
    allDestinations: "All destinations",
    timeZone: "Time zone",
    thisBrowser: (zone: string) => `${zone} (this browser)`,
    truncatedTitle: "Some events are not shown",
    truncatedBody:
      "This range hit the calendar's limits, so only its earliest events are listed. Narrow the view or filter by job, connection or destination.",
    noJobs: "No job matches these filters.",
    legend: "Legend",
    legendHint:
      "Projected expiries assume every planned run succeeds; exact ones follow keep-days or Object Lock. Arrow keys move between days (the view follows), Enter opens a day.",
  },
  legend: {
    planned: "Planned",
    paused: "Paused",
    early: "Started early",
    overdue: "Overdue",
    skipped: "Skipped",
    runSuccess: "Run succeeded",
    runPartial: "Partial",
    runFailed: "Failed",
    runRunning: "Running",
    expiryExact: "Expiry, exact",
    expiryProjected: "Expiry, projected",
  },
  kinds: {
    planned: "Planned",
    paused: "Paused (job disabled)",
    early: "Started early",
    overdue: "Overdue: catch-up run pending",
    skipped: "Skipped",
    expiryExact: "Expires (exact)",
    expiryProjected: "Expiry (projected)",
    run: "Run",
    runStatus: {
      QUEUED: "Run queued",
      RUNNING: "Run in progress",
      SUCCESS: "Run succeeded",
      PARTIAL: "Run partial",
      FAILED: "Run failed",
      CANCELED: "Run canceled",
      SKIPPED: "Run skipped",
    } satisfies Record<RunStatus, string>,
  },
  grid: {
    today: ", today",
    events: (n: number) =>
      n === 0 ? ", no events" : plural(n, { one: ", 1 event", other: `, ${String(n)} events` }),
    more: (n: number) => `+${String(n)} more`,
    moreOn: (day: string) => ` on ${day}`,
  },
};

const nl: Messages<typeof en> = {
  page: {
    eyebrow: "Overzicht",
    title: "Kalender",
    description:
      "Geplande back-ups, runs, overgeslagen runs en verlopen door het bewaarbeleid. Tijden staan in de tijdzone die je kiest.",
    views: { month: "Maand", week: "Week", day: "Dag" },
    viewGroup: "Weergave",
    previous: { month: "Vorige maand", week: "Vorige week", day: "Vorige dag" },
    next: { month: "Volgende maand", week: "Volgende week", day: "Volgende dag" },
    today: "Vandaag",
    filters: "Filters",
    job: "Taak",
    allJobs: "Alle taken",
    connection: "Verbinding",
    allConnections: "Alle verbindingen",
    destination: "Bestemming",
    allDestinations: "Alle bestemmingen",
    timeZone: "Tijdzone",
    thisBrowser: (zone) => `${zone} (deze browser)`,
    truncatedTitle: "Niet alle gebeurtenissen worden getoond",
    truncatedBody:
      "Deze periode raakt de grenzen van de kalender, dus alleen de vroegste gebeurtenissen staan erin. Verklein de weergave of filter op taak, verbinding of bestemming.",
    noJobs: "Geen taak past bij deze filters.",
    legend: "Legenda",
    legendHint:
      "Verwachte verlopen gaan ervan uit dat elke geplande run slaagt; exacte volgen het aantal bewaardagen of Object Lock. Met de pijltjestoetsen ga je naar een andere dag (de weergave volgt), Enter opent een dag.",
  },
  legend: {
    planned: "Gepland",
    paused: "Gepauzeerd",
    early: "Eerder gestart",
    overdue: "Te laat",
    skipped: "Overgeslagen",
    runSuccess: "Run geslaagd",
    runPartial: "Gedeeltelijk",
    runFailed: "Mislukt",
    runRunning: "Bezig",
    expiryExact: "Verloopt, exact",
    expiryProjected: "Verloopt, verwacht",
  },
  kinds: {
    planned: "Gepland",
    paused: "Gepauzeerd (taak uitgeschakeld)",
    early: "Eerder gestart",
    overdue: "Te laat: inhaalrun staat klaar",
    skipped: "Overgeslagen",
    expiryExact: "Verloopt (exact)",
    expiryProjected: "Verloopt (verwacht)",
    run: "Run",
    runStatus: {
      QUEUED: "Run in de wachtrij",
      RUNNING: "Run bezig",
      SUCCESS: "Run geslaagd",
      PARTIAL: "Run gedeeltelijk",
      FAILED: "Run mislukt",
      CANCELED: "Run geannuleerd",
      SKIPPED: "Run overgeslagen",
    },
  },
  grid: {
    today: ", vandaag",
    events: (n) =>
      n === 0
        ? ", geen gebeurtenissen"
        : plural(n, { one: ", 1 gebeurtenis", other: `, ${String(n)} gebeurtenissen` }),
    more: (n) => `+${String(n)} meer`,
    moreOn: (day) => ` op ${day}`,
  },
};

export const messages = defineMessages({ en, nl });
