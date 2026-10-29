/** Query parameter that carries the page to return to after signing in. */
export const RETURN_PARAM = "next";

const MAX_LENGTH = 2048;

/**
 * Validates a return path from the URL: only a same-origin, relative path
 * ("/jobs?x=1#y") is accepted, never a scheme, a protocol-relative
 * "//host", backslashes (browsers treat "/\host" like "//host"), control
 * characters or the login pages themselves. Anything else yields null and
 * the caller falls back to the dashboard (open redirect protection).
 */
export function safeReturnPath(
  raw: string | null | undefined,
  origin: string = window.location.origin,
): string | null {
  if (!raw || raw.length > MAX_LENGTH) return null;
  if (!raw.startsWith("/") || raw.startsWith("//")) return null;
  // eslint-disable-next-line no-control-regex -- rejecting control characters is the point
  if (/[\\\u0000-\u001f\u007f]/.test(raw)) return null;
  let url: URL;
  try {
    url = new URL(raw, origin);
  } catch {
    return null;
  }
  if (url.origin !== new URL(origin).origin) return null;
  // Re-check after normalisation: dot segments can collapse into "//host"
  // ("/a/..//evil", "/.//evil", "/%2e//evil"), which a later navigation or
  // a server redirect could read as protocol-relative.
  if (url.pathname.startsWith("//") || url.pathname.includes("\\")) return null;
  if (/%5c/i.test(url.pathname)) return null;
  if (url.pathname === "/login" || url.pathname.startsWith("/login/")) return null;
  return url.pathname + url.search + url.hash;
}

/** The login URL that returns to path afterwards (when path is safe). */
export function loginPathFor(path: string, origin?: string): string {
  const safe = safeReturnPath(path, origin);
  if (!safe || safe === "/") return "/login";
  return `/login?${RETURN_PARAM}=${encodeURIComponent(safe)}`;
}

/** Appends the return parameter of the current URL to another auth path. */
export function withReturnParam(path: string, search: string, origin?: string): string {
  const next = safeReturnPath(new URLSearchParams(search).get(RETURN_PARAM), origin);
  return next && next !== "/" ? `${path}?${RETURN_PARAM}=${encodeURIComponent(next)}` : path;
}
