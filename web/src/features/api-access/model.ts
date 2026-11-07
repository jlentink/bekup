/**
 * Client-side helpers of the API access UI (D-220). The server decides
 * (internal/apiaccess.ParseAddresses, the 1-365 day expiry or never,
 * D-229); these checks
 * only give early, local feedback with the same rules.
 */

/** The most networks a token may list (the database CHECK). */
export const MAX_ADDRESSES = 32;

/** Expiry presets in days; 90 is the default (D-220). */
export const EXPIRY_PRESETS = [30, 90, 180, 365] as const;
export type ExpiryDays = (typeof EXPIRY_PRESETS)[number];
/** A preset, or "never": a token without expiry (D-229, alerted). */
export type ExpiryPreset = ExpiryDays | "never";
export const EXPIRY_CHOICES: readonly ExpiryPreset[] = [...EXPIRY_PRESETS, "never"];
export const DEFAULT_EXPIRY: ExpiryPreset = 90;

/** Tokens expiring within this many days are flagged. */
export const EXPIRY_WARNING_DAYS = 7;

const DAY_MS = 24 * 60 * 60 * 1000;

/** expiresAt (ISO) for a preset, counted from now. */
export function expiresAtFor(days: ExpiryDays, now: number = Date.now()): string {
  return new Date(now + days * DAY_MS).toISOString();
}

/** The create input's expiry fields for a choice: exactly one of the two. */
export function expiryInput(
  choice: ExpiryPreset,
  now: number = Date.now(),
): {
  expiresAt: string | null;
  neverExpires: boolean;
} {
  if (choice === "never") return { expiresAt: null, neverExpires: true };
  return { expiresAt: expiresAtFor(choice, now), neverExpires: false };
}

/** Splits the address editor's text: one entry per line or comma. */
export function splitAddresses(text: string): string[] {
  return text
    .split(/[\n,]/)
    .map((s) => s.trim())
    .filter((s) => s !== "");
}

function ipv4Octets(s: string): number[] | null {
  const parts = s.split(".");
  if (parts.length !== 4) return null;
  const out: number[] = [];
  for (const p of parts) {
    if (!/^(0|[1-9]\d{0,2})$/.test(p)) return null;
    const n = Number(p);
    if (n > 255) return null;
    out.push(n);
  }
  return out;
}

/**
 * The widest networks an address list may hold (apiaccess.MinPrefixV4/V6):
 * anything wider must be chosen as "Allow from any address".
 */
const MIN_PREFIX_V4 = 8;
const MIN_PREFIX_V6 = 32;

/** A plausible IPv6 address (the server parses it exactly). */
function looksIPv6(s: string): boolean {
  if (!s.includes(":") || s.includes("%")) return false;
  if ((s.match(/::/g) ?? []).length > 1) return false;
  return /^[0-9a-f:.]+$/i.test(s);
}

/**
 * Checks the entries like the server: 1-32 IPv4/IPv6 addresses or
 * networks, none wider than /8 (IPv4) or /32 (IPv6), no host bits
 * (IPv4), no IPv4-mapped IPv6, no duplicates. Returns the problem text, or null when the list looks fine.
 */
export function addressProblem(entries: readonly string[]): string | null {
  if (entries.length === 0) {
    return "Add at least one network the token may be used from, or explicitly allow any address.";
  }
  if (entries.length > MAX_ADDRESSES) return `At most ${String(MAX_ADDRESSES)} networks per token.`;
  const seen = new Set<string>();
  for (const [i, raw] of entries.entries()) {
    const n = i + 1;
    const [addr = "", bits, extra] = raw.split("/");
    if (extra !== undefined || addr === "")
      return `Entry ${String(n)} (${raw}) is not an address or network.`;
    const v4 = ipv4Octets(addr);
    const max = v4 ? 32 : 128;
    if (!v4 && !looksIPv6(addr)) {
      return `Entry ${String(n)} (${raw}) is not an IPv4 or IPv6 address or network such as 203.0.113.0/24.`;
    }
    if (!v4 && /^::ffff:/i.test(addr)) {
      return `Entry ${String(n)} (${raw}) is an IPv4-mapped IPv6 address: write it in the IPv4 form.`;
    }
    let len = max;
    if (bits !== undefined) {
      if (!/^\d{1,3}$/.test(bits) || Number(bits) > max) {
        return `Entry ${String(n)} (${raw}) has an invalid prefix length.`;
      }
      len = Number(bits);
    }
    if (len === 0) {
      return `Entry ${String(n)} (${raw}) allows every address: choose "Allow from any address" instead.`;
    }
    const minLen = v4 ? MIN_PREFIX_V4 : MIN_PREFIX_V6;
    if (len < minLen) {
      return `Entry ${String(n)} (${raw}) is wider than /${String(minLen)}: a network that wide is as good as any address; choose "Allow from any address" instead or narrow it.`;
    }
    if (v4) {
      const value = v4.reduce((acc, o) => acc * 256 + o, 0);
      const size = 2 ** (32 - len);
      if (value % size !== 0) {
        const net = value - (value % size);
        const dotted = [24, 16, 8, 0].map((s) => String(Math.floor(net / 2 ** s) % 256)).join(".");
        return `Entry ${String(n)} (${raw}) has bits set after the prefix length: did you mean ${dotted}/${String(len)}?`;
      }
    }
    const key = `${v4 ? v4.join(".") : addr.toLowerCase()}/${String(len)}`;
    if (seen.has(key)) return `Entry ${String(n)} (${raw}) is listed twice.`;
    seen.add(key);
  }
  return null;
}

/** Whole days until iso (negative once past). */
export function daysUntil(iso: string, now: number = Date.now()): number {
  return Math.floor((Date.parse(iso) - now) / DAY_MS);
}

/** The lifecycle state of a token. */
export type TokenState = "active" | "expiring" | "expired" | "revoked";

export function tokenState(
  t: { expiresAt?: string | null | undefined; revokedAt?: string | null | undefined },
  now: number = Date.now(),
): TokenState {
  if (t.revokedAt) return "revoked";
  // No expiry (D-229): active until revoked.
  if (!t.expiresAt) return "active";
  const ms = Date.parse(t.expiresAt) - now;
  if (ms <= 0) return "expired";
  if (ms < EXPIRY_WARNING_DAYS * DAY_MS) return "expiring";
  return "active";
}
