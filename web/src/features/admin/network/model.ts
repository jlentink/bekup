/**
 * The SSRF allowlist editor model (D-120, D-196, D-198). Client hints only:
 * the server validates every entry with the guard's own rules and answers
 * VALIDATION on `cidrEntries.<i>.value|scopes` / `hostEntries.<i>...`.
 */

import type { SsrfScope } from "@/graphql/gen/graphql";

export type ListKind = "cidrs" | "hosts";

/** Every scope in display order (D-198). */
export const ALL_SCOPES: readonly SsrfScope[] = [
  "DESTINATIONS",
  "SMTP",
  "SHARED_WEBHOOKS",
  "PERSONAL_WEBHOOKS",
];

/** One stored or drafted entry with the uses it widens. */
export interface Entry {
  value: string;
  scopes: readonly SsrfScope[];
}

export interface Row {
  /** Stable React key. */
  key: string;
  value: string;
  scopes: SsrfScope[];
}

let seq = 0;
export function newRow(value = "", scopes: readonly SsrfScope[] = []): Row {
  seq += 1;
  return { key: `r${String(seq)}`, value, scopes: sortScopes(scopes) };
}

/** Scopes deduplicated in display order (unknown values dropped). */
export function sortScopes(scopes: readonly SsrfScope[]): SsrfScope[] {
  const set = new Set(scopes);
  return ALL_SCOPES.filter((s) => set.has(s));
}

/** Adds or removes one scope of a row. */
export function toggleScope(
  scopes: readonly SsrfScope[],
  scope: SsrfScope,
  on: boolean,
): SsrfScope[] {
  return sortScopes(on ? [...scopes, scope] : scopes.filter((s) => s !== scope));
}

/** The form of an entry used to compare lists (roughly the server's canonical form). */
export function canonical(kind: ListKind, value: string): string {
  const v = value.trim().toLowerCase();
  return kind === "hosts" ? v.replace(/\.$/, "") : v;
}

/** The non-empty entries of a draft list, trimmed, in order, with sorted scopes. */
export function entries(rows: readonly Row[]): Entry[] {
  return rows
    .filter((r) => r.value.trim() !== "")
    .map((r) => ({ value: r.value.trim(), scopes: sortScopes(r.scopes) }));
}

/** What a draft adds over the saved list: a new value (every scope) or new scopes of a kept one. */
export interface Widening {
  kind: ListKind;
  value: string;
  /** True for a value that was not stored before. */
  isNew: boolean;
  /** The scopes it gains. */
  scopes: SsrfScope[];
}

function scopesByValue(kind: ListKind, list: readonly Entry[]): Map<string, Set<SsrfScope>> {
  const out = new Map<string, Set<SsrfScope>>();
  for (const e of list) {
    const c = canonical(kind, e.value);
    const set = out.get(c) ?? new Set<SsrfScope>();
    for (const s of e.scopes) set.add(s);
    out.set(c, set);
  }
  return out;
}

/** Entries of next that widen egress over saved (new values or gained scopes). */
export function widenings(
  kind: ListKind,
  saved: readonly Entry[],
  next: readonly Entry[],
): Widening[] {
  const have = scopesByValue(kind, saved);
  const out: Widening[] = [];
  const seen = new Set<string>();
  for (const e of next) {
    const c = canonical(kind, e.value);
    if (seen.has(c)) continue;
    seen.add(c);
    const before = have.get(c);
    const gained = sortScopes(e.scopes).filter((s) => !before?.has(s));
    if (gained.length > 0) {
      out.push({ kind, value: e.value.trim(), isNew: before === undefined, scopes: gained });
    }
  }
  return out;
}

/** True when the draft differs from the saved list (order-insensitive, canonical, scopes included). */
export function changed(kind: ListKind, saved: readonly Entry[], next: readonly Entry[]): boolean {
  const flat = (m: Map<string, Set<SsrfScope>>) =>
    [...m.entries()].map(([v, s]) => `${v}=${sortScopes([...s]).join(",")}`).sort();
  const a = flat(scopesByValue(kind, saved));
  const b = flat(scopesByValue(kind, next));
  return a.length !== b.length || a.some((v, i) => v !== b[i]);
}

const IPV4 = /^\d{1,3}(\.\d{1,3}){3}$/;
const IPV6ISH = /^[0-9a-f:]+$/i;

/** An early hint for an entry (null: looks plausible; the server decides). */
export function hint(kind: ListKind, value: string): string | null {
  const v = value.trim();
  if (v === "") return null;
  if (kind === "cidrs") {
    const [addr, bits, extra] = v.split("/");
    if (bits === undefined || extra !== undefined || addr === undefined || addr === "") {
      return "Use CIDR notation, e.g. 10.20.0.0/16 or 192.0.2.10/32.";
    }
    if (!/^\d{1,3}$/.test(bits)) return "The prefix length must be a number.";
    if (Number(bits) === 0) return "/0 would match every address and is never accepted.";
    if (!IPV4.test(addr) && !(addr.includes(":") && IPV6ISH.test(addr))) {
      return "The address part must be an IPv4 or IPv6 address.";
    }
    return null;
  }
  if (/[/:@\s]/.test(v) || v.includes("://")) {
    return "A host name only: no scheme, port, path or spaces.";
  }
  if (IPV4.test(v)) return "IP addresses go into the CIDR list (e.g. with /32).";
  if (v.startsWith("*.") && !v.slice(2).includes(".")) {
    return "A wildcard needs at least two labels, e.g. *.corp.example.";
  }
  if (v.includes("*") && !v.startsWith("*.")) return "Wildcards are only allowed as *.suffix.";
  return null;
}

/** A hint for the scopes of a non-empty entry (null: fine). */
export function scopeHint(scopes: readonly SsrfScope[]): string | null {
  return scopes.length === 0 ? "Pick at least one use this entry allows." : null;
}

export interface FieldRef {
  kind: ListKind;
  index: number;
  part: "value" | "scopes";
}

/**
 * Splits a server field ("cidrEntries.3.scopes", "hostEntries.0.value",
 * legacy "cidrs.3") into its list, row index and part.
 */
export function fieldRow(field: string | undefined): FieldRef | null {
  const m = /^(cidrs|hosts|cidrEntries|hostEntries)\.(\d+)(?:\.(value|scopes))?$/.exec(field ?? "");
  if (!m?.[1] || !m[2]) return null;
  const kind: ListKind = m[1].startsWith("cidr") ? "cidrs" : "hosts";
  return { kind, index: Number(m[2]), part: m[3] === "scopes" ? "scopes" : "value" };
}

/** The list a list-level field ("cidrEntries", "hosts") names, or null. */
export function fieldList(field: string | undefined): ListKind | null {
  switch (field) {
    case "cidrs":
    case "cidrEntries":
      return "cidrs";
    case "hosts":
    case "hostEntries":
      return "hosts";
    default:
      return null;
  }
}
