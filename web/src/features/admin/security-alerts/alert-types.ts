/** Security alerts per page (filtered by the server to group SECURITY, D-196). */
export const ALERT_PAGE = 50;

const APP_PATHS: readonly RegExp[] = [
  /^\/(runs|jobs|restores|connections|destinations)(\/[0-9a-f-]{36})?$/,
  /^\/profile(\/[a-z-]+)?$/,
  /^\/admin(\/[a-z-]+){1,2}$/,
];

/**
 * The in-app path of an alert link (absolute under server.base_url): only
 * the path, and only for known pages, so a link never leaves the app.
 */
export function alertPath(link: string | null | undefined): string | null {
  if (!link || link.length > 2048) return null;
  let url: URL;
  try {
    url = new URL(link, "https://bekup.invalid");
  } catch {
    return null;
  }
  if (url.protocol !== "https:" && url.protocol !== "http:") return null;
  const path = url.pathname.toLowerCase();
  return APP_PATHS.some((re) => re.test(path)) ? path : null;
}
