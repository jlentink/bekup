/** How long a code read from a link waits in memory for a sign-in. */
const PENDING_TTL_MS = 15 * 60_000;

/**
 * The code of the link, in this tab's memory only (never storage, never a
 * URL): it survives the in-app detour through the sign-in page, which
 * returns to /verify-email without the fragment, and is used once.
 */
let pending: { code: string; until: number } | null = null;

export function takePending(): string | null {
  const p = pending;
  if (!p || p.until < Date.now()) {
    pending = null;
    return null;
  }
  return p.code;
}

/** Keeps the code of the link for PENDING_TTL_MS. */
export function holdPending(code: string) {
  pending = { code, until: Date.now() + PENDING_TTL_MS };
}

/** Forgets the code (used, refused, or a test hook for a code left over from another test). */
export function resetPendingVerification() {
  pending = null;
}
