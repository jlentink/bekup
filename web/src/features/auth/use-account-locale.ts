import { useEffect } from "react";

import {
  browserLocale,
  isReloadPending,
  locale,
  localeFromApi,
  setLocale,
  storageWorks,
  storedFromAccount,
  storedLocale,
  type Locale,
} from "@/lib/i18n";

/** What this browser must do to follow the account's language. */
export type AccountLocaleAction =
  /** Nothing: already in line. */
  | "none"
  /** Store the account's language and reload into it. */
  | "switch"
  /** Already shown: store it quietly as the account's. */
  | "adopt"
  /** The account has no choice any more: drop the copy, no reload needed. */
  | "forget"
  /** As forget, but the browser's language differs: reload. */
  | "forget-reload";

/**
 * Decides how a browser follows the account's language (D-236). An
 * explicit account choice wins; when the account has none, only a value
 * that was copied from the account is dropped (a choice made in this
 * browser alone stays). Without storage nothing can be followed.
 */
export function accountLocaleAction(f: {
  /** The account's choice; undefined while it is not known yet. */
  target: Locale | null | undefined;
  page: Locale;
  stored: Locale | null;
  fromAccount: boolean;
  browser: Locale;
  canStore: boolean;
}): AccountLocaleAction {
  if (!f.canStore || f.target === undefined) return "none";
  if (f.target !== null) {
    if (f.target !== f.page) return "switch";
    return f.stored === f.target && f.fromAccount ? "none" : "adopt";
  }
  if (!f.fromAccount || f.stored === null) return "none";
  return f.browser === f.page ? "forget" : "forget-reload";
}

/**
 * Follows the language stored on the account after sign-in. `apiLocale`
 * is `me.locale` once `me` is loaded and undefined before (never act on a
 * missing answer). Returns true while the page is about to reload into
 * another language (render a placeholder instead of the app).
 */
export function useAccountLocale(apiLocale: string | null | undefined): boolean {
  const target = apiLocale === undefined ? undefined : localeFromApi(apiLocale);
  const action = accountLocaleAction({
    target,
    page: locale,
    stored: storedLocale(),
    fromAccount: storedFromAccount(),
    browser: browserLocale(),
    // Probed per render: storage that fails mid-session never leaves the
    // placeholder up without a reload.
    canStore: storageWorks(),
  });
  useEffect(() => {
    if (action === "none" || isReloadPending() || target === undefined) return;
    if (action === "switch") setLocale(target, { fromAccount: true });
    else if (action === "adopt") setLocale(target, { fromAccount: true, reload: false });
    else if (action === "forget") setLocale(null, { reload: false });
    else setLocale(null);
  }, [action, target]);
  return action === "switch" || action === "forget-reload";
}
