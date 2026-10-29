export const TOTP_DIGITS = 6;

/** Keeps only digits, at most TOTP_DIGITS of them (pasted "123 456" works). */
export function normalizeTotp(value: string): string {
  return value.replace(/\D/g, "").slice(0, TOTP_DIGITS);
}
