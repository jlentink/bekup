/** An age X25519 identity: "AGE-SECRET-KEY-1" + 58 bech32 characters. */
const IDENTITY_RE = /^AGE-SECRET-KEY-1[0-9A-Z]{58}$/;

/**
 * The identity line of a pasted key or an age-keygen file (comments, the
 * public key line and blank lines are skipped). null unless there is
 * exactly one identity: an ambiguous file is refused, never guessed.
 */
export function extractIdentity(text: string): string | null {
  const keys = text
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter((l) => l !== "" && !l.startsWith("#") && IDENTITY_RE.test(l));
  return keys.length === 1 ? (keys[0] ?? null) : null;
}
