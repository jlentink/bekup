import { renderHook, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { accountLocaleAction, useAccountLocale } from "@/features/auth/use-account-locale";
import { LOCALE_SOURCE_KEY, LOCALE_STORAGE_KEY, pageReload } from "@/lib/i18n";
import { baseHandlers, gqlError, meFixture, ok, renderApp } from "@/test/mock-api";

const realRun = pageReload.run;
let reload: ReturnType<typeof vi.fn>;

beforeEach(() => {
  reload = vi.fn();
  pageReload.run = reload as unknown as () => void;
  pageReload.pending = false;
});
afterEach(() => {
  pageReload.run = realRun;
  pageReload.pending = false;
});

const saved = (locale: "EN" | "NL" | null) => () =>
  ok({ setMyLocale: { __typename: "User", id: meFixture().me.id, locale } });

describe("account language (D-236)", () => {
  it("saves the choice on the account, stores it here and reloads once", async () => {
    const { user, callsOf } = renderApp("/profile", {
      ...baseHandlers(),
      SetMyLocale: saved("NL"),
    });
    const select = await screen.findByRole("combobox", { name: "Language" });
    expect(select).toHaveValue("browser");
    await user.selectOptions(select, "nl");
    await waitFor(() => {
      expect(reload).toHaveBeenCalled();
    });
    expect(callsOf("SetMyLocale")[0]?.variables).toEqual({ locale: "NL" });
    expect(window.localStorage.getItem(LOCALE_STORAGE_KEY)).toBe("nl");
    expect(window.localStorage.getItem(LOCALE_SOURCE_KEY)).toBe("account");
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("saves the language the page already shows without a reload", async () => {
    const { user, callsOf } = renderApp("/profile", {
      ...baseHandlers(),
      SetMyLocale: saved("EN"),
    });
    await user.selectOptions(await screen.findByRole("combobox", { name: "Language" }), "en");
    await waitFor(() => {
      expect(window.localStorage.getItem(LOCALE_SOURCE_KEY)).toBe("account");
    });
    expect(callsOf("SetMyLocale")[0]?.variables).toEqual({ locale: "EN" });
    expect(reload).not.toHaveBeenCalled();
  });

  it("follows the browser again: clears the stored choice", async () => {
    window.localStorage.setItem(LOCALE_STORAGE_KEY, "en");
    window.localStorage.setItem(LOCALE_SOURCE_KEY, "account");
    const { user, callsOf } = renderApp("/profile", {
      ...baseHandlers(meFixture({ locale: "EN" })),
      SetMyLocale: saved(null),
    });
    const select = await screen.findByRole("combobox", { name: "Language" });
    expect(select).toHaveValue("en");
    await user.selectOptions(select, "browser");
    await waitFor(() => {
      expect(window.localStorage.getItem(LOCALE_STORAGE_KEY)).toBeNull();
    });
    expect(callsOf("SetMyLocale")[0]?.variables).toEqual({ locale: null });
    // The test browser's language is English, like the page: no reload.
    expect(reload).not.toHaveBeenCalled();
  });

  it("shows a choice made in this browser alone and can save it for the account", async () => {
    window.localStorage.setItem(LOCALE_STORAGE_KEY, "en");
    const { user, callsOf } = renderApp("/profile", {
      ...baseHandlers(),
      SetMyLocale: saved("EN"),
    });
    expect(await screen.findByRole("combobox", { name: "Language" })).toHaveValue("en");
    expect(
      screen.getByText("Chosen in this browser only; your other browsers do not follow it."),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save for my account" }));
    await waitFor(() => {
      expect(window.localStorage.getItem(LOCALE_SOURCE_KEY)).toBe("account");
    });
    expect(callsOf("SetMyLocale")[0]?.variables).toEqual({ locale: "EN" });
    expect(reload).not.toHaveBeenCalled();
  });

  it("keeps the page when saving fails", async () => {
    const { user } = renderApp("/profile", {
      ...baseHandlers(),
      SetMyLocale: () => gqlError("VALIDATION", "unsupported language"),
    });
    const select = await screen.findByRole("combobox", { name: "Language" });
    await user.selectOptions(select, "nl");
    expect(await screen.findByText("Unsupported language.")).toBeInTheDocument();
    expect(reload).not.toHaveBeenCalled();
    expect(window.localStorage.getItem(LOCALE_STORAGE_KEY)).toBeNull();
    expect(select).not.toBeDisabled();
  });
});

describe("accountLocaleAction", () => {
  const base = {
    page: "en" as const,
    stored: null,
    fromAccount: false,
    browser: "en" as const,
    canStore: true,
  };
  it.each([
    [{ ...base, target: "nl" as const }, "switch"],
    [{ ...base, target: "nl" as const, stored: "nl" as const, fromAccount: true }, "switch"],
    [{ ...base, target: "en" as const }, "adopt"],
    [{ ...base, target: "en" as const, stored: "en" as const }, "adopt"],
    [{ ...base, target: "en" as const, stored: "en" as const, fromAccount: true }, "none"],
    [{ ...base, target: null }, "none"],
    [{ ...base, target: null, stored: "nl" as const }, "none"],
    [{ ...base, target: null, stored: "en" as const, fromAccount: true }, "forget"],
    [
      { ...base, target: null, page: "nl" as const, stored: "nl" as const, fromAccount: true },
      "forget-reload",
    ],
    [{ ...base, target: "nl" as const, canStore: false }, "none"],
    [
      { ...base, target: undefined, page: "nl" as const, stored: "nl" as const, fromAccount: true },
      "none",
    ],
  ])("%o -> %s", (input, want) => {
    expect(accountLocaleAction(input)).toBe(want);
  });
});

describe("useAccountLocale", () => {
  it("switches to the account's language and reloads once", () => {
    const { result } = renderHook(() => useAccountLocale("NL"));
    expect(result.current).toBe(true);
    expect(window.localStorage.getItem(LOCALE_STORAGE_KEY)).toBe("nl");
    expect(window.localStorage.getItem(LOCALE_SOURCE_KEY)).toBe("account");
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("reloads when another tab already stored the language", () => {
    window.localStorage.setItem(LOCALE_STORAGE_KEY, "nl");
    window.localStorage.setItem(LOCALE_SOURCE_KEY, "account");
    expect(renderHook(() => useAccountLocale("NL")).result.current).toBe(true);
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("waits while a reload is already pending", () => {
    pageReload.pending = true;
    expect(renderHook(() => useAccountLocale("NL")).result.current).toBe(true);
    expect(reload).not.toHaveBeenCalled();
  });

  it("drops a copied account choice when the account has none", () => {
    window.localStorage.setItem(LOCALE_STORAGE_KEY, "en");
    window.localStorage.setItem(LOCALE_SOURCE_KEY, "account");
    expect(renderHook(() => useAccountLocale(null)).result.current).toBe(false);
    expect(window.localStorage.getItem(LOCALE_STORAGE_KEY)).toBeNull();
    expect(reload).not.toHaveBeenCalled();
  });

  it("does nothing before the account's answer arrived", () => {
    window.localStorage.setItem(LOCALE_STORAGE_KEY, "nl");
    window.localStorage.setItem(LOCALE_SOURCE_KEY, "account");
    expect(renderHook(() => useAccountLocale(undefined)).result.current).toBe(false);
    expect(window.localStorage.getItem(LOCALE_STORAGE_KEY)).toBe("nl");
    expect(reload).not.toHaveBeenCalled();
  });

  it("keeps a choice made in this browser alone", () => {
    window.localStorage.setItem(LOCALE_STORAGE_KEY, "en");
    expect(renderHook(() => useAccountLocale(null)).result.current).toBe(false);
    expect(window.localStorage.getItem(LOCALE_STORAGE_KEY)).toBe("en");
    expect(renderHook(() => useAccountLocale("EN")).result.current).toBe(false);
    expect(window.localStorage.getItem(LOCALE_SOURCE_KEY)).toBe("account");
    expect(reload).not.toHaveBeenCalled();
  });
});
