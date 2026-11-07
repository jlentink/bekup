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
 * Stores the choice and reloads, so every table and formatter is rebuilt
 * in the new language. `null` returns to following the browser.
 */
export function setLocale(
  next: Locale | null,
  reload: () => void = () => {
    window.location.reload();
  },
): void {
  try {
    if (next === null) window.localStorage.removeItem(LOCALE_STORAGE_KEY);
    else window.localStorage.setItem(LOCALE_STORAGE_KEY, next);
  } catch {
    return;
  }
  reload();
}

/** True while the user has picked a language explicitly. */
export function hasStoredLocale(): boolean {
  return isLocale(readStored());
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
