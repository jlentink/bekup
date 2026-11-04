import type { Tone } from "@/components/status/tag";
import type { WebdavTrust } from "@/graphql/gen/graphql";

/** A WebDAV server certificate as the API returns it (public data). */
export interface WebdavCert {
  spkiSha256: string;
  fingerprintSha256: string;
  notBefore: string;
  notAfter: string;
  selfSigned: boolean;
  pinnedAt?: string | null;
}

export const TRUST_TONE: Record<WebdavTrust, Tone> = {
  SYSTEM: "neutral",
  CA_BUNDLE: "info",
  PINNED: "success",
  PLAIN_HTTP: "insecure",
};

/** Hex split into groups of `size` characters (fingerprints compared by eye). */
export function hexGroups(hex: string, size = 4): string[] {
  const clean = hex.replace(/[^0-9a-f]/gi, "").toLowerCase();
  const out: string[] = [];
  for (let i = 0; i < clean.length; i += size) out.push(clean.slice(i, i + size));
  return out;
}

/** Hex as colon-separated upper-case pairs, as browsers and openssl show it. */
export function colonHex(hex: string): string {
  return hexGroups(hex, 2).join(":").toUpperCase();
}
