import type { SecondFactorKind } from "@/graphql/gen/graphql";

/**
 * The second factors a pending login may use (LoginResult.availableFactors)
 * survive a reload of the verify page in sessionStorage. They are factor
 * kinds only, nothing secret; cleared once the login completes.
 */
const KEY = "bekup.pendingFactors";
const KINDS: readonly SecondFactorKind[] = ["PASSKEY", "TOTP", "RECOVERY_CODE"];

export function storeFactors(factors: readonly SecondFactorKind[]): void {
  try {
    window.sessionStorage.setItem(KEY, JSON.stringify(factors));
  } catch {
    /* storage unavailable: the verify page offers every kind */
  }
}

/** The stored factors in the order passkey, TOTP, recovery code; all kinds when unknown. */
export function loadFactors(): SecondFactorKind[] {
  let stored: unknown;
  try {
    stored = JSON.parse(window.sessionStorage.getItem(KEY) ?? "null");
  } catch {
    stored = null;
  }
  if (!Array.isArray(stored)) return [...KINDS];
  const set = new Set(
    stored.filter((v): v is SecondFactorKind => KINDS.includes(v as SecondFactorKind)),
  );
  const out = KINDS.filter((k) => set.has(k));
  return out.length > 0 ? out : [...KINDS];
}

export function clearFactors(): void {
  try {
    window.sessionStorage.removeItem(KEY);
  } catch {
    /* ignore */
  }
}
