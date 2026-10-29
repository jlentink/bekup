import type { FetchConnectionCertificateMutation } from "@/graphql/gen/graphql";

export type Chain = FetchConnectionCertificateMutation["fetchCertificate"];
export type ChainCert = Chain["certificates"][number];

const DAY = 86_400_000;

/** Validity of a certificate relative to now. */
export function validity(
  cert: Pick<ChainCert, "notBefore" | "notAfter">,
  now = Date.now(),
): "not-yet" | "expired" | "7d" | "30d" | "ok" {
  const from = Date.parse(cert.notBefore);
  const to = Date.parse(cert.notAfter);
  if (from > now) return "not-yet";
  if (to <= now) return "expired";
  if (to - now < 7 * DAY) return "7d";
  if (to - now < 30 * DAY) return "30d";
  return "ok";
}

/** What a certificate is in the chain (leaf first). */
export function chainRole(cert: ChainCert, index: number): string {
  if (index === 0) return cert.selfSigned ? "Self-signed server certificate" : "Server certificate";
  if (cert.selfSigned) return "Root CA";
  return "Intermediate CA";
}

/** Only a self-signed leaf or a CA can be an anchor (D-139). */
export function canAnchor(cert: ChainCert, index: number): boolean {
  return cert.isCa || (index === 0 && cert.selfSigned);
}
