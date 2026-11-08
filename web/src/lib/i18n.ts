/**
 * The UI language (D-235): English and Dutch. Strings stay in typed tables
 * next to their components; every table is written once per language with
 * `defineMessages({ en, nl })`, so the compiler refuses a Dutch table that
 * misses a key, adds one or changes a parameter.
 *
 * The language is chosen once per page load (stored choice, else the
 * browser's languages, else English) and never changes while the page
 * runs: switching reloads the page (`setLocale`). That keeps module-level
 * constants built from strings (tab lists, schemas) consistent without a
 * re-render mechanism.
 *
 * This module must stay free of imports from features: message modules
 * import it, and it is evaluated before them.
 */

export const LOCALES = ["en", "nl"] as const;
export type Locale = (typeof LOCALES)[number];

/** localStorage key of the explicit choice (absent = follow the browser). */
export const LOCALE_STORAGE_KEY = "bekup.locale";

/** Each language named in itself (never translated). */
export const LOCALE_NAMES: Readonly<Record<Locale, string>> = {
  en: "English",
  nl: "Nederlands",
};

export function isLocale(value: unknown): value is Locale {
  return typeof value === "string" && (LOCALES as readonly string[]).includes(value);
}

/** The base language of a BCP 47 tag ("nl-BE" → "nl"), lower case. */
function baseOf(tag: string): string {
  return tag.trim().toLowerCase().split(/[-_]/, 1)[0] ?? "";
}

/**
 * The page's language: a valid stored choice wins, then the first browser
 * language bekup has, then English.
 */
export function detectLocale(stored: string | null, languages: readonly string[]): Locale {
  if (isLocale(stored)) return stored;
  for (const tag of languages) {
    const base = baseOf(tag);
    if (isLocale(base)) return base;
  }
  return "en";
}

/**
 * The locale for `Intl` formatting: the browser's own tag when it has the
 * UI language as its base (keeps "en-GB" dates for a British browser);
 * else the UI language with the region of the browser's first language
 * (a German browser with the English UI formats like "en-DE": 24-hour
 * clock, day first); else the UI language itself. Unicode extensions
 * (calendar, numbering system) are dropped.
 */
export function detectIntlLocale(ui: Locale, languages: readonly string[]): string {
  for (const tag of languages) {
    if (baseOf(tag) !== ui) continue;
    try {
      return new Intl.Locale(tag).baseName;
    } catch {
      return ui;
    }
  }
  const first = languages[0];
  if (first === undefined) return ui;
  try {
    const region = new Intl.Locale(first).maximize().region;
    return region ? new Intl.Locale(ui, { region }).baseName : ui;
  } catch {
    return ui;
  }
}

function readStored(): string | null {
  try {
    return window.localStorage.getItem(LOCALE_STORAGE_KEY);
  } catch {
    return null;
  }
}

function browserLanguages(): readonly string[] {
  try {
    if (typeof navigator === "undefined") return [];
    const langs = navigator.languages as readonly string[] | undefined;
    if (langs && langs.length > 0) return langs;
    return navigator.language ? [navigator.language] : [];
  } catch {
    return [];
  }
}

/** The language of this page load. */
export const locale: Locale = detectLocale(readStored(), browserLanguages());

/** The locale every `Intl` formatter uses (see `lib/format`). */
export const intlLocale: string = detectIntlLocale(locale, browserLanguages());

if (typeof document !== "undefined") document.documentElement.lang = locale;

/**
 * The shape of a message table with every string literal widened to
 * `string` (also in function results, arrays and tuples), so a translation
 * only has to match keys, parameters and structure.
 */
export type Messages<T> = T extends string
  ? string
  : T extends (...args: infer A) => infer R
    ? (...args: A) => Messages<R>
    : T extends object
      ? { -readonly [K in keyof T]: Messages<T[K]> }
      : T;

/**
 * One message table in every language; returns the table of this page
 * load. The shape comes from the English table; the Dutch one must have
 * exactly the same keys, parameters and structure (`Messages`). For a
 * large module write `const en = {...}; const nl: Messages<typeof en> =
 * {...}; export const messages = defineMessages({ en, nl });`.
 */
