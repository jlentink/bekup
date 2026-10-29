/**
 * Advisory password strength estimate for the strength meter. It mirrors
 * the server's policy (internal/auth/policy.go: at least 12 characters, at
 * most 1024, no e-mail address or name parts, zxcvbn score >= 3) closely
 * enough to guide the user, but the server's check is authoritative: its
 * VALIDATION message is shown on the field when it refuses a password.
 */

/** Server minimum (auth.MinPasswordLength); an operator may raise it. */
export const MIN_PASSWORD_LENGTH = 12;
export const MAX_PASSWORD_LENGTH = 1024;
/** Server minimum zxcvbn score (auth.MinPasswordScore). */
export const MIN_ACCEPTED_SCORE = 3;

export type StrengthHint = "length" | "variety" | "repeat";

export interface StrengthEstimate {
  /** 0 (very weak) to 4 (very strong), like zxcvbn. */
  score: 0 | 1 | 2 | 3 | 4;
  /** Rough entropy estimate in bits. */
  bits: number;
  tooShort: boolean;
  tooLong: boolean;
  containsAccount: boolean;
  hints: StrengthHint[];
}

const SEQUENCES = [
  "abcdefghijklmnopqrstuvwxyz",
  "0123456789",
  "qwertyuiop",
  "asdfghjkl",
  "zxcvbnm",
];

/** Very common password words; each occurrence counts as one character. */
const COMMON_WORDS = [
  "password",
  "passwort",
  "welcome",
  "letmein",
  "qwerty",
  "admin",
  "login",
  "secret",
  "iloveyou",
  "monkey",
  "dragon",
  "master",
  "sunshine",
  "princess",
  "football",
  "baseball",
  "shadow",
  "summer",
  "winter",
  "spring",
  "autumn",
  "backup",
  "bekup",
  "database",
  "postgres",
  "mysql",
  "mariadb",
  "changeme",
];

function charsetSize(pw: string): number {
  let size = 0;
  if (/[a-z]/.test(pw)) size += 26;
  if (/[A-Z]/.test(pw)) size += 26;
  if (/[0-9]/.test(pw)) size += 10;
  if (/[^A-Za-z0-9\s]/.test(pw)) size += 33;
  if (/\s/.test(pw)) size += 1;
  if (/[^\p{ASCII}]/u.test(pw)) size += 100;
  return Math.max(size, 1);
}

/** Characters that only repeat or continue a sequence add little. */
function effectiveLength(pw: string): { length: number; repetitive: boolean } {
  let lower = pw.toLowerCase();
  for (const word of COMMON_WORDS) lower = lower.split(word).join("§");
  let weak = 0;
  for (let i = 1; i < lower.length; i++) {
    const prev = lower.charAt(i - 1);
    const cur = lower.charAt(i);
    const pair = prev + cur;
    if (prev === cur || SEQUENCES.some((s) => s.includes(pair))) weak++;
  }
  const unique = new Set(lower).size;
  const repeatedUnit = /^(.{1,8})\1+$/su.test(lower);
  const repetitive =
    repeatedUnit || weak > lower.length / 3 || unique < Math.min(6, lower.length / 2);
  return { length: Math.max(1, lower.length - weak * 0.75), repetitive };
}

/** The account strings the server searches for in a password (identityParts). */
export function accountParts(account: readonly string[]): string[] {
  const out: string[] = [];
  const add = (s: string) => {
    const v = s.trim().toLowerCase();
    if (Array.from(v).length >= 3) out.push(v);
  };
  for (const a of account) {
    add(a);
    const at = a.indexOf("@");
    if (at > 0) add(a.slice(0, at));
    const words = a.split(/[^\p{L}\p{N}]+/u).filter(Boolean);
    if (words.length > 1) words.forEach(add);
  }
  return out;
}

export function estimateStrength(
  password: string,
  account: readonly string[] = [],
  minLength: number = MIN_PASSWORD_LENGTH,
): StrengthEstimate {
  const n = Array.from(password).length;
  const lower = password.toLowerCase();
  const containsAccount = accountParts(account).some((p) => lower.includes(p));
  const { length, repetitive } = effectiveLength(password);
  const bits = Math.round(length * Math.log2(charsetSize(password)));
  let score: StrengthEstimate["score"] =
    bits < 30 ? 0 : bits < 45 ? 1 : bits < 60 ? 2 : bits < 80 ? 3 : 4;
  const tooShort = n < minLength;
  const tooLong = n > MAX_PASSWORD_LENGTH;
  if (tooShort || containsAccount) score = Math.min(score, 1) as StrengthEstimate["score"];
  if (repetitive) score = Math.min(score, 2) as StrengthEstimate["score"];
  const hints: StrengthHint[] = [];
  if (n < 16) hints.push("length");
  if (charsetSize(password) < 40 && n < 20) hints.push("variety");
  if (repetitive) hints.push("repeat");
  return { score, bits, tooShort, tooLong, containsAccount, hints };
}
