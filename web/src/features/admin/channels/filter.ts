import type { NotificationFilterInput, NotificationGroup } from "@/graphql/gen/graphql";

export interface CatalogEvent {
  type: string;
  label: string;
  group: NotificationGroup;
  mandatory: boolean;
  perJob: boolean;
}

export interface CatalogGroup {
  group: NotificationGroup;
  label: string;
  events: readonly CatalogEvent[];
}

/**
 * The part of the catalog a shared channel can receive: job events only,
 * never security alerts (D-183, D-191: they go to admins personally and
 * are never sent to shared channels) and never mandatory events.
 */
export function attachableCatalog(catalog: readonly CatalogGroup[]): CatalogGroup[] {
  return catalog
    .filter((g) => g.group !== "SECURITY")
    .map((g) => ({
      ...g,
      events: g.events.filter(
        (e) =>
          e.perJob && !e.mandatory && e.group !== "SECURITY" && !e.type.startsWith("security."),
      ),
    }))
    .filter((g) => g.events.length > 0);
}

export interface FilterDraft {
  groups: readonly NotificationGroup[];
  events: readonly string[];
}

/**
 * The filter input for the selection; security groups and events are
 * dropped even if they were somehow selected. Both empty: every
 * non-security event of the job.
 */
export function toFilterInput(
  d: FilterDraft,
  catalog: readonly CatalogGroup[],
): NotificationFilterInput {
  const allowed = attachableCatalog(catalog);
  const groups = new Set(allowed.map((g) => g.group));
  const events = new Set(allowed.flatMap((g) => g.events.map((e) => e.type)));
  const pickedGroups = d.groups.filter((g) => groups.has(g));
  const covered = new Set(
    allowed
      .filter((g) => pickedGroups.includes(g.group))
      .flatMap((g) => g.events.map((e) => e.type)),
  );
  return {
    groups: pickedGroups,
    // Events inside a whole picked group are redundant.
    events: d.events.filter((e) => events.has(e) && !covered.has(e)),
  };
}

/** A short human summary of an attachment filter. */
export function describeFilter(
  f: FilterDraft,
  catalog: readonly CatalogGroup[] | undefined,
): string {
  if (f.groups.length === 0 && f.events.length === 0) return "All job events";
  const groupLabel = (g: NotificationGroup) =>
    catalog?.find((c) => c.group === g)?.label ?? g.charAt(0) + g.slice(1).toLowerCase();
  const eventLabel = (t: string) =>
    catalog?.flatMap((c) => c.events).find((e) => e.type === t)?.label ?? t;
  return [...f.groups.map(groupLabel), ...f.events.map(eventLabel)].join(", ");
}

/** Splits an e-mail list typed with commas, semicolons, spaces or new lines. */
export function parseAddresses(text: string): string[] {
  return [
    ...new Set(
      text
        .split(/[\s,;]+/)
        .map((a) => a.trim())
        .filter((a) => a !== ""),
    ),
  ];
}
