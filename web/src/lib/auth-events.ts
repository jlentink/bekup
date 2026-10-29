/**
 * A tiny bus from the Apollo error link (outside React) to the router
 * (inside React): the link reports that the session changed, RootLayout
 * reacts (clear the cache, navigate). Events carry no data.
 *
 * - "unauthenticated": the session ended or never existed → login page.
 * - "second-factor-required": the login waits for its second factor.
 * - "restricted": the session became restricted (temporary password or
 *   required 2FA enrollment) → refresh `me` so the gate shows the flow.
 * - "session-check": a live stream ended with a session signal; ask the
 *   server (`me`) before acting, because a password change revokes the
 *   stream's old session while this browser keeps a new one.
 */
export type AuthEvent =
  "unauthenticated" | "second-factor-required" | "restricted" | "session-check";

type Listener = (event: AuthEvent) => void;

const listeners = new Set<Listener>();

export function emitAuthEvent(event: AuthEvent): void {
  for (const l of [...listeners]) l(event);
}

/** Subscribes to auth events; returns the unsubscribe function. */
export function onAuthEvent(listener: Listener): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}
