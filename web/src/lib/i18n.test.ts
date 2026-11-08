import { afterEach, describe, expect, it, vi } from "vitest";

import {
  LOCALE_STORAGE_KEY,
  defineMessages,
  detectIntlLocale,
  detectLocale,
  hasStoredLocale,
  isLocale,
  plural,
  setLocale,
  type Messages,
} from "./i18n";

describe("detectLocale", () => {
  it.each([
    { stored: "nl", languages: ["en-US"], want: "nl" },
    { stored: "en", languages: ["nl-NL"], want: "en" },
    { stored: null, languages: ["nl-BE", "en"], want: "nl" },
    { stored: null, languages: ["de-DE", "nl"], want: "nl" },
    { stored: null, languages: ["NL_nl"], want: "nl" },
    { stored: null, languages: ["de-DE", "fr"], want: "en" },
    { stored: null, languages: [], want: "en" },
    { stored: "xx", languages: ["nl"], want: "nl" },
    { stored: "", languages: [], want: "en" },
  ])("$stored + $languages -> $want", ({ stored, languages, want }) => {
    expect(detectLocale(stored, languages)).toBe(want);
  });
});

describe("detectIntlLocale", () => {
  it("keeps the browser's region for the UI language", () => {
    expect(detectIntlLocale("en", ["en-GB", "nl"])).toBe("en-GB");
    expect(detectIntlLocale("nl", ["en-GB", "nl-BE"])).toBe("nl-BE");
  });
  it("drops Unicode extensions from the browser's tag", () => {
    expect(detectIntlLocale("nl", ["nl-NL-u-ca-buddhist-nu-arab"])).toBe("nl-NL");
  });
  it("keeps the browser's region with another UI language", () => {
    expect(detectIntlLocale("en", ["de-DE"])).toBe("en-DE");
    expect(detectIntlLocale("en", ["de"])).toBe("en-DE");
    expect(detectIntlLocale("nl", ["en-GB"])).toBe("nl-GB");
  });
  it("falls back to the UI language", () => {
    expect(detectIntlLocale("en", [])).toBe("en");
    expect(detectIntlLocale("nl", ["not a tag"])).toBe("nl");
  });
});

describe("isLocale", () => {
  it("accepts only supported languages", () => {
    expect(isLocale("en")).toBe(true);
    expect(isLocale("nl")).toBe(true);
    expect(isLocale("de")).toBe(false);
    expect(isLocale(undefined)).toBe(false);
  });
});

describe("plural", () => {
  it("picks one and other", () => {
    expect(plural(1, { one: "1 file", other: "n files" })).toBe("1 file");
    expect(plural(0, { one: "1 file", other: "n files" })).toBe("n files");
    expect(plural(2, { one: "1 file", other: "n files" })).toBe("n files");
  });
});

describe("setLocale", () => {
  afterEach(() => {
    window.localStorage.clear();
  });
  it("stores the choice and reloads", () => {
    const reload = vi.fn();
    setLocale("nl", reload);
    expect(window.localStorage.getItem(LOCALE_STORAGE_KEY)).toBe("nl");
    expect(hasStoredLocale()).toBe(true);
    expect(reload).toHaveBeenCalledOnce();
  });
  it("null follows the browser again", () => {
    const reload = vi.fn();
    window.localStorage.setItem(LOCALE_STORAGE_KEY, "nl");
    setLocale(null, reload);
    expect(window.localStorage.getItem(LOCALE_STORAGE_KEY)).toBeNull();
    expect(hasStoredLocale()).toBe(false);
    expect(reload).toHaveBeenCalledOnce();
  });
});

describe("page language", () => {
  afterEach(() => {
    window.localStorage.clear();
    vi.resetModules();
  });
  it("is chosen at load from the stored choice", async () => {
    window.localStorage.setItem(LOCALE_STORAGE_KEY, "nl");
    vi.resetModules();
    const mod = await import("./i18n");
    expect(mod.locale).toBe("nl");
    expect(document.documentElement.lang).toBe("nl");
    const table = mod.defineMessages({ en: { hi: "Hello" }, nl: { hi: "Hallo" } });
    expect(table.hi).toBe("Hallo");
    const shell = await import("./messages");
    expect(shell.messages.common.cancel).toBe("Annuleren");
  });
  it("defaults to English in the test browser", async () => {
    vi.resetModules();
    const mod = await import("./i18n");
    expect(mod.locale).toBe("en");
    expect(document.documentElement.lang).toBe("en");
  });
});

describe("Messages (compile-time parity)", () => {
  it("refuses a translation that does not match", () => {
    const en = {
      a: "A",
      f: (n: number) => (n === 1 ? "one" : "many"),
      nested: { b: "B" },
      list: ["x", "y"] as const,
    };
    const ok: Messages<typeof en> = {
      a: "Een",
      f: (n: number) => (n === 1 ? "één" : "veel"),
      nested: { b: "Bee" },
      list: ["ix", "ei"],
    };
    // @ts-expect-error a missing key
    const missing: Messages<typeof en> = { a: "", f: () => "", list: ["", ""] };
    const extra: Messages<typeof en> = {
      ...ok,
      nested: {
        b: "",
        // @ts-expect-error an extra key
        c: "",
      },
    };
    const params: Messages<typeof en> = {
      ...ok,
      // @ts-expect-error a parameter of another type
      f: (n: string) => n,
    };
    const table = defineMessages({ en, nl: ok });
    expect(table.a).toBe("A");
    expect([missing, extra, params]).toHaveLength(3);
  });
});