export function defineMessages<M>(tables: { en: M; nl: NoInfer<Messages<M>> }): Messages<M> {
  if (import.meta.env.MODE === "test") definedTables.push(tables);
  return tables[locale] as Messages<M>;
}

/**
 * Every table defined so far, recorded only under vitest: the parity test
 * (`i18n-parity.test.ts`) checks what the compiler cannot, e.g. a Dutch
 * function that takes fewer parameters than the English one.
 */
export const definedTables: { en: unknown; nl: unknown }[] = [];

/**
 * localStorage key recording that the stored language is the account's
 * (D-236: copied from or saved to `users.locale`). Absent for a choice
 * made in this browser only (D-235), which an account without a choice
 * leaves alone.
 */
export const LOCALE_SOURCE_KEY = "bekup.locale.source";

/**
 * Reloads the page; `run` is replaceable in tests (jsdom cannot navigate)
 * and `pending` records that this page already asked for a reload.
 */
export const pageReload = {
  pending: false,
  run: (): void => {
    window.location.reload();
  },
};

/** True once this page asked for a reload into another language. */
export function isReloadPending(): boolean {
  return pageReload.pending;
}

/**
 * Stores the choice (`null`: follow the browser again) and, unless
 * `reload` is false, reloads so every table and formatter is rebuilt in
 * the new language. `fromAccount` marks the stored value as the account's.
 * False (and no reload) when the browser refuses the storage.
 */
export function setLocale(
  next: Locale | null,
  options: { fromAccount?: boolean; reload?: boolean } = {},
): boolean {
  try {
    if (next === null) {
      window.localStorage.removeItem(LOCALE_STORAGE_KEY);
      window.localStorage.removeItem(LOCALE_SOURCE_KEY);
    } else {
      window.localStorage.setItem(LOCALE_STORAGE_KEY, next);
      if (options.fromAccount) window.localStorage.setItem(LOCALE_SOURCE_KEY, "account");
      else window.localStorage.removeItem(LOCALE_SOURCE_KEY);
    }
  } catch {
    return false;
  }
  if (options.reload !== false) {
    pageReload.pending = true;
    pageReload.run();
  }
  return true;
}

/** True when the stored language came from the account (D-236). */
export function storedFromAccount(): boolean {
  try {
    return window.localStorage.getItem(LOCALE_SOURCE_KEY) === "account";
  } catch {
    return false;
  }
}

/** The language this browser would get without any stored choice. */
export function browserLocale(): Locale {
  return detectLocale(null, browserLanguages());
}

/** The API's `Locale` enum ("EN", "NL") as a page locale; null for none. */
export function localeFromApi(value: string | null | undefined): Locale | null {
  const l = value?.toLowerCase();
  return isLocale(l) ? l : null;
}

/** A page locale as the API's `Locale` enum. */
export function localeToApi(l: Locale): "EN" | "NL" {
  return l === "nl" ? "NL" : "EN";
}

/** True while the user has picked a language explicitly. */
export function hasStoredLocale(): boolean {
  return isLocale(readStored());
}

/**
 * True when this browser lets bekup store the language (checked once per
 * page load). Without it the page cannot follow the account's choice.
 */
export const canStoreLocale: boolean = storageWorks();

/**
 * Probes the storage now (it can fail later than at page load: quota,
 * a privacy setting changed). Cheap and idempotent.
 */
export function storageWorks(): boolean {
  try {
    const probe = `${LOCALE_STORAGE_KEY}.probe`;
    window.localStorage.setItem(probe, "1");
    window.localStorage.removeItem(probe);
    return true;
  } catch {
    return false;
  }
}

/** The language stored in this browser right now (null: none or invalid). */
export function storedLocale(): Locale | null {
  const s = readStored();
  return isLocale(s) ? s : null;
}

const pluralRules = new Intl.PluralRules(intlLocale);

/**
 * Picks the plural form for `n` in the page's language (`one` and `other`
 * are enough for English and Dutch). Use inside message functions:
 * `files: (n: number) => plural(n, { one: "1 file", other: `${n} files` })`.
 */
export function plural(n: number, forms: { one: string; other: string }): string {
  return pluralRules.select(n) === "one" ? forms.one : forms.other;
}
